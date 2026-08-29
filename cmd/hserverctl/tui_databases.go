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

type tuiDatabaseItemKind string

const (
	tuiDatabaseEngineItem tuiDatabaseItemKind = "engine"
	tuiDatabaseRowItem    tuiDatabaseItemKind = "database"
)

type tuiDatabaseItem struct {
	Kind         tuiDatabaseItemKind
	Engine       string
	EngineName   string
	Name         string
	Owner        string
	SizeText     string
	SizeBytes    int64
	Tables       int
	Connections  int
	Objects      int
	Active       string
	Version      string
	Unit         string
	SessionCount int
}

type tuiDatabaseState struct {
	Items       []tuiDatabaseItem
	Sources     map[string]databaseSource
	Manageable  bool
	Restartable bool
}

type tuiDatabasesMsg struct {
	TargetID string
	State    tuiDatabaseState
	Err      error
}

func loadTUIDatabasesCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIDatabases(ctx, client, target)
		return tuiDatabasesMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIDatabases(ctx context.Context, client *apiClient, target tuiTarget) (tuiDatabaseState, error) {
	if !target.Local {
		if !target.Online {
			return tuiDatabaseState{}, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityDatabaseRead) {
			return tuiDatabaseState{}, errors.New("managed agent does not advertise database.read")
		}
		engines, err := loadRemoteDatabaseEngines(ctx, client, target.ID)
		if err != nil {
			return tuiDatabaseState{}, err
		}
		state := tuiDatabaseState{Restartable: target.capability(agenthub.CapabilityDatabaseAction)}
		for _, engine := range engines {
			state.Items = append(state.Items, tuiDatabaseItem{
				Kind: tuiDatabaseEngineItem, Engine: engine.ID, EngineName: engine.Name, Name: engine.Name,
				SizeBytes: engine.DataSize, Active: engine.Active, Version: engine.Version, Unit: engine.Unit, SessionCount: len(engine.Sessions),
			})
			for _, database := range engine.Databases {
				state.Items = append(state.Items, tuiDatabaseItem{
					Kind: tuiDatabaseRowItem, Engine: engine.ID, EngineName: engine.Name, Name: database.Name,
					SizeBytes: database.Size, Connections: database.Connections, Objects: database.Objects,
				})
			}
		}
		sortDatabaseItems(state.Items)
		return state, nil
	}

	response, err := requestJSON[databaseListResponse](ctx, client.withTimeout(45*time.Second), http.MethodGet, "/api/databases", nil, true)
	if err != nil {
		return tuiDatabaseState{}, err
	}
	state := tuiDatabaseState{Sources: response.Sources, Manageable: true}
	for _, id := range []string{"postgresql", "mariadb"} {
		source, ok := response.Sources[id]
		if !ok {
			continue
		}
		name := "PostgreSQL"
		if id == "mariadb" {
			name = "MariaDB"
		}
		state.Items = append(state.Items, tuiDatabaseItem{Kind: tuiDatabaseEngineItem, Engine: id, EngineName: name, Name: name, Active: source.State})
	}
	for _, database := range response.Databases {
		engine, normalizeErr := normalizeRemoteDatabaseEngine(database.Engine)
		if normalizeErr != nil {
			continue
		}
		engineName := "PostgreSQL"
		if engine == "mariadb" {
			engineName = "MariaDB"
		}
		state.Items = append(state.Items, tuiDatabaseItem{
			Kind: tuiDatabaseRowItem, Engine: engine, EngineName: engineName, Name: database.Name,
			Owner: database.Owner, SizeText: database.Size, Tables: database.Tables,
		})
	}
	sortDatabaseItems(state.Items)
	return state, nil
}

func sortDatabaseItems(items []tuiDatabaseItem) {
	sort.SliceStable(items, func(i, j int) bool {
		leftEngine, rightEngine := databaseEngineOrder(items[i].Engine), databaseEngineOrder(items[j].Engine)
		if leftEngine != rightEngine {
			return leftEngine < rightEngine
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == tuiDatabaseEngineItem
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
}

func databaseEngineOrder(engine string) int {
	if engine == "postgresql" {
		return 0
	}
	if engine == "mariadb" {
		return 1
	}
	return 2
}

func runTUIDatabaseOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	item := operation.Database
	if operation.Target.Local {
		if operation.Action != "drop" || item.Kind != tuiDatabaseRowItem {
			return "", fmt.Errorf("unsupported local database TUI action %q", operation.Action)
		}
		engine, err := normalizeLocalDatabaseEngine(item.Engine)
		if err != nil {
			return "", err
		}
		name, err := validateDatabaseIdentity("database", item.Name)
		if err != nil {
			return "", err
		}
		if _, err := loadObservedLocalDatabase(ctx, client, engine, name); err != nil {
			return "", err
		}
		endpoint := "/api/databases/" + url.PathEscape(engine) + "/" + url.PathEscape(name)
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodDelete, endpoint, map[string]string{"confirm": "DROP " + name}, true)
		return databaseOperationMessage(response, "Database dropped", err)
	}

	if !operation.Target.Online {
		return "", errors.New("managed node is offline")
	}
	if operation.Action != "restart" || item.Kind != tuiDatabaseEngineItem {
		return "", fmt.Errorf("unsupported managed database TUI action %q", operation.Action)
	}
	if !operation.Target.capability(agenthub.CapabilityDatabaseAction) {
		return "", errors.New("managed agent does not advertise database.action")
	}
	engine, err := normalizeRemoteDatabaseEngine(item.Engine)
	if err != nil {
		return "", err
	}
	engines, err := loadRemoteDatabaseEngines(ctx, client, operation.Target.ID)
	if err != nil {
		return "", err
	}
	if _, err := findRemoteDatabaseEngine(engines, engine); err != nil {
		return "", err
	}
	endpoint := "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/databases/" + url.PathEscape(engine) + "/actions/restart"
	response, err := requestJSON[map[string]any](ctx, client.withTimeout(3*time.Minute), http.MethodPost, endpoint, nil, true)
	return databaseOperationMessage(response, item.EngineName+" restarted and health-checked", err)
}

func databaseOperationMessage(response map[string]any, fallback string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if message, ok := response["message"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message), nil
	}
	return fallback, nil
}
