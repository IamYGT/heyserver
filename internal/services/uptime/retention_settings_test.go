package uptime

import (
	"database/sql"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/store"
)

func TestRetentionWorker_UsesConfiguredRetentionSettings(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	monitor := createTestMonitor(t, repo, 1)
	settingsSvc := settings.New(store.NewSettingsRepository(db), "test")
	if err := settingsSvc.Set("uptime_compact_after_days", "5"); err != nil {
		t.Fatalf("set compact-after setting: %v", err)
	}
	if err := settingsSvc.Set("uptime_retention_days", "8"); err != nil {
		t.Fatalf("set retention setting: %v", err)
	}

	// The six-day raw heartbeat is past the configured compaction boundary,
	// while the two-day heartbeat must stay raw. The nine-day hourly aggregate
	// is past the configured pruning boundary.
	insertRetentionHeartbeat(t, db, monitor.ID, 6)
	insertRetentionHeartbeat(t, db, monitor.ID, 2)
	insertRetentionHourlyAggregate(t, db, monitor.ID, 9)

	NewRetentionWorker(repo, settingsSvc).compact()

	if got := retentionRowCount(t, db, "uptime_heartbeats"); got != 1 {
		t.Fatalf("raw heartbeat count = %d, want configured compaction to leave only the two-day row", got)
	}
	if got := retentionRowCount(t, db, "uptime_heartbeats_hourly"); got != 1 {
		t.Fatalf("hourly aggregate count = %d, want the compacted six-day row after pruning the nine-day row", got)
	}
}

func TestRetentionWorker_InvalidPersistedSettingsUseFallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		compactAfterDays   string
		retentionDays      string
		rawHeartbeatAge    int
		hourlyAggregateAge int
		wantRaw            int
		wantHourly         int
	}{
		{
			name:             "compact below minimum",
			compactAfterDays: "0",
			retentionDays:    "90",
			rawHeartbeatAge:  10,
			wantRaw:          1,
		},
		{
			name:             "compact non numeric",
			compactAfterDays: "not-a-number",
			retentionDays:    "90",
			rawHeartbeatAge:  40,
			wantHourly:       1,
		},
		{
			name:             "compact above maximum",
			compactAfterDays: "366",
			retentionDays:    "90",
			rawHeartbeatAge:  40,
			wantHourly:       1,
		},
		{
			name:               "retention below minimum",
			compactAfterDays:   "30",
			retentionDays:      "1",
			hourlyAggregateAge: 40,
			wantHourly:         1,
		},
		{
			name:               "retention non numeric",
			compactAfterDays:   "30",
			retentionDays:      "not-a-number",
			hourlyAggregateAge: 100,
		},
		{
			name:               "retention above maximum",
			compactAfterDays:   "30",
			retentionDays:      "3651",
			hourlyAggregateAge: 100,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db, repo := openUptimeTestDB(t)
			monitor := createTestMonitor(t, repo, 1)
			settingsSvc := settings.New(store.NewSettingsRepository(db), "test")
			if err := settingsSvc.Set("uptime_compact_after_days", tc.compactAfterDays); err != nil {
				t.Fatalf("set compact-after setting: %v", err)
			}
			if err := settingsSvc.Set("uptime_retention_days", tc.retentionDays); err != nil {
				t.Fatalf("set retention setting: %v", err)
			}

			if tc.rawHeartbeatAge > 0 {
				insertRetentionHeartbeat(t, db, monitor.ID, tc.rawHeartbeatAge)
			}
			if tc.hourlyAggregateAge > 0 {
				insertRetentionHourlyAggregate(t, db, monitor.ID, tc.hourlyAggregateAge)
			}

			NewRetentionWorker(repo, settingsSvc).compact()

			if got := retentionRowCount(t, db, "uptime_heartbeats"); got != tc.wantRaw {
				t.Errorf("raw heartbeat count = %d, want %d", got, tc.wantRaw)
			}
			if got := retentionRowCount(t, db, "uptime_heartbeats_hourly"); got != tc.wantHourly {
				t.Errorf("hourly aggregate count = %d, want %d", got, tc.wantHourly)
			}
		})
	}
}

func TestRetentionWorker_InvalidRelationUsesSafeDefaults(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	monitor := createTestMonitor(t, repo, 1)
	settingsSvc := settings.New(store.NewSettingsRepository(db), "test")
	if err := settingsSvc.Set("uptime_compact_after_days", "60"); err != nil {
		t.Fatalf("set compact-after setting: %v", err)
	}
	if err := settingsSvc.Set("uptime_retention_days", "30"); err != nil {
		t.Fatalf("set retention setting: %v", err)
	}

	// Both values are individually valid, but compacting after retention would
	// destroy the only copy of data. The worker must use its safe 30/90 defaults.
	insertRetentionHeartbeat(t, db, monitor.ID, 45)
	insertRetentionHourlyAggregate(t, db, monitor.ID, 60)

	NewRetentionWorker(repo, settingsSvc).compact()

	if got := retentionRowCount(t, db, "uptime_heartbeats"); got != 0 {
		t.Fatalf("raw heartbeat count = %d, want the 45-day row compacted by the safe 30-day default", got)
	}
	if got := retentionRowCount(t, db, "uptime_heartbeats_hourly"); got != 2 {
		t.Fatalf("hourly aggregate count = %d, want both aggregates retained by the safe 90-day default", got)
	}
}

func insertRetentionHeartbeat(t *testing.T, db *sql.DB, monitorID int64, ageDays int) {
	t.Helper()

	createdAt := time.Now().UTC().AddDate(0, 0, -ageDays).Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO uptime_heartbeats
			(monitor_id, status, msg, ping_ms, status_code, tls_expiry, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, monitorID, StatusUp, "retention test", 10, 200, nil, createdAt)
	if err != nil {
		t.Fatalf("insert heartbeat: %v", err)
	}
}

func insertRetentionHourlyAggregate(t *testing.T, db *sql.DB, monitorID int64, ageDays int) {
	t.Helper()

	hourBucket := time.Now().UTC().AddDate(0, 0, -ageDays).Truncate(time.Hour).Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO uptime_heartbeats_hourly
			(monitor_id, hour_bucket, total_checks, up_checks, down_checks, avg_ping_ms, min_ping_ms, max_ping_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, monitorID, hourBucket, 1, 1, 0, 10, 10, 10)
	if err != nil {
		t.Fatalf("insert hourly aggregate: %v", err)
	}
}

func retentionRowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	return count
}
