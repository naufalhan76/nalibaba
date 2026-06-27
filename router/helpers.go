package router

import (
	"encoding/json"
	"net/url"
	"strings"
)

// extractTokens pulls total_tokens from a non-streaming response body.
func extractTokens(body []byte) int64 {
	var resp struct {
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0
	}
	return resp.Usage.TotalTokens
}

// extractStreamUsage pulls total_tokens from a final SSE chunk (data: {...}).
func extractStreamUsage(line []byte) int64 {
	var chunk struct {
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(line, &chunk); err != nil {
		return 0
	}
	return chunk.Usage.TotalTokens
}

// normalizeProxyURL ensures the proxy URL has a scheme.
// Accepts: https://user:pass@ip:port, http://user:pass@ip:port, ip:port:user:pass
func normalizeProxyURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		// assume http if no scheme
		s = "http://" + s
	}
	return s
}

// parseProxy parses a proxy URL string into *url.URL.
func parseProxy(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil || u == nil {
		return nil
	}
	return u
}

