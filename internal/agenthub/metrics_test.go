package agenthub

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

func TestEnqueueMetricsTaskRequiresExactCapabilityBeforePersistence(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "metrics-legacy")
	if _, err := service.Heartbeat("metrics-legacy", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion, NodeID: "metrics-legacy", AgentVersion: "test",
		Capabilities: []string{CapabilityInventory}, Hostname: "metrics.example", SentAt: now,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if _, err := service.EnqueueMetricsTask("metrics-legacy"); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("EnqueueMetricsTask error = %v, want ErrCapabilityUnavailable", err)
	}
	tasks, err := service.ListTasksForNode("metrics-legacy", 10)
	if err != nil {
		t.Fatalf("ListTasksForNode: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("capability-gated metrics persisted tasks: %#v", tasks)
	}
}

func TestMetricsTaskHasFixedKindAndEmptyPayload(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "metrics-ready")
	if _, err := service.Heartbeat("metrics-ready", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion, NodeID: "metrics-ready", AgentVersion: "test",
		Capabilities: []string{CapabilityInventory, CapabilityMetricsRead}, Hostname: "metrics.example", SentAt: now,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	task, err := service.EnqueueMetricsTask("metrics-ready")
	if err != nil {
		t.Fatalf("EnqueueMetricsTask: %v", err)
	}
	if task.Kind != TaskMetricsRead || len(task.Payload) != 0 {
		t.Fatalf("metrics task = %#v", task)
	}
	if err := ValidateTaskRequest(TaskRequest{Kind: TaskMetricsRead, Payload: map[string]string{"path": "/proc/stat"}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("metrics payload validation = %v", err)
	}
}

func TestMetricsTaskResultRejectsMalformedStaleAndNonfiniteSnapshots(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "metrics-results")
	if _, err := service.Heartbeat("metrics-results", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion, NodeID: "metrics-results", AgentVersion: "test",
		Capabilities: []string{CapabilityMetricsRead}, Hostname: "metrics.example", SentAt: now,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	tests := []struct {
		name   string
		result string
	}{
		{name: "malformed", result: `{"observed_at":`},
		{name: "unknown field", result: `{"observed_at":"2026-08-21T04:00:00.123456789Z","cpu":{"usage_percent":1,"core_count":1},"load":{"one":0,"five":0,"fifteen":0},"memory":{"total_bytes":1,"used_bytes":0,"available_bytes":1,"usage_percent":0},"network":{"rx_bytes":0,"tx_bytes":0},"root_disk":{"total_bytes":1,"used_bytes":0,"available_bytes":1,"usage_percent":0},"raw":"forbidden"}`},
		{name: "stale", result: marshalMetricsSnapshot(t, validHubMetricsSnapshot(now.Add(-MetricsSnapshotMaxAge-time.Second)))},
		{name: "nonfinite", result: `{"observed_at":"2026-08-21T04:00:00.123456789Z","cpu":{"usage_percent":1e309,"core_count":1},"load":{"one":0,"five":0,"fifteen":0},"memory":{"total_bytes":1,"used_bytes":0,"available_bytes":1,"usage_percent":0},"network":{"rx_bytes":0,"tx_bytes":0},"root_disk":{"total_bytes":1,"used_bytes":0,"available_bytes":1,"usage_percent":0}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task, err := service.EnqueueMetricsTask("metrics-results")
			if err != nil {
				t.Fatalf("EnqueueMetricsTask: %v", err)
			}
			claimed, err := service.PollTask("metrics-results", registered.Token)
			if err != nil || claimed == nil || claimed.ID != task.ID {
				t.Fatalf("PollTask = %#v, %v", claimed, err)
			}
			_, err = service.CompleteTask("metrics-results", registered.Token, task.ID, TaskResultRequest{
				Status: TaskStatusCompleted, Result: map[string]string{"data": test.result},
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CompleteTask error = %v, want ErrInvalidInput", err)
			}
			// The rejected task remains running; fail it with the only accepted
			// closed error so the next table case can enqueue and claim a task.
			if _, err := service.CompleteTask("metrics-results", registered.Token, task.ID, TaskResultRequest{Status: TaskStatusFailed, Error: MetricsUnavailableError}); err != nil {
				t.Fatalf("close rejected task: %v", err)
			}
		})
	}

	snapshot := validHubMetricsSnapshot(now)
	snapshot.CPU.UsagePercent = math.NaN()
	if err := ValidateMetricsSnapshot(snapshot, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nonfinite typed snapshot validation = %v", err)
	}
}

func TestMetricsSnapshotJSONContract(t *testing.T) {
	now := time.Date(2026, time.August, 21, 4, 0, 0, 123456789, time.UTC)
	data := marshalMetricsSnapshot(t, validHubMetricsSnapshot(now))
	want := `{"observed_at":"2026-08-21T04:00:00.123456789Z","cpu":{"usage_percent":25,"core_count":4},"load":{"one":0.5,"five":0.25,"fifteen":0.125},"memory":{"total_bytes":1000,"used_bytes":600,"available_bytes":400,"usage_percent":60},"network":{"rx_bytes":123,"tx_bytes":456},"root_disk":{"total_bytes":1000,"used_bytes":500,"available_bytes":500,"usage_percent":50}}`
	if data != want {
		t.Fatalf("metrics JSON = %s\nwant = %s", data, want)
	}
}

func validHubMetricsSnapshot(observedAt time.Time) MetricsSnapshot {
	return MetricsSnapshot{
		ObservedAt: observedAt,
		CPU:        MetricsCPU{UsagePercent: 25, CoreCount: 4},
		Load:       MetricsLoad{One: .5, Five: .25, Fifteen: .125},
		Memory:     MetricsMemory{TotalBytes: 1000, UsedBytes: 600, AvailableBytes: 400, UsagePercent: 60},
		Network:    MetricsNetwork{RXBytes: 123, TXBytes: 456},
		RootDisk:   MetricsFilesystem{TotalBytes: 1000, UsedBytes: 500, AvailableBytes: 500, UsagePercent: 50},
	}
}

func marshalMetricsSnapshot(t *testing.T, snapshot MetricsSnapshot) string {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(data)
}
