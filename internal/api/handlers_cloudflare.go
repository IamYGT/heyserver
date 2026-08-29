package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/services/cloudflare"
)

type cfRecordMutationRequest struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  *bool  `json:"proxied"`
	Priority int    `json:"priority,omitempty"`
}

func (request cfRecordMutationRequest) record() (cloudflare.CreateRecordRequest, error) {
	if request.Proxied == nil {
		return cloudflare.CreateRecordRequest{}, errors.New("proxied is required")
	}
	return cloudflare.ValidateAndNormalizeRecordRequest(cloudflare.CreateRecordRequest{
		Type: request.Type, Name: request.Name, Content: request.Content,
		TTL: request.TTL, Proxied: *request.Proxied, Priority: request.Priority,
	})
}

type cfAvailabilityErrorResponse struct {
	State integrationstate.State `json:"state"`
	Error string                 `json:"error"`
}

func writeCloudflareError(w http.ResponseWriter, status int, state integrationstate.State, err error) {
	if !state.IsValid() {
		state = integrationstate.Unavailable
	}
	w.Header().Set("X-HServer-Integration-State", string(state))
	jsonResponse(w, status, cfAvailabilityErrorResponse{State: state, Error: err.Error()})
}

func cloudflareErrorState(err error) integrationstate.State {
	if errors.Is(err, cloudflare.ErrNotConfigured) || errors.Is(err, cloudflare.ErrMailDNSNotConfigured) {
		return integrationstate.NotConfigured
	}
	return integrationstate.Unavailable
}

func writeCloudflareOperationError(w http.ResponseWriter, status int, err error) {
	writeCloudflareError(w, status, cloudflareErrorState(err), err)
}

// cfService builds a cloudflare.Service from cfg. Returns nil and writes a
// 503 response with a typed not_configured state when the API token is absent.
func cfService(cfg *config.Config, w http.ResponseWriter) *cloudflare.Service {
	if cfg == nil || strings.TrimSpace(cfg.CloudflareAPIToken) == "" {
		writeCloudflareError(w, http.StatusServiceUnavailable, integrationstate.NotConfigured, cloudflare.ErrNotConfigured)
		return nil
	}
	return cloudflare.NewWithMailDNS(strings.TrimSpace(cfg.CloudflareAPIToken), cfg.CloudflareAPIEmail, cloudflare.MailDNSConfig{
		Hostname:    cfg.MailDNSHostname,
		PublicIP:    cfg.MailDNSPublicIP,
		SPFRecord:   cfg.MailDNSSPFRecord,
		DMARCRecord: cfg.MailDNSDMARCRecord,
		MXPriority:  cfg.MailDNSMXPriority,
		StalwartURL: cfg.StalwartURL,
	})
}

// --- Zones -------------------------------------------------------------------

// handleCFZoneList handles GET /api/cloudflare/zones
func handleCFZoneList(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}
		inventory, err := svc.ProbeZones()
		if err != nil {
			writeCloudflareError(w, http.StatusBadGateway, inventory.State, err)
			return
		}
		w.Header().Set("X-HServer-Integration-State", string(inventory.State))
		// Preserve the established array payload for existing consumers. The
		// canonical availability result is additive in the response header.
		jsonResponse(w, http.StatusOK, inventory.Zones)
	}
}

// handleCFZoneGet handles GET /api/cloudflare/zones/{zoneId}
func handleCFZoneGet(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}
		zoneID := r.PathValue("zoneId")
		zone, err := svc.GetZone(zoneID)
		if err != nil {
			writeCloudflareOperationError(w, http.StatusBadGateway, err)
			return
		}
		jsonResponse(w, http.StatusOK, zone)
	}
}

// --- DNS Records -------------------------------------------------------------

// handleCFRecordList handles GET /api/cloudflare/zones/{zoneId}/records
// Optional query params: type, name
func handleCFRecordList(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}
		zoneID := r.PathValue("zoneId")
		query := r.URL.Query()
		for key := range query {
			if key != "type" && key != "name" {
				jsonError(w, http.StatusBadRequest, "unsupported query parameter: "+key)
				return
			}
		}
		recordType := ""
		if values, present := query["type"]; present {
			if len(values) != 1 {
				jsonError(w, http.StatusBadRequest, "type query parameter must appear exactly once")
				return
			}
			var err error
			recordType, err = cloudflare.NormalizeRecordType(values[0])
			if err != nil {
				jsonError(w, http.StatusBadRequest, "invalid type query parameter: "+err.Error())
				return
			}
		}
		name := ""
		if values, present := query["name"]; present {
			if len(values) != 1 {
				jsonError(w, http.StatusBadRequest, "name query parameter must appear exactly once")
				return
			}
			var err error
			name, err = cloudflare.NormalizeRecordName(values[0])
			if err != nil {
				jsonError(w, http.StatusBadRequest, "invalid name query parameter: "+err.Error())
				return
			}
		}

		records, err := svc.ListRecords(zoneID, recordType, name)
		if err != nil {
			writeCloudflareOperationError(w, http.StatusBadGateway, err)
			return
		}
		jsonResponse(w, http.StatusOK, records)
	}
}

// handleCFRecordCreate handles POST /api/cloudflare/zones/{zoneId}/records
func handleCFRecordCreate(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}
		zoneID := r.PathValue("zoneId")

		var body cfRecordMutationRequest
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		req, err := body.record()
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid DNS record: "+err.Error())
			return
		}

		record, err := svc.CreateRecord(zoneID, req)
		if err != nil {
			writeCloudflareOperationError(w, http.StatusBadGateway, err)
			return
		}
		jsonResponse(w, http.StatusCreated, record)
	}
}

// handleCFRecordUpdate handles PUT /api/cloudflare/zones/{zoneId}/records/{recordId}
func handleCFRecordUpdate(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}
		zoneID := r.PathValue("zoneId")
		recordID := r.PathValue("recordId")

		var body cfRecordMutationRequest
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		req, err := body.record()
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid DNS record: "+err.Error())
			return
		}

		record, err := svc.UpdateRecord(zoneID, recordID, req)
		if err != nil {
			writeCloudflareOperationError(w, http.StatusBadGateway, err)
			return
		}
		jsonResponse(w, http.StatusOK, record)
	}
}

// handleCFRecordDelete handles DELETE /api/cloudflare/zones/{zoneId}/records/{recordId}
func handleCFRecordDelete(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}
		zoneID := r.PathValue("zoneId")
		recordID := r.PathValue("recordId")
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := svc.DeleteRecord(zoneID, recordID); err != nil {
			writeCloudflareOperationError(w, http.StatusBadGateway, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleCFRecordToggleProxy handles PUT /api/cloudflare/zones/{zoneId}/records/{recordId}/proxy
// Body: {"proxied": true|false}
func handleCFRecordToggleProxy(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}
		zoneID := r.PathValue("zoneId")
		recordID := r.PathValue("recordId")

		var body struct {
			Proxied *bool `json:"proxied"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if body.Proxied == nil {
			jsonError(w, http.StatusBadRequest, "proxied is required")
			return
		}

		record, err := svc.ToggleProxy(zoneID, recordID, *body.Proxied)
		if err != nil {
			writeCloudflareOperationError(w, http.StatusBadGateway, err)
			return
		}
		jsonResponse(w, http.StatusOK, record)
	}
}

// --- Cache -------------------------------------------------------------------

// handleCFPurgeCache handles POST /api/cloudflare/zones/{zoneId}/purge
func handleCFPurgeCache(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}
		zoneID := r.PathValue("zoneId")
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := svc.PurgeCache(zoneID); err != nil {
			writeCloudflareOperationError(w, http.StatusBadGateway, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "purged"})
	}
}

// --- Email Routing -----------------------------------------------------------

// handleCFEmailRouting handles GET /api/cloudflare/zones/{zoneId}/email-routing
func handleCFEmailRouting(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}
		zoneID := r.PathValue("zoneId")

		routing, err := svc.GetEmailRouting(zoneID)
		if err != nil {
			writeCloudflareOperationError(w, http.StatusBadGateway, err)
			return
		}
		jsonResponse(w, http.StatusOK, routing)
	}
}

// --- Mail DNS Auto-Fix -------------------------------------------------------

// handleCFMailAutoFix handles POST /api/cloudflare/mail-autofix/{domain}
//
// Automatically reconciles Cloudflare DNS for the given domain so that all
// required mail DNS records (MX, SPF, DKIM, DMARC, mail A) are correct.
// Requires RoleAdmin (enforced in the router).
func handleCFMailAutoFix(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := cfService(cfg, w)
		if svc == nil {
			return
		}

		domain := r.PathValue("domain")
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		domain, err := cloudflare.NormalizeDomain(domain)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid domain: "+err.Error())
			return
		}

		result, err := svc.AutoFixMailDNS(domain)
		if err != nil {
			if errors.Is(err, cloudflare.ErrMailDNSNotConfigured) {
				writeCloudflareOperationError(w, http.StatusServiceUnavailable, err)
				return
			}
			writeCloudflareOperationError(w, http.StatusBadGateway, err)
			return
		}

		jsonResponse(w, http.StatusOK, result)
	}
}
