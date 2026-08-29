# Native Uptime Monitoring — Architecture Document

## Overview
Replace external Uptime Kuma with native Go monitoring built into HServer Panel.
Every check, alert, and status page runs inside the same 15MB binary — zero external dependencies.

## Implementation Phases

### Phase 1: Core (2-3 days)
- SQLite schema: `uptime_monitors`, `uptime_heartbeats`, `uptime_monitor_state`
- Go Engine: goroutine per monitor with `time.Ticker`
- HTTP + TCP checkers
- Basic API: CRUD monitors, list heartbeats
- Simple frontend: monitor list with status badges

### Phase 2: Alerting (1-2 days)
- StateManager: consecutive fail/up counting, incident lifecycle
- AlertDispatcher: reuse existing `notify.Dispatcher` (Telegram/email/Discord/Slack)
- DNS + Ping checkers
- Uptime stats calculator (24h/7d/30d/90d)

### Phase 3: Polish (1-2 days)
- Retention worker: compact heartbeats >30 days to hourly aggregates
- HeartbeatBatcher: batch INSERT for high-volume scenarios
- Dashboard widget: summary card (X up, Y down)
- Monitor detail page: uptime chart, response time graph, incident history

### Phase 4: Status Pages (1 day)
- `uptime_status_pages` + `uptime_status_page_monitors` tables
- Public endpoint: `GET /status/:slug` (no auth)
- Beautiful public page with uptime bars + incident history

## Database Schema

```sql
CREATE TABLE IF NOT EXISTS uptime_monitors (
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
);

CREATE TABLE IF NOT EXISTS uptime_heartbeats (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    monitor_id   INTEGER NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
    status       INTEGER NOT NULL,  -- 0=down, 1=up, 2=pending, 3=maintenance, 4=tls_warn
    msg          TEXT,
    ping_ms      REAL,
    status_code  INTEGER,
    tls_expiry   TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_uptime_hb_monitor_time ON uptime_heartbeats(monitor_id, created_at DESC);

CREATE TABLE IF NOT EXISTS uptime_heartbeats_hourly (
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
);

CREATE TABLE IF NOT EXISTS uptime_incidents (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    monitor_id       INTEGER NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
    type             TEXT NOT NULL,
    cause            TEXT,
    started_at       TEXT NOT NULL,
    resolved_at      TEXT,
    duration_secs    INTEGER
);

CREATE TABLE IF NOT EXISTS uptime_monitor_state (
    monitor_id          INTEGER PRIMARY KEY REFERENCES uptime_monitors(id) ON DELETE CASCADE,
    current_status      INTEGER NOT NULL DEFAULT 2,
    consecutive_fails   INTEGER NOT NULL DEFAULT 0,
    consecutive_ups     INTEGER NOT NULL DEFAULT 0,
    last_check_at       TEXT,
    next_check_at       TEXT,
    last_alert_at       TEXT,
    active_incident_id  INTEGER REFERENCES uptime_incidents(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS uptime_status_pages (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    slug         TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    description  TEXT,
    theme        TEXT NOT NULL DEFAULT 'auto',
    logo_url     TEXT,
    is_public    INTEGER NOT NULL DEFAULT 1,
    history_days INTEGER NOT NULL DEFAULT 90,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS uptime_status_page_monitors (
    status_page_id INTEGER NOT NULL REFERENCES uptime_status_pages(id) ON DELETE CASCADE,
    monitor_id     INTEGER NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
    display_name   TEXT,
    sort_order     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (status_page_id, monitor_id)
);
```

## Go Package Structure
```
internal/services/uptime/
├── engine.go           ← Engine: manages all goroutines, Start/Stop/Add/Update/Delete
├── scheduler.go        ← monitorWorker: per-monitor goroutine with ticker + jitter
├── checker.go          ← dispatchCheck() + CheckResult struct
├── checker_http.go     ← HTTP/HTTPS + keyword + redirect following + TLS expiry
├── checker_tcp.go      ← TCP port dial
├── checker_dns.go      ← DNS lookup
├── checker_ping.go     ← ICMP ping (exec fallback)
├── state.go            ← StateManager: status transitions, incident lifecycle
├── alerter.go          ← AlertDispatcher: wraps notify.Dispatcher for uptime events
├── retention.go        ← RetentionWorker: compact heartbeats, prune old data
└── stats.go            ← UptimeCalculator: uptime%, avg/p95/p99 response times
```

## API Endpoints
```
GET    /api/uptime/monitors              ← list all with current status
POST   /api/uptime/monitors              ← create
GET    /api/uptime/monitors/summary      ← dashboard widget {up, down, paused}
POST   /api/uptime/monitors/test         ← test without saving
GET    /api/uptime/monitors/:id          ← detail with uptime stats
PUT    /api/uptime/monitors/:id          ← update
DELETE /api/uptime/monitors/:id          ← delete
POST   /api/uptime/monitors/:id/pause    ← pause
POST   /api/uptime/monitors/:id/resume   ← resume
GET    /api/uptime/monitors/:id/heartbeats?hours=24
GET    /api/uptime/monitors/:id/uptime?period=30d
GET    /api/uptime/monitors/:id/incidents

GET    /api/uptime/status-pages          ← list
POST   /api/uptime/status-pages          ← create
PUT    /api/uptime/status-pages/:id      ← update
DELETE /api/uptime/status-pages/:id      ← delete

GET    /status/:slug                     ← PUBLIC (no auth) status page
GET    /api/status/:slug                 ← PUBLIC API for status page data
```

## Alert Flow
```
Worker goroutine → dispatchCheck() → CheckResult
  ↓
StateManager.Transition()
  ├── UP→DOWN: CreateIncident() + AlertDispatcher.SendDown() → Telegram/Email
  ├── DOWN→UP: CloseIncident() + AlertDispatcher.SendRecovery()
  └── DOWN→DOWN (reminder): AlertDispatcher.SendReminder()
  ↓
notify.Dispatcher.SendToChannels() → existing channel infrastructure
```

## Data Retention
- 0-30 days: raw heartbeats (every check = 1 row)
- 30-90 days: hourly aggregates (compacted)
- 90+ days: deleted
- Incidents: permanent
- Compaction runs daily at 03:00 UTC

## Performance (1000 monitors)
- 16.7 checks/second, 16 concurrent goroutines
- ~1MB extra RAM
- SQLite WAL handles ~10K writes/sec (headroom: 600x)
- HeartbeatBatcher: batch INSERT every 5s or 100 records
