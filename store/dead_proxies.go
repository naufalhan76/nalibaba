package store

import (
	"database/sql"
	"fmt"
)

// --- Dead account ops ---

// MarkDead flags an account as dead (401/403 — key invalid).
// Dead accounts are skipped in round-robin for ALL models, permanently until revived.
func (s *Store) MarkDead(accountID int64, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE accounts SET is_dead=1, dead_reason=?, dead_at=datetime('now') WHERE id=?`,
		reason, accountID)
	return err
}

// ReviveAccount unflags a dead account.
func (s *Store) ReviveAccount(accountID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE accounts SET is_dead=0, dead_reason=NULL, dead_at=NULL WHERE id=?`, accountID)
	return err
}

// ListDeadAccounts returns all dead accounts.
func (s *Store) ListDeadAccounts() ([]Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, email, api_key, added_at, source FROM accounts WHERE is_dead=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Email, &a.APIKey, &a.AddedAt, &a.Source); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// --- Proxy pool ops ---

type Proxy struct {
	ID          int64  `json:"id"`
	URL         string `json:"url"`
	Region      string `json:"region"`
	Healthy     bool   `json:"healthy"`
	LastCheckAt string `json:"last_check_at"`
	LastError   string `json:"last_error"`
	LatencyMs   int    `json:"latency_ms"`
	AddedAt     string `json:"added_at"`
}

// AddProxy inserts a proxy. Returns error if duplicate URL.
func (s *Store) AddProxy(url, region string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`INSERT INTO proxies(url, region, healthy) VALUES(?,?,1)
		ON CONFLICT(url) DO UPDATE SET region=excluded.region`, url, region)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = s.db.QueryRow("SELECT id FROM proxies WHERE url=?", url).Scan(&id)
	}
	return id, nil
}

// ListProxies returns all proxies.
func (s *Store) ListProxies() ([]Proxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, url, COALESCE(region,''), healthy,
		COALESCE(last_check_at,''), COALESCE(last_error,''), COALESCE(latency_ms,0),
		COALESCE(added_at,'') FROM proxies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proxy
	for rows.Next() {
		var p Proxy
		var h int
		if err := rows.Scan(&p.ID, &p.URL, &p.Region, &h, &p.LastCheckAt,
			&p.LastError, &p.LatencyMs, &p.AddedAt); err != nil {
			return nil, err
		}
		p.Healthy = h == 1
		out = append(out, p)
	}
	return out, nil
}

// GetHealthyProxies returns all healthy proxies for round-robin.
func (s *Store) GetHealthyProxies() ([]Proxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, url, COALESCE(region,''), healthy,
		COALESCE(last_check_at,''), COALESCE(last_error,''), COALESCE(latency_ms,0),
		COALESCE(added_at,'') FROM proxies WHERE healthy=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proxy
	for rows.Next() {
		var p Proxy
		var h int
		if err := rows.Scan(&p.ID, &p.URL, &p.Region, &h, &p.LastCheckAt,
			&p.LastError, &p.LatencyMs, &p.AddedAt); err != nil {
			return nil, err
		}
		p.Healthy = h == 1
		out = append(out, p)
	}
	return out, nil
}

// UpdateProxyHealth updates health status after a check.
func (s *Store) UpdateProxyHealth(id int64, healthy bool, latencyMs int, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := 0
	if healthy {
		h = 1
	}
	_, err := s.db.Exec(`UPDATE proxies SET healthy=?, latency_ms=?, last_check_at=datetime('now'),
		last_error=? WHERE id=?`, h, latencyMs, errMsg, id)
	return err
}

// DeleteProxy removes a proxy.
func (s *Store) DeleteProxy(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM proxies WHERE id=?`, id)
	return err
}

// UpdateProxyRegion updates the detected region for a proxy.
func (s *Store) UpdateProxyRegion(id int64, region string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE proxies SET region=? WHERE id=?`, region, id)
	return err
}

// ProxyCount returns total and healthy proxy counts.
func (s *Store) ProxyCount() (total, healthy int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.db.QueryRow("SELECT COUNT(*) FROM proxies").Scan(&total)
	if err == sql.ErrNoRows {
		total = 0
	}
	err = s.db.QueryRow("SELECT COUNT(*) FROM proxies WHERE healthy=1").Scan(&healthy)
	if err == sql.ErrNoRows {
		healthy = 0
	}
	return total, healthy, nil
}

var _ = fmt.Sprintf // keep fmt if needed later
