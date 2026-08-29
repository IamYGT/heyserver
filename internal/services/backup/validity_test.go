package backup

import "testing"

func TestBackupValidity_databaseTooSmall(t *testing.T) {
	if got := BackupValidity("backup-db-all-postgresql.sql.gz", 20); got != "invalid" {
		t.Fatalf("expected invalid, got %s", got)
	}
}

func TestBackupValidity_databaseOK(t *testing.T) {
	if got := BackupValidity("backup-db-all-postgresql.sql.gz", 1024); got != "completed" {
		t.Fatalf("expected completed, got %s", got)
	}
}

func TestBackupValidity_filesTooSmall(t *testing.T) {
	if got := BackupValidity("backup-files.tar.gz", 142); got != "invalid" {
		t.Fatalf("expected invalid, got %s", got)
	}
}

func TestBackupValidity_filesOK(t *testing.T) {
	if got := BackupValidity("backup-files.tar.gz", 2048); got != "completed" {
		t.Fatalf("expected completed, got %s", got)
	}
}
