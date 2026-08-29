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

func TestRunCronCommandsUseObservedLocalAndManagedIdentities(t *testing.T) {
	t.Parallel()
	const (
		localID  = "0123456789abcdef"
		remoteID = "cron-0123456789ab"
		revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/cron/status":
			_, _ = writer.Write([]byte(`{"available":true,"installed":true,"running":true,"state":"healthy"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/cron/jobs":
			_, _ = writer.Write([]byte(`{"jobs":[{"id":"` + localID + `","user":"root","schedule":"0 3 * * *","command":"/usr/local/bin/backup","description":"Nightly","isActive":true}],"total":1}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/cron/system":
			_, _ = writer.Write([]byte(`{"files":[],"total":0}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/cron/jobs":
			assertCronPayload(t, request, "root", "0 3 * * *", "/usr/local/bin/backup", "Nightly", "")
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"job":{"id":"` + localID + `"},"message":"cron job created"}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/cron/jobs/"+localID:
			if request.URL.Query().Get("user") != "root" {
				t.Errorf("local update user = %q", request.URL.Query().Get("user"))
			}
			var payload struct {
				Schedule    string `json:"schedule"`
				Command     string `json:"command"`
				Description string `json:"description"`
				IsActive    bool   `json:"isActive"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode local update: %v", err)
			}
			if payload.Schedule != "15 4 * * *" || payload.Command != "/usr/local/bin/backup --fast" || payload.Description != "Updated" || payload.IsActive {
				t.Errorf("local update payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"message":"cron job updated"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/cron/jobs/"+localID:
			if request.URL.Query().Get("user") != "root" {
				t.Errorf("local delete user = %q", request.URL.Query().Get("user"))
			}
			_, _ = writer.Write([]byte(`{"id":"` + localID + `","message":"cron job deleted"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west/cron":
			_, _ = writer.Write([]byte(`{"service":"active","jobs":[{"id":"` + remoteID + `","schedule":"0 3 * * *","user":"root","command":"/usr/local/bin/backup","description":"Nightly","enabled":true}],"sources":[],"revision":"` + revision + `"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/nodes/edge west/cron":
			assertCronPayload(t, request, "root", "0 3 * * *", "/usr/local/bin/backup", "Nightly", revision)
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":"cron-abcdef012345","message":"Cron job created"}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/nodes/edge west/cron/"+remoteID:
			assertCronPayload(t, request, "deploy", "30 2 * * 1", "/usr/local/bin/report", "Weekly", revision)
			_, _ = writer.Write([]byte(`{"message":"Cron job updated"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/nodes/edge west/cron/"+remoteID:
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["revision"] != revision {
				t.Errorf("remote delete payload = %#v, err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"Cron job deleted"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/nodes/edge west/cron/"+remoteID+"/run":
			_, _ = writer.Write([]byte(`{"message":"Cron job completed","output":"ok\n"}`))
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
		{"cron", "status"},
		{"cron", "list"},
		{"cron", "system"},
		{"cron", "create", "--confirm", "--schedule", "0 3 * * *", "--command", "/usr/local/bin/backup", "--description", "Nightly"},
		{"cron", "update", "--confirm", "--schedule", "15 4 * * *", "--user", "root", "--command", "/usr/local/bin/backup --fast", "--description", "Updated", "--disabled", localID},
		{"cron", "delete", "--confirm", localID},
		{"cron", "list", "--node", "edge west"},
		{"cron", "create", "--confirm", "--node", "edge west", "--schedule", "0 3 * * *", "--command", "/usr/local/bin/backup", "--description", "Nightly"},
		{"cron", "update", "--confirm", "--node", "edge west", "--schedule", "30 2 * * 1", "--user", "deploy", "--command", "/usr/local/bin/report", "--description", "Weekly", remoteID},
		{"cron", "delete", "--confirm", "--node", "edge west", remoteID},
		{"cron", "run", "--confirm", "--node", "edge west", remoteID},
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
	if requests.Load() != 17 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func assertCronPayload(t *testing.T, request *http.Request, user, schedule, command, description, revision string) {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		t.Errorf("decode cron payload: %v", err)
		return
	}
	if payload["user"] != user || payload["schedule"] != schedule || payload["command"] != command || payload["description"] != description {
		t.Errorf("cron payload = %#v", payload)
	}
	if revision != "" && payload["revision"] != revision {
		t.Errorf("cron revision = %#v", payload["revision"])
	}
}

func TestRunCronRejectsUnsafeOrUnconfirmedInputBeforeRequest(t *testing.T) {
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
		{args: []string{"cron", "create", "--schedule", "0 3 * * *", "--command", "backup"}, want: "explicit --confirm"},
		{args: []string{"cron", "create", "--confirm", "--schedule", "61 3 * * *", "--command", "backup"}, want: "minute"},
		{args: []string{"cron", "create", "--confirm", "--node", "edge-1", "--schedule", "@daily", "--command", "backup"}, want: "exactly five fields"},
		{args: []string{"cron", "create", "--confirm", "--schedule", "0 3 * * *", "--user", "Root", "--command", "backup"}, want: "portable lowercase"},
		{args: []string{"cron", "create", "--confirm", "--schedule", "0 3 * * *", "--command", "backup", "--disabled"}, want: "managed-node cron creation"},
		{args: []string{"cron", "create", "--confirm", "--schedule", "0 3 * * *", "--command", "backup\nreboot"}, want: "control-free"},
		{args: []string{"cron", "update", "--confirm", "--schedule", "0 3 * * *", "--user", "root", "--command", "backup", "../job"}, want: "observed 16-character"},
		{args: []string{"cron", "delete", "--confirm", "--node", "edge-1", "../job"}, want: "observed cron- identity"},
		{args: []string{"cron", "run", "--confirm", "cron-0123456789ab"}, want: "--node NODE"},
		{args: []string{"cron", "run", "--node", "edge-1", "cron-0123456789ab"}, want: "explicit --confirm"},
		{args: []string{"cron", "delete", "--confirm", "--wait", "0s", "0123456789abcdef"}, want: "greater than zero"},
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

func TestCLIHelpAndCompletionExposeCronCommands(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"cron status", "cron list", "cron create", "cron update", "cron delete", "cron run"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q: %q", command, help.String())
		}
	}

	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "cron") {
		t.Fatalf("completion does not expose cron: %q", completion.String())
	}
}
