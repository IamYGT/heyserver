package api

import (
	"errors"
	"net/http"
	"strings"

	cronsvc "github.com/IamYGT/heyserver/internal/services/cron"
)

// handleCronStatus reports whether both the crontab client and cron daemon are
// available before the UI enables scheduled-task mutations.
// GET /api/cron/status
func handleCronStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, cronsvc.GetStatus(r.Context()))
	}
}

// handleCronList returns all user cron jobs across all system users.
// GET /api/cron/jobs
func handleCronList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobs, err := cronsvc.ListAllJobs()
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, cronsvc.ErrCrontabUnavailable) {
				status = http.StatusServiceUnavailable
			}
			jsonError(w, status, err.Error())
			return
		}
		if jobs == nil {
			jobs = []cronsvc.Job{}
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"jobs":  jobs,
			"total": len(jobs),
		})
	}
}

// handleCronCreate adds a new cron job for the specified user.
// POST /api/cron/jobs
// Body: {"user":"root","schedule":"0 3 * * *","command":"/usr/bin/backup.sh","description":"Nightly backup"}
func handleCronCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req cronsvc.CreateRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.User == "" {
			req.User = "root"
		}
		if strings.TrimSpace(req.Schedule) == "" || strings.TrimSpace(req.Command) == "" {
			jsonError(w, http.StatusBadRequest, "schedule and command are required")
			return
		}
		job, err := cronsvc.AddJob(req)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, cronsvc.ErrCrontabUnavailable) {
				status = http.StatusServiceUnavailable
			}
			jsonError(w, status, err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, map[string]any{"job": job, "message": "cron job created"})
	}
}

// handleCronUpdate replaces an existing cron job identified by id.
// PUT /api/cron/jobs/{id}
// Query param: user (defaults to "root")
// Body: {"schedule":"0 4 * * *","command":"/usr/bin/backup.sh","description":"","isActive":true}
func handleCronUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "job id is required")
			return
		}
		user := r.URL.Query().Get("user")
		if user == "" {
			user = "root"
		}
		var body struct {
			Schedule    string  `json:"schedule"`
			Command     string  `json:"command"`
			Description *string `json:"description"`
			IsActive    *bool   `json:"isActive"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if strings.TrimSpace(body.Schedule) == "" || strings.TrimSpace(body.Command) == "" {
			jsonError(w, http.StatusBadRequest, "schedule and command are required")
			return
		}
		if body.Description == nil || body.IsActive == nil {
			jsonError(w, http.StatusBadRequest, "description and isActive are required")
			return
		}
		req := cronsvc.UpdateRequest{
			Schedule:    body.Schedule,
			Command:     body.Command,
			Description: *body.Description,
			IsActive:    *body.IsActive,
		}
		job, err := cronsvc.EditJob(id, user, req)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, cronsvc.ErrCrontabUnavailable) {
				status = http.StatusServiceUnavailable
			} else if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			jsonError(w, status, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"job": job, "message": "cron job updated"})
	}
}

// handleCronDelete removes a cron job by id.
// DELETE /api/cron/jobs/{id}
// Query param: user (defaults to "root")
func handleCronDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "job id is required")
			return
		}
		user := r.URL.Query().Get("user")
		if user == "" {
			user = "root"
		}
		if err := cronsvc.DeleteJob(id, user); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, cronsvc.ErrCrontabUnavailable) {
				status = http.StatusServiceUnavailable
			} else if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			jsonError(w, status, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"id": id, "message": "cron job deleted"})
	}
}

// handleCronSystemList lists files from /etc/cron.d/, /etc/cron.daily/, etc.
// GET /api/cron/system
func handleCronSystemList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		files, err := cronsvc.ListSystemFiles()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if files == nil {
			files = []cronsvc.SystemFile{}
		}
		jsonResponse(w, http.StatusOK, map[string]any{"files": files, "total": len(files)})
	}
}
