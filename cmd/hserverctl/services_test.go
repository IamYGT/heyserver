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

func TestRunServiceCommandsUseLocalAndManagedBoundaries(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/system/services":
			_, _ = writer.Write([]byte(`[{"name":"nginx","status":"active"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/system/services/postgresql/logs":
			if request.URL.Query().Get("lines") != "80" {
				t.Errorf("service log query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"service":"postgresql","lines":[{"message":"ready"}]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/system/actions/service":
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["service"] != "nginx" || payload["action"] != "restart" || len(payload) != 2 {
				t.Errorf("local service payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"message":"nginx restart completed"}`))
		case request.Method == http.MethodGet && request.RequestURI == "/api/nodes/edge%20west":
			_, _ = writer.Write([]byte(`{
				"id":"edge west","online":true,"capabilities":["service.status","service.action"],
				"inventory":{"services":[{"name":"nginx.service","active":"active","sub":"running"}]}
			}`))
		case request.Method == http.MethodPost && request.RequestURI == "/api/nodes/edge%20west/tasks":
			var payload struct {
				Kind    string            `json:"kind"`
				Payload map[string]string `json:"payload"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload.Kind != "service.action" || payload.Payload["service"] != "nginx.service" || payload.Payload["action"] != "stop" {
				t.Errorf("managed service payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"id":41,"node_id":"edge west","kind":"service.action","status":"queued"}`))
		case request.Method == http.MethodGet && request.RequestURI == "/api/nodes/edge%20west/tasks/41":
			_, _ = writer.Write([]byte(`{"id":41,"node_id":"edge west","kind":"service.action","status":"completed","result":{"active":"inactive"}}`))
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
		{args: []string{"services", "list"}, want: `"nginx"`},
		{args: []string{"services", "logs", "--lines", "80", "postgresql"}, want: `"ready"`},
		{args: []string{"services", "action", "--confirm", "nginx", "restart"}, want: `"nginx restart completed"`},
		{args: []string{"services", "list", "--node", "edge west"}, want: `"observationAvailable": true`},
		{args: []string{"services", "action", "--confirm", "--node", "edge west", "nginx.service", "stop"}, want: `"id": 41`},
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
	if requests.Load() != 7 {
		t.Fatalf("requests = %d want 7", requests.Load())
	}
}

func TestRunServiceCommandsRejectInvalidInputBeforeRequest(t *testing.T) {
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
		{args: []string{"services", "logs", "bad service"}, want: "portable systemd"},
		{args: []string{"services", "logs", "--lines", "501", "nginx"}, want: "between 1 and 500"},
		{args: []string{"services", "action", "nginx", "restart"}, want: "explicit --confirm"},
		{args: []string{"services", "action", "--confirm", "../nginx", "restart"}, want: "portable systemd"},
		{args: []string{"services", "action", "--confirm", "nginx", "reload"}, want: "unsupported service action"},
		{args: []string{"services", "action", "--confirm", "--wait", "8m", "nginx", "restart"}, want: "at most 7m"},
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

func TestRunManagedServiceActionRejectsUnavailableTargetBeforeMutation(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			posts.Add(1)
			http.Error(writer, "unexpected mutation", http.StatusInternalServerError)
			return
		}
		reads.Add(1)
		if strings.Contains(request.RequestURI, "offline") {
			_, _ = writer.Write([]byte(`{"id":"offline","online":false,"capabilities":["service.action"],"inventory":{}}`))
			return
		}
		if strings.Contains(request.RequestURI, "mismatch") {
			_, _ = writer.Write([]byte(`{"id":"different-node","online":true,"capabilities":["service.action"],"inventory":{}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"id":"read-only","online":true,"capabilities":["service.status"],"inventory":{}}`))
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}

	for _, item := range []struct {
		node string
		want string
	}{
		{node: "offline", want: "offline"},
		{node: "read-only", want: "does not advertise service.action"},
		{node: "mismatch", want: "does not match requested node"},
	} {
		err := run(context.Background(), []string{
			"--server", server.URL, "services", "action", "--confirm", "--node", item.node, "nginx.service", "restart",
		}, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("node=%s error=%v", item.node, err)
		}
	}
	if reads.Load() != 3 || posts.Load() != 0 {
		t.Fatalf("reads=%d posts=%d", reads.Load(), posts.Load())
	}
}

func TestCLIHelpAndCompletionExposeServiceCommands(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"services list", "services logs", "services action"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q", command)
		}
	}
	if strings.Contains(help.String(), "\n\t  dns status") {
		t.Fatal("help retains accidental DNS indentation")
	}

	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "services") {
		t.Fatalf("completion does not expose services: %q", completion.String())
	}
}
