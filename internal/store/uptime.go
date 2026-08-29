package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// ── Migration ────────────────────────────────────────────────────────────────

func MigrateUptime(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS uptime_monitors (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL,
			type             TEXT NOT NULL DEFAULT 'http',
			url              TEXT,
			hostname         TEXT,
			port             INTEGER,
			method           TEXT NOT NULL DEFAULT 'GET',
			interval_secs    INTEGER NOT NULL DEFAULT 60,
			timeout_secs     INTEGER NOT NULL DEFAULT 30,
			retries          INTEGER NOT NULL DEFAULT 1,
			retry_interval   INTEGER NOT NULL DEFAULT 30,
			accepted_statuscodes TEXT NOT NULL DEFAULT '["200-299"]',
			keyword          TEXT,
			keyword_invert   INTEGER NOT NULL DEFAULT 0,
			req_headers      TEXT,
			req_body         TEXT,
			tls_check        INTEGER NOT NULL DEFAULT 1,
			tls_expiry_warn_days INTEGER NOT NULL DEFAULT 14,
			is_active        INTEGER NOT NULL DEFAULT 1,
			maintenance_mode INTEGER NOT NULL DEFAULT 0,
			group_id         INTEGER REFERENCES uptime_monitors(id) ON DELETE SET NULL,
			description      TEXT,
			max_redirects    INTEGER NOT NULL DEFAULT 5,
			alert_channel_ids TEXT,
			alert_reminder_mins INTEGER NOT NULL DEFAULT 0,
			created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)`,
		`CREATE TABLE IF NOT EXISTS uptime_heartbeats (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			monitor_id   INTEGER NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
			status       INTEGER NOT NULL,
			msg          TEXT,
			ping_ms      REAL,
			status_code  INTEGER,
			tls_expiry   TEXT,
			created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_uptime_hb_monitor_time ON uptime_heartbeats(monitor_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS uptime_heartbeats_hourly (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			monitor_id      INTEGER NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
			hour_bucket     TEXT NOT NULL,
			total_checks    INTEGER NOT NULL DEFAULT 0,
			up_checks       INTEGER NOT NULL DEFAULT 0,
			down_checks     INTEGER NOT NULL DEFAULT 0,
			avg_ping_ms     REAL,
			min_ping_ms     REAL,
			max_ping_ms     REAL,
			UNIQUE(monitor_id, hour_bucket)
		)`,
		`CREATE TABLE IF NOT EXISTS uptime_incidents (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			monitor_id       INTEGER NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
			type             TEXT NOT NULL,
			cause            TEXT,
			started_at       TEXT NOT NULL,
			resolved_at      TEXT,
			duration_secs    INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS uptime_monitor_state (
			monitor_id          INTEGER PRIMARY KEY REFERENCES uptime_monitors(id) ON DELETE CASCADE,
			current_status      INTEGER NOT NULL DEFAULT 2,
			consecutive_fails   INTEGER NOT NULL DEFAULT 0,
			consecutive_ups     INTEGER NOT NULL DEFAULT 0,
			last_check_at       TEXT,
			next_check_at       TEXT,
			last_alert_at       TEXT,
			active_incident_id  INTEGER REFERENCES uptime_incidents(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS uptime_status_pages (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			slug         TEXT NOT NULL UNIQUE,
			title        TEXT NOT NULL,
			description  TEXT,
			theme        TEXT NOT NULL DEFAULT 'auto',
			logo_url     TEXT,
			is_public    INTEGER NOT NULL DEFAULT 1,
			history_days INTEGER NOT NULL DEFAULT 90,
			created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)`,
		`CREATE TABLE IF NOT EXISTS uptime_status_page_monitors (
			status_page_id INTEGER NOT NULL REFERENCES uptime_status_pages(id) ON DELETE CASCADE,
			monitor_id     INTEGER NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
			display_name   TEXT,
			sort_order     INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (status_page_id, monitor_id)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("MigrateUptime: %w", err)
		}
	}
	// Older and manually imported monitors may predate the state table. Without
	// a row, checks are recorded but transitions, incidents and alerts silently
	// stop working. Repair that invariant every time the idempotent migration
	// runs so every monitor immediately participates in state tracking.
	if _, err := db.Exec(`
		INSERT OR IGNORE INTO uptime_monitor_state (monitor_id)
		SELECT id FROM uptime_monitors
	`); err != nil {
		return fmt.Errorf("MigrateUptime: backfill monitor state: %w", err)
	}

	// Re-link every DOWN monitor to its newest unresolved incident. Historical
	// versions could lose this pointer or leave multiple incidents open after a
	// restart, which made the UI report recovered monitors as still incidenting.
	if _, err := db.Exec(`
		UPDATE uptime_monitor_state AS state
		SET active_incident_id = (
			SELECT incident.id
			FROM uptime_incidents AS incident
			WHERE incident.monitor_id = state.monitor_id
				AND incident.resolved_at IS NULL
			ORDER BY incident.started_at DESC, incident.id DESC
			LIMIT 1
		)
		WHERE state.current_status = 0
	`); err != nil {
		return fmt.Errorf("MigrateUptime: relink active incidents: %w", err)
	}

	// Resolve incidents that no longer match the monitor state and collapse old
	// duplicates at the start of the incident that remains active.
	if _, err := db.Exec(`
		WITH incident_end AS (
			SELECT incident.id,
				CASE
					WHEN state.current_status = 0 AND state.active_incident_id IS NOT NULL
						THEN COALESCE(active.started_at, state.last_check_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
					ELSE COALESCE(state.last_check_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
				END AS ended_at
			FROM uptime_incidents AS incident
			JOIN uptime_monitor_state AS state ON state.monitor_id = incident.monitor_id
			LEFT JOIN uptime_incidents AS active ON active.id = state.active_incident_id
			WHERE incident.resolved_at IS NULL
				AND (state.current_status <> 0 OR incident.id <> state.active_incident_id)
		)
		UPDATE uptime_incidents
		SET resolved_at = (SELECT ended_at FROM incident_end WHERE incident_end.id = uptime_incidents.id),
			duration_secs = MAX(0, CAST((
				julianday((SELECT ended_at FROM incident_end WHERE incident_end.id = uptime_incidents.id))
				- julianday(started_at)
			) * 86400 AS INTEGER))
		WHERE id IN (SELECT id FROM incident_end)
	`); err != nil {
		return fmt.Errorf("MigrateUptime: reconcile incidents: %w", err)
	}

	if _, err := db.Exec(`
		UPDATE uptime_monitor_state
		SET active_incident_id = NULL
		WHERE current_status <> 0
	`); err != nil {
		return fmt.Errorf("MigrateUptime: clear stale active incidents: %w", err)
	}

	// A DOWN state without an open incident cannot recover its lifecycle. Return
	// it to PENDING so the next failed checks create a properly linked incident.
	if _, err := db.Exec(`
		UPDATE uptime_monitor_state
		SET current_status = 2, consecutive_fails = 0, active_incident_id = NULL
		WHERE current_status = 0
			AND NOT EXISTS (
				SELECT 1 FROM uptime_incidents
				WHERE uptime_incidents.monitor_id = uptime_monitor_state.monitor_id
					AND uptime_incidents.resolved_at IS NULL
			)
	`); err != nil {
		return fmt.Errorf("MigrateUptime: reset orphaned down states: %w", err)
	}

	if _, err := db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_uptime_one_open_incident
		ON uptime_incidents(monitor_id)
		WHERE resolved_at IS NULL
	`); err != nil {
		return fmt.Errorf("MigrateUptime: open incident invariant: %w", err)
	}
	return nil
}

// ── Models ───────────────────────────────────────────────────────────────────

type UptimeMonitor struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	Type                string     `json:"type"`
	URL                 string     `json:"url,omitempty"`
	Hostname            string     `json:"hostname,omitempty"`
	Port                int        `json:"port,omitempty"`
	Method              string     `json:"method"`
	IntervalSecs        int        `json:"interval_secs"`
	TimeoutSecs         int        `json:"timeout_secs"`
	Retries             int        `json:"retries"`
	RetryInterval       int        `json:"retry_interval"`
	AcceptedStatusCodes string     `json:"accepted_statuscodes"`
	Keyword             string     `json:"keyword,omitempty"`
	KeywordInvert       bool       `json:"keyword_invert"`
	ReqHeaders          string     `json:"req_headers,omitempty"`
	ReqBody             string     `json:"req_body,omitempty"`
	TLSCheck            bool       `json:"tls_check"`
	TLSExpiryWarnDays   int        `json:"tls_expiry_warn_days"`
	IsActive            bool       `json:"is_active"`
	MaintenanceMode     bool       `json:"maintenance_mode"`
	GroupID             *int64     `json:"group_id,omitempty"`
	Description         string     `json:"description,omitempty"`
	MaxRedirects        int        `json:"max_redirects"`
	AlertChannelIDs     ChannelIDs `json:"alert_channel_ids,omitempty"`
	AlertReminderMins   int        `json:"alert_reminder_mins"`
	CreatedAt           string     `json:"created_at"`
	UpdatedAt           string     `json:"updated_at"`
	// Joined fields (not stored in uptime_monitors)
	CurrentStatus *int     `json:"current_status,omitempty"`
	Uptime24h     *float64 `json:"uptime_24h,omitempty"`
	LastCheckAt   *string  `json:"last_check_at,omitempty"`
	AvgPingMs     *float64 `json:"avg_ping_ms,omitempty"`
}

// ChannelIDs is stored as compact JSON text in SQLite, while the HTTP contract
// is a real JSON array. It also accepts the legacy JSON-string representation
// so older API clients continue to work during upgrades.
type ChannelIDs string

func (ids ChannelIDs) MarshalJSON() ([]byte, error) {
	if ids == "" {
		return []byte("[]"), nil
	}
	var parsed []int64
	if err := json.Unmarshal([]byte(ids), &parsed); err != nil {
		return nil, fmt.Errorf("invalid stored alert channel IDs: %w", err)
	}
	return json.Marshal(parsed)
}

func (ids *ChannelIDs) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*ids = ""
		return nil
	}
	var parsed []int64
	if len(data) > 0 && data[0] == '"' {
		var legacy string
		if err := json.Unmarshal(data, &legacy); err != nil {
			return err
		}
		if legacy == "" {
			*ids = ""
			return nil
		}
		if err := json.Unmarshal([]byte(legacy), &parsed); err != nil {
			return fmt.Errorf("invalid alert_channel_ids string: %w", err)
		}
	} else if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("alert_channel_ids must be an array of integers: %w", err)
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return err
	}
	*ids = ChannelIDs(encoded)
	return nil
}

type UptimeHeartbeat struct {
	ID         int64    `json:"id"`
	MonitorID  int64    `json:"monitor_id"`
	Status     int      `json:"status"`
	Msg        string   `json:"msg,omitempty"`
	PingMs     *float64 `json:"ping_ms,omitempty"`
	StatusCode *int     `json:"status_code,omitempty"`
	TLSExpiry  string   `json:"tls_expiry,omitempty"`
	CreatedAt  string   `json:"created_at"`
}

type UptimeIncident struct {
	ID           int64  `json:"id"`
	MonitorID    int64  `json:"monitor_id"`
	MonitorName  string `json:"monitor_name,omitempty"`
	Type         string `json:"type"`
	Cause        string `json:"cause,omitempty"`
	StartedAt    string `json:"started_at"`
	ResolvedAt   string `json:"resolved_at,omitempty"`
	DurationSecs *int   `json:"duration_secs,omitempty"`
}

type UptimeMonitorState struct {
	MonitorID        int64  `json:"monitor_id"`
	CurrentStatus    int    `json:"current_status"`
	ConsecutiveFails int    `json:"consecutive_fails"`
	ConsecutiveUps   int    `json:"consecutive_ups"`
	LastCheckAt      string `json:"last_check_at,omitempty"`
	NextCheckAt      string `json:"next_check_at,omitempty"`
	LastAlertAt      string `json:"last_alert_at,omitempty"`
	ActiveIncidentID *int64 `json:"active_incident_id,omitempty"`
}

type UptimeSummary struct {
	Up          int `json:"up"`
	Down        int `json:"down"`
	Paused      int `json:"paused"`
	Maintenance int `json:"maintenance"`
}

type UptimeStatusPage struct {
	ID          int64                    `json:"id"`
	Slug        string                   `json:"slug"`
	Title       string                   `json:"title"`
	Description string                   `json:"description,omitempty"`
	Theme       string                   `json:"theme"`
	LogoURL     string                   `json:"logo_url,omitempty"`
	IsPublic    bool                     `json:"is_public"`
	HistoryDays int                      `json:"history_days"`
	Monitors    []StatusPageMonitorEntry `json:"monitors,omitempty"`
	CreatedAt   string                   `json:"created_at"`
}

type StatusPageMonitorEntry struct {
	MonitorID   int64  `json:"monitor_id"`
	DisplayName string `json:"display_name,omitempty"`
	SortOrder   int    `json:"sort_order"`
}

// ── Repository ───────────────────────────────────────────────────────────────

type UptimeRepository struct{ db *sql.DB }

func NewUptimeRepository(db *sql.DB) *UptimeRepository {
	return &UptimeRepository{db: db}
}

// ── Summary ──────────────────────────────────────────────────────────────────

func (r *UptimeRepository) Summary() (*UptimeSummary, error) {
	s := &UptimeSummary{}
	rows, err := r.db.Query(`
		SELECT COALESCE(s.current_status, 2), m.is_active, m.maintenance_mode
		FROM uptime_monitors m
		LEFT JOIN uptime_monitor_state s ON s.monitor_id = m.id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var status int
		var isActive, maintenance bool
		if err := rows.Scan(&status, &isActive, &maintenance); err != nil {
			return nil, err
		}
		if maintenance {
			s.Maintenance++
		} else if !isActive {
			s.Paused++
		} else if status == 1 {
			s.Up++
		} else if status == 0 {
			s.Down++
		} else {
			s.Paused++ // pending = paused visually
		}
	}
	return s, rows.Err()
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(i int) interface{} {
	if i == 0 {
		return nil
	}
	return i
}

// ParseChannelIDs parses a JSON array string like "[1,2,3]" into a slice of int64.
func ParseChannelIDs(s string) []int64 {
	if s == "" {
		return nil
	}
	var ids []int64
	_ = json.Unmarshal([]byte(s), &ids)
	return ids
}
