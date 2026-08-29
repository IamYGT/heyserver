package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunCloudflareCommandsUseBoundedProviderEndpoints(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/cloudflare/zones":
			_, _ = writer.Write([]byte(`[{"id":"zone123","name":"example.com","status":"active"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/cloudflare/zones/zone123":
			_, _ = writer.Write([]byte(`{"id":"zone123","name":"example.com","status":"active"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/cloudflare/zones/zone123/email-routing":
			_, _ = writer.Write([]byte(`{"enabled":true,"status":"ready"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/cloudflare/zones/zone123/records":
			if request.URL.Query().Get("type") != "" || request.URL.Query().Get("name") != "" {
				if request.URL.Query().Get("type") != "A" || request.URL.Query().Get("name") != "www.example.com" {
					t.Errorf("record query = %q", request.URL.RawQuery)
				}
			}
			_, _ = writer.Write([]byte(`[{"id":"record456","type":"A","name":"www.example.com","content":"192.0.2.10","ttl":300,"proxied":false}]`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/cloudflare/zones/zone123/records":
			body := decodeCloudflareRequest(t, request)
			assertCloudflareField(t, body, "type", "CNAME")
			assertCloudflareField(t, body, "name", "app.example.com")
			assertCloudflareField(t, body, "content", "origin.example.com")
			assertCloudflareField(t, body, "ttl", float64(1))
			assertCloudflareField(t, body, "proxied", true)
			_, _ = writer.Write([]byte(`{"id":"created","type":"CNAME","name":"app.example.com","content":"origin.example.com","ttl":1,"proxied":true}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/cloudflare/zones/zone123/records/record456":
			body := decodeCloudflareRequest(t, request)
			assertCloudflareField(t, body, "type", "A")
			assertCloudflareField(t, body, "name", "www.example.com")
			assertCloudflareField(t, body, "content", "192.0.2.20")
			assertCloudflareField(t, body, "ttl", float64(300))
			assertCloudflareField(t, body, "proxied", false)
			_, _ = writer.Write([]byte(`{"id":"record456","type":"A","name":"www.example.com","content":"192.0.2.20","ttl":300,"proxied":false}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/cloudflare/zones/zone123/records/record456/proxy":
			body := decodeCloudflareRequest(t, request)
			assertCloudflareField(t, body, "proxied", true)
			_, _ = writer.Write([]byte(`{"id":"record456","type":"A","name":"www.example.com","content":"192.0.2.20","ttl":300,"proxied":true}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/cloudflare/zones/zone123/records/record456":
			writer.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && request.URL.Path == "/api/cloudflare/zones/zone123/purge":
			_, _ = writer.Write([]byte(`{"status":"purged"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/cloudflare/mail-autofix/example.com":
			_, _ = writer.Write([]byte(`{"domain":"example.com","changed":true}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := cloudflareTestEnvironment

	commands := [][]string{
		{"cloudflare", "zones"},
		{"cloudflare", "zone", "zone123"},
		{"cloudflare", "records", "--type", "a", "--name", "www.example.com", "zone123"},
		{"cloudflare", "email-routing", "zone123"},
		{"cloudflare", "record-create", "--confirm", "--type", "cname", "--name", "app.example.com", "--content", "origin.example.com", "--proxied", "true", "zone123"},
		{"cloudflare", "record-update", "--confirm", "--content", "192.0.2.20", "zone123", "record456"},
		{"cloudflare", "record-proxy", "--confirm", "--proxied", "true", "zone123", "record456"},
		{"cloudflare", "record-delete", "--confirm", "zone123", "record456"},
		{"cloudflare", "purge", "--confirm", "zone123"},
		{"cloudflare", "mail-autofix", "--confirm", "Example.COM."},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
	}
	// record-update and record-proxy resolve the current record before mutation.
	if requests.Load() != int32(len(commands)+2) {
		t.Fatalf("requests = %d, want %d", requests.Load(), len(commands)+2)
	}
}

func TestRunCloudflareRejectsUnsafeMutationsBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"cloudflare", "record-create", "--type", "A", "--name", "@", "--content", "192.0.2.1", "zone123"}, want: "explicit --confirm"},
		{args: []string{"cloudflare", "record-create", "--confirm", "--type", "A", "--name", "@", "--content", "192.0.2.1", "--ttl", "29", "zone123"}, want: "TTL"},
		{args: []string{"cloudflare", "record-create", "--confirm", "--type", "MX", "--name", "@", "--content", "mail.example.com", "--proxied", "true", "zone123"}, want: "only for A, AAAA, or CNAME"},
		{args: []string{"cloudflare", "record-update", "--confirm", "zone123", "record456"}, want: "at least one changed field"},
		{args: []string{"cloudflare", "record-proxy", "--confirm", "zone123", "record456"}, want: "requires --proxied"},
		{args: []string{"cloudflare", "record-delete", "--confirm", "zone/123", "record456"}, want: "unsupported character"},
		{args: []string{"cloudflare", "purge", "--confirm", "--wait", "0s", "zone123"}, want: "greater than zero"},
		{args: []string{"cloudflare", "mail-autofix", "--confirm", "localhost"}, want: "containing at least one dot"},
		{args: []string{"cloudflare", "records", "--type", "A/AAAA", "zone123"}, want: "only letters"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, cloudflareTestEnvironment)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected Cloudflare commands sent %d request(s)", requests.Load())
	}
}

func TestCLIHelpAndCompletionExposeCloudflare(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"cloudflare zones", "cloudflare records", "cloudflare record-create", "cloudflare record-delete", "cloudflare purge"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q", command)
		}
	}

	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "cloudflare") {
		t.Fatalf("completion does not expose cloudflare: %q", completion.String())
	}
}

func decodeCloudflareRequest(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	defer request.Body.Close()
	data, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("request body = %q: %v", data, err)
	}
	return body
}

func assertCloudflareField(t *testing.T, body map[string]any, name string, want any) {
	t.Helper()
	if body[name] != want {
		t.Errorf("%s = %#v, want %#v; body=%#v", name, body[name], want, body)
	}
}

func cloudflareTestEnvironment(key string) string {
	if key == "HSERVER_TOKEN" {
		return "test-token"
	}
	return ""
}
