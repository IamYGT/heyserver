package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestRunConnectAuthenticatesVerifiesAndSelectsProtectedContext(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contextsFile := filepath.Join(directory, "config", "contexts.json")
	passwordFile := filepath.Join(directory, "password")
	if err := os.WriteFile(passwordFile, []byte("correct horse\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var verified atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /api/auth/login":
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["email"] != "admin@example.com" || payload["password"] != "correct horse" {
				t.Errorf("login payload=%#v", payload)
			}
			_, _ = writer.Write([]byte(`{"token":"connect-secret-token"}`))
		case "GET /api/auth/me":
			if request.Header.Get("Authorization") != "Bearer connect-secret-token" {
				t.Errorf("Authorization=%q", request.Header.Get("Authorization"))
			}
			verified.Add(1)
			_, _ = writer.Write([]byte(`{"id":7,"name":"Operator","email":"admin@example.com","role":"admin"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_CONTEXT_FILE" {
			return contextsFile
		}
		return ""
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{
		"connect", "--server", server.URL, "--email", "admin@example.com", "--password-file", passwordFile, "production",
	}, &out, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	if verified.Load() != 1 || !strings.Contains(out.String(), `Connected Heyserver context "production"`) || !strings.Contains(out.String(), "Next: hserverctl doctor") || !strings.Contains(out.String(), "Open control center: hserverctl ui") {
		t.Fatalf("verified=%d output=%q", verified.Load(), out.String())
	}
	for _, secret := range []string{"correct horse", "connect-secret-token", "admin@example.com"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("connect output leaked %q: %q", secret, out.String())
		}
	}
	config, exists, err := readContextsConfig(contextsFile)
	if err != nil || !exists || config.Current != "production" {
		t.Fatalf("config=%#v exists=%t err=%v", config, exists, err)
	}
	selected := config.Contexts["production"]
	if selected.Server != server.URL || selected.TokenFile != filepath.Join(directory, "config", "tokens", "production") {
		t.Fatalf("selected=%#v", selected)
	}
	for _, path := range []string{contextsFile, selected.TokenFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode(%s)=%o", path, info.Mode().Perm())
		}
	}
	if token, err := os.ReadFile(selected.TokenFile); err != nil || string(token) != "connect-secret-token\n" {
		t.Fatalf("token=%q err=%v", token, err)
	}

	var whoami bytes.Buffer
	if err := run(context.Background(), []string{"whoami"}, &whoami, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	if verified.Load() != 2 || !strings.Contains(whoami.String(), `"role": "admin"`) {
		t.Fatalf("verified=%d whoami=%q", verified.Load(), whoami.String())
	}
}

func TestRunConnectLeavesNoStateWhenAuthenticationOrVerificationFails(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		me   bool
	}{
		{name: "authentication", me: false},
		{name: "verification", me: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			contextsFile := filepath.Join(directory, "config", "contexts.json")
			passwordFile := filepath.Join(directory, "password")
			if err := os.WriteFile(passwordFile, []byte("password\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/api/auth/login" && test.me {
					_, _ = writer.Write([]byte(`{"token":"unverified-token"}`))
					return
				}
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write([]byte(`{"error":"credentials rejected"}`))
			}))
			defer server.Close()
			err := run(context.Background(), []string{
				"connect", "--server", server.URL, "--email", "admin@example.com", "--password-file", passwordFile, "production",
			}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
				if key == "HSERVER_CONTEXT_FILE" {
					return contextsFile
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), "credentials rejected") {
				t.Fatalf("err=%v", err)
			}
			for _, path := range []string{contextsFile, filepath.Join(directory, "config", "tokens", "production")} {
				if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
					t.Fatalf("failed connect persisted %s: %v", path, statErr)
				}
			}
		})
	}
}

func TestRunConnectRejectsExistingContextOrTokenBeforeNetwork(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	passwordFile := filepath.Join(directory, "password")
	if err := os.WriteFile(passwordFile, []byte("password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	contextPath := filepath.Join(directory, "existing-contexts.json")
	if err := writeContextsConfig(contextPath, cliContextsConfig{Version: contextsConfigVersion, Current: "production", Contexts: map[string]cliContext{
		"production": {Server: server.URL, TokenFile: filepath.Join(directory, "token")},
	}}); err != nil {
		t.Fatal(err)
	}
	err := runConnect(context.Background(), []string{"--server", server.URL, "--email", "admin@example.com", "--password-file", passwordFile, "production"}, contextPath, defaultServerURL, "", 30, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "already exists") || requests.Load() != 0 {
		t.Fatalf("existing context err=%v requests=%d", err, requests.Load())
	}

	emptyContextPath := filepath.Join(directory, "empty-contexts.json")
	existingToken := filepath.Join(directory, "existing-token")
	if err := os.WriteFile(existingToken, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = runConnect(context.Background(), []string{"--server", server.URL, "--token-file", existingToken, "--email", "admin@example.com", "--password-file", passwordFile, "staging"}, emptyContextPath, defaultServerURL, "", 30, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "token file already exists") || requests.Load() != 0 {
		t.Fatalf("existing token err=%v requests=%d", err, requests.Load())
	}
}

func TestRunConnectPromptsForPasswordAndTOTPWithoutEcho(t *testing.T) {
	directory := t.TempDir()
	contextsFile := filepath.Join(directory, "config", "contexts.json")
	var loginCalls atomic.Int32
	var totpCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /api/auth/login":
			loginCalls.Add(1)
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode login: %v", err)
			}
			if payload["password"] != "interactive-password" {
				t.Errorf("password=%q", payload["password"])
			}
			_, _ = writer.Write([]byte(`{"requires_totp":true}`))
		case "POST /api/auth/totp-verify":
			totpCalls.Add(1)
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode TOTP: %v", err)
			}
			if payload["password"] != "interactive-password" || payload["code"] != "654321" {
				t.Errorf("TOTP payload=%#v", payload)
			}
			_, _ = writer.Write([]byte(`{"token":"interactive-token"}`))
		case "GET /api/auth/me":
			if request.Header.Get("Authorization") != "Bearer interactive-token" {
				t.Errorf("Authorization=%q", request.Header.Get("Authorization"))
			}
			_, _ = writer.Write([]byte(`{"id":9,"role":"admin"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	command := exec.Command(os.Args[0], "-test.run=^TestInteractiveConnectHelperProcess$")
	command.Env = append(os.Environ(),
		"HSERVER_CONNECT_HELPER=1",
		"HSERVER_CONNECT_HELPER_URL="+server.URL,
		"HSERVER_CONTEXT_FILE="+contextsFile,
	)
	terminal, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if err := terminal.SetReadDeadline(time.Now().Add(12 * time.Second)); err != nil {
		t.Fatal(err)
	}

	var transcript bytes.Buffer
	buffer := make([]byte, 1024)
	readUntil := func(needle string) {
		t.Helper()
		for !strings.Contains(transcript.String(), needle) {
			count, readErr := terminal.Read(buffer)
			if count > 0 {
				_, _ = transcript.Write(buffer[:count])
			}
			if readErr != nil {
				if errors.Is(readErr, os.ErrDeadlineExceeded) {
					t.Fatalf("interactive connect did not print %q; transcript=%q", needle, transcript.String())
				}
				t.Fatalf("read interactive connect: %v; transcript=%q", readErr, transcript.String())
			}
		}
	}
	readUntil("Heyserver password: ")
	if _, err := terminal.Write([]byte("interactive-password\r")); err != nil {
		t.Fatal(err)
	}
	readUntil("TOTP code: ")
	if _, err := terminal.Write([]byte("654321\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(`Connected Heyserver context "production"`)
	if err := command.Wait(); err != nil {
		t.Fatalf("interactive connect helper: %v; transcript=%q", err, transcript.String())
	}
	for _, secret := range []string{"interactive-password", "654321", "interactive-token"} {
		if strings.Contains(transcript.String(), secret) {
			t.Fatalf("interactive transcript leaked %q: %q", secret, transcript.String())
		}
	}
	if loginCalls.Load() != 1 || totpCalls.Load() != 1 {
		t.Fatalf("login calls=%d TOTP calls=%d", loginCalls.Load(), totpCalls.Load())
	}
	config, exists, err := readContextsConfig(contextsFile)
	if err != nil || !exists || config.Current != "production" {
		t.Fatalf("config=%#v exists=%t err=%v", config, exists, err)
	}
	if info, err := os.Stat(config.Contexts["production"].TokenFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token info=%v err=%v", info, err)
	}
}

func TestInteractiveConnectHelperProcess(t *testing.T) {
	if os.Getenv("HSERVER_CONNECT_HELPER") != "1" {
		return
	}
	err := run(context.Background(), []string{
		"connect", "--server", os.Getenv("HSERVER_CONNECT_HELPER_URL"),
		"--email", "admin@example.com", "production",
	}, os.Stdout, os.Stderr, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunConnectRequiresTTYOrProtectedPasswordFileBeforeNetwork(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	contextsFile := filepath.Join(t.TempDir(), "config", "contexts.json")
	err := runWithInput(context.Background(), []string{
		"connect", "--server", server.URL, "--email", "admin@example.com", "production",
	}, strings.NewReader("not-a-terminal\n"), &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_CONTEXT_FILE" {
			return contextsFile
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "interactive TTY") || !strings.Contains(err.Error(), "--password-file") {
		t.Fatalf("err=%v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d", requests.Load())
	}
	if _, statErr := os.Lstat(contextsFile); !os.IsNotExist(statErr) {
		t.Fatalf("failed non-TTY connect persisted context: %v", statErr)
	}
}
