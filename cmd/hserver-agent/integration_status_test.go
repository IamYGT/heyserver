package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/managedintegrationstatus"
)

func TestManagedIntegrationStatusRunsTwoProbesInParallelAndUsesCanonicalStates(t *testing.T) {
	var mu sync.Mutex
	active, maxActive := 0, 0
	probe := func(state integrationstate.State) managedIntegrationProbe {
		return func(context.Context) (integrationstate.State, error) {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			time.Sleep(30 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			return state, nil
		}
	}
	executor := newTaskExecutor(serviceController{}, nil, nil, nil, nil, nil, nil, false)
	executor.integrationPM2 = probe(integrationstate.Healthy)
	executor.integrationDocker = probe(integrationstate.NotConfigured)
	result := executor.execute(context.Background(), &agenthub.Task{NodeID: "node-1", Kind: agenthub.TaskIntegrationStatus, Payload: map[string]string{}})
	if result.Status != agenthub.TaskStatusCompleted || result.Error != "" {
		t.Fatalf("result = %#v", result)
	}
	if maxActive != 2 {
		t.Fatalf("maximum concurrent probes = %d, want 2", maxActive)
	}
	status, err := managedintegrationstatus.Decode([]byte(result.Result["data"]))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if status.Target.Scope != managedintegrationstatus.ScopeManagedNode || status.Target.NodeID != "node-1" || !status.Partial {
		t.Fatalf("status target/partial = %#v", status)
	}
	if status.Results[0].ID != managedintegrationstatus.ProcessPM2ID || status.Results[0].Probe != managedintegrationstatus.PM2InventoryProbe || status.Results[0].State != integrationstate.Healthy {
		t.Fatalf("PM2 result = %#v", status.Results[0])
	}
	if status.Results[1].ID != managedintegrationstatus.DockerID || status.Results[1].Probe != managedintegrationstatus.DockerInfoProbe || status.Results[1].State != integrationstate.NotConfigured || status.Results[1].ErrorCode != managedintegrationstatus.ErrorCodeNotConfigured {
		t.Fatalf("Docker result = %#v", status.Results[1])
	}
}

func TestManagedIntegrationStatusMapsAggregateCancellationToSafeTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	executor := newTaskExecutor(serviceController{}, nil, nil, nil, nil, nil, nil, false)
	executor.integrationPM2 = func(ctx context.Context) (integrationstate.State, error) {
		<-ctx.Done()
		return integrationstate.Unavailable, ctx.Err()
	}
	executor.integrationDocker = func(context.Context) (integrationstate.State, error) {
		return integrationstate.Healthy, nil
	}
	result := executor.execute(ctx, &agenthub.Task{NodeID: "node-1", Kind: agenthub.TaskIntegrationStatus})
	if result.Status != agenthub.TaskStatusCompleted {
		t.Fatalf("result = %#v", result)
	}
	status, err := managedintegrationstatus.Decode([]byte(result.Result["data"]))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if status.Results[0].State != integrationstate.Unavailable || status.Results[0].ErrorCode != managedintegrationstatus.ErrorCodeTimeout {
		t.Fatalf("PM2 timeout result = %#v", status.Results[0])
	}
}

func TestManagedIntegrationStatusRejectsMalformedTaskWithoutRawError(t *testing.T) {
	executor := newTaskExecutor(serviceController{}, nil, nil, nil, nil, nil, nil, false)
	for _, task := range []*agenthub.Task{
		{Kind: agenthub.TaskIntegrationStatus, NodeID: "node-1", Payload: map[string]string{"unexpected": "value"}},
		{Kind: agenthub.TaskIntegrationStatus, Payload: map[string]string{}},
	} {
		result := executor.execute(context.Background(), task)
		if result.Status != agenthub.TaskStatusFailed || result.Error != managedintegrationstatus.FatalErrorCode || len(result.Result) != 0 {
			t.Fatalf("malformed task result = %#v", result)
		}
	}
}

func TestPM2AndDockerStatusProbesExposeOnlyCanonicalState(t *testing.T) {
	pm2 := newPM2Controller(&fakeRunner{outputs: [][]byte{[]byte(`[]`)}}, true, nil, preparePM2Binary(t), "/root/.pm2", "root")
	state, err := pm2.Probe(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("PM2 Probe = (%q, %v)", state, err)
	}
	docker := newContainerController(&fakeRunner{outputs: [][]byte{nil}}, true, nil)
	state, err = docker.Probe(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("Docker Probe = (%q, %v)", state, err)
	}
	docker = newContainerController(&fakeRunner{}, false, nil)
	state, err = docker.Probe(context.Background())
	if err == nil || state != integrationstate.NotConfigured {
		t.Fatalf("disabled Docker Probe = (%q, %v)", state, err)
	}
}
