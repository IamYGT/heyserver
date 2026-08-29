package store_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/IamYGT/heyserver/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

func TestChannelIDsJSONContractUsesArrayAndAcceptsLegacyString(t *testing.T) {
	monitor := store.UptimeMonitor{AlertChannelIDs: store.ChannelIDs(`[2,5]`)}
	encoded, err := json.Marshal(monitor)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}
	ids, ok := payload["alert_channel_ids"].([]any)
	if !ok || len(ids) != 2 || ids[0] != float64(2) || ids[1] != float64(5) {
		t.Fatalf("alert_channel_ids must be a JSON array: %s", encoded)
	}

	for _, raw := range []string{`{"alert_channel_ids":[3,7]}`, `{"alert_channel_ids":"[3,7]"}`} {
		var decoded store.UptimeMonitor
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			t.Fatalf("Unmarshal %s: %v", raw, err)
		}
		if decoded.AlertChannelIDs != store.ChannelIDs(`[3,7]`) {
			t.Fatalf("decoded IDs = %q", decoded.AlertChannelIDs)
		}
	}
}

func openUptimeDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "uptime.db")
	db, err := sql.Open("sqlite3", "file:"+path+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateUptime(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUptimeRepository_MonitorCRUD(t *testing.T) {
	t.Parallel()
	db := openUptimeDB(t)
	repo := store.NewUptimeRepository(db)

	m := &store.UptimeMonitor{
		Name:                "API Health",
		Type:                "http",
		URL:                 "https://example.com/health",
		Method:              "GET",
		IntervalSecs:        60,
		TimeoutSecs:         30,
		Retries:             1,
		RetryInterval:       30,
		AcceptedStatusCodes: `["200-299"]`,
		TLSCheck:            true,
		TLSExpiryWarnDays:   14,
		IsActive:            true,
		MaxRedirects:        5,
	}
	if err := repo.CreateMonitor(m); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("expected monitor ID")
	}

	got, err := repo.GetMonitor(m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.Name != m.Name || got.URL != m.URL {
		t.Errorf("GetMonitor mismatch: %+v", got)
	}

	st, err := repo.GetState(m.ID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st == nil || st.MonitorID != m.ID {
		t.Fatalf("expected state row for monitor %d", m.ID)
	}

	m.Name = "API Health Updated"
	if err := repo.UpdateMonitor(m); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}

	list, err := repo.ListMonitors()
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(list) != 1 || list[0].Name != "API Health Updated" {
		t.Errorf("ListMonitors = %+v", list)
	}

	if err := repo.SetMonitorActive(m.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetMonitor(m.ID)
	if got.IsActive {
		t.Error("expected monitor inactive")
	}

	if err := repo.DeleteMonitor(m.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListMonitors()
	if len(list) != 0 {
		t.Errorf("expected empty list after delete, got %d", len(list))
	}
}

func TestUptimeRepository_SummaryEmpty(t *testing.T) {
	t.Parallel()
	db := openUptimeDB(t)
	repo := store.NewUptimeRepository(db)
	sum, err := repo.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Up+sum.Down+sum.Paused+sum.Maintenance != 0 {
		t.Errorf("summary counts = %+v, want all zero", sum)
	}
}

func TestMigrateUptime_BackfillsMissingMonitorState(t *testing.T) {
	t.Parallel()
	db := openUptimeDB(t)

	res, err := db.Exec(`INSERT INTO uptime_monitors (name) VALUES (?)`, "legacy monitor")
	if err != nil {
		t.Fatalf("insert legacy monitor: %v", err)
	}
	monitorID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("legacy monitor id: %v", err)
	}

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM uptime_monitor_state WHERE monitor_id = ?`, monitorID).Scan(&before); err != nil {
		t.Fatalf("count state before migration: %v", err)
	}
	if before != 0 {
		t.Fatalf("state rows before migration = %d, want 0", before)
	}

	if err := store.MigrateUptime(db); err != nil {
		t.Fatalf("rerun migration: %v", err)
	}

	state, err := store.NewUptimeRepository(db).GetState(monitorID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state == nil || state.MonitorID != monitorID {
		t.Fatalf("state = %+v, want monitor %d", state, monitorID)
	}
}

func TestMigrateUptime_ReconcilesOpenIncidents(t *testing.T) {
	t.Parallel()
	db := openUptimeDB(t)
	repo := store.NewUptimeRepository(db)

	up := &store.UptimeMonitor{Name: "recovered", Type: "http", URL: "https://up.example", IsActive: true}
	down := &store.UptimeMonitor{Name: "down", Type: "http", URL: "https://down.example", IsActive: true}
	if err := repo.CreateMonitor(up); err != nil {
		t.Fatalf("CreateMonitor(up): %v", err)
	}
	if err := repo.CreateMonitor(down); err != nil {
		t.Fatalf("CreateMonitor(down): %v", err)
	}

	if _, err := db.Exec(`DROP INDEX idx_uptime_one_open_incident`); err != nil {
		t.Fatalf("drop invariant for legacy fixture: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE uptime_monitor_state SET current_status = 1, last_check_at = '2026-01-01T00:10:00Z' WHERE monitor_id = ?;
		UPDATE uptime_monitor_state SET current_status = 0, last_check_at = '2026-01-01T00:10:00Z', active_incident_id = NULL WHERE monitor_id = ?;
	`, up.ID, down.ID); err != nil {
		t.Fatalf("prepare monitor states: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO uptime_incidents (monitor_id, type, cause, started_at) VALUES (?, 'down', 'stale', '2026-01-01T00:00:00Z');
		INSERT INTO uptime_incidents (monitor_id, type, cause, started_at) VALUES (?, 'down', 'older', '2026-01-01T00:01:00Z');
		INSERT INTO uptime_incidents (monitor_id, type, cause, started_at) VALUES (?, 'down', 'current', '2026-01-01T00:05:00Z');
	`, up.ID, down.ID, down.ID); err != nil {
		t.Fatalf("insert legacy incidents: %v", err)
	}

	if err := store.MigrateUptime(db); err != nil {
		t.Fatalf("rerun migration: %v", err)
	}

	var upOpen, downOpen int
	if err := db.QueryRow(`SELECT COUNT(*) FROM uptime_incidents WHERE monitor_id = ? AND resolved_at IS NULL`, up.ID).Scan(&upOpen); err != nil {
		t.Fatalf("count recovered incidents: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM uptime_incidents WHERE monitor_id = ? AND resolved_at IS NULL`, down.ID).Scan(&downOpen); err != nil {
		t.Fatalf("count down incidents: %v", err)
	}
	if upOpen != 0 || downOpen != 1 {
		t.Fatalf("open incidents: recovered=%d down=%d, want 0 and 1", upOpen, downOpen)
	}

	state, err := repo.GetState(down.ID)
	if err != nil {
		t.Fatalf("GetState(down): %v", err)
	}
	if state.ActiveIncidentID == nil {
		t.Fatal("down monitor has no active incident after reconciliation")
	}
	var cause string
	if err := db.QueryRow(`SELECT cause FROM uptime_incidents WHERE id = ?`, *state.ActiveIncidentID).Scan(&cause); err != nil {
		t.Fatalf("read active incident: %v", err)
	}
	if cause != "current" {
		t.Fatalf("active incident cause = %q, want current", cause)
	}

	if _, err := db.Exec(`INSERT INTO uptime_incidents (monitor_id, type, started_at) VALUES (?, 'down', '2026-01-01T00:20:00Z')`, down.ID); err == nil {
		t.Fatal("expected unique open incident invariant to reject duplicate")
	}
}

func TestUptimeRepository_ListIncidentsKeepsOpenRowsFirst(t *testing.T) {
	t.Parallel()
	db := openUptimeDB(t)
	repo := store.NewUptimeRepository(db)
	m := &store.UptimeMonitor{Name: "incident ordering", Type: "http", URL: "https://example.com", IsActive: true}
	if err := repo.CreateMonitor(m); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO uptime_incidents (monitor_id, type, cause, started_at)
		VALUES (?, 'down', 'old but open', '2020-01-01T00:00:00Z')
	`, m.ID); err != nil {
		t.Fatalf("insert open incident: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO uptime_incidents (monitor_id, type, cause, started_at, resolved_at, duration_secs)
		VALUES (?, 'down', 'new but resolved', '2026-01-01T00:00:00Z', '2026-01-01T00:01:00Z', 60)
	`, m.ID); err != nil {
		t.Fatalf("insert resolved incident: %v", err)
	}

	incidents, err := repo.ListIncidents(0, 1)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(incidents) != 1 || incidents[0].Cause != "old but open" || incidents[0].ResolvedAt != "" {
		t.Fatalf("incidents = %+v, want the unresolved row first", incidents)
	}
}
