package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/managedintegrationstatus"
)

const managedIntegrationStatusWait = 45 * time.Second

// handleRemoteNodeIntegrationStatus exposes one fresh, read-only managed-node
// observation. The service performs capability/online checks and coalesces
// concurrent requests before this handler waits for the existing task.
func handleRemoteNodeIntegrationStatus(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueIntegrationStatusTask(nodeID)
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

		waitCtx, cancel := context.WithTimeout(r.Context(), managedIntegrationStatusWait)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				jsonError(w, http.StatusGatewayTimeout, "managed_status_timeout")
			} else {
				jsonError(w, http.StatusBadGateway, "managed_status_failed")
			}
			return
		}
		if task == nil || task.Status != agenthub.TaskStatusCompleted {
			jsonError(w, http.StatusBadGateway, "managed_status_failed")
			return
		}
		data, ok := task.Result["data"]
		if !ok {
			jsonError(w, http.StatusBadGateway, "managed_status_failed")
			return
		}
		status, err := managedintegrationstatus.Decode([]byte(data))
		if err != nil || status.Target.NodeID != nodeID {
			jsonError(w, http.StatusBadGateway, "managed_status_failed")
			return
		}
		jsonResponse(w, http.StatusOK, status)
	}
}
