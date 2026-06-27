package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"alibaba-router/store"
)

// DeadAccounts: GET — list dead accounts.
func (a *AdminHandler) DeadAccounts(w http.ResponseWriter, r *http.Request) {
	accs, err := a.store.ListDeadAccounts()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// mask keys
	for i := range accs {
		k := accs[i].APIKey
		if len(k) > 12 {
			accs[i].APIKey = k[:8] + "..." + k[len(k)-4:]
		}
	}
	writeJSON(w, 200, accs)
}

// ReviveAccount: POST {account_id} — unflag dead account.
func (a *AdminHandler) ReviveAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var body struct {
		AccountID int64 `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := a.store.ReviveAccount(body.AccountID); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "revived"})
}

// Models: GET — list available models (nalibaba-* with upstream mapping).
func (a *AdminHandler) Models(w http.ResponseWriter, r *http.Request) {
	models := a.store.ListModels()
	writeJSON(w, 200, models)
}

// Proxies: GET list, POST add, DELETE remove.
func (a *AdminHandler) Proxies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		proxies, err := a.store.ListProxies()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, proxies)
	case "POST":
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, 400, err)
			return
		}
		if body.URL == "" {
			writeJSON(w, 400, map[string]string{"error": "url required"})
			return
		}
		// detect region (async, set later by check). For now empty.
		id, err := a.store.AddProxy(body.URL, "")
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 201, map[string]any{"id": id, "status": "added"})
	case "DELETE":
		var body struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := a.store.DeleteProxy(body.ID); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "deleted"})
	default:
		w.WriteHeader(405)
	}
}

// ProxyCheck: POST {id} or {url} — health check + region detection.
func (a *AdminHandler) ProxyCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var body struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err)
		return
	}

	// resolve proxy URL
	proxyURL := body.URL
	var proxyID int64 = body.ID
	if proxyURL == "" && body.ID > 0 {
		// fetch from store
		proxies, _ := a.store.ListProxies()
		for _, p := range proxies {
			if p.ID == body.ID {
				proxyURL = p.URL
				break
			}
		}
	}
	if proxyURL == "" {
		writeJSON(w, 400, map[string]string{"error": "id or url required"})
		return
	}

	// health check: connect through proxy to a test endpoint
	normalized := normalizeProxyScheme(proxyURL)
	pu, err := url.Parse(normalized)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid proxy url"})
		return
	}

	t0 := time.Now()
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(pu),
		},
	}
	// use ip-api.com for region detection (returns JSON with country)
	resp, err := client.Get("http://ip-api.com/json/")
	latency := time.Since(t0).Milliseconds()
	if err != nil {
		a.store.UpdateProxyHealth(proxyID, false, int(latency), err.Error())
		writeJSON(w, 200, map[string]any{
			"healthy":   false,
			"latency_ms": latency,
			"error":      err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		a.store.UpdateProxyHealth(proxyID, false, int(latency), fmt.Sprintf("HTTP %d", resp.StatusCode))
		writeJSON(w, 200, map[string]any{
			"healthy":   false,
			"latency_ms": latency,
			"error":      fmt.Sprintf("HTTP %d", resp.StatusCode),
		})
		return
	}

	// parse region
	var ipInfo struct {
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
		Region      string `json:"regionName"`
		City        string `json:"city"`
		Query       string `json:"query"`
	}
	json.NewDecoder(resp.Body).Decode(&ipInfo)
	region := ipInfo.Country
	if ipInfo.City != "" {
		region = ipInfo.City + ", " + ipInfo.Country
	}

	a.store.UpdateProxyHealth(proxyID, true, int(latency), "")
	// update region in DB
	if proxyID > 0 {
		a.store.UpdateProxyRegion(proxyID, region)
	}

	writeJSON(w, 200, map[string]any{
		"healthy":    true,
		"latency_ms": latency,
		"region":     region,
		"ip":         ipInfo.Query,
	})
}

// normalizeProxyScheme ensures proxy URL has a scheme.
func normalizeProxyScheme(s string) string {
	if !contains(s, "://") {
		return "http://" + s
	}
	return s
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ = store.Account{} // keep import
