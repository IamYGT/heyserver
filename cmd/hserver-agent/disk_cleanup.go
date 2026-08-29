package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type diskCleanupTarget struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Size        uint64 `json:"size"`
	Risk        string `json:"risk"`
}

type diskCleanupResult struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Reclaimed uint64 `json:"reclaimed"`
}

type diskCleanupExecution struct {
	Results   []diskCleanupResult `json:"results"`
	ScanError string              `json:"scan_error,omitempty"`
}

type diskCleanupExecutor interface {
	Scan(context.Context) ([]diskCleanupTarget, error)
	Execute(context.Context, []string) (diskCleanupExecution, error)
}

type diskCleanupController struct {
	runner  commandRunner
	allowed map[string]struct{}
	paths   diskCleanupPaths
	now     func() time.Time
}

type diskCleanupPaths struct {
	aptArchives string
	journal     string
	temporary   []string
	logs        string
}

type diskCleanupDefinition struct {
	name, description, risk, message string
}

var diskCleanupDefinitions = map[string]diskCleanupDefinition{
	"apt-cache": {
		name: "APT package cache", description: "Downloaded .deb packages and partial downloads; installed packages stay unchanged",
		risk: "low", message: "APT package cache cleaned",
	},
	"journal": {
		name: "Old system journal", description: "Vacuum archived systemd journal entries while retaining the latest 7 days",
		risk: "low", message: "Journal vacuumed; the latest 7 days were retained",
	},
	"tmp-old": {
		name: "Expired temporary files", description: "Regular files in /tmp and /var/tmp not modified for more than 7 days",
		risk: "medium", message: "Temporary files older than 7 days removed",
	},
	"rotated-logs": {
		name: "Old rotated logs", description: "Compressed and numbered log rotations older than 7 days; active logs stay untouched",
		risk: "medium", message: "Rotated logs older than 7 days removed",
	},
}

func newDiskCleanupController(runner commandRunner, allowed map[string]struct{}) *diskCleanupController {
	return &diskCleanupController{
		runner: runner, allowed: allowed, now: time.Now,
		paths: diskCleanupPaths{
			aptArchives: "/var/cache/apt/archives",
			journal:     "/var/log/journal",
			temporary:   []string{"/tmp", "/var/tmp"},
			logs:        "/var/log",
		},
	}
}

func (c *diskCleanupController) Scan(ctx context.Context) ([]diskCleanupTarget, error) {
	ids := make([]string, 0, len(c.allowed))
	for id := range c.allowed {
		if _, exists := diskCleanupDefinitions[id]; exists {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	targets := make([]diskCleanupTarget, 0, len(ids))
	for _, id := range ids {
		size, err := c.measure(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("measure %s: %w", id, err)
		}
		if size == 0 {
			continue
		}
		definition := diskCleanupDefinitions[id]
		targets = append(targets, diskCleanupTarget{
			ID: id, Name: definition.name, Description: definition.description,
			Size: size, Risk: definition.risk,
		})
	}
	return targets, nil
}

func (c *diskCleanupController) Execute(ctx context.Context, ids []string) (diskCleanupExecution, error) {
	if len(ids) == 0 || len(ids) > len(diskCleanupDefinitions) {
		return diskCleanupExecution{}, errors.New("select between one and four disk cleanup targets")
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, permitted := c.allowed[id]; !permitted {
			return diskCleanupExecution{}, fmt.Errorf("disk cleanup target %s is not in the local allowlist", id)
		}
		if _, exists := diskCleanupDefinitions[id]; !exists {
			return diskCleanupExecution{}, errors.New("unsupported disk cleanup target")
		}
		if _, duplicate := seen[id]; duplicate {
			return diskCleanupExecution{}, errors.New("duplicate disk cleanup target")
		}
		seen[id] = struct{}{}
	}

	before := make(map[string]uint64, len(ids))
	for _, id := range ids {
		size, err := c.measure(ctx, id)
		if err != nil {
			return diskCleanupExecution{}, fmt.Errorf("measure %s before cleanup: %w", id, err)
		}
		before[id] = size
	}

	execution := diskCleanupExecution{Results: make([]diskCleanupResult, 0, len(ids))}
	for _, id := range ids {
		result := diskCleanupResult{ID: id, Status: "ok", Message: diskCleanupDefinitions[id].message}
		if err := c.clean(ctx, id); err != nil {
			result.Status = "error"
			result.Message = err.Error()
		}
		execution.Results = append(execution.Results, result)
	}
	for index := range execution.Results {
		after, err := c.measure(ctx, execution.Results[index].ID)
		if err != nil {
			execution.ScanError = err.Error()
			break
		}
		if before[execution.Results[index].ID] > after {
			execution.Results[index].Reclaimed = before[execution.Results[index].ID] - after
		}
	}
	return execution, nil
}

func (c *diskCleanupController) measure(ctx context.Context, id string) (uint64, error) {
	cutoff := c.now().Add(-7 * 24 * time.Hour)
	switch id {
	case "apt-cache":
		return walkFileSize(ctx, []string{c.paths.aptArchives}, func(path string, info os.FileInfo) bool {
			return strings.HasSuffix(info.Name(), ".deb") || strings.Contains(filepath.ToSlash(path), "/partial/")
		})
	case "journal":
		return walkFileSize(ctx, []string{c.paths.journal}, func(string, os.FileInfo) bool { return true })
	case "tmp-old":
		return walkFileSize(ctx, c.paths.temporary, func(_ string, info os.FileInfo) bool { return info.ModTime().Before(cutoff) })
	case "rotated-logs":
		return walkFileSize(ctx, []string{c.paths.logs}, func(_ string, info os.FileInfo) bool {
			return info.ModTime().Before(cutoff) && rotatedLogName(info.Name())
		})
	default:
		return 0, errors.New("unsupported disk cleanup target")
	}
}

func (c *diskCleanupController) clean(ctx context.Context, id string) error {
	cutoff := c.now().Add(-7 * 24 * time.Hour)
	switch id {
	case "apt-cache":
		return c.run(ctx, "apt-get", "clean")
	case "journal":
		return c.run(ctx, "journalctl", "--vacuum-time=7d")
	case "tmp-old":
		return removeMatchingFiles(ctx, c.paths.temporary, func(_ string, info os.FileInfo) bool { return info.ModTime().Before(cutoff) })
	case "rotated-logs":
		return removeMatchingFiles(ctx, []string{c.paths.logs}, func(_ string, info os.FileInfo) bool {
			return info.ModTime().Before(cutoff) && rotatedLogName(info.Name())
		})
	default:
		return errors.New("unsupported disk cleanup target")
	}
}

func (c *diskCleanupController) run(ctx context.Context, name string, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := c.runner.run(commandCtx, name, args...); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func walkFileSize(ctx context.Context, roots []string, include func(string, os.FileInfo) bool) (uint64, error) {
	var total uint64
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if !info.Mode().IsRegular() || !include(path, info) {
				return nil
			}
			if info.Size() < 0 {
				return errors.New("disk cleanup measurement returned a negative file size")
			}
			size := uint64(info.Size())
			if ^uint64(0)-total < size {
				return errors.New("disk cleanup measurement overflow")
			}
			total += size
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return 0, err
		}
	}
	return total, nil
}

func removeMatchingFiles(ctx context.Context, roots []string, include func(string, os.FileInfo) bool) error {
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if info.Mode().IsRegular() && include(path, info) {
				return os.Remove(path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func rotatedLogName(name string) bool {
	if strings.HasSuffix(name, ".gz") || strings.Contains(name, ".log-") {
		return true
	}
	marker := strings.LastIndex(name, ".log.")
	if marker < 0 || marker+5 >= len(name) {
		return false
	}
	return name[marker+5] >= '0' && name[marker+5] <= '9'
}
