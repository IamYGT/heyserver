package cloudflare

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReconcileDomainAddress(t *testing.T) {
	t.Parallel()

	var created CreateRecordRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			writeCFResult(t, w, []CFZone{{ID: "zone-1", Name: "example.com", Status: "active"}})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-1/dns_records":
			if got := r.URL.Query().Get("type"); got != "A" {
				t.Errorf("record type query = %q, want A", got)
			}
			if got := r.URL.Query().Get("name"); got != "app.example.com" {
				t.Errorf("record name query = %q, want app.example.com", got)
			}
			writeCFResult(t, w, []CFRecord{})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-1/dns_records":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Errorf("decode record: %v", err)
			}
			writeCFResult(t, w, CFRecord{ID: "record-1", Type: created.Type, Name: created.Name, Content: created.Content, TTL: created.TTL, Proxied: created.Proxied})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := New("token", "")
	svc.baseURL = server.URL
	svc.client = server.Client()
	result, err := svc.ReconcileDomainAddress("App.Example.com.", "203.0.113.25", true)
	if err != nil {
		t.Fatalf("ReconcileDomainAddress: %v", err)
	}
	if result.RecordType != "A" || result.Change.Action != "created" {
		t.Fatalf("result = %+v", result)
	}
	if created.Name != "app.example.com" || created.Content != "203.0.113.25" || !created.Proxied || created.TTL != dnsRecordTTL {
		t.Fatalf("created record = %+v", created)
	}
}

func TestReconcileDomainAddressRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	svc := New("token", "")
	if _, err := svc.ReconcileDomainAddress("not-a-domain", "203.0.113.25", false); err == nil {
		t.Fatal("invalid domain should fail")
	}
	if _, err := svc.ReconcileDomainAddress("example.com", "not-an-ip", false); err == nil {
		t.Fatal("invalid origin should fail")
	}
}

func TestReconcileDomainAddressUsesAAAAForIPv6(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zones":
			writeCFResult(t, w, []CFZone{{ID: "zone-1", Name: "example.com"}})
		case "/zones/zone-1/dns_records":
			if r.Method == http.MethodGet {
				if got := r.URL.Query().Get("type"); got != "AAAA" {
					t.Errorf("record type query = %q, want AAAA", got)
				}
				writeCFResult(t, w, []CFRecord{})
				return
			}
			writeCFResult(t, w, CFRecord{ID: "record-6"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := New("token", "")
	svc.baseURL = server.URL
	svc.client = server.Client()
	result, err := svc.ReconcileDomainAddress("example.com", "2001:db8::25", false)
	if err != nil {
		t.Fatalf("ReconcileDomainAddress: %v", err)
	}
	if result.RecordType != "AAAA" {
		t.Fatalf("record type = %q, want AAAA", result.RecordType)
	}
}

func writeCFResult(t *testing.T, w http.ResponseWriter, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result}); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
