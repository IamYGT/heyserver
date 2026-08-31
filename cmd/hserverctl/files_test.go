package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFilesCommandsUseBoundedLocalAndManagedEndpoints(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contentFile := filepath.Join(directory, "replacement.txt")
	if err := os.WriteFile(contentFile, []byte("server {\n  listen 8080;\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checksumBytes := sha256.Sum256([]byte("old\n"))
	checksum := hex.EncodeToString(checksumBytes[:])

	var remoteWrites atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/files" && request.URL.Query().Get("path") == "":
			_, _ = io.WriteString(writer, `{"roots":["/srv/files"],"entries":[]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/files" && request.URL.Query().Get("path") == "/srv/files":
			_, _ = io.WriteString(writer, `{"path":"/srv/files","entries":[{"name":"app.conf","path":"/srv/files/app.conf","type":"file","size":4,"permissions":"-rw-r--r--"},{"name":"old","path":"/srv/files/old","type":"directory","size":0,"permissions":"drwxr-xr-x"}]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/files" && request.URL.Query().Get("path") == "/srv/files/archive":
			_, _ = io.WriteString(writer, `{"path":"/srv/files/archive","entries":[]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/files/read":
			_, _ = io.WriteString(writer, `{"path":"/srv/files/app.conf","content":"old\n"}`)
		case request.Method == http.MethodPut && request.URL.Path == "/api/files/write":
			_, _ = io.WriteString(writer, `{"status":"ok","path":"/srv/files/app.conf"}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/files/create":
			_, _ = io.WriteString(writer, `{"status":"created","path":"/srv/files/new.conf"}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/files/rename":
			_, _ = io.WriteString(writer, `{"status":"renamed","src":"/srv/files/app.conf","dst":"/srv/files/archive/app.conf"}`)
		case request.Method == http.MethodDelete && request.URL.Path == "/api/files":
			_, _ = io.WriteString(writer, `{"status":"deleted","path":"/srv/files/old"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west":
			_, _ = io.WriteString(writer, `{"id":"edge west","online":true,"capabilities":["files.read","files.write"],"inventory":{"file_read_roots":["/srv/managed"],"file_write_roots":["/srv/managed"]}}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west/files":
			_, _ = io.WriteString(writer, `[{"name":"app.conf","path":"/srv/managed/app.conf","type":"file","size":4,"mode":"-rw-r--r--","modified_at":"2026-08-27T00:00:00Z"}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west/file":
			_, _ = io.WriteString(writer, `{"path":"/srv/managed/app.conf","content":"old\n","checksum":"`+checksum+`","size":4,"mode":"-rw-r--r--"}`)
		case request.Method == http.MethodPut && request.URL.Path == "/api/nodes/edge west/file":
			remoteWrites.Add(1)
			_, _ = io.WriteString(writer, `{"message":"Remote file saved","backup":"/srv/managed/app.conf.hserver-backup"}`)
		default:
			http.Error(writer, request.Method+" "+request.URL.RequestURI(), http.StatusNotFound)
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
		{"files", "roots"},
		{"files", "list", "/srv/files"},
		{"files", "read", "/srv/files/app.conf"},
		{"files", "save", "--confirm", "--content-file", contentFile, "/srv/files/app.conf"},
		{"files", "create", "--confirm", "--type", "file", "/srv/files/new.conf"},
		{"files", "rename", "--confirm", "/srv/files/app.conf", "/srv/files/archive/app.conf"},
		{"files", "delete", "--confirm", "/srv/files/old"},
		{"files", "roots", "--node", "edge west"},
		{"files", "list", "--node", "edge west", "/srv/managed"},
		{"files", "read", "--node", "edge west", "/srv/managed/app.conf"},
		{"files", "save", "--confirm", "--node", "edge west", "--checksum", checksum, "--content-file", contentFile, "/srv/managed/app.conf"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s failed: %v", strings.Join(command, " "), err)
		}
		if output.Len() == 0 {
			t.Fatalf("%s returned no JSON", strings.Join(command, " "))
		}
	}
	if remoteWrites.Load() != 1 {
		t.Fatalf("remote writes = %d", remoteWrites.Load())
	}
}

func TestFilesCommandsRejectUnsafeInputBeforeHTTP(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contentFile := filepath.Join(directory, "replacement.txt")
	if err := os.WriteFile(contentFile, []byte("replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "replacement-link.txt")
	if err := os.Symlink(contentFile, link); err != nil {
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
		{args: []string{"files", "list", "relative/path"}, want: "clean absolute path"},
		{args: []string{"files", "read", "/srv/files/../secret"}, want: "clean absolute path"},
		{args: []string{"files", "save", "--content-file", contentFile, "/srv/files/app.conf"}, want: "explicit --confirm"},
		{args: []string{"files", "save", "--confirm", "--node", "edge-1", "--content-file", contentFile, "/srv/files/app.conf"}, want: "64-character SHA-256"},
		{args: []string{"files", "save", "--confirm", "--content-file", contentFile, "/etc/nginx/nginx.conf"}, want: "dedicated Heyserver management surface"},
		{args: []string{"files", "save", "--confirm", "--content-file", link, "/srv/files/app.conf"}, want: "not a symlink"},
		{args: []string{"files", "create", "--confirm", "--type", "socket", "/srv/files/new"}, want: "file or directory"},
		{args: []string{"files", "rename", "/srv/files/a", "/srv/files/b"}, want: "explicit --confirm"},
		{args: []string{"files", "delete", "--confirm", "--wait", "0s", "/srv/files/old"}, want: "greater than zero"},
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

func TestFilesManagedSaveRejectsChangedChecksumBeforeWrite(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contentFile := filepath.Join(directory, "replacement.txt")
	if err := os.WriteFile(contentFile, []byte("replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := strings.Repeat("a", 64)
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge-1":
			_, _ = io.WriteString(writer, `{"id":"edge-1","online":true,"capabilities":["files.read","files.write"],"inventory":{"file_read_roots":["/srv/managed"],"file_write_roots":["/srv/managed"]}}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge-1/file":
			_, _ = io.WriteString(writer, `{"path":"/srv/managed/app.conf","content":"newer","checksum":"`+strings.Repeat("b", 64)+`"}`)
		case request.Method == http.MethodPut:
			writes.Add(1)
			_, _ = io.WriteString(writer, `{}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	args := []string{"--server", server.URL, "files", "save", "--confirm", "--node", "edge-1", "--checksum", expected, "--content-file", contentFile, "/srv/managed/app.conf"}
	err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatalf("error = %v", err)
	}
	if writes.Load() != 0 {
		t.Fatalf("changed checksum sent %d write(s)", writes.Load())
	}
}

func TestFilesRootsReportsLastObservedOfflineNodeConfiguration(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"edge-1","online":false,"capabilities":["files.read"],"inventory":{"file_read_roots":["/srv/apps"]}}`)
	}))
	defer server.Close()
	var output bytes.Buffer
	err := run(context.Background(), []string{"--server", server.URL, "files", "roots", "--node", "edge-1"}, &output, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if err != nil || !strings.Contains(output.String(), `"online": false`) || !strings.Contains(output.String(), "/srv/apps") {
		t.Fatalf("output=%q error=%v", output.String(), err)
	}
}

func TestCLIHelpAndCompletionExposeFileCommands(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"files roots", "files list", "files read", "files save", "files create", "files rename", "files delete"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q: %q", command, help.String())
		}
	}
	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "files") {
		t.Fatalf("completion does not expose files: %q", completion.String())
	}
}
