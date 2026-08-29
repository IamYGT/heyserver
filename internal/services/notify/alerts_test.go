package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
)

type alertReceiptRecorder struct {
	calls   int
	receipt *models.NotificationDeliveryReceipt
	err     error
}

func (r *alertReceiptRecorder) UpsertContext(_ context.Context, receipt *models.NotificationDeliveryReceipt) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	copy := *receipt
	r.receipt = &copy
	return nil
}

func TestAlertCheckerDispatchPersistsProviderFailurePerChannel(t *testing.T) {
	t.Parallel()

	recorder := &alertReceiptRecorder{}
	checker := NewAlertChecker(nil, nil, nil, recorder)
	dispatcher := NewDispatcher([]models.NotificationChannel{
		{
			ID:             21,
			Name:           "loopback blocked",
			Type:           models.ChannelSlack,
			Config:         `{"webhook_url":"http://127.0.0.1/hook"}`,
			Enabled:        true,
			ConfigRevision: 4,
		},
	})

	err := checker.dispatch(dispatcher, Event{Type: models.AlertCPUUsage, Host: "test-host", Message: "threshold exceeded"})
	if err == nil || !strings.Contains(err.Error(), "notify errors") {
		t.Fatalf("dispatch error = %v, want provider failure", err)
	}
	if recorder.calls != 1 {
		t.Fatalf("receipt writes = %d, want one write after the provider attempt", recorder.calls)
	}
	if recorder.receipt == nil {
		t.Fatal("expected a bounded alert delivery receipt")
	}
	if recorder.receipt.ChannelID != 21 || recorder.receipt.ChannelConfigRevision != 4 {
		t.Fatalf("receipt channel identity = %+v", recorder.receipt)
	}
	if recorder.receipt.Outcome != models.NotificationDeliveryOutcomeFailure {
		t.Fatalf("receipt outcome = %q, want failure", recorder.receipt.Outcome)
	}
	if recorder.receipt.Source != models.NotificationDeliverySourceAlert {
		t.Fatalf("receipt source = %q, want alert", recorder.receipt.Source)
	}
}

func TestAlertCheckerDispatchReceiptFailureDoesNotRewriteSendOutcome(t *testing.T) {
	t.Parallel()

	recorder := &alertReceiptRecorder{err: errors.New("receipt store unavailable")}
	checker := NewAlertChecker(nil, nil, nil, recorder)
	dispatcher := NewDispatcher([]models.NotificationChannel{
		{
			ID:             22,
			Name:           "invalid config",
			Type:           models.ChannelSlack,
			Config:         `{`,
			Enabled:        true,
			ConfigRevision: 1,
		},
	})

	err := checker.dispatch(dispatcher, Event{Type: models.AlertMemoryUsage, Host: "test-host", Message: "threshold exceeded"})
	if err == nil || !strings.Contains(err.Error(), "notify errors") {
		t.Fatalf("dispatch error = %v, want provider/build failure", err)
	}
	if strings.Contains(err.Error(), "receipt store unavailable") {
		t.Fatalf("receipt persistence error rewrote send outcome: %v", err)
	}
	if recorder.calls != 1 {
		t.Fatalf("receipt writes = %d, want one write and no duplicate send", recorder.calls)
	}
}
