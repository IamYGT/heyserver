package uptime

import (
	"log/slog"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

// StateManager handles UP/DOWN transitions, incident lifecycle, and alert triggering.
type StateManager struct {
	repo    *store.UptimeRepository
	alerter *Alerter
}

func NewStateManager(repo *store.UptimeRepository, alerter *Alerter) *StateManager {
	return &StateManager{repo: repo, alerter: alerter}
}

// Transition processes a check result and updates state, creates/resolves incidents, fires alerts.
func (sm *StateManager) Transition(monitor *store.UptimeMonitor, result CheckResult) {
	state, err := sm.repo.GetState(monitor.ID)
	if err != nil || state == nil {
		slog.Warn("uptime: failed to get state", "monitor", monitor.ID, "error", err)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	prevStatus := state.CurrentStatus

	switch result.Status {
	case StatusUp, StatusTLSWarn:
		state.ConsecutiveUps++
		state.ConsecutiveFails = 0
		state.LastCheckAt = now
		resolved, resolveErr := sm.repo.ResolveOpenIncidents(monitor.ID)
		if resolveErr != nil {
			slog.Warn("uptime: failed to resolve open incidents", "monitor", monitor.ID, "error", resolveErr)
		} else {
			state.ActiveIncidentID = nil
			if resolved > 0 && prevStatus != StatusDown {
				slog.Info("uptime: repaired stale open incidents", "monitor", monitor.ID, "count", resolved)
			}
		}

		if prevStatus == StatusDown && state.ConsecutiveUps >= 1 {
			// DOWN → UP: resolve incident
			state.CurrentStatus = result.Status
			sm.alerter.SendRecovery(monitor, result)
		} else if prevStatus == StatusPending {
			// PENDING → UP: first successful check
			state.CurrentStatus = result.Status
		} else {
			state.CurrentStatus = result.Status
		}

	case StatusDown:
		state.ConsecutiveFails++
		state.ConsecutiveUps = 0
		state.LastCheckAt = now

		if prevStatus != StatusDown && state.ConsecutiveFails >= monitor.Retries {
			// UP/PENDING → DOWN: create incident + alert
			state.CurrentStatus = StatusDown
			inc := &store.UptimeIncident{
				MonitorID: monitor.ID,
				Type:      "down",
				Cause:     result.Msg,
			}
			if err := sm.repo.CreateIncident(inc); err == nil {
				state.ActiveIncidentID = &inc.ID
			}
			state.LastAlertAt = now
			sm.alerter.SendDown(monitor, result)

		} else if prevStatus == StatusDown {
			// DOWN → DOWN: check reminder
			if monitor.AlertReminderMins > 0 && state.LastAlertAt != "" {
				lastAlert, _ := time.Parse(time.RFC3339, state.LastAlertAt)
				if time.Since(lastAlert) >= time.Duration(monitor.AlertReminderMins)*time.Minute {
					sm.alerter.SendReminder(monitor, result)
					state.LastAlertAt = now
				}
			}
		}
	}

	if err := sm.repo.UpdateState(state); err != nil {
		slog.Error("uptime: failed to update state", "monitor", monitor.ID, "error", err)
	}
}
