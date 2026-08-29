package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPgDumpEnv_defaultPostgres(t *testing.T) {
	t.Setenv("HSERVER_PG_BACKUP_USER", "")
	t.Setenv("PGUSER", "")
	env := pgDumpEnv()
	if !containsEnv(env, "PGUSER=postgres") {
		t.Fatalf("expected PGUSER=postgres, got %v", env)
	}
}

func TestPgDumpEnv_overrideHSERVER(t *testing.T) {
	t.Setenv("HSERVER_PG_BACKUP_USER", "backuprole")
	t.Setenv("PGUSER", "ignored")
	env := pgDumpEnv()
	if !containsEnv(env, "PGUSER=backuprole") {
		t.Fatalf("expected PGUSER=backuprole, got %v", env)
	}
}

func TestPgDumpEnv_hostPort(t *testing.T) {
	t.Setenv("HSERVER_PG_BACKUP_HOST", "127.0.0.1")
	t.Setenv("HSERVER_PG_BACKUP_PORT", "5433")
	env := pgDumpEnv()
	if !containsEnv(env, "PGHOST=127.0.0.1") || !containsEnv(env, "PGPORT=5433") {
		t.Fatalf("expected PGHOST/PGPORT, got %v", env)
	}
}

func containsEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func TestPgUserFromEnv(t *testing.T) {
	t.Setenv("HSERVER_PG_BACKUP_USER", "")
	t.Setenv("PGUSER", "")
	if pgUserFromEnv() != "postgres" {
		t.Fatalf("expected postgres, got %s", pgUserFromEnv())
	}
}

func TestPgDumpCommand_usesSudoPostgres(t *testing.T) {
	t.Setenv("HSERVER_PG_BACKUP_USER", "postgres")
	cmd := pgDumpCommand(context.Background(), "all", "postgresql")
	if cmd.Path != "/usr/bin/sudo" && cmd.Path != "sudo" {
		t.Fatalf("expected sudo wrapper, got %s", cmd.Path)
	}
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "-u postgres -- env PGUSER=postgres pg_dumpall") {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestPgRestoreCommandForwardsProviderNeutralConnectionSettings(t *testing.T) {
	t.Setenv("HSERVER_PG_RUN_AS", "dboperator")
	t.Setenv("HSERVER_PG_BACKUP_USER", "backuprole")
	t.Setenv("HSERVER_PG_BACKUP_HOST", "127.0.0.1")
	t.Setenv("HSERVER_PG_BACKUP_PORT", "55432")
	t.Setenv("HSERVER_PG_PASSFILE", "/etc/hserver/postgres.pass")

	cmd := pgRestoreCommand(context.Background(), "application")
	args := strings.Join(cmd.Args, " ")
	for _, expected := range []string{
		"-u dboperator -- env",
		"PGUSER=backuprole",
		"PGHOST=127.0.0.1",
		"PGPORT=55432",
		"PGPASSFILE=/etc/hserver/postgres.pass",
		"psql -v ON_ERROR_STOP=1 application",
	} {
		if !strings.Contains(args, expected) {
			t.Fatalf("restore args %q missing %q", args, expected)
		}
	}
}

func TestDatabaseTargetMetadataMustMatchBackupFilename(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "nightly-db-expected-postgresql.sql.gz")
	gzipFixture(t, archive, []byte(databaseBackupHeader("other")+"SELECT 1;\n"))
	if _, err := databaseTargetFromBackup(archive, "postgresql"); err == nil || !strings.Contains(err.Error(), "does not match backup filename") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateDatabaseBackupSize_rejectsTiny(t *testing.T) {
	if err := validateDatabaseBackupSize(20); err == nil {
		t.Fatal("expected error for 20 byte dump")
	}
	if err := validateDatabaseBackupSize(1024); err != nil {
		t.Fatal(err)
	}
}

func TestPgDumpEnv_singlePGUSEREntry(t *testing.T) {
	t.Setenv("HSERVER_PG_BACKUP_USER", "backuprole")
	t.Setenv("PGUSER", "root")
	env := pgDumpEnv()
	var pgusers []string
	for _, e := range env {
		if strings.HasPrefix(e, "PGUSER=") {
			pgusers = append(pgusers, e)
		}
	}
	if len(pgusers) != 1 || pgusers[0] != "PGUSER=backuprole" {
		t.Fatalf("want single PGUSER=backuprole, got %v", pgusers)
	}
}

func TestPgDumpEnv_inheritsOS(t *testing.T) {
	key := "HSERVER_PG_ENV_TEST_" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Setenv(key, "probe")
	t.Setenv("HSERVER_PG_BACKUP_USER", "")
	env := pgDumpEnv()
	found := false
	for _, e := range env {
		if e == key+"=probe" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pgDumpEnv should inherit os.Environ(), missing %s", key)
	}
	_ = os.Unsetenv(key)
}
