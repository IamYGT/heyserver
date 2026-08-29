package notify

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/monitor"
)

type AlertChecker struct {
	channelRepo   *store.NotificationChannelRepository
	ruleRepo      *store.AlertRuleRepository
	historyRepo   *store.AlertHistoryRepository
	receiptRepo   ReceiptRecorder
	breachStart   map[int64]time.Time
	mu            sync.Mutex
	checkInterval time.Duration
	pruneAge      time.Duration
}

func NewAlertChecker(ch *store.NotificationChannelRepository, r *store.AlertRuleRepository, h *store.AlertHistoryRepository, receiptRepo ...ReceiptRecorder) *AlertChecker {
	var recorder ReceiptRecorder
	if len(receiptRepo) > 0 { recorder = receiptRepo[0] }
	return &AlertChecker{channelRepo: ch, ruleRepo: r, historyRepo: h, receiptRepo: recorder, breachStart: make(map[int64]time.Time), checkInterval: 60 * time.Second, pruneAge: 30 * 24 * time.Hour}
}

func (ac *AlertChecker) Run(ctx context.Context) {
	slog.Info("alert checker started", "interval", ac.checkInterval)
	tick := time.NewTicker(ac.checkInterval)
	prune := time.NewTicker(6 * time.Hour)
	defer tick.Stop(); defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("alert checker stopped"); return
		case <-tick.C:
			ac.checkAll()
		case <-prune.C:
			if err := ac.historyRepo.PruneOlderThan(ac.pruneAge); err != nil {
				slog.Warn("prune failed", "error", err)
			}
		}
	}
}

func (ac *AlertChecker) checkAll() {
	rules, err := ac.ruleRepo.List()
	if err != nil { slog.Warn("list rules failed", "error", err); return }
	channels, err := ac.channelRepo.List()
	if err != nil { slog.Warn("list channels failed", "error", err); return }
	d := NewDispatcher(channels)
	for _, rule := range rules { if rule.Enabled { ac.evaluate(rule, d) } }
}

func (ac *AlertChecker) evaluate(rule models.AlertRule, d *Dispatcher) {
	value, message, triggered := ac.checkRule(rule)
	ac.mu.Lock(); defer ac.mu.Unlock()
	if !triggered { delete(ac.breachStart, rule.ID); return }
	if _, ok := ac.breachStart[rule.ID]; !ok { ac.breachStart[rule.ID] = time.Now() }
	if time.Since(ac.breachStart[rule.ID]) < time.Duration(rule.DurationMins)*time.Minute { return }
	lastFired, _ := ac.historyRepo.LastFiredAt(rule.ID)
	if !lastFired.IsZero() && time.Since(lastFired) < time.Duration(rule.CooldownMins)*time.Minute { return }
	entry := &models.AlertHistory{RuleID: rule.ID, RuleName: rule.Name, Type: rule.Type, Message: message, Value: value}
	if err := ac.historyRepo.Insert(entry); err != nil { slog.Warn("insert history failed", "error", err) }
	e := Event{Type: rule.Type, Host: hostname(), Target: rule.Target, Value: value, Message: message, Time: time.Now().UTC()}
	if err := ac.dispatch(d, e); err != nil {
		slog.Warn("dispatch failed", "rule", rule.Name, "error", err)
	} else {
		slog.Info("alert fired", "rule", rule.Name, "value", value)
	}
}

// dispatch completes provider delivery before persisting bounded per-channel outcomes.
func (ac *AlertChecker) dispatch(d *Dispatcher, event Event) error {
	results, sendErr := d.SendWithResults(RenderSubject(event), RenderBody(event))
	if err := PersistDeliveryResults(context.Background(), ac.receiptRepo, models.NotificationDeliverySourceAlert, results, time.Now().UTC()); err != nil {
		slog.Warn("alert delivery receipt persistence failed", "error", err)
	}
	return sendErr
}

func (ac *AlertChecker) checkRule(rule models.AlertRule) (float64, string, bool) {
	normalized, err := ValidateAndNormalizeAlertRule(rule)
	if err != nil {
		slog.Warn("invalid alert rule skipped", "rule", rule.Name, "error", err)
		return 0, "", false
	}
	rule = normalized
	switch rule.Type {
	case models.AlertCPUUsage:    return checkCPU(rule)
	case models.AlertMemoryUsage: return checkMemory(rule)
	case models.AlertDiskUsage:   return checkDisk(rule)
	case models.AlertSSLExpiry:   return checkSSL(rule)
	case models.AlertServiceDown: return checkService(rule)
	case models.AlertFailedLogins:return checkFailedLogins(rule)
	default: return 0, "", false
	}
}

var monitorCache = monitor.New(5 * time.Second)

func checkCPU(rule models.AlertRule) (float64, string, bool) {
	s, err := monitorCache.Stats()
	if err != nil { return 0, "", false }
	if s.CPU.Usage >= rule.Threshold { return s.CPU.Usage, fmt.Sprintf("CPU %.1f%% >= %.1f%%", s.CPU.Usage, rule.Threshold), true }
	return s.CPU.Usage, "", false
}

func checkMemory(rule models.AlertRule) (float64, string, bool) {
	s, err := monitorCache.Stats()
	if err != nil { return 0, "", false }
	p := s.Memory.Percentage
	if p >= rule.Threshold { return p, fmt.Sprintf("Memory %.1f%% >= %.1f%%", p, rule.Threshold), true }
	return p, "", false
}

func checkDisk(rule models.AlertRule) (float64, string, bool) {
	s, err := monitorCache.Stats()
	if err != nil { return 0, "", false }
	target := rule.Target; if target == "" { target = "/" }
	for _, d := range s.Disk {
		if d.Mount == target {
			if d.Percentage >= rule.Threshold { return d.Percentage, fmt.Sprintf("Disk %s %.1f%% >= %.1f%%", target, d.Percentage, rule.Threshold), true }
			return d.Percentage, "", false
		}
	}
	return 0, "", false
}

func checkSSL(rule models.AlertRule) (float64, string, bool) {
	if rule.Target == "" { return 0, "", false }
	data, err := os.ReadFile(filepath.Join("/etc/letsencrypt/live", rule.Target, "cert.pem"))
	if err != nil { return 0, "", false }
	block, _ := pem.Decode(data)
	if block == nil { return 0, "", false }
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil { return 0, "", false }
	days := time.Until(cert.NotAfter).Hours() / 24
	if days <= rule.Threshold { return days, fmt.Sprintf("SSL %s expires in %.0f days", rule.Target, days), true }
	return days, "", false
}

func checkService(rule models.AlertRule) (float64, string, bool) {
	if rule.Target == "" { return 0, "", false }
	if err := exec.Command("systemctl", "is-active", "--quiet", rule.Target).Run(); err != nil {
		return 1, fmt.Sprintf("Service %q is not running", rule.Target), true
	}
	return 0, "", false
}

func checkFailedLogins(rule models.AlertRule) (float64, string, bool) {
	n := countFailedSSH()
	if float64(n) >= rule.Threshold { return float64(n), fmt.Sprintf("%d failed SSH logins >= %.0f", n, rule.Threshold), true }
	return float64(n), "", false
}

func countFailedSSH() int {
	out, err := exec.Command("journalctl", "-u", "ssh", "--since", "1 minute ago", "--no-pager", "-q").Output()
	if err != nil { return 0 }
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Failed password") || strings.Contains(line, "Invalid user") { n++ }
	}
	return n
}
