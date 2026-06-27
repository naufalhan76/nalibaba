package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"alibaba-router/router"
	"alibaba-router/store"
)

type AdminHandler struct {
	store   *store.Store
	farmDir string
	router  *router.Router
}

// Keys: GET list, POST generate, DELETE revoke.
func (a *AdminHandler) Keys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		keys, err := a.store.ListRouterKeys()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, keys)
	case "POST":
		var body struct {
			Label string `json:"label"`
		}
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&body)
		}
		key := generateKey()
		if err := a.store.AddRouterKey(key, body.Label); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 201, map[string]string{"key": key, "label": body.Label})
	case "DELETE":
		key := r.URL.Query().Get("key")
		if key == "" {
			writeJSON(w, 400, map[string]string{"error": "key param required"})
			return
		}
		if err := a.store.DeleteRouterKey(key); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "deleted"})
	default:
		w.WriteHeader(405)
	}
}

// Accounts: GET list (masked keys).
func (a *AdminHandler) Accounts(w http.ResponseWriter, r *http.Request) {
	accs, err := a.store.ListAccounts()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// mask api keys
	for i := range accs {
		k := accs[i].APIKey
		if len(k) > 12 {
			accs[i].APIKey = k[:8] + "..." + k[len(k)-4:]
		}
	}
	writeJSON(w, 200, accs)
}

// Usage: GET all usage rows.
func (a *AdminHandler) Usage(w http.ResponseWriter, r *http.Request) {
	usage, err := a.store.ListUsage()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, usage)
}

// Import: POST re-sync from results.json.
func (a *AdminHandler) Import(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	src := filepath.Join(a.farmDir, "results.json")
	data, err := os.ReadFile(src)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "cannot read results.json: " + err.Error()})
		return
	}
	var entries []struct {
		Email  string `json:"email"`
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		writeJSON(w, 400, map[string]string{"error": "parse error: " + err.Error()})
		return
	}
	imported, skipped := 0, 0
	for _, e := range entries {
		if e.APIKey == "" {
			skipped++
			continue
		}
		_, err := a.store.ImportAccount(e.Email, e.APIKey, "results.json")
		if err != nil {
			skipped++
			continue
		}
		imported++
	}
	writeJSON(w, 200, map[string]int{"imported": imported, "skipped": skipped, "total_file": len(entries)})
}

// ResetSlot: POST {account_id, model} — un-flag one exhausted slot.
func (a *AdminHandler) ResetSlot(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var body struct {
		AccountID int64  `json:"account_id"`
		Model     string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := a.store.ResetSlot(body.AccountID, body.Model); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "reset"})
}

// ResetAccount: POST {account_id} — un-flag all slots for an account.
func (a *AdminHandler) ResetAccount(w http.ResponseWriter, r *http.Request) {
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
	if err := a.store.ResetAccountSlots(body.AccountID); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "reset"})
}

// Stats: GET summary stats.
func (a *AdminHandler) Stats(w http.ResponseWriter, r *http.Request) {
	accs, _ := a.store.ListAccounts()
	usage, _ := a.store.ListUsage()
	keys, _ := a.store.ListRouterKeys()
	exhausted := 0
	totalTokens := int64(0)
	for _, u := range usage {
		if u.Exhausted {
			exhausted++
		}
		totalTokens += u.TokensUsed
	}
	// count models with at least 1 exhausted slot
	modelsExhausted := map[string]bool{}
	for _, u := range usage {
		if u.Exhausted {
			modelsExhausted[u.Model] = true
		}
	}
	writeJSON(w, 200, map[string]any{
		"total_accounts":      len(accs),
		"total_router_keys":   len(keys),
		"total_slots":         len(usage),
		"exhausted_slots":     exhausted,
		"models_exhausted":    len(modelsExhausted),
		"total_tokens_used":   totalTokens,
		"models_available":    len(a.store.ListModels()),
	})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (a *AdminHandler) parseInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

var _ = fmt.Sprintf // keep fmt import if unused later

// GetSettings: GET - returns current settings (password excluded).
func (a *AdminHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	st, err := a.store.GetSettings()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"proxy_enabled":     st.ProxyEnabled,
		"routing_method":    st.RoutingMethod,
		"dot_trick_enabled": st.DotTrickEnabled,
	})
}

// SaveSettings: POST - updates settings (proxy_enabled, routing_method, dot_trick_enabled).
func (a *AdminHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var body struct {
		ProxyEnabled    *bool  `json:"proxy_enabled"`
		RoutingMethod   string `json:"routing_method"`
		DotTrickEnabled *bool  `json:"dot_trick_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
		return
	}

	current, err := a.store.GetSettings()
	if err != nil {
		writeErr(w, 500, err)
		return
	}

	// Only update fields that were provided
	if body.ProxyEnabled != nil {
		current.ProxyEnabled = *body.ProxyEnabled
	}
	if body.RoutingMethod != "" {
		if body.RoutingMethod != "round_robin" && body.RoutingMethod != "sticky" {
			writeJSON(w, 400, map[string]string{"error": "routing_method must be round_robin or sticky"})
			return
		}
		current.RoutingMethod = body.RoutingMethod
	}
	if body.DotTrickEnabled != nil {
		current.DotTrickEnabled = *body.DotTrickEnabled
	}

	if err := a.store.SaveSettings(current); err != nil {
		writeErr(w, 500, err)
		return
	}

	// Notify router of routing method change
	if a.router != nil {
		a.router.SetRoutingMethod(current.RoutingMethod)
	}

	writeJSON(w, 200, map[string]string{"status": "saved"})
}

// ChangePassword: POST {old_password, new_password}
func (a *AdminHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
		return
	}

	if body.NewPassword == "" || len(body.NewPassword) < 4 {
		writeJSON(w, 400, map[string]string{"error": "new password must be at least 4 characters"})
		return
	}

	valid, err := a.store.CheckPassword(body.OldPassword)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !valid {
		writeJSON(w, 401, map[string]string{"error": "old password incorrect"})
		return
	}

	if err := a.store.ChangePassword(body.NewPassword); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, 200, map[string]string{"status": "changed"})
}
