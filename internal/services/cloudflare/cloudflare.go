// Package cloudflare provides a client for the Cloudflare v4 API.
// Authentication uses a Bearer token supplied via the HSERVER_CF_API_TOKEN
// environment variable and passed in at construction time.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const (
	baseURL     = "https://api.cloudflare.com/client/v4"
	httpTimeout = 15 * time.Second
)

// ErrNotConfigured identifies a missing installation-owned Cloudflare token.
// Callers should keep this distinct from provider or transport failures.
var ErrNotConfigured = errors.New("Cloudflare API token not configured (HSERVER_CF_API_TOKEN)")

// --- Response envelope -------------------------------------------------------

type cfResponse[T any] struct {
	Success bool      `json:"success"`
	Errors  []cfError `json:"errors"`
	Result  T         `json:"result"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- Public types ------------------------------------------------------------

// CFZone represents a Cloudflare zone (domain).
type CFZone struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Plan   CFPlan   `json:"plan"`
	NS     []string `json:"name_servers"`
}

type CFPlan struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CFZoneInventory is the typed result of an explicit Cloudflare zone probe.
// The state is healthy only after the provider returned a successful zone
// response; an empty Zones slice is therefore a healthy, observed inventory.
type CFZoneInventory struct {
	Zones []CFZone               `json:"zones"`
	State integrationstate.State `json:"state"`
}

// CFRecord represents a DNS record within a zone.
type CFRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority int    `json:"priority,omitempty"`
}

// CFEmailRouting represents the email routing configuration for a zone.
type CFEmailRouting struct {
	Tag      string `json:"tag"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Created  string `json:"created,omitempty"`
	Modified string `json:"modified,omitempty"`
	Status   string `json:"status,omitempty"`
}

// CreateRecordRequest is the payload for creating a new DNS record.
type CreateRecordRequest struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority int    `json:"priority,omitempty"`
}

// UpdateRecordRequest is the payload for updating an existing DNS record.
type UpdateRecordRequest = CreateRecordRequest

// ToggleProxyRequest is the minimal PATCH payload for toggling proxy status.
type toggleProxyRequest struct {
	Proxied bool `json:"proxied"`
}

// --- Service -----------------------------------------------------------------

// Service wraps the Cloudflare v4 API.
// Supports both API Token (Bearer) and Global API Key (X-Auth-Key) auth.
type Service struct {
	token   string
	email   string // non-empty → use Global API Key auth
	client  *http.Client
	baseURL string
	mailDNS MailDNSConfig
}

// New returns a Service. If email is non-empty, Global API Key auth is used
// (X-Auth-Key + X-Auth-Email). Otherwise, Bearer token auth is used.
func New(token, email string) *Service {
	return NewWithMailDNS(token, email, MailDNSConfig{})
}

// NewWithMailDNS returns a Service with an explicit, installation-owned mail
// DNS contract. Empty mail DNS configuration keeps ordinary Cloudflare DNS
// operations available while AutoFixMailDNS reports that it is not configured.
func NewWithMailDNS(token, email string, mailDNS MailDNSConfig) *Service {
	return &Service{
		token:   token,
		email:   email,
		client:  &http.Client{Timeout: httpTimeout},
		baseURL: baseURL,
		mailDNS: mailDNS.normalized(),
	}
}

// --- Zones -------------------------------------------------------------------

// ListZones returns all zones accessible to the API token.
func (s *Service) ListZones() ([]CFZone, error) {
	return s.ListZonesContext(context.Background())
}

// ListZonesContext returns all zones accessible to the API token while
// honoring ctx.  It is the read-only provider operation used by the local
// integration-status aggregate.
func (s *Service) ListZonesContext(ctx context.Context) ([]CFZone, error) {
	var result []CFZone
	if err := s.getContext(ctx, "/zones", nil, &result); err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	return result, nil
}

// ProbeZones performs the read-only provider probe used to classify the
// optional Cloudflare integration. It deliberately does not infer health from
// a configured token or endpoint: only a successful provider response is
// healthy. A successful empty result is normalized to an empty JSON array.
func (s *Service) ProbeZones() (CFZoneInventory, error) {
	return s.ProbeZonesContext(context.Background())
}

// ProbeZonesContext performs one fresh read-only zone observation and honors
// the caller's cancellation/deadline.  A successful empty zone list remains
// healthy; token presence alone never is.
func (s *Service) ProbeZonesContext(ctx context.Context) (CFZoneInventory, error) {
	inventory := CFZoneInventory{
		Zones: []CFZone{},
		State: integrationstate.FromObservation(integrationstate.Observation{Configured: true}),
	}
	if s == nil || strings.TrimSpace(s.token) == "" {
		inventory.State = integrationstate.FromObservation(integrationstate.Observation{})
		return inventory, ErrNotConfigured
	}
	if s.client == nil {
		return inventory, errors.New("cloudflare HTTP client is unavailable")
	}

	zones, err := s.ListZonesContext(ctx)
	if err != nil {
		return inventory, err
	}
	if zones == nil {
		zones = []CFZone{}
	}
	inventory.Zones = zones
	inventory.State = integrationstate.FromObservation(integrationstate.Observation{Configured: true, Successful: true})
	return inventory, nil
}

// GetZone returns a single zone by its ID.
func (s *Service) GetZone(zoneID string) (*CFZone, error) {
	if zoneID == "" {
		return nil, fmt.Errorf("zoneID is required")
	}
	var result CFZone
	if err := s.get("/zones/"+zoneID, nil, &result); err != nil {
		return nil, fmt.Errorf("get zone %s: %w", zoneID, err)
	}
	return &result, nil
}

// --- DNS Records -------------------------------------------------------------

// ListRecords returns DNS records for the given zone.
// Pass empty strings for recordType and name to omit those filters.
func (s *Service) ListRecords(zoneID, recordType, name string) ([]CFRecord, error) {
	if zoneID == "" {
		return nil, fmt.Errorf("zoneID is required")
	}
	params := url.Values{}
	if recordType != "" {
		params.Set("type", recordType)
	}
	if name != "" {
		params.Set("name", name)
	}
	var result []CFRecord
	if err := s.get("/zones/"+zoneID+"/dns_records", params, &result); err != nil {
		return nil, fmt.Errorf("list records zone %s: %w", zoneID, err)
	}
	return result, nil
}

// CreateRecord adds a new DNS record to the given zone.
func (s *Service) CreateRecord(zoneID string, record CreateRecordRequest) (*CFRecord, error) {
	if zoneID == "" {
		return nil, fmt.Errorf("zoneID is required")
	}
	record, err := ValidateAndNormalizeRecordRequest(record)
	if err != nil {
		return nil, fmt.Errorf("validate record: %w", err)
	}
	var result CFRecord
	if err := s.post("/zones/"+zoneID+"/dns_records", record, &result); err != nil {
		return nil, fmt.Errorf("create record zone %s: %w", zoneID, err)
	}
	return &result, nil
}

// UpdateRecord replaces an existing DNS record (PUT — full replacement).
func (s *Service) UpdateRecord(zoneID, recordID string, record UpdateRecordRequest) (*CFRecord, error) {
	if zoneID == "" || recordID == "" {
		return nil, fmt.Errorf("zoneID and recordID are required")
	}
	record, err := ValidateAndNormalizeRecordRequest(record)
	if err != nil {
		return nil, fmt.Errorf("validate record: %w", err)
	}
	var result CFRecord
	if err := s.put("/zones/"+zoneID+"/dns_records/"+recordID, record, &result); err != nil {
		return nil, fmt.Errorf("update record %s zone %s: %w", recordID, zoneID, err)
	}
	return &result, nil
}

// DeleteRecord removes a DNS record from the given zone.
func (s *Service) DeleteRecord(zoneID, recordID string) error {
	if zoneID == "" || recordID == "" {
		return fmt.Errorf("zoneID and recordID are required")
	}
	if err := s.delete("/zones/" + zoneID + "/dns_records/" + recordID); err != nil {
		return fmt.Errorf("delete record %s zone %s: %w", recordID, zoneID, err)
	}
	return nil
}

// ToggleProxy patches only the proxied field of an existing DNS record.
func (s *Service) ToggleProxy(zoneID, recordID string, proxied bool) (*CFRecord, error) {
	if zoneID == "" || recordID == "" {
		return nil, fmt.Errorf("zoneID and recordID are required")
	}
	payload := toggleProxyRequest{Proxied: proxied}
	var result CFRecord
	if err := s.patch("/zones/"+zoneID+"/dns_records/"+recordID, payload, &result); err != nil {
		return nil, fmt.Errorf("toggle proxy record %s zone %s: %w", recordID, zoneID, err)
	}
	return &result, nil
}

// --- Cache -------------------------------------------------------------------

// PurgeCache purges ALL cached files for the given zone.
func (s *Service) PurgeCache(zoneID string) error {
	if zoneID == "" {
		return fmt.Errorf("zoneID is required")
	}
	payload := map[string]bool{"purge_everything": true}
	if err := s.post("/zones/"+zoneID+"/purge_cache", payload, nil); err != nil {
		return fmt.Errorf("purge cache zone %s: %w", zoneID, err)
	}
	return nil
}

// --- Email Routing -----------------------------------------------------------

// GetEmailRouting returns the email routing settings for the given zone.
func (s *Service) GetEmailRouting(zoneID string) (*CFEmailRouting, error) {
	if zoneID == "" {
		return nil, fmt.Errorf("zoneID is required")
	}
	var result CFEmailRouting
	if err := s.get("/zones/"+zoneID+"/email/routing", nil, &result); err != nil {
		return nil, fmt.Errorf("get email routing zone %s: %w", zoneID, err)
	}
	return &result, nil
}

// --- HTTP helpers ------------------------------------------------------------

func (s *Service) buildRequest(method, path string, params url.Values, body interface{}) (*http.Request, error) {
	return s.buildRequestContext(context.Background(), method, path, params, body)
}

func (s *Service) buildRequestContext(ctx context.Context, method, path string, params url.Values, body interface{}) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	u := s.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return nil, err
	}
	if s.email != "" {
		req.Header.Set("X-Auth-Key", s.token)
		req.Header.Set("X-Auth-Email", s.email)
	} else {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (s *Service) do(method, path string, params url.Values, body interface{}, result interface{}) error {
	return s.doContext(context.Background(), method, path, params, body, result)
}

func (s *Service) doContext(ctx context.Context, method, path string, params url.Values, body interface{}, result interface{}) error {
	req, err := s.buildRequestContext(ctx, method, path, params, body)
	if err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	// DELETE 200/204 with no body is success
	if method == http.MethodDelete && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent) {
		return nil
	}

	// Decode envelope
	var envelope cfResponse[json.RawMessage]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode CF response (status %d): %w", resp.StatusCode, err)
	}
	if !envelope.Success {
		if len(envelope.Errors) > 0 {
			return fmt.Errorf("cloudflare API error %d: %s", envelope.Errors[0].Code, envelope.Errors[0].Message)
		}
		return fmt.Errorf("cloudflare API returned success=false (status %d)", resp.StatusCode)
	}

	// Caller does not care about the result body
	if result == nil {
		return nil
	}

	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode CF result: %w", err)
	}
	return nil
}

func (s *Service) get(path string, params url.Values, result interface{}) error {
	return s.do(http.MethodGet, path, params, nil, result)
}

func (s *Service) getContext(ctx context.Context, path string, params url.Values, result interface{}) error {
	return s.doContext(ctx, http.MethodGet, path, params, nil, result)
}

func (s *Service) post(path string, body interface{}, result interface{}) error {
	return s.do(http.MethodPost, path, nil, body, result)
}

func (s *Service) put(path string, body interface{}, result interface{}) error {
	return s.do(http.MethodPut, path, nil, body, result)
}

func (s *Service) patch(path string, body interface{}, result interface{}) error {
	return s.do(http.MethodPatch, path, nil, body, result)
}

func (s *Service) delete(path string) error {
	return s.do(http.MethodDelete, path, nil, nil, nil)
}
