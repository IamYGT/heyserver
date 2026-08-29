package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type tuiSecurityItemKind string

const (
	tuiSecurityCheckItem     tuiSecurityItemKind = "check"
	tuiSecurityJailItem      tuiSecurityItemKind = "jail"
	tuiSecurityBannedIPItem  tuiSecurityItemKind = "banned-ip"
	tuiSecurityBlacklistItem tuiSecurityItemKind = "ip-blacklist"
	tuiSecurityWhitelistItem tuiSecurityItemKind = "ip-whitelist"
)

type tuiSecurityItem struct {
	Kind            tuiSecurityItemKind
	Name            string
	Status          string
	Detail          string
	Jail            string
	IP              string
	CurrentlyFailed int
	CurrentlyBanned int
	TotalBanned     int
	AccessEntry     cliSecurityIPEntry
}

type tuiSecurityState struct {
	Supported         bool
	ScoreLoaded       bool
	Score             cliSecurityScore
	Fail2BanLoaded    bool
	Fail2Ban          cliFail2BanStatus
	AccessListsLoaded bool
	AccessLists       tuiSecurityAccessListsState
	Items             []tuiSecurityItem
	Warnings          []string
	UnsupportedNote   string
}

type tuiSecurityMsg struct {
	TargetID string
	State    tuiSecurityState
}

func loadTUISecurityCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		return tuiSecurityMsg{TargetID: target.ID, State: loadTUISecurity(ctx, client, target)}
	}
}

func loadTUISecurity(ctx context.Context, client *apiClient, target tuiTarget) tuiSecurityState {
	if !target.Local {
		return tuiSecurityState{
			AccessListsLoaded: true,
			AccessLists: tuiSecurityAccessListsState{
				UnsupportedNote: "Panel-local IP blacklist and whitelist management is not an advertised managed-node capability.",
			},
			UnsupportedNote: "Security score, Fail2Ban, and persistent IP access-list management currently run on the panel host; managed-agent security capabilities are not advertised.",
		}
	}
	state := tuiSecurityState{Supported: true}
	score, scoreErr := requestJSON[cliSecurityScore](ctx, client, http.MethodGet, "/api/security/score", nil, true)
	if scoreErr != nil {
		state.Warnings = append(state.Warnings, "Security score unavailable: "+scoreErr.Error())
	} else {
		state.ScoreLoaded = true
		state.Score = score
		for _, check := range score.Checks {
			state.Items = append(state.Items, tuiSecurityItem{
				Kind: tuiSecurityCheckItem, Name: check.Name, Status: check.Status, Detail: check.Detail,
			})
		}
	}
	fail2ban, fail2banErr := loadCLIFail2BanStatus(ctx, client)
	if fail2banErr != nil {
		state.Warnings = append(state.Warnings, "Fail2Ban inventory unavailable: "+fail2banErr.Error())
	} else {
		state.Fail2BanLoaded = true
		state.Fail2Ban = fail2ban
		jails := append([]cliFail2BanJail(nil), fail2ban.Jails...)
		sort.SliceStable(jails, func(i, j int) bool { return strings.ToLower(jails[i].Name) < strings.ToLower(jails[j].Name) })
		for _, jail := range jails {
			state.Items = append(state.Items, tuiSecurityItem{
				Kind: tuiSecurityJailItem, Name: jail.Name, Jail: jail.Name,
				CurrentlyFailed: jail.CurrentlyFailed, CurrentlyBanned: jail.CurrentlyBanned, TotalBanned: jail.TotalBanned,
			})
			ips := append([]string(nil), jail.BannedIPs...)
			sort.Strings(ips)
			for _, ip := range ips {
				state.Items = append(state.Items, tuiSecurityItem{Kind: tuiSecurityBannedIPItem, Name: ip, Jail: jail.Name, IP: ip})
			}
		}
	}
	accessLists := loadTUISecurityAccessLists(ctx, client, target)
	state.AccessListsLoaded = true
	state.AccessLists = accessLists
	state.Warnings = append(state.Warnings, accessLists.Warnings...)
	for index, entry := range accessLists.Blacklist {
		if index >= tuiSecurityAccessDefaultMaxRows {
			break
		}
		state.Items = append(state.Items, tuiSecurityItem{
			Kind: tuiSecurityBlacklistItem, Name: entry.IP, IP: entry.IP, AccessEntry: entry,
		})
	}
	for index, entry := range accessLists.Whitelist {
		if index >= tuiSecurityAccessDefaultMaxRows {
			break
		}
		state.Items = append(state.Items, tuiSecurityItem{
			Kind: tuiSecurityWhitelistItem, Name: entry.IP, IP: entry.IP, AccessEntry: entry,
		})
	}
	return state
}

func runTUISecurityOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if !operation.Target.Local {
		return "", errors.New("Fail2Ban TUI mutations are available only on the panel host")
	}
	if operation.Action != "unban" || operation.Security.Kind != tuiSecurityBannedIPItem {
		return "", fmt.Errorf("unsupported security TUI action %q", operation.Action)
	}
	jail, err := validateCLIFail2BanJail(operation.Security.Jail)
	if err != nil {
		return "", err
	}
	ip, err := validateCLIFail2BanIP(operation.Security.IP)
	if err != nil {
		return "", err
	}
	status, err := requireCLIFail2BanJail(ctx, client, jail)
	if err != nil {
		return "", err
	}
	if !fail2BanStatusHasIP(status, jail, ip) {
		return "", errors.New("IP is no longer present in the current banned inventory for this jail")
	}
	response, err := requestJSON[map[string]string](ctx, client, http.MethodPost, "/api/security/fail2ban/unban", map[string]string{"jail": jail, "ip": ip}, true)
	if err != nil {
		return "", err
	}
	if response["status"] != "unbanned" || response["ip"] != ip {
		return "", errors.New("Fail2Ban returned an unexpected unban receipt")
	}
	return fmt.Sprintf("Unbanned %s from Fail2Ban jail %s", ip, jail), nil
}
