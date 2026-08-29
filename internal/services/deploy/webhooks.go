package deploy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

const gitLabWebhookClockSkew = 5 * time.Minute

var (
	ErrWebhookAuthentication = errors.New("webhook authentication failed")
	ErrWebhookRequest        = errors.New("invalid webhook request")
	ErrWebhookReplay         = errors.New("webhook delivery already processed")
	webhookDeliveryPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

// WebhookHeaders contains only provider-owned delivery headers consumed by the
// adapter. The API boundary extracts them without passing arbitrary headers to
// deployment logic.
type WebhookHeaders struct {
	GitHubEvent      string
	GitHubDelivery   string
	GitHubSignature  string
	GitLabEvent      string
	GitLabWebhookID  string
	GitLabTimestamp  string
	GitLabSignatures string
}

// WebhookDelivery is one authenticated provider event normalized for the
// deployment workflow.
type WebhookDelivery struct {
	Provider   models.DeployWebhookProvider
	DeliveryID string
	Event      string
	Ref        string
}

// AuthenticateWebhook validates provider-owned authentication and normalizes
// a bounded event. It performs no persistence and never mutates a checkout.
func AuthenticateWebhook(provider models.DeployWebhookProvider, secret string, headers WebhookHeaders, body []byte, now time.Time) (WebhookDelivery, error) {
	switch provider {
	case models.DeployWebhookGitHub:
		return authenticateGitHubWebhook(secret, headers, body)
	case models.DeployWebhookGitLab:
		return authenticateGitLabWebhook(secret, headers, body, now)
	default:
		return WebhookDelivery{}, fmt.Errorf("%w: unsupported provider", ErrWebhookRequest)
	}
}

func authenticateGitHubWebhook(secret string, headers WebhookHeaders, body []byte) (WebhookDelivery, error) {
	if secret == "" || !VerifySignature(secret, headers.GitHubSignature, body) {
		return WebhookDelivery{}, fmt.Errorf("%w: invalid GitHub signature", ErrWebhookAuthentication)
	}
	event := strings.TrimSpace(headers.GitHubEvent)
	if event == "" || len(event) > 64 {
		return WebhookDelivery{}, fmt.Errorf("%w: invalid GitHub event", ErrWebhookRequest)
	}
	delivery := WebhookDelivery{Provider: models.DeployWebhookGitHub, Event: event}
	if event == "ping" {
		return delivery, nil
	}
	if event != "push" {
		return delivery, nil
	}
	if !validWebhookDeliveryID(headers.GitHubDelivery) {
		return WebhookDelivery{}, fmt.Errorf("%w: invalid GitHub delivery ID", ErrWebhookRequest)
	}
	delivery.DeliveryID = headers.GitHubDelivery
	ref, err := webhookPushRef(body)
	if err != nil {
		return WebhookDelivery{}, err
	}
	delivery.Ref = ref
	return delivery, nil
}

func authenticateGitLabWebhook(secret string, headers WebhookHeaders, body []byte, now time.Time) (WebhookDelivery, error) {
	key, err := decodeGitLabSigningSecret(secret)
	if err != nil {
		return WebhookDelivery{}, fmt.Errorf("%w: invalid GitLab signing token", ErrWebhookAuthentication)
	}
	if !validWebhookDeliveryID(headers.GitLabWebhookID) {
		return WebhookDelivery{}, fmt.Errorf("%w: invalid GitLab webhook ID", ErrWebhookRequest)
	}
	timestamp, err := strconv.ParseInt(headers.GitLabTimestamp, 10, 64)
	if err != nil {
		return WebhookDelivery{}, fmt.Errorf("%w: invalid GitLab timestamp", ErrWebhookRequest)
	}
	delta := now.Sub(time.Unix(timestamp, 0))
	if delta < -gitLabWebhookClockSkew || delta > gitLabWebhookClockSkew {
		return WebhookDelivery{}, fmt.Errorf("%w: GitLab timestamp is outside the five-minute window", ErrWebhookAuthentication)
	}
	message := make([]byte, 0, len(headers.GitLabWebhookID)+len(headers.GitLabTimestamp)+len(body)+2)
	message = append(message, headers.GitLabWebhookID...)
	message = append(message, '.')
	message = append(message, headers.GitLabTimestamp...)
	message = append(message, '.')
	message = append(message, body...)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	expected := mac.Sum(nil)
	verified := false
	for _, signature := range strings.Fields(headers.GitLabSignatures) {
		if !strings.HasPrefix(signature, "v1,") {
			continue
		}
		received, decodeErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(signature, "v1,"))
		if decodeErr == nil && hmac.Equal(expected, received) {
			verified = true
		}
	}
	if !verified {
		return WebhookDelivery{}, fmt.Errorf("%w: invalid GitLab signature", ErrWebhookAuthentication)
	}
	event := strings.TrimSpace(headers.GitLabEvent)
	if event == "" || len(event) > 64 {
		return WebhookDelivery{}, fmt.Errorf("%w: invalid GitLab event", ErrWebhookRequest)
	}
	delivery := WebhookDelivery{
		Provider:   models.DeployWebhookGitLab,
		DeliveryID: headers.GitLabWebhookID,
		Event:      event,
	}
	if event != "Push Hook" {
		return delivery, nil
	}
	delivery.Event = "push"
	ref, err := webhookPushRef(body)
	if err != nil {
		return WebhookDelivery{}, err
	}
	delivery.Ref = ref
	return delivery, nil
}

func webhookPushRef(body []byte) (string, error) {
	var payload struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("%w: push body must be valid JSON", ErrWebhookRequest)
	}
	payload.Ref = strings.TrimSpace(payload.Ref)
	if !strings.HasPrefix(payload.Ref, "refs/heads/") || len(payload.Ref) > 266 || !validGitBranch(strings.TrimPrefix(payload.Ref, "refs/heads/")) {
		return "", fmt.Errorf("%w: push ref is invalid", ErrWebhookRequest)
	}
	return payload.Ref, nil
}

func decodeGitLabSigningSecret(secret string) ([]byte, error) {
	if !strings.HasPrefix(secret, "whsec_") {
		return nil, errors.New("missing whsec_ prefix")
	}
	encoded := strings.TrimPrefix(secret, "whsec_")
	if encoded == "" || len(encoded) > 1024 {
		return nil, errors.New("invalid signing token length")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(key) < 16 || len(key) > 512 {
		return nil, errors.New("invalid signing token key")
	}
	return key, nil
}

func validWebhookDeliveryID(value string) bool {
	return webhookDeliveryPattern.MatchString(value)
}

// RegisterWebhookDelivery atomically consumes a signed push delivery identity.
// A duplicate never queues another deployment, including across restarts.
func (s *Service) RegisterWebhookDelivery(targetID int64, provider models.DeployWebhookProvider, deliveryID string, receivedAt time.Time) error {
	if provider != models.DeployWebhookGitHub && provider != models.DeployWebhookGitLab {
		return fmt.Errorf("%w: unsupported provider", ErrWebhookRequest)
	}
	if !validWebhookDeliveryID(deliveryID) {
		return fmt.Errorf("%w: invalid delivery ID", ErrWebhookRequest)
	}
	result, err := s.db.Exec(`
		INSERT INTO deploy_webhook_deliveries (target_id, provider, delivery_id, received_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(target_id, provider, delivery_id) DO NOTHING
	`, targetID, provider, deliveryID, receivedAt.UTC())
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted != 1 {
		return ErrWebhookReplay
	}
	return nil
}
