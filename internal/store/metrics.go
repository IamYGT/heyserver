package store

import (
	"database/sql"
	"fmt"
	"time"
)

// ── Migration ────────────────────────────────────────────────────────────────

// MigrateMetrics creates the system metrics tables in the given database.
// Uses Path B (CREATE TABLE IF NOT EXISTS) — same pattern as uptime.go.
func MigrateMetrics(db *sql.DB) error {
	stmts := []string{
		// Raw system metrics — written every 15 seconds.
		`CREATE TABLE IF NOT EXISTS system_metrics (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp        TEXT NOT NULL,
			cpu_percent      REAL NOT NULL DEFAULT 0,
			memory_total     INTEGER NOT NULL DEFAULT 0,
			memory_used      INTEGER NOT NULL DEFAULT 0,
			memory_percent   REAL NOT NULL DEFAULT 0,
			memory_buffers   INTEGER NOT NULL DEFAULT 0,
			memory_cached    INTEGER NOT NULL DEFAULT 0,
			memory_available INTEGER NOT NULL DEFAULT 0,
			swap_total       INTEGER NOT NULL DEFAULT 0,
			swap_used        INTEGER NOT NULL DEFAULT 0,
			swap_percent     REAL NOT NULL DEFAULT 0,
			load_1m          REAL NOT NULL DEFAULT 0,
			load_5m          REAL NOT NULL DEFAULT 0,
			load_15m         REAL NOT NULL DEFAULT 0,
			net_rx_bytes     INTEGER NOT NULL DEFAULT 0,
			net_tx_bytes     INTEGER NOT NULL DEFAULT 0,
			disk_root_percent REAL NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sys_metrics_ts ON system_metrics(timestamp DESC)`,

		// Minute-level aggregates (compacted from raw after 1 hour).
		`CREATE TABLE IF NOT EXISTS system_metrics_minute (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			minute_bucket    TEXT NOT NULL UNIQUE,
			sample_count     INTEGER NOT NULL DEFAULT 0,
			cpu_avg          REAL, cpu_max REAL,
			mem_avg          REAL, mem_max REAL,
			swap_avg         REAL, swap_max REAL,
			load_1m_avg      REAL,
			net_rx_total     INTEGER, net_tx_total INTEGER,
			disk_root_avg    REAL, disk_root_max REAL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sys_metrics_min ON system_metrics_minute(minute_bucket DESC)`,

		// Hourly aggregates (compacted from minute after 24 hours).
		`CREATE TABLE IF NOT EXISTS system_metrics_hourly (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			hour_bucket      TEXT NOT NULL UNIQUE,
			sample_count     INTEGER NOT NULL DEFAULT 0,
			cpu_avg          REAL, cpu_max REAL,
			mem_avg          REAL, mem_max REAL,
			swap_avg         REAL, swap_max REAL,
			load_1m_avg      REAL,
			net_rx_total     INTEGER, net_tx_total INTEGER,
			disk_root_avg    REAL, disk_root_max REAL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sys_metrics_hr ON system_metrics_hourly(hour_bucket DESC)`,

		// Process snapshots — written every 60 seconds (or 15s during spikes).
		`CREATE TABLE IF NOT EXISTS process_snapshots (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp        TEXT NOT NULL,
			pid              INTEGER NOT NULL,
			username         TEXT NOT NULL DEFAULT '',
			cpu_percent      REAL NOT NULL DEFAULT 0,
			memory_percent   REAL NOT NULL DEFAULT 0,
			rss              INTEGER NOT NULL DEFAULT 0,
			command          TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_proc_snap_ts ON process_snapshots(timestamp DESC)`,

		// Service status history — written every 60 seconds.
		`CREATE TABLE IF NOT EXISTS service_status_history (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp        TEXT NOT NULL,
			name             TEXT NOT NULL,
			status           TEXT NOT NULL,
			pid              INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_svc_hist_ts ON service_status_history(timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_svc_hist_name ON service_status_history(name, timestamp DESC)`,
	}

	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("metrics migration: %w", err)
		}
	}
	return nil
}

// ── Repository ───────────────────────────────────────────────────────────────

// MetricsRepository provides CRUD operations on the metrics database.
type MetricsRepository struct {
	db *sql.DB
}

// NewMetricsRepository wraps the given *sql.DB.
func NewMetricsRepository(db *sql.DB) *MetricsRepository {
	return &MetricsRepository{db: db}
}

// ── System Metrics ───────────────────────────────────────────────────────────

// MetricRow represents a single raw system metric sample.
type MetricRow struct {
	Timestamp    string  `json:"timestamp"`
	CPUPercent   float64 `json:"cpu_percent"`
	MemTotal     uint64  `json:"memory_total"`
	MemUsed      uint64  `json:"memory_used"`
	MemPercent   float64 `json:"memory_percent"`
	MemBuffers   uint64  `json:"memory_buffers"`
	MemCached    uint64  `json:"memory_cached"`
	MemAvailable uint64  `json:"memory_available"`
	SwapTotal    uint64  `json:"swap_total"`
	SwapUsed     uint64  `json:"swap_used"`
	SwapPercent  float64 `json:"swap_percent"`
	Load1        float64 `json:"load_1m"`
	Load5        float64 `json:"load_5m"`
	Load15       float64 `json:"load_15m"`
	NetRX        uint64  `json:"net_rx_bytes"`
	NetTX        uint64  `json:"net_tx_bytes"`
	DiskRootPct  float64 `json:"disk_root_percent"`
}

// InsertMetric writes a single raw metric sample.
func (r *MetricsRepository) InsertMetric(m *MetricRow) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.Exec(`INSERT INTO system_metrics
		(timestamp, cpu_percent, memory_total, memory_used, memory_percent,
		 memory_buffers, memory_cached, memory_available,
		 swap_total, swap_used, swap_percent,
		 load_1m, load_5m, load_15m,
		 net_rx_bytes, net_tx_bytes, disk_root_percent)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		now, m.CPUPercent,
		m.MemTotal, m.MemUsed, m.MemPercent,
		m.MemBuffers, m.MemCached, m.MemAvailable,
		m.SwapTotal, m.SwapUsed, m.SwapPercent,
		m.Load1, m.Load5, m.Load15,
		m.NetRX, m.NetTX, m.DiskRootPct,
	)
	return err
}

// AggregatedRow is used for minute and hourly aggregated data.
type AggregatedRow struct {
	Bucket      string  `json:"bucket"`
	SampleCount int     `json:"sample_count"`
	CPUAvg      float64 `json:"cpu_avg"`
	CPUMax      float64 `json:"cpu_max"`
	MemAvg      float64 `json:"mem_avg"`
	MemMax      float64 `json:"mem_max"`
	SwapAvg     float64 `json:"swap_avg"`
	SwapMax     float64 `json:"swap_max"`
	Load1Avg    float64 `json:"load_1m_avg"`
	NetRXTotal  uint64  `json:"net_rx_total"`
	NetTXTotal  uint64  `json:"net_tx_total"`
	DiskRootAvg float64 `json:"disk_root_avg"`
	DiskRootMax float64 `json:"disk_root_max"`
}

// QueryHistory returns metric history for the given duration.
// It automatically selects the right resolution:
//   - ≤1h   → raw 15s data
//   - ≤24h  → minute aggregates
//   - >24h  → hourly aggregates
func (r *MetricsRepository) QueryHistory(duration time.Duration) ([]MetricRow, []AggregatedRow, error) {
	cutoff := time.Now().UTC().Add(-duration).Format(time.RFC3339)

	if duration <= time.Hour {
		// Raw data
		rows, err := r.db.Query(`SELECT timestamp, cpu_percent, memory_total, memory_used, memory_percent,
			memory_buffers, memory_cached, memory_available,
			swap_total, swap_used, swap_percent, load_1m, load_5m, load_15m,
			net_rx_bytes, net_tx_bytes, disk_root_percent
			FROM system_metrics WHERE timestamp >= ? ORDER BY timestamp ASC`, cutoff)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = rows.Close() }()

		var result []MetricRow
		for rows.Next() {
			var m MetricRow
			if err := rows.Scan(&m.Timestamp, &m.CPUPercent,
				&m.MemTotal, &m.MemUsed, &m.MemPercent,
				&m.MemBuffers, &m.MemCached, &m.MemAvailable,
				&m.SwapTotal, &m.SwapUsed, &m.SwapPercent,
				&m.Load1, &m.Load5, &m.Load15,
				&m.NetRX, &m.NetTX, &m.DiskRootPct); err != nil {
				return nil, nil, err
			}
			result = append(result, m)
		}
		return result, nil, rows.Err()
	}

	// Aggregated data (minute or hourly).
	// Table and column names are selected from a closed allowlist — never from
	// user input — so string concatenation here is safe. The allowlist makes
	// the intent explicit and prevents accidental injection if this code is
	// ever refactored to accept external parameters.
	type aggTable struct{ table, bucketCol string }
	allowed := map[bool]aggTable{
		false: {"system_metrics_minute", "minute_bucket"},
		true:  {"system_metrics_hourly", "hour_bucket"},
	}
	sel := allowed[duration > 24*time.Hour]

	// Build query from allowlisted identifiers only (no user-supplied strings).
	query := `SELECT ` + sel.bucketCol + `, sample_count, cpu_avg, cpu_max, mem_avg, mem_max,
		swap_avg, swap_max, load_1m_avg, net_rx_total, net_tx_total,
		disk_root_avg, disk_root_max
		FROM ` + sel.table + ` WHERE ` + sel.bucketCol + ` >= ? ORDER BY ` + sel.bucketCol + ` ASC`

	rows, err := r.db.Query(query, cutoff)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []AggregatedRow
	for rows.Next() {
		var a AggregatedRow
		if err := rows.Scan(&a.Bucket, &a.SampleCount,
			&a.CPUAvg, &a.CPUMax, &a.MemAvg, &a.MemMax,
			&a.SwapAvg, &a.SwapMax, &a.Load1Avg,
			&a.NetRXTotal, &a.NetTXTotal,
			&a.DiskRootAvg, &a.DiskRootMax); err != nil {
			return nil, nil, err
		}
		result = append(result, a)
	}
	return nil, result, rows.Err()
}

// ── Process Snapshots ────────────────────────────────────────────────────────

// ProcessSnapshotRow represents a single process entry at a point in time.
type ProcessSnapshotRow struct {
	Timestamp  string  `json:"timestamp"`
	PID        int     `json:"pid"`
	Username   string  `json:"username"`
	CPUPercent float64 `json:"cpu_percent"`
	MemPercent float64 `json:"memory_percent"`
	RSS        uint64  `json:"rss"`
	Command    string  `json:"command"`
}

// InsertProcessBatch writes a batch of process snapshots in a single transaction.
func (r *MetricsRepository) InsertProcessBatch(timestamp string, procs []ProcessSnapshotRow) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT INTO process_snapshots
		(timestamp, pid, username, cpu_percent, memory_percent, rss, command)
		VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, p := range procs {
		if _, err := stmt.Exec(timestamp, p.PID, p.Username, p.CPUPercent, p.MemPercent, p.RSS, p.Command); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QueryProcessSnapshot returns the process list closest to the given timestamp.
func (r *MetricsRepository) QueryProcessSnapshot(at string) ([]ProcessSnapshotRow, error) {
	// Find the nearest timestamp
	var nearest string
	err := r.db.QueryRow(`SELECT timestamp FROM process_snapshots
		ORDER BY ABS(julianday(timestamp) - julianday(?)) LIMIT 1`, at).Scan(&nearest)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	rows, err := r.db.Query(`SELECT timestamp, pid, username, cpu_percent, memory_percent, rss, command
		FROM process_snapshots WHERE timestamp = ? ORDER BY cpu_percent DESC`, nearest)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []ProcessSnapshotRow
	for rows.Next() {
		var p ProcessSnapshotRow
		if err := rows.Scan(&p.Timestamp, &p.PID, &p.Username, &p.CPUPercent, &p.MemPercent, &p.RSS, &p.Command); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// QueryProcessTimestamps returns distinct process snapshot timestamps in a range.
func (r *MetricsRepository) QueryProcessTimestamps(duration time.Duration) ([]string, error) {
	cutoff := time.Now().UTC().Add(-duration).Format(time.RFC3339)
	rows, err := r.db.Query(`SELECT DISTINCT timestamp FROM process_snapshots
		WHERE timestamp >= ? ORDER BY timestamp ASC`, cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []string
	for rows.Next() {
		var ts string
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		result = append(result, ts)
	}
	return result, rows.Err()
}

// ── Service Status History ───────────────────────────────────────────────────

// ServiceStatusRow represents a service status entry.
type ServiceStatusRow struct {
	Timestamp string `json:"timestamp"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
}

// InsertServiceBatch writes a batch of service statuses.
func (r *MetricsRepository) InsertServiceBatch(timestamp string, services []ServiceStatusRow) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT INTO service_status_history
		(timestamp, name, status, pid) VALUES (?,?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, s := range services {
		if _, err := stmt.Exec(timestamp, s.Name, s.Status, s.PID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QueryServiceHistory returns service status history for the given duration.
func (r *MetricsRepository) QueryServiceHistory(duration time.Duration) ([]ServiceStatusRow, error) {
	cutoff := time.Now().UTC().Add(-duration).Format(time.RFC3339)
	rows, err := r.db.Query(`SELECT timestamp, name, status, pid
		FROM service_status_history WHERE timestamp >= ?
		ORDER BY timestamp ASC`, cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []ServiceStatusRow
	for rows.Next() {
		var s ServiceStatusRow
		if err := rows.Scan(&s.Timestamp, &s.Name, &s.Status, &s.PID); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

// ── Compaction ───────────────────────────────────────────────────────────────

// CompactToMinute aggregates raw metrics older than 1 hour into minute buckets,
// then deletes the raw records. Returns the number of raw rows deleted.
func (r *MetricsRepository) CompactToMinute() (int64, error) {
	cutoff := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)

	_, err := r.db.Exec(`INSERT OR REPLACE INTO system_metrics_minute
		(minute_bucket, sample_count, cpu_avg, cpu_max, mem_avg, mem_max,
		 swap_avg, swap_max, load_1m_avg, net_rx_total, net_tx_total,
		 disk_root_avg, disk_root_max)
		SELECT
			strftime('%Y-%m-%dT%H:%M:00Z', timestamp) AS bucket,
			COUNT(*),
			AVG(cpu_percent), MAX(cpu_percent),
			AVG(memory_percent), MAX(memory_percent),
			AVG(swap_percent), MAX(swap_percent),
			AVG(load_1m),
			SUM(net_rx_bytes), SUM(net_tx_bytes),
			AVG(disk_root_percent), MAX(disk_root_percent)
		FROM system_metrics
		WHERE timestamp < ?
		GROUP BY bucket`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("compact to minute: %w", err)
	}

	res, err := r.db.Exec(`DELETE FROM system_metrics WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete raw after compact: %w", err)
	}
	return res.RowsAffected()
}

// CompactToHourly aggregates minute data older than 24 hours into hourly buckets,
// then deletes the minute records. Returns the number of minute rows deleted.
func (r *MetricsRepository) CompactToHourly() (int64, error) {
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)

	_, err := r.db.Exec(`INSERT OR REPLACE INTO system_metrics_hourly
		(hour_bucket, sample_count, cpu_avg, cpu_max, mem_avg, mem_max,
		 swap_avg, swap_max, load_1m_avg, net_rx_total, net_tx_total,
		 disk_root_avg, disk_root_max)
		SELECT
			strftime('%Y-%m-%dT%H:00:00Z', minute_bucket) AS bucket,
			SUM(sample_count),
			AVG(cpu_avg), MAX(cpu_max),
			AVG(mem_avg), MAX(mem_max),
			AVG(swap_avg), MAX(swap_max),
			AVG(load_1m_avg),
			SUM(net_rx_total), SUM(net_tx_total),
			AVG(disk_root_avg), MAX(disk_root_max)
		FROM system_metrics_minute
		WHERE minute_bucket < ?
		GROUP BY bucket`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("compact to hourly: %w", err)
	}

	res, err := r.db.Exec(`DELETE FROM system_metrics_minute WHERE minute_bucket < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete minute after compact: %w", err)
	}
	return res.RowsAffected()
}

// PruneOldData deletes hourly data older than maxDays, process snapshots older
// than processDays, and service history older than serviceDays.
func (r *MetricsRepository) PruneOldData(maxDays, processDays, serviceDays int) error {
	hourlyCutoff := time.Now().UTC().AddDate(0, 0, -maxDays).Format(time.RFC3339)
	if _, err := r.db.Exec(`DELETE FROM system_metrics_hourly WHERE hour_bucket < ?`, hourlyCutoff); err != nil {
		return fmt.Errorf("prune hourly: %w", err)
	}

	procCutoff := time.Now().UTC().AddDate(0, 0, -processDays).Format(time.RFC3339)
	if _, err := r.db.Exec(`DELETE FROM process_snapshots WHERE timestamp < ?`, procCutoff); err != nil {
		return fmt.Errorf("prune processes: %w", err)
	}

	svcCutoff := time.Now().UTC().AddDate(0, 0, -serviceDays).Format(time.RFC3339)
	if _, err := r.db.Exec(`DELETE FROM service_status_history WHERE timestamp < ?`, svcCutoff); err != nil {
		return fmt.Errorf("prune service history: %w", err)
	}
	return nil
}

// ── Stats ────────────────────────────────────────────────────────────────────

// MetricsSummary provides quick stats for the dashboard.
type MetricsSummary struct {
	TotalSamples    int64  `json:"total_samples"`
	OldestTimestamp string `json:"oldest_timestamp"`
	NewestTimestamp string `json:"newest_timestamp"`
	DBSizeBytes     int64  `json:"db_size_bytes"`
}

// Summary returns a quick overview of the metrics database.
func (r *MetricsRepository) Summary() (*MetricsSummary, error) {
	var s MetricsSummary

	_ = r.db.QueryRow(`SELECT COUNT(*) FROM system_metrics`).Scan(&s.TotalSamples)

	// Oldest across all tables
	_ = r.db.QueryRow(`SELECT MIN(ts) FROM (
		SELECT MIN(timestamp) AS ts FROM system_metrics
		UNION ALL SELECT MIN(minute_bucket) FROM system_metrics_minute
		UNION ALL SELECT MIN(hour_bucket) FROM system_metrics_hourly
	)`).Scan(&s.OldestTimestamp)

	_ = r.db.QueryRow(`SELECT MAX(timestamp) FROM system_metrics`).Scan(&s.NewestTimestamp)

	// page_count * page_size = total DB size
	_ = r.db.QueryRow(`SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()`).Scan(&s.DBSizeBytes)

	return &s, nil
}
