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

func TestRunPM2CommandsUseLocalAndManagedEndpoints(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/pm2/processes":
			_, _ = writer.Write([]byte(`[{"id":1,"name":"api","status":"online"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/pm2/processes/api":
			_, _ = writer.Write([]byte(`{"id":1,"name":"api","status":"online"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/pm2/processes/api/logs":
			if request.URL.Query().Get("lines") != "300" {
				t.Errorf("local log query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"id":"api","lines":300,"output":["ready"]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/pm2/processes/api/reload":
			_, _ = writer.Write([]byte(`{"status":"reloaded","output":"done"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/pm2/save":
			_, _ = writer.Write([]byte(`{"status":"saved"}`))
		case request.Method == http.MethodGet && request.RequestURI == "/api/nodes/edge%20west/pm2":
			_, _ = writer.Write([]byte(`[{"id":1,"name":"api","status":"online"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west/pm2/api/logs":
			if request.URL.Query().Get("lines") != "500" {
				t.Errorf("managed log query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"logs":"remote ready\n"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/nodes/edge west/pm2/api/actions/restart":
			_, _ = writer.Write([]byte(`{"message":"process restarted"}`))
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
		{"pm2", "list"},
		{"pm2", "get", "api"},
		{"pm2", "logs", "--lines", "300", "api"},
		{"pm2", "action", "--confirm", "api", "reload"},
		{"pm2", "save", "--confirm"},
		{"pm2", "list", "--node", "edge west"},
		{"pm2", "logs", "--node", "edge west", "--lines", "500", "api"},
		{"pm2", "action", "--confirm", "--node", "edge west", "api", "restart"},
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

func TestRunPM2RejectsInvalidMutationsAndReadsBeforeRequest(t *testing.T) {
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
		{args: []string{"pm2", "action", "api", "restart"}, want: "explicit --confirm"},
		{args: []string{"pm2", "action", "--confirm", "--node", "edge-1", "api", "delete"}, want: "unsupported managed-node"},
		{args: []string{"pm2", "action", "--confirm", "api", "shell"}, want: "unsupported local"},
		{args: []string{"pm2", "action", "--confirm", "../api", "restart"}, want: "portable process name"},
		{args: []string{"pm2", "logs", "--lines", "5001", "api"}, want: "between 1 and 5000"},
		{args: []string{"pm2", "logs", "--node", "edge-1", "--lines", "501", "api"}, want: "between 1 and 500"},
		{args: []string{"pm2", "save"}, want: "explicit --confirm"},
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
