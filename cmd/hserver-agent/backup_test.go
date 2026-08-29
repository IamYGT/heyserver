package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBackupControllerInventoriesVerifiesAndRunsLocalPlans(t *testing.T) {
	root := t.TempDir()
	backupRoot := filepath.Join(root, "backups", "database")
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("portable backup\n")
	if err := os.WriteFile(filepath.Join(backupRoot, "database.sql"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if err := os.WriteFile(filepath.Join(backupRoot, "SHA256SUMS"), []byte(fmt.Sprintf("%x  database.sql\n", digest)), 0o600); err != nil {
		t.Fatal(err)
	}
	plansPath := filepath.Join(root, "backup-plans.json")
	document := fmt.Sprintf(`{"plans":[{"id":"database-export","name":"All databases","service":"hserver-database-backup.service","timer":"hserver-database-backup.timer","root":%q,"checksum_file":"SHA256SUMS"}]}`, backupRoot)
	if err := os.WriteFile(plansPath, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: [][]byte{
		[]byte("ActiveState=active\nUnitFileState=enabled\nLastTriggerUSec=yesterday\nNextElapseUSecRealtime=tomorrow\n"),
		[]byte("Result=success\n"),
		nil,
		[]byte("Result=success\n"),
	}}
	controller := newBackupController(runner, true, true, plansPath)
	plans, err := controller.Inventory(context.Background())
	if err != nil || len(plans) != 1 || plans[0].ID != "database-export" || !plans[0].Verified || plans[0].TotalSize <= int64(len(content)) || len(plans[0].Files) != 2 {
		t.Fatalf("Inventory = (%#v, %v)", plans, err)
	}
	message, err := controller.Run(context.Background(), "database-export")
	if err != nil || message != "All databases backup completed" {
		t.Fatalf("Run = (%q, %v)", message, err)
	}
	if len(runner.commands) != 4 || runner.commands[2].name != "systemctl" || !reflect.DeepEqual(runner.commands[2].args, []string{"start", "hserver-database-backup.service"}) {
		t.Fatalf("commands = %#v", runner.commands)
	}
}

func TestBackupControllerRejectsUnconfiguredPlanAndDisabledControls(t *testing.T) {
	controller := newBackupController(&fakeRunner{}, false, false, "/etc/hserver/backup-plans.json")
	if _, err := controller.Inventory(context.Background()); err == nil {
		t.Fatal("Inventory succeeded without opt-in")
	}
	if _, err := controller.Run(context.Background(), "unknown"); err == nil {
		t.Fatal("Run succeeded without opt-in")
	}
}
