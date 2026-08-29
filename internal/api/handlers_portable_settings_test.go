package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestIntegration_PortableSettingsExportIsAdminOnlyAndAllowlisted(t *testing.T) {
	deps := contractTestDeps(t)
	if err := deps.Settings.SetMany(map[string]string{
		"hostnameDisplay":        "Portable test server",
		"provider_client_secret": "must-not-export",
		"webmail_url":            "javascript:must-not-export",
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = deps.Settings.Delete("hostnameDisplay")
		_ = deps.Settings.Delete("provider_client_secret")
		_ = deps.Settings.Delete("webmail_url")
	})
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)

	manager := testutil.MakeToken(t, testutil.MakeUser(2, "manager@test.com", models.RoleManager))
	managerRec := httptest.NewRecorder()
	handler.ServeHTTP(managerRec, testutil.NewRequest(t, http.MethodGet, "/api/settings/portable", manager))
	if managerRec.Code != http.StatusForbidden {
		t.Fatalf("manager status = %d, want 403", managerRec.Code)
	}

	admin := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, testutil.NewRequest(t, http.MethodGet, "/api/settings/portable", admin))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Header().Get("Content-Disposition"), "hserver-portable-config-v1.json") {
		t.Fatalf("download headers = %#v", rec.Header())
	}
	var bundle settings.PortableBundle
	testutil.ParseJSON(t, rec, &bundle)
	if bundle.Settings["hostnameDisplay"] != "Portable test server" {
		t.Fatalf("hostnameDisplay = %q", bundle.Settings["hostnameDisplay"])
	}
	serialized, _ := json.Marshal(bundle)
	if bytes.Contains(serialized, []byte("must-not-export")) || bundle.Settings["provider_client_secret"] != "" || bundle.Settings["webmail_url"] != "" {
		t.Fatalf("export leaked excluded or invalid values: %s", serialized)
	}
}

func TestIntegration_PortableSettingsPreviewAndConfirmedImport(t *testing.T) {
	deps := contractTestDeps(t)
	const currentValue = "portable-current-marker"
	const proposedValue = "portable-proposed-marker"
	if err := deps.Settings.Set("hostnameDisplay", currentValue); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deps.Settings.Delete("hostnameDisplay") })
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)
	admin := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))
	bundle := settings.PortableBundle{
		SchemaVersion: settings.PortableSchemaVersion,
		ExportedAt:    time.Date(2026, 8, 26, 20, 0, 0, 0, time.UTC),
		SourceVersion: "test-v1",
		Settings:      map[string]string{"hostnameDisplay": proposedValue},
	}

	previewRec := portableSettingsJSONRequest(t, handler, admin, "/api/settings/portable/preview", bundle)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 body=%s", previewRec.Code, previewRec.Body.String())
	}
	var preview settings.PortablePreview
	testutil.ParseJSON(t, previewRec, &preview)
	if preview.ImportedKeys != 1 || preview.ChangedKeys != 1 {
		t.Fatalf("preview = %#v", preview)
	}
	if got, _ := deps.Settings.Get("hostnameDisplay", ""); got != currentValue {
		t.Fatalf("preview mutated hostnameDisplay to %q", got)
	}

	rejectedRec := portableSettingsJSONRequest(t, handler, admin, "/api/settings/portable/import", portableSettingsImportRequest{Bundle: bundle})
	if rejectedRec.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed import status = %d, want 400 body=%s", rejectedRec.Code, rejectedRec.Body.String())
	}
	if got, _ := deps.Settings.Get("hostnameDisplay", ""); got != currentValue {
		t.Fatalf("unconfirmed import mutated hostnameDisplay to %q", got)
	}

	importRec := portableSettingsJSONRequest(t, handler, admin, "/api/settings/portable/import", portableSettingsImportRequest{Bundle: bundle, Confirmed: true})
	if importRec.Code != http.StatusOK {
		t.Fatalf("confirmed import status = %d, want 200 body=%s", importRec.Code, importRec.Body.String())
	}
	if got, _ := deps.Settings.Get("hostnameDisplay", ""); got != proposedValue {
		t.Fatalf("confirmed import hostnameDisplay = %q", got)
	}

	entries, _, err := db.NewAuditRepository(db.Instance()).List(db.AuditFilter{ActionContains: "portable_config_"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Details, currentValue) || strings.Contains(entry.Details, proposedValue) {
			t.Fatalf("audit details leaked setting value: %q", entry.Details)
		}
	}
}

func TestIntegration_PortableSettingsRejectsUnknownJSONFields(t *testing.T) {
	handler := integrationRouter(t)
	admin := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))
	body := `{"schema_version":1,"exported_at":"2026-08-26T20:00:00Z","source_version":"test-v1","settings":{"notifyOnError":"true"},"unexpected":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/portable/preview", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+admin)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_PortableSettingsRejectsOversizedBodyWith413(t *testing.T) {
	handler := integrationRouter(t)
	admin := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))
	body := `{"schema_version":1,"exported_at":"2026-08-26T20:00:00Z","source_version":"test-v1","settings":{"hostnameDisplay":"` + strings.Repeat("x", portableSettingsRequestLimit) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/portable/preview", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+admin)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 body=%s", rec.Code, rec.Body.String())
	}
}

func portableSettingsJSONRequest(t *testing.T, handler http.Handler, token, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
