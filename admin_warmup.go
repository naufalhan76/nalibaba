package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"alibaba-router/router"
	"alibaba-router/store"
)

// WarmupResult tracks the result of testing a single account
type WarmupResult struct {
	AccountID int64  `json:"account_id"`
	Email     string `json:"email"`
	Status    string `json:"status"` // "alive", "dead", "error"
	Error     string `json:"error,omitempty"`
	Duration  string `json:"duration,omitempty"`
}

// WarmupState tracks live warmup progress
type WarmupState struct {
	mu        sync.RWMutex
	Running   bool           `json:"running"`
	Total     int            `json:"total"`
	Tested    int            `json:"tested"`
	Alive     int            `json:"alive"`
	Dead      int            `json:"dead"`
	Errors    int            `json:"errors"`
	Model     string         `json:"model"`
	StartedAt string         `json:"started_at"`
	Results   []WarmupResult `json:"results"`
}

var warmupState = &WarmupState{}

func (ws *WarmupState) reset(model string, total int) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.Running = true
	ws.Total = total
	ws.Tested = 0
	ws.Alive = 0
	ws.Dead = 0
	ws.Errors = 0
	ws.Model = model
	ws.StartedAt = time.Now().Format(time.RFC3339)
	ws.Results = make([]WarmupResult, 0, total)
}

func (ws *WarmupState) addResult(r WarmupResult) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.Tested++
	ws.Results = append(ws.Results, r)
	switch r.Status {
	case "alive":
		ws.Alive++
	case "dead":
		ws.Dead++
	case "error":
		ws.Errors++
	}
}

func (ws *WarmupState) finish() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.Running = false
}

func (ws *WarmupState) snapshot() WarmupState {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	// copy results slice for safe read
	r := make([]WarmupResult, len(ws.Results))
	copy(r, ws.Results)
	return WarmupState{
		Running:   ws.Running,
		Total:     ws.Total,
		Tested:    ws.Tested,
		Alive:     ws.Alive,
		Dead:      ws.Dead,
		Errors:    ws.Errors,
		Model:     ws.Model,
		StartedAt: ws.StartedAt,
		Results:   r,
	}
}

// warmupRunning tracks if a warmup is in progress (atomic)
var warmupRunning atomic.Bool

// WarmupAccounts starts a warmup job in the background
func (a *AdminHandler) WarmupAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}

	// Prevent concurrent warmups
	if !warmupRunning.CompareAndSwap(false, true) {
		writeJSON(w, 409, map[string]string{"error": "warmup already running"})
		return
	}

	var body struct {
		Model string `json:"model"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	if body.Model == "" {
		body.Model = "nalibaba-qwen3.5-flash"
	}

	modelDef, ok := a.store.GetModel(body.Model)
	if !ok {
		warmupRunning.Store(false)
		writeJSON(w, 400, map[string]string{"error": fmt.Sprintf("model %s not found", body.Model)})
		return
	}

	accounts, err := a.store.ListAccounts()
	if err != nil {
		warmupRunning.Store(false)
		writeErr(w, 500, err)
		return
	}

	var activeAccounts []store.Account
	for _, acc := range accounts {
		if !acc.IsDead {
			activeAccounts = append(activeAccounts, acc)
		}
	}

	if len(activeAccounts) == 0 {
		warmupRunning.Store(false)
		writeJSON(w, 200, map[string]any{
			"status":  "no_active_accounts",
			"message": "No active accounts to test",
		})
		return
	}

	warmupState.reset(body.Model, len(activeAccounts))

	// Run in background
	go func() {
		defer warmupRunning.Store(false)
		defer warmupState.finish()

		log.Printf("[Warmup] Testing %d accounts with model %s (upstream: %s)",
			len(activeAccounts), body.Model, modelDef.Upstream)

		upstream := router.NewUpstreamClient("")
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, 10)

		testPayload := map[string]any{
			"model": modelDef.Upstream,
			"messages": []map[string]string{
				{"role": "user", "content": "reply with only ok"},
			},
			"max_tokens":  5,
			"temperature": 0.1,
		}
		payloadBytes, _ := json.Marshal(testPayload)

		for _, acc := range activeAccounts {
			wg.Add(1)
			semaphore <- struct{}{}

			go func(account store.Account) {
				defer wg.Done()
				defer func() { <-semaphore }()

				result := WarmupResult{
					AccountID: account.ID,
					Email:     account.Email,
				}

				start := time.Now()
				statusCode, respBody, upstreamErr := upstream.ForwardRequest(
					account.APIKey,
					modelDef.Upstream,
					"",
					payloadBytes,
				)
				duration := time.Since(start)
				result.Duration = fmt.Sprintf("%.2fs", duration.Seconds())

				if upstreamErr != nil {
					if upstreamErr.HTTPStatus == 401 || upstreamErr.HTTPStatus == 403 {
						result.Status = "dead"
						result.Error = fmt.Sprintf("HTTP %d: %s", upstreamErr.HTTPStatus, upstreamErr.Error())
						log.Printf("[Warmup] Account %d (%s) DEAD: %s",
							account.ID, account.Email, result.Error)
						a.store.MarkDead(account.ID, result.Error)
					} else {
						result.Status = "error"
						result.Error = upstreamErr.Error()
						log.Printf("[Warmup] Account %d (%s) ERROR: %s (%s)",
							account.ID, account.Email, result.Error, result.Duration)
					}
				} else if statusCode >= 200 && statusCode < 300 {
					result.Status = "alive"
					log.Printf("[Warmup] Account %d (%s) ALIVE (%s)",
						account.ID, account.Email, result.Duration)
				} else {
					result.Status = "error"
					result.Error = fmt.Sprintf("HTTP %d: %s", statusCode, string(respBody))
				}

				warmupState.addResult(result)
			}(acc)
		}

		wg.Wait()
		snap := warmupState.snapshot()
		log.Printf("[Warmup] Complete: %d tested, %d alive, %d dead, %d errors",
			snap.Tested, snap.Alive, snap.Dead, snap.Errors)
	}()

	// Return immediately
	writeJSON(w, 202, map[string]any{
		"status": "started",
		"model":  body.Model,
		"total":  len(activeAccounts),
	})
}

// WarmupStatus returns current warmup progress
func (a *AdminHandler) WarmupStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	snap := warmupState.snapshot()
	writeJSON(w, 200, snap)
}
