package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bindsvc "github.com/IamYGT/heyserver/internal/services/bind"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestIntegration_BindStatusReadiness(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleBindStatus().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/dns/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/dns/status status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var status bindsvc.ServiceStatus
	testutil.ParseJSON(t, recorder, &status)
	if status.State == "" {
		t.Fatal("BIND status has no structured readiness state")
	}
	if status.ZoneManagementReady && (!status.Available || !status.Installed || !status.Active || status.State != bindsvc.StateHealthy) {
		t.Fatalf("manageable BIND status is inconsistent: %#v", status)
	}
	if status.State == bindsvc.StateNotInstalled && status.Installed {
		t.Fatalf("not-installed BIND status claims an installed executable: %#v", status)
	}
}

func TestInitBindServiceExposesPendingRecoveryWithoutStartingRouter(t *testing.T) {
	previous := bindService
	t.Cleanup(func() { bindService = previous })

	dataDir := t.TempDir()
	journalDir := filepath.Join(dataDir, "bind")
	if err := os.Mkdir(journalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalDir, "lifecycle-transaction.json"), []byte(`{"invalid":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InitBindService(dataDir); err == nil {
		t.Fatal("InitBindService() expected invalid journal error")
	}

	recorder := httptest.NewRecorder()
	handleBindStatus().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/dns/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/dns/status status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var status bindsvc.ServiceStatus
	testutil.ParseJSON(t, recorder, &status)
	if !status.RecoveryPending || status.ZoneManagementReady || status.State != bindsvc.StateUnavailable {
		t.Fatalf("pending recovery status=%#v", status)
	}
}

func TestBindZoneCreateRejectsInvalidIPv4BeforeMutation(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/dns/zones", strings.NewReader(`{"domain":"example.com","ip":"not-an-ip"}`))
	handleBindZoneCreate().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/dns/zones status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "ip must be a valid IPv4 address") {
		t.Fatalf("POST /api/dns/zones body=%s", recorder.Body.String())
	}
}

func TestBindMutationsRejectInvalidContractsBeforeServiceAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
		body    string
		domain  string
		want    string
	}{
		{name: "zone unknown field", handler: handleBindZoneCreate(), method: http.MethodPost, target: "/api/dns/zones", body: `{"domain":"example.com","ip":"192.0.2.1","extra":true}`, want: "unknown field"},
		{name: "zone trailing JSON", handler: handleBindZoneCreate(), method: http.MethodPost, target: "/api/dns/zones", body: `{"domain":"example.com","ip":"192.0.2.1"} {}`, want: "trailing JSON"},
		{name: "zone delete body", handler: handleBindZoneDelete(), method: http.MethodDelete, target: "/api/dns/zones/example.com", body: `{}`, domain: "example.com", want: "body must be empty"},
		{name: "SOA timer overflow", handler: handleBindSOAUpdate(), method: http.MethodPut, target: "/api/dns/zones/example.com/soa", body: `{"primaryNs":"ns1.example.com.","hostmaster":"hostmaster.example.com.","refresh":3600,"retry":900,"expire":2147483648,"minimum":300}`, domain: "example.com", want: "expire must be"},
		{name: "SOA missing timer", handler: handleBindSOAUpdate(), method: http.MethodPut, target: "/api/dns/zones/example.com/soa", body: `{"primaryNs":"ns1.example.com.","hostmaster":"hostmaster.example.com.","refresh":3600,"retry":900,"expire":604800}`, domain: "example.com", want: "minimum is required"},
		{name: "record control character", handler: handleBindRecordAdd(), method: http.MethodPost, target: "/api/dns/zones/example.com/records", body: `{"name":"www","type":"TXT","value":"safe\nunsafe","autoReload":true}`, domain: "example.com", want: "control characters"},
		{name: "record update unknown field", handler: handleBindRecordUpdate(), method: http.MethodPut, target: "/api/dns/zones/example.com/records", body: `{"name":"www","type":"A","oldValue":"192.0.2.1","newValue":"192.0.2.2","unexpected":true}`, domain: "example.com", want: "unknown field"},
		{name: "record delete mixed sources", handler: handleBindRecordDelete(), method: http.MethodDelete, target: "/api/dns/zones/example.com/records?name=www&type=A&value=192.0.2.1", body: `{"name":"www","type":"A","value":"192.0.2.1"}`, domain: "example.com", want: "not both"},
		{name: "record delete repeated query", handler: handleBindRecordDelete(), method: http.MethodDelete, target: "/api/dns/zones/example.com/records?name=www&name=api&type=A&value=192.0.2.1", domain: "example.com", want: "name query parameter must appear exactly once"},
		{name: "reload body", handler: handleBindReload(), method: http.MethodPost, target: "/api/dns/reload", body: `{}`, want: "body must be empty"},
		{name: "check body", handler: handleBindCheck(), method: http.MethodPost, target: "/api/dns/check", body: `{}`, want: "body must be empty"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			if test.domain != "" {
				request.SetPathValue("domain", test.domain)
			}
			recorder := httptest.NewRecorder()
			test.handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), test.want) {
				t.Fatalf("status=%d body=%s; want %q", recorder.Code, recorder.Body.String(), test.want)
			}
		})
	}
}

func TestBindLookupRejectsAmbiguousOrInvalidQueries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		target string
		want   string
	}{
		{target: "/api/dns/lookup", want: "domain query parameter is required"},
		{target: "/api/dns/lookup?domain=example.com&domain=other.example", want: "domain query parameter must appear exactly once"},
		{target: "/api/dns/lookup?domain=example.com&type=A%2FAAAA", want: "record type must contain only letters"},
		{target: "/api/dns/lookup?domain=example.com&resolver=1.1.1.1", want: "unsupported query parameter"},
	} {
		recorder := httptest.NewRecorder()
		handleBindDNSLookup().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.target, nil))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), test.want) {
			t.Fatalf("GET %s status=%d body=%s; want %q", test.target, recorder.Code, recorder.Body.String(), test.want)
		}
	}
}

func TestBindCheckReturnsFailedDiagnosticsAsData(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writeBindCheckResult(recorder, bindsvc.CheckResult{OK: false, Output: "zone example.com failed"})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"ok":false`) || !strings.Contains(recorder.Body.String(), "zone example.com failed") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
