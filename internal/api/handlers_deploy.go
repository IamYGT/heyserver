package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	deploysvc "github.com/IamYGT/heyserver/internal/services/deploy"
)

// deployService is the package-level singleton initialised by InitDeployService.
var deployService *deploysvc.Service

// InitDeployService must be called once during application startup before the
// router is created.  It wires the deploy service to the package-level var
// consumed by all deploy handler functions.
func InitDeployService(db *sql.DB, dataDir ...string) error {
	root := ""
	if len(dataDir) > 0 {
		root = dataDir[0]
	}
	return InitDeployServiceWithNginx(db, root, "", "")
}

// InitDeployServiceWithNginx binds project-domain mutations to the
// installation-owned Nginx site directories.
func InitDeployServiceWithNginx(db *sql.DB, dataDir, sitesAvailable, sitesEnabled string) error {
	return InitDeployServiceWithRuntimeConfig(db, dataDir, sitesAvailable, sitesEnabled, "", "", "")
}

// InitDeployServiceWithRuntimeConfig adds the fixed Certbot HTTP-01 boundary
// used by project domains without accepting command or filesystem paths from
// browser requests.
func InitDeployServiceWithRuntimeConfig(db *sql.DB, dataDir, sitesAvailable, sitesEnabled, certbotBin, certbotConfig, acmeWebroot string) error {
	svc, err := deploysvc.NewWithProjectDomainRuntime(
		db, dataDir, deploysvc.NewNginxProjectDomainRuntimeWithTLS(
			sitesAvailable, sitesEnabled, certbotBin, certbotConfig, acmeWebroot,
		),
	)
	if err != nil {
		return err
	}
	deployService = svc
	return nil
}

func deployServiceUnavailable(w http.ResponseWriter) bool {
	if deployService != nil {
		return false
	}
	jsonError(w, http.StatusServiceUnavailable, "deploy service not initialized")
	return true
}

// ---------------------------------------------------------------------------
// GET /api/deploy/templates
// ---------------------------------------------------------------------------

func handleDeployTemplates() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		jsonResponse(w, http.StatusOK, deployService.Templates())
	}
}

// StartDeployTLSMaintenance runs the provider-neutral Certbot renewal boundary
// in the background. The first pass happens at startup; later passes use the
// supplied interval and stop with the application context.
func StartDeployTLSMaintenance(ctx context.Context, interval time.Duration) {
	service := deployService
	if service == nil {
		return
	}
	if interval <= 0 {
		interval = 12 * time.Hour
	}
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				report := service.MaintainProjectDomainTLS()
				if report.Checked > 0 || report.Failed > 0 {
					slog.Info("project domain TLS maintenance completed", "checked", report.Checked, "renewed", report.Renewed, "failed", report.Failed)
				}
				timer.Reset(interval)
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// GET /api/deploy/targets
// ---------------------------------------------------------------------------

func handleDeployTargetList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targets, err := deployService.ListTargets()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Strip webhook tokens from list responses to avoid leaking secrets.
		for i := range targets {
			targets[i].WebhookToken = ""
		}
		if targets == nil {
			targets = []models.DeployTarget{}
		}
		jsonResponse(w, http.StatusOK, targets)
	}
}

// ---------------------------------------------------------------------------
// POST /api/deploy/targets
// ---------------------------------------------------------------------------

func handleDeployTargetCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		var req models.CreateDeployTargetRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" || req.ProjectDir == "" {
			jsonError(w, http.StatusBadRequest, "name and projectDir are required")
			return
		}
		target, err := deployService.CreateTarget(req)
		if err != nil {
			if errors.Is(err, deploysvc.ErrInvalidTarget) {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Do not echo the webhook secret back in the response.
		target.WebhookToken = ""
		jsonResponse(w, http.StatusCreated, target)
	}
}

// ---------------------------------------------------------------------------
// POST /api/deploy/targets/{id}/staging
// ---------------------------------------------------------------------------

func handleDeployStagingCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		id, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var req models.CreateDeployStagingRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		receipt, err := deployService.CreateStagingTarget(id, req)
		if err != nil {
			switch {
			case errors.Is(err, deploysvc.ErrDeployTargetNotFound):
				jsonError(w, http.StatusNotFound, err.Error())
			case errors.Is(err, deploysvc.ErrInvalidTarget):
				jsonError(w, http.StatusBadRequest, err.Error())
			default:
				jsonError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		receipt.Target.WebhookToken = ""
		jsonResponse(w, http.StatusCreated, receipt)
	}
}

// ---------------------------------------------------------------------------
// PUT /api/deploy/targets/{id}
// ---------------------------------------------------------------------------

func handleDeployTargetUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		id, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var req models.UpdateDeployTargetRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		target, err := deployService.UpdateTarget(id, req)
		if err != nil {
			if errors.Is(err, deploysvc.ErrDeployTargetChanged) {
				jsonError(w, http.StatusConflict, err.Error())
				return
			}
			if errors.Is(err, deploysvc.ErrInvalidTarget) {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if target == nil {
			jsonError(w, http.StatusNotFound, "target not found")
			return
		}
		target.WebhookToken = ""
		jsonResponse(w, http.StatusOK, target)
	}
}

// ---------------------------------------------------------------------------
// GET /api/deploy/targets/{id}/preflight
// ---------------------------------------------------------------------------

func handleDeployTargetPreflight() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		id, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		report, err := deployService.Preflight(id)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, report)
	}
}

// ---------------------------------------------------------------------------
// GET /api/deploy/targets/{id}/revision
// ---------------------------------------------------------------------------

func handleDeployTargetRevision() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		id, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		report, err := deployService.RevisionComparison(id)
		if err != nil {
			if errors.Is(err, deploysvc.ErrDeployTargetNotFound) {
				jsonError(w, http.StatusNotFound, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, report)
	}
}

// ---------------------------------------------------------------------------
// Compose project services
// ---------------------------------------------------------------------------

func handleDeployComposeServices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		services, err := deployService.ComposeServices(targetID)
		if err != nil {
			writeDeployComposeError(w, err)
			return
		}
		if services == nil {
			services = []models.ComposeService{}
		}
		jsonResponse(w, http.StatusOK, services)
	}
}

func handleDeployComposeServiceLogs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		tail := 200
		if raw := r.URL.Query().Get("tail"); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 1 || parsed > 1000 {
				jsonError(w, http.StatusBadRequest, "tail must be between 1 and 1000")
				return
			}
			tail = parsed
		}
		logs, err := deployService.ComposeServiceLogs(targetID, r.PathValue("service"), tail)
		if err != nil {
			writeDeployComposeError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, logs)
	}
}

func handleDeployComposeServiceAction() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		action := r.PathValue("action")
		if action != "start" && action != "stop" && action != "restart" && action != "recreate" {
			jsonError(w, http.StatusBadRequest, "invalid Compose service action")
			return
		}
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, "Compose service action request body must be empty")
			return
		}
		service := r.PathValue("service")
		if err := deployService.ComposeServiceAction(targetID, service, action); err != nil {
			writeDeployComposeError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"status": "ok", "service": service, "action": action,
		})
	}
}

func handleDeployEnvironmentGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		environment, err := deployService.Environment(targetID)
		if err != nil {
			writeDeployComposeError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, environment)
	}
}

func handleDeployEnvironmentSet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, (64<<10)+4096)
		var request struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := decodeStrictJSON(r, &request); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				jsonError(w, http.StatusRequestEntityTooLarge, "environment variable request is too large")
				return
			}
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		environment, err := deployService.SetEnvironmentVariable(targetID, request.Key, request.Value)
		if err != nil {
			writeDeployComposeError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, environment)
	}
}

func handleDeployEnvironmentDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		environment, err := deployService.DeleteEnvironmentVariable(targetID, r.PathValue("key"))
		if err != nil {
			writeDeployComposeError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, environment)
	}
}

// ---------------------------------------------------------------------------
// Compose project domains
// ---------------------------------------------------------------------------

func handleDeployProjectDomains() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		domains, err := deployService.ProjectDomains(targetID)
		if err != nil {
			writeDeployProjectDomainError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, domains)
	}
}

func handleDeployProjectDomainCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var request models.CreateDeployDomainRequest
		if err := decodeStrictJSON(r, &request); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				jsonError(w, http.StatusRequestEntityTooLarge, "project domain request is too large")
				return
			}
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		domain, err := deployService.CreateProjectDomain(targetID, request)
		if err != nil {
			writeDeployProjectDomainError(w, err)
			return
		}
		jsonResponse(w, http.StatusCreated, domain)
	}
}

func handleDeployProjectDomainDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		domainID, err := deployParseID(r, "domainId")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid domain id")
			return
		}
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, "project domain delete request body must be empty")
			return
		}
		if err := deployService.DeleteProjectDomain(targetID, domainID); err != nil {
			writeDeployProjectDomainError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeployProjectDomainHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		domainID, err := deployParseID(r, "domainId")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid domain id")
			return
		}
		health, err := deployService.ProjectDomainHealth(r.Context(), targetID, domainID)
		if err != nil {
			writeDeployProjectDomainError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, health)
	}
}

func handleDeployProjectDomainTLSEnable() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		domainID, err := deployParseID(r, "domainId")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid domain id")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var request models.EnableDeployDomainTLSRequest
		if err := decodeStrictJSON(r, &request); err != nil && !errors.Is(err, io.EOF) {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				jsonError(w, http.StatusRequestEntityTooLarge, "project domain TLS request is too large")
				return
			}
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		domain, err := deployService.EnableProjectDomainTLS(targetID, domainID, request.Email)
		if err != nil {
			writeDeployProjectDomainError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, domain)
	}
}

func handleDeployProjectDomainTLSDisable() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		domainID, err := deployParseID(r, "domainId")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid domain id")
			return
		}
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, "project domain TLS disable request body must be empty")
			return
		}
		domain, err := deployService.DisableProjectDomainTLS(targetID, domainID)
		if err != nil {
			writeDeployProjectDomainError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, domain)
	}
}

func writeDeployProjectDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deploysvc.ErrInvalidProjectDomain):
		jsonError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, deploysvc.ErrDeployTargetNotFound), errors.Is(err, deploysvc.ErrProjectDomainNotFound):
		jsonError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, deploysvc.ErrComposeTargetRequired), errors.Is(err, deploysvc.ErrProjectDomainConflict), errors.Is(err, deploysvc.ErrProjectDomainsAttached):
		jsonError(w, http.StatusConflict, err.Error())
	default:
		jsonError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeDeployComposeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deploysvc.ErrDeployTargetNotFound):
		jsonError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, deploysvc.ErrComposeTargetRequired), errors.Is(err, deploysvc.ErrComposeProjectUnavailable):
		jsonError(w, http.StatusConflict, err.Error())
	case errors.Is(err, deploysvc.ErrEnvironmentStoreUnavailable):
		jsonError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, deploysvc.ErrInvalidEnvironmentVariable):
		jsonError(w, http.StatusBadRequest, err.Error())
	case err.Error() == "invalid Compose service name" || err.Error() == "tail must be between 1 and 1000":
		jsonError(w, http.StatusBadRequest, err.Error())
	default:
		jsonError(w, http.StatusBadGateway, err.Error())
	}
}

// ---------------------------------------------------------------------------
// DELETE /api/deploy/targets/{id}
// ---------------------------------------------------------------------------

func handleDeployTargetDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		id, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := deployService.DeleteTarget(id); err != nil {
			if errors.Is(err, deploysvc.ErrProjectDomainsAttached) || errors.Is(err, deploysvc.ErrStagingTargetsAttached) {
				jsonError(w, http.StatusConflict, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": "deleted"})
	}
}

// ---------------------------------------------------------------------------
// POST /api/deploy/webhook/{targetId}  — public, no JWT, signature-verified
// ---------------------------------------------------------------------------

// handleDeployWebhook receives signed provider push events and triggers a
// deployment when the delivery identity and branch match. This public endpoint
// uses the target's GitHub or GitLab signing contract instead of JWT.
func handleDeployWebhook() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "targetId")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid targetId")
			return
		}

		// Read the raw body before parsing because both providers sign the exact
		// delivered bytes. MaxBytesReader rejects rather than truncates oversized
		// signed requests.
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				jsonError(w, http.StatusRequestEntityTooLarge, "webhook body exceeds 10 MiB")
				return
			}
			jsonError(w, http.StatusBadRequest, "could not read body")
			return
		}

		target, err := deployService.GetTarget(targetID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if target == nil {
			// Return 200 even for unknown targets to avoid information leakage.
			w.WriteHeader(http.StatusOK)
			return
		}

		if target.WebhookToken == "" {
			if target.WebhookStatus == models.DeployWebhookUnavailable {
				jsonError(w, http.StatusServiceUnavailable, "webhook signing secret is unavailable")
				return
			}
			jsonError(w, http.StatusForbidden, "webhook not configured: no token set")
			return
		}
		delivery, err := deploysvc.AuthenticateWebhook(target.WebhookProvider, target.WebhookToken, deploysvc.WebhookHeaders{
			GitHubEvent:      r.Header.Get("X-GitHub-Event"),
			GitHubDelivery:   r.Header.Get("X-GitHub-Delivery"),
			GitHubSignature:  r.Header.Get("X-Hub-Signature-256"),
			GitLabEvent:      r.Header.Get("X-Gitlab-Event"),
			GitLabWebhookID:  r.Header.Get("webhook-id"),
			GitLabTimestamp:  r.Header.Get("webhook-timestamp"),
			GitLabSignatures: r.Header.Get("webhook-signature"),
		}, body, time.Now().UTC())
		if errors.Is(err, deploysvc.ErrWebhookAuthentication) {
			jsonError(w, http.StatusUnauthorized, "invalid webhook authentication")
			return
		}
		if errors.Is(err, deploysvc.ErrWebhookRequest) {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "could not authenticate webhook")
			return
		}

		if delivery.Event == "ping" {
			jsonResponse(w, http.StatusOK, map[string]string{"message": "pong"})
			return
		}
		if delivery.Event != "push" {
			jsonResponse(w, http.StatusOK, map[string]string{"message": "event ignored: " + delivery.Event})
			return
		}

		// Consume an authenticated push identity before checking deployment
		// eligibility. Providers retry deliveries; returning 200 for a replay
		// prevents a retry loop without ever queuing the same push twice.
		if err := deployService.RegisterWebhookDelivery(targetID, delivery.Provider, delivery.DeliveryID, time.Now().UTC()); err != nil {
			if errors.Is(err, deploysvc.ErrWebhookReplay) {
				jsonResponse(w, http.StatusOK, map[string]string{"message": "delivery already processed"})
				return
			}
			if errors.Is(err, deploysvc.ErrWebhookRequest) {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, "could not register webhook delivery")
			return
		}

		if !target.AutoDeploy || !target.IsActive {
			jsonResponse(w, http.StatusOK, map[string]string{"message": "auto-deploy disabled"})
			return
		}

		expectedRef := "refs/heads/" + target.Branch
		if delivery.Ref != expectedRef {
			jsonResponse(w, http.StatusOK, map[string]any{
				"message": "push to non-target branch — skipped",
				"ref":     delivery.Ref,
				"target":  expectedRef,
			})
			return
		}

		run, err := deployService.TriggerDeploy(targetID, "webhook")
		if err != nil {
			if errors.Is(err, deploysvc.ErrPreflight) {
				jsonError(w, http.StatusConflict, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		jsonResponse(w, http.StatusAccepted, map[string]any{
			"message": "deployment queued",
			"runId":   run.ID,
		})
	}
}

// ---------------------------------------------------------------------------
// POST /api/deploy/manual/{targetId}
// ---------------------------------------------------------------------------

func handleDeployManual() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "targetId")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid targetId")
			return
		}
		run, err := deployService.TriggerDeploy(targetID, "manual")
		if err != nil {
			if errors.Is(err, deploysvc.ErrPreflight) {
				jsonError(w, http.StatusConflict, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusAccepted, map[string]any{
			"message": "deployment queued",
			"runId":   run.ID,
		})
	}
}

// ---------------------------------------------------------------------------
// GET /api/deploy/history
// ---------------------------------------------------------------------------

func handleDeployHistory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		var targetID int64
		if s := r.URL.Query().Get("targetId"); s != "" {
			var err error
			targetID, err = strconv.ParseInt(s, 10, 64)
			if err != nil || targetID <= 0 {
				jsonError(w, http.StatusBadRequest, "targetId must be a positive integer")
				return
			}
		}

		limit := 50
		if s := r.URL.Query().Get("limit"); s != "" {
			parsed, err := strconv.Atoi(s)
			if err != nil || parsed < 1 || parsed > 500 {
				jsonError(w, http.StatusBadRequest, "limit must be between 1 and 500")
				return
			}
			limit = parsed
		}

		runs, err := deployService.ListRuns(targetID, limit)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Strip log blobs from the list view — callers use the /logs endpoint.
		for i := range runs {
			runs[i].Logs = ""
		}
		if runs == nil {
			runs = []models.DeployRun{}
		}
		jsonResponse(w, http.StatusOK, runs)
	}
}

// ---------------------------------------------------------------------------
// GET /api/deploy/history/{id}/logs
// ---------------------------------------------------------------------------

func handleDeployRunLogs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		id, err := deployParseID(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		logs, err := deployService.GetRunLogs(id)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"logs": logs})
	}
}

// ---------------------------------------------------------------------------
// POST /api/deploy/rollback/{targetId}
// ---------------------------------------------------------------------------

func handleDeployRollback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deployServiceUnavailable(w) {
			return
		}
		targetID, err := deployParseID(r, "targetId")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid targetId")
			return
		}
		run, err := deployService.Rollback(targetID)
		if err != nil {
			if errors.Is(err, deploysvc.ErrPreflight) {
				jsonError(w, http.StatusConflict, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusAccepted, map[string]any{
			"message": "rollback queued",
			"runId":   run.ID,
		})
	}
}

// ---------------------------------------------------------------------------
// Local helper
// ---------------------------------------------------------------------------

func deployParseID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("deploy identifier must be a positive integer")
	}
	return id, nil
}
