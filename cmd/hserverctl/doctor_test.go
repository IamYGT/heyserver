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
	"sync/atomic"
	"testing"
)

func TestRunDoctorReportsPanelAuthenticationAndFleetWithoutPII(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" && r.Header.Get("Authorization") != "Bearer doctor-token" {
			t.Errorf("Authorization = %q for %s", r.Header.Get("Authorization"), r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"status":"ok","version":"v0.1.0","uptime":42,"build_commit":"abc123"}`))
		case "/api/auth/me":
			_, _ = w.Write([]byte(`{"id":1,"email":"private@example.com","name":"Private Operator","role":"admin","totp_enabled":true}`))
		case "/api/nodes":
			_, _ = w.Write([]byte(`[{"id":"edge-1","online":true,"capabilities":["inventory"]},{"id":"edge-2","online":false,"capabilities":["inventory"]}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	err := run(context.Background(), []string{"--server", server.URL, "doctor"}, &output, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "doctor-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.SchemaVersion != 1 || report.Panel == nil || report.Panel.Uptime != 42 {
		t.Fatalf("report = %#v", report)
	}
	if report.Account == nil || report.Account.Role != "admin" || !report.Account.TOTPEnabled {
		t.Fatalf("account = %#v", report.Account)
	}
	if report.Fleet == nil || report.Fleet.Observed != 2 || report.Fleet.Online != 1 || report.Fleet.Offline != 1 {
		t.Fatalf("fleet = %#v", report.Fleet)
	}
	if strings.Contains(output.String(), "private@example.com") || strings.Contains(output.String(), "Private Operator") || strings.Contains(output.String(), "doctor-token") {
		t.Fatalf("doctor output leaked account PII or token: %q", output.String())
	}
}

func TestRunDoctorFailsForOfflineNodeAndMissingCapabilities(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"status":"ok","version":"v0.1.0","uptime":1}`))
		case "/api/auth/me":
			_, _ = w.Write([]byte(`{"role":"admin"}`))
		case "/api/nodes/edge-1":
			_, _ = w.Write([]byte(`{"id":"edge-1","online":false,"agent_version":"v0.1.0","protocol_version":"v1","capabilities":["inventory"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "doctor", "--node", "edge-1",
		"--require-capability", "terminal", "--require-capability", "files.read",
	}, &output, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "doctor-token"
		}
		return ""
	})
	if err == nil || err.Error() != "doctor reported 3 failed check(s)" {
		t.Fatalf("error = %v", err)
	}
	var report doctorReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Node == nil || report.Node.Online || report.Node.ID != "edge-1" {
		t.Fatalf("report = %#v", report)
	}
	wantedFailures := map[string]bool{
		"node.status":                false,
		"node.capability.files.read": false,
		"node.capability.terminal":   false,
	}
	for _, check := range report.Checks {
		if _, ok := wantedFailures[check.Name]; ok && check.Status == "fail" {
			wantedFailures[check.Name] = true
		}
	}
	for name, found := range wantedFailures {
		if !found {
			t.Fatalf("missing failed check %s in %#v", name, report.Checks)
		}
	}
}

func TestRunDoctorRequiresExactManagedNodeArchitecture(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"status":"ok","version":"v1.2.3","uptime":1}`))
		case "/api/auth/me":
			_, _ = w.Write([]byte(`{"role":"admin"}`))
		case "/api/nodes/edge-arm":
			_, _ = w.Write([]byte(`{"id":"edge-arm","online":true,"agent_version":"v1.2.3","protocol_version":"v1","capabilities":["inventory"],"inventory":{"arch":"arm64"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "doctor-token"
		}
		return ""
	}
	var passed bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "doctor", "--node", "edge-arm", "--require-architecture", "arm64",
	}, &passed, &bytes.Buffer{}, getenv); err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(passed.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Node == nil || report.Node.Architecture != "arm64" {
		t.Fatalf("report = %#v", report)
	}
	foundArchitecturePass := false
	for _, check := range report.Checks {
		if check.Name == "node.architecture" && check.Status == "pass" {
			foundArchitecturePass = true
		}
	}
	if !foundArchitecturePass {
		t.Fatalf("architecture check missing from %#v", report.Checks)
	}

	var failed bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "doctor", "--node", "edge-arm", "--require-architecture", "amd64",
	}, &failed, &bytes.Buffer{}, getenv)
	if err == nil || err.Error() != "doctor reported 1 failed check(s)" {
		t.Fatalf("mismatch error = %v", err)
	}
	if !strings.Contains(failed.String(), `"name": "node.architecture"`) ||
		!strings.Contains(failed.String(), "reports agent architecture arm64, expected amd64") {
		t.Fatalf("mismatch report = %s", failed.String())
	}
}

func TestRunDoctorFailsCapabilitiesWhenNodeCannotBeTrusted(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		nodeBody string
		status   int
	}{
		{name: "unavailable", status: http.StatusNotFound, nodeBody: `{"error":"node not found"}`},
		{name: "identity mismatch", status: http.StatusOK, nodeBody: `{"id":"edge-2","online":true,"capabilities":["terminal"]}`},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/health":
					_, _ = w.Write([]byte(`{"status":"ok","version":"v0.1.0","uptime":1}`))
				case "/api/auth/me":
					_, _ = w.Write([]byte(`{"role":"admin"}`))
				case "/api/nodes/edge-1":
					w.WriteHeader(test.status)
					_, _ = w.Write([]byte(test.nodeBody))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			var output bytes.Buffer
			err := run(context.Background(), []string{
				"--server", server.URL, "doctor", "--node", "edge-1", "--require-capability", "terminal",
			}, &output, &bytes.Buffer{}, func(key string) string {
				if key == "HSERVER_TOKEN" {
					return "doctor-token"
				}
				return ""
			})
			if err == nil || err.Error() != "doctor reported 2 failed check(s)" {
				t.Fatalf("error = %v", err)
			}
			var report doctorReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			failed := map[string]bool{}
			for _, check := range report.Checks {
				if check.Status == "fail" {
					failed[check.Name] = true
				}
			}
			if !failed["node.status"] || !failed["node.capability.terminal"] {
				t.Fatalf("failed checks = %#v", failed)
			}
		})
	}
}

func TestRunDoctorSupportsControlSafeTextOutput(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"status":"ok","version":"v0.1.0\u001b[31m","uptime":42,"build_commit":"abc123"}`))
		case "/api/auth/me":
			_, _ = w.Write([]byte(`{"email":"private@example.com","name":"Private Operator","role":"admin","totp_enabled":true}`))
		case "/api/nodes":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "doctor", "--format", "text",
	}, &output, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "doctor-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{
		"Heyserver connection doctor: PASS",
		"Panel: ok | version v0.1.0[31m | uptime 42s | commit abc123",
		"Account: role admin | TOTP enabled",
		"Fleet: 0 observed | 0 online | 0 offline",
		"[PASS] panel.health",
		"[PASS] authentication",
		"[PASS] fleet.inventory",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("text output does not contain %q: %q", expected, text)
		}
	}
	if strings.Contains(text, "\x1b") || strings.Contains(text, "private@example.com") ||
		strings.Contains(text, "Private Operator") || strings.Contains(text, "doctor-token") {
		t.Fatalf("text output leaked control data, account PII, or token: %q", text)
	}
}

func TestRunDoctorWritesNewProtectedReportFile(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"status":"ok","version":"v0.1.0","uptime":42}`))
		case "/api/auth/me":
			_, _ = w.Write([]byte(`{"email":"private@example.com","name":"Private Operator","role":"admin","totp_enabled":true}`))
		case "/api/nodes":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	reportPath := filepath.Join(t.TempDir(), "reports", "doctor.json")
	var receipt bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "doctor", "--output", reportPath,
	}, &receipt, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "doctor-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receipt.String(), "Wrote protected Heyserver doctor report") || strings.Contains(receipt.String(), "doctor-token") || strings.Contains(receipt.String(), "private@example.com") {
		t.Fatalf("receipt=%q", receipt.String())
	}
	info, err := os.Stat(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode=%o", info.Mode().Perm())
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || strings.Contains(string(data), "private@example.com") || strings.Contains(string(data), "Private Operator") || strings.Contains(string(data), "doctor-token") {
		t.Fatalf("report=%s", data)
	}
}

func TestRunDoctorOutputPreservesFailedReportAndRefusesOverwriteBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"status":"degraded","version":"v0.1.0","uptime":1}`))
		case "/api/auth/me":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"token expired"}`))
		case "/api/nodes":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	reportPath := filepath.Join(t.TempDir(), "doctor.txt")
	var receipt bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "doctor", "--format", "text", "--output", reportPath,
	}, &receipt, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "expired-token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "doctor reported 2 failed check(s)") {
		t.Fatalf("err=%v", err)
	}
	data, readErr := os.ReadFile(reportPath)
	if readErr != nil || !strings.Contains(string(data), "Heyserver connection doctor: FAIL") || !strings.Contains(string(data), "token expired") {
		t.Fatalf("report=%q readErr=%v", data, readErr)
	}
	before := requests.Load()
	err = run(context.Background(), []string{
		"--server", server.URL, "doctor", "--output", reportPath,
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "expired-token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") || requests.Load() != before {
		t.Fatalf("overwrite err=%v requests=%d before=%d", err, requests.Load(), before)
	}
}

func TestRunDoctorValidatesCapabilityArgumentsBeforeRequest(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"doctor", "--require-capability", "terminal"},
		{"doctor", "--require-architecture", "amd64"},
		{"doctor", "--node", "edge-1", "--require-architecture", "riscv64"},
		{"doctor", "--node", "edge-1", "--require-capability", "terminal", "--require-capability", "terminal"},
		{"doctor", "--node", "edge-1", "--require-capability", "bad capability"},
		{"doctor", "--format", "yaml"},
	} {
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
			if key == "HSERVER_TOKEN" {
				return "doctor-token"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("args %q unexpectedly succeeded", args)
		}
	}
}
