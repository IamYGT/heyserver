package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunDatabaseCommandsUseLocalAndManagedBoundaries(t *testing.T) {
	t.Parallel()
	queryFile := filepath.Join(t.TempDir(), "query.sql")
	if err := os.WriteFile(queryFile, []byte("SELECT id, name FROM jobs LIMIT 10;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/databases":
			if engine := request.URL.Query().Get("engine"); engine != "" && engine != "postgres" {
				t.Errorf("local list engine = %q", engine)
			}
			_, _ = writer.Write([]byte(`{"databases":[{"name":"portal","engine":"postgres","owner":"portal","size":"16 MB","tables":12}],"sources":{"postgresql":{"available":true,"state":"healthy"}}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/databases/users":
			if request.URL.Query().Get("engine") != "mariadb" {
				t.Errorf("users engine = %q", request.URL.Query().Get("engine"))
			}
			_, _ = writer.Write([]byte(`{"users":[{"name":"app","engine":"mariadb","canLogin":true}],"sources":{"mariadb":{"available":true,"state":"healthy"}}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/databases/postgres/portal/tables":
			_, _ = writer.Write([]byte(`{"database":"portal","engine":"postgres","tables":[{"name":"jobs","schema":"public","rowsEstimate":10,"size":"8 kB","tableType":"BASE TABLE"}]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/databases/postgres/portal/query":
			var payload struct {
				Query     string `json:"query"`
				WriteMode bool   `json:"write_mode"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Query != "SELECT id, name FROM jobs LIMIT 10;" || payload.WriteMode {
				t.Errorf("query payload = %#v, err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"result":{"columns":["id","name"],"rows":[[1,"backup"]],"rowCount":1}}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/databases":
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["engine"] != "postgres" || payload["name"] != "analytics" || payload["owner"] != "reporter" {
				t.Errorf("create payload = %#v, err=%v", payload, err)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"message":"database created successfully","name":"analytics","engine":"postgres"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/databases/postgres/portal":
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["confirm"] != "DROP portal" {
				t.Errorf("drop payload = %#v, err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"database dropped","name":"portal"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west/databases":
			_, _ = writer.Write([]byte(`[{"id":"postgresql","name":"PostgreSQL","version":"16.4","unit":"postgresql@16-main.service","active":"active","data_size":16777216,"databases":[{"name":"portal","size":16777216,"connections":2,"objects":12}],"sessions":[]}]`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/nodes/edge west/databases/postgresql/actions/restart":
			_, _ = writer.Write([]byte(`{"message":"PostgreSQL restarted and readiness check passed"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	commands := [][]string{
		{"databases", "list"},
		{"databases", "list", "--engine", "postgresql"},
		{"databases", "users", "--engine", "mysql"},
		{"databases", "tables", "--engine", "postgres", "portal"},
		{"databases", "query", "--engine", "postgres", "--query-file", queryFile, "portal"},
		{"databases", "create", "--confirm", "--engine", "postgresql", "--owner", "reporter", "analytics"},
		{"databases", "drop", "--confirm", "--engine", "postgres", "portal"},
		{"databases", "list", "--node", "edge west"},
		{"databases", "restart", "--confirm", "--node", "edge west", "postgres"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
	}
	if requests.Load() != 11 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunDatabasesRejectsUnsafeOrUnconfirmedInputBeforeRequest(t *testing.T) {
	t.Parallel()
	readQuery := filepath.Join(t.TempDir(), "select.sql")
	writeQuery := filepath.Join(t.TempDir(), "delete.sql")
	if err := os.WriteFile(readQuery, []byte("SELECT 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writeQuery, []byte("DELETE FROM jobs"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"databases", "create", "--engine", "postgres", "portal"}, want: "explicit --confirm"},
		{args: []string{"databases", "create", "--confirm", "--engine", "sqlite", "portal"}, want: "postgres or mariadb"},
		{args: []string{"databases", "create", "--confirm", "--engine", "mariadb", "--owner", "root", "portal"}, want: "only for PostgreSQL"},
		{args: []string{"databases", "drop", "--confirm", "--engine", "postgres", "../portal"}, want: "portable identifier"},
		{args: []string{"databases", "query", "--engine", "postgres", "--query-file", writeQuery, "portal"}, want: "must begin with SELECT or WITH"},
		{args: []string{"databases", "query", "--engine", "postgres", "--query-file", readQuery, "--wait", "0s", "portal"}, want: "greater than zero"},
		{args: []string{"databases", "list", "--node", "edge-1", "--engine", "postgres"}, want: "only for local"},
		{args: []string{"databases", "restart", "--node", "edge-1", "postgresql"}, want: "explicit --confirm"},
		{args: []string{"databases", "restart", "--confirm", "postgresql"}, want: "--node NODE"},
		{args: []string{"databases", "restart", "--confirm", "--node", "edge-1", "sqlite"}, want: "postgres or mariadb"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}

func TestReadDatabaseQueryFileRejectsSymlink(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "query.sql")
	link := filepath.Join(directory, "query-link.sql")
	if err := os.WriteFile(target, []byte("SELECT 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readDatabaseQueryFile(link); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestCLIHelpAndCompletionExposeDatabaseCommands(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"databases list", "databases users", "databases tables", "databases query", "databases create", "databases drop", "databases restart"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q: %q", command, help.String())
		}
	}
	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "databases") {
		t.Fatalf("completion does not expose databases: %q", completion.String())
	}
}
