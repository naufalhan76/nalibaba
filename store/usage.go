package store

import (
	"os"
	"strings"
)

// Usage operations — per (account, model) tracking

func (s *Store) GetEligibleAccounts(model string) ([]int64, error) {
	// return account IDs yang slot (account, model) belum exhausted AND account not dead.
	// akun baru (belum ada row usage) juga eligible, as long as not dead.
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`
		SELECT a.id FROM accounts a
		LEFT JOIN usage u ON u.account_id = a.id AND u.model = ?
		WHERE a.is_dead = 0
		  AND (u.exhausted = 0 OR u.exhausted IS NULL)
		ORDER BY a.id`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func (s *Store) RecordUsage(accountID int64, model string, tokens int64, proxyURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO usage(account_id, model, tokens_used, last_used_at, last_proxy)
		VALUES(?,?,?,datetime('now'),?)
		ON CONFLICT(account_id, model) DO UPDATE SET
			tokens_used = tokens_used + excluded.tokens_used,
			last_used_at = datetime('now'),
			last_proxy = excluded.last_proxy`,
		accountID, model, tokens, proxyURL)
	return err
}

func (s *Store) MarkExhausted(accountID int64, model, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO usage(account_id, model, exhausted, exhausted_at, last_429_at, last_error)
		VALUES(?,?,1,datetime('now'),datetime('now'),?)
		ON CONFLICT(account_id, model) DO UPDATE SET
			exhausted = 1,
			exhausted_at = datetime('now'),
			last_429_at = datetime('now'),
			last_error = excluded.last_error`,
		accountID, model, errMsg)
	return err
}

func (s *Store) RecordError(accountID int64, model, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO usage(account_id, model, last_error)
		VALUES(?,?,?)
		ON CONFLICT(account_id, model) DO UPDATE SET last_error = excluded.last_error`,
		accountID, model, errMsg)
	return err
}

func (s *Store) ListUsage() ([]Usage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`
		SELECT account_id, model, tokens_used, exhausted,
		       COALESCE(exhausted_at,''), COALESCE(last_429_at,''),
		       COALESCE(last_used_at,''), COALESCE(last_error,''),
		       COALESCE(last_proxy,'')
		FROM usage ORDER BY model, account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Usage
	for rows.Next() {
		var u Usage
		var ex int
		if err := rows.Scan(&u.AccountID, &u.Model, &u.TokensUsed, &ex,
			&u.ExhaustedAt, &u.Last429At, &u.LastUsedAt, &u.LastError, &u.LastProxy); err != nil {
			return nil, err
		}
		u.Exhausted = ex == 1
		out = append(out, u)
	}
	return out, nil
}

func (s *Store) ResetSlot(accountID int64, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE usage SET exhausted=0, exhausted_at=NULL WHERE account_id=? AND model=?`,
		accountID, model)
	return err
}

func (s *Store) ResetAccountSlots(accountID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE usage SET exhausted=0, exhausted_at=NULL WHERE account_id=?`, accountID)
	return err
}

// --- Router key ops ---

func (s *Store) AddRouterKey(key, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO router_keys(key, label, active) VALUES(?,?,1)`, key, label)
	return err
}

func (s *Store) DeactivateRouterKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE router_keys SET active=0 WHERE key=?`, key)
	return err
}

func (s *Store) DeleteRouterKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM router_keys WHERE key=?`, key)
	return err
}

func (s *Store) ValidRouterKey(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var active int
	err := s.db.QueryRow(`SELECT active FROM router_keys WHERE key=?`, key).Scan(&active)
	return err == nil && active == 1
}

func (s *Store) ListRouterKeys() ([]RouterKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT key, COALESCE(label,''), created_at, active FROM router_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouterKey
	for rows.Next() {
		var k RouterKey
		var act int
		if err := rows.Scan(&k.Key, &k.Label, &k.CreatedAt, &act); err != nil {
			return nil, err
		}
		k.Active = act == 1
		out = append(out, k)
	}
	return out, nil
}

// --- Farm run ops ---

func (s *Store) CreateFarmRun(maxAttempts, pid int, logPath string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`INSERT INTO farm_runs(started_at, status, max_attempts, pid, log_path)
		VALUES(datetime('now'),'running',?,?,?)`, maxAttempts, pid, logPath)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateFarmRun(id int64, status string, newAccounts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE farm_runs SET status=?, new_accounts=?, ended_at=datetime('now') WHERE id=?`,
		status, newAccounts, id)
	return err
}

func (s *Store) ListFarmRuns(limit int) ([]FarmRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, COALESCE(started_at,''), COALESCE(ended_at,''),
		COALESCE(status,''), COALESCE(max_attempts,0), COALESCE(pid,0),
		COALESCE(new_accounts,0), COALESCE(log_path,'')
		FROM farm_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FarmRun
	for rows.Next() {
		var f FarmRun
		if err := rows.Scan(&f.ID, &f.StartedAt, &f.EndedAt, &f.Status,
			&f.MaxAttempts, &f.PID, &f.NewAccounts, &f.LogPath); err != nil {
			return nil, err
		}
		// Parse log file to extract success/fail counts
		if f.LogPath != "" {
			if data, err := os.ReadFile(f.LogPath); err == nil {
				logText := string(data)
				// Count "→ ✅ SUCCESS!" lines
				f.SuccessCount = strings.Count(logText, "→ ✅ SUCCESS!")
				// Count "→ ❌ Failed" lines
				f.FailCount = strings.Count(logText, "→ ❌ Failed")
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// --- Model helpers ---

func (s *Store) GetModel(id string) (ModelDef, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.models[id]
	return m, ok
}

func (s *Store) ListModels() []ModelDef {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ModelDef, 0, len(s.models))
	for _, m := range s.models {
		out = append(out, m)
	}
	return out
}


// InsertRequestLog adds a new request log entry and returns the log ID
func (s *Store) InsertRequestLog(accountID int64, accountEmail, model, requestBody, responseBody, proxyURL, errorMessage string, durationMs int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// Truncate to prevent DB bloat
	if len(requestBody) > 2000 {
		requestBody = requestBody[:2000] + "...(truncated)"
	}
	if len(responseBody) > 2000 {
		responseBody = responseBody[:2000] + "...(truncated)"
	}
	
	res, err := s.db.Exec(`
		INSERT INTO request_logs (account_id, account_email, model, request_body, response_body, proxy_url, error_message, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, accountID, accountEmail, model, requestBody, responseBody, proxyURL, errorMessage, durationMs)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateRequestLogResponse updates the response body for an existing request log (used for streaming)
func (s *Store) UpdateRequestLogResponse(id int64, responseBody string, durationMs int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if len(responseBody) > 2000 {
		responseBody = responseBody[:2000] + "...(truncated)"
	}
	
	_, err := s.db.Exec(`UPDATE request_logs SET response_body = ?, duration_ms = ? WHERE id = ?`,
		responseBody, durationMs, id)
	return err
}

// ListRequestLogs returns recent request logs (newest first)
func (s *Store) ListRequestLogs(limit int) ([]RequestLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	rows, err := s.db.Query(`
		SELECT id, account_id, account_email, model, request_body, response_body, proxy_url, duration_ms, error_message, created_at
		FROM request_logs
		ORDER BY created_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var logs []RequestLog
	for rows.Next() {
		var log RequestLog
		if err := rows.Scan(&log.ID, &log.AccountID, &log.AccountEmail, &log.Model, &log.RequestBody, 
			&log.ResponseBody, &log.ProxyURL, &log.DurationMs, &log.ErrorMessage, &log.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}
	return logs, nil
}


// GetUsageOverTime returns usage aggregated by model over time periods
func (s *Store) GetUsageOverTime(hours int) ([]UsageTimePoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	rows, err := s.db.Query(`
		SELECT 
			strftime('%Y-%m-%d %H:00', created_at) as time_period,
			model,
			SUM(duration_ms) as total_duration,
			COUNT(*) as request_count
		FROM request_logs
		WHERE created_at >= datetime('now', '-' || ? || ' hours')
		GROUP BY time_period, model
		ORDER BY time_period, model`, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var points []UsageTimePoint
	for rows.Next() {
		var p UsageTimePoint
		if err := rows.Scan(&p.TimePeriod, &p.Model, &p.TotalDurationMs, &p.RequestCount); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, nil
}

// GetTopModels returns top N models by total requests
func (s *Store) GetTopModels(limit int) ([]ModelUsage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	rows, err := s.db.Query(`
		SELECT 
			model,
			COUNT(*) as total_requests,
			AVG(duration_ms) as avg_duration_ms
		FROM request_logs
		WHERE created_at >= datetime('now', '-24 hours')
		GROUP BY model
		ORDER BY total_requests DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var models []ModelUsage
	for rows.Next() {
		var m ModelUsage
		if err := rows.Scan(&m.Model, &m.TotalRequests, &m.AvgDurationMs); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, nil
}
