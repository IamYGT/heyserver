package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestLoadTUIWebNormalizesLocalAndManagedInventories(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/nginx/configs":
			_, _ = writer.Write([]byte(`[{"filename":"site.conf","domain":"example.com","type":"proxy","isEnabled":true}]`))
		case "/api/domains":
			_, _ = writer.Write([]byte(`{"domains":[{"name":"example.com","type":"proxy","proxyPort":3080,"sslEnabled":true,"isActive":true}]}`))
		case "/api/ssl/certificates":
			_, _ = writer.Write([]byte(`[{"domain":"example.com","issuer":"Local CA","daysRemaining":10,"autoRenew":true}]`))
		case "/api/nodes/edge-1/nginx/configs":
			_, _ = writer.Write([]byte(`[{"name":"remote.conf","enabled":false,"size":2048,"modified_at":"2026-08-27T12:00:00Z"}]`))
		case "/api/nodes/edge-1/domains":
			_, _ = writer.Write([]byte(`[{"name":"remote.example.com","config":"remote.conf","enabled":false,"ssl":true,"proxy_target":"http://127.0.0.1:4000","kind":"proxy"}]`))
		case "/api/nodes/edge-1/certificates":
			_, _ = writer.Write([]byte(`[{"name":"remote.example.com","issuer":"Remote CA","days_remaining":60,"auto_renew":true}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	local, warnings, err := loadTUIWeb(context.Background(), client, initialTUITargets()[0])
	if err != nil || len(warnings) != 0 || len(local) != 3 {
		t.Fatalf("local inventory = %#v, warnings=%#v, err=%v", local, warnings, err)
	}
	if local[0].Kind != tuiWebNginx || local[0].ID != "site.conf" || local[1].Kind != tuiWebDomain || local[1].ID != "example.com" || local[2].Kind != tuiWebSSL || local[2].State != "expiring" {
		t.Fatalf("local normalized inventory = %#v", local)
	}

	remoteTarget := tuiTarget{
		ID: "edge-1", Name: "Edge", Online: true,
		Capabilities: map[string]bool{
			agenthub.CapabilityNginxConfigRead: true,
			agenthub.CapabilityDomainRead:      true,
			agenthub.CapabilitySSLRead:         true,
		},
	}
	remote, warnings, err := loadTUIWeb(context.Background(), client, remoteTarget)
	if err != nil || len(warnings) != 0 || len(remote) != 3 {
		t.Fatalf("remote inventory = %#v, warnings=%#v, err=%v", remote, warnings, err)
	}
	if remote[0].ID != "remote.conf" || remote[1].ID != "remote.conf" || remote[2].DaysRemaining != 60 || remote[2].State != "valid" {
		t.Fatalf("remote normalized inventory = %#v", remote)
	}

	remoteTarget.Capabilities = map[string]bool{agenthub.CapabilityDomainRead: true}
	partial, warnings, err := loadTUIWeb(context.Background(), client, remoteTarget)
	if err != nil || len(partial) != 1 || len(warnings) != 2 {
		t.Fatalf("partial inventory = %#v, warnings=%#v, err=%v", partial, warnings, err)
	}
	remoteTarget.Capabilities = map[string]bool{}
	if _, _, err := loadTUIWeb(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "no web resource read capabilities") {
		t.Fatalf("missing capability error = %v", err)
	}
}

func TestTUIWebMutationsRequireConfirmationAndUseBoundedEndpoints(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /api/domains/example.com/toggle":
			var body map[string]bool
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body["active"] {
				t.Errorf("domain body = %#v, err=%v", body, err)
			}
			_, _ = writer.Write([]byte(`{"message":"domain disabled"}`))
		case "POST /api/nodes/edge-1/nginx/actions/test":
			_, _ = writer.Write([]byte(`{"message":"configuration valid"}`))
		case "POST /api/nodes/edge-1/domains/remote.conf/actions/enable":
			_, _ = writer.Write([]byte(`{"message":"domain enabled"}`))
		case "POST /api/ssl/renew/example.com":
			_, _ = writer.Write([]byte(`{"message":"certificate renewed"}`))
		case "POST /api/nodes/edge-1/certificates/example.com/actions/check":
			_, _ = writer.Write([]byte(`{"message":"certificate valid"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
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
	model.tab = tuiTabWeb
	model.webLoaded = true
	model.webResources = []tuiWebResource{{Kind: tuiWebDomain, ID: "example.com", Name: "example.com", Enabled: true, State: "enabled"}}
	model.snapshot.Selected = model.snapshot.Targets[0]
	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || requests.Load() != 0 {
		t.Fatalf("action menu: dialog=%v command=%v requests=%d", model.dialog.Mode, command != nil, requests.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "disable" {
		t.Fatalf("confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || requests.Load() != 0 {
		t.Fatalf("enter confirmed mutation: command=%v requests=%d", command != nil, requests.Load())
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("explicit y did not start the web operation")
	}
	message := command()
	result, ok := message.(tuiOperationMsg)
	if !ok || result.Err != nil || result.Message != "domain disabled" {
		t.Fatalf("operation result = %#v", message)
	}

	remote := tuiTarget{ID: "edge-1", Name: "Edge", Online: true, Capabilities: map[string]bool{
		agenthub.CapabilityNginxAction: true, agenthub.CapabilityDomainAction: true, agenthub.CapabilitySSLAction: true,
	}}
	operations := []struct {
		operation tuiOperation
		want      string
	}{
		{operation: tuiOperation{Kind: tuiOperationWeb, Target: remote, Action: "test", WebResource: tuiWebResource{Kind: tuiWebNginx, ID: "remote.conf", Name: "remote.conf"}}, want: "configuration valid"},
		{operation: tuiOperation{Kind: tuiOperationWeb, Target: remote, Action: "enable", WebResource: tuiWebResource{Kind: tuiWebDomain, ID: "remote.conf", Name: "remote.example.com"}}, want: "domain enabled"},
		{operation: tuiOperation{Kind: tuiOperationWeb, Target: initialTUITargets()[0], Action: "renew", WebResource: tuiWebResource{Kind: tuiWebSSL, ID: "example.com", Name: "example.com"}}, want: "certificate renewed"},
		{operation: tuiOperation{Kind: tuiOperationWeb, Target: remote, Action: "check", WebResource: tuiWebResource{Kind: tuiWebSSL, ID: "example.com", Name: "example.com"}}, want: "certificate valid"},
	}
	for _, item := range operations {
		message, err := runTUIOperation(context.Background(), client, item.operation)
		if err != nil || message != item.want {
			t.Fatalf("operation = %#v, message=%q, err=%v", item.operation, message, err)
		}
	}
	if requests.Load() != 5 {
		t.Fatalf("requests = %d", requests.Load())
	}

	remote.Capabilities = map[string]bool{}
	if _, err := runTUIOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationWeb, Target: remote, Action: "reload", WebResource: tuiWebResource{Kind: tuiWebNginx, ID: "remote.conf", Name: "remote.conf"},
	}); err == nil || !strings.Contains(err.Error(), "nginx.action") {
		t.Fatalf("missing mutation capability error = %v", err)
	}
	if requests.Load() != 5 {
		t.Fatalf("rejected mutation sent a request; requests=%d", requests.Load())
	}
}
