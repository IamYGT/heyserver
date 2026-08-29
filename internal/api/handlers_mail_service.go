package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/IamYGT/heyserver/internal/config"
	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

// handleMailServiceStatus returns the detailed stalwart-mail systemd status
// (running/stopped/failed, PID, uptime) obtained via systemctl show.
func handleMailServiceStatus(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		status := svc.GetStatus()
		jsonResponse(w, http.StatusOK, status)
	}
}

// handleMailServiceAction executes start / stop / restart on stalwart-mail.
// The action is provided as a path parameter: POST /api/mail/service/{action}
// Requires RoleAdmin (enforced in the router).
func handleMailServiceAction(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		action := r.PathValue("action")
		svc := newMailService(cfg)

		var err error
		switch action {
		case "start":
			err = svc.StartService()
		case "stop":
			err = svc.StopService()
		case "restart":
			err = svc.RestartService()
		default:
			jsonError(w, http.StatusBadRequest, "unknown action: "+action+" (allowed: start, stop, restart)")
			return
		}

		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		// Return the fresh service status after the action
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"action": action,
			"status": svc.GetStatus(),
		})
	}
}

// handleMailConfig returns sections parsed from the configured mail runtime.
func handleMailConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		sections, err := svc.GetConfig()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, sections)
	}
}

// handleMailVersion returns the stalwart-mail binary version.
func handleMailVersion(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		ver, err := svc.GetVersion()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, ver)
	}
}

// handleMailListeners returns the active listener definitions from config.toml.
func handleMailListeners(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		listeners, err := svc.GetListenerInfo()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, listeners)
	}
}

// handleMailStorage returns storage backend info (type, path, disk usage).
func handleMailStorage(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)
		info, err := svc.GetStorageInfo()
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, info)
	}
}

// handleMailLogs returns the last N journal log entries for stalwart-mail.
// Query param: ?lines=100 (default 100, max 5000).
func handleMailLogs(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lines := 100
		if v := r.URL.Query().Get("lines"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				if n > 5000 {
					n = 5000
				}
				lines = n
			}
		}
		svc := newMailService(cfg)
		entries, err := svc.GetMailLogs(lines)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"lines":   lines,
			"count":   len(entries),
			"entries": entries,
		})
	}
}

// handleMailLogsSearch searches stalwart-mail journal for the given query string.
// Query param: ?q=<search_term>
func handleMailLogsSearch(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			jsonError(w, http.StatusBadRequest, "q query parameter is required")
			return
		}
		svc := newMailService(cfg)
		entries, err := svc.SearchMailLogs(query)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"query":   query,
			"count":   len(entries),
			"entries": entries,
		})
	}
}

// handleMailDeliveryLog searches logs for a specific email address.
// Query param: ?email=user@domain
func handleMailDeliveryLog(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := r.URL.Query().Get("email")
		if email == "" {
			jsonError(w, http.StatusBadRequest, "email query parameter is required")
			return
		}
		svc := newMailService(cfg)
		entries, err := svc.GetDeliveryLog(email)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"email":   email,
			"count":   len(entries),
			"entries": entries,
		})
	}
}

// handleMailServiceOverview bundles status + version + listeners + storage
// into a single payload — useful for the dashboard widget.
func handleMailServiceOverview(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := newMailService(cfg)

		status := svc.GetStatus()

		ver, versionErr := svc.GetVersion()
		listeners, listenersErr := svc.GetListenerInfo()
		storage, storageErr := svc.GetStorageInfo()

		statusSource := map[string]interface{}{
			"available": status.Status != "unknown" && status.Status != "not_configured",
			"state":     status.Status,
		}
		switch status.Status {
		case "not_configured":
			statusSource["error"] = "mail service is not configured"
		case "unknown":
			statusSource["error"] = "mail service status is unknown"
		}
		source := func(err error) map[string]interface{} {
			result := map[string]interface{}{"available": err == nil}
			if err != nil {
				if errors.Is(err, mailsvc.ErrNotConfigured) {
					result["state"] = "not_configured"
				} else {
					result["state"] = "unavailable"
				}
				result["error"] = err.Error()
			} else {
				result["state"] = "healthy"
			}
			return result
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"status":    status,
			"version":   ver,
			"listeners": listeners,
			"storage":   storage,
			"sources": map[string]interface{}{
				"status":    statusSource,
				"version":   source(versionErr),
				"listeners": source(listenersErr),
				"storage":   source(storageErr),
			},
		})
	}
}
