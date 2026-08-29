package snapshot

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/rcloneprofile"
)

const defaultRepoFolder = "hserver-snapshots"

type resticRunner struct {
	bin           string
	rcloneBin     string
	rcloneConfig  string
	password      string
	repoFolder    string
	cacheDir      string
	repositoryURL string
	extraEnv      []string
	globalOptions []string
}

func (r *resticRunner) repository() string {
	if r.repositoryURL != "" {
		return r.repositoryURL
	}
	folder := r.repoFolder
	if folder == "" {
		folder = defaultRepoFolder
	}
	return fmt.Sprintf("rclone:%s:%s", rcloneprofile.RemoteName, folder)
}

func (r *resticRunner) found() bool {
	if r.bin == "" {
		r.bin = "restic"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, r.bin, "version").Run() == nil
}

func (r *resticRunner) cachePath() string {
	if r.cacheDir != "" {
		return r.cacheDir
	}
	return filepath.Join(os.TempDir(), "hserver-restic-cache")
}

func (r *resticRunner) env() []string {
	cache := r.cachePath()
	_ = os.MkdirAll(cache, 0o750)
	strip := []string{
		"RESTIC_CACHE_DIR=", "RESTIC_PASSWORD=", "RESTIC_REPOSITORY=",
		"RCLONE_CONFIG=", "RESTIC_PACK_SIZE=",
		"RCLONE_RETRIES=", "RCLONE_LOW_LEVEL_RETRIES=", "RCLONE_TIMEOUT=",
		"AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=", "AWS_DEFAULT_REGION=", "AWS_SESSION_TOKEN=",
	}
	var base []string
	for _, e := range os.Environ() {
		skip := false
		for _, p := range strip {
			if strings.HasPrefix(e, p) {
				skip = true
				break
			}
		}
		if !skip {
			base = append(base, e)
		}
	}
	head := append(base,
		"RESTIC_PASSWORD="+r.password,
		"RESTIC_REPOSITORY="+r.repository(),
		"RESTIC_CACHE_DIR="+cache,
	)
	if r.repositoryURL == "" {
		head = append(head, "RCLONE_CONFIG="+r.rcloneConfig)
		return append(head, rcloneprofile.ResticEnvExtras()...)
	}
	return append(head, r.extraEnv...)
}

func (r *resticRunner) withGlobalOpts(args ...string) []string {
	options := r.globalOptions
	if r.repositoryURL == "" {
		options = rcloneprofile.ResticGlobalOptions()
	}
	return append(append([]string{}, options...), args...)
}

func (r *resticRunner) run(ctx context.Context, args ...string) (string, error) {
	if r.password == "" {
		return "", fmt.Errorf("HSERVER_RESTIC_PASSWORD not set")
	}
	cmd := exec.CommandContext(ctx, r.bin, r.withGlobalOpts(args...)...)
	cmd.Env = r.env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("restic %s: %w — %s", args[0], err, truncate(string(out), 800))
	}
	return string(out), nil
}

func (r *resticRunner) initRepo(ctx context.Context) error {
	_, err := r.run(ctx, "init")
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

// unlockStale removes abandoned restic repo locks (common after Drive API failures).
func (r *resticRunner) unlockStale(ctx context.Context) error {
	out, err := r.run(ctx, "unlock", "--remove-all")
	if err != nil {
		low := strings.ToLower(out + err.Error())
		if strings.Contains(low, "no locks") || strings.Contains(low, "no lock") {
			return nil
		}
		return err
	}
	return nil
}

func (r *resticRunner) repoReady(ctx context.Context) bool {
	_, err := r.run(ctx, "snapshots", "--json")
	return err == nil
}

func (r *resticRunner) backup(ctx context.Context, paths, excludes []string, tags []string, onLine func(string)) error {
	args := r.withGlobalOpts("backup", "-v", "--json")
	for _, ex := range excludes {
		if strings.TrimSpace(ex) != "" {
			args = append(args, "--exclude", ex)
		}
	}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	args = append(args, paths...)
	if r.bin == "" {
		r.bin = "restic"
	}
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Env = r.env()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("restic backup start: %w", err)
	}
	var wg sync.WaitGroup
	streamLines := func(rdr io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(rdr)
		buf := make([]byte, 0, 256*1024)
		sc.Buffer(buf, 10*1024*1024)
		for sc.Scan() {
			if onLine != nil {
				onLine(sc.Text())
			}
		}
	}
	wg.Add(2)
	go streamLines(stdout)
	go streamLines(stderr)
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("restic backup: %w", err)
	}
	return nil
}

func (r *resticRunner) forget(ctx context.Context, keepDaily, keepWeekly, keepMonthly int) error {
	args := []string{"forget", "--prune"}
	if keepDaily > 0 {
		args = append(args, "--keep-daily", fmt.Sprintf("%d", keepDaily))
	}
	if keepWeekly > 0 {
		args = append(args, "--keep-weekly", fmt.Sprintf("%d", keepWeekly))
	}
	if keepMonthly > 0 {
		args = append(args, "--keep-monthly", fmt.Sprintf("%d", keepMonthly))
	}
	_, err := r.run(ctx, args...)
	return err
}

func (r *resticRunner) snapshots(ctx context.Context, limit int) ([]Snapshot, error) {
	out, err := r.run(ctx, "snapshots", "--json")
	if err != nil {
		return nil, err
	}
	type raw struct {
		ID       string    `json:"id"`
		Time     time.Time `json:"time"`
		Hostname string    `json:"hostname"`
		Tags     []string  `json:"tags"`
		Paths    []string  `json:"paths"`
	}
	var list []raw
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("parse restic snapshots: %w", err)
	}
	if limit > 0 && len(list) > limit {
		list = list[len(list)-limit:]
	}
	result := make([]Snapshot, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		e := list[i]
		result = append(result, Snapshot{
			ID:       e.ID,
			Time:     e.Time,
			Hostname: e.Hostname,
			Tags:     e.Tags,
			Paths:    len(e.Paths),
		})
	}
	return result, nil
}

func (r *resticRunner) repoStats(ctx context.Context) (*RepoStats, error) {
	out, err := r.run(ctx, "stats", "--json")
	if err != nil {
		return nil, err
	}
	var raw struct {
		TotalSize     int64 `json:"total_size"`
		TotalFileSize int64 `json:"total_file_size"`
		SnapshotCount int   `json:"snapshot_count"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse restic stats: %w", err)
	}
	return &RepoStats{
		SnapshotCount: raw.SnapshotCount,
		TotalSize:     raw.TotalSize,
		TotalFileSize: raw.TotalFileSize,
	}, nil
}

func (r *resticRunner) restore(ctx context.Context, snapshotID, target string, includes []string) error {
	args := []string{"restore", snapshotID, "--target", target}
	for _, inc := range includes {
		if strings.TrimSpace(inc) != "" {
			args = append(args, "--include", inc)
		}
	}
	_, err := r.run(ctx, args...)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
