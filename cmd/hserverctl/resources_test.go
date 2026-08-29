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

func TestRunContainerCommandsUseLocalAndManagedEndpoints(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/docker/status":
			_, _ = writer.Write([]byte(`{"installed":true,"running":true}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/docker/containers":
			_, _ = writer.Write([]byte(`[{"id":"abc123","name":"web"}]`))
		case request.Method == http.MethodGet && request.RequestURI == "/api/nodes/edge%20west/containers":
			_, _ = writer.Write([]byte(`[{"id":"remote-1","name":"worker"}]`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/docker/containers/abc123/pause":
			_, _ = writer.Write([]byte(`{"status":"ok","action":"pause"}`))
		case request.Method == http.MethodPost && request.RequestURI == "/api/nodes/edge%20west/containers/remote-1/actions/restart":
			_, _ = writer.Write([]byte(`{"message":"container restarted"}`))
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
		{"containers", "status"},
		{"containers", "list"},
		{"containers", "list", "--node", "edge west"},
		{"containers", "action", "--confirm", "abc123", "pause"},
		{"containers", "action", "--confirm", "--node", "edge west", "remote-1", "restart"},
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
	if requests.Load() != int32(len(commands)) {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunContainerActionRejectsUnconfirmedAndUnsupportedMutations(t *testing.T) {
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
		{args: []string{"containers", "action", "abc123", "stop"}, want: "explicit --confirm"},
		{args: []string{"containers", "action", "--confirm", "--node", "edge-1", "abc123", "remove"}, want: "unsupported managed-node"},
		{args: []string{"containers", "action", "--confirm", "abc123", "shell"}, want: "unsupported local"},
		{args: []string{"containers", "action", "--confirm", "--wait", "0s", "abc123", "restart"}, want: "greater than zero"},
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

func TestRunLogCommandsUseLocalAndManagedSources(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/logs/sources":
			_, _ = writer.Write([]byte(`{"sources":[{"path":"/var/log/app & api.log","label":"Application","readable":true}]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/logs/read":
			if request.URL.Query().Get("path") != "/var/log/app & api.log" || request.URL.Query().Get("lines") != "250" {
				t.Errorf("local log query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"lines":["ready"],"total":1}`))
		case request.Method == http.MethodGet && request.RequestURI == "/api/nodes/edge%20west":
			_, _ = writer.Write([]byte(`{
				"id":"edge west","online":true,"capabilities":["logs.read"],
				"inventory":{"log_sources":["system","nginx"]}
			}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west/logs":
			if request.URL.Query().Get("source") != "nginx" || request.URL.Query().Get("lines") != "500" {
				t.Errorf("managed log query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`[{"timestamp":"2026-08-27T10:00:00Z","message":"request complete"}]`))
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

	commands := []struct {
		args []string
		want string
	}{
		{args: []string{"logs", "sources"}, want: `"Application"`},
		{args: []string{"logs", "read", "--source", "/var/log/app & api.log", "--lines", "250"}, want: `"ready"`},
		{args: []string{"logs", "sources", "--node", "edge west"}, want: `"available": true`},
		{args: []string{"logs", "read", "--node", "edge west", "--source", "nginx", "--lines", "500"}, want: `"request complete"`},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command.args...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s: %v", strings.Join(command.args, " "), err)
		}
		if !json.Valid(output.Bytes()) || !strings.Contains(output.String(), command.want) {
			t.Fatalf("%s output = %q", strings.Join(command.args, " "), output.String())
		}
	}
	if requests.Load() != int32(len(commands)) {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunLogReadRejectsInvalidInputBeforeRequest(t *testing.T) {
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
		{args: []string{"logs", "read", "--lines", "10"}, want: "usage:"},
		{args: []string{"logs", "read", "--source", "/var/log/app.log", "--lines", "5001"}, want: "between 1 and 5000"},
		{args: []string{"logs", "read", "--node", "edge-1", "--source", "system", "--lines", "501"}, want: "between 1 and 500"},
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

func TestCLIHelpAndCompletionExposeResourceCommands(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"containers list", "containers action", "logs sources", "logs read", "pm2 list", "pm2 action"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q: %q", command, help.String())
		}
	}

	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"containers", "logs", "pm2"} {
		if !strings.Contains(completion.String(), command) {
			t.Fatalf("completion does not expose %q: %q", command, completion.String())
		}
	}
}
