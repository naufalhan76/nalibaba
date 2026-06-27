# Installation Prompt for AI Agents

Paste the prompt below into an AI coding agent (Claude Code, Codex, Cursor, etc.) to automatically install and set up Nalibaba Router on a Linux VPS.

---

## Prompt

```
Install and set up the Nalibaba Router project on this Linux machine. Follow every step in order. Run each command and verify it succeeds before moving to the next. If any step fails, debug and fix it before continuing.

## Project Overview
Nalibaba Router is a Go binary that acts as an 0penAI-compatible LLM router for Alibaba Cloud Model Studio accounts. It load-balances across multiple accounts, tracks per-(account, model) quota, auto-switches on exhaustion, supports proxy pools, and includes a web dashboard. Repo: https://github.com/naufalhan76/nalibaba

## Steps

### 1. Install Go (if not present)
Check if Go 1.22+ is installed:
  go version
If not installed or version < 1.22, install it:
  sudo apt-get update && sudo apt-get install -y golang-go
Verify: go version  (must show 1.22 or higher)

### 2. Clone the repository
  cd ~
  git clone https://github.com/naufalhan76/nalibaba.git
  cd nalibaba
Verify: ls main.go go.mod  (both files must exist)

### 3. Download dependencies
  go mod download
Verify: no error output

### 4. Build the binary
  go build -o bin/alibaba-router .
Verify: ls -la bin/alibaba-router  (file must exist, ~15MB)

### 5. Create data directory
  mkdir -p data/logs
Verify: ls -d data data/logs  (both directories exist)

### 6. Prepare accounts file
The router needs a results.json file containing Alibaba Cloud accounts. Each entry: {"email": "...", "api_key": "sk-ws-..."}.
If you have an existing results.json, copy it to the parent directory:
  cp /path/to/your/results.json ../results.json
If you don't have one yet, create a placeholder:
  echo '[{"email":"test@example.com","api_key":"sk-ws-test-key"}]' > ../results.json
Note: The test key won't work for real requests, but the router will start. Replace with real keys later.

### 7. Import accounts into the database
  go run ./cmd/importer ../results.json
Verify: output shows "Imported: N | Accounts in DB: N" where N > 0

### 8. Start the router (foreground, to verify it works)
  ./bin/alibaba-router
You should see: "Alibaba Router listening on :7622"
Press Ctrl+C to stop it after confirming.

### 9. Set up as systemd service (for persistent background running)
Create the service file:
  sudo tee /etc/systemd/system/alibaba-router.service > /dev/null << 'EOF'
[Unit]
Description=Nalibaba Router
After=network.target

[Service]
Type=simple
User=$USER
WorkingDirectory=$(pwd)
ExecStart=$(pwd)/bin/alibaba-router
Restart=always
RestartSec=5
Environment=FARM_DIR=$(dirname $(pwd))
Environment=DASHSCOPE_BASE_URL=https://dashscope-intl.aliyuncs.com/compatible-mode/v1

[Install]
WantedBy=multi-user.target
EOF

Reload systemd and start:
  sudo systemctl daemon-reload
  sudo systemctl enable --now alibaba-router
Verify: sudo systemctl is-active alibaba-router  (must show "active")

### 10. Generate a router API key
  curl -s -X POST http://127.0.0.1:7622/admin/api/keys \
    -H 'Content-Type: application/json' \
    -d '{"label":"default"}'
Save the returned key (format: nh-xxxx...). You'll need this for API requests.

### 11. Verify the router works
Replace YOUR_KEY with the key from step 10:
  curl -s http://127.0.0.1:7622/v1/models \
    -H "Authorization: Bearer YOUR_KEY" | head -c 200
Verify: you see JSON with model list (nalibaba-qwen-plus, nalibaba-glm-5.2, etc.)

### 12. Test a chat completion
  curl -s http://127.0.0.1:7622/v1/chat/completions \
    -H "Authorization: Bearer YOUR_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"nalibaba-qwen-plus","messages":[{"role":"user","content":"say hello"}],"max_tokens":10}'
Verify: you get a valid chat completion response with content.

## Optional: Web Dashboard
Open in browser: http://YOUR_SERVER_IP:7622/dashboard.html
- Generate/revoke router keys
- View usage statistics
- Manage accounts
- Configure farm (if farm.py available)

## Optional: Cloudflare Tunnel
To expose the router publicly:
1. Add to your cloudflared config.yml:
     - hostname: nalibaba.yourdomain.com
       service: http://127.0.0.1:7622
2. Add DNS route: cloudflared tunnel route dns TUNNEL_ID nalibaba.yourdomain.com
3. Restart cloudflared: sudo systemctl restart cloudflared
Note: Cloudflare Bot Fight Mode may block requests without a User-Agent. Clients must send a User-Agent header.

## Optional: Farm Setup (for auto-registering new Alibaba accounts)
The farm feature requires farm.py (not included in this repo — it's an external dependency).
If you have farm.py:
1. Place it in the parent directory (FARM_DIR)
2. Install Python deps: pip install camoufox playwright python-dotenv
3. Install Xvfb: sudo apt install xvfb
4. Configure via web UI at /farm.html (IMAP user, password, email domain)
5. Start farm from web UI or: curl -X POST http://127.0.0.1:7622/admin/api/farm/start

## Troubleshooting
- "no such column: is_dead" → delete data/router.db and restart (migration will rebuild schema)
- "no eligible account" → import accounts: go run ./cmd/importer ../results.json
- 401 invalid router key → generate new key via admin API
- Farm "camoufox not found" → use venv python: .venv/bin/python3, or pip install camoufox
- Farm log empty → ensure PYTHONUNBUFFERED=1 is set (router sets this automatically)

## Done
The router is now running at http://127.0.0.1:7622. Use it as an 0penAI-compatible API endpoint with any client (Hermes, OpenCode, curl, etc.).
```
