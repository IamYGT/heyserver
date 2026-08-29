package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestContainerControllerUsesFixedDockerArguments(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{
		[]byte(`{"ID":"abc123","Names":"web-1","Image":"nginx:stable","State":"running","Status":"Up 2 hours","Ports":"80/tcp"}` + "\n"),
		nil,
	}}
	controller := newContainerController(runner, true, map[string]struct{}{"restart": {}})
	containers, err := controller.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(containers) != 1 || containers[0].Name != "web-1" || containers[0].Image != "nginx:stable" {
		t.Fatalf("containers = %#v", containers)
	}
	wantList := recordedCommand{name: "docker", args: []string{"ps", "-a", "--no-trunc", "--format", "{{json .}}"}}
	if !reflect.DeepEqual(runner.commands[0], wantList) {
		t.Fatalf("list command = %#v", runner.commands[0])
	}
	message, err := controller.Action(context.Background(), "web-1", "restart")
	if err != nil || !strings.Contains(message, "restart") {
		t.Fatalf("Action = (%q, %v)", message, err)
	}
	wantAction := recordedCommand{name: "docker", args: []string{"restart", "--", "web-1"}}
	if !reflect.DeepEqual(runner.commands[1], wantAction) {
		t.Fatalf("action command = %#v", runner.commands[1])
	}
	if _, err := controller.Action(context.Background(), "web-1", "stop"); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("blocked action error = %v", err)
	}
	if _, err := controller.Action(context.Background(), "web;id", "restart"); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("invalid identity error = %v", err)
	}
}

func TestTaskExecutorReturnsStructuredContainerInventory(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{[]byte(`{"ID":"abc123","Names":"api","Image":"app:1","State":"exited","Status":"Exited (0)"}` + "\n")}}
	executor := newTaskExecutor(serviceController{}, nil, nil, nil, nil, nil, nil, false)
	executor.containers = newContainerController(runner, true, nil)
	result := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskContainerList, Payload: map[string]string{}})
	if result.Status != agenthub.TaskStatusCompleted || !strings.Contains(result.Result["data"], `"name":"api"`) {
		t.Fatalf("result = %#v", result)
	}
}
