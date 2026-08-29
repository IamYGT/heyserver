package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/services/pm2"
)

const pm2IntegrationStateHeader = "X-HServer-Integration-State"

func writePM2Error(w http.ResponseWriter, status int, state integrationstate.State, message string) {
	if !state.IsValid() {
		state = integrationstate.Unavailable
	}
	w.Header().Set(pm2IntegrationStateHeader, string(state))
	jsonResponse(w, status, map[string]string{
		"error": message,
		"state": string(state),
	})
}

func writePM2Availability(w http.ResponseWriter, state integrationstate.State) {
	w.Header().Set(pm2IntegrationStateHeader, string(state))
}

func pm2Service(cfg *config.Config, w http.ResponseWriter) *pm2.Service {
	service, err := pm2.New(pm2.Config{
		User:         cfg.PM2User,
		Home:         cfg.PM2Home,
		Bin:          cfg.PM2Bin,
		AllowedRoots: cfg.PM2AllowedRoots,
	})
	if err != nil {
		writePM2Error(w, http.StatusServiceUnavailable, pm2.ClassifyInventoryError(err), err.Error())
		return nil
	}
	return service
}

// handlePM2List returns all PM2 managed processes with stats.
func handlePM2List(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := pm2Service(cfg, w)
		if service == nil {
			return
		}
		inventory, err := service.ProbeProcesses()
		if err != nil {
			status := http.StatusInternalServerError
			if inventory.State == integrationstate.NotConfigured {
				status = http.StatusServiceUnavailable
			}
			writePM2Error(w, status, inventory.State, "failed to list pm2 processes: "+err.Error())
			return
		}
		// Preserve the established array payload for existing consumers. The
		// canonical availability result is additive in the response header; the
		// frontend adapter also accepts a state envelope during a rolling upgrade.
		writePM2Availability(w, inventory.State)
		jsonResponse(w, http.StatusOK, inventory.Processes)
	}
}

// handlePM2Get returns details for a single process by id or name.
func handlePM2Get(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := pm2Service(cfg, w)
		if service == nil {
			return
		}
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "process id is required")
			return
		}

		process, err := service.Get(id)
		if err != nil {
			if errors.Is(err, pm2.ErrNotConfigured) {
				writePM2Error(w, http.StatusServiceUnavailable, integrationstate.NotConfigured, err.Error())
				return
			}
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		writePM2Availability(w, integrationstate.Healthy)
		jsonResponse(w, http.StatusOK, process)
	}
}

// handlePM2Control performs lifecycle actions: start, stop, restart, delete, reload.
// Route: POST /api/pm2/processes/{id}/{action}
func handlePM2Control(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		action := r.PathValue("action")

		if id == "" || action == "" {
			jsonError(w, http.StatusBadRequest, "id and action are required")
			return
		}
		if !pm2.IsControlAction(action) {
			jsonError(w, http.StatusBadRequest, "unsupported PM2 action")
			return
		}

		service := pm2Service(cfg, w)
		if service == nil {
			return
		}

		output, err := service.Control(id, action)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]string{
			"status": action + "ed",
			"output": output,
		})
	}
}

// handlePM2Logs returns the last N log lines for a process.
// Query param: ?lines=100 (default 100)
func handlePM2Logs(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "process id is required")
			return
		}

		lines := 100
		if linesParam := r.URL.Query().Get("lines"); linesParam != "" {
			n, err := strconv.Atoi(linesParam)
			if err != nil || n < 1 || n > 5000 {
				jsonError(w, http.StatusBadRequest, "lines must be between 1 and 5000")
				return
			}
			lines = n
		}

		service := pm2Service(cfg, w)
		if service == nil {
			return
		}

		output, err := service.Logs(id, lines)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"id":     id,
			"lines":  lines,
			"output": output,
		})
	}
}

// handlePM2Deploy starts a new application via PM2.
// Body: { "name": "myapp", "script": "/path/to/app.js", "cwd": "/path", "instances": 1, "exec_mode": "fork", "node_env": "production" }
func handlePM2Deploy(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req pm2.DeployRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}

		if req.Name == "" || req.Script == "" {
			jsonError(w, http.StatusBadRequest, "name and script are required")
			return
		}

		service := pm2Service(cfg, w)
		if service == nil {
			return
		}

		output, err := service.Deploy(&req)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, pm2.ErrInvalidDeploy) {
				status = http.StatusBadRequest
			} else if errors.Is(err, pm2.ErrNotConfigured) {
				status = http.StatusServiceUnavailable
			}
			jsonError(w, status, err.Error())
			return
		}

		jsonResponse(w, http.StatusCreated, map[string]string{
			"status": "deployed",
			"name":   req.Name,
			"output": output,
		})
	}
}

// handlePM2Save persists the current PM2 process list (pm2 save).
func handlePM2Save(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := pm2Service(cfg, w)
		if service == nil {
			return
		}
		output, err := service.Save()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"status": "saved",
			"output": output,
		})
	}
}
