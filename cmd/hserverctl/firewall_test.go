package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunFirewallCommandsUseLocalAndManagedBoundaries(t *testing.T) {
	t.Parallel()
	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/firewall/status":
			_, _ = writer.Write([]byte(`{"available":true,"state":"healthy","backend":"ufw","active":true,"rules":[]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/firewall/rules":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode local add: %v", err)
			}
			if payload["action"] != "allow" || payload["direction"] != "in" || payload["protocol"] != "tcp" || payload["port"] != "443" || payload["from"] != "203.0.113.0/24" {
				t.Errorf("local add payload = %#v", payload)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"message":"firewall rule added"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/firewall/rules/3":
			_, _ = writer.Write([]byte(`{"message":"firewall rule deleted","ruleNumber":3}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/firewall/toggle":
			var payload map[string]bool
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || !payload["enable"] {
				t.Errorf("toggle payload = %#v, err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"UFW firewall enabled","status":"enabled"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west/firewall":
			_, _ = writer.Write([]byte(`{"backend":"iptables","policy":"DROP","persistence":"active","rules":[{"id":"fw-0123456789ab","action":"ACCEPT","protocol":"tcp","port":22,"managed":true}],"revision":"` + revision + `","protected_ports":[22]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/nodes/edge west/firewall":
			var payload struct {
				Action   string `json:"action"`
				Protocol string `json:"protocol"`
				Port     int    `json:"port"`
				Source   string `json:"source"`
				Revision string `json:"revision"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode remote add: %v", err)
			}
			if payload.Action != "DROP" || payload.Protocol != "udp" || payload.Port != 53 || payload.Source != "198.51.100.0/24" || payload.Revision != revision {
				t.Errorf("remote add payload = %#v", payload)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":"fw-abcdef012345","message":"Firewall rule added and persisted"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/nodes/edge west/firewall/fw-0123456789ab":
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["revision"] != revision {
				t.Errorf("remote delete payload = %#v, err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"Firewall rule deleted and persisted"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
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
		{"firewall", "status"},
		{"firewall", "add", "--confirm", "--action", "allow", "--protocol", "tcp", "--port", "443", "--source", "203.0.113.0/24"},
		{"firewall", "delete", "--confirm", "3"},
		{"firewall", "toggle", "--confirm", "enable"},
		{"firewall", "list", "--node", "edge west"},
		{"firewall", "add", "--confirm", "--node", "edge west", "--action", "deny", "--protocol", "udp", "--port", "53", "--source", "198.51.100.0/24"},
		{"firewall", "delete", "--confirm", "--node", "edge west", "fw-0123456789ab"},
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
	if requests.Load() != 9 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunFirewallRejectsUnsafeOrUnconfirmedInputBeforeRequest(t *testing.T) {
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
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"firewall", "add", "--action", "allow", "--port", "443"}, want: "explicit --confirm"},
		{args: []string{"firewall", "add", "--confirm", "--action", "shell", "--port", "443"}, want: "allow, deny, reject, or limit"},
		{args: []string{"firewall", "add", "--confirm", "--node", "edge-1", "--action", "allow", "--direction", "out", "--port", "443"}, want: "inbound source filters only"},
		{args: []string{"firewall", "add", "--confirm", "--node", "edge-1", "--action", "allow", "--protocol", "any", "--port", "443"}, want: "cannot select a port"},
		{args: []string{"firewall", "add", "--confirm", "--node", "edge-1", "--action", "allow", "--source", "2001:db8::1"}, want: "IPv4"},
		{args: []string{"firewall", "delete", "--confirm", "0"}, want: "positive observed"},
		{args: []string{"firewall", "delete", "--confirm", "--node", "edge-1", "../rule"}, want: "observed fw- identity"},
		{args: []string{"firewall", "toggle", "disable"}, want: "explicit --confirm"},
		{args: []string{"firewall", "toggle", "--confirm", "--wait", "0s", "enable"}, want: "greater than zero"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}

func TestCLIHelpAndCompletionExposeFirewallCommands(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"firewall status", "firewall add", "firewall delete", "firewall toggle"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q: %q", command, help.String())
		}
	}

	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "firewall") {
		t.Fatalf("completion does not expose firewall: %q", completion.String())
	}
}
