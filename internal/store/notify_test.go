package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

func openNotifyDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify.db")
	db, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateNotify(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNotificationChannelRepository_CRUD(t *testing.T) {
	t.Parallel()
	db := openNotifyDB(t)
	dataDir := t.TempDir()
	repo, err := store.NewNotificationChannelRepository(db, dataDir)
	if err != nil {
		t.Fatal(err)
	}

	ch := &models.NotificationChannel{
		Name:    "Ops Slack",
		Type:    "slack",
		Config:  `{"webhookUrl":"https://hooks.example.com/test"}`,
		Enabled: true,
	}
	if err := repo.Create(ch); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`SELECT config FROM notification_channels WHERE id = ?`, ch.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != fmt.Sprintf("file:channel-%d.json", ch.ID) || strings.Contains(stored, "hooks.example.com") {
		t.Fatalf("database config = %q, want deterministic file reference", stored)
	}
	secretPath := filepath.Join(dataDir, "notification-channel-secrets", fmt.Sprintf("channel-%d.json", ch.ID))
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode = %o, want 600", info.Mode().Perm())
	}
	content, err := os.ReadFile(secretPath)
	if err != nil || string(content) != ch.Config {
		t.Fatalf("protected config mismatch: err=%v content=%q", err, content)
	}

	got, err := repo.Get(ch.ID)
	if err != nil || got == nil || got.Name != ch.Name {
		t.Fatalf("Get: err=%v got=%+v", err, got)
	}

	ch.Name = "Ops Slack Updated"
	if err := repo.Update(ch); err != nil {
		t.Fatal(err)
	}

	list, err := repo.List()
	if err != nil || len(list) != 1 || list[0].Name != "Ops Slack Updated" {
		t.Fatalf("List: err=%v list=%+v", err, list)
	}

	if err := repo.Delete(ch.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.List()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
	if _, err := os.Stat(secretPath); !os.IsNotExist(err) {
		t.Fatalf("deleted channel config still exists: %v", err)
	}
}

func TestNotificationChannelRepository_MigratesLegacyConfigToProtectedFile(t *testing.T) {
	t.Parallel()
	db := openNotifyDB(t)
	legacy := `{"botToken":"123456:legacy-secret","chatId":-1001}`
	res, err := db.Exec(`INSERT INTO notification_channels(name, type, config, enabled) VALUES(?,?,?,1)`, "Legacy Telegram", "telegram", legacy)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	dataDir := t.TempDir()
	repo, err := store.NewNotificationChannelRepository(db, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`SELECT config FROM notification_channels WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != fmt.Sprintf("file:channel-%d.json", id) || strings.Contains(stored, "legacy-secret") {
		t.Fatalf("legacy database config = %q", stored)
	}
	channel, err := repo.Get(id)
	if err != nil || channel == nil || channel.Config != legacy {
		t.Fatalf("hydrated legacy channel: err=%v channel=%+v", err, channel)
	}
}

func TestNotificationChannelRepository_ListContextCancellation(t *testing.T) {
	t.Parallel()
	db := openNotifyDB(t)
	repo, err := store.NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := repo.ListContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListContext error = %v, want context.Canceled", err)
	}
}

func TestAlertRuleRepository_CRUD(t *testing.T) {
	t.Parallel()
	db := openNotifyDB(t)
	repo := store.NewAlertRuleRepository(db)

	rule := &models.AlertRule{
		Name:         "High CPU",
		Type:         "cpu",
		Threshold:    90,
		DurationMins: 5,
		Target:       "host",
		Enabled:      true,
	}
	if err := repo.Create(rule); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(rule.ID)
	if err != nil || got == nil || got.Name != rule.Name {
		t.Fatalf("Get: err=%v got=%+v", err, got)
	}

	rule.Threshold = 95
	rule.Type = models.AlertCPUUsage
	if err := repo.Update(rule); err != nil {
		t.Fatal(err)
	}

	list, err := repo.List()
	if err != nil || len(list) != 1 || list[0].Threshold != 95 || list[0].Type != models.AlertCPUUsage {
		t.Fatalf("List: err=%v list=%+v", err, list)
	}
}

func TestAlertRuleRepositoriesReturnEmptyCollections(t *testing.T) {
	t.Parallel()
	db := openNotifyDB(t)
	rules, err := store.NewAlertRuleRepository(db).List()
	if err != nil || rules == nil || len(rules) != 0 {
		t.Fatalf("empty rules = %#v err=%v, want non-nil empty collection", rules, err)
	}
	history, total, err := store.NewAlertHistoryRepository(db).List(50, 0)
	if err != nil || history == nil || len(history) != 0 || total != 0 {
		t.Fatalf("empty history = %#v total=%d err=%v, want non-nil empty collection", history, total, err)
	}
}

func TestMigrateNotifyCanonicalizesLegacyAlertTypes(t *testing.T) {
	t.Parallel()
	db := openNotifyDB(t)
	for _, alertType := range []string{"cpu", "memory", "disk"} {
		if _, err := db.Exec(`INSERT INTO alert_rules(name, type, threshold, cooldown_mins) VALUES(?,?,90,15)`, alertType, alertType); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO alert_history(rule_id, rule_name, type, message) VALUES(1,?,?,?)`, alertType, alertType, "legacy"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO alert_rules(name, type, threshold, target, cooldown_mins) VALUES('service','service_down',0,'nginx.service',15)`); err != nil {
		t.Fatal(err)
	}

	if err := store.MigrateNotify(db); err != nil {
		t.Fatal(err)
	}

	rules, err := store.NewAlertRuleRepository(db).List()
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{models.AlertCPUUsage, models.AlertMemoryUsage, models.AlertDiskUsage, models.AlertServiceDown}
	for i, wantType := range wantTypes {
		if rules[i].Type != wantType {
			t.Fatalf("rule %d type = %q, want %q", i, rules[i].Type, wantType)
		}
	}
	if rules[2].Target != "/" {
		t.Fatalf("legacy disk target = %q, want /", rules[2].Target)
	}
	if rules[3].Threshold != 1 {
		t.Fatalf("legacy service threshold = %v, want 1", rules[3].Threshold)
	}

	history, total, err := store.NewAlertHistoryRepository(db).List(10, 0)
	if err != nil || total != 3 {
		t.Fatalf("history list: total=%d err=%v", total, err)
	}
	seen := map[string]bool{}
	for _, item := range history {
		seen[item.Type] = true
	}
	for _, wantType := range wantTypes[:3] {
		if !seen[wantType] {
			t.Fatalf("canonical history type %q missing from %#v", wantType, seen)
		}
	}
}

func TestAlertHistoryRepository_InsertListPrune(t *testing.T) {
	t.Parallel()
	db := openNotifyDB(t)
	repo := store.NewAlertHistoryRepository(db)
	h := &models.AlertHistory{
		RuleID:   1,
		RuleName: "CPU",
		Type:     "cpu",
		Message:  "high cpu",
		Value:    95,
	}
	if err := repo.Insert(h); err != nil {
		t.Fatal(err)
	}
	last, err := repo.LastFiredAt(1)
	if err != nil || last.IsZero() {
		t.Fatalf("LastFiredAt: err=%v last=%v", err, last)
	}
	list, total, err := repo.List(10, 0)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("List: err=%v total=%d len=%d", err, total, len(list))
	}
	if err := repo.PruneOlderThan(365 * 24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	_, total, _ = repo.List(10, 0)
	if total != 1 {
		t.Errorf("recent history should remain after prune, total=%d", total)
	}
}
