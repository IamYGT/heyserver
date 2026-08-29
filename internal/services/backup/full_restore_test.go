package backup

import (
	"archive/tar"
	"compress/gzip"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/services/shell"
)

type tarFixtureEntry struct {
	name string
	data []byte
}

func writeTarGzipFixture(t *testing.T, filePath string, entries []tarFixtureEntry) {
	t.Helper()
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(entry.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func gzipFixture(t *testing.T, filePath string, data []byte) {
	t.Helper()
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func readGzipFixture(t *testing.T, filePath string) []byte {
	t.Helper()
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRestoreFullBundleRunsNestedDatabaseAndFilesStages(t *testing.T) {
	dir := t.TempDir()
	databasePart := filepath.Join(dir, "nightly-partial-db-all-postgresql.sql.gz")
	filesPart := filepath.Join(dir, "nightly-partial-files.tar.gz")
	databaseSQL := make([]byte, 8192)
	for i := range databaseSQL {
		databaseSQL[i] = byte((i*37 + 11) % 251)
	}
	gzipFixture(t, databasePart, databaseSQL)
	writeTarGzipFixture(t, filesPart, []tarFixtureEntry{{
		name: "var/www/vhosts/example.test/httpdocs/index.html",
		data: []byte("working site"),
	}})

	databaseBytes, err := os.ReadFile(databasePart)
	if err != nil {
		t.Fatal(err)
	}
	filesBytes, err := os.ReadFile(filesPart)
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(dir, "nightly-full.tar.gz")
	writeTarGzipFixture(t, bundlePath, []tarFixtureEntry{
		{name: "opt/hserver-panel/data/backups/" + filepath.Base(databasePart), data: databaseBytes},
		{name: "opt/hserver-panel/data/backups/" + filepath.Base(filesPart), data: filesBytes},
	})

	var stages []string
	svc := NewAt(dir)
	output, err := svc.restoreFullBundle(
		bundlePath,
		func(filePath, engine string, _ time.Duration) (string, error) {
			stages = append(stages, "database:"+engine)
			if got := readGzipFixture(t, filePath); !reflect.DeepEqual(got, databaseSQL) {
				return "", fmt.Errorf("database payload mismatch")
			}
			return "database restored", nil
		},
		func(filePath string) (string, error) {
			stages = append(stages, "files")
			if err := validateFilesRestoreInput(filePath); err != nil {
				return "", err
			}
			return "files restored", nil
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"database:postgresql", "files"}; !reflect.DeepEqual(stages, want) {
		t.Fatalf("restore stages = %v, want %v", stages, want)
	}
	if !strings.Contains(output, "database restored") || !strings.Contains(output, "files restored") {
		t.Fatalf("restore output = %q", output)
	}
}

func TestRestoreFullBundleRollsBackDatabaseWhenFilesStageFails(t *testing.T) {
	dir := t.TempDir()
	databasePart := filepath.Join(dir, "nightly-partial-db-all-postgresql.sql.gz")
	databaseSQL := make([]byte, 8192)
	for i := range databaseSQL {
		databaseSQL[i] = byte((i*41 + 13) % 251)
	}
	gzipFixture(t, databasePart, databaseSQL)
	filesPart := filepath.Join(dir, "nightly-partial-files.tar.gz")
	writeTarGzipFixture(t, filesPart, []tarFixtureEntry{{name: "var/www/example.txt", data: []byte("ok")}})
	databaseBytes, _ := os.ReadFile(databasePart)
	filesBytes, _ := os.ReadFile(filesPart)
	bundlePath := filepath.Join(dir, "nightly-full.tar.gz")
	writeTarGzipFixture(t, bundlePath, []tarFixtureEntry{
		{name: filepath.Base(databasePart), data: databaseBytes},
		{name: filepath.Base(filesPart), data: filesBytes},
	})

	recoveryPath := filepath.Join(dir, "pre-restore-db-all-postgresql.sql.gz")
	var restored []string
	svc := NewAt(dir)
	_, err := svc.restoreFullBundle(
		bundlePath,
		func(filePath, _ string, _ time.Duration) (string, error) {
			restored = append(restored, filePath)
			return "database restored", nil
		},
		func(string) (string, error) { return "", fmt.Errorf("files restore failed") },
		func(string, string) (string, error) { return recoveryPath, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "database was rolled back") {
		t.Fatalf("error = %v", err)
	}
	if len(restored) != 2 || restored[1] != recoveryPath {
		t.Fatalf("database restore calls = %v", restored)
	}
}

func TestValidateRestoreReportsDatabaseRecoveryWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "nightly-db-application-postgresql.sql.gz")
	payload := append([]byte(databaseBackupHeader("application")), make([]byte, 8192)...)
	if _, err := cryptorand.Read(payload[len(databaseBackupHeader("application")):]); err != nil {
		t.Fatal(err)
	}
	gzipFixture(t, archive, payload)

	svc := NewAt(dir)
	backups, err := svc.List()
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	result, err := svc.ValidateRestore(backups[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IncludesDatabase || result.IncludesFiles || !result.DatabaseRecovery || result.FilesRollback {
		t.Fatalf("validation = %+v", result)
	}
	if result.DatabaseEngine != "postgresql" || result.DatabaseTarget != "application" {
		t.Fatalf("database validation = %+v", result)
	}
	if recovery, _ := filepath.Glob(filepath.Join(dir, "pre-restore-*")); len(recovery) != 0 {
		t.Fatalf("validation created recovery artifacts: %v", recovery)
	}
}

func TestValidateRestoreInspectsBothFullBundleStages(t *testing.T) {
	dir := t.TempDir()
	databasePart := filepath.Join(dir, "nightly-partial-db-application-mariadb.sql.gz")
	databasePayload := append([]byte(databaseBackupHeader("application")), make([]byte, 8192)...)
	if _, err := cryptorand.Read(databasePayload[len(databaseBackupHeader("application")):]); err != nil {
		t.Fatal(err)
	}
	gzipFixture(t, databasePart, databasePayload)
	filesPart := filepath.Join(dir, "nightly-partial-files.tar.gz")
	filePayload := make([]byte, 8192)
	if _, err := cryptorand.Read(filePayload); err != nil {
		t.Fatal(err)
	}
	writeTarGzipFixture(t, filesPart, []tarFixtureEntry{{name: "var/www/example.test/data.bin", data: filePayload}})
	databaseBytes, _ := os.ReadFile(databasePart)
	filesBytes, _ := os.ReadFile(filesPart)
	bundlePath := filepath.Join(dir, "nightly-full.tar.gz")
	writeTarGzipFixture(t, bundlePath, []tarFixtureEntry{
		{name: filepath.Base(databasePart), data: databaseBytes},
		{name: filepath.Base(filesPart), data: filesBytes},
	})

	svc := NewAt(dir)
	backups, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	var bundleID string
	for _, backup := range backups {
		if backup.Name == filepath.Base(bundlePath) {
			bundleID = backup.ID
		}
	}
	result, err := svc.ValidateRestore(bundleID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IncludesDatabase || !result.IncludesFiles || !result.DatabaseRecovery || !result.FilesRollback {
		t.Fatalf("validation = %+v", result)
	}
	if result.DatabaseEngine != "mariadb" || result.DatabaseTarget != "application" {
		t.Fatalf("database validation = %+v", result)
	}
	if staging, _ := filepath.Glob(filepath.Join(dir, ".full-validate-*")); len(staging) != 0 {
		t.Fatalf("validation staging was not removed: %v", staging)
	}
}

func TestValidateRestoreReportsAutomaticFileRollbackWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "nightly-files.tar.gz")
	payload := make([]byte, 8192)
	if _, err := cryptorand.Read(payload); err != nil {
		t.Fatal(err)
	}
	writeTarGzipFixture(t, archive, []tarFixtureEntry{{name: "srv/example/data.bin", data: payload}})

	svc := NewAt(dir)
	backups, err := svc.List()
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	result, err := svc.ValidateRestore(backups[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.IncludesDatabase || !result.IncludesFiles || result.DatabaseRecovery || !result.FilesRollback {
		t.Fatalf("validation = %+v", result)
	}
	if recovery, _ := filepath.Glob(filepath.Join(dir, "pre-restore-*")); len(recovery) != 0 {
		t.Fatalf("validation created recovery artifacts: %v", recovery)
	}
}

func TestRestoreFullBundleRejectsMissingPartBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	filesPart := filepath.Join(dir, "nightly-partial-files.tar.gz")
	writeTarGzipFixture(t, filesPart, []tarFixtureEntry{{name: "var/www/example.txt", data: []byte("ok")}})
	filesBytes, err := os.ReadFile(filesPart)
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(dir, "nightly-full.tar.gz")
	writeTarGzipFixture(t, bundlePath, []tarFixtureEntry{{name: filepath.Base(filesPart), data: filesBytes}})

	called := false
	svc := NewAt(dir)
	_, err = svc.restoreFullBundle(
		bundlePath,
		func(string, string, time.Duration) (string, error) { called = true; return "", nil },
		func(string) (string, error) { called = true; return "", nil },
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "exactly one database part and one files part") {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("a restore stage ran before bundle validation completed")
	}
}

func TestRestoreFullBundleRejectsUnsafeNestedFilesArchiveBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	databasePart := filepath.Join(dir, "nightly-partial-db-all-mariadb.sql.gz")
	databaseSQL := make([]byte, 8192)
	for i := range databaseSQL {
		databaseSQL[i] = byte((i*29 + 7) % 251)
	}
	gzipFixture(t, databasePart, databaseSQL)
	filesPart := filepath.Join(dir, "nightly-partial-files.tar.gz")
	writeTarGzipFixture(t, filesPart, []tarFixtureEntry{{name: "../../outside", data: []byte("no")}})
	databaseBytes, _ := os.ReadFile(databasePart)
	filesBytes, _ := os.ReadFile(filesPart)
	bundlePath := filepath.Join(dir, "nightly-full.tar.gz")
	writeTarGzipFixture(t, bundlePath, []tarFixtureEntry{
		{name: filepath.Base(databasePart), data: databaseBytes},
		{name: filepath.Base(filesPart), data: filesBytes},
	})

	called := false
	svc := NewAt(dir)
	_, err := svc.restoreFullBundle(
		bundlePath,
		func(string, string, time.Duration) (string, error) { called = true; return "", nil },
		func(string) (string, error) { called = true; return "", nil },
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "unsafe files archive path") {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("a restore stage ran for an unsafe bundle")
	}
}

func TestFullArchiveArgsStoreOnlyPartBasenames(t *testing.T) {
	args := fullArchiveArgs(
		"/backups/nightly-full.tar.gz",
		"/backups",
		[]string{"/backups/nightly-partial-db-all-postgresql.sql.gz", "/backups/nightly-partial-files.tar.gz"},
	)
	want := []string{
		"-czf", "/backups/nightly-full.tar.gz", "-C", "/backups",
		"nightly-partial-db-all-postgresql.sql.gz", "nightly-partial-files.tar.gz",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestExecuteCheckedRejectsNonZeroExit(t *testing.T) {
	_, err := executeChecked(time.Second, "tar", "--definitely-invalid-option")
	if err == nil || !strings.Contains(err.Error(), "exit") {
		t.Fatalf("error = %v", err)
	}
}

func TestRestoreFilesBackupAtExtractsValidatedArchive(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "nightly-partial-files.tar.gz")
	relative := "var/www/vhosts/example.test/httpdocs/index.html"
	writeTarGzipFixture(t, archive, []tarFixtureEntry{{
		name: relative,
		data: []byte("restored site"),
	}})
	target := filepath.Join(dir, "restore-target")
	targetFile := filepath.Join(target, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(targetFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetFile, []byte("current site"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := restoreFilesBackupAt(archive, target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Recovery backup:") {
		t.Fatalf("restore output = %q", output)
	}
	got, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "restored site" {
		t.Fatalf("restored content = %q", got)
	}
	recoveries, err := filepath.Glob(filepath.Join(dir, "pre-restore-*-files.tar.gz"))
	if err != nil || len(recoveries) != 1 {
		t.Fatalf("recoveries = %v, %v", recoveries, err)
	}
	info, err := os.Stat(recoveries[0])
	if err != nil || BackupValidity(filepath.Base(recoveries[0]), info.Size()) != "completed" {
		t.Fatalf("recovery is not a completed backup: %v, %v", info, err)
	}
	if err := validateFilesRestoreInput(recoveries[0]); err != nil {
		t.Fatalf("recovery validation failed: %v", err)
	}
	if _, err := restoreFilesBackupAt(recoveries[0], target); err != nil {
		t.Fatalf("manual recovery restore failed: %v", err)
	}
	got, err = os.ReadFile(targetFile)
	if err != nil || string(got) != "current site" {
		t.Fatalf("recovered content = %q, %v", got, err)
	}
}

func TestRestoreFilesBackupAtRollsBackPartialExtractionFailure(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	target := filepath.Join(dir, "restore-target")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	existingRelative := "srv/example/existing.bin"
	newRelative := "srv/example/new.bin"
	existingPath := filepath.Join(target, filepath.FromSlash(existingRelative))
	newPath := filepath.Join(target, filepath.FromSlash(newRelative))
	if err := os.MkdirAll(filepath.Dir(existingPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := make([]byte, 8192)
	if _, err := cryptorand.Read(original); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(backupDir, "nightly-files.tar.gz")
	writeTarGzipFixture(t, archive, []tarFixtureEntry{
		{name: existingRelative, data: []byte("replacement")},
		{name: newRelative, data: []byte("new payload")},
	})

	executor := func(timeout time.Duration, command string, args ...string) (*shell.Result, error) {
		if command == "tar" && containsArgument(args, "--extract") && containsArgument(args, archive) {
			if err := os.WriteFile(existingPath, []byte("partially changed"), 0o600); err != nil {
				return nil, err
			}
			if err := os.WriteFile(newPath, []byte("partially created"), 0o600); err != nil {
				return nil, err
			}
			return &shell.Result{ExitCode: 2, Stderr: "injected extraction failure"}, fmt.Errorf("injected extraction failure")
		}
		return executeChecked(timeout, command, args...)
	}

	_, err := restoreFilesBackupAtWithExecutor(archive, target, executor)
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("error = %v", err)
	}
	recovered, readErr := os.ReadFile(existingPath)
	if readErr != nil || !reflect.DeepEqual(recovered, original) {
		t.Fatalf("existing file was not rolled back: %v", readErr)
	}
	if _, statErr := os.Lstat(newPath); !os.IsNotExist(statErr) {
		t.Fatalf("new partial path was not removed: %v", statErr)
	}
}

func containsArgument(args []string, expected string) bool {
	for _, arg := range args {
		if arg == expected {
			return true
		}
	}
	return false
}

func TestRestoreFilesBackupAtRejectsEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "nightly-partial-files.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "var/www/vhosts/example.test/current",
		Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "../../../../../outside",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := restoreFilesBackupAt(archive, dir); err == nil || !strings.Contains(err.Error(), "unsafe files archive symlink") {
		t.Fatalf("error = %v", err)
	}
}

func TestRestoreFilesBackupAtRejectsArchiveSymlinkTraversal(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "nightly-partial-files.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "srv/example/current",
		Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "releases/live",
	}); err != nil {
		t.Fatal(err)
	}
	payload := []byte("must not traverse archive symlink")
	if err := tw.WriteHeader(&tar.Header{
		Name: "srv/example/current/index.html",
		Mode: 0o600, Size: int64(len(payload)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := restoreFilesBackupAt(archive, dir); err == nil || !strings.Contains(err.Error(), "traverses archive symlink") {
		t.Fatalf("error = %v", err)
	}
}

func TestRestoreFilesBackupAtRejectsExistingParentSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "restore-target")
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(filepath.Join(target, "srv"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "srv", "linked")); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "nightly-partial-files.tar.gz")
	writeTarGzipFixture(t, archive, []tarFixtureEntry{{
		name: "srv/linked/index.html",
		data: []byte("must stay inside target"),
	}})

	if _, err := restoreFilesBackupAt(archive, target); err == nil || !strings.Contains(err.Error(), "traverses existing symlink") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "index.html")); !os.IsNotExist(err) {
		t.Fatalf("restore wrote outside target: %v", err)
	}
}
