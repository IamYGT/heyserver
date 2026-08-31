package nginx

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const (
	defaultSitesAvailable = "/etc/nginx/sites-available"
	defaultSitesEnabled   = "/etc/nginx/sites-enabled"
	defaultSnippetsDir    = "/etc/nginx/snippets"
	maxConfigBytes        = 2 << 20
)

var requiredManagedSnippetNames = []string{
	"hserver-acme-challenge.conf",
	"hserver-ssl-params.conf",
	"hserver-security-headers.conf",
	"hserver-security-deny.conf",
	"hserver-compression.conf",
	"hserver-static-cache.conf",
	"hserver-php-fpm.conf",
	"hserver-proxy-params.conf",
}

// ErrNotConfigured identifies missing or invalid installation-owned paths.
var ErrNotConfigured = errors.New("nginx paths are not configured")

var (
	ErrConfigChanged  = errors.New("nginx configuration changed")
	ErrConfigEnabled  = errors.New("nginx configuration is enabled")
	ErrConfigExists   = errors.New("nginx configuration already exists")
	ErrConfigInvalid  = errors.New("nginx configuration test failed")
	ErrConfigNotFound = errors.New("nginx configuration not found")
	ErrConfigTooLarge = errors.New("nginx configuration exceeds the 2097152-byte limit")
)

const readinessTimeout = 5 * time.Second

// Config represents an nginx vhost configuration file.
type Config struct {
	Filename  string `json:"filename"`
	Domain    string `json:"domain"`
	Type      string `json:"type"` // php, proxy, static, redirect
	IsEnabled bool   `json:"isEnabled"`
	Content   string `json:"content,omitempty"`
	Checksum  string `json:"checksum,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Modified  string `json:"modifiedAt,omitempty"`
}

type SaveReceipt struct {
	Message  string `json:"message"`
	Backup   string `json:"backup"`
	Checksum string `json:"checksum"`
}

type ArchiveReceipt struct {
	Message  string `json:"message"`
	Archive  string `json:"archive"`
	Checksum string `json:"checksum"`
}

// ConfigArchive is one installation-owned recovery copy retained after a
// disabled Nginx configuration is archived.
type ConfigArchive struct {
	Archive  string `json:"archive"`
	Filename string `json:"filename"`
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
	Archived string `json:"archivedAt"`
	Modified string `json:"modifiedAt"`
}

type RestoreArchiveReceipt struct {
	Message   string `json:"message"`
	Archive   string `json:"archive"`
	Filename  string `json:"filename"`
	Checksum  string `json:"checksum"`
	IsEnabled bool   `json:"isEnabled"`
}

// ConfigBackup is one validated pre-edit recovery copy retained by Heyserver.
type ConfigBackup struct {
	Backup   string `json:"backup"`
	Filename string `json:"filename"`
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
	Created  string `json:"createdAt"`
	Modified string `json:"modifiedAt"`
}

type RestoreBackupReceipt struct {
	Message   string `json:"message"`
	Backup    string `json:"backup"`
	Recovery  string `json:"recovery"`
	Filename  string `json:"filename"`
	Checksum  string `json:"checksum"`
	IsEnabled bool   `json:"isEnabled"`
}

// TestResult holds the output of nginx -t.
type TestResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

// Status describes observable nginx runtime readiness without treating a
// missing binary, an inactive unit, and a failed probe as the same state.
type Status struct {
	Installed       bool       `json:"installed"`
	Status          string     `json:"status"`
	StatusAvailable bool       `json:"statusAvailable"`
	Version         string     `json:"version"`
	Uptime          string     `json:"uptime"`
	ConfigTest      TestResult `json:"configTest"`
}

// Snippet is a read-only nginx include file exposed for inspection and reuse.
type Snippet struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CreateRequest holds parameters for generating a new vhost config.
type CreateRequest struct {
	Domain string `json:"domain"`
	Type   string `json:"type"` // php, proxy, static, redirect
	// PHP-FPM specific
	PHPVersion string `json:"phpVersion,omitempty"`
	PHPPool    string `json:"phpPool,omitempty"`
	DocRoot    string `json:"docRoot,omitempty"`
	// Reverse proxy specific
	ProxyPass string `json:"proxyPass,omitempty"`
	// Redirect specific
	RedirectTo string `json:"redirectTo,omitempty"`
	// SSL
	UseSSL   bool   `json:"useSSL"`
	CertPath string `json:"certPath,omitempty"`
	KeyPath  string `json:"keyPath,omitempty"`
}

// ServiceConfig binds Nginx file mutations and generated document roots to
// installation-owned directories.
type ServiceConfig struct {
	SitesAvailable string
	SitesEnabled   string
	VhostsRoot     string
	SnippetsDir    string
}

// Service manages nginx configuration files within one installation.
type Service struct {
	sitesAvailable string
	sitesEnabled   string
	snippetsDir    string
	vhostsRoot     string
	mu             sync.Mutex
}

// New creates a new nginx Service instance.
func New() *Service {
	return NewWithConfig(ServiceConfig{
		SitesAvailable: defaultSitesAvailable,
		SitesEnabled:   defaultSitesEnabled,
		SnippetsDir:    defaultSnippetsDir,
	})
}

// NewWithConfig creates an installation-scoped nginx service. Invalid relative
// paths are retained as unavailable state instead of falling back to another
// installation's filesystem locations.
func NewWithConfig(config ServiceConfig) *Service {
	sitesAvailable := cleanAbsoluteDir(config.SitesAvailable)
	sitesEnabled := cleanAbsoluteDir(config.SitesEnabled)
	vhostsRoot := cleanAbsoluteDir(config.VhostsRoot)
	snippetsDir := cleanAbsoluteDir(config.SnippetsDir)
	return &Service{
		sitesAvailable: sitesAvailable,
		sitesEnabled:   sitesEnabled,
		snippetsDir:    snippetsDir,
		vhostsRoot:     vhostsRoot,
	}
}

func cleanAbsoluteDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Clean(path)
}

func (s *Service) requireConfigDirs() error {
	if s.sitesAvailable == "" || s.sitesEnabled == "" {
		return fmt.Errorf("%w: sites directories must be configured as absolute paths", ErrNotConfigured)
	}
	return nil
}

func (s *Service) requireSnippetsDir() error {
	if s.snippetsDir == "" {
		return fmt.Errorf("%w: snippets directory must be configured as an absolute path", ErrNotConfigured)
	}
	return nil
}

func (s *Service) requireManagedSnippets() error {
	if err := s.requireSnippetsDir(); err != nil {
		return err
	}
	for _, name := range requiredManagedSnippetNames {
		info, err := os.Stat(filepath.Join(s.snippetsDir, name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: managed snippet %s is missing from %s", ErrNotConfigured, name, s.snippetsDir)
		}
	}
	return nil
}

// ListConfigs returns all config files in sites-available with their enabled state.
func (s *Service) ListConfigs() ([]Config, error) {
	if err := s.requireConfigDirs(); err != nil {
		return nil, err
	}
	entries, err := filepath.Glob(filepath.Join(s.sitesAvailable, "*.conf"))
	if err != nil {
		return nil, fmt.Errorf("listing nginx configs: %w", err)
	}

	var configs []Config
	for _, path := range entries {
		filename := filepath.Base(path)
		cfg := Config{
			Filename:  filename,
			Domain:    domainFromFilename(filename),
			Type:      detectType(path),
			IsEnabled: s.isSymlinked(filename),
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// GetConfig reads a config file and returns its full content.
func (s *Service) GetConfig(filename string) (*Config, error) {
	if err := s.requireConfigDirs(); err != nil {
		return nil, err
	}
	if err := validateFilename(filename); err != nil {
		return nil, err
	}
	path := filepath.Join(s.sitesAvailable, filename)
	info, data, checksum, err := readConfigFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config %q not found", filename)
		}
		return nil, fmt.Errorf("reading config %q: %w", filename, err)
	}
	cfg := &Config{
		Filename:  filename,
		Domain:    domainFromFilename(filename),
		Type:      detectType(path),
		IsEnabled: s.isSymlinked(filename),
		Content:   string(data),
		Checksum:  checksum,
		Size:      info.Size(),
		Modified:  info.ModTime().UTC().Format(time.RFC3339Nano),
	}
	return cfg, nil
}

// SaveConfig performs a checksum-locked, backup-first atomic replacement and
// validates the complete Nginx configuration. Reload remains a separate action.
func (s *Service) SaveConfig(filename, content, expectedChecksum string) (SaveReceipt, error) {
	if err := s.requireConfigDirs(); err != nil {
		return SaveReceipt{}, err
	}
	if err := validateFilename(filename); err != nil {
		return SaveReceipt{}, err
	}
	if !validChecksum(expectedChecksum) {
		return SaveReceipt{}, errors.New("expected checksum must be a lowercase 64-character SHA-256")
	}
	data := []byte(content)
	if len(data) > maxConfigBytes {
		return SaveReceipt{}, ErrConfigTooLarge
	}
	if len(data) == 0 || !utf8.Valid(data) || strings.IndexByte(content, 0) >= 0 {
		return SaveReceipt{}, errors.New("nginx configuration must be non-empty NUL-free UTF-8 text of at most 2097152 bytes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.sitesAvailable, filename)
	info, current, checksum, err := readConfigFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SaveReceipt{}, fmt.Errorf("config %q not found", filename)
		}
		return SaveReceipt{}, err
	}
	if checksum != expectedChecksum {
		return SaveReceipt{}, ErrConfigChanged
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return SaveReceipt{}, errors.New("nginx configuration ownership is unavailable")
	}
	backup := path + ".hserver-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := writeNewConfigFile(backup, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return SaveReceipt{}, fmt.Errorf("create nginx configuration backup: %w", err)
	}
	_, _, latestChecksum, err := readConfigFile(path)
	if err != nil {
		return SaveReceipt{}, err
	}
	if latestChecksum != expectedChecksum {
		return SaveReceipt{}, ErrConfigChanged
	}
	if err := replaceConfigFile(path, data, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return SaveReceipt{}, fmt.Errorf("replace nginx configuration: %w", err)
	}
	removeValidation, err := s.ensureValidationLink(filename, path)
	if err != nil {
		_ = replaceConfigFile(path, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid))
		return SaveReceipt{}, err
	}
	if removeValidation != nil {
		defer removeValidation()
	}
	if result := s.Test(); !result.OK {
		if restoreErr := replaceConfigFile(path, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); restoreErr != nil {
			return SaveReceipt{}, fmt.Errorf("%w (%s) and restore failed: %v", ErrConfigInvalid, result.Output, restoreErr)
		}
		return SaveReceipt{}, fmt.Errorf("%w: %s", ErrConfigInvalid, result.Output)
	}
	digest := sha256.Sum256(data)
	return SaveReceipt{
		Message:  "nginx configuration saved and tested",
		Backup:   filepath.Base(backup),
		Checksum: hex.EncodeToString(digest[:]),
	}, nil
}

// ArchiveConfig removes one disabled config from active inventory only after
// creating a same-directory recovery copy under a checksum lock. The document
// root and every other site asset remain untouched, and reload stays explicit.
func (s *Service) ArchiveConfig(filename, expectedChecksum string) (ArchiveReceipt, error) {
	if err := s.requireConfigDirs(); err != nil {
		return ArchiveReceipt{}, err
	}
	if err := validateFilename(filename); err != nil {
		return ArchiveReceipt{}, err
	}
	if !validChecksum(expectedChecksum) {
		return ArchiveReceipt{}, errors.New("expected checksum must be a lowercase 64-character SHA-256")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	enabled, err := s.enabledLinkState(filename)
	if err != nil {
		return ArchiveReceipt{}, err
	}
	if enabled {
		return ArchiveReceipt{}, ErrConfigEnabled
	}

	path := filepath.Join(s.sitesAvailable, filename)
	info, current, checksum, err := readConfigFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ArchiveReceipt{}, fmt.Errorf("%w: %q", ErrConfigNotFound, filename)
		}
		return ArchiveReceipt{}, err
	}
	if checksum != expectedChecksum {
		return ArchiveReceipt{}, ErrConfigChanged
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ArchiveReceipt{}, errors.New("nginx configuration ownership is unavailable")
	}
	archive := path + ".hserver-archive-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := writeNewConfigFile(archive, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return ArchiveReceipt{}, fmt.Errorf("create nginx configuration archive: %w", err)
	}
	_, _, latestChecksum, err := readConfigFile(path)
	if err != nil {
		_ = os.Remove(archive)
		return ArchiveReceipt{}, err
	}
	if latestChecksum != expectedChecksum {
		_ = os.Remove(archive)
		return ArchiveReceipt{}, ErrConfigChanged
	}
	if err := os.Remove(path); err != nil {
		_ = os.Remove(archive)
		return ArchiveReceipt{}, fmt.Errorf("archive nginx configuration: %w", err)
	}
	if result := s.Test(); !result.OK {
		if restoreErr := writeNewConfigFile(path, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); restoreErr != nil {
			return ArchiveReceipt{}, fmt.Errorf("%w (%s) and restore failed: %v; recovery copy retained at %s", ErrConfigInvalid, result.Output, restoreErr, archive)
		}
		_ = os.Remove(archive)
		return ArchiveReceipt{}, fmt.Errorf("%w: %s", ErrConfigInvalid, result.Output)
	}

	return ArchiveReceipt{
		Message:  "nginx configuration archived and tested; document root was not changed",
		Archive:  filepath.Base(archive),
		Checksum: checksum,
	}, nil
}

// ListConfigArchives returns validated installation-owned recovery copies.
// It never exposes arbitrary files or absolute host paths.
func (s *Service) ListConfigArchives() ([]ConfigArchive, error) {
	if err := s.requireConfigDirs(); err != nil {
		return nil, err
	}
	entries, err := filepath.Glob(filepath.Join(s.sitesAvailable, "*.conf.hserver-archive-*"))
	if err != nil {
		return nil, fmt.Errorf("listing nginx configuration archives: %w", err)
	}
	archives := make([]ConfigArchive, 0, len(entries))
	for _, path := range entries {
		archive := filepath.Base(path)
		filename, archivedAt, err := parseArchiveName(archive)
		if err != nil {
			continue
		}
		info, _, checksum, err := readConfigFile(path)
		if err != nil {
			return nil, fmt.Errorf("read nginx configuration archive %q: %w", archive, err)
		}
		archives = append(archives, ConfigArchive{
			Archive:  archive,
			Filename: filename,
			Checksum: checksum,
			Size:     info.Size(),
			Archived: archivedAt.UTC().Format(time.RFC3339Nano),
			Modified: info.ModTime().UTC().Format(time.RFC3339Nano),
		})
	}
	sort.Slice(archives, func(i, j int) bool {
		if archives[i].Archived == archives[j].Archived {
			return archives[i].Archive < archives[j].Archive
		}
		return archives[i].Archived > archives[j].Archived
	})
	return archives, nil
}

// RestoreConfigArchive recreates a missing, disabled sites-available file from
// one exact retained archive. It never overwrites an existing configuration,
// keeps the archive, validates the complete Nginx configuration, and removes
// the restored candidate when validation fails. Reload remains explicit.
func (s *Service) RestoreConfigArchive(archive, expectedChecksum string) (RestoreArchiveReceipt, error) {
	if err := s.requireConfigDirs(); err != nil {
		return RestoreArchiveReceipt{}, err
	}
	filename, _, err := parseArchiveName(archive)
	if err != nil {
		return RestoreArchiveReceipt{}, err
	}
	if !validChecksum(expectedChecksum) {
		return RestoreArchiveReceipt{}, errors.New("expected checksum must be a lowercase 64-character SHA-256")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	archivePath := filepath.Join(s.sitesAvailable, archive)
	archiveInfo, content, checksum, err := readConfigFile(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return RestoreArchiveReceipt{}, fmt.Errorf("%w: archive %q", ErrConfigNotFound, archive)
		}
		return RestoreArchiveReceipt{}, fmt.Errorf("read nginx configuration archive %q: %w", archive, err)
	}
	if checksum != expectedChecksum {
		return RestoreArchiveReceipt{}, ErrConfigChanged
	}
	target := filepath.Join(s.sitesAvailable, filename)
	if _, err := os.Lstat(target); err == nil {
		return RestoreArchiveReceipt{}, fmt.Errorf("%w: config %q", ErrConfigExists, filename)
	} else if !os.IsNotExist(err) {
		return RestoreArchiveReceipt{}, fmt.Errorf("inspect restore target %q: %w", filename, err)
	}
	if enabled, err := s.enabledLinkState(filename); err != nil {
		return RestoreArchiveReceipt{}, err
	} else if enabled {
		return RestoreArchiveReceipt{}, ErrConfigEnabled
	}
	stat, ok := archiveInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return RestoreArchiveReceipt{}, errors.New("nginx configuration archive ownership is unavailable")
	}
	_, _, latestChecksum, err := readConfigFile(archivePath)
	if err != nil {
		return RestoreArchiveReceipt{}, err
	}
	if latestChecksum != expectedChecksum {
		return RestoreArchiveReceipt{}, ErrConfigChanged
	}
	if err := writeNewConfigFile(target, content, archiveInfo.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return RestoreArchiveReceipt{}, fmt.Errorf("%w: config %q", ErrConfigExists, filename)
		}
		return RestoreArchiveReceipt{}, fmt.Errorf("restore nginx configuration archive: %w", err)
	}
	removeCandidate := func() error {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove rejected restored configuration: %w", err)
		}
		return nil
	}
	if enabled, err := s.enabledLinkState(filename); err != nil {
		_ = removeCandidate()
		return RestoreArchiveReceipt{}, err
	} else if enabled {
		_ = removeCandidate()
		return RestoreArchiveReceipt{}, ErrConfigEnabled
	}
	removeValidation, err := s.ensureValidationLink(filename, target)
	if err != nil {
		_ = removeCandidate()
		return RestoreArchiveReceipt{}, err
	}
	if removeValidation != nil {
		defer removeValidation()
	}
	if result := s.Test(); !result.OK {
		if removeErr := removeCandidate(); removeErr != nil {
			return RestoreArchiveReceipt{}, fmt.Errorf("%w (%s) and cleanup failed: %v; archive retained as %s", ErrConfigInvalid, result.Output, removeErr, archive)
		}
		return RestoreArchiveReceipt{}, fmt.Errorf("%w: %s", ErrConfigInvalid, result.Output)
	}
	if enabled, stateErr := s.enabledLinkState(filename); stateErr != nil {
		_ = removeCandidate()
		return RestoreArchiveReceipt{}, stateErr
	} else if enabled {
		_ = removeCandidate()
		return RestoreArchiveReceipt{}, ErrConfigEnabled
	}
	return RestoreArchiveReceipt{
		Message:   "nginx configuration restored and tested; archive retained and site remains disabled",
		Archive:   archive,
		Filename:  filename,
		Checksum:  checksum,
		IsEnabled: false,
	}, nil
}

// ListConfigBackups returns validated pre-edit recovery copies without
// exposing their contents or absolute host paths.
func (s *Service) ListConfigBackups() ([]ConfigBackup, error) {
	if err := s.requireConfigDirs(); err != nil {
		return nil, err
	}
	entries, err := filepath.Glob(filepath.Join(s.sitesAvailable, "*.conf.hserver-backup-*"))
	if err != nil {
		return nil, fmt.Errorf("listing nginx configuration backups: %w", err)
	}
	backups := make([]ConfigBackup, 0, len(entries))
	for _, path := range entries {
		backup := filepath.Base(path)
		filename, createdAt, err := parseBackupName(backup)
		if err != nil {
			continue
		}
		info, _, checksum, err := readConfigFile(path)
		if err != nil {
			return nil, fmt.Errorf("read nginx configuration backup %q: %w", backup, err)
		}
		backups = append(backups, ConfigBackup{
			Backup:   backup,
			Filename: filename,
			Checksum: checksum,
			Size:     info.Size(),
			Created:  createdAt.UTC().Format(time.RFC3339Nano),
			Modified: info.ModTime().UTC().Format(time.RFC3339Nano),
		})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].Created == backups[j].Created {
			return backups[i].Backup < backups[j].Backup
		}
		return backups[i].Created > backups[j].Created
	})
	return backups, nil
}

// RestoreConfigBackup replaces one existing configuration only when both the
// selected backup and the current target still match their observed checksums.
// A fresh pre-restore recovery copy is retained, validation is mandatory, and
// the previous target is restored if Nginx rejects the candidate.
func (s *Service) RestoreConfigBackup(backup, expectedBackupChecksum, expectedCurrentChecksum string) (RestoreBackupReceipt, error) {
	if err := s.requireConfigDirs(); err != nil {
		return RestoreBackupReceipt{}, err
	}
	filename, _, err := parseBackupName(backup)
	if err != nil {
		return RestoreBackupReceipt{}, err
	}
	if !validChecksum(expectedBackupChecksum) || !validChecksum(expectedCurrentChecksum) {
		return RestoreBackupReceipt{}, errors.New("backupChecksum and currentChecksum must be lowercase 64-character SHA-256 values")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	backupPath := filepath.Join(s.sitesAvailable, backup)
	_, backupContent, backupChecksum, err := readConfigFile(backupPath)
	if err != nil {
		if os.IsNotExist(err) {
			return RestoreBackupReceipt{}, fmt.Errorf("%w: backup %q", ErrConfigNotFound, backup)
		}
		return RestoreBackupReceipt{}, fmt.Errorf("read nginx configuration backup %q: %w", backup, err)
	}
	if backupChecksum != expectedBackupChecksum {
		return RestoreBackupReceipt{}, ErrConfigChanged
	}
	target := filepath.Join(s.sitesAvailable, filename)
	targetInfo, current, currentChecksum, err := readConfigFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return RestoreBackupReceipt{}, fmt.Errorf("%w: config %q", ErrConfigNotFound, filename)
		}
		return RestoreBackupReceipt{}, fmt.Errorf("read current nginx configuration %q: %w", filename, err)
	}
	if currentChecksum != expectedCurrentChecksum {
		return RestoreBackupReceipt{}, ErrConfigChanged
	}
	enabled, err := s.enabledLinkState(filename)
	if err != nil {
		return RestoreBackupReceipt{}, err
	}
	stat, ok := targetInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return RestoreBackupReceipt{}, errors.New("nginx configuration ownership is unavailable")
	}
	recoveryPath := target + ".hserver-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := writeNewConfigFile(recoveryPath, current, targetInfo.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return RestoreBackupReceipt{}, fmt.Errorf("create pre-restore nginx configuration recovery: %w", err)
	}
	removeUnusedRecovery := func() { _ = os.Remove(recoveryPath) }
	_, _, latestBackupChecksum, err := readConfigFile(backupPath)
	if err != nil {
		removeUnusedRecovery()
		return RestoreBackupReceipt{}, err
	}
	if latestBackupChecksum != expectedBackupChecksum {
		removeUnusedRecovery()
		return RestoreBackupReceipt{}, ErrConfigChanged
	}
	_, _, latestCurrentChecksum, err := readConfigFile(target)
	if err != nil {
		removeUnusedRecovery()
		return RestoreBackupReceipt{}, err
	}
	if latestCurrentChecksum != expectedCurrentChecksum {
		removeUnusedRecovery()
		return RestoreBackupReceipt{}, ErrConfigChanged
	}
	if err := replaceConfigFile(target, backupContent, targetInfo.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		removeUnusedRecovery()
		return RestoreBackupReceipt{}, fmt.Errorf("restore nginx configuration backup: %w", err)
	}
	removeValidation, err := s.ensureValidationLink(filename, target)
	if err != nil {
		if restoreErr := replaceConfigFile(target, current, targetInfo.Mode().Perm(), int(stat.Uid), int(stat.Gid)); restoreErr != nil {
			return RestoreBackupReceipt{}, fmt.Errorf("prepare restored nginx configuration validation: %w and rollback failed: %v", err, restoreErr)
		}
		return RestoreBackupReceipt{}, err
	}
	if removeValidation != nil {
		defer removeValidation()
	}
	if result := s.Test(); !result.OK {
		if restoreErr := replaceConfigFile(target, current, targetInfo.Mode().Perm(), int(stat.Uid), int(stat.Gid)); restoreErr != nil {
			return RestoreBackupReceipt{}, fmt.Errorf("%w (%s) and rollback failed: %v; recovery retained as %s", ErrConfigInvalid, result.Output, restoreErr, filepath.Base(recoveryPath))
		}
		return RestoreBackupReceipt{}, fmt.Errorf("%w: %s", ErrConfigInvalid, result.Output)
	}
	digest := sha256.Sum256(backupContent)
	return RestoreBackupReceipt{
		Message:   "nginx configuration backup restored and tested; pre-restore recovery retained",
		Backup:    backup,
		Recovery:  filepath.Base(recoveryPath),
		Filename:  filename,
		Checksum:  hex.EncodeToString(digest[:]),
		IsEnabled: enabled,
	}, nil
}

func parseArchiveName(archive string) (string, time.Time, error) {
	return parseRecoveryName(archive, ".hserver-archive-", "archive")
}

func parseBackupName(backup string) (string, time.Time, error) {
	return parseRecoveryName(backup, ".hserver-backup-", "backup")
}

func parseRecoveryName(name, marker, kind string) (string, time.Time, error) {
	if name == "" || filepath.Base(name) != name || strings.Contains(name, "..") {
		return "", time.Time{}, fmt.Errorf("invalid nginx configuration %s %q", kind, name)
	}
	index := strings.LastIndex(name, marker)
	if index <= 0 {
		return "", time.Time{}, fmt.Errorf("invalid nginx configuration %s %q", kind, name)
	}
	filename := name[:index]
	if err := validateFilename(filename); err != nil {
		return "", time.Time{}, fmt.Errorf("invalid nginx configuration %s %q: %w", kind, name, err)
	}
	createdAt, err := time.Parse("20060102T150405.000000000Z", name[index+len(marker):])
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invalid nginx configuration %s %q", kind, name)
	}
	return filename, createdAt, nil
}

func readConfigFile(path string) (os.FileInfo, []byte, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, "", errors.New("nginx configuration must be a regular file and not a symlink")
	}
	if info.Size() > maxConfigBytes {
		return nil, nil, "", ErrConfigTooLarge
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", err
	}
	if len(data) > maxConfigBytes {
		return nil, nil, "", ErrConfigTooLarge
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return nil, nil, "", errors.New("nginx configuration must be NUL-free UTF-8 text")
	}
	digest := sha256.Sum256(data)
	return info, data, hex.EncodeToString(digest[:]), nil
}

func validChecksum(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func writeNewConfigFile(path string, content []byte, mode os.FileMode, uid, gid int) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chown(uid, gid); err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	failed = false
	return nil
}

func replaceConfigFile(path string, content []byte, mode os.FileMode, uid, gid int) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".hserver-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	failed := true
	defer func() {
		_ = temporary.Close()
		if failed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if err := temporary.Chown(uid, gid); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	failed = false
	return nil
}

func (s *Service) ensureValidationLink(filename, target string) (func(), error) {
	enabledPath := filepath.Join(s.sitesEnabled, filename)
	if _, err := os.Lstat(enabledPath); err == nil {
		return nil, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect enabled nginx configuration: %w", err)
	}
	temporary, err := os.CreateTemp(s.sitesEnabled, ".hserver-validation-*.conf")
	if err != nil {
		return nil, fmt.Errorf("reserve nginx validation link: %w", err)
	}
	validationPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(validationPath)
		return nil, err
	}
	if err := os.Remove(validationPath); err != nil {
		return nil, err
	}
	if err := os.Symlink(target, validationPath); err != nil {
		return nil, fmt.Errorf("create nginx validation link: %w", err)
	}
	return func() { _ = os.Remove(validationPath) }, nil
}

// CreateConfig generates a vhost config from a template and writes it.
func (s *Service) CreateConfig(req CreateRequest) (*Config, error) {
	if err := s.requireConfigDirs(); err != nil {
		return nil, err
	}
	if req.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if err := validateDomain(req.Domain); err != nil {
		return nil, err
	}
	if err := validateDocRoot(req.DocRoot); err != nil {
		return nil, err
	}
	if err := validateProxyPass(req.ProxyPass); err != nil {
		return nil, err
	}
	if err := validateRedirectTo(req.RedirectTo); err != nil {
		return nil, err
	}
	if err := validateCreateFields(req); err != nil {
		return nil, err
	}

	if req.DocRoot == "" && (req.Type == "php" || req.Type == "static") {
		if s.vhostsRoot == "" {
			return nil, fmt.Errorf("%w: vhost root must be configured as an absolute path", ErrNotConfigured)
		}
		req.DocRoot = filepath.Join(s.vhostsRoot, req.Domain, "httpdocs")
	}
	if err := s.requireManagedSnippets(); err != nil {
		return nil, err
	}

	filename := req.Domain + ".conf"
	path := filepath.Join(s.sitesAvailable, filename)
	content, err := buildTemplate(req, s.snippetsDir)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("%w: config for %q", ErrConfigExists, req.Domain)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect target config: %w", err)
	}
	directoryInfo, err := os.Stat(s.sitesAvailable)
	if err != nil {
		return nil, fmt.Errorf("inspect sites-available ownership: %w", err)
	}
	directoryStat, ok := directoryInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("sites-available ownership is unavailable")
	}
	if err := writeNewConfigFile(path, []byte(content), 0o644, int(directoryStat.Uid), int(directoryStat.Gid)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: config for %q", ErrConfigExists, req.Domain)
		}
		return nil, fmt.Errorf("create nginx configuration: %w", err)
	}
	removeValidation, err := s.ensureValidationLink(filename, path)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if removeValidation != nil {
		defer removeValidation()
	}
	if result := s.Test(); !result.OK {
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, fmt.Errorf("%w (%s) and cleanup failed: %v", ErrConfigInvalid, result.Output, removeErr)
		}
		return nil, fmt.Errorf("%w: %s", ErrConfigInvalid, result.Output)
	}

	return s.GetConfig(filename)
}

// SetEnabled applies an explicit desired state without treating a retry as the
// opposite operation. It only manages an exact symlink to the selected regular
// sites-available file and refuses to replace or remove foreign entries.
func (s *Service) SetEnabled(filename string, desired bool) (bool, error) {
	if err := s.requireConfigDirs(); err != nil {
		return false, err
	}
	if err := validateFilename(filename); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	srcPath := filepath.Join(s.sitesAvailable, filename)
	sourceInfo, err := os.Lstat(srcPath)
	if os.IsNotExist(err) {
		return false, fmt.Errorf("%w: %q", ErrConfigNotFound, filename)
	}
	if err != nil {
		return false, fmt.Errorf("inspect config %q: %w", filename, err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return false, errors.New("nginx configuration must be a regular file and not a symlink")
	}

	linkPath := filepath.Join(s.sitesEnabled, filename)
	enabled, err := s.enabledLinkState(filename)
	if err != nil {
		return false, err
	}
	if enabled == desired {
		return desired, nil
	}
	if desired {
		if err := os.Symlink(srcPath, linkPath); err != nil {
			return false, fmt.Errorf("enabling site %q: %w", filename, err)
		}
		return true, nil
	}
	if err := os.Remove(linkPath); err != nil {
		return false, fmt.Errorf("disabling site %q: %w", filename, err)
	}
	return false, nil
}

// Test runs `nginx -t` and returns the result.
func (s *Service) Test() TestResult {
	out, err := exec.Command("nginx", "-t").CombinedOutput()
	return TestResult{
		OK:     err == nil,
		Output: strings.TrimSpace(string(out)),
	}
}

// ProbeReadiness performs one fresh, read-only Nginx readiness observation.
// It is intentionally separate from Status: Status is the legacy detailed
// endpoint and includes command output, while this probe returns only the
// canonical integration state and an internal error for the aggregate layer.
func (s *Service) ProbeReadiness() (integrationstate.State, error) {
	return s.ProbeReadinessContext(context.Background())
}

// ProbeReadinessContext verifies the installation boundary, active systemd
// service, and Nginx configuration syntax without mutating or reloading
// anything. Every command is attached to the caller's context and has a
// five-second local deadline so a direct caller cannot leave a subprocess
// behind. The integration-status aggregate adds its own five-second probe
// deadline and eight-second aggregate deadline.
func (s *Service) ProbeReadinessContext(parent context.Context) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, readinessTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if s == nil {
		return integrationstate.NotConfigured, ErrNotConfigured
	}

	if err := s.readinessRoots(); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errors.New("nginx readiness paths are unavailable")
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	nginxBin, err := exec.LookPath("nginx")
	if err != nil {
		// A missing executable is an absent optional integration. Do not expose
		// exec.LookPath's PATH or filesystem details to callers.
		if errors.Is(err, exec.ErrNotFound) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errors.New("nginx executable is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	serviceState, err := nginxSystemdState(ctx)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errors.New("nginx service status is unavailable")
	}
	if serviceState != "active" {
		return integrationstate.Unavailable, errors.New("nginx service is not active")
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	if err := nginxConfigTest(ctx, nginxBin); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errors.New("nginx configuration test failed")
	}
	return integrationstate.Healthy, nil
}

// readinessRoots checks only the installation-owned roots that this service
// uses for Nginx configuration inventory and managed includes. VhostsRoot is
// deliberately optional here: it is required only for generated document
// roots, not for an already-installed Nginx service to be ready.
func (s *Service) readinessRoots() error {
	for _, path := range []string{s.sitesAvailable, s.sitesEnabled, s.snippetsDir} {
		if err := readinessDirectory(path); err != nil {
			return err
		}
	}
	if s.vhostsRoot != "" {
		if err := readinessDirectory(s.vhostsRoot); err != nil {
			return err
		}
	}
	return nil
}

func readinessDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return ErrNotConfigured
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotConfigured
		}
		return err
	}
	if !info.IsDir() {
		return ErrNotConfigured
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return directory.Close()
}

// boundedOutput retains only the small status token needed from systemctl;
// command output is never returned as part of a probe error or wire result.
type boundedOutput struct {
	data []byte
}

func (o *boundedOutput) Write(data []byte) (int, error) {
	const maxBytes = 256
	originalLen := len(data)
	if len(o.data) < maxBytes {
		remaining := maxBytes - len(o.data)
		if len(data) > remaining {
			data = data[:remaining]
		}
		o.data = append(o.data, data...)
	}
	return originalLen, nil
}

func (o *boundedOutput) String() string {
	return string(o.data)
}

func nginxSystemdState(ctx context.Context) (string, error) {
	var output boundedOutput
	cmd := readinessCommandContext(ctx, "systemctl", "is-active", "nginx")
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(output.String()), nil
}

func nginxConfigTest(ctx context.Context, nginxBin string) error {
	cmd := readinessCommandContext(ctx, nginxBin, "-t")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// readinessCommandContext runs a command in its own process group. The
// standard CommandContext cancellation stops the direct process; cancelling
// the group as well prevents a shell-based implementation (or a child helper)
// from keeping pipes open after the bounded observation has ended.
func readinessCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	return cmd
}

// Status observes the installed binary, systemd unit, version, and config
// syntax independently so callers can offer precise recovery guidance.
func (s *Service) Status() Status {
	status := Status{
		Status: "not-installed",
		ConfigTest: TestResult{
			OK:     false,
			Output: "nginx executable not found",
		},
	}

	nginxBin, err := exec.LookPath("nginx")
	if err != nil {
		return status
	}

	status.Installed = true
	status.Status = "unknown"

	versionOut, _ := exec.Command(nginxBin, "-v").CombinedOutput()
	version := strings.TrimSpace(string(versionOut))
	if idx := strings.Index(version, "nginx/"); idx != -1 {
		version = version[idx:]
	}
	status.Version = version

	activeOut, _ := exec.Command("systemctl", "is-active", "nginx").CombinedOutput()
	state := strings.TrimSpace(string(activeOut))
	if isSystemdState(state) {
		status.Status = state
		status.StatusAvailable = true
	}

	if status.Status == "active" {
		uptimeOut, _ := exec.Command("systemctl", "show", "nginx", "--property=ActiveEnterTimestamp").CombinedOutput()
		status.Uptime = strings.TrimPrefix(strings.TrimSpace(string(uptimeOut)), "ActiveEnterTimestamp=")
	}

	status.ConfigTest = s.Test()
	return status
}

func isSystemdState(state string) bool {
	switch state {
	case "active", "inactive", "failed", "activating", "deactivating", "reloading", "maintenance", "unknown":
		return true
	default:
		return false
	}
}

// Reload runs `systemctl reload nginx`.
func (s *Service) Reload() error {
	out, err := exec.Command("systemctl", "reload", "nginx").CombinedOutput()
	if err != nil {
		return fmt.Errorf("reloading nginx: %w — %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ListSnippets returns read-only include files inside /etc/nginx/snippets/.
func (s *Service) ListSnippets() ([]Snippet, error) {
	if s.snippetsDir == "" {
		return nil, fmt.Errorf("nginx snippets directory is unavailable because sites-available is not configured")
	}
	return listSnippets(s.snippetsDir)
}

func listSnippets(dir string) ([]Snippet, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.conf"))
	if err != nil {
		return nil, fmt.Errorf("listing snippets: %w", err)
	}
	snippets := make([]Snippet, 0, len(entries))
	for _, path := range entries {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading snippet %q: %w", filepath.Base(path), err)
		}
		snippets = append(snippets, Snippet{
			Name:    filepath.Base(path),
			Path:    path,
			Content: string(content),
		})
	}
	return snippets, nil
}

// ---------- helpers ----------

func validateFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("filename cannot be empty")
	}
	if strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		return fmt.Errorf("invalid filename %q: path traversal not allowed", filename)
	}
	if !strings.HasSuffix(filename, ".conf") {
		return fmt.Errorf("filename must end with .conf")
	}
	return nil
}

func validateDomain(domain string) error {
	if strings.ContainsAny(domain, " /\\..") {
		// Allow dots in domain but not ".."
		if strings.Contains(domain, "..") {
			return fmt.Errorf("invalid domain %q", domain)
		}
	}
	for _, c := range domain {
		valid := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '*'
		if !valid {
			return fmt.Errorf("invalid character %q in domain %q", c, domain)
		}
	}
	return nil
}

// validateDocRoot ensures a document root is an absolute path without
// shell metacharacters or path-traversal sequences.
func validateDocRoot(docRoot string) error {
	return validateAbsoluteConfigPath("docRoot", docRoot)
}

func validateAbsoluteConfigPath(field, value string) error {
	if value == "" {
		return nil // empty means "use default", which is safe
	}
	if !strings.HasPrefix(value, "/") {
		return fmt.Errorf("%s must be an absolute path: %q", field, value)
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("%s must not contain '..': %q", field, value)
	}
	if strings.ContainsAny(value, ";|&`$(){}\\'\"\n\r\t") {
		return fmt.Errorf("%s contains disallowed characters: %q", field, value)
	}
	return nil
}

func validateCreateFields(req CreateRequest) error {
	if req.CertPath != "" || req.KeyPath != "" {
		if !req.UseSSL {
			return errors.New("certPath and keyPath require useSSL")
		}
		if req.CertPath == "" || req.KeyPath == "" {
			return errors.New("certPath and keyPath must be supplied together")
		}
	}
	if err := validateAbsoluteConfigPath("certPath", req.CertPath); err != nil {
		return err
	}
	if err := validateAbsoluteConfigPath("keyPath", req.KeyPath); err != nil {
		return err
	}
	switch req.Type {
	case "php":
		if req.ProxyPass != "" || req.RedirectTo != "" {
			return errors.New("php type does not accept proxyPass or redirectTo")
		}
		if req.PHPVersion != "" && !validPHPVersion(req.PHPVersion) {
			return errors.New("phpVersion must use the MAJOR.MINOR numeric form")
		}
		if req.PHPPool != "" && !validPHPPool(req.PHPPool) {
			return errors.New("phpPool must contain only letters, numbers, dots, underscores, or hyphens")
		}
	case "static":
		if req.PHPVersion != "" || req.PHPPool != "" || req.ProxyPass != "" || req.RedirectTo != "" {
			return errors.New("static type does not accept PHP, proxy, or redirect fields")
		}
	case "proxy":
		if req.ProxyPass == "" {
			return errors.New("proxyPass is required for proxy type")
		}
		if req.DocRoot != "" || req.PHPVersion != "" || req.PHPPool != "" || req.RedirectTo != "" {
			return errors.New("proxy type does not accept document-root, PHP, or redirect fields")
		}
	case "redirect":
		if req.RedirectTo == "" {
			return errors.New("redirectTo is required for redirect type")
		}
		if req.DocRoot != "" || req.PHPVersion != "" || req.PHPPool != "" || req.ProxyPass != "" {
			return errors.New("redirect type does not accept document-root, PHP, or proxy fields")
		}
	default:
		return fmt.Errorf("unknown vhost type %q: expected php|proxy|static|redirect", req.Type)
	}
	return nil
}

func validPHPVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 2 {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func validPHPPool(value string) bool {
	if len(value) == 0 || len(value) > 128 || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

// validateProxyPass ensures a proxy_pass target is a safe http/https/unix URL
// with no shell metacharacters.
func validateProxyPass(target string) error {
	if target == "" {
		return nil
	}
	if !strings.HasPrefix(target, "http://") &&
		!strings.HasPrefix(target, "https://") &&
		!strings.HasPrefix(target, "unix:") {
		return fmt.Errorf("proxyPass must start with http://, https://, or unix:: %q", target)
	}
	if strings.ContainsAny(target, ";|&`$(){}\\'\"\n\r\t") {
		return fmt.Errorf("proxyPass contains disallowed characters: %q", target)
	}
	return nil
}

// validateRedirectTo ensures a redirect target is a safe http/https URL.
func validateRedirectTo(target string) error {
	if target == "" {
		return nil
	}
	// Allow bare domain/path (https:// prefix is added by template if missing).
	if strings.ContainsAny(target, ";|&`$(){}\\'\"\n\r\t") {
		return fmt.Errorf("redirectTo contains disallowed characters: %q", target)
	}
	if strings.Contains(target, "..") {
		return fmt.Errorf("redirectTo must not contain '..': %q", target)
	}
	return nil
}

func (s *Service) isSymlinked(filename string) bool {
	enabled, err := s.enabledLinkState(filename)
	return err == nil && enabled
}

func (s *Service) enabledLinkState(filename string) (bool, error) {
	linkPath := filepath.Join(s.sitesEnabled, filename)
	linkInfo, err := os.Lstat(linkPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect enabled site %q: %w", filename, err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		return false, fmt.Errorf("enabled-site entry %q is not a symlink", filename)
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		return false, fmt.Errorf("read enabled-site link %q: %w", filename, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(s.sitesEnabled, target)
	}
	if filepath.Clean(target) != filepath.Clean(filepath.Join(s.sitesAvailable, filename)) {
		return false, fmt.Errorf("enabled-site link %q targets a foreign configuration", filename)
	}
	return true, nil
}

func domainFromFilename(filename string) string {
	return strings.TrimSuffix(filename, ".conf")
}

// detectType makes a best-effort guess at the vhost type by scanning the file.
func detectType(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "unknown"
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "proxy_pass"):
			return "proxy"
		case strings.Contains(line, "fastcgi_pass") || strings.Contains(line, "php-fpm"):
			return "php"
		case strings.HasPrefix(line, "return 301") || strings.HasPrefix(line, "return 302"):
			return "redirect"
		}
	}
	return "static"
}
