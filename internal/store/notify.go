package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

func MigrateNotify(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS notification_channels (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, type TEXT NOT NULL, config TEXT NOT NULL DEFAULT '{}', enabled INTEGER NOT NULL DEFAULT 1, config_revision INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')))`,
		`CREATE TABLE IF NOT EXISTS notification_delivery_receipts (
			channel_id              INTEGER PRIMARY KEY REFERENCES notification_channels(id) ON DELETE CASCADE,
			channel_config_revision INTEGER NOT NULL CHECK(channel_config_revision > 0),
			outcome                 TEXT NOT NULL CHECK(outcome IN ('success', 'failure')),
			source                  TEXT NOT NULL CHECK(source IN ('manual_test', 'alert', 'uptime', 'backup')),
			observed_at             TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS alert_rules (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, type TEXT NOT NULL, threshold REAL NOT NULL DEFAULT 0, duration_mins INTEGER NOT NULL DEFAULT 0, target TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, cooldown_mins INTEGER NOT NULL DEFAULT 15, created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')))`,
		`CREATE TABLE IF NOT EXISTS alert_history (id INTEGER PRIMARY KEY AUTOINCREMENT, rule_id INTEGER NOT NULL, rule_name TEXT NOT NULL, type TEXT NOT NULL, message TEXT NOT NULL, value REAL NOT NULL DEFAULT 0, fired_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')))`,
		`CREATE INDEX IF NOT EXISTS idx_alert_history_fired_at ON alert_history(fired_at)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_history_rule_id ON alert_history(rule_id)`,
		`UPDATE alert_rules SET type='cpu_usage' WHERE type='cpu'`,
		`UPDATE alert_rules SET type='memory_usage' WHERE type='memory'`,
		`UPDATE alert_rules SET type='disk_usage' WHERE type='disk'`,
		`UPDATE alert_rules SET target='/' WHERE type='disk_usage' AND trim(target)=''`,
		`UPDATE alert_rules SET threshold=1 WHERE type='service_down'`,
		`UPDATE alert_history SET type='cpu_usage' WHERE type='cpu'`,
		`UPDATE alert_history SET type='memory_usage' WHERE type='memory'`,
		`UPDATE alert_history SET type='disk_usage' WHERE type='disk'`,
	}
	// Existing installations predate config_revision. Inspect the live table
	// before adding the column so an in-place migration remains idempotent.
	if _, err := db.Exec(stmts[0]); err != nil {
		return fmt.Errorf("MigrateNotify: %w", err)
	}
	if err := ensureNotificationChannelConfigRevision(db); err != nil {
		return fmt.Errorf("MigrateNotify: %w", err)
	}
	for _, s := range stmts[1:] {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("MigrateNotify: %w", err)
		}
	}
	return nil
}

func ensureNotificationChannelConfigRevision(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(notification_channels)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var found bool
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "config_revision" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE notification_channels ADD COLUMN config_revision INTEGER NOT NULL DEFAULT 1`); err != nil {
		return err
	}
	return nil
}

func (r *NotificationChannelRepository) List() ([]models.NotificationChannel, error) {
	return r.ListContext(context.Background())
}
func (r *NotificationChannelRepository) ListContext(ctx context.Context) ([]models.NotificationChannel, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, type, config, enabled, config_revision, created_at, updated_at FROM notification_channels ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []models.NotificationChannel
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		if err := r.hydrateChannelConfig(c); err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (r *NotificationChannelRepository) Get(id int64) (*models.NotificationChannel, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	c, err := scanChannel(r.db.QueryRow(`SELECT id, name, type, config, enabled, config_revision, created_at, updated_at FROM notification_channels WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := r.hydrateChannelConfig(c); err != nil {
		return nil, err
	}
	return c, nil
}
func (r *NotificationChannelRepository) Create(c *models.NotificationChannel) error {
	if err := r.ready(); err != nil {
		return err
	}
	if err := validateNotificationChannelConfig(c.Config); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`INSERT INTO notification_channels(name, type, config, enabled, config_revision, created_at, updated_at) VALUES(?,?,?,?,?,?,?)`, c.Name, c.Type, `{}`, boolInt(c.Enabled), 1, now, now)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if err := r.writeChannelConfig(id, c.Config); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = r.removeChannelConfig(id)
		}
	}()
	if _, err := tx.Exec(`UPDATE notification_channels SET config = ? WHERE id = ?`, r.channelConfigReference(id), id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	cleanup = false
	c.ID = id
	c.ConfigRevision = 1
	c.CreatedAt, _ = time.Parse(time.RFC3339, now)
	c.UpdatedAt = c.CreatedAt
	return nil
}
func (r *NotificationChannelRepository) Update(c *models.NotificationChannel) error {
	return r.updateWithConfigWriter(c, r.writeChannelConfig)
}

// notificationChannelUpdateState is the database state that must be restored
// when the protected config replacement fails after the revision fence has
// been committed. The config reference is included deliberately: keeping the
// database row and the protected file reference in lockstep is part of the
// rollback boundary.
type notificationChannelUpdateState struct {
	name           string
	config         string
	enabled        int
	configRevision int64
	updatedAt      string
}

// updateWithConfigWriter performs a channel update with a durable revision
// fence. The database revision is advanced into a non-readable pending state
// before the protected file can be replaced. Readers cannot send with the new
// revision until the protected write has completed and the canonical file
// reference is published.
//
// The writer parameter is kept as a small internal seam so the failure and
// ordering boundary can be tested without relying on filesystem races.
func (r *NotificationChannelRepository) updateWithConfigWriter(c *models.NotificationChannel, writeConfig func(int64, string) error) error {
	if err := r.ready(); err != nil {
		return err
	}
	if err := validateNotificationChannelConfig(c.Config); err != nil {
		return err
	}
	var previous notificationChannelUpdateState
	if err := r.db.QueryRow(`SELECT name, config, enabled, config_revision, updated_at FROM notification_channels WHERE id = ?`, c.ID).
		Scan(&previous.name, &previous.config, &previous.enabled, &previous.configRevision, &previous.updatedAt); err != nil {
		return err
	}
	oldConfig, err := r.readChannelConfig(c.ID, previous.config)
	if err != nil {
		return err
	}

	// Fence old receipts and make the channel unreadable before touching the
	// protected file. Exec is an autocommitted SQLite statement, so a crash
	// after this point leaves the higher revision durable. Startup recovery
	// promotes the complete protected file that survived the atomic rename.
	now := time.Now().UTC().Format(time.RFC3339)
	pendingReference := r.channelConfigPendingReference(c.ID)
	res, err := r.db.Exec(`UPDATE notification_channels SET name=?, config=?, enabled=?, config_revision=config_revision+1, updated_at=? WHERE id=? AND config_revision=? AND config=?`, c.Name, pendingReference, boolInt(c.Enabled), now, c.ID, previous.configRevision, previous.config)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	configRevision := previous.configRevision + 1

	// Only a successful protected-file write completes the update. If it
	// fails, restore the old file first; the old database revision is restored
	// only after that succeeds. If either recovery step fails, leaving the
	// higher revision in place is intentional: it fences any old receipt rather
	// than ever treating an uncertain file state as the old configuration.
	if err := writeConfig(c.ID, c.Config); err != nil {
		if restoreErr := r.writeChannelConfig(c.ID, oldConfig); restoreErr != nil {
			return fmt.Errorf("update notification channel: %v (restore protected config: %w)", err, restoreErr)
		}
		if rollbackErr := r.rollbackNotificationChannelUpdate(c.ID, previous, configRevision, pendingReference); rollbackErr != nil {
			return fmt.Errorf("update notification channel: %v (restore database state: %w)", err, rollbackErr)
		}
		return err
	}

	res, err = r.db.Exec(`UPDATE notification_channels SET config=? WHERE id=? AND config_revision=? AND config=?`, r.channelConfigReference(c.ID), c.ID, configRevision, pendingReference)
	if err != nil {
		return fmt.Errorf("publish notification channel config: %w", err)
	}
	affected, err = res.RowsAffected()
	if err != nil {
		return fmt.Errorf("publish notification channel config: %w", err)
	}
	if affected != 1 {
		return errors.New("publish notification channel config: concurrent update")
	}

	c.ConfigRevision = configRevision
	c.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func (r *NotificationChannelRepository) rollbackNotificationChannelUpdate(id int64, previous notificationChannelUpdateState, fencedRevision int64, pendingReference string) error {
	res, err := r.db.Exec(`UPDATE notification_channels SET name=?, config=?, enabled=?, config_revision=?, updated_at=? WHERE id=? AND config_revision=? AND config=?`, previous.name, previous.config, previous.enabled, previous.configRevision, previous.updatedAt, id, fencedRevision, pendingReference)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (r *NotificationChannelRepository) Delete(id int64) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.deleteChannel(id)
}
func scanChannel(s interface{ Scan(...any) error }) (*models.NotificationChannel, error) {
	var c models.NotificationChannel
	var enabled int
	var ca, ua string
	if err := s.Scan(&c.ID, &c.Name, &c.Type, &c.Config, &enabled, &c.ConfigRevision, &ca, &ua); err != nil {
		return nil, err
	}
	c.Enabled = enabled == 1
	c.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	c.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return &c, nil
}

type AlertRuleRepository struct{ db *sql.DB }

func NewAlertRuleRepository(db *sql.DB) *AlertRuleRepository { return &AlertRuleRepository{db: db} }
func (r *AlertRuleRepository) List() ([]models.AlertRule, error) {
	rows, err := r.db.Query(`SELECT id, name, type, threshold, duration_mins, target, enabled, cooldown_mins, created_at, updated_at FROM alert_rules ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]models.AlertRule, 0)
	for rows.Next() {
		rule, err := scanAlertRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}
func (r *AlertRuleRepository) Get(id int64) (*models.AlertRule, error) {
	rule, err := scanAlertRule(r.db.QueryRow(`SELECT id, name, type, threshold, duration_mins, target, enabled, cooldown_mins, created_at, updated_at FROM alert_rules WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return rule, err
}
func (r *AlertRuleRepository) Create(rule *models.AlertRule) error {
	if rule.CooldownMins <= 0 {
		rule.CooldownMins = 15
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.Exec(`INSERT INTO alert_rules(name, type, threshold, duration_mins, target, enabled, cooldown_mins, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		rule.Name, rule.Type, rule.Threshold, rule.DurationMins, rule.Target, boolInt(rule.Enabled), rule.CooldownMins, now, now)
	if err != nil {
		return err
	}
	rule.ID, _ = res.LastInsertId()
	rule.CreatedAt, _ = time.Parse(time.RFC3339, now)
	rule.UpdatedAt = rule.CreatedAt
	return nil
}
func (r *AlertRuleRepository) Update(rule *models.AlertRule) error {
	if rule.CooldownMins <= 0 {
		rule.CooldownMins = 15
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.Exec(`UPDATE alert_rules SET name=?, type=?, threshold=?, duration_mins=?, target=?, enabled=?, cooldown_mins=?, updated_at=? WHERE id=?`,
		rule.Name, rule.Type, rule.Threshold, rule.DurationMins, rule.Target, boolInt(rule.Enabled), rule.CooldownMins, now, rule.ID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	rule.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}
func (r *AlertRuleRepository) Delete(id int64) error {
	res, err := r.db.Exec(`DELETE FROM alert_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func scanAlertRule(s interface{ Scan(...any) error }) (*models.AlertRule, error) {
	var rule models.AlertRule
	var enabled int
	var ca, ua string
	if err := s.Scan(&rule.ID, &rule.Name, &rule.Type, &rule.Threshold, &rule.DurationMins, &rule.Target, &enabled, &rule.CooldownMins, &ca, &ua); err != nil {
		return nil, err
	}
	rule.Enabled = enabled == 1
	rule.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	rule.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return &rule, nil
}

type AlertHistoryRepository struct{ db *sql.DB }

func NewAlertHistoryRepository(db *sql.DB) *AlertHistoryRepository {
	return &AlertHistoryRepository{db: db}
}
func (r *AlertHistoryRepository) Insert(h *models.AlertHistory) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.Exec(`INSERT INTO alert_history(rule_id, rule_name, type, message, value, fired_at) VALUES(?,?,?,?,?,?)`, h.RuleID, h.RuleName, h.Type, h.Message, h.Value, now)
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	h.FiredAt, _ = time.Parse(time.RFC3339, now)
	return nil
}
func (r *AlertHistoryRepository) LastFiredAt(ruleID int64) (time.Time, error) {
	var firedAt string
	err := r.db.QueryRow(`SELECT fired_at FROM alert_history WHERE rule_id = ? ORDER BY fired_at DESC LIMIT 1`, ruleID).Scan(&firedAt)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, _ := time.Parse(time.RFC3339, firedAt)
	return t, nil
}
func (r *AlertHistoryRepository) List(limit, offset int) ([]models.AlertHistory, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var total int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM alert_history`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(`SELECT id, rule_id, rule_name, type, message, value, fired_at FROM alert_history ORDER BY fired_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]models.AlertHistory, 0)
	for rows.Next() {
		var h models.AlertHistory
		var firedAt string
		if err := rows.Scan(&h.ID, &h.RuleID, &h.RuleName, &h.Type, &h.Message, &h.Value, &firedAt); err != nil {
			return nil, 0, err
		}
		h.FiredAt, _ = time.Parse(time.RFC3339, firedAt)
		out = append(out, h)
	}
	return out, total, rows.Err()
}
func (r *AlertHistoryRepository) PruneOlderThan(age time.Duration) error {
	_, err := r.db.Exec(`DELETE FROM alert_history WHERE fired_at < ?`, time.Now().UTC().Add(-age).Format(time.RFC3339))
	return err
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
