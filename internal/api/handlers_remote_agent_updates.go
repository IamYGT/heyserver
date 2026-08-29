package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/releaseversion"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

var errInvalidRemoteAgentUpdateReceipt = errors.New("managed agent returned an invalid lifecycle receipt")

func handleRemoteAgentUpdateStatus(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := runRemoteAgentUpdateTask(r.Context(), hub, r.PathValue("id"), agenthub.TaskRequest{Kind: agenthub.TaskAgentUpdateStatus, Payload: map[string]string{}}, 45*time.Second)
		if err != nil {
			writeRemoteAgentUpdateError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, status)
	}
}

func handleRemoteAgentUpgrade(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		var request remotenodes.UpgradeAgentRequest
		if !decodeRemoteAgentUpdateRequest(w, r, &request) {
			return
		}
		request.Version = strings.TrimSpace(request.Version)
		if !request.Confirmed || releaseversion.Compare(request.Version, request.Version) != releaseversion.Current {
			jsonError(w, http.StatusBadRequest, "Exact stable version and restart confirmation are required")
			return
		}
		nodeID := r.PathValue("id")
		status, err := runRemoteAgentUpdateTask(r.Context(), hub, nodeID, agenthub.TaskRequest{Kind: agenthub.TaskAgentUpdateAction, Payload: map[string]string{"action": "upgrade", "version": request.Version}}, 11*time.Minute)
		if err != nil {
			auditHostActionFailure(r, "remote_agent_upgrade", nodeID+": "+request.Version, err)
			writeRemoteAgentUpdateError(w, err)
			return
		}
		if status.Operation != "upgrade" || status.OperationStatus != "scheduled" || status.OperationVersion != request.Version {
			auditHostActionFailure(r, "remote_agent_upgrade", nodeID+": "+request.Version, errInvalidRemoteAgentUpdateReceipt)
			jsonError(w, http.StatusBadGateway, errInvalidRemoteAgentUpdateReceipt.Error())
			return
		}
		auditHostAction(r, "remote_agent_upgrade", nodeID+": "+request.Version+" scheduled")
		jsonResponse(w, http.StatusAccepted, status)
	}
}

func handleRemoteAgentRollback(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		var request remotenodes.RollbackAgentRequest
		if !decodeRemoteAgentUpdateRequest(w, r, &request) {
			return
		}
		if !request.Confirmed {
			jsonError(w, http.StatusBadRequest, "Agent restart and rollback confirmation are required")
			return
		}
		nodeID := r.PathValue("id")
		status, err := runRemoteAgentUpdateTask(r.Context(), hub, nodeID, agenthub.TaskRequest{Kind: agenthub.TaskAgentUpdateAction, Payload: map[string]string{"action": "rollback"}}, 90*time.Second)
		if err != nil {
			auditHostActionFailure(r, "remote_agent_rollback", nodeID, err)
			writeRemoteAgentUpdateError(w, err)
			return
		}
		if status.Operation != "rollback" || status.OperationStatus != "scheduled" {
			auditHostActionFailure(r, "remote_agent_rollback", nodeID, errInvalidRemoteAgentUpdateReceipt)
			jsonError(w, http.StatusBadGateway, errInvalidRemoteAgentUpdateReceipt.Error())
			return
		}
		auditHostAction(r, "remote_agent_rollback", nodeID+": scheduled")
		jsonResponse(w, http.StatusAccepted, status)
	}
}

func runRemoteAgentUpdateTask(ctx context.Context, hub *agenthub.Service, nodeID string, request agenthub.TaskRequest, timeout time.Duration) (*remotenodes.AgentUpdateStatus, error) {
	node, err := hub.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	task, err := hub.EnqueueTask(nodeID, request)
	if err != nil {
		return nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
	if err != nil {
		return nil, err
	}
	if task.Status == agenthub.TaskStatusFailed {
		return nil, errors.New(task.Error)
	}
	var status remotenodes.AgentUpdateStatus
	if err := json.Unmarshal([]byte(task.Result["data"]), &status); err != nil || !validRemoteAgentUpdateStatus(status, node.AgentVersion) {
		return nil, errInvalidRemoteAgentUpdateReceipt
	}
	return &status, nil
}

func validRemoteAgentUpdateStatus(status remotenodes.AgentUpdateStatus, observedVersion string) bool {
	if status.CurrentVersion == "" || status.CurrentVersion != observedVersion || status.ReleaseMessage == "" || status.OperationDetail == "" {
		return false
	}
	if status.Platform != "linux_amd64" && status.Platform != "linux_arm64" {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, status.ReleaseCheckedAt); err != nil {
		return false
	}
	if status.ReleaseNotesURL != "" && !validRemoteReleaseURL(status.ReleaseNotesURL) {
		return false
	}
	switch status.ReleaseStatus {
	case "not_configured", "unavailable":
		if status.UpdateAvailable {
			return false
		}
	case "healthy":
		if status.LatestVersion == "" || releaseversion.Compare(status.LatestVersion, status.LatestVersion) != releaseversion.Current {
			return false
		}
		if status.LatestState != "current" && status.LatestState != "behind" && status.LatestState != "ahead" && status.LatestState != "unknown" {
			return false
		}
		if status.UpdateAvailable != (status.LatestState == "ahead") {
			return false
		}
	default:
		return false
	}
	switch status.SignatureStatus {
	case "not_configured":
	case "verified":
		if status.ReleaseStatus == "not_configured" {
			return false
		}
	case "unavailable":
		if status.ReleaseStatus != "unavailable" {
			return false
		}
	default:
		return false
	}
	switch status.OperationStatus {
	case "idle":
		if status.Operation != "" || status.OperationVersion != "" || status.OperationUpdated != "" {
			return false
		}
	case "scheduled", "running", "completed", "failed":
		if status.Operation != "upgrade" && status.Operation != "rollback" {
			return false
		}
		if status.Operation == "upgrade" && releaseversion.Compare(status.OperationVersion, status.OperationVersion) != releaseversion.Current {
			return false
		}
		if status.Operation == "rollback" && status.OperationVersion != "" {
			return false
		}
		if _, err := time.Parse(time.RFC3339Nano, status.OperationUpdated); err != nil {
			return false
		}
	default:
		return false
	}
	return true
}

func validRemoteReleaseURL(raw string) bool {
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil
}

func decodeRemoteAgentUpdateRequest(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := decodeStrictJSON(r, destination); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return false
	}
	return true
}

func writeRemoteAgentUpdateError(w http.ResponseWriter, err error) {
	if errors.Is(err, errInvalidRemoteAgentUpdateReceipt) {
		jsonError(w, http.StatusBadGateway, errInvalidRemoteAgentUpdateReceipt.Error())
		return
	}
	if errors.Is(err, agenthub.ErrNotFound) || errors.Is(err, agenthub.ErrCapabilityUnavailable) || errors.Is(err, agenthub.ErrNodeOffline) || errors.Is(err, agenthub.ErrInvalidInput) {
		writeAgentHubError(w, err)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		jsonError(w, http.StatusGatewayTimeout, err.Error())
		return
	}
	jsonError(w, http.StatusBadGateway, err.Error())
}
