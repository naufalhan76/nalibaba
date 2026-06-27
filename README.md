# Nalibaba Router

A high-performance **0penAI-compatible LLM router** for Alibaba Cloud Model Studio (DashScope) accounts. Built in Go, it fronts multiple Alibaba accounts with round-robin load balancing, automatic quota exhaustion detection, proxy pool support, and a built-in account farm — all behind a single API endpoint.

## Installation

### For LLM Agents

Fetch the full guide and follow it step by step:

```bash
curl -fsSL https://raw.githubusercontent.com/naufalhan76/nalibaba/main/INSTALL.md
```

The guide covers: Go installation, repository clone, dependency download, binary build, account import from results.json, systemd service setup, router key generation, API verification, optional Cloudflare Tunnel exposure, farm configuration (Camoufox + IMAP), and troubleshooting. Don't summarize it; read it end to end and execute every step with verification.

### For Human

```bash
# 1. Clone & build
git clone https://github.com/naufalhan76/nalibaba.git
cd nalibaba
go build -o bin/alibaba-router .

# 2. Import accounts (results.json = [{"email":"...","api_key":"sk-ws-..."}])
cp /path/to/results.json ../results.json
go run ./cmd/importer ../results.json

# 3. Run
./bin/alibaba-router   # → listening on :7622

# 4. Generate router key (save the nh-xxx key)
curl -X POST http://127.0.0.1:7622/admin/api/keys -H 'Content-Type: application/json' -d '{"label":"default"}'

# 5. Test
curl http://127.0.0.1:7622/v1/chat/completions \
  -H "Authorization: Bearer *** \
  -H "Content-Type: application/json" \
  -d '{"model":"nalibaba-qwen-plus","messages":[{"role":"user","content":"hello"}],"max_tokens":10}'
```

For systemd service, Cloudflare tunnel, and farm setup, see the [full manual](./INSTALL.md) or [Quick Start](#quick-start-manual) section below.

## Why?

Alibaba Cloud Model Studio gives each account a **1M token quota per model** (not per account). With 160+ accounts × 35 models, that's millions of tokens available — but managing them manually is painful. Nalibaba Router automates the routing, tracks usage per (account, model), auto-switches when quota is exhausted, and even farms new accounts when you need more capacity.

## Features

### Routing
- **Round-robin** across all accounts per model
- **Per-(account, model) quota tracking** — account flagged exhausted for one model still serves others
- **Auto-switch on 429** `Throttling.AllocationQuota` → marks slot exhausted, retries next account (max 5x)
- **Rate-limit handling** — `limit_requests` / RPM errors trigger cooldown, not permanent flag
- **Dead account detection** — 401/403 (invalid key) marks account dead, skipped for ALL models permanently
- **Streaming SSE** — full support, chunk-by-chunk proxy with usage token capture
- **Tool calling & thinking models** — pass-through `tool_calls`, `reasoning_content` untouched

### Proxy Pool
- Round-robin proxy selection for all upstream requests
- Auto-retry on proxy failure (mark unhealthy, switch to next)
- Health check + **region detection** via ip-api.com
- Seed from existing proxy databases

### Account Farm
- Built-in browser automation (Camoufox + Playwright) for Alibaba account registration
- **Real-time auto-import** — new accounts enter the pool immediately on success (not after farm finishes)
- IMAP OTP reading (Gmail App Password + catch-all domain)
- Slider/captcha detection with retry
- Configurable via web UI (IMAP creds, domain, max attempts, proxy toggle)

### Web Dashboard (6 pages)
- **Dashboard** — stats overview, router keys (generate/copy/revoke), quick health
- **Usage** — per-(account, model) token tracking, filter by model/status, reset slots
- **Accounts** — import from results.json, pagination, dead account management
- **Models** — 35 model catalog with copy upstream ID, search
- **Proxies** — add/check-all/delete, region & latency display
- **Farm** — config form (IMAP, domain, proxy toggle), start/stop, live log, run history, setup guide

### Security & Deployment
- **No Docker** — single Go binary + systemd service (survives reboot, auto-restart)
- Router API keys with `nh-*` prefix, generate/regenerate/revoke via web
- Cloudflare tunnel support
- SQLite storage (WAL mode, single-writer)

## Tech Stack

| Component | Technology |
|---|---|
| Language | Go 1.22 |
| HTTP Router | net/http (stdlib) |
| Database | SQLite (modernc.org/sqlite — pure Go, no CGO) |
| Web UI | Vanilla HTML/CSS/JS (single-file, no frameworks) |
| Account Farm | Python 3 + Camoufox + Playwright (subprocess) |
| Display | Xvfb (headless browser via xvfb-run) |
| Email | IMAP (Gmail App Password) |
| Deployment | systemd service, bare metal |
| Tunnel | Cloudflare Tunnel (cloudflared) |

## Architecture

```
Client (Hermes / OpenCode / curl)
  │  POST /v1/chat/completions  (key: nh-xxx)
  ▼
┌──────────────────────────────────────────┐
│  Nalibaba Router (Go, :7622)             │
│  ┌──────────┐  ┌─────────────────────┐   │
│  │ /v1/* API│  │ Web UI (:7622)      │   │
│  │ (router) │  │ 6 pages, admin API  │   │
│  └────┬─────┘  └─────────────────────┘   │
│       │                                  │
│  ┌────▼─────┐  ┌─────────────────────┐   │
│  │ Router   │  │ Farm Runner         │   │
│  │ core     │  │ (xvfb-run farm.py)  │   │
│  │ round-   │  │ real-time import    │   │
│  │ robin    │  └──────────┬──────────┘   │
│  └────┬─────┘             │              │
│  ┌────▼──────────────────▼──────────┐    │
│  │ SQLite (data/router.db)          │    │
│  │  accounts / usage / keys /       │    │
│  │  proxies / farm_runs / config    │    │
│  └──────────────────────────────────┘    │
└──────────────┬───────────────────────────┘
               │ per-account key + proxy (round-robin)
               ▼
   https://dashscope-intl.aliyuncs.com/compatible-mode/v1
```

## Quick Start (manual)

### Prerequisites
- Go 1.22+
- Python 3.10+ (for farm feature)
- Xvfb (`sudo apt install xvfb`)
- Camoufox + Playwright (`pip install camoufox playwright`)

### 1. Build
```bash
cd alibaba-router
go build -o bin/alibaba-router .
```

### 2. Import accounts
Place your `results.json` (array of `{email, api_key}`) in the parent directory, then:
```bash
go run ./cmd/importer ../results.json
```

### 3. Run
```bash
./bin/alibaba-router
# Listening on :7622
```

### 4. Generate router key
```bash
curl -X POST http://127.0.0.1:7622/admin/api/keys \
  -H 'Content-Type: application/json' \
  -d '{"label":"my-key"}'
# → {"key":"nh-xxxx...","label":"my-key"}
```

### 5. Use as OpenAI-compatible API
```bash
curl http://127.0.0.1:7622/v1/chat/completions \
  -H "Authorization: Bearer nh-xxxx..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "nalibaba-qwen-plus",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 50
  }'
```

## Configuration

### Environment Variables
| Variable | Default | Description |
|---|---|---|
| `DASHSCOPE_BASE_URL` | `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` | Upstream API endpoint |
| `FARM_DIR` | `/home/ubuntu/alibaba-cloud-farm` | Directory containing farm.py |

### Farm Config (via web UI at `/farm.html`)
| Field | Description |
|---|---|
| IMAP User | Gmail address for OTP reading |
| IMAP Password | Gmail App Password (not login password) |
| Email Domain | Catch-all domain forwarding to Gmail |
| Max Attempts | Max successful registrations per farm run |
| Use Proxy Pool | Toggle: route farm traffic through proxy pool |

### systemd Service
```ini
[Unit]
Description=Nalibaba Router
After=network.target

[Service]
Type=simple
User=ubuntu
WorkingDirectory=/path/to/alibaba-router
ExecStart=/path/to/alibaba-router/bin/alibaba-router
Restart=always
RestartSec=5
Environment=FARM_DIR=/path/to/farm
Environment=DASHSCOPE_BASE_URL=https://dashscope-intl.aliyuncs.com/compatible-mode/v1

[Install]
WantedBy=multi-user.target
```

```bash
sudo cp alibaba-router.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now alibaba-router
```

## API Reference

### OpenAI-compatible Endpoints
| Method | Path | Description |
|---|---|---|
| GET | `/v1/models` | List available models (requires router key) |
| POST | `/v1/chat/completions` | Chat completion (streaming + non-streaming) |

### Admin API (no auth)
| Method | Path | Description |
|---|---|---|
| GET | `/admin/api/stats` | Summary statistics |
| GET/POST/DELETE | `/admin/api/keys` | Router key management |
| GET | `/admin/api/accounts` | List accounts |
| GET | `/admin/api/accounts/dead` | List dead accounts |
| POST | `/admin/api/accounts/revive` | Revive dead account |
| POST | `/admin/api/import` | Re-import from results.json |
| GET | `/admin/api/usage` | Usage tracking per (account, model) |
| POST | `/admin/api/reset-slot` | Reset exhausted slot |
| POST | `/admin/api/reset-account` | Reset all slots for account |
| GET | `/admin/api/models` | Model catalog |
| GET/POST/DELETE | `/admin/api/proxies` | Proxy pool management |
| POST | `/admin/api/proxies/check` | Health check + region detection |
| GET/POST | `/admin/api/farm/config` | Farm configuration |
| POST | `/admin/api/farm/start` | Start farm |
| POST | `/admin/api/farm/stop` | Stop farm |
| GET | `/admin/api/farm/status` | Farm run status |
| GET | `/admin/api/farm/runs` | Farm run history |
| GET | `/admin/api/farm/log` | Farm log tail |
| GET | `/healthz` | Health check (no auth) |

## Available Models (35 text models)

All models use `nalibaba-` prefix. The router strips the prefix and forwards the upstream model ID.

| Router Model | Upstream | Context |
|---|---|---|
| `nalibaba-qwen3.7-max` | qwen3.7-max | 256k |
| `nalibaba-qwen-plus` | qwen-plus-latest | 128k |
| `nalibaba-glm-5.2` | glm-5.2 | 1M |
| `nalibaba-deepseek-v4-pro` | deepseek-v4-pro | 128k |
| `nalibaba-kimi-k2.7-code` | kimi-k2.7-code | 256k |
| ... | ... | ... |

Full list at `/admin/api/models` or in `store/models.json`.

## Setup Guide

### Gmail App Password (for IMAP)
1. Enable 2-Step Verification: Google Account → Security → 2-Step Verification
2. Generate App Password: Google Account → Security → App passwords
3. Select "Mail" as app, name it "Farm"
4. Copy the 16-char password (format: `abcd efgh ijkl mnop`)

### Cloudflare Catch-all Email Domain
1. Cloudflare Dashboard → your domain → Email → Email Routing
2. Enable Email Routing (auto-adds MX records)
3. Routing Rules → Catch-all address → Edit
4. Set "Send to destination" → your Gmail address
5. Any email to `*@yourdomain.com` now forwards to your Gmail

## Project Structure

```
alibaba-router/
├── main.go              # Entry point, HTTP server, routing
├── admin.go             # Admin API handlers (keys, accounts, usage, stats)
├── admin_extra.go       # Admin API handlers (dead accounts, models, proxies)
├── farm.go              # Farm runner subprocess + config + auto-import
├── go.mod
├── router/
│   ├── router.go        # Round-robin + retry logic
│   ├── upstream.go      # DashScope client, streaming/non-streaming
│   ├── handler.go       # HTTP handlers for /v1/*
│   ├── proxy.go         # Proxy pool manager
│   ├── helpers.go       # Token extraction, proxy URL parsing
│   └── errors.go        # Error definitions
├── store/
│   ├── store.go         # SQLite layer, schema init, migration
│   ├── usage.go         # Usage tracking ops
│   ├── dead_proxies.go  # Dead account + proxy pool ops
│   ├── farm_config.go   # Farm config key-value store
│   ├── schema.sql       # Database schema
│   └── models.json      # Model allowlist (nalibaba-* → upstream)
├── web/
│   ├── dashboard.html   # Stats + router keys + quick health
│   ├── usage.html       # Token usage tracking
│   ├── accounts.html    # Account management + import
│   ├── models.html      # Model catalog + copy
│   ├── proxies.html     # Proxy pool management
│   └── farm.html        # Farm control + config + setup guide
├── cmd/
│   └── importer/
│       └── main.go      # CLI tool to import results.json
└── data/                # SQLite DB + logs (gitignored)
```

## Wiring to Hermes / OpenCode

### Hermes (`~/.hermes/config.yaml`)
```yaml
providers:
  custom:nalibaba:
    api_key: nh-xxxx...
    base_url: http://127.0.0.1:7622/v1
    model: nalibaba-qwen3.7-max
    models:
    - id: nalibaba/qwen3.7-max
      name: Qwen3.7 Max
      context_window: 256000
    - id: nalibaba/qwen-plus
      name: Qwen Plus
      context_window: 128000
```

### OpenCode (`~/.config/opencode/opencode.json`)
```json
{
  "provider": {
    "nalibaba": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "nalibaba",
      "options": {
        "baseURL": "http://127.0.0.1:7622/v1",
        "apiKey": "nh-xxxx..."
      }
    }
  }
}
```

## Shoutouts

- [**alibaba-cloud-farm**](https://github.com/Micolaabdi/alibaba-cloud-farm) by [@Micolaabdi](https://github.com/Micolaabdi) — Bulk-register Alibaba Cloud accounts and harvest Model Studio API keys (1M free tokens each) using Camoufox + IMAP OTP automation. The perfect companion for feeding accounts into Nalibaba Router.

## License

MIT
