package uptime

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

func checkDNS(m *store.UptimeMonitor) CheckResult {
	result := CheckResult{MonitorID: m.ID, CheckedAt: time.Now()}

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: time.Duration(m.TimeoutSecs) * time.Second}
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.TimeoutSecs)*time.Second)
	defer cancel()

	start := time.Now()

	// Keyword holds record type (A, AAAA, MX, CNAME), ReqBody holds expected value
	recordType := strings.ToUpper(m.Keyword)
	if recordType == "" {
		recordType = "A"
	}

	var records []string
	var err error

	switch recordType {
	case "A":
		var ips []net.IP
		ips, err = resolver.LookupIP(ctx, "ip4", m.Hostname)
		for _, ip := range ips {
			records = append(records, ip.String())
		}
	case "AAAA":
		var ips []net.IP
		ips, err = resolver.LookupIP(ctx, "ip6", m.Hostname)
		for _, ip := range ips {
			records = append(records, ip.String())
		}
	case "MX":
		var mxs []*net.MX
		mxs, err = resolver.LookupMX(ctx, m.Hostname)
		for _, mx := range mxs {
			records = append(records, mx.Host)
		}
	case "CNAME":
		var cname string
		cname, err = resolver.LookupCNAME(ctx, m.Hostname)
		if cname != "" {
			records = append(records, cname)
		}
	default:
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("unsupported DNS record type: %s", recordType)
		return result
	}

	result.PingMs = float64(time.Since(start).Milliseconds())

	if err != nil {
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("DNS lookup failed: %v", err)
		return result
	}

	if len(records) == 0 {
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("no %s records found for %s", recordType, m.Hostname)
		return result
	}

	// Check expected value (stored in ReqBody)
	if m.ReqBody != "" {
		found := false
		for _, r := range records {
			if strings.EqualFold(strings.TrimSuffix(r, "."), strings.TrimSuffix(m.ReqBody, ".")) {
				found = true
				break
			}
		}
		if !found {
			result.Status = StatusDown
			result.Msg = fmt.Sprintf("expected %s=%s but got [%s]", recordType, m.ReqBody, strings.Join(records, ", "))
			return result
		}
	}

	result.Status = StatusUp
	result.Msg = fmt.Sprintf("%s: %s", recordType, strings.Join(records, ", "))
	return result
}
