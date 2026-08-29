package monitor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/pm2"
)

// WatchedServices is the list of system services the panel monitors. PM2 is
// included only when the installation declares its unprivileged owner.
var WatchedServices = configuredWatchedServices()

func configuredWatchedServices() []string {
	services := []string{
		"nginx",
		"php8.4-fpm",
		"php8.5-fpm",
		"php7.4-fpm",
		"postgresql",
		"mariadb",
		"redis-server",
	}
	if service, ok := pm2.SystemdServiceName(os.Getenv("HSERVER_PM2_USER")); ok {
		services = append(services, service)
	}
	return services
}

// serviceCache holds a short-lived list of service statuses.
type serviceCache struct {
	mu        sync.Mutex
	statuses  []models.ServiceStatus
	lastFetch time.Time
	ttl       time.Duration
}

var svcCache = &serviceCache{ttl: 5 * time.Second}

// ServiceStatuses returns cached or freshly polled service statuses.
func ServiceStatuses() ([]models.ServiceStatus, error) {
	svcCache.mu.Lock()
	defer svcCache.mu.Unlock()

	if svcCache.statuses != nil && time.Since(svcCache.lastFetch) < svcCache.ttl {
		return svcCache.statuses, nil
	}

	statuses, err := checkAllServices()
	if err != nil {
		return nil, err
	}
	svcCache.statuses = statuses
	svcCache.lastFetch = time.Now()
	return statuses, nil
}

// checkAllServices runs systemctl queries concurrently for all watched services.
func checkAllServices() ([]models.ServiceStatus, error) {
	results := make([]models.ServiceStatus, len(WatchedServices))
	var wg sync.WaitGroup

	for i, name := range WatchedServices {
		wg.Add(1)
		go func(idx int, svcName string) {
			defer wg.Done()
			results[idx] = checkService(svcName)
		}(i, name)
	}

	wg.Wait()
	return results, nil
}

// checkService queries systemd for a single service's status.
// Uses `systemctl show` for rich info — exit code 0 even when stopped.
func checkService(name string) models.ServiceStatus {
	out, err := exec.Command(
		"systemctl", "show", name,
		"--property=ActiveState,SubState,MainPID,ActiveEnterTimestamp,NRestarts,Result",
		"--no-pager",
	).Output()

	status := models.ServiceStatus{Name: name}

	if err != nil {
		// systemctl not available or unit not found
		status.Status = "unknown"
		return status
	}

	props := parseKeyValue(string(out))

	status.Status, status.Detail = classifySystemdState(props)

	if pidStr, ok := props["MainPID"]; ok {
		pid, _ := strconv.Atoi(pidStr)
		if pid > 0 {
			status.PID = pid
		}
	}

	if ts, ok := props["ActiveEnterTimestamp"]; ok && ts != "" && ts != "0" {
		status.Uptime = formatUptimeSince(ts)
	}

	// If MainPID is 0 but service shows active, try reading PID from cgroup / pidfile.
	if status.PID == 0 && status.Status == "running" {
		status.PID = pidFromProc(name)
	}

	// systemd being active only proves that a process exists. PostgreSQL can
	// remain active while crash recovery is rejecting every client connection;
	// report that state as degraded instead of presenting a false green status.
	if status.Status == "running" {
		if ready, detail := probeServiceReadiness(name); !ready {
			status.Status = "degraded"
			status.Detail = detail
		}
	}

	return status
}

func classifySystemdState(props map[string]string) (string, string) {
	activeState := props["ActiveState"]
	subState := props["SubState"]
	restarts, _ := strconv.Atoi(props["NRestarts"])

	if activeState == "activating" && restarts > 0 {
		return "degraded", fmt.Sprintf(
			"systemd restart loop: %d automatic restarts; state %s/%s",
			restarts, activeState, subState,
		)
	}

	switch activeState {
	case "active":
		return "running", ""
	case "activating":
		return "starting", ""
	case "deactivating":
		return "stopping", ""
	case "failed":
		detail := "systemd unit failed"
		if result := props["Result"]; result != "" && result != "success" {
			detail += ": " + result
		}
		return "failed", detail
	case "inactive":
		return "stopped", ""
	case "":
		return "unknown", ""
	default:
		return activeState, ""
	}
}

func probeServiceReadiness(name string) (bool, string) {
	switch name {
	case "postgresql":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "pg_isready", "-h", "/var/run/postgresql", "-p", "5432").CombinedOutput()
		detail := strings.TrimSpace(string(out))
		if err == nil {
			return true, "accepting connections"
		}
		if ctx.Err() != nil {
			return false, "readiness probe timed out"
		}
		if detail == "" {
			detail = "not accepting connections"
		}
		return false, detail
	default:
		return true, ""
	}
}

// parseKeyValue parses "Key=Value\n..." output from systemctl show.
func parseKeyValue(output string) map[string]string {
	m := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		m[line[:idx]] = line[idx+1:]
	}
	return m
}

// formatUptimeSince converts a systemd timestamp string like
// "Mon 2026-04-07 12:00:00 UTC" into a human-readable uptime duration.
func formatUptimeSince(ts string) string {
	// systemd timestamp layout varies by locale; parse as best-effort.
	// Try common format: "DayName YYYY-MM-DD HH:MM:SS TZ"
	parts := strings.Fields(ts)
	if len(parts) < 3 {
		return ""
	}
	// Attempt parse: "YYYY-MM-DD HH:MM:SS"
	combined := parts[1] + " " + parts[2]
	const layout = "2006-01-02 15:04:05"
	t, err := time.Parse(layout, combined)
	if err != nil {
		return ""
	}

	d := time.Since(t)
	return formatDuration(d)
}

// formatDuration converts a duration into a human string like "2d 3h 5m".
func formatDuration(d time.Duration) string {
	if d < 0 {
		return ""
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// pidFromProc attempts to find a PID for a known service by scanning /proc.
// Only used as fallback when MainPID is unavailable.
func pidFromProc(serviceName string) int {
	// Strip common suffixes to get base binary name
	binary := strings.TrimSuffix(serviceName, ".service")
	// nginx → nginx, php8.4-fpm → php-fpm8.4, redis-server → redis-server
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if strings.Contains(name, binary) || strings.Contains(binary, name) {
			return pid
		}
	}
	return 0
}
