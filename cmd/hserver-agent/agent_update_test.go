package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/services/releaseupdates"
)

type agentUpdateRunner struct {
	calls [][]string
	out   []byte
	err   error
}

func (r *agentUpdateRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.out, r.err
}

func TestAgentUpdateStatusKeepsMissingManifestExplicit(t *testing.T) {
	controller := newAgentUpdateController("v1.0.0", "", "", t.TempDir(), "/usr/local/libexec/hserver-agent-install", "/usr/bin/systemd-run", "/usr/bin/systemctl", &agentUpdateRunner{})
	status, err := controller.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.ReleaseStatus != "not_configured" || status.OperationStatus != agentUpdateIdle || status.UpdateAvailable || status.RollbackAvailable {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestAgentUpdateUpgradeStagesVerifiedReleaseAndSchedulesFixedUnit(t *testing.T) {
	archive := testAgentReleaseArchive(t, "v1.2.3")
	digest := sha256.Sum256(archive)
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	var manifest []byte
	var signature string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			_, _ = w.Write(manifest)
		case "/manifest.json.sig":
			_, _ = w.Write([]byte(signature))
		case "/release.tar.gz":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var err error
	manifest, err = json.Marshal(map[string]any{
		"schema_version": 1,
		"version":        "v1.2.3",
		"artifacts": map[string]any{
			runtime.GOOS + "_" + runtime.GOARCH: map[string]any{
				"url":        server.URL + "/release.tar.gz",
				"sha256":     hex.EncodeToString(digest[:]),
				"size_bytes": len(archive),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifest))

	stateDir := t.TempDir()
	runner := &agentUpdateRunner{}
	controller := newAgentUpdateController("v1.0.0", server.URL+"/manifest.json", base64.StdEncoding.EncodeToString(publicKey), stateDir, "/usr/local/libexec/hserver-agent-install", "/usr/bin/systemd-run", "/usr/bin/systemctl", runner)
	controller.now = func() time.Time { return time.Date(2026, time.August, 26, 22, 0, 0, 0, time.UTC) }
	status, err := controller.Upgrade(context.Background(), "v1.2.3")
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if status.Operation != "upgrade" || status.OperationStatus != agentUpdateScheduled || status.OperationVersion != "v1.2.3" {
		t.Fatalf("unexpected scheduled status: %#v", status)
	}
	if len(runner.calls) != 1 || runner.calls[0][0] != "/usr/bin/systemd-run" || !containsArgument(runner.calls[0], "--unit=hserver-agent-lifecycle") {
		t.Fatalf("systemd-run call = %#v", runner.calls)
	}
	if containsSubstring(runner.calls[0], server.URL) {
		t.Fatalf("release URL leaked into detached command: %#v", runner.calls[0])
	}
	stageID := strings.TrimSpace(readTestFile(t, filepath.Join(stateDir, "updates", "current")))
	stageDir := filepath.Join(stateDir, "updates", stageID)
	for _, name := range []string{"hserver-agent", "agent-install.sh", "lifecycle-runner.sh", "VERSION", "status", "status-detail", "updated-at"} {
		if info, err := os.Stat(filepath.Join(stageDir, name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("staged %s: info=%v err=%v", name, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(stageDir, "release.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("verified archive was retained: %v", err)
	}
}

func TestAgentUpdateUpgradeRequiresVerifiedManifestBeforeMutation(t *testing.T) {
	archive := testAgentReleaseArchive(t, "v1.2.3")
	digest := sha256.Sum256(archive)
	manifestRequests := 0
	archiveRequests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			manifestRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schema_version": 1,
				"version":        "v1.2.3",
				"artifacts": map[string]any{
					runtime.GOOS + "_" + runtime.GOARCH: map[string]any{
						"url": server.URL + "/release.tar.gz", "sha256": hex.EncodeToString(digest[:]), "size_bytes": len(archive),
					},
				},
			})
		case "/release.tar.gz":
			archiveRequests++
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	stateDir := filepath.Join(t.TempDir(), "state")
	runner := &agentUpdateRunner{}
	controller := newAgentUpdateController("v1.0.0", server.URL+"/manifest.json", "", stateDir, "/usr/local/libexec/hserver-agent-install", "/usr/bin/systemd-run", "/usr/bin/systemctl", runner)
	if _, err := controller.Upgrade(context.Background(), "v1.2.3"); !errors.Is(err, releaseupdates.ErrSignedManifestRequired) {
		t.Fatalf("Upgrade error = %v, want %v", err, releaseupdates.ErrSignedManifestRequired)
	}
	if manifestRequests != 1 || archiveRequests != 0 || len(runner.calls) != 0 {
		t.Fatalf("requests: manifest=%d archive=%d, runner calls=%#v", manifestRequests, archiveRequests, runner.calls)
	}
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state directory mutation: %v", err)
	}
}

func TestAgentUpdateUpgradeRejectsChangedVersionBeforeDownload(t *testing.T) {
	archive := testAgentReleaseArchive(t, "v1.2.3")
	digest := sha256.Sum256(archive)
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	manifest, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"version":        "v1.2.3",
		"artifacts": map[string]any{
			runtime.GOOS + "_" + runtime.GOARCH: map[string]any{"url": "https://releases.example.invalid/release.tar.gz", "sha256": hex.EncodeToString(digest[:]), "size_bytes": len(archive)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifest))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sig") {
			_, _ = w.Write([]byte(signature))
			return
		}
		_, _ = w.Write(manifest)
	}))
	defer server.Close()
	controller := newAgentUpdateController("v1.0.0", server.URL, base64.StdEncoding.EncodeToString(publicKey), t.TempDir(), "/usr/local/libexec/hserver-agent-install", "/usr/bin/systemd-run", "/usr/bin/systemctl", &agentUpdateRunner{})
	if _, err := controller.Upgrade(context.Background(), "v1.2.2"); err != errAgentUpdateVersion {
		t.Fatalf("Upgrade error = %v, want %v", err, errAgentUpdateVersion)
	}
}

func TestAgentUpdateRollbackRequiresVerifiedSnapshotAndUsesLocalInstaller(t *testing.T) {
	stateDir := t.TempDir()
	installer := filepath.Join(t.TempDir(), "agent-install")
	if err := os.WriteFile(installer, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(stateDir, "releases", "20260826T220000Z-pre-upgrade")
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "hserver-agent"), []byte("previous"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "releases", "latest-pre-upgrade"), []byte(snapshot+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &agentUpdateRunner{}
	controller := newAgentUpdateController("v1.2.3", "", "", stateDir, installer, "/usr/bin/systemd-run", "/usr/bin/systemctl", runner)
	status, err := controller.Rollback(context.Background())
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if !status.RollbackAvailable || status.Operation != "rollback" || status.OperationStatus != agentUpdateScheduled {
		t.Fatalf("unexpected rollback status: %#v", status)
	}
	if len(runner.calls) != 1 || !containsArgument(runner.calls[0], installer) || containsSubstring(runner.calls[0], "release.tar.gz") {
		t.Fatalf("rollback schedule = %#v", runner.calls)
	}
}

func TestDetachedAgentLifecycleStateRequiresBothInactiveUnits(t *testing.T) {
	inactive := []byte("LoadState=not-found\nActiveState=inactive\n\nLoadState=not-found\nActiveState=inactive\n")
	if active, conclusive := detachedAgentLifecycleState(inactive); active || !conclusive {
		t.Fatalf("inactive state = active:%t conclusive:%t", active, conclusive)
	}
	activeOutput := []byte("LoadState=loaded\nActiveState=active\n\nLoadState=loaded\nActiveState=inactive\n")
	if active, conclusive := detachedAgentLifecycleState(activeOutput); !active || !conclusive {
		t.Fatalf("active state = active:%t conclusive:%t", active, conclusive)
	}
	if active, conclusive := detachedAgentLifecycleState([]byte("garbage")); active || conclusive {
		t.Fatalf("ambiguous state = active:%t conclusive:%t", active, conclusive)
	}
}

func testAgentReleaseArchive(t *testing.T, version string) []byte {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	root := "hserver-panel-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH
	files := map[string][]byte{
		root + "/VERSION":          []byte(version + "\n"),
		root + "/hserver-agent":    binary,
		root + "/agent-install.sh": []byte("#!/usr/bin/env sh\nset -eu\n"),
		root + "/README.md":        []byte("test release\n"),
	}
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func containsSubstring(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if strings.Contains(argument, expected) {
			return true
		}
	}
	return false
}

func readTestFile(t *testing.T, filename string) string {
	t.Helper()
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
