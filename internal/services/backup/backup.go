package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/shell"
)

// cronExprRe validates a standard 5-field cron expression.
// Only digits, *, /, -, and , are allowed — no shell metacharacters.
var cronExprRe = regexp.MustCompile(`^(\*|[0-9]+(?:[,\-/][0-9]+)*)(\s+(\*|[0-9]+(?:[,\-/][0-9]+)*)){4}$`)

var portableVhostNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)

const (
	defaultBackupDir = "/var/lib/hserver/backups"
	dbTimeout        = 5 * time.Minute
	filesTimeout     = 10 * time.Minute
	cronMarker       = "# hserver-backup"
	backupReserve    = uint64(2 * 1024 * 1024 * 1024)
)

// JobStatus represents the state of an async backup operation.
type JobStatus string

const (
	JobPending JobStatus = "pending"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
)

// AfterBackupHook is called after a successful local backup with the output file path.
type AfterBackupHook func(localPath string)

// Service manages server backups.
type Service struct {
	backupDir    string
	legacyDirs   []string
	vhostsRoot   string
	readCronTab  crontabReader
	writeCronTab crontabWriter
	mu           sync.RWMutex
	restoreMu    sync.Mutex
	jobs         map[string]*Job
	jobSubs      map[chan Job]struct{}
	onSuccess    AfterBackupHook
	onNotify     func(success bool, backupType, fileName, errMsg string)
}

func New() *Service {
	dir := os.Getenv("BACKUP_DIR")
	if dir == "" {
		dir = defaultBackupDir
	}
	vhostsRoot := os.Getenv("HSERVER_VHOSTS_ROOT")
	return NewConfigured(dir, vhostsRoot)
}

// NewConfigured creates a production backup service bound to installation-
// owned local backup and vhost roots while retaining legacy artifact discovery.
func NewConfigured(dir, vhostsRoot string) *Service {
	legacyDirs := []string{}
	if filepath.Clean(dir) != filepath.Clean(defaultBackupDir) {
		legacyDirs = append(legacyDirs, defaultBackupDir)
	}
	s := newService(dir, vhostsRoot, legacyDirs)
	s.loadPersistedJobs()
	return s
}

// NewAt creates an isolated backup service without scanning legacy locations.
// It is useful for bounded integrations and tests that own their directory.
func NewAt(dir string) *Service {
	return NewAtWithVhostsRoot(dir, "")
}

// NewAtWithVhostsRoot creates an isolated backup service with an explicit
// installation-owned vhost root and without scanning legacy locations.
func NewAtWithVhostsRoot(dir, vhostsRoot string) *Service {
	s := newService(dir, vhostsRoot, nil)
	s.loadPersistedJobs()
	return s
}

func newService(dir, vhostsRoot string, legacyDirs []string) *Service {
	vhostsRoot = strings.TrimSpace(vhostsRoot)
	if filepath.IsAbs(vhostsRoot) {
		vhostsRoot = filepath.Clean(vhostsRoot)
	} else {
		vhostsRoot = ""
	}
	return &Service{
		backupDir:  dir,
		legacyDirs: legacyDirs,
		vhostsRoot: vhostsRoot,
		jobs:       make(map[string]*Job),
	}
}

// ValidateCreateOptions binds file-bearing backups to the installation-owned
// vhost root and accepts only observed portable site folder identities.
func (s *Service) ValidateCreateOptions(opts CreateOptions) error {
	backupType := strings.ToLower(strings.TrimSpace(opts.Type))
	if backupType == "" {
		backupType = "full"
	}
	if backupType == "database" {
		if len(opts.Vhosts) > 0 {
			return fmt.Errorf("database-only backups cannot select vhosts")
		}
		return nil
	}
	if backupType != "files" && backupType != "full" {
		return fmt.Errorf("backup type must be full, database, or files")
	}
	if _, err := s.validatedVhostsRoot(); err != nil {
		return err
	}
	if len(opts.Vhosts) > 16 {
		return fmt.Errorf("at most 16 vhosts may be selected")
	}

	seen := make(map[string]struct{}, len(opts.Vhosts))
	for _, name := range opts.Vhosts {
		if name == "." || name == ".." || !portableVhostNameRe.MatchString(name) {
			return fmt.Errorf("invalid vhost identity %q", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate vhost identity %q", name)
		}
		seen[name] = struct{}{}
		target := filepath.Join(s.vhostsRoot, name)
		info, statErr := os.Lstat(target)
		if statErr != nil {
			return fmt.Errorf("selected vhost %q is unavailable: %w", name, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("selected vhost %q must be a direct directory below the configured root", name)
		}
		if !info.IsDir() {
			return fmt.Errorf("selected vhost %q is not a directory", name)
		}
	}
	return nil
}

// SetAfterBackupHook registers a callback invoked after successful backup creation.
func (s *Service) SetAfterBackupHook(h AfterBackupHook) { s.onSuccess = h }

// SetNotifyHook registers backup result notifications (success/failure).
func (s *Service) SetNotifyHook(h func(success bool, backupType, fileName, errMsg string)) {
	s.onNotify = h
}

// BackupDir returns the local backup storage directory.
func (s *Service) BackupDir() string { return s.backupDir }

// ListVhostTargets returns portable direct child directories observed below
// the installation-owned vhost root. It never exposes the root path and omits
// files, symlinks, and names that the create contract would reject.
func (s *Service) ListVhostTargets() ([]string, error) {
	root, err := s.validatedVhostsRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read configured vhost root: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !portableVhostNameRe.MatchString(name) || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names, nil
}

func (s *Service) validatedVhostsRoot() (string, error) {
	if s.vhostsRoot == "" {
		return "", fmt.Errorf("configured vhost root must be an absolute path")
	}
	rootInfo, err := os.Stat(s.vhostsRoot)
	if err != nil {
		return "", fmt.Errorf("configured vhost root is unavailable: %w", err)
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("configured vhost root is not a directory")
	}
	return s.vhostsRoot, nil
}

// GetJob returns the job with the given ID, or nil if not found.
func (s *Service) GetJob(id string) *Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job := s.jobs[id]
	if job == nil {
		return nil
	}
	clone := job.clone()
	return &clone
}

// CreateOptions holds parameters for backup creation.
type CreateOptions struct {
	Type             string // "full" | "database" | "files"
	Name             string
	Engine           string // "postgresql" | "mariadb"
	Database         string // specific db, empty = all
	CompressionLevel int    // 1-9
	RetentionCount   int
	Vhosts           []string // portable site folder names under the configured vhost root
	Source           string   // manual | scheduled
	JobID            string   // optional: reuse existing job (cron reporting)
}

// ScheduleOptions for cron-based scheduling.
type ScheduleOptions struct {
	Cron           string
	Type           string
	Database       string
	RetentionCount int
}

// StorageSummary describes local backup storage pressure and artifact health.
// Orphaned files are interrupted/full-backup staging artifacts and are never
// considered restorable backups.
type StorageSummary struct {
	Directory              string   `json:"directory"`
	LegacyDirectories      []string `json:"legacyDirectories"`
	TotalBytes             int64    `json:"totalBytes"`
	ActiveBytes            int64    `json:"activeBytes"`
	CompletedBytes         int64    `json:"completedBytes"`
	InvalidBytes           int64    `json:"invalidBytes"`
	OrphanedBytes          int64    `json:"orphanedBytes"`
	LegacyOrphanedBytes    int64    `json:"legacyOrphanedBytes"`
	CompletedCount         int      `json:"completedCount"`
	InvalidCount           int      `json:"invalidCount"`
	OrphanedCount          int      `json:"orphanedCount"`
	LegacyOrphanedCount    int      `json:"legacyOrphanedCount"`
	RootSize               uint64   `json:"rootSize"`
	RootUsed               uint64   `json:"rootUsed"`
	RootAvailable          uint64   `json:"rootAvailable"`
	RootUsePercent         float64  `json:"rootUsePercent"`
	BackupVolumeSize       uint64   `json:"backupVolumeSize"`
	BackupVolumeUsed       uint64   `json:"backupVolumeUsed"`
	BackupVolumeAvailable  uint64   `json:"backupVolumeAvailable"`
	BackupVolumeUsePercent float64  `json:"backupVolumeUsePercent"`
}

// RestoreValidation describes an artifact-only restore preflight. It never
// mutates the restore target or creates a recovery point.
type RestoreValidation struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	ArtifactBytes    int64  `json:"artifactBytes"`
	IncludesDatabase bool   `json:"includesDatabase"`
	IncludesFiles    bool   `json:"includesFiles"`
	DatabaseEngine   string `json:"databaseEngine,omitempty"`
	DatabaseTarget   string `json:"databaseTarget,omitempty"`
	DatabaseRecovery bool   `json:"databaseRecovery"`
	FilesRollback    bool   `json:"filesRollback"`
}

// ─── List ─────────────────────────────────────────────────────────────────────

// List returns all backup files from the backup directory.
func (s *Service) List() ([]models.BackupInfo, error) {
	if err := s.ensureDir(); err != nil {
		return nil, err
	}

	backups := s.listDirectory(s.backupDir, false, "")
	for _, legacyDir := range s.legacyDirs {
		if filepath.Clean(legacyDir) == filepath.Clean(s.backupDir) {
			continue
		}
		backups = append(backups, s.listDirectory(legacyDir, true, "legacy-")...)
	}

	if len(backups) == 0 {
		return []models.BackupInfo{}, nil
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})
	return backups, nil
}

func (s *Service) listDirectory(dir string, orphanedOnly bool, idPrefix string) []models.BackupInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []models.BackupInfo{}
	}
	var backups []models.BackupInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !isRecognizedBackupArtifact(name) {
			continue
		}
		if orphanedOnly && !isOrphanedArtifact(name) {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		status := BackupValidity(strings.TrimSuffix(name, ".part"), info.Size())
		if isOrphanedArtifact(name) {
			status = "orphaned"
		}
		backups = append(backups, models.BackupInfo{
			ID:        idPrefix + buildID(name),
			Name:      name,
			Type:      parseType(name),
			Path:      filepath.Join(dir, name),
			Size:      info.Size(),
			DiskSize:  allocatedFileBytes(info),
			Status:    status,
			CreatedAt: info.ModTime(),
		})
	}
	return backups
}

// Storage returns measured local backup usage and root filesystem pressure.
func (s *Service) Storage() (StorageSummary, error) {
	list, err := s.List()
	if err != nil {
		return StorageSummary{}, err
	}
	summary := StorageSummary{Directory: s.backupDir, LegacyDirectories: append([]string(nil), s.legacyDirs...)}
	for _, item := range list {
		diskBytes := backupDiskBytes(item)
		summary.TotalBytes += diskBytes
		legacy := strings.HasPrefix(item.ID, "legacy-")
		if !legacy {
			summary.ActiveBytes += diskBytes
		}
		switch item.Status {
		case "completed":
			summary.CompletedCount++
			summary.CompletedBytes += diskBytes
		case "orphaned":
			summary.OrphanedCount++
			summary.OrphanedBytes += diskBytes
			if legacy {
				summary.LegacyOrphanedCount++
				summary.LegacyOrphanedBytes += diskBytes
			}
		default:
			summary.InvalidCount++
			summary.InvalidBytes += diskBytes
		}
	}

	summary.RootSize, summary.RootUsed, summary.RootAvailable, summary.RootUsePercent = filesystemUsage("/")
	summary.BackupVolumeSize, summary.BackupVolumeUsed, summary.BackupVolumeAvailable, summary.BackupVolumeUsePercent = filesystemUsage(s.backupDir)
	return summary, nil
}

func allocatedFileBytes(info os.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Blocks > 0 {
		return stat.Blocks * 512
	}
	return info.Size()
}

func backupDiskBytes(info models.BackupInfo) int64 {
	if info.DiskSize > 0 {
		return info.DiskSize
	}
	return info.Size
}

func filesystemUsage(path string) (size, used, available uint64, usePercent float64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, 0, 0
	}
	blockSize := uint64(stat.Bsize)
	size = stat.Blocks * blockSize
	used = (stat.Blocks - stat.Bfree) * blockSize
	available = stat.Bavail * blockSize
	denominator := used + available
	if denominator > 0 {
		usePercent = float64(used) / float64(denominator) * 100
	}
	return size, used, available, usePercent
}

// ─── Create ───────────────────────────────────────────────────────────────────

// CreateAsync starts a backup in the background and returns a Job immediately.
// Poll GetJob(job.ID) to check status.
func (s *Service) CreateAsync(opts CreateOptions) *Job {
	var job *Job
	if opts.JobID != "" {
		job = s.GetJob(opts.JobID)
	}
	if job == nil {
		job = s.newJob(opts.Type, opts.Source)
	}
	go s.runCreateJob(job.ID, opts)
	return job
}

func (s *Service) runCreateJob(jobID string, opts CreateOptions) {
	s.appendJobLog(jobID, fmt.Sprintf("JOB start id=%s type=%s source=%s", jobID, opts.Type, opts.Source))
	s.appendJobLog(jobID, fmt.Sprintf("backup_dir=%s retention=%d compression=%d", s.backupDir, opts.RetentionCount, opts.CompressionLevel))
	s.updateJob(jobID, PhasePreparing, 3, "Yedek dizini hazırlanıyor…")
	if err := s.ValidateCreateOptions(opts); err != nil {
		s.failJob(jobID, opts, "", err)
		return
	}
	if err := s.ensureDir(); err != nil {
		s.failJob(jobID, opts, "", err)
		return
	}
	s.appendJobLog(jobID, "backup directory ready")
	s.updateJob(jobID, PhasePreparing, 5, "Disk alanı kontrol ediliyor…")
	if err := s.preflightCapacity(opts); err != nil {
		s.failJob(jobID, opts, "", err)
		return
	}

	level := opts.CompressionLevel
	if level < 1 || level > 9 {
		level = 6
	}

	ts := time.Now().Format("20060102150405")
	namePart := opts.Name
	if namePart == "" {
		namePart = fmt.Sprintf("backup-%s", ts)
	}
	namePart = sanitize(namePart)

	var info *models.BackupInfo
	var err error
	switch opts.Type {
	case "database":
		info, err = s.createDatabaseTracked(jobID, namePart, opts, level)
	case "files":
		info, err = s.createFilesTracked(jobID, namePart, opts, level)
	default:
		info, err = s.createFullTracked(jobID, namePart, opts, level)
	}

	if err != nil {
		s.failJob(jobID, opts, namePart, err)
		return
	}
	if info != nil {
		s.setJobOutput(jobID, info.Name, info.Size)
	}
	if s.onNotify != nil && info != nil {
		s.onNotify(true, opts.Type, info.Name, "")
	}
	s.setJob(jobID, JobDone, "Yedek tamamlandı")
	if info != nil && s.onSuccess != nil {
		s.onSuccess(info.Path)
	}
}

// Create runs a backup synchronously and returns the resulting BackupInfo.
func (s *Service) Create(opts CreateOptions) (*models.BackupInfo, error) {
	if err := s.ValidateCreateOptions(opts); err != nil {
		return nil, err
	}
	if err := s.ensureDir(); err != nil {
		return nil, err
	}
	if err := s.preflightCapacity(opts); err != nil {
		return nil, err
	}

	level := opts.CompressionLevel
	if level < 1 || level > 9 {
		level = 6
	}

	ts := time.Now().Format("20060102150405")
	namePart := opts.Name
	if namePart == "" {
		namePart = fmt.Sprintf("backup-%s", ts)
	}
	namePart = sanitize(namePart)

	switch opts.Type {
	case "database":
		return s.createDatabase(namePart, opts, level)
	case "files":
		return s.createFiles(namePart, opts, level)
	default: // "full"
		return s.createFull(namePart, opts, level)
	}
}

func (s *Service) createDatabase(namePart string, opts CreateOptions, level int) (*models.BackupInfo, error) {
	engine := opts.Engine
	if engine == "" {
		engine = "postgresql"
	}
	db := opts.Database
	if db == "" {
		db = "all"
	}
	db = sanitize(db)

	filename := fmt.Sprintf("%s-db-%s-%s.sql.gz", namePart, db, engine)
	fullPath := filepath.Join(s.backupDir, filename)
	completed := false
	defer func() {
		if !completed {
			_ = os.Remove(fullPath)
		}
	}()

	if err := pgDumpGzip(db, engine, fullPath, level, dbTimeout); err != nil {
		return nil, fmt.Errorf("database backup failed: %w", err)
	}
	info := s.statBackup(filename, "database")
	if err := validateDatabaseBackupSize(info.Size); err != nil {
		return nil, err
	}
	completed = true
	return info, nil
}

// pgDumpGzip runs dump | gzip using exec.Cmd pipe chaining — no shell interpreter.
func pgDumpGzip(db, engine, outPath string, level int, timeout time.Duration) error {
	return pgDumpGzipWithLog(db, engine, outPath, level, timeout, nil)
}

// pgDumpGzipWithLog optionally streams dump stderr lines to onLog (for live job verbose).
func pgDumpGzipWithLog(db, engine, outPath string, level int, timeout time.Duration, onLog func(string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	dumpCmd := pgDumpCommand(ctx, db, engine)
	if engine == "postgresql" {
		dumpCmd.Env = pgDumpEnv()
	}

	gzCmd := exec.CommandContext(ctx, "gzip", fmt.Sprintf("-%d", level))

	dumpOutput, dumpWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("prepare dump stream: %w", err)
	}
	defer func() { _ = dumpOutput.Close() }()
	defer func() { _ = dumpWriter.Close() }()
	dumpCmd.Stdout = dumpWriter
	gzCmd.Stdin = io.MultiReader(strings.NewReader(databaseBackupHeader(db)), dumpOutput)

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer func() { _ = out.Close() }()
	gzCmd.Stdout = out

	var dumpStderr, gzStderr strings.Builder
	if onLog != nil {
		stderrR, stderrW := io.Pipe()
		dumpCmd.Stderr = stderrW
		go func() {
			sc := bufio.NewScanner(stderrR)
			for sc.Scan() {
				line := sc.Text()
				dumpStderr.WriteString(line)
				dumpStderr.WriteByte('\n')
				onLog("pg_dump: " + line)
			}
			_ = stderrR.Close()
		}()
	} else {
		dumpCmd.Stderr = &dumpStderr
	}
	gzCmd.Stderr = &gzStderr

	if err := gzCmd.Start(); err != nil {
		return fmt.Errorf("start gzip: %w", err)
	}
	if err := dumpCmd.Start(); err != nil {
		_ = dumpWriter.Close()
		_ = gzCmd.Process.Kill()
		_ = gzCmd.Wait()
		return fmt.Errorf("start dump: %w", err)
	}

	dumpErr := dumpCmd.Wait()
	_ = dumpWriter.Close()
	if onLog != nil {
		if w, ok := dumpCmd.Stderr.(io.Closer); ok {
			_ = w.Close()
		}
	}
	gzErr := gzCmd.Wait()

	if dumpErr != nil {
		stderr := dumpStderr.String()
		if engine == "postgresql" {
			if strings.Contains(stderr, "role \"root\"") || strings.Contains(stderr, "Peer authentication") {
				return fmt.Errorf("dump failed: PostgreSQL peer auth — panel must run pg_dump as OS user %q (sudo). Set HSERVER_PG_RUN_AS or NOPASSWD sudoers; stderr: %s", pgRunAs(), stderr)
			}
			if strings.Contains(dumpErr.Error(), "sudo") || strings.Contains(stderr, "sudo") {
				return fmt.Errorf("dump failed: sudo -u %s required — %w; stderr: %s", pgRunAs(), dumpErr, stderr)
			}
		}
		return fmt.Errorf("dump failed: %w — stderr: %s", dumpErr, stderr)
	}
	if gzErr != nil {
		return fmt.Errorf("gzip failed: %w — stderr: %s", gzErr, gzStderr.String())
	}
	return nil
}

func (s *Service) filesTarArgs(fullPath, backupDir string, opts CreateOptions) []string {
	args := []string{"-czf", fullPath, "--exclude=" + backupDir, "-C", "/"}
	args = append(args, s.resolveFilesTarTargets(opts)...)
	return args
}

func (s *Service) resolveFilesTarTargets(opts CreateOptions) []string {
	if s.vhostsRoot == "" {
		return nil
	}
	if len(opts.Vhosts) > 0 {
		out := make([]string, 0, len(opts.Vhosts))
		for _, v := range opts.Vhosts {
			out = append(out, strings.TrimPrefix(filepath.Join(s.vhostsRoot, v), "/"))
		}
		return out
	}
	return []string{strings.TrimPrefix(s.vhostsRoot, "/")}
}

func (s *Service) createFiles(namePart string, opts CreateOptions, level int) (*models.BackupInfo, error) {
	filename := fmt.Sprintf("%s-files.tar.gz", namePart)
	fullPath := filepath.Join(s.backupDir, filename)
	completed := false
	defer func() {
		if !completed {
			_ = os.Remove(fullPath)
		}
	}()

	args := s.filesTarArgs(fullPath, s.backupDir, opts)
	_, err := executeChecked(filesTimeout, "tar", args...)
	if err != nil {
		return nil, fmt.Errorf("files backup failed: %w", err)
	}

	info := s.statBackup(filename, "files")
	if info.Status != "completed" {
		return nil, fmt.Errorf("files backup archive not created at %s", fullPath)
	}
	completed = true
	return info, nil
}

func (s *Service) createFull(namePart string, opts CreateOptions, level int) (*models.BackupInfo, error) {
	partialPaths := s.fullPartialPaths(namePart, opts)
	filename := fmt.Sprintf("%s-full.tar.gz", namePart)
	fullPath := filepath.Join(s.backupDir, filename)
	completed := false
	defer func() {
		for _, path := range partialPaths {
			_ = os.Remove(path)
		}
		if !completed {
			_ = os.Remove(fullPath)
		}
	}()

	dbInfo, dbErr := s.createDatabase(namePart+"-partial", opts, level)
	filesInfo, filesErr := s.createFiles(namePart+"-partial", opts, level)

	if dbErr != nil || dbInfo == nil || filesErr != nil || filesInfo == nil {
		var errs []string
		if dbErr != nil {
			errs = append(errs, "database: "+dbErr.Error())
		} else if dbInfo == nil {
			errs = append(errs, "database: output missing")
		}
		if filesErr != nil {
			errs = append(errs, "files: "+filesErr.Error())
		} else if filesInfo == nil {
			errs = append(errs, "files: output missing")
		}
		return nil, fmt.Errorf("full backup failed — both database and files are required: %s", strings.Join(errs, "; "))
	}

	parts := []string{dbInfo.Path, filesInfo.Path}
	args := fullArchiveArgs(fullPath, s.backupDir, parts)
	if _, err := executeChecked(filesTimeout, "tar", args...); err != nil {
		return nil, fmt.Errorf("full backup archive failed: %w", err)
	}
	for _, p := range parts {
		os.Remove(p) //nolint:errcheck
	}

	info := s.statBackup(filename, "full")
	if info.Status != "completed" {
		return nil, fmt.Errorf("full backup archive not created at %s", fullPath)
	}

	if opts.RetentionCount > 0 {
		s.applyRetention("full", opts.RetentionCount) //nolint:errcheck
	}
	completed = true
	return info, nil
}

func fullArchiveArgs(fullPath, backupDir string, parts []string) []string {
	args := []string{"-czf", fullPath, "-C", backupDir}
	for _, part := range parts {
		args = append(args, filepath.Base(part))
	}
	return args
}

// ─── Delete ───────────────────────────────────────────────────────────────────

// Delete removes the backup file identified by id.
func (s *Service) Delete(id string) error {
	list, err := s.List()
	if err != nil {
		return err
	}
	safeID := sanitize(id)
	for _, b := range list {
		if b.ID == safeID {
			return os.Remove(b.Path)
		}
	}
	return fmt.Errorf("backup %q not found", id)
}

// PurgeInvalid deletes all backups marked invalid (stub/empty dumps and archives).
func (s *Service) PurgeInvalid() (int, int64, error) {
	list, err := s.List()
	if err != nil {
		return 0, 0, err
	}
	var removed int
	var freed int64
	for _, b := range list {
		if b.Status != "invalid" {
			continue
		}
		if err := os.Remove(b.Path); err != nil {
			return removed, freed, err
		}
		removed++
		freed += b.Size
	}
	return removed, freed, nil
}

// PurgeOrphaned removes only explicitly selected interrupted/staging artifacts.
// It refuses to run while any backup job is active so a live full-backup stage
// can never be mistaken for an abandoned file.
func (s *Service) PurgeOrphaned(ids []string) (int, int64, error) {
	if len(ids) == 0 {
		return 0, 0, fmt.Errorf("at least one orphaned artifact id is required")
	}
	if len(ids) > 200 {
		return 0, 0, fmt.Errorf("too many artifact ids (maximum 200)")
	}
	if len(s.ListActiveJobs()) > 0 {
		return 0, 0, fmt.Errorf("backup cleanup is disabled while a backup job is active")
	}

	list, err := s.List()
	if err != nil {
		return 0, 0, err
	}
	orphaned := make(map[string]models.BackupInfo)
	for _, item := range list {
		if item.Status == "orphaned" {
			orphaned[item.ID] = item
		}
	}

	seen := make(map[string]bool, len(ids))
	selected := make([]models.BackupInfo, 0, len(ids))
	for _, rawID := range ids {
		id := sanitize(rawID)
		if id == "" || seen[id] {
			continue
		}
		item, ok := orphaned[id]
		if !ok {
			return 0, 0, fmt.Errorf("artifact %q is not an orphaned backup partial", rawID)
		}
		seen[id] = true
		selected = append(selected, item)
	}

	var removed int
	var freed int64
	for _, item := range selected {
		if err := os.Remove(item.Path); err != nil {
			return removed, freed, err
		}
		removed++
		freed += backupDiskBytes(item)
	}
	return removed, freed, nil
}

// ─── Restore ──────────────────────────────────────────────────────────────────

// Restore restores from a backup file identified by id (synchronous).
func (s *Service) Restore(id string) (string, error) {
	s.restoreMu.Lock()
	defer s.restoreMu.Unlock()

	found, err := s.restoreCandidate(id)
	if err != nil {
		return "", err
	}

	switch found.Type {
	case "database":
		engine, engineErr := databaseEngineFromBackupName(found.Name)
		if engineErr != nil {
			return "", engineErr
		}
		return s.restoreDatabaseSafely(found.Path, engine, dbTimeout)
	case "files":
		return restoreFilesBackup(found.Path)
	default:
		return s.restoreFullBundle(found.Path, restoreDatabaseBackup, restoreFilesBackup, s.createDatabaseRecovery)
	}
}

func (s *Service) restoreCandidate(id string) (*models.BackupInfo, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	safeID := sanitize(id)
	var found *models.BackupInfo
	for i := range list {
		if list[i].ID == safeID {
			found = &list[i]
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("backup %q not found", id)
	}
	if found.Status != "completed" {
		return nil, fmt.Errorf("backup %q is %s and cannot be restored", id, found.Status)
	}
	return found, nil
}

// ValidateRestore fully reads and validates a restore artifact without
// changing files or databases. Connection/authentication readiness is checked
// when the actual restore creates its pre-mutation recovery point.
func (s *Service) ValidateRestore(id string) (*RestoreValidation, error) {
	found, err := s.restoreCandidate(id)
	if err != nil {
		return nil, err
	}
	result := &RestoreValidation{
		ID:            found.ID,
		Name:          found.Name,
		Type:          found.Type,
		ArtifactBytes: found.Size,
	}
	switch found.Type {
	case "database":
		if err := validateDatabaseRestoreInput(found.Path); err != nil {
			return nil, fmt.Errorf("database artifact validation failed: %w", err)
		}
		engine, err := databaseEngineFromBackupName(found.Name)
		if err != nil {
			return nil, err
		}
		target, err := databaseTargetFromBackup(found.Path, engine)
		if err != nil {
			return nil, fmt.Errorf("database restore target validation failed: %w", err)
		}
		result.IncludesDatabase = true
		result.DatabaseEngine = engine
		result.DatabaseTarget = target
		result.DatabaseRecovery = true
	case "files":
		if err := validateFilesRestoreInput(found.Path); err != nil {
			return nil, fmt.Errorf("files artifact validation failed: %w", err)
		}
		result.IncludesFiles = true
		result.FilesRollback = true
	default:
		stagingDir, err := os.MkdirTemp(s.backupDir, ".full-validate-")
		if err != nil {
			return nil, fmt.Errorf("full restore validation staging directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(stagingDir) }()
		parts, err := extractFullBundle(found.Path, stagingDir)
		if err != nil {
			return nil, fmt.Errorf("full restore bundle validation failed: %w", err)
		}
		target, err := databaseTargetFromBackup(parts.databasePath, parts.databaseEngine)
		if err != nil {
			return nil, fmt.Errorf("database restore target validation failed: %w", err)
		}
		result.IncludesDatabase = true
		result.IncludesFiles = true
		result.DatabaseEngine = parts.databaseEngine
		result.DatabaseTarget = target
		result.DatabaseRecovery = true
		result.FilesRollback = true
	}
	return result, nil
}

type fullBundleParts struct {
	databasePath   string
	databaseEngine string
	filesPath      string
}

type databaseRestoreFunc func(string, string, time.Duration) (string, error)
type filesRestoreFunc func(string) (string, error)
type databaseRecoveryFunc func(string, string) (string, error)

func (s *Service) restoreFullBundle(bundlePath string, restoreDatabase databaseRestoreFunc, restoreFiles filesRestoreFunc, createRecovery databaseRecoveryFunc) (string, error) {
	stagingDir, err := os.MkdirTemp(s.backupDir, ".full-restore-")
	if err != nil {
		return "", fmt.Errorf("full restore staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()

	parts, err := extractFullBundle(bundlePath, stagingDir)
	if err != nil {
		return "", fmt.Errorf("full restore bundle validation failed: %w", err)
	}
	recoveryPath := ""
	if createRecovery != nil {
		recoveryPath, err = createRecovery(parts.databasePath, parts.databaseEngine)
		if err != nil {
			return "", fmt.Errorf("full restore recovery backup failed before mutation: %w", err)
		}
	}

	dbOutput, err := restoreDatabase(parts.databasePath, parts.databaseEngine, dbTimeout)
	if err != nil {
		if recoveryPath != "" {
			if _, rollbackErr := restoreDatabase(recoveryPath, parts.databaseEngine, dbTimeout); rollbackErr != nil {
				return "", fmt.Errorf("full restore database stage failed: %w; automatic database rollback also failed: %v", err, rollbackErr)
			}
			return "", fmt.Errorf("full restore database stage failed and was rolled back from %s: %w", filepath.Base(recoveryPath), err)
		}
		return "", fmt.Errorf("full restore database stage failed: %w", err)
	}
	filesOutput, err := restoreFiles(parts.filesPath)
	if err != nil {
		if recoveryPath != "" {
			if _, rollbackErr := restoreDatabase(recoveryPath, parts.databaseEngine, dbTimeout); rollbackErr != nil {
				return "", fmt.Errorf("full restore files stage failed after database stage completed: %w; automatic database rollback also failed: %v", err, rollbackErr)
			}
			return "", fmt.Errorf("full restore files stage failed and the database was rolled back from %s: %w", filepath.Base(recoveryPath), err)
		}
		return "", fmt.Errorf("full restore files stage failed after database stage completed: %w", err)
	}
	output := strings.TrimSpace(dbOutput + "\n" + filesOutput)
	if recoveryPath != "" {
		output = strings.TrimSpace(output + "\nRecovery backup: " + filepath.Base(recoveryPath))
	}
	return output, nil
}

func (s *Service) createDatabaseRecovery(sourcePath, engine string) (string, error) {
	target, err := databaseTargetFromBackup(sourcePath, engine)
	if err != nil {
		return "", err
	}
	if err := s.ensureDir(); err != nil {
		return "", err
	}
	name := fmt.Sprintf("pre-restore-%s-db-%s-%s.sql.gz", time.Now().UTC().Format("20060102T150405.000000000Z"), sanitize(target), engine)
	finalPath := filepath.Join(s.backupDir, name)
	partialPath := finalPath + ".part"
	if err := pgDumpGzip(target, engine, partialPath, 6, dbTimeout); err != nil {
		_ = os.Remove(partialPath)
		return "", err
	}
	if err := os.Rename(partialPath, finalPath); err != nil {
		_ = os.Remove(partialPath)
		return "", err
	}
	return finalPath, nil
}

func (s *Service) restoreDatabaseSafely(sourcePath, engine string, timeout time.Duration) (string, error) {
	if err := validateDatabaseRestoreInput(sourcePath); err != nil {
		return "", fmt.Errorf("restore validation failed before mutation: %w", err)
	}
	recoveryPath, err := s.createDatabaseRecovery(sourcePath, engine)
	if err != nil {
		return "", fmt.Errorf("recovery backup failed before mutation: %w", err)
	}
	output, err := restoreDatabaseBackup(sourcePath, engine, timeout)
	if err == nil {
		return strings.TrimSpace(output + "\nRecovery backup: " + filepath.Base(recoveryPath)), nil
	}
	if _, rollbackErr := restoreDatabaseBackup(recoveryPath, engine, timeout); rollbackErr != nil {
		return "", fmt.Errorf("restore failed: %w; automatic rollback also failed: %v", err, rollbackErr)
	}
	return "", fmt.Errorf("restore failed and was rolled back from %s: %w", filepath.Base(recoveryPath), err)
}

func extractFullBundle(bundlePath, stagingDir string) (fullBundleParts, error) {
	var parts fullBundleParts
	bundle, err := os.Open(bundlePath)
	if err != nil {
		return parts, err
	}
	defer func() { _ = bundle.Close() }()

	gz, err := gzip.NewReader(bundle)
	if err != nil {
		return parts, fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		header, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return parts, fmt.Errorf("read tar entry: %w", nextErr)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return parts, fmt.Errorf("unsupported bundle entry type for %q", header.Name)
		}

		base := path.Base(strings.TrimSpace(header.Name))
		var destination string
		switch {
		case strings.Contains(base, "-partial-db-") && (strings.HasSuffix(base, ".sql.gz") || strings.HasSuffix(base, ".sql")):
			if parts.databasePath != "" {
				return parts, fmt.Errorf("bundle contains multiple database parts")
			}
			engine, engineErr := databaseEngineFromBackupName(base)
			if engineErr != nil {
				return parts, engineErr
			}
			ext := ".sql"
			if strings.HasSuffix(base, ".gz") {
				ext = ".sql.gz"
			}
			destination = filepath.Join(stagingDir, "database"+ext)
			parts.databasePath = destination
			parts.databaseEngine = engine
		case strings.HasSuffix(base, "-partial-files.tar.gz"):
			if parts.filesPath != "" {
				return parts, fmt.Errorf("bundle contains multiple files parts")
			}
			destination = filepath.Join(stagingDir, "files.tar.gz")
			parts.filesPath = destination
		default:
			return parts, fmt.Errorf("unexpected bundle entry %q", header.Name)
		}

		if header.Size < 0 {
			return parts, fmt.Errorf("invalid size for bundle entry %q", header.Name)
		}
		_, _, available, _ := filesystemUsage(stagingDir)
		required := uint64(header.Size)
		if required > ^uint64(0)-backupReserve {
			required = ^uint64(0)
		} else {
			required += backupReserve
		}
		if available < required {
			return parts, fmt.Errorf(
				"insufficient staging space for %q: %s required, %s available",
				header.Name,
				formatCapacityBytes(required),
				formatCapacityBytes(available),
			)
		}
		out, openErr := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr != nil {
			return parts, openErr
		}
		_, copyErr := io.CopyN(out, tr, header.Size)
		closeErr := out.Close()
		if copyErr != nil {
			return parts, fmt.Errorf("extract bundle entry %q: %w", header.Name, copyErr)
		}
		if closeErr != nil {
			return parts, fmt.Errorf("close bundle entry %q: %w", header.Name, closeErr)
		}
	}

	if parts.databasePath == "" || parts.filesPath == "" {
		return parts, fmt.Errorf("full bundle must contain exactly one database part and one files part")
	}
	if err := validateDatabaseRestoreInput(parts.databasePath); err != nil {
		return parts, fmt.Errorf("database part is invalid: %w", err)
	}
	if err := validateFilesRestoreInput(parts.filesPath); err != nil {
		return parts, fmt.Errorf("files part is invalid: %w", err)
	}
	return parts, nil
}

func validateDatabaseRestoreInput(filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	if err := validateDatabaseBackupSize(info.Size()); err != nil {
		return err
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if !strings.HasSuffix(filePath, ".gz") {
		_, err = io.Copy(io.Discard, f)
		return err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, gz)
	closeErr := gz.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

const fileRecoveryMetadataRoot = ".hserver-recovery"

type filesArchiveEntry struct {
	name     string
	typeflag byte
}

type filesRestorePlan struct {
	existingPaths []string
	createdRoots  []string
}

type backupCommandExecutor func(time.Duration, string, ...string) (*shell.Result, error)

func validateFilesRestoreInput(filePath string) error {
	_, err := inspectFilesRestoreInput(filePath)
	return err
}

func inspectFilesRestoreInput(filePath string) ([]filesArchiveEntry, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	allowRecoveryMetadata := isFilesRecoveryArchive(filePath)
	entries := make([]filesArchiveEntry, 0)
	seen := make(map[string]struct{})
	for {
		header, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		cleanName := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if path.IsAbs(header.Name) || cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
			return nil, fmt.Errorf("unsafe files archive path %q", header.Name)
		}
		if cleanName == fileRecoveryMetadataRoot || strings.HasPrefix(cleanName, fileRecoveryMetadataRoot+"/") {
			if !allowRecoveryMetadata {
				return nil, fmt.Errorf("files archive uses reserved recovery metadata path %q", header.Name)
			}
			switch header.Typeflag {
			case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
				continue
			default:
				return nil, fmt.Errorf("unsupported recovery metadata entry type for %q", header.Name)
			}
		}
		if _, duplicate := seen[cleanName]; duplicate {
			return nil, fmt.Errorf("files archive contains duplicate path %q", header.Name)
		}
		seen[cleanName] = struct{}{}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
		case tar.TypeSymlink:
			linkTarget := path.Clean(path.Join(path.Dir(cleanName), header.Linkname))
			if path.IsAbs(header.Linkname) || linkTarget == ".." || strings.HasPrefix(linkTarget, "../") {
				return nil, fmt.Errorf("unsafe files archive symlink %q -> %q", header.Name, header.Linkname)
			}
		case tar.TypeLink:
			linkTarget := path.Clean(strings.TrimPrefix(header.Linkname, "./"))
			if path.IsAbs(header.Linkname) || linkTarget == ".." || strings.HasPrefix(linkTarget, "../") {
				return nil, fmt.Errorf("unsafe files archive hardlink %q -> %q", header.Name, header.Linkname)
			}
		default:
			return nil, fmt.Errorf("unsupported files archive entry type for %q", header.Name)
		}
		entries = append(entries, filesArchiveEntry{name: cleanName, typeflag: header.Typeflag})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("files archive is empty")
	}
	for _, symlink := range entries {
		if symlink.typeflag != tar.TypeSymlink {
			continue
		}
		for _, entry := range entries {
			if entry.name != symlink.name && strings.HasPrefix(entry.name, symlink.name+"/") {
				return nil, fmt.Errorf("files archive path %q traverses archive symlink %q", entry.name, symlink.name)
			}
		}
	}
	return entries, nil
}

func databaseEngineFromBackupName(name string) (string, error) {
	base := filepath.Base(name)
	switch {
	case strings.HasSuffix(base, "-postgresql.sql.gz"), strings.HasSuffix(base, "-postgresql.sql"):
		return "postgresql", nil
	case strings.HasSuffix(base, "-mariadb.sql.gz"), strings.HasSuffix(base, "-mariadb.sql"):
		return "mariadb", nil
	default:
		return "", fmt.Errorf("database engine is not encoded in backup name %q", base)
	}
}

func restoreFilesBackup(filePath string) (string, error) {
	return restoreFilesBackupAt(filePath, "/")
}

func restoreFilesBackupAt(filePath, targetRoot string) (string, error) {
	return restoreFilesBackupAtWithExecutor(filePath, targetRoot, executeChecked)
}

func restoreFilesBackupAtWithExecutor(filePath, targetRoot string, executor backupCommandExecutor) (string, error) {
	targetRoot = filepath.Clean(targetRoot)
	if !filepath.IsAbs(targetRoot) {
		return "", fmt.Errorf("restore target must be absolute: %q", targetRoot)
	}
	info, err := os.Lstat(targetRoot)
	if err != nil {
		return "", fmt.Errorf("restore target: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("restore target is not a directory: %q", targetRoot)
	}
	plan, err := prepareFilesRestore(filePath, targetRoot)
	if err != nil {
		return "", fmt.Errorf("restore validation failed: %w", err)
	}
	recoveryPath, err := createFilesRecovery(filePath, targetRoot, plan, executor)
	if err != nil {
		return "", fmt.Errorf("file recovery backup failed before mutation: %w", err)
	}
	result, err := extractFilesArchive(filePath, targetRoot, executor)
	if err != nil {
		rollbackErr := rollbackFilesRestore(plan, recoveryPath, targetRoot, executor)
		if rollbackErr != nil {
			return "", fmt.Errorf("restore failed: %w; automatic file rollback also failed: %v", err, rollbackErr)
		}
		if recoveryPath != "" {
			return "", fmt.Errorf("restore failed and file changes were rolled back from %s: %w", filepath.Base(recoveryPath), err)
		}
		return "", fmt.Errorf("restore failed and newly created paths were removed: %w", err)
	}
	output := result.Stdout + result.Stderr
	if recoveryPath != "" {
		output = strings.TrimSpace(output + "\nRecovery backup: " + filepath.Base(recoveryPath))
	}
	return output, nil
}

func prepareFilesRestore(filePath, targetRoot string) (filesRestorePlan, error) {
	entries, err := inspectFilesRestoreInput(filePath)
	if err != nil {
		return filesRestorePlan{}, err
	}
	plan := filesRestorePlan{}
	backupRoot := filepath.Clean(filepath.Dir(filePath))
	for _, entry := range entries {
		relative := filepath.FromSlash(entry.name)
		target := filepath.Join(targetRoot, relative)
		if !pathWithinRoot(targetRoot, target) || target == targetRoot {
			return filesRestorePlan{}, fmt.Errorf("unsafe files restore target %q", entry.name)
		}
		if targetRoot == string(filepath.Separator) && pathWithinRoot(backupRoot, target) {
			return filesRestorePlan{}, fmt.Errorf("files restore target overlaps backup storage: %q", entry.name)
		}
		if err := rejectSymlinkParents(targetRoot, target); err != nil {
			return filesRestorePlan{}, err
		}
		targetInfo, statErr := os.Lstat(target)
		if statErr != nil && !os.IsNotExist(statErr) {
			return filesRestorePlan{}, statErr
		}
		isDirectory := entry.typeflag == tar.TypeDir
		if statErr == nil {
			if isDirectory {
				if !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
					return filesRestorePlan{}, fmt.Errorf("files restore directory conflicts with existing path %q", entry.name)
				}
				continue
			}
			if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
				return filesRestorePlan{}, fmt.Errorf("files restore entry conflicts with existing directory %q", entry.name)
			}
			plan.existingPaths = addMinimalRestorePath(plan.existingPaths, entry.name)
			continue
		}
		missingRoot, missingErr := highestMissingRestoreRoot(targetRoot, target)
		if missingErr != nil {
			return filesRestorePlan{}, missingErr
		}
		missingRelative, relativeErr := filepath.Rel(targetRoot, missingRoot)
		if relativeErr != nil {
			return filesRestorePlan{}, relativeErr
		}
		plan.createdRoots = addMinimalRestorePath(plan.createdRoots, filepath.ToSlash(missingRelative))
	}
	sort.Strings(plan.existingPaths)
	sort.Strings(plan.createdRoots)
	return plan, nil
}

func pathWithinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func rejectSymlinkParents(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	current := root
	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("files restore path traverses existing symlink %q", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("files restore path traverses non-directory %q", current)
		}
	}
	return nil
}

func highestMissingRestoreRoot(root, target string) (string, error) {
	candidate := target
	for {
		parent := filepath.Dir(candidate)
		if parent == root {
			return candidate, nil
		}
		info, err := os.Lstat(parent)
		if os.IsNotExist(err) {
			candidate = parent
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("files restore parent is not a safe directory %q", parent)
		}
		return candidate, nil
	}
}

func addMinimalRestorePath(paths []string, candidate string) []string {
	candidate = strings.Trim(filepath.ToSlash(candidate), "/")
	if candidate == "" {
		return paths
	}
	filtered := paths[:0]
	for _, existing := range paths {
		if candidate == existing || strings.HasPrefix(candidate, existing+"/") {
			return paths
		}
		if strings.HasPrefix(existing, candidate+"/") {
			continue
		}
		filtered = append(filtered, existing)
	}
	return append(filtered, candidate)
}

func createFilesRecovery(filePath, targetRoot string, plan filesRestorePlan, executor backupCommandExecutor) (string, error) {
	if len(plan.existingPaths) == 0 {
		return "", nil
	}
	backupDir := filepath.Dir(filePath)
	stagingDir, err := os.MkdirTemp(backupDir, ".files-recovery-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()
	metadataDir := filepath.Join(stagingDir, fileRecoveryMetadataRoot)
	if err := os.Mkdir(metadataDir, 0o700); err != nil {
		return "", err
	}
	readme := fmt.Sprintf("schema=1\npurpose=hserver-file-restore-recovery\noverwritten_paths=%d\n", len(plan.existingPaths))
	if err := os.WriteFile(filepath.Join(metadataDir, "README"), []byte(readme), 0o600); err != nil {
		return "", err
	}
	padding := make([]byte, 2048)
	if _, err := cryptorand.Read(padding); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "padding.bin"), padding, 0o600); err != nil {
		return "", err
	}
	listPath := filepath.Join(stagingDir, "existing-paths.list")
	listFile, err := os.OpenFile(listPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	for _, existing := range plan.existingPaths {
		if _, err := listFile.WriteString(existing + "\x00"); err != nil {
			_ = listFile.Close()
			return "", err
		}
	}
	if err := listFile.Close(); err != nil {
		return "", err
	}
	name := fmt.Sprintf("pre-restore-%s-files.tar.gz", time.Now().UTC().Format("20060102T150405.000000000Z"))
	finalPath := filepath.Join(backupDir, name)
	partialPath := finalPath + ".part"
	args := []string{
		"--create", "--gzip", "--file", partialPath,
		"--directory", targetRoot, "--no-recursion", "--null", "--verbatim-files-from", "--files-from", listPath,
		"--directory", stagingDir,
		filepath.Join(fileRecoveryMetadataRoot, "README"),
		filepath.Join(fileRecoveryMetadataRoot, "padding.bin"),
	}
	if _, err := executor(filesTimeout, "tar", args...); err != nil {
		_ = os.Remove(partialPath)
		return "", err
	}
	if err := os.Chmod(partialPath, 0o600); err != nil {
		_ = os.Remove(partialPath)
		return "", err
	}
	if err := os.Rename(partialPath, finalPath); err != nil {
		_ = os.Remove(partialPath)
		return "", err
	}
	return finalPath, nil
}

func extractFilesArchive(filePath, targetRoot string, executor backupCommandExecutor) (*shell.Result, error) {
	return executor(filesTimeout,
		"tar", "--extract", "--gzip", "--file", filePath, "--directory", targetRoot,
		"--no-overwrite-dir",
		"--exclude="+fileRecoveryMetadataRoot, "--exclude="+fileRecoveryMetadataRoot+"/*",
	)
}

func rollbackFilesRestore(plan filesRestorePlan, recoveryPath, targetRoot string, executor backupCommandExecutor) error {
	var failures []string
	for _, relative := range plan.createdRoots {
		target := filepath.Join(targetRoot, filepath.FromSlash(relative))
		if !pathWithinRoot(targetRoot, target) || target == targetRoot {
			failures = append(failures, "unsafe cleanup path: "+relative)
			continue
		}
		if err := os.RemoveAll(target); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if recoveryPath != "" {
		if _, err := extractFilesArchive(recoveryPath, targetRoot, executor); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func isFilesRecoveryArchive(filePath string) bool {
	base := filepath.Base(filePath)
	return strings.HasPrefix(base, "pre-restore-") && strings.HasSuffix(base, "-files.tar.gz")
}

// RestoreAsync starts a restore in the background and returns a Job immediately.
func (s *Service) RestoreAsync(id string) *Job {
	job := s.newJob("restore", "manual")
	go func() {
		s.appendJobLog(job.ID, "restore target backup_id="+id)
		s.updateJob(job.ID, PhaseRestore, 10, "Geri yükleme başlatılıyor…")
		s.setJobCommand(job.ID, "restore backup_id="+id)
		s.updateJob(job.ID, PhaseRestore, 45, "Veriler geri yükleniyor…")
		output, err := s.Restore(id)
		if err != nil {
			s.setJob(job.ID, JobFailed, err.Error())
		} else {
			msg := "Geri yükleme tamamlandı"
			if output != "" {
				msg = output
			}
			s.setJob(job.ID, JobDone, msg)
		}
	}()
	return job
}

// ─── Cron Schedule ────────────────────────────────────────────────────────────

// GetSchedule returns cron entries managed by hserver-panel.
func (s *Service) GetSchedule() ([]ScheduleEntry, error) {
	existing, err := s.loadCrontab()
	if err != nil {
		return nil, err
	}
	return parseSchedule(existing), nil
}

// SetSchedule adds or replaces a backup cron entry for the given type.
func (s *Service) SetSchedule(opts ScheduleOptions) error {
	backupType := strings.ToLower(strings.TrimSpace(opts.Type))
	if err := validateCron(opts.Cron); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidScheduleOptions, err)
	}
	if backupType != "full" && backupType != "database" && backupType != "files" && backupType != "snapshot" {
		return fmt.Errorf("%w: type must be full, database, files, or snapshot", ErrInvalidScheduleOptions)
	}
	if opts.RetentionCount < 1 || opts.RetentionCount > 365 {
		return fmt.Errorf("%w: retention count must be between 1 and 365", ErrInvalidScheduleOptions)
	}
	if opts.Database != "" && sanitize(opts.Database) != opts.Database {
		return fmt.Errorf("%w: database contains unsupported schedule characters", ErrInvalidScheduleOptions)
	}

	existing, err := s.loadCrontab()
	if err != nil {
		return err
	}

	scriptPath := filepath.Join(s.backupDir, "run-backup.sh")
	if err := s.writeCronScript(scriptPath); err != nil {
		return fmt.Errorf("write cron script: %w", err)
	}
	meta := fmt.Sprintf("type=%s retention=%d", backupType, opts.RetentionCount)
	if opts.Database != "" {
		meta += " db=" + sanitize(opts.Database)
	}
	newLine := fmt.Sprintf("%s %s %s %s", opts.Cron, scriptPath, meta, cronMarker)

	// Remove existing line for same type
	var lines []string
	for _, l := range strings.Split(existing, "\n") {
		if strings.Contains(l, cronMarker) && extractMeta(l, "type=") == backupType {
			continue
		}
		lines = append(lines, l)
	}
	lines = append(lines, newLine)
	updated := strings.Join(lines, "\n") + "\n"

	return s.installCrontab(updated)
}

// DeleteSchedule removes a cron line by exact rawLine match.
func (s *Service) DeleteSchedule(rawLine string) error {
	target := strings.TrimSpace(rawLine)
	if !isManagedScheduleTarget(target) {
		return ErrInvalidScheduleTarget
	}

	existing, err := s.loadCrontab()
	if err != nil {
		return err
	}

	var lines []string
	found := false
	for _, l := range strings.Split(existing, "\n") {
		if strings.TrimSpace(l) == target {
			found = true
			continue
		}
		lines = append(lines, l)
	}
	if !found {
		return ErrScheduleNotFound
	}
	updated := strings.Join(lines, "\n") + "\n"

	return s.installCrontab(updated)
}

func isManagedScheduleTarget(line string) bool {
	if !strings.HasSuffix(line, cronMarker) {
		return false
	}
	entries := parseSchedule(line)
	return len(entries) == 1 && entries[0].Type != "" && validateCron(entries[0].Cron) == nil
}

// validateCron ensures the cron expression contains only valid 5-field syntax.
// This prevents injection of shell metacharacters into the crontab.
func validateCron(expr string) error {
	expr = strings.TrimSpace(expr)
	if !cronExprRe.MatchString(expr) {
		return fmt.Errorf("invalid cron expression: %q (must be a standard 5-field cron)", expr)
	}
	return nil
}

// restoreDatabaseBackup streams a compressed or plain SQL backup into the
// database client using exec.Cmd pipe chaining — no shell interpreter involved.
func restoreDatabaseBackup(srcPath, engine string, timeout time.Duration) (string, error) {
	if engine != "postgresql" && engine != "mariadb" {
		return "", fmt.Errorf("unsupported database engine %q", engine)
	}
	target, err := databaseTargetFromBackup(srcPath, engine)
	if err != nil {
		return "", fmt.Errorf("restore target: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var client *exec.Cmd
	switch engine {
	case "postgresql":
		database := target
		if target == "all" {
			database = "postgres"
		}
		client = pgRestoreCommand(ctx, database)
	case "mariadb":
		client = mysqlRestoreCommand(ctx, target)
	}

	var source io.ReadCloser
	var decompressor *exec.Cmd
	var decompressorStderr strings.Builder
	if strings.HasSuffix(srcPath, ".gz") {
		decompressor = exec.CommandContext(ctx, "gzip", "-dc", srcPath)
		pipe, err := decompressor.StdoutPipe()
		if err != nil {
			return "", fmt.Errorf("prepare gzip stream: %w", err)
		}
		source = pipe
		decompressor.Stderr = &decompressorStderr
	} else {
		file, err := os.Open(srcPath)
		if err != nil {
			return "", err
		}
		defer func() { _ = file.Close() }()
		source = file
	}
	client.Stdin = source

	var clientOut, clientStderr strings.Builder
	client.Stdout = &clientOut
	client.Stderr = &clientStderr
	if err := client.Start(); err != nil {
		return "", fmt.Errorf("start %s restore client: %w", engine, err)
	}
	if decompressor != nil {
		if err := decompressor.Start(); err != nil {
			_ = client.Process.Kill()
			return "", fmt.Errorf("start gzip: %w", err)
		}
	}
	clientErr := client.Wait()
	if decompressor != nil {
		_ = source.Close()
		if err := decompressor.Wait(); err != nil {
			return "", fmt.Errorf("gzip failed: %w — %s", err, decompressorStderr.String())
		}
	}
	if clientErr != nil {
		return "", fmt.Errorf("%s restore failed: %w — %s", engine, clientErr, clientStderr.String())
	}
	return clientOut.String() + clientStderr.String(), nil
}

// gunzipPsql remains as a narrow compatibility wrapper for package callers.
func gunzipPsql(srcPath string, timeout time.Duration) (string, error) {
	return restoreDatabaseBackup(srcPath, "postgresql", timeout)
}

func executeChecked(timeout time.Duration, command string, args ...string) (*shell.Result, error) {
	result, err := shell.ExecuteWithTimeout(timeout, command, args...)
	if err != nil {
		return result, err
	}
	if result == nil {
		return nil, fmt.Errorf("%s returned no result", command)
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		return result, fmt.Errorf("%s failed (exit %d): %s", command, result.ExitCode, message)
	}
	return result, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *Service) ensureDir() error {
	return os.MkdirAll(s.backupDir, 0o750)
}

func (s *Service) statBackup(filename, bType string) *models.BackupInfo {
	fullPath := filepath.Join(s.backupDir, filename)
	info, err := os.Stat(fullPath)
	if err != nil {
		return &models.BackupInfo{
			ID:        buildID(filename),
			Name:      filename,
			Type:      bType,
			Path:      fullPath,
			Status:    "failed",
			CreatedAt: time.Now(),
		}
	}
	return &models.BackupInfo{
		ID:        buildID(filename),
		Name:      filename,
		Type:      bType,
		Path:      fullPath,
		Size:      info.Size(),
		DiskSize:  allocatedFileBytes(info),
		Status:    BackupValidity(filename, info.Size()),
		CreatedAt: info.ModTime(),
	}
}

func (s *Service) applyRetention(bType string, keep int) error {
	list, err := s.List()
	if err != nil {
		return err
	}
	var ofType []models.BackupInfo
	for _, b := range list {
		if b.Type == bType && b.Status == "completed" {
			ofType = append(ofType, b)
		}
	}
	// already sorted newest-first
	for i := keep; i < len(ofType); i++ {
		os.Remove(ofType[i].Path) //nolint:errcheck
	}
	return nil
}

func buildID(filename string) string {
	id := strings.TrimSuffix(filename, ".part")
	for _, ext := range []string{".tar.gz", ".sql.gz", ".sql"} {
		id = strings.TrimSuffix(id, ext)
	}
	return id
}

func parseType(filename string) string {
	filename = strings.TrimSuffix(filename, ".part")
	if strings.Contains(filename, "-db-") || strings.Contains(filename, "_db_") {
		return "database"
	}
	if strings.Contains(filename, "-files") || strings.Contains(filename, "_files") {
		return "files"
	}
	return "full"
}

func isRecognizedBackupArtifact(name string) bool {
	base := strings.TrimSuffix(name, ".part")
	return strings.HasSuffix(base, ".tar.gz") || strings.HasSuffix(base, ".sql.gz") || strings.HasSuffix(base, ".sql")
}

func isOrphanedArtifact(name string) bool {
	if strings.HasSuffix(name, ".part") {
		return true
	}
	return strings.HasSuffix(name, "-partial-files.tar.gz") ||
		(strings.Contains(name, "-partial-db-") && (strings.HasSuffix(name, ".sql.gz") || strings.HasSuffix(name, ".sql")))
}

func requiredBackupBytes(backupType string, sourceBytes uint64) uint64 {
	switch backupType {
	case "files":
		if sourceBytes > ^uint64(0)-backupReserve {
			return ^uint64(0)
		}
		return sourceBytes + backupReserve
	case "full", "":
		if sourceBytes > (^uint64(0)-backupReserve)/2 {
			return ^uint64(0)
		}
		return sourceBytes*2 + backupReserve
	default:
		return backupReserve
	}
}

func formatCapacityBytes(value uint64) string {
	const maxInt64 = uint64(^uint64(0) >> 1)
	if value > maxInt64 {
		return "> " + formatJobBytes(int64(maxInt64))
	}
	return formatJobBytes(int64(value))
}

func (s *Service) preflightCapacity(opts CreateOptions) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.backupDir, &stat); err != nil {
		return fmt.Errorf("backup disk preflight failed: %w", err)
	}
	available := stat.Bavail * uint64(stat.Bsize)
	if available < backupReserve {
		return fmt.Errorf("backup blocked: only %s is available; at least %s free space is required before writing a backup", formatCapacityBytes(available), formatCapacityBytes(backupReserve))
	}

	backupType := strings.ToLower(opts.Type)
	if backupType == "" {
		backupType = "full"
	}
	if backupType != "files" && backupType != "full" {
		return nil
	}
	sourceBytes, err := s.estimateBackupSourceBytes(opts)
	if err != nil {
		return fmt.Errorf("backup source-size preflight failed: %w", err)
	}
	required := requiredBackupBytes(backupType, sourceBytes)
	if available < required {
		return fmt.Errorf("backup blocked: %s source data requires about %s free space for a %s backup, but only %s is available", formatCapacityBytes(sourceBytes), formatCapacityBytes(required), backupType, formatCapacityBytes(available))
	}
	return nil
}

func (s *Service) estimateBackupSourceBytes(opts CreateOptions) (uint64, error) {
	targets := s.resolveFilesTarTargets(opts)
	args := []string{"-sx", "-B1", "--"}
	for _, target := range targets {
		args = append(args, "/"+strings.TrimPrefix(target, "/"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/du", args...).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("du failed: %w — %s", err, strings.TrimSpace(string(output)))
	}
	var total uint64
	for _, line := range strings.Split(string(output), "\n") {
		var size uint64
		if _, scanErr := fmt.Sscanf(line, "%d", &size); scanErr == nil {
			total += size
		}
	}
	if total == 0 {
		return 0, fmt.Errorf("source size could not be measured")
	}
	return total, nil
}

func (s *Service) fullPartialPaths(namePart string, opts CreateOptions) []string {
	engine := opts.Engine
	if engine == "" {
		engine = "postgresql"
	}
	database := opts.Database
	if database == "" {
		database = "all"
	}
	base := sanitize(namePart) + "-partial"
	return []string{
		filepath.Join(s.backupDir, fmt.Sprintf("%s-db-%s-%s.sql.gz", base, sanitize(database), engine)),
		filepath.Join(s.backupDir, base+"-files.tar.gz"),
	}
}

func sanitize(s string) string {
	var out strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// ─── Schedule Parsing ─────────────────────────────────────────────────────────

// ScheduleEntry represents a parsed backup cron entry.
type ScheduleEntry struct {
	ID             string `json:"id"`
	Cron           string `json:"cron"`
	Type           string `json:"type"`
	Database       string `json:"database,omitempty"`
	RetentionCount int    `json:"retentionCount"`
	IsActive       bool   `json:"isActive"`
	RawLine        string `json:"rawLine"`
}

func parseSchedule(crontab string) []ScheduleEntry {
	var entries []ScheduleEntry
	for i, line := range strings.Split(crontab, "\n") {
		if !strings.Contains(line, cronMarker) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 6 {
			continue
		}
		cron := strings.Join(parts[:5], " ")
		bType := extractMeta(line, "type=")
		db := extractMeta(line, "db=")
		ret := 10
		if r := extractMeta(line, "retention="); r != "" {
			_, _ = fmt.Sscanf(r, "%d", &ret)
		}
		entries = append(entries, ScheduleEntry{
			ID:             fmt.Sprintf("cron-%d", i),
			Cron:           cron,
			Type:           bType,
			Database:       db,
			RetentionCount: ret,
			IsActive:       !strings.HasPrefix(strings.TrimSpace(line), "#"),
			RawLine:        line,
		})
	}
	return entries
}

// writeCronScript generates the backup runner invoked by system crontab.
func (s *Service) writeCronScript(path string) error {
	port := os.Getenv("HSERVER_PORT")
	if port == "" {
		port = "3085"
	}
	secret := os.Getenv("HSERVER_CRON_SECRET")
	content := fmt.Sprintf(`#!/bin/bash
# HServer automated backup runner — do not edit manually
set -euo pipefail
TYPE="full"
RETENTION="10"
DB=""
for arg in "$@"; do
  case "$arg" in
    type=*) TYPE="${arg#type=}" ;;
    retention=*) RETENTION="${arg#retention=}" ;;
    db=*) DB="${arg#db=}" ;;
  esac
done
PORT=%s
SECRET=%q
BODY="{\"type\":\"${TYPE}\",\"retention\":${RETENTION}"
if [ -n "$DB" ]; then BODY="${BODY},\"database\":\"${DB}\""; fi
BODY="${BODY}}"
if [ -n "$SECRET" ]; then
  curl -sf -X POST "http://127.0.0.1:${PORT}/api/internal/cron/backup" \
    -H "Content-Type: application/json" \
    -H "X-Cron-Secret: ${SECRET}" \
    -d "$BODY" > /dev/null
else
  echo "HSERVER_CRON_SECRET not set — scheduled backup skipped" >&2
  exit 1
fi
`, port, secret)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o750); err != nil {
		return err
	}
	return nil
}

func extractMeta(line, prefix string) string {
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(prefix):]
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
