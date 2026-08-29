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

func TestRemoteSSLRunsThroughAgentCapabilities(t *testing.T) {
	const nodeID = "ssl-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "SSL Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test", Capabilities: []string{agenthub.CapabilityInventory, agenthub.CapabilitySSLRead, agenthub.CapabilitySSLAction}, Hostname: "ssl.example", SentAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	listDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskSSLInventory, func(*agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"name":"example.com","domains":["example.com"],"issuer":"CN=Test CA","serial":"2A","not_before":"2026-08-01T00:00:00Z","not_after":"2026-11-01T00:00:00Z","days_remaining":66,"auto_renew":true}]`}}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/certificates", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeCertificates(hub).ServeHTTP(response, request)
	if err := <-listDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"example.com"`) {
		t.Fatalf("list=%d %s", response.Code, response.Body.String())
	}

	actionDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskSSLAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["name"] != "example.com" || task.Payload["action"] != "renew" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Certificate checked and renewed if due; Nginx reloaded"}}, nil
	})
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/certificates/example.com/actions/renew", nil)
	request.SetPathValue("id", nodeID)
	request.SetPathValue("name", "example.com")
	request.SetPathValue("action", "renew")
	response = httptest.NewRecorder()
	handleRemoteNodeCertificateAction(hub).ServeHTTP(response, request)
	if err := <-actionDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "renewed if due") {
		t.Fatalf("action=%d %s", response.Code, response.Body.String())
	}
}
