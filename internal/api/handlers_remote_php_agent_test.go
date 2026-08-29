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

func TestRemotePHPFPMRunsThroughAgentCapabilities(t *testing.T) {
	const nodeID = "php-fpm-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "PHP-FPM Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "agent-test",
		Capabilities: []string{
			agenthub.CapabilityInventory,
			agenthub.CapabilityPHPRead,
			agenthub.CapabilityPHPWrite,
			agenthub.CapabilityPHPAction,
		},
		Hostname: "php-fpm.example",
		SentAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	inventoryDone := completeNextPHPTask(t, hub, nodeID, registered.Token, agenthub.TaskPHPInventory, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"version":"8.3","unit":"php8.3-fpm.service","active":"active","enabled":"enabled","binary":"/usr/sbin/php-fpm8.3","pools":[{"name":"www","path":"/etc/php/8.3/fpm/pool.d/www.conf"}]}]`}}, nil
	})
	inventoryRequest := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/php", nil)
	inventoryRequest.SetPathValue("id", nodeID)
	inventoryResponse := httptest.NewRecorder()
	handleRemoteNodePHPFPM(hub).ServeHTTP(inventoryResponse, inventoryRequest)
	if err := <-inventoryDone; err != nil {
		t.Fatalf("inventory agent: %v", err)
	}
	if inventoryResponse.Code != http.StatusOK || !strings.Contains(inventoryResponse.Body.String(), `"version":"8.3"`) {
		t.Fatalf("inventory status = %d, body=%s", inventoryResponse.Code, inventoryResponse.Body.String())
	}

	readDone := completeNextPHPTask(t, hub, nodeID, registered.Token, agenthub.TaskPHPConfigRead, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["version"] != "8.3" || task.Payload["pool"] != "www" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `{"path":"/etc/php/8.3/fpm/pool.d/www.conf","content":"[www]\n","checksum":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":6,"mode":"0640","modified_at":"2026-08-26T10:00:00Z"}`}}, nil
	})
	readRequest := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/php/8.3/pools/www", nil)
	readRequest.SetPathValue("id", nodeID)
	readRequest.SetPathValue("version", "8.3")
	readRequest.SetPathValue("pool", "www")
	readResponse := httptest.NewRecorder()
	handleRemoteNodePHPFPMConfigGet(hub).ServeHTTP(readResponse, readRequest)
	if err := <-readDone; err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if readResponse.Code != http.StatusOK || !strings.Contains(readResponse.Body.String(), `"content":"[www]\n"`) {
		t.Fatalf("read status = %d, body=%s", readResponse.Code, readResponse.Body.String())
	}

	writeDone := completeNextPHPTask(t, hub, nodeID, registered.Token, agenthub.TaskPHPConfigWrite, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		content, decodeErr := base64.StdEncoding.DecodeString(task.Payload["content_b64"])
		if decodeErr != nil || string(content) != "[www]\npm = dynamic\n" || task.Payload["reload"] != "true" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{
			"message": "PHP-FPM pool saved, tested and reloaded", "backup": "/etc/php/8.3/fpm/pool.d/www.conf.hserver-backup-test",
		}}, nil
	})
	body := `{"content":"[www]\npm = dynamic\n","checksum":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","reload":true}`
	writeRequest := httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/php/8.3/pools/www", bytes.NewBufferString(body))
	writeRequest.SetPathValue("id", nodeID)
	writeRequest.SetPathValue("version", "8.3")
	writeRequest.SetPathValue("pool", "www")
	writeResponse := httptest.NewRecorder()
	handleRemoteNodePHPFPMConfigSave(hub).ServeHTTP(writeResponse, writeRequest)
	if err := <-writeDone; err != nil {
		t.Fatalf("write agent: %v", err)
	}
	if writeResponse.Code != http.StatusOK || !strings.Contains(writeResponse.Body.String(), `"backup":"/etc/php/8.3/fpm/pool.d/www.conf.hserver-backup-test"`) {
		t.Fatalf("write status = %d, body=%s", writeResponse.Code, writeResponse.Body.String())
	}

	actionDone := completeNextPHPTask(t, hub, nodeID, registered.Token, agenthub.TaskPHPAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["version"] != "8.3" || task.Payload["action"] != "restart" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "PHP-FPM configuration tested and restarted"}}, nil
	})
	actionRequest := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/php/8.3/actions/restart", nil)
	actionRequest.SetPathValue("id", nodeID)
	actionRequest.SetPathValue("version", "8.3")
	actionRequest.SetPathValue("action", "restart")
	actionResponse := httptest.NewRecorder()
	handleRemoteNodePHPFPMAction(hub).ServeHTTP(actionResponse, actionRequest)
	if err := <-actionDone; err != nil {
		t.Fatalf("action agent: %v", err)
	}
	if actionResponse.Code != http.StatusOK || !strings.Contains(actionResponse.Body.String(), `"message":"PHP-FPM configuration tested and restarted"`) {
		t.Fatalf("action status = %d, body=%s", actionResponse.Code, actionResponse.Body.String())
	}
}

func completeNextPHPTask(t *testing.T, hub *agenthub.Service, nodeID, token, kind string, result func(*agenthub.Task) (agenthub.TaskResultRequest, error)) <-chan error {
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
