package settings

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/store"
)

type Service struct {
	repo    *store.SettingsRepository
	version string
	now     func() time.Time
}

func New(repo *store.SettingsRepository, version string) *Service {
	return &Service{repo: repo, version: version, now: time.Now}
}

func (s *Service) Get(key, def string) (string, error) {
	return s.GetContext(context.Background(), key, def)
}
func (s *Service) GetContext(ctx context.Context, key, def string) (string, error) {
	setting, err := s.repo.GetContext(ctx, key)
	if err != nil {
		return def, err
	}
	if setting == nil {
		return def, nil
	}
	return setting.Value, nil
}
func (s *Service) GetAll() ([]models.Setting, error) { return s.GetAllContext(context.Background()) }
func (s *Service) GetAllContext(ctx context.Context) ([]models.Setting, error) {
	return s.repo.GetAllContext(ctx)
}
func (s *Service) Set(key, value string) error           { return s.repo.Set(key, value) }
func (s *Service) SetMany(pairs map[string]string) error { return s.repo.SetMany(pairs) }
func (s *Service) Delete(key string) error               { return s.repo.Delete(key) }

func (s *Service) SystemInfo() models.SystemInfo {
	return models.SystemInfo{
		OS:           readOS(),
		Kernel:       readKernel(),
		BootID:       readBootID(),
		Hostname:     sysHostname(),
		Arch:         runtime.GOARCH,
		Nginx:        nginxVersion(),
		PHP:          phpVersions(),
		PostgreSQL:   postgresVersion(),
		Interfaces:   networkInterfaces(),
		GoVersion:    runtime.Version(),
		NodeVersion:  nodeVersion(),
		BuildCommit:  config.BuildCommit,
		BuildDate:    config.BuildDate,
		PanelVersion: s.version,
		ProjectURL:   config.ProjectURL,
	}
}

var startedAt = time.Now()
var uptimeSecs int64

func (s *Service) Health() models.HealthStatus {
	secs := int64(time.Since(startedAt).Seconds())
	atomic.StoreInt64(&uptimeSecs, secs)
	return models.HealthStatus{Status: "ok", Version: s.version, Uptime: secs, BuildCommit: config.BuildCommit}
}

func nodeVersion() string {
	if out, err := exec.Command("node", "--version").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "not found"
}

func sysHostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}
func readBootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
func readOS() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return runtime.GOOS
}
func readKernel() string {
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "unknown"
}
func nginxVersion() string {
	if out, err := exec.Command("nginx", "-v").CombinedOutput(); err == nil {
		line := strings.TrimSpace(string(out))
		if idx := strings.Index(line, "/"); idx != -1 {
			return line[idx+1:]
		}
		return line
	}
	return "not found"
}
func phpVersions() []string {
	found := make([]string, 0)
	for _, bin := range []string{"php7.4", "php8.0", "php8.1", "php8.2", "php8.3", "php8.4", "php8.5"} {
		if out, err := exec.Command(bin, "--version").Output(); err == nil {
			parts := strings.Fields(strings.SplitN(string(out), "\n", 2)[0])
			if len(parts) >= 2 {
				found = append(found, fmt.Sprintf("%s (%s)", parts[1], bin))
			}
		}
	}
	return found
}
func postgresVersion() string {
	if out, err := exec.Command("psql", "--version").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("pg_config", "--version").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "not found"
}
func networkInterfaces() []models.NetInterface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return []models.NetInterface{}
	}
	out := make([]models.NetInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		addrStrs := make([]string, 0, len(addrs))
		for _, a := range addrs {
			addrStrs = append(addrStrs, a.String())
		}
		out = append(out, models.NetInterface{Name: iface.Name, Addrs: addrStrs, IsUp: iface.Flags&net.FlagUp != 0})
	}
	return out
}
