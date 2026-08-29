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

func TestRemoteDatabasesRunThroughAgentCapabilities(t *testing.T) {
	const nodeID = "database-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Database Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test", Capabilities: []string{agenthub.CapabilityInventory, agenthub.CapabilityDatabaseRead, agenthub.CapabilityDatabaseAction}, Hostname: "database.example", SentAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	listDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDatabaseInventory, func(*agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"id":"mariadb","name":"MariaDB","version":"11.4","unit":"mariadb.service","active":"active","data_size":4096,"databases":[{"name":"app","size":4096,"connections":2,"objects":12}],"sessions":[]}]`}}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/databases", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeDatabases(hub).ServeHTTP(response, request)
	if err := <-listDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"mariadb"`) {
		t.Fatalf("list=%d %s", response.Code, response.Body.String())
	}

	actionDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDatabaseAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["engine"] != "mariadb" || task.Payload["action"] != "restart" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "MariaDB restarted and socket health check passed"}}, nil
	})
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/databases/mariadb/actions/restart", nil)
	request.SetPathValue("id", nodeID)
	request.SetPathValue("engine", "mariadb")
	request.SetPathValue("action", "restart")
	response = httptest.NewRecorder()
	handleRemoteNodeDatabaseAction(hub).ServeHTTP(response, request)
	if err := <-actionDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "socket health check passed") {
		t.Fatalf("action=%d %s", response.Code, response.Body.String())
	}
}
