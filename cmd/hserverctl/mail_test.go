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

	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

func TestRunMailReadCommandsUseProtectedTypedProjections(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		if request.URL.Query().Get("domain") != "example.com" {
			t.Errorf("domain = %q, want example.com", request.URL.Query().Get("domain"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/mail/accounts":
			_, _ = io.WriteString(writer, `[{"email":"admin@example.com","name":"Administrator","domain":"example.com","quota":1048576,"usedStorage":42,"isEnabled":true,"aliases":["ops@example.com"],"password":"must-not-print","secret":"must-not-print","hash":"must-not-print","body":"must-not-print"}]`)
		case "/api/mail/aliases":
			_, _ = io.WriteString(writer, `[{"id":"alias-1","address":"info@example.com","destinations":["admin@example.com"],"description":"Public inbox","password":"must-not-print","secret":"must-not-print","hash":"must-not-print","body":"must-not-print"}]`)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.RequestURI())
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}

	var accountsOutput bytes.Buffer
	if err := run(context.Background(), []string{"--server", server.URL, "mail", "accounts", "--domain", "example.com"}, &accountsOutput, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("mail accounts: %v", err)
	}
	var accounts []mailsvc.MailAccount
	if err := json.Unmarshal(accountsOutput.Bytes(), &accounts); err != nil {
		t.Fatalf("accounts output = %q: %v", accountsOutput.String(), err)
	}
	if len(accounts) != 1 || accounts[0].Email != "admin@example.com" || accounts[0].Aliases == nil {
		t.Fatalf("accounts = %#v", accounts)
	}
	assertMailCLIOutputOmitsProviderSecrets(t, accountsOutput.String())

	var aliasesOutput bytes.Buffer
	if err := run(context.Background(), []string{"--server", server.URL, "mail", "aliases", "--domain", "example.com"}, &aliasesOutput, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("mail aliases: %v", err)
	}
	var aliases []mailsvc.MailAlias
	if err := json.Unmarshal(aliasesOutput.Bytes(), &aliases); err != nil {
		t.Fatalf("aliases output = %q: %v", aliasesOutput.String(), err)
	}
	if len(aliases) != 1 || aliases[0].Address != "info@example.com" || aliases[0].Destinations == nil {
		t.Fatalf("aliases = %#v", aliases)
	}
	assertMailCLIOutputOmitsProviderSecrets(t, aliasesOutput.String())

	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestRunMailErrorsDoNotDumpProviderBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/mail/accounts" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.RequestURI())
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(writer, `{"error":"mail provider failed password=hunter2","secret":"body-secret","hash":"body-hash","body":"raw-provider-body"}`)
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}

	var output bytes.Buffer
	err := run(context.Background(), []string{"--server", server.URL, "mail", "accounts"}, &output, &bytes.Buffer{}, getenv)
	if err == nil {
		t.Fatal("mail accounts unexpectedly succeeded")
	}
	for _, value := range []string{"body-secret", "body-hash", "raw-provider-body", "hunter2"} {
		if strings.Contains(err.Error(), value) || strings.Contains(output.String(), value) {
			t.Fatalf("provider value %q leaked: err=%q output=%q", value, err.Error(), output.String())
		}
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("error = %q, want HTTP 502", err.Error())
	}
}

func TestRunMailOperationalReadCommandsUsePanelPayloads(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/mail/service/status", "/api/mail/status":
			_, _ = io.WriteString(writer, `{"running":true,"status":"running","pid":"1234","uptime":"today","secret":"must-not-print"}`)
		case "/api/mail/service/overview":
			_, _ = io.WriteString(writer, `{"status":{"running":true,"status":"running","pid":"1234"},"version":{"raw":"Stalwart Mail Server v0.15.5","version":"0.15.5"},"listeners":[{"id":"smtp","protocol":"smtp","bind":"0.0.0.0","port":25,"tls":false}],"storage":{"backend":"rocksdb","path":"/var/lib/mail","sizeBytes":42},"sources":{"status":{"available":true,"state":"running"},"version":{"available":true,"state":"healthy"},"listeners":{"available":true,"state":"healthy"},"storage":{"available":false,"state":"unavailable","error":"not readable"}},"secret":"must-not-print"}`)
		case "/api/mail/logs":
			if request.URL.Query().Get("lines") != "7" {
				t.Errorf("lines = %q, want 7", request.URL.Query().Get("lines"))
			}
			_, _ = io.WriteString(writer, `{"lines":7,"count":1,"entries":[{"timestamp":"2026-08-29T00:00:00Z","level":"INFO","message":"delivered\u001b[31m"}],"secret":"must-not-print"}`)
		case "/api/mail/logs/search":
			if request.URL.Query().Get("q") != "sender=alice@example.com & delivered" {
				t.Errorf("q = %q", request.URL.Query().Get("q"))
			}
			_, _ = io.WriteString(writer, `{"query":"sender=alice@example.com & delivered","count":0,"entries":[]}`)
		case "/api/mail/logs/delivery":
			if request.URL.Query().Get("email") != "alice@example.com" {
				t.Errorf("email = %q", request.URL.Query().Get("email"))
			}
			_, _ = io.WriteString(writer, `{"email":"alice@example.com","count":0,"entries":[]}`)
		case "/api/mail/queue":
			if request.URL.Query().Get("limit") != "3" {
				t.Errorf("limit = %q, want 3", request.URL.Query().Get("limit"))
			}
			_, _ = io.WriteString(writer, `[{"id":"queue-1","sender":"alice@example.com","recipients":["bob@example.com"],"createdAt":"2026-08-29T00:00:00Z","retries":1,"secret":"must-not-print"}]`)
		case "/api/mail/domains":
			_, _ = io.WriteString(writer, `[{"name":"example.com","description":"1 accounts","secret":"must-not-print"}]`)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.RequestURI())
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := mailTestGetenv

	tests := []struct {
		name string
		args []string
	}{
		{name: "service status", args: []string{"mail", "service", "status"}},
		{name: "service overview", args: []string{"mail", "service", "overview"}},
		{name: "mail status", args: []string{"mail", "status"}},
		{name: "normal logs", args: []string{"mail", "logs", "--lines", "7"}},
		{name: "search logs", args: []string{"mail", "logs", "search", "--query", "sender=alice@example.com & delivered"}},
		{name: "delivery logs", args: []string{"mail", "logs", "delivery", "--email", "alice@example.com"}},
		{name: "queue list", args: []string{"mail", "queue", "list", "--limit", "3"}},
		{name: "domains list", args: []string{"mail", "domains", "list"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			args := append([]string{"--server", server.URL}, test.args...)
			if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
				t.Fatalf("%s: %v", test.name, err)
			}
			if !json.Valid(output.Bytes()) {
				t.Fatalf("output is not JSON: %q", output.String())
			}
			if strings.Contains(strings.ToLower(output.String()), "must-not-print") {
				t.Fatalf("provider field leaked: %q", output.String())
			}
		})
	}
}

func TestRunMailMutationsRequireConfirmationAndEscapePathValues(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	var observed []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		observed = append(observed, request.Method+" "+request.RequestURI)
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost && request.URL.Path == "/api/mail/domains" {
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode domain payload: %v", err)
			}
			if payload["domain"] != "example.com" {
				t.Errorf("domain payload = %#v", payload)
			}
		}
		_, _ = io.WriteString(writer, `{"status":"ok","secret":"must-not-print"}`)
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"mail", "queue", "retry", "queue/1"},
		{"mail", "queue", "delete", "queue/1"},
		{"mail", "domains", "create", "example.com"},
		{"mail", "domains", "delete", "example.com"},
	} {
		if err := run(context.Background(), append([]string{"--server", server.URL}, args...), &bytes.Buffer{}, &bytes.Buffer{}, mailTestGetenv); err == nil || !strings.Contains(err.Error(), "explicit --confirm") {
			t.Fatalf("%v without confirmation: %v", args, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("unconfirmed commands sent %d request(s)", requests.Load())
	}

	for _, args := range [][]string{
		{"mail", "queue", "retry", "--confirm", "queue/1?raw=2"},
		{"mail", "queue", "delete", "--confirm", "queue/1?raw=2"},
		{"mail", "domains", "create", "--confirm", "example.com"},
		{"mail", "domains", "delete", "--confirm", "example.com"},
	} {
		var output bytes.Buffer
		if err := run(context.Background(), append([]string{"--server", server.URL}, args...), &output, &bytes.Buffer{}, mailTestGetenv); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(output.String(), "must-not-print") {
			t.Fatalf("mutation response leaked provider field: %q", output.String())
		}
	}
	if requests.Load() != 4 {
		t.Fatalf("requests = %d, want 4", requests.Load())
	}
	joined := strings.Join(observed, "\n")
	if !strings.Contains(joined, "/api/mail/queue/queue%2F1%3Fraw=2/retry") || !strings.Contains(joined, "/api/mail/queue/queue%2F1%3Fraw=2") {
		t.Fatalf("queue path values were not escaped: %s", joined)
	}
}

func TestRunMailLogQueriesRejectControlCharactersBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"mail", "logs", "search", "--query", "sender\nsecret"},
		{"mail", "logs", "delivery", "--email", "alice\r\nsecret@example.com"},
		{"mail", "logs", "search"},
		{"mail", "logs", "delivery"},
	} {
		err := run(context.Background(), append([]string{"--server", server.URL}, args...), &bytes.Buffer{}, &bytes.Buffer{}, mailTestGetenv)
		if err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}

func TestRunMailServiceStatus503RemainsVisibleWithoutBodyDump(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(writer, `{"error":"mail provider unavailable password=hunter2","secret":"body-secret","body":"raw-provider-log"}`)
	}))
	defer server.Close()

	var output bytes.Buffer
	err := run(context.Background(), []string{"--server", server.URL, "mail", "service", "status"}, &output, &bytes.Buffer{}, mailTestGetenv)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("error = %v, want HTTP 503", err)
	}
	for _, value := range []string{"hunter2", "body-secret", "raw-provider-log"} {
		if strings.Contains(err.Error(), value) || strings.Contains(output.String(), value) {
			t.Fatalf("provider value %q leaked: err=%q output=%q", value, err.Error(), output.String())
		}
	}
}

func mailTestGetenv(key string) string {
	if key == "HSERVER_TOKEN" {
		return "test-token"
	}
	return ""
}

func TestRunMailRejectsUnsafeDomainBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}

	for _, domain := range []string{"example.com\npassword=secret", strings.Repeat("a", maxMailDomainBytes+1)} {
		err := run(context.Background(), []string{"--server", server.URL, "mail", "accounts", "--domain", domain}, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), "mail domain") {
			t.Fatalf("domain %q error = %v", domain, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}

func TestCLIHelpAndCompletionExposeMailCommands(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"mail accounts", "mail aliases"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q: %q", command, help.String())
		}
	}
	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "mail") {
		t.Fatalf("completion does not expose mail: %q", completion.String())
	}
}

func assertMailCLIOutputOmitsProviderSecrets(t *testing.T, output string) {
	t.Helper()
	if !json.Valid([]byte(output)) {
		t.Fatalf("output is not JSON: %q", output)
	}
	for _, forbidden := range []string{"password", "secret", "hash", "body", "must-not-print"} {
		if strings.Contains(strings.ToLower(output), forbidden) {
			t.Fatalf("output contains %q: %q", forbidden, output)
		}
	}
}
