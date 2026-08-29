package monitor

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

func TestParseProcessStartTimeHandlesSpacesAndClosingParen(t *testing.T) {
	raw := []byte("123 (worker name)) S" + strings.Repeat(" 0", 18) + " 987654 0\n")
	got, err := parseProcessStartTime(raw)
	if err != nil {
		t.Fatalf("parseProcessStartTime: %v", err)
	}
	if got != 987654 {
		t.Fatalf("start time = %d", got)
	}
}

func TestClassifySystemdStateDetectsRestartLoop(t *testing.T) {
	status, detail := classifySystemdState(map[string]string{
		"ActiveState": "activating",
		"SubState":    "start",
		"NRestarts":   "78",
	})
	if status != "degraded" {
		t.Fatalf("status = %q, want degraded", status)
	}
	if !strings.Contains(detail, "78 automatic restarts") {
		t.Fatalf("detail = %q, want restart count", detail)
	}
}

func TestClassifySystemdStateKeepsNormalActivationTransient(t *testing.T) {
	status, detail := classifySystemdState(map[string]string{
		"ActiveState": "activating",
		"SubState":    "start",
		"NRestarts":   "0",
	})
	if status != "starting" || detail != "" {
		t.Fatalf("got (%q, %q), want (starting, empty)", status, detail)
	}
}

func TestCPUSnapshot_math(t *testing.T) {
	t.Parallel()

	s := cpuSnapshot{
		user:    100,
		nice:    10,
		system:  50,
		idle:    200,
		iowait:  20,
		irq:     5,
		softirq: 5,
	}

	total := s.total()
	wantTotal := uint64(390)
	if total != wantTotal {
		t.Errorf("total() = %d, want %d", total, wantTotal)
	}

	active := s.active()
	wantActive := uint64(170)
	if active != wantActive {
		t.Errorf("active() = %d, want %d", active, wantActive)
	}
}

func TestRoundFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    float64
		decimals int
		want     float64
	}{
		{"two decimals round up", 12.345, 2, 12.35},
		{"two decimals round down", 12.344, 2, 12.34},
		{"zero decimals", 9.6, 0, 10},
		{"negative value", -1.234, 2, -1.22},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := roundFloat(tc.value, tc.decimals)
			if got != tc.want {
				t.Errorf("roundFloat(%v, %d) = %v, want %v", tc.value, tc.decimals, got, tc.want)
			}
		})
	}
}

func TestReadCPUStat_linux(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("/proc/stat not available")
	}

	snap, err := readCPUStat()
	if err != nil {
		t.Fatalf("readCPUStat: %v", err)
	}
	if snap.total() == 0 {
		t.Error("expected non-zero cpu total jiffies")
	}
	if snap.idle == 0 {
		t.Error("expected idle counter > 0")
	}
}

func TestCollectLoad_linux(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/proc/loadavg"); err != nil {
		t.Skip("/proc/loadavg not available")
	}

	load, err := collectLoad()
	if err != nil {
		t.Fatalf("collectLoad: %v", err)
	}
	for i, v := range load {
		if v < 0 {
			t.Errorf("load[%d] = %v, want >= 0", i, v)
		}
	}
}

func TestCollectUptime_linux(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/proc/uptime"); err != nil {
		t.Skip("/proc/uptime not available")
	}

	uptime, err := collectUptime()
	if err != nil {
		t.Fatalf("collectUptime: %v", err)
	}
	if uptime <= 0 {
		t.Errorf("uptime = %d, want > 0", uptime)
	}
}

func TestCollectMemory_linux(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/proc/meminfo"); err != nil {
		t.Skip("/proc/meminfo not available")
	}

	mem, err := collectMemory()
	if err != nil {
		t.Fatalf("collectMemory: %v", err)
	}
	if mem.Total == 0 {
		t.Error("MemTotal should be > 0")
	}
	if mem.Percentage < 0 || mem.Percentage > 100 {
		t.Errorf("Percentage = %v, want 0-100", mem.Percentage)
	}
}

func TestParseDiskStatsUsesDFPercentageInsteadOfTotalBlocks(t *testing.T) {
	stats := parseDiskStats("Mounted on 1B-blocks Used Available Use%\n/ 1000 700 200 78%\n")
	if len(stats) != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	root := stats[0]
	if root.Mount != "/" || root.Total != 1000 || root.Used != 700 || root.Free != 200 {
		t.Fatalf("root = %#v", root)
	}
	if root.Percentage != 78 {
		t.Fatalf("percentage = %v, want df value 78; used/total would incorrectly report 70", root.Percentage)
	}
}

func TestParseDiskStatsReturnsEmptyCollection(t *testing.T) {
	stats := parseDiskStats("Mounted on 1B-blocks Used Available Use%\n")
	if stats == nil || len(stats) != 0 {
		t.Fatalf("stats = %#v, want non-null empty collection", stats)
	}
}

func TestCPUModel_linux(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/proc/cpuinfo"); err != nil {
		t.Skip("/proc/cpuinfo not available")
	}

	model := cpuModel()
	if model == "" {
		t.Error("cpu model should not be empty")
	}
}

func TestCache_statsTTL(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("/proc/stat not available")
	}

	c := New(2 * time.Second)

	first, err := c.Stats()
	if err != nil {
		t.Fatalf("Stats first: %v", err)
	}

	second, err := c.Stats()
	if err != nil {
		t.Fatalf("Stats second: %v", err)
	}
	if first != second {
		t.Error("expected cached stats pointer within TTL")
	}

	time.Sleep(2100 * time.Millisecond)

	third, err := c.Stats()
	if err != nil {
		t.Fatalf("Stats third: %v", err)
	}
	if third == first {
		t.Error("expected refreshed stats after TTL expiry")
	}
}

func TestCacheProcessTTLIsIndependentFromStatsRefresh(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/proc"); err != nil {
		t.Skip("/proc not available")
	}

	c := New(time.Hour)
	c.processes = []models.ProcessInfo{{PID: -1, Command: "stale-sentinel"}}
	c.lastStatsFetch = time.Now()

	processes, err := c.Processes()
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}
	if len(processes) == 1 && processes[0].PID == -1 {
		t.Fatal("process cache was kept stale by an unrelated stats refresh")
	}
	if c.lastProcessesFetch.IsZero() {
		t.Fatal("process refresh timestamp was not recorded")
	}
}

func TestConfiguredWatchedServicesUsesDeclaredPM2Owner(t *testing.T) {
	t.Setenv("HSERVER_PM2_USER", "deploy")
	services := configuredWatchedServices()
	if !containsString(services, "pm2-deploy") {
		t.Fatalf("configured services = %v, want pm2-deploy", services)
	}

	t.Setenv("HSERVER_PM2_USER", "")
	for _, service := range configuredWatchedServices() {
		if strings.HasPrefix(service, "pm2-") {
			t.Fatalf("unconfigured PM2 service leaked into defaults: %q", service)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
