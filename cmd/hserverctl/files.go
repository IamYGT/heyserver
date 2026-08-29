package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

const maxCLIManagedFileBytes = 2 << 20

type cliFileEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	Mode        string `json:"mode,omitempty"`
	Permissions string `json:"permissions,omitempty"`
	Owner       string `json:"owner,omitempty"`
	Group       string `json:"group,omitempty"`
	Modified    string `json:"modified,omitempty"`
	ModifiedAt  string `json:"modified_at,omitempty"`
}

type localFileListResponse struct {
	Path    string         `json:"path,omitempty"`
	Roots   []string       `json:"roots,omitempty"`
	Entries []cliFileEntry `json:"entries"`
}

type cliFileContent struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Checksum   string `json:"checksum,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Mode       string `json:"mode,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

func runFiles(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl files roots|list|read|save|create|rename|delete")
	}
	switch args[0] {
	case "roots":
		return runFilesRoots(ctx, client, args[1:], out)
	case "list":
		return runFilesList(ctx, client, args[1:], out)
	case "read":
		return runFilesRead(ctx, client, args[1:], out)
	case "save":
		return runFilesSave(ctx, client, args[1:], out)
	case "create":
		return runFilesCreate(ctx, client, args[1:], out)
	case "rename":
		return runFilesRename(ctx, client, args[1:], out)
	case "delete":
		return runFilesDelete(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown files command %q", args[0])
	}
}

func runFilesRoots(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("files roots", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl files roots [--node NODE]")
	}
	selectedNode := strings.TrimSpace(*node)
	if selectedNode == "" {
		return printRequest(ctx, client, out, http.MethodGet, "/api/files", nil, true)
	}
	target, err := loadCLIManagedNode(ctx, client, selectedNode)
	if err != nil {
		return err
	}
	return printJSONValue(out, map[string]any{
		"node": selectedNode, "online": target.Online,
		"read_roots": target.Inventory.FileReadRoots, "write_roots": target.Inventory.FileWriteRoots,
	})
}

func runFilesList(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("files list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl files list [--node NODE] PATH")
	}
	path, err := validateCLIManagedPath(flags.Args()[0])
	if err != nil {
		return err
	}
	selectedNode := strings.TrimSpace(*node)
	if selectedNode == "" {
		if _, err := requireLocalFilePath(ctx, client, path, true); err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/files?path="+url.QueryEscape(path), nil, true)
	}
	if _, err := requireRemoteFilePath(ctx, client, selectedNode, path, false); err != nil {
		return err
	}
	endpoint := "/api/nodes/" + url.PathEscape(selectedNode) + "/files?path=" + url.QueryEscape(path)
	return printRequest(ctx, client.withTimeout(45*time.Second), out, http.MethodGet, endpoint, nil, true)
}

func runFilesRead(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("files read", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl files read [--node NODE] PATH")
	}
	path, err := validateCLIManagedPath(flags.Args()[0])
	if err != nil {
		return err
	}
	selectedNode := strings.TrimSpace(*node)
	if selectedNode == "" {
		entry, err := requireLocalFilePath(ctx, client, path, false)
		if err != nil {
			return err
		}
		if entry.Type != "file" {
			return errors.New("local file read requires an observed regular file")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/files/read?path="+url.QueryEscape(path), nil, true)
	}
	if _, err := requireRemoteFilePath(ctx, client, selectedNode, path, false); err != nil {
		return err
	}
	endpoint := "/api/nodes/" + url.PathEscape(selectedNode) + "/file?path=" + url.QueryEscape(path)
	return printRequest(ctx, client.withTimeout(45*time.Second), out, http.MethodGet, endpoint, nil, true)
}

func runFilesSave(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("files save", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	contentFile := flags.String("content-file", "", "regular UTF-8 file containing the replacement text")
	checksum := flags.String("checksum", "", "expected remote SHA-256 checksum")
	confirmed := flags.Bool("confirm", false, "confirm replacement of the observed text file")
	wait := flags.Duration("wait", 60*time.Second, "maximum save wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*contentFile) == "" {
		return errors.New("usage: hserverctl files save --confirm [--node NODE --checksum SHA256] --content-file PATH [--wait DURATION] TARGET")
	}
	if !*confirmed {
		return errors.New("file save requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("save wait must be greater than zero")
	}
	targetPath, err := validateCLIManagedPath(flags.Args()[0])
	if err != nil {
		return err
	}
	if dedicatedConfigFamily(targetPath) != "" {
		return fmt.Errorf("%s configuration must be saved through its dedicated HServer management surface", dedicatedConfigFamily(targetPath))
	}
	content, err := readCLIManagedTextFile(*contentFile)
	if err != nil {
		return err
	}
	selectedNode := strings.TrimSpace(*node)
	if selectedNode == "" {
		if strings.TrimSpace(*checksum) != "" {
			return errors.New("--checksum is available only for managed-node file saves")
		}
		entry, err := requireLocalFilePath(ctx, client, targetPath, false)
		if err != nil {
			return err
		}
		if entry.Type != "file" {
			return errors.New("local file save requires an observed regular file")
		}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, "/api/files/write", map[string]string{"path": targetPath, "content": content}, true)
	}

	expected, err := validateCLIFileChecksum(*checksum)
	if err != nil {
		return err
	}
	target, err := requireRemoteFilePath(ctx, client, selectedNode, targetPath, true)
	if err != nil {
		return err
	}
	endpoint := "/api/nodes/" + url.PathEscape(selectedNode) + "/file?path=" + url.QueryEscape(targetPath)
	current, err := requestJSON[cliFileContent](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	if current.Path != targetPath {
		return errors.New("managed agent resolved the file to a different path; use the reported canonical path")
	}
	if !pathWithinRoots(current.Path, target.Inventory.FileWriteRoots) {
		return errors.New("managed file is outside the agent's current write roots")
	}
	if strings.ToLower(current.Checksum) != expected {
		return errors.New("managed file checksum changed; read the current file and retry with its checksum")
	}
	endpoint = "/api/nodes/" + url.PathEscape(selectedNode) + "/file"
	payload := map[string]string{"path": targetPath, "content": content, "checksum": expected}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, endpoint, payload, true)
}

func runFilesCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("files create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	kind := flags.String("type", "file", "entry type: file or directory")
	confirmed := flags.Bool("confirm", false, "confirm creation under a configured local root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl files create --confirm [--type file|directory] TARGET")
	}
	if !*confirmed {
		return errors.New("file creation requires explicit --confirm")
	}
	entryType := strings.ToLower(strings.TrimSpace(*kind))
	if entryType != "file" && entryType != "directory" {
		return errors.New("file type must be file or directory")
	}
	targetPath, err := validateCLIManagedPath(flags.Args()[0])
	if err != nil {
		return err
	}
	if err := requireLocalDestinationAbsent(ctx, client, targetPath); err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodPost, "/api/files/create", map[string]string{"path": targetPath, "type": entryType}, true)
}

func runFilesRename(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("files rename", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm the observed local rename or move")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return errors.New("usage: hserverctl files rename --confirm SOURCE TARGET")
	}
	if !*confirmed {
		return errors.New("file rename requires explicit --confirm")
	}
	source, err := validateCLIManagedPath(flags.Args()[0])
	if err != nil {
		return err
	}
	target, err := validateCLIManagedPath(flags.Args()[1])
	if err != nil {
		return err
	}
	entry, err := requireLocalFilePath(ctx, client, source, false)
	if err != nil {
		return err
	}
	if entry.Type == "symlink" {
		return errors.New("renaming symlinks is unavailable through hserverctl")
	}
	if err := requireLocalDestinationAbsent(ctx, client, target); err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodPost, "/api/files/rename", map[string]string{"old_path": source, "new_path": target}, true)
}

func runFilesDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("files delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm recursive deletion of the observed local entry")
	wait := flags.Duration("wait", 60*time.Second, "maximum delete wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl files delete --confirm [--wait DURATION] TARGET")
	}
	if !*confirmed {
		return errors.New("file deletion requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("delete wait must be greater than zero")
	}
	targetPath, err := validateCLIManagedPath(flags.Args()[0])
	if err != nil {
		return err
	}
	entry, err := requireLocalFilePath(ctx, client, targetPath, false)
	if err != nil {
		return err
	}
	if entry.Type == "symlink" {
		return errors.New("deleting symlinks is unavailable through hserverctl")
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, "/api/files?path="+url.QueryEscape(targetPath), nil, true)
}

func loadCLIManagedNode(ctx context.Context, client *apiClient, nodeID string) (managedNodeEnvelope, error) {
	target, err := requestJSON[managedNodeEnvelope](ctx, client, http.MethodGet, "/api/nodes/"+url.PathEscape(nodeID), nil, true)
	if err != nil {
		return managedNodeEnvelope{}, err
	}
	return target, nil
}

func requireRemoteFilePath(ctx context.Context, client *apiClient, nodeID, path string, write bool) (managedNodeEnvelope, error) {
	target, err := loadCLIManagedNode(ctx, client, nodeID)
	if err != nil {
		return managedNodeEnvelope{}, err
	}
	if !target.Online {
		return managedNodeEnvelope{}, errors.New("managed node is offline")
	}
	capability := agenthub.CapabilityFilesRead
	roots := target.Inventory.FileReadRoots
	if write {
		capability = agenthub.CapabilityFilesWrite
		roots = target.Inventory.FileWriteRoots
	}
	available := false
	for _, item := range target.Capabilities {
		if item == capability {
			available = true
			break
		}
	}
	if !available {
		return managedNodeEnvelope{}, fmt.Errorf("managed agent does not advertise %s", capability)
	}
	if !pathWithinRoots(path, roots) {
		return managedNodeEnvelope{}, fmt.Errorf("managed path is outside the agent's current %s roots", map[bool]string{false: "read", true: "write"}[write])
	}
	return target, nil
}

func requireLocalFilePath(ctx context.Context, client *apiClient, path string, allowRoot bool) (cliFileEntry, error) {
	roots, err := loadLocalFileRoots(ctx, client)
	if err != nil {
		return cliFileEntry{}, err
	}
	if !pathWithinRoots(path, roots) {
		return cliFileEntry{}, errors.New("local path is outside the server's configured file roots")
	}
	if pathIsRoot(path, roots) {
		if allowRoot {
			return cliFileEntry{Path: path, Name: filepath.Base(path), Type: "directory"}, nil
		}
		return cliFileEntry{}, errors.New("the configured file root itself cannot be mutated or read as a file")
	}
	parent, err := loadLocalFileList(ctx, client, filepath.Dir(path))
	if err != nil {
		return cliFileEntry{}, err
	}
	for _, entry := range parent.Entries {
		if entry.Path == path {
			return entry, nil
		}
	}
	return cliFileEntry{}, errors.New("local path is not present in the current parent-directory inventory")
}

func requireLocalDestinationAbsent(ctx context.Context, client *apiClient, path string) error {
	roots, err := loadLocalFileRoots(ctx, client)
	if err != nil {
		return err
	}
	if !pathWithinRoots(path, roots) || pathIsRoot(path, roots) {
		return errors.New("local destination must be below a configured file root")
	}
	parent, err := loadLocalFileList(ctx, client, filepath.Dir(path))
	if err != nil {
		return err
	}
	for _, entry := range parent.Entries {
		if entry.Path == path {
			return errors.New("local destination already exists in the current directory inventory")
		}
	}
	return nil
}

func loadLocalFileRoots(ctx context.Context, client *apiClient) ([]string, error) {
	response, err := requestJSON[localFileListResponse](ctx, client, http.MethodGet, "/api/files", nil, true)
	if err != nil {
		return nil, err
	}
	if len(response.Roots) == 0 {
		return nil, errors.New("local file manager reports no configured roots")
	}
	return response.Roots, nil
}

func loadLocalFileList(ctx context.Context, client *apiClient, path string) (localFileListResponse, error) {
	return requestJSON[localFileListResponse](ctx, client, http.MethodGet, "/api/files?path="+url.QueryEscape(path), nil, true)
}

func validateCLIManagedPath(value string) (string, error) {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return "", errors.New("managed path must be non-empty UTF-8 text of at most 4096 bytes")
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("managed path must not contain control characters")
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value || value == string(filepath.Separator) {
		return "", errors.New("managed path must be a clean absolute path below a configured root")
	}
	return value, nil
}

func pathWithinRoots(path string, roots []string) bool {
	for _, rawRoot := range roots {
		root := filepath.Clean(strings.TrimSpace(rawRoot))
		if !filepath.IsAbs(root) || root == string(filepath.Separator) {
			continue
		}
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func pathIsRoot(path string, roots []string) bool {
	for _, root := range roots {
		if path == filepath.Clean(strings.TrimSpace(root)) {
			return true
		}
	}
	return false
}

func dedicatedConfigFamily(path string) string {
	for _, item := range []struct {
		root   string
		family string
	}{
		{root: "/etc/nginx", family: "Nginx"},
		{root: "/etc/php", family: "PHP"},
	} {
		if path == item.root || strings.HasPrefix(path, item.root+string(filepath.Separator)) {
			return item.family
		}
	}
	return ""
}

func readCLIManagedTextFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect content file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("content file must be a regular file and not a symlink")
	}
	if info.Size() > maxCLIManagedFileBytes {
		return "", fmt.Errorf("content file exceeds %d bytes", maxCLIManagedFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read content file: %w", err)
	}
	if len(data) > maxCLIManagedFileBytes || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return "", errors.New("content file must be NUL-free UTF-8 text of at most 2097152 bytes")
	}
	return string(data), nil
}

func validateCLIFileChecksum(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("managed-node file save requires a 64-character SHA-256 --checksum")
	}
	return value, nil
}

func printJSONValue(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
