package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/integrationstate"
)

func TestHandleCFZoneListReportsNotConfiguredStateAndKeepsDetail(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/cloudflare/zones", nil)
	handleCFZoneList(&config.Config{})(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	if got := recorder.Header().Get("X-HServer-Integration-State"); got != string(integrationstate.NotConfigured) {
		t.Fatalf("state header = %q, want %q", got, integrationstate.NotConfigured)
	}
	var response struct {
		State integrationstate.State `json:"state"`
		Error string                 `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.State != integrationstate.NotConfigured {
		t.Fatalf("state = %q, want %q", response.State, integrationstate.NotConfigured)
	}
	if !strings.Contains(response.Error, "HSERVER_CF_API_TOKEN") {
		t.Fatalf("error = %q, want token configuration detail", response.Error)
	}
}

func TestWriteCloudflareErrorSeparatesUnavailableStateFromProviderDetail(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writeCloudflareError(recorder, http.StatusBadGateway, integrationstate.Unavailable, errors.New("HTTP 403 from Cloudflare"))

	var response struct {
		State integrationstate.State `json:"state"`
		Error string                 `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.State != integrationstate.Unavailable {
		t.Fatalf("state = %q, want %q", response.State, integrationstate.Unavailable)
	}
	if response.Error != "HTTP 403 from Cloudflare" {
		t.Fatalf("error = %q, want provider detail", response.Error)
	}
}

func TestHandleCFMailAutoFixReportsNotConfigured(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{CloudflareAPIToken: "test-token"}
	req := httptest.NewRequest(http.MethodPost, "/api/cloudflare/mail-autofix/example.com", nil)
	req.SetPathValue("domain", "example.com")
	rec := httptest.NewRecorder()

	handleCFMailAutoFix(cfg)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "HSERVER_MAIL_DNS_HOSTNAME") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestHandleCFRecordMutationsRejectStrictInvalidContracts(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{CloudflareAPIToken: "test-token"}
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		pathValue map[string]string
		handler   http.HandlerFunc
		want      string
	}{
		{
			name: "create unknown field", method: http.MethodPost, path: "/api/cloudflare/zones/zone/records",
			body:      `{"type":"A","name":"@","content":"192.0.2.1","ttl":1,"proxied":false,"extra":true}`,
			pathValue: map[string]string{"zoneId": "zone"}, handler: handleCFRecordCreate(cfg), want: "unknown field",
		},
		{
			name: "create trailing JSON", method: http.MethodPost, path: "/api/cloudflare/zones/zone/records",
			body:      `{"type":"A","name":"@","content":"192.0.2.1","ttl":1,"proxied":false}{}`,
			pathValue: map[string]string{"zoneId": "zone"}, handler: handleCFRecordCreate(cfg), want: "trailing JSON",
		},
		{
			name: "create requires proxy decision", method: http.MethodPost, path: "/api/cloudflare/zones/zone/records",
			body:      `{"type":"A","name":"@","content":"192.0.2.1","ttl":1}`,
			pathValue: map[string]string{"zoneId": "zone"}, handler: handleCFRecordCreate(cfg), want: "proxied is required",
		},
		{
			name: "update validates full record", method: http.MethodPut, path: "/api/cloudflare/zones/zone/records/record",
			body:      `{"type":"MX","name":"@","content":"mail.example.com","ttl":1,"proxied":true}`,
			pathValue: map[string]string{"zoneId": "zone", "recordId": "record"}, handler: handleCFRecordUpdate(cfg), want: "only for A, AAAA, or CNAME",
		},
		{
			name: "proxy requires field", method: http.MethodPut, path: "/api/cloudflare/zones/zone/records/record/proxy",
			body:      `{}`,
			pathValue: map[string]string{"zoneId": "zone", "recordId": "record"}, handler: handleCFRecordToggleProxy(cfg), want: "proxied is required",
		},
		{
			name: "delete requires empty body", method: http.MethodDelete, path: "/api/cloudflare/zones/zone/records/record",
			body:      `{"force":true}`,
			pathValue: map[string]string{"zoneId": "zone", "recordId": "record"}, handler: handleCFRecordDelete(cfg), want: "must be empty",
		},
		{
			name: "purge requires empty body", method: http.MethodPost, path: "/api/cloudflare/zones/zone/purge",
			body:      `{"files":["https://example.com/"]}`,
			pathValue: map[string]string{"zoneId": "zone"}, handler: handleCFPurgeCache(cfg), want: "must be empty",
		},
		{
			name: "mail domain", method: http.MethodPost, path: "/api/cloudflare/mail-autofix/localhost",
			pathValue: map[string]string{"domain": "localhost"}, handler: handleCFMailAutoFix(cfg), want: "at least one dot",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			for name, value := range test.pathValue {
				request.SetPathValue(name, value)
			}
			recorder := httptest.NewRecorder()
			test.handler(recorder, request)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), test.want) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestHandleCFRecordListRejectsInvalidFiltersBeforeProviderAccess(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{CloudflareAPIToken: "test-token"}
	for _, target := range []string{
		"/api/cloudflare/zones/zone/records?search=www",
		"/api/cloudflare/zones/zone/records?type=A/AAAA",
		"/api/cloudflare/zones/zone/records?type=A&type=AAAA",
		"/api/cloudflare/zones/zone/records?name=",
		"/api/cloudflare/zones/zone/records?name=a&name=b",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.SetPathValue("zoneId", "zone")
		recorder := httptest.NewRecorder()
		handleCFRecordList(cfg)(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("target=%s status=%d body=%s", target, recorder.Code, recorder.Body.String())
		}
	}
}
