package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewAPIClientValidatesServerURL(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"ftp://example.com",
		"https://user:pass@example.com",
		"https://example.com/panel",
		"https://example.com?token=value",
		"https://example.com#fragment",
	} {
		rawURL := rawURL
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()
			if _, err := newAPIClient(rawURL, "", time.Second); err == nil {
				t.Fatalf("newAPIClient(%q) unexpectedly succeeded", rawURL)
			}
		})
	}
}

func TestRunNodesGetUsesTokenAndEscapesNodeID(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("test-bearer\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "/api/nodes/edge%20west" {
			t.Errorf("RequestURI = %q", r.RequestURI)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-bearer" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"edge west"}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"--token-file", tokenFile,
		"nodes", "get", "edge west",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"id": "edge west"`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunWhoamiShowsAuthenticatedAccount(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("test-bearer\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/auth/me" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-bearer" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"Operator","email":"operator@example.com","role":"admin"}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"--token-file", tokenFile,
		"whoami",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"role": "admin"`) || strings.Contains(out.String(), "test-bearer") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunLogoutRemovesOnlyStoredRegularToken(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	tokenFile := filepath.Join(directory, "token")
	if err := os.WriteFile(tokenFile, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--token-file", tokenFile,
		"logout",
	}, &out, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "environment-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(tokenFile); !os.IsNotExist(err) {
		t.Fatalf("token file remains: %v", err)
	}
	if strings.Contains(out.String(), "secret-token") || strings.Contains(out.String(), "environment-token") {
		t.Fatalf("token leaked to output: %q", out.String())
	}
	if !strings.Contains(out.String(), "Removed stored Heyserver token") || !strings.Contains(out.String(), "HSERVER_TOKEN remains active") {
		t.Fatalf("output = %q", out.String())
	}

	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "token-link")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	err = run(context.Background(), []string{
		"--token-file", symlink,
		"logout",
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink logout error = %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep\n" {
		t.Fatalf("symlink target changed: data=%q err=%v", data, err)
	}
}

func TestLoadTokenFallsBackToEnvironmentWhenDefaultFileIsAbsent(t *testing.T) {
	t.Parallel()
	token, err := loadToken(filepath.Join(t.TempDir(), "missing"), "environment-token")
	if err != nil {
		t.Fatal(err)
	}
	if token != "environment-token" {
		t.Fatalf("token = %q", token)
	}
}

func TestRunLoginStoresProtectedTokenWithoutPrintingIt(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	passwordFile := filepath.Join(directory, "password")
	tokenFile := filepath.Join(directory, "config", "token")
	if err := os.WriteFile(passwordFile, []byte("correct horse\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/login" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["email"] != "admin@example.com" || request["password"] != "correct horse" {
			t.Fatalf("unexpected login request: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "secret-token-value"})
	}))
	defer server.Close()

	var out bytes.Buffer
	args := []string{
		"--server", server.URL,
		"--token-file", tokenFile,
		"login", "--email", "admin@example.com", "--password-file", passwordFile,
	}
	if err := run(context.Background(), args, &out, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "secret-token-value") {
		t.Fatalf("token leaked to output: %q", out.String())
	}
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "secret-token-value\n" {
		t.Fatalf("token file = %q", data)
	}
	info, err := os.Stat(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("token mode = %o", got)
	}
	if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second login error = %v", err)
	}
	args = append(args, "--replace")
	if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatalf("replace login: %v", err)
	}
}

func TestRunLoginCompletesTOTPChallenge(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	passwordFile := filepath.Join(directory, "password")
	totpFile := filepath.Join(directory, "totp")
	tokenFile := filepath.Join(directory, "token")
	for path, value := range map[string]string{passwordFile: "password\n", totpFile: "123456\n"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]any{"requires_totp": true})
		case "/api/auth/totp-verify":
			var request map[string]string
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request["code"] != "123456" {
				t.Errorf("TOTP code = %q", request["code"])
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "totp-token"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL, "--token-file", tokenFile,
		"login", "--email", "admin@example.com", "--password-file", passwordFile, "--totp-file", totpFile,
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(tokenFile); err != nil || string(data) != "totp-token\n" {
		t.Fatalf("token data = %q, err = %v", data, err)
	}
}

func TestRunTaskCreateReturnsServerConflict(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes/node-a/tasks" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var request struct {
			Kind      string            `json:"kind"`
			Payload   map[string]string `json:"payload"`
			Confirmed bool              `json:"confirmed"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Kind != "process.signal" || request.Payload["pid"] != "12" || !request.Confirmed {
			t.Errorf("request = %#v", request)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"managed node is offline"}`))
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL, "--token-file", tokenFile,
		"tasks", "create", "--confirm", "--kind", "process.signal", "--payload", "pid=12", "node-a",
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" })
	if err == nil || err.Error() != "HTTP 409: managed node is offline" {
		t.Fatalf("error = %v", err)
	}
}

func TestRunTaskCreateRequiresTargetConfirmationBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL,
		"tasks", "create", "--kind", "service.status", "--payload", "service=nginx.service", "edge-west",
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), `managed node "edge-west"`) || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestRunHostActionRequiresConfirmation(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "unexpected"})
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL, "host", "action", "swap-reset",
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "explicit --confirm") {
		t.Fatalf("error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestRunManagedNodeActionUsesBoundedEndpointAndLongWait(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.RequestURI != "/api/nodes/edge%20west/actions/memory-optimize" {
			t.Errorf("request = %s %s", r.Method, r.RequestURI)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		time.Sleep(20 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "memory optimized"})
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "--timeout", "1ms",
		"nodes", "action", "--confirm", "--wait", "1s", "edge west", "memory-optimize",
	}, &out, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "memory optimized") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunDiskScanAndManagedCleanup(t *testing.T) {
	t.Parallel()
	var cleanupRequest struct {
		Targets   []string `json:"targets"`
		Confirmed bool     `json:"confirmed"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/disk/cleanup/scan":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "journal", "size": 1024}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/nodes/edge-1/disk/cleanup":
			if err := json.NewDecoder(r.Body).Decode(&cleanupRequest); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "token"
		}
		return ""
	}

	var scanOut bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "disk", "scan",
	}, &scanOut, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scanOut.String(), `"id": "journal"`) {
		t.Fatalf("scan output = %q", scanOut.String())
	}
	if err := run(context.Background(), []string{
		"--server", server.URL, "disk", "clean", "--confirm", "--node", "edge-1",
		"--target", "journal", "--target", "package-cache",
	}, &bytes.Buffer{}, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	if !cleanupRequest.Confirmed || strings.Join(cleanupRequest.Targets, ",") != "journal,package-cache" {
		t.Fatalf("cleanup request = %#v", cleanupRequest)
	}
}

func TestRunDiskCleanupRejectsDuplicateTargetBeforeRequest(t *testing.T) {
	t.Parallel()
	err := run(context.Background(), []string{
		"--server", "http://127.0.0.1:1", "disk", "clean", "--confirm",
		"--target", "journal", "--target", "journal",
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate cleanup target") {
		t.Fatalf("error = %v", err)
	}
}

func TestParsePayloadRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()
	if _, err := parsePayload([]string{"pid=12", "pid=13"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunCompletion(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{"completion", shell}, &out, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
			t.Fatalf("%s completion: %v", shell, err)
		}
		if !strings.Contains(out.String(), "hserverctl") || !strings.Contains(out.String(), "tasks") || !strings.Contains(out.String(), "context") || !strings.Contains(out.String(), "connect") || !strings.Contains(out.String(), "terminal") || !strings.Contains(out.String(), "doctor") || !strings.Contains(out.String(), "updates") {
			t.Fatalf("%s completion = %q", shell, out.String())
		}
	}
}

func TestRunCompletionIncludesNestedCommandsAndFlags(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"bash", "zsh", "fish"} {
		shell := shell
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if err := run(context.Background(), []string{"completion", shell}, &out, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
				t.Fatalf("%s completion: %v", shell, err)
			}
			completion := out.String()
			for _, fragment := range []string{
				"updates agent",
				"status upgrade rollback",
				"deploy domain",
				"create health tls delete",
				"backups snapshot",
				"status list vhosts run restore destination purge",
			} {
				if !strings.Contains(completion, fragment) {
					t.Errorf("%s completion does not contain %q: %s", shell, fragment, completion)
				}
			}
			flagFragments := []string{"confirm", "node", "wait", "service", "host-port", "email", "all", "manifest", "vhost", "repository"}
			for _, fragment := range flagFragments {
				if !strings.Contains(completion, fragment) {
					t.Errorf("%s completion does not contain nested flag %q: %s", shell, fragment, completion)
				}
			}
		})
	}
}
