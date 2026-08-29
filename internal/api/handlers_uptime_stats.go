package api

import (
	"log/slog"
	"net/http"
	"strconv"

	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
)

// ── Summary & Stats ──────────────────────────────────────────────────────────

func handleUptimeSummary(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		summary, err := engine.Repo().Summary()
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		jsonResponse(w, http.StatusOK, summary)
	}
}

func handleUptimeHeartbeats(engine *uptime.Engine) http.HandlerFunc {
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
		hours := parsePeriodHours(r.URL.Query().Get("hours"), 24)
		heartbeats, err := engine.Repo().ListHeartbeats(id, hours)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if heartbeats == nil {
			heartbeats = []store.UptimeHeartbeat{}
		}
		jsonResponse(w, http.StatusOK, heartbeats)
	}
}

func handleUptimeUptime(engine *uptime.Engine) http.HandlerFunc {
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
		stats, err := uptime.ComputeStats(engine.Repo(), id)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		jsonResponse(w, http.StatusOK, stats)
	}
}

func handleUptimeIncidents(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		// Monitor ID from path param (/{id}/incidents) or query param (?monitor_id=)
		var monitorID int64
		if idStr := r.PathValue("id"); idStr != "" {
			monitorID, _ = strconv.ParseInt(idStr, 10, 64)
		} else if idStr := r.URL.Query().Get("monitor_id"); idStr != "" {
			monitorID, _ = strconv.ParseInt(idStr, 10, 64)
		}
		limit := 50
		if monitorID == 0 {
			// The global incident screen needs enough room for the complete active
			// set plus useful recent history. Active rows are ordered first by the
			// repository, so this remains deterministic as history grows.
			limit = 200
		}
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if l, e := strconv.Atoi(lStr); e == nil && l > 0 {
				limit = min(l, 1000)
			}
		}
		incidents, err := engine.Repo().ListIncidents(monitorID, limit)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if incidents == nil {
			incidents = []store.UptimeIncident{}
		}
		jsonResponse(w, http.StatusOK, incidents)
	}
}
