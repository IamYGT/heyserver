package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/gdrive"
)

func withBoundedGDriveService(t *testing.T) {
	t.Helper()
	previous := gdriveSvc
	gdriveSvc = gdrive.New(t.TempDir(), 0, "client-id", "client-secret", "", "rclone", nil, nil)
	t.Cleanup(func() { gdriveSvc = previous })
}

func TestGDriveNoBodyOperationsRejectPayload(t *testing.T) {
	withBoundedGDriveService(t)
	handlers := map[string]http.HandlerFunc{
		"oauth start":   handleGDriveOAuthStart(&config.Config{}),
		"disconnect":    handleGDriveDisconnect(),
		"dismiss error": handleGDriveDismissError(),
		"test":          handleGDriveTest(&config.Config{}),
	}
	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/backups/gdrive/test", strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "request body must be empty") {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGDriveMutationBodiesAreClosed(t *testing.T) {
	withBoundedGDriveService(t)
	admin := &models.User{ID: 7, Role: models.RoleAdmin}
	tests := []struct {
		name    string
		handler http.HandlerFunc
		body    string
		user    bool
	}{
		{"oauth app unknown", handleGDriveOAuthAppSet(), `{"gcpProjectId":"project","unknown":true}`, false},
		{"oauth app trailing", handleGDriveOAuthAppSet(), `{"gcpProjectId":"project"} {}`, false},
		{"oauth app empty client", handleGDriveOAuthAppSet(), `{"clientId":"","gcpProjectId":"project"}`, false},
		{"oauth complete unknown", handleGDriveOAuthComplete(), `{"state":"0123456789abcdef0123456789abcdef","unknown":true}`, true},
		{"oauth complete malformed state", handleGDriveOAuthComplete(), `{"state":"state-1"}`, true},
		{"settings missing field", handleGDriveUpdateSettings(), `{"folder":"hserver-backups","autoUpload":false,"remoteRetentionDays":30,"notifyOnSuccess":true}`, false},
		{"settings unknown", handleGDriveUpdateSettings(), `{"folder":"hserver-backups","autoUpload":false,"remoteRetentionDays":30,"notifyOnSuccess":true,"notifyOnFailure":true,"lastError":"clear me"}`, false},
		{"restore path", handleGDriveRestore(&config.Config{}), `{"fileName":"../nightly-full.tar.gz"}`, false},
		{"restore trailing", handleGDriveRestore(&config.Config{}), `{"fileName":"nightly-full.tar.gz"} {}`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/backups/gdrive/mutation", strings.NewReader(test.body))
			if test.user {
				req = req.WithContext(context.WithValue(req.Context(), userContextKey, admin))
			}
			rec := httptest.NewRecorder()
			test.handler(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
