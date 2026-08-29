package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	appdb "github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	deploysvc "github.com/IamYGT/heyserver/internal/services/deploy"
)

type apiProjectDomainRuntime struct {
	applied []models.DeployDomain
	removed []models.DeployDomain
}

func (runtime *apiProjectDomainRuntime) Apply(domain models.DeployDomain) error {
	runtime.applied = append(runtime.applied, domain)
	return nil
}

func (runtime *apiProjectDomainRuntime) Remove(domain models.DeployDomain) error {
	runtime.removed = append(runtime.removed, domain)
	return nil
}

func (runtime *apiProjectDomainRuntime) EnableTLS(_ models.DeployDomain, _ string) (models.DeployDomainTLSState, error) {
	return models.DeployDomainTLSState{Status: "healthy", Message: "valid"}, nil
}

func (runtime *apiProjectDomainRuntime) DisableTLS(_ models.DeployDomain) error { return nil }

func (runtime *apiProjectDomainRuntime) TLSState(domain models.DeployDomain) (models.DeployDomainTLSState, error) {
	if domain.TLSEnabled {
		return models.DeployDomainTLSState{Status: "healthy", Message: "valid"}, nil
	}
	return models.DeployDomainTLSState{Status: "not_configured", Message: "not configured"}, nil
}

func (runtime *apiProjectDomainRuntime) RenewTLS(_ models.DeployDomain) (models.DeployDomainTLSState, error) {
	return models.DeployDomainTLSState{Status: "healthy", Message: "renewed"}, nil
}

func withNilDeployService(t *testing.T, fn func()) {
	t.Helper()
	prev := deployService
	deployService = nil
	t.Cleanup(func() { deployService = prev })
	fn()
}

func TestHandleDeployTargetList_nilService(t *testing.T) {
	withNilDeployService(t, func() {
		req := httptest.NewRequest(http.MethodGet, "/api/deploy/targets", nil)
		rec := httptest.NewRecorder()

		handleDeployTargetList()(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
		}
		if body["error"] != "deploy service not initialized" {
			t.Errorf("error = %q, want %q", body["error"], "deploy service not initialized")
		}
	})
}

func TestHandleDeployTemplatesReportsInstallationInventory(t *testing.T) {
	dataDir := t.TempDir()
	service, err := deploysvc.NewWithDataDir(appdb.Instance(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	previous := deployService
	deployService = service
	t.Cleanup(func() { deployService = previous })

	request := httptest.NewRequest(http.MethodGet, "/api/deploy/templates", nil)
	recorder := httptest.NewRecorder()
	handleDeployTemplates()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"not_configured"`) {
		t.Fatalf("not-configured status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	directory := filepath.Join(dataDir, "deploy-templates")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"schemaVersion":1,"id":"compose","name":"Docker Compose","deploymentKind":"compose","composeFile":"deploy/compose.yaml"}`
	if err := os.WriteFile(filepath.Join(directory, "compose.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handleDeployTemplates()(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"status":"healthy"`) || !strings.Contains(body, `"id":"compose"`) {
		t.Fatalf("healthy status=%d body=%s", recorder.Code, body)
	}
	for _, forbidden := range []string{"repoUrl", "projectDir", "webhookToken", "repositoryUrl"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("template response exposes %q: %s", forbidden, body)
		}
	}
}

func TestHandleDeployRunLogs_nilService(t *testing.T) {
	withNilDeployService(t, func() {
		req := httptest.NewRequest(http.MethodGet, "/api/deploy/history/1/logs", nil)
		req.SetPathValue("id", "1")
		rec := httptest.NewRecorder()

		handleDeployRunLogs()(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
		}
		if body["error"] != "deploy service not initialized" {
			t.Errorf("error = %q, want %q", body["error"], "deploy service not initialized")
		}
	})
}

func TestHandleDeployTargetCreate_nilService(t *testing.T) {
	withNilDeployService(t, func() {
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/deploy/targets",
			strings.NewReader(`{"name":"test","projectDir":"/tmp"}`),
		)
		rec := httptest.NewRecorder()

		handleDeployTargetCreate()(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
		}
		if body["error"] != "deploy service not initialized" {
			t.Errorf("error = %q, want %q", body["error"], "deploy service not initialized")
		}
	})
}

func TestHandleDeployTargetUpdateReturnsConflictForStaleObservation(t *testing.T) {
	root := t.TempDir()
	service, err := deploysvc.NewWithDataDir(appdb.Instance(), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	previous := deployService
	deployService = service
	t.Cleanup(func() { deployService = previous })
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name:            "Conflict App",
		ProjectDir:      filepath.Join(root, "app"),
		DeployKind:      models.DeployKindCompose,
		WebhookProvider: models.DeployWebhookGitHub,
		WebhookToken:    "fixture-signing-secret",
		AutoDeploy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestBody, err := json.Marshal(models.UpdateDeployTargetRequest{
		Name:              target.Name,
		RepoURL:           target.RepoURL,
		Branch:            target.Branch,
		ProjectDir:        target.ProjectDir,
		DeployKind:        target.DeployKind,
		ComposeFile:       target.ComposeFile,
		WebhookProvider:   target.WebhookProvider,
		AutoDeploy:        target.AutoDeploy,
		IsActive:          target.IsActive,
		ExpectedUpdatedAt: target.UpdatedAt.Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/deploy/targets/1", strings.NewReader(string(requestBody)))
	request.SetPathValue("id", strconv.FormatInt(target.ID, 10))
	recorder := httptest.NewRecorder()
	handleDeployTargetUpdate()(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "deploy target changed") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	current, err := service.GetTarget(target.ID)
	if err != nil || current.WebhookToken != "fixture-signing-secret" || !current.UpdatedAt.Equal(target.UpdatedAt) {
		t.Fatalf("stale update changed target=%+v err=%v", current, err)
	}
}

func TestHandleDeployStagingCreateReturnsExplicitIsolationReceipt(t *testing.T) {
	root := t.TempDir()
	service, err := deploysvc.NewWithDataDir(appdb.Instance(), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	previous := deployService
	deployService = service
	t.Cleanup(func() { deployService = previous })
	production, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name:            "API App",
		RepoURL:         "https://github.com/example/api-app.git",
		Branch:          "main",
		ProjectDir:      filepath.Join(root, "production"),
		DeployKind:      models.DeployKindCompose,
		WebhookProvider: models.DeployWebhookGitHub,
		WebhookToken:    "production-secret",
		AutoDeploy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/deploy/targets/1/staging", strings.NewReader(`{
		"name":"API App Staging",
		"branch":"develop",
		"projectDir":"`+filepath.Join(root, "staging")+`"
	}`))
	request.SetPathValue("id", strconv.FormatInt(production.ID, 10))
	recorder := httptest.NewRecorder()
	handleDeployStagingCreate()(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var receipt models.DeployStagingReceipt
	if err := json.NewDecoder(recorder.Body).Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Target.Environment != models.DeployEnvironmentStaging || receipt.Target.SourceTargetID == nil || *receipt.Target.SourceTargetID != production.ID {
		t.Fatalf("staging target = %+v", receipt.Target)
	}
	if receipt.Target.WebhookToken != "" || receipt.Target.AutoDeploy || receipt.EnvironmentValuesCopied || receipt.WebhookSecretCopied || receipt.DomainsCopied || receipt.DNSConfigured {
		t.Fatalf("isolation receipt = %+v", receipt)
	}

	unknownRequest := httptest.NewRequest(http.MethodPost, "/api/deploy/targets/1/staging", strings.NewReader(`{
		"projectDir":"`+filepath.Join(root, "other-staging")+`",
		"copySecrets":true
	}`))
	unknownRequest.SetPathValue("id", strconv.FormatInt(production.ID, 10))
	unknownRecorder := httptest.NewRecorder()
	handleDeployStagingCreate()(unknownRecorder, unknownRequest)
	if unknownRecorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknownRecorder.Code, unknownRecorder.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/deploy/targets/1", nil)
	deleteRequest.SetPathValue("id", strconv.FormatInt(production.ID, 10))
	deleteRecorder := httptest.NewRecorder()
	handleDeployTargetDelete()(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusConflict {
		t.Fatalf("production delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if err := service.DeleteTarget(receipt.Target.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteTarget(production.ID); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDeployHistory_nilService(t *testing.T) {
	withNilDeployService(t, func() {
		req := httptest.NewRequest(http.MethodGet, "/api/deploy/history", nil)
		rec := httptest.NewRecorder()

		handleDeployHistory()(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
		}
		if body["error"] != "deploy service not initialized" {
			t.Errorf("error = %q, want %q", body["error"], "deploy service not initialized")
		}
	})
}

func TestDeployTargetMutationsRejectUnknownAndTrailingJSON(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
		body    string
	}{
		{
			name:    "create unknown field",
			handler: handleDeployTargetCreate(),
			method:  http.MethodPost,
			path:    "/api/deploy/targets",
			body:    `{"name":"app","projectDir":"/srv/app","deploymentKind":"script","unexpected":true}`,
		},
		{
			name:    "create trailing value",
			handler: handleDeployTargetCreate(),
			method:  http.MethodPost,
			path:    "/api/deploy/targets",
			body:    `{"name":"app","projectDir":"/srv/app","deploymentKind":"script"} {}`,
		},
		{
			name:    "update unknown field",
			handler: handleDeployTargetUpdate(),
			method:  http.MethodPut,
			path:    "/api/deploy/targets/1",
			body:    `{"name":"app","projectDir":"/srv/app","deploymentKind":"script","unexpected":true}`,
		},
		{
			name:    "update trailing value",
			handler: handleDeployTargetUpdate(),
			method:  http.MethodPut,
			path:    "/api/deploy/targets/1",
			body:    `{"name":"app","projectDir":"/srv/app","deploymentKind":"script"} {}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.method == http.MethodPut {
				request.SetPathValue("id", "1")
			}
			recorder := httptest.NewRecorder()

			test.handler(recorder, request)

			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"error":"invalid JSON"`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestDeployHistoryRejectsInvalidFilters(t *testing.T) {
	tests := []struct {
		query string
		error string
	}{
		{query: "targetId=invalid", error: "targetId must be a positive integer"},
		{query: "targetId=0", error: "targetId must be a positive integer"},
		{query: "targetId=-1", error: "targetId must be a positive integer"},
		{query: "limit=invalid", error: "limit must be between 1 and 500"},
		{query: "limit=0", error: "limit must be between 1 and 500"},
		{query: "limit=501", error: "limit must be between 1 and 500"},
	}

	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/deploy/history?"+test.query, nil)
			recorder := httptest.NewRecorder()

			handleDeployHistory()(recorder, request)

			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), test.error) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestDeployParseIDRejectsNonPositiveIdentifiers(t *testing.T) {
	for _, value := range []string{"invalid", "0", "-1"} {
		t.Run(value, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/deploy/targets/"+value+"/preflight", nil)
			request.SetPathValue("id", value)
			recorder := httptest.NewRecorder()

			handleDeployTargetPreflight()(recorder, request)

			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"error":"invalid id"`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestHandleDeployWebhookConsumesGitHubDeliveryOnce(t *testing.T) {
	secret := "github-api-webhook-secret"
	target, err := deployService.CreateTarget(models.CreateDeployTargetRequest{
		Name: "github-webhook-api", ProjectDir: t.TempDir(), DeployKind: models.DeployKindScript,
		WebhookProvider: models.DeployWebhookGitHub, WebhookToken: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deployService.DeleteTarget(target.ID) })
	body := []byte(`{"ref":"refs/heads/main"}`)
	deliver := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/deploy/webhook/"+strconv.FormatInt(target.ID, 10), strings.NewReader(string(body)))
		request.SetPathValue("targetId", strconv.FormatInt(target.ID, 10))
		request.Header.Set("X-GitHub-Event", "push")
		request.Header.Set("X-GitHub-Delivery", "api-delivery-1")
		request.Header.Set("X-Hub-Signature-256", deploysvcSignature(secret, body))
		recorder := httptest.NewRecorder()
		handleDeployWebhook()(recorder, request)
		return recorder
	}
	first := deliver()
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "auto-deploy disabled") {
		t.Fatalf("first delivery status=%d body=%s", first.Code, first.Body.String())
	}
	second := deliver()
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "delivery already processed") {
		t.Fatalf("replay status=%d body=%s", second.Code, second.Body.String())
	}
	var count int
	if err := appdb.Instance().QueryRow(`SELECT COUNT(*) FROM deploy_webhook_deliveries WHERE target_id = ?`, target.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("delivery count=%d err=%v", count, err)
	}
}

func TestHandleDeployWebhookAcceptsSignedGitLabPush(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	secret := "whsec_" + base64.StdEncoding.EncodeToString(key)
	target, err := deployService.CreateTarget(models.CreateDeployTargetRequest{
		Name: "gitlab-webhook-api", ProjectDir: t.TempDir(), DeployKind: models.DeployKindScript,
		WebhookProvider: models.DeployWebhookGitLab, WebhookToken: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deployService.DeleteTarget(target.ID) })
	body := []byte(`{"object_kind":"push","ref":"refs/heads/main"}`)
	webhookID := "b5a5cda2-2d39-42a7-9445-b7652da535b2"
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/api/deploy/webhook/"+strconv.FormatInt(target.ID, 10), strings.NewReader(string(body)))
	request.SetPathValue("targetId", strconv.FormatInt(target.ID, 10))
	request.Header.Set("X-Gitlab-Event", "Push Hook")
	request.Header.Set("webhook-id", webhookID)
	request.Header.Set("webhook-timestamp", timestamp)
	request.Header.Set("webhook-signature", deploysvcGitLabSignature(key, webhookID, timestamp, body))
	recorder := httptest.NewRecorder()
	handleDeployWebhook()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "auto-deploy disabled") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleDeployWebhookRejectsMalformedAndOversizedBodies(t *testing.T) {
	secret := "github-invalid-body-secret"
	target, err := deployService.CreateTarget(models.CreateDeployTargetRequest{
		Name: "invalid-webhook-api", ProjectDir: t.TempDir(), DeployKind: models.DeployKindScript,
		WebhookProvider: models.DeployWebhookGitHub, WebhookToken: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deployService.DeleteTarget(target.ID) })
	targetID := strconv.FormatInt(target.ID, 10)
	body := []byte(`{`)
	request := httptest.NewRequest(http.MethodPost, "/api/deploy/webhook/"+targetID, strings.NewReader(string(body)))
	request.SetPathValue("targetId", targetID)
	request.Header.Set("X-GitHub-Event", "push")
	request.Header.Set("X-GitHub-Delivery", "malformed-delivery")
	request.Header.Set("X-Hub-Signature-256", deploysvcSignature(secret, body))
	recorder := httptest.NewRecorder()
	handleDeployWebhook()(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "valid JSON") {
		t.Fatalf("malformed status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/deploy/webhook/"+targetID, strings.NewReader(strings.Repeat("x", (10<<20)+1)))
	request.SetPathValue("targetId", targetID)
	recorder = httptest.NewRecorder()
	handleDeployWebhook()(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleDeployWebhookReportsUnavailableSigningSecret(t *testing.T) {
	dataDir := t.TempDir()
	service, err := deploysvc.NewWithDataDir(appdb.Instance(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	previous := deployService
	deployService = service
	t.Cleanup(func() { deployService = previous })
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name: "unavailable-webhook-secret", ProjectDir: t.TempDir(), DeployKind: models.DeployKindScript,
		WebhookProvider: models.DeployWebhookGitHub, WebhookToken: "unavailable-secret", AutoDeploy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.DeleteTarget(target.ID) })
	secretPath := filepath.Join(dataDir, "deploy-webhook-secrets", "target-"+strconv.FormatInt(target.ID, 10)+".secret")
	if err := os.Remove(secretPath); err != nil {
		t.Fatal(err)
	}
	targetID := strconv.FormatInt(target.ID, 10)
	request := httptest.NewRequest(http.MethodPost, "/api/deploy/webhook/"+targetID, strings.NewReader(`{}`))
	request.SetPathValue("targetId", targetID)
	recorder := httptest.NewRecorder()
	handleDeployWebhook()(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "signing secret is unavailable") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func deploysvcSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + fmt.Sprintf("%x", mac.Sum(nil))
}

func deploysvcGitLabSignature(key []byte, id, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id + "." + timestamp + "."))
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestHandleDeployTargetCreate_rejectsInvalidComposeConfiguration(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/deploy/targets",
		strings.NewReader(`{"name":"compose-app","projectDir":"relative/app","deploymentKind":"compose","composeFile":"../compose.yaml"}`),
	)
	rec := httptest.NewRecorder()

	handleDeployTargetCreate()(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid deploy target") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestHandleDeployTargetPreflight_returnsEligibilityReport(t *testing.T) {
	target, err := deployService.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "preflight-compose",
		ProjectDir: t.TempDir(),
		DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/deploy/targets/1/preflight", nil)
	req.SetPathValue("id", strconv.FormatInt(target.ID, 10))
	rec := httptest.NewRecorder()

	handleDeployTargetPreflight()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var report models.DeployPreflight
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.TargetID != target.ID || report.DeploymentKind != models.DeployKindCompose || len(report.Checks) == 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestHandleDeployTargetRevision_reportsUnprovisionedCheckout(t *testing.T) {
	target, err := deployService.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "revision-new-app",
		RepoURL:    "https://github.com/example/revision-new-app.git",
		ProjectDir: filepath.Join(t.TempDir(), "missing-checkout"),
		DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/deploy/targets/1/revision", nil)
	req.SetPathValue("id", strconv.FormatInt(target.ID, 10))
	rec := httptest.NewRecorder()

	handleDeployTargetRevision()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var report models.DeployRevisionComparison
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.TargetID != target.ID || report.State != "not_deployed" || report.CurrentCommit != "" {
		t.Fatalf("revision report = %+v", report)
	}
}

func TestHandleDeployComposeServicesAndLogs(t *testing.T) {
	projectDir := t.TempDir()
	binDir := t.TempDir()
	docker := `#!/bin/sh
case "$*" in
  *" ps --all --format json")
    printf '%s\n' '[{"Service":"web","Name":"project-web-1","Image":"example/app:latest","State":"running","Health":"healthy","ExitCode":0,"Publishers":[]}]'
    ;;
  *" logs --no-color --timestamps --tail 250 web") printf 'ready\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(docker), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	target, err := deployService.CreateTarget(models.CreateDeployTargetRequest{
		Name: "compose-services", ProjectDir: projectDir, DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/deploy/targets/services", nil)
	request.SetPathValue("id", strconv.FormatInt(target.ID, 10))
	recorder := httptest.NewRecorder()
	handleDeployComposeServices()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"service":"web"`) {
		t.Fatalf("services status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/deploy/targets/services/web/logs?tail=250", nil)
	request.SetPathValue("id", strconv.FormatInt(target.ID, 10))
	request.SetPathValue("service", "web")
	recorder = httptest.NewRecorder()
	handleDeployComposeServiceLogs()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"logs":"ready\n"`) || !strings.Contains(recorder.Body.String(), `"tail":250`) {
		t.Fatalf("logs status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleDeployComposeServiceActionRejectsArbitraryAction(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/deploy/targets/1/services/web/exec", nil)
	request.SetPathValue("id", "1")
	request.SetPathValue("service", "web")
	request.SetPathValue("action", "exec")
	recorder := httptest.NewRecorder()
	handleDeployComposeServiceAction()(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleDeployComposeServiceActionRejectsRequestBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/deploy/targets/1/services/web/restart", strings.NewReader(`{}`))
	request.SetPathValue("id", "1")
	request.SetPathValue("service", "web")
	request.SetPathValue("action", "restart")
	recorder := httptest.NewRecorder()

	handleDeployComposeServiceAction()(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "request body must be empty") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleDeployEnvironmentSetRejectsUnknownAndTrailingJSON(t *testing.T) {
	for _, body := range []string{
		`{"key":"APP_MODE","value":"production","unexpected":true}`,
		`{"key":"APP_MODE","value":"production"} {}`,
	} {
		request := httptest.NewRequest(http.MethodPut, "/api/deploy/targets/1/environment", strings.NewReader(body))
		request.SetPathValue("id", "1")
		recorder := httptest.NewRecorder()

		handleDeployEnvironmentSet()(recorder, request)

		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"error":"invalid JSON"`) {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestHandleDeployEnvironmentNeverReturnsStoredValues(t *testing.T) {
	service, err := deploysvc.NewWithDataDir(appdb.Instance(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := deployService
	deployService = service
	t.Cleanup(func() { deployService = previous })
	target, err := deployService.CreateTarget(models.CreateDeployTargetRequest{
		Name: "environment-api", ProjectDir: t.TempDir(), DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(target.ID, 10)

	request := httptest.NewRequest(http.MethodPut, "/api/deploy/targets/"+id+"/environment", strings.NewReader(`{"key":"APP_MODE","value":"production"}`))
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	handleDeployEnvironmentSet()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"key":"APP_MODE"`) || strings.Contains(recorder.Body.String(), "production") {
		t.Fatalf("set status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/deploy/targets/"+id+"/environment", nil)
	request.SetPathValue("id", id)
	recorder = httptest.NewRecorder()
	handleDeployEnvironmentGet()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"configured":true`) || strings.Contains(recorder.Body.String(), "production") {
		t.Fatalf("get status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/deploy/targets/"+id+"/environment/APP_MODE", nil)
	request.SetPathValue("id", id)
	request.SetPathValue("key", "APP_MODE")
	recorder = httptest.NewRecorder()
	handleDeployEnvironmentDelete()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"configured":false`) {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleDeployProjectDomainsManageFixedLoopbackMappings(t *testing.T) {
	runtime := &apiProjectDomainRuntime{}
	service, err := deploysvc.NewWithProjectDomainRuntime(appdb.Instance(), t.TempDir(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	previous := deployService
	deployService = service
	t.Cleanup(func() { deployService = previous })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	target, err := deployService.CreateTarget(models.CreateDeployTargetRequest{
		Name: "project-domain-api", ProjectDir: t.TempDir(), DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetID := strconv.FormatInt(target.ID, 10)

	request := httptest.NewRequest(http.MethodPost, "/api/deploy/targets/"+targetID+"/domains", strings.NewReader(
		`{"domain":"App.Example.com","service":"web","hostPort":`+strconv.Itoa(port)+`}`,
	))
	request.SetPathValue("id", targetID)
	recorder := httptest.NewRecorder()
	handleDeployProjectDomainCreate()(recorder, request)
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"upstream":"http://127.0.0.1:`+strconv.Itoa(port)+`"`) || !strings.Contains(recorder.Body.String(), `"tlsStatus":"not_configured"`) {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var domain models.DeployDomain
	if err := json.Unmarshal(recorder.Body.Bytes(), &domain); err != nil {
		t.Fatal(err)
	}
	if len(runtime.applied) != 1 {
		t.Fatalf("applied = %#v", runtime.applied)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/deploy/targets/"+targetID+"/domains", nil)
	request.SetPathValue("id", targetID)
	recorder = httptest.NewRecorder()
	handleDeployProjectDomains()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"domain":"app.example.com"`) {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	domainID := strconv.FormatInt(domain.ID, 10)
	request = httptest.NewRequest(http.MethodGet, "/api/deploy/targets/"+targetID+"/domains/"+domainID+"/health", nil)
	request.SetPathValue("id", targetID)
	request.SetPathValue("domainId", domainID)
	recorder = httptest.NewRecorder()
	handleDeployProjectDomainHealth()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"healthy"`) || !strings.Contains(recorder.Body.String(), `"statusCode":204`) {
		t.Fatalf("health status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/deploy/targets/"+targetID+"/domains/"+domainID+"/tls", nil)
	request.SetPathValue("id", targetID)
	request.SetPathValue("domainId", domainID)
	recorder = httptest.NewRecorder()
	handleDeployProjectDomainTLSEnable()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"tlsStatus":"healthy"`) {
		t.Fatalf("TLS enable status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/deploy/targets/"+targetID+"/domains/"+domainID+"/tls", nil)
	request.SetPathValue("id", targetID)
	request.SetPathValue("domainId", domainID)
	recorder = httptest.NewRecorder()
	handleDeployProjectDomainTLSDisable()(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"tlsStatus":"not_configured"`) {
		t.Fatalf("TLS disable status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/deploy/targets/"+targetID+"/domains/"+domainID, nil)
	request.SetPathValue("id", targetID)
	request.SetPathValue("domainId", domainID)
	recorder = httptest.NewRecorder()
	handleDeployProjectDomainDelete()(recorder, request)
	if recorder.Code != http.StatusNoContent || len(runtime.removed) != 1 {
		t.Fatalf("delete status=%d removed=%#v", recorder.Code, runtime.removed)
	}
}

func TestHandleDeployProjectDomainJSONBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		pathValues map[string]string
		body       string
		wantStatus int
	}{
		{
			name: "create unknown field", handler: handleDeployProjectDomainCreate(),
			pathValues: map[string]string{"id": "1"},
			body:       `{"domain":"app.example.com","service":"web","hostPort":8080,"unexpected":true}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "create trailing value", handler: handleDeployProjectDomainCreate(),
			pathValues: map[string]string{"id": "1"},
			body:       `{"domain":"app.example.com","service":"web","hostPort":8080} {}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "create oversized", handler: handleDeployProjectDomainCreate(),
			pathValues: map[string]string{"id": "1"},
			body:       `{"domain":"` + strings.Repeat("x", 8<<10) + `","service":"web","hostPort":8080}`,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "TLS unknown field", handler: handleDeployProjectDomainTLSEnable(),
			pathValues: map[string]string{"id": "1", "domainId": "1"},
			body:       `{"email":"admin@example.com","unexpected":true}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "TLS trailing value", handler: handleDeployProjectDomainTLSEnable(),
			pathValues: map[string]string{"id": "1", "domainId": "1"},
			body:       `{"email":"admin@example.com"} {}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "TLS oversized", handler: handleDeployProjectDomainTLSEnable(),
			pathValues: map[string]string{"id": "1", "domainId": "1"},
			body:       `{"email":"` + strings.Repeat("x", 8<<10) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/deploy/project-domain", strings.NewReader(test.body))
			for name, value := range test.pathValues {
				request.SetPathValue(name, value)
			}
			recorder := httptest.NewRecorder()

			test.handler(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestHandleDeployProjectDomainDeletesRejectRequestBodies(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "domain", handler: handleDeployProjectDomainDelete()},
		{name: "TLS", handler: handleDeployProjectDomainTLSDisable()},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodDelete, "/api/deploy/project-domain", strings.NewReader(`{}`))
			request.SetPathValue("id", "1")
			request.SetPathValue("domainId", "1")
			recorder := httptest.NewRecorder()

			test.handler(recorder, request)

			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "request body must be empty") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
