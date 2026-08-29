package store

import (
	"database/sql"
	"time"
)

// ── Incident Operations ──────────────────────────────────────────────────────

func (r *UptimeRepository) CreateIncident(inc *UptimeIncident) error {
	// Treat an already-open incident as the same lifecycle instead of creating a
	// duplicate. The partial unique index is the final concurrency guard.
	var existingID int64
	var existingStartedAt string
	err := r.db.QueryRow(`
		SELECT id, started_at FROM uptime_incidents
		WHERE monitor_id = ? AND resolved_at IS NULL
		ORDER BY started_at DESC, id DESC LIMIT 1
	`, inc.MonitorID).Scan(&existingID, &existingStartedAt)
	if err == nil {
		inc.ID = existingID
		inc.StartedAt = existingStartedAt
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.Exec(`
		INSERT INTO uptime_incidents (monitor_id, type, cause, started_at)
		VALUES (?,?,?,?)
	`, inc.MonitorID, inc.Type, nullStr(inc.Cause), now)
	if err != nil {
		return err
	}
	inc.ID, _ = res.LastInsertId()
	inc.StartedAt = now
	return nil
}

// ResolveOpenIncidents closes every unresolved incident for a monitor. This is
// intentionally idempotent so a successful check also repairs orphaned legacy
// incidents whose active pointer was lost.
func (r *UptimeRepository) ResolveOpenIncidents(monitorID int64) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.Exec(`
		UPDATE uptime_incidents SET resolved_at = ?,
			duration_secs = MAX(0, CAST((julianday(?) - julianday(started_at)) * 86400 AS INTEGER))
		WHERE monitor_id = ? AND resolved_at IS NULL
	`, now, now, monitorID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *UptimeRepository) ResolveIncident(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.Exec(`
		UPDATE uptime_incidents SET resolved_at = ?,
			duration_secs = CAST((julianday(?) - julianday(started_at)) * 86400 AS INTEGER)
		WHERE id = ?
	`, now, now, id)
	return err
}

func (r *UptimeRepository) ListIncidents(monitorID int64, limit int) ([]UptimeIncident, error) {
	q := `SELECT i.id, i.monitor_id, m.name, i.type, i.cause, i.started_at, i.resolved_at, i.duration_secs
		FROM uptime_incidents i
		JOIN uptime_monitors m ON m.id = i.monitor_id`
	var args []interface{}
	if monitorID > 0 {
		q += ` WHERE i.monitor_id = ?`
		args = append(args, monitorID)
	}
	// Keep unresolved incidents visible even when a monitor has enough history
	// to fill the requested page. The UI can then report the complete active set
	// instead of silently hiding old but still-open incidents.
	q += ` ORDER BY (i.resolved_at IS NULL) DESC, i.started_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UptimeIncident
	for rows.Next() {
		var inc UptimeIncident
		var cause, resolvedAt sql.NullString
		var dur sql.NullInt64
		if err := rows.Scan(&inc.ID, &inc.MonitorID, &inc.MonitorName, &inc.Type, &cause, &inc.StartedAt, &resolvedAt, &dur); err != nil {
			return nil, err
		}
		if cause.Valid {
			inc.Cause = cause.String
		}
		if resolvedAt.Valid {
			inc.ResolvedAt = resolvedAt.String
		}
		if dur.Valid {
			d := int(dur.Int64)
			inc.DurationSecs = &d
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

func (r *UptimeRepository) ActiveIncidents() ([]UptimeIncident, error) {
	return r.listIncidentsWhere("i.resolved_at IS NULL", 100)
}

func (r *UptimeRepository) listIncidentsWhere(where string, limit int) ([]UptimeIncident, error) {
	q := `SELECT i.id, i.monitor_id, m.name, i.type, i.cause, i.started_at, i.resolved_at, i.duration_secs
		FROM uptime_incidents i JOIN uptime_monitors m ON m.id = i.monitor_id
		WHERE ` + where + ` ORDER BY i.started_at DESC LIMIT ?`
	rows, err := r.db.Query(q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UptimeIncident
	for rows.Next() {
		var inc UptimeIncident
		var cause, resolvedAt sql.NullString
		var dur sql.NullInt64
		if err := rows.Scan(&inc.ID, &inc.MonitorID, &inc.MonitorName, &inc.Type, &cause, &inc.StartedAt, &resolvedAt, &dur); err != nil {
			return nil, err
		}
		if cause.Valid {
			inc.Cause = cause.String
		}
		if resolvedAt.Valid {
			inc.ResolvedAt = resolvedAt.String
		}
		if dur.Valid {
			d := int(dur.Int64)
			inc.DurationSecs = &d
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}
