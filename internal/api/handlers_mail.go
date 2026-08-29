package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/IamYGT/heyserver/internal/config"
	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

// newMailService builds a mail.Service from the panel config.
// It prefers StalwartAPIKey (Bearer token) when set; otherwise falls back
// to StalwartAdminUser / StalwartAdminPass (HTTP Basic auth).
func newMailService(cfg *config.Config) *mailsvc.Service {
	return mailsvc.NewWithAPIKey(
		cfg.StalwartURL,
		cfg.StalwartAPIKey,
		cfg.StalwartAdminUser,
		cfg.StalwartAdminPass,
	).WithRuntime(cfg.StalwartService, cfg.StalwartConfig, cfg.StalwartBinary)
}

// mailErrorStatus maps an unconfigured optional mail integration to the
// truthful service-unavailable response while preserving each handler's
// existing fallback status for other failures.
func mailErrorStatus(err error, fallback int) int {
	if errors.Is(err, mailsvc.ErrNotConfigured) {
		return http.StatusServiceUnavailable
	}
	return fallback
}

// parseInt is a small helper to avoid importing strconv everywhere.
func parseInt(s string) (int, error) {
	return strconv.Atoi(s)
}

// handleMailStatus returns the Stalwart service state + queue size.
func handleMailStatus(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		status := svc.GetStatus()
		jsonResponse(w, http.StatusOK, status)
	}
}

// handleMailDomains lists all domain principals from Stalwart.
func handleMailDomains(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		domains, err := svc.ListDomains()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, domains)
	}
}

// handleMailDomainCreate registers a new mail domain in Stalwart.
func handleMailDomainCreate(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Domain string `json:"domain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if body.Domain == "" {
			jsonError(w, http.StatusBadRequest, "domain is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.CreateDomain(body.Domain); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, map[string]string{"status": "created"})
	}
}

// handleMailDomainDelete removes a mail domain from Stalwart (path param {domain}).
func handleMailDomainDelete(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.PathValue("domain")
		if domain == "" {
			jsonError(w, http.StatusBadRequest, "domain path parameter is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.DeleteDomain(domain); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// handleMailAccountList lists all individual (mailbox) accounts.
func handleMailAccountList(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		domain := r.URL.Query().Get("domain")
		accounts, err := svc.ListAccounts(domain)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, accounts)
	}
}

// handleMailAccountCreate creates a new mailbox account.
func handleMailAccountCreate(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req mailsvc.CreateAccountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if req.Email == "" || req.Password == "" {
			jsonError(w, http.StatusBadRequest, "email and password are required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.CreateAccount(req); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, map[string]string{"status": "created"})
	}
}

// handleMailAccountGet fetches the details of a single mailbox account.
func handleMailAccountGet(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := r.PathValue("email")
		if email == "" {
			jsonError(w, http.StatusBadRequest, "email path parameter is required")
			return
		}

		svc := newMailService(cfg)
		acc, err := svc.GetAccount(email)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, acc)
	}
}

// handleMailAccountDelete removes a mailbox account (path param {email}).
func handleMailAccountDelete(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := r.PathValue("email")
		if email == "" {
			jsonError(w, http.StatusBadRequest, "email path parameter is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.DeleteAccount(email); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// handleMailAccountQuota updates the storage quota of an existing account.
func handleMailAccountQuota(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := r.PathValue("email")
		if email == "" {
			jsonError(w, http.StatusBadRequest, "email path parameter is required")
			return
		}

		var body struct {
			Quota int64 `json:"quota"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}

		svc := newMailService(cfg)
		if err := svc.UpdateQuota(email, body.Quota); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "updated"})
	}
}

// handleMailAccountPassword updates the password of an existing account.
func handleMailAccountPassword(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := r.PathValue("email")
		if email == "" {
			jsonError(w, http.StatusBadRequest, "email path parameter is required")
			return
		}

		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if body.Password == "" {
			jsonError(w, http.StatusBadRequest, "password is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.UpdatePassword(email, body.Password); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "updated"})
	}
}

// handleMailAccountGetPassword retrieves the plaintext password for an account.
func handleMailAccountGetPassword(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := r.PathValue("email")
		if email == "" {
			jsonError(w, http.StatusBadRequest, "email path parameter is required")
			return
		}
		svc := newMailService(cfg)
		password, err := svc.GetPassword(email)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"email": email, "password": password})
	}
}

// ── Aliases (type=list principals) ───────────────────────────────────────────

// handleMailAliasList lists all alias (list) principals.
// Optional query param: ?domain= to filter by domain.
func handleMailAliasList(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.URL.Query().Get("domain")
		svc := newMailService(cfg)
		aliases, err := svc.ListAliases(domain)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, aliases)
	}
}

// handleMailAliasCreate creates a new alias (list principal).
// Body: {"alias":"info@example.com","destination":"admin@example.com"}
func handleMailAliasCreate(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Alias       string `json:"alias"`
			Destination string `json:"destination"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if body.Alias == "" || body.Destination == "" {
			jsonError(w, http.StatusBadRequest, "alias and destination are required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.CreateAlias(body.Alias, body.Destination); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, map[string]string{"status": "created"})
	}
}

// handleMailAliasDelete removes an alias by name (path param {id}).
func handleMailAliasDelete(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "id path parameter is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.DeleteAlias(id); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// ── Groups (type=group principals) ───────────────────────────────────────────

// handleMailGroupList lists all group principals.
func handleMailGroupList(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		groups, err := svc.ListGroups()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, groups)
	}
}

// handleMailGroupCreate creates a new group principal.
// Body: {"name":"support","members":["alice@example.com","bob@example.com"]}
func handleMailGroupCreate(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name    string   `json:"name"`
			Members []string `json:"members"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if body.Name == "" {
			jsonError(w, http.StatusBadRequest, "name is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.CreateGroup(body.Name, body.Members); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, map[string]string{"status": "created"})
	}
}

// handleMailGroupUpdateMembers replaces the member list of an existing group.
// Body: {"members":["alice@example.com","bob@example.com"]}
func handleMailGroupUpdateMembers(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			jsonError(w, http.StatusBadRequest, "name path parameter is required")
			return
		}

		var body struct {
			Members []string `json:"members"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}

		svc := newMailService(cfg)
		if err := svc.UpdateGroupMembers(name, body.Members); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "updated"})
	}
}

// ── Queue ─────────────────────────────────────────────────────────────────────

// handleMailQueue returns messages currently in the Stalwart outbound queue.
// Optional query param: ?limit=N (default 100).
func handleMailQueue(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := parseInt(v); err == nil && n > 0 {
				limit = n
			}
		}
		svc := newMailService(cfg)
		msgs, err := svc.ListQueueMessages(limit)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, msgs)
	}
}

// handleMailQueueRetry reschedules a queued message for immediate retry (path param {id}).
func handleMailQueueRetry(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "id path parameter is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.RetryMessage(id); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "retrying"})
	}
}

// handleMailQueueDelete cancels and removes a queued message (path param {id}).
func handleMailQueueDelete(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "id path parameter is required")
			return
		}

		svc := newMailService(cfg)
		if err := svc.DeleteMessage(id); err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// ── DNS ───────────────────────────────────────────────────────────────────────

// handleMailDNSCheck performs a comprehensive DNS health check for the given domain.
// It checks MX, SPF, DKIM (multiple selectors), DMARC, and optionally rDNS.
//
// Path parameter: {domain}
// Optional query parameter: ip — the server public IP for rDNS (PTR) verification.
//
// Returns a DNSHealthReport with a 0-100 score and actionable suggestions.
func handleMailDNSCheck(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.PathValue("domain")
		if domain == "" {
			jsonError(w, http.StatusBadRequest, "domain path parameter is required")
			return
		}

		// Server IP for rDNS check (PTR record verification).
		// Auto-detect from MX record if not provided.
		serverIP := r.URL.Query().Get("ip")
		if serverIP == "" {
			// Look up the MX target's A record to get the server IP automatically.
			mxs, _ := net.LookupMX(domain)
			if len(mxs) > 0 {
				addrs, _ := net.LookupHost(strings.TrimSuffix(mxs[0].Host, "."))
				if len(addrs) > 0 {
					serverIP = addrs[0]
				}
			}
		}

		svc := newMailService(cfg)
		report := svc.CheckDNS(domain, serverIP)
		jsonResponse(w, http.StatusOK, report)
	}
}
