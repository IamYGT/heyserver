package api

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/services/cloudflare"
	domainsvc "github.com/IamYGT/heyserver/internal/services/domain"
	"github.com/IamYGT/heyserver/internal/services/pm2"
	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
)

const (
	domainRequestBodyLimit       = 64 << 10
	domainToggleRequestBodyLimit = 4 << 10
)

func localDomainService(cfg *config.Config) *domainsvc.Service {
	return domainsvc.NewWithConfig(domainsvc.ServiceConfig{
		PM2: pm2.Config{
			User:         cfg.PM2User,
			Home:         cfg.PM2Home,
			Bin:          cfg.PM2Bin,
			AllowedRoots: cfg.PM2AllowedRoots,
		},
		VhostsRoot:     cfg.VhostsRoot,
		SitesAvailable: cfg.NginxSitesAvailable,
		SitesEnabled:   cfg.NginxSitesEnabled,
		SnippetsDir:    cfg.NginxSnippetsDir,
		CertbotBin:     cfg.CertbotBin,
		CertbotDir:     cfg.CertbotConfigDir,
		ACMEWebroot:    cfg.ACMEWebroot,
	})
}

func writeDomainServiceError(w http.ResponseWriter, defaultStatus int, message string, err error) {
	if errors.Is(err, domainsvc.ErrNotConfigured) {
		defaultStatus = http.StatusServiceUnavailable
	} else if errors.Is(err, domainsvc.ErrInvalidRequest) {
		defaultStatus = http.StatusBadRequest
	} else if errors.Is(err, domainsvc.ErrNotFound) {
		defaultStatus = http.StatusNotFound
	} else if errors.Is(err, domainsvc.ErrConflict) {
		defaultStatus = http.StatusConflict
	}
	jsonError(w, defaultStatus, message+err.Error())
}

type domainDNSCapability struct {
	Provider   string `json:"provider"`
	Status     string `json:"status"`
	Origin     string `json:"origin,omitempty"`
	RecordType string `json:"recordType,omitempty"`
	Proxied    bool   `json:"proxied"`
	Message    string `json:"message"`
}

type domainProvisioningCapabilities struct {
	VhostsRoot          string              `json:"vhostsRoot"`
	NginxSitesAvailable string              `json:"nginxSitesAvailable"`
	NginxSitesEnabled   string              `json:"nginxSitesEnabled"`
	NginxSnippetsDir    string              `json:"nginxSnippetsDir"`
	DNS                 domainDNSCapability `json:"dns"`
}

type domainDNSReconcileFunc func(domain, origin string, proxied bool) (*cloudflare.DomainDNSResult, error)

type domainDNSAttempt struct {
	Attempted bool
	Result    *cloudflare.DomainDNSResult
}

func newDomainCertificateHooks(
	req domainsvc.CreateRequest,
	status domainDNSCapability,
	reconcile domainDNSReconcileFunc,
) (domainsvc.CreateHooks, *domainDNSAttempt) {
	attempt := &domainDNSAttempt{}
	if !req.CreateDNSRecord || !req.IssueSSL {
		return domainsvc.CreateHooks{}, attempt
	}
	return domainsvc.CreateHooks{BeforeCertificate: func() error {
		attempt.Attempted = true
		result, err := reconcile(req.Domain, status.Origin, status.Proxied)
		if err != nil {
			return fmt.Errorf("Cloudflare DNS provisioning failed: %w", err)
		}
		attempt.Result = result
		return nil
	}}, attempt
}

func newDomainUptimeMonitor(domain string, httpsActive bool) *store.UptimeMonitor {
	scheme := "http"
	if httpsActive {
		scheme = "https"
	}
	return &store.UptimeMonitor{
		Name:                domain,
		Type:                "http",
		URL:                 scheme + "://" + domain,
		Method:              "GET",
		IntervalSecs:        60,
		TimeoutSecs:         10,
		Retries:             1,
		RetryInterval:       30,
		AcceptedStatusCodes: `["200-299"]`,
		MaxRedirects:        5,
		IsActive:            true,
		TLSCheck:            httpsActive,
		TLSExpiryWarnDays:   14,
	}
}

func domainMonitorMatches(monitor store.UptimeMonitor, domain string) bool {
	if strings.EqualFold(strings.TrimSpace(monitor.Hostname), domain) {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(monitor.URL))
	return err == nil && strings.EqualFold(parsed.Hostname(), domain)
}

func absoluteCapabilityPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Clean(path)
}

func domainDNSStatus(cfg *config.Config, probe bool) domainDNSCapability {
	status := domainDNSCapability{
		Provider: "cloudflare",
		Status:   "not_configured",
		Origin:   strings.TrimSpace(cfg.DomainDNSOrigin),
		Proxied:  cfg.DomainDNSProxied,
		Message:  "Set HSERVER_CF_API_TOKEN and HSERVER_DOMAIN_DNS_ORIGIN to enable DNS provisioning.",
	}
	if cfg.CloudflareAPIToken == "" || status.Origin == "" {
		return status
	}
	ip := net.ParseIP(status.Origin)
	if ip == nil {
		status.Message = "HSERVER_DOMAIN_DNS_ORIGIN must be a valid IPv4 or IPv6 address."
		return status
	}
	status.RecordType = "A"
	if ip.To4() == nil {
		status.RecordType = "AAAA"
	}
	if !probe {
		status.Status = "healthy"
		status.Message = "Cloudflare domain DNS provisioning is configured."
		return status
	}
	zones, err := cloudflare.New(cfg.CloudflareAPIToken, cfg.CloudflareAPIEmail).ListZones()
	if err != nil {
		status.Status = "unavailable"
		status.Message = "Cloudflare API is unavailable: " + err.Error()
		return status
	}
	if len(zones) == 0 {
		status.Status = "unavailable"
		status.Message = "Cloudflare credentials have no accessible DNS zones."
		return status
	}
	status.Status = "healthy"
	status.Message = "Cloudflare domain DNS provisioning is ready."
	return status
}

// handleDomainProvisioningCapabilities reports installation-owned defaults and
// an honest optional-provider state for the create-domain flow.
func handleDomainProvisioningCapabilities(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, domainProvisioningCapabilities{
			VhostsRoot:          absoluteCapabilityPath(cfg.VhostsRoot),
			NginxSitesAvailable: absoluteCapabilityPath(cfg.NginxSitesAvailable),
			NginxSitesEnabled:   absoluteCapabilityPath(cfg.NginxSitesEnabled),
			NginxSnippetsDir:    absoluteCapabilityPath(cfg.NginxSnippetsDir),
			DNS:                 domainDNSStatus(cfg, true),
		})
	}
}

// handleDomainList returns all domains parsed from nginx sites-available.
// GET /api/domains
func handleDomainList(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domains, err := localDomainService(cfg).List()
		if err != nil {
			writeDomainServiceError(w, http.StatusInternalServerError, "failed to list domains: ", err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{"domains": domains})
	}
}

// handleDomainGet returns full detail for a single domain.
// GET /api/domains/{id}
func handleDomainGet(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := domainsvc.NormalizeDomain(r.PathValue("id"))
		if err != nil {
			jsonError(w, http.StatusBadRequest, "domain id is invalid")
			return
		}
		detail, err := localDomainService(cfg).Get(id)
		if err != nil {
			writeDomainServiceError(w, http.StatusInternalServerError, "", err)
			return
		}
		jsonResponse(w, http.StatusOK, detail)
	}
}

// handleDomainCreate provisions a new nginx domain and auto-creates an uptime monitor.
// POST /api/domains
func handleDomainCreate(cfg *config.Config, engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req domainsvc.CreateRequest
		r.Body = http.MaxBytesReader(w, r.Body, domainRequestBodyLimit)
		if err := decodeStrictJSON(r, &req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				jsonError(w, http.StatusRequestEntityTooLarge, "domain request body is too large")
			} else {
				jsonError(w, http.StatusBadRequest, "invalid request body")
			}
			return
		}
		normalizedDomain, err := domainsvc.NormalizeDomain(req.Domain)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.Domain = normalizedDomain
		if req.Type == "" {
			req.Type = "php"
		}
		if req.IssueSSL && strings.TrimSpace(req.SSLEmail) == "" {
			jsonError(w, http.StatusBadRequest, "sslEmail is required when issueSSL is true")
			return
		}
		dnsStatus := domainDNSCapability{}
		if req.CreateDNSRecord {
			// Validate local provider configuration without making an early remote
			// call. The real reconciliation belongs after the HTTP site is active.
			dnsStatus = domainDNSStatus(cfg, false)
			if dnsStatus.Status != "healthy" {
				jsonError(w, http.StatusServiceUnavailable, dnsStatus.Message)
				return
			}
		}
		service := localDomainService(cfg)
		reconcileDNS := func(domain, origin string, proxied bool) (*cloudflare.DomainDNSResult, error) {
			return cloudflare.New(cfg.CloudflareAPIToken, cfg.CloudflareAPIEmail).
				ReconcileDomainAddress(domain, origin, proxied)
		}
		createHooks, dnsAttempt := newDomainCertificateHooks(req, dnsStatus, reconcileDNS)
		createResult, err := service.CreateWithHooks(req, createHooks)
		if err != nil {
			writeDomainServiceError(w, http.StatusInternalServerError, "", err)
			return
		}

		dnsResult := dnsAttempt.Result
		warnings := make([]string, 0, 2)
		if createResult.Warning != "" {
			warnings = append(warnings, createResult.Warning)
		}
		if req.CreateDNSRecord && !dnsAttempt.Attempted {
			var dnsErr error
			dnsResult, dnsErr = reconcileDNS(req.Domain, dnsStatus.Origin, dnsStatus.Proxied)
			if dnsErr != nil {
				dnsWarning := "Domain created, but Cloudflare DNS provisioning failed: " + dnsErr.Error()
				warnings = append(warnings, dnsWarning)
				log.Printf("[domain] warn: %s", dnsWarning)
			}
		}

		// Best-effort: auto-create uptime HTTP monitor for the new domain.
		if engine != nil {
			monitor := newDomainUptimeMonitor(req.Domain, createResult.HTTPSActive)
			if err := engine.Repo().CreateMonitor(monitor); err != nil {
				log.Printf("[uptime] warn: failed to auto-create monitor for %s: %v", req.Domain, err)
			} else {
				engine.AddMonitor(r.Context(), monitor)
			}
		}

		response := map[string]interface{}{
			"message": "domain created",
			"domain":  req.Domain,
		}
		if dnsResult != nil {
			response["dns"] = dnsResult
		}
		statusCode := http.StatusCreated
		if len(warnings) > 0 {
			response["warning"] = strings.Join(warnings, " ")
			statusCode = http.StatusMultiStatus
		}
		jsonResponse(w, statusCode, response)
	}
}

// handleDomainDelete removes a domain's nginx config and PHP pool,
// and auto-deletes any matching uptime monitor.
// DELETE /api/domains/{id}?deleteFiles=true
func handleDomainDelete(cfg *config.Config, engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := domainsvc.NormalizeDomain(r.PathValue("id"))
		if err != nil {
			jsonError(w, http.StatusBadRequest, "domain id is invalid")
			return
		}
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		query := r.URL.Query()
		for key := range query {
			if key != "deleteFiles" {
				jsonError(w, http.StatusBadRequest, "unsupported query parameter")
				return
			}
		}
		deleteFiles := false
		if values, exists := query["deleteFiles"]; exists {
			if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
				jsonError(w, http.StatusBadRequest, "deleteFiles must appear once as true or false")
				return
			}
			deleteFiles = values[0] == "true"
		}
		if err := localDomainService(cfg).Delete(id, deleteFiles); err != nil {
			writeDomainServiceError(w, http.StatusInternalServerError, "", err)
			return
		}

		// Best-effort: remove uptime monitor whose URL contains the domain name.
		if engine != nil {
			monitors, err := engine.Repo().ListMonitors()
			if err != nil {
				log.Printf("[uptime] warn: failed to list monitors for domain cleanup (%s): %v", id, err)
			} else {
				for _, m := range monitors {
					if domainMonitorMatches(m, id) {
						engine.RemoveMonitor(m.ID)
						if delErr := engine.Repo().DeleteMonitor(m.ID); delErr != nil {
							log.Printf("[uptime] warn: failed to delete monitor %d for domain %s: %v", m.ID, id, delErr)
						}
					}
				}
			}
		}

		jsonResponse(w, http.StatusOK, map[string]string{"message": "domain deleted", "domain": id})
	}
}

// handleDomainCheck performs a pre-creation analysis for a domain name.
// POST /api/domains/check   body: {"domain": "app.example.com"}
func handleDomainCheck(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req domainsvc.CheckRequest
		r.Body = http.MaxBytesReader(w, r.Body, domainRequestBodyLimit)
		if err := decodeStrictJSON(r, &req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				jsonError(w, http.StatusRequestEntityTooLarge, "domain check request body is too large")
			} else {
				jsonError(w, http.StatusBadRequest, "invalid request body")
			}
			return
		}
		req.Domain = strings.TrimSpace(strings.ToLower(req.Domain))
		if req.Domain == "" {
			jsonError(w, http.StatusBadRequest, "domain is required")
			return
		}

		// Collect Cloudflare zone names — best-effort, empty list on error.
		var cfZoneNames []string
		if cfg.CloudflareAPIToken != "" {
			cfSvc := cloudflare.New(cfg.CloudflareAPIToken, cfg.CloudflareAPIEmail)
			if zones, err := cfSvc.ListZones(); err == nil {
				for _, z := range zones {
					cfZoneNames = append(cfZoneNames, z.Name)
				}
			}
		}

		result := localDomainService(cfg).Check(req.Domain, cfZoneNames)
		jsonResponse(w, http.StatusOK, result)
	}
}

// handleDomainToggle enables or disables a domain via sites-enabled symlink.
// POST /api/domains/{id}/toggle   body: {"active": true|false}
func handleDomainToggle(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := domainsvc.NormalizeDomain(r.PathValue("id"))
		if err != nil {
			jsonError(w, http.StatusBadRequest, "domain id is invalid")
			return
		}
		var body struct {
			Active *bool `json:"active"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, domainToggleRequestBodyLimit)
		if err := decodeStrictJSON(r, &body); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				jsonError(w, http.StatusRequestEntityTooLarge, "domain toggle request body is too large")
			} else {
				jsonError(w, http.StatusBadRequest, "invalid request body")
			}
			return
		}
		if body.Active == nil {
			jsonError(w, http.StatusBadRequest, "active is required")
			return
		}
		if err := localDomainService(cfg).Toggle(id, *body.Active); err != nil {
			writeDomainServiceError(w, http.StatusInternalServerError, "", err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"domain": id,
			"active": *body.Active,
		})
	}
}
