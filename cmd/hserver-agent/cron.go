package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxCronJobs       = 256
	maxCronStateBytes = 1 << 20
)

var (
	agentSystemUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	errCronChanged         = errors.New("cron jobs changed on the server")
	errCronNotFound        = errors.New("cron job was not found")
	errCronInvalid         = errors.New("cron job is invalid")
)

type cronJob struct {
	ID          string `json:"id"`
	Schedule    string `json:"schedule"`
	User        string `json:"user"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type cronSource struct {
	Path       string `json:"path"`
	EntryCount int    `json:"entry_count"`
	Managed    bool   `json:"managed"`
}

type cronInventory struct {
	Service  string       `json:"service"`
	Jobs     []cronJob    `json:"jobs"`
	Sources  []cronSource `json:"sources"`
	Revision string       `json:"revision"`
}

type cronController struct {
	runner        commandRunner
	allowRead     bool
	allowWrite    bool
	allowRun      bool
	statePath     string
	cronPath      string
	lockPath      string
	crontabBinary string
	runuserBinary string
	shell         string
	service       string
	mu            *sync.Mutex
}

func newCronController(runner commandRunner, allowRead, allowWrite, allowRun bool, statePath, cronPath, lockPath, crontabBinary, runuserBinary, shell, service string) cronController {
	return cronController{runner: runner, allowRead: allowRead, allowWrite: allowWrite, allowRun: allowRun, statePath: statePath, cronPath: cronPath, lockPath: lockPath, crontabBinary: crontabBinary, runuserBinary: runuserBinary, shell: shell, service: service, mu: &sync.Mutex{}}
}

func (c cronController) Inventory(ctx context.Context) (cronInventory, error) {
	if !c.allowRead {
		return cronInventory{}, errors.New("cron inventory is not enabled locally")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	jobs, err := c.loadJobs()
	if err != nil {
		return cronInventory{}, err
	}
	sources, err := c.sources(ctx)
	if err != nil {
		return cronInventory{}, err
	}
	return cronInventory{Service: c.serviceState(ctx), Jobs: jobs, Sources: sources, Revision: cronRevision(jobs)}, nil
}

func (c cronController) Create(ctx context.Context, job cronJob, expectedRevision string) (string, error) {
	if !c.allowWrite {
		return "", errors.New("cron writing is not enabled locally")
	}
	job.ID = ""
	if err := validateCronJob(job, false); err != nil {
		return "", err
	}
	id, err := newCronID()
	if err != nil {
		return "", err
	}
	job.ID = id
	if err := c.mutate(ctx, expectedRevision, func(jobs []cronJob) ([]cronJob, error) { return append(jobs, job), nil }); err != nil {
		return "", err
	}
	return id, nil
}

func (c cronController) Update(ctx context.Context, job cronJob, expectedRevision string) error {
	if !c.allowWrite {
		return errors.New("cron writing is not enabled locally")
	}
	if err := validateCronJob(job, true); err != nil {
		return err
	}
	return c.mutate(ctx, expectedRevision, func(jobs []cronJob) ([]cronJob, error) {
		for index := range jobs {
			if jobs[index].ID == job.ID {
				jobs[index] = job
				return jobs, nil
			}
		}
		return nil, errCronNotFound
	})
}

func (c cronController) Delete(ctx context.Context, id, expectedRevision string) error {
	if !c.allowWrite {
		return errors.New("cron writing is not enabled locally")
	}
	if !validAgentCronID(id) {
		return errCronInvalid
	}
	return c.mutate(ctx, expectedRevision, func(jobs []cronJob) ([]cronJob, error) {
		for index := range jobs {
			if jobs[index].ID == id {
				return append(jobs[:index], jobs[index+1:]...), nil
			}
		}
		return nil, errCronNotFound
	})
}

func (c cronController) Run(ctx context.Context, id string) (string, error) {
	if !c.allowRun {
		return "", errors.New("manual cron execution is not enabled locally")
	}
	if !validAgentCronID(id) {
		return "", errCronInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	jobs, err := c.loadJobs()
	if err != nil {
		return "", err
	}
	for _, job := range jobs {
		if job.ID != id {
			continue
		}
		commandCtx, cancel := context.WithTimeout(ctx, 130*time.Second)
		defer cancel()
		output, err := c.runner.run(commandCtx, c.runuserBinary, "-u", job.User, "--", c.shell, "-lc", job.Command)
		if err != nil {
			return "", fmt.Errorf("cron job failed: %w", err)
		}
		return string(output), nil
	}
	return "", errCronNotFound
}

func (c cronController) mutate(ctx context.Context, expectedRevision string, apply func([]cronJob) ([]cronJob, error)) error {
	if !agentSHA256Pattern.MatchString(expectedRevision) {
		return errCronChanged
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.lockPath), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(c.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	jobs, err := c.loadJobs()
	if err != nil {
		return err
	}
	if cronRevision(jobs) != expectedRevision {
		return errCronChanged
	}
	jobs, err = apply(jobs)
	if err != nil {
		return err
	}
	if len(jobs) > maxCronJobs {
		return errCronInvalid
	}
	for _, job := range jobs {
		if err := validateCronJob(job, true); err != nil {
			return err
		}
	}
	return c.saveJobs(ctx, jobs)
}

func (c cronController) loadJobs() ([]cronJob, error) {
	_, content, _, err := readManagedFile(c.statePath)
	if os.IsNotExist(err) {
		return []cronJob{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(content) > maxCronStateBytes {
		return nil, errors.New("cron state exceeds the size limit")
	}
	var jobs []cronJob
	if err := json.Unmarshal(content, &jobs); err != nil || len(jobs) > maxCronJobs {
		return nil, errors.New("cron state is invalid")
	}
	for _, job := range jobs {
		if err := validateCronJob(job, true); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

func (c cronController) saveJobs(ctx context.Context, jobs []cronJob) error {
	state, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	state = append(state, '\n')
	cron := renderCronJobs(jobs, c.shell)
	if err := c.validateRendered(ctx, jobs); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.statePath), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.cronPath), 0o755); err != nil {
		return err
	}
	backupDir := filepath.Join(filepath.Dir(c.statePath), "backups", "cron")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	previousState, stateExisted, err := backupCronFile(c.statePath, backupDir)
	if err != nil {
		return err
	}
	previousCron, cronExisted, err := backupCronFile(c.cronPath, backupDir)
	if err != nil {
		return err
	}
	if err := atomicCronWrite(c.cronPath, cron, 0o644); err != nil {
		return err
	}
	if err := atomicCronWrite(c.statePath, state, 0o600); err != nil {
		_ = restoreCronFile(c.cronPath, previousCron, cronExisted, 0o644)
		return err
	}
	pruneCronBackups(backupDir, 20)
	_ = previousState
	_ = stateExisted
	return nil
}

func (c cronController) validateRendered(ctx context.Context, jobs []cronJob) error {
	temporary, err := os.CreateTemp("", ".hserver-cron-check-*")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
	for _, job := range jobs {
		if job.Enabled {
			if _, err := fmt.Fprintf(temporary, "%s %s\n", job.Schedule, job.Command); err != nil {
				_ = temporary.Close()
				return err
			}
		}
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := c.runner.run(commandCtx, c.crontabBinary, "-n", path); err != nil {
		return fmt.Errorf("%w: cron syntax test failed", errCronInvalid)
	}
	return nil
}

func (c cronController) sources(ctx context.Context) ([]cronSource, error) {
	directory := filepath.Dir(c.cronPath)
	entries, err := os.ReadDir(directory)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sources := make([]cronSource, 0, len(entries)+1)
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil || len(content) > maxCronStateBytes {
			continue
		}
		count := 0
		for _, raw := range strings.Split(string(content), "\n") {
			line := strings.TrimSpace(raw)
			fields := strings.Fields(line)
			if line != "" && !strings.HasPrefix(line, "#") && len(fields) > 0 && !strings.Contains(fields[0], "=") {
				count++
			}
		}
		sources = append(sources, cronSource{Path: path, EntryCount: count, Managed: path == c.cronPath})
	}
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	rootCount := 0
	if output, runErr := c.runner.run(commandCtx, c.crontabBinary, "-l"); runErr == nil {
		for _, raw := range strings.Split(string(output), "\n") {
			line := strings.TrimSpace(raw)
			if line != "" && !strings.HasPrefix(line, "#") {
				rootCount++
			}
		}
	}
	sources = append(sources, cronSource{Path: "root crontab", EntryCount: rootCount})
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	return sources, nil
}

func (c cronController) serviceState(ctx context.Context) string {
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := c.runner.run(commandCtx, "systemctl", "show", "--property=ActiveState", "--value", c.service)
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func validateCronJob(job cronJob, requireID bool) error {
	if (requireID && !validAgentCronID(job.ID)) || (!requireID && job.ID != "") || !agentSystemUserPattern.MatchString(job.User) || len(job.Command) == 0 || len(job.Command) > 4096 || strings.ContainsAny(job.Command, "\r\n\x00") || len(job.Description) > 160 || strings.ContainsAny(job.Description, "\r\n\x00") || len(job.Schedule) > 160 || strings.ContainsAny(job.Schedule, "\r\n\x00") || len(strings.Fields(job.Schedule)) != 5 {
		return errCronInvalid
	}
	if _, err := user.Lookup(job.User); err != nil {
		return fmt.Errorf("%w: user does not exist", errCronInvalid)
	}
	return nil
}

func validAgentCronID(value string) bool {
	if len(value) != 17 || !strings.HasPrefix(value, "cron-") {
		return false
	}
	_, err := hex.DecodeString(value[5:])
	return err == nil
}

func newCronID() (string, error) {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "cron-" + hex.EncodeToString(raw[:]), nil
}

func cronRevision(jobs []cronJob) string {
	encoded, _ := json.Marshal(jobs)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func renderCronJobs(jobs []cronJob, shell string) []byte {
	var output strings.Builder
	fmt.Fprintln(&output, "# Managed by HServer. Changes outside HServer will be replaced.")
	fmt.Fprintf(&output, "SHELL=%s\n", shell)
	fmt.Fprintln(&output, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	for _, job := range jobs {
		fmt.Fprintf(&output, "\n# HSERVER %s", job.ID)
		if job.Description != "" {
			fmt.Fprintf(&output, " - %s", job.Description)
		}
		fmt.Fprintln(&output)
		line := fmt.Sprintf("%s %s %s", job.Schedule, job.User, job.Command)
		if !job.Enabled {
			line = "# DISABLED " + line
		}
		fmt.Fprintln(&output, line)
	}
	return []byte(output.String())
}

func backupCronFile(path, backupDir string) ([]byte, bool, error) {
	_, content, _, err := readManagedFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backup := filepath.Join(backupDir, filepath.Base(path)+".backup-"+stamp)
	if err := os.WriteFile(backup, content, 0o600); err != nil {
		return nil, false, err
	}
	return content, true, nil
}

func atomicCronWrite(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".hserver-cron-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func restoreCronFile(path string, content []byte, existed bool, mode os.FileMode) error {
	if !existed {
		return os.Remove(path)
	}
	return atomicCronWrite(path, content, mode)
}

func pruneCronBackups(directory string, keep int) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	type candidate struct {
		path string
		time time.Time
	}
	items := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			items = append(items, candidate{filepath.Join(directory, entry.Name()), info.ModTime()})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].time.After(items[j].time) })
	if len(items) <= keep {
		return
	}
	for _, item := range items[keep:] {
		_ = os.Remove(item.path)
	}
}
