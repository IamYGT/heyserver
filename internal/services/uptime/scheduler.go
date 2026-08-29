package uptime

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/binary"
	"log/slog"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

// monitorWorker runs checks for a single monitor on a ticker.
func monitorWorker(ctx context.Context, m *store.UptimeMonitor, sm *StateManager, batcher *HeartbeatBatcher, customCheck ...func(*store.UptimeMonitor) CheckResult) {
	interval := time.Duration(m.IntervalSecs) * time.Second
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}

	// Random jitter 0-5s to prevent burst (crypto/rand for uniform distribution)
	var jitterBuf [2]byte
	_, _ = cryptoRand.Read(jitterBuf[:])
	jitterMs := int(binary.LittleEndian.Uint16(jitterBuf[:])) % 5000
	jitter := time.Duration(jitterMs) * time.Millisecond
	select {
	case <-time.After(jitter):
	case <-ctx.Done():
		return
	}

	slog.Debug("uptime: worker started", "monitor", m.Name, "interval", interval)

	check := func(monitor *store.UptimeMonitor) CheckResult { return runCheck(monitor, sm, batcher) }
	if len(customCheck) > 0 && customCheck[0] != nil {
		check = customCheck[0]
	}

	// Run first check immediately
	check(m)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Debug("uptime: worker stopped", "monitor", m.Name)
			return
		case <-ticker.C:
			check(m)
		}
	}
}

func runCheck(m *store.UptimeMonitor, sm *StateManager, batcher *HeartbeatBatcher) CheckResult {
	result := dispatchCheck(m)

	// Queue heartbeat for batch insert
	hb := store.UptimeHeartbeat{
		MonitorID: result.MonitorID,
		Status:    result.Status,
		Msg:       result.Msg,
		TLSExpiry: result.TLSExpiry,
		CreatedAt: result.CheckedAt.UTC().Format(time.RFC3339),
	}
	if result.PingMs > 0 {
		hb.PingMs = &result.PingMs
	}
	if result.StatusCode > 0 {
		hb.StatusCode = &result.StatusCode
	}
	batcher.Add(hb)

	// Process state transition
	sm.Transition(m, result)
	return result
}
