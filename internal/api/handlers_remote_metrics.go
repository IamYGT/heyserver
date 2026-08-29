package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

const managedMetricsWait = 45 * time.Second

// handleRemoteNodeMetrics returns one fresh typed metrics snapshot. The task
// kind and empty payload are fixed by the agent-hub service.
func handleRemoteNodeMetrics(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueMetricsTask(nodeID)
		if err != nil {
			switch {
			case errors.Is(err, agenthub.ErrCapabilityUnavailable):
				jsonError(w, http.StatusConflict, "capability_unavailable")
			case errors.Is(err, agenthub.ErrNodeOffline):
				jsonError(w, http.StatusConflict, "managed_node_offline")
			default:
				writeAgentHubError(w, err)
			}
			return
		}

		waitCtx, cancel := context.WithTimeout(r.Context(), managedMetricsWait)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				jsonError(w, http.StatusGatewayTimeout, "managed_metrics_timeout")
			} else {
				jsonError(w, http.StatusBadGateway, "managed_metrics_failed")
			}
			return
		}
		if task == nil || task.Status != agenthub.TaskStatusCompleted || len(task.Result) != 1 {
			jsonError(w, http.StatusBadGateway, "managed_metrics_failed")
			return
		}
		data, ok := task.Result["data"]
		if !ok {
			jsonError(w, http.StatusBadGateway, "managed_metrics_failed")
			return
		}
		snapshot, err := agenthub.DecodeMetricsSnapshot([]byte(data))
		if err != nil || agenthub.ValidateMetricsSnapshot(snapshot, time.Now().UTC()) != nil {
			jsonError(w, http.StatusBadGateway, "managed_metrics_failed")
			return
		}
		jsonResponse(w, http.StatusOK, snapshot)
	}
}
