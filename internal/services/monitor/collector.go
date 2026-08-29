package monitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

const (
	// MetricInterval is how often system metrics are sampled.
	MetricInterval = 15 * time.Second

	// ProcessInterval is how often process snapshots are taken (normal).
	ProcessInterval = 60 * time.Second

	// ProcessIntervalSpike is the interval during CPU/RAM spikes.
	ProcessIntervalSpike = 15 * time.Second

	// SpikeThreshold — CPU or memory above this triggers faster process sampling.
	SpikeThreshold = 80.0

	// CompactionInterval is how often the compaction job runs.
	CompactionInterval = 30 * time.Minute

	// MaxDataDays is how many days of hourly data to keep.
	MaxDataDays = 30

	// MaxProcessDays is how many days of process snapshots to keep.
	MaxProcessDays = 7

	// MaxServiceDays is how many days of service history to keep.
	MaxServiceDays = 7
)

// Collector runs background goroutines that persist system metrics,
// process snapshots and service statuses into the metrics database.
type Collector struct {
	cache *Cache
	repo  *store.MetricsRepository
}

// NewCollector creates a Collector that reads from the monitor cache
// and writes to the metrics repository.
func NewCollector(cache *Cache, repo *store.MetricsRepository) *Collector {
	return &Collector{cache: cache, repo: repo}
}

// Start launches the collection and compaction loops. It blocks until ctx is cancelled.
func (c *Collector) Start(ctx context.Context) {
	slog.Info("metrics collector started",
		"metric_interval", MetricInterval,
		"process_interval", ProcessInterval,
		"compaction_interval", CompactionInterval,
	)

	metricTick := time.NewTicker(MetricInterval)
	processTick := time.NewTicker(ProcessInterval)
	compactTick := time.NewTicker(CompactionInterval)
	defer metricTick.Stop()
	defer processTick.Stop()
	defer compactTick.Stop()

	// Collect immediately on start, then on tick.
	c.collectMetrics()
	c.collectProcesses()
	c.collectServices()

	for {
		select {
		case <-ctx.Done():
			slog.Info("metrics collector stopped")
			return

		case <-metricTick.C:
			c.collectMetrics()

		case <-processTick.C:
			c.collectProcesses()
			c.collectServices()

			// Adaptive: if spike detected, reset process ticker to faster interval.
			if c.isSpike() {
				processTick.Reset(ProcessIntervalSpike)
			} else {
				processTick.Reset(ProcessInterval)
			}

		case <-compactTick.C:
			c.runCompaction()
		}
	}
}

// collectMetrics samples system stats and writes a row to system_metrics.
func (c *Collector) collectMetrics() {
	stats, err := c.cache.Stats()
	if err != nil {
		slog.Warn("metrics collect failed", "error", err)
		return
	}

	// Sum all network interfaces for total RX/TX.
	var totalRX, totalTX uint64
	for _, n := range stats.Net {
		totalRX += n.BytesIn
		totalTX += n.BytesOut
	}

	// Find root disk percentage.
	diskRootPct := 0.0
	for _, d := range stats.Disk {
		if d.Mount == "/" {
			diskRootPct = d.Percentage
			break
		}
	}

	row := &store.MetricRow{
		CPUPercent:   stats.CPU.Usage,
		MemTotal:     stats.Memory.Total,
		MemUsed:      stats.Memory.Used,
		MemPercent:   stats.Memory.Percentage,
		MemBuffers:   stats.Memory.Buffers,
		MemCached:    stats.Memory.Cached,
		MemAvailable: stats.Memory.Available,
		SwapTotal:    stats.Memory.SwapTotal,
		SwapUsed:     stats.Memory.SwapUsed,
		SwapPercent:  stats.Memory.SwapPercentage,
		Load1:        stats.Load[0],
		Load5:        stats.Load[1],
		Load15:       stats.Load[2],
		NetRX:        totalRX,
		NetTX:        totalTX,
		DiskRootPct:  diskRootPct,
	}

	if err := c.repo.InsertMetric(row); err != nil {
		slog.Warn("metrics insert failed", "error", err)
	}
}

// collectProcesses takes a snapshot of top processes and writes them.
func (c *Collector) collectProcesses() {
	procs, err := c.cache.Processes()
	if err != nil {
		slog.Warn("process snapshot failed", "error", err)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rows := make([]store.ProcessSnapshotRow, 0, len(procs))
	for _, p := range procs {
		rows = append(rows, store.ProcessSnapshotRow{
			PID:        p.PID,
			Username:   p.User,
			CPUPercent: p.CPU,
			MemPercent: p.Memory,
			RSS:        p.RSS,
			Command:    p.Command,
		})
	}

	if err := c.repo.InsertProcessBatch(now, rows); err != nil {
		slog.Warn("process batch insert failed", "error", err)
	}
}

// collectServices records current service statuses.
func (c *Collector) collectServices() {
	statuses, err := ServiceStatuses()
	if err != nil {
		slog.Warn("service status collect failed", "error", err)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rows := make([]store.ServiceStatusRow, 0, len(statuses))
	for _, s := range statuses {
		rows = append(rows, store.ServiceStatusRow{
			Name:   s.Name,
			Status: s.Status,
			PID:    s.PID,
		})
	}

	if err := c.repo.InsertServiceBatch(now, rows); err != nil {
		slog.Warn("service status batch insert failed", "error", err)
	}
}

// isSpike returns true if current CPU or memory usage is above SpikeThreshold.
func (c *Collector) isSpike() bool {
	stats, err := c.cache.Stats()
	if err != nil {
		return false
	}
	return stats.CPU.Usage >= SpikeThreshold || stats.Memory.Percentage >= SpikeThreshold
}

// runCompaction performs the three compaction steps:
// 1. Raw (>1h) → minute aggregates
// 2. Minute (>24h) → hourly aggregates
// 3. Prune old data (hourly >30d, process >7d, service >7d)
func (c *Collector) runCompaction() {
	n1, err := c.repo.CompactToMinute()
	if err != nil {
		slog.Warn("compact to minute failed", "error", err)
	} else if n1 > 0 {
		slog.Info("metrics compacted raw→minute", "deleted_rows", n1)
	}

	n2, err := c.repo.CompactToHourly()
	if err != nil {
		slog.Warn("compact to hourly failed", "error", err)
	} else if n2 > 0 {
		slog.Info("metrics compacted minute→hourly", "deleted_rows", n2)
	}

	if err := c.repo.PruneOldData(MaxDataDays, MaxProcessDays, MaxServiceDays); err != nil {
		slog.Warn("metrics prune failed", "error", err)
	}
}
