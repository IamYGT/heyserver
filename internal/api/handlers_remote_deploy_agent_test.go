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

func TestRemoteDeployUsesConfiguredAgentTasksAndCentralJobHistory(t *testing.T) {
	const nodeID = "deploy-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Deploy Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test", Capabilities: []string{agenthub.CapabilityInventory, agenthub.CapabilityDeployRead, agenthub.CapabilityDeployAction}, Hostname: "deploy.example", SentAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	inventoryDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDeployInventory, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if len(task.Payload) != 0 {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"id":"example-app","name":"Example app","description":"Portable plan","kind":"application","path":"/srv/example","status":"ready","eligible":true,"actions":["preflight","deploy"]}]`}}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/deploy", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeDeployTargets(hub).ServeHTTP(response, request)
	if err := <-inventoryDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"example-app"`) || !strings.Contains(response.Body.String(), `"deploy"`) {
		t.Fatalf("inventory=%d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/deploy/example-app/actions/deploy", nil)
	request.SetPathValue("id", nodeID)
	request.SetPathValue("target", "example-app")
	request.SetPathValue("action", "deploy")
	response = httptest.NewRecorder()
	handleRemoteNodeDeployAction(hub).ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"queued"`) {
		t.Fatalf("action=%d %s", response.Code, response.Body.String())
	}
	claimed, err := hub.PollTask(nodeID, registered.Token)
	if err != nil || claimed == nil || claimed.Kind != agenthub.TaskDeployAction || claimed.Payload["target"] != "example-app" {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if _, err := hub.CompleteTask(nodeID, registered.Token, claimed.ID, agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Example app deploy completed", "output": "release complete"}}); err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/deploy/jobs", nil)
	request.SetPathValue("id", nodeID)
	response = httptest.NewRecorder()
	handleRemoteNodeDeployJobs(hub).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"completed"`) || !strings.Contains(response.Body.String(), "release complete") {
		t.Fatalf("jobs=%d %s", response.Code, response.Body.String())
	}
}
