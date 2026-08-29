package uptime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/IamYGT/heyserver/internal/store"
)

func TestApplyHTTPHeadersJSON(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com/health", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	err = applyHTTPHeaders(req, `{"Authorization":"Bearer test-token","X-Trace":"trace-123"}`)
	if err != nil {
		t.Fatalf("applyHTTPHeaders() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
	}
	if got := req.Header.Get("X-Trace"); got != "trace-123" {
		t.Errorf("X-Trace = %q, want %q", got, "trace-123")
	}
}

func TestApplyHTTPHeadersLineFormat(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com/health", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	err = applyHTTPHeaders(req, "X-Trace: abc:def\n\nAccept: application/json")
	if err != nil {
		t.Fatalf("applyHTTPHeaders() error = %v", err)
	}
	if got := req.Header.Get("X-Trace"); got != "abc:def" {
		t.Errorf("X-Trace = %q, want %q", got, "abc:def")
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want %q", got, "application/json")
	}
}

func TestApplyHTTPHeadersHost(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "json", raw: `{"Host":"status.example.com"}`},
		{name: "line", raw: "host: status.example.org"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://example.com/health", nil)
			if err != nil {
				t.Fatalf("http.NewRequest() error = %v", err)
			}

			if err := applyHTTPHeaders(req, tc.raw); err != nil {
				t.Fatalf("applyHTTPHeaders() error = %v", err)
			}
			wantHost := "status.example.com"
			if tc.name == "line" {
				wantHost = "status.example.org"
			}
			if req.Host != wantHost {
				t.Errorf("req.Host = %q, want %q", req.Host, wantHost)
			}
			if got := req.Header.Get("Host"); got != "" {
				t.Errorf("req.Header Host = %q, want empty", got)
			}
		})
	}
}

func TestParseHTTPHeadersRejectsMalformedInput(t *testing.T) {
	const secret = "header-secret-value"
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing separator", raw: "not-a-header"},
		{name: "empty name", raw: ": value"},
		{name: "invalid name", raw: "Bad Name: value"},
		{name: "invalid value", raw: "X-Token: " + secret + "\x01"},
		{name: "json value is not a string", raw: `{"X-Count":42}`},
		{name: "json null value", raw: `{"X-Optional":null}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseHTTPHeaders(tc.raw)
			if err == nil {
				t.Fatal("parseHTTPHeaders() error = nil, want error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error contains secret value: %q", err)
			}
		})
	}
}

func TestCheckHTTPMalformedHeadersDownBeforeNetwork(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	const secret = "request-header-secret"
	result := checkHTTP(&store.UptimeMonitor{
		URL:        server.URL,
		ReqHeaders: "X-Token: " + secret + "\x01",
	})

	if result.Status != StatusDown {
		t.Fatalf("Status = %d, want StatusDown (%d)", result.Status, StatusDown)
	}
	if !strings.Contains(result.Msg, "request headers error") {
		t.Errorf("Msg = %q, want request headers error", result.Msg)
	}
	if strings.Contains(result.Msg, secret) {
		t.Errorf("Msg contains secret value: %q", result.Msg)
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Errorf("server requests = %d, want 0", got)
	}
}
