package monitor

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

// cpuSnapshot holds a single /proc/stat reading for delta calculation.
type cpuSnapshot struct {
	user    uint64
	nice    uint64
	system  uint64
	idle    uint64
	iowait  uint64
	irq     uint64
	softirq uint64
}

func (s cpuSnapshot) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq
}

func (s cpuSnapshot) active() uint64 {
	return s.total() - s.idle - s.iowait
}

// Cache holds a timestamped snapshot to avoid hammering /proc.
type Cache struct {
	mu                 sync.Mutex
	stats              *models.SystemStats
	processes          []models.ProcessInfo
	lastStatsFetch     time.Time
	lastProcessesFetch time.Time
	ttl                time.Duration
	lastCPUSnap        cpuSnapshot
	cpuSnapTime        time.Time
}

// New returns a Cache with the given TTL (recommended: 2s).
func New(ttl time.Duration) *Cache {
	c := &Cache{ttl: ttl}
	// Warm up the first CPU snapshot so the next call has a delta.
	snap, _ := readCPUStat()
	c.lastCPUSnap = snap
	c.cpuSnapTime = time.Now()
	return c
}

// Stats returns cached or freshly collected system stats.
func (c *Cache) Stats() (*models.SystemStats, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stats != nil && time.Since(c.lastStatsFetch) < c.ttl {
		return c.stats, nil
	}

	s, err := collect(c)
	if err != nil {
		return nil, err
	}
	c.stats = s
	c.lastStatsFetch = time.Now()
	return s, nil
}

// Processes returns cached or freshly collected process list.
func (c *Cache) Processes() ([]models.ProcessInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.processes != nil && time.Since(c.lastProcessesFetch) < c.ttl {
		return c.processes, nil
	}

	procs, err := topProcesses(15)
	if err != nil {
		return nil, err
	}
	c.processes = procs
	c.lastProcessesFetch = time.Now()
	return procs, nil
}

// collect gathers all system metrics.
func collect(c *Cache) (*models.SystemStats, error) {
	var wg sync.WaitGroup
	var (
		cpu    models.CPUStats
		mem    models.MemoryStats
		disks  []models.DiskStats
		nets   []models.NetStats
		load   [3]float64
		uptime int64
		host   string
		osName string
	)

	wg.Add(6)

	go func() {
		defer wg.Done()
		cpu = collectCPU(c)
	}()
	go func() {
		defer wg.Done()
		mem, _ = collectMemory()
	}()
	go func() {
		defer wg.Done()
		disks, _ = collectDisks()
	}()
	go func() {
		defer wg.Done()
		nets, _ = collectNetwork()
	}()
	go func() {
		defer wg.Done()
		load, _ = collectLoad()
		uptime, _ = collectUptime()
	}()
	go func() {
		defer wg.Done()
		host, _ = os.Hostname()
		osName = collectOS()
	}()

	wg.Wait()
	if disks == nil {
		disks = make([]models.DiskStats, 0)
	}
	if nets == nil {
		nets = make([]models.NetStats, 0)
	}

	return &models.SystemStats{
		CPU:    cpu,
		Memory: mem,
		Disk:   disks,
		Load:   load,
		Uptime: uptime,
		Host:   host,
		OS:     osName,
		Net:    nets,
	}, nil
}

// ── CPU ─────────────────────────────────────────────────────────────────────

func collectCPU(c *Cache) models.CPUStats {
	model := cpuModel()
	cores := runtime.NumCPU()

	snap, err := readCPUStat()
	if err != nil {
		return models.CPUStats{Model: model, Cores: cores}
	}

	elapsed := time.Since(c.cpuSnapTime).Seconds()
	usage := 0.0
	if elapsed > 0 {
		prevActive := c.lastCPUSnap.active()
		prevTotal := c.lastCPUSnap.total()
		currActive := snap.active()
		currTotal := snap.total()

		dTotal := currTotal - prevTotal
		dActive := currActive - prevActive
		if dTotal > 0 {
			usage = float64(dActive) / float64(dTotal) * 100
		}
	}

	c.lastCPUSnap = snap
	c.cpuSnapTime = time.Now()

	return models.CPUStats{
		Usage: roundFloat(usage, 2),
		Cores: cores,
		Model: model,
	}
}

// readCPUStat reads the aggregate cpu line from /proc/stat.
func readCPUStat() (cpuSnapshot, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSnapshot{}, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			break
		}
		parse := func(idx int) uint64 {
			v, _ := strconv.ParseUint(fields[idx], 10, 64)
			return v
		}
		return cpuSnapshot{
			user:    parse(1),
			nice:    parse(2),
			system:  parse(3),
			idle:    parse(4),
			iowait:  parse(5),
			irq:     parse(6),
			softirq: parse(7),
		}, nil
	}
	return cpuSnapshot{}, fmt.Errorf("cpu line not found in /proc/stat")
}

// cpuModel reads /proc/cpuinfo for the model name.
func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "unknown"
}

// ── Memory ───────────────────────────────────────────────────────────────────

func collectMemory() (models.MemoryStats, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return models.MemoryStats{}, err
	}
	defer func() { _ = f.Close() }()

	fields := map[string]uint64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		val, _ := strconv.ParseUint(parts[1], 10, 64)
		fields[key] = val * 1024 // kB → bytes
	}

	total := fields["MemTotal"]
	free := fields["MemFree"]
	buffers := fields["Buffers"]
	cached := fields["Cached"] + fields["SReclaimable"] - fields["Shmem"]
	used := total - free - buffers - cached
	pct := 0.0
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}

	swapTotal := fields["SwapTotal"]
	swapFree := fields["SwapFree"]
	swapUsed := swapTotal - swapFree
	swapPct := 0.0
	if swapTotal > 0 {
		swapPct = float64(swapUsed) / float64(swapTotal) * 100
	}

	available := fields["MemAvailable"]

	return models.MemoryStats{
		Total:          total,
		Used:           used,
		Free:           free,
		Percentage:     roundFloat(pct, 2),
		Buffers:        buffers,
		Cached:         cached,
		Available:      available,
		SwapTotal:      swapTotal,
		SwapUsed:       swapUsed,
		SwapFree:       swapFree,
		SwapPercentage: roundFloat(swapPct, 2),
	}, nil
}

// ── Disk ─────────────────────────────────────────────────────────────────────

func collectDisks() ([]models.DiskStats, error) {
	out, err := exec.Command("df", "-B1", "--output=target,size,used,avail,pcent", "-x", "tmpfs", "-x", "devtmpfs", "-x", "squashfs").Output()
	if err != nil {
		return nil, err
	}
	return parseDiskStats(string(out)), nil
}

func parseDiskStats(out string) []models.DiskStats {
	stats := make([]models.DiskStats, 0)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines[1:] { // skip header
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mount := fields[0]
		total, _ := strconv.ParseUint(fields[1], 10, 64)
		used, _ := strconv.ParseUint(fields[2], 10, 64)
		free, _ := strconv.ParseUint(fields[3], 10, 64)
		pct, parseErr := strconv.ParseFloat(strings.TrimSuffix(fields[4], "%"), 64)
		if parseErr != nil {
			denominator := used + free
			if denominator > 0 {
				pct = float64(used) / float64(denominator) * 100
			}
		}
		stats = append(stats, models.DiskStats{
			Mount:      mount,
			Total:      total,
			Used:       used,
			Free:       free,
			Percentage: roundFloat(pct, 2),
		})
	}
	return stats
}

// ── Network ──────────────────────────────────────────────────────────────────

func collectNetwork() ([]models.NetStats, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	stats := make([]models.NetStats, 0)
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 { // skip header rows
			continue
		}
		line := scanner.Text()
		// Format: "  eth0:  12345  0  0  0  0  0  0  0  67890  0  0  0  0  0  0  0"
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colonIdx])
		// Skip loopback
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[colonIdx+1:])
		if len(fields) < 9 {
			continue
		}
		rxBytes, _ := strconv.ParseUint(fields[0], 10, 64)
		txBytes, _ := strconv.ParseUint(fields[8], 10, 64)
		stats = append(stats, models.NetStats{
			Interface: iface,
			BytesIn:   rxBytes,
			BytesOut:  txBytes,
		})
	}
	return stats, nil
}

// ── Load average ─────────────────────────────────────────────────────────────

func collectLoad() ([3]float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return [3]float64{}, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return [3]float64{}, fmt.Errorf("unexpected /proc/loadavg format")
	}
	var load [3]float64
	for i := range 3 {
		load[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return load, nil
}

// ── Uptime ───────────────────────────────────────────────────────────────────

func collectUptime() (int64, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0, fmt.Errorf("unexpected /proc/uptime format")
	}
	f, _ := strconv.ParseFloat(fields[0], 64)
	return int64(f), nil
}

// ── OS info ──────────────────────────────────────────────────────────────────

func collectOS() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			val := strings.TrimPrefix(line, "PRETTY_NAME=")
			val = strings.Trim(val, `"`)
			return val
		}
	}
	return runtime.GOOS
}

// ── Top processes ─────────────────────────────────────────────────────────────

// topProcesses returns up to n processes sorted by CPU usage descending.
func topProcesses(n int) ([]models.ProcessInfo, error) {
	out, err := exec.Command(
		"ps", "aux",
		"--sort=-pcpu",
		"--no-headers",
		"-ww",
	).Output()
	if err != nil {
		return nil, err
	}

	var procs []models.ProcessInfo
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, line := range lines {
		if i >= n {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}
		cpu, _ := strconv.ParseFloat(fields[2], 64)
		mem, _ := strconv.ParseFloat(fields[3], 64)
		pid, _ := strconv.Atoi(fields[1])
		startTime, startErr := processStartTime(pid)
		if startErr != nil {
			// The process exited or changed while ps output was being parsed.
			// Do not publish an action target without a stable identity.
			continue
		}
		vsz, _ := strconv.ParseUint(fields[4], 10, 64)
		rss, _ := strconv.ParseUint(fields[5], 10, 64)
		cmd := strings.Join(fields[10:], " ")

		procs = append(procs, models.ProcessInfo{
			PID:       pid,
			StartTime: startTime,
			User:      fields[0],
			CPU:       roundFloat(cpu, 1),
			Memory:    roundFloat(mem, 1),
			VSZ:       vsz * 1024, // kB → bytes
			RSS:       rss * 1024, // kB → bytes
			Stat:      fields[7],
			Command:   cmd,
		})
	}
	return procs, nil
}

func processStartTime(pid int) (uint64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	return parseProcessStartTime(raw)
}

func parseProcessStartTime(raw []byte) (uint64, error) {
	// Field 2 (comm) is parenthesized and may contain spaces or ')', so split
	// after its final closing parenthesis. The remaining slice begins at field 3.
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return 0, fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) <= 19 {
		return 0, fmt.Errorf("process stat is missing start time")
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || startTime == 0 {
		return 0, fmt.Errorf("invalid process start time")
	}
	return startTime, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func roundFloat(f float64, decimals int) float64 {
	pow := 1.0
	for range decimals {
		pow *= 10
	}
	return float64(int(f*pow+0.5)) / pow
}
