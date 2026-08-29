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

func TestRemoteDomainsRunThroughAgentCapabilities(t *testing.T) {
	const nodeID = "domain-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Domain Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test", Capabilities: []string{agenthub.CapabilityInventory, agenthub.CapabilityDomainRead, agenthub.CapabilityDomainAction}, Hostname: "domain.example", SentAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	listDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDomainInventory, func(*agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"name":"example.com","aliases":["example.com"],"config":"example.conf","enabled":true,"ssl":false,"kind":"proxy"}]`}}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/domains", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeDomains(hub).ServeHTTP(response, request)
	if err := <-listDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"config":"example.conf"`) {
		t.Fatalf("list=%d %s", response.Code, response.Body.String())
	}

	actionDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDomainAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["config"] != "example.conf" || task.Payload["action"] != "disable" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Domain disabled and Nginx reloaded"}}, nil
	})
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/domains/example.conf/actions/disable", nil)
	request.SetPathValue("id", nodeID)
	request.SetPathValue("config", "example.conf")
	request.SetPathValue("action", "disable")
	response = httptest.NewRecorder()
	handleRemoteNodeDomainAction(hub).ServeHTTP(response, request)
	if err := <-actionDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "disabled and Nginx reloaded") {
		t.Fatalf("action=%d %s", response.Code, response.Body.String())
	}
}
