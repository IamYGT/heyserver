package db

import (
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

var (
	instance *sql.DB
	once     sync.Once
)

// Open initialises the SQLite database, enables WAL mode and runs all pending
// migrations. It is safe to call multiple times — only the first call does work.
func Open(path string) (*sql.DB, error) {
	var initErr error

	once.Do(func() {
		dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
		db, err := sql.Open("sqlite3", dsn)
		if err != nil {
			initErr = fmt.Errorf("open sqlite: %w", err)
			return
		}

		// SQLite performs best with a single writer connection.
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)

		if err = db.Ping(); err != nil {
			initErr = fmt.Errorf("ping sqlite: %w", err)
			return
		}

		if err = runMigrations(db); err != nil {
			initErr = fmt.Errorf("migrations: %w", err)
			return
		}

		instance = db
		slog.Info("database ready", "path", path)
	})

	if initErr != nil {
		return nil, initErr
	}
	return instance, nil
}

// Instance returns the already-opened database. Calls log.Fatal if Open has not been
// called yet — callers must call Open at startup.
func Instance() *sql.DB {
	if instance == nil {
		log.Fatalf("db.Open must be called before db.Instance")
	}
	return instance
}

// migrations is an ordered slice of SQL statements. Each entry is idempotent
// (uses IF NOT EXISTS). A schema_migrations table tracks the applied version.
var migrations = []string{
	// v1: core tables
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version   INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`,

	`CREATE TABLE IF NOT EXISTS users (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		email      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
		name       TEXT    NOT NULL,
		password   TEXT    NOT NULL,
		role       TEXT    NOT NULL DEFAULT 'viewer' CHECK(role IN ('admin','manager','viewer')),
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`,

	`CREATE TABLE IF NOT EXISTS sessions (
		id         TEXT    PRIMARY KEY,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		ip_address TEXT,
		user_agent TEXT,
		last_seen  DATETIME NOT NULL DEFAULT (datetime('now')),
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`,

	`CREATE TABLE IF NOT EXISTS domains (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT    NOT NULL UNIQUE COLLATE NOCASE,
		type        TEXT    NOT NULL DEFAULT 'static' CHECK(type IN ('php','proxy','static','redirect')),
		root        TEXT    NOT NULL DEFAULT '',
		php_version TEXT,
		proxy_port  INTEGER,
		ssl_enabled INTEGER NOT NULL DEFAULT 0,
		is_active   INTEGER NOT NULL DEFAULT 1,
		created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at  DATETIME NOT NULL DEFAULT (datetime('now'))
	)`,

	`CREATE TABLE IF NOT EXISTS audit_logs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL DEFAULT 0,
		user_name  TEXT    NOT NULL DEFAULT '',
		action     TEXT    NOT NULL,
		resource   TEXT    NOT NULL DEFAULT '',
		details    TEXT    NOT NULL DEFAULT '',
		ip_address TEXT    NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`,

	`CREATE TABLE IF NOT EXISTS system_settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`,

	// Indexes for common query patterns
	`CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id    ON audit_logs(user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_logs_action     ON audit_logs(action)`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_user_id      ON sessions(user_id)`,

	// v2: TOTP 2FA support — add columns to existing users table
	`ALTER TABLE users ADD COLUMN totp_secret  TEXT    NOT NULL DEFAULT ''`,
	`ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0`,

	// v3: TOTP recovery codes — hashed, single-use
	`CREATE TABLE IF NOT EXISTS totp_recovery_codes (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		code_hash  TEXT    NOT NULL,
		used       INTEGER NOT NULL DEFAULT 0,
		used_at    DATETIME,
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE INDEX IF NOT EXISTS idx_totp_recovery_codes_user_id ON totp_recovery_codes(user_id)`,
}

func runMigrations(db *sql.DB) error {
	// Ensure the tracking table exists first.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var currentVersion int
	_ = db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentVersion)

	for i, stmt := range migrations {
		version := i + 1
		if version <= currentVersion {
			continue
		}

		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migration v%d: %w", version, err)
		}

		// schema_migrations table itself is migration #1 — don't double-insert.
		if i > 0 {
			if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES(?)`, version); err != nil {
				return fmt.Errorf("record migration v%d: %w", version, err)
			}
		} else {
			if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES(1)`); err != nil {
				return fmt.Errorf("record migration v1: %w", err)
			}
		}
	}

	slog.Debug("migrations complete", "total", len(migrations))
	return nil
}
