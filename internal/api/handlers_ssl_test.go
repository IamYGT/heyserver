package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/ssl"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestIntegration_SSLStatusReadiness(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleSSLStatus().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ssl/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/ssl/status status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var status ssl.Status
	testutil.ParseJSON(t, recorder, &status)
	if status.State == "" {
		t.Fatal("SSL status has no structured readiness state")
	}
	if status.Available && (!status.Installed || status.State != ssl.StateHealthy) {
		t.Fatalf("available SSL status is inconsistent: %#v", status)
	}
}

func TestSSLIssueRejectsUnknownChallenge(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ssl/issue", strings.NewReader(`{"domain":"example.com","email":"admin@example.com","challengeType":"manual"}`))
	handleSSLIssue().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/ssl/issue status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
