package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

func TestProbeZonesContextHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
			t.Error("provider request context was not canceled")
		}
	}))
	defer server.Close()

	service := New("token", "")
	service.baseURL = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	inventory, err := service.ProbeZonesContext(ctx)
	if err == nil {
		t.Fatal("ProbeZonesContext() error = nil, want cancellation error")
	}
	if inventory.State != integrationstate.Unavailable {
		t.Fatalf("ProbeZonesContext() state = %q, want %q", inventory.State, integrationstate.Unavailable)
	}
}

func TestTxtCategory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"spf", "v=spf1 mx a ~all", "spf"},
		{"spf uppercase", "  V=SPF1 include:_spf.google.com ~all", "spf"},
		{"dkim v=dkim1", "v=DKIM1; k=rsa; p=abc123", "dkim"},
		{"dkim domainkey", "selector._domainkey IN TXT", "dkim"},
		{"dmarc", "v=DMARC1; p=reject", "dmarc"},
		{"other", "google-site-verification=abc", "other"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := txtCategory(tc.content); got != tc.want {
				t.Fatalf("txtCategory(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

func TestFindExisting(t *testing.T) {
	t.Parallel()

	existing := []CFRecord{
		{ID: "1", Type: "TXT", Name: "example.com", Content: "v=spf1 mx ~all"},
		{ID: "2", Type: "TXT", Name: "_dmarc.example.com", Content: "v=DMARC1; p=reject"},
		{ID: "3", Type: "MX", Name: "example.com", Content: "mail.example.com", Priority: 10},
		{ID: "4", Type: "A", Name: "mail.example.com", Content: "1.2.3.4"},
	}

	tests := []struct {
		name    string
		want    desiredRecord
		wantID  string
		wantNil bool
	}{
		{
			name:   "spf at apex",
			want:   desiredRecord{Type: "TXT", Name: "example.com", Content: "v=spf1 mx a ip4:1.2.3.4 ~all"},
			wantID: "1",
		},
		{
			name:   "dmarc",
			want:   desiredRecord{Type: "TXT", Name: "_dmarc.example.com", Content: "v=DMARC1; p=reject"},
			wantID: "2",
		},
		{
			name:   "mx",
			want:   desiredRecord{Type: "MX", Name: "example.com", Content: "mail.example.com"},
			wantID: "3",
		},
		{
			name:    "txt other category not matched",
			want:    desiredRecord{Type: "TXT", Name: "example.com", Content: "google-site-verification=xyz"},
			wantNil: true,
		},
		{
			name:    "missing A",
			want:    desiredRecord{Type: "A", Name: "www.example.com", Content: "1.2.3.4"},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := findExisting(existing, tc.want)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("findExisting() = %+v, want nil", got)
				}
				return
			}
			if got == nil || got.ID != tc.wantID {
				t.Fatalf("findExisting() = %+v, want ID %q", got, tc.wantID)
			}
		})
	}
}

func TestRecordMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rec  CFRecord
		want desiredRecord
		ok   bool
	}{
		{
			name: "exact match",
			rec:  CFRecord{Content: "v=spf1 mx ~all", Proxied: false},
			want: desiredRecord{Content: "v=spf1 mx ~all", Proxied: false},
			ok:   true,
		},
		{
			name: "trimmed content",
			rec:  CFRecord{Content: "  1.2.3.4  ", Proxied: false},
			want: desiredRecord{Content: "1.2.3.4", Proxied: false},
			ok:   true,
		},
		{
			name: "proxy mismatch",
			rec:  CFRecord{Content: "1.2.3.4", Proxied: true},
			want: desiredRecord{Content: "1.2.3.4", Proxied: false},
			ok:   false,
		},
		{
			name: "content mismatch",
			rec:  CFRecord{Content: "old", Proxied: false},
			want: desiredRecord{Content: "new", Proxied: false},
			ok:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := recordMatches(tc.rec, tc.want); got != tc.ok {
				t.Fatalf("recordMatches() = %v, want %v", got, tc.ok)
			}
		})
	}
}

func TestBuildDesiredRecords(t *testing.T) {
	t.Parallel()

	svc := NewWithMailDNS("token", "", MailDNSConfig{
		Hostname:   "MX.EXAMPLE.NET.",
		PublicIP:   "203.0.113.10",
		MXPriority: 20,
	})
	records := svc.buildDesiredRecords("example.com")

	if len(records) != 3 {
		t.Fatalf("buildDesiredRecords len = %d, want 3", len(records))
	}

	mx := records[0]
	if mx.Type != "MX" || mx.Name != "example.com" || mx.Content != "mx.example.net" || mx.Priority != 20 {
		t.Fatalf("mx record = %+v", mx)
	}

	spf := records[1]
	if spf.Type != "TXT" || spf.Content != "v=spf1 mx ip4:203.0.113.10 ~all" {
		t.Fatalf("spf record = %+v", spf)
	}

	dmarc := records[2]
	if dmarc.Type != "TXT" || dmarc.Name != "_dmarc.example.com" || dmarc.Content != defaultDMARCRecord {
		t.Fatalf("dmarc record = %+v", dmarc)
	}
}

func TestBuildDesiredRecordsPublishesOnlyOwnedMailAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		hostname  string
		publicIP  string
		wantType  string
		wantCount int
	}{
		{name: "same-zone IPv4", hostname: "mail.example.com", publicIP: "203.0.113.10", wantType: "A", wantCount: 4},
		{name: "same-zone IPv6", hostname: "mail.example.com", publicIP: "2001:db8::10", wantType: "AAAA", wantCount: 4},
		{name: "external host", hostname: "mail.example.net", publicIP: "203.0.113.10", wantCount: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := NewWithMailDNS("token", "", MailDNSConfig{Hostname: tc.hostname, PublicIP: tc.publicIP})
			records := svc.buildDesiredRecords("example.com")
			if len(records) != tc.wantCount {
				t.Fatalf("record count = %d, want %d: %+v", len(records), tc.wantCount, records)
			}
			if tc.wantType != "" {
				address := records[len(records)-1]
				if address.Type != tc.wantType || address.Name != tc.hostname || address.Content != tc.publicIP || address.Proxied {
					t.Fatalf("address record = %+v", address)
				}
			}
		})
	}
}

func TestAutoFixMailDNSRequiresInstallationConfig(t *testing.T) {
	t.Parallel()

	_, err := New("token", "").AutoFixMailDNS("example.com")
	if err == nil || !strings.Contains(err.Error(), "HSERVER_MAIL_DNS_HOSTNAME") {
		t.Fatalf("AutoFixMailDNS error = %v", err)
	}
}

func TestProbeZonesReportsNotConfiguredWithoutCallingProvider(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"", "   "} {
		inventory, err := New(token, "").ProbeZones()
		if !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("ProbeZones(%q) error = %v, want ErrNotConfigured", token, err)
		}
		if inventory.State != integrationstate.NotConfigured {
			t.Fatalf("ProbeZones(%q) state = %q, want %q", token, inventory.State, integrationstate.NotConfigured)
		}
		if inventory.Zones == nil {
			t.Fatalf("ProbeZones(%q) zones = nil, want an empty array", token)
		}
	}
}

func TestProbeZonesTreatsSuccessfulEmptyInventoryAsHealthy(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones" {
			t.Fatalf("provider path = %q, want /zones", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(cfResponse[json.RawMessage]{
			Success: true,
			Result:  json.RawMessage(`[]`),
		})
	}))
	defer server.Close()

	svc := New("token", "")
	svc.baseURL = server.URL
	inventory, err := svc.ProbeZones()
	if err != nil {
		t.Fatalf("ProbeZones() error = %v", err)
	}
	if inventory.State != integrationstate.Healthy {
		t.Fatalf("ProbeZones() state = %q, want %q", inventory.State, integrationstate.Healthy)
	}
	if inventory.Zones == nil || len(inventory.Zones) != 0 {
		t.Fatalf("ProbeZones() zones = %#v, want an empty array", inventory.Zones)
	}
}

func TestProbeZonesReportsProviderFailureAsUnavailableWithDetail(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(cfResponse[json.RawMessage]{
			Success: false,
			Errors:  []cfError{{Code: 9109, Message: "token cannot access zones"}},
		})
	}))
	defer server.Close()

	svc := New("token", "")
	svc.baseURL = server.URL
	inventory, err := svc.ProbeZones()
	if err == nil || !strings.Contains(err.Error(), "9109") || !strings.Contains(err.Error(), "token cannot access zones") {
		t.Fatalf("ProbeZones() error = %v, want provider detail", err)
	}
	if inventory.State != integrationstate.Unavailable {
		t.Fatalf("ProbeZones() state = %q, want %q", inventory.State, integrationstate.Unavailable)
	}
}

func TestMailDNSConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  MailDNSConfig
		want string
	}{
		{name: "missing hostname", cfg: MailDNSConfig{}, want: "HSERVER_MAIL_DNS_HOSTNAME"},
		{name: "invalid hostname", cfg: MailDNSConfig{Hostname: "https://mail.example.com"}, want: "HSERVER_MAIL_DNS_HOSTNAME"},
		{name: "invalid IP", cfg: MailDNSConfig{Hostname: "mail.example.com", PublicIP: "not-an-ip"}, want: "HSERVER_MAIL_DNS_PUBLIC_IP"},
		{name: "invalid priority", cfg: MailDNSConfig{Hostname: "mail.example.com", MXPriority: 70000}, want: "HSERVER_MAIL_DNS_MX_PRIORITY"},
		{name: "invalid SPF", cfg: MailDNSConfig{Hostname: "mail.example.com", SPFRecord: "include:example.com"}, want: "HSERVER_MAIL_DNS_SPF"},
		{name: "invalid DMARC", cfg: MailDNSConfig{Hostname: "mail.example.com", DMARCRecord: "p=reject"}, want: "HSERVER_MAIL_DNS_DMARC"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.cfg.normalized().validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate() = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestBuildRequest_AuthHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		token     string
		email     string
		wantAuth  string
		wantKey   string
		wantEmail string
	}{
		{
			name:     "bearer token",
			token:    "cf-token-abc",
			email:    "",
			wantAuth: "Bearer cf-token-abc",
		},
		{
			name:      "global api key",
			token:     "global-key",
			email:     "admin@example.com",
			wantKey:   "global-key",
			wantEmail: "admin@example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := New(tc.token, tc.email)
			req, err := svc.buildRequest(http.MethodGet, "/zones", nil, nil)
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}
			if tc.wantAuth != "" {
				if got := req.Header.Get("Authorization"); got != tc.wantAuth {
					t.Fatalf("Authorization = %q, want %q", got, tc.wantAuth)
				}
			}
			if tc.wantKey != "" {
				if got := req.Header.Get("X-Auth-Key"); got != tc.wantKey {
					t.Fatalf("X-Auth-Key = %q, want %q", got, tc.wantKey)
				}
				if got := req.Header.Get("X-Auth-Email"); got != tc.wantEmail {
					t.Fatalf("X-Auth-Email = %q, want %q", got, tc.wantEmail)
				}
			}
			if req.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", req.Header.Get("Content-Type"))
			}
		})
	}
}

func TestResolveZoneIDForDomain(t *testing.T) {
	t.Parallel()

	zoneMap := map[string]string{
		"example.com":     "zone-root",
		"api.example.com": "zone-sub",
	}

	tests := []struct {
		name   string
		domain string
		wantID string
		found  bool
	}{
		{"exact apex", "example.com", "zone-root", true},
		{"subdomain walks to apex", "www.example.com", "zone-root", true},
		{"nested under sub zone", "deep.api.example.com", "zone-sub", true},
		{"direct sub zone", "api.example.com", "zone-sub", true},
		{"trailing dot", "example.com.", "zone-root", true},
		{"unknown", "nowhere.test", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := resolveZoneIDForDomain(tc.domain, zoneMap)
			if ok != tc.found {
				t.Fatalf("resolveZoneIDForDomain(%q) found=%v, want %v", tc.domain, ok, tc.found)
			}
			if got != tc.wantID {
				t.Fatalf("resolveZoneIDForDomain(%q) = %q, want %q", tc.domain, got, tc.wantID)
			}
		})
	}
}

// resolveZoneIDForDomain mirrors the hierarchy walk in findZoneForDomain.
func resolveZoneIDForDomain(domain string, zoneMap map[string]string) (string, bool) {
	candidate := strings.TrimSuffix(domain, ".")
	for {
		if id, ok := zoneMap[candidate]; ok {
			return id, true
		}
		dot := strings.Index(candidate, ".")
		if dot == -1 {
			break
		}
		candidate = candidate[dot+1:]
	}
	return "", false
}

func TestServiceDo_WithMockClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/client/v4/zones/bad":
			_ = json.NewEncoder(w).Encode(cfResponse[json.RawMessage]{
				Success: false,
				Errors:  []cfError{{Code: 1003, Message: "invalid zone identifier"}},
			})
		default:
			body, _ := io.ReadAll(r.Body)
			if len(body) > 0 {
				t.Fatalf("GET should not send body, got %q", body)
			}
			_ = json.NewEncoder(w).Encode(cfResponse[json.RawMessage]{
				Success: true,
				Result:  json.RawMessage(`{"id":"z1","name":"example.com","status":"active"}`),
			})
		}
	}))
	defer server.Close()

	svc := &Service{
		token:  "token",
		client: server.Client(),
	}

	tests := []struct {
		name    string
		run     func() error
		wantErr string
	}{
		{
			name: "api error envelope",
			run: func() error {
				req, err := http.NewRequest(http.MethodGet, server.URL+"/client/v4/zones/bad", nil)
				if err != nil {
					return err
				}
				req.Header.Set("Authorization", "Bearer token")
				resp, err := svc.client.Do(req)
				if err != nil {
					return err
				}
				defer func() { _ = resp.Body.Close() }()
				raw, err := io.ReadAll(resp.Body)
				if err != nil {
					return err
				}
				var envelope cfResponse[json.RawMessage]
				if err := json.Unmarshal(raw, &envelope); err != nil {
					return err
				}
				if envelope.Success {
					return nil
				}
				return fmt.Errorf("cloudflare API error %d: %s", envelope.Errors[0].Code, envelope.Errors[0].Message)
			},
			wantErr: "1003",
		},
		{
			name: "delete no content",
			run: func() error {
				req, err := http.NewRequest(http.MethodDelete, server.URL+"/client/v4/zones/z1/dns_records/r1", nil)
				if err != nil {
					return err
				}
				resp, err := svc.client.Do(req)
				if err != nil {
					return err
				}
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != http.StatusNoContent {
					return fmt.Errorf("status %d", resp.StatusCode)
				}
				return nil
			},
		},
		{
			name: "decode zone result",
			run: func() error {
				req, err := http.NewRequest(http.MethodGet, server.URL+"/client/v4/zones/z1", nil)
				if err != nil {
					return err
				}
				resp, err := svc.client.Do(req)
				if err != nil {
					return err
				}
				defer func() { _ = resp.Body.Close() }()
				raw, err := io.ReadAll(resp.Body)
				if err != nil {
					return err
				}
				var envelope cfResponse[CFZone]
				if err := json.Unmarshal(raw, &envelope); err != nil {
					return err
				}
				if !envelope.Success || envelope.Result.ID != "z1" {
					return fmt.Errorf("unexpected result: %+v", envelope.Result)
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestServiceValidation_EmptyZoneID(t *testing.T) {
	t.Parallel()

	svc := New("token", "")

	tests := []struct {
		name string
		run  func() error
	}{
		{"GetZone", func() error { _, err := svc.GetZone(""); return err }},
		{"ListRecords", func() error { _, err := svc.ListRecords("", "", ""); return err }},
		{"CreateRecord", func() error { _, err := svc.CreateRecord("", CreateRecordRequest{}); return err }},
		{"UpdateRecord", func() error { _, err := svc.UpdateRecord("", "", UpdateRecordRequest{}); return err }},
		{"DeleteRecord", func() error { return svc.DeleteRecord("", "") }},
		{"ToggleProxy", func() error { _, err := svc.ToggleProxy("", "", true); return err }},
		{"PurgeCache", func() error { return svc.PurgeCache("") }},
		{"GetEmailRouting", func() error { _, err := svc.GetEmailRouting(""); return err }},
		{"AutoFixMailDNS", func() error { _, err := svc.AutoFixMailDNS(""); return err }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.run(); err == nil {
				t.Fatalf("%s expected validation error", tc.name)
			}
		})
	}
}
