package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRunUpdatesReadsPanelAndAgentStatus(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer updates-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/system/update":
			_, _ = w.Write([]byte(`{"status":"healthy","current_version":"v1.0.0","update_available":false}`))
		case "/api/system/update/stage":
			_, _ = w.Write([]byte(`{"stage":null}`))
		case "/api/nodes/edge-1/agent-update":
			_, _ = w.Write([]byte(`{"release_status":"healthy","current_version":"v1.0.0","update_available":false,"operation_status":"idle","rollback_available":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"updates", "status"},
		{"updates", "stage-status"},
		{"updates", "agent", "status", "--node", "edge-1"},
	} {
		var output bytes.Buffer
		if err := runUpdatesTestCLI(server.URL, args, &output); err != nil {
			t.Fatalf("args %q: %v", args, err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("args %q returned invalid JSON: %q", args, output.String())
		}
	}
}

func TestRunUpdatesStagesAndInstallsObservedRelease(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/system/update":
			_, _ = w.Write([]byte(`{"status":"healthy","signature_status":"verified","current_version":"v1.0.0","latest_version":"v1.2.0","update_available":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/system/update/stage":
			_, _ = w.Write([]byte(`{"id":"v1.2.0-0123456789ab","version":"v1.2.0","status":"staged"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/system/update/stage":
			_, _ = w.Write([]byte(`{"stage":{"id":"v1.2.0-0123456789ab","version":"v1.2.0","status":"staged"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/system/update/install":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["stage_id"] != "v1.2.0-0123456789ab" || body["version"] != "v1.2.0" || body["confirmed"] != true || len(body) != 3 {
				t.Errorf("install body = %#v", body)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"v1.2.0-0123456789ab","version":"v1.2.0","status":"scheduled"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"updates", "stage", "--confirm"},
		{"updates", "install", "--confirm"},
	} {
		var output bytes.Buffer
		if err := runUpdatesTestCLI(server.URL, args, &output); err != nil {
			t.Fatalf("args %q: %v", args, err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("args %q returned invalid JSON: %q", args, output.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET /api/system/update", "POST /api/system/update/stage", "GET /api/system/update", "GET /api/system/update/stage", "POST /api/system/update/install"}
	if strings.Join(requests, "|") != strings.Join(want, "|") {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestRunUpdatesUsesObservedAgentLifecycleState(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := make([]string, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/nodes/edge-1/agent-update":
			_, _ = w.Write([]byte(`{"release_status":"healthy","signature_status":"verified","latest_version":"v1.2.0","update_available":true,"operation_status":"idle","rollback_available":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/nodes/edge-1/agent-update/upgrade":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["version"] != "v1.2.0" || body["confirmed"] != true || len(body) != 2 {
				t.Errorf("upgrade body = %#v", body)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"operation":"upgrade","operation_status":"scheduled","operation_version":"v1.2.0"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/nodes/edge-1/agent-update/rollback":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["confirmed"] != true || len(body) != 1 {
				t.Errorf("rollback body = %#v", body)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"operation":"rollback","operation_status":"scheduled"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"updates", "agent", "upgrade", "--confirm", "--node", "edge-1"},
		{"updates", "agent", "rollback", "--confirm", "--node", "edge-1"},
	} {
		var output bytes.Buffer
		if err := runUpdatesTestCLI(server.URL, args, &output); err != nil {
			t.Fatalf("args %q: %v", args, err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("args %q returned invalid JSON: %q", args, output.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"GET /api/nodes/edge-1/agent-update", "POST /api/nodes/edge-1/agent-update/upgrade",
		"GET /api/nodes/edge-1/agent-update", "POST /api/nodes/edge-1/agent-update/rollback",
	}
	if strings.Join(requests, "|") != strings.Join(want, "|") {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestRunUpdatesRejectsUnverifiedManifestBeforeMutation(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/system/update":
			_, _ = w.Write([]byte(`{"status":"healthy","signature_status":"not_configured","current_version":"v1.0.0","latest_version":"v1.2.0","update_available":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/nodes/edge-1/agent-update":
			_, _ = w.Write([]byte(`{"release_status":"healthy","signature_status":"unavailable","latest_version":"v1.2.0","update_available":true,"operation_status":"idle","rollback_available":true}`))
		case r.Method == http.MethodPost:
			posts.Add(1)
			http.Error(w, "mutation must not be sent", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"updates", "stage", "--confirm"},
		{"updates", "install", "--confirm"},
		{"updates", "agent", "upgrade", "--confirm", "--node", "edge-1"},
	} {
		if err := runUpdatesTestCLI(server.URL, args, &bytes.Buffer{}); err == nil {
			t.Fatalf("args %q unexpectedly succeeded", args)
		}
	}
	if posts.Load() != 0 {
		t.Fatalf("unverified preflight sent %d mutation request(s)", posts.Load())
	}
}

func TestRunUpdatesRejectsUnconfirmedOrUnreadyMutations(t *testing.T) {
	t.Parallel()
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/system/update/stage":
			_, _ = w.Write([]byte(`{"stage":null}`))
		case "/api/nodes/edge-1/agent-update":
			_, _ = w.Write([]byte(`{"release_status":"healthy","latest_version":"v1.2.0","update_available":false,"operation_status":"running","rollback_available":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"updates", "stage"},
		{"updates", "install"},
		{"updates", "agent", "status", "--confirm", "--node", "edge-1"},
		{"updates", "agent", "upgrade", "--node", "edge-1"},
		{"updates", "agent", "rollback", "--confirm", "--node", "bad node"},
		{"updates", "stage", "--confirm", "--wait", "21m"},
	} {
		if err := runUpdatesTestCLI(server.URL, args, &bytes.Buffer{}); err == nil {
			t.Fatalf("args %q unexpectedly succeeded", args)
		}
	}
	if requestCount.Load() != 0 {
		t.Fatalf("invalid mutations sent %d request(s)", requestCount.Load())
	}

	for _, args := range [][]string{
		{"updates", "install", "--confirm"},
		{"updates", "agent", "upgrade", "--confirm", "--node", "edge-1"},
		{"updates", "agent", "rollback", "--confirm", "--node", "edge-1"},
	} {
		if err := runUpdatesTestCLI(server.URL, args, &bytes.Buffer{}); err == nil {
			t.Fatalf("args %q unexpectedly passed an unready preflight", args)
		}
	}
	if requestCount.Load() != 3 {
		t.Fatalf("unready preflights sent %d requests, want only three reads", requestCount.Load())
	}
}

func runUpdatesTestCLI(server string, args []string, output *bytes.Buffer) error {
	fullArgs := append([]string{"--server", server}, args...)
	return run(context.Background(), fullArgs, output, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "updates-token"
		}
		return ""
	})
}
