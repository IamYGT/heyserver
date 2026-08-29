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

type tuiPHPItemKind string

const (
	tuiPHPVersionItem tuiPHPItemKind = "version"
	tuiPHPPoolItem    tuiPHPItemKind = "pool"
)

type tuiPHPItem struct {
	Kind        tuiPHPItemKind
	Version     string
	Name        string
	Unit        string
	Active      string
	Enabled     string
	Masked      bool
	Runtime     bool
	PoolPath    string
	User        string
	Group       string
	Listen      string
	PM          string
	MaxChildren int
}

type tuiPHPState struct {
	Items      []tuiPHPItem
	Actionable bool
	Readable   bool
	Writable   bool
}

type tuiPHPMsg struct {
	TargetID string
	State    tuiPHPState
	Err      error
}

type tuiPHPConfigMsg struct {
	TargetID string
	Version  string
	Pool     string
	Path     string
	Lines    []string
	Err      error
}

func loadTUIPHPCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIPHP(ctx, client, target)
		return tuiPHPMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIPHP(ctx context.Context, client *apiClient, target tuiTarget) (tuiPHPState, error) {
	if !target.Local {
		if !target.Online {
			return tuiPHPState{}, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityPHPRead) {
			return tuiPHPState{}, errors.New("managed agent does not advertise php.read")
		}
		endpoint := "/api/nodes/" + url.PathEscape(target.ID) + "/php"
		versions, err := requestJSON[[]remotePHPVersion](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return tuiPHPState{}, err
		}
		state := tuiPHPState{
			Readable: true, Writable: target.capability(agenthub.CapabilityPHPWrite),
			Actionable: target.capability(agenthub.CapabilityPHPAction),
		}
		for _, version := range versions {
			state.Items = append(state.Items, tuiPHPItem{
				Kind: tuiPHPVersionItem, Version: version.Version, Name: "PHP " + version.Version,
				Unit: version.Unit, Active: version.Active, Enabled: version.Enabled, Masked: version.Masked, Runtime: version.Binary != "",
			})
			for _, pool := range version.Pools {
				state.Items = append(state.Items, tuiPHPItem{
					Kind: tuiPHPPoolItem, Version: version.Version, Name: pool.Name, PoolPath: pool.Path,
					User: pool.User, Group: pool.Group, Listen: pool.Listen, PM: pool.PM, MaxChildren: pool.MaxChildren,
				})
			}
		}
		sortTUIPHPItems(state.Items)
		return state, nil
	}

	versions, err := loadLocalPHPVersions(ctx, client)
	if err != nil {
		return tuiPHPState{}, err
	}
	pools, err := requestJSON[[]cliPHPPool](ctx, client, http.MethodGet, "/api/php/pools", nil, true)
	if err != nil {
		return tuiPHPState{}, err
	}
	state := tuiPHPState{Readable: true, Writable: true, Actionable: true}
	for _, version := range versions {
		active := "inactive"
		if version.Active {
			active = "active"
		}
		state.Items = append(state.Items, tuiPHPItem{
			Kind: tuiPHPVersionItem, Version: version.Version, Name: "PHP " + version.Version,
			Active: active, Enabled: "installed", Runtime: true,
		})
	}
	for _, pool := range pools {
		maxChildren := 0
		if value, ok := pool.PMSettings["max_children"].(float64); ok {
			maxChildren = int(value)
		}
		state.Items = append(state.Items, tuiPHPItem{
			Kind: tuiPHPPoolItem, Version: pool.Version, Name: pool.Name, PoolPath: pool.ConfigFile,
			User: pool.User, Group: pool.Group, Listen: pool.Listen, PM: pool.PM, MaxChildren: maxChildren,
		})
	}
	sortTUIPHPItems(state.Items)
	return state, nil
}

func loadTUIPHPConfigCmd(ctx context.Context, client *apiClient, target tuiTarget, item tuiPHPItem) tea.Cmd {
	return func() tea.Msg {
		path, lines, err := loadTUIPHPConfig(ctx, client, target, item)
		return tuiPHPConfigMsg{TargetID: target.ID, Version: item.Version, Pool: item.Name, Path: path, Lines: lines, Err: err}
	}
}

func loadTUIPHPConfig(ctx context.Context, client *apiClient, target tuiTarget, item tuiPHPItem) (string, []string, error) {
	version, pool, err := validateCLIPHPIdentity(item.Version, item.Name)
	if err != nil {
		return "", nil, err
	}
	if target.Local {
		observed, err := requireLocalPHPPool(ctx, client, version, pool)
		if err != nil {
			return "", nil, err
		}
		endpoint := "/api/php/pools/" + url.PathEscape(version) + "/" + url.PathEscape(pool) + "/config"
		response, err := requestJSON[cliFileContent](ctx, client, http.MethodGet, endpoint, nil, true)
		if err != nil {
			return "", nil, err
		}
		if response.Path != observed.ConfigFile {
			return "", nil, errors.New("local panel resolved the PHP-FPM pool to a different path")
		}
		lines, err := splitTUIFileContent(response.Content)
		return response.Path, lines, err
	}
	if !target.Online {
		return "", nil, errors.New("managed node is offline")
	}
	if !target.capability(agenthub.CapabilityPHPRead) {
		return "", nil, errors.New("managed agent does not advertise php.read")
	}
	state, err := loadTUIPHP(ctx, client, target)
	if err != nil {
		return "", nil, err
	}
	observed, ok := findTUIPHPPool(state.Items, version, pool)
	if !ok {
		return "", nil, errors.New("PHP-FPM pool is no longer present in managed inventory")
	}
	endpoint := remotePHPConfigEndpoint(target.ID, version, pool)
	response, err := requestJSON[cliFileContent](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return "", nil, err
	}
	if response.Path != observed.PoolPath {
		return "", nil, errors.New("managed agent resolved the PHP-FPM pool to a different path")
	}
	lines, err := splitTUIFileContent(response.Content)
	return response.Path, lines, err
}

func runTUIPHPOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	item := operation.PHP
	if item.Kind != tuiPHPVersionItem {
		return "", errors.New("PHP-FPM lifecycle actions require a version row")
	}
	version, err := validateCLIPHPVersion(item.Version)
	if err != nil {
		return "", err
	}
	if operation.Target.Local {
		if operation.Action != "test" && operation.Action != "reload" && operation.Action != "restart" {
			return "", fmt.Errorf("unsupported local PHP-FPM action %q", operation.Action)
		}
		if _, err := requireLocalPHPVersion(ctx, client, version); err != nil {
			return "", err
		}
		endpoint := "/api/php/versions/" + url.PathEscape(version) + "/actions/" + operation.Action
		response, err := requestJSON[map[string]string](ctx, client.withTimeout(2*time.Minute), http.MethodPost, endpoint, nil, true)
		return phpOperationMessage(response, "PHP "+version+" "+operation.Action+" completed", err)
	}
	if !operation.Target.Online {
		return "", errors.New("managed node is offline")
	}
	if !operation.Target.capability(agenthub.CapabilityPHPRead) || !operation.Target.capability(agenthub.CapabilityPHPAction) {
		return "", errors.New("managed agent must advertise php.read and php.action")
	}
	if operation.Action != "test" && operation.Action != "reload" && operation.Action != "restart" {
		return "", fmt.Errorf("unsupported managed PHP-FPM action %q", operation.Action)
	}
	state, err := loadTUIPHP(ctx, client, operation.Target)
	if err != nil {
		return "", err
	}
	observed, ok := findTUIPHPVersion(state.Items, version)
	if !ok {
		return "", errors.New("PHP version is no longer present in managed inventory")
	}
	if observed.Masked || !observed.Runtime {
		return "", errors.New("managed PHP-FPM runtime is masked or has no executable binary")
	}
	endpoint := "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/php/" + url.PathEscape(version) + "/actions/" + operation.Action
	response, err := requestJSON[map[string]string](ctx, client.withTimeout(2*time.Minute), http.MethodPost, endpoint, nil, true)
	return phpOperationMessage(response, "PHP "+version+" action completed", err)
}

func phpOperationMessage(response map[string]string, fallback string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if message := strings.TrimSpace(response["message"]); message != "" {
		return message, nil
	}
	return fallback, nil
}

func sortTUIPHPItems(items []tuiPHPItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Version != items[j].Version {
			return phpVersionLess(items[j].Version, items[i].Version)
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == tuiPHPVersionItem
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
}

func phpVersionLess(left, right string) bool {
	var leftMajor, leftMinor, rightMajor, rightMinor int
	_, _ = fmt.Sscanf(left, "%d.%d", &leftMajor, &leftMinor)
	_, _ = fmt.Sscanf(right, "%d.%d", &rightMajor, &rightMinor)
	if leftMajor != rightMajor {
		return leftMajor < rightMajor
	}
	return leftMinor < rightMinor
}

func findTUIPHPVersion(items []tuiPHPItem, version string) (tuiPHPItem, bool) {
	for _, item := range items {
		if item.Kind == tuiPHPVersionItem && item.Version == version {
			return item, true
		}
	}
	return tuiPHPItem{}, false
}

func findTUIPHPPool(items []tuiPHPItem, version, pool string) (tuiPHPItem, bool) {
	for _, item := range items {
		if item.Kind == tuiPHPPoolItem && item.Version == version && item.Name == pool {
			return item, true
		}
	}
	return tuiPHPItem{}, false
}
