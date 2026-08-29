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
	"sync/atomic"
	"testing"
	"time"
)

func TestRunUptimeMonitorUpdateSendsPartialPayloadToFixedAuthenticatedEndpoint(t *testing.T) {
	t.Parallel()
	var method, path, authorization string
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		method = request.Method
		path = request.URL.Path
		authorization = request.Header.Get("Authorization")
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode update payload: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":41,"name":"Renamed"}`))
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "update-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runUptimeMonitorUpdate(context.Background(), client, []string{
		"--confirm",
		"--name", "Renamed",
		"--interval-secs", "120",
		"--method", "head",
		"--accepted-statuscodes", "200-299, 301",
		"--keyword", "ready",
		"--req-headers", "X-Test: value\nX-Second: two",
		"--req-body", `{"probe":true}`,
		"--tls-check=false",
		"--tls-expiry-warn-days", "21",
		"--max-redirects", "7",
		"--description", "updated monitor",
		"--alert-reminder-mins", "15",
		"--alert-channel", "7",
		"--alert-channel", "9",
		"41",
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPut || path != "/api/uptime/monitors/41" {
		t.Fatalf("request = %s %s, want PUT /api/uptime/monitors/41", method, path)
	}
	if authorization != "Bearer update-token" {
		t.Fatalf("Authorization = %q", authorization)
	}
	want := map[string]any{
		"name":                 "Renamed",
		"interval_secs":        float64(120),
		"method":               "HEAD",
		"accepted_statuscodes": `["200-299","301"]`,
		"keyword":              "ready",
		"req_headers":          "X-Test: value\nX-Second: two",
		"req_body":             `{"probe":true}`,
		"tls_check":            false,
		"tls_expiry_warn_days": float64(21),
		"max_redirects":        float64(7),
		"description":          "updated monitor",
		"alert_reminder_mins":  float64(15),
		"alert_channel_ids":    []any{float64(7), float64(9)},
	}
	if len(payload) != len(want) {
		t.Fatalf("partial payload has %d fields: %#v", len(payload), payload)
	}
	for key, expected := range want {
		left, _ := json.Marshal(payload[key])
		right, _ := json.Marshal(expected)
		if string(left) != string(right) {
			t.Errorf("payload[%q] = %s, want %s", key, left, right)
		}
	}
	if !json.Valid(output.Bytes()) {
		t.Fatalf("output is not JSON: %q", output.String())
	}
}

func TestRunUptimeMonitorUpdateSupportsClearOperationsAndProtectedFiles(t *testing.T) {
	t.Parallel()
	headersFile := filepath.Join(t.TempDir(), "headers")
	bodyFile := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(headersFile, []byte(`{"Authorization":"Bearer protected-value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bodyFile, []byte("file body"), 0o600); err != nil {
		t.Fatal(err)
	}
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode update payload: %v", err)
		}
		payloads = append(payloads, payload)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"--confirm", "--clear-alert-channels", "--clear-description", "--clear-keyword", "41"},
		{"--confirm", "--req-headers-file", headersFile, "--req-body-file", bodyFile, "41"},
	} {
		if err := runUptimeMonitorUpdate(context.Background(), client, args, &bytes.Buffer{}); err != nil {
			t.Fatalf("runUptimeMonitorUpdate(%q): %v", args, err)
		}
	}
	if len(payloads) != 2 {
		t.Fatalf("payload count = %d", len(payloads))
	}
	if got := payloads[0]["alert_channel_ids"]; got == nil {
		t.Fatal("clear payload omitted alert_channel_ids")
	} else {
		encoded, _ := json.Marshal(got)
		if string(encoded) != "[]" {
			t.Fatalf("cleared alert channels = %s", encoded)
		}
	}
	for _, key := range []string{"description", "keyword"} {
		if payloads[0][key] != "" {
			t.Errorf("cleared %s = %#v", key, payloads[0][key])
		}
	}
	if payloads[1]["req_headers"] != `{"Authorization":"Bearer protected-value"}` || payloads[1]["req_body"] != "file body" {
		t.Fatalf("file payload = %#v", payloads[1])
	}
}

func TestRunUptimeMonitorUpdateRequiresConfirmationAndChangedField(t *testing.T) {
	t.Parallel()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"unexpected"}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"41"}, want: "explicit --confirm"},
		{args: []string{"--confirm", "41"}, want: "at least one changed field"},
	} {
		err := runUptimeMonitorUpdate(context.Background(), client, test.args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("args %q error = %v, want containing %q", test.args, err, test.want)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("confirmation/no-op validation made %d request(s)", got)
	}
}

func TestRunUptimeMonitorUpdateRejectsInvalidValuesBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
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
		{args: []string{"--confirm", "--interval-secs", "9", "41"}, want: "between 10 and 86400"},
		{args: []string{"--confirm", "--method", "TRACE", "41"}, want: "HTTP method"},
		{args: []string{"--confirm", "--accepted-statuscodes", "99-200", "41"}, want: "invalid uptime status range"},
		{args: []string{"--confirm", "--alert-channel", "0", "41"}, want: "positive integer"},
		{args: []string{"--confirm", "--url", "ftp://example.com", "41"}, want: "absolute http:// or https://"},
		{args: []string{"--confirm", "--req-headers", "bad\rheader", "41"}, want: "request headers"},
		{args: []string{"--confirm", "--alert-channel", "7", "--clear-alert-channels", "41"}, want: "cannot be combined"},
		{args: []string{"--confirm", "--description", strings.Repeat("x", 2049), "41"}, want: "at most 2048"},
	}
	for _, test := range cases {
		err := runUptimeMonitorUpdate(context.Background(), client, test.args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("args %q error = %v, want containing %q", test.args, err, test.want)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid updates made %d request(s)", got)
	}
}

func TestRunUptimeMonitorUpdateRejectsUnsafeInputFileBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"unexpected"}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "headers")
	if err := os.WriteFile(path, []byte("X-Test: value"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = runUptimeMonitorUpdate(context.Background(), client, []string{"--confirm", "--req-headers-file", path, "41"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "must not be accessible") {
		t.Fatalf("unsafe file error = %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unsafe file validation made %d request(s)", got)
	}
}
