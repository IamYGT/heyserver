package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/services/systemactions"
)

type recordedCommand struct {
	name string
	args []string
}

type fakeRunner struct {
	commands []recordedCommand
	outputs  [][]byte
	errors   []error
}

type fakeHostActions struct {
	action        string
	message       string
	err           error
	processResult systemactions.ProcessSignalResult
	pid           int
	startTime     uint64
	signal        string
}

type fakeDiskCleanup struct {
	targets   []diskCleanupTarget
	execution diskCleanupExecution
	executed  []string
}

type fakeAgentUpdates struct {
	action  string
	version string
	status  managedAgentUpdateStatus
	err     error
}

func (f *fakeAgentUpdates) Status(context.Context) (managedAgentUpdateStatus, error) {
	f.action = "status"
	return f.status, f.err
}
func (f *fakeAgentUpdates) Upgrade(_ context.Context, version string) (managedAgentUpdateStatus, error) {
	f.action, f.version = "upgrade", version
	return f.status, f.err
}
func (f *fakeAgentUpdates) Rollback(context.Context) (managedAgentUpdateStatus, error) {
	f.action = "rollback"
	return f.status, f.err
}

func (f *fakeDiskCleanup) Scan(context.Context) ([]diskCleanupTarget, error) { return f.targets, nil }
func (f *fakeDiskCleanup) Execute(_ context.Context, targets []string) (diskCleanupExecution, error) {
	f.executed = append([]string(nil), targets...)
	return f.execution, nil
}

func (f *fakeHostActions) run(action string) (string, error) {
	f.action = action
	return f.message, f.err
}
func (f *fakeHostActions) OptimizeMemory(context.Context) (string, error) {
	return f.run("memory-optimize")
}
func (f *fakeHostActions) ResetSwap(context.Context) (string, error) { return f.run("swap-reset") }
func (f *fakeHostActions) CleanTemporaryFiles(context.Context) (string, error) {
	return f.run("temp-clean")
}
func (f *fakeHostActions) ScheduleReboot(context.Context) (string, error) { return f.run("reboot") }
func (f *fakeHostActions) CancelScheduledReboot(context.Context) (string, error) {
	return f.run("reboot-cancel")
}
func (f *fakeHostActions) TerminateProcess(pid int, signal string, startTime uint64) (systemactions.ProcessSignalResult, error) {
	f.pid, f.signal, f.startTime = pid, signal, startTime
	return f.processResult, f.err
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.commands = append(f.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	index := len(f.commands) - 1
	var output []byte
	var err error
	if index < len(f.outputs) {
		output = f.outputs[index]
	}
	if index < len(f.errors) {
		err = f.errors[index]
	}
	return output, err
}

func TestTaskExecutorRunsOnlyAllowlistedActionWithoutShell(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{nil, []byte("active\nrunning\n")}}
	services := serviceController{runner: runner}
	executor := newTaskExecutor(services, nil, nil, nil, []string{"ssh.service"}, map[string]struct{}{"nginx.service": {}}, nil, false)
	result := executor.execute(context.Background(), &agenthub.Task{
		Kind:    agenthub.TaskServiceAction,
		Payload: map[string]string{"service": "nginx.service", "action": "restart"},
	})
	if result.Status != "completed" || result.Result["active"] != "active" || result.Result["sub"] != "running" {
		t.Fatalf("result = %#v", result)
	}
	want := []recordedCommand{
		{name: "systemctl", args: []string{"restart", "nginx.service"}},
		{name: "systemctl", args: []string{"show", "--property=ActiveState,SubState", "--value", "nginx.service"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestTaskExecutorKeepsAgentLifecycleBehindLocalActionOptIn(t *testing.T) {
	updates := &fakeAgentUpdates{status: managedAgentUpdateStatus{CurrentVersion: "v1.0.0", LatestVersion: "v1.2.3", OperationStatus: agentUpdateScheduled}}
	executor := taskExecutor{agentUpdates: updates}
	status := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskAgentUpdateStatus, Payload: map[string]string{}})
	if status.Status != agenthub.TaskStatusCompleted || status.Result["data"] == "" || updates.action != "status" {
		t.Fatalf("status result = %#v, action=%q", status, updates.action)
	}
	blocked := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskAgentUpdateAction, Payload: map[string]string{"action": "upgrade", "version": "v1.2.3"}})
	if blocked.Status != agenthub.TaskStatusFailed || updates.action != "status" {
		t.Fatalf("blocked result = %#v, action=%q", blocked, updates.action)
	}
	executor.allowAgentUpdates = true
	upgrade := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskAgentUpdateAction, Payload: map[string]string{"action": "upgrade", "version": "v1.2.3"}})
	if upgrade.Status != agenthub.TaskStatusCompleted || updates.action != "upgrade" || updates.version != "v1.2.3" {
		t.Fatalf("upgrade result = %#v, update executor=%#v", upgrade, updates)
	}
}

func TestTaskExecutorRejectsForbiddenInputsBeforeExecution(t *testing.T) {
	tests := []agenthub.Task{
		{Kind: agenthub.TaskServiceAction, Payload: map[string]string{"service": "ssh.service", "action": "restart"}},
		{Kind: agenthub.TaskServiceAction, Payload: map[string]string{"service": "nginx.service", "action": "enable"}},
		{Kind: agenthub.TaskServiceAction, Payload: map[string]string{"service": "nginx.service", "action": "restart", "arg": "--now"}},
		{Kind: "shell", Payload: map[string]string{"service": "nginx.service"}},
		{Kind: agenthub.TaskServiceStatus, Payload: map[string]string{"service": "unknown.service"}},
	}
	for _, task := range tests {
		runner := &fakeRunner{}
		executor := newTaskExecutor(serviceController{runner: runner}, nil, nil, nil, []string{"ssh.service"}, map[string]struct{}{"nginx.service": {}}, nil, false)
		result := executor.execute(context.Background(), &task)
		if result.Status != "failed" || result.Error == "" {
			t.Fatalf("task %#v result = %#v", task, result)
		}
		if len(runner.commands) != 0 {
			t.Fatalf("task %#v executed commands %#v", task, runner.commands)
		}
	}
}

func TestTaskExecutorReportsCommandFailureWithoutOutput(t *testing.T) {
	runner := &fakeRunner{errors: []error{errors.New("exit status 1")}}
	executor := newTaskExecutor(serviceController{runner: runner}, nil, nil, nil, nil, map[string]struct{}{"nginx.service": {}}, nil, false)
	result := executor.execute(context.Background(), &agenthub.Task{
		Kind:    agenthub.TaskServiceAction,
		Payload: map[string]string{"service": "nginx.service", "action": "stop"},
	})
	if result.Status != "failed" || result.Error != "systemctl action failed: exit status 1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestTaskExecutorRunsOnlyLocallyAllowedHostAction(t *testing.T) {
	host := &fakeHostActions{message: "swap reset completed"}
	executor := newTaskExecutor(serviceController{}, host, nil, nil, nil, nil, map[string]struct{}{"swap-reset": {}}, false)
	result := executor.execute(context.Background(), &agenthub.Task{
		Kind:    agenthub.TaskHostAction,
		Payload: map[string]string{"action": "swap-reset"},
	})
	if result.Status != "completed" || result.Result["message"] != host.message || host.action != "swap-reset" {
		t.Fatalf("result = %#v, host action = %q", result, host.action)
	}

	blocked := executor.execute(context.Background(), &agenthub.Task{
		Kind:    agenthub.TaskHostAction,
		Payload: map[string]string{"action": "reboot"},
	})
	if blocked.Status != "failed" || blocked.Error == "" || host.action != "swap-reset" {
		t.Fatalf("blocked result = %#v, host action = %q", blocked, host.action)
	}
}

func TestTaskExecutorSignalsOnlyStableProcessIdentityWhenEnabled(t *testing.T) {
	host := &fakeHostActions{processResult: systemactions.ProcessSignalResult{Message: "TERM stopped worker", Exited: true, Confirmed: true}}
	executor := newTaskExecutor(serviceController{}, host, nil, nil, nil, nil, nil, true)
	result := executor.execute(context.Background(), &agenthub.Task{
		Kind:    agenthub.TaskProcessSignal,
		Payload: map[string]string{"pid": "42", "start_time": "987654", "signal": "term"},
	})
	if result.Status != "completed" || result.Result["exited"] != "true" || host.pid != 42 || host.startTime != 987654 || host.signal != "term" {
		t.Fatalf("result = %#v, host = %#v", result, host)
	}

	blocked := newTaskExecutor(serviceController{}, host, nil, nil, nil, nil, nil, false).execute(context.Background(), &agenthub.Task{
		Kind:    agenthub.TaskProcessSignal,
		Payload: map[string]string{"pid": "42", "start_time": "987654", "signal": "kill"},
	})
	if blocked.Status != "failed" {
		t.Fatalf("blocked result = %#v", blocked)
	}
}

func TestTaskExecutorReturnsStructuredDiskCleanupResults(t *testing.T) {
	disk := &fakeDiskCleanup{
		targets:   []diskCleanupTarget{{ID: "journal", Name: "Journal", Size: 1024, Risk: "low"}},
		execution: diskCleanupExecution{Results: []diskCleanupResult{{ID: "journal", Status: "ok", Reclaimed: 1024}}},
	}
	executor := newTaskExecutor(serviceController{}, nil, disk, nil, nil, nil, nil, false)
	scan := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskDiskCleanupScan, Payload: map[string]string{}})
	if scan.Status != agenthub.TaskStatusCompleted || scan.Result["data"] == "" {
		t.Fatalf("scan result = %#v", scan)
	}
	execute := executor.execute(context.Background(), &agenthub.Task{
		Kind: agenthub.TaskDiskCleanupExecute, Payload: map[string]string{"targets": "journal"},
	})
	if execute.Status != agenthub.TaskStatusCompleted || execute.Result["data"] == "" || len(disk.executed) != 1 || disk.executed[0] != "journal" {
		t.Fatalf("execute result = %#v, targets=%#v", execute, disk.executed)
	}
}

func TestBoundedBufferRejectsExcessOutput(t *testing.T) {
	buffer := &boundedBuffer{}
	written, err := buffer.Write(make([]byte, maxCommandOutputBytes+1))
	if written != maxCommandOutputBytes || err == nil || len(buffer.bytes) != maxCommandOutputBytes {
		t.Fatalf("Write() = (%d, %v), buffer length = %d", written, err, len(buffer.bytes))
	}
}
