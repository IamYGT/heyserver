package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

type profileApplyRunner struct {
	calls []recordedCommand
	err   error
}

func (r *profileApplyRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	return nil, r.err
}

func TestDecodeProfileTaskPayloadIsExactBoundedAndStrict(t *testing.T) {
	valid := profileTestPayload(t, profileTestWrapper(t, 5))
	wrapper, code := decodeProfileTaskPayload(valid)
	if code != "" || wrapper.Revision != 5 {
		t.Fatalf("valid payload = %#v, code=%q", wrapper, code)
	}
	for _, test := range []struct {
		name    string
		payload map[string]string
		want    string
	}{
		{name: "unknown field", payload: map[string]string{"revision": "5", "profile_json_b64": valid["profile_json_b64"], "path": "/tmp"}, want: profileErrorPayload},
		{name: "noncanonical revision", payload: map[string]string{"revision": "+5", "profile_json_b64": valid["profile_json_b64"]}, want: profileErrorRevision},
		{name: "revision mismatch", payload: map[string]string{"revision": "6", "profile_json_b64": valid["profile_json_b64"]}, want: profileErrorRevision},
		{name: "invalid base64", payload: map[string]string{"revision": "5", "profile_json_b64": "not-base64"}, want: profileErrorPayload},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, code := decodeProfileTaskPayload(test.payload); code != test.want {
				t.Fatalf("code = %q, want %q", code, test.want)
			}
		})
	}
}

func TestProfileApplyIsIdempotentAndSupersedesOlderRevision(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	runner := &profileApplyRunner{}
	controller := newProfileApplyController(stateDir, "/local/installer", "/local/systemd-run", runner)
	controller.readyFn = func() bool { return true }
	if err := controller.store.ensureDirectory(); err != nil {
		t.Fatalf("ensure profile directory: %v", err)
	}
	active := profileTestWrapper(t, 5)
	if err := controller.store.writeWrapper(controller.store.activePath(), active); err != nil {
		t.Fatalf("write active profile: %v", err)
	}

	result, code := controller.apply(context.Background(), profileTestPayload(t, active))
	if code != "" || result.State != profileResultAlreadyActive || len(runner.calls) != 0 {
		t.Fatalf("idempotent apply = %#v code=%q calls=%#v", result, code, runner.calls)
	}
	_, code = controller.apply(context.Background(), profileTestPayload(t, profileTestWrapper(t, 4)))
	if code != profileErrorSuperseded || len(runner.calls) != 0 {
		t.Fatalf("older apply code=%q calls=%#v, want superseded/no execution", code, runner.calls)
	}

	newProfile := profileTestWrapper(t, 6)
	result, code = controller.apply(context.Background(), profileTestPayload(t, newProfile))
	if code != "" || result.State != profileResultRestartScheduled || result.Revision != 6 {
		t.Fatalf("new apply = %#v code=%q", result, code)
	}
	wantArgs := []string{"--collect", "--quiet", "--unit=hserver-agent-profile", "--on-active=3s", "--timer-property=AccuracySec=1s", "/local/installer", "apply-profile"}
	if !reflect.DeepEqual(runner.calls, []recordedCommand{{name: "/local/systemd-run", args: wantArgs}}) {
		t.Fatalf("schedule calls = %#v, want fixed argv %#v", runner.calls, wantArgs)
	}
	if _, err := os.Stat(controller.store.candidatePath()); err != nil {
		t.Fatalf("candidate profile: %v", err)
	}
	state, exists, err := controller.store.readState()
	if err != nil || !exists || state.State != profileStatePendingRestart || state.Revision != 6 {
		t.Fatalf("pending state = %#v exists=%t err=%v", state, exists, err)
	}
}

func TestProfileApplyNeverReturnsRunnerErrorOrHubPayload(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	runner := &profileApplyRunner{err: errors.New("exec /tmp/secret-command: output contains token=abc")}
	controller := newProfileApplyController(stateDir, "/local/installer", "/local/systemd-run", runner)
	controller.readyFn = func() bool { return true }
	result, code := controller.apply(context.Background(), profileTestPayload(t, profileTestWrapper(t, 1)))
	if result != (profileApplyOutcome{}) || code != profileErrorSchedule {
		t.Fatalf("failed apply = %#v code=%q", result, code)
	}
	failed := failedProfileTaskResult(code)
	if failed.Error != profileErrorSchedule || strings.Contains(failed.Error, "secret") || strings.Contains(failed.Error, "token") {
		t.Fatalf("unsafe failure result = %#v", failed)
	}
}

func TestProfileCapabilityRequiresBothFixedExecutables(t *testing.T) {
	directory := t.TempDir()
	installer := filepath.Join(directory, "installer")
	systemdRun := filepath.Join(directory, "systemd-run")
	if err := os.WriteFile(installer, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(systemdRun, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config{agentLifecycleInstaller: installer, systemdRunBinary: systemdRun}
	if !profileApplyReady(cfg) {
		t.Fatal("profile apply should be ready when both fixed executables exist")
	}
	cfg.systemdRunBinary = filepath.Join(directory, "missing")
	if profileApplyReady(cfg) {
		t.Fatal("profile apply should not be ready without systemd-run")
	}
	for _, capability := range advertisedCapabilities(cfg) {
		if capability == profileApplyCapability {
			t.Fatalf("profile apply capability advertised without systemd-run: %#v", advertisedCapabilities(cfg))
		}
	}
	capabilities := advertisedCapabilities(config{agentLifecycleInstaller: installer, systemdRunBinary: systemdRun})
	found := false
	for _, capability := range capabilities {
		if capability == profileApplyCapability {
			found = true
		}
	}
	if !found {
		t.Fatalf("advertised capabilities = %#v", capabilities)
	}
}

func TestProfileObservationAttachmentUsesBoundedWireShape(t *testing.T) {
	request := agenthub.HeartbeatRequest{}
	attachProfileObservation(&request, profileObservation{State: profileObservationFailed, Revision: 7, ErrorCode: profileErrorCorrupt})
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Profile *agenthub.AgentProfileObservation `json:"profile"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Profile == nil || decoded.Profile.State != profileObservationFailed || decoded.Profile.Revision != 7 || decoded.Profile.ErrorCode != profileErrorCorrupt {
		t.Fatalf("profile observation = %#v, wire=%s", decoded.Profile, data)
	}
}
