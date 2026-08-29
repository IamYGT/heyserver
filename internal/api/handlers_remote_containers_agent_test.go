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
)

func TestRemoteContainersRunThroughAdvertisedAgentCapabilities(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: "containers-agent", Name: "Containers Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat("containers-agent", registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          "containers-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityContainerRead, agenthub.CapabilityContainerAction},
		Hostname:        "containers.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	listDone := completeNextContainerTask(t, hub, registered.Token, agenthub.TaskContainerList,
		`[{"id":"abc123","name":"web-1","image":"nginx:stable","state":"running","status":"Up","ports":"80/tcp"}]`, "")
	listRequest := httptest.NewRequest(http.MethodGet, "/api/nodes/containers-agent/containers", nil)
	listRequest.SetPathValue("id", "containers-agent")
	listResponse := httptest.NewRecorder()
	handleRemoteNodeContainers(hub).ServeHTTP(listResponse, listRequest)
	if err := <-listDone; err != nil {
		t.Fatalf("list agent: %v", err)
	}
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"name":"web-1"`) {
		t.Fatalf("list status = %d, body=%s", listResponse.Code, listResponse.Body.String())
	}

	actionDone := completeNextContainerTask(t, hub, registered.Token, agenthub.TaskContainerAction, "", "Container restart completed for web-1")
	actionRequest := httptest.NewRequest(http.MethodPost, "/api/nodes/containers-agent/containers/web-1/actions/restart", nil)
	actionRequest.SetPathValue("id", "containers-agent")
	actionRequest.SetPathValue("container", "web-1")
	actionRequest.SetPathValue("action", "restart")
	actionResponse := httptest.NewRecorder()
	handleRemoteNodeContainerAction(hub).ServeHTTP(actionResponse, actionRequest)
	if err := <-actionDone; err != nil {
		t.Fatalf("action agent: %v", err)
	}
	if actionResponse.Code != http.StatusOK || !strings.Contains(actionResponse.Body.String(), `"message":"Container restart completed for web-1"`) {
		t.Fatalf("action status = %d, body=%s", actionResponse.Code, actionResponse.Body.String())
	}
}

func completeNextContainerTask(t *testing.T, hub *agenthub.Service, token, kind, data, message string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			task, err := hub.PollTask("containers-agent", token)
			if err != nil {
				done <- err
				return
			}
			if task == nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if task.Kind != kind {
				done <- context.Canceled
				return
			}
			result := map[string]string{}
			if data != "" {
				result["data"] = data
			}
			if message != "" {
				result["message"] = message
			}
			_, err = hub.CompleteTask("containers-agent", token, task.ID, agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: result})
			done <- err
			return
		}
		done <- context.DeadlineExceeded
	}()
	return done
}
