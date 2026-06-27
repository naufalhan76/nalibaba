package store

import (
	"crypto/rand"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

type Settings struct {
	AuthPassword    string `json:"-"` // hashed bcrypt, not exposed
	ProxyEnabled    bool   `json:"proxy_enabled"`
	RoutingMethod   string `json:"routing_method"`
	DotTrickEnabled bool   `json:"dot_trick_enabled"`
}

// GetSettings returns current settings with defaults.
func (s *Store) GetSettings() (*Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &Settings{
		RoutingMethod: "round_robin",
	}
	rows, err := s.db.Query("SELECT key, value FROM settings")
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		switch k {
		case "auth_password":
			st.AuthPassword = v
		case "proxy_enabled":
			st.ProxyEnabled = v == "1"
		case "routing_method":
			st.RoutingMethod = v
		case "dot_trick_enabled":
			st.DotTrickEnabled = v == "1"
		}
	}
	// Initialize default password if not set
	if st.AuthPassword == "" {
		hash, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.DefaultCost)
		if err != nil {
			return st, err
		}
		st.AuthPassword = string(hash)
		_, err = s.db.Exec(`INSERT INTO settings(key, value) VALUES(?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "auth_password", st.AuthPassword)
		if err != nil {
			return st, err
		}
	}
	return st, nil
}

// SaveSettings updates settings.
func (s *Store) SaveSettings(st *Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	proxyVal := "0"
	if st.ProxyEnabled {
		proxyVal = "1"
	}
	dotVal := "0"
	if st.DotTrickEnabled {
		dotVal = "1"
	}
	updates := map[string]string{
		"proxy_enabled":    proxyVal,
		"routing_method":   st.RoutingMethod,
		"dot_trick_enabled": dotVal,
	}
	if st.AuthPassword != "" {
		updates["auth_password"] = st.AuthPassword
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for k, v := range updates {
		_, err := tx.Exec(`INSERT INTO settings(key, value) VALUES(?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// CheckPassword validates password against stored hash.
func (s *Store) CheckPassword(password string) (bool, error) {
	st, err := s.GetSettings()
	if err != nil {
		return false, err
	}
	err = bcrypt.CompareHashAndPassword([]byte(st.AuthPassword), []byte(password))
	return err == nil, nil
}

// ChangePassword updates the password hash.
func (s *Store) ChangePassword(newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	st := &Settings{AuthPassword: string(hash)}
	return s.SaveSettings(st)
}

// GenerateSessionToken creates a random session token.
func GenerateSessionToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
