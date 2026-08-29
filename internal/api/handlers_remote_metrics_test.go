package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
)

func TestRemoteManagedMetricsReturnsTypedSnapshot(t *testing.T) {
	hub, nodeID, token := registerMetricsAPINode(t, "api-metrics-success", true)
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/metrics", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handleRemoteNodeMetrics(hub).ServeHTTP(response, request)
		close(done)
	}()

	task := waitForMetricsAPITask(t, hub, nodeID)
	claimed, err := hub.PollTask(nodeID, token)
	if err != nil || claimed == nil || claimed.ID != task.ID || claimed.Kind != agenthub.TaskMetricsRead || len(claimed.Payload) != 0 {
		t.Fatalf("claimed metrics task = %#v, err=%v", claimed, err)
	}
	snapshot := metricsAPISnapshot(time.Now().UTC())
	data, _ := json.Marshal(snapshot)
	if _, err := hub.CompleteTask(nodeID, token, task.ID, agenthub.TaskResultRequest{
		Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": string(data)},
	}); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("metrics handler did not return after completion")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	decoded, err := agenthub.DecodeMetricsSnapshot(response.Body.Bytes())
	if err != nil || decoded.CPU.CoreCount != 8 || decoded.Network.RXBytes != 1000 {
		t.Fatalf("typed response = %#v, err=%v", decoded, err)
	}
	if strings.Contains(response.Body.String(), "data") || strings.Contains(response.Body.String(), "task") {
		t.Fatalf("response exposed task transport wrapper: %s", response.Body.String())
	}
}

func TestRemoteManagedMetricsErrorMappingAndCapabilityGate(t *testing.T) {
	t.Run("missing node", func(t *testing.T) {
		hub, err := agenthub.New(db.Instance())
		if err != nil {
			t.Fatalf("agenthub.New: %v", err)
		}
		request := httptest.NewRequest(http.MethodGet, "/api/nodes/missing-metrics/metrics", nil)
		request.SetPathValue("id", "missing-metrics")
		response := httptest.NewRecorder()
		handleRemoteNodeMetrics(hub).ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("missing response = %d %s", response.Code, response.Body.String())
		}
	})

	t.Run("missing capability persists no task", func(t *testing.T) {
		hub, nodeID, _ := registerMetricsAPINode(t, "api-metrics-legacy", false)
		request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/metrics", nil)
		request.SetPathValue("id", nodeID)
		response := httptest.NewRecorder()
		handleRemoteNodeMetrics(hub).ServeHTTP(response, request)
		if response.Code != http.StatusConflict || response.Body.String() != `{"error":"capability_unavailable"}`+"\n" {
			t.Fatalf("capability response = %d %q", response.Code, response.Body.String())
		}
		tasks, err := hub.ListTasksForNode(nodeID, 10)
		if err != nil || len(tasks) != 0 {
			t.Fatalf("capability-gated tasks = %#v, err=%v", tasks, err)
		}
	})

	t.Run("offline node", func(t *testing.T) {
		hub, nodeID, _ := registerMetricsAPINode(t, "api-metrics-offline", true)
		old := time.Now().UTC().Add(-2 * agenthub.NodeOnlineWindow).Format(time.RFC3339Nano)
		if _, err := db.Instance().Exec(`UPDATE agent_nodes SET last_seen_at = ? WHERE id = ?`, old, nodeID); err != nil {
			t.Fatalf("age node heartbeat: %v", err)
		}
		request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/metrics", nil)
		request.SetPathValue("id", nodeID)
		response := httptest.NewRecorder()
		handleRemoteNodeMetrics(hub).ServeHTTP(response, request)
		if response.Code != http.StatusConflict || response.Body.String() != `{"error":"managed_node_offline"}`+"\n" {
			t.Fatalf("offline response = %d %q", response.Code, response.Body.String())
		}
		tasks, err := hub.ListTasksForNode(nodeID, 10)
		if err != nil || len(tasks) != 0 {
			t.Fatalf("offline request tasks = %#v, err=%v", tasks, err)
		}
	})

	t.Run("bounded timeout", func(t *testing.T) {
		hub, nodeID, _ := registerMetricsAPINode(t, "api-metrics-timeout", true)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/metrics", nil).WithContext(ctx)
		request.SetPathValue("id", nodeID)
		response := httptest.NewRecorder()
		handleRemoteNodeMetrics(hub).ServeHTTP(response, request)
		if response.Code != http.StatusGatewayTimeout || response.Body.String() != `{"error":"managed_metrics_timeout"}`+"\n" {
			t.Fatalf("timeout response = %d %q", response.Code, response.Body.String())
		}
	})
}

func TestRemoteManagedMetricsMapsInvalidTypedResultToBadGateway(t *testing.T) {
	hub, nodeID, token := registerMetricsAPINode(t, "api-metrics-invalid-result", true)
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/metrics", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handleRemoteNodeMetrics(hub).ServeHTTP(response, request)
		close(done)
	}()
	task := waitForMetricsAPITask(t, hub, nodeID)
	if _, err := hub.PollTask(nodeID, token); err != nil {
		t.Fatalf("PollTask: %v", err)
	}
	valid, _ := json.Marshal(metricsAPISnapshot(time.Now().UTC()))
	if _, err := hub.CompleteTask(nodeID, token, task.ID, agenthub.TaskResultRequest{
		Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": string(valid)},
	}); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	// Defense in depth: simulate corrupted persisted transport data after the
	// strict completion gate, before the handler's next bounded poll.
	corruptResult, _ := json.Marshal(map[string]string{"data": `{"observed_at":`})
	if _, err := db.Instance().Exec(`UPDATE agent_tasks SET result_json = ? WHERE id = ?`, string(corruptResult), task.ID); err != nil {
		t.Fatalf("corrupt task fixture: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("metrics handler did not return")
	}
	if response.Code != http.StatusBadGateway || response.Body.String() != `{"error":"managed_metrics_failed"}`+"\n" {
		t.Fatalf("invalid result response = %d %q", response.Code, response.Body.String())
	}
}

func TestRemoteManagedMetricsRouteIsAdminOnly(t *testing.T) {
	for _, route := range AllRoutes() {
		if route.Method == http.MethodGet && route.Path == "/api/nodes/{id}/metrics" {
			if route.Auth != RouteAdmin {
				t.Fatalf("metrics route auth = %q", route.Auth)
			}
			return
		}
	}
	t.Fatal("metrics route missing from route manifest")
}

func registerMetricsAPINode(t *testing.T, nodeID string, metricsCapability bool) (*agenthub.Service, string, string) {
	t.Helper()
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: nodeID})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	capabilities := []string{agenthub.CapabilityInventory}
	if metricsCapability {
		capabilities = append(capabilities, agenthub.CapabilityMetricsRead)
	}
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test",
		Capabilities: capabilities, Hostname: nodeID + ".example", SentAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	return hub, nodeID, registered.Token
}

func waitForMetricsAPITask(t *testing.T, hub *agenthub.Service, nodeID string) *agenthub.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := hub.ListTasksForNode(nodeID, 10)
		if err != nil {
			t.Fatalf("ListTasksForNode: %v", err)
		}
		if len(tasks) == 1 && tasks[0].Kind == agenthub.TaskMetricsRead {
			return &tasks[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("metrics task was not enqueued")
	return nil
}

func metricsAPISnapshot(observedAt time.Time) agenthub.MetricsSnapshot {
	return agenthub.MetricsSnapshot{
		ObservedAt: observedAt,
		CPU:        agenthub.MetricsCPU{UsagePercent: 20, CoreCount: 8},
		Load:       agenthub.MetricsLoad{One: 1, Five: .5, Fifteen: .25},
		Memory:     agenthub.MetricsMemory{TotalBytes: 2000, UsedBytes: 1000, AvailableBytes: 1000, UsagePercent: 50},
		Network:    agenthub.MetricsNetwork{RXBytes: 1000, TXBytes: 2000},
		RootDisk:   agenthub.MetricsFilesystem{TotalBytes: 4000, UsedBytes: 1000, AvailableBytes: 3000, UsagePercent: 25},
	}
}
