package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bindsvc "github.com/IamYGT/heyserver/internal/services/bind"
)

// integrationRouterWithBindFixture keeps the route smoke independent of the
// machine's optional BIND installation while still exercising the real route
// and authentication middleware before serving the local fixture response.
func integrationRouterWithBindFixture(t *testing.T) http.Handler {
	t.Helper()
	next := integrationRouter(t)
	fixture := newBindZoneHTTPFixture(t)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/dns/zones" {
			next.ServeHTTP(w, r)
			return
		}

		// Keep auth and route registration under test. A clean CI runner has no
		// host BIND inventory, so the real handler's expected inventory error is
		// replaced with a deterministic response from the temp fixture. Any
		// unrelated status, including an unexpected 5xx, remains visible to the
		// caller and fails the broad route smoke.
		actual := httptest.NewRecorder()
		next.ServeHTTP(actual, r)
		if actual.Code == http.StatusNotFound {
			t.Fatalf("GET /api/dns/zones route is not registered")
		}
		if actual.Code == http.StatusOK || bindInventoryUnavailable(actual.Body.String()) {
			fixture.ServeHTTP(w, r)
			return
		}
		copyTestResponse(w, actual)
	})
}

func bindInventoryUnavailable(body string) bool {
	return strings.Contains(body, "BIND zone inventory is unavailable:") ||
		strings.Contains(body, "reading named.conf.local:")
}

func copyTestResponse(w http.ResponseWriter, response *httptest.ResponseRecorder) {
	for key, values := range response.Header() {
		w.Header()[key] = append([]string(nil), values...)
	}
	w.WriteHeader(response.Code)
	_, _ = w.Write(response.Body.Bytes())
}

type bindZoneHTTPFixture struct {
	path string
}

func newBindZoneHTTPFixture(t *testing.T) *bindZoneHTTPFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bind-zones.json")
	zones := []bindsvc.Zone{{
		Domain:      "fixture.example.com",
		File:        "/fixture/db.fixture.example.com",
		Serial:      2026082901,
		RecordCount: 1,
	}}
	payload, err := json.Marshal(zones)
	if err != nil {
		t.Fatalf("marshal BIND fixture: %v", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write BIND fixture: %v", err)
	}
	return &bindZoneHTTPFixture{path: path}
}

func (f *bindZoneHTTPFixture) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	payload, err := os.ReadFile(f.path)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "reading BIND fixture: "+err.Error())
		return
	}
	var zones []bindsvc.Zone
	if err := json.Unmarshal(payload, &zones); err != nil {
		jsonError(w, http.StatusInternalServerError, "decoding BIND fixture: "+err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, zones)
}
