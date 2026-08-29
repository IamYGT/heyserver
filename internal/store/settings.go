package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

func MigrateSettings(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')))`)
	if err != nil {
		return fmt.Errorf("MigrateSettings: %w", err)
	}
	return nil
}

type SettingsRepository struct{ db *sql.DB }

func NewSettingsRepository(db *sql.DB) *SettingsRepository { return &SettingsRepository{db: db} }
func (r *SettingsRepository) Get(key string) (*models.Setting, error) {
	return r.GetContext(context.Background(), key)
}
func (r *SettingsRepository) GetContext(ctx context.Context, key string) (*models.Setting, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var s models.Setting
	var ua string
	err := r.db.QueryRowContext(ctx, `SELECT key, value, updated_at FROM settings WHERE key = ?`, key).Scan(&s.Key, &s.Value, &ua)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("settings get: %w", err)
	}
	s.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return &s, nil
}
func (r *SettingsRepository) GetAll() ([]models.Setting, error) {
	return r.GetAllContext(context.Background())
}
func (r *SettingsRepository) GetAllContext(ctx context.Context) ([]models.Setting, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := r.db.QueryContext(ctx, `SELECT key, value, updated_at FROM settings ORDER BY key ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []models.Setting
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var s models.Setting
		var ua string
		if err := rows.Scan(&s.Key, &s.Value, &ua); err != nil {
			return nil, err
		}
		s.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (r *SettingsRepository) Set(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.Exec(`INSERT INTO settings(key, value, updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, now)
	return err
}
func (r *SettingsRepository) SetMany(pairs map[string]string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	stmt, err := tx.Prepare(`INSERT INTO settings(key, value, updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`)
	if err != nil {
		tx.Rollback()
		return err
	} //nolint:errcheck
	defer func() { _ = stmt.Close() }()
	for k, v := range pairs {
		if _, err := stmt.Exec(k, v, now); err != nil {
			tx.Rollback()
			return err
		} //nolint:errcheck
	}
	return tx.Commit()
}
func (r *SettingsRepository) Delete(key string) error {
	_, err := r.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}
