package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestLoadTUIFilesBrowsesLocalAndManagedRoots(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/files":
			if path := request.URL.Query().Get("path"); path == "" {
				_, _ = io.WriteString(writer, `{"roots":["/srv/files","/var/log"],"entries":[]}`)
				return
			}
			_, _ = io.WriteString(writer, `{"path":"/srv/files","entries":[{"name":"z.conf","path":"/srv/files/z.conf","type":"file","size":12,"permissions":"-rw-r--r--"},{"name":"apps","path":"/srv/files/apps","type":"directory","size":0,"permissions":"drwxr-xr-x"}]}`)
		case "/api/nodes/edge-1/files":
			_, _ = io.WriteString(writer, `[{"name":"remote.conf","path":"/srv/managed/remote.conf","type":"file","size":8,"mode":"-rw-r--r--"}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	local, err := loadTUIFiles(context.Background(), client, initialTUITargets()[0], "/srv/files")
	if err != nil {
		t.Fatal(err)
	}
	if !local.Manageable || local.CurrentRoot != "/srv/files" || len(local.Roots) != 2 || len(local.Entries) != 2 || local.Entries[0].Type != "directory" {
		t.Fatalf("local = %#v", local)
	}
	remoteTarget := tuiTarget{
		ID: "edge-1", Name: "Edge", Online: true,
		Capabilities: map[string]bool{agenthub.CapabilityFilesRead: true, agenthub.CapabilityFilesWrite: true},
		Inventory:    agenthub.Inventory{FileReadRoots: []string{"/srv/managed"}, FileWriteRoots: []string{"/srv/managed"}},
	}
	remote, err := loadTUIFiles(context.Background(), client, remoteTarget, "")
	if err != nil {
		t.Fatal(err)
	}
	if remote.Manageable || remote.CurrentPath != "/srv/managed" || len(remote.Entries) != 1 || remote.Entries[0].Name != "remote.conf" {
		t.Fatalf("remote = %#v", remote)
	}

	remoteTarget.Capabilities = map[string]bool{}
	if _, err := loadTUIFiles(context.Background(), client, remoteTarget, ""); err == nil || !strings.Contains(err.Error(), "files.read") {
		t.Fatalf("missing capability error = %v", err)
	}
	remoteTarget.Online = false
	if _, err := loadTUIFiles(context.Background(), client, remoteTarget, ""); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline error = %v", err)
	}
}

func TestTUIFileViewerAndDeleteUseObservedEntries(t *testing.T) {
	t.Parallel()
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/files" && request.URL.Query().Get("path") == "":
			_, _ = io.WriteString(writer, `{"roots":["/srv/files"],"entries":[]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/files" && request.URL.Query().Get("path") == "/srv/files":
			_, _ = io.WriteString(writer, `{"path":"/srv/files","entries":[{"name":"app.conf","path":"/srv/files/app.conf","type":"file","size":12,"permissions":"-rw-r--r--"}]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/files/read":
			_, _ = io.WriteString(writer, `{"path":"/srv/files/app.conf","content":"line one\nline two\n"}`)
		case request.Method == http.MethodDelete && request.URL.Path == "/api/files":
			deletes.Add(1)
			_, _ = io.WriteString(writer, `{"status":"deleted","path":"/srv/files/app.conf"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	entry := cliFileEntry{Name: "app.conf", Path: "/srv/files/app.conf", Type: "file", Size: 12, Permissions: "-rw-r--r--"}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabFiles
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.filesLoaded = true
	model.filesTarget = localTargetID
	model.files = tuiFileState{Roots: []string{"/srv/files"}, CurrentRoot: "/srv/files", CurrentPath: "/srv/files", Entries: []cliFileEntry{entry}, Manageable: true}

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command == nil || !model.resourceLoading {
		t.Fatal("Enter did not start the file viewer")
	}
	message := command().(tuiFileContentMsg)
	if message.Err != nil || len(message.Lines) != 3 {
		t.Fatalf("file content message = %#v", message)
	}
	updated, _ = model.Update(message)
	model = updated.(tuiModel)
	if model.dialog.Mode != tuiDialogLogs || model.dialog.FilePath != entry.Path || !strings.Contains(model.View().Content, "line one") {
		t.Fatalf("viewer dialog = %#v", model.dialog)
	}
	model.dialog = tuiDialog{}

	updated, command = model.updateKey("x")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || deletes.Load() != 0 {
		t.Fatalf("delete confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	if command != nil || deletes.Load() != 0 {
		t.Fatal("Enter bypassed file deletion confirmation")
	}
	model = updated.(tuiModel)
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start file deletion")
	}
	result := command().(tuiOperationMsg)
	if result.Err != nil || !strings.Contains(result.Message, "Deleted /srv/files/app.conf") || deletes.Load() != 1 {
		t.Fatalf("delete result = %#v; requests=%d", result, deletes.Load())
	}
}

func TestTUIFileNavigationStaysInsideCurrentRoot(t *testing.T) {
	t.Parallel()
	model := tuiModel{
		filesLoaded: true,
		files: tuiFileState{
			Roots: []string{"/srv/a", "/srv/b"}, CurrentRoot: "/srv/a", CurrentPath: "/srv/a/apps/site",
		},
	}
	selected, root, err := selectTUIFilePath("/srv/a/apps", model.files.Roots)
	if err != nil || selected != "/srv/a/apps" || root != "/srv/a" {
		t.Fatalf("selected=%q root=%q err=%v", selected, root, err)
	}
	if _, _, err := selectTUIFilePath("/etc/passwd", model.files.Roots); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside-root error = %v", err)
	}
	if _, err := splitTUIFileContent(strings.Repeat("line\n", maxTUIFileLines+1)); err == nil || !strings.Contains(err.Error(), "line") {
		t.Fatalf("line limit error = %v", err)
	}
	if _, err := splitTUIFileContent("text\x00binary"); err == nil || !strings.Contains(err.Error(), "NUL-free") {
		t.Fatalf("text boundary error = %v", err)
	}
}
