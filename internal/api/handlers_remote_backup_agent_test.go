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

func TestRemoteBackupsRunThroughAgentCapabilities(t *testing.T) {
	const nodeID = "backup-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Backup Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test", Capabilities: []string{agenthub.CapabilityInventory, agenthub.CapabilityBackupRead, agenthub.CapabilityBackupRun}, Hostname: "backup.example", SentAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	listDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskBackupInventory, func(*agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"id":"database-export","name":"All databases","service":"hserver-database-backup.service","timer":"hserver-database-backup.timer","active":"active","enabled":"enabled","last_result":"success","last_run":"yesterday","next_run":"tomorrow","verified":true,"total_size":4096,"files":[]}]`}}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/backups", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeBackups(hub).ServeHTTP(response, request)
	if err := <-listDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"database-export"`) {
		t.Fatalf("list=%d %s", response.Code, response.Body.String())
	}

	runDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskBackupRun, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["plan"] != "database-export" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "All databases backup completed"}}, nil
	})
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/backups/database-export/run", nil)
	request.SetPathValue("id", nodeID)
	request.SetPathValue("plan", "database-export")
	response = httptest.NewRecorder()
	handleRemoteNodeBackupRun(hub).ServeHTTP(response, request)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "backup completed") {
		t.Fatalf("run=%d %s", response.Code, response.Body.String())
	}
}
