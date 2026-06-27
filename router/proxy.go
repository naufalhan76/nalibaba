package router

import (
	"sync"

	"alibaba-router/store"
)

// ProxyManager handles round-robin proxy selection with retry.
type ProxyManager struct {
	store   *store.Store
	mu      sync.Mutex
	pointer int
}

func NewProxyManager(s *store.Store) *ProxyManager {
	return &ProxyManager{store: s}
}

// PickProxy returns next healthy proxy URL by round-robin.
// Returns empty string if no proxies configured (direct connection).
func (pm *ProxyManager) PickProxy() (string, error) {
	proxies, err := pm.store.GetHealthyProxies()
	if err != nil {
		return "", err
	}
	if len(proxies) == 0 {
		return "", nil // no proxies — direct
	}
	pm.mu.Lock()
	idx := pm.pointer % len(proxies)
	pm.pointer = (idx + 1) % len(proxies)
	pm.mu.Unlock()
	return proxies[idx].URL, nil
}

// MarkProxyUnhealthy flags a proxy as unhealthy after a failed request.
func (pm *ProxyManager) MarkProxyUnhealthy(proxyURL, errMsg string) {
	// find proxy by URL and mark unhealthy
	proxies, err := pm.store.GetHealthyProxies()
	if err != nil {
		return
	}
	for _, p := range proxies {
		if p.URL == proxyURL {
			pm.store.UpdateProxyHealth(p.ID, false, 0, errMsg)
			return
		}
	}
}

// Count returns total and healthy proxy counts.
func (pm *ProxyManager) Count() (total, healthy int) {
	t, h, _ := pm.store.ProxyCount()
	return t, h
}
