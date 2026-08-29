package releaseupdates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type staticDiscovery struct {
	result Result
}

func (d staticDiscovery) Check(context.Context) Result {
	return d.result
}

func TestManagerStagesVerifiedArchiveAndReusesIt(t *testing.T) {
	version := "v1.1.0"
	platform := "linux_" + runtime.GOARCH
	archive := testReleaseArchive(t, version, platform, false)
	digest := sha256.Sum256(archive)
	downloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads++
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	manager := NewManager(staticDiscovery{result: healthyUpdateResult(version, platform, server.URL, hex.EncodeToString(digest[:]), int64(len(archive)))}, t.TempDir())
	manager.client = server.Client()
	manager.now = func() time.Time { return time.Date(2026, 8, 26, 20, 0, 0, 0, time.UTC) }

	staged, err := manager.Stage(context.Background())
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if staged.Status != StageStaged || staged.Version != version || staged.Platform != platform || staged.SizeBytes != int64(len(archive)) {
		t.Fatalf("Stage() = %#v", staged)
	}
	if !strings.Contains(staged.StatusDetail, "verified") {
		t.Fatalf("stage detail = %q", staged.StatusDetail)
	}
	if staged.ID != version+"-"+hex.EncodeToString(digest[:])[:12] {
		t.Fatalf("stage ID = %q", staged.ID)
	}

	latest, err := manager.Latest(context.Background())
	if err != nil || latest == nil || latest.ID != staged.ID || latest.Status != StageStaged {
		t.Fatalf("Latest() = %#v, %v", latest, err)
	}
	if _, err := verifyStagedFiles(manager.dataDir, staged.ID, platform); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(manager.dataDir, "updates", "latest")); err != nil {
		t.Fatal(err)
	}

	again, err := manager.Stage(context.Background())
	if err != nil || again.ID != staged.ID {
		t.Fatalf("second Stage() = %#v, %v", again, err)
	}
	if downloads != 1 {
		t.Fatalf("downloads = %d, want 1", downloads)
	}
	latest, err = manager.Latest(context.Background())
	if err != nil || latest == nil || latest.ID != staged.ID {
		t.Fatalf("Latest() after marker repair = %#v, %v", latest, err)
	}
}

func TestManagerRejectsInvalidArchivesWithoutPublishingAStage(t *testing.T) {
	version := "v1.1.0"
	platform := "linux_" + runtime.GOARCH
	tests := []struct {
		name       string
		unsafePath bool
		checksum   string
		omit       string
	}{
		{name: "checksum mismatch", checksum: strings.Repeat("0", 64)},
		{name: "unsafe path", unsafePath: true},
		{name: "missing managed snippet", omit: "nginx-snippets/hserver-proxy-params.conf"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := testReleaseArchive(t, version, platform, test.unsafePath, test.omit)
			digest := sha256.Sum256(archive)
			checksum := test.checksum
			if checksum == "" {
				checksum = hex.EncodeToString(digest[:])
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(archive)
			}))
			defer server.Close()
			dataDir := t.TempDir()
			manager := NewManager(staticDiscovery{result: healthyUpdateResult(version, platform, server.URL, checksum, int64(len(archive)))}, dataDir)
			manager.client = server.Client()

			if _, err := manager.Stage(context.Background()); err == nil {
				t.Fatal("Stage() succeeded for an invalid archive")
			}
			if latest, err := manager.Latest(context.Background()); err != nil || latest != nil {
				t.Fatalf("Latest() = %#v, %v", latest, err)
			}
			entries, err := os.ReadDir(filepath.Join(dataDir, "updates"))
			if err != nil {
				t.Fatalf("ReadDir(updates) error = %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("updates contains %d partial entries", len(entries))
			}
		})
	}
}

func TestManagerDistinguishesUnavailableAndCurrentReleases(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		want   error
	}{
		{name: "unavailable", result: Result{Status: StatusUnavailable, Message: "offline"}, want: ErrDiscoveryUnavailable},
		{name: "current", result: Result{Status: StatusHealthy, SignatureStatus: SignatureVerified, Artifact: &Artifact{}, UpdateAvailable: false}, want: ErrNoUpdateAvailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(staticDiscovery{result: test.result}, t.TempDir())
			if _, err := manager.Stage(context.Background()); !errors.Is(err, test.want) {
				t.Fatalf("Stage() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestManagerRequiresVerifiedManifestBeforeStageMutation(t *testing.T) {
	for _, test := range []struct {
		name            string
		signatureStatus string
	}{
		{name: "unsigned", signatureStatus: ""},
		{name: "not configured", signatureStatus: SignatureNotConfigured},
		{name: "invalid", signatureStatus: SignatureUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			downloads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				downloads++
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()

			dataDir := filepath.Join(t.TempDir(), "state")
			result := healthyUpdateResult("v1.1.0", "linux_"+runtime.GOARCH, server.URL, strings.Repeat("0", 64), 1)
			result.SignatureStatus = test.signatureStatus
			manager := NewManager(staticDiscovery{result: result}, dataDir)
			manager.client = server.Client()

			if _, err := manager.Stage(context.Background()); !errors.Is(err, ErrSignedManifestRequired) {
				t.Fatalf("Stage() error = %v, want %v", err, ErrSignedManifestRequired)
			}
			if downloads != 0 {
				t.Fatalf("archive downloads = %d, want 0", downloads)
			}
			if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("state directory mutation: %v", err)
			}
		})
	}
}

func healthyUpdateResult(version, platform, archiveURL, checksum string, size int64) Result {
	return Result{
		Status:          StatusHealthy,
		SignatureStatus: SignatureVerified,
		CurrentVersion:  "v1.0.0",
		LatestVersion:   version,
		UpdateAvailable: true,
		Platform:        platform,
		Artifact:        &Artifact{URL: archiveURL, SHA256: checksum, SizeBytes: size},
	}
}

func testReleaseArchive(t *testing.T, version, platform string, unsafePath bool, omitted ...string) []byte {
	t.Helper()
	binary, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatalf("read test ELF: %v", err)
	}
	root := "hserver-panel-" + version + "-" + strings.Replace(platform, "_", "-", 1)
	files := []struct {
		name string
		body []byte
		mode int64
	}{
		{name: root + "/hserver-panel", body: binary, mode: 0o755},
		{name: root + "/hserverctl", body: binary, mode: 0o755},
		{name: root + "/install.sh", body: []byte("#!/bin/sh\nexit 0\n"), mode: 0o755},
		{name: root + "/doctor.sh", body: []byte("#!/bin/sh\nexit 0\n"), mode: 0o755},
		{name: root + "/VERSION", body: []byte(version + "\n"), mode: 0o644},
	}
	for _, name := range managedReleaseSnippetNames {
		files = append(files, struct {
			name string
			body []byte
			mode int64
		}{name: root + "/nginx-snippets/" + name, body: []byte("# " + name + "\n"), mode: 0o644})
	}
	omit := ""
	if len(omitted) > 0 {
		omit = omitted[0]
	}
	if unsafePath {
		files = append([]struct {
			name string
			body []byte
			mode int64
		}{{name: root + "/../escape", body: []byte("no"), mode: 0o644}}, files...)
	}

	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: root + "/", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatalf("write root directory header: %v", err)
	}
	for _, file := range files {
		if strings.TrimPrefix(file.name, root+"/") == omit {
			continue
		}
		header := &tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.body)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tarWriter.Write(file.body); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

func verifyStagedFiles(dataDir, stageID, platform string) (string, error) {
	stageDir := filepath.Join(dataDir, "updates", stageID)
	for _, name := range []string{"hserver-panel", "hserverctl", "install.sh", "doctor.sh", "VERSION", "upgrade-runner.sh", "record.json", "status", "status-detail"} {
		if _, err := os.Stat(filepath.Join(stageDir, name)); err != nil {
			return "", fmt.Errorf("missing staged file %s: %w", name, err)
		}
	}
	for _, name := range managedReleaseSnippetNames {
		if _, err := os.Stat(filepath.Join(stageDir, "nginx-snippets", name)); err != nil {
			return "", fmt.Errorf("missing staged Nginx snippet %s: %w", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(stageDir, "release.tar.gz")); !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("verified release archive was retained")
	}
	if err := verifyELFArchitecture(filepath.Join(stageDir, "hserver-panel"), platform); err != nil {
		return "", err
	}
	if err := verifyELFArchitecture(filepath.Join(stageDir, "hserverctl"), platform); err != nil {
		return "", err
	}
	return stageDir, nil
}

func TestManagerPrunesOldStagesWithoutRemovingActiveOrUnknownData(t *testing.T) {
	dataDir := t.TempDir()
	manager := NewManager(staticDiscovery{}, dataDir)
	now := time.Date(2026, 8, 26, 21, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	stages := []struct {
		id      string
		status  string
		created time.Time
	}{
		{id: "v1.0.0-000000000001", status: StageCompleted, created: now.Add(-4 * time.Hour)},
		{id: "v1.1.0-000000000002", status: StageRunning, created: now.Add(-3 * time.Hour)},
		{id: "v1.2.0-000000000003", status: StageFailed, created: now.Add(-2 * time.Hour)},
		{id: "v1.3.0-000000000004", status: StageStaged, created: now.Add(-time.Hour)},
	}
	for _, stage := range stages {
		writeTestStageRecord(t, dataDir, stage.id, stage.status, stage.created)
	}
	updatesDir := filepath.Join(dataDir, "updates")
	staleTemp := filepath.Join(updatesDir, ".partial-stage")
	if err := os.Mkdir(staleTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(staleTemp, now.Add(-25*time.Hour), now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(updatesDir, "operator-data")
	if err := os.Mkdir(unknown, 0o700); err != nil {
		t.Fatal(err)
	}

	manager.pruneStagesLocked("v1.3.0-000000000004")

	for _, id := range []string{"v1.1.0-000000000002", "v1.2.0-000000000003", "v1.3.0-000000000004", "operator-data"} {
		if _, err := os.Stat(filepath.Join(updatesDir, id)); err != nil {
			t.Fatalf("retained path %s: %v", id, err)
		}
	}
	for _, id := range []string{"v1.0.0-000000000001", ".partial-stage"} {
		if _, err := os.Stat(filepath.Join(updatesDir, id)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pruned path %s still exists", id)
		}
	}
}

func writeTestStageRecord(t *testing.T, dataDir, id, status string, created time.Time) {
	t.Helper()
	directory := filepath.Join(dataDir, "updates", id)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	record := stageRecord{
		Stage: Stage{
			ID:             id,
			Version:        strings.SplitN(id, "-", 2)[0],
			CurrentVersion: "v0.9.0",
			Platform:       "linux_amd64",
			SHA256:         testSHA256,
			SizeBytes:      1024,
			Status:         status,
			StatusDetail:   stageStatusDetail(status),
			CreatedAt:      created,
			UpdatedAt:      created,
		},
		BinarySHA256:    testSHA256,
		CLISHA256:       testSHA256,
		InstallerSHA256: testSHA256,
		DoctorSHA256:    testSHA256,
		RunnerSHA256:    testSHA256,
		NginxSnippetSHA256: func() map[string]string {
			hashes := make(map[string]string, len(managedReleaseSnippetNames))
			for _, name := range managedReleaseSnippetNames {
				hashes[name] = testSHA256
			}
			return hashes
		}(),
	}
	if err := writeJSONAtomic(filepath.Join(directory, "record.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeStageStatus(directory, status, record.StatusDetail); err != nil {
		t.Fatal(err)
	}
}
