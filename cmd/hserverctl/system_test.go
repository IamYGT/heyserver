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

func TestRunSystemInfoAndStatsUseAuthenticatedGET(t *testing.T) {
	t.Parallel()
	for _, command := range []struct {
		name     string
		endpoint string
	}{
		{name: "info", endpoint: systemInfoEndpoint},
		{name: "stats", endpoint: systemStatsEndpoint},
	} {
		command := command
		t.Run(command.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != command.endpoint {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer system-token" {
					t.Errorf("Authorization = %q", got)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			var out bytes.Buffer
			if err := run(context.Background(), []string{"--server", server.URL, "system", command.name}, &out, &bytes.Buffer{}, systemTestEnvironment); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"ok": true`) {
				t.Fatalf("output = %s", out.String())
			}
		})
	}
}

func TestRunSystemProcessActionUsesExactRouteAndIdentityBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != systemProcessActionEndpoint {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer system-token" {
			t.Errorf("Authorization = %q", got)
		}
		var body struct {
			PID       int    `json:"pid"`
			StartTime uint64 `json:"startTime"`
			Signal    string `json:"signal"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.PID != 1234 || body.StartTime != 987654 || body.Signal != "term" {
			t.Errorf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"message":"signal sent"}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "system", "actions", "process",
		"--pid", "1234", "--start-time", "987654", "--signal", "TERM", "--confirm",
	}, &out, &bytes.Buffer{}, systemTestEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "signal sent") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestRunSystemProcessActionRejectsInvalidInputBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "confirmation", args: []string{"--pid", "2", "--start-time", "1", "--signal", "term"}, want: "--confirm"},
		{name: "pid", args: []string{"--pid", "1", "--start-time", "1", "--signal", "term", "--confirm"}, want: "PID"},
		{name: "start time", args: []string{"--pid", "2", "--start-time", "0", "--signal", "term", "--confirm"}, want: "start time"},
		{name: "signal", args: []string{"--pid", "2", "--start-time", "1", "--signal", "stop", "--confirm"}, want: "term or kill"},
		{name: "wait", args: []string{"--pid", "2", "--start-time", "1", "--signal", "kill", "--confirm", "--wait", "0s"}, want: "wait"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--server", server.URL, "system", "actions", "process"}, test.args...)
			err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, systemTestEnvironment)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunSystemHelpAndCompletion(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.String(), "system actions process --pid PID --start-time START --signal term|kill --confirm") {
		t.Fatalf("help missing system process action:\n%s", help.String())
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var completion bytes.Buffer
		if err := run(context.Background(), []string{"completion", shell}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
			t.Fatal(err)
		}
		for _, fragment := range []string{"system", "info", "stats", "actions", "process", "term", "kill"} {
			if !strings.Contains(completion.String(), fragment) {
				t.Errorf("%s completion missing %q", shell, fragment)
			}
		}
		flagFragment := "--start-time"
		if shell == "fish" {
			flagFragment = "-l start-time"
		}
		if !strings.Contains(completion.String(), flagFragment) {
			t.Errorf("%s completion missing %q", shell, flagFragment)
		}
	}
}

func systemTestEnvironment(key string) string {
	if key == "HSERVER_TOKEN" {
		return "system-token"
	}
	return ""
}
