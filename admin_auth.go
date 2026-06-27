package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"alibaba-router/store"
)

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]int64 // token -> expiresAt timestamp
}

var sessionMgr = &SessionManager{
	sessions: make(map[string]int64),
}

func (sm *SessionManager) CreateSession() string {
	token := store.GenerateSessionToken()
	sm.mu.Lock()
	// Sessions expire after 24 hours
	sm.sessions[token] = time.Now().Add(24 * time.Hour).Unix()
	sm.mu.Unlock()
	return token
}

func (sm *SessionManager) ValidateSession(token string) bool {
	sm.mu.RLock()
	expiresAt, exists := sm.sessions[token]
	sm.mu.RUnlock()
	if !exists {
		return false
	}
	if time.Now().Unix() > expiresAt {
		// Session expired, remove it
		sm.mu.Lock()
		delete(sm.sessions, token)
		sm.mu.Unlock()
		return false
	}
	return true
}

func (sm *SessionManager) DeleteSession(token string) {
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.mu.Unlock()
}

// Login: POST {password} - validates password, creates session, sets cookie
func (a *AdminHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON"})
		return
	}

	valid, err := a.store.CheckPassword(body.Password)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !valid {
		writeJSON(w, 401, map[string]string{"error": "invalid password"})
		return
	}

	// Create session
	token := sessionMgr.CreateSession()
	http.SetCookie(w, &http.Cookie{
		Name:     "nalibaba_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// Logout: POST - clears session cookie
func (a *AdminHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}

	// Get and delete session
	cookie, err := r.Cookie("nalibaba_session")
	if err == nil {
		sessionMgr.DeleteSession(cookie.Value)
	}

	// Clear cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "nalibaba_session",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
	})

	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// AuthCheck: GET - returns {authenticated: true/false}
func (a *AdminHandler) AuthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}

	cookie, err := r.Cookie("nalibaba_session")
	if err != nil {
		writeJSON(w, 200, map[string]bool{"authenticated": false})
		return
	}

	valid := sessionMgr.ValidateSession(cookie.Value)
	writeJSON(w, 200, map[string]bool{"authenticated": valid})
}

// AuthMiddleware wraps handlers that require authentication
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("nalibaba_session")
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}

		if !sessionMgr.ValidateSession(cookie.Value) {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}

		next(w, r)
	}
}
