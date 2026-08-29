package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxAgentNginxConfigBytes = 2 << 20

var (
	agentNginxConfigNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`)
	agentSHA256Pattern          = regexp.MustCompile(`^[a-f0-9]{64}$`)
	errNginxConfigChanged       = errors.New("Nginx configuration changed on the server")
	errNginxConfigInvalid       = errors.New("Nginx configuration test failed")
	errNginxConfigTooLarge      = errors.New("Nginx configuration exceeds the size limit")
	errManagedFileTooLarge      = errors.New("managed configuration file exceeds the size limit")
)

type nginxConfig struct {
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
	Content    string `json:"content,omitempty"`
	Checksum   string `json:"checksum,omitempty"`
}

type nginxController struct {
	runner         commandRunner
	allowedActions map[string]struct{}
	allowRead      bool
	allowWrite     bool
	availableDir   string
	enabledDir     string
	mu             *sync.Mutex
}

func newNginxController(runner commandRunner, allowedActions map[string]struct{}, allowRead, allowWrite bool, availableDir, enabledDir string) nginxController {
	return nginxController{
		runner: runner, allowedActions: allowedActions, allowRead: allowRead, allowWrite: allowWrite,
		availableDir: availableDir, enabledDir: enabledDir, mu: &sync.Mutex{},
	}
}

func (c nginxController) Action(ctx context.Context, action string) (string, error) {
	if _, allowed := c.allowedActions[action]; !allowed {
		return "", errors.New("Nginx action is not in the local allowlist")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.testLocked(ctx); err != nil {
		return "", err
	}
	if action == "test" {
		return "Nginx configuration is valid", nil
	}
	if err := c.reloadLocked(ctx); err != nil {
		return "", err
	}
	return "Nginx configuration tested and reloaded", nil
}

func (c nginxController) List() ([]nginxConfig, error) {
	if !c.allowRead {
		return nil, errors.New("Nginx configuration reading is not enabled locally")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.availableDir)
	if err != nil {
		return nil, fmt.Errorf("read Nginx configuration directory: %w", err)
	}
	configs := make([]nginxConfig, 0, len(entries))
	for _, entry := range entries {
		if !agentNginxConfigNamePattern.MatchString(entry.Name()) || strings.Contains(entry.Name(), ".hserver-backup-") {
			continue
		}
		info, statErr := regularManagedFile(filepath.Join(c.availableDir, entry.Name()))
		if statErr != nil {
			continue
		}
		configs = append(configs, nginxConfig{
			Name: entry.Name(), Enabled: c.enabled(entry.Name()), Size: info.Size(),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
		})
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].Name < configs[j].Name })
	return configs, nil
}

func (c nginxController) Read(name string) (nginxConfig, error) {
	if !c.allowRead {
		return nginxConfig{}, errors.New("Nginx configuration reading is not enabled locally")
	}
	if !agentNginxConfigNamePattern.MatchString(name) {
		return nginxConfig{}, errors.New("invalid Nginx configuration name")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	path := filepath.Join(c.availableDir, name)
	info, content, checksum, err := readManagedFile(path)
	if err != nil {
		return nginxConfig{}, err
	}
	return nginxConfig{
		Name: name, Enabled: c.enabled(name), Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
		Content: string(content), Checksum: checksum,
	}, nil
}

func (c nginxController) Write(ctx context.Context, name string, content []byte, expectedChecksum string, reload bool) (string, error) {
	if !c.allowWrite {
		return "", errors.New("Nginx configuration writing is not enabled locally")
	}
	if !agentNginxConfigNamePattern.MatchString(name) || !agentSHA256Pattern.MatchString(expectedChecksum) {
		return "", errors.New("invalid Nginx configuration write request")
	}
	if len(content) > maxAgentNginxConfigBytes {
		return "", errNginxConfigTooLarge
	}
	if !validManagedTextContent(content) {
		return "", errors.New("Nginx configuration must be valid UTF-8 text")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	target := filepath.Join(c.availableDir, name)
	info, current, checksum, err := readManagedFile(target)
	if err != nil {
		return "", err
	}
	if checksum != expectedChecksum {
		return "", errNginxConfigChanged
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("Nginx configuration ownership is unavailable")
	}
	backup := target + ".hserver-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := writeNewManagedFile(backup, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return "", fmt.Errorf("create Nginx configuration backup: %w", err)
	}
	_, _, latestChecksum, err := readManagedFile(target)
	if err != nil {
		return "", err
	}
	if latestChecksum != expectedChecksum {
		return "", errNginxConfigChanged
	}
	if err := replaceManagedFile(target, content, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return "", fmt.Errorf("replace Nginx configuration: %w", err)
	}
	removeValidation, err := c.ensureValidationLink(name, target)
	if err != nil {
		_ = replaceManagedFile(target, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid))
		return "", err
	}
	if removeValidation != nil {
		defer removeValidation()
	}
	if err := c.testLocked(ctx); err != nil {
		if restoreErr := replaceManagedFile(target, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); restoreErr != nil {
			return "", fmt.Errorf("%w and restore failed: %v", errNginxConfigInvalid, restoreErr)
		}
		return "", errNginxConfigInvalid
	}
	if reload {
		if err := c.reloadLocked(ctx); err != nil {
			return "", err
		}
	}
	return backup, nil
}

func (c nginxController) testLocked(ctx context.Context) error {
	commandCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if _, err := c.runner.run(commandCtx, "nginx", "-t"); err != nil {
		return fmt.Errorf("%w: %v", errNginxConfigInvalid, err)
	}
	return nil
}

func (c nginxController) reloadLocked(ctx context.Context) error {
	commandCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if _, err := c.runner.run(commandCtx, "systemctl", "reload", "nginx.service"); err != nil {
		return fmt.Errorf("Nginx reload failed: %w", err)
	}
	return nil
}

func (c nginxController) enabled(name string) bool {
	_, err := os.Lstat(filepath.Join(c.enabledDir, name))
	return err == nil
}

func (c nginxController) ensureValidationLink(name, target string) (func(), error) {
	enabledPath := filepath.Join(c.enabledDir, name)
	if _, err := os.Lstat(enabledPath); err == nil {
		return nil, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect enabled Nginx configuration: %w", err)
	}
	if err := os.MkdirAll(c.enabledDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare enabled Nginx directory: %w", err)
	}
	temporary, err := os.CreateTemp(c.enabledDir, ".hserver-validation-*.conf")
	if err != nil {
		return nil, fmt.Errorf("reserve Nginx validation link: %w", err)
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
		return nil, fmt.Errorf("create Nginx validation link: %w", err)
	}
	return func() { _ = os.Remove(validationPath) }, nil
}

func regularManagedFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("managed configuration is not a regular file")
	}
	return info, nil
}

func readManagedFile(path string) (os.FileInfo, []byte, string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, "", err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, nil, "", errors.New("open managed configuration")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, "", errors.New("managed configuration is not a regular file")
	}
	if info.Size() < 0 || info.Size() > maxAgentNginxConfigBytes {
		return nil, nil, "", errManagedFileTooLarge
	}
	content, err := io.ReadAll(io.LimitReader(file, maxAgentNginxConfigBytes+1))
	if err != nil {
		return nil, nil, "", err
	}
	if len(content) > maxAgentNginxConfigBytes {
		return nil, nil, "", errManagedFileTooLarge
	}
	digest := sha256.Sum256(content)
	return info, content, hex.EncodeToString(digest[:]), nil
}

func writeNewManagedFile(path string, content []byte, mode os.FileMode, uid, gid int) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
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
	cleanup = false
	return nil
}

func replaceManagedFile(target string, content []byte, mode os.FileMode, uid, gid int) error {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".hserver-nginx-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
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
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func validManagedTextContent(content []byte) bool {
	if !utf8.Valid(content) {
		return false
	}
	for _, value := range content {
		if value < 0x20 && value != '\n' && value != '\r' && value != '\t' {
			return false
		}
	}
	return true
}
