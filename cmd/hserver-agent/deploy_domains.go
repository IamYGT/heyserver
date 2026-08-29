package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	deployservice "github.com/IamYGT/heyserver/internal/services/deploy"
)

func runDeployDomainTLSMaintenance(ctx context.Context, logger *slog.Logger, controller deployDomainController, interval time.Duration) {
	if interval <= 0 {
		interval = 12 * time.Hour
	}
	run := func() {
		report := controller.MaintainTLS()
		if report.Checked > 0 || report.Renewed > 0 || report.Failed > 0 {
			logger.Info("managed project TLS maintenance completed", "checked", report.Checked, "renewed", report.Renewed, "failed", report.Failed)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

const (
	maxManagedDeployDomains    = 64
	maxManagedDomainConfigSize = 128 << 10
)

var (
	agentDeployDomainLabelPattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	agentDeployProxyPassPattern      = regexp.MustCompile(`(?m)^\s*proxy_pass http://127\.0\.0\.1:([0-9]{1,5});\s*$`)
	agentDeployDomainRevisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const (
	deployDomainObservationAbsent  = "absent"
	deployDomainObservationManaged = "managed"
	deployDomainObservationDrift   = "drift"
	deployDomainObservationForeign = "foreign"
)

var (
	errManagedDeployDomainStale       = errors.New("stale_observation")
	errManagedDeployDomainDrift       = errors.New("domain_drift")
	errManagedDeployDomainConflict    = errors.New("domain_conflict")
	errManagedDeployDomainCleanup     = errors.New("domain_cleanup_failed")
	errManagedDeployDomainObservation = errors.New("domain_observation_failed")
	errManagedDeployDomainOperation   = errors.New("domain_operation_failed")
)

type managedDeployDomain struct {
	TargetID         string `json:"target_id"`
	Domain           string `json:"domain"`
	HostPort         int    `json:"host_port"`
	DesiredHostPort  int    `json:"desired_host_port"`
	Upstream         string `json:"upstream"`
	Status           string `json:"status"`
	Message          string `json:"message"`
	TLSStatus        string `json:"tls_status"`
	TLSExpiresAt     string `json:"tls_expires_at,omitempty"`
	TLSDaysRemaining int    `json:"tls_days_remaining,omitempty"`
	TLSMessage       string `json:"tls_message"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	Enabled          bool   `json:"enabled"`
	Revision         string `json:"revision"`
}

type managedDeployDomainEnsureReceipt struct {
	Changed     bool                `json:"changed"`
	Observation managedDeployDomain `json:"observation"`
}

type managedDeployDomainObservation struct {
	state   string
	managed managedDeployDomain
}

type managedDeployDomainHealth struct {
	Domain     string `json:"domain"`
	Upstream   string `json:"upstream"`
	Status     string `json:"status"`
	StatusCode int    `json:"status_code,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`
	Message    string `json:"message"`
	CheckedAt  string `json:"checked_at"`
}

type deployDomainTLSMaintenanceReport struct {
	Checked int
	Renewed int
	Failed  int
}

type deployProjectDomainRuntime interface {
	Apply(models.DeployDomain) error
	Remove(models.DeployDomain) error
	EnableTLS(models.DeployDomain, string) (models.DeployDomainTLSState, error)
	DisableTLS(models.DeployDomain) error
	TLSState(models.DeployDomain) (models.DeployDomainTLSState, error)
	RenewTLS(models.DeployDomain) (models.DeployDomainTLSState, error)
}

type deployDomainController struct {
	deploys        deployController
	runtime        deployProjectDomainRuntime
	allowRead      bool
	allowActions   bool
	sitesAvailable string
	sitesEnabled   string
	operations     *sync.Mutex
	now            func() time.Time
}

func (c deployDomainController) Ensure(_ context.Context, targetID, rawDomain, expectedRevision string) (managedDeployDomainEnsureReceipt, error) {
	if !c.allowActions {
		return managedDeployDomainEnsureReceipt{}, errors.New("project domain actions are not enabled locally")
	}
	domain, err := normalizeAgentDeployDomain(rawDomain)
	if err != nil {
		return managedDeployDomainEnsureReceipt{}, err
	}
	if !validAgentDeployDomainRevision(expectedRevision) {
		return managedDeployDomainEnsureReceipt{}, errors.New("expected_revision must be absent or a lowercase SHA-256 revision")
	}

	c.operations.Lock()
	defer c.operations.Unlock()

	plan, err := c.configuredTarget(targetID)
	if err != nil {
		return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainObservation
	}
	observed, err := c.observeDomainWithPlan(plan, targetID, domain)
	if err != nil {
		return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainObservation
	}

	switch observed.state {
	case deployDomainObservationForeign:
		return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainConflict
	case deployDomainObservationDrift:
		if observed.managed.Revision != "" && observed.managed.Revision != expectedRevision {
			return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainStale
		}
		return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainDrift
	case deployDomainObservationManaged:
		if observed.managed.Revision != expectedRevision {
			return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainStale
		}
		if observed.managed.Status != "active" || !observed.managed.Enabled {
			return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainDrift
		}
		return managedDeployDomainEnsureReceipt{Changed: false, Observation: observed.managed}, nil
	case deployDomainObservationAbsent:
		if expectedRevision != "absent" {
			return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainStale
		}
	default:
		return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainObservation
	}

	if err := c.runtime.Apply(c.domainModel(plan, domain, plan.HostPort, false)); err != nil {
		if errors.Is(err, deployservice.ErrProjectDomainCleanup) {
			return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainCleanup
		}
		return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainOperation
	}

	postApply, err := c.observeDomainWithPlan(plan, targetID, domain)
	if err != nil {
		return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainObservation
	}
	if postApply.state != deployDomainObservationManaged || postApply.managed.Status != "active" || !postApply.managed.Enabled {
		// Apply is deliberately followed by a fresh observation. A result that
		// is not an active, enabled, target-owned mapping is not reported as a
		// successful ensure, even if the runtime returned nil.
		if postApply.state == deployDomainObservationForeign {
			return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainConflict
		}
		return managedDeployDomainEnsureReceipt{}, errManagedDeployDomainDrift
	}
	return managedDeployDomainEnsureReceipt{Changed: true, Observation: postApply.managed}, nil
}

func newDeployDomainController(deploys deployController, runtime deployProjectDomainRuntime, allowRead, allowActions bool, sitesAvailable, sitesEnabled string, operations *sync.Mutex) deployDomainController {
	if operations == nil {
		operations = &sync.Mutex{}
	}
	if strings.TrimSpace(sitesEnabled) == "" {
		sitesEnabled = filepath.Join(filepath.Dir(filepath.Clean(sitesAvailable)), "sites-enabled")
	}
	return deployDomainController{
		deploys: deploys, runtime: runtime, allowRead: allowRead, allowActions: allowActions,
		sitesAvailable: filepath.Clean(sitesAvailable), sitesEnabled: filepath.Clean(sitesEnabled), operations: operations, now: time.Now,
	}
}

func (c deployDomainController) Inventory(_ context.Context, targetID string) ([]managedDeployDomain, error) {
	if !c.allowRead {
		return nil, errors.New("project domain inventory is not enabled locally")
	}
	c.operations.Lock()
	defer c.operations.Unlock()
	return c.inventoryLocked(targetID)
}

func (c deployDomainController) Create(_ context.Context, targetID, rawDomain string) (managedDeployDomain, error) {
	if !c.allowActions {
		return managedDeployDomain{}, errors.New("project domain actions are not enabled locally")
	}
	domain, err := normalizeAgentDeployDomain(rawDomain)
	if err != nil {
		return managedDeployDomain{}, err
	}
	c.operations.Lock()
	defer c.operations.Unlock()
	plan, err := c.configuredTarget(targetID)
	if err != nil {
		return managedDeployDomain{}, err
	}
	model := c.domainModel(plan, domain, plan.HostPort, false)
	if err := c.runtime.Apply(model); err != nil {
		return managedDeployDomain{}, fmt.Errorf("activate managed project domain: %w", err)
	}
	return c.domainLocked(targetID, domain)
}

func (c deployDomainController) Delete(_ context.Context, targetID, rawDomain string) error {
	if !c.allowActions {
		return errors.New("project domain actions are not enabled locally")
	}
	domain, err := normalizeAgentDeployDomain(rawDomain)
	if err != nil {
		return err
	}
	c.operations.Lock()
	defer c.operations.Unlock()
	observed, err := c.domainLocked(targetID, domain)
	if err != nil {
		return err
	}
	if err := c.runtime.Remove(c.observedDomainModel(observed)); err != nil {
		return fmt.Errorf("deactivate managed project domain: %w", err)
	}
	return nil
}

func (c deployDomainController) EnableTLS(_ context.Context, targetID, rawDomain, rawEmail string) (managedDeployDomain, error) {
	if !c.allowActions {
		return managedDeployDomain{}, errors.New("project domain actions are not enabled locally")
	}
	domain, err := normalizeAgentDeployDomain(rawDomain)
	if err != nil {
		return managedDeployDomain{}, err
	}
	email := strings.TrimSpace(rawEmail)
	if email != "" {
		address, parseErr := mail.ParseAddress(email)
		if parseErr != nil || address.Address != email || address.Name != "" {
			return managedDeployDomain{}, errors.New("invalid ACME account email")
		}
	}
	c.operations.Lock()
	defer c.operations.Unlock()
	observed, err := c.domainLocked(targetID, domain)
	if err != nil {
		return managedDeployDomain{}, err
	}
	if _, err := c.runtime.EnableTLS(c.observedDomainModel(observed), email); err != nil {
		return managedDeployDomain{}, fmt.Errorf("enable managed project TLS: %w", err)
	}
	return c.domainLocked(targetID, domain)
}

func (c deployDomainController) DisableTLS(_ context.Context, targetID, rawDomain string) (managedDeployDomain, error) {
	if !c.allowActions {
		return managedDeployDomain{}, errors.New("project domain actions are not enabled locally")
	}
	domain, err := normalizeAgentDeployDomain(rawDomain)
	if err != nil {
		return managedDeployDomain{}, err
	}
	c.operations.Lock()
	defer c.operations.Unlock()
	observed, err := c.domainLocked(targetID, domain)
	if err != nil {
		return managedDeployDomain{}, err
	}
	if err := c.runtime.DisableTLS(c.observedDomainModel(observed)); err != nil {
		return managedDeployDomain{}, fmt.Errorf("disable managed project TLS: %w", err)
	}
	return c.domainLocked(targetID, domain)
}

func (c deployDomainController) RenewTLS(_ context.Context, targetID, rawDomain string) (managedDeployDomain, error) {
	if !c.allowActions {
		return managedDeployDomain{}, errors.New("project domain actions are not enabled locally")
	}
	domain, err := normalizeAgentDeployDomain(rawDomain)
	if err != nil {
		return managedDeployDomain{}, err
	}
	c.operations.Lock()
	defer c.operations.Unlock()
	observed, err := c.domainLocked(targetID, domain)
	if err != nil {
		return managedDeployDomain{}, err
	}
	if observed.TLSStatus == "not_configured" {
		return managedDeployDomain{}, errors.New("managed project TLS is not enabled")
	}
	if _, err := c.runtime.RenewTLS(c.observedDomainModel(observed)); err != nil {
		return managedDeployDomain{}, fmt.Errorf("renew managed project TLS: %w", err)
	}
	return c.domainLocked(targetID, domain)
}

func (c deployDomainController) Health(ctx context.Context, targetID, rawDomain string) (managedDeployDomainHealth, error) {
	if !c.allowRead {
		return managedDeployDomainHealth{}, errors.New("project domain inventory is not enabled locally")
	}
	domain, err := normalizeAgentDeployDomain(rawDomain)
	if err != nil {
		return managedDeployDomainHealth{}, err
	}
	c.operations.Lock()
	observed, err := c.domainLocked(targetID, domain)
	c.operations.Unlock()
	if err != nil {
		return managedDeployDomainHealth{}, err
	}
	result := managedDeployDomainHealth{
		Domain: observed.Domain, Upstream: observed.Upstream, Status: "unavailable",
		Message: "Loopback upstream did not respond.", CheckedAt: c.now().UTC().Format(time.RFC3339Nano),
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, observed.Upstream+"/", nil)
	if err != nil {
		return managedDeployDomainHealth{}, err
	}
	started := c.now()
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, requestErr := client.Do(request)
	result.LatencyMS = c.now().Sub(started).Milliseconds()
	result.CheckedAt = c.now().UTC().Format(time.RFC3339Nano)
	if requestErr != nil {
		return result, nil
	}
	defer func() { _ = response.Body.Close() }()
	result.StatusCode = response.StatusCode
	if response.StatusCode >= 200 && response.StatusCode < 400 {
		result.Status = "healthy"
		result.Message = "Loopback upstream returned a successful response."
	} else {
		result.Status = "unhealthy"
		result.Message = "Loopback upstream responded outside the accepted 200-399 range."
	}
	return result, nil
}

func (c deployDomainController) MaintainTLS() deployDomainTLSMaintenanceReport {
	report := deployDomainTLSMaintenanceReport{}
	if !c.allowRead || !c.allowActions {
		return report
	}
	c.operations.Lock()
	defer c.operations.Unlock()
	plans, err := c.deploys.loadPlans()
	if err != nil {
		report.Failed++
		return report
	}
	for _, plan := range plans {
		if plan.HostPort == 0 {
			continue
		}
		domains, inventoryErr := c.inventoryLocked(plan.ID)
		if inventoryErr != nil {
			report.Failed++
			continue
		}
		for _, domain := range domains {
			if domain.TLSStatus == "not_configured" {
				continue
			}
			report.Checked++
			if domain.TLSStatus != "expiring" && domain.TLSStatus != "expired" {
				continue
			}
			if _, renewErr := c.runtime.RenewTLS(c.observedDomainModel(domain)); renewErr != nil {
				report.Failed++
				continue
			}
			report.Renewed++
		}
	}
	return report
}

func (c deployDomainController) inventoryLocked(targetID string) ([]managedDeployDomain, error) {
	plan, err := c.configuredTarget(targetID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(c.sitesAvailable)
	if err != nil {
		return nil, fmt.Errorf("read Nginx sites: %w", err)
	}
	domains := make([]managedDeployDomain, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		domain := strings.TrimSuffix(entry.Name(), ".conf")
		if _, err := normalizeAgentDeployDomain(domain); err != nil {
			continue
		}
		observed, observeErr := c.observeDomainWithPlan(plan, targetID, domain)
		if observeErr != nil || (observed.state != deployDomainObservationManaged && observed.state != deployDomainObservationDrift) {
			continue
		}
		if len(domains) == maxManagedDeployDomains {
			return nil, fmt.Errorf("managed project domains exceed the %d-domain limit", maxManagedDeployDomains)
		}
		domains = append(domains, observed.managed)
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].Domain < domains[j].Domain })
	return domains, nil
}

func (c deployDomainController) observeDomainWithPlan(plan deployPlanConfig, targetID, domain string) (managedDeployDomainObservation, error) {
	availablePath := filepath.Join(c.sitesAvailable, domain+".conf")
	enabledPath := filepath.Join(c.sitesEnabled, domain+".conf")
	availableInfo, err := os.Lstat(availablePath)
	if os.IsNotExist(err) {
		if _, enabledErr := os.Lstat(enabledPath); os.IsNotExist(enabledErr) {
			return managedDeployDomainObservation{state: deployDomainObservationAbsent}, nil
		} else if enabledErr != nil {
			return managedDeployDomainObservation{}, enabledErr
		}
		return managedDeployDomainObservation{state: deployDomainObservationForeign}, nil
	}
	if err != nil {
		return managedDeployDomainObservation{}, err
	}
	if !availableInfo.Mode().IsRegular() || availableInfo.Size() > maxManagedDomainConfigSize {
		return managedDeployDomainObservation{state: deployDomainObservationForeign}, nil
	}
	content, err := os.ReadFile(availablePath)
	if err != nil {
		return managedDeployDomainObservation{}, err
	}
	if len(content) > maxManagedDomainConfigSize {
		return managedDeployDomainObservation{state: deployDomainObservationForeign}, nil
	}
	enabled, err := exactManagedDomainSymlink(enabledPath, availablePath)
	if err != nil {
		return managedDeployDomainObservation{}, err
	}
	revision := agentDeployDomainRevision(content, enabled)
	base := managedDeployDomain{
		TargetID: targetID, Domain: domain, DesiredHostPort: plan.HostPort,
		Enabled: enabled, Revision: revision, UpdatedAt: availableInfo.ModTime().UTC().Format(time.RFC3339Nano),
	}
	text := string(content)
	owner := agentDeployDomainOwner(targetID)
	if !strings.Contains(text, "# hserver-project-target: "+owner+"\n") || !strings.Contains(text, "# hserver-project-domain: "+domain+"\n") {
		base.Status = "conflict"
		base.Message = "An existing Nginx mapping is not owned by this deploy target."
		return managedDeployDomainObservation{state: deployDomainObservationForeign, managed: base}, nil
	}
	matches := agentDeployProxyPassPattern.FindStringSubmatch(text)
	if len(matches) != 2 {
		base.Status = "drifted"
		base.Message = "Managed Nginx mapping has no valid loopback upstream."
		return managedDeployDomainObservation{state: deployDomainObservationDrift, managed: base}, nil
	}
	hostPort, parseErr := strconv.Atoi(matches[1])
	if parseErr != nil || hostPort < 1 || hostPort > 65535 {
		base.Status = "drifted"
		base.Message = "Managed Nginx mapping has an invalid loopback port."
		return managedDeployDomainObservation{state: deployDomainObservationDrift, managed: base}, nil
	}
	tlsEnabled := strings.Contains(text, "listen 443 ssl;")
	model := c.domainModel(plan, domain, hostPort, tlsEnabled)
	state, stateErr := c.runtime.TLSState(model)
	base.HostPort = hostPort
	base.Upstream = fmt.Sprintf("http://127.0.0.1:%d", hostPort)
	base.Status = "active"
	base.Message = "Managed Nginx mapping matches the local deploy plan."
	base.TLSStatus = state.Status
	base.TLSMessage = state.Message
	base.TLSDaysRemaining = state.DaysRemaining
	if stateErr != nil {
		base.TLSStatus = "unavailable"
		base.TLSMessage = stateErr.Error()
	} else if state.ExpiresAt != nil {
		base.TLSExpiresAt = state.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if hostPort != plan.HostPort {
		base.Status = "drifted"
		base.Message = fmt.Sprintf("Nginx maps port %d while the local deploy plan declares port %d.", hostPort, plan.HostPort)
	} else if !enabled {
		base.Status = "drifted"
		base.Message = "Managed Nginx mapping is not enabled by its exact sites-enabled symlink."
	}
	stateName := deployDomainObservationManaged
	if base.Status != "active" {
		stateName = deployDomainObservationDrift
	}
	return managedDeployDomainObservation{state: stateName, managed: base}, nil
}

func exactManagedDomainSymlink(enabledPath, availablePath string) (bool, error) {
	info, err := os.Lstat(enabledPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	target, err := os.Readlink(enabledPath)
	if err != nil {
		return false, err
	}
	return target == availablePath, nil
}

func agentDeployDomainRevision(content []byte, enabled bool) string {
	hash := sha256.New()
	_, _ = hash.Write(content)
	_, _ = hash.Write([]byte{0})
	if enabled {
		_, _ = hash.Write([]byte("enabled"))
	} else {
		_, _ = hash.Write([]byte("disabled"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validAgentDeployDomainRevision(value string) bool {
	return value == "absent" || agentDeployDomainRevisionPattern.MatchString(value)
}

func (c deployDomainController) domainLocked(targetID, domain string) (managedDeployDomain, error) {
	domains, err := c.inventoryLocked(targetID)
	if err != nil {
		return managedDeployDomain{}, err
	}
	for _, candidate := range domains {
		if candidate.Domain == domain {
			return candidate, nil
		}
	}
	return managedDeployDomain{}, errors.New("managed project domain was not found")
}

func (c deployDomainController) configuredTarget(targetID string) (deployPlanConfig, error) {
	plans, err := c.deploys.loadPlans()
	if err != nil {
		return deployPlanConfig{}, err
	}
	for _, plan := range plans {
		if plan.ID != targetID {
			continue
		}
		if plan.HostPort < 1 || plan.HostPort > 65535 {
			return deployPlanConfig{}, errors.New("deploy target has no locally configured host_port")
		}
		return plan, nil
	}
	return deployPlanConfig{}, errors.New("deploy target is not configured locally")
}

func (c deployDomainController) domainModel(plan deployPlanConfig, domain string, hostPort int, tlsEnabled bool) models.DeployDomain {
	return models.DeployDomain{RuntimeOwner: agentDeployDomainOwner(plan.ID), Domain: domain, Service: plan.ID, HostPort: hostPort, TLSEnabled: tlsEnabled}
}

func (c deployDomainController) observedDomainModel(domain managedDeployDomain) models.DeployDomain {
	return models.DeployDomain{
		RuntimeOwner: agentDeployDomainOwner(domain.TargetID), Domain: domain.Domain, Service: domain.TargetID,
		HostPort: domain.HostPort, TLSEnabled: domain.TLSStatus != "not_configured",
	}
}

func agentDeployDomainOwner(targetID string) string {
	return "agent-" + targetID
}

func normalizeAgentDeployDomain(value string) (string, error) {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if value == "" || len(value) > 253 {
		return "", errors.New("domain must be a valid ASCII hostname")
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return "", errors.New("domain must contain at least two labels")
	}
	for _, label := range labels {
		if !agentDeployDomainLabelPattern.MatchString(label) {
			return "", errors.New("domain must be a valid ASCII hostname")
		}
	}
	return value, nil
}
