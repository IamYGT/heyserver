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
	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/managedintegrationstatus"
)

func TestRemoteManagedIntegrationStatusQueuesOneTypedReadOnlyTask(t *testing.T) {
	hub, nodeID, token := registerManagedStatusAPINode(t, "api-status-success", true)
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/integrations/status", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handleRemoteNodeIntegrationStatus(hub).ServeHTTP(response, request)
		close(done)
	}()

	task := waitForManagedStatusAPITask(t, hub, nodeID)
	claimed, err := hub.PollTask(nodeID, token)
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claimed task = %#v, err=%v", claimed, err)
	}
	data := managedStatusAPIData(t, nodeID)
	if _, err := hub.CompleteTask(nodeID, token, task.ID, agenthub.TaskResultRequest{
		Status: agenthub.TaskStatusCompleted,
		Result: map[string]string{"data": data},
	}); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("status handler did not return after task completion")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	status, err := managedintegrationstatus.Decode(response.Body.Bytes())
	if err != nil || status.Target.NodeID != nodeID || len(status.Results) != 2 {
		t.Fatalf("response status = %#v, err=%v", status, err)
	}
}

func TestRemoteManagedIntegrationStatusCapabilityGateDoesNotPersist(t *testing.T) {
	hub, nodeID, _ := registerManagedStatusAPINode(t, "api-status-legacy", false)
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/integrations/status", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeIntegrationStatus(hub).ServeHTTP(response, request)
	if response.Code != http.StatusConflict || response.Body.String() != `{"error":"capability_unavailable"}`+"\n" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	tasks, err := hub.ListTasksForNode(nodeID, 10)
	if err != nil {
		t.Fatalf("ListTasksForNode: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("capability-gated request persisted tasks: %#v", tasks)
	}
}

func TestRemoteManagedIntegrationStatusRequestCancellationIsSafeTimeout(t *testing.T) {
	hub, nodeID, _ := registerManagedStatusAPINode(t, "api-status-timeout", true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/integrations/status", nil).WithContext(ctx)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeIntegrationStatus(hub).ServeHTTP(response, request)
	if response.Code != http.StatusGatewayTimeout || response.Body.String() != `{"error":"managed_status_timeout"}`+"\n" {
		t.Fatalf("timeout response = %d %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "integration_status_failed") || strings.Contains(response.Body.String(), "task") {
		t.Fatalf("timeout response leaked task details: %q", response.Body.String())
	}
}

func registerManagedStatusAPINode(t *testing.T, suffix string, capability bool) (*agenthub.Service, string, string) {
	t.Helper()
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	nodeID := "managed-" + suffix
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: nodeID})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	capabilities := []string{agenthub.CapabilityInventory}
	if capability {
		capabilities = append(capabilities, agenthub.CapabilityIntegrationStatus)
	}
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "agent-test",
		Capabilities:    capabilities,
		Hostname:        nodeID + ".example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	return hub, nodeID, registered.Token
}

func waitForManagedStatusAPITask(t *testing.T, hub *agenthub.Service, nodeID string) *agenthub.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := hub.ListTasksForNode(nodeID, 10)
		if err != nil {
			t.Fatalf("ListTasksForNode: %v", err)
		}
		if len(tasks) == 1 && tasks[0].Kind == agenthub.TaskIntegrationStatus {
			return &tasks[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("managed status task was not enqueued")
	return nil
}

func managedStatusAPIData(t *testing.T, nodeID string) string {
	t.Helper()
	data, err := (managedintegrationstatus.ManagedIntegrationStatusResponse{
		SchemaVersion: managedintegrationstatus.SchemaVersion,
		ObservedAt:    time.Now().UTC(),
		Target: managedintegrationstatus.ManagedIntegrationStatusTarget{
			Scope:  managedintegrationstatus.ScopeManagedNode,
			NodeID: nodeID,
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
