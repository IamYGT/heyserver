package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/settings"
	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func authenticatedUptimeRequest(t *testing.T, handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func uptimeTestDeps(t *testing.T) *Deps {
	t.Helper()
	sqlDB := db.Instance()
	repo := store.NewUptimeRepository(sqlDB)
	settingsSvc := settings.New(store.NewSettingsRepository(sqlDB), "test")
	channelRepo, err := store.NewNotificationChannelRepository(sqlDB, contractTestDataDir)
	if err != nil {
		t.Fatalf("notification repository: %v", err)
	}
	return &Deps{
		MetricsRepo:  store.NewMetricsRepository(sqlDB),
		Settings:     settingsSvc,
		ChannelRepo:  channelRepo,
		DeliveryRepo: store.NewNotificationDeliveryReceiptRepository(sqlDB),
		RuleRepo:     store.NewAlertRuleRepository(sqlDB),
		HistoryRepo:  store.NewAlertHistoryRepository(sqlDB),
		UptimeEngine: uptime.NewEngine(repo, channelRepo, settingsSvc),
	}
}

func TestIntegration_UptimeMonitorsNilEngine(t *testing.T) {
	deps := contractTestDeps(t)
	deps.UptimeEngine = nil
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/uptime/monitors", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when uptime engine nil", rec.Code)
	}
}

func TestIntegration_UptimeMonitorsWithEngine(t *testing.T) {
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), uptimeTestDeps(t))
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/uptime/monitors", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var list []any
	testutil.ParseJSON(t, rec, &list)
	if list == nil {
		t.Fatal("expected JSON array")
	}
}

func TestIntegration_GDriveStatusWithoutInit(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/backups/gdrive/status", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when gdrive not initialized", rec.Code)
	}
}

func TestIntegration_SnapshotStatusWithoutInit(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/backups/snapshot/status", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when snapshot not initialized", rec.Code)
	}
}

func TestIntegration_UptimeBulkFromDomains(t *testing.T) {
	cfg := testutil.TestConfig()
	nginxRoot := t.TempDir()
	cfg.NginxSitesAvailable = filepath.Join(nginxRoot, "available")
	cfg.NginxSitesEnabled = filepath.Join(nginxRoot, "enabled")
	for _, path := range []string{cfg.NginxSitesAvailable, cfg.NginxSitesEnabled} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create Nginx domain fixture: %v", err)
		}
	}
	handler := NewRouter(cfg, testutil.MinimalWebFS(t), uptimeTestDeps(t))
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	token := testutil.MakeToken(t, user)

	req := httptest.NewRequest(http.MethodPost, "/api/uptime/monitors/bulk-from-domains", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk-from-domains status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Malformed JSON body is ignored; must not panic when engine is not started.
	req = httptest.NewRequest(http.MethodPost, "/api/uptime/monitors/bulk-from-domains", strings.NewReader("{not-json"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bad JSON bulk status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_UptimeStatusPagePutBadJSON(t *testing.T) {
	deps := uptimeTestDeps(t)
	repo := deps.UptimeEngine.Repo()
	sp := store.UptimeStatusPage{
		Slug:  "ops",
		Title: "Ops",
	}
	if err := repo.CreateStatusPage(&sp); err != nil {
		t.Fatalf("CreateStatusPage: %v", err)
	}

	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	token := testutil.MakeToken(t, user)

	req := httptest.NewRequest(http.MethodPut, "/api/uptime/status-pages/1", strings.NewReader("{not-json"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUptimeMonitorTestResponseMatchesFrontendContract(t *testing.T) {
	checkedAt := time.Date(2026, 8, 25, 23, 0, 0, 123, time.UTC)
	response := newUptimeMonitorTestResponse(uptime.CheckResult{
		Status: uptime.StatusUp, Msg: "200 OK", PingMs: 12.5,
		StatusCode: 200, TLSExpiry: "2026-11-03T03:06:40Z", CheckedAt: checkedAt,
	})
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload["ok"] != true || payload["ping_ms"] != 12.5 || payload["status_code"] != float64(200) {
		t.Fatalf("frontend response = %s", encoded)
	}
	if _, exists := payload["Status"]; exists {
		t.Fatalf("legacy Go field names leaked: %s", encoded)
	}
	if _, exists := payload["error"]; exists {
		t.Fatalf("successful check must not include error: %s", encoded)
	}

	failure := newUptimeMonitorTestResponse(uptime.CheckResult{
		Status: uptime.StatusDown, Msg: "dial timeout", CheckedAt: checkedAt,
	})
	if failure.OK || failure.Error != "dial timeout" || failure.Message != "dial timeout" {
		t.Fatalf("failure response = %#v", failure)
	}
}

func TestIntegration_UptimeSettingsAreStrictEffectiveAndAtomic(t *testing.T) {
	deps := uptimeTestDeps(t)
	for key, value := range uptimeSettingsDefaults {
		key, value := key, value
		previous, _ := deps.Settings.Get(key, value)
		t.Cleanup(func() { _ = deps.Settings.Set(key, previous) })
		if err := deps.Settings.Set(key, value); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	token := testutil.MakeToken(t, user)

	recorder := authenticatedUptimeRequest(t, handler, http.MethodGet, "/api/uptime/settings", token, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET settings status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var settingsPayload map[string]string
	testutil.ParseJSON(t, recorder, &settingsPayload)
	if settingsPayload["uptime_default_channels"] != "[]" || settingsPayload["uptime_retention_days"] != "90" {
		t.Fatalf("default settings = %#v", settingsPayload)
	}

	valid := `{"uptime_retention_days":"120","uptime_compact_after_days":"20","uptime_default_interval":"45","uptime_default_timeout":"15","uptime_default_channels":"[7,7,9]"}`
	recorder = authenticatedUptimeRequest(t, handler, http.MethodPut, "/api/uptime/settings", token, valid)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT settings status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	testutil.ParseJSON(t, recorder, &settingsPayload)
	if settingsPayload["uptime_default_channels"] != "[7,9]" || settingsPayload["uptime_default_interval"] != "45" {
		t.Fatalf("normalized settings = %#v", settingsPayload)
	}

	for name, body := range map[string]string{
		"unknown":       `{"unexpected":"value"}`,
		"empty":         `{}`,
		"invalid range": `{"uptime_default_timeout":"301"}`,
		"invalid pair":  `{"uptime_retention_days":"10"}`,
		"trailing":      `{"uptime_default_interval":"60"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			failed := authenticatedUptimeRequest(t, handler, http.MethodPut, "/api/uptime/settings", token, body)
			if failed.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", failed.Code, failed.Body.String())
			}
		})
	}
	recorder = authenticatedUptimeRequest(t, handler, http.MethodGet, "/api/uptime/settings", token, "")
	testutil.ParseJSON(t, recorder, &settingsPayload)
	if settingsPayload["uptime_retention_days"] != "120" || settingsPayload["uptime_default_timeout"] != "15" {
		t.Fatalf("failed update changed settings = %#v", settingsPayload)
	}

	if err := deps.Settings.Set("uptime_retention_days", "not-a-number"); err != nil {
		t.Fatalf("persist invalid setting: %v", err)
	}
	recorder = authenticatedUptimeRequest(t, handler, http.MethodGet, "/api/uptime/settings", token, "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("invalid persisted settings GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = authenticatedUptimeRequest(t, handler, http.MethodPut, "/api/uptime/settings", token, `{"uptime_retention_days":"120"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("repair persisted settings status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := deps.Settings.Set("uptime_compact_after_days", "130"); err != nil {
		t.Fatalf("persist invalid setting relation: %v", err)
	}
	recorder = authenticatedUptimeRequest(t, handler, http.MethodGet, "/api/uptime/settings", token, "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("invalid persisted relation GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = authenticatedUptimeRequest(t, handler, http.MethodPut, "/api/uptime/settings", token, `{"uptime_compact_after_days":"20"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("repair persisted relation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestIntegration_UptimeMonitorMutationsAreStrictAndClearValues(t *testing.T) {
	deps := uptimeTestDeps(t)
	previousInterval, _ := deps.Settings.Get("uptime_default_interval", "60")
	previousTimeout, _ := deps.Settings.Get("uptime_default_timeout", "30")
	t.Cleanup(func() {
		_ = deps.Settings.Set("uptime_default_interval", previousInterval)
		_ = deps.Settings.Set("uptime_default_timeout", previousTimeout)
	})
	_ = deps.Settings.Set("uptime_default_interval", "45")
	_ = deps.Settings.Set("uptime_default_timeout", "15")
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	token := testutil.MakeToken(t, user)

	for _, body := range []string{
		`{"name":"Invalid","type":"http","url":"https://example.com","unexpected":true}`,
		`{"name":"Invalid","type":"http","url":"https://example.com"}{}`,
		`{"name":"Invalid","type":"tcp","hostname":"example.com","port":0}`,
		`{"name":"Invalid","type":"smtp","hostname":"example.com"}`,
	} {
		recorder := authenticatedUptimeRequest(t, handler, http.MethodPost, "/api/uptime/monitors", token, body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid create status=%d body=%s request=%s", recorder.Code, recorder.Body.String(), body)
		}
	}

	name := fmt.Sprintf("Strict monitor %d", time.Now().UnixNano())
	body := fmt.Sprintf(`{"name":%q,"type":"http","url":"https://example.com/health","keyword":"ready","req_headers":"X-Test: value","req_body":"payload","description":"temporary"}`, name)
	recorder := authenticatedUptimeRequest(t, handler, http.MethodPost, "/api/uptime/monitors", token, body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var monitor store.UptimeMonitor
	testutil.ParseJSON(t, recorder, &monitor)
	t.Cleanup(func() { _ = deps.UptimeEngine.Repo().DeleteMonitor(monitor.ID) })
	if monitor.IntervalSecs != 45 || monitor.TimeoutSecs != 15 || monitor.AcceptedStatusCodes != `["200-299"]` {
		t.Fatalf("created defaults = %#v", monitor)
	}

	clearBody := `{"keyword":"","req_headers":"","req_body":"","description":""}`
	recorder = authenticatedUptimeRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/uptime/monitors/%d", monitor.ID), token, clearBody)
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var clearedMonitor store.UptimeMonitor
	testutil.ParseJSON(t, recorder, &clearedMonitor)
	if clearedMonitor.Keyword != "" || clearedMonitor.ReqHeaders != "" || clearedMonitor.ReqBody != "" || clearedMonitor.Description != "" {
		t.Fatalf("cleared monitor = %#v", clearedMonitor)
	}

	recorder = authenticatedUptimeRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/uptime/monitors/%d", monitor.ID), token, `{"unexpected":true}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown update status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	for _, mutation := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/uptime/monitors/999999999/pause"},
		{method: http.MethodPost, path: "/api/uptime/monitors/999999999/resume"},
		{method: http.MethodDelete, path: "/api/uptime/monitors/999999999"},
	} {
		recorder = authenticatedUptimeRequest(t, handler, mutation.method, mutation.path, token, "")
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("missing monitor mutation %s %s status=%d body=%s", mutation.method, mutation.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestIntegration_UptimeStatusPageReplacementIsValidatedAndVisible(t *testing.T) {
	deps := uptimeTestDeps(t)
	monitor := &store.UptimeMonitor{
		Name: fmt.Sprintf("Status monitor %d", time.Now().UnixNano()), Type: "http", URL: "https://example.com/status",
		Method: "GET", IntervalSecs: 60, TimeoutSecs: 30, Retries: 1, RetryInterval: 30,
		AcceptedStatusCodes: `["200-299"]`, TLSExpiryWarnDays: 14, MaxRedirects: 5, IsActive: true,
	}
	if err := deps.UptimeEngine.Repo().CreateMonitor(monitor); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	t.Cleanup(func() { _ = deps.UptimeEngine.Repo().DeleteMonitor(monitor.ID) })
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	token := testutil.MakeToken(t, user)
	slug := fmt.Sprintf("ops-%d", time.Now().UnixNano())
	createBody := fmt.Sprintf(`{"slug":%q,"title":"Operations","description":"temporary","logo_url":"https://example.com/logo.png","theme":"auto","history_days":90,"is_public":false,"monitors":[{"monitor_id":%d,"display_name":"API","sort_order":99}]}`, slug, monitor.ID)
	recorder := authenticatedUptimeRequest(t, handler, http.MethodPost, "/api/uptime/status-pages", token, createBody)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page store.UptimeStatusPage
	testutil.ParseJSON(t, recorder, &page)
	t.Cleanup(func() { _ = deps.UptimeEngine.Repo().DeleteStatusPage(page.ID) })
	if page.IsPublic || len(page.Monitors) != 1 || page.Monitors[0].SortOrder != 1 {
		t.Fatalf("created status page = %#v", page)
	}

	recorder = authenticatedUptimeRequest(t, handler, http.MethodGet, "/api/uptime/status-pages", token, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var pages []store.UptimeStatusPage
	testutil.ParseJSON(t, recorder, &pages)
	found := false
	for _, listed := range pages {
		if listed.ID == page.ID {
			found = len(listed.Monitors) == 1 && listed.Monitors[0].MonitorID == monitor.ID
		}
	}
	if !found {
		t.Fatalf("status page monitor mapping missing from list: %#v", pages)
	}

	recorder = authenticatedUptimeRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/uptime/status-pages/%d", page.ID), token, `{"title":"Operations partial"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("partial status-page update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var partialPage store.UptimeStatusPage
	testutil.ParseJSON(t, recorder, &partialPage)
	if partialPage.Title != "Operations partial" || partialPage.IsPublic || partialPage.Description != "temporary" ||
		partialPage.LogoURL != "https://example.com/logo.png" || len(partialPage.Monitors) != 1 || partialPage.Monitors[0].MonitorID != monitor.ID {
		t.Fatalf("partial status-page update lost omitted fields = %#v", partialPage)
	}
	emptyUpdate := authenticatedUptimeRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/uptime/status-pages/%d", page.ID), token, `{}`)
	if emptyUpdate.Code != http.StatusBadRequest {
		t.Fatalf("empty status-page update status=%d body=%s", emptyUpdate.Code, emptyUpdate.Body.String())
	}

	replaceBody := fmt.Sprintf(`{"slug":%q,"title":"Operations 2","description":"","logo_url":"","theme":"dark","history_days":30,"is_public":true,"monitors":[]}`, slug)
	recorder = authenticatedUptimeRequest(t, handler, http.MethodPut, fmt.Sprintf("/api/uptime/status-pages/%d", page.ID), token, replaceBody)
	if recorder.Code != http.StatusOK {
		t.Fatalf("replace status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var replacedPage store.UptimeStatusPage
	testutil.ParseJSON(t, recorder, &replacedPage)
	if !replacedPage.IsPublic || replacedPage.Description != "" || replacedPage.LogoURL != "" || len(replacedPage.Monitors) != 0 || replacedPage.Theme != "dark" {
		t.Fatalf("replacement status page = %#v", replacedPage)
	}

	for _, body := range []string{
		fmt.Sprintf(`{"slug":%q,"title":"Bad","theme":"auto","history_days":30,"is_public":true,"monitors":[{"monitor_id":0}]}`, slug+"-bad"),
		fmt.Sprintf(`{"slug":%q,"title":"Bad","theme":"auto","history_days":30,"is_public":true,"monitors":[{"monitor_id":999999999}]}`, slug+"-missing"),
		fmt.Sprintf(`{"slug":%q,"title":"Bad","theme":"auto","history_days":30,"is_public":true,"monitors":[],"unexpected":true}`, slug+"-unknown"),
	} {
		failed := authenticatedUptimeRequest(t, handler, http.MethodPost, "/api/uptime/status-pages", token, body)
		if failed.Code != http.StatusBadRequest {
			t.Fatalf("invalid status page status=%d body=%s", failed.Code, failed.Body.String())
		}
	}

	recorder = authenticatedUptimeRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/uptime/status-pages/%d", page.ID), token, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = authenticatedUptimeRequest(t, handler, http.MethodDelete, fmt.Sprintf("/api/uptime/status-pages/%d", page.ID), token, "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("second delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
