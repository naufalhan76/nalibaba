package router

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"alibaba-router/store"
)

// Handler holds HTTP handlers for the router.
type Handler struct {
	router *Router
	store  *store.Store
}

func NewHandler(r *Router, s *store.Store) *Handler {
	return &Handler{router: r, store: s}
}

// extractBearer pulls the router key from Authorization header.
func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// writeJSON helper.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeUpstreamError formats an error like dashscope/0penAI.
func writeUpstreamError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"message":%q,"code":%q}}`, msg, code)
}

// Models returns the list of available models (0penAI-compatible).
func (h *Handler) Models(w http.ResponseWriter, r *http.Request) {
	if !h.store.ValidRouterKey(extractBearer(r)) {
		writeUpstreamError(w, 401, "invalid_key", "invalid router key")
		return
	}
	models := h.store.ListModels()
	out := struct {
		Object string         `json:"object"`
		Data   []store.ModelDef `json:"data"`
	}{Object: "list", Data: models}
	writeJSON(w, 200, out)
}

// ChatCompletions handles both streaming and non-streaming chat requests.
func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	key := extractBearer(r)
	if !h.store.ValidRouterKey(key) {
		writeUpstreamError(w, 401, "invalid_key", "invalid router key")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeUpstreamError(w, 400, "read_error", "cannot read body")
		return
	}
	// parse model + stream flag
	var payload struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeUpstreamError(w, 400, "parse_error", "invalid JSON body")
		return
	}
	if payload.Model == "" {
		writeUpstreamError(w, 400, "missing_model", "model field required")
		return
	}

	result, err := h.router.RouteChat(key, payload.Model, body, payload.Stream)
	if err != nil {
		// map errors to appropriate status
		switch err {
		case ErrUnknownModel:
			writeUpstreamError(w, 404, "model_not_found", "model not in allowlist: "+payload.Model)
		case ErrNoEligibleAccount:
			writeUpstreamError(w, 429, "model_exhausted", "all accounts exhausted for model: "+payload.Model)
		default:
			ue, ok := err.(*UpstreamError)
			if ok {
				status := 429
				if !ue.IsAllocationQuota() && !ue.IsRateLimit() {
					status = 502
				}
				writeUpstreamError(w, status, ue.ErrBody.Code, ue.ErrBody.Message)
			} else {
				writeUpstreamError(w, 502, "upstream_error", err.Error())
			}
		}
		return
	}

	if payload.Stream && result.StreamResp != nil {
		h.proxyStream(w, result, payload.Model)
		return
	}
	// non-stream: forward body
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(result.StatusCode)
	w.Write(result.Body)
}

// proxyStream pipes the upstream SSE stream to the client, capturing usage tokens.
func (h *Handler) proxyStream(w http.ResponseWriter, result *RouteResult, model string) {
	defer result.StreamResp.Body.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeUpstreamError(w, 500, "no_flusher", "streaming not supported")
		return
	}
	w.WriteHeader(200)
	scanner := bufio.NewScanner(result.StreamResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		// write line + newline
		w.Write(line)
		w.Write([]byte("\n"))
		// check for usage in data lines
		if bytes.HasPrefix(line, []byte("data: ")) {
			dataJSON := bytes.TrimPrefix(line, []byte("data: "))
			if bytes.Equal(bytes.TrimSpace(dataJSON), []byte("[DONE]")) {
				continue
			}
			tokens := extractStreamUsage(dataJSON)
			if tokens > 0 {
				// resolve upstream model id
				if mdef, ok := h.store.GetModel(model); ok {
					h.store.RecordUsage(result.AccountID, mdef.Upstream, tokens)
				}
			}
		}
		flusher.Flush()
	}
}
