package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleOnboardingGet_nilService503(t *testing.T) {
	rec := httptest.NewRecorder()
	handleOnboardingGet(nil)(rec, httptest.NewRequest(http.MethodGet, "/api/onboarding", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleAlertRuleList_nilRepo503(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAlertRuleList(nil)(rec, httptest.NewRequest(http.MethodGet, "/api/notify/rules", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleAlertRuleCreate_nilRepo503(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notify/rules", strings.NewReader(`{}`))
	handleAlertRuleCreate(nil)(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleAlertHistoryList_nilRepo503(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAlertHistoryList(nil)(rec, httptest.NewRequest(http.MethodGet, "/api/notify/history", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleUptimeMonitorList_nilEngine503(t *testing.T) {
	rec := httptest.NewRecorder()
	handleUptimeMonitorList(nil)(rec, httptest.NewRequest(http.MethodGet, "/api/uptime/monitors", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
