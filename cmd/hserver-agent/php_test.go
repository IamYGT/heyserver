package main

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestPHPControllerDiscoversConfiguredRuntimeRoots(t *testing.T) {
	configRoot := filepath.Join(t.TempDir(), "php-config")
	binaryRoot := filepath.Join(t.TempDir(), "php-binaries")
	poolDir := filepath.Join(configRoot, "8.3", "fpm", "pool.d")
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binaryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	poolContent := "[site]\nuser = deploy\ngroup = web\nlisten = /run/php/site.sock\npm = dynamic\npm.max_children = 12\n"
	if err := os.WriteFile(filepath.Join(poolDir, "site.conf"), []byte(poolContent), 0o640); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binaryRoot, "php-fpm8.3")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: [][]byte{[]byte("ActiveState=active\nUnitFileState=enabled\n")}}
	controller := newPHPController(runner, nil, true, false, configRoot, binaryRoot)

	versions, err := controller.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(versions) != 1 || versions[0].Version != "8.3" || versions[0].Unit != "php8.3-fpm.service" || versions[0].Binary != binary {
		t.Fatalf("versions = %#v", versions)
	}
	if versions[0].Active != "active" || versions[0].Enabled != "enabled" || versions[0].Masked {
		t.Fatalf("service state = %#v", versions[0])
	}
	if len(versions[0].Pools) != 1 || versions[0].Pools[0].Name != "site" || versions[0].Pools[0].User != "deploy" || versions[0].Pools[0].MaxChildren != 12 {
		t.Fatalf("pools = %#v", versions[0].Pools)
	}
	want := []recordedCommand{{name: "systemctl", args: []string{"show", "--property=ActiveState", "--property=UnitFileState", "php8.3-fpm.service"}}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestPHPControllerReadsAndAtomicallyWritesPool(t *testing.T) {
	configRoot, binaryRoot, path := preparePHPRuntime(t)
	oldContent := []byte("[www]\npm = dynamic\npm.max_children = 5\n")
	newContent := []byte("[www]\npm = dynamic\npm.max_children = 10\n")
	if err := os.WriteFile(path, oldContent, 0o640); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: [][]byte{nil, nil}}
	controller := newPHPController(runner, map[string]struct{}{"reload": {}}, true, true, configRoot, binaryRoot)
	config, err := controller.Read("8.3", "www")
	if err != nil || config.Content != string(oldContent) || config.Checksum != checksumBytes(oldContent) {
		t.Fatalf("Read = (%#v, %v)", config, err)
	}

	backup, err := controller.Write(context.Background(), "8.3", "www", newContent, config.Checksum, true)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if saved, readErr := os.ReadFile(path); readErr != nil || string(saved) != string(newContent) {
		t.Fatalf("saved content = %q, err=%v", saved, readErr)
	}
	if saved, readErr := os.ReadFile(backup); readErr != nil || string(saved) != string(oldContent) {
		t.Fatalf("backup content = %q, err=%v", saved, readErr)
	}
	want := []recordedCommand{
		{name: filepath.Join(binaryRoot, "php-fpm8.3"), args: []string{"-t"}},
		{name: "systemctl", args: []string{"reload", "php8.3-fpm.service"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
	if _, err := controller.Write(context.Background(), "8.3", "www", oldContent, config.Checksum, false); !errors.Is(err, errPHPConfigChanged) {
		t.Fatalf("stale checksum error = %v", err)
	}
}

func TestPHPControllerRestoresInvalidPool(t *testing.T) {
	configRoot, binaryRoot, path := preparePHPRuntime(t)
	oldContent := []byte("[www]\npm = dynamic\n")
	if err := os.WriteFile(path, oldContent, 0o640); err != nil {
		t.Fatal(err)
	}
	controller := newPHPController(&fakeRunner{errors: []error{errors.New("exit status 1")}}, nil, true, true, configRoot, binaryRoot)
	_, err := controller.Write(context.Background(), "8.3", "www", []byte("invalid\n"), checksumBytes(oldContent), false)
	if !errors.Is(err, errPHPConfigInvalid) {
		t.Fatalf("Write error = %v", err)
	}
	if restored, readErr := os.ReadFile(path); readErr != nil || string(restored) != string(oldContent) {
		t.Fatalf("restored content = %q, err=%v", restored, readErr)
	}
}

func TestTaskExecutorWritesBase64PHPConfig(t *testing.T) {
	configRoot, binaryRoot, path := preparePHPRuntime(t)
	oldContent := []byte("[www]\npm = dynamic\n")
	if err := os.WriteFile(path, oldContent, 0o640); err != nil {
		t.Fatal(err)
	}
	executor := newTaskExecutor(serviceController{}, nil, nil, nil, nil, nil, nil, false)
	executor.php = newPHPController(&fakeRunner{outputs: [][]byte{nil}}, nil, true, true, configRoot, binaryRoot)
	result := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskPHPConfigWrite, Payload: map[string]string{
		"version": "8.3", "pool": "www", "content_b64": base64.StdEncoding.EncodeToString([]byte("[www]\npm = static\n")), "checksum": checksumBytes(oldContent), "reload": "false",
	}})
	if result.Status != agenthub.TaskStatusCompleted || result.Result["backup"] == "" || !strings.Contains(result.Result["message"], "saved") {
		t.Fatalf("result = %#v", result)
	}
}

func preparePHPRuntime(t *testing.T) (configRoot, binaryRoot, poolPath string) {
	t.Helper()
	configRoot = filepath.Join(t.TempDir(), "php-config")
	binaryRoot = filepath.Join(t.TempDir(), "php-binaries")
	poolDir := filepath.Join(configRoot, "8.3", "fpm", "pool.d")
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binaryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binaryRoot, "php-fpm8.3"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return configRoot, binaryRoot, filepath.Join(poolDir, "www.conf")
}
