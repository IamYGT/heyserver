package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

// DeliveryResult is the bounded outcome of one enabled notification-channel
// attempt. It deliberately contains no provider response or error text.
type DeliveryResult struct {
	ChannelID             int64                              `json:"channelId"`
	ChannelConfigRevision int64                              `json:"channelConfigRevision"`
	Outcome               models.NotificationDeliveryOutcome `json:"outcome"`
}

// ReceiptRecorder is the persistence seam used after a provider call has
// completed. Implementations own storage, retention, and transaction policy.
type ReceiptRecorder interface {
	UpsertContext(context.Context, *models.NotificationDeliveryReceipt) error
}

// PersistDeliveryResults turns bounded dispatcher results into bounded
// receipts. Every result is attempted independently so a recorder failure for
// one channel does not prevent receipts for the remaining channels. Provider
// errors are intentionally absent from DeliveryResult and can never reach the
// receipt store through this seam.
func PersistDeliveryResults(ctx context.Context, recorder ReceiptRecorder, source models.NotificationDeliverySource, results []DeliveryResult, observedAt time.Time) error {
	if recorder == nil || len(results) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	observedAt = observedAt.UTC()
	var errs []error
	for _, result := range results {
		receipt := &models.NotificationDeliveryReceipt{
			ChannelID:             result.ChannelID,
			ChannelConfigRevision: result.ChannelConfigRevision,
			Outcome:               result.Outcome,
			Source:                source,
			ObservedAt:            observedAt,
		}
		if err := recorder.UpsertContext(ctx, receipt); err != nil {
			errs = append(errs, fmt.Errorf("channel %d: %w", result.ChannelID, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("notification receipt errors: %w", errors.Join(errs...))
	}
	return nil
}
