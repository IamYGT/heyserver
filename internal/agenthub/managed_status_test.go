package agenthub

import (
	"errors"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/managedintegrationstatus"
)

func integrationHeartbeat(t *testing.T, service *Service, nodeID, token string, capabilities []string) {
	t.Helper()
	if _, err := service.Heartbeat(nodeID, token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "agent-test",
		Capabilities:    capabilities,
		Hostname:        nodeID + ".example",
		SentAt:          service.currentTime(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
}

func validManagedStatusData(t *testing.T, nodeID string) string {
	t.Helper()
	data, err := (managedintegrationstatus.ManagedIntegrationStatusResponse{
		SchemaVersion: managedintegrationstatus.SchemaVersion,
		ObservedAt:    time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
		Target: managedintegrationstatus.ManagedIntegrationStatusTarget{
			Scope: managedintegrationstatus.ScopeManagedNode, NodeID: nodeID,
		},
		Results: []managedintegrationstatus.ManagedIntegrationStatusResult{
			{ID: managedintegrationstatus.ProcessPM2ID, State: integrationstate.Healthy, Probe: managedintegrationstatus.PM2InventoryProbe},
			{ID: managedintegrationstatus.DockerID, State: integrationstate.Healthy, Probe: managedintegrationstatus.DockerInfoProbe},
		},
		Partial: false,
	}).Marshal()
	if err != nil {
		t.Fatalf("Marshal managed status: %v", err)
	}
	return string(data)
}

func TestIntegrationStatusRequiresCapabilityWithoutPersistingTask(t *testing.T) {
	service, _, _ := newTestService(t)
	registered := registerTestNode(t, service, "legacy-agent")
	integrationHeartbeat(t, service, "legacy-agent", registered.Token, []string{CapabilityInventory})
	if _, err := service.EnqueueIntegrationStatusTask("legacy-agent"); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("enqueue without capability = %v, want ErrCapabilityUnavailable", err)
	}
	tasks, err := service.ListTasksForNode("legacy-agent", 10)
	if err != nil {
		t.Fatalf("ListTasksForNode: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("legacy node received task: %#v", tasks)
	}
}

func TestIntegrationStatusCoalescesQueuedAndRunningTasks(t *testing.T) {
	service, _, _ := newTestService(t)
	registered := registerTestNode(t, service, "status-agent")
	integrationHeartbeat(t, service, "status-agent", registered.Token, []string{CapabilityInventory, CapabilityIntegrationStatus})

	first, err := service.EnqueueIntegrationStatusTask("status-agent")
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	second, err := service.EnqueueIntegrationStatusTask("status-agent")
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("queued task IDs = %d and %d, want coalesced task", first.ID, second.ID)
	}
	claimed, err := service.PollTask("status-agent", registered.Token)
	if err != nil || claimed == nil || claimed.ID != first.ID {
		t.Fatalf("claim = %#v, err=%v", claimed, err)
	}
	running, err := service.EnqueueIntegrationStatusTask("status-agent")
	if err != nil || running.ID != first.ID {
		t.Fatalf("running enqueue = %#v, err=%v", running, err)
	}
	if _, err := service.CompleteTask("status-agent", registered.Token, first.ID, TaskResultRequest{
		Status: TaskStatusCompleted,
		Result: map[string]string{"data": validManagedStatusData(t, "status-agent")},
	}); err != nil {
		t.Fatalf("complete status task: %v", err)
	}
	third, err := service.EnqueueIntegrationStatusTask("status-agent")
	if err != nil || third.ID == first.ID {
		t.Fatalf("post-completion enqueue = %#v, err=%v", third, err)
	}
}

func TestIntegrationStatusResultRejectsUnknownDataAndRawTaskError(t *testing.T) {
	service, _, _ := newTestService(t)
	registered := registerTestNode(t, service, "typed-status-agent")
	integrationHeartbeat(t, service, "typed-status-agent", registered.Token, []string{CapabilityIntegrationStatus})
	task, err := service.EnqueueIntegrationStatusTask("typed-status-agent")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := service.PollTask("typed-status-agent", registered.Token); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if _, err := service.CompleteTask("typed-status-agent", registered.Token, task.ID, TaskResultRequest{
		Status: TaskStatusCompleted,
		Result: map[string]string{"data": `{"schema_version":1,"unexpected":"raw"}`},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown typed result = %v, want ErrInvalidInput", err)
	}
	if _, err := service.CompleteTask("typed-status-agent", registered.Token, task.ID, TaskResultRequest{
		Status: TaskStatusFailed,
		Error:  "command output: /etc/secret",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("raw task error = %v, want ErrInvalidInput", err)
	}
}
