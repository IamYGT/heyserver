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

func TestLoadTUIPHPNormalizesLocalAndManagedInventory(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/php/versions":
			_, _ = io.WriteString(writer, `[{"version":"8.3","active":false},{"version":"8.4","active":true}]`)
		case "/api/php/pools":
			_, _ = io.WriteString(writer, `[{"name":"portal","version":"8.4","config_file":"/etc/php/8.4/fpm/pool.d/portal.conf","user":"portal","group":"portal","listen":"/run/php/portal.sock","pm":"dynamic","pm_settings":{"max_children":12}}]`)
		case "/api/nodes/edge-1/php":
			_, _ = io.WriteString(writer, `[{"version":"8.4","unit":"php8.4-fpm.service","active":"active","enabled":"enabled","binary":"/usr/sbin/php-fpm8.4","pools":[{"name":"portal","path":"/etc/php/8.4/fpm/pool.d/portal.conf","user":"portal","listen":"/run/php/portal.sock","pm":"ondemand","max_children":8}]}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	local, err := loadTUIPHP(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !local.Readable || !local.Writable || !local.Actionable || len(local.Items) != 3 || local.Items[0].Version != "8.4" || local.Items[0].Kind != tuiPHPVersionItem || local.Items[1].MaxChildren != 12 {
		t.Fatalf("local PHP state = %#v", local)
	}
	remoteTarget := tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{
		agenthub.CapabilityPHPRead: true, agenthub.CapabilityPHPWrite: true, agenthub.CapabilityPHPAction: true,
	}}
	remote, err := loadTUIPHP(context.Background(), client, remoteTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !remote.Readable || !remote.Writable || !remote.Actionable || len(remote.Items) != 2 || remote.Items[1].PM != "ondemand" {
		t.Fatalf("remote PHP state = %#v", remote)
	}
	remoteTarget.Capabilities = map[string]bool{}
	if _, err := loadTUIPHP(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "php.read") {
		t.Fatalf("missing capability error = %v", err)
	}
	remoteTarget.Online = false
	if _, err := loadTUIPHP(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline error = %v", err)
	}
}

func TestTUIPHPConfigViewerReobservesPoolAndCanonicalPath(t *testing.T) {
	t.Parallel()
	var localConfigReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/php/versions":
			_, _ = io.WriteString(writer, `[{"version":"8.4","active":true}]`)
		case "/api/php/pools":
			_, _ = io.WriteString(writer, `[{"name":"portal","version":"8.4","config_file":"/etc/php/8.4/fpm/pool.d/portal.conf"}]`)
		case "/api/php/pools/8.4/portal/config":
			if localConfigReads.Add(1) == 2 {
				_, _ = io.WriteString(writer, `{"path":"/etc/php/8.4/fpm/pool.d/other.conf","content":"[other]\n"}`)
				return
			}
			_, _ = io.WriteString(writer, `{"path":"/etc/php/8.4/fpm/pool.d/portal.conf","content":"[portal]\npm = dynamic\n","checksum":"0123456789abcdef"}`)
		case "/api/nodes/edge-1/php":
			_, _ = io.WriteString(writer, `[{"version":"8.4","binary":"/usr/sbin/php-fpm8.4","pools":[{"name":"portal","path":"/etc/php/8.4/fpm/pool.d/portal.conf"}]}]`)
		case "/api/nodes/edge-1/php/8.4/pools/portal":
			_, _ = io.WriteString(writer, `{"path":"/etc/php/8.4/fpm/pool.d/portal.conf","content":"[portal]\npm = dynamic\n"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	item := tuiPHPItem{Kind: tuiPHPPoolItem, Version: "8.4", Name: "portal", PoolPath: "/etc/php/8.4/fpm/pool.d/portal.conf"}
	localPath, localLines, err := loadTUIPHPConfig(context.Background(), client, initialTUITargets()[0], item)
	if err != nil || localPath != item.PoolPath || !strings.HasPrefix(strings.Join(localLines, "|"), "[portal]|pm = dynamic") {
		t.Fatalf("local path=%q lines=%q err=%v", localPath, localLines, err)
	}
	if _, _, err := loadTUIPHPConfig(context.Background(), client, initialTUITargets()[0], item); err == nil || !strings.Contains(err.Error(), "different path") {
		t.Fatalf("local canonical path mismatch error = %v", err)
	}
	remoteTarget := tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityPHPRead: true}}
	remotePath, remoteLines, err := loadTUIPHPConfig(context.Background(), client, remoteTarget, item)
	if err != nil || remotePath != item.PoolPath || !strings.HasPrefix(strings.Join(remoteLines, "|"), "[portal]|pm = dynamic") {
		t.Fatalf("remote path=%q lines=%q err=%v", remotePath, remoteLines, err)
	}
}

func TestTUIPHPLifecycleRequiresExplicitConfirmation(t *testing.T) {
	t.Parallel()
	var actions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/php/versions":
			_, _ = io.WriteString(writer, `[{"version":"8.4","active":true}]`)
		case "POST /api/php/versions/8.4/actions/test":
			actions.Add(1)
			_, _ = io.WriteString(writer, `{"message":"PHP-FPM configuration is valid"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabPHP
	model.phpLoaded = true
	model.phpTarget = localTargetID
	model.php = tuiPHPState{Readable: true, Actionable: true, Items: []tuiPHPItem{{Kind: tuiPHPVersionItem, Version: "8.4", Runtime: true}}}

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 3 || actions.Load() != 0 {
		t.Fatalf("choices = %#v command=%v actions=%d", model.dialog, command != nil, actions.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || actions.Load() != 0 {
		t.Fatal("choosing test bypassed PHP-FPM confirmation")
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || actions.Load() != 0 {
		t.Fatal("Enter bypassed PHP-FPM confirmation")
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start PHP-FPM test")
	}
	message := command().(tuiOperationMsg)
	if message.Err != nil || message.Message != "PHP-FPM configuration is valid" || actions.Load() != 1 {
		t.Fatalf("message=%#v actions=%d", message, actions.Load())
	}
}

func TestTUIPHPLocalActionsUseCanonicalEndpoints(t *testing.T) {
	t.Parallel()
	requests := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/php/versions" {
			_, _ = io.WriteString(writer, `[{"version":"8.4","active":true}]`)
			return
		}
		if request.Method == http.MethodPost {
			requests <- request.URL.Path
			_, _ = io.WriteString(writer, `{"message":"action completed"}`)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	for _, action := range []string{"test", "reload", "restart"} {
		message, err := runTUIPHPOperation(context.Background(), client, tuiOperation{
			Kind: tuiOperationPHP, Target: initialTUITargets()[0], Action: action,
			PHP: tuiPHPItem{Kind: tuiPHPVersionItem, Version: "8.4", Runtime: true},
		})
		if err != nil || message != "action completed" {
			t.Fatalf("%s message=%q err=%v", action, message, err)
		}
		if path := <-requests; path != "/api/php/versions/8.4/actions/"+action {
			t.Fatalf("%s path=%q", action, path)
		}
	}
}

func TestTUIPHPManagedActionRefreshesObservedRuntime(t *testing.T) {
	t.Parallel()
	var inventoryRequests, actionRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/nodes/edge-1/php":
			inventoryRequests.Add(1)
			_, _ = io.WriteString(writer, `[{"version":"8.4","active":"active","binary":"/usr/sbin/php-fpm8.4","pools":[]}]`)
		case "POST /api/nodes/edge-1/php/8.4/actions/reload":
			actionRequests.Add(1)
			_, _ = io.WriteString(writer, `{"message":"PHP-FPM configuration tested and reloaded"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{
		agenthub.CapabilityPHPRead: true, agenthub.CapabilityPHPAction: true,
	}}
	message, err := runTUIPHPOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationPHP, Target: target, Action: "reload",
		PHP: tuiPHPItem{Kind: tuiPHPVersionItem, Version: "8.4", Runtime: true},
	})
	if err != nil || message != "PHP-FPM configuration tested and reloaded" || inventoryRequests.Load() != 1 || actionRequests.Load() != 1 {
		t.Fatalf("message=%q err=%v inventory=%d action=%d", message, err, inventoryRequests.Load(), actionRequests.Load())
	}
}

func TestTUIPHPDirectJumpAndView(t *testing.T) {
	t.Parallel()
	model := tuiModel{
		tab: tuiTabOverview, width: 150, height: 30, selectedTargetID: localTargetID,
		snapshot:  tuiSnapshot{Selected: initialTUITargets()[0]},
		phpLoaded: true, phpTarget: localTargetID,
		php: tuiPHPState{Readable: true, Actionable: true, Items: []tuiPHPItem{
			{Kind: tuiPHPVersionItem, Version: "8.4", Active: "active", Enabled: "installed", Runtime: true},
			{Kind: tuiPHPPoolItem, Version: "8.4", Name: "portal", PM: "dynamic", MaxChildren: 12, User: "portal"},
		}},
	}
	updated, command := model.updateKey("P")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabPHP {
		t.Fatalf("direct jump tab=%v command=%v", model.tab, command != nil)
	}
	view := model.View().Content
	for _, expected := range []string{"PHP-FPM", "8.4", "portal", "dynamic", "max 12"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view missing %q: %q", expected, view)
		}
	}
}
