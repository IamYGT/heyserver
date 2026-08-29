package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunDNSCommandsUseLocalBINDAPI(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/dns/status":
			_, _ = writer.Write([]byte(`{"available":true,"state":"healthy","zoneManagementReady":true}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/dns/zones":
			_, _ = writer.Write([]byte(`[{"domain":"example.com","serial":2026082701,"recordCount":4}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/dns/zones/example.com":
			_, _ = writer.Write([]byte(`{"domain":"example.com","records":[]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/dns/zones/example.com/records":
			_, _ = writer.Write([]byte(`[{"name":"www","ttl":"3600","type":"A","value":"192.0.2.10"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/dns/zones/example.com/soa":
			_, _ = writer.Write([]byte(`{"primaryNs":"ns1.example.com.","hostmaster":"hostmaster.example.com.","serial":2026082701,"refresh":3600,"retry":900,"expire":604800,"minimum":300}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/dns/lookup":
			if request.URL.Query().Get("domain") != "_sip._tcp.example.com" || request.URL.Query().Get("type") != "SRV" {
				t.Errorf("lookup query=%q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"query":{"domain":"_sip._tcp.example.com","type":"SRV"},"results":[]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/dns/check":
			assertDNSNoBody(t, request)
			_, _ = writer.Write([]byte(`{"ok":false,"output":"zone check failed","zoneChecks":[]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/dns/zones/example.com/export":
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write([]byte("$TTL 3600\n@ IN A 192.0.2.10\n"))
		case request.Method == http.MethodPost && request.URL.Path == "/api/dns/zones":
			body := decodeDNSRequest(t, request)
			assertDNSField(t, body, "domain", "example.com")
			assertDNSField(t, body, "ip", "192.0.2.10")
			_, _ = writer.Write([]byte(`{"domain":"example.com","records":[]}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/dns/zones/example.com":
			assertDNSNoBody(t, request)
			_, _ = writer.Write([]byte(`{"message":"zone example.com deleted"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/dns/zones/example.com/records":
			body := decodeDNSRequest(t, request)
			assertDNSField(t, body, "name", "www")
			assertDNSField(t, body, "type", "A")
			assertDNSField(t, body, "value", "192.0.2.20")
			assertDNSField(t, body, "ttl", "300")
			assertDNSField(t, body, "autoReload", true)
			_, _ = writer.Write([]byte(`{"message":"record added"}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/dns/zones/example.com/records":
			body := decodeDNSRequest(t, request)
			assertDNSField(t, body, "name", "www")
			assertDNSField(t, body, "type", "A")
			assertDNSField(t, body, "oldValue", "192.0.2.20")
			assertDNSField(t, body, "newValue", "192.0.2.21")
			_, _ = writer.Write([]byte(`{"message":"record updated"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/dns/zones/example.com/records":
			body := decodeDNSRequest(t, request)
			assertDNSField(t, body, "name", "www")
			assertDNSField(t, body, "type", "A")
			assertDNSField(t, body, "value", "192.0.2.21")
			_, _ = writer.Write([]byte(`{"message":"record deleted"}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/dns/zones/example.com/soa":
			body := decodeDNSRequest(t, request)
			assertDNSField(t, body, "primaryNs", "ns1.example.com.")
			assertDNSField(t, body, "refresh", float64(7200))
			assertDNSField(t, body, "retry", float64(900))
			_, _ = writer.Write([]byte(`{"message":"SOA updated"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/dns/reload":
			assertDNSNoBody(t, request)
			_, _ = writer.Write([]byte(`{"message":"BIND9 reloaded successfully"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	commands := [][]string{
		{"dns", "status"},
		{"dns", "zones"},
		{"dns", "zone", "Example.COM."},
		{"dns", "records", "example.com"},
		{"dns", "soa", "example.com"},
		{"dns", "lookup", "--type", "srv", "_sip._tcp.Example.COM."},
		{"dns", "check"},
		{"dns", "zone-create", "--confirm", "--ip", "192.0.2.10", "Example.COM."},
		{"dns", "zone-delete", "--confirm", "example.com"},
		{"dns", "record-add", "--confirm", "--name", "WWW", "--type", "a", "--value", "192.0.2.20", "--ttl", "300", "example.com"},
		{"dns", "record-update", "--confirm", "--name", "www", "--type", "A", "--old-value", "192.0.2.20", "--new-value", "192.0.2.21", "example.com"},
		{"dns", "record-delete", "--confirm", "--name", "www", "--type", "A", "--value", "192.0.2.21", "example.com"},
		{"dns", "soa-update", "--confirm", "--refresh", "7200", "example.com"},
		{"dns", "reload", "--confirm"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, dnsTestEnvironment); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
	}

	var raw bytes.Buffer
	if err := run(context.Background(), []string{"--server", server.URL, "dns", "export", "example.com"}, &raw, &bytes.Buffer{}, dnsTestEnvironment); err != nil {
		t.Fatal(err)
	}
	if raw.String() != "$TTL 3600\n@ IN A 192.0.2.10\n" {
		t.Fatalf("raw export=%q", raw.String())
	}

	outputPath := filepath.Join(t.TempDir(), "db.example.com")
	var receipt bytes.Buffer
	if err := run(context.Background(), []string{"--server", server.URL, "dns", "export", "--output", outputPath, "example.com"}, &receipt, &bytes.Buffer{}, dnsTestEnvironment); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil || string(data) != raw.String() || !json.Valid(receipt.Bytes()) {
		t.Fatalf("file export data=%q receipt=%q err=%v", data, receipt.String(), err)
	}
	if requests.Load() != int32(len(commands)+3) { // SOA update preflight plus two exports.
		t.Fatalf("requests=%d want=%d", requests.Load(), len(commands)+3)
	}
}

func TestRunDNSRejectsUnsafeCommandsBeforeRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"dns", "zone-create", "--ip", "192.0.2.1", "example.com"}, want: "explicit --confirm"},
		{args: []string{"dns", "zone-create", "--confirm", "--ip", "2001:db8::1", "example.com"}, want: "valid IPv4"},
		{args: []string{"dns", "zone-delete", "--confirm", "../example.com"}, want: "invalid DNS label"},
		{args: []string{"dns", "record-add", "--confirm", "--type", "A", "--value", "not-an-ip", "example.com"}, want: "valid IPv4"},
		{args: []string{"dns", "record-add", "--confirm", "--type", "TXT", "--value", "ok", "--ttl", "1h", "example.com"}, want: "TTL must be"},
		{args: []string{"dns", "record-update", "--confirm", "--name", "www", "--type", "A", "--old-value", "192.0.2.1", "example.com"}, want: "record value is required"},
		{args: []string{"dns", "record-delete", "--confirm", "--name", "www", "--type", "TXT", "--value", "safe\nunsafe", "example.com"}, want: "control characters"},
		{args: []string{"dns", "soa-update", "--confirm", "example.com"}, want: "at least one changed field"},
		{args: []string{"dns", "reload", "--confirm", "--wait", "0s"}, want: "greater than zero"},
		{args: []string{"dns", "lookup", "--type", "A/AAAA", "example.com"}, want: "only letters"},
	}
	for _, test := range tests {
		args := append([]string{"--server", server.URL}, test.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, dnsTestEnvironment)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s error=%v; want %q", strings.Join(test.args, " "), err, test.want)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected DNS commands sent %d request(s)", requests.Load())
	}
}

func TestDNSExportRefusesOverwrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "zone")
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusiveFile(path, []byte("replace")); err == nil {
		t.Fatal("writeExclusiveFile accepted an existing destination")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("existing file data=%q err=%v", data, err)
	}
}

func TestCLIHelpAndCompletionExposeDNS(t *testing.T) {
	t.Parallel()

	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"dns status", "dns records", "dns zone-create", "dns record-add", "dns soa-update", "dns reload"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q", command)
		}
	}

	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "dns") {
		t.Fatalf("completion does not expose dns: %q", completion.String())
	}
}

func decodeDNSRequest(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	defer request.Body.Close()
	data, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("request body=%q: %v", data, err)
	}
	return body
}

func assertDNSNoBody(t *testing.T, request *http.Request) {
	t.Helper()
	data, err := io.ReadAll(request.Body)
	if err != nil || len(data) != 0 {
		t.Errorf("request body=%q err=%v", data, err)
	}
}

func assertDNSField(t *testing.T, body map[string]any, name string, want any) {
	t.Helper()
	if body[name] != want {
		t.Errorf("%s=%#v want=%#v; body=%#v", name, body[name], want, body)
	}
}

func dnsTestEnvironment(key string) string {
	if key == "HSERVER_TOKEN" {
		return "test-token"
	}
	return ""
}
