package api

import (
	"log/slog"
	"net/http"

	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
)

// ── Public Endpoints (no auth) ───────────────────────────────────────────────

// handlePublicStatusPage renders the HTML status page for a given slug.
func handlePublicStatusPage(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			http.Error(w, "uptime engine not initialized", http.StatusServiceUnavailable)
			return
		}
		slug := r.PathValue("slug")
		if slug == "" {
			http.NotFound(w, r)
			return
		}
		sp, err := engine.Repo().GetStatusPage(slug)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if sp == nil || !sp.IsPublic {
			http.NotFound(w, r)
			return
		}
		data, err := buildStatusPageData(engine, sp)
		if err != nil {
			http.Error(w, "failed to build status page", http.StatusInternalServerError)
			return
		}
		renderStatusPage(w, data)
	}
}

// handlePublicStatusAPI returns JSON data (monitors + stats) for a status page slug.
func handlePublicStatusAPI(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		slug := r.PathValue("slug")
		if slug == "" {
			jsonError(w, http.StatusBadRequest, "slug required")
			return
		}
		sp, err := engine.Repo().GetStatusPage(slug)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if sp == nil || !sp.IsPublic {
			http.NotFound(w, r)
			return
		}

		type monitorStatus struct {
			Monitor store.UptimeMonitor `json:"monitor"`
			Stats   *uptime.UptimeStats `json:"stats"`
		}

		var monitors []monitorStatus
		for _, entry := range sp.Monitors {
			m, err := engine.Repo().GetMonitor(entry.MonitorID)
			if err != nil || m == nil {
				continue
			}
			stats, _ := uptime.ComputeStats(engine.Repo(), entry.MonitorID)
			monitors = append(monitors, monitorStatus{Monitor: *m, Stats: stats})
		}
		if monitors == nil {
			monitors = []monitorStatus{}
		}

		summary, _ := engine.Repo().Summary()

		jsonResponse(w, http.StatusOK, map[string]any{
			"page":     sp,
			"monitors": monitors,
			"summary":  summary,
		})
	}
}
