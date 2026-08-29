package uptime

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/IamYGT/heyserver/internal/services/shell"
	"github.com/IamYGT/heyserver/internal/store"
)

var pingTimeRegex = regexp.MustCompile(`time[=<]([\d.]+)\s*ms`)

func checkPing(m *store.UptimeMonitor) CheckResult {
	result := CheckResult{MonitorID: m.ID, CheckedAt: time.Now()}

	timeout := m.TimeoutSecs
	if timeout <= 0 {
		timeout = 10
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout+2)*time.Second)
	defer cancel()

	// ping -c 1 -W timeout hostname
	out, err := shell.ExecuteContext(ctx, "ping", "-c", "1", "-W", strconv.Itoa(timeout), m.Hostname)
	if err != nil {
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("ping failed: %v", err)
		return result
	}

	// Extract time from output
	matches := pingTimeRegex.FindStringSubmatch(out)
	if len(matches) >= 2 {
		result.PingMs, _ = strconv.ParseFloat(matches[1], 64)
	}

	result.Status = StatusUp
	result.Msg = fmt.Sprintf("ping %s: %.1fms", m.Hostname, result.PingMs)
	return result
}
