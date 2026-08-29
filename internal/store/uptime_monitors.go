package store

import (
	"database/sql"
	"time"
)

// ── Monitor CRUD ─────────────────────────────────────────────────────────────

func (r *UptimeRepository) ListMonitors() ([]UptimeMonitor, error) {
	rows, err := r.db.Query(`
		SELECT m.id, m.name, m.type, m.url, m.hostname, m.port, m.method,
			m.interval_secs, m.timeout_secs, m.retries, m.retry_interval,
			m.accepted_statuscodes, m.keyword, m.keyword_invert,
			m.req_headers, m.req_body, m.tls_check, m.tls_expiry_warn_days,
			m.is_active, m.maintenance_mode, m.group_id, m.description,
			m.max_redirects, m.alert_channel_ids, m.alert_reminder_mins,
			m.created_at, m.updated_at,
			s.current_status, s.last_check_at,
			(SELECT CASE WHEN COUNT(*) = 0 THEN NULL
				ELSE CAST(SUM(CASE WHEN h.status = 1 THEN 1 ELSE 0 END) AS REAL) / COUNT(*) * 100
				END FROM uptime_heartbeats h
				WHERE h.monitor_id = m.id
				AND h.created_at >= strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-24 hours')
			) AS uptime_24h,
			(SELECT AVG(h.ping_ms) FROM uptime_heartbeats h
				WHERE h.monitor_id = m.id AND h.status = 1 AND h.ping_ms IS NOT NULL
				AND h.created_at >= strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-24 hours')
			) AS avg_ping_ms
		FROM uptime_monitors m
		LEFT JOIN uptime_monitor_state s ON s.monitor_id = m.id
		ORDER BY m.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var monitors []UptimeMonitor
	for rows.Next() {
		var m UptimeMonitor
		var groupID, port sql.NullInt64
		var url, hostname, keyword, reqHeaders, reqBody, desc, alertChIDs sql.NullString
		var currentStatus sql.NullInt64
		var lastCheckAt sql.NullString
		var uptime24h, avgPingMs sql.NullFloat64

		err := rows.Scan(
			&m.ID, &m.Name, &m.Type, &url, &hostname, &port, &m.Method,
			&m.IntervalSecs, &m.TimeoutSecs, &m.Retries, &m.RetryInterval,
			&m.AcceptedStatusCodes, &keyword, &m.KeywordInvert,
			&reqHeaders, &reqBody, &m.TLSCheck, &m.TLSExpiryWarnDays,
			&m.IsActive, &m.MaintenanceMode, &groupID, &desc,
			&m.MaxRedirects, &alertChIDs, &m.AlertReminderMins,
			&m.CreatedAt, &m.UpdatedAt,
			&currentStatus, &lastCheckAt,
			&uptime24h, &avgPingMs,
		)
		if err != nil {
			return nil, err
		}
		if url.Valid {
			m.URL = url.String
		}
		if hostname.Valid {
			m.Hostname = hostname.String
		}
		if port.Valid {
			m.Port = int(port.Int64)
		}
		if keyword.Valid {
			m.Keyword = keyword.String
		}
		if reqHeaders.Valid {
			m.ReqHeaders = reqHeaders.String
		}
		if reqBody.Valid {
			m.ReqBody = reqBody.String
		}
		if desc.Valid {
			m.Description = desc.String
		}
		if alertChIDs.Valid {
			m.AlertChannelIDs = ChannelIDs(alertChIDs.String)
		}
		if groupID.Valid {
			gid := groupID.Int64
			m.GroupID = &gid
		}
		if currentStatus.Valid {
			cs := int(currentStatus.Int64)
			m.CurrentStatus = &cs
		}
		if lastCheckAt.Valid {
			m.LastCheckAt = &lastCheckAt.String
		}
		if uptime24h.Valid {
			m.Uptime24h = &uptime24h.Float64
		}
		if avgPingMs.Valid {
			m.AvgPingMs = &avgPingMs.Float64
		}

		monitors = append(monitors, m)
	}
	return monitors, rows.Err()
}

func (r *UptimeRepository) GetMonitor(id int64) (*UptimeMonitor, error) {
	var m UptimeMonitor
	var groupID, port sql.NullInt64
	var url, hostname, keyword, reqHeaders, reqBody, desc, alertChIDs sql.NullString
	var currentStatus sql.NullInt64
	var lastCheckAt sql.NullString

	err := r.db.QueryRow(`
		SELECT m.id, m.name, m.type, m.url, m.hostname, m.port, m.method,
			m.interval_secs, m.timeout_secs, m.retries, m.retry_interval,
			m.accepted_statuscodes, m.keyword, m.keyword_invert,
			m.req_headers, m.req_body, m.tls_check, m.tls_expiry_warn_days,
			m.is_active, m.maintenance_mode, m.group_id, m.description,
			m.max_redirects, m.alert_channel_ids, m.alert_reminder_mins,
			m.created_at, m.updated_at,
			s.current_status, s.last_check_at
		FROM uptime_monitors m
		LEFT JOIN uptime_monitor_state s ON s.monitor_id = m.id
		WHERE m.id = ?
	`, id).Scan(
		&m.ID, &m.Name, &m.Type, &url, &hostname, &port, &m.Method,
		&m.IntervalSecs, &m.TimeoutSecs, &m.Retries, &m.RetryInterval,
		&m.AcceptedStatusCodes, &keyword, &m.KeywordInvert,
		&reqHeaders, &reqBody, &m.TLSCheck, &m.TLSExpiryWarnDays,
		&m.IsActive, &m.MaintenanceMode, &groupID, &desc,
		&m.MaxRedirects, &alertChIDs, &m.AlertReminderMins,
		&m.CreatedAt, &m.UpdatedAt,
		&currentStatus, &lastCheckAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if url.Valid {
		m.URL = url.String
	}
	if hostname.Valid {
		m.Hostname = hostname.String
	}
	if port.Valid {
		m.Port = int(port.Int64)
	}
	if keyword.Valid {
		m.Keyword = keyword.String
	}
	if reqHeaders.Valid {
		m.ReqHeaders = reqHeaders.String
	}
	if reqBody.Valid {
		m.ReqBody = reqBody.String
	}
	if desc.Valid {
		m.Description = desc.String
	}
	if alertChIDs.Valid {
		m.AlertChannelIDs = ChannelIDs(alertChIDs.String)
	}
	if groupID.Valid {
		gid := groupID.Int64
		m.GroupID = &gid
	}
	if currentStatus.Valid {
		cs := int(currentStatus.Int64)
		m.CurrentStatus = &cs
	}
	if lastCheckAt.Valid {
		m.LastCheckAt = &lastCheckAt.String
	}

	return &m, nil
}

func (r *UptimeRepository) CreateMonitor(m *UptimeMonitor) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.Exec(`
		INSERT INTO uptime_monitors (name, type, url, hostname, port, method,
			interval_secs, timeout_secs, retries, retry_interval,
			accepted_statuscodes, keyword, keyword_invert,
			req_headers, req_body, tls_check, tls_expiry_warn_days,
			is_active, maintenance_mode, group_id, description,
			max_redirects, alert_channel_ids, alert_reminder_mins,
			created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		m.Name, m.Type, nullStr(m.URL), nullStr(m.Hostname), nullInt(m.Port), m.Method,
		m.IntervalSecs, m.TimeoutSecs, m.Retries, m.RetryInterval,
		m.AcceptedStatusCodes, nullStr(m.Keyword), m.KeywordInvert,
		nullStr(m.ReqHeaders), nullStr(m.ReqBody), m.TLSCheck, m.TLSExpiryWarnDays,
		m.IsActive, m.MaintenanceMode, m.GroupID, nullStr(m.Description),
		m.MaxRedirects, nullStr(string(m.AlertChannelIDs)), m.AlertReminderMins,
		now, now,
	)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.CreatedAt = now
	m.UpdatedAt = now

	// Initialize state row
	_, err = r.db.Exec(`INSERT INTO uptime_monitor_state (monitor_id) VALUES (?)`, m.ID)
	return err
}

func (r *UptimeRepository) UpdateMonitor(m *UptimeMonitor) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.Exec(`
		UPDATE uptime_monitors SET
			name=?, type=?, url=?, hostname=?, port=?, method=?,
			interval_secs=?, timeout_secs=?, retries=?, retry_interval=?,
			accepted_statuscodes=?, keyword=?, keyword_invert=?,
			req_headers=?, req_body=?, tls_check=?, tls_expiry_warn_days=?,
			is_active=?, maintenance_mode=?, group_id=?, description=?,
			max_redirects=?, alert_channel_ids=?, alert_reminder_mins=?,
			updated_at=?
		WHERE id=?
	`,
		m.Name, m.Type, nullStr(m.URL), nullStr(m.Hostname), nullInt(m.Port), m.Method,
		m.IntervalSecs, m.TimeoutSecs, m.Retries, m.RetryInterval,
		m.AcceptedStatusCodes, nullStr(m.Keyword), m.KeywordInvert,
		nullStr(m.ReqHeaders), nullStr(m.ReqBody), m.TLSCheck, m.TLSExpiryWarnDays,
		m.IsActive, m.MaintenanceMode, m.GroupID, nullStr(m.Description),
		m.MaxRedirects, nullStr(string(m.AlertChannelIDs)), m.AlertReminderMins,
		now, m.ID,
	)
	if err == nil {
		m.UpdatedAt = now
	}
	return err
}

func (r *UptimeRepository) DeleteMonitor(id int64) error {
	_, err := r.db.Exec(`DELETE FROM uptime_monitors WHERE id = ?`, id)
	return err
}

func (r *UptimeRepository) SetMonitorActive(id int64, active bool) error {
	_, err := r.db.Exec(`UPDATE uptime_monitors SET is_active = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = ?`, active, id)
	return err
}

// ── State Operations ─────────────────────────────────────────────────────────

func (r *UptimeRepository) GetState(monitorID int64) (*UptimeMonitorState, error) {
	var s UptimeMonitorState
	var lastCheck, nextCheck, lastAlert sql.NullString
	var activeIncident sql.NullInt64
	err := r.db.QueryRow(`SELECT monitor_id, current_status, consecutive_fails, consecutive_ups, last_check_at, next_check_at, last_alert_at, active_incident_id FROM uptime_monitor_state WHERE monitor_id = ?`, monitorID).Scan(
		&s.MonitorID, &s.CurrentStatus, &s.ConsecutiveFails, &s.ConsecutiveUps,
		&lastCheck, &nextCheck, &lastAlert, &activeIncident,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastCheck.Valid {
		s.LastCheckAt = lastCheck.String
	}
	if nextCheck.Valid {
		s.NextCheckAt = nextCheck.String
	}
	if lastAlert.Valid {
		s.LastAlertAt = lastAlert.String
	}
	if activeIncident.Valid {
		v := activeIncident.Int64
		s.ActiveIncidentID = &v
	}
	return &s, nil
}

func (r *UptimeRepository) UpdateState(s *UptimeMonitorState) error {
	_, err := r.db.Exec(`
		UPDATE uptime_monitor_state SET
			current_status=?, consecutive_fails=?, consecutive_ups=?,
			last_check_at=?, next_check_at=?, last_alert_at=?, active_incident_id=?
		WHERE monitor_id=?
	`, s.CurrentStatus, s.ConsecutiveFails, s.ConsecutiveUps,
		nullStr(s.LastCheckAt), nullStr(s.NextCheckAt), nullStr(s.LastAlertAt), s.ActiveIncidentID,
		s.MonitorID,
	)
	return err
}
