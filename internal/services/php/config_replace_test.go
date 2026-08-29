package php

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReplacePoolConfigCreatesBackupValidatesAndReloads(t *testing.T) {
	t.Parallel()
	service, poolPath, binaryPath := prepareLocalPoolConfigService(t)
	var calls []string
	service.runCommandContext = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		return []byte("ok"), nil
	}
	current := []byte("[www]\npm = dynamic\n")
	replacement := []byte("[www]\npm = ondemand\n")
	checksum := sha256.Sum256(current)

	observed, err := service.ReadPoolConfig("8.4", "www")
	if err != nil {
		t.Fatal(err)
	}
	if observed.Path != poolPath || observed.Content != string(current) || observed.Checksum != hex.EncodeToString(checksum[:]) {
		t.Fatalf("observed = %#v", observed)
	}
	receipt, err := service.ReplacePoolConfig(context.Background(), "8.4", "www", replacement, observed.Checksum, true)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Backup == "" || !receipt.Reloaded || receipt.Checksum == observed.Checksum {
		t.Fatalf("receipt = %#v", receipt)
	}
	if content, err := os.ReadFile(poolPath); err != nil || string(content) != string(replacement) {
		t.Fatalf("pool content = %q, err=%v", content, err)
	}
	if content, err := os.ReadFile(receipt.Backup); err != nil || string(content) != string(current) {
		t.Fatalf("backup content = %q, err=%v", content, err)
	}
	wantCalls := []string{binaryPath + " -t", "systemctl reload php8.4-fpm"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestReplacePoolConfigRejectsConflictAndRestoresFailedChanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		failCall   string
		checksum   string
		wantError  error
		wantCalls  int
		wantBackup bool
	}{
		{name: "changed checksum", checksum: strings.Repeat("a", 64), wantError: ErrPoolConfigChanged},
		{name: "invalid config", failCall: "php-fpm8.4", wantError: ErrPoolConfigInvalid, wantCalls: 1, wantBackup: true},
		{name: "reload failure", failCall: "systemctl", wantError: ErrPoolConfigReload, wantCalls: 2, wantBackup: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, poolPath, _ := prepareLocalPoolConfigService(t)
			calls := 0
			service.runCommandContext = func(_ context.Context, name string, _ ...string) ([]byte, error) {
				calls++
				if strings.Contains(name, test.failCall) && test.failCall != "" {
					return []byte("fixture failure"), errors.New("exit status 1")
				}
				return nil, nil
			}
			current, err := service.ReadPoolConfig("8.4", "www")
			if err != nil {
				t.Fatal(err)
			}
			checksum := current.Checksum
			if test.checksum != "" {
				checksum = test.checksum
			}
			_, err = service.ReplacePoolConfig(context.Background(), "8.4", "www", []byte("[www]\npm = static\n"), checksum, true)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want %v", err, test.wantError)
			}
			content, readErr := os.ReadFile(poolPath)
			if readErr != nil || string(content) != current.Content {
				t.Fatalf("restored content = %q, err=%v", content, readErr)
			}
			if calls != test.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, test.wantCalls)
			}
			backups, globErr := filepath.Glob(poolPath + ".hserver-backup-*")
			if globErr != nil || (len(backups) > 0) != test.wantBackup {
				t.Fatalf("backups = %#v, err=%v", backups, globErr)
			}
		})
	}
}

func TestReadPoolConfigRejectsSymlink(t *testing.T) {
	t.Parallel()
	service, poolPath, _ := prepareLocalPoolConfigService(t)
	target := poolPath + ".target"
	if err := os.Rename(poolPath, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, poolPath); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadPoolConfig("8.4", "www"); err == nil {
		t.Fatal("symlink pool configuration unexpectedly passed")
	}
}

func prepareLocalPoolConfigService(t *testing.T) (*Service, string, string) {
	t.Helper()
	configRoot := filepath.Join(t.TempDir(), "php")
	binaryRoot := filepath.Join(t.TempDir(), "bin")
	poolPath := filepath.Join(configRoot, "8.4", "fpm", "pool.d", "www.conf")
	if err := os.MkdirAll(filepath.Dir(poolPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poolPath, []byte("[www]\npm = dynamic\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binaryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(binaryRoot, "php-fpm8.4")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewWithConfig(ServiceConfig{ConfigRoot: configRoot, BinaryRoot: binaryRoot}), poolPath, binaryPath
}
