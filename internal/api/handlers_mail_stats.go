package api

// handlers_mail_stats.go — HTTP handlers for mail statistics and auto-discovery.
//
// Stats endpoints (auth required):
//   GET  /api/mail/stats
//   GET  /api/mail/stats/top-senders?limit=N
//   GET  /api/mail/stats/top-recipients?limit=N
//   GET  /api/mail/stats/volume?period=24h|7d
//   GET  /api/mail/stats/storage
//   GET  /api/mail/stats/deliverability
//
// Auto-discovery endpoints (no auth — must be reachable by mail clients):
//   GET  /mail/autoconfig/mail/config-v1.1.xml?emailaddress=user@domain.com
//   GET  /.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=user@domain.com
//   POST /autodiscover/autodiscover.xml

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	"github.com/IamYGT/heyserver/internal/config"
	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

// ── Mail Statistics ───────────────────────────────────────────────────────────

// handleMailStats returns aggregate send/receive/spam/failure counts for today.
func handleMailStats(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		stats := svc.GetMailStats()
		jsonResponse(w, http.StatusOK, stats)
	}
}

// handleMailTopSenders returns the most active sending addresses.
//
// Query params:
//   - limit — max results to return (default 10, max 100)
func handleMailTopSenders(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := parseLimit(r, 10, 100)
		svc := newMailService(cfg)
		senders := svc.GetTopSenders(limit)
		jsonResponse(w, http.StatusOK, map[string]any{
			"senders": senders,
			"total":   len(senders),
		})
	}
}

// handleMailTopRecipients returns the most active recipient addresses.
//
// Query params:
//   - limit — max results to return (default 10, max 100)
func handleMailTopRecipients(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := parseLimit(r, 10, 100)
		svc := newMailService(cfg)
		recipients := svc.GetTopRecipients(limit)
		jsonResponse(w, http.StatusOK, map[string]any{
			"recipients": recipients,
			"total":      len(recipients),
		})
	}
}

// handleMailVolume returns a time-series of sent/received message counts.
//
// Query params:
//   - period — "24h" (hourly, default) or "7d" (daily)
func handleMailVolume(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		period := r.URL.Query().Get("period")
		if period != "7d" {
			period = "24h"
		}
		svc := newMailService(cfg)
		points := svc.GetMailVolume(period)
		jsonResponse(w, http.StatusOK, map[string]any{
			"period": period,
			"points": points,
		})
	}
}

// handleMailStorageUsage returns per-account storage usage.
func handleMailStorageUsage(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		usage, err := svc.GetStorageUsage()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"accounts": usage,
			"total":    len(usage),
		})
	}
}

// handleMailDeliverability returns the delivery success rate for today.
func handleMailDeliverability(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		d := svc.GetDeliverability()
		jsonResponse(w, http.StatusOK, d)
	}
}

// ── Auto-discovery ────────────────────────────────────────────────────────────

// handleMailAutoconfig serves Mozilla Thunderbird auto-configuration XML.
//
// Routes (registered without auth):
//
//	GET /mail/autoconfig/mail/config-v1.1.xml?emailaddress=user@domain
//	GET /.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=user@domain
func handleMailAutoconfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := domainFromRequest(r, cfg)
		if domain == "" {
			http.Error(w, "domain could not be determined", http.StatusBadRequest)
			return
		}

		svc := mailsvc.New(cfg.StalwartURL, cfg.StalwartAdminUser, cfg.StalwartAdminPass)
		data, err := svc.GenerateAutoconfig(domain)
		if err != nil {
			http.Error(w, "failed to generate autoconfig: "+err.Error(), mailErrorStatus(err, http.StatusInternalServerError))
			return
		}

		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// handleMailAutodiscover serves Microsoft Outlook Autodiscover XML.
//
// Routes (registered without auth):
//
//	POST /autodiscover/autodiscover.xml
//
// Outlook sends a POST with an XML body containing the user's email address.
// We parse it, extract the domain, and return the appropriate settings.
func handleMailAutodiscover(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req mailsvc.AutodiscoverRequest
		email := ""
		domain := ""

		// Try to decode the POST body; gracefully fall back to Host header.
		if r.Body != nil {
			_ = xml.NewDecoder(r.Body).Decode(&req)
			email = strings.TrimSpace(req.Request.EMailAddress)
		}

		if at := strings.LastIndex(email, "@"); at > 0 {
			domain = email[at+1:]
		}
		if domain == "" {
			domain = domainFromRequest(r, cfg)
		}
		if domain == "" {
			http.Error(w, "domain could not be determined", http.StatusBadRequest)
			return
		}

		svc := mailsvc.New(cfg.StalwartURL, cfg.StalwartAdminUser, cfg.StalwartAdminPass)
		data, err := svc.GenerateAutodiscover(domain, email)
		if err != nil {
			http.Error(w, "failed to generate autodiscover: "+err.Error(), mailErrorStatus(err, http.StatusInternalServerError))
			return
		}

		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// parseLimit reads ?limit=N from the request and clamps it to [1, max].
func parseLimit(r *http.Request, defaultVal, maxVal int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return defaultVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

// domainFromRequest extracts the domain to use for auto-discovery.
// Priority:
//  1. ?emailaddress=user@domain  query param
//  2. Host header (strips port)
func domainFromRequest(r *http.Request, _ *config.Config) string {
	if ea := r.URL.Query().Get("emailaddress"); ea != "" {
		if at := strings.LastIndex(ea, "@"); at > 0 {
			return strings.ToLower(ea[at+1:])
		}
	}
	host := r.Host
	if host == "" {
		return ""
	}
	// Strip port if present
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	return strings.ToLower(host)
}
