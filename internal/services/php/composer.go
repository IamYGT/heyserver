package php

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const composerTimeout = 300 * time.Second

// ErrComposerProjectPath identifies caller-correctable project path failures.
var ErrComposerProjectPath = errors.New("invalid Composer project path")

// ComposerResult holds the outcome of a Composer command.
type ComposerResult struct {
	Success  bool     `json:"success"`
	Output   string   `json:"output"`
	Errors   []string `json:"errors"`
	Duration float64  `json:"duration_seconds"`
}

// ComposerPackage represents a single package in the outdated list.
type ComposerPackage struct {
	Name           string `json:"name"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Description    string `json:"description"`
	Abandoned      bool   `json:"abandoned"`
}

// ComposerVersion returns the installed Composer version string.
func (s *Service) ComposerVersion() (string, error) {
	composerPath, err := findComposer()
	if err != nil {
		return "", err
	}
	out, err := exec.Command(composerPath, "--version", "--no-ansi").Output()
	if err != nil {
		return "", fmt.Errorf("running composer --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ComposerInstall runs `composer install --no-interaction` in projectDir using phpVersion.
func (s *Service) ComposerInstall(projectDir, phpVersion string) (*ComposerResult, error) {
	return s.runComposer(projectDir, phpVersion, "install",
		"--no-interaction", "--no-progress", "--prefer-dist",
	)
}

// ComposerUpdate runs `composer update --no-interaction` in projectDir using phpVersion.
func (s *Service) ComposerUpdate(projectDir, phpVersion string) (*ComposerResult, error) {
	return s.runComposer(projectDir, phpVersion, "update",
		"--no-interaction", "--no-progress",
	)
}

// ComposerRequire adds a package to composer.json and runs install.
func (s *Service) ComposerRequire(projectDir, phpVersion, packageName string) (*ComposerResult, error) {
	if err := validatePackageName(packageName); err != nil {
		return nil, err
	}
	return s.runComposer(projectDir, phpVersion, "require",
		"--no-interaction", "--no-progress", packageName,
	)
}

// ComposerRemove removes a package from composer.json.
func (s *Service) ComposerRemove(projectDir, phpVersion, packageName string) (*ComposerResult, error) {
	if err := validatePackageName(packageName); err != nil {
		return nil, err
	}
	return s.runComposer(projectDir, phpVersion, "remove",
		"--no-interaction", packageName,
	)
}

// ComposerOutdated returns a list of packages that have newer versions available.
// It calls `composer outdated --format=json --no-interaction`.
func (s *Service) ComposerOutdated(projectDir, phpVersion string) ([]ComposerPackage, error) {
	result, err := s.runComposer(projectDir, phpVersion, "outdated",
		"--no-interaction", "--format=json", "--no-ansi",
	)
	if err != nil {
		return nil, err
	}

	// composer outdated --format=json outputs to stdout; parse it.
	packages, parseErr := parseOutdatedJSON(result.Output)
	if parseErr != nil {
		// Return whatever partial data we have along with original output for debugging.
		return nil, fmt.Errorf("parsing composer outdated output: %w (raw: %.200s)", parseErr, result.Output)
	}
	return packages, nil
}

// ComposerDumpAutoload regenerates the Composer autoloader.
func (s *Service) ComposerDumpAutoload(projectDir, phpVersion string) error {
	_, err := s.runComposer(projectDir, phpVersion, "dump-autoload",
		"--no-interaction", "--optimize",
	)
	return err
}

// ---------- internal helpers ----------

// runComposer executes a Composer command in projectDir via the domain's system user.
func (s *Service) runComposer(projectDir, phpVersion string, args ...string) (*ComposerResult, error) {
	if err := s.validateComposerProjectDir(projectDir); err != nil {
		return nil, err
	}
	if err := validateVersion(phpVersion); err != nil {
		return nil, err
	}

	composerPath, err := findComposer()
	if err != nil {
		return nil, err
	}

	phpBin := fmt.Sprintf("/usr/bin/php%s", phpVersion)
	if _, statErr := os.Stat(phpBin); os.IsNotExist(statErr) {
		return nil, fmt.Errorf("PHP binary not found: %s", phpBin)
	}

	// Determine the system user that owns the project directory.
	user, err := resolveDirectoryOwner(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving directory owner for %s: %w", projectDir, err)
	}

	// Build argument list:
	// sudo -u <user> /usr/bin/phpX.Y /path/to/composer <cmd> --working-dir=<dir> [extra args...]
	fullArgs := []string{"-u", user, phpBin, composerPath}
	fullArgs = append(fullArgs, args[0]) // command (install / update / etc.)
	fullArgs = append(fullArgs, "--working-dir="+projectDir)
	if len(args) > 1 {
		fullArgs = append(fullArgs, args[1:]...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), composerTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sudo", fullArgs...)
	cmd.Dir = projectDir

	// Capture stdout and stderr separately so we can report them independently.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start).Seconds()

	result := &ComposerResult{
		Success:  runErr == nil,
		Output:   stdout.String(),
		Duration: duration,
	}

	// Parse stderr lines into the Errors slice.
	if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
		for _, line := range strings.Split(stderrStr, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				result.Errors = append(result.Errors, trimmed)
			}
		}
	}

	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return result, fmt.Errorf("composer command timed out after %s", composerTimeout)
		}
		return result, fmt.Errorf("composer %s failed: %w", args[0], runErr)
	}

	return result, nil
}

// findComposer returns the path to the Composer executable.
func findComposer() (string, error) {
	candidates := []string{
		"/usr/local/bin/composer",
		"/usr/bin/composer",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("composer not found (checked: %s)", strings.Join(candidates, ", "))
}

// validateComposerProjectDir ensures projectDir stays inside this installation's
// vhost root, including after resolving symlinks.
func (s *Service) validateComposerProjectDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("%w: project directory cannot be empty", ErrComposerProjectPath)
	}
	root, err := s.requireVhostsRoot()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrComposerProjectPath, err)
	}

	clean := filepath.Clean(dir)
	if !filepath.IsAbs(clean) || !pathWithinRoot(clean, root) {
		return fmt.Errorf("%w: project directory %q is outside configured vhosts root %s", ErrComposerProjectPath, clean, root)
	}

	info, err := os.Stat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: project directory does not exist: %s", ErrComposerProjectPath, clean)
		}
		return fmt.Errorf("%w: accessing project directory: %v", ErrComposerProjectPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: project path is not a directory: %s", ErrComposerProjectPath, clean)
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("%w: resolving configured vhosts root: %v", ErrComposerProjectPath, err)
	}
	resolvedProject, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return fmt.Errorf("%w: resolving project directory: %v", ErrComposerProjectPath, err)
	}
	if !pathWithinRoot(resolvedProject, resolvedRoot) {
		return fmt.Errorf("%w: project directory %q resolves outside configured vhosts root %s", ErrComposerProjectPath, clean, root)
	}

	return nil
}

func pathWithinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// validatePackageName performs basic validation on a Composer package name.
// Expected format: vendor/package or vendor/package:^version
func validatePackageName(name string) error {
	if name == "" {
		return fmt.Errorf("package name cannot be empty")
	}
	// Must contain exactly one slash (vendor/package) or one slash with optional version constraint.
	packagePart, _, _ := strings.Cut(name, ":")
	if !strings.Contains(packagePart, "/") {
		return fmt.Errorf("invalid package name %q: expected vendor/package format", name)
	}
	// Reject shell meta-characters.
	forbidden := []string{";", "&", "|", "`", "$", "(", ")", "<", ">", "\n", "\r"}
	for _, c := range forbidden {
		if strings.Contains(name, c) {
			return fmt.Errorf("invalid package name %q: contains forbidden character %q", name, c)
		}
	}
	return nil
}

// resolveDirectoryOwner returns the username of the owner of the given directory.
func resolveDirectoryOwner(dir string) (string, error) {
	// Use stat --format=%U to get the owner username portably.
	out, err := exec.Command("stat", "--format=%U", dir).Output()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", dir, err)
	}
	owner := strings.TrimSpace(string(out))
	if owner == "" || owner == "UNKNOWN" {
		return "", fmt.Errorf("could not determine owner of %s", dir)
	}
	// Never run as root — fall back to www-data for safety.
	if owner == "root" {
		return "www-data", nil
	}
	return owner, nil
}

// composerOutdatedJSON is the shape of `composer outdated --format=json` output.
type composerOutdatedJSON struct {
	Installed []struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Latest      string `json:"latest"`
		Description string `json:"description"`
		Abandoned   any    `json:"abandoned"` // bool or string replacement name
	} `json:"installed"`
}

// parseOutdatedJSON converts the JSON output of `composer outdated --format=json`
// into a slice of ComposerPackage.
func parseOutdatedJSON(raw string) ([]ComposerPackage, error) {
	// Composer may emit progress noise before the JSON block; find the first '{'.
	idx := strings.Index(raw, "{")
	if idx < 0 {
		// No packages outdated — Composer prints nothing or a short message.
		return []ComposerPackage{}, nil
	}
	jsonStr := raw[idx:]

	var out composerOutdatedJSON
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return nil, err
	}

	packages := make([]ComposerPackage, 0, len(out.Installed))
	for _, p := range out.Installed {
		abandoned := false
		if p.Abandoned != nil {
			switch v := p.Abandoned.(type) {
			case bool:
				abandoned = v
			case string:
				abandoned = v != ""
			}
		}
		packages = append(packages, ComposerPackage{
			Name:           p.Name,
			CurrentVersion: p.Version,
			LatestVersion:  p.Latest,
			Description:    p.Description,
			Abandoned:      abandoned,
		})
	}
	return packages, nil
}
