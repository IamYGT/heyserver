package uptime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/notify"
	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/store"
)

// Alerter wraps notify.Dispatcher for uptime-specific alerts.
// Resolves channels: per-monitor override first, then global default.
type Alerter struct {
	channelRepo *store.NotificationChannelRepository
	settings    *settings.Service
	receiptRepo notify.ReceiptRecorder
}

func NewAlerter(channelRepo *store.NotificationChannelRepository, settingsSvc *settings.Service, receiptRepo ...notify.ReceiptRecorder) *Alerter {
	var recorder notify.ReceiptRecorder
	if len(receiptRepo) > 0 {
		recorder = receiptRepo[0]
	}
	return &Alerter{channelRepo: channelRepo, settings: settingsSvc, receiptRepo: recorder}
}

func (a *Alerter) SendDown(monitor *store.UptimeMonitor, result CheckResult) {
	subject := fmt.Sprintf("[Heyserver] DOWN: %s", monitor.Name)
	body := fmt.Sprintf("Monitor: %s\nType: %s\nTarget: %s\nError: %s\nResponse Time: %.0fms",
		monitor.Name, monitor.Type, monitorTarget(monitor), result.Msg, result.PingMs)
	a.dispatch(monitor, subject, body)
}

func (a *Alerter) SendRecovery(monitor *store.UptimeMonitor, result CheckResult) {
	subject := fmt.Sprintf("[Heyserver] UP: %s is back online", monitor.Name)
	body := fmt.Sprintf("Monitor: %s\nType: %s\nTarget: %s\nStatus: %s\nResponse Time: %.0fms",
		monitor.Name, monitor.Type, monitorTarget(monitor), result.Msg, result.PingMs)
	a.dispatch(monitor, subject, body)
}

func (a *Alerter) SendReminder(monitor *store.UptimeMonitor, result CheckResult) {
	subject := fmt.Sprintf("[Heyserver] STILL DOWN: %s", monitor.Name)
	body := fmt.Sprintf("Monitor: %s\nType: %s\nTarget: %s\nError: %s\nThis monitor has been down for multiple check cycles.",
		monitor.Name, monitor.Type, monitorTarget(monitor), result.Msg)
	a.dispatch(monitor, subject, body)
}

func (a *Alerter) dispatch(monitor *store.UptimeMonitor, subject, body string) {
	channelIDs := store.ParseChannelIDs(string(monitor.AlertChannelIDs))
	if len(channelIDs) == 0 {
		// Fallback to global default
		defaultStr, _ := a.settings.Get("uptime_default_channels", "")
		channelIDs = store.ParseChannelIDs(defaultStr)
	}
	if len(channelIDs) == 0 {
		slog.Debug("uptime: no alert channels configured", "monitor", monitor.Name)
		return
	}

	// store.NotificationChannelRepository.List() returns []models.NotificationChannel
	allChannels, err := a.channelRepo.List()
	if err != nil {
		slog.Error("uptime: failed to list channels", "error", err)
		return
	}

	// Build a set of requested IDs for quick lookup
	idSet := make(map[int64]bool, len(channelIDs))
	for _, id := range channelIDs {
		idSet[id] = true
	}

	// Filter to selected, enabled channels (type is already models.NotificationChannel)
	var selected []models.NotificationChannel
	for _, ch := range allChannels {
		if idSet[ch.ID] && ch.Enabled {
			selected = append(selected, ch)
		}
	}

	if len(selected) == 0 {
		return
	}

	// Pass directly to notify.NewDispatcher — it accepts []models.NotificationChannel
	d := notify.NewDispatcher(selected)
	results, sendErr := d.SendWithResults(subject, body)
	if sendErr != nil {
		slog.Error("uptime: alert dispatch failed", "monitor", monitor.Name, "error", sendErr)
	}
	if err := notify.PersistDeliveryResults(context.Background(), a.receiptRepo, models.NotificationDeliverySourceUptime, results, time.Now().UTC()); err != nil {
		slog.Warn("uptime: alert delivery receipt persistence failed", "monitor", monitor.Name, "error", err)
	}
}

func monitorTarget(m *store.UptimeMonitor) string {
	if m.URL != "" {
		return m.URL
	}
	if m.Port > 0 {
		return fmt.Sprintf("%s:%d", m.Hostname, m.Port)
	}
	return m.Hostname
}
