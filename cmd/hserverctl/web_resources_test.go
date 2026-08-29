package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunWebResourceCommandsUseLocalAndManagedContracts(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/nginx/status":
			_, _ = writer.Write([]byte(`{"active":true}`))
		case "GET /api/nginx/configs":
			_, _ = writer.Write([]byte(`[{"filename":"site.conf","isEnabled":true}]`))
		case "GET /api/nginx/archives":
			_, _ = writer.Write([]byte(`[{"archive":"site.conf.hserver-archive-20260827T120000.000000000Z","filename":"site.conf","checksum":"` + strings.Repeat("c", 64) + `"}]`))
		case "GET /api/nginx/backups":
			_, _ = writer.Write([]byte(`[{"backup":"site.conf.hserver-backup-20260827T130000.000000000Z","filename":"site.conf","checksum":"` + strings.Repeat("e", 64) + `"}]`))
		case "GET /api/nginx/snippets":
			_, _ = writer.Write([]byte(`[{"name":"hserver-security-headers.conf","content":"# managed"}]`))
		case "GET /api/nodes/edge-1/nginx/configs":
			_, _ = writer.Write([]byte(`[{"name":"site.conf","enabled":true}]`))
		case "GET /api/nginx/configs/site.conf":
			_, _ = writer.Write([]byte(`{"filename":"site.conf","content":"server {}","checksum":"` + strings.Repeat("a", 64) + `"}`))
		case "GET /api/nodes/edge-1/nginx/configs/site.conf":
			_, _ = writer.Write([]byte(`{"name":"site.conf","content":"server {}","checksum":"` + strings.Repeat("a", 64) + `"}`))
		case "POST /api/nginx/test", "POST /api/nginx/reload", "POST /api/nodes/edge-1/nginx/actions/test", "POST /api/nodes/edge-1/nginx/actions/reload":
			_, _ = writer.Write([]byte(`{"message":"ok"}`))
		case "PUT /api/nginx/configs/site.conf":
			assertJSONFields(t, request, map[string]any{"content": "server {}\n", "checksum": strings.Repeat("a", 64)})
			_, _ = writer.Write([]byte(`{"message":"config saved"}`))
		case "PUT /api/nodes/edge-1/nginx/configs/site.conf":
			assertJSONFields(t, request, map[string]any{"content": "server {}\n", "checksum": strings.Repeat("a", 64), "reload": false})
			_, _ = writer.Write([]byte(`{"message":"config saved","backup":"site.conf.hserver-backup"}`))
		case "POST /api/nginx/configs":
			assertJSONFields(t, request, map[string]any{"domain": "new.example", "type": "static", "useSSL": false})
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"filename":"new.example.conf","checksum":"` + strings.Repeat("b", 64) + `"}`))
		case "PUT /api/nginx/configs/site.conf/state":
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode nginx state: %v", err)
			}
			_, _ = writer.Write([]byte(fmt.Sprintf(`{"filename":"site.conf","isEnabled":%t}`, body.Enabled)))
		case "DELETE /api/nginx/configs/site.conf":
			assertJSONFields(t, request, map[string]any{"checksum": strings.Repeat("a", 64)})
			_, _ = writer.Write([]byte(`{"message":"config archived","archive":"site.conf.hserver-archive","checksum":"` + strings.Repeat("a", 64) + `"}`))
		case "POST /api/nginx/archives/site.conf.hserver-archive-20260827T120000.000000000Z/restore":
			assertJSONFields(t, request, map[string]any{"checksum": strings.Repeat("c", 64)})
			_, _ = writer.Write([]byte(`{"message":"config restored","archive":"site.conf.hserver-archive-20260827T120000.000000000Z","filename":"site.conf","checksum":"` + strings.Repeat("c", 64) + `","isEnabled":false}`))
		case "POST /api/nginx/backups/site.conf.hserver-backup-20260827T130000.000000000Z/restore":
			assertJSONFields(t, request, map[string]any{"backupChecksum": strings.Repeat("e", 64), "currentChecksum": strings.Repeat("a", 64)})
			_, _ = writer.Write([]byte(`{"message":"config rolled back","backup":"site.conf.hserver-backup-20260827T130000.000000000Z","recovery":"site.conf.hserver-backup-latest","filename":"site.conf","checksum":"` + strings.Repeat("e", 64) + `","isEnabled":true}`))
		case "POST /api/nodes/edge-1/domains/site.conf/actions/enable", "POST /api/nodes/edge-1/domains/site.conf/actions/disable":
			_, _ = writer.Write([]byte(`{"message":"state applied"}`))
		case "GET /api/domains":
			_, _ = writer.Write([]byte(`{"domains":[{"id":"example.com"}]}`))
		case "GET /api/nodes/edge-1/domains":
			_, _ = writer.Write([]byte(`[{"name":"example.com","config":"example.com.conf"}]`))
		case "GET /api/domains/provisioning":
			_, _ = writer.Write([]byte(`{"dns":{"provider":"cloudflare","status":"healthy"}}`))
		case "GET /api/domains/example.com":
			_, _ = writer.Write([]byte(`{"name":"example.com"}`))
		case "POST /api/domains/check":
			assertJSONFields(t, request, map[string]any{"domain": "example.com"})
			_, _ = writer.Write([]byte(`{"domain":"example.com","available":true}`))
		case "POST /api/domains":
			var actual map[string]any
			if err := json.NewDecoder(request.Body).Decode(&actual); err != nil {
				t.Errorf("decode domain create request: %v", err)
				break
			}
			var expected map[string]any
			switch actual["domain"] {
			case "php.example.com":
				expected = map[string]any{
					"domain": "php.example.com", "type": "php", "phpVersion": "8.3", "fpmPreset": "high",
					"webRoot": "/srv/sites/php.example.com/public_html", "wwwRedirect": true,
					"issueSSL": true, "sslEmail": "admin@example.com", "createDnsRecord": true, "isolatedLinuxUser": true,
				}
			case "proxy.example.com":
				expected = map[string]any{
					"domain": "proxy.example.com", "type": "proxy", "proxyPort": float64(3100),
					"pm2_app": "api", "pm2_script": "server.js", "pm2_cwd": "/srv/apps/api", "pm2_port": float64(3101),
					"nodeEnv": "development", "existingCertName": "api-cert",
				}
			case "static.example.com":
				expected = map[string]any{
					"domain": "static.example.com", "type": "static", "webRoot": "/srv/sites/static.example.com/public_html", "spaMode": true,
				}
			default:
				t.Errorf("unexpected domain create payload: %#v", actual)
			}
			if expected != nil && !reflect.DeepEqual(actual, expected) {
				t.Errorf("domain create payload = %#v, want %#v", actual, expected)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"message":"domain created","domain":"` + fmt.Sprint(actual["domain"]) + `"}`))
		case "POST /api/domains/example.com/toggle":
			assertJSONFields(t, request, map[string]any{"active": true})
			_, _ = writer.Write([]byte(`{"domain":"example.com","active":true}`))
		case "POST /api/nodes/edge-1/domains/example.com.conf/actions/disable":
			_, _ = writer.Write([]byte(`{"message":"domain disabled"}`))
		case "DELETE /api/domains/example.com":
			if request.URL.Query().Get("deleteFiles") != "true" {
				t.Errorf("delete query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"message":"domain deleted"}`))
		case "GET /api/ssl/status":
			_, _ = writer.Write([]byte(`{"certbot":{"available":true}}`))
		case "GET /api/ssl/certificates":
			_, _ = writer.Write([]byte(`[{"domain":"example.com","daysRemaining":60}]`))
		case "GET /api/nodes/edge-1/certificates":
			_, _ = writer.Write([]byte(`[{"name":"example.com","days_remaining":60}]`))
		case "GET /api/ssl/certificates/example.com":
			_, _ = writer.Write([]byte(`{"domain":"example.com"}`))
		case "POST /api/ssl/renew/example.com", "POST /api/nodes/edge-1/certificates/example.com/actions/check", "POST /api/nodes/edge-1/certificates/example.com/actions/renew":
			_, _ = writer.Write([]byte(`{"ok":true,"message":"certificate ready"}`))
		case "POST /api/ssl/issue":
			assertJSONFields(t, request, map[string]any{"domain": "example.com", "email": "admin@example.com", "challengeType": "dns-01"})
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"ok":true,"domain":"example.com"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	configFile := filepath.Join(t.TempDir(), "site.conf")
	if err := os.WriteFile(configFile, []byte("server {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(t.TempDir(), "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf 'server {}\\n' >\"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	getenv := testWebResourceEnvironment
	commands := [][]string{
		{"nginx", "status"}, {"nginx", "configs"}, {"nginx", "configs", "--node", "edge-1"}, {"nginx", "archives"}, {"nginx", "backups"}, {"nginx", "snippets"},
		{"nginx", "get", "site.conf"}, {"nginx", "get", "--node", "edge-1", "site.conf"},
		{"nginx", "create", "--confirm", "--type", "static", "New.Example"},
		{"nginx", "enable", "--confirm", "site.conf"}, {"nginx", "disable", "--confirm", "site.conf"},
		{"nginx", "archive", "--confirm", "site.conf"},
		{"nginx", "restore", "--confirm", "site.conf.hserver-archive-20260827T120000.000000000Z"},
		{"nginx", "rollback", "--confirm", "site.conf.hserver-backup-20260827T130000.000000000Z"},
		{"nginx", "enable", "--confirm", "--node", "edge-1", "site.conf"}, {"nginx", "disable", "--confirm", "--node", "edge-1", "site.conf"},
		{"nginx", "edit", "--confirm", "--editor", editor, "site.conf"},
		{"nginx", "edit", "--confirm", "--editor", editor, "--node", "edge-1", "site.conf"},
		{"nginx", "test"}, {"nginx", "test", "--node", "edge-1"},
		{"nginx", "reload", "--confirm"}, {"nginx", "reload", "--confirm", "--node", "edge-1"},
		{"nginx", "save", "--confirm", "--content-file", configFile, "--checksum", strings.Repeat("a", 64), "site.conf"},
		{"nginx", "save", "--confirm", "--node", "edge-1", "--content-file", configFile, "--checksum", strings.Repeat("a", 64), "site.conf"},
		{"domains", "list"}, {"domains", "list", "--node", "edge-1"}, {"domains", "provisioning"},
		{"domains", "get", "example.com"}, {"domains", "check", "Example.COM"},
		{"domains", "create", "--confirm", "--type", "php", "--php-version", "8.3", "--fpm-preset", "high", "--web-root", "/srv/sites/php.example.com/public_html", "--www-redirect", "--issue-ssl", "--ssl-email", "admin@example.com", "--create-dns-record", "--isolated-linux-user", "PHP.Example.COM"},
		{"domains", "create", "--confirm", "--type", "proxy", "--proxy-port", "3100", "--pm2-app", "api", "--pm2-script", "server.js", "--pm2-cwd", "/srv/apps/api", "--pm2-port", "3101", "--node-env", "development", "--existing-cert", "api-cert", "proxy.example.com"},
		{"domains", "create", "--confirm", "--type", "static", "--web-root", "/srv/sites/static.example.com/public_html", "--spa", "static.example.com"},
		{"domains", "action", "--confirm", "example.com", "enable"},
		{"domains", "action", "--confirm", "--node", "edge-1", "example.com.conf", "disable"},
		{"domains", "delete", "--confirm", "--delete-files", "example.com"},
		{"ssl", "status"}, {"ssl", "list"}, {"ssl", "list", "--node", "edge-1"}, {"ssl", "get", "example.com"},
		{"ssl", "action", "--confirm", "example.com", "renew"},
		{"ssl", "action", "--node", "edge-1", "example.com", "check"},
		{"ssl", "action", "--confirm", "--node", "edge-1", "example.com", "renew"},
		{"ssl", "issue", "--confirm", "--domain", "example.com", "--email", "admin@example.com", "--challenge", "dns-01"},
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
	if requests.Load() != int32(len(commands)+6) {
		t.Fatalf("requests = %d, commands = %d", requests.Load(), len(commands))
	}
}

func TestRunWebResourceCommandsRejectUnsafeInputsBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	configFile := filepath.Join(t.TempDir(), "site.conf")
	if err := os.WriteFile(configFile, []byte("server {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"nginx", "reload"}, want: "explicit --confirm"},
		{args: []string{"nginx", "create", "--type", "static", "site.example"}, want: "explicit --confirm"},
		{args: []string{"nginx", "enable", "site.conf"}, want: "explicit --confirm"},
		{args: []string{"nginx", "archive", "site.conf"}, want: "explicit --confirm"},
		{args: []string{"nginx", "archive", "--confirm", "--checksum", "BAD", "site.conf"}, want: "lowercase SHA-256"},
		{args: []string{"nginx", "restore", "site.conf.hserver-archive-20260827T120000.000000000Z"}, want: "explicit --confirm"},
		{args: []string{"nginx", "restore", "--confirm", "site.conf.hserver-archive-invalid"}, want: "exact HServer UTC timestamp"},
		{args: []string{"nginx", "restore", "--confirm", "--checksum", "BAD", "site.conf.hserver-archive-20260827T120000.000000000Z"}, want: "lowercase SHA-256"},
		{args: []string{"nginx", "rollback", "site.conf.hserver-backup-20260827T130000.000000000Z"}, want: "explicit --confirm"},
		{args: []string{"nginx", "rollback", "--confirm", "site.conf.hserver-backup-invalid"}, want: "exact HServer UTC timestamp"},
		{args: []string{"nginx", "rollback", "--confirm", "--backup-checksum", "BAD", "site.conf.hserver-backup-20260827T130000.000000000Z"}, want: "lowercase backup SHA-256"},
		{args: []string{"nginx", "rollback", "--confirm", "--backup-checksum", strings.Repeat("e", 64), "--current-checksum", "BAD", "site.conf.hserver-backup-20260827T130000.000000000Z"}, want: "lowercase current config SHA-256"},
		{args: []string{"nginx", "create", "--confirm", "--type", "proxy", "site.example"}, want: "require --proxy-pass"},
		{args: []string{"nginx", "create", "--confirm", "--type", "static", "--cert-path", "/etc/site.pem", "--ssl", "site.example"}, want: "--cert-path and --key-path together"},
		{args: []string{"nginx", "get", "../site.conf"}, want: "portable config filename"},
		{args: []string{"nginx", "edit", "--editor", "/bin/true", "site.conf"}, want: "explicit --confirm"},
		{args: []string{"nginx", "save", "--confirm", "--node", "edge-1", "--content-file", configFile, "site.conf"}, want: "requires the lowercase SHA-256"},
		{args: []string{"nginx", "save", "--confirm", "--content-file", configFile, "site.conf"}, want: "requires the lowercase SHA-256"},
		{args: []string{"domains", "check", "bad_domain.example"}, want: "exact portable DNS hostname"},
		{args: []string{"domains", "check", "*.example.com"}, want: "exact portable DNS hostname"},
		{args: []string{"domains", "get", "site_config.conf"}, want: "exact portable DNS hostname"},
		{args: []string{"domains", "create", "example.com"}, want: "explicit --confirm"},
		{args: []string{"domains", "create", "--confirm", "--type", "redirect", "example.com"}, want: "php, proxy, or static"},
		{args: []string{"domains", "create", "--confirm", "--php-version", "9.0", "example.com"}, want: "7.4 or 8.0 through 8.5"},
		{args: []string{"domains", "create", "--confirm", "--type", "static", "--php-version", "8.4", "example.com"}, want: "do not accept PHP"},
		{args: []string{"domains", "create", "--confirm", "--type", "proxy", "--pm2-app", "api", "example.com"}, want: "app and script"},
		{args: []string{"domains", "create", "--confirm", "--type", "proxy", "--pm2-app", "api", "--pm2-script", "server.js", "example.com"}, want: "relative PM2 script"},
		{args: []string{"domains", "create", "--confirm", "--issue-ssl", "example.com"}, want: "plain valid --ssl-email"},
		{args: []string{"domains", "create", "--confirm", "--ssl-email", "admin@example.com", "example.com"}, want: "requires --issue-ssl"},
		{args: []string{"domains", "create", "--confirm", "--type", "proxy", "--proxy-port", "70000", "example.com"}, want: "between 1 and 65535"},
		{args: []string{"domains", "create", "--confirm", "--web-root", "relative/path", "example.com"}, want: "absolute single-line path"},
		{args: []string{"domains", "create", "--confirm", "--existing-cert", "../secret", "example.com"}, want: "certificate name is invalid"},
		{args: []string{"domains", "action", "example.com", "enable"}, want: "explicit --confirm"},
		{args: []string{"domains", "action", "--confirm", "example.com", "remove"}, want: "unsupported domain action"},
		{args: []string{"domains", "action", "--confirm", "site_config.conf", "enable"}, want: "exact portable DNS hostname"},
		{args: []string{"domains", "delete", "example.com"}, want: "explicit --confirm"},
		{args: []string{"domains", "delete", "--confirm", "*.example.com"}, want: "exact portable DNS hostname"},
		{args: []string{"ssl", "action", "example.com", "renew"}, want: "explicit --confirm"},
		{args: []string{"ssl", "action", "example.com", "check"}, want: "available for managed nodes"},
		{args: []string{"ssl", "issue", "--confirm", "--domain", "example.com", "--email", "not-an-email"}, want: "plain valid email"},
		{args: []string{"ssl", "issue", "--confirm", "--domain", "example.com", "--email", "admin@example.com", "--challenge", "manual"}, want: "http-01 or dns-01"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, testWebResourceEnvironment)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}

func assertJSONFields(t *testing.T, request *http.Request, expected map[string]any) {
	t.Helper()
	var actual map[string]any
	if err := json.NewDecoder(request.Body).Decode(&actual); err != nil {
		t.Errorf("decode request body: %v", err)
		return
	}
	for key, value := range expected {
		if actualValue, exists := actual[key]; !exists || actualValue != value {
			t.Errorf("request field %s = %#v, want %#v", key, actualValue, value)
		}
	}
}

func testWebResourceEnvironment(key string) string {
	if key == "HSERVER_TOKEN" {
		return "test-token"
	}
	return ""
}
