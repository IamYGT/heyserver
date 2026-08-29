package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

// handleNodeProfileGet exposes desired profile state and the latest raw node
// observation. It never asks the agent to execute anything.
func handleNodeProfileGet(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response, err := hub.GetNodeProfile(r.PathValue("id"))
		if err != nil {
			writeNodeProfileError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, response)
	}
}

// handleNodeProfilePut persists only the panel-owned desired profile. The
// apply boundary remains explicit in the response; saving desired state never
// enqueues an agent task.
func handleNodeProfilePut(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var request agenthub.AgentProfilePutRequest
		if err := decodeStrictJSON(r, &request); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		response, err := hub.UpdateNodeProfile(r.PathValue("id"), request.Profile, request.ExpectedRevision)
		if err != nil {
			writeNodeProfileError(w, err)
			return
		}
		auditHostAction(r, "remote_agent_profile_update", r.PathValue("id")+": revision "+strconv.FormatInt(response.Desired.Revision, 10))
		jsonResponse(w, http.StatusOK, response)
	}
}

// handleNodeProfileApply queues only the panel-owned desired revision. The
// strict request has no profile or task payload field; the service reads and
// validates the desired profile before creating the fixed agent task.
func handleNodeProfileApply(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		var request agenthub.AgentProfileApplyRequest
		if err := decodeStrictJSON(r, &request); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		nodeID := r.PathValue("id")
		_, err := hub.ApplyNodeProfile(nodeID, request.ExpectedRevision)
		if err != nil {
			writeNodeProfileError(w, err)
			return
		}
		response, err := hub.GetNodeProfile(nodeID)
		if err != nil {
			writeNodeProfileError(w, err)
			return
		}
		status := http.StatusAccepted
		if response.Apply.State == "applied" {
			status = http.StatusOK
		}
		auditHostAction(r, "remote_agent_profile_apply", nodeID+": revision "+strconv.FormatInt(request.ExpectedRevision, 10))
		jsonResponse(w, status, response)
	}
}

func writeNodeProfileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agenthub.ErrNotFound):
		jsonError(w, http.StatusNotFound, "not found")
	case errors.Is(err, agenthub.ErrProfileRevisionStale):
		jsonError(w, http.StatusConflict, "stale_profile_revision")
	case errors.Is(err, agenthub.ErrProfileNotConfigured):
		jsonError(w, http.StatusConflict, "profile_not_configured")
	case errors.Is(err, agenthub.ErrProfileApplyInFlight):
		jsonError(w, http.StatusConflict, "profile_apply_in_flight")
	case errors.Is(err, agenthub.ErrCapabilityUnavailable):
		jsonError(w, http.StatusConflict, "profile_apply_capability_unavailable")
	case errors.Is(err, agenthub.ErrNodeOffline):
		jsonError(w, http.StatusConflict, "node_offline")
	case errors.Is(err, agenthub.ErrInvalidInput):
		jsonError(w, http.StatusBadRequest, "invalid profile")
	default:
		jsonError(w, http.StatusInternalServerError, "agent profile operation failed")
	}
}
