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

	// Load routing method from settings
	if settings, err := s.GetSettings(); err == nil {
		r.SetRoutingMethod(settings.RoutingMethod)
	}

	mux := http.NewServeMux()
	// 0penAI-compatible API
	mux.HandleFunc("/v1/models", h.Models)
	mux.HandleFunc("/v1/chat/completions", h.ChatCompletions)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Admin API
	ah := &AdminHandler{store: s, farmDir: farmDir, router: r}
	// Auth routes (no middleware)
	mux.HandleFunc("/admin/api/login", ah.Login)
	mux.HandleFunc("/admin/api/logout", ah.Logout)
	mux.HandleFunc("/admin/api/auth-check", ah.AuthCheck)
	// Protected routes (require auth)
	mux.HandleFunc("/admin/api/keys", AuthMiddleware(ah.Keys))
	mux.HandleFunc("/admin/api/accounts", AuthMiddleware(ah.Accounts))
	mux.HandleFunc("/admin/api/accounts/dead", AuthMiddleware(ah.DeadAccounts))
	mux.HandleFunc("/admin/api/accounts/revive", AuthMiddleware(ah.ReviveAccount))
	mux.HandleFunc("/admin/api/usage", AuthMiddleware(ah.Usage))
	mux.HandleFunc("/admin/api/import", AuthMiddleware(ah.Import))
	mux.HandleFunc("/admin/api/reset-slot", AuthMiddleware(ah.ResetSlot))
	mux.HandleFunc("/admin/api/reset-account", AuthMiddleware(ah.ResetAccount))
	mux.HandleFunc("/admin/api/stats", AuthMiddleware(ah.Stats))
	mux.HandleFunc("/admin/api/models", AuthMiddleware(ah.Models))
	mux.HandleFunc("/admin/api/proxies", AuthMiddleware(ah.Proxies))
	mux.HandleFunc("/admin/api/proxies/check", AuthMiddleware(ah.ProxyCheck))
	mux.HandleFunc("/admin/api/farm/start", AuthMiddleware(ah.FarmStart))
	mux.HandleFunc("/admin/api/farm/stop", AuthMiddleware(ah.FarmStop))
	mux.HandleFunc("/admin/api/farm/status", AuthMiddleware(ah.FarmStatus))
	mux.HandleFunc("/admin/api/farm/runs", AuthMiddleware(ah.FarmRuns))
	mux.HandleFunc("/admin/api/farm/log", AuthMiddleware(ah.FarmLog))
	mux.HandleFunc("/admin/api/farm/config", AuthMiddleware(ah.FarmConfig))
	mux.HandleFunc("/admin/api/usage-over-time", AuthMiddleware(ah.UsageOverTime))
	mux.HandleFunc("/admin/api/top-models", AuthMiddleware(ah.TopModels))
	mux.HandleFunc("/admin/api/request-logs", AuthMiddleware(ah.RequestLogs))
	mux.HandleFunc("/admin/api/settings", AuthMiddleware(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "GET" {
			ah.GetSettings(w, req)
		} else if req.Method == "POST" {
			ah.SaveSettings(w, req)
		} else {
			w.WriteHeader(405)
		}
	}))
	mux.HandleFunc("/admin/api/change-password", AuthMiddleware(ah.ChangePassword))

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
