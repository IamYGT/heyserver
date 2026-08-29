package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxPHPVersions = 32
	maxPHPPools    = 512
)

var (
	agentPHPVersionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
	agentPHPPoolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
	errPHPConfigChanged     = errors.New("PHP-FPM pool changed on the server")
	errPHPConfigInvalid     = errors.New("PHP-FPM configuration test failed")
	errPHPConfigTooLarge    = errors.New("PHP-FPM pool exceeds the size limit")
)

type phpFPMVersion struct {
	Version string       `json:"version"`
	Unit    string       `json:"unit"`
	Active  string       `json:"active"`
	Enabled string       `json:"enabled"`
	Masked  bool         `json:"masked"`
	Binary  string       `json:"binary,omitempty"`
	Pools   []phpFPMPool `json:"pools"`
}

type phpFPMPool struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	User        string `json:"user,omitempty"`
	Group       string `json:"group,omitempty"`
	Listen      string `json:"listen,omitempty"`
	PM          string `json:"pm,omitempty"`
	MaxChildren int    `json:"max_children,omitempty"`
}

type managedFileContent struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Checksum   string `json:"checksum"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	ModifiedAt string `json:"modified_at"`
}

type phpController struct {
	runner         commandRunner
	allowedActions map[string]struct{}
	allowRead      bool
	allowWrite     bool
	configRoot     string
	binaryRoot     string
	mu             *sync.Mutex
}

func newPHPController(runner commandRunner, allowedActions map[string]struct{}, allowRead, allowWrite bool, configRoot, binaryRoot string) phpController {
	return phpController{
		runner: runner, allowedActions: allowedActions, allowRead: allowRead, allowWrite: allowWrite,
		configRoot: configRoot, binaryRoot: binaryRoot, mu: &sync.Mutex{},
	}
}

func (c phpController) Inventory(ctx context.Context) ([]phpFPMVersion, error) {
	if !c.allowRead {
		return nil, errors.New("PHP-FPM inventory is not enabled locally")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.configRoot)
	if err != nil {
		return nil, fmt.Errorf("read PHP configuration root: %w", err)
	}
	versions := make([]phpFPMVersion, 0, len(entries))
	for _, entry := range entries {
		if len(versions) == maxPHPVersions {
			break
		}
		version := entry.Name()
		if !entry.IsDir() || !agentPHPVersionPattern.MatchString(version) {
			continue
		}
		poolDir := filepath.Join(c.configRoot, version, "fpm", "pool.d")
		if info, statErr := os.Stat(poolDir); statErr != nil || !info.IsDir() {
			continue
		}
		unit := "php" + version + "-fpm.service"
		active, enabled := c.serviceState(ctx, unit)
		binary := filepath.Join(c.binaryRoot, "php-fpm"+version)
		if info, statErr := os.Stat(binary); statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			binary = ""
		}
		pools, poolErr := readPHPPools(poolDir)
		if poolErr != nil {
			return nil, fmt.Errorf("read PHP %s pools: %w", version, poolErr)
		}
		versions = append(versions, phpFPMVersion{
			Version: version, Unit: unit, Active: active, Enabled: enabled, Masked: enabled == "masked", Binary: binary, Pools: pools,
		})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
	return versions, nil
}

func (c phpController) Read(version, pool string) (managedFileContent, error) {
	if !c.allowRead {
		return managedFileContent{}, errors.New("PHP-FPM configuration reading is not enabled locally")
	}
	path, err := c.poolPath(version, pool)
	if err != nil {
		return managedFileContent{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	info, content, checksum, err := readManagedFile(path)
	if err != nil {
		return managedFileContent{}, err
	}
	return managedFileContent{
		Path: path, Content: string(content), Checksum: checksum, Size: info.Size(),
		Mode: fmt.Sprintf("%04o", info.Mode().Perm()), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (c phpController) Write(ctx context.Context, version, pool string, content []byte, expectedChecksum string, reload bool) (string, error) {
	if !c.allowWrite {
		return "", errors.New("PHP-FPM configuration writing is not enabled locally")
	}
	path, err := c.poolPath(version, pool)
	if err != nil || !agentSHA256Pattern.MatchString(expectedChecksum) {
		return "", errors.New("invalid PHP-FPM configuration write request")
	}
	if len(content) > maxAgentNginxConfigBytes {
		return "", errPHPConfigTooLarge
	}
	if !validManagedTextContent(content) {
		return "", errors.New("PHP-FPM configuration must be valid UTF-8 text")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	info, current, checksum, err := readManagedFile(path)
	if err != nil {
		return "", err
	}
	if checksum != expectedChecksum {
		return "", errPHPConfigChanged
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("PHP-FPM pool ownership is unavailable")
	}
	backup := path + ".hserver-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := writeNewManagedFile(backup, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return "", fmt.Errorf("create PHP-FPM pool backup: %w", err)
	}
	_, _, latestChecksum, err := readManagedFile(path)
	if err != nil {
		return "", err
	}
	if latestChecksum != expectedChecksum {
		return "", errPHPConfigChanged
	}
	if err := replaceManagedFile(path, content, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); err != nil {
		return "", fmt.Errorf("replace PHP-FPM pool: %w", err)
	}
	if err := c.testLocked(ctx, version); err != nil {
		if restoreErr := replaceManagedFile(path, current, info.Mode().Perm(), int(stat.Uid), int(stat.Gid)); restoreErr != nil {
			return "", fmt.Errorf("%w and restore failed: %v", errPHPConfigInvalid, restoreErr)
		}
		return "", errPHPConfigInvalid
	}
	if reload {
		if err := c.serviceActionLocked(ctx, version, "reload"); err != nil {
			return "", err
		}
	}
	return backup, nil
}

func (c phpController) Action(ctx context.Context, version, action string) (string, error) {
	if !agentPHPVersionPattern.MatchString(version) {
		return "", errors.New("invalid PHP-FPM version")
	}
	if _, allowed := c.allowedActions[action]; !allowed {
		return "", errors.New("PHP-FPM action is not in the local allowlist")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.testLocked(ctx, version); err != nil {
		return "", err
	}
	if action == "test" {
		return "PHP-FPM configuration is valid", nil
	}
	if err := c.serviceActionLocked(ctx, version, action); err != nil {
		return "", err
	}
	if action == "reload" {
		return "PHP-FPM configuration tested and reloaded", nil
	}
	return "PHP-FPM configuration tested and restarted", nil
}

func (c phpController) serviceState(ctx context.Context, unit string) (string, string) {
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := c.runner.run(commandCtx, "systemctl", "show", "--property=ActiveState", "--property=UnitFileState", unit)
	if err != nil {
		return "unknown", "unknown"
	}
	values := make(map[string]string, 2)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			values[key] = value
		}
	}
	if values["ActiveState"] == "" || values["UnitFileState"] == "" {
		return "unknown", "unknown"
	}
	return values["ActiveState"], values["UnitFileState"]
}

func (c phpController) testLocked(ctx context.Context, version string) error {
	binary := filepath.Join(c.binaryRoot, "php-fpm"+version)
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w: runtime binary is unavailable", errPHPConfigInvalid)
	}
	commandCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if _, err := c.runner.run(commandCtx, binary, "-t"); err != nil {
		return fmt.Errorf("%w: %v", errPHPConfigInvalid, err)
	}
	return nil
}

func (c phpController) serviceActionLocked(ctx context.Context, version, action string) error {
	if action != "reload" && action != "restart" {
		return errors.New("unsupported PHP-FPM service action")
	}
	commandCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if _, err := c.runner.run(commandCtx, "systemctl", action, "php"+version+"-fpm.service"); err != nil {
		return fmt.Errorf("PHP-FPM %s failed: %w", action, err)
	}
	return nil
}

func (c phpController) poolPath(version, pool string) (string, error) {
	if !agentPHPVersionPattern.MatchString(version) || !agentPHPPoolNamePattern.MatchString(pool) {
		return "", errors.New("invalid PHP-FPM version or pool")
	}
	return filepath.Join(c.configRoot, version, "fpm", "pool.d", pool+".conf"), nil
}

func readPHPPools(poolDir string) ([]phpFPMPool, error) {
	entries, err := os.ReadDir(poolDir)
	if err != nil {
		return nil, err
	}
	pools := make([]phpFPMPool, 0, len(entries))
	for _, entry := range entries {
		if len(pools) == maxPHPPools {
			break
		}
		name := strings.TrimSuffix(entry.Name(), ".conf")
		if !strings.HasSuffix(entry.Name(), ".conf") || !agentPHPPoolNamePattern.MatchString(name) || strings.Contains(entry.Name(), ".hserver-backup-") {
			continue
		}
		path := filepath.Join(poolDir, entry.Name())
		_, content, _, readErr := readManagedFile(path)
		if readErr != nil {
			continue
		}
		values := parsePHPConfigValues(string(content))
		maxChildren, _ := strconv.Atoi(values["pm.max_children"])
		pools = append(pools, phpFPMPool{
			Name: name, Path: path, User: values["user"], Group: values["group"], Listen: values["listen"], PM: values["pm"], MaxChildren: maxChildren,
		})
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].Name < pools[j].Name })
	return pools, nil
}

func parsePHPConfigValues(content string) map[string]string {
	values := make(map[string]string)
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, ";", 2)[0])
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if found {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

func checksumBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
