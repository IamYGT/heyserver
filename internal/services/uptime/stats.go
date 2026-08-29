package uptime

import (
	"math"
	"sort"

	"github.com/IamYGT/heyserver/internal/store"
)

// UptimeStats holds computed statistics for a monitor.
type UptimeStats struct {
	Uptime24h float64 `json:"uptime_24h"`
	Uptime7d  float64 `json:"uptime_7d"`
	Uptime30d float64 `json:"uptime_30d"`
	Uptime90d float64 `json:"uptime_90d"`
	AvgPingMs float64 `json:"avg_ping_ms"`
	P95PingMs float64 `json:"p95_ping_ms"`
	P99PingMs float64 `json:"p99_ping_ms"`
}

// ComputeStats calculates uptime percentages and response time percentiles.
func ComputeStats(repo *store.UptimeRepository, monitorID int64) (*UptimeStats, error) {
	s := &UptimeStats{}
	var err error

	s.Uptime24h, err = repo.UptimePercent(monitorID, 24)
	if err != nil {
		return nil, err
	}

	s.Uptime7d, err = repo.UptimePercent(monitorID, 24*7)
	if err != nil {
		return nil, err
	}

	s.Uptime30d, err = repo.UptimePercent(monitorID, 24*30)
	if err != nil {
		return nil, err
	}

	s.Uptime90d, err = repo.UptimePercent(monitorID, 24*90)
	if err != nil {
		return nil, err
	}

	s.AvgPingMs, err = repo.AvgPing(monitorID, 24)
	if err != nil {
		return nil, err
	}

	// Compute p95/p99 from recent heartbeats
	heartbeats, err := repo.ListHeartbeats(monitorID, 24)
	if err != nil {
		return nil, err
	}

	var pings []float64
	for _, h := range heartbeats {
		if h.PingMs != nil && *h.PingMs > 0 {
			pings = append(pings, *h.PingMs)
		}
	}

	if len(pings) > 0 {
		sort.Float64s(pings)
		s.P95PingMs = percentile(pings, 0.95)
		s.P99PingMs = percentile(pings, 0.99)
	}

	return s, nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}
