package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleDiskUsageRequiresPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/disk/usage", nil)
	rec := httptest.NewRecorder()

	handleDiskUsage().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "path is required") {
		t.Fatalf("body = %q, want path requirement", rec.Body.String())
	}
}

func TestHandleDiskLargestRequiresPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/disk/largest", nil)
	rec := httptest.NewRecorder()

	handleDiskLargest().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "path is required") {
		t.Fatalf("body = %q, want path requirement", rec.Body.String())
	}
}

func TestHandleDiskSmartRequiresExplicitDevice(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/disk/smart/", nil)
	rec := httptest.NewRecorder()

	handleDiskSmart().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "disk device is required") {
		t.Fatalf("body = %q, want device requirement", rec.Body.String())
	}
}

func TestHandleDiskCleanupExecuteRejectsUnknownFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/disk/cleanup/execute", strings.NewReader(`{"targets":["npm-cache"],"unexpected":true}`))
	rec := httptest.NewRecorder()

	handleDiskCleanupExecute(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
