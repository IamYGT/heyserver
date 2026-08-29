package uptime

import (
	"fmt"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

// Status constants
const (
	StatusDown        = 0
	StatusUp          = 1
	StatusPending     = 2
	StatusMaintenance = 3
	StatusTLSWarn     = 4
)

// CheckResult holds the outcome of a single monitor check.
type CheckResult struct {
	MonitorID  int64
	Status     int
	Msg        string
	PingMs     float64
	StatusCode int
	TLSExpiry  string
	CheckedAt  time.Time
}

// TestCheck runs a one-off check for the given monitor and returns the result.
// It is exported so HTTP handlers can call it for "test monitor" functionality.
func TestCheck(m *store.UptimeMonitor) CheckResult {
	return dispatchCheck(m)
}

// dispatchCheck routes to the appropriate checker based on monitor type.
func dispatchCheck(m *store.UptimeMonitor) CheckResult {
	start := time.Now()
	var result CheckResult
	result.MonitorID = m.ID
	result.CheckedAt = start

	switch m.Type {
	case "http":
		result = checkHTTP(m)
	case "tcp":
		result = checkTCP(m)
	case "dns":
		result = checkDNS(m)
	case "ping":
		result = checkPing(m)
	default:
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("unknown monitor type: %s", m.Type)
	}

	result.MonitorID = m.ID
	result.CheckedAt = start
	return result
}
