package uptime

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/store"
)

func openUptimeTestDB(t *testing.T) (*sql.DB, *store.UptimeRepository) {
	t.Helper()

	// A plain :memory: database is private to each database/sql connection. The
	// worker tests use a goroutine, so a second connection could observe an empty
	// schema and make the full release suite flaky. A per-test file mirrors the
	// production connection boundary while remaining isolated.
	databasePath := filepath.Join(t.TempDir(), "uptime.db")
	db, err := sql.Open("sqlite3", databasePath+"?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, migrate := range []func(*sql.DB) error{
		store.MigrateUptime,
		store.MigrateNotify,
		store.MigrateSettings,
	} {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	return db, store.NewUptimeRepository(db)
}

func newTestStateManager(t *testing.T, db *sql.DB, repo *store.UptimeRepository) *StateManager {
	t.Helper()

	channelRepo, err := store.NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settingsRepo := store.NewSettingsRepository(db)
	settingsSvc := settings.New(settingsRepo, "test")
	alerter := NewAlerter(channelRepo, settingsSvc)
	return NewStateManager(repo, alerter)
}

func createTestMonitor(t *testing.T, repo *store.UptimeRepository, retries int) *store.UptimeMonitor {
	t.Helper()

	m := &store.UptimeMonitor{
		Name:         "api",
		Type:         "http",
		URL:          "https://example.com",
		Retries:      retries,
		IntervalSecs: 60,
		IsActive:     true,
	}
	if err := repo.CreateMonitor(m); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return m
}

func setMonitorState(t *testing.T, repo *store.UptimeRepository, monitorID int64, status, fails, ups int, lastAlert string) {
	t.Helper()

	state, err := repo.GetState(monitorID)
	if err != nil || state == nil {
		t.Fatalf("GetState: %v", err)
	}
	state.CurrentStatus = status
	state.ConsecutiveFails = fails
	state.ConsecutiveUps = ups
	state.LastAlertAt = lastAlert
	if err := repo.UpdateState(state); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
}

func TestStateManager_Transition_pendingToUp(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 1)

	sm.Transition(m, CheckResult{MonitorID: m.ID, Status: StatusUp, Msg: "OK"})

	state, err := repo.GetState(m.ID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.CurrentStatus != StatusUp {
		t.Errorf("CurrentStatus = %d, want UP", state.CurrentStatus)
	}
	if state.ConsecutiveUps != 1 {
		t.Errorf("ConsecutiveUps = %d, want 1", state.ConsecutiveUps)
	}
	if state.ConsecutiveFails != 0 {
		t.Errorf("ConsecutiveFails = %d, want 0", state.ConsecutiveFails)
	}
}

func TestStateManager_Transition_downAfterRetries(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 2)

	setMonitorState(t, repo, m.ID, StatusUp, 0, 0, "")

	sm.Transition(m, CheckResult{MonitorID: m.ID, Status: StatusDown, Msg: "timeout"})
	state, _ := repo.GetState(m.ID)
	if state.CurrentStatus == StatusDown {
		t.Fatal("should not be DOWN after single fail with retries=2")
	}

	sm.Transition(m, CheckResult{MonitorID: m.ID, Status: StatusDown, Msg: "timeout"})
	state, err := repo.GetState(m.ID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.CurrentStatus != StatusDown {
		t.Errorf("CurrentStatus = %d, want DOWN", state.CurrentStatus)
	}
	if state.ConsecutiveFails != 2 {
		t.Errorf("ConsecutiveFails = %d, want 2", state.ConsecutiveFails)
	}
}

func TestStateManager_Transition_recoveryFromDown(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 1)

	inc := &store.UptimeIncident{MonitorID: m.ID, Type: "down", Cause: "offline"}
	if err := repo.CreateIncident(inc); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	state, _ := repo.GetState(m.ID)
	state.CurrentStatus = StatusDown
	state.ActiveIncidentID = &inc.ID
	state.ConsecutiveFails = 3
	if err := repo.UpdateState(state); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	sm.Transition(m, CheckResult{MonitorID: m.ID, Status: StatusUp, Msg: "recovered"})

	after, err := repo.GetState(m.ID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if after.CurrentStatus != StatusUp {
		t.Errorf("CurrentStatus = %d, want UP", after.CurrentStatus)
	}
	if after.ActiveIncidentID != nil {
		t.Errorf("ActiveIncidentID = %v, want nil", after.ActiveIncidentID)
	}
}

func TestStateManager_Transition_tlsWarnTreatedAsUp(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 1)

	sm.Transition(m, CheckResult{MonitorID: m.ID, Status: StatusTLSWarn, Msg: "cert expiring"})

	state, err := repo.GetState(m.ID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.CurrentStatus != StatusTLSWarn {
		t.Errorf("CurrentStatus = %d, want TLSWarn", state.CurrentStatus)
	}
	if state.ConsecutiveUps != 1 {
		t.Errorf("ConsecutiveUps = %d, want 1", state.ConsecutiveUps)
	}
}

func TestStateManager_Transition_successRepairsStaleOpenIncident(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 1)
	setMonitorState(t, repo, m.ID, StatusUp, 0, 1, "")

	inc := &store.UptimeIncident{MonitorID: m.ID, Type: "down", Cause: "legacy stale incident"}
	if err := repo.CreateIncident(inc); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	sm.Transition(m, CheckResult{MonitorID: m.ID, Status: StatusUp, Msg: "OK"})

	active, err := repo.ActiveIncidents()
	if err != nil {
		t.Fatalf("ActiveIncidents: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active incidents = %+v, want none", active)
	}
	state, err := repo.GetState(m.ID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ActiveIncidentID != nil {
		t.Fatalf("ActiveIncidentID = %v, want nil", state.ActiveIncidentID)
	}
}

func TestStateManager_Transition_reminderWhenStillDown(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)
	m := createTestMonitor(t, repo, 1)
	m.AlertReminderMins = 1
	if err := repo.UpdateMonitor(m); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}

	oldAlert := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	setMonitorState(t, repo, m.ID, StatusDown, 5, 0, oldAlert)

	sm.Transition(m, CheckResult{MonitorID: m.ID, Status: StatusDown, Msg: "still down"})

	state, err := repo.GetState(m.ID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.LastAlertAt == oldAlert {
		t.Errorf("LastAlertAt unchanged %q, expected reminder refresh", state.LastAlertAt)
	}
}

func TestStateManager_Transition_missingStateNoPanic(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	sm := newTestStateManager(t, db, repo)

	m := &store.UptimeMonitor{ID: 9999, Retries: 1}
	sm.Transition(m, CheckResult{MonitorID: 9999, Status: StatusDown, Msg: "fail"})
}
