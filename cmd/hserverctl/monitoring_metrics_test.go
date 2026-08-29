package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func metricsRouteTestEnvironment(key string) string {
	if key == "HSERVER_TOKEN" {
		return "metrics-test-token"
	}
	return ""
}

func TestRunMonitoringMetricsRoutesPreserveAuthenticatedGETAndQueries(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer metrics-test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case metricsHistoryEndpoint:
			if request.URL.Query().Get("range") != "7d" {
				t.Errorf("history query = %q, want range=7d", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"range":"7d","resolution":"hourly","data":[{"bucket":"2026-08-28T12:00:00Z","sample_count":4,"cpu_avg":12.5,"cpu_max":18.0,"mem_avg":33.5,"mem_max":41.0,"swap_avg":1.5,"swap_max":2.0,"load_1m_avg":0.75,"net_rx_total":100,"net_tx_total":200,"disk_root_avg":40.0,"disk_root_max":45.0}]}`))
		case metricsProcessesEndpoint:
			if request.URL.Query().Get("at") != "2026-08-28T12:34:56.123Z" {
				t.Errorf("process query = %q, want exact at timestamp", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"requested_at":"2026-08-28T12:34:56.123Z","processes":[{"timestamp":"2026-08-28T12:34:55Z","pid":42,"username":"operator","cpu_percent":12.5,"memory_percent":3.25,"rss":9007199254740993,"command":"worker --mode=read"}]}`))
		case metricsProcessTimestampsEndpoint:
			if request.URL.Query().Get("range") != "6h" {
				t.Errorf("process timestamps query = %q, want range=6h", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(` ["2026-08-28T11:00:00Z","2026-08-28T12:00:00Z"] `))
		case metricsServicesHistoryEndpoint:
			if request.URL.Query().Get("range") != "24h" {
				t.Errorf("service history query = %q, want range=24h", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`[{"timestamp":"2026-08-28T12:00:00Z","name":"nginx","status":"active","pid":123}]`))
		case metricsSummaryEndpoint:
			if request.URL.RawQuery != "" {
				t.Errorf("summary unexpectedly received query %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"total_samples":17,"oldest_timestamp":"2026-08-27T12:00:00Z","newest_timestamp":"2026-08-28T12:00:00Z","db_size_bytes":4096}`))
		case monitoringProcessesEndpoint:
			if request.URL.RawQuery != "" {
				t.Errorf("monitoring processes unexpectedly received query %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`[{"pid":7,"startTime":123456,"user":"root","cpu":4.5,"memory":1.25,"vsz":1000,"rss":500,"stat":"S","command":"hserver"}]`))
		case monitoringStatsEndpoint:
			if request.URL.RawQuery != "" {
				t.Errorf("monitoring stats unexpectedly received query %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"cpu":{"usage":12.5,"cores":8,"model":"portable-cpu"},"memory":{"total":1000,"used":400,"free":600,"percentage":40,"buffers":10,"cached":20,"available":600,"swapTotal":200,"swapUsed":30,"swapFree":170,"swapPercentage":15},"disk":[{"mount":"/","total":10000,"used":4000,"free":6000,"percentage":40}],"load":[0.5,0.4,0.3],"uptime":3600,"hostname":"host.example","os":"linux","network":[{"interface":"eth0","bytesIn":100,"bytesOut":200}]}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	commands := [][]string{
		{"metrics", "history", "--range", "7d"},
		{"metrics", "processes", "--at", "2026-08-28T12:34:56.123Z"},
		{"metrics", "processes", "timestamps", "--range", "6h"},
		{"metrics", "services", "history", "--range", "24h"},
		{"metrics", "summary"},
		{"monitoring", "processes"},
		{"monitoring", "stats"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, metricsRouteTestEnvironment); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
	}
	if requests.Load() != int32(len(commands)) {
		t.Fatalf("requests = %d, want %d", requests.Load(), len(commands))
	}
}

func TestRunMonitoringMetricsTableOutputIsDeterministicAndContractBound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case metricsHistoryEndpoint:
			_, _ = writer.Write([]byte(`{"range":"1h","resolution":"raw","data":[{"timestamp":"2026-08-28T12:00:00Z","cpu_percent":1.5,"memory_total":100,"memory_used":50,"memory_percent":50,"memory_buffers":10,"memory_cached":20,"memory_available":50,"swap_total":200,"swap_used":25,"swap_percent":12.5,"load_1m":0.1,"load_5m":0.2,"load_15m":0.3,"net_rx_bytes":1000,"net_tx_bytes":2000,"disk_root_percent":30}]}`))
		case metricsProcessesEndpoint:
			_, _ = writer.Write([]byte(`{"requested_at":"2026-08-28T12:00:00Z","processes":[{"timestamp":"2026-08-28T12:00:00Z","pid":42,"username":"operator","cpu_percent":1.5,"memory_percent":2.5,"rss":500,"command":"worker"}]}`))
		case metricsProcessTimestampsEndpoint:
			_, _ = writer.Write([]byte(`["2026-08-28T12:00:00Z"]`))
		case metricsServicesHistoryEndpoint:
			_, _ = writer.Write([]byte(`[{"timestamp":"2026-08-28T12:00:00Z","name":"nginx","status":"active","pid":123}]`))
		case metricsSummaryEndpoint:
			_, _ = writer.Write([]byte(`{"total_samples":2,"oldest_timestamp":"old","newest_timestamp":"new","db_size_bytes":128}`))
		case monitoringProcessesEndpoint:
			_, _ = writer.Write([]byte(`[{"pid":7,"startTime":123456,"user":"root","cpu":4.5,"memory":1.25,"vsz":1000,"rss":500,"stat":"S","command":"hserver"}]`))
		case monitoringStatsEndpoint:
			_, _ = writer.Write([]byte(`{"cpu":{"usage":12.5,"cores":8,"model":"portable-cpu"},"memory":{"total":1000,"used":400,"free":600,"percentage":40,"buffers":10,"cached":20,"available":600,"swapTotal":200,"swapUsed":30,"swapFree":170,"swapPercentage":15},"disk":[{"mount":"/","total":10000,"used":4000,"free":6000,"percentage":40}],"load":[0.5,0.4,0.3],"uptime":3600,"hostname":"host.example","os":"linux","network":[{"interface":"eth0","bytesIn":100,"bytesOut":200}]}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	commands := [][]string{
		{"metrics", "history", "--format", "table"},
		{"metrics", "processes", "--format", "table"},
		{"metrics", "processes", "timestamps", "--format", "table"},
		{"metrics", "services", "history", "--format", "table"},
		{"metrics", "summary", "--format", "table"},
		{"monitoring", "processes", "--format", "table"},
		{"monitoring", "stats", "--format", "table"},
	}
	fragments := []string{
		"TIMESTAMP\tCPU_PERCENT\tMEMORY_TOTAL",
		"TIMESTAMP\tPID\tUSERNAME\tCPU_PERCENT",
		"TIMESTAMP\n2026-08-28T12:00:00Z",
		"TIMESTAMP\tNAME\tSTATUS\tPID",
		"FIELD\tVALUE\ntotal_samples\t2",
		"PID\tSTART_TIME\tUSER\tCPU\tMEMORY\tVSZ\tRSS\tSTAT\tCOMMAND",
		"CPU\nUSAGE\tCORES\tMODEL\n12.5\t8\tportable-cpu",
	}
	for index, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, metricsRouteTestEnvironment); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !strings.Contains(output.String(), fragments[index]) {
			t.Errorf("%s output missing %q:\n%s", strings.Join(command, " "), fragments[index], output.String())
		}
	}
}

func TestRunMonitoringMetricsRejectsInvalidReadOnlyOptionsBeforeNetwork(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"metrics", "history", "--range", "2h"}, want: "range must be 1h, 6h, 24h, 7d, or 30d"},
		{args: []string{"metrics", "processes", "--at", "yesterday"}, want: "must be RFC3339"},
		{args: []string{"metrics", "summary", "--format", "yaml"}, want: "format must be json or table"},
		{args: []string{"monitoring", "stats", "unexpected"}, want: "does not accept positional arguments"},
	}
	for _, test := range cases {
		err := run(context.Background(), append([]string{"--server", server.URL}, test.args...), &bytes.Buffer{}, &bytes.Buffer{}, metricsRouteTestEnvironment)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s error = %v, want %q", strings.Join(test.args, " "), err, test.want)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}

func TestMonitoringMetricsHelpAndCompletionExposeAuthenticatedRoutes(t *testing.T) {
	for _, args := range [][]string{
		{"help", "metrics"},
		{"help", "metrics", "processes", "timestamps"},
		{"monitoring", "stats", "--help"},
	} {
		var output bytes.Buffer
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, func(key string) string {
			if key == "HSERVER_TOKEN" {
				t.Fatal("help attempted to load the environment token")
			}
			return ""
		}); err != nil {
			t.Fatalf("%v help: %v", args, err)
		}
		expectedRoot := "metrics"
		if args[0] == "monitoring" {
			expectedRoot = "monitoring"
		}
		for _, fragment := range []string{expectedRoot, "--format", "json|table"} {
			if !strings.Contains(output.String(), fragment) {
				t.Errorf("%v help missing %q:\n%s", args, fragment, output.String())
			}
		}
		if args[0] == "monitoring" && !strings.Contains(output.String(), "--node NODE") {
			t.Errorf("%v help missing managed-node selector:\n%s", args, output.String())
		}
	}

	bash := generatedCompletionScript(t, "bash")
	for _, check := range []struct {
		words    []string
		expected string
	}{
		{words: []string{"hserverctl", "metrics", ""}, expected: "history"},
		{words: []string{"hserverctl", "metrics", "processes", ""}, expected: "timestamps"},
		{words: []string{"hserverctl", "metrics", "services", ""}, expected: "history"},
		{words: []string{"hserverctl", "monitoring", ""}, expected: "stats"},
		{words: []string{"hserverctl", "monitoring", "stats", "--"}, expected: "--node"},
		{words: []string{"hserverctl", "monitoring", "stats", "--"}, expected: "--format"},
	} {
		if !completionContains(runGeneratedBashCompletion(t, bash, check.words...), check.expected) {
			t.Errorf("completion for %q missing %q", check.words, check.expected)
		}
	}
}

func managedMonitoringStatsPayload() string {
	return `{"observed_at":"2026-08-29T10:11:12.123456Z","cpu":{"usage_percent":12.5,"core_count":8,"secret":"must-not-leak"},"load":{"one":0.5,"five":0.4,"fifteen":0.3},"memory":{"total_bytes":16000,"used_bytes":4000,"available_bytes":12000,"usage_percent":25},"network":{"rx_bytes":100,"tx_bytes":200},"root_disk":{"total_bytes":100000,"used_bytes":40000,"available_bytes":60000,"usage_percent":40},"secret":"must-not-leak"}`
}

func TestRunManagedMonitoringStatsUsesEscapedAuthenticatedGETAndFixedProjection(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", request.Method)
		}
		if request.URL.EscapedPath() != "/api/nodes/edge%2Fblue/metrics" {
			t.Errorf("escaped path = %q", request.URL.EscapedPath())
		}
		if request.Header.Get("Authorization") != "Bearer metrics-test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		if request.ContentLength != 0 {
			t.Errorf("GET content length = %d, want 0", request.ContentLength)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(managedMonitoringStatsPayload()))
	}))
	defer server.Close()

	var output bytes.Buffer
	if err := run(context.Background(), []string{"--server", server.URL, "monitoring", "stats", "--node", "edge/blue"}, &output, &bytes.Buffer{}, metricsRouteTestEnvironment); err != nil {
		t.Fatalf("managed monitoring stats: %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(output.Bytes(), &value); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, output.String())
	}
	if _, ok := value["secret"]; ok {
		t.Fatalf("top-level secret leaked in projection: %s", output.String())
	}
	cpu, ok := value["cpu"].(map[string]any)
	if !ok {
		t.Fatalf("cpu projection = %#v", value["cpu"])
	}
	if _, ok := cpu["secret"]; ok {
		t.Fatalf("nested secret leaked in projection: %s", output.String())
	}
	if got, want := cpu["core_count"], float64(8); got != want {
		t.Errorf("cpu.core_count = %v, want %v", got, want)
	}
	if got, want := value["observed_at"], "2026-08-29T10:11:12.123456Z"; got != want {
		t.Errorf("observed_at = %v, want %q", got, want)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestRunManagedMonitoringStatsTableIsBoundedAndTerminalSafe(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(managedMonitoringStatsPayload()))
	}))
	defer server.Close()

	var output bytes.Buffer
	if err := run(context.Background(), []string{"--server", server.URL, "monitoring", "stats", "--node", "edge-1", "--format", "text"}, &output, &bytes.Buffer{}, metricsRouteTestEnvironment); err != nil {
		t.Fatalf("managed monitoring stats table: %v", err)
	}
	for _, fragment := range []string{
		"Observed at: 2026-08-29T10:11:12.123456Z",
		"CPU\nUSAGE_PERCENT\tCORE_COUNT\n12.5\t8",
		"LOAD\nONE\tFIVE\tFIFTEEN\n0.5\t0.4\t0.3",
		"MEMORY\nTOTAL_BYTES\tUSED_BYTES\tAVAILABLE_BYTES\tUSAGE_PERCENT\n16000\t4000\t12000\t25",
		"NETWORK\nRX_BYTES\tTX_BYTES\n100\t200",
		"ROOT_DISK\nTOTAL_BYTES\tUSED_BYTES\tAVAILABLE_BYTES\tUSAGE_PERCENT\n100000\t40000\t60000\t40",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Errorf("table output missing %q:\n%s", fragment, output.String())
		}
	}
	if strings.Contains(output.String(), "secret") {
		t.Fatalf("table output leaked unprojected fields: %s", output.String())
	}
}

func TestManagedMonitoringStatsRejectsInvalidArgsAndPayloads(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(managedMonitoringStatsPayload()))
	}))
	defer server.Close()

	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"monitoring", "stats", "--node", ""}, want: "--node must not be empty"},
		{args: []string{"monitoring", "stats", "--node", "edge-1", "extra"}, want: "does not accept positional arguments"},
		{args: []string{"monitoring", "stats", "--node", "edge-1", "--node", "edge-2"}, want: "--node may be specified only once"},
		{args: []string{"monitoring", "stats", "--node", "edge-1", "--format", "yaml"}, want: "format must be json or table"},
		{args: []string{"monitoring", "processes", "--node", "edge-1"}, want: "flag provided but not defined: -node"},
		{args: []string{"metrics", "history", "--node", "edge-1"}, want: "flag provided but not defined: -node"},
	} {
		err := run(context.Background(), append([]string{"--server", server.URL}, test.args...), &bytes.Buffer{}, &bytes.Buffer{}, metricsRouteTestEnvironment)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s error = %v, want %q", strings.Join(test.args, " "), err, test.want)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid commands sent %d request(s)", requests.Load())
	}
}

func TestDecodeManagedMonitoringStatsRejectsMissingNegativeAndNonFiniteValues(t *testing.T) {
	base := managedMonitoringStatsPayload()
	for _, test := range []struct {
		name    string
		payload string
		want    string
	}{
		{name: "missing root disk usage", payload: strings.Replace(base, `,"usage_percent":40}`, `}`, 1), want: "missing root_disk.usage_percent"},
		{name: "negative load", payload: strings.Replace(base, `"one":0.5`, `"one":-0.5`, 1), want: "load.one must not be negative"},
		{name: "negative bytes", payload: strings.Replace(base, `"rx_bytes":100`, `"rx_bytes":-1`, 1), want: "network.rx_bytes must be a non-negative integer"},
		{name: "negative cores", payload: strings.Replace(base, `"core_count":8`, `"core_count":-1`, 1), want: "cpu.core_count must not be negative"},
		{name: "nonfinite usage", payload: strings.Replace(base, `"usage_percent":12.5`, `"usage_percent":1e1000`, 1), want: "cpu.usage_percent must be a finite number"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeManagedMonitoringStats([]byte(test.payload), managedMonitoringMetricsEndpoint); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
