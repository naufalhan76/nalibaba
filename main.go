package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"alibaba-router/router"
	"alibaba-router/store"
)

var (
	addr     = ":7622"
	dataDir  = "data"
	farmDir  = os.Getenv("FARM_DIR")
	baseURL  = os.Getenv("DASHSCOPE_BASE_URL")
)

func main() {
	flag.StringVar(&addr, "addr", addr, "listen address")
	flag.StringVar(&dataDir, "data", dataDir, "data directory")
	flag.Parse()
	if farmDir == "" {
		farmDir = "/home/ubuntu/alibaba-cloud-farm"
	}

	s, err := store.Open(dataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer s.Close()

	r := router.New(s, baseURL)
	h := router.NewHandler(r, s)

	mux := http.NewServeMux()
	// 0penAI-compatible API
	mux.HandleFunc("/v1/models", h.Models)
	mux.HandleFunc("/v1/chat/completions", h.ChatCompletions)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Admin API
	ah := &AdminHandler{store: s, farmDir: farmDir}
	mux.HandleFunc("/admin/api/keys", ah.Keys)
	mux.HandleFunc("/admin/api/accounts", ah.Accounts)
	mux.HandleFunc("/admin/api/accounts/dead", ah.DeadAccounts)
	mux.HandleFunc("/admin/api/accounts/revive", ah.ReviveAccount)
	mux.HandleFunc("/admin/api/usage", ah.Usage)
	mux.HandleFunc("/admin/api/import", ah.Import)
	mux.HandleFunc("/admin/api/reset-slot", ah.ResetSlot)
	mux.HandleFunc("/admin/api/reset-account", ah.ResetAccount)
	mux.HandleFunc("/admin/api/stats", ah.Stats)
	mux.HandleFunc("/admin/api/models", ah.Models)
	mux.HandleFunc("/admin/api/proxies", ah.Proxies)
	mux.HandleFunc("/admin/api/proxies/check", ah.ProxyCheck)
	mux.HandleFunc("/admin/api/farm/start", ah.FarmStart)
	mux.HandleFunc("/admin/api/farm/stop", ah.FarmStop)
	mux.HandleFunc("/admin/api/farm/status", ah.FarmStatus)
	mux.HandleFunc("/admin/api/farm/runs", ah.FarmRuns)
	mux.HandleFunc("/admin/api/farm/log", ah.FarmLog)
	mux.HandleFunc("/admin/api/farm/config", ah.FarmConfig)

	// Web UI
	mux.HandleFunc("/", serveWeb)

	log.Printf("Alibaba Router listening on %s (farmDir=%s)", addr, farmDir)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// serveWeb serves the web UI files.
func serveWeb(w http.ResponseWriter, r *http.Request) {
	webDir := "web"
	if _, err := os.Stat(webDir); os.IsNotExist(err) {
		webDir = "/home/ubuntu/alibaba-cloud-farm/alibaba-router/web"
	}
	path := r.URL.Path
	if path == "/" {
		path = "/dashboard.html"
	}
	// security: prevent path traversal
	clean := filepath.Clean(path)
	if clean == ".." || len(clean) > 0 && clean[0] == '/' {
		clean = clean[1:]
	}
	fp := filepath.Join(webDir, clean)
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		// fallback to old admin.html if dashboard not found
		if clean == "dashboard.html" {
			fp = filepath.Join(webDir, "admin.html")
			if _, err := os.Stat(fp); os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
		} else {
			http.NotFound(w, r)
			return
		}
	}
	http.ServeFile(w, r, fp)
}

// generateKey creates a router key with nh- prefix.
func generateKey() string {
	b := make([]byte, 20)
	rand.Read(b)
	return "nh-" + hex.EncodeToString(b)
}

// readJSON reads JSON body into v.
func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
