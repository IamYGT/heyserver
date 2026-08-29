package store

import (
	"database/sql"
	"fmt"
	"time"
)

// ── Uptime Stats ─────────────────────────────────────────────────────────────

func (r *UptimeRepository) UptimePercent(monitorID int64, periodHours int) (float64, error) {
	var total, up int
	err := r.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END), 0)
		FROM uptime_heartbeats
		WHERE monitor_id = ? AND created_at >= strftime('%Y-%m-%dT%H:%M:%SZ', 'now', ? || ' hours')
	`, monitorID, fmt.Sprintf("-%d", periodHours)).Scan(&total, &up)
	if err != nil || total == 0 {
		return 100.0, err
	}
	return float64(up) / float64(total) * 100.0, nil
}

func (r *UptimeRepository) AvgPing(monitorID int64, periodHours int) (float64, error) {
	var avg sql.NullFloat64
	err := r.db.QueryRow(`
		SELECT AVG(ping_ms) FROM uptime_heartbeats
		WHERE monitor_id = ? AND status = 1 AND ping_ms IS NOT NULL
			AND created_at >= strftime('%Y-%m-%dT%H:%M:%SZ', 'now', ? || ' hours')
	`, monitorID, fmt.Sprintf("-%d", periodHours)).Scan(&avg)
	if avg.Valid {
		return avg.Float64, err
	}
	return 0, err
}

// ── Retention ────────────────────────────────────────────────────────────────

func (r *UptimeRepository) CompactHeartbeats(olderThanDays int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -olderThanDays).Format(time.RFC3339)

	// Insert hourly aggregates
	_, err := r.db.Exec(`
		INSERT OR REPLACE INTO uptime_heartbeats_hourly
			(monitor_id, hour_bucket, total_checks, up_checks, down_checks, avg_ping_ms, min_ping_ms, max_ping_ms)
		SELECT monitor_id,
			strftime('%Y-%m-%dT%H:00:00Z', created_at) AS hour_bucket,
			COUNT(*), SUM(CASE WHEN status=1 THEN 1 ELSE 0 END), SUM(CASE WHEN status=0 THEN 1 ELSE 0 END),
			AVG(ping_ms), MIN(ping_ms), MAX(ping_ms)
		FROM uptime_heartbeats
		WHERE created_at < ?
		GROUP BY monitor_id, hour_bucket
	`, cutoff)
	if err != nil {
		return 0, err
	}

	// Delete compacted raw heartbeats
	res, err := r.db.Exec(`DELETE FROM uptime_heartbeats WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *UptimeRepository) PruneHourlyAggregates(olderThanDays int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -olderThanDays).Format(time.RFC3339)
	res, err := r.db.Exec(`DELETE FROM uptime_heartbeats_hourly WHERE hour_bucket < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
