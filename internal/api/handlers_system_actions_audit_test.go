package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/systemactions"
)

type failingSystemActions struct{ err error }

func (f failingSystemActions) TerminateProcess(int, string, uint64) (systemactions.ProcessSignalResult, error) {
	return systemactions.ProcessSignalResult{}, f.err
}
func (f failingSystemActions) ControlService(context.Context, string, string) (string, error) {
	return "", f.err
}
func (f failingSystemActions) ResetSwap(context.Context) (string, error) {
	return "", f.err
}
func (f failingSystemActions) OptimizeMemory(context.Context) (string, error) {
	return "", f.err
}
func (f failingSystemActions) CleanTemporaryFiles(context.Context) (string, error) {
	return "", f.err
}
func (f failingSystemActions) ScheduleReboot(context.Context) (string, error) {
	return "", f.err
}
func (f failingSystemActions) CancelScheduledReboot(context.Context) (string, error) {
	return "", f.err
}
func (f failingSystemActions) RebootPending(context.Context) (bool, error) {
	return false, f.err
}

func requestWithAuditUser(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	user := &models.User{ID: 991, Name: "Failure Audit Test", Role: models.RoleAdmin}
	return req.WithContext(context.WithValue(req.Context(), userContextKey, user))
}

func latestAuditForAction(t *testing.T, action string) models.AuditLog {
	t.Helper()
	entries, _, err := db.NewAuditRepository(db.Instance()).List(db.AuditFilter{Action: action, Resource: "system"}, 1, 0)
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	return entries[0]
}

func TestSwapResetFailureIsPersistedToAudit(t *testing.T) {
	req := requestWithAuditUser(http.MethodPost, "/api/system/actions/swap-reset")
	rec := httptest.NewRecorder()

	handleSwapReset(failingSystemActions{err: systemactions.ErrInsufficientMemory}).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	entry := latestAuditForAction(t, "swap_reset")
	if entry.Details != "swap reset failed: insufficient available memory" {
		t.Fatalf("audit details = %q", entry.Details)
	}
}

func TestRemoteHostActionFailureIsPersistedToAudit(t *testing.T) {
	req := requestWithAuditUser(http.MethodPost, "/api/nodes/unknown/actions/memory-optimize")
	req.SetPathValue("id", "unknown")
	req.SetPathValue("action", "memory-optimize")
	rec := httptest.NewRecorder()

	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	handleRemoteNodeAction(hub).ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, want a rejected remote action", rec.Code)
	}
	entry := latestAuditForAction(t, "remote_system_action")
	if entry.Details != "unknown: memory-optimize failed: agent hub: record not found" {
		t.Fatalf("audit details = %q", entry.Details)
	}
}

func TestRemoteHostActionRunsThroughAdvertisedAgentCapability(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: "host-action-agent", Name: "Host Action Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat("host-action-agent", registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          "host-action-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityHostAction},
		Hostname:        "agent.example",
		SentAt:          time.Now().UTC(),
		Inventory: agenthub.Inventory{
			MemoryTotal:       4 << 30,
			MemoryAvailable:   2 << 30,
			SwapTotal:         1 << 30,
			SwapUsed:          128 << 20,
			SwapFree:          896 << 20,
			SwapResetEligible: true,
		},
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			task, pollErr := hub.PollTask("host-action-agent", registered.Token)
			if pollErr != nil {
				done <- pollErr
				return
			}
			if task == nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			_, completeErr := hub.CompleteTask("host-action-agent", registered.Token, task.ID, agenthub.TaskResultRequest{
				Status: agenthub.TaskStatusCompleted,
				Result: map[string]string{"action": "swap-reset", "message": "Swap reset completed"},
			})
			done <- completeErr
			return
		}
		done <- errors.New("agent did not receive task")
	}()

	req := requestWithAuditUser(http.MethodPost, "/api/nodes/host-action-agent/actions/swap-reset")
	req.SetPathValue("id", "host-action-agent")
	req.SetPathValue("action", "swap-reset")
	rec := httptest.NewRecorder()
	handleRemoteNodeAction(hub).ServeHTTP(rec, req)
	if err := <-done; err != nil {
		t.Fatalf("agent completion: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response["message"] != "Swap reset completed" {
		t.Fatalf("response = %#v, error=%v", response, err)
	}

	memoryReq := httptest.NewRequest(http.MethodGet, "/api/nodes/host-action-agent/memory", nil)
	memoryReq.SetPathValue("id", "host-action-agent")
	memoryRec := httptest.NewRecorder()
	handleRemoteNodeMemory(hub).ServeHTTP(memoryRec, memoryReq)
	if memoryRec.Code != http.StatusOK || !strings.Contains(memoryRec.Body.String(), `"swap_used_bytes":134217728`) {
		t.Fatalf("memory status = %d, body=%s", memoryRec.Code, memoryRec.Body.String())
	}
}

func TestRemoteProcessInventoryAndSignalRunThroughAgent(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: "process-agent", Name: "Process Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat("process-agent", registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          "process-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityProcessSignal},
		Hostname:        "process.example",
		SentAt:          time.Now().UTC(),
		Inventory: agenthub.Inventory{Processes: []agenthub.Process{{
			PID: 42, StartTime: 987654, User: "app", CPU: 3.2, Memory: 1.5, RSS: 1 << 20, Command: "/usr/bin/php worker",
		}}},
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/nodes/process-agent/processes", nil)
	listReq.SetPathValue("id", "process-agent")
	listRec := httptest.NewRecorder()
	handleRemoteNodeProcesses(hub).ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"startTime":987654`) {
		t.Fatalf("process list status = %d, body=%s", listRec.Code, listRec.Body.String())
	}

	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			task, pollErr := hub.PollTask("process-agent", registered.Token)
			if pollErr != nil {
				done <- pollErr
				return
			}
			if task == nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if task.Kind != agenthub.TaskProcessSignal || task.Payload["pid"] != "42" || task.Payload["start_time"] != "987654" {
				done <- errors.New("unexpected process task")
				return
			}
			_, completeErr := hub.CompleteTask("process-agent", registered.Token, task.ID, agenthub.TaskResultRequest{
				Status: agenthub.TaskStatusCompleted,
				Result: map[string]string{"message": "TERM stopped worker", "exited": "true", "confirmed": "true"},
			})
			done <- completeErr
			return
		}
		done <- errors.New("agent did not receive process task")
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/process-agent/processes/signal", strings.NewReader(`{"pid":42,"startTime":987654,"signal":"term"}`))
	req.SetPathValue("id", "process-agent")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &models.User{ID: 992, Name: "Process Test", Role: models.RoleAdmin}))
	rec := httptest.NewRecorder()
	handleRemoteNodeProcessSignal(hub).ServeHTTP(rec, req)
	if err := <-done; err != nil {
		t.Fatalf("agent completion: %v", err)
	}
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"exited":true`) {
		t.Fatalf("signal status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuditHostActionFailureIgnoresNilError(t *testing.T) {
	req := requestWithAuditUser(http.MethodPost, "/api/system/actions/test")
	before, _, err := db.NewAuditRepository(db.Instance()).List(db.AuditFilter{Action: "nil_failure", Resource: "system"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	auditHostActionFailure(req, "nil_failure", "should not persist", nil)
	after, _, err := db.NewAuditRepository(db.Instance()).List(db.AuditFilter{Action: "nil_failure", Resource: "system"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("nil error added an audit entry: before=%d after=%d", len(before), len(after))
	}
}

var _ systemActionExecutor = failingSystemActions{err: errors.New("test")}
