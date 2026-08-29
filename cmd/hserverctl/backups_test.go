package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunBackupCommandsUseLocalAndManagedContracts(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/backups":
			_, _ = writer.Write([]byte(`{"backups":[{"id":"backup-1","name":"backup-1.tar.gz","status":"completed"}],"storage":{"totalBytes":1024}}`))
		case "GET /api/nodes/edge-1/backups":
			_, _ = writer.Write([]byte(`[{"id":"nightly","name":"Nightly","verified":true,"total_size":2048}]`))
		case "POST /api/backups":
			var body struct {
				Type        string   `json:"type"`
				Name        string   `json:"name"`
				Engine      string   `json:"engine"`
				Database    string   `json:"database"`
				Compression int      `json:"compression"`
				Retention   int      `json:"retention"`
				Vhosts      []string `json:"vhosts"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body.Type != "full" || body.Name != "manual-1" || body.Engine != "mariadb" || body.Database != "portal" || body.Compression != 7 || body.Retention != 14 || !reflect.DeepEqual(body.Vhosts, []string{"example.com", "api.example.com"}) {
				t.Errorf("create body = %#v", body)
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"jobId":"job-1","status":"pending","message":"backup started in background"}`))
		case "POST /api/nodes/edge-1/backups/nightly/run":
			_, _ = writer.Write([]byte(`{"message":"Nightly backup completed"}`))
		case "GET /api/backups/jobs":
			if request.URL.Query().Get("hours") != "12.5" || request.URL.Query().Get("active") != "1" {
				t.Errorf("jobs query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"jobs":[{"id":"job-1","status":"running"}]}`))
		case "GET /api/backups/jobs/job-1":
			_, _ = writer.Write([]byte(`{"id":"job-1","status":"done"}`))
		case "GET /api/backups/restore/backup-1/validate":
			_, _ = writer.Write([]byte(`{"id":"backup-1","type":"full","databaseRecovery":true,"filesRollback":true}`))
		case "POST /api/backups/restore/backup-1":
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"jobId":"restore-1","status":"pending","message":"restore started in background"}`))
		case "DELETE /api/backups/backup-1":
			_, _ = writer.Write([]byte(`{"status":"deleted","id":"backup-1"}`))
		case "GET /api/backups/snapshot/status":
			if request.URL.Query().Get("refresh") != "" && request.URL.Query().Get("refresh") != "1" {
				t.Errorf("snapshot refresh query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"destination":"gdrive","destinationStatus":"healthy","resticFound":true,"passwordSet":true,"canPurgeRepository":true,"settings":{"destination":"gdrive","repoFolder":"hserver-snapshots","enabledPaths":["vhosts"],"keepDaily":14,"keepWeekly":8,"keepMonthly":6,"passwordAcknowledged":true}}`))
		case "GET /api/backups/snapshot/list":
			_, _ = writer.Write([]byte(`{"snapshots":[]}`))
		case "GET /api/backups/snapshot/vhosts":
			_, _ = writer.Write([]byte(`{"vhosts":["example.com"]}`))
		case "POST /api/backups/snapshot/run":
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"status":"running","jobId":"snapshot-1"}`))
		case "POST /api/backups/snapshot/restore":
			var body struct {
				SnapshotID  string   `json:"snapshotId"`
				ManifestIDs []string `json:"manifestIds"`
				Vhosts      []string `json:"vhosts"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode snapshot restore: %v", err)
			}
			if body.SnapshotID != "abcdef1234567890" || !reflect.DeepEqual(body.ManifestIDs, []string{"nginx"}) || !reflect.DeepEqual(body.Vhosts, []string{"example.com"}) {
				t.Errorf("snapshot restore body = %#v", body)
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"status":"restoring","jobId":"snapshot-restore-1"}`))
		case "POST /api/backups/snapshot/purge-repo":
			var body struct {
				RepoFolder   string `json:"repoFolder"`
				Confirmation string `json:"confirmation"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode snapshot purge: %v", err)
			}
			if body.RepoFolder != "hserver-snapshots" || body.Confirmation != "purge-snapshot-repository" {
				t.Errorf("snapshot purge body = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"success":true}`))
		case "GET /api/backups/snapshot/settings":
			_, _ = writer.Write([]byte(`{"destination":"gdrive","repoFolder":"hserver-snapshots","enabledPaths":["vhosts"],"keepDaily":14,"keepWeekly":8,"keepMonthly":6,"passwordAcknowledged":true}`))
		case "PUT /api/backups/snapshot/settings":
			var body cliSnapshotSettings
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode snapshot settings: %v", err)
			}
			if body.Destination != "s3" || body.RepoFolder != "hserver-snapshots" || !reflect.DeepEqual(body.EnabledPaths, []string{"vhosts"}) || body.KeepDaily != 14 || !body.PasswordAcknowledged {
				t.Errorf("snapshot settings body = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"success":true}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	commands := [][]string{
		{"backups", "list"},
		{"backups", "list", "--node", "edge-1"},
		{"backups", "create", "--confirm", "--type", "full", "--name", "manual-1", "--engine", "mariadb", "--database", "portal", "--compression", "7", "--retention", "14", "--vhost", "example.com", "--vhost", "api.example.com"},
		{"backups", "run", "--confirm", "--node", "edge-1", "nightly"},
		{"backups", "jobs", "--hours", "12.5", "--active"},
		{"backups", "job", "job-1"},
		{"backups", "validate", "backup-1"},
		{"backups", "restore", "--confirm", "--validated", "backup-1"},
		{"backups", "delete", "--confirm", "backup-1"},
		{"backups", "snapshot", "status"},
		{"backups", "snapshot", "list"},
		{"backups", "snapshot", "vhosts"},
		{"backups", "snapshot", "run", "--confirm"},
		{"backups", "snapshot", "restore", "--confirm", "--manifest", "nginx", "--vhost", "example.com", "abcdef1234567890"},
		{"backups", "snapshot", "destination", "s3"},
		{"backups", "snapshot", "purge", "--confirm", "--repository", "hserver-snapshots"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, testBackupEnvironment); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
	}
	if requests.Load() != int32(len(commands)+2) { // destination and purge each perform one observed-state read before mutation
		t.Fatalf("requests = %d, commands = %d", requests.Load(), len(commands))
	}
}

func TestRunBackupCommandsRejectUnsafeInputsBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"backups", "create"}, want: "explicit --confirm"},
		{args: []string{"backups", "create", "--confirm", "--type", "archive"}, want: "full, database, or files"},
		{args: []string{"backups", "create", "--confirm", "--engine", "sqlite"}, want: "postgresql or mariadb"},
		{args: []string{"backups", "create", "--confirm", "--compression", "10"}, want: "between 1 and 9"},
		{args: []string{"backups", "create", "--confirm", "--retention", "366"}, want: "between 0 and 365"},
		{args: []string{"backups", "create", "--confirm", "--vhost", "example.com", "--vhost", "example.com"}, want: "duplicate vhost"},
		{args: []string{"backups", "create", "--confirm", "--type", "database", "--vhost", "example.com"}, want: "cannot select vhosts"},
		{args: []string{"backups", "run", "--confirm", "nightly"}, want: "--node NODE"},
		{args: []string{"backups", "run", "--node", "edge-1", "nightly"}, want: "explicit --confirm"},
		{args: []string{"backups", "jobs", "--hours", "169"}, want: "at most 168"},
		{args: []string{"backups", "job", "../job"}, want: "portable name"},
		{args: []string{"backups", "validate", "/backup"}, want: "portable name"},
		{args: []string{"backups", "restore", "--validated", "backup-1"}, want: "explicit --confirm"},
		{args: []string{"backups", "restore", "--confirm", "backup-1"}, want: "explicit --validated"},
		{args: []string{"backups", "delete", "backup-1"}, want: "explicit --confirm"},
		{args: []string{"backups", "snapshot", "run"}, want: "explicit --confirm"},
		{args: []string{"backups", "snapshot", "restore", "--all", "abcdef1234567890"}, want: "explicit --confirm"},
		{args: []string{"backups", "snapshot", "restore", "--confirm", "abcdef1234567890"}, want: "requires --all"},
		{args: []string{"backups", "snapshot", "restore", "--confirm", "--all", "not-hex"}, want: "hexadecimal"},
		{args: []string{"backups", "snapshot", "restore", "--confirm", "--all", "--manifest", "nginx", "abcdef1234567890"}, want: "cannot be combined"},
		{args: []string{"backups", "snapshot", "restore", "--confirm", "--manifest", "root-crontab", "abcdef1234567890"}, want: "not selectable"},
		{args: []string{"backups", "snapshot", "restore", "--confirm", "--manifest", "nginx", "--manifest", "nginx", "abcdef1234567890"}, want: "duplicate snapshot manifest"},
		{args: []string{"backups", "snapshot", "restore", "--confirm", "--manifest", "vhosts", "--vhost", "example.com", "abcdef1234567890"}, want: "either the vhosts manifest"},
		{args: []string{"backups", "snapshot", "destination", "ftp"}, want: "gdrive or s3"},
		{args: []string{"backups", "snapshot", "purge", "--repository", "hserver-snapshots"}, want: "explicit --confirm"},
		{args: []string{"backups", "snapshot", "purge", "--confirm", "--repository", "../snapshots"}, want: "portable relative"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, testBackupEnvironment)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}

func TestSnapshotPurgeRequiresObservedSupportedRepository(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		name       string
		status     string
		repository string
		want       string
	}{
		{
			name:       "unsupported destination",
			status:     `{"canPurgeRepository":false,"settings":{"repoFolder":"hserver-snapshots"}}`,
			repository: "hserver-snapshots",
			want:       "does not support repository purge",
		},
		{
			name:       "stale repository confirmation",
			status:     `{"canPurgeRepository":true,"settings":{"repoFolder":"current-snapshots"}}`,
			repository: "old-snapshots",
			want:       "does not match observed repository",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Method != http.MethodGet || request.URL.Path != "/api/backups/snapshot/status" || request.URL.Query().Get("refresh") != "1" {
					t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(item.status))
			}))
			defer server.Close()

			args := []string{"--server", server.URL, "backups", "snapshot", "purge", "--confirm", "--repository", item.repository}
			err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, testBackupEnvironment)
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("error = %v", err)
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d, want one observed-status read", requests.Load())
			}
		})
	}
}

func TestRunBackupScheduleCommandsUseAuthenticatedContracts(t *testing.T) {
	t.Parallel()
	const rawLine = "0 3 * * * /var/lib/hserver/backups/run-backup.sh type=full retention=10 # hserver-backup"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/backups/schedules":
			if request.Body != nil {
				defer request.Body.Close()
			}
			_, _ = writer.Write([]byte(`{"schedules":[{"rawLine":"` + rawLine + `","frequency":"daily","time":"03:00","retention_count":10}]}`))
		case "POST /api/backups/schedules":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode schedule body: %v", err)
			}
			switch requests.Load() {
			case 2:
				if body["frequency"] != "daily" || body["time"] != "03:00" || body["retention_count"] != float64(14) || body["type"] != "database" || body["database"] != "portal" {
					t.Errorf("frequency schedule body = %#v", body)
				}
				if _, present := body["cron"]; present {
					t.Errorf("frequency schedule unexpectedly included cron: %#v", body)
				}
			case 3:
				if body["cron"] != "15 2 * * 1" || body["retention_count"] != float64(7) {
					t.Errorf("cron schedule body = %#v", body)
				}
				for _, key := range []string{"frequency", "time", "type", "database"} {
					if _, present := body[key]; present {
						t.Errorf("cron schedule unexpectedly included %s: %#v", key, body)
					}
				}
			default:
				t.Errorf("unexpected schedule POST number %d", requests.Load())
			}
			_, _ = writer.Write([]byte(`{"success":true}`))
		case "DELETE /api/backups/schedules":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode delete schedule body: %v", err)
			}
			if body["rawLine"] != rawLine {
				t.Errorf("delete schedule body = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"success":true}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	commands := [][]string{
		{"backups", "schedule", "list"},
		{"backups", "schedule", "set", "--confirm", "--frequency", "daily", "--time", "03:00", "--retention-count", "14", "--type", "database", "--database", "portal"},
		{"backups", "schedule", "set", "--confirm", "--cron", "15 2 * * 1", "--retention-count", "7"},
		{"backups", "schedule", "delete", "--confirm", "--raw-line", rawLine},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, testBackupEnvironment); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
	}
	if requests.Load() != int32(len(commands)) {
		t.Fatalf("requests = %d, commands = %d", requests.Load(), len(commands))
	}
}

func TestRunBackupScheduleRejectsInvalidInputsBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	const rawLine = "0 3 * * * /var/lib/hserver/backups/run-backup.sh type=full retention=10 # hserver-backup"
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"backups", "schedule", "set", "--frequency", "daily", "--time", "03:00"}, want: "explicit --confirm"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--cron", "0 3 * * *", "--frequency", "daily", "--time", "03:00"}, want: "either --cron"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--frequency", "daily"}, want: "both --frequency and --time"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--frequency", "hourly", "--time", "03:00"}, want: "frequency must be daily"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--frequency", "daily", "--time", "3:00"}, want: "time must use HH:MM"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--frequency", "daily", "--time", "24:00"}, want: "time must use HH:MM"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--frequency", "daily", "--time", "03:00", "--retention-count", "0"}, want: "retention count must be between"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--frequency", "daily", "--time", "03:00", "--retention-count", "366"}, want: "retention count must be between"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--frequency", "daily", "--time", "03:00", "--type", "archive"}, want: "type must be full"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--frequency", "daily", "--time", "03:00", "--database", "db name"}, want: "portable name"},
		{args: []string{"backups", "schedule", "set", "--confirm", "--cron", "0 3 * * *; rm -rf /"}, want: "standard five-field"},
		{args: []string{"backups", "schedule", "delete", "--raw-line", rawLine}, want: "explicit --confirm"},
		{args: []string{"backups", "schedule", "delete", "--confirm"}, want: "raw-line is required"},
		{args: []string{"backups", "schedule", "delete", "--confirm", "--raw-line", "0 3 * * * /var/lib/hserver/backups/run-backup.sh type=full retention=10"}, want: "observed hserver-backup line"},
		{args: []string{"backups", "schedule", "delete", "--confirm", "--raw-line", "0 3 * * x /var/lib/hserver/backups/run-backup.sh type=full retention=10 # hserver-backup"}, want: "standard five-field"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, testBackupEnvironment)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}

func testBackupEnvironment(key string) string {
	if key == "HSERVER_TOKEN" {
		return "test-token"
	}
	return ""
}
