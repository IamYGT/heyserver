package api

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/store"
	"github.com/IamYGT/heyserver/internal/testutil"
	_ "github.com/mattn/go-sqlite3"
)

func TestHandleSettingsGetAll_nilService503(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	handleSettingsGetAll(nil)(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body map[string]string
	testutil.ParseJSON(t, rec, &body)
	if body[mailAccessStateKey] != string(integrationstate.Unavailable) {
		t.Fatalf("mail access state = %q, want %q", body[mailAccessStateKey], integrationstate.Unavailable)
	}
}

func TestHandleSettingsGetAllReportsCanonicalMailAccessState(t *testing.T) {
	deps := contractTestDeps(t)
	for _, key := range requiredMailAccessSettings {
		if err := deps.Settings.Delete(key); err != nil {
			t.Fatalf("delete %s: %v", key, err)
		}
	}
	t.Cleanup(func() {
		for _, key := range requiredMailAccessSettings {
			_ = deps.Settings.Delete(key)
		}
	})

	readState := func() string {
		t.Helper()
		recorder := httptest.NewRecorder()
		handleSettingsGetAll(deps.Settings)(recorder, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET settings status = %d body=%s", recorder.Code, recorder.Body.String())
		}
		var body map[string]string
		testutil.ParseJSON(t, recorder, &body)
		return body[mailAccessStateKey]
	}

	if got := readState(); got != string(integrationstate.NotConfigured) {
		t.Fatalf("empty mail access state = %q, want %q", got, integrationstate.NotConfigured)
	}
	if err := deps.Settings.Set("webmail_url", "https://webmail.example.com"); err != nil {
		t.Fatal(err)
	}
	if got := readState(); got != string(integrationstate.NotConfigured) {
		t.Fatalf("partial mail access state = %q, want %q", got, integrationstate.NotConfigured)
	}
	if err := deps.Settings.SetMany(map[string]string{
		"webmail_url":             "https://webmail.example.com",
		"mail_admin_url":          "https://admin.example.com",
		"mail_server_host":        "mail.example.com",
		"mail_imap_port":          "993",
		"mail_smtp_starttls_port": "587",
		"mail_smtp_ssl_port":      "465",
	}); err != nil {
		t.Fatal(err)
	}
	if got := readState(); got != string(integrationstate.Unavailable) {
		t.Fatalf("complete mail access state = %q, want %q", got, integrationstate.Unavailable)
	}
}

func TestHandleSettingsGetAllReadFailureReportsUnavailable(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	svc := settings.New(store.NewSettingsRepository(db), "test")
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	recorder := httptest.NewRecorder()
	handleSettingsGetAll(svc)(recorder, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	var body map[string]string
	testutil.ParseJSON(t, recorder, &body)
	if body["error"] != "internal server error" {
		t.Fatalf("error = %q, want internal server error", body["error"])
	}
	if body[mailAccessStateKey] != string(integrationstate.Unavailable) {
		t.Fatalf("mail access state = %q, want %q", body[mailAccessStateKey], integrationstate.Unavailable)
	}
}

func TestIntegration_SettingsCRUD(t *testing.T) {
	handler := integrationRouter(t)
	manager := testutil.MakeUser(2, "mgr@test.com", models.RoleManager)
	token := testutil.MakeToken(t, manager)

	// PUT settings
	putBody := `{"hostnameDisplay":"Primary panel","adminEmail":"admin@example.com"}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(putBody))
	putReq.Header.Set("Authorization", "Bearer "+token)
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 body=%s", putRec.Code, putRec.Body.String())
	}

	// GET all
	getReq := testutil.NewRequest(t, http.MethodGet, "/api/settings", token)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET all status = %d, want 200", getRec.Code)
	}
	var all map[string]string
	testutil.ParseJSON(t, getRec, &all)
	if all["hostnameDisplay"] != "Primary panel" || all["adminEmail"] != "admin@example.com" {
		t.Errorf("settings = %+v", all)
	}

	// GET single key
	keyReq := testutil.NewRequest(t, http.MethodGet, "/api/settings/hostnameDisplay", token)
	keyRec := httptest.NewRecorder()
	handler.ServeHTTP(keyRec, keyReq)
	if keyRec.Code != http.StatusOK {
		t.Fatalf("GET key status = %d, want 200", keyRec.Code)
	}
	var single map[string]string
	testutil.ParseJSON(t, keyRec, &single)
	if single["value"] != "Primary panel" {
		t.Errorf("value = %q, want Primary panel", single["value"])
	}

	// DELETE key (admin only)
	admin := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	adminToken := testutil.MakeToken(t, admin)
	delReq := testutil.NewRequest(t, http.MethodDelete, "/api/settings/adminEmail", adminToken)
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200 body=%s", delRec.Code, delRec.Body.String())
	}
}

func TestIntegration_SettingsHideAndProtectInternalRecords(t *testing.T) {
	deps := contractTestDeps(t)
	if err := deps.Settings.Set("gdrive_oauth_app", `{"clientId":"id","clientSecret":"must-not-leak"}`); err != nil {
		t.Fatal(err)
	}
	if err := deps.Settings.Set("hostnameDisplay", "Community panel"); err != nil {
		t.Fatal(err)
	}
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)
	viewerToken := testutil.MakeToken(t, testutil.MakeUser(3, "viewer@test.com", models.RoleViewer))

	allRecorder := httptest.NewRecorder()
	handler.ServeHTTP(allRecorder, testutil.NewRequest(t, http.MethodGet, "/api/settings", viewerToken))
	if allRecorder.Code != http.StatusOK {
		t.Fatalf("GET settings status = %d body=%s", allRecorder.Code, allRecorder.Body.String())
	}
	if bytes.Contains(allRecorder.Body.Bytes(), []byte("must-not-leak")) || bytes.Contains(allRecorder.Body.Bytes(), []byte("gdrive_oauth_app")) {
		t.Fatalf("generic settings response exposed internal record: %s", allRecorder.Body.String())
	}

	keyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(keyRecorder, testutil.NewRequest(t, http.MethodGet, "/api/settings/gdrive_oauth_app", viewerToken))
	if keyRecorder.Code != http.StatusNotFound {
		t.Fatalf("GET internal setting status = %d, want 404 body=%s", keyRecorder.Code, keyRecorder.Body.String())
	}
}

func TestIntegration_SettingsPutRequiresBoundedEditableValues(t *testing.T) {
	handler := integrationRouter(t)
	token := testutil.MakeToken(t, testutil.MakeUser(2, "mgr@test.com", models.RoleManager))
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "valid", body: `{"notifyOnLogin":"true"}`, want: http.StatusOK},
		{name: "unknown internal key", body: `{"onboarding_completed":"true"}`, want: http.StatusBadRequest},
		{name: "invalid boolean", body: `{"notifyOnLogin":"yes"}`, want: http.StatusBadRequest},
		{name: "oversized email", body: `{"adminEmail":"` + strings.Repeat("a", 250) + `@example.com"}`, want: http.StatusBadRequest},
		{name: "unknown setting", body: `{"panel.theme":"dark"}`, want: http.StatusBadRequest},
		{name: "trailing JSON", body: `{"notifyOnLogin":"true"}{}`, want: http.StatusBadRequest},
		{name: "oversized", body: `{"hostnameDisplay":"` + strings.Repeat("x", portableSettingsRequestLimit) + `"}`, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(test.body))
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, test.want, rec.Body.String())
			}
		})
	}
}

func TestIntegration_SettingsDeleteRejectsInternalKeysAndBodies(t *testing.T) {
	handler := integrationRouter(t)
	token := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))
	for _, test := range []struct {
		name string
		path string
		body string
		want int
	}{
		{name: "internal key", path: "/api/settings/gdrive_oauth_app", want: http.StatusNotFound},
		{name: "request body", path: "/api/settings/adminEmail", body: `{}`, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, test.path, strings.NewReader(test.body))
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, test.want, rec.Body.String())
			}
		})
	}
}

func TestIntegration_OnboardingFlow(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	token := testutil.MakeToken(t, user)

	getReq := testutil.NewRequest(t, http.MethodGet, "/api/onboarding", token)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET onboarding status = %d", getRec.Code)
	}
	var initial map[string]any
	testutil.ParseJSON(t, getRec, &initial)
	if initial["completed"] != false {
		t.Errorf("completed = %v, want false", initial["completed"])
	}

	putBody := `{"completed":true,"step":3}`
	putReq := httptest.NewRequest(http.MethodPost, "/api/onboarding", strings.NewReader(putBody))
	putReq.Header.Set("Authorization", "Bearer "+token)
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT onboarding status = %d body=%s", putRec.Code, putRec.Body.String())
	}

	getRec2 := httptest.NewRecorder()
	handler.ServeHTTP(getRec2, getReq)
	if getRec2.Code != http.StatusOK {
		t.Fatalf("GET onboarding after PUT status = %d", getRec2.Code)
	}
	var updated map[string]any
	testutil.ParseJSON(t, getRec2, &updated)
	if updated["completed"] != true {
		t.Errorf("completed = %v, want true", updated["completed"])
	}
	if updated["step"] != float64(3) {
		t.Errorf("step = %v, want 3", updated["step"])
	}
}

func TestHandleOnboardingSetRequiresExactBoundedState(t *testing.T) {
	service := contractTestDeps(t).Settings
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "valid", body: `{"completed":false,"step":5}`, want: http.StatusOK},
		{name: "missing completed", body: `{"step":2}`, want: http.StatusBadRequest},
		{name: "missing step", body: `{"completed":false}`, want: http.StatusBadRequest},
		{name: "negative step", body: `{"completed":false,"step":-1}`, want: http.StatusBadRequest},
		{name: "step too high", body: `{"completed":false,"step":6}`, want: http.StatusBadRequest},
		{name: "unknown field", body: `{"completed":false,"step":2,"skip":true}`, want: http.StatusBadRequest},
		{name: "trailing JSON", body: `{"completed":false,"step":2}{}`, want: http.StatusBadRequest},
		{name: "oversized", body: `{"completed":false,"step":2,"padding":"` + strings.Repeat("x", 4096) + `"}`, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handleOnboardingSet(service)(recorder, httptest.NewRequest(http.MethodPost, "/api/onboarding", strings.NewReader(test.body)))
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestIntegration_SettingsPutEmptyBody400(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(2, "mgr@test.com", models.RoleManager)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+testutil.MakeToken(t, user))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
