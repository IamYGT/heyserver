package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// jsonResponse writes a JSON response with the given status code and data.
func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// jsonError writes a JSON error response with the given status code and message.
func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

func decodeStrictJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body contains trailing JSON")
	}
	return nil
}

func requireEmptyRequestBody(r *http.Request) error {
	const maxWhitespaceBytes = 4096
	data, err := io.ReadAll(io.LimitReader(r.Body, maxWhitespaceBytes+1))
	if err != nil {
		return errors.New("read request body")
	}
	if len(data) > maxWhitespaceBytes || strings.TrimSpace(string(data)) != "" {
		return errors.New("request body must be empty")
	}
	return nil
}

// All handler implementations are in their respective files:
// Auth     → handlers_auth.go
// Audit    → handlers_audit.go
// Backup   → handlers_backup.go
// Cron     → handlers_cron.go
// Database → handlers_database.go
// Deploy   → handlers_deploy.go
// Domain   → handlers_domain.go
// Files    → handlers_files.go
// Firewall → handlers_firewall.go
// Logs     → handlers_logs.go
// Mail     → handlers_mail.go
// Monitor  → handlers_monitor.go
// Nginx    → handlers_nginx.go
// Notify   → handlers_notify.go
// PHP-FPM  → handlers_php.go
// PM2      → handlers_pm2.go
// Security → handlers_security.go
// Settings → handlers_settings.go
// SSL      → handlers_ssl.go
// Terminal → handlers_terminal.go
// Users    → handlers_users.go
