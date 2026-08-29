package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunNodeEnrollWritesProtectedTokenAndSafeEnvironment(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	tokenFile := filepath.Join(directory, "agent.token")
	environmentFile := filepath.Join(directory, "agent.env")
	clientToken := filepath.Join(directory, "panel.token")
	if err := os.WriteFile(clientToken, []byte("panel-bearer\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/nodes" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer panel-bearer" {
			t.Errorf("Authorization = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if got, want := string(body), `{"id":"edge-1","name":"Production edge"}`; got != want {
			t.Errorf("request body = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"node":{"id":"edge-1","name":"Production edge"},"token":"one-time-agent-token"}`)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"--token-file", clientToken,
		"nodes", "enroll", "--confirm", "--id", "edge-1", "--name", "Production edge",
		"--agent-token-output", tokenFile, "--agent-env-output", environmentFile,
	}, &stdout, &stderr, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "one-time-agent-token") {
		t.Fatalf("token leaked to stdout: %q", stdout.String())
	}
	for _, value := range []string{"edge-1", "Production edge", tokenFile, environmentFile} {
		if !strings.Contains(stdout.String(), value) {
			t.Errorf("stdout %q does not contain %q", stdout.String(), value)
		}
	}

	data, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "one-time-agent-token\n"; got != want {
		t.Fatalf("token file = %q, want %q", got, want)
	}
	if info, err := os.Stat(tokenFile); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("token mode = %o, want 600", got)
	}

	environment, err := os.ReadFile(environmentFile)
	if err != nil {
		t.Fatal(err)
	}
	envText := string(environment)
	for _, line := range []string{
		"HSERVER_AGENT_HUB_URL=" + server.URL,
		"HSERVER_AGENT_NODE_ID=edge-1",
		"HSERVER_AGENT_TOKEN_FILE=/etc/hserver-agent.token",
		"HSERVER_AGENT_INTERVAL=30s",
		"HSERVER_AGENT_ALLOW_PROCESS_SIGNALS=false",
		"HSERVER_AGENT_ALLOW_TERMINAL=false",
		"HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=false",
		"HSERVER_AGENT_ALLOW_FIREWALL_WRITE=false",
	} {
		if !strings.Contains(envText, line) {
			t.Errorf("environment missing %q", line)
		}
	}
	if strings.Contains(envText, "one-time-agent-token") || strings.Contains(envText, "HSERVER_AGENT_TOKEN=") {
		t.Fatalf("environment contains token material: %q", envText)
	}
	if info, err := os.Stat(environmentFile); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("environment mode = %o, want 600", got)
	}
}

func TestRunNodeEnrollPreservesTypedAPIErrorAndCleansReservations(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	clientToken := filepath.Join(directory, "panel.token")
	if err := os.WriteFile(clientToken, []byte("panel-bearer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(directory, "agent.token")
	environmentFile := filepath.Join(directory, "agent.env")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"node already exists"}`)
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL, "--token-file", clientToken,
		"nodes", "enroll", "--confirm", "--id", "edge-1", "--name", "Production edge",
		"--agent-token-output", tokenFile, "--agent-env-output", environmentFile,
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" })
	if err == nil || err.Error() != "HTTP 409: node already exists" {
		t.Fatalf("error = %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	for _, path := range []string{tokenFile, environmentFile} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Errorf("reserved path %s remains: %v", path, statErr)
		}
	}
}

func TestRunNodeEnrollRejectsInvalidInputsBeforeHTTP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		prepare     func(t *testing.T, directory string) (tokenPath, environmentPath string)
		id          string
		displayName string
		want        string
	}{
		{
			name: "missing confirmation",
			prepare: func(_ *testing.T, directory string) (string, string) {
				return filepath.Join(directory, "token"), filepath.Join(directory, "env")
			},
			id: "edge-1", displayName: "Edge", want: "requires explicit --confirm",
		},
		{
			name: "invalid node id",
			prepare: func(_ *testing.T, directory string) (string, string) {
				return filepath.Join(directory, "token"), filepath.Join(directory, "env")
			},
			id: "edge west", displayName: "Edge", want: "node ID",
		},
		{
			name: "blank name",
			prepare: func(_ *testing.T, directory string) (string, string) {
				return filepath.Join(directory, "token"), filepath.Join(directory, "env")
			},
			id: "edge-1", displayName: "  ", want: "node name",
		},
		{
			name: "existing target",
			prepare: func(t *testing.T, directory string) (string, string) {
				token := filepath.Join(directory, "token")
				if err := os.WriteFile(token, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				return token, filepath.Join(directory, "env")
			},
			id: "edge-1", displayName: "Edge", want: "already exists",
		},
		{
			name: "symlink target",
			prepare: func(t *testing.T, directory string) (string, string) {
				token := filepath.Join(directory, "token")
				if err := os.Symlink(filepath.Join(directory, "missing"), token); err != nil {
					t.Fatal(err)
				}
				return token, filepath.Join(directory, "env")
			},
			id: "edge-1", displayName: "Edge", want: "symlink",
		},
		{
			name: "same path",
			prepare: func(_ *testing.T, directory string) (string, string) {
				path := filepath.Join(directory, "token")
				return path, path
			},
			id: "edge-1", displayName: "Edge", want: "distinct",
		},
		{
			name: "absent parent",
			prepare: func(_ *testing.T, directory string) (string, string) {
				return filepath.Join(directory, "missing", "token"), filepath.Join(directory, "env")
			},
			id: "edge-1", displayName: "Edge", want: "does not exist",
		},
		{
			name: "non-directory parent",
			prepare: func(t *testing.T, directory string) (string, string) {
				parent := filepath.Join(directory, "parent")
				if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(parent, "token"), filepath.Join(directory, "env")
			},
			id: "edge-1", displayName: "Edge", want: "not a directory",
		},
		{
			name: "symlink parent",
			prepare: func(t *testing.T, directory string) (string, string) {
				target := filepath.Join(directory, "real")
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				parent := filepath.Join(directory, "link")
				if err := os.Symlink(target, parent); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(parent, "token"), filepath.Join(directory, "env")
			},
			id: "edge-1", displayName: "Edge", want: "symlink",
		},
		{
			name: "not writable parent",
			prepare: func(t *testing.T, directory string) (string, string) {
				parent := filepath.Join(directory, "readonly")
				if err := os.Mkdir(parent, 0o555); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(parent, "token"), filepath.Join(directory, "env")
			},
			id: "edge-1", displayName: "Edge", want: "not writable",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusCreated)
			}))
			defer server.Close()
			clientToken := filepath.Join(directory, "panel.token")
			if err := os.WriteFile(clientToken, []byte("panel-bearer\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			tokenPath, environmentPath := test.prepare(t, directory)
			args := []string{
				"--server", server.URL, "--token-file", clientToken,
				"nodes", "enroll", "--id", test.id, "--name", test.displayName,
				"--agent-token-output", tokenPath, "--agent-env-output", environmentPath,
			}
			if test.name != "missing confirmation" {
				args = append(args, "--confirm")
			}
			err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" })
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("requests = %d, want 0", got)
			}
		})
	}
}

func TestDecodeNodeEnrollResponseRejectsWrongIdentityWithoutExposingToken(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"node":{"id":"other","name":"Edge"},"token":"secret-value"}`,
		`{"node":{"id":"edge-1","name":"Edge"},"token":""}`,
		`{"node":{"id":"edge-1","name":"Edge"},"token":" secret-value "}`,
	} {
		raw := raw
		t.Run(fmt.Sprintf("response-%d", len(raw)), func(t *testing.T) {
			t.Parallel()
			_, err := decodeNodeEnrollResponse([]byte(raw), "edge-1")
			if err == nil {
				t.Fatal("decode unexpectedly succeeded")
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("token leaked in error: %v", err)
			}
		})
	}
}

func TestNodeEnrollEnvironmentIsValidAgentConfigWithoutMutationDefaults(t *testing.T) {
	t.Parallel()
	environment := nodeEnrollEnvironment("https://panel.example.test", "edge-1")
	lines := strings.Split(string(environment), "\n")
	values := make(map[string]string)
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			t.Fatalf("invalid environment line %q", line)
		}
		values[key] = value
	}
	if got := values["HSERVER_AGENT_HUB_URL"]; got != "https://panel.example.test" {
		t.Fatalf("hub URL = %q", got)
	}
	if got := values["HSERVER_AGENT_NODE_ID"]; got != "edge-1" {
		t.Fatalf("node ID = %q", got)
	}
	for _, key := range []string{
		"HSERVER_AGENT_ALLOW_PROCESS_SIGNALS",
		"HSERVER_AGENT_ALLOW_TERMINAL",
		"HSERVER_AGENT_ALLOW_CONTAINER_READ",
		"HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ",
		"HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE",
		"HSERVER_AGENT_ALLOW_DOMAIN_READ",
		"HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS",
		"HSERVER_AGENT_ALLOW_SSL_READ",
		"HSERVER_AGENT_ALLOW_SSL_ACTIONS",
		"HSERVER_AGENT_ALLOW_DATABASE_READ",
		"HSERVER_AGENT_ALLOW_BACKUP_READ",
		"HSERVER_AGENT_ALLOW_BACKUP_RUN",
		"HSERVER_AGENT_ALLOW_DEPLOY_READ",
		"HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS",
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ",
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS",
		"HSERVER_AGENT_ALLOW_UPDATE_READ",
		"HSERVER_AGENT_ALLOW_UPDATE_ACTIONS",
		"HSERVER_AGENT_ALLOW_PHP_CONFIG_READ",
		"HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE",
		"HSERVER_AGENT_ALLOW_PM2_READ",
		"HSERVER_AGENT_ALLOW_CRON_READ",
		"HSERVER_AGENT_ALLOW_CRON_WRITE",
		"HSERVER_AGENT_ALLOW_CRON_RUN",
		"HSERVER_AGENT_ALLOW_FIREWALL_READ",
		"HSERVER_AGENT_ALLOW_FIREWALL_WRITE",
	} {
		if got := values[key]; got != "false" {
			t.Errorf("%s = %q, want false", key, got)
		}
	}
	for _, key := range []string{
		"HSERVER_AGENT_TOKEN",
		"HSERVER_AGENT_DEPLOY_PLANS_FILE",
		"HSERVER_AGENT_DEPLOY_ACME_WEBROOT",
		"HSERVER_AGENT_UPDATE_MANIFEST_URL",
		"HSERVER_AGENT_IPTABLES_BINARY",
	} {
		if _, present := values[key]; present {
			t.Errorf("provider/secret key %s unexpectedly present", key)
		}
	}
	if strings.Contains(string(environment), "HSERVER_AGENT_TOKEN=") {
		t.Fatal("environment contains bearer-token key")
	}
}

func TestNodeEnrollOutputReservationKeepsCompletedTokenAfterEnvironmentFailure(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "token")
	environmentPath := filepath.Join(directory, "environment")
	reservations, err := reserveNodeEnrollOutputs(tokenPath, environmentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := reservations[0].write([]byte("completed-token\n")); err != nil {
		t.Fatal(err)
	}
	if err := reservations[1].file.Close(); err != nil {
		t.Fatal(err)
	}
	reservations[1].file = nil
	if err := reservations[1].write([]byte("env\n")); err == nil {
		t.Fatal("environment write unexpectedly succeeded")
	}
	if err := cleanupNodeEnrollReservations(reservations); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(tokenPath); err != nil || string(data) != "completed-token\n" {
		t.Fatalf("completed token = %q, err = %v", data, err)
	}
	if _, err := os.Lstat(environmentPath); !os.IsNotExist(err) {
		t.Fatalf("failed environment remains: %v", err)
	}
}
