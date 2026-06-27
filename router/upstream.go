package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const DefaultBaseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"

// UpstreamClient wraps calls to dashscope-intl.
type UpstreamClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewUpstreamClient(baseURL string) *UpstreamClient {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &UpstreamClient{
		BaseURL: baseURL,
		HTTP:    &http.Client{},
	}
}

// clientForProxy returns an http.Client configured with the given proxy.
// If proxyURL is empty, returns the default client (direct).
func (c *UpstreamClient) clientForProxy(proxyURL string) *http.Client {
	if proxyURL == "" {
		return c.HTTP
	}
	proxyURL = normalizeProxyURL(proxyURL)
	proxyFunc := http.ProxyURL(parseProxy(proxyURL))
	return &http.Client{
		Transport: &http.Transport{Proxy: proxyFunc},
	}
}

// UpstreamError represents the error body from dashscope.
type UpstreamError struct {
	HTTPStatus int `json:"-"`
	ErrBody    struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (e *UpstreamError) Error() string {
	return e.ErrBody.Code + ": " + e.ErrBody.Message
}

// IsAllocationQuota checks if error is the 429 quota-exhausted signal.
func (e *UpstreamError) IsAllocationQuota() bool {
	return e.ErrBody.Code == "Throttling.AllocationQuota"
}

// IsRateLimit checks if error is transient rate-limiting (RPM/TPM), not exhaustion.
func (e *UpstreamError) IsRateLimit() bool {
	c := e.ErrBody.Code
	return c == "Throttling.RateQPS" || c == "Throttling.RateTPS" ||
		c == "Throttling.FreeEntryRPM" || c == "Throttling" ||
		c == "limit_requests" || c == "Throttling.RateConcurrent"
}

// ParseUpstreamError parses error body, returns nil if not an error shape.
func ParseUpstreamError(body []byte) *UpstreamError {
	var ue UpstreamError
	if err := json.Unmarshal(body, &ue); err != nil {
		return nil
	}
	if ue.ErrBody.Code == "" && ue.ErrBody.Message == "" {
		return nil
	}
	return &ue
}

// ForwardRequest forwards a non-streaming chat completion request.
// proxyURL is optional (empty = direct). Returns (statusCode, body, respErr).
func (c *UpstreamClient) ForwardRequest(apiKey, model, proxyURL string, body []byte) (int, []byte, *UpstreamError) {
	// replace model in body with upstream model id BEFORE creating request
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		payload["model"] = model
		if b, err := json.Marshal(payload); err == nil {
			body = b
		}
	}
	req, err := http.NewRequest("POST", c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := c.clientForProxy(proxyURL)
	resp, err := client.Do(req)
	if err != nil {
		ue := &UpstreamError{}
		ue.ErrBody.Code = "proxy_error"
		ue.ErrBody.Message = err.Error()
		return 0, nil, ue
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		ue := ParseUpstreamError(respBody)
		if ue == nil {
			ue = &UpstreamError{}
			ue.ErrBody.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		ue.HTTPStatus = resp.StatusCode
		return resp.StatusCode, respBody, ue
	}
	return resp.StatusCode, respBody, nil
}

// ForwardStream forwards a streaming request, returning the raw response for streaming.
// proxyURL is optional (empty = direct).
func (c *UpstreamClient) ForwardStream(apiKey, model, proxyURL string, body []byte) (*http.Response, []byte, *UpstreamError) {
	// replace model in body + inject stream_options.include_usage for token tracking
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		payload["model"] = model
		// ensure usage is included in final stream chunk
		so, _ := payload["stream_options"].(map[string]any)
		if so == nil {
			so = map[string]any{}
		}
		so["include_usage"] = true
		payload["stream_options"] = so
		if b, err := json.Marshal(payload); err == nil {
			body = b
		}
	}
	req, err := http.NewRequest("POST", c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, nil, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "text/event-stream")
	client := c.clientForProxy(proxyURL)
	resp, err := client.Do(req)
	if err != nil {
		ue := &UpstreamError{}
		ue.ErrBody.Code = "proxy_error"
		ue.ErrBody.Message = err.Error()
		return nil, nil, ue
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		ue := ParseUpstreamError(respBody)
		if ue == nil {
			ue = &UpstreamError{}
			ue.ErrBody.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		ue.HTTPStatus = resp.StatusCode
		return nil, respBody, ue
	}
	return resp, nil, nil
}
