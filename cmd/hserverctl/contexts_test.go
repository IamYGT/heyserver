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
)

func TestContextsSelectIndependentServersAndTokens(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contextsFile := filepath.Join(directory, "config", "contexts.json")
	getenv := func(key string) string {
		if key == "HSERVER_CONTEXT_FILE" {
			return contextsFile
		}
		return ""
	}

	alpha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "alpha should not be selected", http.StatusBadGateway)
	}))
	defer alpha.Close()
	beta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/auth/me" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer beta-token" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"email": "beta@example.com"})
	}))
	defer beta.Close()

	for _, item := range []struct {
		name   string
		server string
	}{
		{name: "alpha", server: alpha.URL},
		{name: "beta", server: beta.URL},
	} {
		if err := run(context.Background(), []string{
			"context", "add", "--server", item.server, item.name,
		}, &bytes.Buffer{}, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("add %s: %v", item.name, err)
		}
	}
	info, err := os.Stat(contextsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("context file mode = %o", got)
	}

	var listed bytes.Buffer
	if err := run(context.Background(), []string{"context", "list"}, &listed, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed.String(), `"name": "alpha"`) || !strings.Contains(listed.String(), `"name": "beta"`) {
		t.Fatalf("context list = %q", listed.String())
	}
	if strings.Index(listed.String(), `"alpha"`) > strings.Index(listed.String(), `"beta"`) {
		t.Fatalf("context list is not sorted: %q", listed.String())
	}

	betaToken := filepath.Join(directory, "config", "tokens", "beta")
	if err := os.MkdirAll(filepath.Dir(betaToken), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(betaToken, []byte("beta-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var selectedOnce bytes.Buffer
	if err := run(context.Background(), []string{"--context", "beta", "whoami"}, &selectedOnce, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(selectedOnce.String(), `"email": "beta@example.com"`) {
		t.Fatalf("one-command context output = %q", selectedOnce.String())
	}

	if err := run(context.Background(), []string{"context", "use", "beta"}, &bytes.Buffer{}, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}

	var current bytes.Buffer
	if err := run(context.Background(), []string{"context", "current"}, &current, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current.String(), `"name": "beta"`) || !strings.Contains(current.String(), beta.URL) {
		t.Fatalf("current context = %q", current.String())
	}

	var whoami bytes.Buffer
	if err := run(context.Background(), []string{"whoami"}, &whoami, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(whoami.String(), `"email": "beta@example.com"`) || strings.Contains(whoami.String(), "beta-token") {
		t.Fatalf("whoami output = %q", whoami.String())
	}

	var removed bytes.Buffer
	if err := run(context.Background(), []string{"context", "remove", "beta"}, &removed, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(betaToken); err != nil {
		t.Fatalf("context removal deleted token file: %v", err)
	}
	if !strings.Contains(removed.String(), "token file was not deleted") {
		t.Fatalf("remove output = %q", removed.String())
	}
}

func TestExplicitServerDoesNotReuseCurrentContextToken(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contextsFile := filepath.Join(directory, "contexts.json")
	contextToken := filepath.Join(directory, "tokens", "production")
	if err := os.MkdirAll(filepath.Dir(contextToken), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contextToken, []byte("context-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := cliContextsConfig{
		Version: contextsConfigVersion,
		Current: "production",
		Contexts: map[string]cliContext{
			"production": {Server: "https://production.example.com", TokenFile: contextToken},
		},
	}
	if err := writeContextsConfig(contextsFile, config); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer environment-token" {
			t.Errorf("Authorization = %q; current-context token leaked to an overridden server", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"email": "override@example.com"})
	}))
	defer server.Close()
	getenv := func(key string) string {
		switch key {
		case "HSERVER_CONTEXT_FILE":
			return contextsFile
		case "HSERVER_TOKEN":
			return "environment-token"
		}
		return ""
	}

	if err := run(context.Background(), []string{"--server", server.URL, "whoami"}, &bytes.Buffer{}, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
}

func TestContextsRejectSymlinkAndInvalidName(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, []byte(`{"version":1,"contexts":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "contexts.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		if key == "HSERVER_CONTEXT_FILE" {
			return link
		}
		return ""
	}
	if err := run(context.Background(), []string{"context", "list"}, &bytes.Buffer{}, &bytes.Buffer{}, getenv); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("symlink error = %v", err)
	}
	if err := run(context.Background(), []string{"context", "add", "--server", "https://example.com", "bad name"}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_CONTEXT_FILE" {
			return filepath.Join(directory, "valid.json")
		}
		return ""
	}); err == nil || !strings.Contains(err.Error(), "context name") {
		t.Fatalf("invalid-name error = %v", err)
	}
}

func TestResolveCLIContextRetainsNameAndDirectOverride(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "contexts.json")
	if err := writeContextsConfig(path, cliContextsConfig{
		Version: contextsConfigVersion,
		Current: "production",
		Contexts: map[string]cliContext{
			"production": {Server: "https://panel.example.com", TokenFile: filepath.Join(t.TempDir(), "token")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	selected, err := resolveCLIContext(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected == nil || selected.Name != "production" {
		t.Fatalf("selected context = %#v; want retained current name", selected)
	}
	if got := effectiveCLIContextLabel(selected, false); got != "production" {
		t.Fatalf("context label = %q; want production", got)
	}
	if got := effectiveCLIContextLabel(selected, true); got != "direct" {
		t.Fatalf("direct override label = %q; want direct", got)
	}
}

func TestContextStatusProbesAllContextsWithoutReadingTokens(t *testing.T) {
	t.Parallel()
	alpha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/health" {
			t.Errorf("alpha request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("alpha Authorization = %q; context status must use the public health probe", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": "v0.1.0", "uptime": 42})
	}))
	defer alpha.Close()
	beta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("beta Authorization = %q; context status must not load a bearer token", got)
		}
		http.Error(w, `{"error":"panel not ready","state":"unavailable"}`, http.StatusServiceUnavailable)
	}))
	defer beta.Close()

	directory := t.TempDir()
	contextsFile := filepath.Join(directory, "contexts.json")
	if err := writeContextsConfig(contextsFile, cliContextsConfig{
		Version: contextsConfigVersion,
		Current: "alpha",
		Contexts: map[string]cliContext{
			"alpha": {Server: alpha.URL, TokenFile: filepath.Join(directory, "tokens", "alpha-secret-token")},
			"beta":  {Server: beta.URL, TokenFile: filepath.Join(directory, "tokens", "beta-secret-token")},
		},
	}); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		if key == "HSERVER_CONTEXT_FILE" {
			return contextsFile
		}
		return ""
	}

	var out bytes.Buffer
	err := run(context.Background(), []string{"--timeout", "1s", "context", "status", "--format", "json"}, &out, &bytes.Buffer{}, getenv)
	if err == nil || !strings.Contains(err.Error(), "1 unhealthy context") {
		t.Fatalf("context status error = %v; output = %q", err, out.String())
	}
	var statuses []cliContextStatus
	if err := json.Unmarshal(out.Bytes(), &statuses); err != nil {
		t.Fatalf("decode context status output: %v\n%s", err, out.String())
	}
	if len(statuses) != 2 || statuses[0].Name != "alpha" || statuses[1].Name != "beta" {
		t.Fatalf("statuses = %#v; want sorted alpha/beta", statuses)
	}
	if statuses[0].Status != "healthy" || statuses[0].Version != "v0.1.0" || !statuses[0].Current {
		t.Fatalf("alpha status = %#v", statuses[0])
	}
	if statuses[1].Status != "unavailable" || statuses[1].Current || !strings.Contains(statuses[1].Error, "HTTP 503") {
		t.Fatalf("beta status = %#v", statuses[1])
	}
	for _, secret := range []string{"alpha-secret-token", "beta-secret-token", "Bearer"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("context status output leaked %q: %s", secret, out.String())
		}
	}
}

func TestContextStatusSupportsHumanOutputAndSubset(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": "v0.2.0", "uptime": 7})
	}))
	defer server.Close()
	directory := t.TempDir()
	contextsFile := filepath.Join(directory, "contexts.json")
	if err := writeContextsConfig(contextsFile, cliContextsConfig{
		Version: contextsConfigVersion,
		Current: "production",
		Contexts: map[string]cliContext{
			"production": {Server: server.URL, TokenFile: filepath.Join(directory, "production-token")},
			"staging":    {Server: "https://staging.example.com", TokenFile: filepath.Join(directory, "staging-token")},
		},
	}); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		if key == "HSERVER_CONTEXT_FILE" {
			return contextsFile
		}
		return ""
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"context", "status", "production"}, &out, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HServer context status", "NAME\tSTATUS\tLATENCY_MS\tSERVER", "production\thealthy", "current", "version=v0.2.0"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("human status output missing %q: %q", want, out.String())
		}
	}
	if strings.Contains(out.String(), "staging") {
		t.Fatalf("subset status probed or printed staging: %q", out.String())
	}
}

func TestContextStatusRejectsUnknownContextBeforeNetwork(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "contexts.json")
	if err := writeContextsConfig(path, cliContextsConfig{
		Version:  contextsConfigVersion,
		Contexts: map[string]cliContext{"known": {Server: server.URL, TokenFile: filepath.Join(t.TempDir(), "token")}},
	}); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), []string{"context", "status", "missing"}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_CONTEXT_FILE" {
			return path
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), `HServer context "missing" does not exist`) {
		t.Fatalf("unknown context error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("unknown context made %d network requests", requests)
	}
}
