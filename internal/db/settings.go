package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

// MigrateSettings creates the settings table if it does not exist.
func MigrateSettings(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS settings (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("MigrateSettings: %w", err)
	}
	return nil
}

// SettingsRepository provides key-value CRUD over the settings table.
type SettingsRepository struct{ db *sql.DB }

// NewSettingsRepository creates a SettingsRepository backed by db.
func NewSettingsRepository(db *sql.DB) *SettingsRepository {
	return &SettingsRepository{db: db}
}

// Get returns the Setting for key. Returns (nil, nil) when the key is absent.
func (r *SettingsRepository) Get(key string) (*models.Setting, error) {
	var s models.Setting
	var updatedAt string
	err := r.db.QueryRow(
		`SELECT key, value, updated_at FROM settings WHERE key = ?`, key,
	).Scan(&s.Key, &s.Value, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("settings get %q: %w", key, err)
	}
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &s, nil
}

// GetAll returns all settings ordered by key.
func (r *SettingsRepository) GetAll() ([]models.Setting, error) {
	rows, err := r.db.Query(`SELECT key, value, updated_at FROM settings ORDER BY key ASC`)
	if err != nil {
		return nil, fmt.Errorf("settings list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []models.Setting
	for rows.Next() {
		var s models.Setting
		var updatedAt string
		if err := rows.Scan(&s.Key, &s.Value, &updatedAt); err != nil {
			return nil, err
		}
		s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Set upserts a key-value pair.
func (r *SettingsRepository) Set(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.Exec(
		`INSERT INTO settings(key, value, updated_at) VALUES(?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, now,
	)
	if err != nil {
		return fmt.Errorf("settings set %q: %w", key, err)
	}
	return nil
}

// SetMany upserts multiple key-value pairs inside a single transaction.
func (r *SettingsRepository) SetMany(pairs map[string]string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("settings setmany begin: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	stmt, err := tx.Prepare(
		`INSERT INTO settings(key, value, updated_at) VALUES(?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
	)
	if err != nil {
		tx.Rollback() //nolint:errcheck
		return fmt.Errorf("settings setmany prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for k, v := range pairs {
		if _, err := stmt.Exec(k, v, now); err != nil {
			tx.Rollback() //nolint:errcheck
			return fmt.Errorf("settings setmany exec %q: %w", k, err)
		}
	}
	return tx.Commit()
}

// Delete removes a setting by key. No-op when key is absent.
func (r *SettingsRepository) Delete(key string) error {
	_, err := r.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}
