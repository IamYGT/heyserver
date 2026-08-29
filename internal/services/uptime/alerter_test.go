package uptime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/store"
)

type uptimeReceiptRecorder struct {
	mu       sync.Mutex
	calls    int
	receipts []*models.NotificationDeliveryReceipt
	err      error
}

func (r *uptimeReceiptRecorder) UpsertContext(_ context.Context, receipt *models.NotificationDeliveryReceipt) error {
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

func TestAlerterPersistsProviderFailureAsUptimeReceipt(t *testing.T) {
	t.Parallel()

	db, _ := openUptimeTestDB(t)
	channelRepo, err := store.NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	channel := &models.NotificationChannel{
		Name:    "loopback blocked",
		Type:    models.ChannelSlack,
		Config:  `{"webhook_url":"http://127.0.0.1/hook"}`,
		Enabled: true,
	}
	if err := channelRepo.Create(channel); err != nil {
		t.Fatalf("Create channel: %v", err)
	}

	settingsSvc := settings.New(store.NewSettingsRepository(db), "test")
	recorder := &uptimeReceiptRecorder{}
	alerter := NewAlerter(channelRepo, settingsSvc, recorder)
	monitor := &store.UptimeMonitor{
		ID:              1,
		Name:            "api",
		Type:            "http",
		URL:             "https://example.com",
		AlertChannelIDs: store.ChannelIDs(fmt.Sprintf("[%d]", channel.ID)),
	}

	alerter.SendDown(monitor, CheckResult{MonitorID: monitor.ID, Status: StatusDown, Msg: "timeout"})

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.calls != 1 {
		t.Fatalf("receipt writes = %d, want one write after the provider attempt", recorder.calls)
	}
	if len(recorder.receipts) != 1 {
		t.Fatalf("receipts = %d, want one bounded receipt", len(recorder.receipts))
	}
	receipt := recorder.receipts[0]
	if receipt.ChannelID != channel.ID || receipt.ChannelConfigRevision != channel.ConfigRevision {
		t.Fatalf("receipt channel identity = %+v", receipt)
	}
	if receipt.Outcome != models.NotificationDeliveryOutcomeFailure {
		t.Fatalf("receipt outcome = %q, want failure", receipt.Outcome)
	}
	if receipt.Source != models.NotificationDeliverySourceUptime {
		t.Fatalf("receipt source = %q, want uptime", receipt.Source)
	}
}

func TestAlerterReceiptFailureDoesNotTriggerDuplicateSend(t *testing.T) {
	t.Parallel()

	db, _ := openUptimeTestDB(t)
	channelRepo, err := store.NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	channel := &models.NotificationChannel{
		Name:    "unknown provider",
		Type:    "pagerduty",
		Config:  `{}`,
		Enabled: true,
	}
	if err := channelRepo.Create(channel); err != nil {
		t.Fatalf("Create channel: %v", err)
	}

	settingsSvc := settings.New(store.NewSettingsRepository(db), "test")
	recorder := &uptimeReceiptRecorder{err: errors.New("receipt store unavailable")}
	alerter := NewAlerter(channelRepo, settingsSvc, recorder)
	monitor := &store.UptimeMonitor{
		ID:              2,
		Name:            "api",
		Type:            "http",
		AlertChannelIDs: store.ChannelIDs(fmt.Sprintf("[%d]", channel.ID)),
	}

	alerter.SendDown(monitor, CheckResult{MonitorID: monitor.ID, Status: StatusDown, Msg: "timeout"})

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.calls != 1 {
		t.Fatalf("receipt writes = %d, want one write and no duplicate send", recorder.calls)
	}
}

func TestNewEngineWiresOptionalReceiptRecorder(t *testing.T) {
	t.Parallel()

	db, repo := openUptimeTestDB(t)
	channelRepo, err := store.NewNotificationChannelRepository(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settingsSvc := settings.New(store.NewSettingsRepository(db), "test")
	recorder := &uptimeReceiptRecorder{}
	engine := NewEngine(repo, channelRepo, settingsSvc, recorder)
	if engine.stateManager == nil || engine.stateManager.alerter == nil {
		t.Fatal("engine did not initialize state alerter")
	}
	if engine.stateManager.alerter.receiptRepo != recorder {
		t.Fatal("engine did not wire the optional receipt recorder")
	}
}
