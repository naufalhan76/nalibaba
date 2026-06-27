package store

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

func (s *Store) RecordUsage(accountID int64, model string, tokens int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO usage(account_id, model, tokens_used, last_used_at)
		VALUES(?,?,?,datetime('now'))
		ON CONFLICT(account_id, model) DO UPDATE SET
			tokens_used = tokens_used + excluded.tokens_used,
			last_used_at = datetime('now')`,
		accountID, model, tokens)
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
		       COALESCE(last_used_at,''), COALESCE(last_error,'')
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
			&u.ExhaustedAt, &u.Last429At, &u.LastUsedAt, &u.LastError); err != nil {
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
