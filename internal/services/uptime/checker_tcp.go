package uptime

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

func checkTCP(m *store.UptimeMonitor) CheckResult {
	result := CheckResult{MonitorID: m.ID, CheckedAt: time.Now()}

	// NOTE: SSRF validation is done at create/update time in the handler, not here.

	addr := net.JoinHostPort(m.Hostname, strconv.Itoa(m.Port))
	timeout := time.Duration(m.TimeoutSecs) * time.Second

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	result.PingMs = float64(time.Since(start).Milliseconds())

	if err != nil {
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("TCP connection failed: %v", err)
		return result
	}
	_ = conn.Close()

	result.Status = StatusUp
	result.Msg = fmt.Sprintf("TCP %s connected in %.0fms", addr, result.PingMs)
	return result
}
