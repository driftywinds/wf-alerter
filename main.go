package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"

	"warframe-portal/internal/db"
	"warframe-portal/internal/handlers"
	"warframe-portal/internal/scheduler"
	"warframe-portal/internal/warframe"
)

//go:embed web
var webFiles embed.FS

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9091"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "warframe.db"
	}

	// Initialize database
	database, err := db.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Initialize Warframe API client
	platform := os.Getenv("WF_PLATFORM")
	if platform == "" {
		platform = "pc"
	}
	wfClient := warframe.NewClient(platform, 60*time.Second)

	// Initialize session store
	sessionKey := os.Getenv("SESSION_SECRET")
	if sessionKey == "" {
		sessionKey = "change-this-secret-in-production-please-32+"
		log.Println("WARNING: Using default SESSION_SECRET. Set SESSION_SECRET env var in production!")
	}
	store := sessions.NewCookieStore([]byte(sessionKey))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	// Apprise API URL from environment
	appriseAPIURL := os.Getenv("APPRISE_API_URL")
	if appriseAPIURL == "" {
		appriseAPIURL = "http://localhost:8000"
	}

	// Initialize handlers
	h := handlers.New(database, wfClient, store, appriseAPIURL)

	// Initialize and start scheduler
	s := scheduler.New(database, wfClient, appriseAPIURL)
	go s.Run()

	// Setup router
	r := mux.NewRouter()

	// --- API routes ---
	api := r.PathPrefix("/api").Subrouter()

	// Auth
	api.HandleFunc("/auth/register", h.Register).Methods("POST", "OPTIONS")
	api.HandleFunc("/auth/login", h.Login).Methods("POST", "OPTIONS")
	api.HandleFunc("/auth/logout", h.Logout).Methods("POST", "OPTIONS")
	api.HandleFunc("/auth/me", h.Me).Methods("GET", "OPTIONS")

	// Data endpoints (public)
	api.HandleFunc("/arcanes", h.GetArcanes).Methods("GET")
	api.HandleFunc("/archon-shards", h.GetArchonShards).Methods("GET")

	// Worldstate (public)
	api.HandleFunc("/worldstate", h.GetWorldstate).Methods("GET")

	// Alerts (authenticated)
	api.HandleFunc("/alerts", h.AuthMiddleware(h.GetAlerts)).Methods("GET")
	api.HandleFunc("/alerts", h.AuthMiddleware(h.CreateAlert)).Methods("POST")
	api.HandleFunc("/alerts/{id:[0-9]+}", h.AuthMiddleware(h.UpdateAlert)).Methods("PUT")
	api.HandleFunc("/alerts/{id:[0-9]+}", h.AuthMiddleware(h.DeleteAlert)).Methods("DELETE")
	api.HandleFunc("/alerts/{id:[0-9]+}/test", h.AuthMiddleware(h.TestAlert)).Methods("POST")
	api.HandleFunc("/alerts/log", h.AuthMiddleware(h.GetAlertLog)).Methods("GET")

	// Settings (authenticated)
	api.HandleFunc("/settings", h.AuthMiddleware(h.GetSettings)).Methods("GET")
	api.HandleFunc("/settings", h.AuthMiddleware(h.UpdateSettings)).Methods("PUT")
	api.HandleFunc("/settings/password", h.AuthMiddleware(h.ChangePassword)).Methods("PUT")

	// Admin (admin only)
	api.HandleFunc("/admin/users", h.AuthMiddleware(h.AdminGetUsers)).Methods("GET")
	api.HandleFunc("/admin/users/{id:[0-9]+}", h.AuthMiddleware(h.AdminDeleteUser)).Methods("DELETE")
	api.HandleFunc("/admin/users/{id:[0-9]+}/role", h.AuthMiddleware(h.AdminSetUserRole)).Methods("PUT")

	// Serve static web files
	webFS, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.FS(webFS))))

	// Catch-all: serve index.html for SPA routing
	r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, err := webFiles.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content)
	})

	log.Printf("🚀 Warframe Portal starting on http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}
