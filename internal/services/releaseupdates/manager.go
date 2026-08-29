package releaseupdates

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	StageStaged    = "staged"
	StageScheduled = "scheduled"
	StageRunning   = "running"
	StageCompleted = "completed"
	StageFailed    = "failed"

	maxReleaseArchiveBytes  = int64(1 << 30)
	maxArchiveEntryBytes    = int64(512 << 20)
	maxArchiveExpandedBytes = int64(2 << 30)
	maxRetainedUpdateStages = 2
	staleStageTempAge       = 24 * time.Hour
)

var (
	ErrNoUpdateAvailable      = errors.New("no newer stable release is available")
	ErrDiscoveryUnavailable   = errors.New("release discovery is unavailable")
	ErrSignedManifestRequired = errors.New("a verified signed release manifest is required")
	ErrInvalidStage           = errors.New("invalid update stage")
)

type Discovery interface {
	Check(context.Context) Result
}

type Stage struct {
	ID             string    `json:"id"`
	Version        string    `json:"version"`
	CurrentVersion string    `json:"current_version"`
	Platform       string    `json:"platform"`
	SHA256         string    `json:"sha256"`
	SizeBytes      int64     `json:"size_bytes"`
	Status         string    `json:"status"`
	StatusDetail   string    `json:"status_detail,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type stageRecord struct {
	Stage
	BinarySHA256       string            `json:"binary_sha256"`
	CLISHA256          string            `json:"cli_sha256"`
	InstallerSHA256    string            `json:"installer_sha256"`
	DoctorSHA256       string            `json:"doctor_sha256"`
	RunnerSHA256       string            `json:"runner_sha256"`
	NginxSnippetSHA256 map[string]string `json:"nginx_snippet_sha256"`
}

var managedReleaseSnippetNames = []string{
	"hserver-acme-challenge.conf",
	"hserver-ssl-params.conf",
	"hserver-security-headers.conf",
	"hserver-security-deny.conf",
	"hserver-compression.conf",
	"hserver-static-cache.conf",
	"hserver-php-fpm.conf",
	"hserver-proxy-params.conf",
}

type Manager struct {
	discovery          Discovery
	dataDir            string
	client             *http.Client
	runner             commandRunner
	now                func() time.Time
	panelPath          string
	cliPath            string
	executablePath     func() (string, error)
	pathValidationRoot string
	mu                 sync.Mutex
}

func NewManager(discovery Discovery, dataDir string, installedBinaryPaths ...string) *Manager {
	panelPath := ""
	cliPath := ""
	if len(installedBinaryPaths) > 0 {
		panelPath = strings.TrimSpace(installedBinaryPaths[0])
	}
	if len(installedBinaryPaths) > 1 {
		cliPath = strings.TrimSpace(installedBinaryPaths[1])
	}
	return &Manager{
		discovery:      discovery,
		dataDir:        dataDir,
		client:         &http.Client{Timeout: 15 * time.Minute},
		runner:         execRunner{},
		now:            time.Now,
		panelPath:      panelPath,
		cliPath:        cliPath,
		executablePath: os.Executable,
	}
}

func (m *Manager) Stage(ctx context.Context) (Stage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	discovery := m.discovery.Check(ctx)
	if discovery.Status != StatusHealthy || discovery.Artifact == nil {
		return Stage{}, fmt.Errorf("%w: %s", ErrDiscoveryUnavailable, discovery.Message)
	}
	if discovery.SignatureStatus != SignatureVerified {
		return Stage{}, ErrSignedManifestRequired
	}
	if !discovery.UpdateAvailable {
		return Stage{}, ErrNoUpdateAvailable
	}
	artifact := *discovery.Artifact
	if !validSHA256(artifact.SHA256) {
		return Stage{}, ErrInvalidStage
	}
	stageID := discovery.LatestVersion + "-" + artifact.SHA256[:12]
	if !validStageID(stageID) {
		return Stage{}, ErrInvalidStage
	}
	updatesDir := filepath.Join(m.dataDir, "updates")
	if existing, err := m.getLocked(stageID); err == nil {
		if err := writeFileAtomic(filepath.Join(updatesDir, "latest"), []byte(stageID+"\n"), 0o600); err != nil {
			return Stage{}, fmt.Errorf("write latest update stage: %w", err)
		}
		m.pruneStagesLocked(stageID)
		return existing.Stage, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Stage{}, err
	}

	stageDir := filepath.Join(updatesDir, stageID)
	if err := os.MkdirAll(updatesDir, 0o700); err != nil {
		return Stage{}, fmt.Errorf("create update directory: %w", err)
	}
	workingDir, err := os.MkdirTemp(updatesDir, "."+stageID+".")
	if err != nil {
		return Stage{}, fmt.Errorf("create update stage: %w", err)
	}
	defer os.RemoveAll(workingDir)
	if err := os.Chmod(workingDir, 0o700); err != nil {
		return Stage{}, fmt.Errorf("protect update stage: %w", err)
	}

	archivePath := filepath.Join(workingDir, "release.tar.gz")
	size, err := m.download(ctx, artifact, archivePath)
	if err != nil {
		return Stage{}, err
	}
	hashes, err := extractReleaseArchive(archivePath, workingDir, discovery.LatestVersion, discovery.Platform)
	if err != nil {
		return Stage{}, fmt.Errorf("validate release archive: %w", err)
	}
	if err := os.Remove(archivePath); err != nil {
		return Stage{}, fmt.Errorf("remove verified release archive: %w", err)
	}
	runnerPath := filepath.Join(workingDir, "upgrade-runner.sh")
	if err := writeFileAtomic(runnerPath, []byte(upgradeRunnerScript), 0o700); err != nil {
		return Stage{}, fmt.Errorf("write update runner: %w", err)
	}
	runnerSHA256, err := fileSHA256(runnerPath)
	if err != nil {
		return Stage{}, fmt.Errorf("hash update runner: %w", err)
	}
	now := m.now().UTC()
	record := stageRecord{
		Stage: Stage{
			ID:             stageID,
			Version:        discovery.LatestVersion,
			CurrentVersion: discovery.CurrentVersion,
			Platform:       discovery.Platform,
			SHA256:         artifact.SHA256,
			SizeBytes:      size,
			Status:         StageStaged,
			StatusDetail:   stageStatusDetail(StageStaged),
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		BinarySHA256:       hashes.binary,
		CLISHA256:          hashes.cli,
		InstallerSHA256:    hashes.installer,
		DoctorSHA256:       hashes.doctor,
		RunnerSHA256:       runnerSHA256,
		NginxSnippetSHA256: hashes.nginxSnippets,
	}
	if err := writeJSONAtomic(filepath.Join(workingDir, "record.json"), record, 0o600); err != nil {
		return Stage{}, fmt.Errorf("write update stage record: %w", err)
	}
	if err := writeStageStatus(workingDir, StageStaged, record.StatusDetail); err != nil {
		return Stage{}, err
	}
	if err := os.Rename(workingDir, stageDir); err != nil {
		return Stage{}, fmt.Errorf("commit update stage: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(updatesDir, "latest"), []byte(stageID+"\n"), 0o600); err != nil {
		return Stage{}, fmt.Errorf("write latest update stage: %w", err)
	}
	m.pruneStagesLocked(stageID)
	return record.Stage, nil
}

func (m *Manager) pruneStagesLocked(currentID string) {
	updatesDir := filepath.Join(m.dataDir, "updates")
	entries, err := os.ReadDir(updatesDir)
	if err != nil {
		slog.Warn("release stage retention scan failed", "error", err)
		return
	}
	type retainedStage struct {
		id        string
		createdAt time.Time
	}
	candidates := make([]retainedStage, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(updatesDir, name)
		if name == "latest" {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if strings.HasPrefix(name, ".") {
			if m.now().Sub(info.ModTime()) > staleStageTempAge {
				if err := os.RemoveAll(path); err != nil {
					slog.Warn("stale release stage cleanup failed", "stage", name, "error", err)
				}
			}
			continue
		}
		if name == currentID || !validStageID(name) {
			continue
		}
		record, err := m.getLocked(name)
		if err != nil {
			continue
		}
		if record.Status == StageScheduled || record.Status == StageRunning {
			continue
		}
		candidates = append(candidates, retainedStage{id: name, createdAt: record.CreatedAt})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].createdAt.After(candidates[j].createdAt)
	})
	keepPrevious := maxRetainedUpdateStages - 1
	for index, candidate := range candidates {
		if index < keepPrevious {
			continue
		}
		if err := os.RemoveAll(filepath.Join(updatesDir, candidate.id)); err != nil {
			slog.Warn("old release stage cleanup failed", "stage", candidate.id, "error", err)
		}
	}
}

func (m *Manager) Latest(ctx context.Context) (*Stage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	marker, err := os.ReadFile(filepath.Join(m.dataDir, "updates", "latest"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read latest update stage: %w", err)
	}
	id := strings.TrimSpace(string(marker))
	if !validStageID(id) {
		return nil, ErrInvalidStage
	}
	record, err := m.getLocked(id)
	if err != nil {
		return nil, err
	}
	record = m.reconcileInterruptedLocked(ctx, record)
	return &record.Stage, nil
}

func (m *Manager) Get(ctx context.Context, id string) (Stage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.getLocked(id)
	if err == nil {
		record = m.reconcileInterruptedLocked(ctx, record)
	}
	return record.Stage, err
}

func (m *Manager) getLocked(id string) (stageRecord, error) {
	if !validStageID(id) {
		return stageRecord{}, ErrInvalidStage
	}
	stageDir := filepath.Join(m.dataDir, "updates", id)
	payload, err := os.ReadFile(filepath.Join(stageDir, "record.json"))
	if err != nil {
		return stageRecord{}, err
	}
	var record stageRecord
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil || record.ID != id {
		return stageRecord{}, ErrInvalidStage
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return stageRecord{}, ErrInvalidStage
	}
	if !validStageRecord(record) {
		return stageRecord{}, ErrInvalidStage
	}
	status, err := readStatus(stageDir)
	if err != nil {
		return stageRecord{}, err
	}
	record.Status = status
	record.StatusDetail = readStatusDetail(stageDir, status)
	if info, err := os.Stat(filepath.Join(stageDir, "status")); err == nil {
		record.UpdatedAt = info.ModTime().UTC()
	}
	return record, nil
}

func validStageRecord(record stageRecord) bool {
	if !validStageID(record.ID) || record.Version == "" || record.CurrentVersion == "" || record.SizeBytes <= 0 || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		return false
	}
	if record.Platform != "linux_amd64" && record.Platform != "linux_arm64" {
		return false
	}
	if !validSHA256(record.SHA256) || !validSHA256(record.BinarySHA256) || !validSHA256(record.CLISHA256) || !validSHA256(record.InstallerSHA256) || !validSHA256(record.DoctorSHA256) || !validSHA256(record.RunnerSHA256) {
		return false
	}
	if len(record.NginxSnippetSHA256) != len(managedReleaseSnippetNames) {
		return false
	}
	for _, name := range managedReleaseSnippetNames {
		if !validSHA256(record.NginxSnippetSHA256[name]) {
			return false
		}
	}
	return true
}

func (m *Manager) download(ctx context.Context, artifact Artifact, destination string) (int64, error) {
	limit := maxReleaseArchiveBytes
	if artifact.SizeBytes > 0 {
		if artifact.SizeBytes > maxReleaseArchiveBytes {
			return 0, fmt.Errorf("release archive exceeds the 1 GiB limit")
		}
		limit = artifact.SizeBytes
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return 0, fmt.Errorf("create release download request: %w", err)
	}
	request.Header.Set("Accept", "application/gzip, application/octet-stream")
	response, err := m.client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("download release archive")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("release archive returned HTTP %d", response.StatusCode)
	}
	if response.Request == nil || response.Request.URL == nil || !validHTTPURL(response.Request.URL.String()) {
		return 0, fmt.Errorf("release archive redirected to an invalid URL")
	}

	temporary := destination + ".part"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create staged release archive: %w", err)
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, digest), io.LimitReader(response.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > limit || (artifact.SizeBytes > 0 && written != artifact.SizeBytes) {
		_ = os.Remove(temporary)
		return 0, fmt.Errorf("release archive size or download is invalid")
	}
	if hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		_ = os.Remove(temporary)
		return 0, fmt.Errorf("release archive SHA-256 mismatch")
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return 0, fmt.Errorf("commit staged release archive: %w", err)
	}
	return written, nil
}

type extractedHashes struct {
	binary        string
	cli           string
	installer     string
	doctor        string
	nginxSnippets map[string]string
}

func extractReleaseArchive(archivePath, stageDir, version, platform string) (extractedHashes, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return extractedHashes{}, err
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return extractedHashes{}, err
	}
	defer gzipReader.Close()

	root := "hserver-panel-" + version + "-" + strings.Replace(platform, "_", "-", 1)
	required := map[string]struct {
		output string
		mode   os.FileMode
		hash   hash.Hash
	}{
		"hserver-panel": {output: filepath.Join(stageDir, "hserver-panel"), mode: 0o700, hash: sha256.New()},
		"hserverctl":    {output: filepath.Join(stageDir, "hserverctl"), mode: 0o700, hash: sha256.New()},
		"install.sh":    {output: filepath.Join(stageDir, "install.sh"), mode: 0o700, hash: sha256.New()},
		"doctor.sh":     {output: filepath.Join(stageDir, "doctor.sh"), mode: 0o700, hash: sha256.New()},
		"VERSION":       {output: filepath.Join(stageDir, "VERSION"), mode: 0o600, hash: sha256.New()},
	}
	if err := os.Mkdir(filepath.Join(stageDir, "nginx-snippets"), 0o700); err != nil {
		return extractedHashes{}, err
	}
	for _, name := range managedReleaseSnippetNames {
		relative := "nginx-snippets/" + name
		required[relative] = struct {
			output string
			mode   os.FileMode
			hash   hash.Hash
		}{output: filepath.Join(stageDir, relative), mode: 0o600, hash: sha256.New()}
	}
	found := make(map[string]bool, len(required))
	reader := tar.NewReader(gzipReader)
	var expanded int64
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return extractedHashes{}, err
		}
		entries++
		if entries > 1024 || header.Size < 0 || header.Size > maxArchiveEntryBytes {
			return extractedHashes{}, fmt.Errorf("archive entry limit exceeded")
		}
		expanded += header.Size
		if expanded > maxArchiveExpandedBytes {
			return extractedHashes{}, fmt.Errorf("expanded archive limit exceeded")
		}
		entryName := header.Name
		if header.Typeflag == tar.TypeDir {
			entryName = strings.TrimSuffix(entryName, "/")
		}
		clean := path.Clean(entryName)
		if clean != entryName || (clean != root && !strings.HasPrefix(clean, root+"/")) {
			return extractedHashes{}, fmt.Errorf("unsafe archive path")
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return extractedHashes{}, fmt.Errorf("unsupported archive entry type")
		}
		relative := strings.TrimPrefix(clean, root+"/")
		target, selected := required[relative]
		if !selected {
			continue
		}
		if found[relative] {
			return extractedHashes{}, fmt.Errorf("duplicate required archive entry")
		}
		output, err := os.OpenFile(target.output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, target.mode)
		if err != nil {
			return extractedHashes{}, err
		}
		_, copyErr := io.Copy(io.MultiWriter(output, target.hash), reader)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			return extractedHashes{}, fmt.Errorf("extract %s", relative)
		}
		found[relative] = true
	}
	for name := range required {
		if !found[name] {
			return extractedHashes{}, fmt.Errorf("release archive is missing %s", name)
		}
	}
	versionBytes, err := os.ReadFile(filepath.Join(stageDir, "VERSION"))
	if err != nil || strings.TrimSpace(string(versionBytes)) != version {
		return extractedHashes{}, fmt.Errorf("release VERSION does not match manifest")
	}
	if err := verifyELFArchitecture(filepath.Join(stageDir, "hserver-panel"), platform); err != nil {
		return extractedHashes{}, err
	}
	if err := verifyELFArchitecture(filepath.Join(stageDir, "hserverctl"), platform); err != nil {
		return extractedHashes{}, err
	}
	snippetHashes := make(map[string]string, len(managedReleaseSnippetNames))
	for _, name := range managedReleaseSnippetNames {
		snippetHashes[name] = hex.EncodeToString(required["nginx-snippets/"+name].hash.Sum(nil))
	}
	return extractedHashes{
		binary:        hex.EncodeToString(required["hserver-panel"].hash.Sum(nil)),
		cli:           hex.EncodeToString(required["hserverctl"].hash.Sum(nil)),
		installer:     hex.EncodeToString(required["install.sh"].hash.Sum(nil)),
		doctor:        hex.EncodeToString(required["doctor.sh"].hash.Sum(nil)),
		nginxSnippets: snippetHashes,
	}, nil
}

func verifyELFArchitecture(binaryPath, platform string) error {
	binary, err := elf.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("release binary is not ELF: %w", err)
	}
	defer binary.Close()
	want := elf.EM_NONE
	switch platform {
	case "linux_amd64":
		want = elf.EM_X86_64
	case "linux_arm64":
		want = elf.EM_AARCH64
	default:
		return fmt.Errorf("unsupported release platform")
	}
	if binary.Machine != want {
		return fmt.Errorf("release binary architecture does not match %s", platform)
	}
	return nil
}

func validStageID(id string) bool {
	if len(id) < 3 || len(id) > 96 {
		return false
	}
	for _, char := range id {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func readStatus(stageDir string) (string, error) {
	payload, err := os.ReadFile(filepath.Join(stageDir, "status"))
	if err != nil {
		return "", err
	}
	status := strings.TrimSpace(string(payload))
	switch status {
	case StageStaged, StageScheduled, StageRunning, StageCompleted, StageFailed:
		return status, nil
	default:
		return "", ErrInvalidStage
	}
}

func readStatusDetail(stageDir, status string) string {
	payload, err := os.ReadFile(filepath.Join(stageDir, "status-detail"))
	if err != nil || strings.TrimSpace(string(payload)) == "" {
		return stageStatusDetail(status)
	}
	return strings.TrimSpace(string(payload))
}

func writeStageStatus(stageDir, status, detail string) error {
	if strings.TrimSpace(detail) == "" {
		detail = stageStatusDetail(status)
	}
	if err := writeFileAtomic(filepath.Join(stageDir, "status-detail"), []byte(detail+"\n"), 0o600); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(stageDir, "status"), []byte(status+"\n"), 0o600)
}

func stageStatusDetail(status string) string {
	switch status {
	case StageStaged:
		return "Release archive verified and ready for admin confirmation."
	case StageScheduled:
		return "Detached panel upgrade scheduled."
	case StageRunning:
		return "Packaged panel installer is running."
	case StageCompleted:
		return "New panel release passed its health check."
	case StageFailed:
		return "Panel upgrade failed; inspect the upgrade and panel service journals."
	default:
		return "Unknown panel upgrade state."
	}
}

func writeJSONAtomic(destination string, value any, mode os.FileMode) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(destination, append(payload, '\n'), mode)
}

func writeFileAtomic(destination string, payload []byte, mode os.FileMode) error {
	directory := filepath.Dir(destination)
	file, err := os.CreateTemp(directory, "."+filepath.Base(destination)+".")
	if err != nil {
		return err
	}
	temporary := file.Name()
	if err := file.Chmod(mode); err != nil {
		file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if _, err := file.Write(payload); err != nil {
		file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, destination)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
