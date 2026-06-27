package store

import "database/sql"

// FarmConfig keys
const (
	CfgIMAPUser    = "IMAP_USER"
	CfgIMAPPass    = "IMAP_PASS"
	CfgEmailDomain = "EMAIL_DOMAIN"
	CfgIMAPHost    = "IMAP_HOST"
	CfgIMAPPort    = "IMAP_PORT"
	CfgFarmProxy   = "FARM_PROXY"
	CfgMaxAttempts = "MAX_ATTEMPTS"
	CfgResultsFile = "RESULTS_FILE"
)

// DefaultFarmConfig returns default values for farm config.
func DefaultFarmConfig() map[string]string {
	return map[string]string{
		CfgIMAPUser:    "",
		CfgIMAPPass:    "",
		CfgEmailDomain: "",
		CfgIMAPHost:    "imap.gmail.com",
		CfgIMAPPort:    "993",
		CfgFarmProxy:   "",
		CfgMaxAttempts: "10",
		CfgResultsFile: "results.json",
	}
}

// GetFarmConfig returns all farm config values (with defaults for missing).
func (s *Store) GetFarmConfig() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := DefaultFarmConfig()
	rows, err := s.db.Query("SELECT key, value FROM farm_config")
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		result[k] = v
	}
	return result, nil
}

// SetFarmConfig sets a single config key.
func (s *Store) SetFarmConfig(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO farm_config(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// SetFarmConfigBatch sets multiple config keys at once.
func (s *Store) SetFarmConfigBatch(cfg map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for k, v := range cfg {
		_, err := tx.Exec(`INSERT INTO farm_config(key, value) VALUES(?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

var _ = sql.ErrNoRows
