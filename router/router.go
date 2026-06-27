package router

import (
	"net/http"
	"sync"

	"alibaba-router/store"
)

const MaxRetries = 5

// Router handles request routing across accounts.
type Router struct {
	store     *store.Store
	upstream  *UpstreamClient
	proxies   *ProxyManager
	mu        sync.Mutex
	pointers  map[string]int // model -> last used index in eligible list
}

func New(s *store.Store, baseURL string) *Router {
	return &Router{
		store:    s,
		upstream: NewUpstreamClient(baseURL),
		proxies:  NewProxyManager(s),
		pointers: make(map[string]int),
	}
}

// PickAccount selects next account by round-robin among eligible (non-exhausted) accounts.
func (r *Router) PickAccount(model string) (int64, error) {
	eligible, err := r.store.GetEligibleAccounts(model)
	if err != nil {
		return 0, err
	}
	if len(eligible) == 0 {
		return 0, ErrNoEligibleAccount
	}
	r.mu.Lock()
	idx := r.pointers[model] % len(eligible)
	r.pointers[model] = (idx + 1) % len(eligible)
	r.mu.Unlock()
	return eligible[idx], nil
}

// AdvancePointer forces round-robin pointer to move (used after exhaust/retry).
func (r *Router) advancePointer(model string) {
	r.mu.Lock()
	if p, ok := r.pointers[model]; ok {
		r.pointers[model] = (p + 1) % 1000 // broad advance
	}
	r.mu.Unlock()
}

// RouteResult holds the outcome of a routing attempt.
type RouteResult struct {
	AccountID  int64
	StatusCode int
	Body       []byte
	StreamResp *http.Response
	Err        error
}

// RouteChat routes a chat completion request with retry logic.
// isStream determines whether to use streaming forward.
// onUsage is called with token count from response (for non-stream).
// The caller is responsible for closing stream resp.Body if StreamResp != nil.
func (r *Router) RouteChat(routerKey, model string, body []byte, isStream bool) (*RouteResult, error) {
	// resolve model alias
	mdef, ok := r.store.GetModel(model)
	if !ok {
		return nil, ErrUnknownModel
	}
	upstreamModel := mdef.Upstream

	var lastErr error
	for attempt := 0; attempt < MaxRetries; attempt++ {
		accID, err := r.PickAccount(upstreamModel)
		if err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		acc, err := r.store.GetAccount(accID)
		if err != nil {
			continue
		}

		// pick proxy (round-robin, empty = direct)
		proxyURL, _ := r.proxies.PickProxy()

		if isStream {
			resp, _, ue := r.upstream.ForwardStream(acc.APIKey, upstreamModel, proxyURL, body)
			if resp != nil {
				// success — return stream to caller (caller closes body)
				return &RouteResult{
					AccountID:  accID,
					StatusCode: resp.StatusCode,
					StreamResp: resp,
				}, nil
			}
			// error path
			if ue != nil {
				lastErr = ue
				// if proxy error, mark proxy unhealthy & retry (don't penalize account)
				if ue.ErrBody.Code == "proxy_error" && proxyURL != "" {
					r.proxies.MarkProxyUnhealthy(proxyURL, ue.ErrBody.Message)
					continue
				}
				r.handleUpstreamError(accID, upstreamModel, ue, ue.HTTPStatus)
				r.advancePointer(upstreamModel)
				continue
			}
			r.store.RecordError(accID, upstreamModel, "stream connection failed")
			continue
		}

		// non-stream
		status, respBody, ue := r.upstream.ForwardRequest(acc.APIKey, upstreamModel, proxyURL, body)
		if ue == nil {
			// success — record usage
			tokens := extractTokens(respBody)
			if tokens > 0 {
				r.store.RecordUsage(accID, upstreamModel, tokens)
			}
			return &RouteResult{
				AccountID:  accID,
				StatusCode: status,
				Body:       respBody,
			}, nil
		}
		lastErr = ue
		// if proxy error, mark proxy unhealthy & retry
		if ue.ErrBody.Code == "proxy_error" && proxyURL != "" {
			r.proxies.MarkProxyUnhealthy(proxyURL, ue.ErrBody.Message)
			continue
		}
		r.handleUpstreamError(accID, upstreamModel, ue, status)
		r.advancePointer(upstreamModel)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrNoEligibleAccount
}

// handleUpstreamError marks exhausted, dead, or records transient error.
func (r *Router) handleUpstreamError(accID int64, model string, ue *UpstreamError, httpStatus int) {
	if ue.IsAllocationQuota() {
		r.store.MarkExhausted(accID, model, ue.ErrBody.Message)
		return
	}
	if ue.IsRateLimit() {
		// transient — don't mark exhausted, just record error
		r.store.RecordError(accID, model, "rate-limit: "+ue.ErrBody.Code)
		return
	}
	// 401/403 — key invalid, mark account dead (skip ALL models permanently)
	if httpStatus == 401 || httpStatus == 403 {
		r.store.MarkDead(accID, ue.ErrBody.Code+": "+ue.ErrBody.Message)
		return
	}
	// other errors — record per-slot error, don't flag account globally
	r.store.RecordError(accID, model, ue.ErrBody.Code+": "+ue.ErrBody.Message)
}
