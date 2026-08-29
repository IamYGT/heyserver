package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestNginxControllerTestsBeforeAllowlistedReload(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{nil, nil}}
	controller := newNginxController(runner, map[string]struct{}{"reload": {}}, false, false, "", "")
	message, err := controller.Action(context.Background(), "reload")
	if err != nil || !strings.Contains(message, "reloaded") {
		t.Fatalf("Action = (%q, %v)", message, err)
	}
	want := []recordedCommand{
		{name: "nginx", args: []string{"-t"}},
		{name: "systemctl", args: []string{"reload", "nginx.service"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if _, err := controller.Action(context.Background(), "test"); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("blocked action error = %v", err)
	}
}

func TestTaskExecutorRunsStructuredNginxAction(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{nil}}
	executor := newTaskExecutor(serviceController{}, nil, nil, nil, nil, nil, nil, false)
	executor.nginx = newNginxController(runner, map[string]struct{}{"test": {}}, false, false, "", "")
	result := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskNginxAction, Payload: map[string]string{"action": "test"}})
	if result.Status != agenthub.TaskStatusCompleted || !strings.Contains(result.Result["message"], "valid") {
		t.Fatalf("result = %#v", result)
	}
}

func TestNginxControllerReadsAndAtomicallyWritesConfiguredSites(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(enabled, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(available, "example.conf")
	oldContent := []byte("server { listen 80; }\n")
	newContent := []byte("server { listen 8080; }\n")
	if err := os.WriteFile(path, oldContent, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(available, "linked.conf")); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(oldContent)
	checksum := hex.EncodeToString(digest[:])
	runner := &fakeRunner{outputs: [][]byte{nil, nil}}
	controller := newNginxController(runner, nil, true, true, available, enabled)

	configs, err := controller.List()
	if err != nil || len(configs) != 1 || configs[0].Name != "example.conf" || configs[0].Enabled {
		t.Fatalf("List = (%#v, %v)", configs, err)
	}
	if _, err := controller.Read("linked.conf"); err == nil {
		t.Fatal("Read accepted a symlinked Nginx configuration")
	}
	config, err := controller.Read("example.conf")
	if err != nil || config.Content != string(oldContent) || config.Checksum != checksum {
		t.Fatalf("Read = (%#v, %v)", config, err)
	}
	backup, err := controller.Write(context.Background(), "example.conf", newContent, checksum, true)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if saved, err := os.ReadFile(path); err != nil || string(saved) != string(newContent) {
		t.Fatalf("saved content = %q, err=%v", saved, err)
	}
	if saved, err := os.ReadFile(backup); err != nil || string(saved) != string(oldContent) {
		t.Fatalf("backup content = %q, err=%v", saved, err)
	}
	validationLinks, err := filepath.Glob(filepath.Join(enabled, ".hserver-validation-*.conf"))
	if err != nil || len(validationLinks) != 0 {
		t.Fatalf("validation links = %#v, err=%v", validationLinks, err)
	}
	wantCommands := []recordedCommand{{name: "nginx", args: []string{"-t"}}, {name: "systemctl", args: []string{"reload", "nginx.service"}}}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("commands = %#v", runner.commands)
	}
	configs, err = controller.List()
	if err != nil || len(configs) != 1 {
		t.Fatalf("List after backup = (%#v, %v)", configs, err)
	}
}

func TestNginxControllerRestoresInvalidConfiguration(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(available, "broken.conf")
	oldContent := []byte("server {}\n")
	if err := os.WriteFile(path, oldContent, 0o640); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(oldContent)
	runner := &fakeRunner{errors: []error{errors.New("exit status 1")}}
	controller := newNginxController(runner, nil, true, true, available, enabled)
	_, err := controller.Write(context.Background(), "broken.conf", []byte("invalid"), hex.EncodeToString(digest[:]), false)
	if !errors.Is(err, errNginxConfigInvalid) {
		t.Fatalf("Write error = %v", err)
	}
	if restored, readErr := os.ReadFile(path); readErr != nil || string(restored) != string(oldContent) {
		t.Fatalf("restored content = %q, err=%v", restored, readErr)
	}
}

func TestTaskExecutorWritesBase64NginxConfig(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	oldContent := []byte("server {}\n")
	if err := os.WriteFile(filepath.Join(available, "site.conf"), oldContent, 0o640); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(oldContent)
	executor := newTaskExecutor(serviceController{}, nil, nil, nil, nil, nil, nil, false)
	executor.nginx = newNginxController(&fakeRunner{outputs: [][]byte{nil}}, nil, true, true, available, filepath.Join(root, "enabled"))
	result := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskNginxConfigWrite, Payload: map[string]string{
		"name": "site.conf", "content_b64": base64.StdEncoding.EncodeToString([]byte("server { listen 80; }\n")), "checksum": hex.EncodeToString(digest[:]), "reload": "false",
	}})
	if result.Status != agenthub.TaskStatusCompleted || result.Result["backup"] == "" || !strings.Contains(result.Result["message"], "saved") {
		t.Fatalf("result = %#v", result)
	}
}
