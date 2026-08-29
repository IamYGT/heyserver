package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxBackupPlans       = 32
	maxBackupPlanBytes   = 64 << 10
	maxBackupFiles       = 256
	maxBackupDepth       = 4
	maxBackupVerifyBytes = int64(16 << 30)
)

type backupPlanConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Service      string `json:"service"`
	Timer        string `json:"timer"`
	Root         string `json:"root"`
	ChecksumFile string `json:"checksum_file,omitempty"`
}

type backupPlanDocument struct {
	Plans []backupPlanConfig `json:"plans"`
}

type managedBackupFile struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

type managedBackupPlan struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Service     string              `json:"service"`
	Timer       string              `json:"timer"`
	Active      string              `json:"active"`
	Enabled     string              `json:"enabled"`
	LastResult  string              `json:"last_result"`
	LastRun     string              `json:"last_run"`
	NextRun     string              `json:"next_run"`
	CompletedAt string              `json:"completed_at,omitempty"`
	Verified    bool                `json:"verified"`
	TotalSize   int64               `json:"total_size"`
	Files       []managedBackupFile `json:"files"`
}

type backupController struct {
	runner    commandRunner
	allowRead bool
	allowRun  bool
	plansPath string
}

func newBackupController(runner commandRunner, allowRead, allowRun bool, plansPath string) backupController {
	return backupController{runner: runner, allowRead: allowRead, allowRun: allowRun, plansPath: plansPath}
}

func (c backupController) Inventory(ctx context.Context) ([]managedBackupPlan, error) {
	if !c.allowRead {
		return nil, errors.New("backup inventory is not enabled locally")
	}
	plans, err := c.loadPlans()
	if err != nil {
		return nil, err
	}
	inventoryCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result := make([]managedBackupPlan, 0, len(plans))
	for _, plan := range plans {
		if err := inventoryCtx.Err(); err != nil {
			return nil, err
		}
		result = append(result, c.inspectPlan(inventoryCtx, plan))
	}
	return result, nil
}

func (c backupController) inspectPlan(ctx context.Context, plan backupPlanConfig) managedBackupPlan {
	timer := c.unitProperties(ctx, plan.Timer)
	service := c.unitProperties(ctx, plan.Service)
	files, totalSize, completedAt, fileErr := listBackupFiles(plan.Root)
	verified := false
	if fileErr == nil && plan.ChecksumFile != "" {
		verified = verifyBackupChecksums(ctx, plan.Root, plan.ChecksumFile)
	}
	return managedBackupPlan{
		ID: plan.ID, Name: plan.Name, Service: plan.Service, Timer: plan.Timer,
		Active: valueOrUnknown(timer["ActiveState"]), Enabled: valueOrUnknown(timer["UnitFileState"]),
		LastResult: valueOrUnknown(service["Result"]), LastRun: timer["LastTriggerUSec"], NextRun: timer["NextElapseUSecRealtime"],
		CompletedAt: completedAt, Verified: verified, TotalSize: totalSize, Files: files,
	}
}

func (c backupController) unitProperties(ctx context.Context, unit string) map[string]string {
	output, err := c.runner.run(ctx, "systemctl", "show", unit, "-p", "ActiveState", "-p", "UnitFileState", "-p", "NextElapseUSecRealtime", "-p", "LastTriggerUSec", "-p", "Result")
	if err != nil {
		return map[string]string{}
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && len(key) <= 64 && len(value) <= 512 {
			values[key] = value
		}
	}
	return values
}

func (c backupController) Run(ctx context.Context, id string) (string, error) {
	if !c.allowRun {
		return "", errors.New("backup execution is not enabled locally")
	}
	plans, err := c.loadPlans()
	if err != nil {
		return "", err
	}
	var selected *backupPlanConfig
	for index := range plans {
		if plans[index].ID == id {
			selected = &plans[index]
			break
		}
	}
	if selected == nil {
		return "", errors.New("backup plan is not configured locally")
	}
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	if _, err := c.runner.run(runCtx, "systemctl", "start", selected.Service); err != nil {
		return "", fmt.Errorf("backup service failed: %w", err)
	}
	properties := c.unitProperties(runCtx, selected.Service)
	if properties["Result"] != "success" {
		return "", fmt.Errorf("backup service result is %s", valueOrUnknown(properties["Result"]))
	}
	return selected.Name + " backup completed", nil
}

func (c backupController) loadPlans() ([]backupPlanConfig, error) {
	info, err := os.Stat(c.plansPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxBackupPlanBytes {
		return nil, errors.New("backup plan configuration is unavailable or invalid")
	}
	data, err := os.ReadFile(c.plansPath)
	if err != nil {
		return nil, errors.New("backup plan configuration is unavailable")
	}
	var document backupPlanDocument
	if err := json.Unmarshal(data, &document); err != nil || len(document.Plans) > maxBackupPlans {
		return nil, errors.New("backup plan configuration is invalid")
	}
	seen := make(map[string]struct{}, len(document.Plans))
	for _, plan := range document.Plans {
		if !agentNginxConfigNamePattern.MatchString(plan.ID) || plan.Name == "" || len(plan.Name) > 128 || strings.ContainsAny(plan.Name, "\r\n\x00") || !servicePattern.MatchString(plan.Service) || !servicePattern.MatchString(plan.Timer) || !validBackupRoot(plan.Root) || !validBackupRelativePath(plan.ChecksumFile, true) {
			return nil, errors.New("backup plan configuration contains an invalid plan")
		}
		if _, exists := seen[plan.ID]; exists {
			return nil, errors.New("backup plan IDs must be unique")
		}
		seen[plan.ID] = struct{}{}
	}
	return document.Plans, nil
}

func validBackupRoot(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator) && len(path) <= 4096
}
func validBackupRelativePath(path string, optional bool) bool {
	if path == "" {
		return optional
	}
	return !filepath.IsAbs(path) && filepath.Clean(path) == path && path != "." && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator)) && len(path) <= 512
}

func listBackupFiles(root string) ([]managedBackupFile, int64, string, error) {
	files := make([]managedBackupFile, 0)
	var total int64
	var latest time.Time
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		depth := len(strings.Split(filepath.ToSlash(relative), "/"))
		if entry.IsDir() {
			if depth > maxBackupDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || depth > maxBackupDepth {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if len(files) >= maxBackupFiles {
			return errors.New("backup file inventory exceeds the limit")
		}
		if info.Size() > 0 && total > int64(^uint64(0)>>1)-info.Size() {
			return errors.New("backup size exceeds the supported range")
		}
		total += info.Size()
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		files = append(files, managedBackupFile{Name: filepath.ToSlash(relative), Path: path, Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano)})
		return nil
	})
	if os.IsNotExist(err) {
		return []managedBackupFile{}, 0, "", nil
	}
	if err != nil {
		return []managedBackupFile{}, 0, "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	completed := ""
	if !latest.IsZero() {
		completed = latest.UTC().Format(time.RFC3339Nano)
	}
	return files, total, completed, nil
}

func verifyBackupChecksums(ctx context.Context, root, checksumFile string) bool {
	manifestPath := filepath.Join(root, checksumFile)
	info, err := os.Lstat(manifestPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxBackupPlanBytes {
		return false
	}
	manifest, err := os.Open(manifestPath)
	if err != nil {
		return false
	}
	defer manifest.Close()
	scanner := bufio.NewScanner(manifest)
	entries, total := 0, int64(0)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 67 {
			return false
		}
		digest, err := hex.DecodeString(line[:64])
		if err != nil || len(digest) != sha256.Size {
			return false
		}
		relative := strings.TrimLeft(line[64:], " *")
		if !validBackupRelativePath(relative, false) {
			return false
		}
		path := filepath.Join(root, relative)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxBackupVerifyBytes-total {
			return false
		}
		total += info.Size()
		entries++
		if entries > maxBackupFiles || ctx.Err() != nil {
			return false
		}
		file, err := os.Open(path)
		if err != nil {
			return false
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, io.LimitReader(contextReader{ctx: ctx, reader: file}, info.Size()+1))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(digest)) {
			return false
		}
	}
	return scanner.Err() == nil && entries > 0
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func valueOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
