package gdrive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/notify"
	"github.com/IamYGT/heyserver/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

type gdriveReceiptRecorder struct {
	mu       sync.Mutex
	calls    int
	receipts []*models.NotificationDeliveryReceipt
	err      error
}

func (r *gdriveReceiptRecorder) UpsertContext(_ context.Context, receipt *models.NotificationDeliveryReceipt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return r.err
	}
	copy := *receipt
	r.receipts = append(r.receipts, &copy)
	return nil
}

func newNotificationReceiptService(t *testing.T) *Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gdrive-notify.db")
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, migrate := range []func(*sql.DB) error{store.MigrateSettings, store.MigrateNotify} {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	channelRepo, err := store.NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range []*models.NotificationChannel{
		{
			Name:    "loopback blocked",
			Type:    models.ChannelSlack,
			Config:  `{"webhook_url":"http://127.0.0.1/hook"}`,
			Enabled: true,
		},
		{
			Name:    "unknown provider",
			Type:    "pagerduty",
			Config:  `{}`,
			Enabled: true,
		},
	} {
		if err := channelRepo.Create(channel); err != nil {
			t.Fatalf("Create channel: %v", err)
		}
	}
	return New(t.TempDir(), 0, "client-id", "client-secret", "", "rclone", store.NewSettingsRepository(db), channelRepo)
}

func TestNotifyPersistsBoundedBackupReceiptsForAllEnabledChannels(t *testing.T) {
	t.Parallel()

	service := newNotificationReceiptService(t)
	recorder := &gdriveReceiptRecorder{}
	service.SetReceiptRecorder(recorder)
	service.notify("backup finished", "backup details")

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.calls != 2 {
		t.Fatalf("receipt writes = %d, want one per enabled channel", recorder.calls)
	}
	if len(recorder.receipts) != 2 {
		t.Fatalf("receipts = %d, want one per enabled channel", len(recorder.receipts))
	}
	for _, receipt := range recorder.receipts {
		if receipt.Outcome != models.NotificationDeliveryOutcomeFailure {
			t.Errorf("receipt outcome = %q, want failure", receipt.Outcome)
		}
		if receipt.Source != models.NotificationDeliverySourceBackup {
			t.Errorf("receipt source = %q, want backup", receipt.Source)
		}
		if receipt.ObservedAt.IsZero() {
			t.Error("receipt observed time is missing")
		}
	}
}

func TestNotifyReceiptFailureDoesNotTriggerDuplicateProviderAttempt(t *testing.T) {
	t.Parallel()

	service := newNotificationReceiptService(t)
	recorder := &gdriveReceiptRecorder{err: errors.New("receipt store unavailable")}
	service.SetReceiptRecorder(recorder)
	service.notify("backup finished", "backup details")

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.calls != 2 {
		t.Fatalf("receipt writes = %d, want one write per channel and no duplicate send", recorder.calls)
	}
}

func TestSetReceiptRecorderAcceptsSharedRecorderContract(t *testing.T) {
	service := &Service{}
	recorder := &gdriveReceiptRecorder{}
	service.SetReceiptRecorder(recorder)
	if service.receiptRepo != recorder {
		t.Fatal("SetReceiptRecorder did not retain the shared recorder")
	}
	var _ notify.ReceiptRecorder = recorder
}
