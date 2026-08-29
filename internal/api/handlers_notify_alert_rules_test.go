package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

func openAlertRuleTestRepositories(t *testing.T) (*store.AlertRuleRepository, *store.AlertHistoryRepository) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+t.TempDir()+"/notify.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.MigrateNotify(db); err != nil {
		t.Fatal(err)
	}
	return store.NewAlertRuleRepository(db), store.NewAlertHistoryRepository(db)
}

func TestHandleAlertRuleCreateNormalizesAndRespectsDisabled(t *testing.T) {
	rules, _ := openAlertRuleTestRepositories(t)
	req := httptest.NewRequest(http.MethodPost, "/api/notify/rules", strings.NewReader(`{
		"name":" High CPU ","type":"cpu","threshold":90,"enabled":false
	}`))
	rec := httptest.NewRecorder()
	handleAlertRuleCreate(rules)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var rule models.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatal(err)
	}
	if rule.Name != "High CPU" || rule.Type != models.AlertCPUUsage || rule.Target != "" || rule.Enabled || rule.CooldownMins != 15 {
		t.Fatalf("created rule = %#v", rule)
	}
}

func TestHandleAlertRuleCreateValidatesStrictContract(t *testing.T) {
	rules, _ := openAlertRuleTestRepositories(t)
	tests := []struct {
		name string
		body string
	}{
		{"unknown field", `{"name":"CPU","type":"cpu_usage","threshold":90,"operator":">"}`},
		{"trailing JSON", `{"name":"CPU","type":"cpu_usage","threshold":90} {}`},
		{"missing threshold", `{"name":"CPU","type":"cpu_usage"}`},
		{"unsafe SSL target", `{"name":"SSL","type":"ssl_expiry","threshold":14,"target":"../example.com"}`},
		{"invalid service target", `{"name":"Service","type":"service_down","target":"--user.service"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleAlertRuleCreate(rules)(rec, httptest.NewRequest(http.MethodPost, "/api/notify/rules", strings.NewReader(tt.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleAlertRuleCreateAcceptsServiceWithoutThreshold(t *testing.T) {
	rules, _ := openAlertRuleTestRepositories(t)
	rec := httptest.NewRecorder()
	handleAlertRuleCreate(rules)(rec, httptest.NewRequest(http.MethodPost, "/api/notify/rules", strings.NewReader(`{
		"name":"Web service","type":"service_down","target":"nginx.service"
	}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var rule models.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatal(err)
	}
	if rule.Threshold != 1 || rule.Target != "nginx.service" {
		t.Fatalf("service rule = %#v", rule)
	}
}

func TestHandleAlertRuleUpdateOverlaysOnlyProvidedFields(t *testing.T) {
	rules, _ := openAlertRuleTestRepositories(t)
	rule := &models.AlertRule{Name: "CPU", Type: models.AlertCPUUsage, Threshold: 90, Enabled: true, CooldownMins: 30}
	if err := rules.Create(rule); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/notify/rules/1", strings.NewReader(`{"enabled":false}`))
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	handleAlertRuleUpdate(rules)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	got, err := rules.Get(rule.ID)
	if err != nil || got == nil {
		t.Fatalf("get: rule=%#v err=%v", got, err)
	}
	if got.Enabled || got.Name != "CPU" || got.Type != models.AlertCPUUsage || got.Threshold != 90 || got.CooldownMins != 30 {
		t.Fatalf("updated rule = %#v", got)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/notify/rules/1", strings.NewReader(`{"type":"disk","target":"/srv","threshold":80}`))
	req.SetPathValue("id", "1")
	rec = httptest.NewRecorder()
	handleAlertRuleUpdate(rules)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("type update status = %d body=%s", rec.Code, rec.Body.String())
	}
	got, _ = rules.Get(rule.ID)
	if got.Type != models.AlertDiskUsage || got.Target != "/srv" || got.Threshold != 80 {
		t.Fatalf("type-updated rule = %#v", got)
	}
}

func TestHandleAlertHistoryListValidatesPagination(t *testing.T) {
	_, history := openAlertRuleTestRepositories(t)
	for _, path := range []string{
		"/api/notify/history?limit=0",
		"/api/notify/history?limit=201",
		"/api/notify/history?limit=01",
		"/api/notify/history?offset=-1",
		"/api/notify/history?offset=+1",
		"/api/notify/history?limit=10&limit=20",
		"/api/notify/history?unknown=1",
	} {
		rec := httptest.NewRecorder()
		handleAlertHistoryList(history)(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d want 400 body=%s", path, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	handleAlertHistoryList(history)(rec, httptest.NewRequest(http.MethodGet, "/api/notify/history?limit=200&offset=0", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid pagination status = %d body=%s", rec.Code, rec.Body.String())
	}
}
