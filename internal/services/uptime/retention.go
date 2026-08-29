package uptime

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/store"
)

// ── HeartbeatBatcher ─────────────────────────────────────────────────────────

// HeartbeatBatcher collects heartbeats and flushes them in batches.
type HeartbeatBatcher struct {
	repo   *store.UptimeRepository
	mu     sync.Mutex
	buffer []store.UptimeHeartbeat
}

func NewHeartbeatBatcher(repo *store.UptimeRepository) *HeartbeatBatcher {
	return &HeartbeatBatcher{repo: repo}
}

func (b *HeartbeatBatcher) Add(h store.UptimeHeartbeat) {
	b.mu.Lock()
	b.buffer = append(b.buffer, h)
	shouldFlush := len(b.buffer) >= 100
	b.mu.Unlock()

	if shouldFlush {
		b.Flush()
	}
}

func (b *HeartbeatBatcher) Flush() {
	b.mu.Lock()
	if len(b.buffer) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.buffer
	b.buffer = nil
	b.mu.Unlock()

	if err := b.repo.InsertHeartbeatBatch(batch); err != nil {
		slog.Error("uptime: heartbeat batch insert failed", "count", len(batch), "error", err)
		// Put them back
		b.mu.Lock()
		b.buffer = append(batch, b.buffer...)
		b.mu.Unlock()
	}
}

func (b *HeartbeatBatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			b.Flush() // Final flush on shutdown
			return
		case <-ticker.C:
			b.Flush()
		}
	}
}

// ── RetentionWorker ──────────────────────────────────────────────────────────

// RetentionWorker compacts and prunes old heartbeat data.
type RetentionWorker struct {
	repo     *store.UptimeRepository
	settings *settings.Service
}

func NewRetentionWorker(repo *store.UptimeRepository, settingsSvc ...*settings.Service) *RetentionWorker {
	worker := &RetentionWorker{repo: repo}
	if len(settingsSvc) > 0 {
		worker.settings = settingsSvc[0]
	}
	return worker
}

func (rw *RetentionWorker) Run(ctx context.Context) {
	// Run once at startup
	rw.compact()

	// Then run daily at 03:00 UTC
	for {
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 3, 0, 0, 0, time.UTC)
		if now.Hour() < 3 {
			next = time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			rw.compact()
		}
	}
}

func (rw *RetentionWorker) compact() {
	compactAfterDays := rw.settingDays("uptime_compact_after_days", 30, 1, 365)
	retentionDays := rw.settingDays("uptime_retention_days", 90, 2, 3650)
	if compactAfterDays >= retentionDays {
		slog.Warn("uptime: invalid retention settings; using safe defaults",
			"compact_after_days", compactAfterDays, "retention_days", retentionDays)
		compactAfterDays, retentionDays = 30, 90
	}
	compacted, err := rw.repo.CompactHeartbeats(compactAfterDays)
	if err != nil {
		slog.Error("uptime: compaction failed", "error", err)
	} else if compacted > 0 {
		slog.Info("uptime: compacted heartbeats", "deleted", compacted)
	}

	pruned, err := rw.repo.PruneHourlyAggregates(retentionDays)
	if err != nil {
		slog.Error("uptime: hourly prune failed", "error", err)
	} else if pruned > 0 {
		slog.Info("uptime: pruned hourly aggregates", "deleted", pruned)
	}
}

func (rw *RetentionWorker) settingDays(key string, fallback, minimum, maximum int) int {
	if rw.settings == nil {
		return fallback
	}
	value, err := rw.settings.Get(key, strconv.Itoa(fallback))
	if err != nil {
		slog.Warn("uptime: failed to read retention setting", "key", key, "error", err)
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		slog.Warn("uptime: invalid retention setting", "key", key, "value", value)
		return fallback
	}
	return parsed
}
