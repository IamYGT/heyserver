# Heyserver — Database Schema

## Overview

Heyserver uses **SQLite** (WAL mode, foreign keys enabled) as its sole datastore.
The database file lives at the path configured by `HSERVER_DB_PATH`
(default: `/var/lib/hserver/hserver.db`).

SQLite is opened with the following DSN options:

| Option | Value | Purpose |
|---|---|---|
| `_journal_mode` | `WAL` | Allows concurrent readers with a single writer |
| `_foreign_keys` | `on` | Enforces `REFERENCES` constraints at runtime |
| `_busy_timeout` | `5000` ms | Retries on lock contention before returning an error |
| `MaxOpenConns` | 1 | One writer connection — SQLite best practice |

---

## Two Migration Paths

The schema is created through two independent mechanisms. Both are idempotent
(`CREATE TABLE IF NOT EXISTS`) so there are no conflicts on restart.

### Path A — `internal/db/db.go` (version-tracked)

Runs automatically inside `db.Open()` at startup. Maintains a
`schema_migrations` table that records each applied migration version.
Migrations are an ordered slice of SQL statements; skipped if the
current version is already >= the statement index. Use this path for
**core system tables** (users, sessions, domains, audit logs).

### Path B — `internal/store/*.go` (idempotent bootstrap)

Each `store` package exposes a `MigrateXxx(db *sql.DB) error` function
that runs `CREATE TABLE IF NOT EXISTS` directly. Called once from
`main.go` during startup. There is no version tracking — the statements
are safe to re-run. Use this path for **feature-specific tables**
(notifications, settings, uptime monitors).

### Path C — Service-local migrations

Some service packages (e.g. `internal/services/deploy/deploy.go`) call
their own `migrate()` method inside the service constructor. These also
use `CREATE TABLE IF NOT EXISTS` and are invoked when the service is
wired up in `main.go`.

---

## Tables

### `schema_migrations` (Path A)

Tracks which Path A migration versions have been applied.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `version` | INTEGER | PRIMARY KEY | Migration version number (1-based index) |
| `applied_at` | DATETIME | NOT NULL, default `datetime('now')` | UTC timestamp when applied |

**Indexes:** none (PK lookup only)

---

### `users` (Path A)

Stores panel operator accounts. Supports three roles with different
access levels enforced by the API middleware layer.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Internal user ID |
| `email` | TEXT | NOT NULL, UNIQUE, COLLATE NOCASE | Login identifier; case-insensitive |
| `name` | TEXT | NOT NULL | Display name |
| `password` | TEXT | NOT NULL | bcrypt hash |
| `role` | TEXT | NOT NULL, DEFAULT `'viewer'`, CHECK `IN ('admin','manager','viewer')` | RBAC role |
| `created_at` | DATETIME | NOT NULL, default `datetime('now')` | Account creation time |
| `updated_at` | DATETIME | NOT NULL, default `datetime('now')` | Last modification time |

**Indexes:** implicit unique index on `email`

**Roles:**

| Role | Permissions |
|---|---|
| `admin` | Full access including user management and all destructive operations |
| `manager` | Manage services, domains, files; cannot manage users |
| `viewer` | Read-only access to dashboard and logs |

**Relationships:** referenced by `sessions.user_id`

---

### `sessions` (Path A)

Active JWT sessions. A row is created on login and deleted on logout or
when the user is deleted (CASCADE).

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | TEXT | PRIMARY KEY | UUID session token |
| `user_id` | INTEGER | NOT NULL, REFERENCES `users(id)` ON DELETE CASCADE | Owning user |
| `ip_address` | TEXT | — | Client IP at login |
| `user_agent` | TEXT | — | Client User-Agent at login |
| `last_seen` | DATETIME | NOT NULL, default `datetime('now')` | Updated on each authenticated request |
| `created_at` | DATETIME | NOT NULL, default `datetime('now')` | Session creation time |

**Indexes:**
- `idx_sessions_user_id` on `sessions(user_id)` — fast lookup by user

**Relationships:** `user_id` → `users(id)` (CASCADE DELETE)

---

### `domains` (Path A)

Registered web domains managed by the panel. Each domain has a type
that determines how nginx serves it.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Internal domain ID |
| `name` | TEXT | NOT NULL, UNIQUE, COLLATE NOCASE | Fully-qualified domain name |
| `type` | TEXT | NOT NULL, DEFAULT `'static'`, CHECK `IN ('php','proxy','static','redirect')` | Serving mode |
| `root` | TEXT | NOT NULL, DEFAULT `''` | Document root path on disk |
| `php_version` | TEXT | — | PHP version for `php` type (e.g. `8.4`) |
| `proxy_port` | INTEGER | — | Upstream port for `proxy` type |
| `ssl_enabled` | INTEGER | NOT NULL, DEFAULT 0 | 1 = SSL certificate active |
| `is_active` | INTEGER | NOT NULL, DEFAULT 1 | 0 = disabled (nginx not reloaded) |
| `created_at` | DATETIME | NOT NULL, default `datetime('now')` | Domain registration time |
| `updated_at` | DATETIME | NOT NULL, default `datetime('now')` | Last modification time |

**Indexes:** implicit unique index on `name`

**Domain types:**

| Type | Description |
|---|---|
| `static` | Serve files directly from `root` via nginx |
| `php` | FastCGI via PHP-FPM pool at the specified `php_version` |
| `proxy` | Reverse proxy to `localhost:proxy_port` |
| `redirect` | HTTP 301/302 redirect target |

---

### `audit_logs` (Path A)

Immutable record of all user actions for compliance and debugging.
Rows are never updated or deleted through the application.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Log entry ID |
| `user_id` | INTEGER | NOT NULL, DEFAULT 0 | ID of the acting user (0 = system) |
| `user_name` | TEXT | NOT NULL, DEFAULT `''` | Display name snapshot at action time |
| `action` | TEXT | NOT NULL | Action verb (e.g. `domain.create`, `user.delete`) |
| `resource` | TEXT | NOT NULL, DEFAULT `''` | Affected resource identifier |
| `details` | TEXT | NOT NULL, DEFAULT `''` | JSON or human-readable detail string |
| `ip_address` | TEXT | NOT NULL, DEFAULT `''` | Client IP address |
| `created_at` | DATETIME | NOT NULL, default `datetime('now')` | When the action occurred |

**Indexes:**
- `idx_audit_logs_user_id` on `audit_logs(user_id)`
- `idx_audit_logs_created_at` on `audit_logs(created_at)`
- `idx_audit_logs_action` on `audit_logs(action)`

**Note:** `user_id` is not a foreign key — audit records must survive
user deletion. The `user_name` column preserves the identity at the
time of the action.

---

### `system_settings` (Path A)

Legacy key-value store created by Path A. Superseded by the `settings`
table (Path B) but kept for backward compatibility with any existing
data. New code should use the `store.SettingsRepository` backed by
the `settings` table.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `key` | TEXT | PRIMARY KEY | Setting identifier |
| `value` | TEXT | NOT NULL, DEFAULT `''` | Setting value |
| `updated_at` | DATETIME | NOT NULL, default `datetime('now')` | Last update time |

---

### `settings` (Path B — `internal/store/settings.go` and `internal/db/settings.go`)

The authoritative key-value store for all panel configuration. Accessed
through `store.SettingsRepository` (or `db.SettingsRepository`).
Upserts are atomic (`INSERT ... ON CONFLICT DO UPDATE`).

| Column | Type | Constraints | Description |
|---|---|---|---|
| `key` | TEXT | PRIMARY KEY | Setting identifier (e.g. `smtp.host`, `panel.theme`) |
| `value` | TEXT | NOT NULL, DEFAULT `''` | Setting value (always stored as text) |
| `updated_at` | TEXT | NOT NULL, default `strftime('%Y-%m-%dT%H:%M:%SZ','now')` | RFC3339 UTC timestamp |

**Note:** `updated_at` is stored as `TEXT` in RFC3339 format (ISO 8601),
not as SQLite `DATETIME`. The repository parses it with `time.RFC3339`.

---

### `notification_channels` (Path B — `internal/store/notify.go`)

Configured delivery channels for alert notifications. Each channel has a
`type`; secret-bearing provider JSON lives in an installation-owned `0600`
file, while SQLite stores only its deterministic non-secret reference.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Channel ID |
| `name` | TEXT | NOT NULL | Human-readable label |
| `type` | TEXT | NOT NULL | Channel type: `email`, `telegram`, `discord`, `slack` |
| `config` | TEXT | NOT NULL, DEFAULT `'{}'` | Non-secret protected-file reference; a short-lived `pending:` reference fences readers during crash-safe replacement |
| `enabled` | INTEGER | NOT NULL, DEFAULT 1 | 1 = active, 0 = disabled |
| `config_revision` | INTEGER | NOT NULL, DEFAULT 1 | Monotonically increasing configuration generation used to fence delivery receipts |
| `created_at` | TEXT | NOT NULL, default `strftime(...)` | RFC3339 UTC creation timestamp |
| `updated_at` | TEXT | NOT NULL, default `strftime(...)` | RFC3339 UTC last-update timestamp |

**Relationships:** referenced by the notification dispatcher at runtime
(not a formal FK — channels are resolved by ID at send time)

Existing installations receive `config_revision` through an additive,
column-aware `ALTER TABLE` step; the migration does not rebuild or reset the
notification channel table. New channels start at revision `1`, and each
successful configuration update increments the revision atomically.
Updates first commit the next revision with a non-readable `pending:`
reference, atomically replace the protected file, and only then publish the
canonical `file:` reference. Startup recovery promotes a valid protected file
left by an interrupted update under the already-advanced revision, so an old
delivery receipt cannot become valid for uncertain configuration content.

### `notification_delivery_receipts` (Path B — `internal/store/notify_delivery.go`)

Bounded latest delivery observations for notification channels. The primary key
means there is at most one receipt per channel. An upsert accepts a higher
configuration revision, or a newer observation within the same revision;
delayed lower-revision and older same-revision observations cannot overwrite
the current row. `channel_config_revision` fences an observation to the
configuration generation that was active when the delivery was attempted.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `channel_id` | INTEGER | PRIMARY KEY, REFERENCES `notification_channels(id)` ON DELETE CASCADE | Notification channel |
| `channel_config_revision` | INTEGER | NOT NULL, CHECK `> 0` | Channel configuration generation for this observation |
| `outcome` | TEXT | NOT NULL, CHECK `IN ('success','failure')` | Delivery outcome |
| `source` | TEXT | NOT NULL, CHECK `IN ('manual_test','alert','uptime','backup')` | Bounded operation source |
| `observed_at` | TEXT | NOT NULL | UTC RFC3339Nano observation timestamp |

The receipt table intentionally contains no provider payload, destination,
provider error, secret, subject, or body fields. Receipts are deleted with
their channel when foreign-key enforcement is enabled.

---

### `alert_rules` (Path B — `internal/store/notify.go`)

Threshold-based monitoring rules. When a rule's condition is met for
`duration_mins` continuously, the notification dispatcher fires for all
enabled channels and then waits `cooldown_mins` before re-firing.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Rule ID |
| `name` | TEXT | NOT NULL | Human-readable rule name |
| `type` | TEXT | NOT NULL | Canonical metric type: `cpu_usage`, `memory_usage`, `disk_usage`, `ssl_expiry`, `service_down`, or `failed_logins`; legacy aliases migrate in place |
| `threshold` | REAL | NOT NULL, DEFAULT 0 | Trigger value (e.g. 90.0 for 90% CPU) |
| `duration_mins` | INTEGER | NOT NULL, DEFAULT 0 | How long condition must persist before firing (0 = immediate) |
| `target` | TEXT | NOT NULL, DEFAULT `''` | Target identifier (service name, mount point, etc.) |
| `enabled` | INTEGER | NOT NULL, DEFAULT 1 | 1 = rule is active |
| `cooldown_mins` | INTEGER | NOT NULL, DEFAULT 15 | Minimum minutes between re-fires (validated range `1–10080`) |
| `created_at` | TEXT | NOT NULL, default `strftime(...)` | RFC3339 UTC creation timestamp |
| `updated_at` | TEXT | NOT NULL, default `strftime(...)` | RFC3339 UTC last-update timestamp |

---

### `alert_history` (Path B — `internal/store/notify.go`)

Append-only log of every alert that was fired. Used for the alert
history UI and for cooldown enforcement (the dispatcher queries
`LastFiredAt` before sending).

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | History entry ID |
| `rule_id` | INTEGER | NOT NULL | ID of the `alert_rules` row that fired |
| `rule_name` | TEXT | NOT NULL | Snapshot of the rule name at fire time |
| `type` | TEXT | NOT NULL | Metric type snapshot |
| `message` | TEXT | NOT NULL | Human-readable alert message |
| `value` | REAL | NOT NULL, DEFAULT 0 | Observed metric value that crossed the threshold |
| `fired_at` | TEXT | NOT NULL, default `strftime(...)` | RFC3339 UTC timestamp |

**Indexes:**
- `idx_alert_history_fired_at` on `alert_history(fired_at)` — range scans for pruning/history list
- `idx_alert_history_rule_id` on `alert_history(rule_id)` — cooldown lookup by rule

**Note:** `rule_id` is not a FK — history is retained after rule deletion.
Old entries can be pruned with `AlertHistoryRepository.PruneOlderThan(duration)`.

---

### `deploy_targets` (Path C — `internal/services/deploy/deploy.go`)

Registered deployment targets. Each target maps a Git repository branch
to a directory on disk and an optional shell script to run after pull.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Target ID |
| `name` | TEXT | NOT NULL | Human-readable label |
| `repo_url` | TEXT | NOT NULL, DEFAULT `''` | Git remote URL (SSH or HTTPS) |
| `branch` | TEXT | NOT NULL, DEFAULT `'main'` | Branch to track |
| `project_dir` | TEXT | NOT NULL | Absolute path where `git pull` runs |
| `deployment_kind` | TEXT | NOT NULL, DEFAULT `'script'` | Fixed executor: `script` or `compose` |
| `compose_file` | TEXT | NOT NULL, DEFAULT `''` | Optional contained relative Compose file |
| `deploy_script` | TEXT | NOT NULL, DEFAULT `''` | Shell script body executed after pull (via `bash -e`) |
| `webhook_provider` | TEXT | NOT NULL, DEFAULT `'github'` | Signed delivery adapter: `github` or `gitlab` |
| `webhook_token` | TEXT | NOT NULL, DEFAULT `''` | Legacy column name; stores only `file:target-ID.secret` when the native data directory is configured |
| `auto_deploy` | INTEGER | NOT NULL, DEFAULT 1 | 1 = fire on incoming webhook push events |
| `is_active` | INTEGER | NOT NULL, DEFAULT 1 | 0 = target is disabled, webhooks ignored |
| `created_at` | DATETIME | NOT NULL, default `datetime('now')` | Registration time |
| `updated_at` | DATETIME | NOT NULL, default `datetime('now')` | Last modification time |

Webhook signing values are not retained in SQLite on native installations. They
live below `${HSERVER_DATA_DIR}/deploy-webhook-secrets` with directory mode
`0700` and file mode `0600`. Startup migrates an existing literal value to its
target file and replaces the column value with the deterministic reference.

**Relationships:** referenced by `deploy_runs.target_id`,
`deploy_webhook_deliveries.target_id`, and `deploy_domains.target_id`

---

### `deploy_webhook_deliveries` (Path C — `internal/services/deploy/deploy.go`)

Durable replay lock for authenticated provider push deliveries. Registration
uses the composite primary key with `ON CONFLICT DO NOTHING`; a duplicate is
acknowledged without queueing another deployment.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `target_id` | INTEGER | NOT NULL, REFERENCES `deploy_targets(id)` ON DELETE CASCADE | Parent target |
| `provider` | TEXT | NOT NULL | Authenticated `github` or `gitlab` adapter |
| `delivery_id` | TEXT | NOT NULL | Provider-owned stable delivery identity |
| `received_at` | DATETIME | NOT NULL, default `datetime('now')` | First accepted delivery time |

**Primary key:** (`target_id`, `provider`, `delivery_id`)

**Relationships:** `target_id` → `deploy_targets(id)` (CASCADE DELETE; the
application also removes delivery rows explicitly for SQLite callers without
foreign-key enforcement)

---

### `deploy_runs` (Path C — `internal/services/deploy/deploy.go`)

Execution log for every deployment attempt (manual, webhook, or rollback).
Rows start with `status = 'pending'` and are updated in-place as the
background goroutine progresses.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Run ID |
| `target_id` | INTEGER | NOT NULL, REFERENCES `deploy_targets(id)` ON DELETE CASCADE | Parent target |
| `trigger` | TEXT | NOT NULL, DEFAULT `'manual'` | How it was triggered: `manual`, `webhook`, `rollback` |
| `branch` | TEXT | NOT NULL, DEFAULT `''` | Branch that was deployed |
| `commit` | TEXT | NOT NULL, DEFAULT `''` | New HEAD commit SHA after deployment |
| `prev_commit` | TEXT | NOT NULL, DEFAULT `''` | Previous HEAD SHA (used as rollback reference) |
| `status` | TEXT | NOT NULL, DEFAULT `'pending'` | `pending`, `running`, `success`, `failed` |
| `logs` | TEXT | NOT NULL, DEFAULT `''` | Full captured stdout+stderr from git and deploy script |
| `started_at` | DATETIME | NOT NULL, default `datetime('now')` | When the run was enqueued |
| `finished_at` | DATETIME | — | NULL until the run completes or fails |
| `duration_ms` | INTEGER | NOT NULL, DEFAULT 0 | Wall-clock duration in milliseconds |

**Indexes:** none beyond PK (queries use target_id + status with small table sizes)

**Relationships:** `target_id` → `deploy_targets(id)` (CASCADE DELETE — runs are
removed when their target is deleted)

**Status lifecycle:**
```
pending → running → success
                 → failed
```

### `deploy_domains` (Path C — `internal/services/deploy/deploy.go`)

Desired local Nginx mappings for Docker Compose deploy targets. The upstream is
not stored as a free-form URL; it is reconstructed as loopback plus `host_port`.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Mapping ID |
| `target_id` | INTEGER | NOT NULL, REFERENCES `deploy_targets(id)` ON DELETE CASCADE | Parent Compose target |
| `domain` | TEXT | NOT NULL, UNIQUE | Validated ASCII hostname |
| `service` | TEXT | NOT NULL | Validated Compose service label |
| `host_port` | INTEGER | NOT NULL | Explicit published host port |
| `tls_enabled` | INTEGER | NOT NULL, DEFAULT 0 | Desired Heyserver-managed HTTPS state; certificate health is observed from Certbot files |
| `created_at` | DATETIME | NOT NULL, default `datetime('now')` | Mapping creation time |
| `updated_at` | DATETIME | NOT NULL, default `datetime('now')` | Last mapping update time |

Application deletion refuses to remove a target while a mapping exists, even
though the foreign key is defensive, so active Nginx files cannot be orphaned.

---

## Entity Relationship Diagram (text)

```
users ──────────< sessions          (1:N, CASCADE DELETE)
users ──────────< audit_logs        (1:N, no FK — survives user deletion)

deploy_targets ─< deploy_runs       (1:N, CASCADE DELETE)
deploy_targets ─< deploy_webhook_deliveries (1:N, CASCADE DELETE)
deploy_targets ─< deploy_domains    (1:N, guarded application delete)

alert_rules ────< alert_history     (1:N, no FK — survives rule deletion)

notification_channels ──< notification_delivery_receipts (1:1 latest row, CASCADE DELETE)

settings                            (standalone key-value, no relations)
system_settings                     (standalone key-value, no relations — legacy)
schema_migrations                   (standalone version tracker)
```

---

## Adding New Tables

Follow **Path B**:

1. Create `internal/store/yourfeature.go`
2. Implement `MigrateYourfeature(db *sql.DB) error` with `CREATE TABLE IF NOT EXISTS`
3. Implement a repository struct with CRUD methods
4. Call `store.MigrateYourfeature(db)` in `main.go` startup sequence
5. Wire up the repository into the service/handler that needs it

Do **not** add new entries to the `migrations` slice in `internal/db/db.go` unless
the table is a core system table that must be version-tracked.
