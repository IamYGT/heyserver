// Package mail provides management operations for the configured mail server
// via its REST Management API.
//
// Stalwart uses "principals" for accounts and domains:
//   - type="individual" -> mail account (user@domain)
//   - type="domain"     -> mail domain
//   - type="list"       -> mailing list / alias
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	httpTimeout = 10 * time.Second
)

// ErrNotConfigured identifies an optional mail integration without the
// installation-owned setting required by the requested operation.
var ErrNotConfigured = errors.New("mail integration not configured")

type ServiceStatus struct {
	Running bool   `json:"running"`
	Status  string `json:"status"`
	PID     string `json:"pid,omitempty"`
	Uptime  string `json:"uptime,omitempty"`
}

type MailDomain struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type MailAccount struct {
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	Domain      string   `json:"domain"`
	Quota       int64    `json:"quota"`
	UsedStorage int64    `json:"usedStorage"`
	IsEnabled   bool     `json:"isEnabled"`
	Aliases     []string `json:"aliases"`
}

type MailAlias struct {
	ID           string   `json:"id"`
	Address      string   `json:"address"`
	Destinations []string `json:"destinations"`
	Description  string   `json:"description,omitempty"`
}

type QueueMessage struct {
	ID         string    `json:"id"`
	Sender     string    `json:"sender"`
	Recipients []string  `json:"recipients"`
	CreatedAt  time.Time `json:"createdAt"`
	NextRetry  time.Time `json:"nextRetry,omitempty"`
	Retries    int       `json:"retries"`
}

// DNSCheckResult, DNSHealthReport and related types are defined in dns_check.go.

type CreateAccountRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Quota    int64  `json:"quota"`
}

type stalwartPrincipal struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Quota       int64    `json:"quota,omitempty"`
	Description string   `json:"description,omitempty"`
	Secrets     []string `json:"secrets,omitempty"`
	Emails      []string `json:"emails,omitempty"`
	Members     []string `json:"members,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
}

type stalwartListResponse struct {
	Total int      `json:"total"`
	Items []string `json:"items"`
}

type stalwartDataListResponse struct {
	Data stalwartDataList `json:"data"`
}

type stalwartDataList struct {
	Total int                    `json:"total"`
	Items []stalwartDataListItem `json:"items"`
}

type stalwartDataListItem struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Members int    `json:"members"`
}

type Service struct {
	baseURL     string
	username    string
	password    string
	apiKey      string // Bearer token — preferred over Basic auth when non-empty
	client      *http.Client
	serviceName string
	configPath  string
	binary      string

	// readinessRunner is the context-aware local systemd boundary used by the
	// read-only readiness probe. It is initialized by New and kept injectable
	// so readiness tests never need to call the host's systemctl.
	readinessRunner readinessCommandRunner
}

// New creates a Service using HTTP Basic authentication (admin user + password).
// Empty values leave the optional mail integration unconfigured.
func New(baseURL, username, password string) *Service {
	return &Service{
		baseURL:         strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		username:        strings.TrimSpace(username),
		password:        password,
		client:          &http.Client{Timeout: httpTimeout},
		readinessRunner: execReadinessCommandRunner{},
	}
}

// WithRuntime configures installation-owned Stalwart runtime locations.
// Empty values leave the current optional runtime value unchanged.
func (s *Service) WithRuntime(serviceName, configPath, binary string) *Service {
	if value := strings.TrimSpace(serviceName); value != "" {
		s.serviceName = value
	}
	if value := strings.TrimSpace(configPath); value != "" {
		if filepath.IsAbs(value) && filepath.Clean(value) == value {
			s.configPath = value
		} else {
			s.configPath = ""
		}
	}
	if value := strings.TrimSpace(binary); value != "" {
		if filepath.IsAbs(value) {
			if filepath.Clean(value) == value {
				s.binary = value
			} else {
				s.binary = ""
			}
		} else {
			s.binary = value
		}
	}
	return s
}

// NewWithAPIKey creates a Service that authenticates via Bearer token.
// Falls back to Basic auth (username/password) when apiKey is empty.
func NewWithAPIKey(baseURL, apiKey, username, password string) *Service {
	svc := New(baseURL, username, password)
	svc.apiKey = apiKey
	return svc
}

// setAuth attaches the correct Authorization header to r.
// Bearer token takes priority; Basic auth is used as fallback.
func (s *Service) setAuth(r *http.Request) {
	if s.apiKey != "" {
		r.Header.Set("Authorization", "Bearer "+s.apiKey)
		return
	}
	r.SetBasicAuth(s.username, s.password)
}

func notConfigured(setting string) error {
	return fmt.Errorf("%w: %s is not configured", ErrNotConfigured, setting)
}

func (s *Service) requireBaseURL() error {
	if s == nil || strings.TrimSpace(s.baseURL) == "" {
		return notConfigured("mail API base URL")
	}
	return nil
}

func (s *Service) requireServiceName() error {
	if s == nil || strings.TrimSpace(s.serviceName) == "" {
		return notConfigured("mail service name")
	}
	return nil
}

func (s *Service) requireConfigPath() error {
	if s == nil || strings.TrimSpace(s.configPath) == "" {
		return notConfigured("mail config path")
	}
	return nil
}

func (s *Service) requireBinary() error {
	if s == nil || strings.TrimSpace(s.binary) == "" {
		return notConfigured("mail binary")
	}
	return nil
}

func (s *Service) GetStatus() ServiceStatus {
	if err := s.requireServiceName(); err != nil {
		return ServiceStatus{Status: "not_configured"}
	}
	out, err := exec.Command("systemctl", "is-active", s.serviceName).Output()
	state := strings.TrimSpace(string(out))
	if err != nil && state == "" {
		state = "unknown"
	}
	status := ServiceStatus{Running: state == "active", Status: statusLabel(state)}
	show, err := exec.Command("systemctl", "show", s.serviceName,
		"--property=MainPID,ActiveEnterTimestamp").Output()
	if err == nil {
		for _, line := range strings.Split(string(show), "\n") {
			kv := strings.SplitN(line, "=", 2)
			if len(kv) != 2 {
				continue
			}
			switch kv[0] {
			case "MainPID":
				if kv[1] != "0" {
					status.PID = kv[1]
				}
			case "ActiveEnterTimestamp":
				if kv[1] != "" {
					status.Uptime = kv[1]
				}
			}
		}
	}
	return status
}

func statusLabel(state string) string {
	switch state {
	case "active":
		return "running"
	case "inactive":
		return "stopped"
	case "failed":
		return "failed"
	default:
		return "unknown"
	}
}

func (s *Service) ListDomains() ([]MailDomain, error) {
	var resp stalwartDataListResponse
	if err := s.get("/api/principal?type=domain&limit=500", &resp); err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	domains := make([]MailDomain, 0, len(resp.Data.Items))
	for _, item := range resp.Data.Items {
		domains = append(domains, MailDomain{Name: item.Name, Description: fmt.Sprintf("%d accounts", item.Members)})
	}
	return domains, nil
}

func (s *Service) ListAccounts(domain string) ([]MailAccount, error) {
	var resp stalwartDataListResponse
	if err := s.get("/api/principal?type=individual&limit=1000", &resp); err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	accounts := make([]MailAccount, 0)
	for _, item := range resp.Data.Items {
		var p stalwartPrincipal
		if err := s.get("/api/principal/"+item.Name, &p); err != nil {
			continue
		}
		acc := principalToAccount(item.Name, &p)
		if domain == "" || acc.Domain == domain {
			accounts = append(accounts, acc)
		}
	}
	return accounts, nil
}

func (s *Service) GetAccount(email string) (*MailAccount, error) {
	var p stalwartPrincipal
	if err := s.get("/api/principal/"+email, &p); err != nil {
		return nil, fmt.Errorf("get account %s: %w", email, err)
	}
	acc := principalToAccount(email, &p)
	return &acc, nil
}

func (s *Service) CreateAccount(req CreateAccountRequest) error {
	if req.Email == "" || req.Password == "" {
		return fmt.Errorf("email and password are required")
	}
	enabled := true
	p := stalwartPrincipal{
		Name:        req.Email,
		Type:        "individual",
		Secrets:     []string{req.Password},
		Emails:      []string{req.Email},
		Quota:       req.Quota,
		Description: req.Name,
		Enabled:     &enabled,
	}
	if err := s.post("/api/principal", p, nil); err != nil {
		return fmt.Errorf("create account %s: %w", req.Email, err)
	}
	return nil
}

func (s *Service) DeleteAccount(email string) error {
	if err := s.delete("/api/principal/" + email); err != nil {
		return fmt.Errorf("delete account %s: %w", email, err)
	}
	return nil
}

func (s *Service) UpdatePassword(email, newPassword string) error {
	if email == "" || newPassword == "" {
		return fmt.Errorf("email and password are required")
	}
	// Stalwart PATCH uses array of operations: [{"action":"set","field":"secrets","value":["pass"]}]
	ops := []map[string]interface{}{
		{"action": "set", "field": "secrets", "value": []string{newPassword}},
	}
	if err := s.patch("/api/principal/"+email, ops, nil); err != nil {
		return fmt.Errorf("update password for %s: %w", email, err)
	}
	return nil
}

// GetPassword returns the plaintext password (secret) for a mail account.
// Stalwart stores secrets in the principal's "secrets" array.
func (s *Service) GetPassword(email string) (string, error) {
	var resp stalwartDataResponse
	if err := s.get("/api/principal/"+email, &resp); err != nil {
		return "", fmt.Errorf("get account %s: %w", email, err)
	}
	if len(resp.Data.Secrets) > 0 {
		return resp.Data.Secrets[0], nil
	}
	return "", nil
}

type stalwartDataResponse struct {
	Data stalwartPrincipal `json:"data"`
}

// CheckDNS runs a comprehensive DNS health check for the given domain.
// It delegates to CheckAll defined in dns_check.go.
// An optional serverIP may be passed to also verify the PTR / rDNS record.
func (s *Service) CheckDNS(domain string, serverIP ...string) DNSHealthReport {
	return CheckAll(domain, serverIP...)
}

func principalToAccount(name string, p *stalwartPrincipal) MailAccount {
	email := primaryEmail(p.Emails)
	if email == "" {
		email = name
	}
	domain := ""
	if at := strings.LastIndex(email, "@"); at > 0 {
		domain = email[at+1:]
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	return MailAccount{
		Email:     email,
		Name:      p.Description,
		Domain:    domain,
		Quota:     p.Quota,
		IsEnabled: enabled,
		Aliases:   p.Emails,
	}
}

func primaryEmail(emails []string) string {
	if len(emails) == 0 {
		return ""
	}
	return emails[0]
}

func (s *Service) get(path string, out interface{}) error {
	return s.getContext(context.Background(), path, out)
}

// getContext is the context-aware HTTP read boundary used by the readiness
// probe and by the legacy background-context helper above. Keeping the
// request construction here ensures a caller deadline reaches the actual
// transport instead of only bounding the caller-side wait.
func (s *Service) getContext(ctx context.Context, path string, out interface{}) error {
	return s.getContextWithClient(ctx, path, out, s.client)
}

// getContextWithClient performs one context-aware GET with the supplied
// client. The legacy helper keeps the client's normal redirect policy; the
// readiness seam passes a client with redirects disabled.
func (s *Service) getContextWithClient(ctx context.Context, path string, out interface{}, client *http.Client) error {
	if err := s.requireBaseURL(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	s.setAuth(req)
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (s *Service) post(path string, body interface{}, out interface{}) error {
	return s.doWithBody(http.MethodPost, path, body, out)
}

func (s *Service) patch(path string, body interface{}, out interface{}) error {
	return s.doWithBody(http.MethodPatch, path, body, out)
}

func (s *Service) delete(path string) error {
	if err := s.requireBaseURL(); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodDelete, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	s.setAuth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *Service) doWithBody(method, path string, body interface{}, out interface{}) error {
	if err := s.requireBaseURL(); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return err
	}
	req, err := http.NewRequest(method, s.baseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	s.setAuth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
