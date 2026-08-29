package php

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Extension represents a PHP extension with its status and metadata.
type Extension struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Version string `json:"version"`
	Type    string `json:"type"`    // "builtin" | "shared" | "zend"
	INIFile string `json:"ini_file"` // e.g. /etc/php/8.4/fpm/conf.d/20-json.ini
}

// coreExtensions are PHP built-in extensions that cannot be disabled via phpdismod.
// These are compiled directly into the PHP binary (php -n -m output).
var coreExtensions = map[string]bool{
	"core":       true,
	"date":       true,
	"ereg":       true,
	"filter":     true,
	"hash":       true,
	"json":       true,
	"libxml":     true,
	"openssl":    true,
	"pcntl":      true,
	"pcre":       true,
	"random":     true,
	"reflection": true,
	"session":    true,
	"sodium":     true,
	"spl":        true,
	"standard":   true,
	"zlib":       true,
}

// conflictingExtensions lists pairs of extensions that cannot coexist.
// Key = extension, Value = list of extensions it conflicts with.
var conflictingExtensions = map[string][]string{
	"opcache": {"xcache", "apc"},
	"xcache":  {"opcache", "apc"},
	"apc":     {"opcache", "xcache"},
}

// extensionDependencies maps extensions to their prerequisites.
var extensionDependencies = map[string][]string{
	"pdo_mysql":  {"pdo"},
	"pdo_pgsql":  {"pdo"},
	"pdo_sqlite": {"pdo"},
	"pdo_oci":    {"pdo"},
	"mysqli":     {"mysqlnd"},
	"simplexml":  {"libxml"},
	"dom":        {"libxml"},
	"xmlreader":  {"libxml"},
	"xmlwriter":  {"libxml"},
	"xsl":        {"libxml", "dom"},
}

// ---------- Public API ----------

// ListExtensions returns all extensions for the given PHP version, including
// both enabled (loaded) and available-but-disabled ones.
func (s *Service) ListExtensions(version string) ([]Extension, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}

	// 1. Get loaded extensions (what PHP currently sees)
	loaded, zend, err := getLoadedExtensions(version)
	if err != nil {
		return nil, err
	}

	// 2. Get builtin extensions (compiled-in, -n flag)
	builtins, err := getBuiltinExtensions(version)
	if err != nil {
		return nil, err
	}

	// 3. Get extensions available in mods-available/
	available, err := listModsAvailable(version)
	if err != nil {
		return nil, err
	}

	// 4. Get symlinked (enabled) extensions in conf.d/
	enabledIniFiles, err := listEnabledExtensionINIs(version)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var result []Extension

	// Add all available (mods-available) extensions first
	for extName, modPath := range available {
		lowerName := strings.ToLower(extName)
		seen[lowerName] = true

		enabled := loaded[lowerName] || loaded[extName]
		iniFile := enabledIniFiles[lowerName]
		extType := "shared"
		if builtins[lowerName] {
			extType = "builtin"
		}
		if zend[lowerName] {
			extType = "zend"
		}

		ver := getExtensionVersion(version, extName)
		_ = modPath

		result = append(result, Extension{
			Name:    extName,
			Enabled: enabled,
			Version: ver,
			Type:    extType,
			INIFile: iniFile,
		})
	}

	// Add loaded extensions that are not in mods-available (builtin/core)
	for extName := range loaded {
		if seen[extName] {
			continue
		}
		seen[extName] = true

		extType := "builtin"
		if zend[extName] {
			extType = "zend"
		}

		result = append(result, Extension{
			Name:    extName,
			Enabled: true,
			Version: getExtensionVersion(version, extName),
			Type:    extType,
			INIFile: enabledIniFiles[extName],
		})
	}

	return result, nil
}

// EnableExtension enables a PHP FPM extension using phpenmod.
// Validates dependencies and conflicts before enabling.
func (s *Service) EnableExtension(version, extName string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validateExtensionName(extName); err != nil {
		return err
	}
	if isCoreExtension(extName) {
		return fmt.Errorf("extension %q is built into PHP and cannot be managed via phpenmod", extName)
	}

	// Ensure it exists in mods-available
	modPath := fmt.Sprintf("/etc/php/%s/mods-available/%s.ini", version, extName)
	if _, err := os.Stat(modPath); os.IsNotExist(err) {
		return fmt.Errorf("extension %q not found in /etc/php/%s/mods-available/", extName, version)
	}

	// Check for dependency satisfaction
	if deps, ok := extensionDependencies[extName]; ok {
		loaded, _, err := getLoadedExtensions(version)
		if err != nil {
			return fmt.Errorf("checking dependencies: %w", err)
		}
		available, _ := listModsAvailable(version)
		for _, dep := range deps {
			lowerDep := strings.ToLower(dep)
			if !loaded[lowerDep] {
				// Auto-enable the dependency if it is available
				if _, avail := available[dep]; avail {
					if depErr := s.EnableExtension(version, dep); depErr != nil {
						return fmt.Errorf("auto-enabling dependency %q for %q failed: %w", dep, extName, depErr)
					}
				} else {
					return fmt.Errorf("extension %q requires %q which is not available; install php%s-%s first",
						extName, dep, version, dep)
				}
			}
		}
	}

	// Check for conflicts with currently enabled extensions
	if conflicts, ok := conflictingExtensions[extName]; ok {
		loaded, _, err := getLoadedExtensions(version)
		if err != nil {
			return fmt.Errorf("checking conflicts: %w", err)
		}
		for _, conflict := range conflicts {
			if loaded[strings.ToLower(conflict)] {
				return fmt.Errorf("extension %q conflicts with already-enabled %q; disable it first", extName, conflict)
			}
		}
	}

	cmd := exec.Command("phpenmod", "-v", version, "-s", "fpm", extName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("phpenmod %s %s: %w — %s", version, extName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DisableExtension disables a PHP FPM extension using phpdismod.
// Prevents disabling core extensions and checks for dependents.
func (s *Service) DisableExtension(version, extName string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validateExtensionName(extName); err != nil {
		return err
	}
	if isCoreExtension(extName) {
		return fmt.Errorf("extension %q is built into PHP and cannot be disabled", extName)
	}

	// Check if other enabled extensions depend on this one
	dependents, err := findDependents(version, extName)
	if err != nil {
		return fmt.Errorf("checking dependents: %w", err)
	}
	if len(dependents) > 0 {
		return fmt.Errorf("cannot disable %q: the following enabled extensions depend on it: %s",
			extName, strings.Join(dependents, ", "))
	}

	cmd := exec.Command("phpdismod", "-v", version, "-s", "fpm", extName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("phpdismod %s %s: %w — %s", version, extName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// GetExtensionInfo returns metadata for a single extension.
func (s *Service) GetExtensionInfo(version, extName string) (*Extension, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validateExtensionName(extName); err != nil {
		return nil, err
	}

	extensions, err := s.ListExtensions(version)
	if err != nil {
		return nil, err
	}

	lowerName := strings.ToLower(extName)
	for i := range extensions {
		if strings.ToLower(extensions[i].Name) == lowerName {
			return &extensions[i], nil
		}
	}
	return nil, fmt.Errorf("extension %q not found for php%s", extName, version)
}

// SearchAvailableExtensions searches apt-cache for installable PHP extensions
// matching the query. Returns package names (e.g. "php8.4-redis").
func (s *Service) SearchAvailableExtensions(version, query string) ([]string, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validateSearchQuery(query); err != nil {
		return nil, err
	}

	// Search for php{version}-{query}
	searchTerm := fmt.Sprintf("php%s-%s", version, query)
	cmd := exec.Command("apt-cache", "search", "--names-only", searchTerm)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("apt-cache search failed: %w", err)
	}

	var results []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Line format: "php8.4-redis - Redis extension for PHP"
		pkg := strings.Fields(line)[0]
		results = append(results, pkg)
	}
	return results, nil
}

// ---------- Internal helpers ----------

// getLoadedExtensions runs php{version} -m and returns loaded modules.
// Returns two maps: regular extensions and Zend extensions (OPcache, Xdebug, etc.).
func getLoadedExtensions(version string) (loaded map[string]bool, zend map[string]bool, err error) {
	binary := fmt.Sprintf("php%s", version)
	out, err := exec.Command(binary, "-m").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("php%s -m failed: %w", version, err)
	}

	loaded = make(map[string]bool)
	zend = make(map[string]bool)
	inZend := false

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "[PHP Modules]" {
			inZend = false
			continue
		}
		if line == "[Zend Modules]" {
			inZend = true
			continue
		}
		lowerLine := strings.ToLower(line)
		if inZend {
			// "Zend OPcache" → "zend opcache"
			zend[lowerLine] = true
			// Also add the short name (e.g. "opcache")
			parts := strings.Fields(lowerLine)
			if len(parts) > 1 {
				zend[parts[len(parts)-1]] = true
			}
		} else {
			loaded[lowerLine] = true
		}
	}
	return loaded, zend, scanner.Err()
}

// getBuiltinExtensions runs php{version} -n -m to get compiled-in extensions.
func getBuiltinExtensions(version string) (map[string]bool, error) {
	binary := fmt.Sprintf("php%s", version)
	out, err := exec.Command(binary, "-n", "-m").Output()
	if err != nil {
		return nil, fmt.Errorf("php%s -n -m failed: %w", version, err)
	}

	builtins := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		builtins[strings.ToLower(line)] = true
	}
	return builtins, nil
}

// listModsAvailable returns all available extensions in /etc/php/{version}/mods-available/.
// Returns map[extName]iniFilePath.
func listModsAvailable(version string) (map[string]string, error) {
	dir := fmt.Sprintf("/etc/php/%s/mods-available", version)
	entries, err := filepath.Glob(filepath.Join(dir, "*.ini"))
	if err != nil {
		return nil, fmt.Errorf("listing mods-available for php%s: %w", version, err)
	}

	result := make(map[string]string)
	for _, path := range entries {
		base := filepath.Base(path)
		name := strings.TrimSuffix(base, ".ini")
		result[name] = path
	}
	return result, nil
}

// listEnabledExtensionINIs returns a map of enabled extension names to their
// symlinked .ini files in /etc/php/{version}/fpm/conf.d/.
func listEnabledExtensionINIs(version string) (map[string]string, error) {
	dir := fmt.Sprintf("/etc/php/%s/fpm/conf.d", version)
	entries, err := filepath.Glob(filepath.Join(dir, "*.ini"))
	if err != nil {
		return nil, fmt.Errorf("listing conf.d for php%s fpm: %w", version, err)
	}

	// Pattern: 20-redis.ini → "redis"
	// Numbers are load order prefixes (10, 15, 20, 25)
	numPrefixRe := regexp.MustCompile(`^\d+-`)
	result := make(map[string]string)
	for _, path := range entries {
		base := filepath.Base(path)
		name := strings.TrimSuffix(base, ".ini")
		name = numPrefixRe.ReplaceAllString(name, "")
		result[strings.ToLower(name)] = path
	}
	return result, nil
}

// getExtensionVersion retrieves the version string for a loaded extension.
// Returns empty string if the extension is not loaded or has no version.
func getExtensionVersion(version, extName string) string {
	code := fmt.Sprintf(`echo phpversion(%q);`, extName)
	out, err := runPHPEval(version, code)
	if err != nil {
		return ""
	}
	ver := strings.TrimSpace(string(out))
	if ver == "" || ver == "false" {
		return ""
	}
	return ver
}

// findDependents returns the list of currently enabled extensions that depend on extName.
func findDependents(version, extName string) ([]string, error) {
	loaded, _, err := getLoadedExtensions(version)
	if err != nil {
		return nil, err
	}

	lowerName := strings.ToLower(extName)
	var dependents []string
	for ext, deps := range extensionDependencies {
		if !loaded[strings.ToLower(ext)] {
			continue
		}
		for _, dep := range deps {
			if strings.ToLower(dep) == lowerName {
				dependents = append(dependents, ext)
				break
			}
		}
	}
	return dependents, nil
}

// isCoreExtension returns true if the extension is compiled into PHP and
// cannot be managed via phpenmod/phpdismod.
func isCoreExtension(extName string) bool {
	return coreExtensions[strings.ToLower(extName)]
}

// validateExtensionName ensures an extension name is safe to pass to shell commands.
// Only lowercase alphanumeric and underscore allowed.
func validateExtensionName(name string) error {
	if name == "" {
		return fmt.Errorf("extension name cannot be empty")
	}
	re := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	if !re.MatchString(name) {
		return fmt.Errorf("invalid extension name %q: only alphanumeric and underscore allowed", name)
	}
	return nil
}

// validateSearchQuery prevents shell injection in apt-cache search queries.
func validateSearchQuery(query string) error {
	if query == "" {
		return fmt.Errorf("search query cannot be empty")
	}
	re := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !re.MatchString(query) {
		return fmt.Errorf("invalid search query %q: only alphanumeric, dash, and underscore allowed", query)
	}
	return nil
}
