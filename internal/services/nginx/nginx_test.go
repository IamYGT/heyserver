package nginx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLeavesVhostsRootUnconfigured(t *testing.T) {
	t.Parallel()

	svc := New()
	if svc.vhostsRoot != "" {
		t.Fatalf("New() vhostsRoot = %q, want unconfigured", svc.vhostsRoot)
	}
}

func TestService_SaveConfigIsChecksumLockedAtomicAndValidated(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(available, "example.conf")
	original := []byte("server { listen 80; }\n")
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}
	validator := filepath.Join(root, "nginx")
	script := "#!/bin/sh\nif /bin/grep -q INVALID \"$HSERVER_TEST_NGINX_CONFIG\"; then echo invalid >&2; exit 1; fi\n"
	if err := os.WriteFile(validator, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("HSERVER_TEST_NGINX_CONFIG", target)
	svc := NewWithConfig(ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	observed, err := svc.GetConfig("example.conf")
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := sha256.Sum256(original)
	if observed.Checksum != hex.EncodeToString(originalDigest[:]) || observed.Size != int64(len(original)) {
		t.Fatalf("observed config = %#v", observed)
	}

	replacement := "server { listen 8080; }\n"
	receipt, err := svc.SaveConfig("example.conf", replacement, observed.Checksum)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if receipt.Backup == "" || receipt.Checksum == observed.Checksum {
		t.Fatalf("receipt = %#v", receipt)
	}
	if backup, err := os.ReadFile(filepath.Join(available, receipt.Backup)); err != nil || string(backup) != string(original) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != replacement {
		t.Fatalf("saved = %q, %v", content, err)
	}
	if matches, _ := filepath.Glob(filepath.Join(enabled, ".hserver-validation-*.conf")); len(matches) != 0 {
		t.Fatalf("validation links remain: %v", matches)
	}

	if _, err := svc.SaveConfig("example.conf", "server {}\n", observed.Checksum); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale checksum error = %v", err)
	}
	current, err := svc.GetConfig("example.conf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveConfig("example.conf", "INVALID\n", current.Checksum); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("invalid config error = %v", err)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != replacement {
		t.Fatalf("rollback = %q, %v", content, err)
	}
}

func TestService_UsesInstallationOwnedNginxAndVhostRoots(t *testing.T) {
	root := t.TempDir()
	nginxRoot := filepath.Join(root, "nginx")
	sitesAvailable := filepath.Join(nginxRoot, "available")
	sitesEnabled := filepath.Join(nginxRoot, "enabled")
	vhostsRoot := filepath.Join(root, "sites")
	for _, dir := range []string{sitesAvailable, sitesEnabled, filepath.Join(nginxRoot, "snippets"), vhostsRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nginxRoot, "snippets", "security.conf"), []byte("add_header X-Test ok;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredManagedSnippetNames {
		if err := os.WriteFile(filepath.Join(nginxRoot, "snippets", name), []byte("# test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	validatorDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(validatorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(validatorDir, "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", validatorDir)

	svc := NewWithConfig(ServiceConfig{
		SitesAvailable: sitesAvailable,
		SitesEnabled:   sitesEnabled,
		VhostsRoot:     vhostsRoot,
		SnippetsDir:    filepath.Join(nginxRoot, "snippets"),
	})
	created, err := svc.CreateConfig(CreateRequest{Domain: "example.com", Type: "static"})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	wantDocRoot := filepath.Join(vhostsRoot, "example.com", "httpdocs")
	if !strings.Contains(created.Content, "root "+wantDocRoot+";") {
		t.Fatalf("generated config does not use %q:\n%s", wantDocRoot, created.Content)
	}
	if !strings.Contains(created.Content, filepath.Join(nginxRoot, "snippets", "hserver-static-cache.conf")) {
		t.Fatalf("generated config does not use the configured managed snippets:\n%s", created.Content)
	}
	if _, err := os.Stat(filepath.Join(sitesAvailable, "example.com.conf")); err != nil {
		t.Fatalf("config was not written below sites-available: %v", err)
	}
	if _, err := svc.CreateConfig(CreateRequest{Domain: "example.com", Type: "static"}); !errors.Is(err, ErrConfigExists) {
		t.Fatalf("duplicate CreateConfig error = %v", err)
	}

	enabled, err := svc.SetEnabled("example.com.conf", true)
	if err != nil || !enabled {
		t.Fatalf("SetEnabled enable = %v, %v", enabled, err)
	}
	linkTarget, err := os.Readlink(filepath.Join(sitesEnabled, "example.com.conf"))
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if linkTarget != filepath.Join(sitesAvailable, "example.com.conf") {
		t.Fatalf("link target = %q", linkTarget)
	}
	if enabled, err := svc.SetEnabled("example.com.conf", true); err != nil || !enabled {
		t.Fatalf("idempotent enable = %v, %v", enabled, err)
	}

	configs, err := svc.ListConfigs()
	if err != nil || len(configs) != 1 || !configs[0].IsEnabled {
		t.Fatalf("ListConfigs = %+v, %v", configs, err)
	}
	if enabled, err := svc.SetEnabled("example.com.conf", false); err != nil || enabled {
		t.Fatalf("SetEnabled disable = %v, %v", enabled, err)
	}
	if enabled, err := svc.SetEnabled("example.com.conf", false); err != nil || enabled {
		t.Fatalf("idempotent disable = %v, %v", enabled, err)
	}
	snippets, err := svc.ListSnippets()
	if err != nil || len(snippets) != len(requiredManagedSnippetNames)+1 {
		t.Fatalf("ListSnippets = %+v, %v", snippets, err)
	}
	foundSecurity := false
	for _, snippet := range snippets {
		foundSecurity = foundSecurity || snippet.Name == "security.conf"
	}
	if !foundSecurity {
		t.Fatalf("operator snippet missing from ListSnippets: %+v", snippets)
	}
}

func TestService_CreateConfigRemovesCandidateRejectedByNginx(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	snippets := filepath.Join(root, "snippets")
	vhosts := filepath.Join(root, "vhosts")
	for _, directory := range []string{available, enabled, snippets, vhosts} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range requiredManagedSnippetNames {
		if err := os.WriteFile(filepath.Join(snippets, name), []byte("# test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "nginx"), []byte("#!/bin/sh\necho rejected >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	svc := NewWithConfig(ServiceConfig{SitesAvailable: available, SitesEnabled: enabled, SnippetsDir: snippets, VhostsRoot: vhosts})

	_, err := svc.CreateConfig(CreateRequest{Domain: "invalid.example", Type: "static"})
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("CreateConfig error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(available, "invalid.example.conf")); !os.IsNotExist(err) {
		t.Fatalf("rejected candidate remains: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(enabled, ".hserver-validation-*.conf")); len(matches) != 0 {
		t.Fatalf("validation links remain: %v", matches)
	}
}

func TestService_SetEnabledRefusesForeignEntries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(enabled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(available, "site.conf"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewWithConfig(ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	foreign := filepath.Join(enabled, "site.conf")
	if err := os.WriteFile(foreign, []byte("do not remove\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetEnabled("site.conf", false); err == nil || !strings.Contains(err.Error(), "is not a symlink") {
		t.Fatalf("regular foreign entry error = %v", err)
	}
	if content, err := os.ReadFile(foreign); err != nil || string(content) != "do not remove\n" {
		t.Fatalf("foreign entry changed: %q, %v", content, err)
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(available, "other.conf"), foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetEnabled("site.conf", true); err == nil || !strings.Contains(err.Error(), "foreign configuration") {
		t.Fatalf("foreign symlink error = %v", err)
	}
	if _, err := svc.SetEnabled("missing.conf", true); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("missing config error = %v", err)
	}
}

func TestService_ArchiveConfigRequiresDisabledChecksumAndRestoresOnValidationFailure(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(available, "site.conf")
	original := []byte("server { listen 80; }\n")
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(enabled, "site.conf")); err != nil {
		t.Fatal(err)
	}
	validator := filepath.Join(root, "nginx")
	script := "#!/bin/sh\nif [ \"${HSERVER_TEST_ARCHIVE_FAIL:-0}\" = 1 ] && [ ! -e \"$HSERVER_TEST_NGINX_CONFIG\" ]; then echo missing archived config >&2; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(validator, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("HSERVER_TEST_NGINX_CONFIG", target)
	svc := NewWithConfig(ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	observed, err := svc.GetConfig("site.conf")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ArchiveConfig("site.conf", observed.Checksum); !errors.Is(err, ErrConfigEnabled) {
		t.Fatalf("enabled archive error = %v, want ErrConfigEnabled", err)
	}
	if err := os.Remove(filepath.Join(enabled, "site.conf")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ArchiveConfig("site.conf", strings.Repeat("0", 64)); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale archive error = %v, want ErrConfigChanged", err)
	}

	t.Setenv("HSERVER_TEST_ARCHIVE_FAIL", "1")
	if _, err := svc.ArchiveConfig("site.conf", observed.Checksum); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("invalid archive error = %v, want ErrConfigInvalid", err)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != string(original) {
		t.Fatalf("rejected archive did not restore config: %q, %v", content, err)
	}
	if archives, _ := filepath.Glob(target + ".hserver-archive-*"); len(archives) != 0 {
		t.Fatalf("rejected archive recovery copies remain: %v", archives)
	}

	t.Setenv("HSERVER_TEST_ARCHIVE_FAIL", "0")
	receipt, err := svc.ArchiveConfig("site.conf", observed.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Checksum != observed.Checksum || !strings.Contains(receipt.Message, "document root was not changed") {
		t.Fatalf("archive receipt = %+v", receipt)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("archived config remains in inventory: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(available, receipt.Archive)); err != nil || string(content) != string(original) {
		t.Fatalf("archive recovery copy = %q, %v", content, err)
	}
}

func TestService_ListAndRestoreConfigArchivesWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(available, "site.conf")
	validator := filepath.Join(root, "nginx")
	script := "#!/bin/sh\nif [ -e \"$HSERVER_TEST_NGINX_CONFIG\" ] && /bin/grep -q INVALID \"$HSERVER_TEST_NGINX_CONFIG\"; then echo invalid >&2; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(validator, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("HSERVER_TEST_NGINX_CONFIG", target)
	svc := NewWithConfig(ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	archive := "site.conf.hserver-archive-20260827T120000.000000000Z"
	content := []byte("server { listen 80; }\n")
	if err := os.WriteFile(filepath.Join(available, archive), content, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(available, "foreign.conf.hserver-archive-invalid"), []byte("ignored\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	archives, err := svc.ListConfigArchives()
	if err != nil || len(archives) != 1 {
		t.Fatalf("ListConfigArchives = %+v, %v", archives, err)
	}
	observed := archives[0]
	if observed.Archive != archive || observed.Filename != "site.conf" || observed.Size != int64(len(content)) || observed.Archived != "2026-08-27T12:00:00Z" {
		t.Fatalf("archive inventory = %+v", observed)
	}
	if _, err := svc.RestoreConfigArchive(archive, strings.Repeat("0", 64)); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale restore error = %v, want ErrConfigChanged", err)
	}
	if err := os.WriteFile(target, []byte("do not overwrite\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreConfigArchive(archive, observed.Checksum); !errors.Is(err, ErrConfigExists) {
		t.Fatalf("existing target restore error = %v, want ErrConfigExists", err)
	}
	if current, err := os.ReadFile(target); err != nil || string(current) != "do not overwrite\n" {
		t.Fatalf("existing target changed = %q, %v", current, err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	receipt, err := svc.RestoreConfigArchive(archive, observed.Checksum)
	if err != nil {
		t.Fatalf("RestoreConfigArchive: %v", err)
	}
	if receipt.Archive != archive || receipt.Filename != "site.conf" || receipt.Checksum != observed.Checksum || receipt.IsEnabled {
		t.Fatalf("restore receipt = %+v", receipt)
	}
	if restored, err := os.ReadFile(target); err != nil || string(restored) != string(content) {
		t.Fatalf("restored target = %q, %v", restored, err)
	}
	if _, err := os.Stat(filepath.Join(available, archive)); err != nil {
		t.Fatalf("successful restore removed archive: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(enabled, "site.conf")); !os.IsNotExist(err) {
		t.Fatalf("restored config was enabled: %v", err)
	}
}

func TestService_RestoreConfigArchiveRemovesInvalidCandidate(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(available, "invalid.conf")
	if err := os.WriteFile(filepath.Join(root, "nginx"), []byte("#!/bin/sh\n/bin/grep -q INVALID \"$HSERVER_TEST_NGINX_CONFIG\" && exit 1\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("HSERVER_TEST_NGINX_CONFIG", target)
	archive := "invalid.conf.hserver-archive-20260827T120001.000000000Z"
	if err := os.WriteFile(filepath.Join(available, archive), []byte("INVALID\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	svc := NewWithConfig(ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	archives, err := svc.ListConfigArchives()
	if err != nil || len(archives) != 1 {
		t.Fatalf("ListConfigArchives = %+v, %v", archives, err)
	}
	if _, err := svc.RestoreConfigArchive(archive, archives[0].Checksum); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("invalid restore error = %v, want ErrConfigInvalid", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("invalid restored candidate remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(available, archive)); err != nil {
		t.Fatalf("invalid restore removed recovery archive: %v", err)
	}
	if links, _ := filepath.Glob(filepath.Join(enabled, ".hserver-validation-*.conf")); len(links) != 0 {
		t.Fatalf("validation links remain: %v", links)
	}
}

func TestService_ListAndRestoreConfigBackupsWithTwoChecksumLocks(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(available, "site.conf")
	current := []byte("server { listen 8080; }\n")
	previous := []byte("server { listen 80; }\n")
	if err := os.WriteFile(target, current, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(enabled, "site.conf")); err != nil {
		t.Fatal(err)
	}
	backup := "site.conf.hserver-backup-20260827T130000.000000000Z"
	if err := os.WriteFile(filepath.Join(available, backup), previous, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	svc := NewWithConfig(ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	backups, err := svc.ListConfigBackups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("ListConfigBackups = %+v, %v", backups, err)
	}
	observedBackup := backups[0]
	if observedBackup.Backup != backup || observedBackup.Filename != "site.conf" || observedBackup.Size != int64(len(previous)) || observedBackup.Created != "2026-08-27T13:00:00Z" {
		t.Fatalf("backup inventory = %+v", observedBackup)
	}
	observedCurrent, err := svc.GetConfig("site.conf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreConfigBackup(backup, strings.Repeat("0", 64), observedCurrent.Checksum); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale backup error = %v, want ErrConfigChanged", err)
	}
	if _, err := svc.RestoreConfigBackup(backup, observedBackup.Checksum, strings.Repeat("0", 64)); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("stale current error = %v, want ErrConfigChanged", err)
	}
	receipt, err := svc.RestoreConfigBackup(backup, observedBackup.Checksum, observedCurrent.Checksum)
	if err != nil {
		t.Fatalf("RestoreConfigBackup: %v", err)
	}
	if receipt.Backup != backup || receipt.Filename != "site.conf" || receipt.Recovery == "" || !receipt.IsEnabled || receipt.Checksum != observedBackup.Checksum {
		t.Fatalf("restore receipt = %+v", receipt)
	}
	if restored, err := os.ReadFile(target); err != nil || string(restored) != string(previous) {
		t.Fatalf("restored target = %q, %v", restored, err)
	}
	if recovery, err := os.ReadFile(filepath.Join(available, receipt.Recovery)); err != nil || string(recovery) != string(current) {
		t.Fatalf("pre-restore recovery = %q, %v", recovery, err)
	}
	if selected, err := os.ReadFile(filepath.Join(available, backup)); err != nil || string(selected) != string(previous) {
		t.Fatalf("selected backup changed = %q, %v", selected, err)
	}
	linkTarget, err := os.Readlink(filepath.Join(enabled, "site.conf"))
	if err != nil || linkTarget != target {
		t.Fatalf("enabled state changed = %q, %v", linkTarget, err)
	}
}

func TestService_RestoreConfigBackupRollsBackRejectedCandidate(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, directory := range []string{available, enabled} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(available, "site.conf")
	current := []byte("server { listen 8080; }\n")
	if err := os.WriteFile(target, current, 0o640); err != nil {
		t.Fatal(err)
	}
	backup := "site.conf.hserver-backup-20260827T130001.000000000Z"
	if err := os.WriteFile(filepath.Join(available, backup), []byte("INVALID\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nginx"), []byte("#!/bin/sh\n/bin/grep -q INVALID \"$HSERVER_TEST_NGINX_CONFIG\" && exit 1\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("HSERVER_TEST_NGINX_CONFIG", target)
	svc := NewWithConfig(ServiceConfig{SitesAvailable: available, SitesEnabled: enabled})
	backups, err := svc.ListConfigBackups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("ListConfigBackups = %+v, %v", backups, err)
	}
	observedCurrent, err := svc.GetConfig("site.conf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreConfigBackup(backup, backups[0].Checksum, observedCurrent.Checksum); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("invalid backup restore error = %v, want ErrConfigInvalid", err)
	}
	if restored, err := os.ReadFile(target); err != nil || string(restored) != string(current) {
		t.Fatalf("rollback target = %q, %v", restored, err)
	}
	if _, err := os.Stat(filepath.Join(available, backup)); err != nil {
		t.Fatalf("rejected restore removed selected backup: %v", err)
	}
	if links, _ := filepath.Glob(filepath.Join(enabled, ".hserver-validation-*.conf")); len(links) != 0 {
		t.Fatalf("validation links remain: %v", links)
	}
}

func TestService_InvalidInstallationDirectoriesFailClosed(t *testing.T) {
	t.Parallel()

	svc := NewWithConfig(ServiceConfig{
		SitesAvailable: "relative/available",
		SitesEnabled:   "relative/enabled",
		VhostsRoot:     "relative/sites",
	})
	if _, err := svc.CreateConfig(CreateRequest{Domain: "example.com", Type: "static"}); err == nil || !strings.Contains(err.Error(), "absolute paths") {
		t.Fatalf("CreateConfig error = %v", err)
	}
	if _, err := svc.ListConfigs(); err == nil || !strings.Contains(err.Error(), "absolute paths") {
		t.Fatalf("ListConfigs error = %v", err)
	} else if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ListConfigs error = %v, want ErrNotConfigured", err)
	}
}

func TestService_CreateConfigRejectsIncompleteManagedSnippetSet(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sitesAvailable := filepath.Join(root, "available")
	sitesEnabled := filepath.Join(root, "enabled")
	snippetsDir := filepath.Join(root, "snippets")
	for _, dir := range []string{sitesAvailable, sitesEnabled, snippetsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range requiredManagedSnippetNames {
		if name == "hserver-proxy-params.conf" {
			continue
		}
		if err := os.WriteFile(filepath.Join(snippetsDir, name), []byte("# test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewWithConfig(ServiceConfig{
		SitesAvailable: sitesAvailable,
		SitesEnabled:   sitesEnabled,
		VhostsRoot:     filepath.Join(root, "sites"),
		SnippetsDir:    snippetsDir,
	})

	_, err := svc.CreateConfig(CreateRequest{Domain: "example.com", Type: "static"})
	if !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "hserver-proxy-params.conf") {
		t.Fatalf("CreateConfig() error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(sitesAvailable, "example.com.conf")); !os.IsNotExist(statErr) {
		t.Fatalf("config mutated before snippet preflight: %v", statErr)
	}
}

func TestValidateFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "example.com.conf", false},
		{"empty", "", true},
		{"no suffix", "example.com", true},
		{"path traversal dots", "../evil.conf", true},
		{"slash in name", "foo/bar.conf", true},
		{"wildcard subdomain", "*.example.com.conf", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateFilename(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("validateFilename(%q) expected error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateFilename(%q) unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestValidateDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"apex", "example.com", false},
		{"subdomain", "api.example.com", false},
		{"wildcard", "*.example.com", false},
		{"underscore", "my_site.example.com", false},
		{"double dot", "evil..com", true},
		{"slash", "evil/com", true},
		{"space", "evil com", true},
		{"semicolon", "evil;com", true},
		{"backslash", `evil\com`, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateDomain(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("validateDomain(%q) expected error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateDomain(%q) unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestValidateDocRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty allowed", "", false},
		{"absolute path", "/var/www/vhosts/example.com/public_html", false},
		{"relative path", "var/www/html", true},
		{"path traversal", "/var/www/../etc/passwd", true},
		{"shell metachar", "/var/www;rm -rf /", true},
		{"pipe char", "/var/www|cat", true},
		{"backtick", "/var/www/`id`", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateDocRoot(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("validateDocRoot(%q) expected error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateDocRoot(%q) unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestValidateCreateFieldsRejectsCrossTypeAndUnsafeRuntimeInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request CreateRequest
		wantErr bool
	}{
		{name: "static", request: CreateRequest{Type: "static"}},
		{name: "PHP defaults", request: CreateRequest{Type: "php"}},
		{name: "PHP version", request: CreateRequest{Type: "php", PHPVersion: "8.4", PHPPool: "portal_pool"}},
		{name: "proxy", request: CreateRequest{Type: "proxy", ProxyPass: "http://127.0.0.1:3000"}},
		{name: "redirect", request: CreateRequest{Type: "redirect", RedirectTo: "target.example"}},
		{name: "custom TLS pair", request: CreateRequest{Type: "static", UseSSL: true, CertPath: "/etc/certs/site.pem", KeyPath: "/etc/certs/site.key"}},
		{name: "unknown type", request: CreateRequest{Type: "mail"}, wantErr: true},
		{name: "invalid PHP version", request: CreateRequest{Type: "php", PHPVersion: "8.4;load_module"}, wantErr: true},
		{name: "invalid PHP pool", request: CreateRequest{Type: "php", PHPPool: "portal pool"}, wantErr: true},
		{name: "proxy missing upstream", request: CreateRequest{Type: "proxy"}, wantErr: true},
		{name: "redirect missing target", request: CreateRequest{Type: "redirect"}, wantErr: true},
		{name: "cross-type field", request: CreateRequest{Type: "static", ProxyPass: "http://127.0.0.1:3000"}, wantErr: true},
		{name: "TLS path without TLS", request: CreateRequest{Type: "static", CertPath: "/etc/certs/site.pem", KeyPath: "/etc/certs/site.key"}, wantErr: true},
		{name: "partial TLS pair", request: CreateRequest{Type: "static", UseSSL: true, CertPath: "/etc/certs/site.pem"}, wantErr: true},
		{name: "relative TLS path", request: CreateRequest{Type: "static", UseSSL: true, CertPath: "site.pem", KeyPath: "site.key"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateCreateFields(test.request)
			if test.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateProxyPass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty allowed", "", false},
		{"http upstream", "http://127.0.0.1:3000", false},
		{"https upstream", "https://backend.internal:8443", false},
		{"unix socket", "unix:/run/app.sock", false},
		{"invalid scheme", "ftp://127.0.0.1", true},
		{"bare host", "127.0.0.1:3000", true},
		{"shell injection", "http://127.0.0.1;rm -rf /", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateProxyPass(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("validateProxyPass(%q) expected error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateProxyPass(%q) unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestValidateRedirectTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty allowed", "", false},
		{"https url", "https://example.com/new-path", false},
		{"bare domain", "example.com", false},
		{"path only", "/landing", false},
		{"path traversal", "https://evil.com/../admin", true},
		{"shell metachar", "https://x.com;rm -rf", true},
		{"newline", "https://x.com\n/evil", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateRedirectTo(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("validateRedirectTo(%q) expected error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateRedirectTo(%q) unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestDomainFromFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filename string
		want     string
	}{
		{"example.com.conf", "example.com"},
		{"api.example.com.conf", "api.example.com"},
		{"no-extension", "no-extension"},
	}

	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			t.Parallel()
			if got := domainFromFilename(tc.filename); got != tc.want {
				t.Errorf("domainFromFilename(%q) = %q, want %q", tc.filename, got, tc.want)
			}
		})
	}
}

func TestDetectType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "proxy",
			content: `server {
    listen 80;
    proxy_pass http://127.0.0.1:3000;
}`,
			want: "proxy",
		},
		{
			name: "php fastcgi",
			content: `server {
    location ~ \.php$ {
        fastcgi_pass unix:/run/php/php8.4-fpm.sock;
    }
}`,
			want: "php",
		},
		{
			name:    "php-fpm marker",
			content: `include /etc/nginx/snippets/php-fpm.conf;`,
			want:    "php",
		},
		{
			name: "redirect 301",
			content: `server {
    return 301 https://example.com$request_uri;
}`,
			want: "redirect",
		},
		{
			name:    "redirect 302",
			content: `return 302 https://other.example;`,
			want:    "redirect",
		},
		{
			name: "static default",
			content: `server {
    root /var/www/html;
    index index.html;
}`,
			want: "static",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeTempConfig(t, tc.content)
			if got := detectType(path); got != tc.want {
				t.Errorf("detectType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectType_MissingFile(t *testing.T) {
	t.Parallel()
	if got := detectType("/nonexistent/nginx-test-missing.conf"); got != "unknown" {
		t.Errorf("detectType(missing) = %q, want unknown", got)
	}
}

func TestListSnippetsReturnsReadableContent(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "cache.conf")
	secondPath := filepath.Join(dir, "security.conf")
	if err := os.WriteFile(firstPath, []byte("proxy_cache cache;\n"), 0644); err != nil {
		t.Fatalf("write first snippet: %v", err)
	}
	if err := os.WriteFile(secondPath, []byte("add_header X-Test true;\n"), 0644); err != nil {
		t.Fatalf("write second snippet: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("ignored"), 0644); err != nil {
		t.Fatalf("write non-snippet: %v", err)
	}

	snippets, err := listSnippets(dir)
	if err != nil {
		t.Fatalf("listSnippets: %v", err)
	}
	if len(snippets) != 2 {
		t.Fatalf("len(snippets) = %d, want 2", len(snippets))
	}
	if snippets[0].Name != "cache.conf" || snippets[0].Path != firstPath || snippets[0].Content != "proxy_cache cache;\n" {
		t.Errorf("first snippet = %#v", snippets[0])
	}
	if snippets[1].Name != "security.conf" || snippets[1].Path != secondPath || snippets[1].Content != "add_header X-Test true;\n" {
		t.Errorf("second snippet = %#v", snippets[1])
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "site.conf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
