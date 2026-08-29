package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	_ "github.com/mattn/go-sqlite3"
)

func openNotificationChannelCrashTest(t *testing.T) (*sql.DB, *NotificationChannelRepository, *models.NotificationChannel) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "notify-crash.db")+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := MigrateNotify(db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo, err := NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	channel := &models.NotificationChannel{
		Name:    "Crash fence",
		Type:    models.ChannelEmail,
		Config:  `{"host":"smtp.example.com","port":587}`,
		Enabled: true,
	}
	if err := repo.Create(channel); err != nil {
		t.Fatal(err)
	}
	return db, repo, channel
}

func insertNotificationChannelCrashReceipt(t *testing.T, db *sql.DB, channelID int64) {
	t.Helper()
	receipt := &models.NotificationDeliveryReceipt{
		ChannelID:             channelID,
		ChannelConfigRevision: 1,
		Outcome:               models.NotificationDeliveryOutcomeSuccess,
		Source:                models.NotificationDeliverySourceManualTest,
		ObservedAt:            time.Date(2026, time.August, 28, 18, 0, 0, 0, time.UTC),
	}
	if err := NewNotificationDeliveryReceiptRepository(db).UpsertContext(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationChannelUpdateFencesBeforeProtectedConfigWrite(t *testing.T) {
	db, repo, channel := openNotificationChannelCrashTest(t)
	insertNotificationChannelCrashReceipt(t, db, channel.ID)

	candidate := *channel
	candidate.Name = "Crash fence updated"
	candidate.Config = `{"host":"smtp-new.example.com","port":587}`
	writerCalled := false
	err := repo.updateWithConfigWriter(&candidate, func(id int64, config string) error {
		writerCalled = true
		var revision int64
		var stored string
		if err := db.QueryRow(`SELECT config_revision, config FROM notification_channels WHERE id = ?`, id).Scan(&revision, &stored); err != nil {
			t.Fatal(err)
		}
		if revision != 2 {
			t.Fatalf("revision visible before protected write = %d, want 2", revision)
		}
		if stored != repo.channelConfigPendingReference(id) {
			t.Fatalf("config reference during protected write = %q, want pending reference", stored)
		}
		if got, getErr := repo.Get(id); getErr == nil || got != nil {
			t.Fatalf("channel was readable during protected write: channel=%+v err=%v", got, getErr)
		}
		var receiptRevision int64
		if err := db.QueryRow(`SELECT channel_config_revision FROM notification_delivery_receipts WHERE channel_id = ?`, id).Scan(&receiptRevision); err != nil {
			t.Fatal(err)
		}
		if receiptRevision == revision {
			t.Fatalf("old receipt revision %d still matches fenced revision %d", receiptRevision, revision)
		}
		return repo.writeChannelConfig(id, config)
	})
	if err != nil {
		t.Fatalf("fenced update: %v", err)
	}
	if !writerCalled {
		t.Fatal("protected config writer was not called")
	}
	if candidate.ConfigRevision != 2 {
		t.Fatalf("updated config revision = %d, want 2", candidate.ConfigRevision)
	}
	got, err := repo.Get(channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Config != candidate.Config || got.Name != candidate.Name || got.ConfigRevision != 2 {
		t.Fatalf("updated channel = %+v, want config/name/revision from candidate", got)
	}
}

func TestNotificationChannelRepositoryRecoversPendingConfigWithoutRevalidatingOldReceipt(t *testing.T) {
	db, repo, channel := openNotificationChannelCrashTest(t)
	insertNotificationChannelCrashReceipt(t, db, channel.ID)

	res, err := db.Exec(`UPDATE notification_channels SET config=?, config_revision=config_revision+1 WHERE id=? AND config_revision=?`, repo.channelConfigPendingReference(channel.ID), channel.ID, channel.ConfigRevision)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := res.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("stage interrupted update: affected=%d err=%v", affected, err)
	}

	recovered, err := NewNotificationChannelRepository(db, filepath.Dir(repo.secretDir))
	if err != nil {
		t.Fatalf("recover pending config: %v", err)
	}
	got, err := recovered.Get(channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Config != channel.Config || got.ConfigRevision != 2 {
		t.Fatalf("recovered channel = %+v, want protected config at fenced revision 2", got)
	}
	receipt, err := NewNotificationDeliveryReceiptRepository(db).GetContext(context.Background(), channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || receipt.ChannelConfigRevision == got.ConfigRevision {
		t.Fatalf("recovery revalidated old receipt: receipt=%+v channel_revision=%d", receipt, got.ConfigRevision)
	}
}

func TestNotificationChannelUpdateFailureRestoresFileAndRevision(t *testing.T) {
	db, repo, channel := openNotificationChannelCrashTest(t)
	insertNotificationChannelCrashReceipt(t, db, channel.ID)

	old := *channel
	candidate := *channel
	candidate.Name = "failed update"
	candidate.Config = `{"host":"smtp-new.example.com","port":587}`
	writeErr := errors.New("protected config write failed")
	err := repo.updateWithConfigWriter(&candidate, func(id int64, config string) error {
		// Simulate a failure after the atomic replacement has happened. Update
		// must restore the old file before returning to the old revision.
		if err := repo.writeChannelConfig(id, config); err != nil {
			return err
		}
		return writeErr
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("failed update error = %v, want %v", err, writeErr)
	}
	got, err := repo.Get(channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Config != old.Config || got.Name != old.Name || got.ConfigRevision != old.ConfigRevision {
		t.Fatalf("state after failed update = %+v, want original file/name/revision", got)
	}
	var receiptRevision int64
	if err := db.QueryRow(`SELECT channel_config_revision FROM notification_delivery_receipts WHERE channel_id = ?`, channel.ID).Scan(&receiptRevision); err != nil {
		t.Fatal(err)
	}
	if receiptRevision != got.ConfigRevision {
		t.Fatalf("restored receipt revision = %d, channel revision = %d", receiptRevision, got.ConfigRevision)
	}
}

func TestNotificationChannelUpdateKeepsFenceWhenFileRecoveryFails(t *testing.T) {
	db, repo, channel := openNotificationChannelCrashTest(t)
	insertNotificationChannelCrashReceipt(t, db, channel.ID)

	candidate := *channel
	candidate.Config = `{"host":"smtp-new.example.com","port":587}`
	writeErr := errors.New("protected config write failed after replace")
	blockedDir := repo.secretDir + ".blocked"
	err := repo.updateWithConfigWriter(&candidate, func(id int64, config string) error {
		if err := repo.writeChannelConfig(id, config); err != nil {
			return err
		}
		if err := os.Rename(repo.secretDir, blockedDir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(repo.secretDir, []byte("blocked"), 0o600); err != nil {
			t.Fatal(err)
		}
		return writeErr
	})
	// Restore the test-only directory so TempDir cleanup can remove it.
	if removeErr := os.Remove(repo.secretDir); removeErr != nil {
		t.Fatalf("remove blocked secret path: %v (update error: %v)", removeErr, err)
	}
	if restoreErr := os.Rename(blockedDir, repo.secretDir); restoreErr != nil {
		t.Fatalf("restore secret directory: %v (update error: %v)", restoreErr, err)
	}
	if err == nil {
		t.Fatal("failed update unexpectedly succeeded")
	}
	var revision int64
	if err := db.QueryRow(`SELECT config_revision FROM notification_channels WHERE id = ?`, channel.ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 2 {
		t.Fatalf("revision after failed recovery = %d, want fenced revision 2", revision)
	}
	var receiptRevision int64
	if err := db.QueryRow(`SELECT channel_config_revision FROM notification_delivery_receipts WHERE channel_id = ?`, channel.ID).Scan(&receiptRevision); err != nil {
		t.Fatal(err)
	}
	if receiptRevision == revision {
		t.Fatalf("old receipt revision %d unexpectedly matches fenced revision %d", receiptRevision, revision)
	}
	if got, getErr := repo.Get(channel.ID); getErr == nil || got != nil {
		t.Fatalf("uncertain channel became readable before recovery: channel=%+v err=%v", got, getErr)
	}
}
