package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func processSignalTestEnv(key string) string {
	if key == "HSERVER_TOKEN" {
		return "process-test-token"
	}
	return ""
}

func TestProcessSignalRequiresConfirmationBeforeNetwork(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(writer, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"processes", "signal", "--node", "edge-1", "--pid", "42", "--start-time", "987654", "--signal", "term",
	}, &out, &bytes.Buffer{}, processSignalTestEnv)
	if err == nil {
		t.Fatal("process signal without --confirm unexpectedly succeeded")
	}
	for _, want := range []string{"edge-1", "PID 42", "start-time 987654", `signal "term"`, "--confirm"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if requests != 0 {
		t.Fatalf("confirmation refusal sent %d request(s)", requests)
	}
}

func TestProcessSignalRejectsInvalidSignalBeforeNetwork(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(writer, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL,
		"processes", "signal", "--node", "edge-1", "--pid", "42", "--start-time", "987654", "--signal", "hup", "--confirm",
	}, &bytes.Buffer{}, &bytes.Buffer{}, processSignalTestEnv)
	if err == nil {
		t.Fatal("invalid process signal unexpectedly succeeded")
	}
	for _, want := range []string{"edge-1", "PID 42", "start-time 987654", `signal "hup"`, "unsupported process signal", "term or kill"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid signal sent %d request(s)", requests)
	}
}

func TestProcessSignalSendsExactAuthenticatedRequestAndPrintsResponse(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", request.Method)
		}
		if request.URL.Path != "/api/nodes/edge west/processes/signal" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.RequestURI != "/api/nodes/edge%20west/processes/signal" {
			t.Errorf("request URI = %q", request.RequestURI)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer process-test-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(body), `{"pid":42,"startTime":987654,"signal":"kill"}`; got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
		var payload processSignalRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if payload != (processSignalRequest{PID: 42, StartTime: 987654, Signal: "kill"}) {
			t.Errorf("typed payload = %#v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"message":"KILL stopped worker","exited":true,"confirmed":true}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"processes", "signal", "--node", "edge west", "--pid", "42", "--start-time", "987654", "--signal", "kill", "--confirm", "--wait", "2s",
	}, &out, &bytes.Buffer{}, processSignalTestEnv)
	if err != nil {
		t.Fatalf("process signal returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if !json.Valid(out.Bytes()) {
		t.Fatalf("response is not JSON: %q", out.String())
	}
	for _, want := range []string{`"message": "KILL stopped worker"`, `"exited": true`, `"confirmed": true`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("response %q does not contain %q", out.String(), want)
		}
	}
}

func TestProcessSignalHelpAndCompletionExposeCanonicalFlags(t *testing.T) {
	for _, args := range [][]string{{"help", "processes", "signal"}, {"processes", "signal", "--help"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out, &bytes.Buffer{}, func(key string) string {
			if key == "HSERVER_TOKEN" {
				t.Fatal("help attempted to load the environment token")
			}
			return ""
		}); err != nil {
			t.Fatalf("%v help returned error: %v", args, err)
		}
		for _, want := range []string{
			"Usage: hserverctl processes signal --node NODE --pid PID --start-time START --signal term|kill --confirm [--wait DURATION]",
			"--node", "--pid", "--start-time", "--signal", "--confirm", "--wait", "Safety flags:",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v help output does not contain %q: %q", args, want, out.String())
			}
		}
	}

	bash := generatedCompletionScript(t, "bash")
	bashSyntax := exec.Command("bash", "-n")
	bashSyntax.Stdin = strings.NewReader(bash)
	if output, err := bashSyntax.CombinedOutput(); err != nil {
		t.Fatalf("generated bash completion syntax: %v\n%s", err, output)
	}
	if candidates := runGeneratedBashCompletion(t, bash, "hserverctl", "processes", ""); !completionContains(candidates, "signal") {
		t.Fatalf("processes completion = %v, missing signal", candidates)
	}
	for _, expected := range []string{"--node", "--pid", "--start-time", "--signal", "--confirm", "--wait"} {
		if candidates := runGeneratedBashCompletion(t, bash, "hserverctl", "processes", "signal", "--"); !completionContains(candidates, expected) {
			t.Fatalf("process signal flag completion = %v, missing %s", candidates, expected)
		}
	}
	for _, expected := range []string{"term", "kill"} {
		if candidates := runGeneratedBashCompletion(t, bash, "hserverctl", "processes", "signal", "--signal", ""); !completionContains(candidates, expected) {
			t.Fatalf("process signal enum completion = %v, missing %s", candidates, expected)
		}
	}
	for _, shell := range []string{"zsh", "fish"} {
		completion := generatedCompletionScript(t, shell)
		for _, fragment := range []string{"processes signal", "start-time", "term", "kill"} {
			if !strings.Contains(completion, fragment) {
				t.Errorf("%s completion does not contain %q", shell, fragment)
			}
		}
	}
}
