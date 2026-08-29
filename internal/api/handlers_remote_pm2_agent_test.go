package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
)

func TestRemotePM2RunsThroughAgentCapabilities(t *testing.T) {
	const nodeID = "pm2-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "PM2 Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityPM2Read, agenthub.CapabilityPM2Action},
		Hostname:        "pm2.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	listDone := completeNextPM2Task(t, hub, nodeID, registered.Token, agenthub.TaskPM2List, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"id":1,"name":"api","status":"online","pid":42,"cpu":1.5,"memory":1048576,"uptime":1787700000000,"restarts":2,"mode":"fork_mode","cwd":"/srv/api","script":"/srv/api/server.js","version":"1.0.0"}]`}}, nil
	})
	listRequest := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/pm2", nil)
	listRequest.SetPathValue("id", nodeID)
	listResponse := httptest.NewRecorder()
	handleRemoteNodePM2(hub).ServeHTTP(listResponse, listRequest)
	if err := <-listDone; err != nil {
		t.Fatalf("list agent: %v", err)
	}
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"name":"api"`) {
		t.Fatalf("list status = %d, body=%s", listResponse.Code, listResponse.Body.String())
	}

	logsDone := completeNextPM2Task(t, hub, nodeID, registered.Token, agenthub.TaskPM2Logs, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["name"] != "api" || task.Payload["lines"] != "200" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `"line one\nline two\n"`}}, nil
	})
	logsRequest := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/pm2/api/logs?lines=200", nil)
	logsRequest.SetPathValue("id", nodeID)
	logsRequest.SetPathValue("name", "api")
	logsResponse := httptest.NewRecorder()
	handleRemoteNodePM2Logs(hub).ServeHTTP(logsResponse, logsRequest)
	if err := <-logsDone; err != nil {
		t.Fatalf("logs agent: %v", err)
	}
	if logsResponse.Code != http.StatusOK || !strings.Contains(logsResponse.Body.String(), `"logs":"line one\nline two\n"`) {
		t.Fatalf("logs status = %d, body=%s", logsResponse.Code, logsResponse.Body.String())
	}

	actionDone := completeNextPM2Task(t, hub, nodeID, registered.Token, agenthub.TaskPM2Action, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["name"] != "api" || task.Payload["action"] != "restart" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "PM2 process restart completed and process list saved"}}, nil
	})
	actionRequest := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/pm2/api/actions/restart", nil)
	actionRequest.SetPathValue("id", nodeID)
	actionRequest.SetPathValue("name", "api")
	actionRequest.SetPathValue("action", "restart")
	actionResponse := httptest.NewRecorder()
	handleRemoteNodePM2Action(hub).ServeHTTP(actionResponse, actionRequest)
	if err := <-actionDone; err != nil {
		t.Fatalf("action agent: %v", err)
	}
	if actionResponse.Code != http.StatusOK || !strings.Contains(actionResponse.Body.String(), `"message":"PM2 process restart completed and process list saved"`) {
		t.Fatalf("action status = %d, body=%s", actionResponse.Code, actionResponse.Body.String())
	}
}

func completeNextPM2Task(t *testing.T, hub *agenthub.Service, nodeID, token, kind string, result func(*agenthub.Task) (agenthub.TaskResultRequest, error)) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			task, err := hub.PollTask(nodeID, token)
			if err != nil {
				done <- err
				return
			}
			if task == nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if task.Kind != kind {
				done <- context.Canceled
				return
			}
			response, err := result(task)
			if err == nil {
				_, err = hub.CompleteTask(nodeID, token, task.ID, response)
			}
			done <- err
			return
		}
		done <- context.DeadlineExceeded
	}()
	return done
}
