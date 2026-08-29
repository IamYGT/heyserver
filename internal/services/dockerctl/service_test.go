package dockerctl

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

type fakeRunner struct {
	responses map[string][]byte
	errors    map[string]error
	calls     []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	return f.responses[call], f.errors[call]
}

func testService(runner *fakeRunner) *Service {
	return &Service{runner: runner, lookPath: func(string) (string, error) { return "/usr/bin/docker", nil }}
}

type blockingRunner struct{}

func (blockingRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestProbeInfoContextReportsFreshDockerInfoSuccess(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{
		"docker info": []byte("Server Version: 27.3.1\n"),
	}, errors: map[string]error{}}

	state, err := testService(runner).ProbeInfoContext(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("ProbeInfoContext() = state %q, err %v; want healthy", state, err)
	}
	if !reflect.DeepEqual(runner.calls, []string{"docker info"}) {
		t.Fatalf("docker calls = %#v, want only docker info", runner.calls)
	}
}

func TestProbeInfoContextDistinguishesMissingCLIFromDaemonFailure(t *testing.T) {
	missing := &Service{
		runner:   &fakeRunner{},
		lookPath: func(string) (string, error) { return "", errors.New("docker executable missing") },
	}
	state, err := missing.ProbeInfoContext(context.Background())
	if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing Docker = state %q, err %v; want not_configured", state, err)
	}

	runner := &fakeRunner{
		responses: map[string][]byte{},
		errors:    map[string]error{"docker info": errors.New("permission denied /var/run/docker.sock")},
	}
	state, err = testService(runner).ProbeInfoContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("daemon failure = state %q, err %v; want unavailable with error", state, err)
	}
}

func TestProbeInfoContextHonorsCallerTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	service := &Service{
		runner:   blockingRunner{},
		lookPath: func(string) (string, error) { return "/usr/bin/docker", nil },
	}
	started := time.Now()
	state, err := service.ProbeInfoContext(ctx)
	if state != integrationstate.Unavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed Docker probe = state %q, err %v; want unavailable/deadline", state, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timed Docker probe took %s", elapsed)
	}
}

func TestStatusUsesStableCamelCaseCounts(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{
		"docker info --format {{.ServerVersion}}": []byte("27.3.1\n"),
		"docker ps -aq":    []byte("one\ntwo\n"),
		"docker ps -q":     []byte("one\n"),
		"docker images -q": []byte("image-a\nimage-a\nimage-b\n"),
	}, errors: map[string]error{}}
	status, err := testService(runner).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Running || status.Version != "27.3.1" || status.ContainersTotal != 2 || status.ContainersRunning != 1 || status.ImageCount != 2 {
		t.Fatalf("status = %+v", status)
	}
}

func TestStatusDistinguishesMissingCLIAndStoppedDaemon(t *testing.T) {
	missing := &Service{runner: &fakeRunner{}, lookPath: func(string) (string, error) { return "", errors.New("missing") }}
	status, err := missing.Status(context.Background())
	if err != nil || status.Installed || status.Running || status.Error != "Docker CLI is not installed" {
		t.Fatalf("missing status = %+v, err=%v", status, err)
	}

	runner := &fakeRunner{responses: map[string][]byte{"docker --version": []byte("Docker version 27.3.1\n")}, errors: map[string]error{"docker info --format {{.ServerVersion}}": errors.New("daemon stopped")}}
	status, err = testService(runner).Status(context.Background())
	if err != nil || !status.Installed || status.Running || status.Error != "Docker daemon is unavailable" || status.Version == "" {
		t.Fatalf("stopped status = %+v, err=%v", status, err)
	}
}

func TestContainersNormalizesStatePortsAndStats(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{
		"docker ps -a --no-trunc --format {{json .}}":  []byte(`{"ID":"abc123","Names":"web-1","Image":"nginx:latest","State":"running","Status":"Up 2 minutes (healthy)","Ports":"0.0.0.0:8080->80/tcp, [::]:8080->80/tcp","CreatedAt":"2026-08-26 20:00:00 +0000 UTC"}` + "\n"),
		"docker stats --no-stream --format {{json .}}": []byte(`{"Container":"abc123","CPUPerc":"12.50%","MemUsage":"64MiB / 1GiB"}` + "\n"),
	}, errors: map[string]error{}}
	containers, err := testService(runner).Containers(context.Background())
	if err != nil || len(containers) != 1 {
		t.Fatalf("containers = %+v, err=%v", containers, err)
	}
	got := containers[0]
	if got.Status != "running" || got.Detail != "Up 2 minutes (healthy)" || got.CPUPercent != 12.5 || got.MemoryUsage != 64*(1<<20) || got.MemoryLimit != 1<<30 || len(got.Ports) != 2 {
		t.Fatalf("container = %+v", got)
	}
}

func TestImagesGroupsTagsByImmutableID(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{
		"docker images --no-trunc --format {{json .}}": []byte(
			`{"ID":"sha256:abc","Repository":"example/app","Tag":"latest","Size":"120MB","CreatedAt":"today"}` + "\n" +
				`{"ID":"sha256:abc","Repository":"example/app","Tag":"stable","Size":"120MB","CreatedAt":"today"}` + "\n"),
	}, errors: map[string]error{}}
	images, err := testService(runner).Images(context.Background())
	if err != nil || len(images) != 1 || !reflect.DeepEqual(images[0].RepoTags, []string{"example/app:latest", "example/app:stable"}) {
		t.Fatalf("images = %+v, err=%v", images, err)
	}
}

func TestBoundedLogsAndFixedMutationArguments(t *testing.T) {
	large := strings.Repeat("x", maxLogBytes+100)
	runner := &fakeRunner{responses: map[string][]byte{
		"docker logs --tail 200 --timestamps -- web-1": []byte(large),
	}, errors: map[string]error{}}
	service := testService(runner)
	logs, err := service.ContainerLogs(context.Background(), "web-1", 200)
	if err != nil || !logs.Truncated || len(logs.Logs) != maxLogBytes {
		t.Fatalf("logs length=%d truncated=%t err=%v", len(logs.Logs), logs.Truncated, err)
	}
	if err := service.ContainerAction(context.Background(), "web-1", "remove"); err != nil {
		t.Fatal(err)
	}
	if err := service.PullImage(context.Background(), "nginx:1.27"); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveImage(context.Background(), "sha256:abc"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"docker logs --tail 200 --timestamps -- web-1",
		"docker rm -- web-1",
		"docker pull -- nginx:1.27",
		"docker image rm -- sha256:abc",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestRejectsInvalidObjectIDsAndTail(t *testing.T) {
	service := testService(&fakeRunner{})
	if _, err := service.ContainerLogs(context.Background(), "../host", 200); err == nil {
		t.Fatal("unsafe container ID accepted")
	}
	if _, err := service.ContainerLogs(context.Background(), "web", 1001); err == nil {
		t.Fatal("oversized tail accepted")
	}
	if err := service.PullImage(context.Background(), "--help"); err == nil {
		t.Fatal("option-like image reference accepted")
	}
}

func TestValidContainerActionUsesFixedVocabulary(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "pause", "unpause", "remove"} {
		if !ValidContainerAction(action) {
			t.Fatalf("valid action %q rejected", action)
		}
	}
	for _, action := range []string{"exec", "kill", "rm", ""} {
		if ValidContainerAction(action) {
			t.Fatalf("unsupported action %q accepted", action)
		}
	}
}
