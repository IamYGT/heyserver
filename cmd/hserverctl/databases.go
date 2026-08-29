package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const maxDatabaseQueryBytes = 64 << 10

var databaseIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type databaseInfo struct {
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Owner  string `json:"owner"`
	Size   string `json:"size"`
	Tables int    `json:"tables"`
}

type databaseSource struct {
	Available bool   `json:"available"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
}

type databaseListResponse struct {
	Databases []databaseInfo            `json:"databases"`
	Sources   map[string]databaseSource `json:"sources"`
}

type remoteDatabase struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Connections int    `json:"connections"`
	Objects     int    `json:"objects"`
}

type remoteDatabaseSession struct {
	ID       string `json:"id"`
	User     string `json:"user"`
	Database string `json:"database,omitempty"`
	State    string `json:"state"`
	Age      int    `json:"age_seconds"`
	Query    string `json:"query,omitempty"`
}

type remoteDatabaseEngine struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Version   string                  `json:"version"`
	Unit      string                  `json:"unit"`
	Active    string                  `json:"active"`
	DataSize  int64                   `json:"data_size"`
	Databases []remoteDatabase        `json:"databases"`
	Sessions  []remoteDatabaseSession `json:"sessions"`
}

func runDatabases(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl databases list|users|tables|query|create|drop|restart")
	}
	switch args[0] {
	case "list":
		return runDatabasesList(ctx, client, args[1:], out)
	case "users":
		return runDatabaseUsers(ctx, client, args[1:], out)
	case "tables":
		return runDatabaseTables(ctx, client, args[1:], out)
	case "query":
		return runDatabaseQuery(ctx, client, args[1:], out)
	case "create":
		return runDatabaseCreate(ctx, client, args[1:], out)
	case "drop":
		return runDatabaseDrop(ctx, client, args[1:], out)
	case "restart":
		return runDatabaseRestart(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown databases command %q", args[0])
	}
}

func runDatabasesList(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("databases list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	engine := flags.String("engine", "", "optional local engine filter")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl databases list [--node NODE] [--engine postgres|mariadb]")
	}
	selectedNode := strings.TrimSpace(*node)
	selectedEngine := strings.TrimSpace(*engine)
	if selectedNode != "" && selectedEngine != "" {
		return errors.New("--engine filtering is available only for local database inventory")
	}
	endpoint := "/api/databases"
	timeout := 30 * time.Second
	if selectedNode != "" {
		endpoint = "/api/nodes/" + url.PathEscape(selectedNode) + "/databases"
		timeout = 60 * time.Second
	} else if selectedEngine != "" {
		engineID, err := normalizeLocalDatabaseEngine(selectedEngine)
		if err != nil {
			return err
		}
		endpoint += "?engine=" + url.QueryEscape(engineID)
	}
	return printRequest(ctx, client.withTimeout(timeout), out, http.MethodGet, endpoint, nil, true)
}

func runDatabaseUsers(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("databases users", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	engine := flags.String("engine", "", "optional local engine filter")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl databases users [--engine postgres|mariadb]")
	}
	endpoint := "/api/databases/users"
	if strings.TrimSpace(*engine) != "" {
		engineID, err := normalizeLocalDatabaseEngine(*engine)
		if err != nil {
			return err
		}
		endpoint += "?engine=" + url.QueryEscape(engineID)
	}
	return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
}

func runDatabaseTables(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("databases tables", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	engine := flags.String("engine", "", "local database engine")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*engine) == "" {
		return errors.New("usage: hserverctl databases tables --engine postgres|mariadb DATABASE")
	}
	engineID, err := normalizeLocalDatabaseEngine(*engine)
	if err != nil {
		return err
	}
	name, err := validateDatabaseIdentity("database", flags.Args()[0])
	if err != nil {
		return err
	}
	endpoint := "/api/databases/" + url.PathEscape(engineID) + "/" + url.PathEscape(name) + "/tables"
	return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
}

func runDatabaseQuery(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("databases query", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	engine := flags.String("engine", "", "local database engine")
	queryFile := flags.String("query-file", "", "regular UTF-8 file containing one read-only SELECT or WITH query")
	wait := flags.Duration("wait", 45*time.Second, "maximum query wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*engine) == "" || strings.TrimSpace(*queryFile) == "" {
		return errors.New("usage: hserverctl databases query --engine postgres|mariadb --query-file PATH [--wait DURATION] DATABASE")
	}
	if *wait <= 0 {
		return errors.New("database query wait must be greater than zero")
	}
	engineID, err := normalizeLocalDatabaseEngine(*engine)
	if err != nil {
		return err
	}
	name, err := validateDatabaseIdentity("database", flags.Args()[0])
	if err != nil {
		return err
	}
	query, err := readDatabaseQueryFile(strings.TrimSpace(*queryFile))
	if err != nil {
		return err
	}
	upper := strings.ToUpper(strings.TrimSpace(query))
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return errors.New("database query file must begin with SELECT or WITH; write mode is not exposed by hserverctl")
	}
	endpoint := "/api/databases/" + url.PathEscape(engineID) + "/" + url.PathEscape(name) + "/query"
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, map[string]any{"query": query, "write_mode": false}, true)
}

func runDatabaseCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("databases create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm database creation")
	engine := flags.String("engine", "", "local database engine")
	owner := flags.String("owner", "", "optional PostgreSQL owner")
	wait := flags.Duration("wait", 45*time.Second, "maximum creation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*engine) == "" {
		return errors.New("usage: hserverctl databases create --confirm --engine postgres|mariadb [--owner USER] [--wait DURATION] DATABASE")
	}
	if !*confirmed {
		return errors.New("database creation requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("database creation wait must be greater than zero")
	}
	engineID, err := normalizeLocalDatabaseEngine(*engine)
	if err != nil {
		return err
	}
	name, err := validateDatabaseIdentity("database", flags.Args()[0])
	if err != nil {
		return err
	}
	ownerID := ""
	if strings.TrimSpace(*owner) != "" {
		ownerID, err = validateDatabaseIdentity("database owner", *owner)
		if err != nil {
			return err
		}
		if engineID != "postgres" {
			return errors.New("--owner is available only for PostgreSQL database creation")
		}
	}
	payload := map[string]string{"engine": engineID, "name": name, "owner": ownerID}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/databases", payload, true)
}

func runDatabaseDrop(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("databases drop", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm permanent deletion of the observed database")
	engine := flags.String("engine", "", "local database engine")
	wait := flags.Duration("wait", 45*time.Second, "maximum deletion wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*engine) == "" {
		return errors.New("usage: hserverctl databases drop --confirm --engine postgres|mariadb [--wait DURATION] DATABASE")
	}
	if !*confirmed {
		return errors.New("database drop requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("database drop wait must be greater than zero")
	}
	engineID, err := normalizeLocalDatabaseEngine(*engine)
	if err != nil {
		return err
	}
	name, err := validateDatabaseIdentity("database", flags.Args()[0])
	if err != nil {
		return err
	}
	if _, err := loadObservedLocalDatabase(ctx, client, engineID, name); err != nil {
		return err
	}
	endpoint := "/api/databases/" + url.PathEscape(engineID) + "/" + url.PathEscape(name)
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, endpoint, map[string]string{"confirm": "DROP " + name}, true)
}

func runDatabaseRestart(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("databases restart", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm managed database restart and health check")
	node := flags.String("node", "", "managed node ID")
	wait := flags.Duration("wait", 3*time.Minute, "maximum restart wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*node) == "" {
		return errors.New("usage: hserverctl databases restart --confirm --node NODE [--wait DURATION] postgresql|mariadb")
	}
	if !*confirmed {
		return errors.New("managed database restart requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("database restart wait must be greater than zero")
	}
	engineID, err := normalizeRemoteDatabaseEngine(flags.Args()[0])
	if err != nil {
		return err
	}
	selectedNode := strings.TrimSpace(*node)
	engines, err := loadRemoteDatabaseEngines(ctx, client, selectedNode)
	if err != nil {
		return err
	}
	if _, err := findRemoteDatabaseEngine(engines, engineID); err != nil {
		return err
	}
	endpoint := "/api/nodes/" + url.PathEscape(selectedNode) + "/databases/" + url.PathEscape(engineID) + "/actions/restart"
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
}

func normalizeLocalDatabaseEngine(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "postgres", "postgresql":
		return "postgres", nil
	case "mariadb", "mysql":
		return "mariadb", nil
	default:
		return "", errors.New("database engine must be postgres or mariadb")
	}
}

func normalizeRemoteDatabaseEngine(value string) (string, error) {
	local, err := normalizeLocalDatabaseEngine(value)
	if err != nil {
		return "", err
	}
	if local == "postgres" {
		return "postgresql", nil
	}
	return local, nil
}

func validateDatabaseIdentity(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if !databaseIdentityPattern.MatchString(value) {
		return "", fmt.Errorf("%s must be a 1-64 character portable identifier", label)
	}
	return value, nil
}

func readDatabaseQueryFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read database query file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("database query file must be a regular file and not a symlink")
	}
	if info.Size() > maxDatabaseQueryBytes {
		return "", fmt.Errorf("database query file exceeds %d bytes", maxDatabaseQueryBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read database query file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxDatabaseQueryBytes+1))
	if err != nil {
		return "", fmt.Errorf("read database query file: %w", err)
	}
	if len(data) == 0 {
		return "", errors.New("database query file is empty")
	}
	if len(data) > maxDatabaseQueryBytes {
		return "", fmt.Errorf("database query file exceeds %d bytes", maxDatabaseQueryBytes)
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return "", errors.New("database query file must be valid UTF-8 text without NUL bytes")
	}
	return strings.TrimSpace(string(data)), nil
}

func loadObservedLocalDatabase(ctx context.Context, client *apiClient, engine, name string) (databaseInfo, error) {
	response, err := requestJSON[databaseListResponse](ctx, client, http.MethodGet, "/api/databases?engine="+url.QueryEscape(engine), nil, true)
	if err != nil {
		return databaseInfo{}, err
	}
	for _, database := range response.Databases {
		currentEngine, normalizeErr := normalizeLocalDatabaseEngine(database.Engine)
		if normalizeErr == nil && currentEngine == engine && database.Name == name {
			return database, nil
		}
	}
	return databaseInfo{}, fmt.Errorf("%s database %s is no longer present", engine, name)
}

func loadRemoteDatabaseEngines(ctx context.Context, client *apiClient, node string) ([]remoteDatabaseEngine, error) {
	engines, err := requestJSON[[]remoteDatabaseEngine](ctx, client.withTimeout(60*time.Second), http.MethodGet,
		"/api/nodes/"+url.PathEscape(strings.TrimSpace(node))+"/databases", nil, true)
	if err != nil {
		return nil, err
	}
	for _, engine := range engines {
		if engine.ID != "postgresql" && engine.ID != "mariadb" {
			return nil, errors.New("managed database inventory returned an invalid engine identity")
		}
	}
	return engines, nil
}

func findRemoteDatabaseEngine(engines []remoteDatabaseEngine, id string) (remoteDatabaseEngine, error) {
	for _, engine := range engines {
		if engine.ID == id {
			return engine, nil
		}
	}
	return remoteDatabaseEngine{}, fmt.Errorf("managed database engine %s is no longer present", id)
}
