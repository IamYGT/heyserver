package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunNotifyChannelLifecycleUsesProtectedCredentialFiles(t *testing.T) {
	t.Parallel()
	secretDir := t.TempDir()
	passwordFile := filepath.Join(secretDir, "smtp-password")
	updatedPasswordFile := filepath.Join(secretDir, "smtp-password-next")
	if err := os.WriteFile(passwordFile, []byte("first-private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(updatedPasswordFile, []byte("second-private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		redacted := `{"id":1,"name":"Primary email","type":"email","config":"{\"smtp_host\":\"smtp.example.com\",\"smtp_port\":\"587\",\"smtp_user\":\"ops\",\"from_address\":\"alerts@example.com\",\"to_address\":\"admin@example.com\",\"secret_configured\":true}","enabled":true}`
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/notify/channels":
			_, _ = io.WriteString(writer, "["+redacted+"]")
		case request.Method == http.MethodGet && request.URL.Path == "/api/notify/channels/1":
			_, _ = io.WriteString(writer, redacted)
		case request.Method == http.MethodPost && request.URL.Path == "/api/notify/channels":
			var payload struct {
				Name    string `json:"name"`
				Type    string `json:"type"`
				Config  string `json:"config"`
				Enabled bool   `json:"enabled"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode create: %v", err)
			}
			var config map[string]any
			if err := json.Unmarshal([]byte(payload.Config), &config); err != nil {
				t.Errorf("decode create config: %v", err)
			}
			if payload.Name != "Primary email" || payload.Type != "email" || !payload.Enabled ||
				config["password"] != "first-private-value" || config["host"] != "smtp.example.com" ||
				config["port"] != float64(587) || config["tls"] != false {
				t.Errorf("create payload = %#v config=%#v", payload, config)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(writer, redacted)
		case request.Method == http.MethodPut && request.URL.Path == "/api/notify/channels/1":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode update: %v", err)
			}
			var config map[string]any
			if err := json.Unmarshal([]byte(payload["config"].(string)), &config); err != nil {
				t.Errorf("decode update config: %v", err)
			}
			if payload["name"] != "Primary email" || payload["enabled"] != false ||
				config["password"] != "second-private-value" || config["host"] != "smtp2.example.com" {
				t.Errorf("update payload = %#v config=%#v", payload, config)
			}
			_, _ = io.WriteString(writer, redacted)
		case request.Method == http.MethodPost && request.URL.Path == "/api/notify/channels/1/test":
			_, _ = io.WriteString(writer, `{"status":"sent"}`)
		case request.Method == http.MethodDelete && request.URL.Path == "/api/notify/channels/1":
			_, _ = io.WriteString(writer, `{"status":"deleted"}`)
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
		{"notify", "channels"},
		{"notify", "channel", "1"},
		{"notify", "create", "--confirm", "--name", "Primary email", "--type", "email",
			"--smtp-host", "smtp.example.com", "--smtp-port", "587", "--from", "alerts@example.com",
			"--to", "admin@example.com", "--smtp-tls", "false", "--credential-file", passwordFile},
		{"notify", "update", "--confirm", "--enabled", "false", "--smtp-host", "smtp2.example.com",
			"--credential-file", updatedPasswordFile, "1"},
		{"notify", "test", "--confirm", "1"},
		{"notify", "delete", "--confirm", "1"},
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
		if strings.Contains(output.String(), "private-value") {
			t.Fatalf("%s exposed a credential: %q", strings.Join(command, " "), output.String())
		}
	}
	if requests.Load() != 7 {
		t.Fatalf("requests = %d, want 7", requests.Load())
	}
}

func TestRunNotifyRejectsUnsafeOrUnconfirmedInputBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	credential := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credential, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
		{args: []string{"notify", "create", "--name", "Ops", "--type", "telegram", "--chat-id", "-1001"}, want: "explicit --confirm"},
		{args: []string{"notify", "create", "--confirm", "--name", "Ops", "--type", "telegram", "--chat-id", "-1001", "--credential-file", credential}, want: "accessible by group"},
		{args: []string{"notify", "create", "--confirm", "--name", "Ops", "--type", "telegram", "--chat-id", "-1001", "--smtp-host", "smtp.example.com"}, want: "not valid for a telegram"},
		{args: []string{"notify", "update", "--confirm", "1"}, want: "at least one changed field"},
		{args: []string{"notify", "update", "--confirm", "--credential-file", credential, "--clear-credential", "1"}, want: "cannot be combined"},
		{args: []string{"notify", "channel", "01"}, want: "positive canonical integer"},
		{args: []string{"notify", "test", "1"}, want: "explicit --confirm"},
		{args: []string{"notify", "delete", "--confirm", "--wait", "0s", "1"}, want: "greater than zero"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v, want %q", strings.Join(item.args, " "), err, item.want)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected notify commands sent %d request(s)", requests.Load())
	}
}

func TestBuildNotifyConfigKeepsWebhookOutOfArgumentsAndValidatesTypeFields(t *testing.T) {
	t.Parallel()
	credential := filepath.Join(t.TempDir(), "webhook")
	if err := os.WriteFile(credential, []byte("https://hooks.example.com/services/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := &notifyMutationOptions{
		CredentialFile: credential,
		Username:       "Heyserver",
		Channel:        "#ops",
		visited: map[string]bool{
			"confirm": true, "name": true, "type": true, "credential-file": true,
			"username": true, "channel": true,
		},
	}
	raw, err := buildNotifyConfig("slack", options, true)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	if config["webhookUrl"] != "https://hooks.example.com/services/test" ||
		config["username"] != "Heyserver" || config["channel"] != "#ops" {
		t.Fatalf("config = %#v", config)
	}
	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "notify") {
		t.Fatalf("completion does not expose notify: %q", completion.String())
	}
}

func TestRunNotifyAlertRuleLifecycleUsesCanonicalContracts(t *testing.T) {
	t.Parallel()
	const ruleJSON = `{"id":4,"name":"CPU pressure","type":"cpu_usage","threshold":90,"durationMins":5,"target":"","enabled":true,"cooldownMins":30}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/notify/rules":
			_, _ = io.WriteString(writer, "["+ruleJSON+"]")
		case request.Method == http.MethodGet && request.URL.Path == "/api/notify/rules/4":
			_, _ = io.WriteString(writer, ruleJSON)
		case request.Method == http.MethodPost && request.URL.Path == "/api/notify/rules":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode create: %v", err)
			}
			if payload["name"] != "CPU pressure" || payload["type"] != "cpu_usage" || payload["threshold"] != float64(90) ||
				payload["durationMins"] != float64(5) || payload["cooldownMins"] != float64(30) || payload["enabled"] != true {
				t.Errorf("create payload = %#v", payload)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(writer, ruleJSON)
		case request.Method == http.MethodPut && request.URL.Path == "/api/notify/rules/4":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode update: %v", err)
			}
			if len(payload) != 3 || payload["type"] != "service_down" || payload["target"] != "nginx.service" || payload["enabled"] != false {
				t.Errorf("partial update payload = %#v", payload)
			}
			_, _ = io.WriteString(writer, `{"id":4,"name":"CPU pressure","type":"service_down","threshold":1,"durationMins":5,"target":"nginx.service","enabled":false,"cooldownMins":30}`)
		case request.Method == http.MethodDelete && request.URL.Path == "/api/notify/rules/4":
			_, _ = io.WriteString(writer, `{"status":"deleted"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/notify/history":
			if request.URL.RawQuery != "limit=20&offset=5" {
				t.Errorf("history query = %q", request.URL.RawQuery)
			}
			_, _ = io.WriteString(writer, `{"items":[],"total":0,"limit":20,"offset":5}`)
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
		{"notify", "rules"},
		{"notify", "rule", "4"},
		{"notify", "rule-create", "--confirm", "--name", "CPU pressure", "--type", "cpu", "--threshold", "90", "--duration-mins", "5", "--cooldown-mins", "30"},
		{"notify", "rule-update", "--confirm", "--type", "service_down", "--target", "nginx.service", "--enabled", "false", "4"},
		{"notify", "rule-delete", "--confirm", "4"},
		{"notify", "history", "--limit", "20", "--offset", "5"},
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
	if requests.Load() != 7 {
		t.Fatalf("requests = %d, want 7", requests.Load())
	}
}

func TestRunNotifyAlertRulesRejectInvalidInputBeforeRequest(t *testing.T) {
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
		{args: []string{"notify", "rule-create", "--name", "CPU", "--type", "cpu", "--threshold", "90"}, want: "explicit --confirm"},
		{args: []string{"notify", "rule-create", "--confirm", "--name", "CPU", "--type", "cpu"}, want: "requires --threshold"},
		{args: []string{"notify", "rule-create", "--confirm", "--name", "SSL", "--type", "ssl_expiry", "--threshold", "14", "--target", "../example.com"}, want: "valid DNS name"},
		{args: []string{"notify", "rule-create", "--confirm", "--name", "Service", "--type", "service_down", "--target", "--user.service"}, want: "valid systemd unit"},
		{args: []string{"notify", "rule-create", "--confirm", "--name", "CPU", "--type", "cpu", "--threshold", "NaN"}, want: "finite"},
		{args: []string{"notify", "rule-update", "--confirm", "1"}, want: "at least one changed field"},
		{args: []string{"notify", "rule-update", "--confirm", "--type", "unknown", "1"}, want: "alert rule type must be"},
		{args: []string{"notify", "rule", "01"}, want: "positive canonical integer"},
		{args: []string{"notify", "rule-delete", "4"}, want: "explicit --confirm"},
		{args: []string{"notify", "rule-delete", "--confirm", "--wait", "0s", "4"}, want: "greater than zero"},
		{args: []string{"notify", "history", "--limit", "0"}, want: "canonical integer"},
		{args: []string{"notify", "history", "--limit", "201"}, want: "canonical integer"},
		{args: []string{"notify", "history", "--limit", "01"}, want: "canonical integer"},
		{args: []string{"notify", "history", "--offset", "-1"}, want: "canonical integer"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v, want %q", strings.Join(item.args, " "), err, item.want)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected alert rule commands sent %d request(s)", requests.Load())
	}
}
