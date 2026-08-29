package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

const maxTUIFileLines = 10000

type tuiFileState struct {
	Roots       []string
	CurrentRoot string
	CurrentPath string
	Entries     []cliFileEntry
	Manageable  bool
}

type tuiFilesMsg struct {
	TargetID string
	State    tuiFileState
	Err      error
}

type tuiFileContentMsg struct {
	TargetID string
	Path     string
	Lines    []string
	Err      error
}

func loadTUIFilesCmd(ctx context.Context, client *apiClient, target tuiTarget, path string) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIFiles(ctx, client, target, path)
		return tuiFilesMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIFiles(ctx context.Context, client *apiClient, target tuiTarget, path string) (tuiFileState, error) {
	if !target.Local {
		if !target.Online {
			return tuiFileState{}, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityFilesRead) {
			return tuiFileState{}, errors.New("managed agent does not advertise files.read")
		}
		roots := cleanTUIFileRoots(target.Inventory.FileReadRoots)
		if len(roots) == 0 {
			return tuiFileState{}, errors.New("managed agent reports no readable file roots")
		}
		selectedPath, root, err := selectTUIFilePath(path, roots)
		if err != nil {
			return tuiFileState{}, err
		}
		endpoint := "/api/nodes/" + url.PathEscape(target.ID) + "/files?path=" + url.QueryEscape(selectedPath)
		entries, err := requestJSON[[]cliFileEntry](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return tuiFileState{}, err
		}
		sortTUIFileEntries(entries)
		return tuiFileState{Roots: roots, CurrentRoot: root, CurrentPath: selectedPath, Entries: entries}, nil
	}

	roots, err := loadLocalFileRoots(ctx, client)
	if err != nil {
		return tuiFileState{}, err
	}
	roots = cleanTUIFileRoots(roots)
	selectedPath, root, err := selectTUIFilePath(path, roots)
	if err != nil {
		return tuiFileState{}, err
	}
	response, err := loadLocalFileList(ctx, client, selectedPath)
	if err != nil {
		return tuiFileState{}, err
	}
	sortTUIFileEntries(response.Entries)
	return tuiFileState{Roots: roots, CurrentRoot: root, CurrentPath: selectedPath, Entries: response.Entries, Manageable: true}, nil
}

func loadTUIFileContentCmd(ctx context.Context, client *apiClient, target tuiTarget, path string) tea.Cmd {
	return func() tea.Msg {
		lines, err := loadTUIFileContent(ctx, client, target, path)
		return tuiFileContentMsg{TargetID: target.ID, Path: path, Lines: lines, Err: err}
	}
}

func loadTUIFileContent(ctx context.Context, client *apiClient, target tuiTarget, path string) ([]string, error) {
	if !target.Local {
		if !target.Online {
			return nil, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityFilesRead) {
			return nil, errors.New("managed agent does not advertise files.read")
		}
		if !pathWithinRoots(path, target.Inventory.FileReadRoots) {
			return nil, errors.New("managed file is outside the agent's current read roots")
		}
		endpoint := "/api/nodes/" + url.PathEscape(target.ID) + "/file?path=" + url.QueryEscape(path)
		response, err := requestJSON[cliFileContent](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, err
		}
		if response.Path != path {
			return nil, errors.New("managed agent resolved the file to a different path")
		}
		return splitTUIFileContent(response.Content)
	}

	entry, err := requireLocalFilePath(ctx, client, path, false)
	if err != nil {
		return nil, err
	}
	if entry.Type != "file" {
		return nil, errors.New("file viewer requires an observed regular file")
	}
	response, err := requestJSON[cliFileContent](ctx, client, http.MethodGet, "/api/files/read?path="+url.QueryEscape(path), nil, true)
	if err != nil {
		return nil, err
	}
	return splitTUIFileContent(response.Content)
}

func splitTUIFileContent(content string) ([]string, error) {
	if len(content) > maxCLIManagedFileBytes {
		return nil, fmt.Errorf("file exceeds the %d-byte CLI viewer limit", maxCLIManagedFileBytes)
	}
	if !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return nil, errors.New("file viewer accepts only NUL-free UTF-8 text")
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > maxTUIFileLines {
		return nil, fmt.Errorf("file exceeds the %d-line CLI viewer limit", maxTUIFileLines)
	}
	return lines, nil
}

func runTUIFileOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if !operation.Target.Local || operation.Action != "delete" {
		return "", fmt.Errorf("unsupported file TUI action %q", operation.Action)
	}
	path, err := validateCLIManagedPath(operation.File.Path)
	if err != nil {
		return "", err
	}
	entry, err := requireLocalFilePath(ctx, client, path, false)
	if err != nil {
		return "", err
	}
	if entry.Name != operation.File.Name || entry.Type != operation.File.Type {
		return "", errors.New("selected file identity changed; refresh the directory and retry")
	}
	if entry.Type == "symlink" {
		return "", errors.New("deleting symlinks is unavailable through hserverctl")
	}
	response, err := requestJSON[map[string]string](ctx, client.withTimeout(60*time.Second), http.MethodDelete, "/api/files?path="+url.QueryEscape(path), nil, true)
	if err != nil {
		return "", err
	}
	if message := strings.TrimSpace(response["message"]); message != "" {
		return message, nil
	}
	return "Deleted " + path, nil
}

func cleanTUIFileRoots(roots []string) []string {
	cleaned := make([]string, 0, len(roots))
	seen := make(map[string]bool, len(roots))
	for _, raw := range roots {
		root := filepath.Clean(strings.TrimSpace(raw))
		if !filepath.IsAbs(root) || root == string(filepath.Separator) || seen[root] {
			continue
		}
		seen[root] = true
		cleaned = append(cleaned, root)
	}
	sort.Strings(cleaned)
	return cleaned
}

func selectTUIFilePath(path string, roots []string) (string, string, error) {
	if len(roots) == 0 {
		return "", "", errors.New("file manager reports no configured roots")
	}
	if strings.TrimSpace(path) == "" {
		return roots[0], roots[0], nil
	}
	validated, err := validateCLIManagedPath(path)
	if err != nil {
		return "", "", err
	}
	root := ""
	for _, candidate := range roots {
		if (validated == candidate || strings.HasPrefix(validated, candidate+string(filepath.Separator))) && len(candidate) > len(root) {
			root = candidate
		}
	}
	if root == "" {
		return "", "", errors.New("file browser path is outside the current roots")
	}
	return validated, root, nil
}

func sortTUIFileEntries(entries []cliFileEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		leftDir, rightDir := entries[i].Type == "directory", entries[j].Type == "directory"
		if leftDir != rightDir {
			return leftDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}
