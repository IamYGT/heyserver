package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

var (
	ErrInvalidProjectDomain   = errors.New("invalid project domain")
	ErrProjectDomainNotFound  = errors.New("project domain not found")
	ErrProjectDomainConflict  = errors.New("project domain already exists")
	ErrProjectDomainsAttached = errors.New("remove project domains before deleting the deploy target")
	projectDomainLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

// ProjectDomainRuntime owns the bounded host mutation needed to make a
// persisted domain mapping effective. Implementations may not accept raw
// Nginx content or arbitrary commands from API input.
type ProjectDomainRuntime interface {
	Apply(models.DeployDomain) error
	Remove(models.DeployDomain) error
	EnableTLS(models.DeployDomain, string) (models.DeployDomainTLSState, error)
	DisableTLS(models.DeployDomain) error
	TLSState(models.DeployDomain) (models.DeployDomainTLSState, error)
	RenewTLS(models.DeployDomain) (models.DeployDomainTLSState, error)
}

type ProjectDomainTLSMaintenanceReport struct {
	Checked int
	Renewed int
	Failed  int
}

func (s *Service) ProjectDomains(targetID int64) ([]models.DeployDomain, error) {
	if _, err := s.configuredComposeTarget(targetID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT id, target_id, domain, service, host_port, tls_enabled, created_at, updated_at
		FROM deploy_domains WHERE target_id = ? ORDER BY domain ASC
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	domains := make([]models.DeployDomain, 0)
	for rows.Next() {
		var domain models.DeployDomain
		var tlsEnabled int
		if err := rows.Scan(&domain.ID, &domain.TargetID, &domain.Domain, &domain.Service, &domain.HostPort, &tlsEnabled, &domain.CreatedAt, &domain.UpdatedAt); err != nil {
			return nil, err
		}
		domain.TLSEnabled = tlsEnabled == 1
		s.decorateProjectDomain(&domain)
		domains = append(domains, domain)
	}
	return domains, rows.Err()
}

func (s *Service) CreateProjectDomain(targetID int64, req models.CreateDeployDomainRequest) (*models.DeployDomain, error) {
	if _, err := s.configuredComposeTarget(targetID); err != nil {
		return nil, err
	}
	req.Domain = normalizeProjectDomain(req.Domain)
	req.Service = strings.TrimSpace(req.Service)
	if !validProjectDomain(req.Domain) {
		return nil, fmt.Errorf("%w: domain must be a valid ASCII hostname", ErrInvalidProjectDomain)
	}
	if !validComposeService(req.Service) {
		return nil, fmt.Errorf("%w: invalid Compose service", ErrInvalidProjectDomain)
	}
	if req.HostPort < 1 || req.HostPort > 65535 {
		return nil, fmt.Errorf("%w: hostPort must be between 1 and 65535", ErrInvalidProjectDomain)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(`
		INSERT INTO deploy_domains (target_id, domain, service, host_port, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, targetID, req.Domain, req.Service, req.HostPort, now, now)
	if err != nil {
		_ = tx.Rollback()
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, fmt.Errorf("%w: %s", ErrProjectDomainConflict, req.Domain)
		}
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	domain := &models.DeployDomain{
		ID: id, TargetID: targetID, Domain: req.Domain, Service: req.Service,
		HostPort: req.HostPort, CreatedAt: now, UpdatedAt: now,
	}
	s.decorateProjectDomain(domain)
	if err := s.projectDomainRuntime.Apply(*domain); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("activate project domain: %w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		if runtimeErr := s.projectDomainRuntime.Remove(*domain); runtimeErr != nil {
			return nil, fmt.Errorf("persist project domain: %v; remove activated Nginx mapping: %w", err, runtimeErr)
		}
		return nil, err
	}
	return domain, nil
}

func (s *Service) DeleteProjectDomain(targetID, domainID int64) error {
	if _, err := s.configuredComposeTarget(targetID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	domain, err := s.projectDomainByID(domainID)
	if err != nil {
		return err
	}
	if domain.TargetID != targetID {
		return ErrProjectDomainNotFound
	}
	if err := s.projectDomainRuntime.Remove(*domain); err != nil {
		return fmt.Errorf("deactivate project domain: %w", err)
	}
	result, err := s.db.Exec(`DELETE FROM deploy_domains WHERE id = ? AND target_id = ?`, domainID, targetID)
	if err != nil {
		if restoreErr := s.projectDomainRuntime.Apply(*domain); restoreErr != nil {
			return fmt.Errorf("delete project domain: %v; restore Nginx mapping: %w", err, restoreErr)
		}
		return err
	}
	deleted, _ := result.RowsAffected()
	if deleted != 1 {
		_ = s.projectDomainRuntime.Apply(*domain)
		return ErrProjectDomainNotFound
	}
	return nil
}

func (s *Service) ProjectDomainHealth(ctx context.Context, targetID, domainID int64) (*models.DeployDomainHealth, error) {
	if _, err := s.configuredComposeTarget(targetID); err != nil {
		return nil, err
	}
	domain, err := s.projectDomainByID(domainID)
	if err != nil || domain.TargetID != targetID {
		return nil, ErrProjectDomainNotFound
	}
	result := &models.DeployDomainHealth{
		Domain: domain.Domain, Upstream: domain.Upstream, Status: "unavailable",
		Message: "Loopback upstream did not respond.", CheckedAt: time.Now().UTC(),
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, domain.Upstream+"/", nil)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(req)
	result.LatencyMs = time.Since(started).Milliseconds()
	result.CheckedAt = time.Now().UTC()
	if err != nil {
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

func (s *Service) EnableProjectDomainTLS(targetID, domainID int64, email string) (*models.DeployDomain, error) {
	if _, err := s.configuredComposeTarget(targetID); err != nil {
		return nil, err
	}
	email = strings.TrimSpace(email)
	if email != "" {
		address, err := mail.ParseAddress(email)
		if len(email) > 254 || err != nil || address.Address != email || address.Name != "" {
			return nil, fmt.Errorf("%w: email", ErrInvalidProjectDomain)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	domain, err := s.projectDomainByID(domainID)
	if err != nil || domain.TargetID != targetID {
		return nil, ErrProjectDomainNotFound
	}
	if _, err := s.projectDomainRuntime.EnableTLS(*domain, email); err != nil {
		return nil, fmt.Errorf("enable project domain TLS: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE deploy_domains SET tls_enabled = 1, updated_at = ? WHERE id = ? AND target_id = ?`, time.Now().UTC(), domainID, targetID); err != nil {
		if rollbackErr := s.projectDomainRuntime.DisableTLS(*domain); rollbackErr != nil {
			return nil, fmt.Errorf("persist project domain TLS: %v; restore HTTP mapping: %w", err, rollbackErr)
		}
		return nil, err
	}
	return s.projectDomainByID(domainID)
}

func (s *Service) DisableProjectDomainTLS(targetID, domainID int64) (*models.DeployDomain, error) {
	if _, err := s.configuredComposeTarget(targetID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	domain, err := s.projectDomainByID(domainID)
	if err != nil || domain.TargetID != targetID {
		return nil, ErrProjectDomainNotFound
	}
	if !domain.TLSEnabled {
		return domain, nil
	}
	if err := s.projectDomainRuntime.DisableTLS(*domain); err != nil {
		return nil, fmt.Errorf("disable project domain TLS: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE deploy_domains SET tls_enabled = 0, updated_at = ? WHERE id = ? AND target_id = ?`, time.Now().UTC(), domainID, targetID); err != nil {
		if _, rollbackErr := s.projectDomainRuntime.EnableTLS(*domain, ""); rollbackErr != nil {
			return nil, fmt.Errorf("persist disabled project domain TLS: %v; restore TLS mapping: %w", err, rollbackErr)
		}
		return nil, err
	}
	return s.projectDomainByID(domainID)
}

// MaintainProjectDomainTLS renews only mappings whose observed certificate is
// expiring or expired. Missing certificates stay visible as unavailable and
// are never silently reissued without the operator's ACME account decision.
func (s *Service) MaintainProjectDomainTLS() ProjectDomainTLSMaintenanceReport {
	report := ProjectDomainTLSMaintenanceReport{}
	rows, err := s.db.Query(`
		SELECT id, target_id, domain, service, host_port, tls_enabled, created_at, updated_at
		FROM deploy_domains WHERE tls_enabled = 1 ORDER BY id ASC
	`)
	if err != nil {
		report.Failed = 1
		return report
	}
	domains := make([]models.DeployDomain, 0)
	for rows.Next() {
		var domain models.DeployDomain
		if err := rows.Scan(&domain.ID, &domain.TargetID, &domain.Domain, &domain.Service, &domain.HostPort, &domain.TLSEnabled, &domain.CreatedAt, &domain.UpdatedAt); err != nil {
			report.Failed++
			continue
		}
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		report.Failed++
	}
	_ = rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, domain := range domains {
		report.Checked++
		state, err := s.projectDomainRuntime.TLSState(domain)
		if err != nil {
			report.Failed++
			continue
		}
		if state.Status != "expiring" && state.Status != "expired" {
			continue
		}
		if _, err := s.projectDomainRuntime.RenewTLS(domain); err != nil {
			report.Failed++
			continue
		}
		report.Renewed++
	}
	return report
}

func (s *Service) projectDomainByID(id int64) (*models.DeployDomain, error) {
	var domain models.DeployDomain
	err := s.db.QueryRow(`
		SELECT id, target_id, domain, service, host_port, tls_enabled, created_at, updated_at
		FROM deploy_domains WHERE id = ?
	`, id).Scan(&domain.ID, &domain.TargetID, &domain.Domain, &domain.Service, &domain.HostPort, &domain.TLSEnabled, &domain.CreatedAt, &domain.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrProjectDomainNotFound
	}
	if err != nil {
		return nil, err
	}
	s.decorateProjectDomain(&domain)
	return &domain, nil
}

func (s *Service) decorateProjectDomain(domain *models.DeployDomain) {
	domain.Upstream = fmt.Sprintf("http://127.0.0.1:%d", domain.HostPort)
	if !domain.TLSEnabled {
		domain.TLSStatus = "not_configured"
		domain.TLSMessage = "TLS is not enabled for this project domain."
		return
	}
	state, err := s.projectDomainRuntime.TLSState(*domain)
	if err != nil {
		domain.TLSStatus = "unavailable"
		domain.TLSMessage = err.Error()
		return
	}
	domain.TLSStatus = state.Status
	domain.TLSExpiresAt = state.ExpiresAt
	domain.TLSDaysRemaining = state.DaysRemaining
	domain.TLSMessage = state.Message
}

func normalizeProjectDomain(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func validProjectDomain(value string) bool {
	if value == "" || len(value) > 253 || strings.Contains(value, "..") {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !projectDomainLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}
