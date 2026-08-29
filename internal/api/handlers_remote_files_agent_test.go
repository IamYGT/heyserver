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

func TestRemoteFilesRunThroughAgentCapabilities(t *testing.T) {
	const nodeID = "files-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Files Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test", Capabilities: []string{agenthub.CapabilityInventory, agenthub.CapabilityFilesRead, agenthub.CapabilityFilesWrite}, Hostname: "files.example", SentAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	browseDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskFilesBrowse, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["path"] != "/srv/apps" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"name":"app.conf","path":"/srv/apps/app.conf","type":"file","size":7,"mode":"-rw-r-----","modified_at":"2026-08-26T00:00:00Z"}]`}}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/files?path=/srv/apps", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeFiles(hub).ServeHTTP(response, request)
	if err := <-browseDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "app.conf") {
		t.Fatalf("browse=%d %s", response.Code, response.Body.String())
	}

	readDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskFilesRead, func(*agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `{"path":"/srv/apps/app.conf","content":"before\n","checksum":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":7,"mode":"-rw-r-----","modified_at":"2026-08-26T00:00:00Z"}`}}, nil
	})
	request = httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/file?path=/srv/apps/app.conf", nil)
	request.SetPathValue("id", nodeID)
	response = httptest.NewRecorder()
	handleRemoteNodeFileGet(hub).ServeHTTP(response, request)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"before\n"`) {
		t.Fatalf("read=%d %s", response.Code, response.Body.String())
	}

	writeDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskFilesWrite, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["path"] != "/srv/apps/app.conf" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "File saved", "backup": "/srv/apps/app.conf.hserver-backup-20260826T000000Z"}}, nil
	})
	body := `{"path":"/srv/apps/app.conf","content":"after\n","checksum":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	request = httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/file", strings.NewReader(body))
	request.SetPathValue("id", nodeID)
	response = httptest.NewRecorder()
	handleRemoteNodeFileSave(hub).ServeHTTP(response, request)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "hserver-backup") {
		t.Fatalf("write=%d %s", response.Code, response.Body.String())
	}
}
