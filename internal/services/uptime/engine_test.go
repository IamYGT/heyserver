package uptime

import (
	"context"
	"errors"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/store"
)

func TestEngineCheckNowPersistsImmediatelyAndRejectsPausedMonitor(t *testing.T) {
	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	monitor := createTestMonitor(t, repo, 1)
	monitor.Type = "invalid"
	if err := repo.UpdateMonitor(monitor); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
	engine := &Engine{
		repo: repo, stateManager: sm, batcher: NewHeartbeatBatcher(repo),
		workers: make(map[int64]context.CancelFunc),
	}

	result, err := engine.CheckNow(monitor.ID)
	if err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	if result.Status != StatusDown || result.MonitorID != monitor.ID {
		t.Fatalf("result = %#v", result)
	}
	heartbeats, err := repo.ListHeartbeats(monitor.ID, 10)
	if err != nil || len(heartbeats) != 1 {
		t.Fatalf("heartbeats=%#v err=%v", heartbeats, err)
	}
	state, err := repo.GetState(monitor.ID)
	if err != nil || state == nil || state.LastCheckAt == "" {
		t.Fatalf("state=%#v err=%v", state, err)
	}

	if err := repo.SetMonitorActive(monitor.ID, false); err != nil {
		t.Fatalf("SetMonitorActive: %v", err)
	}
	if _, err := engine.CheckNow(monitor.ID); !errors.Is(err, ErrMonitorPaused) {
		t.Fatalf("paused error = %v", err)
	}
	if _, err := engine.CheckNow(999999); !errors.Is(err, ErrMonitorNotFound) {
		t.Fatalf("missing error = %v", err)
	}
}

func TestAddMonitor_nilAppCtxDoesNotPanic(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	channelRepo, err := store.NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settingsSvc := settings.New(store.NewSettingsRepository(db), "test")
	engine := NewEngine(repo, channelRepo, settingsSvc)

	m := createTestMonitor(t, repo, 1)
	engine.AddMonitor(context.Background(), m)
}

func TestUpdateMonitor_nilAppCtxDoesNotPanic(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	channelRepo, err := store.NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settingsSvc := settings.New(store.NewSettingsRepository(db), "test")
	engine := NewEngine(repo, channelRepo, settingsSvc)

	m := createTestMonitor(t, repo, 1)
	engine.UpdateMonitor(context.Background(), m)
}
