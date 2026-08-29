package gdrive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/rcloneprofile"
)

// ProgressFn receives rclone transfer updates parsed from --stats-one-line output.
type ProgressFn func(bytesDone, bytesTotal int64, percent int, speed, eta, rawLine string)

var rcloneXferRe = regexp.MustCompile(`Transferred:\s+([\d.]+\s*\w+)\s*/\s*([\d.]+\s*\w+),\s*(\d+)%`)
var rcloneSpeedRe = regexp.MustCompile(`([\d.]+\s*\w+/s),\s*ETA\s+(\S+)`)

var safeRemotePathRe = regexp.MustCompile(`^[a-zA-Z0-9._\-/]+$`)

const rcloneReadinessTimeout = 5 * time.Second

var errRcloneReadinessUnavailable = errors.New("rclone readiness is unavailable")

func isRcloneMissingError(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

// rcloneRunner wraps rclone CLI with hardened exec (no shell).
type rcloneRunner struct {
	bin        string
	configPath string
}

func newRcloneRunner(dataDir, bin string) *rcloneRunner {
	if bin == "" {
		bin = "rclone"
	}
	return &rcloneRunner{
		bin:        bin,
		configPath: filepath.Join(dataDir, rcloneConfName),
	}
}

func (r *rcloneRunner) found() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.bin, "version")
	return cmd.Run() == nil
}

// foundContext performs the bounded, read-only rclone prerequisite check used
// by the readiness seam. Executable discovery distinguishes an absent optional
// integration from an installed but broken rclone command. No command output
// is retained because rclone may include installation or provider details in
// it; callers receive only the package-level canonical state/error.
func (r *rcloneRunner) foundContext(parent context.Context) error {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return err
	}
	if r == nil || strings.TrimSpace(r.bin) == "" {
		return ErrNotConfigured
	}

	resolved, err := exec.LookPath(r.bin)
	if err != nil {
		if isRcloneMissingError(err) {
			return ErrNotConfigured
		}
		return errRcloneReadinessUnavailable
	}
	if strings.TrimSpace(resolved) == "" {
		return ErrNotConfigured
	}
	if strings.TrimSpace(r.configPath) == "" {
		return ErrNotConfigured
	}

	if _, err := r.runReadinessCommand(parent, resolved, false, "version"); err != nil {
		return err
	}
	remotes, err := r.runReadinessCommand(parent, resolved, true, "--config", r.configPath, "listremotes")
	if err != nil {
		return err
	}
	if !hasReadinessRemote(remotes) {
		return errRcloneReadinessUnavailable
	}
	return nil
}

// boundedReadinessOutput keeps a helper's output bounded while allowing the
// production probe to verify the configured remote name. Output is never
// returned to callers or included in an error.
type boundedReadinessOutput struct {
	data      []byte
	max       int
	truncated bool
}

func (o *boundedReadinessOutput) Write(p []byte) (int, error) {
	if o.max <= len(o.data) {
		o.truncated = o.truncated || len(p) > 0
		return len(p), nil
	}
	remaining := o.max - len(o.data)
	if len(p) > remaining {
		o.data = append(o.data, p[:remaining]...)
		o.truncated = true
		return len(p), nil
	}
	o.data = append(o.data, p...)
	return len(p), nil
}

func hasReadinessRemote(output []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == rcloneprofile.RemoteName+":" {
			return true
		}
	}
	return false
}

func (r *rcloneRunner) runReadinessCommand(parent context.Context, resolved string, capture bool, args ...string) ([]byte, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, rcloneReadinessTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, resolved, args...)
	output := &boundedReadinessOutput{max: 64 << 10}
	if capture {
		cmd.Stdout = output
	} else {
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = io.Discard
	// rclone is normally a single process, but a wrapper can leave descendants
	// behind. Kill the process group on cancellation so a status request cannot
	// strand a subprocess beyond its deadline.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = 250 * time.Millisecond
	if err := cmd.Run(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, errRcloneReadinessUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if capture && output.truncated {
		return nil, errRcloneReadinessUnavailable
	}
	return output.data, nil
}

func (r *rcloneRunner) run(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"--config", r.configPath}, args...)
	cmd := exec.CommandContext(ctx, r.bin, full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("rclone %s: %w — %s", args[0], err, truncate(string(out), 500))
	}
	return string(out), nil
}

// writeConfig generates rclone.conf from a stored OAuth token.
func (r *rcloneRunner) writeConfig(token *tokenData) error {
	if token == nil || token.RefreshToken == "" {
		return fmt.Errorf("no valid OAuth token")
	}
	tokenJSON, err := json.Marshal(token)
	if err != nil {
		return err
	}
	content := rcloneprofile.RenderDriveRemoteConfig(rcloneprofile.RemoteName, string(tokenJSON), rcloneprofile.DefaultDriveTuning())
	dir := filepath.Dir(r.configPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(r.configPath, []byte(content), 0o600)
}

func (r *rcloneRunner) remoteDest(folder, name string) (string, error) {
	folder = sanitizePath(folder)
	if folder == "" {
		folder = defaultFolder
	}
	dest := rcloneprofile.RemoteName + ":" + folder
	if name != "" {
		name = sanitizePath(name)
		if name == "" {
			return "", fmt.Errorf("invalid remote file name")
		}
		dest += "/" + name
	}
	return dest, nil
}

func (r *rcloneRunner) copy(ctx context.Context, localPath, folder string) error {
	return r.copyWithProgress(ctx, localPath, folder, nil, nil)
}

func (r *rcloneRunner) copyWithProgress(ctx context.Context, localPath, folder string, onProgress ProgressFn, onLog func(string)) error {
	dest, err := r.remoteDest(folder, "")
	if err != nil {
		return err
	}
	full := append([]string{"--config", r.configPath}, rcloneprofile.CLICopyFlags()...)
	full = append(full, "copy", localPath, dest, "--checksum", "--stats", "1s", "--stats-one-line", "-v")
	cmd := exec.CommandContext(ctx, r.bin, full...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if onProgress == nil {
			_, _ = io.Copy(io.Discard, stderr)
			return
		}
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			if onLog != nil && strings.TrimSpace(line) != "" {
				onLog(line)
			}
			if onProgress != nil {
				parseRcloneProgress(line, onProgress)
			}
		}
	}()

	waitErr := cmd.Wait()
	<-done
	if waitErr != nil {
		return fmt.Errorf("rclone copy: %w", waitErr)
	}
	return nil
}

func parseRcloneProgress(line string, onProgress ProgressFn) {
	if onProgress == nil || !strings.Contains(line, "Transferred:") {
		return
	}
	m := rcloneXferRe.FindStringSubmatch(line)
	if len(m) < 4 {
		return
	}
	pct, _ := strconv.Atoi(m[3])
	speed, eta := "", ""
	if sm := rcloneSpeedRe.FindStringSubmatch(line); len(sm) >= 3 {
		speed = sm[1]
		eta = sm[2]
	}
	onProgress(parseSizeToken(m[1]), parseSizeToken(m[2]), pct, speed, eta, line)
}

func parseSizeToken(tok string) int64 {
	tok = strings.TrimSpace(tok)
	fields := strings.Fields(tok)
	if len(fields) < 2 {
		return 0
	}
	val, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToUpper(fields[1])
	mult := float64(1)
	switch {
	case strings.HasPrefix(unit, "KI"):
		mult = 1024
	case strings.HasPrefix(unit, "MI"):
		mult = 1024 * 1024
	case strings.HasPrefix(unit, "GI"):
		mult = 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "TI"):
		mult = 1024 * 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "B"):
		mult = 1
	}
	return int64(val * mult)
}

func (r *rcloneRunner) copyFromRemote(ctx context.Context, remoteName_, folder, localDir string) error {
	return r.copyFromRemoteWithProgress(ctx, remoteName_, folder, localDir, nil, nil)
}

func (r *rcloneRunner) copyFromRemoteWithProgress(ctx context.Context, remoteName_, folder, localDir string, onProgress ProgressFn, onLog func(string)) error {
	folder = sanitizePath(folder)
	if folder == "" {
		folder = defaultFolder
	}
	name := sanitizePath(remoteName_)
	if name == "" {
		return fmt.Errorf("invalid remote file name")
	}
	src := rcloneprofile.RemoteName + ":" + folder + "/" + name
	full := append([]string{"--config", r.configPath}, rcloneprofile.CLICopyFlags()...)
	full = append(full, "copy", src, localDir, "--checksum", "--stats", "1s", "--stats-one-line", "-v")
	cmd := exec.CommandContext(ctx, r.bin, full...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if onProgress == nil && onLog == nil {
			_, _ = io.Copy(io.Discard, stderr)
			return
		}
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			if onLog != nil && strings.TrimSpace(line) != "" {
				onLog(line)
			}
			if onProgress != nil {
				parseRcloneProgress(line, onProgress)
			}
		}
	}()

	waitErr := cmd.Wait()
	<-done
	if waitErr != nil {
		return fmt.Errorf("rclone copy: %w", waitErr)
	}
	return nil
}

func (r *rcloneRunner) listJSON(ctx context.Context, folder string) ([]RemoteBackup, error) {
	dest, err := r.remoteDest(folder, "")
	if err != nil {
		return nil, err
	}
	out, err := r.run(ctx, "lsjson", dest, "--recursive")
	if err != nil {
		return nil, err
	}
	type entry struct {
		Name    string `json:"Name"`
		Path    string `json:"Path"`
		Size    int64  `json:"Size"`
		ModTime string `json:"ModTime"`
		IsDir   bool   `json:"IsDir"`
	}
	var raw []entry
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse rclone lsjson: %w", err)
	}
	byName := make(map[string]RemoteBackup)
	for _, e := range raw {
		if e.IsDir {
			continue
		}
		if !isBackupFile(e.Name) {
			continue
		}
		mt, _ := time.Parse(time.RFC3339, e.ModTime)
		candidate := RemoteBackup{
			Name:    e.Name,
			Path:    e.Path,
			Size:    e.Size,
			ModTime: mt,
		}
		existing, ok := byName[e.Name]
		if !ok || candidate.ModTime.After(existing.ModTime) || (candidate.ModTime.Equal(existing.ModTime) && candidate.Size > existing.Size) {
			byName[e.Name] = candidate
		}
	}
	backups := make([]RemoteBackup, 0, len(byName))
	for _, b := range byName {
		backups = append(backups, b)
	}
	return backups, nil
}

func (r *rcloneRunner) remoteFileSize(ctx context.Context, folder, fileName string) (int64, error) {
	fileName = filepath.Base(fileName)
	backups, err := r.listJSON(ctx, folder)
	if err != nil {
		return 0, err
	}
	for _, b := range backups {
		if b.Name == fileName {
			return b.Size, nil
		}
	}
	return 0, fmt.Errorf("remote file not found: %s", fileName)
}

func verifyFileSizes(localSize, remoteSize int64) error {
	if localSize <= 0 {
		return fmt.Errorf("local file is empty")
	}
	if remoteSize <= 0 {
		return fmt.Errorf("remote file size unknown")
	}
	if localSize != remoteSize {
		return fmt.Errorf("size mismatch: local=%d remote=%d", localSize, remoteSize)
	}
	return nil
}

func (r *rcloneRunner) verify(ctx context.Context, localPath, folder, fileName string) error {
	localInfo, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("local stat: %w", err)
	}
	remoteSize, err := r.remoteFileSize(ctx, folder, fileName)
	if err != nil {
		return err
	}
	return verifyFileSizes(localInfo.Size(), remoteSize)
}

func (r *rcloneRunner) deleteOlderThan(ctx context.Context, folder string, cutoff time.Time) (int, error) {
	backups, err := r.listJSON(ctx, folder)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, b := range backups {
		if b.ModTime.IsZero() || b.ModTime.After(cutoff) {
			continue
		}
		rel := b.Path
		if rel == "" {
			rel = b.Name
		}
		remote, err := r.remoteDest(folder, filepath.Base(rel))
		if err != nil {
			continue
		}
		if _, err := r.run(ctx, "deletefile", remote); err != nil {
			continue
		}
		deleted++
	}
	return deleted, nil
}

func (r *rcloneRunner) test(ctx context.Context, folder string) error {
	dest, err := r.remoteDest(folder, "")
	if err != nil {
		return err
	}
	if _, err = r.run(ctx, "lsd", dest); err != nil {
		// First connect: create backup folder on Drive (Plesk "My Plesk" equivalent).
		if _, mkErr := r.run(ctx, "mkdir", dest); mkErr != nil {
			return err
		}
	}
	return nil
}

func sanitizePath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "/")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '/' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	// Reject path traversal segments
	for _, part := range strings.Split(out, "/") {
		if part == ".." {
			return ""
		}
	}
	if out != "" && !safeRemotePathRe.MatchString(out) {
		return ""
	}
	return out
}

func isBackupFile(name string) bool {
	return strings.HasSuffix(name, ".tar.gz") ||
		strings.HasSuffix(name, ".sql.gz")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
