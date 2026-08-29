package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

type fakeDeployProcessRunner struct {
	directory string
	command   string
	args      []string
	output    []byte
	truncated bool
	err       error
}

func (r *fakeDeployProcessRunner) run(_ context.Context, directory, command string, args []string, _ int) ([]byte, bool, error) {
	r.directory, r.command, r.args = directory, command, append([]string(nil), args...)
	return r.output, r.truncated, r.err
}

func TestDeployControllerInventoriesAndRunsOnlyLocalPlanActions(t *testing.T) {
	root := t.TempDir()
	command := filepath.Join(root, "release")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	plansPath := filepath.Join(root, "deploy-plans.json")
	document := fmt.Sprintf(`{"plans":[{"id":"example-app","name":"Example app","description":"Portable release plan","kind":"application","path":%q,"actions":{"preflight":{"command":%q,"args":["check"]},"deploy":{"command":%q,"args":["release"],"timeout_seconds":120}}}]}`, root, command, command)
	if err := os.WriteFile(plansPath, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeDeployProcessRunner{output: []byte("release complete\n")}
	controller := newDeployController(runner, true, true, plansPath)
	targets, err := controller.Inventory(context.Background())
	if err != nil || len(targets) != 1 || targets[0].ID != "example-app" || !targets[0].Eligible || !reflect.DeepEqual(targets[0].Actions, []string{"preflight", "deploy"}) {
		t.Fatalf("Inventory = (%#v, %v)", targets, err)
	}
	message, output, err := controller.Run(context.Background(), "example-app", "deploy")
	if err != nil || message != "Example app deploy completed" || output != "release complete" {
		t.Fatalf("Run = (%q, %q, %v)", message, output, err)
	}
	if runner.directory != root || runner.command != command || !reflect.DeepEqual(runner.args, []string{"release"}) {
		t.Fatalf("runner = dir:%q command:%q args:%#v", runner.directory, runner.command, runner.args)
	}
	taskResult := (taskExecutor{deploys: controller}).execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskDeployAction, Payload: map[string]string{"target": "example-app", "action": "preflight"}})
	if taskResult.Status != agenthub.TaskStatusCompleted || taskResult.Result["message"] != "Example app preflight completed" {
		t.Fatalf("deploy task result = %#v", taskResult)
	}
	if _, _, err := controller.Run(context.Background(), "example-app", "rollback"); err == nil {
		t.Fatal("unconfigured rollback action succeeded")
	}
}

func TestDeployControllerRejectsInvalidPlansAndDisabledControls(t *testing.T) {
	root := t.TempDir()
	plansPath := filepath.Join(root, "deploy-plans.json")
	if err := os.WriteFile(plansPath, []byte(`{"plans":[{"id":"bad","name":"Bad","path":"/","actions":{"deploy":{"command":"bin/sh"}}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	controller := newDeployController(&fakeDeployProcessRunner{}, true, true, plansPath)
	if _, err := controller.Inventory(context.Background()); err == nil {
		t.Fatal("invalid plan inventory succeeded")
	}
	disabled := newDeployController(&fakeDeployProcessRunner{}, false, false, plansPath)
	if _, err := disabled.Inventory(context.Background()); err == nil {
		t.Fatal("disabled deploy inventory succeeded")
	}
	if _, _, err := disabled.Run(context.Background(), "bad", "deploy"); err == nil {
		t.Fatal("disabled deploy action succeeded")
	}
}
