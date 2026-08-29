package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/db"
	phpsvc "github.com/IamYGT/heyserver/internal/services/php"
)

type fakePHPVersionActions struct {
	action string
	err    error
}

func (f *fakePHPVersionActions) TestFPM(string) error {
	f.action = "test"
	return f.err
}

func (f *fakePHPVersionActions) ReloadFPM(string) error {
	f.action = "reload"
	return f.err
}

func (f *fakePHPVersionActions) RestartFPM(string) error {
	f.action = "restart"
	return f.err
}

func TestLocalPHPVersionActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		action  string
		message string
	}{
		{action: "test", message: "PHP-FPM configuration is valid"},
		{action: "reload", message: "php8.4-fpm reloaded after configuration validation"},
		{action: "restart", message: "php8.4-fpm restarted after configuration validation"},
	}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			service := &fakePHPVersionActions{}
			request := httptest.NewRequest(http.MethodPost, "/api/php/versions/8.4/actions/"+test.action, nil)
			request.SetPathValue("version", "8.4")
			request.SetPathValue("action", test.action)
			recorder := httptest.NewRecorder()

			handlePHPVersionAction(service).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if service.action != test.action {
				t.Fatalf("called action = %q, want %q", service.action, test.action)
			}
			if !strings.Contains(recorder.Body.String(), test.message) {
				t.Fatalf("body = %s, want message %q", recorder.Body.String(), test.message)
			}
		})
	}
}

func TestLocalPHPVersionActionErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		version string
		action  string
		err     error
		status  int
	}{
		{name: "invalid version", version: "latest", action: "test", status: http.StatusBadRequest},
		{name: "invalid action", version: "8.4", action: "stop", status: http.StatusBadRequest},
		{name: "invalid configuration", version: "8.4", action: "test", err: phpsvc.ErrFPMConfigInvalid, status: http.StatusUnprocessableEntity},
		{name: "systemd failure", version: "8.4", action: "reload", err: phpsvc.ErrFPMLifecycleAction, status: http.StatusBadGateway},
		{name: "unexpected failure", version: "8.4", action: "restart", err: errors.New("unexpected"), status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakePHPVersionActions{err: test.err}
			request := httptest.NewRequest(http.MethodPost, "/api/php/versions/"+test.version+"/actions/"+test.action, nil)
			request.SetPathValue("version", test.version)
			request.SetPathValue("action", test.action)
			recorder := httptest.NewRecorder()

			handlePHPVersionAction(service).ServeHTTP(recorder, request)

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}
}

func TestLocalPHPVersionRestartAliasUsesValidatedAction(t *testing.T) {
	t.Parallel()
	service := &fakePHPVersionActions{}
	request := httptest.NewRequest(http.MethodPost, "/api/php/restart/8.4", nil)
	request.SetPathValue("version", "8.4")
	recorder := httptest.NewRecorder()

	handlePHPVersionRestart(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if service.action != "restart" {
		t.Fatalf("called action = %q, want restart", service.action)
	}
	if !strings.Contains(recorder.Body.String(), "after configuration validation") {
		t.Fatalf("body = %s, want validated restart receipt", recorder.Body.String())
	}
}

func TestLocalPHPVersionActionAuditsSuccessAndFailure(t *testing.T) {
	successService := &fakePHPVersionActions{}
	successRequest := requestWithAuditUser(http.MethodPost, "/api/php/versions/8.4/actions/test")
	successRequest.SetPathValue("version", "8.4")
	successRequest.SetPathValue("action", "test")
	successRecorder := httptest.NewRecorder()
	handlePHPVersionAction(successService).ServeHTTP(successRecorder, successRequest)
	if successRecorder.Code != http.StatusOK {
		t.Fatalf("success status = %d; body=%s", successRecorder.Code, successRecorder.Body.String())
	}
	entry := latestAuditForAction(t, "local_php_fpm_action")
	if entry.Details != "PHP 8.4 test" {
		t.Fatalf("success audit details = %q", entry.Details)
	}

	failureService := &fakePHPVersionActions{err: phpsvc.ErrFPMLifecycleAction}
	failureRequest := requestWithAuditUser(http.MethodPost, "/api/php/versions/8.4/actions/reload")
	failureRequest.SetPathValue("version", "8.4")
	failureRequest.SetPathValue("action", "reload")
	failureRecorder := httptest.NewRecorder()
	handlePHPVersionAction(failureService).ServeHTTP(failureRecorder, failureRequest)
	if failureRecorder.Code != http.StatusBadGateway {
		t.Fatalf("failure status = %d; body=%s", failureRecorder.Code, failureRecorder.Body.String())
	}
	entries, _, err := db.NewAuditRepository(db.Instance()).List(db.AuditFilter{Action: "local_php_fpm_action", Resource: "system"}, 50, 0)
	if err != nil {
		t.Fatalf("list action audits: %v", err)
	}
	wantFailure := "PHP 8.4 reload failed: PHP-FPM lifecycle action failed"
	foundFailure := false
	for _, item := range entries {
		if item.Details == wantFailure {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Fatalf("failure audit %q not found in %#v", wantFailure, entries)
	}
}
