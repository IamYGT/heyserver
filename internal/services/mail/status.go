package mail

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ServiceControl runs a systemctl command (start/stop/restart) for the
// configured mail service.
// Returns an error if the command exits non-zero.
func (s *Service) StartService() error {
	if err := s.requireServiceName(); err != nil {
		return err
	}
	return runSystemctl("start", s.serviceName)
}

// StopService stops the configured mail systemd unit.
func (s *Service) StopService() error {
	if err := s.requireServiceName(); err != nil {
		return err
	}
	return runSystemctl("stop", s.serviceName)
}

// RestartService restarts the configured mail systemd unit.
func (s *Service) RestartService() error {
	if err := s.requireServiceName(); err != nil {
		return err
	}
	return runSystemctl("restart", s.serviceName)
}

func runSystemctl(action, unit string) error {
	if strings.TrimSpace(unit) == "" {
		return notConfigured("mail service name")
	}
	out, err := exec.Command("systemctl", action, unit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %w — %s", action, unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ConfigSection holds a named block of key-value pairs from config.toml.
type ConfigSection struct {
	Name   string            `json:"name"`
	Values map[string]string `json:"values"`
}

// ListenerInfo describes a single protocol listener parsed from config.toml.
type ListenerInfo struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
	Bind     string `json:"bind,omitempty"`
	Port     int    `json:"port,omitempty"`
	TLS      bool   `json:"tls"`
}

// StorageInfo describes the storage backend parsed from config.toml.
type StorageInfo struct {
	Backend   string `json:"backend"`
	Path      string `json:"path,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

// VersionInfo holds the parsed mail binary version string.
type VersionInfo struct {
	Raw     string `json:"raw"`
	Version string `json:"version"`
}

// GetConfig reads the configured Stalwart TOML file and returns selected sections
// (server, storage, directory, certificate) as ConfigSection slices.
func (s *Service) GetConfig() ([]ConfigSection, error) {
	if err := s.requireConfigPath(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.configPath)
	if err != nil {
		return nil, fmt.Errorf("open mail config: %w", err)
	}
	defer func() { _ = f.Close() }()

	interestingSections := map[string]bool{
		"server":      true,
		"storage":     true,
		"directory":   true,
		"certificate": true,
		"tracer":      true,
	}

	var sections []ConfigSection
	current := ""
	values := map[string]string{}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			// Flush previous section if interesting
			if current != "" && interestingSections[rootSection(current)] {
				sections = append(sections, ConfigSection{Name: current, Values: values})
			}
			current = line[1 : len(line)-1]
			values = map[string]string{}
			continue
		}

		if kv := strings.SplitN(line, "=", 2); len(kv) == 2 {
			key := strings.TrimSpace(kv[0])
			val := strings.Trim(strings.TrimSpace(kv[1]), `"`)
			values[key] = val
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	// Flush last section
	if current != "" && interestingSections[rootSection(current)] {
		sections = append(sections, ConfigSection{Name: current, Values: values})
	}

	return sections, nil
}

// rootSection extracts the first component of a dotted TOML section name.
// e.g. "server.listener.smtp" → "server"
func rootSection(name string) string {
	parts := strings.SplitN(name, ".", 2)
	return parts[0]
}

// GetVersion runs the configured mail binary with --version and returns the
// parsed output.
func (s *Service) GetVersion() (VersionInfo, error) {
	if err := s.requireBinary(); err != nil {
		return VersionInfo{}, err
	}
	out, err := exec.Command(s.binary, "--version").CombinedOutput()
	if err != nil {
		return VersionInfo{}, fmt.Errorf("mail binary %q unavailable: %w", s.binary, err)
	}
	raw := strings.TrimSpace(string(out))
	return VersionInfo{Raw: raw, Version: parseVersion(raw)}, nil
}

func parseVersion(raw string) string {
	// Typical output: "Stalwart Mail Server v0.9.3"
	for _, field := range strings.Fields(raw) {
		if strings.HasPrefix(field, "v") || strings.HasPrefix(field, "V") {
			return strings.TrimLeft(field, "vV")
		}
	}
	return raw
}

// GetListenerInfo parses config.toml for server.listener.* sections.
func (s *Service) GetListenerInfo() ([]ListenerInfo, error) {
	if err := s.requireConfigPath(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.configPath)
	if err != nil {
		return nil, fmt.Errorf("open mail config: %w", err)
	}
	defer func() { _ = f.Close() }()

	type raw struct {
		bind     string
		port     string
		protocol string
		tls      string
	}
	listeners := map[string]*raw{}
	var currentID string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := line[1 : len(line)-1]
			// Match [server.listener.ID] or [server.listeners.ID]
			if strings.HasPrefix(section, "server.listener") {
				parts := strings.Split(section, ".")
				if len(parts) >= 3 {
					currentID = parts[len(parts)-1]
					if _, ok := listeners[currentID]; !ok {
						listeners[currentID] = &raw{}
					}
				} else {
					currentID = ""
				}
			} else {
				currentID = ""
			}
			continue
		}

		if currentID == "" {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		r := listeners[currentID]
		switch key {
		case "bind":
			r.bind = val
		case "port":
			r.port = val
		case "protocol":
			r.protocol = val
		case "tls":
			r.tls = val
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	result := make([]ListenerInfo, 0, len(listeners))
	for id, r := range listeners {
		li := ListenerInfo{
			ID:       id,
			Protocol: r.protocol,
			Bind:     r.bind,
			TLS:      strings.EqualFold(r.tls, "true"),
		}
		if r.port != "" {
			if p, err := strconv.Atoi(r.port); err == nil {
				li.Port = p
			}
		}
		result = append(result, li)
	}
	return result, nil
}

// GetStorageInfo parses config.toml for the storage backend and, if it's a
// local filesystem path (RocksDB / SQLite), reports the disk usage.
func (s *Service) GetStorageInfo() (StorageInfo, error) {
	if err := s.requireConfigPath(); err != nil {
		return StorageInfo{}, err
	}
	f, err := os.Open(s.configPath)
	if err != nil {
		return StorageInfo{}, fmt.Errorf("open mail config: %w", err)
	}
	defer func() { _ = f.Close() }()

	info := StorageInfo{}
	inStorage := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section := strings.Trim(line, "[]")
			inStorage = section == "storage" || strings.HasPrefix(section, "store.")
			continue
		}
		if !inStorage {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		switch key {
		case "type", "backend":
			info.Backend = val
		case "path":
			info.Path = val
		}
	}
	if err := scanner.Err(); err != nil {
		return StorageInfo{}, fmt.Errorf("read config: %w", err)
	}

	// Attempt disk usage for local paths
	if info.Path != "" {
		info.SizeBytes = dirSize(info.Path)
	}
	return info, nil
}

// dirSize returns the total byte size of files under dir (best-effort).
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return total
}
