package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/services/settings"
	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
)

// ── Monitor CRUD ─────────────────────────────────────────────────────────────

func handleUptimeMonitorList(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		monitors, err := engine.Repo().ListMonitors()
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if monitors == nil {
			monitors = []store.UptimeMonitor{}
		}
		jsonResponse(w, http.StatusOK, monitors)
	}
}

func handleUptimeMonitorCreate(engine *uptime.Engine, settingsSvc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, uptimeMonitorRequestBodyLimit)
		var m store.UptimeMonitor
		if err := decodeStrictJSON(r, &m); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid uptime monitor request")
			return
		}
		applyUptimeMonitorDefaults(&m, settingsSvc)
		m.IsActive = true
		m.MaintenanceMode = false
		m.GroupID = nil
		if err := normalizeUptimeMonitor(&m); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := engine.Repo().CreateMonitor(&m); err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		engine.AddMonitor(r.Context(), &m)
		jsonResponse(w, http.StatusCreated, m)
	}
}

func handleUptimeMonitorGet(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		m, err := engine.Repo().GetMonitor(id)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if m == nil {
			jsonError(w, http.StatusNotFound, "monitor not found")
			return
		}
		// Enrich with stats
		stats, err := uptime.ComputeStats(engine.Repo(), id)
		if err == nil && stats != nil {
			m.Uptime24h = &stats.Uptime24h
			m.AvgPingMs = &stats.AvgPingMs
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"monitor": m,
			"stats":   stats,
		})
	}
}

func handleUptimeMonitorUpdate(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		existing, err := engine.Repo().GetMonitor(id)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if existing == nil {
			jsonError(w, http.StatusNotFound, "monitor not found")
			return
		}
		createdAt := existing.CreatedAt
		currentStatus := existing.CurrentStatus
		lastCheckAt := existing.LastCheckAt
		uptime24h := existing.Uptime24h
		avgPingMS := existing.AvgPingMs
		r.Body = http.MaxBytesReader(w, r.Body, uptimeMonitorRequestBodyLimit)
		if err := decodeStrictJSON(r, existing); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid uptime monitor request")
			return
		}
		existing.ID = id
		existing.CreatedAt = createdAt
		existing.CurrentStatus = currentStatus
		existing.LastCheckAt = lastCheckAt
		existing.Uptime24h = uptime24h
		existing.AvgPingMs = avgPingMS
		if err := normalizeUptimeMonitor(existing); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := engine.Repo().UpdateMonitor(existing); err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		engine.UpdateMonitor(r.Context(), existing)
		jsonResponse(w, http.StatusOK, existing)
	}
}

func handleUptimeMonitorDelete(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		existing, err := engine.Repo().GetMonitor(id)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if existing == nil {
			jsonError(w, http.StatusNotFound, "monitor not found")
			return
		}
		if err := engine.Repo().DeleteMonitor(id); err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		engine.RemoveMonitor(id)
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func handleUptimeMonitorPause(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		m, err := engine.Repo().GetMonitor(id)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if m == nil {
			jsonError(w, http.StatusNotFound, "monitor not found")
			return
		}
		if err := engine.Repo().SetMonitorActive(id, false); err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		engine.RemoveMonitor(id)
		jsonResponse(w, http.StatusOK, map[string]string{"status": "paused"})
	}
}

func handleUptimeMonitorResume(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		m, err := engine.Repo().GetMonitor(id)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if m == nil {
			jsonError(w, http.StatusNotFound, "monitor not found")
			return
		}
		if err := engine.Repo().SetMonitorActive(id, true); err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		m, err = engine.Repo().GetMonitor(id)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if m == nil {
			jsonError(w, http.StatusNotFound, "monitor not found")
			return
		}
		engine.AddMonitor(r.Context(), m)
		jsonResponse(w, http.StatusOK, map[string]string{"status": "resumed"})
	}
}

func handleUptimeMonitorCheckNow(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		allowLongSystemAction(w)
		result, err := engine.CheckNow(id)
		if err != nil {
			switch {
			case errors.Is(err, uptime.ErrMonitorNotFound):
				jsonError(w, http.StatusNotFound, err.Error())
			case errors.Is(err, uptime.ErrMonitorPaused):
				jsonError(w, http.StatusConflict, err.Error())
			default:
				slog.Error("uptime check-now failed", "monitor", id, "err", err)
				jsonError(w, http.StatusInternalServerError, "check now failed")
			}
			return
		}
		jsonResponse(w, http.StatusOK, newUptimeMonitorTestResponse(result))
	}
}

func handleUptimeMonitorTest(settingsSvc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, uptimeMonitorRequestBodyLimit)
		var m store.UptimeMonitor
		if err := decodeStrictJSON(r, &m); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid uptime monitor request")
			return
		}
		if strings.TrimSpace(m.Name) == "" {
			m.Name = "one-time uptime test"
		}
		applyUptimeMonitorDefaults(&m, settingsSvc)
		if err := normalizeUptimeMonitor(&m); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		result := uptime.TestCheck(&m)
		jsonResponse(w, http.StatusOK, newUptimeMonitorTestResponse(result))
	}
}

type uptimeMonitorTestResponse struct {
	OK         bool     `json:"ok"`
	Status     int      `json:"status"`
	Message    string   `json:"message,omitempty"`
	Error      string   `json:"error,omitempty"`
	PingMS     *float64 `json:"ping_ms,omitempty"`
	StatusCode *int     `json:"status_code,omitempty"`
	TLSExpiry  string   `json:"tls_expiry,omitempty"`
	CheckedAt  string   `json:"checked_at"`
}

func newUptimeMonitorTestResponse(result uptime.CheckResult) uptimeMonitorTestResponse {
	response := uptimeMonitorTestResponse{
		OK:        result.Status == uptime.StatusUp || result.Status == uptime.StatusTLSWarn,
		Status:    result.Status,
		Message:   result.Msg,
		TLSExpiry: result.TLSExpiry,
		CheckedAt: result.CheckedAt.UTC().Format(time.RFC3339Nano),
	}
	if result.PingMs > 0 {
		response.PingMS = &result.PingMs
	}
	if result.StatusCode > 0 {
		response.StatusCode = &result.StatusCode
	}
	if !response.OK {
		response.Error = result.Msg
	}
	return response
}

// ── Domain Integration ───────────────────────────────────────────────────────

// handleUptimeBulkFromDomains creates HTTP monitors for all domains that don't
// already have a matching monitor.
// POST /api/uptime/monitors/bulk-from-domains
func handleUptimeBulkFromDomains(cfg *config.Config, engine *uptime.Engine, settingsSvc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}

		domains, err := localDomainService(cfg).List()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to list domains")
			return
		}

		existing, err := engine.Repo().ListMonitors()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to list monitors")
			return
		}

		// Build a set of URLs already monitored.
		monitored := make(map[string]struct{}, len(existing))
		for _, m := range existing {
			monitored[m.URL] = struct{}{}
		}

		var created, skipped int
		var errors []string

		for _, d := range domains {
			targetURL := "https://" + d.Name
			if _, exists := monitored[targetURL]; exists {
				skipped++
				continue
			}
			monitor := &store.UptimeMonitor{
				Name:                d.Name,
				Type:                "http",
				URL:                 targetURL,
				Method:              "GET",
				TimeoutSecs:         10,
				Retries:             1,
				RetryInterval:       30,
				AcceptedStatusCodes: `["200-299"]`,
				MaxRedirects:        5,
				IsActive:            true,
				TLSCheck:            true,
				TLSExpiryWarnDays:   14,
			}
			applyUptimeMonitorDefaults(monitor, settingsSvc)
			if err := normalizeUptimeMonitor(monitor); err != nil {
				errors = append(errors, d.Name+": "+err.Error())
				continue
			}
			if err := engine.Repo().CreateMonitor(monitor); err != nil {
				errors = append(errors, d.Name+": "+err.Error())
				continue
			}
			engine.AddMonitor(r.Context(), monitor)
			monitored[targetURL] = struct{}{}
			created++
		}

		jsonResponse(w, http.StatusOK, map[string]any{
			"created": created,
			"skipped": skipped,
			"errors":  errors,
		})
	}
}

// handleUptimeMonitorByDomain searches for a monitor whose URL or hostname
// contains the given domain name.
// GET /api/uptime/monitors/by-domain/{domain}
func handleUptimeMonitorByDomain(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		domain := r.PathValue("domain")
		if domain == "" {
			jsonError(w, http.StatusBadRequest, "domain is required")
			return
		}

		monitors, err := engine.Repo().ListMonitors()
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		// Exact match first: URL ends with "://domain" or "://domain/"
		// Then fallback to hostname exact match, then contains
		var bestMatch *store.UptimeMonitor
		for i, m := range monitors {
			urlHost := strings.TrimPrefix(strings.TrimPrefix(m.URL, "https://"), "http://")
			urlHost = strings.SplitN(urlHost, "/", 2)[0] // remove path
			if urlHost == domain || m.Hostname == domain || m.Name == domain {
				bestMatch = &monitors[i]
				break
			}
		}
		if bestMatch == nil {
			// Fallback: contains match
			for i, m := range monitors {
				if strings.Contains(m.URL, domain) || strings.Contains(m.Hostname, domain) {
					bestMatch = &monitors[i]
					break
				}
			}
		}
		if bestMatch != nil {
			stats, _ := uptime.ComputeStats(engine.Repo(), bestMatch.ID)
			jsonResponse(w, http.StatusOK, map[string]any{
				"monitor": *bestMatch,
				"stats":   stats,
			})
			return
		}

		jsonError(w, http.StatusNotFound, "no monitor found for domain: "+domain)
	}
}
