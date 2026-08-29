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

	tea "charm.land/bubbletea/v2"

	bindsvc "github.com/IamYGT/heyserver/internal/services/bind"
)

func TestLoadTUIDNSKeepsLocalAndManagedBoundariesExplicit(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/dns/status":
			_, _ = writer.Write([]byte(`{"available":true,"installed":true,"state":"healthy","active":true,"serviceState":"active","version":"BIND 9.18","configAvailable":true,"checkToolsAvailable":true,"reloadAvailable":true,"zoneManagementReady":true}`))
		case "GET /api/dns/zones":
			_, _ = writer.Write([]byte(`[{"domain":"example.com","file":"/etc/bind/zones/db.example.com","serial":2026082801,"recordCount":3}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	local, err := loadTUIDNS(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !local.Supported || local.Status.State != bindsvc.StateHealthy || !local.Status.ZoneManagementReady || len(local.Zones) != 1 || local.Zones[0].Domain != "example.com" {
		t.Fatalf("local = %#v", local)
	}
	before := requests.Load()
	managed, err := loadTUIDNS(context.Background(), client, tuiTarget{ID: "edge-1", Name: "Edge", Online: true})
	if err != nil || managed.Supported || !strings.Contains(managed.Message, "panel host") || requests.Load() != before {
		t.Fatalf("managed=%#v err=%v requests=%d", managed, err, requests.Load())
	}
}

func TestTUIDNSInspectCheckAndObservedMutationsRequireConfirmation(t *testing.T) {
	t.Parallel()
	var checkRequests atomic.Int32
	var reloadRequests atomic.Int32
	var recordDeletes atomic.Int32
	var zoneDeletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/dns/zones/example.com":
			_, _ = writer.Write([]byte(`{"domain":"example.com","file":"/etc/bind/zones/db.example.com","serial":2026082801,"recordCount":2,"records":[{"name":"@","ttl":"3600","class":"IN","type":"SOA","value":"ns1.example.com. hostmaster.example.com."},{"name":"www","ttl":"300","class":"IN","type":"A","value":"192.0.2.20"}]}`))
		case "POST /api/dns/check":
			checkRequests.Add(1)
			assertDNSNoBody(t, request)
			_, _ = writer.Write([]byte(`{"ok":false,"output":"configuration valid","zoneChecks":[{"domain":"example.com","ok":false,"output":"zone serial mismatch"}]}`))
		case "POST /api/dns/reload":
			reloadRequests.Add(1)
			assertDNSNoBody(t, request)
			_, _ = writer.Write([]byte(`{"message":"BIND9 reloaded successfully"}`))
		case "DELETE /api/dns/zones/example.com/records":
			recordDeletes.Add(1)
			var payload bindsvc.DeleteRecordRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode record delete: %v", err)
			}
			if payload.Name != "www" || payload.Type != "A" || payload.Value != "192.0.2.20" || !payload.AutoReload {
				t.Errorf("record delete payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"message":"record deleted"}`))
		case "DELETE /api/dns/zones/example.com":
			zoneDeletes.Add(1)
			assertDNSNoBody(t, request)
			_, _ = writer.Write([]byte(`{"message":"zone example.com deleted"}`))
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
		model.tab = tuiTabDNS
		model.snapshot.Selected = model.snapshot.Targets[0]
		model.dnsLoaded = true
		model.dnsTarget = localTargetID
		model.dns = tuiDNSState{
			Supported: true,
			Status: bindsvc.ServiceStatus{
				State: bindsvc.StateHealthy, ConfigAvailable: true,
				ReloadAvailable: true, ZoneManagementReady: true,
			},
			Zones: []tuiDNSZone{{Domain: "example.com", File: "/etc/bind/zones/db.example.com", Serial: 2026082801, RecordCount: 2}},
		}
		return model
	}

	model := base()
	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command == nil || !model.resourceLoading {
		t.Fatal("Enter did not start observed zone detail loading")
	}
	detailMessage := command().(tuiDNSDetailMsg)
	updated, _ = model.Update(detailMessage)
	model = updated.(tuiModel)
	if model.dns.Detail == nil || len(model.dns.Detail.Records) != 2 || model.resourceLoading {
		t.Fatalf("detail model = %#v", model.dns)
	}

	model.cursor = 0
	updated, command = model.updateKey("x")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogNone || !model.noticeError || !strings.Contains(model.notice, "SOA") {
		t.Fatalf("SOA delete guard: dialog=%#v notice=%q", model.dialog, model.notice)
	}
	model.cursor = 1
	updated, command = model.updateKey("x")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || !model.dialog.Operation.Dangerous || recordDeletes.Load() != 0 {
		t.Fatalf("record confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	if command != nil || recordDeletes.Load() != 0 {
		t.Fatal("Enter bypassed DNS record deletion confirmation")
	}
	model = updated.(tuiModel)
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start DNS record deletion")
	}
	operationMessage := command().(tuiOperationMsg)
	if operationMessage.Err != nil || operationMessage.Message != "record deleted" || recordDeletes.Load() != 1 {
		t.Fatalf("record delete message = %#v", operationMessage)
	}

	model = base()
	updated, command = model.updateKey("t")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || reloadRequests.Load() != 0 {
		t.Fatalf("reload confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start BIND reload")
	}
	operationMessage = command().(tuiOperationMsg)
	if operationMessage.Err != nil || reloadRequests.Load() != 1 {
		t.Fatalf("reload message = %#v", operationMessage)
	}

	model = base()
	updated, command = model.updateKey("c")
	model = updated.(tuiModel)
	if command == nil || !model.resourceLoading {
		t.Fatal("C did not start BIND check")
	}
	checkMessage := command().(tuiDNSCheckMsg)
	updated, _ = model.Update(checkMessage)
	model = updated.(tuiModel)
	if checkRequests.Load() != 1 || model.dialog.Mode != tuiDialogLogs || !model.noticeError || model.dns.Check == nil || model.dns.Check.OK {
		t.Fatalf("check state: requests=%d dialog=%#v noticeError=%t check=%#v", checkRequests.Load(), model.dialog, model.noticeError, model.dns.Check)
	}
	if got := strings.Join(model.dialog.LogLines, "\n"); !strings.Contains(got, "Zone example.com: FAILED") || !strings.Contains(got, "zone serial mismatch") {
		t.Fatalf("check lines = %q", got)
	}

	model = base()
	updated, command = model.updateKey("x")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || zoneDeletes.Load() != 0 {
		t.Fatalf("zone confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start DNS zone deletion")
	}
	operationMessage = command().(tuiOperationMsg)
	if operationMessage.Err != nil || zoneDeletes.Load() != 1 {
		t.Fatalf("zone delete message = %#v", operationMessage)
	}
}

func TestTUIDNSShortcutAndPaletteExposeLocalControlPlane(t *testing.T) {
	t.Parallel()
	model := newTUIModel(context.Background(), nil, "https://panel.example.com", 5*time.Second)
	model.loading = false
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.dnsLoaded = true
	model.dnsTarget = localTargetID
	model.dns = tuiDNSState{Supported: true, Status: bindsvc.ServiceStatus{ReloadAvailable: true}}

	updated, command := model.updateKey("Z")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabDNS {
		t.Fatalf("DNS shortcut: tab=%v command=%v", model.tab, command != nil)
	}
	items := model.buildPaletteItems()
	foundSection, foundReload := false, false
	for _, item := range items {
		foundSection = foundSection || item.Kind == tuiPaletteNavigate && item.Tab == tuiTabDNS
		foundReload = foundReload || item.Kind == tuiPaletteOperation && item.Operation.Kind == tuiOperationDNS && item.Operation.Action == "reload"
	}
	if !foundSection || !foundReload {
		t.Fatalf("palette missing DNS section=%t reload=%t", foundSection, foundReload)
	}
}

func TestTUIDNSGuidedFormsCoverCreateUpdateAndSOAWithSeparateConfirmation(t *testing.T) {
	t.Parallel()
	originalSOA := bindsvc.SOARecord{
		PrimaryNs: "ns1.example.com.", Hostmaster: "hostmaster.example.com.", Serial: 2026082801,
		Refresh: 3600, Retry: 900, Expire: 604800, Minimum: 300,
	}
	var zoneCreates atomic.Int32
	var recordAdds atomic.Int32
	var recordUpdates atomic.Int32
	var soaUpdates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /api/dns/zones":
			zoneCreates.Add(1)
			var payload bindsvc.CreateZoneRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Domain != "new.example" || payload.IP != "192.0.2.30" {
				t.Errorf("zone create payload=%#v err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"domain":"new.example","file":"/etc/bind/zones/db.new.example","serial":2026082801,"recordCount":4}`))
		case "POST /api/dns/zones/example.com/records":
			recordAdds.Add(1)
			var payload bindsvc.AddRecordRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Name != "api" || payload.Type != "AAAA" || payload.Value != "2001:db8::20" || payload.TTL != "600" || !payload.AutoReload {
				t.Errorf("record add payload=%#v err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"record added"}`))
		case "GET /api/dns/zones/example.com":
			_, _ = writer.Write([]byte(`{"domain":"example.com","file":"/etc/bind/zones/db.example.com","serial":2026082801,"recordCount":2,"records":[{"name":"@","ttl":"3600","class":"IN","type":"SOA","value":"ns1.example.com. hostmaster.example.com."},{"name":"www","ttl":"300","class":"IN","type":"A","value":"192.0.2.20"}]}`))
		case "PUT /api/dns/zones/example.com/records":
			recordUpdates.Add(1)
			var payload bindsvc.UpdateRecordRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Name != "www" || payload.Type != "A" || payload.OldValue != "192.0.2.20" || payload.NewValue != "192.0.2.21" || payload.NewTTL != "900" || !payload.AutoReload {
				t.Errorf("record update payload=%#v err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"record updated"}`))
		case "GET /api/dns/zones/example.com/soa":
			_ = json.NewEncoder(writer).Encode(originalSOA)
		case "PUT /api/dns/zones/example.com/soa":
			soaUpdates.Add(1)
			var payload bindsvc.UpdateSOARequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.PrimaryNs != originalSOA.PrimaryNs || payload.Hostmaster != originalSOA.Hostmaster || payload.Refresh != 7200 || payload.Retry != originalSOA.Retry {
				t.Errorf("SOA update payload=%#v err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"SOA updated"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	base := func(detail bool) tuiModel {
		model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
		model.loading = false
		model.tab = tuiTabDNS
		model.snapshot.Selected = model.snapshot.Targets[0]
		model.dnsLoaded = true
		model.dnsTarget = localTargetID
		model.dns = tuiDNSState{
			Supported: true,
			Status: bindsvc.ServiceStatus{
				State: bindsvc.StateHealthy, ConfigAvailable: true,
				ReloadAvailable: true, ZoneManagementReady: true,
			},
			Zones: []tuiDNSZone{{Domain: "example.com", File: "/etc/bind/zones/db.example.com", Serial: 2026082801, RecordCount: 2}},
		}
		if detail {
			model.dns.Detail = &bindsvc.ZoneDetail{
				Zone: bindsvc.Zone{Domain: "example.com", File: "/etc/bind/zones/db.example.com", Serial: 2026082801, RecordCount: 2},
				Records: []bindsvc.Record{
					{Name: "@", TTL: "3600", Class: "IN", Type: "SOA", Value: "ns1.example.com. hostmaster.example.com."},
					{Name: "www", TTL: "300", Class: "IN", Type: "A", Value: "192.0.2.20"},
				},
			}
		}
		return model
	}
	confirmAndRun := func(t *testing.T, model tuiModel, command tea.Cmd) tuiOperationMsg {
		t.Helper()
		if command != nil || model.dialog.Mode != tuiDialogConfirm {
			t.Fatalf("expected confirmation, dialog=%#v command=%v", model.dialog, command != nil)
		}
		updated, operationCommand := model.updateDialogKey("y")
		model = updated.(tuiModel)
		if operationCommand == nil || !model.operating {
			t.Fatal("Y did not start DNS form operation")
		}
		return operationCommand().(tuiOperationMsg)
	}

	model := base(false)
	updated, command := model.updateKey("a")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogDNSForm || model.dialog.DNSForm.Kind != tuiDNSFormZoneCreate {
		t.Fatalf("zone form = %#v", model.dialog)
	}
	model.dialog.DNSForm.Fields[0].Value = "new.example"
	model.dialog.DNSForm.Fields[1].Value = "192.0.2.30"
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if zoneCreates.Load() != 0 {
		t.Fatal("zone form mutated before confirmation")
	}
	message := confirmAndRun(t, model, command)
	if message.Err != nil || message.Message != "DNS zone new.example created" || zoneCreates.Load() != 1 {
		t.Fatalf("zone create message = %#v", message)
	}

	model = base(true)
	updated, command = model.updateKey("a")
	model = updated.(tuiModel)
	if command != nil || model.dialog.DNSForm.Kind != tuiDNSFormRecordAdd {
		t.Fatalf("record add form = %#v", model.dialog)
	}
	values := map[string]string{"name": "api", "type": "AAAA", "value": "2001:db8::20", "ttl": "600", "priority": "0"}
	for index := range model.dialog.DNSForm.Fields {
		model.dialog.DNSForm.Fields[index].Value = values[model.dialog.DNSForm.Fields[index].Key]
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	message = confirmAndRun(t, model, command)
	if message.Err != nil || recordAdds.Load() != 1 {
		t.Fatalf("record add message = %#v", message)
	}

	model = base(true)
	model.cursor = 1
	updated, command = model.updateKey("e")
	model = updated.(tuiModel)
	if command != nil || model.dialog.DNSForm.Kind != tuiDNSFormRecordUpdate {
		t.Fatalf("record update form = %#v", model.dialog)
	}
	for index := range model.dialog.DNSForm.Fields {
		switch model.dialog.DNSForm.Fields[index].Key {
		case "value":
			model.dialog.DNSForm.Fields[index].Value = "192.0.2.21"
		case "ttl":
			model.dialog.DNSForm.Fields[index].Value = "900"
		}
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	message = confirmAndRun(t, model, command)
	if message.Err != nil || recordUpdates.Load() != 1 {
		t.Fatalf("record update message = %#v", message)
	}

	model = base(true)
	model.cursor = 0
	updated, command = model.updateKey("e")
	model = updated.(tuiModel)
	if command == nil || !model.resourceLoading {
		t.Fatal("SOA edit did not load current structured fields")
	}
	soaMessage := command().(tuiDNSSOAMsg)
	updated, _ = model.Update(soaMessage)
	model = updated.(tuiModel)
	if model.dialog.Mode != tuiDialogDNSForm || model.dialog.DNSForm.Kind != tuiDNSFormSOAUpdate || model.dialog.DNSForm.OriginalSOA != originalSOA {
		t.Fatalf("SOA form = %#v", model.dialog)
	}
	for index := range model.dialog.DNSForm.Fields {
		if model.dialog.DNSForm.Fields[index].Key == "refresh" {
			model.dialog.DNSForm.Fields[index].Value = "7200"
		}
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	message = confirmAndRun(t, model, command)
	if message.Err != nil || soaUpdates.Load() != 1 {
		t.Fatalf("SOA update message = %#v", message)
	}
}

func TestTUIDNSFormValidationStaysInFormAndMutationRejectsStaleRecord(t *testing.T) {
	t.Parallel()
	model := tuiModel{
		snapshot: tuiSnapshot{Selected: initialTUITargets()[0]},
		dialog: tuiDialog{
			Mode: tuiDialogDNSForm,
			DNSForm: tuiDNSForm{Kind: tuiDNSFormZoneCreate, Fields: []tuiDNSFormField{
				{Key: "domain", Label: "Zone", Value: "../bad", Maximum: 253},
				{Key: "ip", Label: "IPv4", Value: "not-an-ip", Maximum: 45},
			}},
		},
	}
	updated, command := model.updateDNSFormKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogDNSForm || model.dialog.DNSForm.Error == "" {
		t.Fatalf("invalid form escaped validation: dialog=%#v command=%v", model.dialog, command != nil)
	}

	var updates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/dns/zones/example.com":
			_, _ = writer.Write([]byte(`{"domain":"example.com","records":[{"name":"www","ttl":"300","type":"A","value":"192.0.2.99"}]}`))
		case "PUT /api/dns/zones/example.com/records":
			updates.Add(1)
			_, _ = writer.Write([]byte(`{"message":"record updated"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runTUIDNSOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationDNS, Target: initialTUITargets()[0], Action: "record-update",
		DNSZone:   tuiDNSZone{Domain: "example.com"},
		DNSRecord: tuiDNSRecord{Name: "www", TTL: "300", Type: "A", Value: "192.0.2.20"},
		DNSUpdate: tuiDNSUpdateRequest{
			Name: "www", Type: "A", OldValue: "192.0.2.20", NewValue: "192.0.2.21", NewTTL: "300", AutoReload: true,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "changed since") || updates.Load() != 0 {
		t.Fatalf("stale update err=%v requests=%d", err, updates.Load())
	}
}
