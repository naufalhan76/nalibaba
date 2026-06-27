-- Alibaba Router schema

CREATE TABLE IF NOT EXISTS accounts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT UNIQUE NOT NULL,
  api_key TEXT NOT NULL,
  added_at TEXT DEFAULT (datetime('now')),
  source TEXT DEFAULT 'results.json',
  is_dead INTEGER DEFAULT 0,
  dead_reason TEXT,
  dead_at TEXT
);

-- per (akun, model) tracking. exhausted=1 -> skip di roundrobin utk model itu.
-- akun TIDAK pernah di-flag global.
CREATE TABLE IF NOT EXISTS usage (
  account_id INTEGER NOT NULL,
  model TEXT NOT NULL,
  tokens_used INTEGER DEFAULT 0,
  exhausted INTEGER DEFAULT 0,
  exhausted_at TEXT,
  last_429_at TEXT,
  last_used_at TEXT,
  last_error TEXT,
  PRIMARY KEY (account_id, model),
  FOREIGN KEY (account_id) REFERENCES accounts(id)
);

-- router API keys (nh-*), bisa multiple
CREATE TABLE IF NOT EXISTS router_keys (
  key TEXT PRIMARY KEY,
  label TEXT,
  created_at TEXT DEFAULT (datetime('now')),
  active INTEGER DEFAULT 1
);

-- farm run tracking
CREATE TABLE IF NOT EXISTS farm_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TEXT,
  ended_at TEXT,
  status TEXT,
  max_attempts INTEGER,
  pid INTEGER,
  new_accounts INTEGER DEFAULT 0,
  log_path TEXT
);

-- proxy pool: https://user:pass@ip:port
CREATE TABLE IF NOT EXISTS proxies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  url TEXT UNIQUE NOT NULL,
  region TEXT,
  healthy INTEGER DEFAULT 1,
  last_check_at TEXT,
  last_error TEXT,
  latency_ms INTEGER,
  added_at TEXT DEFAULT (datetime('now'))
);

-- farm config (key-value store for env vars)
CREATE TABLE IF NOT EXISTS farm_config (
  key TEXT PRIMARY KEY,
  value TEXT
);

-- settings (key-value for app settings)
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT
);

CREATE INDEX IF NOT EXISTS idx_usage_model ON usage(model, exhausted);
CREATE INDEX IF NOT EXISTS idx_usage_account ON usage(account_id);


-- Request logs table for individual API requests
CREATE TABLE IF NOT EXISTS request_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id INTEGER NOT NULL,
  model TEXT NOT NULL,
  request_body TEXT,
  response_body TEXT,
  proxy_url TEXT,
  status_code INTEGER,
  tokens_used INTEGER DEFAULT 0,
  created_at TEXT DEFAULT (datetime('now')),
  FOREIGN KEY (account_id) REFERENCES accounts(id)
);

-- Index for fast queries
CREATE INDEX IF NOT EXISTS idx_request_logs_created ON request_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_account ON request_logs(account_id, model);
