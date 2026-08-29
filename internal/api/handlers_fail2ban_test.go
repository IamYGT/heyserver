package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/security"
)

func TestHandleFail2BanStatusReportsMissingOptionalDependency(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	recorder := httptest.NewRecorder()
	handleFail2BanStatus()(recorder, httptest.NewRequest(http.MethodGet, "/api/security/fail2ban/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var status security.Fail2BanStatus
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.State != security.Fail2BanStateNotInstalled || status.Installed || status.Available {
		t.Fatalf("status = %#v", status)
	}
}

func TestHandleFail2BanMutationUsesReadinessAndValidationStatuses(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	recorder := httptest.NewRecorder()
	handleFail2BanBan()(recorder, httptest.NewRequest(http.MethodPost, "/api/security/fail2ban/ban", strings.NewReader(`{"jail":"sshd","ip":"192.0.2.10"}`)))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handleFail2BanBan()(recorder, httptest.NewRequest(http.MethodPost, "/api/security/fail2ban/ban", strings.NewReader(`{"jail":"bad jail","ip":"192.0.2.10"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid input status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestSecurityScoreDistinguishesOptionalMissingFail2Ban(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	recorder := httptest.NewRecorder()
	handleSecurityScore()(recorder, httptest.NewRequest(http.MethodGet, "/api/security/score", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, check := range body.Checks {
		if check.Name == "Fail2Ban" {
			if check.Status != "warn" || check.Detail != "Not installed (optional)" {
				t.Fatalf("Fail2Ban check = %#v", check)
			}
			return
		}
	}
	t.Fatal("Fail2Ban check is missing")
}
