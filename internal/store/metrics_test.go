package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

func openMetricsDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateMetrics(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMetricsRepository_InsertAndQueryHistory(t *testing.T) {
	t.Parallel()
	db := openMetricsDB(t)
	repo := store.NewMetricsRepository(db)

	row := &store.MetricRow{
		CPUPercent:  42.5,
		MemTotal:    8_000_000_000,
		MemUsed:     4_000_000_000,
		MemPercent:  50,
		Load1:       1.2,
		DiskRootPct: 55,
	}
	if err := repo.InsertMetric(row); err != nil {
		t.Fatal(err)
	}

	raw, agg, err := repo.QueryHistory(30 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("expected raw metric rows")
	}
	if raw[0].CPUPercent != 42.5 {
		t.Errorf("cpu = %v", raw[0].CPUPercent)
	}
	if agg != nil && len(agg) > 0 {
		t.Errorf("unexpected aggregates for 30m window: %d", len(agg))
	}
}

func TestMetricsRepository_ProcessSnapshot(t *testing.T) {
	t.Parallel()
	db := openMetricsDB(t)
	repo := store.NewMetricsRepository(db)
	ts := time.Now().UTC().Format(time.RFC3339)
	procs := []store.ProcessSnapshotRow{
		{PID: 1, Username: "root", CPUPercent: 5, MemPercent: 2, RSS: 1024, Command: "hserver"},
	}
	if err := repo.InsertProcessBatch(ts, procs); err != nil {
		t.Fatal(err)
	}
	got, err := repo.QueryProcessSnapshot(ts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Command != "hserver" {
		t.Errorf("snapshot = %+v", got)
	}
}
