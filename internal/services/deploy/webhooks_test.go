package deploy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

func TestAuthenticateGitHubWebhookRequiresSignedUniquePushMetadata(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	delivery, err := AuthenticateWebhook(models.DeployWebhookGitHub, "github-secret", WebhookHeaders{
		GitHubEvent: "push", GitHubDelivery: "delivery-123", GitHubSignature: computeSignature("github-secret", body),
	}, body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if delivery.Event != "push" || delivery.Ref != "refs/heads/main" || delivery.DeliveryID != "delivery-123" {
		t.Fatalf("delivery = %+v", delivery)
	}

	_, err = AuthenticateWebhook(models.DeployWebhookGitHub, "github-secret", WebhookHeaders{
		GitHubEvent: "push", GitHubSignature: computeSignature("github-secret", body),
	}, body, time.Now())
	if !errors.Is(err, ErrWebhookRequest) {
		t.Fatalf("missing delivery ID error = %v", err)
	}
	_, err = AuthenticateWebhook(models.DeployWebhookGitHub, "github-secret", WebhookHeaders{
		GitHubEvent: "push", GitHubDelivery: "delivery-124", GitHubSignature: computeSignature("github-secret", []byte(`{}`)),
	}, body, time.Now())
	if !errors.Is(err, ErrWebhookAuthentication) {
		t.Fatalf("tampered body error = %v", err)
	}
}

func TestAuthenticateGitLabWebhookUsesStandardSignatureAndFreshTimestamp(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	body := []byte(`{"object_kind":"push","ref":"refs/heads/main"}`)
	key := []byte("0123456789abcdef0123456789abcdef")
	secret := "whsec_" + base64.StdEncoding.EncodeToString(key)
	webhookID := "f5e5f430-f57b-4e6e-9fac-d9128cd7232f"
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := gitLabSignatureForTest(key, webhookID, timestamp, body)
	delivery, err := AuthenticateWebhook(models.DeployWebhookGitLab, secret, WebhookHeaders{
		GitLabEvent: "Push Hook", GitLabWebhookID: webhookID, GitLabTimestamp: timestamp, GitLabSignatures: "v1,ignored " + signature,
	}, body, now)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.Event != "push" || delivery.Ref != "refs/heads/main" || delivery.DeliveryID != webhookID {
		t.Fatalf("delivery = %+v", delivery)
	}

	_, err = AuthenticateWebhook(models.DeployWebhookGitLab, secret, WebhookHeaders{
		GitLabEvent: "Push Hook", GitLabWebhookID: webhookID, GitLabTimestamp: strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10), GitLabSignatures: signature,
	}, body, now)
	if !errors.Is(err, ErrWebhookAuthentication) {
		t.Fatalf("stale timestamp error = %v", err)
	}
}

func TestRegisterWebhookDeliveryRejectsReplayAcrossCalls(t *testing.T) {
	database := openMemDB(t)
	service, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name: "webhook-app", ProjectDir: "/srv/webhook-app", DeployKind: models.DeployKindScript,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := service.RegisterWebhookDelivery(target.ID, models.DeployWebhookGitHub, "delivery-1", now); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterWebhookDelivery(target.ID, models.DeployWebhookGitHub, "delivery-1", now); !errors.Is(err, ErrWebhookReplay) {
		t.Fatalf("replay error = %v", err)
	}
	var count int
	err = database.QueryRow(`SELECT COUNT(*) FROM deploy_webhook_deliveries WHERE target_id = ?`, target.ID).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("delivery count = %d, err=%v", count, err)
	}
	if err := service.DeleteTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	err = database.QueryRow(`SELECT COUNT(*) FROM deploy_webhook_deliveries WHERE target_id = ?`, target.ID).Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("delivery count after target delete = %d, err=%v", count, err)
	}
}

func TestCreateTargetValidatesWebhookProviderAndGitLabSigningToken(t *testing.T) {
	database := openMemDB(t)
	service, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	validSecret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name: "gitlab-app", ProjectDir: "/srv/gitlab-app", DeployKind: models.DeployKindScript,
		WebhookProvider: models.DeployWebhookGitLab, WebhookToken: validSecret, AutoDeploy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.WebhookProvider != models.DeployWebhookGitLab {
		t.Fatalf("provider = %q", target.WebhookProvider)
	}
	for _, request := range []models.CreateDeployTargetRequest{
		{Name: "unknown", ProjectDir: "/srv/unknown", WebhookProvider: "other"},
		{Name: "legacy-gitlab", ProjectDir: "/srv/legacy", WebhookProvider: models.DeployWebhookGitLab, WebhookToken: "plain-secret", AutoDeploy: true},
	} {
		if _, err := service.CreateTarget(request); !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("request %+v error = %v", request, err)
		}
	}
}

func gitLabSignatureForTest(key []byte, id, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id + "." + timestamp + "."))
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
