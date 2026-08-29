package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runPostgreSQLDrillQuery(t *testing.T, database, sql string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := pgRestoreCommand(ctx, database)
	cmd.Stdin = strings.NewReader(sql)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("psql query failed: %v: %s", err, output)
	}
	return string(output)
}

func TestPostgreSQLBackupRestoreDrill(t *testing.T) {
	if os.Getenv("HSERVER_RUN_POSTGRES_RESTORE_DRILL") != "1" {
		t.Skip("set HSERVER_RUN_POSTGRES_RESTORE_DRILL=1 on a disposable PostgreSQL cluster")
	}
	database := os.Getenv("HSERVER_TEST_PG_DATABASE")
	if database == "" {
		t.Fatal("HSERVER_TEST_PG_DATABASE is required")
	}

	runPostgreSQLDrillQuery(t, database, `
DROP TABLE IF EXISTS hserver_restore_probe;
CREATE TABLE hserver_restore_probe (marker text NOT NULL);
INSERT INTO hserver_restore_probe(marker) VALUES ('backup-state');
`)
	service := NewAt(t.TempDir())
	archive := filepath.Join(service.BackupDir(), "drill-db-"+database+"-postgresql.sql.gz")
	if err := pgDumpGzip(database, "postgresql", archive, 6, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	runPostgreSQLDrillQuery(t, database, "UPDATE hserver_restore_probe SET marker='changed-after-backup';")

	output, err := service.restoreDatabaseSafely(archive, "postgresql", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Recovery backup:") {
		t.Fatalf("restore output = %q", output)
	}
	recoveryFiles, err := filepath.Glob(filepath.Join(service.BackupDir(), "pre-restore-*-db-"+database+"-postgresql.sql.gz"))
	if err != nil || len(recoveryFiles) != 1 {
		t.Fatalf("recovery files = %v, %v", recoveryFiles, err)
	}
	output = runPostgreSQLDrillQuery(t, database, "SELECT marker FROM hserver_restore_probe;")
	if !strings.Contains(output, "backup-state") || strings.Contains(output, "changed-after-backup") {
		t.Fatalf("restored marker output = %q", output)
	}

	brokenArchive := filepath.Join(service.BackupDir(), "broken-db-"+database+"-postgresql.sql.gz")
	brokenSQL := databaseBackupHeader(database) + `
UPDATE hserver_restore_probe SET marker='partially-mutated';
SELECT * FROM hserver_restore_table_that_does_not_exist;
`
	brokenSQL += strings.Repeat("-- deliberate restore failure payload\n", 16)
	gzipFixture(t, brokenArchive, []byte(brokenSQL))
	if _, err := service.restoreDatabaseSafely(brokenArchive, "postgresql", 30*time.Second); err == nil || !strings.Contains(err.Error(), "was rolled back") {
		t.Fatalf("failed restore error = %v", err)
	}
	output = runPostgreSQLDrillQuery(t, database, "SELECT marker FROM hserver_restore_probe;")
	if !strings.Contains(output, "backup-state") || strings.Contains(output, "partially-mutated") {
		t.Fatalf("rolled back marker output = %q", output)
	}
}

func runMariaDBDrillQuery(t *testing.T, database, sql string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := mysqlRestoreCommand(ctx, database)
	cmd.Stdin = strings.NewReader(sql)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("MariaDB query failed: %v: %s", err, output)
	}
	return string(output)
}

func TestMariaDBBackupRestoreDrill(t *testing.T) {
	if os.Getenv("HSERVER_RUN_MARIADB_RESTORE_DRILL") != "1" {
		t.Skip("set HSERVER_RUN_MARIADB_RESTORE_DRILL=1 on a disposable MariaDB instance")
	}
	database := os.Getenv("HSERVER_TEST_MYSQL_DATABASE")
	if database == "" {
		t.Fatal("HSERVER_TEST_MYSQL_DATABASE is required")
	}

	runMariaDBDrillQuery(t, "all", "CREATE DATABASE IF NOT EXISTS `"+database+"`;\n")
	runMariaDBDrillQuery(t, database, `
DROP TABLE IF EXISTS hserver_restore_probe;
CREATE TABLE hserver_restore_probe (marker varchar(255) NOT NULL);
INSERT INTO hserver_restore_probe(marker) VALUES ('backup-state');
`)
	service := NewAt(t.TempDir())
	archive := filepath.Join(service.BackupDir(), "drill-db-"+database+"-mariadb.sql.gz")
	if err := pgDumpGzip(database, "mariadb", archive, 6, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	runMariaDBDrillQuery(t, database, "UPDATE hserver_restore_probe SET marker='changed-after-backup';")

	output, err := service.restoreDatabaseSafely(archive, "mariadb", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Recovery backup:") {
		t.Fatalf("restore output = %q", output)
	}
	output = runMariaDBDrillQuery(t, database, "SELECT marker FROM hserver_restore_probe;")
	if !strings.Contains(output, "backup-state") || strings.Contains(output, "changed-after-backup") {
		t.Fatalf("restored marker output = %q", output)
	}

	brokenArchive := filepath.Join(service.BackupDir(), "broken-db-"+database+"-mariadb.sql.gz")
	brokenSQL := databaseBackupHeader(database) + `
UPDATE hserver_restore_probe SET marker='partially-mutated';
SELECT * FROM hserver_restore_table_that_does_not_exist;
`
	brokenSQL += strings.Repeat("-- deliberate restore failure payload\n", 16)
	gzipFixture(t, brokenArchive, []byte(brokenSQL))
	if _, err := service.restoreDatabaseSafely(brokenArchive, "mariadb", 30*time.Second); err == nil || !strings.Contains(err.Error(), "was rolled back") {
		t.Fatalf("failed restore error = %v", err)
	}
	output = runMariaDBDrillQuery(t, database, "SELECT marker FROM hserver_restore_probe;")
	if !strings.Contains(output, "backup-state") || strings.Contains(output, "partially-mutated") {
		t.Fatalf("rolled back marker output = %q", output)
	}
}

func TestPgDumpGzipWritesRestoreTargetMetadata(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sudoPath := filepath.Join(binDir, "sudo")
	script := `#!/bin/sh
set -eu
printf '%s\n' 'CREATE TABLE restored_from_dump (id INTEGER);'
`
	if err := os.WriteFile(sudoPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HSERVER_PG_RUN_AS", "postgres")
	t.Setenv("HSERVER_PG_BACKUP_USER", "postgres")
	t.Setenv("HSERVER_PG_BACKUP_HOST", "")
	t.Setenv("HSERVER_PG_BACKUP_PORT", "")
	t.Setenv("HSERVER_PG_PASSFILE", "")

	archive := filepath.Join(dir, "nightly-db-application-postgresql.sql.gz")
	if err := pgDumpGzip("application", "postgresql", archive, 6, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if target, err := databaseTargetFromBackup(archive, "postgresql"); err != nil || target != "application" {
		t.Fatalf("database target = %q, %v", target, err)
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte("CREATE TABLE restored_from_dump")) {
		t.Fatalf("compressed dump payload = %q", contents)
	}
}

func TestRestoreDatabaseBackupStreamsGzipIntoPostgreSQLClient(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stdinPath := filepath.Join(dir, "stdin.sql")
	argsPath := filepath.Join(dir, "args.txt")
	sudoPath := filepath.Join(binDir, "sudo")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >"${HSERVER_TEST_RESTORE_ARGS:?}"
cat >"${HSERVER_TEST_RESTORE_STDIN:?}"
printf '%s\n' 'postgresql restore client completed'
`
	if err := os.WriteFile(sudoPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HSERVER_TEST_RESTORE_STDIN", stdinPath)
	t.Setenv("HSERVER_TEST_RESTORE_ARGS", argsPath)
	t.Setenv("HSERVER_PG_RUN_AS", "postgres")
	t.Setenv("HSERVER_PG_BACKUP_USER", "postgres")
	t.Setenv("HSERVER_PG_BACKUP_HOST", "")
	t.Setenv("HSERVER_PG_BACKUP_PORT", "")
	t.Setenv("HSERVER_PG_PASSFILE", "")

	sql := bytes.Repeat([]byte("INSERT INTO restore_probe VALUES ('restored');\n"), 64)
	archive := filepath.Join(dir, "nightly-partial-db-all-postgresql.sql.gz")
	gzipFixture(t, archive, sql)

	output, err := restoreDatabaseBackup(archive, "postgresql", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "restore client completed") {
		t.Fatalf("restore output = %q", output)
	}
	gotSQL, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotSQL, sql) {
		t.Fatal("PostgreSQL restore client did not receive the complete decompressed SQL stream")
	}
	gotArgs, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(gotArgs)) != "-n -u postgres -- env PGUSER=postgres psql -v ON_ERROR_STOP=1 postgres" {
		t.Fatalf("restore client args = %q", gotArgs)
	}
}
