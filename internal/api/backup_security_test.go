package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestIsLoopbackRequest(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/internal/cron/backup", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	if !isLoopbackRequest(req) {
		t.Error("expected loopback for 127.0.0.1")
	}

	req.RemoteAddr = "[::1]:54321"
	if !isLoopbackRequest(req) {
		t.Error("expected loopback for ::1")
	}

	req.RemoteAddr = "203.0.113.5:1234"
	if isLoopbackRequest(req) {
		t.Error("expected non-loopback for external IP")
	}
}

func TestResolvePathUnderBase_allowsFileInBase(t *testing.T) {
	base := t.TempDir()
	f := filepath.Join(base, "backup-full.tar.gz")
	if err := os.WriteFile(f, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolvePathUnderBase(base, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved == "" {
		t.Error("expected resolved path")
	}
}

func TestResolvePathUnderBase_rejectsOutside(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.tar.gz")
	if err := os.WriteFile(outsideFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := resolvePathUnderBase(base, outsideFile)
	if err == nil {
		t.Error("expected error for path outside base")
	}
}
