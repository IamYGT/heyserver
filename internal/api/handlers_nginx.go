package api

import (
	"errors"
	"net/http"

	nginxsvc "github.com/IamYGT/heyserver/internal/services/nginx"
)

func nginxErrorStatus(err error, fallback int) int {
	if errors.Is(err, nginxsvc.ErrNotConfigured) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, nginxsvc.ErrConfigChanged) {
		return http.StatusConflict
	}
	if errors.Is(err, nginxsvc.ErrConfigEnabled) {
		return http.StatusConflict
	}
	if errors.Is(err, nginxsvc.ErrConfigExists) {
		return http.StatusConflict
	}
	if errors.Is(err, nginxsvc.ErrConfigNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, nginxsvc.ErrConfigInvalid) {
		return http.StatusUnprocessableEntity
	}
	if errors.Is(err, nginxsvc.ErrConfigTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return fallback
}

// handleNginxList returns all nginx vhost configs with their enabled state.
// GET /api/nginx/configs
func handleNginxList(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		configs, err := nginxService.ListConfigs()
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, configs)
	}
}

// handleNginxGet returns the full content of a single config file.
// GET /api/nginx/configs/{filename}
func handleNginxGet(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		cfg, err := nginxService.GetConfig(filename)
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusNotFound), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, cfg)
	}
}

// handleNginxSave overwrites the content of an existing config file.
// PUT /api/nginx/configs/{filename}
// Requires manager role. Callers must call POST /api/nginx/test + POST /api/nginx/reload separately.
func handleNginxSave(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")

		var body struct {
			Content  string `json:"content"`
			Checksum string `json:"checksum"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if body.Content == "" {
			jsonError(w, http.StatusBadRequest, "content cannot be empty")
			return
		}

		receipt, err := nginxService.SaveConfig(filename, body.Content, body.Checksum)
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusBadRequest), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, receipt)
	}
}

// handleNginxCreate generates a vhost config from a template and writes it to sites-available.
// POST /api/nginx/configs
// Requires manager role.
func handleNginxCreate(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req nginxsvc.CreateRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if req.Domain == "" {
			jsonError(w, http.StatusBadRequest, "domain is required")
			return
		}
		if req.Type == "" {
			jsonError(w, http.StatusBadRequest, "type is required (php|proxy|static|redirect)")
			return
		}

		cfg, err := nginxService.CreateConfig(req)
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusBadRequest), err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, cfg)
	}
}

// handleNginxArchive removes one disabled config from inventory while retaining
// a checksum-bound recovery copy. It never removes the site's document root.
// DELETE /api/nginx/configs/{filename}
func handleNginxArchive(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		var body struct {
			Checksum string `json:"checksum"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		receipt, err := nginxService.ArchiveConfig(filename, body.Checksum)
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusBadRequest), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, receipt)
	}
}

// handleNginxArchiveList returns validated recovery copies retained below the
// installation-owned sites-available directory.
// GET /api/nginx/archives
func handleNginxArchiveList(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		archives, err := nginxService.ListConfigArchives()
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, archives)
	}
}

// handleNginxArchiveRestore restores a missing disabled config from one exact
// observed archive and checksum. The archive is retained and reload is explicit.
// POST /api/nginx/archives/{archive}/restore
func handleNginxArchiveRestore(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		archive := r.PathValue("archive")
		var body struct {
			Checksum string `json:"checksum"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		receipt, err := nginxService.RestoreConfigArchive(archive, body.Checksum)
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusBadRequest), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, receipt)
	}
}

// handleNginxBackupList returns validated pre-edit recovery copies retained
// below the installation-owned sites-available directory.
// GET /api/nginx/backups
func handleNginxBackupList(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backups, err := nginxService.ListConfigBackups()
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, backups)
	}
}

// handleNginxBackupRestore replaces one exact current config from an observed
// pre-edit backup under separate backup and current-target checksum locks.
// POST /api/nginx/backups/{backup}/restore
func handleNginxBackupRestore(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backup := r.PathValue("backup")
		var body struct {
			BackupChecksum  string `json:"backupChecksum"`
			CurrentChecksum string `json:"currentChecksum"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		receipt, err := nginxService.RestoreConfigBackup(backup, body.BackupChecksum, body.CurrentChecksum)
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusBadRequest), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, receipt)
	}
}

// handleNginxState applies an explicit enabled state by managing the exact
// sites-enabled symlink. PUT /state is canonical; POST /toggle is retained as
// a backwards-compatible alias with the same desired-state body.
func handleNginxState(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if body.Enabled == nil {
			jsonError(w, http.StatusBadRequest, "enabled is required")
			return
		}

		enabled, err := nginxService.SetEnabled(filename, *body.Enabled)
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusBadRequest), err.Error())
			return
		}

		status := "disabled"
		if enabled {
			status = "enabled"
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"filename":  filename,
			"isEnabled": enabled,
			"message":   "site " + status,
		})
	}
}

// handleNginxTest runs `nginx -t` and returns the result.
// POST /api/nginx/test
func handleNginxTest(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := nginxService.Test()
		jsonResponse(w, http.StatusOK, result)
	}
}

// handleNginxReload reloads nginx via systemctl.
// Runs nginx -t first; returns 422 if the config test fails.
// POST /api/nginx/reload
// Requires manager role.
func handleNginxReload(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Always test before reload to prevent downtime
		test := nginxService.Test()
		if !test.OK {
			jsonResponse(w, http.StatusUnprocessableEntity, map[string]interface{}{
				"error":  "nginx config test failed — reload aborted",
				"output": test.Output,
			})
			return
		}

		if err := nginxService.Reload(); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"message": "nginx reloaded successfully",
		})
	}
}

// handleNginxStatus returns nginx service status, version, uptime and config test result.
// GET /api/nginx/status
func handleNginxStatus(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, nginxService.Status())
	}
}

// handleNginxSnippets returns the list of available snippet include files.
// GET /api/nginx/snippets
func handleNginxSnippets(nginxService *nginxsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snippets, err := nginxService.ListSnippets()
		if err != nil {
			jsonError(w, nginxErrorStatus(err, http.StatusInternalServerError), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, snippets)
	}
}
