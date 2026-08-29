package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newDiskDiagnosticsTestClient(t *testing.T, serverURL string) *apiClient {
	t.Helper()
	client, err := newAPIClient(serverURL, "disk-test-token", 5*time.Second)
	if err != nil {
		t.Fatalf("newAPIClient: %v", err)
	}
	return client
}

func TestRunDiskDispatchesLocalDiagnostics(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != diskIOEndpoint {
			t.Fatalf("request = %s %s, want GET %s", r.Method, r.URL.Path, diskIOEndpoint)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := runDisk(context.Background(), newDiskDiagnosticsTestClient(t, server.URL), []string{"io"}, &out); err != nil {
		t.Fatalf("runDisk io: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Fatalf("output = %q, want []", got)
	}
}

func TestRunDiskDiagnosticsReadOnlyRoutesAndEscapesInputs(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer disk-test-token" {
			t.Errorf("authorization = %q", got)
		}
		if body, err := io.ReadAll(r.Body); err != nil || len(body) != 0 {
			t.Errorf("read-only request body = %q (err=%v)", body, err)
		}

		query := r.URL.Query()
		switch r.URL.Path {
		case diskDirSizeEndpoint:
			if got := query.Get("path"); got != "/var/lib/app data" {
				t.Errorf("dirsize path = %q", got)
			}
			if _, ok := query["depth"]; ok {
				t.Error("dirsize unexpectedly sent depth")
			}
			_, _ = w.Write([]byte(`{"path":"/var/lib/app data","size":123}`))
		case diskIOEndpoint:
			if len(query) != 0 {
				t.Errorf("io query = %v", query)
			}
			_, _ = w.Write([]byte(`[{"device":"nvme0n1","readsCompleted":11,"writesCompleted":22,"sectorsRead":33,"sectorsWritten":44,"readBytes":16896,"writeBytes":22528,"ioInProgress":0,"ioTimeMs":55}]`))
		case diskLargestEndpoint:
			if got := query.Get("path"); got != "/var/lib/app data" {
				t.Errorf("largest path = %q", got)
			}
			if got := query.Get("limit"); got != "7" {
				t.Errorf("largest limit = %q", got)
			}
			_, _ = w.Write([]byte(`[{"path":"/var/lib/app data/file.bin","size":700,"modified":"2026-08-29T00:00:00Z"}]`))
		case diskListEndpoint:
			if got := query.Get("path"); got != "/var/lib/app data" {
				t.Errorf("list path = %q", got)
			}
			_, _ = w.Write([]byte(`{"path":"/var/lib/app data","entries":[],"count":0}`))
		case diskMountsEndpoint:
			if len(query) != 0 {
				t.Errorf("mounts query = %v", query)
			}
			_, _ = w.Write([]byte(`[{"device":"/dev/nvme0n1p2","mountPoint":"/","fsType":"ext4","options":"rw","source":"active"}]`))
		case diskUsageEndpoint:
			if got := query.Get("path"); got != "/var/lib/app data" {
				t.Errorf("usage path = %q", got)
			}
			if got := query.Get("depth"); got != "2" {
				t.Errorf("usage depth = %q", got)
			}
			_, _ = w.Write([]byte(`[{"path":"/var/lib/app data/cache","size":500,"items":3}]`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newDiskDiagnosticsTestClient(t, server.URL)
	ctx := context.Background()

	commands := [][]string{
		{"dirsize", "--format", "json", "/var/lib/app data"},
		{"io"},
		{"largest", "--limit", "7", "/var/lib/app data"},
		{"list", "/var/lib/app data"},
		{"mounts", "--format", "table"},
		{"usage", "--depth", "2", "/var/lib/app data"},
	}
	for _, command := range commands {
		var out bytes.Buffer
		if err := runDiskDiagnostics(ctx, client, command, &out); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(out.Bytes()) && command[0] != "mounts" {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), out.String())
		}
	}

	if requests.Load() != int32(len(commands)) {
		t.Fatalf("requests = %d, want %d", requests.Load(), len(commands))
	}
}

func TestRunDiskSmartEscapesExplicitDeviceAndDoesNotChooseOne(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.URL.EscapedPath(); got != diskSmartEndpoint+"%2Fdev%2Fnvme0n1" {
			t.Errorf("escaped path = %q", got)
		}
		if got := r.URL.Path; got != diskSmartEndpoint+"/dev/nvme0n1" {
			t.Errorf("decoded path = %q", got)
		}
		_, _ = w.Write([]byte(`{"available":false,"healthy":false,"device":"/dev/nvme0n1","status":"UNAVAILABLE","message":"not configured"}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := runDiskDiagnostics(context.Background(), newDiskDiagnosticsTestClient(t, server.URL), []string{"smart", "/dev/nvme0n1"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"device": "/dev/nvme0n1"`) {
		t.Fatalf("smart output = %q", out.String())
	}
}

func TestRunDiskAnalysisStartRequiresExplicitConfirmation(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := runDiskDiagnostics(context.Background(), newDiskDiagnosticsTestClient(t, server.URL), []string{"analysis", "start"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "requires explicit --confirm") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("unconfirmed start sent %d request(s)", requests.Load())
	}
}

func TestRunDiskAnalysisStartAndStatusUseLocalAPIAndSupportTableOutput(t *testing.T) {
	t.Parallel()
	var startRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case diskAnalysisStartEndpoint:
			startRequests.Add(1)
			if r.Method != http.MethodPost {
				t.Errorf("analysis start method = %s, want POST", r.Method)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read analysis start body: %v", err)
			}
			if len(body) != 0 {
				t.Errorf("analysis start body = %q, want empty", body)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"disk-abc123","unit":"hserver-disk-analysis-abc123.service","status":"queued","message":"Deep disk analysis queued","entries":[]}`))
		case diskAnalysisStatusEndpoint:
			if r.Method != http.MethodGet {
				t.Errorf("analysis status method = %s, want GET", r.Method)
			}
			_, _ = w.Write([]byte(`{"id":"disk-abc123","unit":"hserver-disk-analysis-abc123.service","status":"completed","message":"done","root_size":1000,"root_used":400,"root_available":600,"entries":[{"path":"/var","size":400}],"errors":[]}`))
		default:
			t.Errorf("unexpected analysis request: %s %s", r.Method, r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newDiskDiagnosticsTestClient(t, server.URL)

	var started bytes.Buffer
	if err := runDiskDiagnostics(context.Background(), client, []string{"analysis", "start", "--confirm", "--format", "table"}, &started); err != nil {
		t.Fatalf("analysis start: %v", err)
	}
	if !strings.Contains(started.String(), "status\tqueued") {
		t.Fatalf("analysis start table = %q", started.String())
	}

	var status bytes.Buffer
	if err := runDiskDiagnostics(context.Background(), client, []string{"analysis", "status", "--format", "table"}, &status); err != nil {
		t.Fatalf("analysis status: %v", err)
	}
	for _, fragment := range []string{"status\tcompleted", "ENTRIES", "/var\t400"} {
		if !strings.Contains(status.String(), fragment) {
			t.Errorf("analysis status table missing %q:\n%s", fragment, status.String())
		}
	}
	if startRequests.Load() != 1 {
		t.Fatalf("analysis start requests = %d, want 1", startRequests.Load())
	}
}

func TestRunDiskDiagnosticsDoesNotInventPathDefaultsAndIsLocalOnly(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != diskListEndpoint {
			t.Errorf("path = %q", r.URL.Path)
		}
		if _, ok := r.URL.Query()["path"]; ok {
			t.Error("pathless list request contained a fabricated path")
		}
		_, _ = w.Write([]byte(`{"path":"/","entries":[],"count":0}`))
	}))
	defer server.Close()
	client := newDiskDiagnosticsTestClient(t, server.URL)

	var out bytes.Buffer
	if err := runDiskDiagnostics(context.Background(), client, []string{"list"}, &out); err != nil {
		t.Fatalf("pathless list: %v", err)
	}
	if !strings.Contains(out.String(), `"path": "/"`) {
		t.Fatalf("list output = %q", out.String())
	}

	for _, command := range [][]string{
		{"dirsize"},
		{"usage"},
		{"largest"},
		{"smart"},
		{"io", "--node", "edge-1"},
		{"mounts", "--node", "edge-1"},
	} {
		err := runDiskDiagnostics(context.Background(), client, command, &bytes.Buffer{})
		if err == nil {
			t.Errorf("%s unexpectedly succeeded", strings.Join(command, " "))
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want only pathless list request", requests.Load())
	}
}

func TestRunDiskDiagnosticsTableOutputUsesResponseFields(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case diskIOEndpoint:
			_, _ = w.Write([]byte(`[{"device":"sda","readsCompleted":1,"writesCompleted":2,"sectorsRead":3,"sectorsWritten":4,"readBytes":1536,"writeBytes":2048,"ioInProgress":5,"ioTimeMs":6}]`))
		case diskDirSizeEndpoint:
			_, _ = w.Write([]byte(`{"path":"/var","size":42}`))
		case diskLargestEndpoint:
			_, _ = w.Write([]byte(`[{"path":"/var/log/app.log","size":99,"modified":"2026-08-29T00:00:00Z"}]`))
		case diskListEndpoint:
			_, _ = w.Write([]byte(`{"path":"/var","entries":[{"name":"app.log","path":"/var/app.log","isDir":false,"size":12,"modified":"2026-08-29T00:00:00Z","mode":"-rw-------"}],"count":1}`))
		case diskMountsEndpoint:
			_, _ = w.Write([]byte(`[{"device":"/dev/sda1","mountPoint":"/","fsType":"ext4","options":"rw","dump":0,"pass":1,"source":"active"}]`))
		case diskSmartEndpoint + "sda":
			_, _ = w.Write([]byte(`{"available":true,"healthy":true,"device":"/dev/sda","model":"Example Disk","status":"PASSED","attrs":[{"id":5,"name":"Reallocated_Sector_Ct","value":100,"worst":100,"raw":"0"}]}`))
		case diskUsageEndpoint:
			_, _ = w.Write([]byte(`[{"path":"/var/log","size":77,"items":4}]`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newDiskDiagnosticsTestClient(t, server.URL)

	cases := []struct {
		args      []string
		fragments []string
	}{
		{args: []string{"io", "--format", "table"}, fragments: []string{"DEVICE\tREADS_COMPLETED", "sda\t1\t2"}},
		{args: []string{"dirsize", "--format", "table", "/var"}, fragments: []string{"PATH\tSIZE", "/var\t42"}},
		{args: []string{"largest", "--format", "table", "/var"}, fragments: []string{"PATH\tSIZE\tMODIFIED", "/var/log/app.log\t99"}},
		{args: []string{"list", "--format", "table", "/var"}, fragments: []string{"Path: /var", "TYPE\tNAME\tPATH", "file\tapp.log"}},
		{args: []string{"mounts", "--format", "table"}, fragments: []string{"DEVICE\tMOUNT_POINT", "/dev/sda1\t/\text4"}},
		{args: []string{"smart", "--format", "table", "sda"}, fragments: []string{"FIELD\tVALUE", "status\tPASSED", "ATTRS", "Reallocated_Sector_Ct"}},
		{args: []string{"usage", "--format", "table", "/var"}, fragments: []string{"PATH\tSIZE\tITEMS", "/var/log\t77\t4"}},
	}
	for _, test := range cases {
		var out bytes.Buffer
		if err := runDiskDiagnostics(context.Background(), client, test.args, &out); err != nil {
			t.Fatalf("%s: %v", strings.Join(test.args, " "), err)
		}
		for _, fragment := range test.fragments {
			if !strings.Contains(out.String(), fragment) {
				t.Errorf("%s output missing %q:\n%s", strings.Join(test.args, " "), fragment, out.String())
			}
		}
	}
}

func TestDiskEndpointWithQueryUsesURLEncoding(t *testing.T) {
	endpoint := diskEndpointWithQuery(diskUsageEndpoint, url.Values{"path": []string{"/var/lib/app data"}, "depth": []string{"2"}})
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("path") != "/var/lib/app data" || parsed.Query().Get("depth") != "2" {
		t.Fatalf("query = %v", parsed.Query())
	}
	if strings.Contains(parsed.RawQuery, "/var/lib/app data") {
		t.Fatalf("raw query was not encoded: %q", parsed.RawQuery)
	}
}
