package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
)

func TestRemoteNginxConfigsRunThroughAgentReadAndWriteCapabilities(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: "nginx-config-agent", Name: "Nginx Config Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat("nginx-config-agent", registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          "nginx-config-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityNginxConfigRead, agenthub.CapabilityNginxConfigWrite},
		Hostname:        "nginx-config.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	listDone := completeNextNginxConfigTask(t, hub, registered.Token, agenthub.TaskNginxConfigList, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"name":"site.conf","enabled":true,"size":12,"modified_at":"2026-08-26T10:00:00Z"}]`}}, nil
	})
	listRequest := httptest.NewRequest(http.MethodGet, "/api/nodes/nginx-config-agent/nginx/configs", nil)
	listRequest.SetPathValue("id", "nginx-config-agent")
	listResponse := httptest.NewRecorder()
	handleRemoteNodeNginxConfigs(hub).ServeHTTP(listResponse, listRequest)
	if err := <-listDone; err != nil {
		t.Fatalf("list agent: %v", err)
	}
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"name":"site.conf"`) {
		t.Fatalf("list status = %d, body=%s", listResponse.Code, listResponse.Body.String())
	}

	readDone := completeNextNginxConfigTask(t, hub, registered.Token, agenthub.TaskNginxConfigRead, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["name"] != "site.conf" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `{"name":"site.conf","enabled":true,"size":12,"modified_at":"2026-08-26T10:00:00Z","content":"server {}\n","checksum":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`}}, nil
	})
	readRequest := httptest.NewRequest(http.MethodGet, "/api/nodes/nginx-config-agent/nginx/configs/site.conf", nil)
	readRequest.SetPathValue("id", "nginx-config-agent")
	readRequest.SetPathValue("name", "site.conf")
	readResponse := httptest.NewRecorder()
	handleRemoteNodeNginxConfigGet(hub).ServeHTTP(readResponse, readRequest)
	if err := <-readDone; err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if readResponse.Code != http.StatusOK || !strings.Contains(readResponse.Body.String(), `"content":"server {}\n"`) {
		t.Fatalf("read status = %d, body=%s", readResponse.Code, readResponse.Body.String())
	}

	writeDone := completeNextNginxConfigTask(t, hub, registered.Token, agenthub.TaskNginxConfigWrite, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		content, decodeErr := base64.StdEncoding.DecodeString(task.Payload["content_b64"])
		if decodeErr != nil || string(content) != "server { listen 8080; }\n" || task.Payload["reload"] != "true" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{
			"message": "Nginx configuration saved, tested and reloaded", "backup": "/etc/nginx/sites-available/site.conf.hserver-backup-test",
		}}, nil
	})
	body := `{"content":"server { listen 8080; }\n","checksum":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","reload":true}`
	writeRequest := httptest.NewRequest(http.MethodPut, "/api/nodes/nginx-config-agent/nginx/configs/site.conf", bytes.NewBufferString(body))
	writeRequest.SetPathValue("id", "nginx-config-agent")
	writeRequest.SetPathValue("name", "site.conf")
	writeResponse := httptest.NewRecorder()
	handleRemoteNodeNginxConfigSave(hub).ServeHTTP(writeResponse, writeRequest)
	if err := <-writeDone; err != nil {
		t.Fatalf("write agent: %v", err)
	}
	if writeResponse.Code != http.StatusOK || !strings.Contains(writeResponse.Body.String(), `"backup":"/etc/nginx/sites-available/site.conf.hserver-backup-test"`) {
		t.Fatalf("write status = %d, body=%s", writeResponse.Code, writeResponse.Body.String())
	}
}

func completeNextNginxConfigTask(t *testing.T, hub *agenthub.Service, token, kind string, result func(*agenthub.Task) (agenthub.TaskResultRequest, error)) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			task, err := hub.PollTask("nginx-config-agent", token)
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
				_, err = hub.CompleteTask("nginx-config-agent", token, task.ID, response)
			}
			done <- err
			return
		}
		done <- context.DeadlineExceeded
	}()
	return done
}
