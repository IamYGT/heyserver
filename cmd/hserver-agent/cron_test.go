package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCronControllerCreatesInventoriesAndRunsManagedJob(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state", "cron-jobs.json")
	cronPath := filepath.Join(root, "cron.d", "hserver-managed")
	lockPath := filepath.Join(root, "run", "cron.lock")
	runner := &fakeRunner{outputs: [][]byte{nil, nil, []byte("active\n"), []byte("manual output\n")}}
	controller := newCronController(runner, true, true, true, statePath, cronPath, lockPath, "/usr/bin/crontab", "/usr/sbin/runuser", "/bin/bash", "cron.service")

	emptyRevision := cronRevision([]cronJob{})
	id, err := controller.Create(context.Background(), cronJob{Schedule: "*/5 * * * *", User: "root", Command: "/usr/bin/true", Description: "health", Enabled: true}, emptyRevision)
	if err != nil || !validAgentCronID(id) {
		t.Fatalf("Create = (%q, %v)", id, err)
	}
	if rendered, readErr := os.ReadFile(cronPath); readErr != nil || !strings.Contains(string(rendered), "*/5 * * * * root /usr/bin/true") {
		t.Fatalf("rendered cron = %q, err=%v", rendered, readErr)
	}
	if _, err := controller.Create(context.Background(), cronJob{Schedule: "0 * * * *", User: "root", Command: "/usr/bin/true", Enabled: true}, emptyRevision); !errors.Is(err, errCronChanged) {
		t.Fatalf("stale create error = %v", err)
	}

	inventory, err := controller.Inventory(context.Background())
	if err != nil || inventory.Service != "active" || len(inventory.Jobs) != 1 || inventory.Jobs[0].ID != id || inventory.Revision == emptyRevision {
		t.Fatalf("Inventory = (%#v, %v)", inventory, err)
	}
	output, err := controller.Run(context.Background(), id)
	if err != nil || output != "manual output\n" {
		t.Fatalf("Run = (%q, %v)", output, err)
	}
	last := runner.commands[len(runner.commands)-1]
	if last.name != "/usr/sbin/runuser" || strings.Join(last.args, " ") != "-u root -- /bin/bash -lc /usr/bin/true" {
		t.Fatalf("run command = %#v", last)
	}
}

func TestCronControllerRestoresInvalidMutation(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state", "cron-jobs.json")
	cronPath := filepath.Join(root, "cron.d", "hserver-managed")
	controller := newCronController(&fakeRunner{errors: []error{errors.New("invalid")}}, true, true, false, statePath, cronPath, filepath.Join(root, "cron.lock"), "/usr/bin/crontab", "/usr/sbin/runuser", "/bin/bash", "cron.service")
	_, err := controller.Create(context.Background(), cronJob{Schedule: "bad * * * *", User: "root", Command: "/usr/bin/true", Enabled: true}, cronRevision([]cronJob{}))
	if !errors.Is(err, errCronInvalid) {
		t.Fatalf("Create error = %v", err)
	}
	if _, statErr := os.Stat(cronPath); !os.IsNotExist(statErr) {
		t.Fatalf("cron file unexpectedly exists: %v", statErr)
	}
}
