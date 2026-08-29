package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/services/backup"
)

var backupSvc = backup.New()

// handleBackupList returns all backup files with metadata.
// GET /api/backups
func handleBackupList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backups, err := backupSvc.List()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		storage, storageErr := backupSvc.Storage()
		if storageErr != nil {
			jsonError(w, http.StatusInternalServerError, storageErr.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"backups": backups, "storage": storage})
	}
}

// handleBackupTargets returns server-observed portable vhost identities for
// selective file-bearing backups without exposing the configured root path.
// GET /api/backups/targets
func handleBackupTargets() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vhosts, err := backupSvc.ListVhostTargets()
		if err != nil {
			jsonError(w, http.StatusServiceUnavailable, "backup target discovery is unavailable")
			return
		}
		if vhosts == nil {
			vhosts = []string{}
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"vhosts":            vhosts,
			"maxSelectedVhosts": 16,
			"emptySelection":    "all-configured-vhosts",
		})
	}
}

// handleBackupCreate starts a backup in the background and returns a job ID immediately.
// POST /api/backups
// Body JSON: {"type":"full"|"database"|"files", "name":"...", "engine":"postgresql"|"mariadb",
//
//	"database":"...", "compression":6, "retention":10, "vhosts":["example.com"]}
func handleBackupCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Type             string   `json:"type"`
			Name             string   `json:"name"`
			Engine           string   `json:"engine"`
			Database         string   `json:"database"`
			CompressionLevel int      `json:"compression"`
			RetentionCount   int      `json:"retention"`
			Vhosts           []string `json:"vhosts"`
		}
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		bType := strings.ToLower(req.Type)
		if bType == "" {
			bType = "full"
		}
		if bType != "full" && bType != "database" && bType != "files" {
			jsonError(w, http.StatusBadRequest, "backup type must be full, database, or files")
			return
		}
		engine := strings.ToLower(req.Engine)
		if engine == "" {
			engine = "postgresql"
		}
		if engine != "postgresql" && engine != "mariadb" {
			jsonError(w, http.StatusBadRequest, "database engine must be postgresql or mariadb")
			return
		}
		if backupSvc.HasActiveJobTypes("full", "database", "files") {
			jsonError(w, http.StatusConflict, "another local backup is already running")
			return
		}

		opts := backup.CreateOptions{
			Type:             bType,
			Name:             req.Name,
			Engine:           engine,
			Database:         req.Database,
			CompressionLevel: req.CompressionLevel,
			RetentionCount:   req.RetentionCount,
			Vhosts:           req.Vhosts,
			Source:           "manual",
		}
		if err := backupSvc.ValidateCreateOptions(opts); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		job := backupSvc.CreateAsync(opts)

		jsonResponse(w, http.StatusAccepted, map[string]any{
			"jobId":   job.ID,
			"status":  backup.JobPending,
			"message": "backup started in background",
		})
	}
}

// handleBackupDelete deletes a backup file by ID.
// DELETE /api/backups/{id}
func handleBackupDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "backup id is required")
			return
		}

		if err := backupSvc.Delete(id); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
	}
}

// handleBackupRestore starts a restore in the background and returns a job ID.
// POST /api/backups/restore/{id}
func handleBackupRestore() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "backup id is required")
			return
		}

		job := backupSvc.RestoreAsync(id)

		jsonResponse(w, http.StatusAccepted, map[string]any{
			"jobId":   job.ID,
			"status":  backup.JobPending,
			"message": "restore started in background",
		})
	}
}

// handleBackupRestoreValidate performs an artifact-only restore preflight.
// GET /api/backups/restore/{id}/validate
func handleBackupRestoreValidate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "backup id is required")
			return
		}
		result, err := backupSvc.ValidateRestore(id)
		if err != nil {
			jsonError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}

// handleBackupScheduleList returns active cron schedule entries.
// GET /api/backups/schedules
// Supports UI format: { frequency, time, retention_count } when a single entry exists.
func handleBackupScheduleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		schedule, err := backupSvc.GetSchedule()
		if err != nil {
			writeBackupScheduleError(w, err)
			return
		}
		if len(schedule) == 0 {
			jsonResponse(w, http.StatusOK, map[string]any{"schedules": []any{}})
			return
		}
		items := make([]map[string]any, 0, len(schedule))
		for _, entry := range schedule {
			freq, tm, convErr := backup.CronToFrequency(entry.Cron)
			item := map[string]any{
				"retention_count": entry.RetentionCount,
				"retention_days":  entry.RetentionCount, // deprecated compatibility alias
				"cron":            entry.Cron,
				"type":            entry.Type,
				"database":        entry.Database,
				"rawLine":         entry.RawLine,
			}
			if convErr == nil {
				item["frequency"] = freq
				item["time"] = tm
			}
			items = append(items, item)
		}
		jsonResponse(w, http.StatusOK, map[string]any{"schedules": items})
	}
}

// handleBackupScheduleSet adds or replaces a backup cron entry.
// POST /api/backups/schedules
// Accepts either cron format or UI format: { frequency, time, retention_count }.
func handleBackupScheduleSet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req backupScheduleSetRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		opts, err := req.scheduleOptions()
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := backupSvc.SetSchedule(opts); err != nil {
			writeBackupScheduleError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// handleBackupScheduleDelete removes a cron line by rawLine.
// DELETE /api/backups/schedules
// Body JSON: {"rawLine": "..."}
func handleBackupScheduleDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req backupScheduleDeleteRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		rawLine := strings.TrimSpace(req.RawLine)
		if rawLine == "" {
			jsonError(w, http.StatusBadRequest, "rawLine is required")
			return
		}
		if err := backupSvc.DeleteSchedule(rawLine); err != nil {
			writeBackupScheduleError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
	}
}

type backupScheduleSetRequest struct {
	Cron                 string `json:"cron"`
	Frequency            string `json:"frequency"`
	Time                 string `json:"time"`
	Type                 string `json:"type"`
	Database             string `json:"database"`
	RetentionCount       *int   `json:"retention_count"`
	RetentionCountLegacy *int   `json:"retentionCount"`
	RetentionDaysLegacy  *int   `json:"retention_days"`
}

func (req backupScheduleSetRequest) scheduleOptions() (backup.ScheduleOptions, error) {
	opts := backup.ScheduleOptions{
		Type:           strings.ToLower(strings.TrimSpace(req.Type)),
		Database:       strings.TrimSpace(req.Database),
		RetentionCount: 10,
	}
	if opts.Type == "" {
		opts.Type = "full"
	}

	cron := strings.TrimSpace(req.Cron)
	frequency := strings.TrimSpace(req.Frequency)
	timeHHMM := strings.TrimSpace(req.Time)
	if cron != "" && (frequency != "" || timeHHMM != "") {
		return backup.ScheduleOptions{}, fmt.Errorf("use either cron or frequency/time, not both")
	}
	if cron == "" {
		if frequency == "" || timeHHMM == "" {
			return backup.ScheduleOptions{}, fmt.Errorf("cron expression or frequency/time required")
		}
		converted, err := backup.FrequencyToCron(frequency, timeHHMM)
		if err != nil {
			return backup.ScheduleOptions{}, err
		}
		cron = converted
	}
	opts.Cron = cron

	retentionAliases := 0
	for _, value := range []*int{req.RetentionCount, req.RetentionCountLegacy, req.RetentionDaysLegacy} {
		if value == nil {
			continue
		}
		retentionAliases++
		opts.RetentionCount = *value
	}
	if retentionAliases > 1 {
		return backup.ScheduleOptions{}, fmt.Errorf("provide only retention_count")
	}

	return opts, nil
}

type backupScheduleDeleteRequest struct {
	RawLine string `json:"rawLine"`
}

func writeBackupScheduleError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, backup.ErrCrontabUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, backup.ErrInvalidScheduleTarget):
		status = http.StatusBadRequest
	case errors.Is(err, backup.ErrInvalidScheduleOptions):
		status = http.StatusBadRequest
	case errors.Is(err, backup.ErrScheduleNotFound):
		status = http.StatusNotFound
	}
	jsonError(w, status, err.Error())
}

// handleBackupJobList returns recent and active backup jobs (default: last 24h).
// GET /api/backups/jobs
func handleBackupJobList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		since := time.Now().Add(-24 * time.Hour)
		if q := r.URL.Query().Get("hours"); q != "" {
			var hours float64
			if _, err := fmt.Sscanf(q, "%f", &hours); err == nil && hours > 0 && hours <= 168 {
				since = time.Now().Add(-time.Duration(hours * float64(time.Hour)))
			}
		}
		activeOnly := r.URL.Query().Get("active") == "1"
		var jobs []backup.Job
		if activeOnly {
			jobs = backupSvc.ListActiveJobs()
		} else {
			jobs = backupSvc.ListJobs(since)
		}
		payload := make([]map[string]any, 0, len(jobs))
		for i := range jobs {
			payload = append(payload, normalizeJobResponse(&jobs[i]))
		}
		jsonResponse(w, http.StatusOK, map[string]any{"jobs": payload})
	}
}

// handleBackupJobStream pushes job updates via Server-Sent Events.
// GET /api/backups/jobs/stream
func handleBackupJobStream(shutdownCtx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamCtx, cancel := requestStreamContext(r.Context(), shutdownCtx)
		defer cancel()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Time{})

		flusher, ok := w.(http.Flusher)
		if !ok {
			_, _ = fmt.Fprint(w, "data: {\"error\":\"streaming unsupported\"}\n\n")
			return
		}

		// Initial snapshot
		for _, j := range backupSvc.ListJobs(time.Now().Add(-24 * time.Hour)) {
			raw, _ := json.Marshal(normalizeJobResponse(&j))
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		flusher.Flush()

		ch, unsub := backupSvc.SubscribeJobs()
		defer unsub()

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case j, open := <-ch:
				if !open {
					return
				}
				raw, err := json.Marshal(normalizeJobResponse(&j))
				if err != nil {
					continue
				}
				_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
				flusher.Flush()
			}
		}
	}
}

// handleBackupJobDismiss marks a stale active job as failed (admin dismiss).
// POST /api/backups/jobs/{id}/dismiss
func handleBackupJobDismiss() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		jobID := r.PathValue("id")
		if jobID == "" {
			jsonError(w, http.StatusBadRequest, "job id required")
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		job := backupSvc.GetJob(jobID)
		if err := backupSvc.DismissJob(jobID, body.Reason); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if job != nil && snapshotSvc != nil && (job.Type == "snapshot" || job.Type == "snapshot_restore") {
			snapshotSvc.AbortRunning()
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// handleBackupPurgeInvalid removes all invalid local backup files.
// POST /api/backups/purge-invalid
func handleBackupPurgeInvalid() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, freed, err := backupSvc.PurgeInvalid()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"removed":    n,
			"bytesFreed": freed,
			"success":    true,
		})
	}
}

// handleBackupPurgeOrphaned removes only explicitly selected staging artifacts
// left by interrupted full-backup runs. The confirmation phrase prevents an
// accidental or stale client request from deleting files.
// POST /api/backups/purge-orphaned
func handleBackupPurgeOrphaned() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs     []string `json:"ids"`
			Confirm string   `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.Confirm != "DELETE_ORPHANED_PARTIALS" {
			jsonError(w, http.StatusBadRequest, "explicit orphaned-partial cleanup confirmation is required")
			return
		}

		rootBefore := rootAvailableBytes()
		removed, freed, err := backupSvc.PurgeOrphaned(body.IDs)
		if err != nil {
			auditHostAction(r, "backup_partial_cleanup", "failed: "+err.Error())
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		rootAfter := rootAvailableBytes()
		auditHostAction(r, "backup_partial_cleanup", fmt.Sprintf("removed %d artifacts; reclaimed %d bytes", removed, freed))
		jsonResponse(w, http.StatusOK, map[string]any{
			"success":             true,
			"removed":             removed,
			"bytesFreed":          freed,
			"rootAvailableBefore": rootBefore,
			"rootAvailableAfter":  rootAfter,
		})
	}
}

// handleBackupJobStatus returns the current status of an async backup/restore job.
// GET /api/backups/jobs/{id}
func handleBackupJobStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := r.PathValue("id")
		if jobID == "" {
			jsonError(w, http.StatusBadRequest, "job id required")
			return
		}
		job := backupSvc.GetJob(jobID)
		if job == nil {
			jsonError(w, http.StatusNotFound, "job not found")
			return
		}
		jsonResponse(w, http.StatusOK, normalizeJobResponse(job))
	}
}

// handleBackupDownload streams a backup file to the client.
// GET /api/backups/download/{id}
func handleBackupDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			jsonError(w, http.StatusBadRequest, "backup id required")
			return
		}
		list, err := backupSvc.List()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var foundPath string
		for _, b := range list {
			if b.ID == id {
				foundPath = b.Path
				break
			}
		}
		if foundPath == "" {
			jsonError(w, http.StatusNotFound, "backup not found")
			return
		}
		safePath, err := resolvePathUnderBase(backupSvc.BackupDir(), foundPath)
		if err != nil {
			jsonError(w, http.StatusForbidden, err.Error())
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(safePath))
		http.ServeFile(w, r, safePath)
	}
}

func normalizeJobResponse(job *backup.Job) map[string]any {
	status := "running"
	switch job.Status {
	case backup.JobPending:
		status = "pending"
	case backup.JobRunning:
		status = "running"
	case backup.JobDone:
		status = "completed"
	case backup.JobFailed:
		status = "failed"
	}
	progress := job.Progress
	if progress <= 0 {
		switch job.Status {
		case backup.JobPending:
			progress = 2
		case backup.JobRunning:
			progress = 10
		case backup.JobDone, backup.JobFailed:
			progress = 100
		}
	}
	resp := map[string]any{
		"id":           job.ID,
		"jobId":        job.ID,
		"status":       status,
		"progress":     progress,
		"phase":        job.Phase,
		"message":      job.Message,
		"type":         job.Type,
		"source":       job.Source,
		"startedAt":    job.StartedAt,
		"doneAt":       job.DoneAt,
		"etaSeconds":   job.ETASeconds,
		"bytesDone":    job.BytesDone,
		"bytesTotal":   job.BytesTotal,
		"sizeEstimate": job.SizeEstimate,
		"outputFile":   job.OutputFile,
		"speed":        job.Speed,
		"command":      job.Command,
		"logs":         job.Logs,
	}
	if job.Status == backup.JobFailed {
		errMsg := job.Error
		if errMsg == "" {
			errMsg = job.Message
		}
		resp["error"] = errMsg
	}
	if job.DoneAt.IsZero() {
		delete(resp, "doneAt")
	}
	return resp
}
