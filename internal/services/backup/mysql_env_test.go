package backup

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMySQLCommandsForwardProtectedDefaultsFile(t *testing.T) {
	defaultsFile := filepath.Join(t.TempDir(), "client.cnf")
	t.Setenv("HSERVER_MYSQL_DEFAULTS_FILE", defaultsFile)

	dumpArgs := strings.Join(mysqlDumpCommand(context.Background(), "application").Args[1:], " ")
	if dumpArgs != "--defaults-extra-file="+defaultsFile+" application" {
		t.Fatalf("dump args = %q", dumpArgs)
	}
	restoreArgs := strings.Join(mysqlRestoreCommand(context.Background(), "application").Args[1:], " ")
	if restoreArgs != "--defaults-extra-file="+defaultsFile+" application" {
		t.Fatalf("restore args = %q", restoreArgs)
	}
}

func TestMySQLAllDatabaseCommandsDoNotSelectSingleTarget(t *testing.T) {
	t.Setenv("HSERVER_MYSQL_DEFAULTS_FILE", "")
	if args := strings.Join(mysqlDumpCommand(context.Background(), "all").Args[1:], " "); args != "--all-databases" {
		t.Fatalf("dump args = %q", args)
	}
	if args := mysqlRestoreCommand(context.Background(), "all").Args[1:]; len(args) != 0 {
		t.Fatalf("restore args = %v", args)
	}
}
