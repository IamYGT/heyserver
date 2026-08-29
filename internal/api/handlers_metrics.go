package api

import (
	"net/http"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

// Supported time ranges for metrics queries.
var metricsRanges = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

// handleMetricsHistory returns system metrics for the given time range.
// GET /api/metrics/history?range=1h
func handleMetricsHistory(repo *store.MetricsRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rangeParam := r.URL.Query().Get("range")
		if rangeParam == "" {
			rangeParam = "1h"
		}

		duration, ok := metricsRanges[rangeParam]
		if !ok {
			jsonError(w, http.StatusBadRequest, "invalid range: use 1h, 6h, 24h, 7d, or 30d")
			return
		}

		raw, agg, err := repo.QueryHistory(duration)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to query metrics: "+err.Error())
			return
		}

		// Return a unified response that includes the resolution info.
		resp := map[string]interface{}{
			"range": rangeParam,
		}

		if raw != nil {
			resp["resolution"] = "raw"
			resp["data"] = raw
		} else {
			if duration > 24*time.Hour {
				resp["resolution"] = "hourly"
			} else {
				resp["resolution"] = "minute"
			}
			resp["data"] = agg
		}

		jsonResponse(w, http.StatusOK, resp)
	}
}

// handleMetricsProcesses returns the process snapshot closest to a given timestamp.
// GET /api/metrics/processes?at=2026-04-11T14:30:00Z
// GET /api/metrics/processes (returns latest snapshot)
func handleMetricsProcesses(repo *store.MetricsRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		at := r.URL.Query().Get("at")
		if at == "" {
			at = time.Now().UTC().Format(time.RFC3339)
		}

		procs, err := repo.QueryProcessSnapshot(at)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to query processes: "+err.Error())
			return
		}
		if procs == nil {
			procs = []store.ProcessSnapshotRow{}
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"requested_at": at,
			"processes":    procs,
		})
	}
}

// handleMetricsProcessTimestamps returns available process snapshot timestamps.
// GET /api/metrics/processes/timestamps?range=1h
func handleMetricsProcessTimestamps(repo *store.MetricsRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rangeParam := r.URL.Query().Get("range")
		if rangeParam == "" {
			rangeParam = "1h"
		}

		duration, ok := metricsRanges[rangeParam]
		if !ok {
			jsonError(w, http.StatusBadRequest, "invalid range: use 1h, 6h, 24h, 7d, or 30d")
			return
		}

		timestamps, err := repo.QueryProcessTimestamps(duration)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to query process timestamps: "+err.Error())
			return
		}
		if timestamps == nil {
			timestamps = []string{}
		}

		jsonResponse(w, http.StatusOK, timestamps)
	}
}

// handleMetricsServiceHistory returns service status history for the given range.
// GET /api/metrics/services/history?range=24h
func handleMetricsServiceHistory(repo *store.MetricsRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rangeParam := r.URL.Query().Get("range")
		if rangeParam == "" {
			rangeParam = "24h"
		}

		duration, ok := metricsRanges[rangeParam]
		if !ok {
			jsonError(w, http.StatusBadRequest, "invalid range: use 1h, 6h, 24h, 7d, or 30d")
			return
		}

		history, err := repo.QueryServiceHistory(duration)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to query service history: "+err.Error())
			return
		}
		if history == nil {
			history = []store.ServiceStatusRow{}
		}

		jsonResponse(w, http.StatusOK, history)
	}
}

// handleMetricsSummary returns a quick overview of the metrics database.
// GET /api/metrics/summary
func handleMetricsSummary(repo *store.MetricsRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		summary, err := repo.Summary()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to query summary: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, summary)
	}
}
