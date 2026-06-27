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

## Authentication
The web dashboard requires login. Default credentials:
- **Username:** admin
- **Password:** 123456

**Important:** Change the default password immediately via Settings page after first login.

## Optional: Web Dashboard
Open in browser: http://YOUR_SERVER_IP:7622
- Generate/revoke router keys
- View usage statistics and request logs
- Manage accounts
- Configure farm (IMAP, proxy, dot trick)
- Settings (proxy toggle, routing method, change password)

## Optional: Cloudflare Tunnel
To expose the router publicly:
1. Add to your cloudflared config.yml:
     - hostname: nalibaba.yourdomain.com
       service: http://127.0.0.1:7622
2. Add DNS route: cloudflared tunnel route dns TUNNEL_ID nalibaba.yourdomain.com
3. Restart cloudflared: sudo systemctl restart cloudflared
Note: Cloudflare Bot Fight Mode may block requests without a User-Agent. Clients must send a User-Agent header.

## Optional: Farm Setup (auto-register new Alibaba accounts)
The farm module is included in the repo at `farm/farm.py`. It automates Alibaba Cloud account registration using Camoufox browser automation.

### 1. Install system dependencies
  sudo apt-get update
  sudo apt-get install -y xvfb python3-pip python3-venv
Verify: which xvfb-run && python3 --version

### 2. Set up Python environment
  cd farm
  python3 -m venv .venv
  source .venv/bin/activate
  pip install -r requirements.txt
Verify: pip list | grep -E "camoufox|playwright"

### 3. Install Camoufox browser
  camoufox fetch
Verify: camoufox --version

### 4. Configure farm credentials
Open the web UI at `/farm.html` and configure:

Required:
- **IMAP User**: Your Gmail address (for OTP retrieval)
- **IMAP Password**: Gmail App Password (16-char, spaces ok)
  - Go to: Google Account → Security → 2-Step Verification → App passwords
- **Email Domain**: Your email domain (e.g., gmail.com for dot trick, or yourdomain.com for catch-all)

Optional:
- **Max Attempts**: Number of registration attempts per run (default: 10)
- **Route farm via proxy pool**: Toggle to use proxy pool for registrations

### 5. Gmail Dot Trick (optional)
Enable in web UI at `/farm.html`:
- **Use Gmail dot trick**: Insert random dots in Gmail username (e.g., `g.a.r.n.a.s.u.n.5.1.4@gmail.com`)
- Gmail ignores dots, so all variations deliver to the same inbox
- Useful for generating multiple "unique" emails from one Gmail account
- When enabled, Email Domain field is hidden (uses Gmail domain automatically)

### 6. Start the farm
Via web UI at `/farm.html`:
1. Configure IMAP credentials (step 4)
2. Set max attempts (default: 10)
3. Toggle "Use Proxy Pool" if needed
4. Click "Start Farm"

Or via API:
  curl -X POST http://127.0.0.1:7622/admin/api/farm/start \
    -H "Content-Type: application/json" \
    -d '{"max_attempts": 10}'

Monitor progress:
  tail -f data/logs/farm-*.log

### 7. Auto-import results
The router automatically imports new accounts from `results.json` every 30 seconds.
Check imported accounts via web UI at `/accounts.html` or:
  curl http://127.0.0.1:7622/admin/api/accounts

## Troubleshooting

### Router Issues
- **"no such column: is_dead"** → delete data/router.db and restart (migration will rebuild schema)
- **"no eligible account"** → import accounts: go run ./cmd/importer ../results.json
- **401 invalid router key** → generate new key via admin API
- **Login page keeps redirecting** → clear browser cookies, default password is 123456

### Farm Issues
- **"camoufox not found"** → run `camoufox fetch` or reinstall: `pip install -U camoufox`
- **"playwright not found"** → run `playwright install chromium`
- **Farm log empty** → ensure PYTHONUNBUFFERED=1 is set (router sets this automatically)
- **"IMAP authentication failed"** → check IMAP_PASS (must be App Password, not regular password)
- **OTP not received** → verify EMAIL_DOMAIN matches your actual email domain
- **Slider solving fails** → ensure uinput module is loaded: `sudo modprobe uinput`
- **Proxy connection failed** → check FARM_PROXY format (must include http://)

### Performance Issues
- **Slow registration** → reduce MAX_CONCURRENT or use faster proxy
- **High memory usage** → check for zombie Camoufox processes: `ps aux | grep camoufox`
- **Disk full** → rotate logs: `logrotate data/logs/farm-*.log`

## Done
The router is now running at http://127.0.0.1:7622. Use it as an 0penAI-compatible API endpoint with any client (Hermes, OpenCode, curl, etc.).

For farm automation, configure via web UI at http://YOUR_SERVER_IP:7622/farm.html
```
