package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/services/snapshot"
)

func handleSnapshotStatus(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if snapshotServiceUnavailable(w) {
			return
		}
		skipHeavy := backupSvc != nil && backupSvc.HasActiveJobTypes("snapshot", "snapshot_restore")
		forceRefresh := r.URL.Query().Get("refresh") == "1"
		st, err := snapshotSvc.Status(gdriveServerRedirectURI(cfg), skipHeavy, forceRefresh)
		if err != nil {
			writeSnapshotError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, st)
	}
}

func handleSnapshotPurgeRepo(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if snapshotServiceUnavailable(w) {
			return
		}
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if backupSvc != nil && backupSvc.HasActiveJobTypes("snapshot", "snapshot_restore") {
			jsonError(w, http.StatusConflict, "aktif snapshot varken repo silinemez")
			return
		}
		var req snapshotPurgeRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if err := snapshotSvc.PurgeRemoteRepo(snapshot.PurgeRequest{
			RepoFolder:   req.RepoFolder,
			Confirmation: req.Confirmation,
		}, gdriveServerRedirectURI(cfg)); err != nil {
			writeSnapshotError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
	}
}

func handleSnapshotRun() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if snapshotServiceUnavailable(w) {
			return
		}
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jobID, err := snapshotSvc.RunAsync("manual")
		if err != nil {
			writeSnapshotError(w, err)
			return
		}
		resp := map[string]any{"status": "running", "message": "incremental snapshot started"}
		if jobID != "" {
			resp["jobId"] = jobID
		}
		jsonResponse(w, http.StatusAccepted, resp)
	}
}

func handleSnapshotList(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if snapshotServiceUnavailable(w) {
			return
		}
		snaps, err := snapshotSvc.ListSnapshots(gdriveServerRedirectURI(cfg), 30)
		if err != nil {
			writeSnapshotError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"snapshots": snaps})
	}
}

func handleSnapshotVhosts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if snapshotServiceUnavailable(w) {
			return
		}
		names, err := snapshotSvc.ListVhosts()
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"vhosts": names})
	}
}

func handleSnapshotRestore(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if snapshotServiceUnavailable(w) {
			return
		}
		var req snapshotRestoreRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		jobID, err := snapshotSvc.RestoreAsync(snapshot.RestoreRequest{
			SnapshotID:  strings.TrimSpace(req.SnapshotID),
			ManifestIDs: req.ManifestIDs,
			Vhosts:      req.Vhosts,
		}, gdriveServerRedirectURI(cfg))
		if err != nil {
			writeSnapshotError(w, err)
			return
		}
		resp := map[string]any{"status": "restoring"}
		if jobID != "" {
			resp["jobId"] = jobID
		}
		jsonResponse(w, http.StatusAccepted, resp)
	}
}

func handleSnapshotSettings(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if snapshotServiceUnavailable(w) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			skipHeavy := backupSvc != nil && backupSvc.HasActiveJobTypes("snapshot", "snapshot_restore")
			st, err := snapshotSvc.Status(gdriveServerRedirectURI(cfg), skipHeavy, false)
			if err != nil {
				writeSnapshotError(w, err)
				return
			}
			jsonResponse(w, http.StatusOK, st.Settings)
		case http.MethodPut:
			var req snapshotSettingsUpdateRequest
			if err := decodeStrictJSON(r, &req); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			settings, err := req.settingsUpdate()
			if err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := snapshotSvc.UpdateSettings(settings); err != nil {
				writeSnapshotError(w, err)
				return
			}
			jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
		default:
			jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

type snapshotRestoreRequest struct {
	SnapshotID  string   `json:"snapshotId"`
	ManifestIDs []string `json:"manifestIds"`
	Vhosts      []string `json:"vhosts"`
}

type snapshotPurgeRequest struct {
	RepoFolder   string `json:"repoFolder"`
	Confirmation string `json:"confirmation"`
}

type snapshotSettingsUpdateRequest struct {
	Destination          *snapshot.Destination `json:"destination"`
	RepoFolder           string                `json:"repoFolder"`
	EnabledPaths         *[]string             `json:"enabledPaths"`
	KeepDaily            int                   `json:"keepDaily"`
	KeepWeekly           int                   `json:"keepWeekly"`
	KeepMonthly          int                   `json:"keepMonthly"`
	PasswordAcknowledged *bool                 `json:"passwordAcknowledged"`
}

func (req snapshotSettingsUpdateRequest) settingsUpdate() (snapshot.SettingsUpdate, error) {
	if req.Destination == nil {
		return snapshot.SettingsUpdate{}, errors.New("destination is required")
	}
	if req.EnabledPaths == nil {
		return snapshot.SettingsUpdate{}, errors.New("enabledPaths is required")
	}
	if req.PasswordAcknowledged == nil {
		return snapshot.SettingsUpdate{}, errors.New("passwordAcknowledged is required")
	}
	return snapshot.SettingsUpdate{
		Destination:          *req.Destination,
		RepoFolder:           req.RepoFolder,
		EnabledPaths:         append([]string(nil), (*req.EnabledPaths)...),
		KeepDaily:            req.KeepDaily,
		KeepWeekly:           req.KeepWeekly,
		KeepMonthly:          req.KeepMonthly,
		PasswordAcknowledged: *req.PasswordAcknowledged,
	}, nil
}

func writeSnapshotError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, snapshot.ErrSettingsUnavailable) || errors.Is(err, snapshot.ErrDestinationUnavailable) {
		status = http.StatusServiceUnavailable
	} else if errors.Is(err, snapshot.ErrUnsupportedCapability) {
		status = http.StatusUnprocessableEntity
	}
	jsonError(w, status, err.Error())
}
