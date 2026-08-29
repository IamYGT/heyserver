package notify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
)

func receiptTestChannel(id, revision int64, enabled bool) models.NotificationChannel {
	return models.NotificationChannel{
		ID:             id,
		Type:           models.ChannelSlack,
		Config:         `{"webhook_url":"https://hooks.example.com/abc"}`,
		Enabled:        enabled,
		ConfigRevision: revision,
	}
}

func TestChannelAvailabilityWithReceipt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	channel := receiptTestChannel(7, 3, true)
	tests := []struct {
		name    string
		receipt *models.NotificationDeliveryReceipt
		state   integrationstate.State
		detail  string
	}{
		{
			name:   "missing receipt",
			state:  integrationstate.Unavailable,
			detail: notificationDetailProbeUnverified,
		},
		{
			name: "fresh success",
			receipt: &models.NotificationDeliveryReceipt{
				ChannelID: 7, ChannelConfigRevision: 3,
				Outcome:    models.NotificationDeliveryOutcomeSuccess,
				ObservedAt: now.Add(-time.Hour),
			},
			state:  integrationstate.Healthy,
			detail: notificationDetailDeliveryConfirmed,
		},
		{
			name: "latest failure",
			receipt: &models.NotificationDeliveryReceipt{
				ChannelID: 7, ChannelConfigRevision: 3,
				Outcome:    models.NotificationDeliveryOutcomeFailure,
				ObservedAt: now.Add(-time.Hour),
			},
			state:  integrationstate.Unavailable,
			detail: notificationDetailDeliveryFailed,
		},
		{
			name: "stale success",
			receipt: &models.NotificationDeliveryReceipt{
				ChannelID: 7, ChannelConfigRevision: 3,
				Outcome:    models.NotificationDeliveryOutcomeSuccess,
				ObservedAt: now.Add(-notificationReceiptFreshness - time.Second),
			},
			state:  integrationstate.Unavailable,
			detail: notificationDetailDeliveryStale,
		},
		{
			name: "revision mismatch",
			receipt: &models.NotificationDeliveryReceipt{
				ChannelID: 7, ChannelConfigRevision: 2,
				Outcome:    models.NotificationDeliveryOutcomeSuccess,
				ObservedAt: now.Add(-time.Hour),
			},
			state:  integrationstate.Unavailable,
			detail: notificationDetailProbeUnverified,
		},
		{
			name: "future observation",
			receipt: &models.NotificationDeliveryReceipt{
				ChannelID: 7, ChannelConfigRevision: 3,
				Outcome:    models.NotificationDeliveryOutcomeSuccess,
				ObservedAt: now.Add(time.Second),
			},
			state:  integrationstate.Unavailable,
			detail: notificationDetailProbeUnverified,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ChannelAvailabilityWithReceipt(channel, test.receipt, now)
			if got.State != test.state || got.Detail != test.detail {
				t.Fatalf("status = %#v, want state=%q detail=%q", got, test.state, test.detail)
			}
		})
	}
}

func TestChannelsAvailabilityWithReceiptsIsStrict(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	first := receiptTestChannel(1, 2, true)
	second := receiptTestChannel(2, 4, true)
	fresh := func(channel models.NotificationChannel) models.NotificationDeliveryReceipt {
		return models.NotificationDeliveryReceipt{
			ChannelID: channel.ID, ChannelConfigRevision: channel.ConfigRevision,
			Outcome: models.NotificationDeliveryOutcomeSuccess, ObservedAt: now,
		}
	}

	tests := []struct {
		name     string
		channels []models.NotificationChannel
		receipts map[int64]models.NotificationDeliveryReceipt
		state    integrationstate.State
		detail   string
	}{
		{name: "empty", state: integrationstate.NotConfigured, detail: notificationDetailNotConfigured},
		{
			name:     "all unconfigured",
			channels: []models.NotificationChannel{{ID: 1, Type: models.ChannelSlack, Config: `{}`, Enabled: true}},
			state:    integrationstate.NotConfigured,
			detail:   notificationDetailNotConfigured,
		},
		{
			name:     "all disabled",
			channels: []models.NotificationChannel{receiptTestChannel(1, 2, false)},
			state:    integrationstate.Unavailable,
			detail:   notificationDetailConfiguredDisabled,
		},
		{
			name:     "all fresh success",
			channels: []models.NotificationChannel{first, second},
			receipts: map[int64]models.NotificationDeliveryReceipt{1: fresh(first), 2: fresh(second)},
			state:    integrationstate.Healthy,
			detail:   notificationDetailDeliveryConfirmed,
		},
		{
			name:     "one failed blocks aggregate",
			channels: []models.NotificationChannel{first, second},
			receipts: map[int64]models.NotificationDeliveryReceipt{1: fresh(first), 2: {ChannelID: 2, ChannelConfigRevision: 4, Outcome: models.NotificationDeliveryOutcomeFailure, ObservedAt: now}},
			state:    integrationstate.Unavailable,
			detail:   notificationDetailDegraded,
		},
		{
			name:     "one disabled blocks aggregate",
			channels: []models.NotificationChannel{first, receiptTestChannel(2, 4, false)},
			receipts: map[int64]models.NotificationDeliveryReceipt{1: fresh(first)},
			state:    integrationstate.Unavailable,
			detail:   notificationDetailDegraded,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ChannelsAvailabilityWithReceipts(test.channels, test.receipts, now)
			if got.State != test.state || got.Detail != test.detail {
				t.Fatalf("status = %#v, want state=%q detail=%q", got, test.state, test.detail)
			}
		})
	}
}

func TestDispatcherSendWithResultsIncludesFailuresAndSkipsDisabled(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher([]models.NotificationChannel{
		{ID: 1, Name: "broken", Type: models.ChannelSlack, Config: `{`, Enabled: true, ConfigRevision: 2},
		{ID: 2, Name: "blocked", Type: models.ChannelSlack, Config: `{"webhook_url":"http://127.0.0.1/hook"}`, Enabled: true, ConfigRevision: 3},
		{ID: 3, Name: "disabled", Type: models.ChannelSlack, Config: `{"webhook_url":"http://127.0.0.1/hook"}`, Enabled: false, ConfigRevision: 4},
	})
	results, err := dispatcher.SendWithResults("subject", "body")
	if err == nil || !strings.Contains(err.Error(), "notify errors") {
		t.Fatalf("SendWithResults error = %v, want aggregate error", err)
	}
	if len(results) != 2 {
		t.Fatalf("results length = %d, want 2", len(results))
	}
	for i, wantID := range []int64{1, 2} {
		if results[i].ChannelID != wantID || results[i].Outcome != models.NotificationDeliveryOutcomeFailure {
			t.Errorf("result[%d] = %#v, want failed channel %d", i, results[i], wantID)
		}
	}
}

func TestFireManualWithResultKeepsCompatibilityAndResult(t *testing.T) {
	t.Parallel()

	result, err := FireManualWithResult(models.NotificationChannel{
		ID: 9, Name: "broken", Type: models.ChannelSlack, Config: `{`, ConfigRevision: 5,
	})
	if err == nil {
		t.Fatal("expected build error")
	}
	if result.ChannelID != 9 || result.ChannelConfigRevision != 5 || result.Outcome != models.NotificationDeliveryOutcomeFailure {
		t.Fatalf("result = %#v, want failed channel result", result)
	}
	if err := FireManual(models.NotificationChannel{Type: models.ChannelSlack, Config: `{`}); err == nil {
		t.Fatal("FireManual should preserve aggregate error behavior")
	}
}

type receiptRecorderTestDouble struct {
	mu       sync.Mutex
	receipts []*models.NotificationDeliveryReceipt
	failIDs  map[int64]bool
}

func (r *receiptRecorderTestDouble) UpsertContext(_ context.Context, receipt *models.NotificationDeliveryReceipt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failIDs[receipt.ChannelID] {
		return errors.New("recorder failed")
	}
	copy := *receipt
	r.receipts = append(r.receipts, &copy)
	return nil
}

func TestPersistDeliveryResultsAggregatesRecorderErrors(t *testing.T) {
	t.Parallel()

	recorder := &receiptRecorderTestDouble{failIDs: map[int64]bool{2: true}}
	observedAt := time.Date(2026, 8, 28, 12, 0, 0, 123, time.FixedZone("test", 2*60*60))
	results := []DeliveryResult{
		{ChannelID: 1, ChannelConfigRevision: 2, Outcome: models.NotificationDeliveryOutcomeSuccess},
		{ChannelID: 2, ChannelConfigRevision: 3, Outcome: models.NotificationDeliveryOutcomeFailure},
		{ChannelID: 3, ChannelConfigRevision: 4, Outcome: models.NotificationDeliveryOutcomeSuccess},
	}

	err := PersistDeliveryResults(context.Background(), recorder, models.NotificationDeliverySourceManualTest, results, observedAt)
	if err == nil || !strings.Contains(err.Error(), "channel 2") {
		t.Fatalf("PersistDeliveryResults error = %v, want channel 2 aggregate error", err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.receipts) != 2 {
		t.Fatalf("persisted receipts = %d, want successful writes for remaining channels", len(recorder.receipts))
	}
	for _, receipt := range recorder.receipts {
		if receipt.Source != models.NotificationDeliverySourceManualTest || !receipt.ObservedAt.Equal(observedAt.UTC()) {
			t.Errorf("receipt = %#v, want bounded source and UTC timestamp", receipt)
		}
	}
}

func TestPersistDeliveryResultsConcurrent(t *testing.T) {
	t.Parallel()

	recorder := &receiptRecorderTestDouble{}
	result := []DeliveryResult{{ChannelID: 1, ChannelConfigRevision: 1, Outcome: models.NotificationDeliveryOutcomeSuccess}}
	const workers = 16
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := PersistDeliveryResults(context.Background(), recorder, models.NotificationDeliverySourceAlert, result, time.Now().UTC()); err != nil {
				t.Errorf("PersistDeliveryResults: %v", err)
			}
		}()
	}
	wg.Wait()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.receipts) != workers {
		t.Fatalf("persisted receipts = %d, want %d", len(recorder.receipts), workers)
	}
}
