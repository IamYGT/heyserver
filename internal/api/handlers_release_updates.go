package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/IamYGT/heyserver/internal/services/releaseupdates"
)

type releaseUpdateChecker interface {
	Check(context.Context) releaseupdates.Result
}

type releaseUpdateManager interface {
	Stage(context.Context) (releaseupdates.Stage, error)
	Latest(context.Context) (*releaseupdates.Stage, error)
	Schedule(context.Context, string, string, bool) (releaseupdates.Stage, error)
}

func handleReleaseUpdateCheck(checker releaseUpdateChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, checker.Check(r.Context()))
	}
}

func handleReleaseUpdateStageStatus(manager releaseUpdateManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stage, err := manager.Latest(r.Context())
		if err != nil {
			writeReleaseUpdateError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"stage": stage})
	}
}

func handleReleaseUpdateStage(manager releaseUpdateManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, "Update stage request body must be empty")
			return
		}
		stage, err := manager.Stage(r.Context())
		if err != nil {
			auditHostActionFailure(r, "release_stage", "release archive staging", err)
			writeReleaseUpdateError(w, err)
			return
		}
		auditHostAction(r, "release_stage", fmt.Sprintf("%s staged with SHA-256 %s", stage.Version, stage.SHA256))
		jsonResponse(w, http.StatusOK, stage)
	}
}

type releaseUpdateInstallRequest struct {
	StageID   string `json:"stage_id"`
	Version   string `json:"version"`
	Confirmed bool   `json:"confirmed"`
}

func handleReleaseUpdateInstall(manager releaseUpdateManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body releaseUpdateInstallRequest
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := decodeStrictJSON(r, &body); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				jsonError(w, http.StatusRequestEntityTooLarge, "Upgrade request is too large")
				return
			}
			jsonError(w, http.StatusBadRequest, "Invalid upgrade request")
			return
		}

		stage, err := manager.Schedule(r.Context(), body.StageID, body.Version, body.Confirmed)
		if err != nil {
			auditHostActionFailure(r, "release_upgrade", body.Version, err)
			writeReleaseUpdateError(w, err)
			return
		}
		auditHostAction(r, "release_upgrade", fmt.Sprintf("%s scheduled from verified stage %s", stage.Version, stage.ID))
		jsonResponse(w, http.StatusAccepted, stage)
	}
}

func writeReleaseUpdateError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "Release update operation failed"
	switch {
	case errors.Is(err, releaseupdates.ErrConfirmationRequired), errors.Is(err, releaseupdates.ErrInvalidStage):
		status = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, releaseupdates.ErrNoUpdateAvailable), errors.Is(err, releaseupdates.ErrStageConflict), errors.Is(err, releaseupdates.ErrStageIntegrity):
		status = http.StatusConflict
		message = err.Error()
	case errors.Is(err, releaseupdates.ErrDiscoveryUnavailable), errors.Is(err, releaseupdates.ErrUpgradeSchedule):
		status = http.StatusServiceUnavailable
		message = err.Error()
	case errors.Is(err, releaseupdates.ErrSignedManifestRequired):
		status = http.StatusServiceUnavailable
		message = "A verified signed release manifest is required"
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
		message = "Update stage was not found"
	}
	jsonError(w, status, message)
}
