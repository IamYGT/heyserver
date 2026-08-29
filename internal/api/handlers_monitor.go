package api

import (
	"net/http"
	"time"

	"github.com/IamYGT/heyserver/internal/services/monitor"
)

// Global monitor cache — shared across all requests.
// TTL = 2 seconds; safe for concurrent polling from the React dashboard.
var monitorCache = monitor.New(2 * time.Second)

// MonitorCache returns the shared monitor cache instance so that other
// packages (e.g. the metrics collector in main.go) can read from it.
func MonitorCache() *monitor.Cache {
	return monitorCache
}

// handleSystemStats returns a full SystemStats snapshot.
// GET /api/system/stats
func handleSystemStats() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := monitorCache.Stats()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to collect system stats: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, stats)
	}
}

// handleServiceStatus returns the status of all watched system services.
// GET /api/system/services
func handleServiceStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		statuses, err := monitor.ServiceStatuses()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to collect service statuses: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, statuses)
	}
}

// handleMonitoringStats is the polling endpoint for the real-time dashboard.
// Same data as handleSystemStats but intended for frequent (2–5s) polling.
// GET /api/monitoring/stats
func handleMonitoringStats() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := monitorCache.Stats()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to collect monitoring stats: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, stats)
	}
}

// handleMonitoringProcesses returns the top 15 processes sorted by CPU.
// GET /api/monitoring/processes
func handleMonitoringProcesses() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		procs, err := monitorCache.Processes()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to collect process list: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, procs)
	}
}
