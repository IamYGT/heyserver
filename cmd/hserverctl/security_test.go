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

func TestSecurityCommandsUseObservedFail2BanInventory(t *testing.T) {
	t.Parallel()
	var bans, unbans atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/security/score":
			_, _ = io.WriteString(writer, `{"score":82,"maxScore":100,"checks":[{"name":"Fail2Ban","status":"pass","detail":"Running"}]}`)
		case "GET /api/security/fail2ban/status":
			_, _ = io.WriteString(writer, `{"available":true,"installed":true,"running":true,"state":"healthy","daemonState":"active","availableJails":["sshd"],"jails":[{"name":"sshd","currentlyBanned":1,"totalBanned":4,"currentlyFailed":2,"bannedIPs":["192.0.2.10"]}]}`)
		case "GET /api/security/fail2ban/jails/sshd":
			_, _ = io.WriteString(writer, `{"name":"sshd","currentlyBanned":1,"totalBanned":4,"bannedIPs":["192.0.2.10"]}`)
		case "POST /api/security/fail2ban/ban":
			bans.Add(1)
			assertFail2BanPayload(t, request, "sshd", "2001:db8::10")
			_, _ = io.WriteString(writer, `{"status":"banned","ip":"2001:db8::10"}`)
		case "POST /api/security/fail2ban/unban":
			unbans.Add(1)
			assertFail2BanPayload(t, request, "sshd", "192.0.2.10")
			_, _ = io.WriteString(writer, `{"status":"unbanned","ip":"192.0.2.10"}`)
		default:
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
	commands := [][]string{
		{"security", "score"},
		{"security", "fail2ban", "status"},
		{"security", "fail2ban", "jail", "sshd"},
		{"security", "fail2ban", "ban", "--confirm", "sshd", "2001:db8::10"},
		{"security", "fail2ban", "unban", "--confirm", "sshd", "192.0.2.10"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s failed: %v", strings.Join(command, " "), err)
		}
		if output.Len() == 0 {
			t.Fatalf("%s returned no JSON", strings.Join(command, " "))
		}
	}
	if bans.Load() != 1 || unbans.Load() != 1 {
		t.Fatalf("mutations: bans=%d unbans=%d", bans.Load(), unbans.Load())
	}
}

func TestSecurityFail2BanRejectsUnsafeOrStaleInput(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/security/fail2ban/status" {
			_, _ = io.WriteString(writer, `{"available":true,"installed":true,"running":true,"state":"healthy","daemonState":"active","availableJails":["sshd"],"jails":[{"name":"sshd","bannedIPs":["192.0.2.10"]}]}`)
			return
		}
		http.Error(writer, "unexpected mutation", http.StatusInternalServerError)
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	cases := []struct {
		args         []string
		want         string
		wantRequests int32
	}{
		{args: []string{"security", "fail2ban", "ban", "sshd", "192.0.2.20"}, want: "explicit --confirm"},
		{args: []string{"security", "fail2ban", "ban", "--confirm", "bad/jail", "192.0.2.20"}, want: "portable name"},
		{args: []string{"security", "fail2ban", "ban", "--confirm", "sshd", "example.com"}, want: "plain IPv4 or IPv6"},
		{args: []string{"security", "fail2ban", "ban", "--confirm", "--wait", "0s", "sshd", "192.0.2.20"}, want: "greater than zero"},
		{args: []string{"security", "fail2ban", "ban", "--confirm", "nginx", "192.0.2.20"}, want: "not present", wantRequests: 1},
		{args: []string{"security", "fail2ban", "unban", "--confirm", "sshd", "192.0.2.99"}, want: "not present in the current banned", wantRequests: 1},
	}
	for _, item := range cases {
		before := requests.Load()
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
		if delta := requests.Load() - before; delta != item.wantRequests {
			t.Fatalf("%s sent %d request(s), want %d", strings.Join(item.args, " "), delta, item.wantRequests)
		}
	}
}

func TestSecurityFail2BanRejectsUnavailableRuntimeBeforeMutation(t *testing.T) {
	t.Parallel()
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/security/fail2ban/status" {
			_, _ = io.WriteString(writer, `{"available":false,"installed":true,"running":false,"state":"stopped","daemonState":"inactive","error":"fail2ban service is inactive","availableJails":[],"jails":[]}`)
			return
		}
		mutations.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	err := run(context.Background(), []string{"--server", server.URL, "security", "fail2ban", "ban", "--confirm", "sshd", "192.0.2.20"}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "not ready") || mutations.Load() != 0 {
		t.Fatalf("error=%v mutations=%d", err, mutations.Load())
	}
}

func TestSecurityIPListCommandsUseDecodedLocalAdminAPI(t *testing.T) {
	t.Parallel()
	var gets, adds, deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /api/security/ip-blacklist", "GET /api/security/ip-whitelist":
			gets.Add(1)
			_, _ = io.WriteString(writer, `[{"id":7,"ip":"198.51.100.0/24","listType":"blacklist","comment":"blocked range","createdAt":"2026-08-29T00:00:00Z","expiresAt":"2026-08-30T00:00:00Z"}]`)
		case "POST /api/security/ip-blacklist":
			adds.Add(1)
			assertSecurityIPListPayload(t, request, "198.51.100.20", "repeat abuse", 0, false)
			_, _ = io.WriteString(writer, `{"id":8,"ip":"198.51.100.20","listType":"blacklist","comment":"repeat abuse","createdAt":"2026-08-29T01:00:00Z"}`)
		case "POST /api/security/ip-whitelist":
			adds.Add(1)
			assertSecurityIPListPayload(t, request, "2001:db8::/64", "office", 60, true)
			_, _ = io.WriteString(writer, `{"id":9,"ip":"2001:db8::/64","listType":"whitelist","comment":"office","createdAt":"2026-08-29T01:00:00Z","expiresAt":"2026-08-29T02:00:00Z"}`)
		case "DELETE /api/security/ip-whitelist/2001:db8::/64":
			deletes.Add(1)
			if request.URL.EscapedPath() != "/api/security/ip-whitelist/2001:db8::%2F64" {
				t.Errorf("delete escaped path = %q", request.URL.EscapedPath())
			}
			if body, err := io.ReadAll(request.Body); err != nil {
				t.Errorf("read delete body: %v", err)
			} else if len(body) != 0 {
				t.Errorf("delete body = %q", body)
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
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

	var listJSON bytes.Buffer
	if err := run(context.Background(), []string{"--server", server.URL, "security", "ip-blacklist", "list"}, &listJSON, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("blacklist list failed: %v", err)
	}
	var listed []cliSecurityIPEntry
	if err := json.Unmarshal(listJSON.Bytes(), &listed); err != nil {
		t.Fatalf("decode blacklist list: %v; output=%q", err, listJSON.String())
	}
	if len(listed) != 1 || listed[0].IP != "198.51.100.0/24" || listed[0].Comment != "blocked range" {
		t.Fatalf("blacklist list = %+v", listed)
	}

	var addJSON bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "security", "ip-blacklist", "add", "--confirm", "--ip", "198.51.100.20", "--comment", "repeat abuse",
	}, &addJSON, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("blacklist add failed: %v", err)
	}
	var added cliSecurityIPEntry
	if err := json.Unmarshal(addJSON.Bytes(), &added); err != nil {
		t.Fatalf("decode blacklist add: %v; output=%q", err, addJSON.String())
	}
	if added.ID != 8 || added.IP != "198.51.100.20" || added.ListType != "blacklist" {
		t.Fatalf("blacklist add = %+v", added)
	}

	var addText bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "security", "ip-whitelist", "add", "--confirm", "--comment", "office", "--expires-in-minutes", "60", "--format", "text", "2001:db8::/64",
	}, &addText, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("whitelist add failed: %v", err)
	}
	for _, fragment := range []string{"Added security IP entry", "IP: 2001:db8::/64", "List: whitelist", "Comment: office"} {
		if !strings.Contains(addText.String(), fragment) {
			t.Fatalf("whitelist human output missing %q: %q", fragment, addText.String())
		}
	}

	var deleteText bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "security", "ip-whitelist", "delete", "--confirm", "--ip", "2001:db8::/64", "--format", "text",
	}, &deleteText, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("whitelist delete failed: %v", err)
	}
	if !strings.Contains(deleteText.String(), "Deleted security IP entry: 2001:db8::/64 from whitelist") {
		t.Fatalf("whitelist delete human output = %q", deleteText.String())
	}
	if gets.Load() != 1 || adds.Load() != 2 || deletes.Load() != 1 {
		t.Fatalf("request counts: gets=%d adds=%d deletes=%d", gets.Load(), adds.Load(), deletes.Load())
	}
}

func TestSecurityIPListMutationsRequireConfirmationAndPreserveForbidden(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		http.Error(writer, `{"error":"administrator role required"}`, http.StatusForbidden)
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}

	for _, item := range []struct {
		name string
		args []string
	}{
		{name: "add", args: []string{"security", "ip-blacklist", "add", "198.51.100.20"}},
		{name: "delete", args: []string{"security", "ip-blacklist", "delete", "198.51.100.20"}},
	} {
		before := requests.Load()
		err := run(context.Background(), append([]string{"--server", server.URL}, item.args...), &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), "requires explicit --confirm") {
			t.Fatalf("%s error = %v", item.name, err)
		}
		if requests.Load() != before {
			t.Fatalf("%s without confirmation sent a request", item.name)
		}
	}

	for _, args := range [][]string{
		{"security", "ip-blacklist", "add", "--confirm", "198.51.100.20"},
		{"security", "ip-blacklist", "delete", "--confirm", "198.51.100.20"},
	} {
		var output bytes.Buffer
		err := run(context.Background(), append([]string{"--server", server.URL}, args...), &output, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
			t.Fatalf("%s error = %v", strings.Join(args, " "), err)
		}
		if output.Len() != 0 {
			t.Fatalf("%s emitted success output on 403: %q", strings.Join(args, " "), output.String())
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("forbidden requests = %d, want 2", requests.Load())
	}

	before := requests.Load()
	err := run(context.Background(), []string{"--server", server.URL, "security", "ip-blacklist", "list", "--node", "edge-1"}, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -node") {
		t.Fatalf("--node error = %v", err)
	}
	if requests.Load() != before {
		t.Fatal("--node rejection sent a request")
	}
}

func assertSecurityIPListPayload(t *testing.T, request *http.Request, ip, comment string, expires int, wantExpires bool) {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		t.Errorf("decode security IP-list payload: %v", err)
		return
	}
	if payload["ip"] != ip || payload["comment"] != comment {
		t.Errorf("security IP-list payload = %#v", payload)
	}
	if wantExpires {
		value, ok := payload["expiresInMinutes"].(float64)
		if !ok || int(value) != expires {
			t.Errorf("security IP-list expiration = %#v, want %d", payload["expiresInMinutes"], expires)
		}
	} else if _, ok := payload["expiresInMinutes"]; ok {
		t.Errorf("unexpected security IP-list expiration in payload = %#v", payload)
	}
}

func TestCLIHelpAndCompletionExposeSecurityCommands(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"security score",
		"security fail2ban status",
		"security fail2ban jail",
		"security fail2ban ban",
		"security fail2ban unban",
		"security ip-blacklist list",
		"security ip-blacklist add",
		"security ip-blacklist delete",
		"security ip-whitelist list",
		"security ip-whitelist add",
		"security ip-whitelist delete",
	} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q", command)
		}
	}
	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "security") {
		t.Fatalf("completion does not expose security: %q", completion.String())
	}
}

func assertFail2BanPayload(t *testing.T, request *http.Request, jail, ip string) {
	t.Helper()
	var payload map[string]string
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		t.Errorf("decode payload: %v", err)
		return
	}
	if payload["jail"] != jail || payload["ip"] != ip {
		t.Errorf("payload = %#v", payload)
	}
}
