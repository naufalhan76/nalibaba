package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"alibaba-router/store"
)

// Farm runner manages farm.py subprocess execution.

type FarmRunner struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	runID  int64
	logPath string
	logFile *os.File
}

var farmRunner = &FarmRunner{}

// FarmStart: POST {max_attempts} — start farm.py via xvfb-run.
// Reads farm config from DB, passes as env to subprocess.
// Real-time: tails log file, detects "✅ SUCCESS", imports account to pool immediately.
func (a *AdminHandler) FarmStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var body struct {
		MaxAttempts int `json:"max_attempts"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}

	// load farm config from DB
	cfg, err := a.store.GetFarmConfig()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// validate required config
	if cfg[store.CfgIMAPUser] == "" || cfg[store.CfgEmailDomain] == "" {
		writeJSON(w, 400, map[string]string{
			"error": "farm config incomplete — set IMAP_USER and EMAIL_DOMAIN first",
		})
		return
	}
	// max_attempts: body override > config > default
	maxAttempts := body.MaxAttempts
	if maxAttempts <= 0 {
		if ma, err := strconv.Atoi(cfg[store.CfgMaxAttempts]); err == nil && ma > 0 {
			maxAttempts = ma
		} else {
			maxAttempts = 10
		}
	}

	// FARM_PROXY: if "pool" (toggle on), pick a healthy proxy from pool and convert format
	// farm.py expects: host:port:user:pass
	// proxy pool stores: http://user:pass@ip:port
	proxyMode := cfg[store.CfgFarmProxy] // "pool" = use pool, "" = direct
	farmProxyEnv := ""
	if proxyMode == "pool" {
		proxies, err := a.store.GetHealthyProxies()
		if err == nil && len(proxies) > 0 {
			// pick random healthy proxy
			p := proxies[time.Now().UnixNano()%int64(len(proxies))]
			farmProxyEnv = convertProxyURL(p.URL)
		}
	}
	if farmProxyEnv != "" {
		cfg[store.CfgFarmProxy] = farmProxyEnv
	} else if proxyMode == "pool" {
		// pool requested but no healthy proxies — warn but continue direct
		cfg[store.CfgFarmProxy] = ""
	}

	farmRunner.mu.Lock()
	defer farmRunner.mu.Unlock()
	if farmRunner.cmd != nil && farmRunner.cmd.Process != nil {
		writeJSON(w, 409, map[string]string{"error": "farm already running"})
		return
	}

	// prepare log file
	logDir := filepath.Join(a.farmDir, "alibaba-router", "data", "logs")
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, fmt.Sprintf("farm-%d.log", os.Getpid()))
	logFile, err := os.Create(logPath)
	if err != nil {
		writeErr(w, 500, err)
		return
	}

	// build command: xvfb-run python3 farm.py
	// use venv python if available (camoufox installed in .venv)
	pythonBin := "python3"
	venvPython := filepath.Join(a.farmDir, ".venv", "bin", "python3")
	if _, err := os.Stat(venvPython); err == nil {
		pythonBin = venvPython
	}
	cmd := exec.Command("xvfb-run", "--auto-servernum", pythonBin, "farm.py")
	cmd.Dir = a.farmDir
	// build env from config (pass through host env + override with config)
	env := os.Environ()
	for k, v := range cfg {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	env = append(env, "MAX_ATTEMPTS="+strconv.Itoa(maxAttempts))
	env = append(env, "PYTHONUNBUFFERED=1") // ensure real-time log output
	cmd.Env = env
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		writeErr(w, 500, err)
		return
	}

	// count accounts before run (for delta calc)
	accsBefore, _ := a.store.ListAccounts()
	countBefore := len(accsBefore)

	runID, _ := a.store.CreateFarmRun(maxAttempts, cmd.Process.Pid, logPath)
	farmRunner.cmd = cmd
	farmRunner.runID = runID
	farmRunner.logPath = logPath
	farmRunner.logFile = logFile

	// background: wait + real-time log tail for auto-import
	go func() {
		// start log tailer for real-time import
		importDone := make(chan struct{})
		go a.tailLogForImport(logPath, importDone)

		err := cmd.Wait()
		logFile.Close()
		close(importDone)

		// final re-import to catch any missed
		a.importResultsDirect()

		status := "success"
		if err != nil {
			status = "failed"
		}
		accsAfter, _ := a.store.ListAccounts()
		newCount := len(accsAfter) - countBefore
		if newCount < 0 {
			newCount = 0
		}
		a.store.UpdateFarmRun(runID, status, newCount)
		farmRunner.mu.Lock()
		farmRunner.cmd = nil
		farmRunner.logFile = nil
		farmRunner.mu.Unlock()
	}()

	writeJSON(w, 200, map[string]any{
		"status":   "started",
		"run_id":   runID,
		"pid":      cmd.Process.Pid,
		"log_path": logPath,
	})
}

// tailLogForImport reads log file line-by-line, detects successful registrations,
// and imports the account to the pool immediately (real-time auto-import).
// farm.py prints: "→ ✅ SUCCESS! Total accounts: N" followed by "→ Email: X" and "→ API Key: Y..."
// We parse results.json directly when we detect a success line, since the log format
// may vary. Simpler: on each "SUCCESS" line, re-read results.json and import new entries.
func (a *AdminHandler) tailLogForImport(logPath string, done chan struct{}) {
	f, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		select {
		case <-done:
			return
		default:
		}
		n, err := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			// process complete lines
			for {
				idx := -1
				for i, b := range buf {
					if b == '\n' {
						idx = i
						break
					}
				}
				if idx < 0 {
					break
				}
				line := string(buf[:idx])
				buf = buf[idx+1:]
				// detect success line
				if contains(line, "SUCCESS") || contains(line, "✅") {
					// re-import results.json to pick up the new account
					a.importResultsDirect()
				}
			}
		}
		if err != nil {
			// EOF — wait a bit for more output
			select {
			case <-done:
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

// FarmStop: POST — kill running farm.
func (a *AdminHandler) FarmStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	farmRunner.mu.Lock()
	defer farmRunner.mu.Unlock()
	if farmRunner.cmd == nil || farmRunner.cmd.Process == nil {
		writeJSON(w, 404, map[string]string{"error": "no farm running"})
		return
	}
	// kill process group
	pid := farmRunner.cmd.Process.Pid
	syscall.Kill(-pid, syscall.SIGTERM)
	farmRunner.cmd = nil
	if farmRunner.logFile != nil {
		farmRunner.logFile.Close()
		farmRunner.logFile = nil
	}
	a.store.UpdateFarmRun(farmRunner.runID, "stopped", 0)
	writeJSON(w, 200, map[string]string{"status": "stopped"})
}

// FarmStatus: GET — current run status.
func (a *AdminHandler) FarmStatus(w http.ResponseWriter, r *http.Request) {
	farmRunner.mu.Lock()
	defer farmRunner.mu.Unlock()
	running := farmRunner.cmd != nil && farmRunner.cmd.Process != nil
	resp := map[string]any{"running": running}
	if running {
		resp["pid"] = farmRunner.cmd.Process.Pid
		resp["run_id"] = farmRunner.runID
		resp["log_path"] = farmRunner.logPath
	}
	writeJSON(w, 200, resp)
}

// FarmRuns: GET — list recent runs.
func (a *AdminHandler) FarmRuns(w http.ResponseWriter, r *http.Request) {
	limit := a.parseInt(r, "limit", 20)
	runs, err := a.store.ListFarmRuns(limit)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, runs)
}

// FarmLog: GET ?run_id=X — tail of log file (last 200 lines).
func (a *AdminHandler) FarmLog(w http.ResponseWriter, r *http.Request) {
	logPath := r.URL.Query().Get("path")
	if logPath == "" && farmRunner.logPath != "" {
		logPath = farmRunner.logPath
	}
	if logPath == "" {
		// get latest from runs
		runs, _ := a.store.ListFarmRuns(1)
		if len(runs) > 0 {
			logPath = runs[0].LogPath
		}
	}
	if logPath == "" {
		writeJSON(w, 404, map[string]string{"error": "no log path"})
		return
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write(data)
}

// importResultsDirect re-imports results.json without writing HTTP response.
func (a *AdminHandler) importResultsDirect() {
	src := filepath.Join(a.farmDir, "results.json")
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	var entries []struct {
		Email  string `json:"email"`
		APIKey string `json:"api_key"`
	}
	json.Unmarshal(data, &entries)
	for _, e := range entries {
		if e.APIKey != "" {
			a.store.ImportAccount(e.Email, e.APIKey, "results.json")
		}
	}
}

// countAccounts returns current account count (for delta calc).
func (a *AdminHandler) countAccounts() int {
	accs, _ := a.store.ListAccounts()
	return len(accs)
}

// convertProxyURL converts http://user:pass@ip:port → ip:port:user:pass (farm.py format).
func convertProxyURL(proxyURL string) string {
	// parse: http://user:pass@ip:port
	// strip scheme
	s := proxyURL
	if i := indexOf(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// split user:pass@ip:port
	atIdx := lastIndexOf(s, "@")
	if atIdx < 0 {
		return s // no auth, just ip:port
	}
	creds := s[:atIdx]   // user:pass
	host := s[atIdx+1:]  // ip:port
	return host + ":" + creds
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func lastIndexOf(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// FarmConfig: GET returns config, POST updates config.
// Config keys: IMAP_USER, IMAP_PASS, EMAIL_DOMAIN, IMAP_HOST, IMAP_PORT, FARM_PROXY, MAX_ATTEMPTS, RESULTS_FILE
func (a *AdminHandler) FarmConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		cfg, err := a.store.GetFarmConfig()
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, cfg)
	case "POST":
		var cfg map[string]string
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := a.store.SetFarmConfigBatch(cfg); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "saved"})
	default:
		w.WriteHeader(405)
	}
}
