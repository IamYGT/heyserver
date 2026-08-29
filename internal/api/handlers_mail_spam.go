package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/IamYGT/heyserver/internal/config"
	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

// ── Spam Config ──────────────────────────────────────────────────────────────

// handleMailSpamConfig returns the current spam filter configuration.
func handleMailSpamConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		result, err := svc.GetSpamConfig()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}

// handleMailSpamConfigUpdate updates spam filter settings.
func handleMailSpamConfigUpdate(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req mailsvc.UpdateSpamConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}

		svc := newMailService(cfg)
		if err := svc.UpdateSpamConfig(req); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "updated"})
	}
}

// ── Blocklist ────────────────────────────────────────────────────────────────

// handleMailBlocklist handles GET and POST for the spam blocklist.
func handleMailBlocklist(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)

		switch r.Method {
		case http.MethodGet:
			patterns, err := svc.GetBlockedSenders()
			if err != nil {
				jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
				return
			}
			jsonResponse(w, http.StatusOK, patterns)

		case http.MethodPost:
			var body struct {
				Pattern string `json:"pattern"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
			if body.Pattern == "" {
				jsonError(w, http.StatusBadRequest, "pattern is required")
				return
			}
			if err := svc.AddBlockedSender(body.Pattern); err != nil {
				jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
				return
			}
			jsonResponse(w, http.StatusCreated, map[string]string{"status": "added"})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// handleMailBlocklistDelete removes a pattern from the spam blocklist.
// Pattern is URL-encoded in the path: DELETE /api/mail/spam/blocklist/{pattern}
func handleMailBlocklistDelete(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pattern := r.PathValue("pattern")
		if pattern == "" {
			jsonError(w, http.StatusBadRequest, "pattern path parameter is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.RemoveBlockedSender(pattern); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "removed"})
	}
}

// ── Allowlist ────────────────────────────────────────────────────────────────

// handleMailAllowlist handles GET and POST for the spam allowlist.
func handleMailAllowlist(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)

		switch r.Method {
		case http.MethodGet:
			patterns, err := svc.GetAllowedSenders()
			if err != nil {
				jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
				return
			}
			jsonResponse(w, http.StatusOK, patterns)

		case http.MethodPost:
			var body struct {
				Pattern string `json:"pattern"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
			if body.Pattern == "" {
				jsonError(w, http.StatusBadRequest, "pattern is required")
				return
			}
			if err := svc.AddAllowedSender(body.Pattern); err != nil {
				jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
				return
			}
			jsonResponse(w, http.StatusCreated, map[string]string{"status": "added"})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// handleMailAllowlistDelete removes a pattern from the spam allowlist.
func handleMailAllowlistDelete(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pattern := r.PathValue("pattern")
		if pattern == "" {
			jsonError(w, http.StatusBadRequest, "pattern path parameter is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.RemoveAllowedSender(pattern); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "removed"})
	}
}

// ── Security / TLS ───────────────────────────────────────────────────────────

// handleMailTLSConfig returns the current TLS configuration.
func handleMailTLSConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		result, err := svc.GetTLSConfig()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}

// ── Security / Rate Limits ───────────────────────────────────────────────────

// handleMailRateLimits handles GET and PUT for SMTP rate limits.
func handleMailRateLimits(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)

		switch r.Method {
		case http.MethodGet:
			limits, err := svc.GetRateLimits()
			if err != nil {
				jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
				return
			}
			jsonResponse(w, http.StatusOK, limits)

		case http.MethodPut:
			var req mailsvc.UpdateRateLimitsRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
			if err := svc.UpdateRateLimits(req); err != nil {
				jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
				return
			}
			jsonResponse(w, http.StatusOK, map[string]string{"status": "updated"})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// ── Security / Failed Logins ─────────────────────────────────────────────────

// handleMailFailedLogins returns recent failed login attempts from Stalwart logs.
func handleMailFailedLogins(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}

		svc := newMailService(cfg)
		entries, err := svc.GetFailedLogins(limit)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, entries)
	}
}

// ── Security / Connections ───────────────────────────────────────────────────

// handleMailConnectionStats returns active SMTP/IMAP connection counts.
func handleMailConnectionStats(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		stats, err := svc.GetConnectionStats()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, stats)
	}
}
