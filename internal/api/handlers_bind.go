package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	bindsvc "github.com/IamYGT/heyserver/internal/services/bind"
)

var bindService = bindsvc.New()

type bindSOAMutationRequest struct {
	PrimaryNs  string  `json:"primaryNs"`
	Hostmaster string  `json:"hostmaster"`
	Refresh    *uint64 `json:"refresh"`
	Retry      *uint64 `json:"retry"`
	Expire     *uint64 `json:"expire"`
	Minimum    *uint64 `json:"minimum"`
}

func (request bindSOAMutationRequest) soa() (bindsvc.UpdateSOARequest, error) {
	timers := []struct {
		name  string
		value *uint64
	}{
		{name: "refresh", value: request.Refresh},
		{name: "retry", value: request.Retry},
		{name: "expire", value: request.Expire},
		{name: "minimum", value: request.Minimum},
	}
	for _, timer := range timers {
		if timer.value == nil {
			return bindsvc.UpdateSOARequest{}, fmt.Errorf("%s is required", timer.name)
		}
		if *timer.value > 2147483647 {
			return bindsvc.UpdateSOARequest{}, fmt.Errorf("%s must be between 0 and 2147483647 seconds", timer.name)
		}
	}
	return bindsvc.ValidateAndNormalizeSOA(bindsvc.UpdateSOARequest{
		PrimaryNs: request.PrimaryNs, Hostmaster: request.Hostmaster,
		Refresh: uint32(*request.Refresh), Retry: uint32(*request.Retry),
		Expire: uint32(*request.Expire), Minimum: uint32(*request.Minimum),
	})
}

// InitBindService enables durable BIND lifecycle recovery in the configured
// Heyserver data directory and attempts recovery before the HTTP router starts.
func InitBindService(dataDir string) error {
	service := bindsvc.NewWithStateDir(dataDir)
	bindService = service
	return service.RecoverPendingTransaction()
}

// handleBindZoneList returns all zones declared in named.conf.local with their serials.
// GET /api/dns/zones
func handleBindZoneList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zones, err := bindService.ListZones()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, zones)
	}
}

// handleBindZoneGet returns zone metadata and all DNS records.
// GET /api/dns/zones/{domain}
func handleBindZoneGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain, err := bindZoneDomain(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		zone, err := bindService.GetZone(domain)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, zone)
	}
}

// handleBindZoneCreate creates a new zone file and registers it in named.conf.local.
// POST /api/dns/zones — requires admin role.
func handleBindZoneCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req bindsvc.CreateZoneRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		req, err := bindsvc.ValidateAndNormalizeCreateZone(req)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid zone: "+err.Error())
			return
		}

		zone, err := bindService.CreateZone(req)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, zone)
	}
}

// handleBindZoneDelete removes a zone file and its named.conf.local declaration.
// DELETE /api/dns/zones/{domain} — requires admin role.
func handleBindZoneDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		domain, err := bindZoneDomain(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := bindService.DeleteZone(domain); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"message": "zone " + domain + " deleted",
		})
	}
}

// handleBindZoneExport returns the raw zone file content as text/plain.
// GET /api/dns/zones/{domain}/export
func handleBindZoneExport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain, err := bindZoneDomain(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		content, err := bindService.ExportZone(domain)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"db."+domain+"\"")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(content))
	}
}

// handleBindSOAGet returns the parsed SOA record for a zone.
// GET /api/dns/zones/{domain}/soa
func handleBindSOAGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain, err := bindZoneDomain(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		soa, err := bindService.GetSOA(domain)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, soa)
	}
}

// handleBindSOAUpdate replaces SOA fields and bumps the serial.
// PUT /api/dns/zones/{domain}/soa — requires manager role.
func handleBindSOAUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain, err := bindZoneDomain(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		var body bindSOAMutationRequest
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		req, err := body.soa()
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid SOA: "+err.Error())
			return
		}

		if err := bindService.UpdateSOA(domain, req); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"message": "SOA updated",
		})
	}
}

// handleBindRecordList returns all DNS records for the given zone.
// GET /api/dns/zones/{domain}/records
func handleBindRecordList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain, err := bindZoneDomain(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		records, err := bindService.ListRecords(domain)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, records)
	}
}

// handleBindRecordAdd appends a new DNS record to the zone file and bumps the serial.
// POST /api/dns/zones/{domain}/records — requires manager role.
func handleBindRecordAdd() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain, err := bindZoneDomain(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		var req bindsvc.AddRecordRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		req, err = bindsvc.ValidateAndNormalizeAddRecord(req)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid DNS record: "+err.Error())
			return
		}

		if err := bindService.AddRecord(domain, req); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, map[string]string{
			"message": "record added",
		})
	}
}

// handleBindRecordUpdate replaces a record matched by name+type+oldValue.
// PUT /api/dns/zones/{domain}/records — requires manager role.
func handleBindRecordUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain, err := bindZoneDomain(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		var req bindsvc.UpdateRecordRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		req, err = bindsvc.ValidateAndNormalizeUpdateRecord(req)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid DNS record: "+err.Error())
			return
		}

		if err := bindService.UpdateRecord(domain, req); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"message": "record updated",
		})
	}
}

// handleBindRecordDelete removes a record matched by name+type+value.
// DELETE /api/dns/zones/{domain}/records — requires manager role.
//
// Supports either an exact JSON body or the legacy exact query contract used
// by the web client. Mixing both sources is rejected.
func handleBindRecordDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain, err := bindZoneDomain(r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		var req bindsvc.DeleteRecordRequest
		hasBody := r.ContentLength != 0 || len(r.TransferEncoding) > 0
		if hasBody {
			if len(r.URL.Query()) != 0 {
				jsonError(w, http.StatusBadRequest, "record delete must use either JSON body or query parameters, not both")
				return
			}
			if err := decodeStrictJSON(r, &req); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
		} else {
			var parseErr error
			req, parseErr = bindDeleteRequestFromQuery(r)
			if parseErr != nil {
				jsonError(w, http.StatusBadRequest, parseErr.Error())
				return
			}
		}

		req, err = bindsvc.ValidateAndNormalizeDeleteRecord(req)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid DNS record: "+err.Error())
			return
		}

		if err := bindService.DeleteRecord(domain, req); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"message": "record deleted",
		})
	}
}

// handleBindReload runs `rndc reload` to apply zone changes at runtime.
// POST /api/dns/reload — requires manager role.
func handleBindReload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := bindService.Reload(); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"message": "BIND9 reloaded successfully",
		})
	}
}

// handleBindCheck runs named-checkconf -z and per-zone named-checkzone, returning all results.
// POST /api/dns/check
func handleBindCheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeBindCheckResult(w, bindService.Check())
	}
}

func writeBindCheckResult(w http.ResponseWriter, result bindsvc.CheckResult) {
	// A failed configuration check is a successful diagnostic operation. Keep
	// its complete structured output available to the panel and CLI.
	jsonResponse(w, http.StatusOK, result)
}

// handleBindStatus returns the current state of the named/BIND9 service.
// GET /api/dns/status
func handleBindStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, bindService.Status())
	}
}

// handleBindDNSLookup performs a live DNS lookup against multiple resolvers.
// GET /api/dns/lookup?domain=X&type=A
func handleBindDNSLookup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		for key := range query {
			if key != "domain" && key != "type" {
				jsonError(w, http.StatusBadRequest, "unsupported query parameter: "+key)
				return
			}
		}
		domainValue, err := exactBindQueryValue(query, "domain", true)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		domain, err := bindsvc.NormalizeLookupDomain(domainValue)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid domain query parameter: "+err.Error())
			return
		}
		typeValue, err := exactBindQueryValue(query, "type", false)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		qtype := "A"
		if typeValue != "" {
			qtype, err = bindsvc.NormalizeRecordType(typeValue)
			if err != nil {
				jsonError(w, http.StatusBadRequest, "invalid type query parameter: "+err.Error())
				return
			}
		}

		result := bindService.LookupDNS(domain, qtype)
		jsonResponse(w, http.StatusOK, result)
	}
}

func bindZoneDomain(r *http.Request) (string, error) {
	domain, err := bindsvc.NormalizeZoneDomain(r.PathValue("domain"))
	if err != nil {
		return "", fmt.Errorf("invalid zone domain: %w", err)
	}
	return domain, nil
}

func bindDeleteRequestFromQuery(r *http.Request) (bindsvc.DeleteRecordRequest, error) {
	query := r.URL.Query()
	for key := range query {
		if key != "name" && key != "type" && key != "value" && key != "autoReload" {
			return bindsvc.DeleteRecordRequest{}, fmt.Errorf("unsupported query parameter: %s", key)
		}
	}
	name, err := exactBindQueryValue(query, "name", true)
	if err != nil {
		return bindsvc.DeleteRecordRequest{}, err
	}
	recordType, err := exactBindQueryValue(query, "type", true)
	if err != nil {
		return bindsvc.DeleteRecordRequest{}, err
	}
	value, err := exactBindQueryValue(query, "value", true)
	if err != nil {
		return bindsvc.DeleteRecordRequest{}, err
	}
	autoReload := false
	if values, present := query["autoReload"]; present {
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			return bindsvc.DeleteRecordRequest{}, errors.New("autoReload query parameter must appear once and be true or false")
		}
		autoReload, _ = strconv.ParseBool(values[0])
	}
	return bindsvc.DeleteRecordRequest{Name: name, Type: recordType, Value: value, AutoReload: autoReload}, nil
}

func exactBindQueryValue(query map[string][]string, name string, required bool) (string, error) {
	values, present := query[name]
	if !present {
		if required {
			return "", fmt.Errorf("%s query parameter is required", name)
		}
		return "", nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", fmt.Errorf("%s query parameter must appear exactly once and not be empty", name)
	}
	return values[0], nil
}
