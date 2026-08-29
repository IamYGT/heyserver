package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxAgentFileEntries = 512

type managedFileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	ModifiedAt string `json:"modified_at"`
}

type fileController struct {
	readRoots  []string
	writeRoots []string
	mu         *sync.Mutex
}

func newFileController(readRoots, writeRoots []string) fileController {
	return fileController{readRoots: append([]string(nil), readRoots...), writeRoots: append([]string(nil), writeRoots...), mu: &sync.Mutex{}}
}

func (c fileController) Browse(_ context.Context, candidate string) ([]managedFileEntry, error) {
	resolved, err := resolveManagedPath(candidate, c.readRoots)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil, errors.New("managed path is not a directory")
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, fmt.Errorf("read managed directory: %w", err)
	}
	if len(entries) > maxAgentFileEntries {
		return nil, errors.New("managed directory exceeds the entry limit")
	}
	result := make([]managedFileEntry, 0, len(entries))
	for _, entry := range entries {
		entryInfo, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		kind := "file"
		if entry.Type()&os.ModeSymlink != 0 {
			kind = "symlink"
		} else if entry.IsDir() {
			kind = "directory"
		} else if !entryInfo.Mode().IsRegular() {
			kind = "other"
		}
		result = append(result, managedFileEntry{Name: entry.Name(), Path: filepath.Join(resolved, entry.Name()), Type: kind, Size: entryInfo.Size(), Mode: entryInfo.Mode().String(), ModifiedAt: entryInfo.ModTime().UTC().Format(time.RFC3339Nano)})
	}
	sort.Slice(result, func(i, j int) bool {
		if (result[i].Type == "directory") != (result[j].Type == "directory") {
			return result[i].Type == "directory"
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func (c fileController) Read(_ context.Context, candidate string) (managedFileContent, error) {
	resolved, err := resolveManagedPath(candidate, c.readRoots)
	if err != nil {
		return managedFileContent{}, err
	}
	info, content, checksum, err := readManagedFile(resolved)
	if err != nil {
		return managedFileContent{}, err
	}
	if !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 {
		return managedFileContent{}, errors.New("managed file must be UTF-8 text")
	}
	return managedFileContent{Path: resolved, Content: string(content), Checksum: checksum, Size: info.Size(), Mode: info.Mode().String(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano)}, nil
}

func (c fileController) Write(_ context.Context, candidate string, content []byte, expectedChecksum string) (string, error) {
	if len(c.writeRoots) == 0 {
		return "", errors.New("file writing is not enabled locally")
	}
	if !agentSHA256Pattern.MatchString(expectedChecksum) || len(content) > maxAgentNginxConfigBytes || !validManagedTextContent(content) {
		return "", errors.New("invalid managed file write request")
	}
	resolved, err := resolveManagedPath(candidate, c.writeRoots)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	info, current, checksum, err := readManagedFile(resolved)
	if err != nil {
		return "", err
	}
	if checksum != expectedChecksum {
		return "", errNginxConfigChanged
	}
	stat, ok := fileOwnership(info)
	if !ok {
		return "", errors.New("managed file ownership is unavailable")
	}
	backup := resolved + ".hserver-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := writeNewManagedFile(backup, current, info.Mode().Perm(), stat.uid, stat.gid); err != nil {
		return "", fmt.Errorf("create managed file backup: %w", err)
	}
	_, _, latestChecksum, err := readManagedFile(resolved)
	if err != nil {
		return "", err
	}
	if latestChecksum != expectedChecksum {
		return "", errNginxConfigChanged
	}
	if err := replaceManagedFile(resolved, content, info.Mode().Perm(), stat.uid, stat.gid); err != nil {
		return "", fmt.Errorf("replace managed file: %w", err)
	}
	return backup, nil
}

type managedOwnership struct{ uid, gid int }

func fileOwnership(info os.FileInfo) (managedOwnership, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return managedOwnership{}, false
	}
	return managedOwnership{uid: int(stat.Uid), gid: int(stat.Gid)}, true
}

func resolveManagedPath(candidate string, roots []string) (string, error) {
	if len(roots) == 0 || !filepath.IsAbs(candidate) || filepath.Clean(candidate) != candidate || candidate == string(filepath.Separator) {
		return "", errors.New("managed path is outside the local roots")
	}
	for _, root := range roots {
		if candidate != root && !strings.HasPrefix(candidate, root+string(filepath.Separator)) {
			continue
		}
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		resolved, pathErr := filepath.EvalSymlinks(candidate)
		if rootErr != nil || pathErr != nil {
			continue
		}
		if resolved == resolvedRoot || strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", errors.New("managed path is outside the local roots")
}
