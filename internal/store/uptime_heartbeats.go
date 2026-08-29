package store

import (
	"database/sql"
	"fmt"
	"time"
)

// ── Heartbeat Operations ─────────────────────────────────────────────────────

func (r *UptimeRepository) InsertHeartbeat(h *UptimeHeartbeat) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.Exec(`
		INSERT INTO uptime_heartbeats (monitor_id, status, msg, ping_ms, status_code, tls_expiry, created_at)
		VALUES (?,?,?,?,?,?,?)
	`, h.MonitorID, h.Status, nullStr(h.Msg), h.PingMs, h.StatusCode, nullStr(h.TLSExpiry), now)
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	h.CreatedAt = now
	return nil
}

func (r *UptimeRepository) InsertHeartbeatBatch(heartbeats []UptimeHeartbeat) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO uptime_heartbeats (monitor_id, status, msg, ping_ms, status_code, tls_expiry, created_at)
		VALUES (?,?,?,?,?,?,?)
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, h := range heartbeats {
		if _, err := stmt.Exec(h.MonitorID, h.Status, nullStr(h.Msg), h.PingMs, h.StatusCode, nullStr(h.TLSExpiry), h.CreatedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (r *UptimeRepository) ListHeartbeats(monitorID int64, hours int) ([]UptimeHeartbeat, error) {
	rows, err := r.db.Query(`
		SELECT id, monitor_id, status, msg, ping_ms, status_code, tls_expiry, created_at
		FROM uptime_heartbeats
		WHERE monitor_id = ? AND created_at >= strftime('%Y-%m-%dT%H:%M:%SZ', 'now', ? || ' hours')
		ORDER BY created_at DESC
	`, monitorID, fmt.Sprintf("-%d", hours))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UptimeHeartbeat
	for rows.Next() {
		var h UptimeHeartbeat
		var msg, tlsExpiry sql.NullString
		var pingMs sql.NullFloat64
		var statusCode sql.NullInt64
		if err := rows.Scan(&h.ID, &h.MonitorID, &h.Status, &msg, &pingMs, &statusCode, &tlsExpiry, &h.CreatedAt); err != nil {
			return nil, err
		}
		if msg.Valid { h.Msg = msg.String }
		if pingMs.Valid { v := pingMs.Float64; h.PingMs = &v }
		if statusCode.Valid { v := int(statusCode.Int64); h.StatusCode = &v }
		if tlsExpiry.Valid { h.TLSExpiry = tlsExpiry.String }
		out = append(out, h)
	}
	return out, rows.Err()
}

// ── Daily Stats ──────────────────────────────────────────────────────────────

// DailyHeartbeatResult holds per-day heartbeat counts for a monitor.
type DailyHeartbeatResult struct {
	Day   string // "2006-01-02"
	Total int
	Up    int
}

// DailyHeartbeatStats returns per-day totals for the last N days from raw heartbeats.
func (r *UptimeRepository) DailyHeartbeatStats(monitorID int64, days int) ([]DailyHeartbeatResult, error) {
	rows, err := r.db.Query(`
		SELECT strftime('%Y-%m-%d', created_at) AS day,
		       COUNT(*) AS total,
		       COALESCE(SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END), 0) AS up
		FROM uptime_heartbeats
		WHERE monitor_id = ?
		  AND created_at >= strftime('%Y-%m-%dT%H:%M:%SZ', 'now', ? || ' days')
		GROUP BY day
		ORDER BY day ASC
	`, monitorID, fmt.Sprintf("-%d", days))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []DailyHeartbeatResult
	for rows.Next() {
		var r DailyHeartbeatResult
		if err := rows.Scan(&r.Day, &r.Total, &r.Up); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
