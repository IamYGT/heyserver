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

func dockerCLIEnvironment(key string) string {
	if key == "HSERVER_TOKEN" {
		return "docker-cli-token"
	}
	return ""
}

func TestRunDockerCLICommandsUseExactRoutesQueriesBodiesAndAuth(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if got := request.Header.Get("Authorization"); got != "Bearer docker-cli-token" {
			t.Errorf("authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.RequestURI == "/api/docker/containers/web-1/logs?tail=250":
			_, _ = writer.Write([]byte(`{"logs":"ready\n","tail":250,"truncated":false}`))
		case request.Method == http.MethodGet && request.RequestURI == "/api/docker/images":
			_, _ = writer.Write([]byte(`[{"id":"sha256:abc","repoTags":["nginx:1.27"],"size":"72 MB"}]`))
		case request.Method == http.MethodPost && request.RequestURI == "/api/docker/images/pull":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("pull body: %v", err)
			}
			if len(body) != 1 || body["name"] != "nginx:1.27" {
				t.Errorf("pull body = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"status":"ok","image":"nginx:1.27"}`))
		case request.Method == http.MethodDelete && request.RequestURI == "/api/docker/images/sha256:abc":
			_, _ = writer.Write([]byte(`{"status":"ok","image":"sha256:abc"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	commands := [][]string{
		{"containers", "logs", "--tail", "250", "web-1"},
		{"images", "list"},
		{"images", "pull", "--confirm", "nginx:1.27"},
		{"images", "delete", "--confirm", "sha256:abc"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, dockerCLIEnvironment); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
	}
	if requests.Load() != int32(len(commands)) {
		t.Fatalf("requests = %d, want %d", requests.Load(), len(commands))
	}
}

func TestRunDockerCLIMutationsAndLogsRejectInvalidInputBeforeNetwork(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"images", "pull", "nginx:1.27"}, want: "explicit --confirm"},
		{args: []string{"images", "delete", "--confirm", "../image"}, want: "invalid image reference"},
		{args: []string{"images", "pull", "--confirm", "--wait", "0s", "nginx:1.27"}, want: "greater than zero"},
		{args: []string{"containers", "logs", "--tail", "1001", "web"}, want: "between 1 and 1000"},
		{args: []string{"containers", "logs", "../web"}, want: "invalid container id format"},
	}
	for _, test := range cases {
		err := run(context.Background(), append([]string{"--server", server.URL}, test.args...), &bytes.Buffer{}, &bytes.Buffer{}, dockerCLIEnvironment)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s error = %v, want %q", strings.Join(test.args, " "), err, test.want)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}

func TestDockerCLIHelpAndCompletionExposeRoutesAndSafetyFlags(t *testing.T) {
	for _, test := range []struct {
		args      []string
		fragments []string
	}{
		{args: []string{"help", "containers"}, fragments: []string{"containers logs"}},
		{args: []string{"help", "images"}, fragments: []string{"images list", "images pull", "images delete"}},
		{args: []string{"images", "pull", "--help"}, fragments: []string{"images pull", "--confirm", "--wait"}},
	} {
		var output bytes.Buffer
		if err := run(context.Background(), test.args, &output, &bytes.Buffer{}, func(string) string {
			return ""
		}); err != nil {
			t.Fatalf("%v help: %v", test.args, err)
		}
		for _, fragment := range test.fragments {
			if !strings.Contains(output.String(), fragment) {
				t.Errorf("%v help missing %q: %s", test.args, fragment, output.String())
			}
		}
	}

	bash := generatedCompletionScript(t, "bash")
	for _, check := range []struct {
		words    []string
		expected string
	}{
		{words: []string{"hserverctl", "containers", ""}, expected: "logs"},
		{words: []string{"hserverctl", "containers", "logs", "--"}, expected: "--tail"},
		{words: []string{"hserverctl", "images", ""}, expected: "list"},
		{words: []string{"hserverctl", "images", "pull", "--"}, expected: "--confirm"},
		{words: []string{"hserverctl", "images", "delete", "--"}, expected: "--wait"},
	} {
		if !completionContains(runGeneratedBashCompletion(t, bash, check.words...), check.expected) {
			t.Errorf("completion for %q missing %q", check.words, check.expected)
		}
	}
}
