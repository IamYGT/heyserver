package php

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
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxPoolConfigBytes = 2 << 20

var (
	ErrPoolConfigChanged  = errors.New("PHP-FPM pool configuration changed")
	ErrPoolConfigInvalid  = errors.New("PHP-FPM configuration test failed")
	ErrPoolConfigReload   = errors.New("PHP-FPM reload failed")
	ErrPoolConfigTooLarge = errors.New("PHP-FPM pool configuration exceeds the size limit")
	poolChecksumPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// PoolConfigContent is a checksum-bound local PHP-FPM pool configuration.
type PoolConfigContent struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Checksum   string `json:"checksum"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	ModifiedAt string `json:"modified_at"`
}

// PoolConfigReplaceReceipt records the backup created by a successful local
// pool replacement and whether PHP-FPM was reloaded.
type PoolConfigReplaceReceipt struct {
	Message  string `json:"message"`
	Backup   string `json:"backup"`
	Checksum string `json:"checksum"`
	Reloaded bool   `json:"reloaded"`
}

// ReadPoolConfig returns one observed regular pool file and its checksum.
func (s *Service) ReadPoolConfig(version, pool string) (PoolConfigContent, error) {
	path, err := s.localPoolConfigPath(version, pool)
	if err != nil {
		return PoolConfigContent{}, err
	}
	s.lock()
	defer s.unlock()
	info, content, checksum, err := readLocalPoolConfig(path)
	if err != nil {
		return PoolConfigContent{}, err
	}
	return PoolConfigContent{
		Path: path, Content: string(content), Checksum: checksum, Size: info.Size(),
		Mode: fmt.Sprintf("%04o", info.Mode().Perm()), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
	}, nil
}

// ReplacePoolConfig performs a checksum-locked, backup-first atomic replace,
// validates the complete PHP-FPM configuration, restores the old content when
// validation fails, and optionally reloads the matching service.
func (s *Service) ReplacePoolConfig(ctx context.Context, version, pool string, content []byte, expectedChecksum string, reload bool) (PoolConfigReplaceReceipt, error) {
	path, err := s.localPoolConfigPath(version, pool)
	if err != nil {
		return PoolConfigReplaceReceipt{}, err
	}
	expectedChecksum = strings.TrimSpace(expectedChecksum)
	if !poolChecksumPattern.MatchString(expectedChecksum) {
		return PoolConfigReplaceReceipt{}, errors.New("expected checksum must be a lowercase 64-character SHA-256")
	}
	if len(content) > maxPoolConfigBytes {
		return PoolConfigReplaceReceipt{}, ErrPoolConfigTooLarge
	}
	if !validPoolConfigText(content) {
		return PoolConfigReplaceReceipt{}, errors.New("PHP-FPM pool configuration must be NUL-free UTF-8 text")
	}

	s.lock()
	defer s.unlock()
	info, current, checksum, err := readLocalPoolConfig(path)
	if err != nil {
		return PoolConfigReplaceReceipt{}, err
	}
	if checksum != expectedChecksum {
		return PoolConfigReplaceReceipt{}, ErrPoolConfigChanged
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return PoolConfigReplaceReceipt{}, errors.New("PHP-FPM pool ownership is unavailable")
	}
	backup := path + ".hserver-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := writeExclusivePoolConfig(backup, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return PoolConfigReplaceReceipt{}, fmt.Errorf("create PHP-FPM pool backup: %w", err)
	}
	_, _, latestChecksum, err := readLocalPoolConfig(path)
	if err != nil {
		return PoolConfigReplaceReceipt{}, err
	}
	if latestChecksum != expectedChecksum {
		return PoolConfigReplaceReceipt{}, ErrPoolConfigChanged
	}
	if err := replaceLocalPoolConfig(path, content, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return PoolConfigReplaceReceipt{}, fmt.Errorf("replace PHP-FPM pool: %w", err)
	}
	if err := s.testLocalPoolConfig(ctx, version); err != nil {
		if restoreErr := replaceLocalPoolConfig(path, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); restoreErr != nil {
			return PoolConfigReplaceReceipt{}, fmt.Errorf("%w: %v; restore failed: %v", ErrPoolConfigInvalid, err, restoreErr)
		}
		return PoolConfigReplaceReceipt{}, fmt.Errorf("%w: %v", ErrPoolConfigInvalid, err)
	}
	if reload {
		if err := s.reloadLocalPHPFPM(ctx, version); err != nil {
			if restoreErr := replaceLocalPoolConfig(path, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); restoreErr != nil {
				return PoolConfigReplaceReceipt{}, fmt.Errorf("%w: %v; restore failed: %v", ErrPoolConfigReload, err, restoreErr)
			}
			return PoolConfigReplaceReceipt{}, fmt.Errorf("%w: %v", ErrPoolConfigReload, err)
		}
	}
	digest := sha256.Sum256(content)
	message := "PHP-FPM pool saved and tested"
	if reload {
		message += " and reloaded"
	}
	return PoolConfigReplaceReceipt{
		Message: message, Backup: backup, Checksum: hex.EncodeToString(digest[:]), Reloaded: reload,
	}, nil
}

func (s *Service) localPoolConfigPath(version, pool string) (string, error) {
	if err := validateVersion(version); err != nil {
		return "", err
	}
	if err := validatePoolName(pool); err != nil || len(pool) > 128 {
		return "", errors.New("invalid PHP-FPM pool name")
	}
	root := defaultPHPConfigRoot
	if s != nil && s.configRoot != "" {
		root = s.configRoot
	}
	return filepath.Join(root, version, "fpm", "pool.d", pool+".conf"), nil
}

func (s *Service) lock() {
	if s.mu == nil {
		s.mu = &sync.Mutex{}
	}
	s.mu.Lock()
}

func (s *Service) unlock() {
	s.mu.Unlock()
}

func (s *Service) testLocalPoolConfig(ctx context.Context, version string) error {
	binaryRoot := defaultPHPBinaryRoot
	if s != nil && s.binaryRoot != "" {
		binaryRoot = s.binaryRoot
	}
	binary := filepath.Join(binaryRoot, "php-fpm"+version)
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("PHP-FPM runtime binary is unavailable")
	}
	commandCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	output, err := s.runLifecycleCommandContext(commandCtx, binary, "-t")
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s *Service) reloadLocalPHPFPM(ctx context.Context, version string) error {
	commandCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	output, err := s.runLifecycleCommandContext(commandCtx, "systemctl", "reload", "php"+version+"-fpm")
	if err != nil {
		return fmt.Errorf("reloading php%s-fpm: %w — %s", version, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func readLocalPoolConfig(path string) (os.FileInfo, []byte, string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, "", err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, nil, "", errors.New("open PHP-FPM pool configuration")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, "", errors.New("PHP-FPM pool configuration is not a regular file")
	}
	if info.Size() < 0 || info.Size() > maxPoolConfigBytes {
		return nil, nil, "", ErrPoolConfigTooLarge
	}
	content, err := io.ReadAll(io.LimitReader(file, maxPoolConfigBytes+1))
	if err != nil {
		return nil, nil, "", err
	}
	if len(content) > maxPoolConfigBytes {
		return nil, nil, "", ErrPoolConfigTooLarge
	}
	if !validPoolConfigText(content) {
		return nil, nil, "", errors.New("PHP-FPM pool configuration is not NUL-free UTF-8 text")
	}
	digest := sha256.Sum256(content)
	return info, content, hex.EncodeToString(digest[:]), nil
}

func writeExclusivePoolConfig(path string, content []byte, mode os.FileMode, uid, gid int) error {
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

func replaceLocalPoolConfig(target string, content []byte, mode os.FileMode, uid, gid int) error {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".hserver-php-*")
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

func validPoolConfigText(content []byte) bool {
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
