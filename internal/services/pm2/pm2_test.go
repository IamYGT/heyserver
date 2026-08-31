package pm2

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

func TestProbeProcessesContextHonorsCancellation(t *testing.T) {
	installFakeSudo(t, "[]")
	service, err := New(Config{User: "app", Bin: "pm2"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	inventory, err := service.ProbeProcessesContext(ctx)
	if err == nil {
		t.Fatal("ProbeProcessesContext() error = nil, want cancellation error")
	}
	if inventory.State != integrationstate.Unavailable {
		t.Fatalf("ProbeProcessesContext() state = %q, want %q", inventory.State, integrationstate.Unavailable)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("ProbeProcessesContext() took %s after cancellation", elapsed)
	}
}

func TestNewRequiresUnprivilegedOwner(t *testing.T) {
	_, err := New(Config{})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("New(Config{}): got %v, want ErrNotConfigured", err)
	}
}

func installFakeSudo(t *testing.T, output string) {
	installFakeSudoWithExit(t, output, 0)
}

func installFakeSudoFailure(t *testing.T, output string) {
	installFakeSudoWithExit(t, output, 1)
}

func installFakeSudoWithExit(t *testing.T, output string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "sudo")
	contents := "#!/bin/sh\ncat <<'EOF'\n" + output + "\nEOF\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatalf("write fake sudo: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestClassifyInventoryErrorUsesCanonicalStates(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want integrationstate.State
	}{
		{name: "successful inventory", want: integrationstate.Healthy},
		{name: "missing configuration", err: ErrNotConfigured, want: integrationstate.NotConfigured},
		{name: "missing executable", err: errors.New(`exec: "pm2": executable file not found in $PATH`), want: integrationstate.NotConfigured},
		{name: "configured probe failure", err: errors.New("pm2 jlist: exit status 1"), want: integrationstate.Unavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyInventoryError(test.err); got != test.want {
				t.Fatalf("ClassifyInventoryError(%v) = %q, want %q", test.err, got, test.want)
			}
		})
	}
}

func TestListMissingAbsoluteBinaryIsNotConfigured(t *testing.T) {
	service, err := New(Config{
		User: "app",
		Bin:  filepath.Join(t.TempDir(), "missing-pm2"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = service.List()
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("List() error = %v, want ErrNotConfigured", err)
	}
	if got := ClassifyInventoryError(err); got != integrationstate.NotConfigured {
		t.Fatalf("ClassifyInventoryError(List error) = %q, want %q", got, integrationstate.NotConfigured)
	}
}

func TestProbeProcessesTreatsSuccessfulEmptyInventoryAsHealthy(t *testing.T) {
	installFakeSudo(t, "[]")
	service, err := New(Config{User: "app", Bin: "pm2"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	inventory, err := service.ProbeProcesses()
	if err != nil {
		t.Fatalf("ProbeProcesses() error = %v", err)
	}
	if inventory.State != integrationstate.Healthy {
		t.Fatalf("ProbeProcesses() state = %q, want %q", inventory.State, integrationstate.Healthy)
	}
	if inventory.Processes == nil || len(inventory.Processes) != 0 {
		t.Fatalf("ProbeProcesses() processes = %#v, want a non-nil empty array", inventory.Processes)
	}
}

func TestProbeProcessesReportsMissingCommandAsNotConfigured(t *testing.T) {
	installFakeSudoFailure(t, "env: pm2: No such file or directory")
	service, err := New(Config{User: "app", Bin: "pm2"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The command name is intentionally resolved inside the configured user's
	// environment, so the probe output is the authoritative missing-binary
	// signal rather than Heyserver's own PATH.
	inventory, err := service.ProbeProcesses()
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ProbeProcesses() error = %v, want ErrNotConfigured", err)
	}
	if inventory.State != integrationstate.NotConfigured {
		t.Fatalf("ProbeProcesses() state = %q, want %q", inventory.State, integrationstate.NotConfigured)
	}
}

func TestProbeProcessesKeepsRuntimeStatusSeparateFromAvailability(t *testing.T) {
	installFakeSudo(t, `[ {"pm_id": 1, "name": "worker", "pm2_env": {"status": "stopped"} } ]`)
	service, err := New(Config{User: "app", Bin: "pm2"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	inventory, err := service.ProbeProcesses()
	if err != nil {
		t.Fatalf("ProbeProcesses() error = %v", err)
	}
	if inventory.State != integrationstate.Healthy {
		t.Fatalf("ProbeProcesses() state = %q, want %q", inventory.State, integrationstate.Healthy)
	}
	if len(inventory.Processes) != 1 || inventory.Processes[0].Status != "stopped" {
		t.Fatalf("ProbeProcesses() processes = %#v, want one stopped process", inventory.Processes)
	}
}

func TestNewValidatesPortableConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{"root owner", Config{User: "root"}},
		{"invalid user", Config{User: "root;id"}},
		{"relative home", Config{User: "app", Home: ".pm2"}},
		{"relative binary path", Config{User: "app", Bin: "bin/pm2"}},
		{"invalid binary name", Config{User: "app", Bin: "pm2;id"}},
		{"relative allowed root", Config{User: "app", AllowedRoots: []string{"srv/apps"}}},
		{"filesystem root", Config{User: "app", AllowedRoots: []string{"/"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.config); !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("New(%+v): got %v, want ErrNotConfigured", test.config, err)
			}
		})
	}
}

func TestCommandArgsUseConfiguredOwnerAndDaemon(t *testing.T) {
	service, err := New(Config{
		User: "app",
		Home: "/home/app/.pm2",
		Bin:  "/home/app/.nvm/versions/node/v22.14.0/bin/pm2",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := []string{
		"-H", "-u", "app", "--", "env",
		"PM2_HOME=/home/app/.pm2",
		"/home/app/.nvm/versions/node/v22.14.0/bin/pm2",
		"restart", "api",
	}
	if got := service.commandArgs("restart", "api"); !reflect.DeepEqual(got, want) {
		t.Fatalf("commandArgs: got %#v, want %#v", got, want)
	}
}

func TestCommandArgsDefaultToPathPM2(t *testing.T) {
	service, err := New(Config{User: "app"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := []string{"-H", "-u", "app", "--", "env", "pm2", "save"}
	if got := service.commandArgs("save"); !reflect.DeepEqual(got, want) {
		t.Fatalf("commandArgs: got %#v, want %#v", got, want)
	}
}

func TestCommandArgsWithNodeEnvUsesFixedEnvironment(t *testing.T) {
	service, err := New(Config{User: "app", Home: "/home/app/.pm2"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := []string{
		"-H", "-u", "app", "--", "env",
		"PM2_HOME=/home/app/.pm2",
		"NODE_ENV=production",
		"pm2", "start", "/srv/app/server.js",
	}
	if got := service.commandArgsWithNodeEnv("production", "start", "/srv/app/server.js"); !reflect.DeepEqual(got, want) {
		t.Fatalf("commandArgsWithNodeEnv: got %#v, want %#v", got, want)
	}
}

func TestNormalizeDeployRequestDefaults(t *testing.T) {
	request, err := normalizeDeployRequest(DeployRequest{
		Name:   " api ",
		Script: " /var/www/example.com/./server.js ",
		Cwd:    " /var/www/example.com/./ ",
	}, []string{"/var/www"})
	if err != nil {
		t.Fatal(err)
	}
	if request.Name != "api" || request.Script != "/var/www/example.com/server.js" || request.Cwd != "/var/www/example.com" {
		t.Fatalf("normalized request = %#v", request)
	}
	if request.ExecMode != "fork" || request.Instances != 1 {
		t.Fatalf("defaults = mode %q instances %d", request.ExecMode, request.Instances)
	}
}

func TestNormalizeDeployRequestRejectsUnboundedValues(t *testing.T) {
	tests := []DeployRequest{
		{Name: "api worker", Script: "/var/www/example.com/server.js"},
		{Name: "api", Script: "/srv/example.com/server.js"},
		{Name: "api", Script: "/var/www/example.com/server.js", ExecMode: "shell"},
		{Name: "api", Script: "/var/www/example.com/server.js", Instances: -1},
		{Name: "api", Script: "/var/www/example.com/server.js", Instances: 65},
		{Name: "api", Script: "/var/www/example.com/server.js", NodeEnv: "staging"},
	}
	for _, request := range tests {
		if _, err := normalizeDeployRequest(request, []string{"/var/www"}); !errors.Is(err, ErrInvalidDeploy) {
			t.Fatalf("normalizeDeployRequest(%#v): got %v, want ErrInvalidDeploy", request, err)
		}
	}
}

func TestNormalizeDeployRequestUsesConfiguredRoots(t *testing.T) {
	request, err := normalizeDeployRequest(DeployRequest{
		Name:   "api",
		Script: "/srv/hserver/sites/example.com/server.js",
		Cwd:    "/srv/hserver/sites/example.com",
	}, []string{"/srv/hserver/sites"})
	if err != nil {
		t.Fatalf("configured root rejected: %v", err)
	}
	if request.Script != "/srv/hserver/sites/example.com/server.js" {
		t.Fatalf("request = %#v", request)
	}

	_, err = normalizeDeployRequest(DeployRequest{
		Name:   "api",
		Script: "/srv/hserver/sites-other/example.com/server.js",
	}, []string{"/srv/hserver/sites"})
	if !errors.Is(err, ErrInvalidDeploy) {
		t.Fatalf("sibling-prefix escape error = %v, want ErrInvalidDeploy", err)
	}
}

func TestDeployRequiresConfiguredRoots(t *testing.T) {
	service, err := New(Config{User: "app"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Deploy(&DeployRequest{Name: "api", Script: "/var/www/example.com/server.js"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Deploy() error = %v, want ErrNotConfigured", err)
	}
}

// ---------------------------------------------------------------------------
// parseJList tests
// ---------------------------------------------------------------------------

func TestParseJList_ValidJSON(t *testing.T) {
	input := `[
		{
			"pm_id": 0,
			"name": "myapp",
			"pid": 1234,
			"pm2_env": {
				"status": "online",
				"exec_mode": "fork_mode",
				"restart_time": 3,
				"pm_uptime": 0,
				"created_at": 0,
				"pm_exec_path": "/app/server.js",
				"pm_out_log_path": "/logs/out.log",
				"pm_err_log_path": "/logs/err.log"
			},
			"monit": {"cpu": 0.5, "memory": 52428800}
		}
	]`

	procs, err := parseJList(input)
	if err != nil {
		t.Fatalf("parseJList: unexpected error: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("expected 1 process, got %d", len(procs))
	}

	p := procs[0]
	if p.PMID != 0 {
		t.Errorf("PMID: got %d, want 0", p.PMID)
	}
	if p.Name != "myapp" {
		t.Errorf("Name: got %q, want %q", p.Name, "myapp")
	}
	if p.PID != 1234 {
		t.Errorf("PID: got %d, want 1234", p.PID)
	}
	if p.PM2Env.Status != "online" {
		t.Errorf("Status: got %q, want %q", p.PM2Env.Status, "online")
	}
	if p.Monit.Memory != 52428800 {
		t.Errorf("Memory: got %d, want 52428800", p.Monit.Memory)
	}
}

func TestParseJList_WithDebugPrefix(t *testing.T) {
	// pm2 sometimes emits non-bracket debug lines before the JSON array.
	// The implementation finds the first '[' and parses from there.
	// Debug lines that don't contain '[' are safely skipped.
	input := `PM2 Spawning PM2 daemon with pm2_home=/home/app/.pm2
PM2 PM2 Successfully daemonized
[{"pm_id":1,"name":"api","pid":5678,"pm2_env":{"status":"online","exec_mode":"cluster_mode","restart_time":0,"pm_uptime":0,"created_at":0,"pm_exec_path":"/api/index.js","pm_out_log_path":"","pm_err_log_path":""},"monit":{"cpu":1.2,"memory":10485760}}]`

	procs, err := parseJList(input)
	if err != nil {
		t.Fatalf("parseJList with prefix: unexpected error: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("expected 1 process, got %d", len(procs))
	}
	if procs[0].Name != "api" {
		t.Errorf("Name: got %q, want %q", procs[0].Name, "api")
	}
}

func TestParseJList_IgnoresTrailingPM2Notice(t *testing.T) {
	input := "[]PM2 module update available\n"
	procs, err := parseJList(input)
	if err != nil {
		t.Fatalf("unexpected trailing-notice error: %v", err)
	}
	if len(procs) != 0 {
		t.Fatalf("expected empty process list, got %d", len(procs))
	}
}

func TestParseJList_SkipsANSIBracketBeforeJSON(t *testing.T) {
	input := "\x1b[32m[]\x1b[0m"
	procs, err := parseJList(input)
	if err != nil {
		t.Fatalf("unexpected ANSI-prefix error: %v", err)
	}
	if len(procs) != 0 {
		t.Fatalf("expected empty process list, got %d", len(procs))
	}
}

func TestParseJList_EmptyOutput(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no bracket", "PM2 daemon not running"},
		{"whitespace", "   \n  "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			procs, err := parseJList(tc.input)
			if err != nil {
				t.Fatalf("parseJList(%q): unexpected error: %v", tc.name, err)
			}
			if procs != nil {
				t.Errorf("parseJList(%q): expected nil, got %v", tc.name, procs)
			}
		})
	}
}

func TestParseJList_EmptyArray(t *testing.T) {
	procs, err := parseJList("[]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(procs) != 0 {
		t.Errorf("expected 0 processes, got %d", len(procs))
	}
}

func TestParseJList_MalformedJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"truncated", `[{"pm_id":1,"name":"app"`},
		{"invalid value", `[{"pm_id":"notanumber"}]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseJList(tc.input)
			if err == nil {
				t.Errorf("parseJList(%q): expected error for malformed JSON, got nil", tc.name)
			}
		})
	}
}

func TestParseJList_MultipleProcesses(t *testing.T) {
	input := `[
		{"pm_id":0,"name":"web","pid":100,"pm2_env":{"status":"online","exec_mode":"fork_mode","restart_time":0,"pm_uptime":0,"created_at":0,"pm_exec_path":"","pm_out_log_path":"","pm_err_log_path":""},"monit":{"cpu":0,"memory":0}},
		{"pm_id":1,"name":"worker","pid":200,"pm2_env":{"status":"stopped","exec_mode":"cluster_mode","restart_time":5,"pm_uptime":0,"created_at":0,"pm_exec_path":"","pm_out_log_path":"","pm_err_log_path":""},"monit":{"cpu":0,"memory":0}},
		{"pm_id":2,"name":"scheduler","pid":300,"pm2_env":{"status":"errored","exec_mode":"fork_mode","restart_time":10,"pm_uptime":0,"created_at":0,"pm_exec_path":"","pm_out_log_path":"","pm_err_log_path":""},"monit":{"cpu":0,"memory":0}}
	]`

	procs, err := parseJList(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(procs) != 3 {
		t.Fatalf("expected 3 processes, got %d", len(procs))
	}

	names := []string{"web", "worker", "scheduler"}
	for i, want := range names {
		if procs[i].Name != want {
			t.Errorf("procs[%d].Name: got %q, want %q", i, procs[i].Name, want)
		}
	}
}

// ---------------------------------------------------------------------------
// toModel tests
// ---------------------------------------------------------------------------

func TestToModel_BasicFields(t *testing.T) {
	raw := pm2RawProcess{
		PMID: 3,
		Name: "nextjs",
		PID:  9999,
	}
	raw.PM2Env.Status = "online"
	raw.PM2Env.ExecMode = "cluster_mode"
	raw.PM2Env.RestartTime = 7
	raw.Monit.CPU = 2.5
	raw.Monit.Memory = 0

	model := toModel(raw)

	if model.ID != 3 {
		t.Errorf("ID: got %d, want 3", model.ID)
	}
	if model.Name != "nextjs" {
		t.Errorf("Name: got %q, want %q", model.Name, "nextjs")
	}
	if model.PID != 9999 {
		t.Errorf("PID: got %d, want 9999", model.PID)
	}
	if model.Status != "online" {
		t.Errorf("Status: got %q, want %q", model.Status, "online")
	}
	if model.Mode != "cluster_mode" {
		t.Errorf("Mode: got %q, want %q", model.Mode, "cluster_mode")
	}
	if model.Restarts != 7 {
		t.Errorf("Restarts: got %d, want 7", model.Restarts)
	}
	if model.CPU != 2.5 {
		t.Errorf("CPU: got %f, want 2.5", model.CPU)
	}
}

func TestToModel_MemoryConversion(t *testing.T) {
	tests := []struct {
		name      string
		bytes     int64
		wantMBMin float64
		wantMBMax float64
	}{
		{"50 MB", 52428800, 49.9, 50.1},
		{"100 MB", 104857600, 99.9, 100.1},
		{"1 MB", 1048576, 0.99, 1.01},
		{"zero", 0, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := pm2RawProcess{}
			raw.Monit.Memory = tc.bytes
			model := toModel(raw)

			if model.Memory < tc.wantMBMin || model.Memory > tc.wantMBMax {
				t.Errorf("Memory(%d bytes): got %.4f MB, want in [%.1f, %.1f]",
					tc.bytes, model.Memory, tc.wantMBMin, tc.wantMBMax)
			}
		})
	}
}

func TestToModel_MemoryFormula(t *testing.T) {
	// Verify exact formula: bytes / 1024 / 1024
	const bytes = int64(15 * 1024 * 1024) // 15 MB exactly
	raw := pm2RawProcess{}
	raw.Monit.Memory = bytes
	model := toModel(raw)

	want := float64(bytes) / 1024 / 1024
	if math.Abs(model.Memory-want) > 1e-9 {
		t.Errorf("Memory: got %f, want %f", model.Memory, want)
	}
}

func TestToModel_UptimeZeroWhenNoUptime(t *testing.T) {
	raw := pm2RawProcess{}
	raw.PM2Env.PMUptime = 0 // not started / stopped

	model := toModel(raw)
	if model.Uptime != 0 {
		t.Errorf("Uptime: got %d, want 0 when PMUptime=0", model.Uptime)
	}
}

func TestToModel_UptimePositiveWhenRunning(t *testing.T) {
	// Process started 5 minutes ago
	startedAt := time.Now().Add(-5 * time.Minute)
	raw := pm2RawProcess{}
	raw.PM2Env.PMUptime = startedAt.UnixMilli()

	model := toModel(raw)

	// Allow 2-second tolerance for test execution time
	wantSeconds := int64(5 * 60)
	if model.Uptime < wantSeconds-2 || model.Uptime > wantSeconds+2 {
		t.Errorf("Uptime: got %d seconds, want ~%d seconds", model.Uptime, wantSeconds)
	}
}

func TestToModel_UptimeFromFutureTimestampIsZero(t *testing.T) {
	// pm_uptime > 0 but far in the future should not panic; uptime will be negative
	// toModel uses time.Since which can be negative — just verify no panic.
	future := time.Now().Add(10 * time.Minute)
	raw := pm2RawProcess{}
	raw.PM2Env.PMUptime = future.UnixMilli()

	// Should not panic
	_ = toModel(raw)
}

// ---------------------------------------------------------------------------
// Integration: parseJList → toModel round-trip
// ---------------------------------------------------------------------------

func TestParseJListToModel_RoundTrip(t *testing.T) {
	// pm_uptime=0 → stopped; monit.memory=20971520 = 20 MB
	input := `[{"pm_id":5,"name":"turbo","pid":7777,"pm2_env":{"status":"online","exec_mode":"fork_mode","restart_time":2,"pm_uptime":0,"created_at":0,"pm_exec_path":"/app/server.js","pm_out_log_path":"","pm_err_log_path":""},"monit":{"cpu":3.14,"memory":20971520}}]`

	procs, err := parseJList(input)
	if err != nil {
		t.Fatalf("parseJList: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("expected 1 process")
	}

	model := toModel(procs[0])

	if model.ID != 5 {
		t.Errorf("ID: got %d, want 5", model.ID)
	}
	if model.Name != "turbo" {
		t.Errorf("Name: got %q, want turbo", model.Name)
	}
	if math.Abs(model.Memory-20.0) > 0.01 {
		t.Errorf("Memory: got %f MB, want 20.0 MB", model.Memory)
	}
	if math.Abs(model.CPU-3.14) > 0.001 {
		t.Errorf("CPU: got %f, want 3.14", model.CPU)
	}
	if !strings.Contains(model.Mode, "fork") {
		t.Errorf("Mode: got %q, expected to contain 'fork'", model.Mode)
	}
}

func TestSystemdServiceNameAcceptsOnlyUnprivilegedLinuxOwners(t *testing.T) {
	if got, ok := SystemdServiceName(" deploy "); !ok || got != "pm2-deploy" {
		t.Fatalf("SystemdServiceName(deploy) = %q, %v", got, ok)
	}
	for _, user := range []string{"", "root", "Invalid", "invalid user", "../escape"} {
		if got, ok := SystemdServiceName(user); ok || got != "" {
			t.Fatalf("SystemdServiceName(%q) = %q, %v; want rejected", user, got, ok)
		}
	}
}
