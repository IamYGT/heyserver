package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

type tuiCronJob struct {
	ID          string
	Schedule    string
	User        string
	Command     string
	Description string
	Enabled     bool
}

type tuiCronSource struct {
	Path       string
	EntryCount int
	Managed    bool
}

type tuiCronState struct {
	Service    string
	Available  bool
	Manageable bool
	Runnable   bool
	Revision   string
	Jobs       []tuiCronJob
	Sources    []tuiCronSource
}

type tuiCronMsg struct {
	TargetID string
	State    tuiCronState
	Err      error
}

func loadTUICronCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUICron(ctx, client, target)
		return tuiCronMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUICron(ctx context.Context, client *apiClient, target tuiTarget) (tuiCronState, error) {
	if !target.Local {
		if !target.Online {
			return tuiCronState{}, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityCronRead) {
			return tuiCronState{}, errors.New("managed agent does not advertise cron.read")
		}
		inventory, err := loadRemoteCronInventory(ctx, client, target.ID)
		if err != nil {
			return tuiCronState{}, err
		}
		state := tuiCronState{
			Service: inventory.Service, Available: true,
			Manageable: target.capability(agenthub.CapabilityCronWrite), Runnable: target.capability(agenthub.CapabilityCronRun),
			Revision: inventory.Revision, Jobs: make([]tuiCronJob, 0, len(inventory.Jobs)), Sources: make([]tuiCronSource, 0, len(inventory.Sources)),
		}
		for _, job := range inventory.Jobs {
			state.Jobs = append(state.Jobs, tuiCronJob{ID: job.ID, Schedule: job.Schedule, User: job.User, Command: job.Command, Description: job.Description, Enabled: job.Enabled})
		}
		for _, source := range inventory.Sources {
			state.Sources = append(state.Sources, tuiCronSource{Path: source.Path, EntryCount: source.EntryCount, Managed: source.Managed})
		}
		sortCronState(&state)
		return state, nil
	}

	status, err := requestJSON[struct {
		Available   bool   `json:"available"`
		State       string `json:"state"`
		DaemonState string `json:"daemonState"`
	}](ctx, client, http.MethodGet, "/api/cron/status", nil, true)
	if err != nil {
		return tuiCronState{}, err
	}
	jobs, err := requestJSON[struct {
		Jobs []localCronJob `json:"jobs"`
	}](ctx, client, http.MethodGet, "/api/cron/jobs", nil, true)
	if err != nil {
		return tuiCronState{}, err
	}
	service := status.DaemonState
	if strings.TrimSpace(service) == "" {
		service = status.State
	}
	state := tuiCronState{Service: service, Available: status.Available, Manageable: status.Available, Jobs: make([]tuiCronJob, 0, len(jobs.Jobs))}
	for _, job := range jobs.Jobs {
		state.Jobs = append(state.Jobs, tuiCronJob{ID: job.ID, Schedule: job.Schedule, User: job.User, Command: job.Command, Description: job.Description, Enabled: job.IsActive})
	}
	sortCronState(&state)
	return state, nil
}

func sortCronState(state *tuiCronState) {
	sort.SliceStable(state.Jobs, func(i, j int) bool {
		left := state.Jobs[i].Schedule + "\x00" + state.Jobs[i].User + "\x00" + state.Jobs[i].ID
		right := state.Jobs[j].Schedule + "\x00" + state.Jobs[j].User + "\x00" + state.Jobs[j].ID
		return left < right
	})
	sort.SliceStable(state.Sources, func(i, j int) bool { return state.Sources[i].Path < state.Sources[j].Path })
}

func runTUICronOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	job := operation.CronJob
	if operation.Target.Local {
		if !operation.CronState.Manageable {
			return "", errors.New("local cron runtime is not manageable")
		}
		if !localCronJobIDPattern.MatchString(job.ID) {
			return "", errors.New("local cron operation requires an observed job identity")
		}
		current, err := loadLocalCronJob(ctx, client, job.ID)
		if err != nil {
			return "", err
		}
		endpoint := "/api/cron/jobs/" + url.PathEscape(current.ID) + "?user=" + url.QueryEscape(current.User)
		switch operation.Action {
		case "enable", "disable":
			payload := map[string]any{"schedule": current.Schedule, "command": current.Command, "description": current.Description, "isActive": operation.Action == "enable"}
			response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodPut, endpoint, payload, true)
			return cronOperationMessage(response, "Cron job updated", err)
		case "delete":
			response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodDelete, endpoint, nil, true)
			return cronOperationMessage(response, "Cron job deleted", err)
		case "run":
			return "", errors.New("manual cron execution is available only for managed-agent jobs")
		default:
			return "", fmt.Errorf("unsupported local cron TUI action %q", operation.Action)
		}
	}

	if !operation.Target.Online {
		return "", errors.New("managed node is offline")
	}
	if !remoteCronJobIDPattern.MatchString(job.ID) {
		return "", errors.New("managed cron operation requires an observed cron- identity")
	}
	inventory, err := loadRemoteCronInventory(ctx, client, operation.Target.ID)
	if err != nil {
		return "", err
	}
	current, err := findRemoteCronJob(inventory, job.ID)
	if err != nil {
		return "", err
	}
	base := "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/cron/" + url.PathEscape(current.ID)
	switch operation.Action {
	case "enable", "disable":
		if !operation.Target.capability(agenthub.CapabilityCronWrite) {
			return "", errors.New("managed agent does not advertise cron.write")
		}
		current.Enabled = operation.Action == "enable"
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodPut, base, cronRemotePayload(current, inventory.Revision), true)
		return cronOperationMessage(response, "Cron job updated", err)
	case "delete":
		if !operation.Target.capability(agenthub.CapabilityCronWrite) {
			return "", errors.New("managed agent does not advertise cron.write")
		}
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodDelete, base, map[string]string{"revision": inventory.Revision}, true)
		return cronOperationMessage(response, "Cron job deleted", err)
	case "run":
		if !operation.Target.capability(agenthub.CapabilityCronRun) {
			return "", errors.New("managed agent does not advertise cron.run")
		}
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(3*time.Minute), http.MethodPost, base+"/run", nil, true)
		return cronOperationMessage(response, "Cron job completed", err)
	default:
		return "", fmt.Errorf("unsupported managed cron TUI action %q", operation.Action)
	}
}

func cronOperationMessage(response map[string]any, fallback string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	message := fallback
	if value, ok := response["message"].(string); ok && strings.TrimSpace(value) != "" {
		message = strings.TrimSpace(value)
	}
	if output, ok := response["output"].(string); ok && strings.TrimSpace(output) != "" {
		message += " · " + truncateTUI(strings.TrimSpace(output), 120)
	}
	return message, nil
}
