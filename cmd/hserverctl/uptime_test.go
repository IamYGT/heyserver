package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunUptimeUsesAuthenticatedCommandDispatch(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("uptime-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/uptime/monitors/summary" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer uptime-token" {
			t.Errorf("Authorization = %q", authorization)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"up":1,"down":0,"paused":0,"maintenance":0}`))
	}))
	defer server.Close()

	var output bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"--token-file", tokenFile,
		"uptime", "summary",
	}, &output, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"up": 1`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunUptimeCommandsUseFixedEndpoints(t *testing.T) {
	t.Parallel()
	var mutex sync.Mutex
	requests := make([]string, 0, 15)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer test-token" {
			t.Errorf("Authorization = %q", authorization)
		}
		mutex.Lock()
		requests = append(requests, request.Method+" "+request.URL.RequestURI())
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/uptime/monitors":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode monitor request: %v", err)
			}
			if payload["name"] == "Public API" {
				if payload["type"] != "http" || payload["url"] != "https://app.example.com/health" || payload["method"] != "HEAD" || payload["accepted_statuscodes"] != `["200-299","301"]` || payload["tls_check"] != true {
					t.Errorf("HTTP monitor payload = %#v", payload)
				}
				channels, ok := payload["alert_channel_ids"].([]any)
				if !ok || len(channels) != 2 || channels[0] != float64(7) || channels[1] != float64(9) {
					t.Errorf("HTTP monitor channels = %#v", payload["alert_channel_ids"])
				}
			} else if payload["name"] == "PostgreSQL" {
				if payload["type"] != "tcp" || payload["hostname"] != "db.example.com" || payload["port"] != float64(5432) {
					t.Errorf("TCP monitor payload = %#v", payload)
				}
			} else {
				t.Errorf("unexpected monitor payload = %#v", payload)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":41,"name":"created"}`))
		default:
			_, _ = writer.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"summary"},
		{"monitors"},
		{"monitor", "get", "41"},
		{"monitor", "create", "--confirm", "--name", "Public API", "--type", "http", "--url", "https://app.example.com/health", "--method", "head", "--accepted-statuscodes", "200-299,301", "--tls-check", "--alert-channel", "7", "--alert-channel", "9"},
		{"monitor", "create", "--confirm", "--name", "PostgreSQL", "--type", "tcp", "--hostname", "db.example.com", "--port", "5432"},
		{"monitor", "update", "--confirm", "--name", "Public API v2", "41"},
		{"monitor", "heartbeats", "--hours", "168", "41"},
		{"monitor", "stats", "41"},
		{"monitor", "check", "--confirm", "41"},
		{"monitor", "pause", "--confirm", "41"},
		{"monitor", "resume", "--confirm", "41"},
		{"monitor", "delete", "--confirm", "41"},
		{"incidents", "--monitor", "41", "--limit", "25"},
		{"status-pages"},
		{"status-page", "create", "--confirm", "--slug", "operations", "--title", "Operations"},
		{"status-page", "delete", "--confirm", "41"},
		{"settings"},
		{"settings", "update", "--confirm", "--retention-days", "120"},
		{"import-domains", "--confirm"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		if err := runUptime(context.Background(), client, command, &output); err != nil {
			t.Fatalf("runUptime(%q): %v", command, err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("runUptime(%q) output is not JSON: %q", command, output.String())
		}
	}

	want := []string{
		"GET /api/uptime/monitors/summary",
		"GET /api/uptime/monitors",
		"GET /api/uptime/monitors/41",
		"POST /api/uptime/monitors",
		"POST /api/uptime/monitors",
		"PUT /api/uptime/monitors/41",
		"GET /api/uptime/monitors/41/heartbeats?hours=168",
		"GET /api/uptime/monitors/41/uptime",
		"POST /api/uptime/monitors/41/check-now",
		"POST /api/uptime/monitors/41/pause",
		"POST /api/uptime/monitors/41/resume",
		"DELETE /api/uptime/monitors/41",
		"GET /api/uptime/incidents?limit=25&monitor_id=41",
		"GET /api/uptime/status-pages",
		"POST /api/uptime/status-pages",
		"DELETE /api/uptime/status-pages/41",
		"GET /api/uptime/settings",
		"PUT /api/uptime/settings",
		"POST /api/uptime/monitors/bulk-from-domains",
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(requests) != len(want) {
		t.Fatalf("requests = %#v", requests)
	}
	for index := range want {
		if requests[index] != want[index] {
			t.Errorf("request %d = %q, want %q", index, requests[index], want[index])
		}
	}
}

func TestRunUptimeRejectsUnsafeActionsBeforeRequest(t *testing.T) {
	t.Parallel()
	var requestCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"unexpected"}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"monitor", "get", "0"}, want: "positive integer"},
		{args: []string{"monitor", "create", "--name", "API", "--type", "http", "--url", "https://app.example.com"}, want: "explicit --confirm"},
		{args: []string{"monitor", "create", "--confirm", "--name", "", "--type", "http", "--url", "https://app.example.com"}, want: "name is required"},
		{args: []string{"monitor", "create", "--confirm", "--name", "API", "--type", "smtp", "--hostname", "mail.example.com"}, want: "http, tcp, ping, or dns"},
		{args: []string{"monitor", "create", "--confirm", "--name", "API", "--type", "http", "--url", "ftp://app.example.com"}, want: "absolute http:// or https://"},
		{args: []string{"monitor", "create", "--confirm", "--name", "API", "--type", "http", "--url", "https://app.example.com", "--hostname", "app.example.com"}, want: "not valid for HTTP"},
		{args: []string{"monitor", "create", "--confirm", "--name", "API", "--type", "http", "--url", "https://app.example.com", "--accepted-statuscodes", "99-200"}, want: "invalid uptime status range"},
		{args: []string{"monitor", "create", "--confirm", "--name", "API", "--type", "http", "--url", "https://app.example.com", "--interval-secs", "9"}, want: "between 10 and 86400"},
		{args: []string{"monitor", "create", "--confirm", "--name", "DB", "--type", "tcp", "--hostname", "db.example.com", "--port", "0"}, want: "between 1 and 65535"},
		{args: []string{"monitor", "create", "--confirm", "--name", "DB", "--type", "tcp", "--hostname", "localhost", "--port", "5432"}, want: "valid ASCII hostname or IP"},
		{args: []string{"monitor", "create", "--confirm", "--name", "DNS", "--type", "dns", "--hostname", "example.com", "--dns-record-type", "TXT"}, want: "A, AAAA, MX, or CNAME"},
		{args: []string{"monitor", "create", "--confirm", "--name", "API", "--type", "http", "--url", "https://app.example.com", "--alert-channel", "0"}, want: "positive integer"},
		{args: []string{"monitor", "heartbeats", "--hours", "0", "41"}, want: "between 1 and 2160"},
		{args: []string{"monitor", "check", "41"}, want: "explicit --confirm"},
		{args: []string{"monitor", "pause", "--confirm", "--wait", "0s", "41"}, want: "greater than zero"},
		{args: []string{"monitor", "delete", "--confirm", "0"}, want: "positive integer"},
		{args: []string{"incidents", "--monitor", "0"}, want: "positive integer"},
		{args: []string{"incidents", "--monitor", "-1"}, want: "positive integer"},
		{args: []string{"incidents", "--limit", "1001"}, want: "between 1 and 1000"},
		{args: []string{"import-domains"}, want: "explicit --confirm"},
	}
	for _, test := range cases {
		err := runUptime(context.Background(), client, test.args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("runUptime(%q) error = %v, want containing %q", test.args, err, test.want)
		}
	}
	if got := requestCount.Load(); got != 0 {
		t.Fatalf("unsafe commands made %d requests", got)
	}
}

func TestNormalizeUptimeStatusCodes(t *testing.T) {
	t.Parallel()
	got, err := normalizeUptimeStatusCodes(" 200-299, 301,301 ")
	if err != nil {
		t.Fatal(err)
	}
	if got != `["200-299","301"]` {
		t.Fatalf("normalized status codes = %q", got)
	}
	for _, invalid := range []string{"", "200-", "600", "400-300", "200,,201"} {
		if _, err := normalizeUptimeStatusCodes(invalid); err == nil {
			t.Errorf("normalizeUptimeStatusCodes(%q) unexpectedly succeeded", invalid)
		}
	}
}
