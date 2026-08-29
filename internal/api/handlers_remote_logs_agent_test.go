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

func TestRemoteLogsRunThroughAdvertisedAgentCapability(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: "logs-agent", Name: "Logs Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat("logs-agent", registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          "logs-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityLogsRead},
		Hostname:        "logs.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			task, pollErr := hub.PollTask("logs-agent", registered.Token)
			if pollErr != nil {
				done <- pollErr
				return
			}
			if task == nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if task.Kind != agenthub.TaskLogsRead || task.Payload["source"] != "nginx" || task.Payload["lines"] != "200" {
				done <- context.Canceled
				return
			}
			_, completeErr := hub.CompleteTask("logs-agent", registered.Token, task.ID, agenthub.TaskResultRequest{
				Status: agenthub.TaskStatusCompleted,
				Result: map[string]string{"data": `[{"timestamp":"2026-08-26T10:00:00Z","unit":"nginx.service","priority":3,"message":"upstream failed"}]`},
			})
			done <- completeErr
			return
		}
		done <- context.DeadlineExceeded
	}()

	request := httptest.NewRequest(http.MethodGet, "/api/nodes/logs-agent/logs?source=nginx&lines=200", nil)
	request.SetPathValue("id", "logs-agent")
	response := httptest.NewRecorder()
	handleRemoteNodeLogs(hub).ServeHTTP(response, request)
	if err := <-done; err != nil {
		t.Fatalf("agent: %v", err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"unit":"nginx.service"`) || !strings.Contains(response.Body.String(), `"message":"upstream failed"`) {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
}
