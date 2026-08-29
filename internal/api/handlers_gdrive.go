package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/services/backup"
	"github.com/IamYGT/heyserver/internal/services/gdrive"
)

var gdriveOAuthStatePattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type gdriveOAuthAppUpdateRequest struct {
	ClientID     *string `json:"clientId"`
	ClientSecret *string `json:"clientSecret"`
	GCPProjectID *string `json:"gcpProjectId"`
}

type gdriveOAuthCompleteRequest struct {
	State string `json:"state"`
}

type gdriveSettingsUpdateRequest struct {
	Folder              string `json:"folder"`
	AutoUpload          *bool  `json:"autoUpload"`
	RemoteRetentionDays *int   `json:"remoteRetentionDays"`
	NotifyOnSuccess     *bool  `json:"notifyOnSuccess"`
	NotifyOnFailure     *bool  `json:"notifyOnFailure"`
}

type gdriveRestoreRequest struct {
	FileName string `json:"fileName"`
}

// gdrivePublicRedirectURI is sent to Google during browser OAuth (must match Console).
func gdrivePublicRedirectURI(cfg *config.Config, r *http.Request) string {
	if cfg.GDriveRedirectURI != "" {
		return cfg.GDriveRedirectURI
	}
	return gdrive.BuildRedirectURIFromRequest(r)
}

// gdriveServerRedirectURI is for server-side refresh/upload — must match OAuth callback (env or token).
func gdriveServerRedirectURI(cfg *config.Config) string {
	if cfg.GDriveRedirectURI != "" {
		return cfg.GDriveRedirectURI
	}
	return gdrive.BuildInternalRedirectURIFromPort(cfg.Port)
}

func handleGDriveOAuthAppGet(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		redirectURI := gdrivePublicRedirectURI(cfg, r)
		info, err := gdriveSvc.GetOAuthAppInfo(redirectURI)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, info)
	}
}

func handleGDriveOAuthAppSet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		var body gdriveOAuthAppUpdateRequest
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if (body.ClientID != nil && strings.TrimSpace(*body.ClientID) == "") ||
			(body.GCPProjectID != nil && strings.TrimSpace(*body.GCPProjectID) == "") {
			jsonError(w, http.StatusBadRequest, "clientId and gcpProjectId must not be empty when provided")
			return
		}
		if err := gdriveSvc.SaveOAuthApp(optionalString(body.ClientID), optionalString(body.ClientSecret), optionalString(body.GCPProjectID)); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
	}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func handleGDriveStatus(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		st, err := gdriveSvc.Status(gdrivePublicRedirectURI(cfg, r))
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, st)
	}
}

func handleGDriveOAuthStart(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		user := getUserFromContext(r.Context())
		if user == nil {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		redirectURI := gdrivePublicRedirectURI(cfg, r)
		resp, err := gdriveSvc.OAuthStart(redirectURI, user.ID)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, resp)
	}
}

func handleGDriveOAuthCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveSvc == nil {
			http.Error(w, gdrive.OAuthCallbackHTML(false, "servis başlatılmadı", ""), http.StatusServiceUnavailable)
			return
		}
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		if code == "" || state == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(gdrive.OAuthCallbackHTML(false, "eksik OAuth parametreleri", "")))
			return
		}
		if err := gdriveSvc.OAuthCallback(code, state); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(gdrive.OAuthCallbackHTML(false, err.Error(), "")))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(gdrive.OAuthCallbackHTML(true, "Google onayı alındı — panel bağlantıyı tamamlıyor.", state)))
	}
}

func handleGDriveOAuthComplete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		user := getUserFromContext(r.Context())
		if user == nil {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var body gdriveOAuthCompleteRequest
		if err := decodeStrictJSON(r, &body); err != nil || !gdriveOAuthStatePattern.MatchString(body.State) {
			jsonError(w, http.StatusBadRequest, "valid OAuth state required")
			return
		}
		if err := gdriveSvc.OAuthComplete(body.State, user.ID); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
	}
}

func handleGDriveDisconnect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := gdriveSvc.Disconnect(); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
	}
}

func handleGDriveUpdateSettings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		var req gdriveSettingsUpdateRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.AutoUpload == nil || req.RemoteRetentionDays == nil || req.NotifyOnSuccess == nil || req.NotifyOnFailure == nil {
			jsonError(w, http.StatusBadRequest, "complete Google Drive settings are required")
			return
		}
		err := gdriveSvc.UpdateSettings(gdrive.SettingsUpdate{
			Folder:              req.Folder,
			AutoUpload:          *req.AutoUpload,
			RemoteRetentionDays: *req.RemoteRetentionDays,
			NotifyOnSuccess:     *req.NotifyOnSuccess,
			NotifyOnFailure:     *req.NotifyOnFailure,
		})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, gdrive.ErrInvalidSettings) {
				status = http.StatusBadRequest
			}
			jsonError(w, status, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
	}
}

func handleGDriveDismissError() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := gdriveSvc.ClearLastError(); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
	}
}

func handleGDriveTest(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := gdriveSvc.TestConnection(gdriveServerRedirectURI(cfg)); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok", "message": "Google Drive bağlantısı çalışıyor"})
	}
}

func handleGDriveListRemote(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		backups, err := gdriveSvc.ListRemote(gdriveServerRedirectURI(cfg))
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"backups": backups})
	}
}

func handleGDriveUpload(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
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
		var path string
		for _, b := range list {
			if b.ID == id {
				path = b.Path
				break
			}
		}
		if path == "" {
			jsonError(w, http.StatusNotFound, "backup not found")
			return
		}
		jobID, err := gdriveSvc.UploadAsync(path, gdriveServerRedirectURI(cfg), "manual")
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp := map[string]any{"status": "uploading", "file": path}
		if jobID != "" {
			resp["jobId"] = jobID
		}
		jsonResponse(w, http.StatusAccepted, resp)
	}
}

func handleGDriveRestore(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gdriveServiceUnavailable(w) {
			return
		}
		var body gdriveRestoreRequest
		if err := decodeStrictJSON(r, &body); err != nil || strings.TrimSpace(body.FileName) == "" {
			jsonError(w, http.StatusBadRequest, "fileName required")
			return
		}
		jobID, err := gdriveSvc.RestoreFromRemoteAsync(body.FileName, backupSvc.BackupDir(), gdriveServerRedirectURI(cfg))
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp := map[string]any{"status": "downloading", "fileName": body.FileName}
		if jobID != "" {
			resp["jobId"] = jobID
		}
		jsonResponse(w, http.StatusAccepted, resp)
	}
}

func handleInternalCronBackup(cronSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cronSecret == "" {
			jsonError(w, http.StatusForbidden, "cron backup disabled — set HSERVER_CRON_SECRET")
			return
		}
		if !isLoopbackRequest(r) {
			jsonError(w, http.StatusForbidden, "cron backup only allowed from localhost")
			return
		}
		got := r.Header.Get("X-Cron-Secret")
		if subtle.ConstantTimeCompare([]byte(got), []byte(cronSecret)) != 1 {
			jsonError(w, http.StatusForbidden, "invalid cron secret")
			return
		}
		var req struct {
			Type        string `json:"type"`
			Retention   int    `json:"retention"`
			Database    string `json:"database"`
			Engine      string `json:"engine"`
			Compression int    `json:"compression"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		bType := strings.ToLower(req.Type)
		if bType == "" {
			bType = "full"
		}
		if bType == "snapshot" {
			if snapshotServiceUnavailable(w) {
				return
			}
			jobID, err := snapshotSvc.RunAsync("scheduled")
			if err != nil {
				writeSnapshotError(w, err)
				return
			}
			jsonResponse(w, http.StatusAccepted, map[string]any{"status": "running", "jobId": jobID, "type": "snapshot"})
			return
		}
		job := backupSvc.CreateAsync(backup.CreateOptions{
			Type:             bType,
			Database:         req.Database,
			Engine:           req.Engine,
			CompressionLevel: req.Compression,
			RetentionCount:   req.Retention,
			Source:           "scheduled",
		})
		resp := normalizeJobResponse(job)
		jsonResponse(w, http.StatusAccepted, resp)
	}
}
