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

func TestRemoteNginxActionRunsThroughAdvertisedAgentCapability(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: "nginx-agent", Name: "Nginx Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat("nginx-agent", registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          "nginx-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityNginxAction},
		Hostname:        "nginx.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			task, pollErr := hub.PollTask("nginx-agent", registered.Token)
			if pollErr != nil {
				done <- pollErr
				return
			}
			if task == nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if task.Kind != agenthub.TaskNginxAction || task.Payload["action"] != "reload" {
				done <- context.Canceled
				return
			}
			_, completeErr := hub.CompleteTask("nginx-agent", registered.Token, task.ID, agenthub.TaskResultRequest{
				Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Nginx configuration tested and reloaded"},
			})
			done <- completeErr
			return
		}
		done <- context.DeadlineExceeded
	}()

	request := httptest.NewRequest(http.MethodPost, "/api/nodes/nginx-agent/nginx/actions/reload", nil)
	request.SetPathValue("id", "nginx-agent")
	request.SetPathValue("action", "reload")
	response := httptest.NewRecorder()
	handleRemoteNodeNginxAction(hub).ServeHTTP(response, request)
	if err := <-done; err != nil {
		t.Fatalf("agent: %v", err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"message":"Nginx configuration tested and reloaded"`) {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
}
