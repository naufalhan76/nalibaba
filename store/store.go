package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

type Account struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	APIKey      string `json:"api_key"`
	AddedAt     string `json:"added_at"`
	Source      string `json:"source"`
	IsDead      bool   `json:"is_dead"`
	DeadReason  string `json:"dead_reason,omitempty"`
	DeadAt      string `json:"dead_at,omitempty"`
}

type Usage struct {
	AccountID   int64  `json:"account_id"`
	Model       string `json:"model"`
	TokensUsed  int64  `json:"tokens_used"`
	Exhausted   bool   `json:"exhausted"`
	ExhaustedAt string `json:"exhausted_at"`
	Last429At   string `json:"last_429_at"`
	LastUsedAt  string `json:"last_used_at"`
	LastError   string `json:"last_error"`
	LastProxy   string `json:"last_proxy"`
}

type RouterKey struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	CreatedAt string `json:"created_at"`
	Active    bool   `json:"active"`
}

type FarmRun struct {
	ID           int64  `json:"id"`
	StartedAt    string `json:"started_at"`
	EndedAt      string `json:"ended_at"`
	Status       string `json:"status"`
	MaxAttempts  int    `json:"max_attempts"`
	PID          int    `json:"pid"`
	NewAccounts  int    `json:"new_accounts"`
	LogPath      string `json:"log_path"`
	SuccessCount int    `json:"success_count"`
	FailCount    int    `json:"fail_count"`
}

type ModelDef struct {
	ID       string `json:"id"`
	Upstream string `json:"upstream"`
	Context  int    `json:"context"`
}

type Store struct {
	db     *sql.DB
	mu     sync.Mutex
	models map[string]ModelDef // id -> def
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "router.db")
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite single-writer
	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		return nil, err
	}
	if err := s.loadModels(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) initSchema() error {
	schema, err := os.ReadFile("store/schema.sql")
	if err != nil {
		// fallback: embed path relative to binary
		schema, err = os.ReadFile("/home/ubuntu/alibaba-cloud-farm/alibaba-router/store/schema.sql")
		if err != nil {
			return fmt.Errorf("schema.sql not found: %w", err)
		}
	}
	if _, err := s.db.Exec(string(schema)); err != nil {
		return err
	}
	// migration: add is_dead column if missing (for existing DBs)
	return s.migrate()
}

// migrate handles schema changes for existing databases.
func (s *Store) migrate() error {
	// check if accounts.is_dead column exists
	cols, err := s.db.Query("PRAGMA table_info(accounts)")
	if err != nil {
		return err
	}
	hasDead := false
	for cols.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			cols.Close()
			return err
		}
		if name == "is_dead" {
			hasDead = true
		}
	}
	cols.Close()
	if !hasDead {
		_, err := s.db.Exec(`ALTER TABLE accounts ADD COLUMN is_dead INTEGER DEFAULT 0`)
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`ALTER TABLE accounts ADD COLUMN dead_reason TEXT`)
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`ALTER TABLE accounts ADD COLUMN dead_at TEXT`)
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_accounts_dead ON accounts(is_dead)`)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadModels() error {
	data, err := os.ReadFile("store/models.json")
	if err != nil {
		data, err = os.ReadFile("/home/ubuntu/alibaba-cloud-farm/alibaba-router/store/models.json")
		if err != nil {
			return err
		}
	}
	var mc struct {
		Models []ModelDef `json:"models"`
	}
	if err := json.Unmarshal(data, &mc); err != nil {
		return err
	}
	s.mu.Lock()
	s.models = make(map[string]ModelDef)
	for _, m := range mc.Models {
		s.models[m.ID] = m
	}
	s.mu.Unlock()
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// --- Account ops ---

func (s *Store) ImportAccount(email, apiKey, source string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(
		`INSERT INTO accounts(email, api_key, source) VALUES(?,?,?)
		 ON CONFLICT(email) DO UPDATE SET api_key=excluded.api_key`,
		email, apiKey, source)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = s.db.QueryRow("SELECT id FROM accounts WHERE email=?", email).Scan(&id)
	}
	return id, nil
}

func (s *Store) ListAccounts() ([]Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, email, api_key, added_at, source,
		COALESCE(is_dead,0), COALESCE(dead_reason,''), COALESCE(dead_at,'')
		FROM accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var dead int
		if err := rows.Scan(&a.ID, &a.Email, &a.APIKey, &a.AddedAt, &a.Source,
			&dead, &a.DeadReason, &a.DeadAt); err != nil {
			return nil, err
		}
		a.IsDead = dead == 1
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) GetAccount(id int64) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var a Account
	err := s.db.QueryRow(`SELECT id, email, api_key, added_at, source FROM accounts WHERE id=?`, id).
		Scan(&a.ID, &a.Email, &a.APIKey, &a.AddedAt, &a.Source)
	if err != nil {
		return nil, err
	}
	return &a, nil
}


// RequestLog represents an individual API request
type RequestLog struct {
	ID           int64  `json:"id"`
	AccountID    int64  `json:"account_id"`
	AccountEmail string `json:"account_email"`
	Model        string `json:"model"`
	RequestBody  string `json:"request_body"`
	ResponseBody string `json:"response_body"`
	ProxyURL     string `json:"proxy_url"`
	DurationMs   int    `json:"duration_ms"`
	ErrorMessage string `json:"error_message"`
	CreatedAt    string `json:"created_at"`
}


// UsageTimePoint represents usage at a specific time period
type UsageTimePoint struct {
	TimePeriod     string `json:"time_period"`
	Model          string `json:"model"`
	TotalDurationMs int   `json:"total_duration_ms"`
	RequestCount   int    `json:"request_count"`
}

// ModelUsage represents aggregated usage for a model
type ModelUsage struct {
	Model         string  `json:"model"`
	TotalRequests int     `json:"total_requests"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
}
