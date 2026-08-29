package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamYGT/heyserver/internal/config"
	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

func TestMailErrorStatusMapsOnlyUnconfiguredMailTo503(t *testing.T) {
	wrapped := fmt.Errorf("mail operation failed: %w", mailsvc.ErrNotConfigured)
	for _, tc := range []struct {
		name     string
		err      error
		fallback int
		want     int
	}{
		{name: "wrapped not configured from 502 handler", err: wrapped, fallback: http.StatusBadGateway, want: http.StatusServiceUnavailable},
		{name: "wrapped not configured from 500 handler", err: wrapped, fallback: http.StatusInternalServerError, want: http.StatusServiceUnavailable},
		{name: "provider failure keeps 502 fallback", err: errors.New("provider request failed"), fallback: http.StatusBadGateway, want: http.StatusBadGateway},
		{name: "runtime failure keeps 500 fallback", err: errors.New("runtime command failed"), fallback: http.StatusInternalServerError, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mailErrorStatus(tc.err, tc.fallback); got != tc.want {
				t.Fatalf("mailErrorStatus() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMailHandlersMapUnconfiguredIntegrationTo503(t *testing.T) {
	cfg := &config.Config{}
	tests := []struct {
		name    string
		handler http.HandlerFunc
		request *http.Request
	}{
		{
			name:    "domains",
			handler: handleMailDomains(cfg),
			request: httptest.NewRequest(http.MethodGet, "/api/mail/domains", nil),
		},
		{
			name:    "service config",
			handler: handleMailConfig(cfg),
			request: httptest.NewRequest(http.MethodGet, "/api/mail/config", nil),
		},
		{
			name:    "service action",
			handler: handleMailServiceAction(cfg),
			request: httptest.NewRequest(http.MethodPost, "/api/mail/service/start", nil),
		},
	}
	tests[2].request.SetPathValue("action", "start")

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			tc.handler.ServeHTTP(recorder, tc.request)
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
			}
		})
	}
}

func TestMailServiceOverviewReportsSourceAvailability(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/mail/service/overview", nil)
	recorder := httptest.NewRecorder()

	handleMailServiceOverview(&config.Config{}).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Sources map[string]struct {
			Available bool   `json:"available"`
			State     string `json:"state"`
			Error     string `json:"error"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, source := range []string{"status", "version", "listeners", "storage"} {
		value, ok := payload.Sources[source]
		if !ok {
			t.Fatalf("missing source %q in %#v", source, payload.Sources)
		}
		if !value.Available && value.Error == "" {
			t.Fatalf("unavailable source %q has no error", source)
		}
		if value.State != "not_configured" {
			t.Fatalf("source %q state = %q, want not_configured", source, value.State)
		}
	}
	if payload.Sources["status"].Available {
		t.Fatal("not_configured status source must not be reported as available")
	}
}
