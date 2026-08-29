package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
)

func TestRemoteFirewallRunsThroughAgentCapabilities(t *testing.T) {
	const nodeID = "firewall-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Firewall Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test", Capabilities: []string{agenthub.CapabilityInventory, agenthub.CapabilityFirewallRead, agenthub.CapabilityFirewallWrite}, Hostname: "firewall.example", SentAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	listDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskFirewallInventory, func(*agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `{"backend":"iptables","policy":"DROP","persistence":"active","rules":[],"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","protected_sources":["192.0.2.10/32"],"protected_ports":[22]}`}}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/firewall", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeFirewallList(hub).ServeHTTP(response, request)
	if err := <-listDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"protected_ports":[22]`) {
		t.Fatalf("list=%d %s", response.Code, response.Body.String())
	}

	addDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskFirewallAdd, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		raw, err := base64.StdEncoding.DecodeString(task.Payload["rule_b64"])
		if err != nil {
			return agenthub.TaskResultRequest{}, err
		}
		var rule map[string]any
		if json.Unmarshal(raw, &rule) != nil || rule["action"] != "ACCEPT" || rule["source"] != "203.0.113.0/24" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		if _, exists := rule["revision"]; exists {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"id": "fw-0123456789ab", "message": "Firewall rule added and persisted"}}, nil
	})
	body := `{"action":"ACCEPT","protocol":"tcp","port":443,"source":"203.0.113.0/24","comment":"web","revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/firewall", bytes.NewBufferString(body))
	request.SetPathValue("id", nodeID)
	response = httptest.NewRecorder()
	handleRemoteNodeFirewallAdd(hub).ServeHTTP(response, request)
	if err := <-addDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"id":"fw-0123456789ab"`) {
		t.Fatalf("add=%d %s", response.Code, response.Body.String())
	}

	deleteDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskFirewallDelete, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["id"] != "fw-0123456789ab" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Firewall rule deleted and persisted"}}, nil
	})
	request = httptest.NewRequest(http.MethodDelete, "/api/nodes/"+nodeID+"/firewall/fw-0123456789ab", bytes.NewBufferString(`{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	request.SetPathValue("id", nodeID)
	request.SetPathValue("rule", "fw-0123456789ab")
	response = httptest.NewRecorder()
	handleRemoteNodeFirewallDelete(hub).ServeHTTP(response, request)
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "deleted and persisted") {
		t.Fatalf("delete=%d %s", response.Code, response.Body.String())
	}
}
