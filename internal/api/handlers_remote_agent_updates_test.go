package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

func TestRemoteAgentLifecycleUsesFixedCapabilityScopedTasks(t *testing.T) {
	const nodeID = "lifecycle-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Lifecycle Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "v1.0.0",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityAgentUpdateRead, agenthub.CapabilityAgentUpdateAction},
		Hostname:        "agent.example",
		SentAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	statusDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskAgentUpdateStatus, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if len(task.Payload) != 0 {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return remoteAgentUpdateReceipt("", "idle", ""), nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/agent-update", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteAgentUpdateStatus(hub).ServeHTTP(response, request)
	if err := <-statusDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"latest_version":"v1.2.3"`) {
		t.Fatalf("status=%d %s", response.Code, response.Body.String())
	}

	upgradeDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskAgentUpdateAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if len(task.Payload) != 2 || task.Payload["action"] != "upgrade" || task.Payload["version"] != "v1.2.3" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		if task.Payload["url"] != "" || task.Payload["sha256"] != "" || task.Payload["command"] != "" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return remoteAgentUpdateReceipt("upgrade", "scheduled", "v1.2.3"), nil
	})
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/agent-update/upgrade", strings.NewReader(`{"version":"v1.2.3","confirmed":true}`))
	request.SetPathValue("id", nodeID)
	response = httptest.NewRecorder()
	handleRemoteAgentUpgrade(hub).ServeHTTP(response, request)
	if err := <-upgradeDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"operation_status":"scheduled"`) {
		t.Fatalf("upgrade=%d %s", response.Code, response.Body.String())
	}

	rollbackDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskAgentUpdateAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if len(task.Payload) != 1 || task.Payload["action"] != "rollback" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return remoteAgentUpdateReceipt("rollback", "scheduled", ""), nil
	})
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/agent-update/rollback", strings.NewReader(`{"confirmed":true}`))
	request.SetPathValue("id", nodeID)
	response = httptest.NewRecorder()
	handleRemoteAgentRollback(hub).ServeHTTP(response, request)
	if err := <-rollbackDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"operation":"rollback"`) {
		t.Fatalf("rollback=%d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/agent-update/upgrade", strings.NewReader(`{"version":"v1.2.3","confirmed":false}`))
	request.SetPathValue("id", nodeID)
	response = httptest.NewRecorder()
	handleRemoteAgentUpgrade(hub).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed upgrade=%d %s", response.Code, response.Body.String())
	}
}

func TestRemoteAgentLifecycleRejectsMismatchedObservedVersion(t *testing.T) {
	status := validRemoteAgentUpdateStatus(remotenodes.AgentUpdateStatus{
		ReleaseStatus: "healthy", CurrentVersion: "v1.0.1", LatestVersion: "v1.2.3", LatestState: "ahead", UpdateAvailable: true,
		SignatureStatus: "verified", Platform: "linux_amd64", ReleaseMessage: "update available", ReleaseCheckedAt: "2026-08-26T22:00:00Z",
		OperationStatus: "idle", OperationDetail: "none",
	}, "v1.0.0")
	if status {
		t.Fatal("receipt with a different heartbeat version was accepted")
	}
}

func TestRemoteAgentLifecycleRejectsImpossibleSignatureState(t *testing.T) {
	status := remotenodes.AgentUpdateStatus{
		ReleaseStatus: "healthy", SignatureStatus: "unavailable", CurrentVersion: "v1.0.0", LatestVersion: "v1.2.3", LatestState: "ahead", UpdateAvailable: true,
		Platform: "linux_amd64", ReleaseMessage: "update available", ReleaseCheckedAt: "2026-08-26T22:00:00Z",
		OperationStatus: "idle", OperationDetail: "none",
	}
	if validRemoteAgentUpdateStatus(status, "v1.0.0") {
		t.Fatal("healthy receipt with an unavailable signature was accepted")
	}
}

func remoteAgentUpdateReceipt(action, status, version string) agenthub.TaskResultRequest {
	payload := `{"release_status":"healthy","signature_status":"verified","current_version":"v1.0.0","latest_version":"v1.2.3","latest_version_state":"ahead","update_available":true,"platform":"linux_amd64","release_notes_url":"https://releases.example.com/v1.2.3","release_message":"update available","release_checked_at":"2026-08-26T22:00:00Z","operation":"` + action + `","operation_status":"` + status + `","operation_version":"` + version + `","operation_detail":"lifecycle status","rollback_available":true`
	if status != "idle" {
		payload += `,"operation_updated_at":"2026-08-26T22:00:00Z"`
	}
	payload += `}`
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": payload}}
}
