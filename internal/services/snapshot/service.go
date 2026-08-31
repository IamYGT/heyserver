package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"strconv"

	"github.com/IamYGT/heyserver/internal/services/backup"
)

func snapshotTimeout() time.Duration {
	hours := 6
	if v := os.Getenv("HSERVER_SNAPSHOT_TIMEOUT_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hours = n
		}
	}
	return time.Duration(hours) * time.Hour
}

// ErrSettingsUnavailable means the persisted snapshot policy could not be
// observed safely. Snapshot operations must stop instead of silently using
// defaults that may change paths, retention, or repository selection.
var ErrSettingsUnavailable = errors.New("snapshot settings unavailable")

// ErrInvalidSettings means an operator supplied a snapshot policy that cannot
// be represented safely by the fixed installation-owned manifest.
var ErrInvalidSettings = errors.New("invalid snapshot settings")

// ErrInvalidRestoreRequest means a restore does not identify an observed
// restic snapshot or requests paths outside the installation-owned manifest.
var ErrInvalidRestoreRequest = errors.New("invalid snapshot restore request")

// ErrInvalidPurgeRequest means a destructive repository reset was not bound to
// the exact currently observed repository policy and fixed acknowledgement.
var ErrInvalidPurgeRequest = errors.New("invalid snapshot purge request")

// ErrDestinationUnavailable means the selected optional repository provider is
// missing installation configuration or cannot currently be used.
var ErrDestinationUnavailable = errors.New("snapshot destination unavailable")

// ErrNotConfigured identifies a missing or invalid installation-owned path.
var ErrNotConfigured = errors.New("snapshot paths are not configured")

// ErrUnsupportedCapability means the selected provider deliberately does not
// expose a destructive capability through Heyserver.
var ErrUnsupportedCapability = errors.New("snapshot destination capability unsupported")

const PurgeConfirmation = "purge-snapshot-repository"

var (
	repoFolderPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	snapshotIDPattern = regexp.MustCompile(`^[A-Fa-f0-9]{8,64}$`)
	vhostNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
)

// DriveGate ensures Google Drive / rclone is ready before restic operations.
type DriveGate interface {
	EnsureReady(redirectURI string) error
	RefreshSession(redirectURI string) error
	Connected(redirectURI string) bool
	RcloneConfigPath() string
	InternalRedirectURI(port int) string
}

type driveConfigurationGate interface {
	Configured(redirectURI string) bool
}

// JobTracker mirrors backup job progress for snapshot runs.
type JobTracker interface {
	StartJob(jobType, source, message string) string
	AppendJobLog(id, line string)
	SetJobCommand(id, cmd string)
	UpdateJobProgress(id string, phase backup.JobPhase, progress int, message string, bytesDone, bytesTotal int64, speed string)
	CompleteJob(id string, success bool, message, outputFile string)
}

// Service runs incremental server snapshots to a client-side encrypted restic
// repository on an explicitly selected remote destination.
type Service struct {
	dataDir         string
	vhostsRoot      string
	localDir        string
	port            int
	resticBin       string
	rcloneBin       string
	password        string
	drive           DriveGate
	s3              S3Config
	jobs            JobTracker
	mu              sync.Mutex
	running         bool
	statusMu        sync.RWMutex
	repoStatusCache *repoStatusCache

	// readinessRunner is an optional command seam for the context-aware,
	// read-only integration probe. Production leaves it nil so the probe uses
	// the bounded local exec runner; package tests can inject a fake without
	// touching the host's restic/rclone installation.
	readinessRunner snapshotReadinessRunner
	// readinessFileReader is an optional bounded file-observation seam for
	// readiness tests. Production uses the regular-file reader in
	// readiness.go, which checks the caller context before and after each local
	// operation without spawning an uncancellable goroutine.
	readinessFileReader snapshotReadinessFileReader
}

func New(dataDir, vhostsRoot, localDir string, port int, resticBin, rcloneBin, password string, drive DriveGate, jobs JobTracker) *Service {
	return NewWithS3(dataDir, vhostsRoot, localDir, port, resticBin, rcloneBin, password, drive, S3Config{}, jobs)
}

// NewWithS3 preserves the Google Drive constructor while allowing an
// installation to expose an optional S3-compatible repository destination.
func NewWithS3(dataDir, vhostsRoot, localDir string, port int, resticBin, rcloneBin, password string, drive DriveGate, s3 S3Config, jobs JobTracker) *Service {
	if resticBin == "" {
		resticBin = "restic"
	}
	if rcloneBin == "" {
		rcloneBin = "rclone"
	}
	if localDir == "" {
		localDir = filepath.Join(dataDir, "backups")
	}
	return &Service{
		dataDir:    dataDir,
		vhostsRoot: vhostsRoot,
		localDir:   localDir,
		port:       port,
		resticBin:  resticBin,
		rcloneBin:  rcloneBin,
		password:   password,
		drive:      drive,
		s3:         s3.normalized(),
		jobs:       jobs,
	}
}

func (s *Service) settingsFile() string {
	return filepath.Join(s.dataDir, "snapshot-settings.json")
}

func (s *Service) loadSettings() (Settings, error) {
	def := Settings{
		Destination: DestinationGoogleDrive,
		RepoFolder:  defaultRepoFolder,
		KeepDaily:   14,
		KeepWeekly:  8,
		KeepMonthly: 6,
	}
	raw, err := os.ReadFile(s.settingsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return def, nil
		}
		return def, fmt.Errorf("%w: read %s: %v", ErrSettingsUnavailable, s.settingsFile(), err)
	}
	var st Settings
	if err := json.Unmarshal(raw, &st); err != nil {
		return def, fmt.Errorf("%w: parse %s: %v", ErrSettingsUnavailable, s.settingsFile(), err)
	}
	if st.RepoFolder == "" {
		st.RepoFolder = defaultRepoFolder
	}
	if st.Destination == "" {
		st.Destination = DestinationGoogleDrive
	}
	if st.KeepDaily <= 0 {
		st.KeepDaily = def.KeepDaily
	}
	if st.KeepWeekly <= 0 {
		st.KeepWeekly = def.KeepWeekly
	}
	if st.KeepMonthly <= 0 {
		st.KeepMonthly = def.KeepMonthly
	}
	return st, nil
}

func (s *Service) saveSettings(st Settings) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.dataDir, ".snapshot-settings-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, s.settingsFile())
}

func (s *Service) runner(st Settings) (*resticRunner, error) {
	r := &resticRunner{
		bin:        s.resticBin,
		rcloneBin:  s.rcloneBin,
		password:   s.password,
		repoFolder: st.RepoFolder,
		cacheDir:   filepath.Join(s.dataDir, "restic-cache"),
	}
	switch normalizedDestination(st.Destination) {
	case DestinationGoogleDrive:
		if s.drive == nil {
			return nil, fmt.Errorf("%w: Google Drive is not configured", ErrDestinationUnavailable)
		}
		r.rcloneConfig = s.drive.RcloneConfigPath()
		return r, nil
	case DestinationS3:
		credentials, err := s.s3.credentials()
		if err != nil {
			return nil, fmt.Errorf("%w: S3 configuration: %v", ErrDestinationUnavailable, err)
		}
		r.repositoryURL = s.s3.repository(st.RepoFolder)
		r.extraEnv = []string{
			"AWS_ACCESS_KEY_ID=" + credentials.accessKey,
			"AWS_SECRET_ACCESS_KEY=" + credentials.secretKey,
		}
		if s.s3.Region != "" {
			r.extraEnv = append(r.extraEnv, "AWS_DEFAULT_REGION="+s.s3.Region)
		}
		r.globalOptions = []string{"-o", "s3.bucket-lookup=" + s.s3.BucketLookup}
		return r, nil
	default:
		return nil, fmt.Errorf("%w: unsupported destination %q", ErrInvalidSettings, st.Destination)
	}
}

func normalizedDestination(destination Destination) Destination {
	if destination == "" {
		return DestinationGoogleDrive
	}
	return destination
}

func (s *Service) destinationState(st Settings, redirectURI string) (DestinationStatus, string) {
	switch normalizedDestination(st.Destination) {
	case DestinationGoogleDrive:
		if s.drive == nil {
			return DestinationNotConfigured, "Google Drive integration is not configured"
		}
		if configured, ok := s.drive.(driveConfigurationGate); ok && !configured.Configured(redirectURI) {
			return DestinationNotConfigured, "Google Drive OAuth client is not configured"
		}
		if !s.drive.Connected(redirectURI) {
			return DestinationUnavailable, "Google Drive is configured but not connected"
		}
		return DestinationHealthy, "Google Drive is connected"
	case DestinationS3:
		if !s.s3.configured() {
			return DestinationNotConfigured, "S3-compatible destination is not configured"
		}
		if err := s.s3.validate(); err != nil {
			return DestinationUnavailable, "S3-compatible destination is unavailable: " + err.Error()
		}
		return DestinationHealthy, "S3-compatible configuration and credential files are ready"
	default:
		return DestinationUnavailable, fmt.Sprintf("unsupported snapshot destination %q", st.Destination)
	}
}

func (s *Service) ensureDestinationReady(st Settings, redirectURI string) error {
	switch normalizedDestination(st.Destination) {
	case DestinationGoogleDrive:
		if s.drive == nil {
			return fmt.Errorf("%w: Google Drive is not configured", ErrDestinationUnavailable)
		}
		if err := s.drive.EnsureReady(redirectURI); err != nil {
			return fmt.Errorf("%w: Google Drive not ready: %v", ErrDestinationUnavailable, err)
		}
		return nil
	case DestinationS3:
		if !s.s3.configured() {
			return fmt.Errorf("%w: S3-compatible destination is not configured", ErrDestinationUnavailable)
		}
		if err := s.s3.validate(); err != nil {
			return fmt.Errorf("%w: S3 configuration: %v", ErrDestinationUnavailable, err)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported destination %q", ErrInvalidSettings, st.Destination)
	}
}

func (s *Service) operationRedirectURI(st Settings) string {
	if normalizedDestination(st.Destination) == DestinationGoogleDrive && s.drive != nil {
		return s.drive.InternalRedirectURI(s.port)
	}
	return ""
}

func (s *Service) refreshDestinationForRestic(jobID, redirectURI string, st Settings) error {
	if normalizedDestination(st.Destination) == DestinationS3 {
		return s.ensureDestinationReady(st, redirectURI)
	}
	return s.refreshDriveForRestic(jobID, redirectURI)
}

func (s *Service) destinationLabel(st Settings) string {
	if normalizedDestination(st.Destination) == DestinationS3 {
		return "S3-compatible storage"
	}
	return "Google Drive"
}

func (s *Service) log(jobID, line string) {
	if jobID != "" && s.jobs != nil {
		s.jobs.AppendJobLog(jobID, line)
	}
}

// Status returns snapshot subsystem health.
// When skipHeavyIO is true (snapshot job running), restic list/stats calls are skipped.
// Repo metadata is cached (2 min) unless forceRefresh is set.
func (s *Service) Status(redirectURI string, skipHeavyIO, forceRefresh bool) (*Status, error) {
	st, err := s.loadSettings()
	if err != nil {
		return nil, err
	}
	destination := normalizedDestination(st.Destination)
	destinationState, destinationMessage := s.destinationState(st, redirectURI)
	st.Destination = destination
	r, runnerErr := s.runner(st)
	resticProbe := &resticRunner{bin: s.resticBin}
	driveOK := destination == DestinationGoogleDrive && destinationState == DestinationHealthy
	status := &Status{
		ResticFound:        resticProbe.found(),
		PasswordSet:        s.password != "",
		Destination:        destination,
		DestinationStatus:  destinationState,
		DestinationMessage: destinationMessage,
		CanPurgeRepository: destination == DestinationGoogleDrive,
		DriveConnected:     driveOK,
		Settings:           st,
		Manifest:           s.manifestForUI(st),
	}
	configurationReady := destinationState == DestinationHealthy
	if runnerErr != nil && destinationState == DestinationHealthy {
		status.DestinationStatus = DestinationUnavailable
		status.DestinationMessage = runnerErr.Error()
	}
	if destination == DestinationS3 && configurationReady && runnerErr == nil {
		status.DestinationStatus = DestinationUnavailable
		status.DestinationMessage = "S3-compatible configuration loaded; remote repository probe is pending"
	}
	if configurationReady && runnerErr == nil && s.password != "" && status.ResticFound {
		if !forceRefresh {
			if cached, ok := s.getRepoStatusCache(); ok {
				status.RepoInitialized = cached.repoInitialized
				status.LastSnapshots = cached.lastSnapshots
				status.RepoStats = cached.repoStats
				if cached.destinationStatus != "" {
					status.DestinationStatus = cached.destinationStatus
					status.DestinationMessage = cached.destinationMessage
				}
				return status, nil
			}
		}
		if skipHeavyIO {
			return status, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var (
			snaps   []Snapshot
			snapErr error
			stats   *RepoStats
		)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			snaps, snapErr = r.snapshots(ctx, 5)
		}()
		go func() {
			defer wg.Done()
			stats, _ = r.repoStats(ctx)
		}()
		wg.Wait()
		status.RepoInitialized = snapErr == nil
		if destination == DestinationS3 {
			switch {
			case snapErr == nil:
				status.DestinationStatus = DestinationHealthy
				status.DestinationMessage = "S3-compatible remote repository probe succeeded"
			case isRepositoryUninitializedError(snapErr):
				status.DestinationStatus = DestinationHealthy
				status.DestinationMessage = "S3-compatible endpoint accepted the repository request; repository is not initialized"
			default:
				status.DestinationStatus = DestinationUnavailable
				status.DestinationMessage = "S3-compatible remote repository probe failed: " + truncate(snapErr.Error(), 300)
			}
		}
		if status.RepoInitialized {
			status.LastSnapshots = snaps
			if st.LastError != "" {
				st.LastError = ""
				_ = s.saveSettings(st)
				status.Settings = st
			}
			if stats != nil {
				status.RepoStats = stats
			}
		}
		s.setRepoStatusCache(repoStatusCache{
			repoInitialized:    status.RepoInitialized,
			lastSnapshots:      status.LastSnapshots,
			repoStats:          status.RepoStats,
			destinationStatus:  status.DestinationStatus,
			destinationMessage: status.DestinationMessage,
		})
	}
	return status, nil
}

func isRepositoryUninitializedError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"config file does not exist",
		"repository does not exist",
		"specified key does not exist",
		"is there a repository at",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return strings.Contains(message, "config") && strings.Contains(message, "no such file or directory")
}

// WarmRepoCache preloads restic repo metadata in the background (panel startup).
func (s *Service) WarmRepoCache(redirectURI string) {
	go func() {
		_, _ = s.Status(redirectURI, false, false)
	}()
}

// UpdateSettings persists one complete operator-controlled snapshot policy in
// a single atomic replacement while retaining server-owned runtime fields.
func (s *Service) UpdateSettings(in SettingsUpdate) error {
	cur, err := s.loadSettings()
	if err != nil {
		return err
	}
	if err := s.validateSettingsUpdate(in); err != nil {
		return err
	}
	cur.RepoFolder = in.RepoFolder
	cur.Destination = normalizedDestination(in.Destination)
	cur.EnabledPaths = append([]string(nil), in.EnabledPaths...)
	cur.KeepDaily = in.KeepDaily
	cur.KeepWeekly = in.KeepWeekly
	cur.KeepMonthly = in.KeepMonthly
	cur.PasswordAcknowledged = in.PasswordAcknowledged
	if err := s.saveSettings(cur); err != nil {
		return err
	}
	s.invalidateStatusCache()
	return nil
}

func (s *Service) validateSettingsUpdate(in SettingsUpdate) error {
	switch normalizedDestination(in.Destination) {
	case DestinationGoogleDrive, DestinationS3:
	default:
		return fmt.Errorf("%w: destination must be gdrive or s3", ErrInvalidSettings)
	}
	if !repoFolderPattern.MatchString(in.RepoFolder) || path.Clean(in.RepoFolder) != in.RepoFolder {
		return fmt.Errorf("%w: repoFolder must be a clean relative repository path", ErrInvalidSettings)
	}
	if in.KeepDaily < 1 || in.KeepDaily > 365 {
		return fmt.Errorf("%w: keepDaily must be between 1 and 365", ErrInvalidSettings)
	}
	if in.KeepWeekly < 1 || in.KeepWeekly > 260 {
		return fmt.Errorf("%w: keepWeekly must be between 1 and 260", ErrInvalidSettings)
	}
	if in.KeepMonthly < 1 || in.KeepMonthly > 120 {
		return fmt.Errorf("%w: keepMonthly must be between 1 and 120", ErrInvalidSettings)
	}
	if len(in.EnabledPaths) > len(DefaultManifest(s.dataDir, s.vhostsRoot)) {
		return fmt.Errorf("%w: enabledPaths contains too many entries", ErrInvalidSettings)
	}
	allowed := make(map[string]struct{})
	for _, entry := range DefaultManifest(s.dataDir, s.vhostsRoot) {
		allowed[entry.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(in.EnabledPaths))
	for _, id := range in.EnabledPaths {
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("%w: enabledPaths contains unknown id %q", ErrInvalidSettings, id)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%w: enabledPaths contains duplicate id %q", ErrInvalidSettings, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// RunAsync starts an incremental snapshot on the selected destination.
func (s *Service) RunAsync(source string) (string, error) {
	st, err := s.loadSettings()
	if err != nil {
		return "", err
	}
	if s.password == "" {
		return "", fmt.Errorf("HSERVER_RESTIC_PASSWORD not set — run: openssl rand -base64 32")
	}
	if err := checkDiskForSnapshot(); err != nil {
		return "", err
	}
	redirectURI := s.operationRedirectURI(st)
	if err := s.ensureDestinationReady(st, redirectURI); err != nil {
		return "", err
	}
	s.invalidateStatusCache()
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return "", fmt.Errorf("snapshot zaten çalışıyor — bitmesini bekleyin")
	}
	s.running = true
	s.mu.Unlock()
	jobID := ""
	if s.jobs != nil {
		jobID = s.jobs.StartJob("snapshot", source, "Sunucu artımlı snapshot başlıyor…")
	}
	go func() {
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()
		if err := s.runTracked(jobID, source, redirectURI, st); err != nil {
			slog.Warn("snapshot failed", "error", err)
		}
	}()
	return jobID, nil
}

func (s *Service) runTracked(jobID, source, redirectURI string, st Settings) error {
	r, err := s.runner(st)
	if err != nil {
		s.fail(jobID, st, err)
		return err
	}

	if !r.found() {
		err := fmt.Errorf("restic not installed — apt install restic")
		s.fail(jobID, st, err)
		return err
	}
	if err := s.ensureDestinationReady(st, redirectURI); err != nil {
		s.fail(jobID, st, err)
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout())
	defer cancel()
	s.log(jobID, fmt.Sprintf("timeout=%s", snapshotTimeout()))

	s.progress(jobID, backup.PhasePreparing, 3, s.destinationLabel(st)+" hazırlanıyor (restic öncesi)…")
	if err := s.refreshDestinationForRestic(jobID, redirectURI, st); err != nil {
		s.fail(jobID, st, err)
		return err
	}

	s.progress(jobID, backup.PhasePreparing, 5, "Restic repo hazırlanıyor…")
	s.log(jobID, "EXEC: restic init (if needed) repo="+r.repository())
	if err := r.initRepo(ctx); err != nil {
		s.fail(jobID, st, err)
		return err
	}

	s.progress(jobID, backup.PhaseDatabase, 15, "Veritabanları staging'e alınıyor…")
	staging, err := s.prepareStaging(ctx, jobID)
	if err != nil {
		s.fail(jobID, st, err)
		return err
	}

	manifest := s.resolvedManifest(st)
	paths := manifestBackupPaths(manifest, staging)
	cmd := fmt.Sprintf("restic backup %d paths (incremental) tags=daily,auto", len(paths))
	if s.jobs != nil {
		s.jobs.SetJobCommand(jobID, cmd)
	}
	s.log(jobID, "EXEC: "+cmd)
	s.progress(jobID, backup.PhaseFiles, 33, s.destinationLabel(st)+" hazırlanıyor, repo kilitleri temizleniyor…")
	if err := s.refreshDestinationForRestic(jobID, redirectURI, st); err != nil {
		s.fail(jobID, st, err)
		return err
	}
	if err := r.unlockStale(ctx); err != nil {
		s.log(jobID, "WARN unlock: "+truncate(err.Error(), 200))
	} else {
		s.log(jobID, "EXEC: restic unlock --remove-all (stale locks)")
	}
	s.progress(jobID, backup.PhaseFiles, 35, "Artımlı snapshot yükleniyor (restic → "+s.destinationLabel(st)+")…")

	tags := []string{"daily", "auto"}
	if source == "scheduled" {
		tags = append(tags, "scheduled")
	}
	excludes := collectExcludes(manifest)
	if len(excludes) > 0 {
		s.log(jobID, fmt.Sprintf("EXCLUDE %d patterns", len(excludes)))
	}
	var lastBD int64
	var lastAt time.Time
	if err := r.backup(ctx, paths, excludes, tags, func(line string) {
		if pct, bd, bt, ok := parseResticStatusLine(line); ok && s.jobs != nil {
			pctHuman := int(float64(pct-35) / 55 * 100)
			if pctHuman < 0 {
				pctHuman = 0
			}
			if pctHuman > 100 {
				pctHuman = 100
			}
			speed := resticUploadSpeed(lastBD, lastAt, bd)
			lastBD = bd
			lastAt = time.Now()
			msg := fmt.Sprintf("Artımlı snapshot yükleniyor… %d%% (%s / %s)",
				pctHuman, formatBytesShort(bd), formatBytesShort(bt))
			if speed != "" {
				msg += " · " + speed
			}
			s.jobs.UpdateJobProgress(jobID, backup.PhaseFiles, pct, msg, bd, bt, speed)
			return
		}
		if resticLogWorthy(line) {
			s.log(jobID, "restic: "+truncate(line, 300))
		}
	}); err != nil {
		s.fail(jobID, st, err)
		return err
	}

	s.progress(jobID, backup.PhaseRetention, 88, "Retention uygulanıyor (eski snapshot'lar temizleniyor)…")
	s.log(jobID, fmt.Sprintf("EXEC: restic forget --keep-daily %d --keep-weekly %d --keep-monthly %d --prune",
		st.KeepDaily, st.KeepWeekly, st.KeepMonthly))
	if err := r.forget(ctx, st.KeepDaily, st.KeepWeekly, st.KeepMonthly); err != nil {
		s.log(jobID, "WARN forget: "+err.Error())
	}

	snaps, _ := r.snapshots(ctx, 1)
	st.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	st.LastError = ""
	if len(snaps) > 0 {
		st.LastSnapshot = snaps[0].ID
	}
	_ = s.saveSettings(st)

	if s.jobs != nil {
		msg := "Artımlı snapshot tamamlandı"
		if len(snaps) > 0 {
			msg += " — id=" + snaps[0].ID
		}
		s.jobs.CompleteJob(jobID, true, msg, st.LastSnapshot)
	}
	s.invalidateStatusCache()
	return nil
}

func formatBytesShort(n int64) string {
	if n <= 0 {
		return "—"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (s *Service) fail(jobID string, st Settings, err error) {
	st.LastError = truncate(err.Error(), 500)
	_ = s.saveSettings(st)
	if s.jobs != nil {
		s.jobs.CompleteJob(jobID, false, truncate(err.Error(), 600), "")
	}
	s.invalidateStatusCache()
}

func (s *Service) progress(jobID string, phase backup.JobPhase, pct int, msg string) {
	if s.jobs != nil {
		s.jobs.UpdateJobProgress(jobID, phase, pct, msg, 0, 0, "")
	}
}

// ListSnapshots returns recent restic snapshots from the selected destination.
func (s *Service) ListSnapshots(redirectURI string, limit int) ([]Snapshot, error) {
	st, err := s.loadSettings()
	if err != nil {
		return nil, err
	}
	if err := s.ensureDestinationReady(st, redirectURI); err != nil {
		return nil, err
	}
	r, err := s.runner(st)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return r.snapshots(ctx, limit)
}

// ListVhosts returns domain folders available for selective restore.
func (s *Service) ListVhosts() ([]string, error) {
	root, err := s.requireVhostsRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "system" && e.Name() != "default" {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func (s *Service) requireVhostsRoot() (string, error) {
	root := strings.TrimSpace(s.vhostsRoot)
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: vhosts root must be configured as an absolute path", ErrNotConfigured)
	}
	return filepath.Clean(root), nil
}

// RestoreAsync restores paths from a snapshot (domain or full server).
func (s *Service) RestoreAsync(req RestoreRequest, redirectURI string) (string, error) {
	includes, err := s.restoreIncludes(req)
	if err != nil {
		return "", err
	}
	st, err := s.loadSettings()
	if err != nil {
		return "", err
	}
	if err := s.ensureDestinationReady(st, redirectURI); err != nil {
		return "", err
	}
	r, err := s.runner(st)
	if err != nil {
		return "", err
	}
	jobID := ""
	if s.jobs != nil {
		jobID = s.jobs.StartJob("snapshot_restore", "manual", "Snapshot geri yükleme: "+req.SnapshotID)
	}
	go func() {
		target := filepath.Join(s.localDir, "restore-"+req.SnapshotID)
		_ = os.MkdirAll(target, 0o750)
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout())
		defer cancel()
		s.log(jobID, fmt.Sprintf("RESTORE snapshot=%s target=%s includes=%v timeout=%s", req.SnapshotID, target, includes, snapshotTimeout()))
		if s.jobs != nil {
			s.jobs.SetJobCommand(jobID, fmt.Sprintf("restic restore %s --target %s", req.SnapshotID, target))
		}
		err := r.restore(ctx, req.SnapshotID, target, includes)
		if err != nil {
			if s.jobs != nil {
				s.jobs.CompleteJob(jobID, false, err.Error(), "")
			}
			return
		}
		if s.jobs != nil {
			s.jobs.CompleteJob(jobID, true, "Geri yükleme tamamlandı: "+target, target)
		}
	}()
	return jobID, nil
}

func (s *Service) restoreIncludes(req RestoreRequest) ([]string, error) {
	if !snapshotIDPattern.MatchString(req.SnapshotID) {
		return nil, fmt.Errorf("%w: snapshotId must be an 8-64 character hexadecimal snapshot identity", ErrInvalidRestoreRequest)
	}
	if len(req.Vhosts) > 0 || containsString(req.ManifestIDs, "vhosts") {
		if _, err := s.requireVhostsRoot(); err != nil {
			return nil, err
		}
	}
	manifest := DefaultManifest(s.dataDir, s.vhostsRoot)
	if len(req.ManifestIDs) > len(manifest) {
		return nil, fmt.Errorf("%w: manifestIds contains too many entries", ErrInvalidRestoreRequest)
	}
	if len(req.Vhosts) > 16 {
		return nil, fmt.Errorf("%w: vhosts accepts at most 16 names", ErrInvalidRestoreRequest)
	}
	manifestPaths := make(map[string]string, len(manifest))
	for _, entry := range manifest {
		if entry.Path != "" {
			manifestPaths[entry.ID] = filepath.Clean(entry.Path)
		}
	}
	seenManifest := make(map[string]struct{}, len(req.ManifestIDs))
	includes := make([]string, 0, len(req.ManifestIDs)+len(req.Vhosts))
	for _, id := range req.ManifestIDs {
		include, ok := manifestPaths[id]
		if !ok {
			return nil, fmt.Errorf("%w: manifestIds contains unknown or non-selectable id %q", ErrInvalidRestoreRequest, id)
		}
		if _, ok := seenManifest[id]; ok {
			return nil, fmt.Errorf("%w: manifestIds contains duplicate id %q", ErrInvalidRestoreRequest, id)
		}
		seenManifest[id] = struct{}{}
		includes = append(includes, include)
	}
	if _, allVhosts := seenManifest["vhosts"]; allVhosts && len(req.Vhosts) > 0 {
		return nil, fmt.Errorf("%w: choose either the vhosts manifest or specific vhost names", ErrInvalidRestoreRequest)
	}
	seenVhosts := make(map[string]struct{}, len(req.Vhosts))
	root := s.vhostsRoot
	if len(req.Vhosts) > 0 {
		root, _ = s.requireVhostsRoot()
	}
	for _, name := range req.Vhosts {
		if !vhostNamePattern.MatchString(name) || filepath.Base(name) != name || name == "." || name == ".." {
			return nil, fmt.Errorf("%w: vhosts contains invalid name %q", ErrInvalidRestoreRequest, name)
		}
		if _, ok := seenVhosts[name]; ok {
			return nil, fmt.Errorf("%w: vhosts contains duplicate name %q", ErrInvalidRestoreRequest, name)
		}
		seenVhosts[name] = struct{}{}
		includes = append(includes, filepath.Join(root, name))
	}
	return includes, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// refreshDriveForRestic forces OAuth token refresh and rewrites rclone.conf before restic/rclone I/O.
func (s *Service) refreshDriveForRestic(jobID, redirectURI string) error {
	if s.drive == nil {
		return fmt.Errorf("%w: Google Drive is not configured", ErrDestinationUnavailable)
	}
	if err := s.drive.RefreshSession(redirectURI); err != nil {
		s.log(jobID, "WARN OAuth refresh: "+truncate(err.Error(), 200))
		if err2 := s.drive.EnsureReady(redirectURI); err2 != nil {
			return err2
		}
		return nil
	}
	s.log(jobID, "OAuth session refreshed for restic")
	return nil
}
