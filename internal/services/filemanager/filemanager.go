// Package filemanager provides secure file system operations for the hserver panel.
// All operations are restricted to explicitly configured allowed paths.
// Path traversal attacks are prevented using filepath.Clean + prefix validation
// combined with symlink resolution via filepath.EvalSymlinks.
package filemanager

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// MaxReadSize is the maximum file size allowed for read operations (5 MB).
const MaxReadSize = 5 * 1024 * 1024

// sensitivePatterns contains path segments that must never be accessed.
var sensitivePatterns = []string{
	"/.ssh/",
	"/.ssh",
	"/shadow",
	"/passwd",
	"/.gnupg/",
	"/private/",
	"id_rsa",
	"id_ed25519",
	"id_ecdsa",
	".pem",
	".key",
	".p12",
	".pfx",
}

// FileEntry represents a single item in a directory listing.
type FileEntry struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Type        string    `json:"type"` // "file", "directory", "symlink"
	Size        int64     `json:"size"`
	Permissions string    `json:"permissions"`
	Owner       string    `json:"owner"`
	Group       string    `json:"group"`
	Modified    time.Time `json:"modified"`
	Target      string    `json:"target,omitempty"` // symlink destination
}

// Service is a file manager restricted to installation-owned roots.
// All exported methods enforce path security before performing I/O.
type Service struct {
	allowedPrefixes []string
}

// New returns a fail-closed Service instance with no allowed roots.
//
// Call NewWithAllowedRoots to grant access to explicitly configured
// installation-owned directories.
func New() *Service {
	return NewWithAllowedRoots(nil)
}

// NewWithAllowedRoots returns a Service restricted to the supplied absolute
// directories. Empty or relative roots are ignored. An empty or invalid root
// list therefore produces a fail-closed service.
func NewWithAllowedRoots(roots []string) *Service {
	prefixes := make([]string, 0, len(roots))
	for _, root := range roots {
		cleaned := filepath.Clean(strings.TrimSpace(root))
		if cleaned == "." || !filepath.IsAbs(cleaned) {
			continue
		}
		prefixes = append(prefixes, cleaned+string(filepath.Separator))
	}
	return &Service{allowedPrefixes: prefixes}
}

// ─── Security helpers ────────────────────────────────────────────────────────

// validatePath ensures that path is:
//  1. Cleaned (no ..)
//  2. Within an allowed prefix
//  3. Not targeting a sensitive file/pattern
//  4. Symlinks resolved and still within an allowed prefix
func (s *Service) validatePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("path must not be empty")
	}

	// Canonicalise without following symlinks first.
	cleaned := filepath.Clean(path)

	// Check against allowed prefixes before following symlinks.
	if !s.isAllowed(cleaned) {
		return "", fmt.Errorf("path %q is not within an allowed directory", cleaned)
	}

	// Check for sensitive patterns on the cleaned path.
	if isSensitive(cleaned) {
		return "", fmt.Errorf("access to %q is not permitted", cleaned)
	}

	// Resolve symlinks to catch symlink-escape attacks.
	// If the path does not exist yet (create operations) EvalSymlinks will fail;
	// in that case validate the parent directory instead.
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Validate parent must exist and be within allowed scope.
			parent := filepath.Dir(cleaned)
			resolvedParent, err2 := filepath.EvalSymlinks(parent)
			if err2 != nil {
				return "", fmt.Errorf("parent directory %q does not exist", parent)
			}
			if !s.isAllowed(resolvedParent) {
				return "", fmt.Errorf("parent path %q is not within an allowed directory", resolvedParent)
			}
			// Return the non-resolved path for new-file creation.
			return cleaned, nil
		}
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}

	// Re-check after symlink resolution.
	if !s.isAllowed(resolved) {
		return "", fmt.Errorf("resolved path %q is not within an allowed directory", resolved)
	}
	if isSensitive(resolved) {
		return "", fmt.Errorf("access to resolved path %q is not permitted", resolved)
	}

	return resolved, nil
}

// isAllowed returns true when p starts with one of the allowed prefixes.
func (s *Service) isAllowed(p string) bool {
	for _, prefix := range s.allowedPrefixes {
		if strings.HasPrefix(p, prefix) || p == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

// isSensitive returns true when p contains a sensitive pattern.
func isSensitive(p string) bool {
	lower := strings.ToLower(p)
	for _, pattern := range sensitivePatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// ─── Directory listing ────────────────────────────────────────────────────────

// List returns the contents of directory at path with full metadata.
func (s *Service) List(path string) ([]FileEntry, error) {
	safePath, err := s.validatePath(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(safePath)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", safePath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", safePath)
	}

	entries, err := os.ReadDir(safePath)
	if err != nil {
		return nil, fmt.Errorf("readdir %q: %w", safePath, err)
	}

	result := make([]FileEntry, 0, len(entries))
	for _, de := range entries {
		entryPath := filepath.Join(safePath, de.Name())
		fe, err := buildEntry(entryPath, de.Name())
		if err != nil {
			// Skip entries we can't stat (permission denied, broken symlinks, etc.)
			continue
		}
		result = append(result, fe)
	}
	return result, nil
}

// buildEntry constructs a FileEntry from a path and a display name.
func buildEntry(path, name string) (FileEntry, error) {
	// Use Lstat so we see symlinks rather than their targets.
	info, err := os.Lstat(path)
	if err != nil {
		return FileEntry{}, err
	}

	fe := FileEntry{
		Name:        name,
		Path:        path,
		Size:        info.Size(),
		Permissions: info.Mode().String(),
		Modified:    info.ModTime(),
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		fe.Type = "symlink"
		if target, err := os.Readlink(path); err == nil {
			fe.Target = target
		}
	case info.IsDir():
		fe.Type = "directory"
	default:
		fe.Type = "file"
	}

	// Resolve owner/group from syscall stat.
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		fe.Owner = uidToName(stat.Uid)
		fe.Group = gidToName(stat.Gid)
	}

	return fe, nil
}

func uidToName(uid uint32) string {
	u, err := user.LookupId(strconv.Itoa(int(uid)))
	if err != nil {
		return strconv.Itoa(int(uid))
	}
	return u.Username
}

func gidToName(gid uint32) string {
	g, err := user.LookupGroupId(strconv.Itoa(int(gid)))
	if err != nil {
		return strconv.Itoa(int(gid))
	}
	return g.Name
}

// ─── Read / Write ─────────────────────────────────────────────────────────────

// Read returns the text content of a file (max MaxReadSize bytes).
func (s *Service) Read(path string) (string, error) {
	safePath, err := s.validatePath(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(safePath)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", safePath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory", safePath)
	}
	if info.Size() > MaxReadSize {
		return "", fmt.Errorf("file size %d exceeds maximum read size (%d bytes)", info.Size(), MaxReadSize)
	}

	data, err := os.ReadFile(safePath)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", safePath, err)
	}
	return string(data), nil
}

// Write saves content to path, creating the file if it does not exist.
// Preserves existing permissions if the file exists; otherwise creates with 0644.
func (s *Service) Write(path, content string) error {
	safePath, err := s.validatePath(path)
	if err != nil {
		return err
	}

	// Determine permissions to use.
	perm := fs.FileMode(0644)
	if info, err2 := os.Stat(safePath); err2 == nil {
		perm = info.Mode().Perm()
	}

	return os.WriteFile(safePath, []byte(content), perm)
}

// ─── Create ───────────────────────────────────────────────────────────────────

// CreateFile creates an empty file at path.
func (s *Service) CreateFile(path string) error {
	safePath, err := s.validatePath(path)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(safePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("file %q already exists", safePath)
		}
		return fmt.Errorf("create %q: %w", safePath, err)
	}
	return f.Close()
}

// CreateDir creates a directory (and all parents) at path with 0755.
func (s *Service) CreateDir(path string) error {
	safePath, err := s.validatePath(path)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(safePath, 0755); err != nil {
		return fmt.Errorf("mkdir %q: %w", safePath, err)
	}
	return nil
}

// ─── Delete ───────────────────────────────────────────────────────────────────

// Delete removes a file or directory recursively.
func (s *Service) Delete(path string) error {
	safePath, err := s.validatePath(path)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(safePath); err != nil {
		return fmt.Errorf("delete %q: %w", safePath, err)
	}
	return nil
}

// ─── Rename / Move ────────────────────────────────────────────────────────────

// Rename moves src to dst. Both paths must be within allowed prefixes.
func (s *Service) Rename(src, dst string) error {
	safeSrc, err := s.validatePath(src)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	safeDst, err := s.validatePath(dst)
	if err != nil {
		return fmt.Errorf("destination: %w", err)
	}

	if err := os.Rename(safeSrc, safeDst); err != nil {
		return fmt.Errorf("rename %q → %q: %w", safeSrc, safeDst, err)
	}
	return nil
}

// ─── Copy ─────────────────────────────────────────────────────────────────────

// Copy copies a file from src to dst.
// If dst is an existing directory, the file is copied inside it.
func (s *Service) Copy(src, dst string) error {
	safeSrc, err := s.validatePath(src)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	safeDst, err := s.validatePath(dst)
	if err != nil {
		return fmt.Errorf("destination: %w", err)
	}

	srcInfo, err := os.Stat(safeSrc)
	if err != nil {
		return fmt.Errorf("stat source %q: %w", safeSrc, err)
	}
	if srcInfo.IsDir() {
		return errors.New("copying directories is not supported; use individual files")
	}

	// If dst is an existing directory, place file inside it.
	if dstInfo, err2 := os.Stat(safeDst); err2 == nil && dstInfo.IsDir() {
		safeDst = filepath.Join(safeDst, srcInfo.Name())
	}

	return copyFile(safeSrc, safeDst, srcInfo.Mode().Perm())
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}

	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// ─── Stat ─────────────────────────────────────────────────────────────────────

// Stat returns metadata for path.
func (s *Service) Stat(path string) (FileEntry, error) {
	safePath, err := s.validatePath(path)
	if err != nil {
		return FileEntry{}, err
	}
	return buildEntry(safePath, filepath.Base(safePath))
}

// ─── Allowed roots ────────────────────────────────────────────────────────────

// AllowedRoots returns the list of directories configured on a new service.
// New is fail-closed, so this returns an empty list; use the service method on
// a NewWithAllowedRoots instance to inspect explicit roots.
func AllowedRoots() []string {
	return New().AllowedRoots()
}

// AllowedRoots returns the configured directories the service may browse.
func (s *Service) AllowedRoots() []string {
	roots := make([]string, len(s.allowedPrefixes))
	for i, p := range s.allowedPrefixes {
		roots[i] = strings.TrimSuffix(p, "/")
	}
	return roots
}
