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

func TestLoadTUIFirewallNormalizesLocalAndManagedInventory(t *testing.T) {
	t.Parallel()
	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/firewall/status":
			_, _ = writer.Write([]byte(`{"available":true,"state":"healthy","backend":"ufw","active":true,"defaultIncoming":"deny","defaultOutgoing":"allow","rules":[{"number":2,"to":"443","action":"ALLOW","direction":"IN","from":"Anywhere","protocol":"tcp","comment":"HTTPS"}]}`))
		case "/api/nodes/edge-1/firewall":
			_, _ = writer.Write([]byte(`{"backend":"iptables","policy":"DROP","persistence":"active","rules":[{"id":"fw-0123456789ab","action":"ACCEPT","protocol":"tcp","port":22,"source":"0.0.0.0/0","comment":"SSH","managed":true},{"action":"ACCEPT","protocol":"all","raw":"-A INPUT -j ACCEPT","managed":false}],"revision":"` + revision + `","protected_sources":["192.0.2.10/32"],"protected_ports":[22]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	local, err := loadTUIFirewall(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !local.Manageable || !local.Active || local.Backend != "ufw" || len(local.Rules) != 1 || local.Rules[0].Number != 2 || !local.Rules[0].Managed {
		t.Fatalf("local = %#v", local)
	}
	remoteTarget := tuiTarget{
		ID: "edge-1", Name: "Edge", Online: true,
		Capabilities: map[string]bool{agenthub.CapabilityFirewallRead: true, agenthub.CapabilityFirewallWrite: true},
	}
	remote, err := loadTUIFirewall(context.Background(), client, remoteTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !remote.Manageable || remote.Backend != "iptables" || remote.Policy != "DROP" || remote.Revision != revision || len(remote.Rules) != 2 || !remote.Rules[0].Managed || remote.Rules[0].ID != "fw-0123456789ab" || len(remote.ProtectedPorts) != 1 {
		t.Fatalf("remote = %#v", remote)
	}

	remoteTarget.Capabilities = map[string]bool{}
	if _, err := loadTUIFirewall(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "firewall.read") {
		t.Fatalf("missing capability error = %v", err)
	}
	remoteTarget.Online = false
	if _, err := loadTUIFirewall(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline error = %v", err)
	}
}

func TestTUIFirewallGuidedLocalActionsRequireExplicitConfirmation(t *testing.T) {
	t.Parallel()
	var addRequests atomic.Int32
	var deleteRequests atomic.Int32
	var toggleRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /api/firewall/rules":
			addRequests.Add(1)
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode add: %v", err)
			}
			if payload["action"] != "allow" || payload["protocol"] != "tcp" || payload["port"] != "443" || payload["from"] != "any" {
				t.Errorf("add payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"message":"firewall rule added"}`))
		case "DELETE /api/firewall/rules/2":
			deleteRequests.Add(1)
			_, _ = writer.Write([]byte(`{"message":"firewall rule deleted"}`))
		case "POST /api/firewall/toggle":
			toggleRequests.Add(1)
			var payload map[string]bool
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["enable"] {
				t.Errorf("toggle payload = %#v, err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"UFW firewall disabled"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	base := func() tuiModel {
		model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
		model.loading = false
		model.tab = tuiTabFirewall
		model.snapshot.Selected = model.snapshot.Targets[0]
		model.firewallLoaded = true
		model.firewallTarget = localTargetID
		model.firewall = tuiFirewallState{
			Backend: "ufw", State: "healthy", Active: true, Manageable: true,
			Rules: []tuiFirewallRule{{ID: "2", Number: 2, Action: "ALLOW", Direction: "IN", Protocol: "tcp", Target: "443", Source: "Anywhere", Managed: true}},
		}
		return model
	}

	model := base()
	updated, command := model.updateKey("a")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 4 {
		t.Fatalf("add profiles = %#v", model.dialog)
	}
	for range 2 {
		updated, _ = model.updateDialogKey("down")
		model = updated.(tuiModel)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.FirewallSpec.Port != 443 || addRequests.Load() != 0 {
		t.Fatalf("add confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	if command != nil || addRequests.Load() != 0 {
		t.Fatal("Enter bypassed firewall add confirmation")
	}
	model = updated.(tuiModel)
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start firewall add")
	}
	message := command().(tuiOperationMsg)
	if message.Err != nil || message.Message != "firewall rule added" || addRequests.Load() != 1 {
		t.Fatalf("add message = %#v", message)
	}

	model = base()
	updated, command = model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices {
		t.Fatalf("delete menu = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || !model.dialog.Operation.Dangerous {
		t.Fatalf("delete confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start firewall delete")
	}
	message = command().(tuiOperationMsg)
	if message.Err != nil || deleteRequests.Load() != 1 {
		t.Fatalf("delete message = %#v", message)
	}

	model = base()
	updated, command = model.updateKey("t")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "disable" || !model.dialog.Operation.Dangerous {
		t.Fatalf("toggle confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start firewall toggle")
	}
	message = command().(tuiOperationMsg)
	if message.Err != nil || toggleRequests.Load() != 1 {
		t.Fatalf("toggle message = %#v", message)
	}
}

func TestTUIFirewallRemoteDeleteRefreshesRevisionAndOwnership(t *testing.T) {
	t.Parallel()
	const revision = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	var getRequests atomic.Int32
	var deleteRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/nodes/edge-1/firewall":
			getRequests.Add(1)
			_, _ = writer.Write([]byte(`{"backend":"iptables","rules":[{"id":"fw-0123456789ab","action":"ACCEPT","protocol":"tcp","port":22,"managed":true}],"revision":"` + revision + `"}`))
		case "DELETE /api/nodes/edge-1/firewall/fw-0123456789ab":
			deleteRequests.Add(1)
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["revision"] != revision {
				t.Errorf("delete payload = %#v, err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"Firewall rule deleted and persisted"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := tuiTarget{
		ID: "edge-1", Name: "Edge", Online: true,
		Capabilities: map[string]bool{agenthub.CapabilityFirewallRead: true, agenthub.CapabilityFirewallWrite: true},
	}
	message, err := runTUIFirewallOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationFirewall, Target: target, Action: "delete",
		FirewallRule: tuiFirewallRule{ID: "fw-0123456789ab", Managed: true},
	})
	if err != nil || message != "Firewall rule deleted and persisted" || getRequests.Load() != 1 || deleteRequests.Load() != 1 {
		t.Fatalf("message=%q err=%v get=%d delete=%d", message, err, getRequests.Load(), deleteRequests.Load())
	}

	before := getRequests.Load()
	_, err = runTUIFirewallOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationFirewall, Target: target, Action: "delete",
		FirewallRule: tuiFirewallRule{ID: "system-rule", Managed: false},
	})
	if err == nil || !strings.Contains(err.Error(), "installation-owned") || getRequests.Load() != before {
		t.Fatalf("ownership error=%v get=%d", err, getRequests.Load())
	}
}
