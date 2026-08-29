package uptime

import (
	"context"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

func TestRunCheck_queuesHeartbeatAndUpdatesState(t *testing.T) {

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 1)
	m.Type = "invalid"
	batcher := NewHeartbeatBatcher(repo)

	runCheck(m, sm, batcher)
	batcher.Flush()

	heartbeats, err := repo.ListHeartbeats(m.ID, 1)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(heartbeats) != 1 {
		t.Fatalf("heartbeat count = %d, want 1", len(heartbeats))
	}
	if heartbeats[0].Status != StatusDown {
		t.Errorf("Status = %d, want DOWN for unreachable http target", heartbeats[0].Status)
	}

	state, err := repo.GetState(m.ID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.LastCheckAt == "" {
		t.Error("state LastCheckAt should be updated")
	}
}

func TestRunCheck_storesPingAndStatusCodeInHeartbeat(t *testing.T) {

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 1)
	batcher := NewHeartbeatBatcher(repo)

	ping := 12.5
	code := 503
	batcher.Add(store.UptimeHeartbeat{
		MonitorID:  m.ID,
		Status:     StatusDown,
		Msg:        "service unavailable",
		TLSExpiry:  "2026-12-01",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		PingMs:     &ping,
		StatusCode: &code,
	})
	batcher.Flush()

	sm.Transition(m, CheckResult{
		MonitorID:  m.ID,
		Status:     StatusDown,
		Msg:        "service unavailable",
		PingMs:     ping,
		StatusCode: code,
		TLSExpiry:  "2026-12-01",
	})

	heartbeats, err := repo.ListHeartbeats(m.ID, 1)
	if err != nil {
		t.Fatalf("ListHeartbeats: %v", err)
	}
	if len(heartbeats) != 1 {
		t.Fatalf("heartbeat count = %d, want 1", len(heartbeats))
	}
	if heartbeats[0].PingMs == nil || *heartbeats[0].PingMs != ping {
		t.Errorf("PingMs = %v, want %v", heartbeats[0].PingMs, ping)
	}
	if heartbeats[0].StatusCode == nil || *heartbeats[0].StatusCode != code {
		t.Errorf("StatusCode = %v, want %v", heartbeats[0].StatusCode, code)
	}
}

func TestMonitorWorker_cancelsDuringJitter(t *testing.T) {

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 1)
	m.IntervalSecs = 3600
	batcher := NewHeartbeatBatcher(repo)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		monitorWorker(ctx, m, sm, batcher)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitorWorker did not exit after context cancel")
	}
}

func TestMonitorWorker_runsFirstCheckBeforeTicker(t *testing.T) {

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 1)
	m.IntervalSecs = 3600
	m.Type = "invalid"
	batcher := NewHeartbeatBatcher(repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go monitorWorker(ctx, m, sm, batcher)

	deadline := time.Now().Add(6 * time.Second)
	for {
		state, err := repo.GetState(m.ID)
		if err != nil {
			t.Fatalf("GetState: %v", err)
		}
		if state.LastCheckAt != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first check did not run before timeout")
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
}
