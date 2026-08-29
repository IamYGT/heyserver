package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestJournalReaderUsesFixedAllowlistedUnitArguments(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{[]byte(
		`{"__REALTIME_TIMESTAMP":"1720000000000000","_SYSTEMD_UNIT":"php8.3-fpm.service","PRIORITY":"3","MESSAGE":"worker failed"}` + "\n",
	)}}
	reader := newJournalReader(runner, map[string]struct{}{"php": {}})
	entries, err := reader.Read(context.Background(), "php", 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	wantArgs := []string{"--no-pager", "-o", "json", "--lines=25", "-u", "php*-fpm.service"}
	if len(runner.commands) != 1 || runner.commands[0].name != "journalctl" || !reflect.DeepEqual(runner.commands[0].args, wantArgs) {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if len(entries) != 1 || entries[0].Unit != "php8.3-fpm.service" || entries[0].Priority != 3 || entries[0].Message != "worker failed" || !strings.HasPrefix(entries[0].Timestamp, "2024-") {
		t.Fatalf("entries = %#v", entries)
	}
	if _, err := reader.Read(context.Background(), "system", 25); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("system read error = %v", err)
	}
}

func TestTaskExecutorReturnsStructuredJournalEntries(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{[]byte(`{"SYSLOG_IDENTIFIER":"kernel","PRIORITY":"6","MESSAGE":"ready"}` + "\n")}}
	reader := newJournalReader(runner, map[string]struct{}{"system": {}})
	executor := newTaskExecutor(serviceController{}, nil, nil, reader, nil, nil, nil, false)
	result := executor.execute(context.Background(), &agenthub.Task{
		Kind: agenthub.TaskLogsRead, Payload: map[string]string{"source": "system", "lines": "200"},
	})
	if result.Status != agenthub.TaskStatusCompleted || !strings.Contains(result.Result["data"], `"unit":"kernel"`) || !strings.Contains(result.Result["data"], `"message":"ready"`) {
		t.Fatalf("result = %#v", result)
	}
}
