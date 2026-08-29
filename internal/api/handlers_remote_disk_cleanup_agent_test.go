package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
)

func TestRemoteDiskCleanupRunsThroughAdvertisedAgentCapability(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: "disk-cleanup-agent", Name: "Disk Cleanup Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat("disk-cleanup-agent", registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          "disk-cleanup-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityDiskCleanup},
		Hostname:        "cleanup.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	scanDone := completeNextDiskTask(t, hub, registered.Token, agenthub.TaskDiskCleanupScan,
		`[{"id":"journal","name":"Old system journal","description":"Retain seven days","size":2048,"risk":"low"}]`)
	scanRequest := httptest.NewRequest(http.MethodGet, "/api/nodes/disk-cleanup-agent/disk/cleanup", nil)
	scanRequest.SetPathValue("id", "disk-cleanup-agent")
	scanResponse := httptest.NewRecorder()
	handleRemoteNodeDiskCleanupScan(hub).ServeHTTP(scanResponse, scanRequest)
	if err := <-scanDone; err != nil {
		t.Fatalf("scan agent: %v", err)
	}
	if scanResponse.Code != http.StatusOK || !strings.Contains(scanResponse.Body.String(), `"id":"journal"`) {
		t.Fatalf("scan status = %d, body=%s", scanResponse.Code, scanResponse.Body.String())
	}

	executeDone := completeNextDiskTask(t, hub, registered.Token, agenthub.TaskDiskCleanupExecute,
		`{"results":[{"id":"journal","status":"ok","message":"Journal vacuumed","reclaimed":1024}]}`)
	executeRequest := httptest.NewRequest(http.MethodPost, "/api/nodes/disk-cleanup-agent/disk/cleanup", bytes.NewBufferString(`{"targets":["journal"],"confirmed":true}`))
	executeRequest.SetPathValue("id", "disk-cleanup-agent")
	executeResponse := httptest.NewRecorder()
	handleRemoteNodeDiskCleanupExecute(hub).ServeHTTP(executeResponse, executeRequest)
	if err := <-executeDone; err != nil {
		t.Fatalf("execute agent: %v", err)
	}
	if executeResponse.Code != http.StatusOK || !strings.Contains(executeResponse.Body.String(), `"reclaimed":1024`) {
		t.Fatalf("execute status = %d, body=%s", executeResponse.Code, executeResponse.Body.String())
	}
}

func TestRemoteDiskCleanupRejectsUnknownFieldsBeforeQueueing(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/nodes/example/disk/cleanup", bytes.NewBufferString(`{"targets":["journal"],"confirmed":true,"unexpected":true}`))
	request.SetPathValue("id", "example")
	response := httptest.NewRecorder()

	handleRemoteNodeDiskCleanupExecute(nil).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func completeNextDiskTask(t *testing.T, hub *agenthub.Service, token, kind, data string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			task, err := hub.PollTask("disk-cleanup-agent", token)
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
			_, err = hub.CompleteTask("disk-cleanup-agent", token, task.ID, agenthub.TaskResultRequest{
				Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": data},
			})
			done <- err
			return
		}
		done <- context.DeadlineExceeded
	}()
	return done
}
