package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

// ErrInvalidNotificationDeliveryReceipt identifies a receipt that violates
// the bounded persistence contract. Provider payloads and errors are not part
// of this model and therefore cannot be accepted by this repository.
var ErrInvalidNotificationDeliveryReceipt = errors.New("invalid notification delivery receipt")

// NotificationDeliveryReceiptRepository stores one latest bounded delivery
// observation for each notification channel.
type NotificationDeliveryReceiptRepository struct {
	db *sql.DB
}

// NewNotificationDeliveryReceiptRepository wraps an existing SQLite
// connection. MigrateNotify must have run before repository operations.
func NewNotificationDeliveryReceiptRepository(db *sql.DB) *NotificationDeliveryReceiptRepository {
	return &NotificationDeliveryReceiptRepository{db: db}
}

// Upsert stores the latest receipt using a background context. Callers with a
// request or worker context should use UpsertContext instead.
func (r *NotificationDeliveryReceiptRepository) Upsert(receipt *models.NotificationDeliveryReceipt) error {
	return r.UpsertContext(context.Background(), receipt)
}

// UpsertContext atomically replaces the latest receipt for receipt.ChannelID.
// The channel configuration revision is persisted with the observation so a
// caller can reject a receipt produced by an older configuration generation.
func (r *NotificationDeliveryReceiptRepository) UpsertContext(ctx context.Context, receipt *models.NotificationDeliveryReceipt) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateNotificationDeliveryReceipt(receipt); err != nil {
		return err
	}
	if r == nil || r.db == nil {
		return fmt.Errorf("%w: repository database is nil", ErrInvalidNotificationDeliveryReceipt)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	receipt.ObservedAt = receipt.ObservedAt.UTC()
	// Keep the RFC3339 text representation for compatibility, but order
	// conflicts by numeric seconds and nanoseconds. Comparing RFC3339 text
	// directly is not stable across fractional-precision spellings (or legacy
	// timezone offsets), and SQLite's date helpers otherwise discard precision
	// beyond milliseconds.
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notification_delivery_receipts
			(channel_id, channel_config_revision, outcome, source, observed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			channel_config_revision = excluded.channel_config_revision,
			outcome = excluded.outcome,
			source = excluded.source,
			observed_at = excluded.observed_at
		WHERE excluded.channel_config_revision > notification_delivery_receipts.channel_config_revision
		   OR (
				excluded.channel_config_revision = notification_delivery_receipts.channel_config_revision
				AND (
					? > CAST(strftime('%s', notification_delivery_receipts.observed_at) AS INTEGER)
					OR (
						? = CAST(strftime('%s', notification_delivery_receipts.observed_at) AS INTEGER)
						AND ? >= CASE
							WHEN substr(notification_delivery_receipts.observed_at, 20, 1) = '.' THEN
								CAST(substr(
									rtrim(substr(notification_delivery_receipts.observed_at, 21), 'Z') || '000000000',
									1,
									9
								) AS INTEGER)
							ELSE 0
						END
					)
				)
			)
	`, receipt.ChannelID, receipt.ChannelConfigRevision, receipt.Outcome, receipt.Source,
		receipt.ObservedAt.Format(time.RFC3339Nano), receipt.ObservedAt.Unix(),
		receipt.ObservedAt.Unix(), receipt.ObservedAt.Nanosecond())
	if err != nil {
		return fmt.Errorf("upsert notification delivery receipt: %w", err)
	}
	return nil
}

// Get returns the latest receipt for a channel, or nil when no receipt exists.
func (r *NotificationDeliveryReceiptRepository) Get(channelID int64) (*models.NotificationDeliveryReceipt, error) {
	return r.GetContext(context.Background(), channelID)
}

// GetContext returns the latest receipt for a channel while honoring ctx.
func (r *NotificationDeliveryReceiptRepository) GetContext(ctx context.Context, channelID int64) (*models.NotificationDeliveryReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateNotificationDeliveryChannelID(channelID); err != nil {
		return nil, err
	}
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("%w: repository database is nil", ErrInvalidNotificationDeliveryReceipt)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	receipt, err := scanNotificationDeliveryReceipt(r.db.QueryRowContext(ctx, `
		SELECT channel_id, channel_config_revision, outcome, source, observed_at
		FROM notification_delivery_receipts
		WHERE channel_id = ?
	`, channelID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get notification delivery receipt: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return receipt, nil
}

// List returns latest receipts ordered by channel ID using a background
// context. Callers with a request or worker context should use ListContext.
func (r *NotificationDeliveryReceiptRepository) List() ([]models.NotificationDeliveryReceipt, error) {
	return r.ListContext(context.Background())
}

// ListContext returns all latest receipts in stable channel order while
// honoring ctx.
func (r *NotificationDeliveryReceiptRepository) ListContext(ctx context.Context) ([]models.NotificationDeliveryReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("%w: repository database is nil", ErrInvalidNotificationDeliveryReceipt)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT channel_id, channel_config_revision, outcome, source, observed_at
		FROM notification_delivery_receipts
		ORDER BY channel_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list notification delivery receipts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]models.NotificationDeliveryReceipt, 0)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		receipt, err := scanNotificationDeliveryReceipt(rows)
		if err != nil {
			return nil, fmt.Errorf("scan notification delivery receipt: %w", err)
		}
		out = append(out, *receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notification delivery receipts rows: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func validateNotificationDeliveryReceipt(receipt *models.NotificationDeliveryReceipt) error {
	if receipt == nil {
		return fmt.Errorf("%w: receipt is nil", ErrInvalidNotificationDeliveryReceipt)
	}
	if err := validateNotificationDeliveryChannelID(receipt.ChannelID); err != nil {
		return err
	}
	if receipt.ChannelConfigRevision <= 0 {
		return fmt.Errorf("%w: channel config revision must be positive", ErrInvalidNotificationDeliveryReceipt)
	}
	switch receipt.Outcome {
	case models.NotificationDeliveryOutcomeSuccess, models.NotificationDeliveryOutcomeFailure:
	default:
		return fmt.Errorf("%w: unsupported outcome %q", ErrInvalidNotificationDeliveryReceipt, receipt.Outcome)
	}
	switch receipt.Source {
	case models.NotificationDeliverySourceManualTest,
		models.NotificationDeliverySourceAlert,
		models.NotificationDeliverySourceUptime,
		models.NotificationDeliverySourceBackup:
	default:
		return fmt.Errorf("%w: unsupported source %q", ErrInvalidNotificationDeliveryReceipt, receipt.Source)
	}
	if receipt.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed time is required", ErrInvalidNotificationDeliveryReceipt)
	}
	return nil
}

func validateNotificationDeliveryChannelID(channelID int64) error {
	if channelID <= 0 {
		return fmt.Errorf("%w: channel ID must be positive", ErrInvalidNotificationDeliveryReceipt)
	}
	return nil
}

func scanNotificationDeliveryReceipt(s interface{ Scan(...any) error }) (*models.NotificationDeliveryReceipt, error) {
	var receipt models.NotificationDeliveryReceipt
	var outcome, source, observedAt string
	if err := s.Scan(&receipt.ChannelID, &receipt.ChannelConfigRevision, &outcome, &source, &observedAt); err != nil {
		return nil, err
	}
	receipt.Outcome = models.NotificationDeliveryOutcome(outcome)
	receipt.Source = models.NotificationDeliverySource(source)
	parsed, err := time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return nil, fmt.Errorf("observed_at is not RFC3339Nano: %w", err)
	}
	receipt.ObservedAt = parsed.UTC()
	return &receipt, nil
}
