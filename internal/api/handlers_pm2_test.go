package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/integrationstate"
)

func TestPM2ListReturnsNotConfiguredWithoutOwner(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/pm2/processes", nil)
	response := httptest.NewRecorder()

	handlePM2List(&config.Config{PM2Bin: "pm2"}).ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", response.Code, http.StatusServiceUnavailable)
	}
	if body := response.Body.String(); !strings.Contains(body, "HSERVER_PM2_USER") {
		t.Fatalf("body %q does not explain how to configure PM2", body)
	}
	if got := response.Header().Get("X-HServer-Integration-State"); got != string(integrationstate.NotConfigured) {
		t.Fatalf("state header = %q, want %q", got, integrationstate.NotConfigured)
	}
	var body struct {
		State integrationstate.State `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.State != integrationstate.NotConfigured {
		t.Fatalf("state = %q, want %q", body.State, integrationstate.NotConfigured)
	}
}

func installPM2FakeSudo(t *testing.T, output string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "sudo")
	contents := "#!/bin/sh\ncat <<'EOF'\n" + output + "\nEOF\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatalf("write fake sudo: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPM2ListReportsHealthyForSuccessfulEmptyInventory(t *testing.T) {
	installPM2FakeSudo(t, "[]", 0)
	request := httptest.NewRequest(http.MethodGet, "/api/pm2/processes", nil)
	response := httptest.NewRecorder()

	handlePM2List(&config.Config{PM2User: "app", PM2Bin: "pm2"}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := response.Header().Get("X-HServer-Integration-State"); got != string(integrationstate.Healthy) {
		t.Fatalf("state header = %q, want %q", got, integrationstate.Healthy)
	}
	var body []struct{}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body == nil || len(body) != 0 {
		t.Fatalf("processes = %#v, want a non-nil empty array", body)
	}
}

func TestPM2ListReportsConfiguredProbeFailureAsUnavailable(t *testing.T) {
	installPM2FakeSudo(t, "pm2 daemon unavailable", 1)
	request := httptest.NewRequest(http.MethodGet, "/api/pm2/processes", nil)
	response := httptest.NewRecorder()

	handlePM2List(&config.Config{PM2User: "app", PM2Bin: "pm2"}).ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if got := response.Header().Get("X-HServer-Integration-State"); got != string(integrationstate.Unavailable) {
		t.Fatalf("state header = %q, want %q", got, integrationstate.Unavailable)
	}
	var body struct {
		State integrationstate.State `json:"state"`
		Error string                 `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.State != integrationstate.Unavailable {
		t.Fatalf("state = %q, want %q", body.State, integrationstate.Unavailable)
	}
	if !strings.Contains(body.Error, "failed to list pm2 processes") {
		t.Fatalf("error = %q, want inventory failure detail", body.Error)
	}
}

func TestPM2DeployRequiresExactBoundedPayload(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown environment field",
			body: `{"name":"api","script":"/var/www/example.com/server.js","env":{"TOKEN":"value"}}`,
			want: "invalid request body",
		},
		{
			name: "unsupported execution mode",
			body: `{"name":"api","script":"/var/www/example.com/server.js","exec_mode":"shell"}`,
			want: "exec_mode must be fork or cluster",
		},
		{
			name: "excessive instances",
			body: `{"name":"api","script":"/var/www/example.com/server.js","instances":65}`,
			want: "instances must be between 1 and 64",
		},
		{
			name: "unsupported node environment",
			body: `{"name":"api","script":"/var/www/example.com/server.js","node_env":"staging"}`,
			want: "node_env must be production or development",
		},
	}

	cfg := &config.Config{PM2User: "app", PM2Bin: "pm2", PM2AllowedRoots: []string{"/var/www"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/pm2/deploy", bytes.NewBufferString(tt.body))
			response := httptest.NewRecorder()

			handlePM2Deploy(cfg).ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), tt.want) {
				t.Fatalf("response = %d %s, want 400 containing %q", response.Code, response.Body.String(), tt.want)
			}
		})
	}
}

func TestPM2ControlRejectsUnsupportedActionBeforeConfiguration(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/pm2/processes/1/shell", nil)
	request.SetPathValue("id", "1")
	request.SetPathValue("action", "shell")
	response := httptest.NewRecorder()

	handlePM2Control(&config.Config{}).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unsupported PM2 action") {
		t.Fatalf("response = %d %s, want bounded-action rejection", response.Code, response.Body.String())
	}
}

func TestPM2LogsRejectInvalidLineBoundsBeforeConfiguration(t *testing.T) {
	for _, lines := range []string{"invalid", "0", "5001"} {
		t.Run(lines, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/pm2/processes/api/logs?lines="+lines, nil)
			request.SetPathValue("id", "api")
			response := httptest.NewRecorder()

			handlePM2Logs(&config.Config{}).ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "lines must be between 1 and 5000") {
				t.Fatalf("response = %d %s, want bounded lines rejection", response.Code, response.Body.String())
			}
		})
	}
}
