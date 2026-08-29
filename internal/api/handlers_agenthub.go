package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/config"
)

const agentHubIdentityHeader = "X-HServer-Node-ID"

func handleAgentHeartbeat(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID, token, ok := agentCredentials(r)
		if !ok {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var req agenthub.HeartbeatRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.NodeID != nodeID {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		response, err := hub.Heartbeat(nodeID, token, req)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, response)
	}
}

func handleAgentTaskPoll(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID, token, ok := agentCredentials(r)
		if !ok {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		task, err := hub.PollTask(nodeID, token)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, agenthub.TaskPollResponse{Task: task})
	}
}

func handleAgentTaskResult(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		nodeID, token, ok := agentCredentials(r)
		if !ok {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || taskID <= 0 {
			jsonError(w, http.StatusBadRequest, "invalid task id")
			return
		}
		var req agenthub.TaskResultRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		task, err := hub.CompleteTask(nodeID, token, taskID, req)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, task)
	}
}

func handleNodeRegister(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req agenthub.RegisterNodeRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		response, err := hub.RegisterNode(req)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jsonResponse(w, http.StatusCreated, response)
	}
}

func handleNodeList(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		nodes, err := hub.ListNodes()
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		response := make([]managedNodeResponse, 0, len(nodes))
		for _, node := range nodes {
			response = append(response, newManagedNodeResponse(node, config.Version, hub.IsNodeOnline(node)))
		}
		jsonResponse(w, http.StatusOK, response)
	}
}

func handleNodeGet(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node, err := hub.GetNode(r.PathValue("id"))
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, newManagedNodeResponse(*node, config.Version, hub.IsNodeOnline(*node)))
	}
}

type managedNodeResponse struct {
	agenthub.Node
	Compatibility agenthub.NodeCompatibility `json:"compatibility"`
	Online        bool                       `json:"online"`
}

// remoteTaskCreateRequest is deliberately separate from agenthub.TaskRequest:
// the confirmation is a panel HTTP boundary, not part of the task payload
// persisted for or delivered to the agent.
type remoteTaskCreateRequest struct {
	Kind      string            `json:"kind"`
	Payload   map[string]string `json:"payload"`
	Confirmed bool              `json:"confirmed"`
}

func newManagedNodeResponse(node agenthub.Node, panelVersion string, online bool) managedNodeResponse {
	return managedNodeResponse{
		Node:          node,
		Compatibility: agenthub.Compatibility(node.AgentVersion, node.ProtocolVersion, panelVersion),
		Online:        online,
	}
}

func handleNodeTaskCreate(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req remoteTaskCreateRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if !req.Confirmed {
			jsonError(w, http.StatusBadRequest, "explicit remote task confirmation (confirmed:true) is required")
			return
		}
		task, err := hub.EnqueueTask(r.PathValue("id"), agenthub.TaskRequest{Kind: req.Kind, Payload: req.Payload})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jsonResponse(w, http.StatusCreated, task)
	}
}

func handleNodeTaskList(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				jsonError(w, http.StatusBadRequest, "invalid task limit")
				return
			}
			limit = parsed
		}
		tasks, err := hub.ListTasksForNode(r.PathValue("id"), limit)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, tasks)
	}
}

func handleNodeTaskGet(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID, err := strconv.ParseInt(r.PathValue("taskID"), 10, 64)
		if err != nil || taskID <= 0 {
			jsonError(w, http.StatusBadRequest, "invalid task id")
			return
		}
		task, err := hub.GetTaskForNode(r.PathValue("id"), taskID)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, task)
	}
}

func agentCredentials(r *http.Request) (string, string, bool) {
	nodeID := strings.TrimSpace(r.Header.Get(agentHubIdentityHeader))
	authHeader := r.Header.Get("Authorization")
	if nodeID == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		return "", "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	return nodeID, token, token != ""
}

func writeAgentHubError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agenthub.ErrUnauthorized):
		jsonError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, agenthub.ErrNotFound):
		jsonError(w, http.StatusNotFound, "not found")
	case errors.Is(err, agenthub.ErrAlreadyExists):
		jsonError(w, http.StatusConflict, "node already exists")
	case errors.Is(err, agenthub.ErrCapabilityUnavailable):
		jsonError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agenthub.ErrNodeOffline):
		jsonError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agenthub.ErrInvalidInput),
		errors.Is(err, agenthub.ErrUnsupportedProtocol),
		errors.Is(err, agenthub.ErrStaleHeartbeat),
		errors.Is(err, agenthub.ErrTaskNotClaimed):
		jsonError(w, http.StatusBadRequest, err.Error())
	default:
		jsonError(w, http.StatusInternalServerError, "agent hub operation failed")
	}
}
