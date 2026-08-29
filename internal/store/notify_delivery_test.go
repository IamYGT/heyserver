package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

func openNotificationDeliveryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "notify-delivery.db")+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := store.MigrateNotify(db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertNotificationDeliveryChannel(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO notification_channels(name, type, config, enabled) VALUES(?,?,?,1)`, "Delivery test", "email", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func validNotificationDeliveryReceipt(channelID int64) models.NotificationDeliveryReceipt {
	return models.NotificationDeliveryReceipt{
		ChannelID:             channelID,
		ChannelConfigRevision: 1,
		Outcome:               models.NotificationDeliveryOutcomeSuccess,
		Source:                models.NotificationDeliverySourceManualTest,
		ObservedAt:            time.Date(2026, time.August, 28, 18, 0, 0, 123456789, time.FixedZone("EET", 2*60*60)),
	}
}

func TestMigrateNotifyAddsConfigRevisionAndDeliveryTableIdempotently(t *testing.T) {
	t.Parallel()
	db := openNotificationDeliveryDB(t)

	if err := store.MigrateNotify(db); err != nil {
		t.Fatalf("second MigrateNotify: %v", err)
	}

	channelID := insertNotificationDeliveryChannel(t, db)
	var revision int64
	if err := db.QueryRow(`SELECT config_revision FROM notification_channels WHERE id = ?`, channelID).Scan(&revision); err != nil {
		t.Fatalf("config_revision query: %v", err)
	}
	if revision != 1 {
		t.Fatalf("new config revision = %d, want 1", revision)
	}

	var tableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='notification_delivery_receipts'`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 1 {
		t.Fatalf("delivery receipt table count = %d, want 1", tableCount)
	}

	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('notification_delivery_receipts')`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if columnCount != 5 {
		t.Fatalf("delivery receipt column count = %d, want 5", columnCount)
	}
}

func TestMigrateNotifyAddsConfigRevisionToLegacyChannelsInPlace(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "legacy-notify.db")
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE notification_channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		config TEXT NOT NULL DEFAULT '{}',
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
		updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO notification_channels(name, type) VALUES('legacy', 'email')`); err != nil {
		t.Fatal(err)
	}

	if err := store.MigrateNotify(db); err != nil {
		t.Fatalf("MigrateNotify legacy: %v", err)
	}
	if err := store.MigrateNotify(db); err != nil {
		t.Fatalf("MigrateNotify legacy rerun: %v", err)
	}
	var revision int64
	if err := db.QueryRow(`SELECT config_revision FROM notification_channels WHERE id=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 1 {
		t.Fatalf("legacy config_revision = %d, want 1", revision)
	}
}

func TestNotificationDeliveryReceiptRepository_UpsertOverwritesLatest(t *testing.T) {
	t.Parallel()
	db := openNotificationDeliveryDB(t)
	channelID := insertNotificationDeliveryChannel(t, db)
	repo := store.NewNotificationDeliveryReceiptRepository(db)

	first := validNotificationDeliveryReceipt(channelID)
	if err := repo.UpsertContext(context.Background(), &first); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	second := first
	second.ChannelConfigRevision = 2
	second.Outcome = models.NotificationDeliveryOutcomeFailure
	second.Source = models.NotificationDeliverySourceAlert
	second.ObservedAt = time.Date(2026, time.August, 28, 18, 1, 2, 987654321, time.UTC)
	if err := repo.UpsertContext(context.Background(), &second); err != nil {
		t.Fatalf("upsert second: %v", err)
	}

	got, err := repo.GetContext(context.Background(), channelID)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got == nil {
		t.Fatal("get latest returned nil")
	}
	if got.ChannelID != second.ChannelID || got.ChannelConfigRevision != second.ChannelConfigRevision || got.Outcome != second.Outcome || got.Source != second.Source || !got.ObservedAt.Equal(second.ObservedAt) {
		t.Fatalf("latest receipt = %+v, want %+v", got, second)
	}

	list, err := repo.ListContext(context.Background())
	if err != nil {
		t.Fatalf("list latest: %v", err)
	}
	if len(list) != 1 || list[0].ChannelID != channelID {
		t.Fatalf("latest list = %+v, want one row for %d", list, channelID)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notification_delivery_receipts WHERE channel_id=?`, channelID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("receipt count = %d, want 1", count)
	}
}

func TestNotificationDeliveryReceiptRepository_UpsertDoesNotRegressConfigRevision(t *testing.T) {
	t.Parallel()
	db := openNotificationDeliveryDB(t)
	channelID := insertNotificationDeliveryChannel(t, db)
	repo := store.NewNotificationDeliveryReceiptRepository(db)

	latest := validNotificationDeliveryReceipt(channelID)
	latest.ChannelConfigRevision = 2
	latest.ObservedAt = time.Date(2026, time.August, 28, 18, 0, 0, 900000000, time.UTC)
	latest.Outcome = models.NotificationDeliveryOutcomeFailure
	latest.Source = models.NotificationDeliverySourceAlert
	if err := repo.UpsertContext(context.Background(), &latest); err != nil {
		t.Fatalf("upsert latest revision: %v", err)
	}

	delayed := latest
	delayed.ChannelConfigRevision = 1
	delayed.ObservedAt = latest.ObservedAt.Add(time.Hour)
	delayed.Outcome = models.NotificationDeliveryOutcomeSuccess
	delayed.Source = models.NotificationDeliverySourceUptime
	if err := repo.UpsertContext(context.Background(), &delayed); err != nil {
		t.Fatalf("upsert delayed revision: %v", err)
	}

	got, err := repo.GetContext(context.Background(), channelID)
	if err != nil {
		t.Fatalf("get receipt: %v", err)
	}
	if got == nil {
		t.Fatal("get receipt returned nil")
	}
	if got.ChannelConfigRevision != latest.ChannelConfigRevision || got.Outcome != latest.Outcome || got.Source != latest.Source || !got.ObservedAt.Equal(latest.ObservedAt) {
		t.Fatalf("receipt after delayed lower revision = %+v, want %+v", got, latest)
	}
}

func TestNotificationDeliveryReceiptRepository_UpsertRejectsOlderSameRevisionTimestamp(t *testing.T) {
	t.Parallel()
	db := openNotificationDeliveryDB(t)
	channelID := insertNotificationDeliveryChannel(t, db)
	repo := store.NewNotificationDeliveryReceiptRepository(db)

	latest := validNotificationDeliveryReceipt(channelID)
	latest.ChannelConfigRevision = 2
	latest.ObservedAt = time.Date(2026, time.August, 28, 18, 0, 0, 100000000, time.UTC)
	if err := repo.UpsertContext(context.Background(), &latest); err != nil {
		t.Fatalf("upsert latest timestamp: %v", err)
	}

	delayed := latest
	delayed.ObservedAt = latest.ObservedAt.Add(-time.Nanosecond)
	delayed.Outcome = models.NotificationDeliveryOutcomeFailure
	delayed.Source = models.NotificationDeliverySourceAlert
	if err := repo.UpsertContext(context.Background(), &delayed); err != nil {
		t.Fatalf("upsert delayed timestamp: %v", err)
	}

	got, err := repo.GetContext(context.Background(), channelID)
	if err != nil {
		t.Fatalf("get receipt: %v", err)
	}
	if got == nil {
		t.Fatal("get receipt returned nil")
	}
	if got.ChannelConfigRevision != latest.ChannelConfigRevision || got.Outcome != latest.Outcome || got.Source != latest.Source || !got.ObservedAt.Equal(latest.ObservedAt) {
		t.Fatalf("receipt after delayed same-revision timestamp = %+v, want %+v", got, latest)
	}
}

func TestNotificationDeliveryReceiptRepository_ValidatesBoundedFields(t *testing.T) {
	t.Parallel()
	db := openNotificationDeliveryDB(t)
	channelID := insertNotificationDeliveryChannel(t, db)
	repo := store.NewNotificationDeliveryReceiptRepository(db)
	valid := validNotificationDeliveryReceipt(channelID)

	tests := []struct {
		name   string
		mutate func(*models.NotificationDeliveryReceipt)
	}{
		{name: "nil", mutate: nil},
		{name: "non-positive channel id", mutate: func(r *models.NotificationDeliveryReceipt) { r.ChannelID = 0 }},
		{name: "non-positive config revision", mutate: func(r *models.NotificationDeliveryReceipt) { r.ChannelConfigRevision = 0 }},
		{name: "unsupported outcome", mutate: func(r *models.NotificationDeliveryReceipt) { r.Outcome = "pending" }},
		{name: "unsupported source", mutate: func(r *models.NotificationDeliveryReceipt) { r.Source = "manual" }},
		{name: "zero observed time", mutate: func(r *models.NotificationDeliveryReceipt) { r.ObservedAt = time.Time{} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var receipt *models.NotificationDeliveryReceipt
			if tc.mutate != nil {
				copy := valid
				tc.mutate(&copy)
				receipt = &copy
			}
			if err := repo.UpsertContext(context.Background(), receipt); !errors.Is(err, store.ErrInvalidNotificationDeliveryReceipt) {
				t.Fatalf("validation error = %v, want ErrInvalidNotificationDeliveryReceipt", err)
			}
		})
	}

	if _, err := repo.GetContext(context.Background(), 0); !errors.Is(err, store.ErrInvalidNotificationDeliveryReceipt) {
		t.Fatalf("invalid Get channel ID error = %v", err)
	}
}

func TestNotificationDeliveryReceiptRepository_ContextCancellation(t *testing.T) {
	t.Parallel()
	db := openNotificationDeliveryDB(t)
	channelID := insertNotificationDeliveryChannel(t, db)
	repo := store.NewNotificationDeliveryReceiptRepository(db)
	receipt := validNotificationDeliveryReceipt(channelID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := repo.UpsertContext(ctx, &receipt); !errors.Is(err, context.Canceled) {
		t.Fatalf("UpsertContext error = %v, want context.Canceled", err)
	}
	if _, err := repo.GetContext(ctx, channelID); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetContext error = %v, want context.Canceled", err)
	}
	if _, err := repo.ListContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListContext error = %v, want context.Canceled", err)
	}
}

func TestNotificationDeliveryReceiptRepository_CascadesWithChannelDelete(t *testing.T) {
	t.Parallel()
	db := openNotificationDeliveryDB(t)
	channelID := insertNotificationDeliveryChannel(t, db)
	repo := store.NewNotificationDeliveryReceiptRepository(db)
	receipt := validNotificationDeliveryReceipt(channelID)
	if err := repo.UpsertContext(context.Background(), &receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM notification_channels WHERE id=?`, channelID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notification_delivery_receipts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("receipt rows after channel delete = %d, want 0", count)
	}
}

func TestNotificationDeliveryReceiptRepository_SchemaIsBounded(t *testing.T) {
	t.Parallel()
	db := openNotificationDeliveryDB(t)
	rows, err := db.Query(`PRAGMA table_info(notification_delivery_receipts)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"channel_id", "channel_config_revision", "outcome", "source", "observed_at"}
	if strings.Join(columns, ",") != strings.Join(want, ",") {
		t.Fatalf("receipt columns = %v, want %v", columns, want)
	}
	forbidden := []string{"payload", "destination", "provider_error", "secret", "subject", "body", "fingerprint"}
	for _, column := range columns {
		for _, word := range forbidden {
			if strings.Contains(strings.ToLower(column), word) {
				t.Fatalf("forbidden receipt column %q contains %q", column, word)
			}
		}
	}
}

func TestNotificationChannelRepository_ConfigRevisionFencesOldReceipt(t *testing.T) {
	t.Parallel()
	db := openNotificationDeliveryDB(t)
	channelRepo, err := store.NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	channel := &models.NotificationChannel{
		Name:    "Revision test",
		Type:    models.ChannelEmail,
		Config:  `{"host":"smtp.example.com","port":587}`,
		Enabled: true,
	}
	if err := channelRepo.Create(channel); err != nil {
		t.Fatal(err)
	}
	if channel.ConfigRevision != 1 {
		t.Fatalf("created channel revision = %d, want 1", channel.ConfigRevision)
	}
	receiptRepo := store.NewNotificationDeliveryReceiptRepository(db)
	receipt := validNotificationDeliveryReceipt(channel.ID)
	if err := receiptRepo.UpsertContext(context.Background(), &receipt); err != nil {
		t.Fatal(err)
	}

	channel.Name = "Revision test updated"
	if err := channelRepo.Update(channel); err != nil {
		t.Fatal(err)
	}
	if channel.ConfigRevision != 2 {
		t.Fatalf("updated channel revision = %d, want 2", channel.ConfigRevision)
	}
	got, err := receiptRepo.GetContext(context.Background(), channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ChannelConfigRevision != 1 {
		t.Fatalf("old receipt after config update = %+v, want revision 1 for fencing", got)
	}
	if got.ChannelConfigRevision == channel.ConfigRevision {
		t.Fatal("old receipt unexpectedly matches new channel config revision")
	}
}

func TestNotificationDeliveryReceiptRepository_ConcurrentUpserts(t *testing.T) {
	db := openNotificationDeliveryDB(t)
	channelID := insertNotificationDeliveryChannel(t, db)
	repo := store.NewNotificationDeliveryReceiptRepository(db)

	const writers = 16
	errCh := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt := validNotificationDeliveryReceipt(channelID)
			receipt.ChannelConfigRevision = int64(i + 1)
			receipt.ObservedAt = time.Date(2026, time.August, 28, 19, 0, i, i, time.UTC)
			if i%2 == 0 {
				receipt.Outcome = models.NotificationDeliveryOutcomeFailure
				receipt.Source = models.NotificationDeliverySourceAlert
			}
			errCh <- repo.UpsertContext(context.Background(), &receipt)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent upsert: %v", err)
		}
	}
	list, err := repo.ListContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ChannelID != channelID {
		t.Fatalf("concurrent receipt list = %+v, want one row for %d", list, channelID)
	}
}
