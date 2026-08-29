package database

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListPGMCredentials_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		if command == "sudo" && strings.Contains(strings.Join(args, " "), "pgm_metadata") {
			return "1\tshop\tshopuser\tsecret\t127.0.0.1\t5432\t\t\tt\t2026-01-01\n", nil
		}
		return "", nil
	}
	creds, err := ListPGMCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 || creds[0].DBName != "shop" {
		t.Fatalf("creds = %+v", creds)
	}
}

func TestGetPGMCredential_mockedExec(t *testing.T) {
	old := rawExec
	defer func() { rawExec = old }()
	rawExec = func(_ time.Duration, command string, args ...string) (string, error) {
		return "1\tapp\tu\tp\t127.0.0.1\t5432\tconn\tnote\tt\t2026-01-01\n", nil
	}
	cred, err := GetPGMCredential("app")
	if err != nil {
		t.Fatal(err)
	}
	if cred.DBName != "app" {
		t.Fatalf("cred = %+v", cred)
	}
}

func TestFormatPGMBackupDirName(t *testing.T) {
	got := formatPGMBackupDirName("20260409_060007")
	want := "2026-04-09 06:00:07"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if formatPGMBackupDirName("short") != "" {
		t.Fatal("expected empty for short name")
	}
}

func TestConfiguredPGMBackupRoot(t *testing.T) {
	t.Setenv("HSERVER_PGM_BACKUP_DIR", "/srv/hserver/database-backups/")
	if got, want := configuredPGMBackupRoot(), "/srv/hserver/database-backups"; got != want {
		t.Fatalf("configuredPGMBackupRoot() = %q, want %q", got, want)
	}

	t.Setenv("HSERVER_PGM_BACKUP_DIR", "")
	if got := configuredPGMBackupRoot(); got != defaultPGMBackupDir {
		t.Fatalf("configuredPGMBackupRoot() = %q, want default %q", got, defaultPGMBackupDir)
	}
}

func TestListPGMBackupFiles_invalidName(t *testing.T) {
	for _, name := range []string{"../escape", strings.Repeat("x", 129)} {
		_, err := ListPGMBackupFiles(name)
		if !errors.Is(err, ErrInvalidBackupInput) {
			t.Fatalf("name=%q err=%v want ErrInvalidBackupInput", name, err)
		}
	}
}

func TestRestorePGMBackup_rejectsOutsideRoot(t *testing.T) {
	err := RestorePGMBackup("app", "/etc/passwd")
	if !errors.Is(err, ErrInvalidBackupInput) {
		t.Fatalf("err=%v want ErrInvalidBackupInput", err)
	}
}

func TestRestorePGMBackup_invalidDBName(t *testing.T) {
	err := RestorePGMBackup("bad name!", filepath.Join(pgmBackupRoot, "20260101_000000", "x.sql"))
	if err == nil {
		t.Fatal("expected invalid db name error")
	}
}

func TestRestorePGMBackupRejectsSymlink(t *testing.T) {
	oldRoot := pgmBackupRoot
	t.Cleanup(func() { pgmBackupRoot = oldRoot })

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.sql")
	if err := os.WriteFile(outside, []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "app.sql")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	pgmBackupRoot = root

	if err := RestorePGMBackup("app", link); !errors.Is(err, ErrInvalidBackupInput) {
		t.Fatalf("err=%v want ErrInvalidBackupInput", err)
	}
}

func TestListPGMBackups_tempDir(t *testing.T) {
	oldRoot := pgmBackupRoot
	t.Cleanup(func() { pgmBackupRoot = oldRoot })

	dir := t.TempDir()
	backupDir := filepath.Join(dir, "20260409_060007")
	if err := os.Mkdir(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "app.sql.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "invalid backup"), 0o755); err != nil {
		t.Fatal(err)
	}
	pgmBackupRoot = dir

	backups, err := ListPGMBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %+v", backups)
	}
	if backups[0].Databases != 1 || backups[0].CreatedAt != "2026-04-09 06:00:07" {
		t.Fatalf("backup = %+v", backups[0])
	}
}

func TestPGMBackupRootAvailability(t *testing.T) {
	oldRoot := pgmBackupRoot
	t.Cleanup(func() { pgmBackupRoot = oldRoot })

	missingRoot := filepath.Join(t.TempDir(), "missing")
	pgmBackupRoot = missingRoot

	if _, err := ListPGMBackups(); !errors.Is(err, ErrBackupRootUnavailable) {
		t.Fatalf("ListPGMBackups err=%v want ErrBackupRootUnavailable", err)
	}
	if _, err := ListPGMBackupFiles("20260409_060007"); !errors.Is(err, ErrBackupRootUnavailable) {
		t.Fatalf("ListPGMBackupFiles err=%v want ErrBackupRootUnavailable", err)
	}
	restorePath := filepath.Join(missingRoot, "20260409_060007", "app.sql")
	if err := RestorePGMBackup("app", restorePath); !errors.Is(err, ErrBackupRootUnavailable) {
		t.Fatalf("RestorePGMBackup err=%v want ErrBackupRootUnavailable", err)
	}
}

func TestPGMBackupHealthyEmptyAndMissingChild(t *testing.T) {
	oldRoot := pgmBackupRoot
	t.Cleanup(func() { pgmBackupRoot = oldRoot })

	pgmBackupRoot = t.TempDir()
	backups, err := ListPGMBackups()
	if err != nil {
		t.Fatal(err)
	}
	if backups == nil || len(backups) != 0 {
		t.Fatalf("backups=%v want non-nil empty inventory", backups)
	}
	if _, err := ListPGMBackupFiles("20260409_060007"); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("ListPGMBackupFiles err=%v want ErrBackupNotFound", err)
	}
}

func TestRestorePGMBackupRejectsOversizedPath(t *testing.T) {
	err := RestorePGMBackup("app", "/"+strings.Repeat("x", 4090)+".sql")
	if !errors.Is(err, ErrInvalidBackupInput) {
		t.Fatalf("err=%v want ErrInvalidBackupInput", err)
	}
}

func TestListPGMBackupFiles_readsDir(t *testing.T) {
	oldRoot := pgmBackupRoot
	t.Cleanup(func() { pgmBackupRoot = oldRoot })

	dir := t.TempDir()
	name := "20260409_060007"
	backupDir := filepath.Join(dir, name)
	if err := os.Mkdir(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.sql.gz", "b.sql", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(backupDir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pgmBackupRoot = dir

	files, err := ListPGMBackupFiles(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "a.sql.gz" || files[1] != "b.sql" {
		t.Fatalf("files = %v", files)
	}
}

func TestParsePGMCredentialLines(t *testing.T) {
	out := "1\tmydb\tmyuser\tsecret\t127.0.0.1\t5432\tconn\tnote\tt\t2026-01-01\n"
	creds := parsePGMCredentialLines(out)
	if len(creds) != 1 {
		t.Fatalf("len=%d", len(creds))
	}
	c := creds[0]
	if c.DBName != "mydb" || c.DBPort != 5432 || !c.IsActive {
		t.Errorf("cred = %+v", c)
	}
}

func TestParsePGMCredentialLinesSkipsShortRows(t *testing.T) {
	if len(parsePGMCredentialLines("1\tshort\n")) != 0 {
		t.Fatal("expected skip short row")
	}
}
