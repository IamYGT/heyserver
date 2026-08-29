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

func TestRemoteCronRunsThroughAgentCapabilities(t *testing.T) {
	const nodeID = "cron-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Cron Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test", Capabilities: []string{agenthub.CapabilityInventory, agenthub.CapabilityCronRead, agenthub.CapabilityCronWrite, agenthub.CapabilityCronRun}, Hostname: "cron.example", SentAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	listDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskCronInventory, func(*agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `{"service":"active","jobs":[],"sources":[],"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`}}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/cron", nil)
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleRemoteNodeCronList(hub).ServeHTTP(response, request)
	if err := <-listDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"service":"active"`) {
		t.Fatalf("list=%d %s", response.Code, response.Body.String())
	}

	createDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskCronCreate, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		raw, err := base64.StdEncoding.DecodeString(task.Payload["job_b64"])
		if err != nil {
			return agenthub.TaskResultRequest{}, err
		}
		var job map[string]any
		if json.Unmarshal(raw, &job) != nil || job["command"] != "/usr/bin/true" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"id": "cron-0123456789ab", "message": "Cron job created and validated"}}, nil
	})
	body := `{"schedule":"0 * * * *","user":"root","command":"/usr/bin/true","enabled":true,"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/cron", bytes.NewBufferString(body))
	request.SetPathValue("id", nodeID)
	response = httptest.NewRecorder()
	handleRemoteNodeCronCreate(hub).ServeHTTP(response, request)
	if err := <-createDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"id":"cron-0123456789ab"`) {
		t.Fatalf("create=%d %s", response.Code, response.Body.String())
	}

	runDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskCronRun, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["id"] != "cron-0123456789ab" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Cron job completed", "data": `"done\n"`}}, nil
	})
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/cron/cron-0123456789ab/run", nil)
	request.SetPathValue("id", nodeID)
	request.SetPathValue("job", "cron-0123456789ab")
	response = httptest.NewRecorder()
	handleRemoteNodeCronRun(hub).ServeHTTP(response, request)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"output":"done\n"`) {
		t.Fatalf("run=%d %s", response.Code, response.Body.String())
	}
}

func completeNextCronTask(t *testing.T, hub *agenthub.Service, nodeID, token, kind string, result func(*agenthub.Task) (agenthub.TaskResultRequest, error)) <-chan error {
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
