# Alibaba Router — Final Plan

## Spec (confirmed)
- Bahasa: **Go**
- Port: **7622**
- Router key: prefix `nh-*`, generate & regenerate via web UI
- Web UI: minimal, 2 fitur — router admin + bot farm control
- No monthly reset — slot exhausted permanent sampai manual reset
- Flag per (akun, model) — akun jangan di-flag global
- Model scope: **text only** (skip image/audio/embedding/realtime/tts)
- Source akun: `~/alibaba-cloud-farm/results.json` (160 valid)
- Upstream: `https://dashscope-intl.aliyuncs.com/compatible-mode/v1`

## Architecture

```
Client (Hermes/OpenCode)
   │  POST /v1/chat/completions  (key: nh-xxxx)
   ▼
┌─────────────────────────────────────────────┐
│  Alibaba Router (Go, :7622)                 │
│  ┌─────────────┐  ┌──────────────────────┐  │
│  │ /v1/* API   │  │ Web UI (:7622)       │  │
│  │ (router)    │  │  - Router admin page │  │
│  │             │  │  - Farm control page │  │
│  └──────┬──────┘  └──────────────────────┘  │
│         │                                   │
│  ┌──────▼──────┐  ┌──────────────────────┐  │
│  │ Router core │  │ Farm runner          │  │
│  │ (roundrobin │  │ (subproc farm.py via │  │
│  │  + retry)   │  │  xvfb-run, stream log│  │
│  └──────┬──────┘  └──────────┬───────────┘  │
│         │                    │              │
│  ┌──────▼────────────────────▼──────────┐   │
│  │ SQLite (data/router.db)              │   │
│  │  accounts / usage / keys / farm_runs │   │
│  └──────────────────────────────────────┘   │
└─────────────────┬───────────────────────────┘
                  │  per-account key, round-robin
                  ▼
   https://dashscope-intl.aliyuncs.com/compatible-mode/v1
```

## SQLite schema
```sql
-- akun: never flagged dead globally (kecuali 401 key invalid)
CREATE TABLE accounts (
  id INTEGER PRIMARY KEY,
  email TEXT UNIQUE,
  api_key TEXT,
  added_at TEXT,
  source TEXT DEFAULT 'results.json'
);

-- flag per (akun, model). exhausted=1 → skip di roundrobin utk model itu
CREATE TABLE usage (
  account_id INTEGER,
  model TEXT,
  tokens_used INTEGER DEFAULT 0,
  exhausted INTEGER DEFAULT 0,       -- 1 = quota habis, skip
  exhausted_at TEXT,
  last_429_at TEXT,
  last_used_at TEXT,
  last_error TEXT,
  PRIMARY KEY (account_id, model)
);

-- router API keys (nh-*), bisa multiple
CREATE TABLE router_keys (
  key TEXT PRIMARY KEY,              -- "nh-xxxx..."
  label TEXT,
  created_at TEXT,
  active INTEGER DEFAULT 1
);

-- farm run tracking
CREATE TABLE farm_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TEXT,
  ended_at TEXT,
  status TEXT,                       -- running/success/failed/stopped
  max_attempts INTEGER,
  pid INTEGER,
  new_accounts INTEGER DEFAULT 0,
  log_path TEXT
);
```

## Routing algorithm (per request)
1. Auth: cek `Authorization: Bearer nh-xxx` di router_keys, 401 kalo invalid
2. Parse `model` dari body. Kalo model di text-allowlist lanjut, else 404
3. Kandidat: `SELECT account_id FROM usage WHERE model=? AND exhausted=0`
   + cross-join accounts (semua akun eligible — akun gak pernah global-flagged)
4. Round-robin: ambil pointer model dari memory, pilih kandidat next
5. Forward ke upstream pake api_key akun itu, stream response balik
6. Update `tokens_used += usage.total_tokens`, `last_used_at=now()`
7. Error handling upstream:
   - 429 + code `Throttling.AllocationQuota` → set `exhausted=1` utk slot ini, advance pointer, **retry next akun** (max 5x)
   - 429 + code `Throttling.*` lain (RPM/TPM) → cooldown 30s slot ini (gak exhausted), retry next akun
   - 401/403 → log `last_error`, skip akun ini utk SEMUA model utk request ini (tapi gak set exhausted permanent — bisa jadi temporary). Retry next akun.
   - 5xx → retry next akun, log
8. Kalo semua kandidat exhausted → 429 ke client `{error:"model exhausted, all accounts depleted"}`
9. Kalo 0 kandidat (model baru belum pernah dipake) → treat semua akun sebagai kandidat

## Model catalog (prefix `nalibaba-*`, text only)
Router expose model dengan prefix `nalibaba-`. Client request `nalibaba-qwen3.7-max` →
router strip prefix, forward `qwen3.7-max` ke upstream. `/v1/models` return list lengkap.

Allowlist text model (~35), filtered dari 148 total (skip image/audio/embed/mt/realtime/tts/vl-ocr/asr):
```
nalibaba-qwen3.7-max            nalibaba-qwen3.7-max-preview      nalibaba-qwen3.7-plus
nalibaba-qwen3.6-max-preview    nalibaba-qwen3.6-plus             nalibaba-qwen3.6-flash
nalibaba-qwen3.5-plus           nalibaba-qwen3.5-flash
nalibaba-qwen3-max              nalibaba-qwen3-max-preview
nalibaba-qwen-plus              nalibaba-qwen-plus-latest         nalibaba-qwen-turbo
nalibaba-qwen-flash             nalibaba-qwen-max                 nalibaba-qwq-plus
nalibaba-qwen3-coder-plus       nalibaba-qwen3-coder-next         nalibaba-qwen3-coder-flash
nalibaba-qwen-coder-plus
nalibaba-deepseek-v3.2          nalibaba-deepseek-v4-pro          nalibaba-deepseek-v4-flash
nalibaba-glm-5.1                nalibaba-glm-5.2
nalibaba-kimi-k2.7-code
nalibaba-qwen3-235b-a22b-instruct-2507    nalibaba-qwen3-235b-a22b-thinking-2507
nalibaba-qwen3-30b-a3b-instruct-2507      nalibaba-qwen3-30b-a3b-thinking-2507
nalibaba-qwen3-coder-480b-a35b-instruct
nalibaba-qwen3-next-80b-a3b-instruct      nalibaba-qwen3-next-80b-a3b-thinking
nalibaba-ccai-pro               nalibaba-qvq-max
```
Alias (opsional, map ke latest dated snapshot):
- `nalibaba-qwen3.7-max` → upstream `qwen3.7-max-2026-06-08`
- `nalibaba-qwen-plus` → upstream `qwen-plus-latest`
- `nalibaba-qwen3-max` → upstream `qwen3-max-2026-01-23`
List disimpan di `store/models.json` (bisa di-update via admin API tanpa recompile).

## Web UI (2 page, minimal)

### Page 1: Router Admin (`/` atau `/admin`)
- **Stats bar**: total akun, total slot aktif per model populer, requests served, router uptime
- **API Keys section**: list keys `nh-*`, tombol **Generate** (buat key baru), tombol **Regenerate/Revoke** per key
- **Accounts table**: email, api_key (masked), # slot exhausted, # slot active, last used. Tombol **Import from results.json** (re-sync), tombol **Reset exhausted** per akun (manual un-flag semua model)
- **Usage table** (collapsible per model): model → list akun dgn tokens_used + status exhausted. Tombol **Reset slot** (un-flag 1 slot)

### Page 2: Farm Control (`/farm`)
- **Config form** (pre-filled dari .env, editable): MAX_ATTEMPTS, FARM_PROXY
- **Start Farm** button → jalanin `xvfb-run python3 farm.py` via subprocess, stream stdout ke log panel real-time (SSE/polling)
- **Stop Farm** button → kill subprocess
- **Run history**: tabel farm_runs (waktu, status, new_accounts), klik → lihat log full
- **Live log panel**: tail output farm.py, parse "✅ SUCCESS" utk increment counter + auto-import akun baru ke accounts table
- Auto-import: tiap akun baru di results.json → insert ke accounts table (dedup by email)

## Project structure
```
~/alibaba-cloud-farm/alibaba-router/
├── go.mod
├── main.go                 # entry, router + mux
├── router/
│   ├── handler.go          # /v1/* handlers
│   ├── router.go           # roundrobin + retry logic
│   └── upstream.go         # dashscope client, streaming proxy
├── store/
│   ├── store.go            # SQLite layer
│   ├── schema.sql
│   ├── models.go
│   └── models.json         # model allowlist (nalibaba-*)
├── farm/
│   └── runner.go           # subprocess farm.py, log stream, auto-import
├── web/
│   ├── admin.html          # page 1
│   ├── farm.html           # page 2
│   └── assets/             # css/js minimal
├── bin/
│   └── alibaba-router      # compiled binary (systemd ExecStart)
└── data/                   # router.db, logs/
```

## Deployment (bare metal, kayak enowxai daemon — NO Docker)
- Build Go binary, jalan via **systemd service** `alibaba-router.service` (auto-restart, survive reboot)
- Binary: `~/alibaba-cloud-farm/alibaba-router/bin/alibaba-router`
- Data dir: `~/alibaba-cloud-farm/alibaba-router/data/` (router.db, logs/)
- Farm.py jalan di host langsung (Xvfb + camoufox udah terinstall di VPS) — router eksekusi via subprocess `xvfb-run python3 farm.py`, gak perlu bundling deps
- Service file: `/etc/systemd/system/alibaba-router.service`
  ```ini
  [Unit]
  Description=Alibaba Router
  After=network.target
  [Service]
  Type=simple
  User=ubuntu
  WorkingDirectory=/home/ubuntu/alibaba-cloud-farm/alibaba-router
  ExecStart=/home/ubuntu/alibaba-cloud-farm/alibaba-router/bin/alibaba-router
  Restart=always
  RestartSec=5
  Environment=FARM_DIR=/home/ubuntu/alibaba-cloud-farm
  Environment=DASHSCOPE_BASE_URL=https://dashscope-intl.aliyuncs.com/compatible-mode/v1
  [Install]
  WantedBy=multi-user.target
  ```
- Go install: `sudo apt install -y golang` (atau download tarball go1.22)

## Build order (task list)
1. Init Go module + project skeleton
2. SQLite schema + store layer + import results.json
3. Router core: auth, roundrobin, upstream proxy (non-streaming dulu)
4. Streaming SSE support (proxy stream chunk-by-chunk)
5. Error detection: 429 AllocationQuota → mark exhausted + retry
6. Web UI page 1 (router admin): keys, accounts, usage tables
7. Router key generate/regenerate (nh-*)
8. Farm runner subprocess + log stream + auto-import
9. Web UI page 2 (farm control): start/stop, live log, history
10. systemd service + wiring Hermes provider
11. E2E test: hit router, verify roundrobin + auto-switch
```
