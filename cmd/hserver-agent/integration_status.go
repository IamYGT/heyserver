package main

import (
	"context"
	"errors"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/managedintegrationstatus"
)

const (
	managedIntegrationProbeTimeout     = 5 * time.Second
	managedIntegrationAggregateTimeout = 10 * time.Second
)

type managedIntegrationProbeResult struct {
	result   managedintegrationstatus.ManagedIntegrationStatusResult
	timedOut bool
}

// executeManagedIntegrationStatus runs the two fixed read-only probes in
// parallel. Probe functions receive independent five-second child contexts;
// the aggregate is bounded to ten seconds and the buffered result channel
// keeps cancellation from leaving a sender blocked.
func (e taskExecutor) executeManagedIntegrationStatus(ctx context.Context, task *agenthub.Task) agenthub.TaskResultRequest {
	if task == nil || len(task.Payload) != 0 || task.NodeID == "" {
		return failedManagedIntegrationStatus()
	}

	aggregateCtx, cancel := context.WithTimeout(ctx, managedIntegrationAggregateTimeout)
	defer cancel()

	type probeSpec struct {
		id     string
		probe  string
		invoke managedIntegrationProbe
	}
	probes := [...]probeSpec{
		{id: managedintegrationstatus.ProcessPM2ID, probe: managedintegrationstatus.PM2InventoryProbe, invoke: e.integrationPM2},
		{id: managedintegrationstatus.DockerID, probe: managedintegrationstatus.DockerInfoProbe, invoke: e.integrationDocker},
	}
	results := make(chan managedIntegrationProbeResult, len(probes))
	for _, spec := range probes {
		go func(spec probeSpec) {
			started := time.Now()
			state, err, timedOut := invokeManagedIntegrationProbe(aggregateCtx, spec.invoke)
			duration := time.Since(started).Milliseconds()
			if duration < 0 {
				duration = 0
			}
			results <- managedIntegrationProbeResult{
				result:   normalizeManagedIntegrationResult(spec.id, spec.probe, state, err, timedOut, duration),
				timedOut: timedOut,
			}
		}(spec)
	}

	collected := make([]managedintegrationstatus.ManagedIntegrationStatusResult, len(probes))
	got := 0
	for got < len(probes) {
		select {
		case item := <-results:
			index := 0
			if item.result.ID == managedintegrationstatus.DockerID {
				index = 1
			}
			collected[index] = item.result
			got++
		case <-aggregateCtx.Done():
			// Any probe that has not reported by the aggregate deadline is a
			// bounded item-level timeout, not a task-level error.
			for i := range collected {
				if collected[i].ID == "" {
					collected[i] = normalizeManagedIntegrationResult(probes[i].id, probes[i].probe, integrationstate.Unavailable, aggregateCtx.Err(), true, managedIntegrationAggregateTimeout.Milliseconds())
				}
			}
			break
		}
		if aggregateCtx.Err() != nil {
			break
		}
	}
	for i := range collected {
		if collected[i].ID == "" {
			collected[i] = normalizeManagedIntegrationResult(probes[i].id, probes[i].probe, integrationstate.Unavailable, aggregateCtx.Err(), true, managedIntegrationAggregateTimeout.Milliseconds())
		}
	}

	partial := false
	for _, result := range collected {
		if result.State != integrationstate.Healthy {
			partial = true
			break
		}
	}
	response := managedintegrationstatus.ManagedIntegrationStatusResponse{
		SchemaVersion: managedintegrationstatus.SchemaVersion,
		ObservedAt:    time.Now().UTC(),
		Target: managedintegrationstatus.ManagedIntegrationStatusTarget{
			Scope:  managedintegrationstatus.ScopeManagedNode,
			NodeID: task.NodeID,
		},
		Results: collected,
		Partial: partial,
	}
	data, err := response.Marshal()
	if err != nil {
		return failedManagedIntegrationStatus()
	}
	return agenthub.TaskResultRequest{
		Status: agenthub.TaskStatusCompleted,
		Result: map[string]string{"data": string(data)},
	}
}

func invokeManagedIntegrationProbe(parent context.Context, probe managedIntegrationProbe) (integrationstate.State, error, bool) {
	probeCtx, cancel := context.WithTimeout(parent, managedIntegrationProbeTimeout)
	defer cancel()
	if probe == nil {
		return integrationstate.NotConfigured, errors.New("probe is not configured"), false
	}
	state, err := probe(probeCtx)
	if probeCtx.Err() != nil {
		return integrationstate.Unavailable, probeCtx.Err(), true
	}
	return state, err, false
}

func normalizeManagedIntegrationResult(id, probe string, state integrationstate.State, err error, timedOut bool, durationMS int64) managedintegrationstatus.ManagedIntegrationStatusResult {
	result := managedintegrationstatus.ManagedIntegrationStatusResult{
		ID: id, State: state, Probe: probe, DurationMS: durationMS,
	}
	if timedOut {
		result.State = integrationstate.Unavailable
		result.ErrorCode = managedintegrationstatus.ErrorCodeTimeout
		return result
	}
	if state == integrationstate.NotConfigured {
		result.ErrorCode = managedintegrationstatus.ErrorCodeNotConfigured
		return result
	}
	if state == integrationstate.Healthy && err == nil {
		return result
	}
	result.State = integrationstate.Unavailable
	result.ErrorCode = managedintegrationstatus.ErrorCodeProbeFailed
	return result
}

func failedManagedIntegrationStatus() agenthub.TaskResultRequest {
	return agenthub.TaskResultRequest{
		Status: agenthub.TaskStatusFailed,
		Error:  managedintegrationstatus.FatalErrorCode,
	}
}
