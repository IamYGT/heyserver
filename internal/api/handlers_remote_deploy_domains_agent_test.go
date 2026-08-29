package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
)

func TestRemoteDeployProjectDomainsUseFixedAgentTasksWithoutUpstreamInput(t *testing.T) {
	const nodeID = "project-domain-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Project Domain Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion, NodeID: nodeID, AgentVersion: "agent-test",
		Capabilities: []string{agenthub.CapabilityInventory, agenthub.CapabilityDeployRead, agenthub.CapabilityDeployDomainRead, agenthub.CapabilityDeployDomainAction},
		Hostname:     "node.example", SentAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	inventoryDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDeployDomainInventory, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if len(task.Payload) != 1 || task.Payload["target"] != "example-app" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `[{"target_id":"example-app","domain":"app.example.com","host_port":8080,"desired_host_port":8080,"upstream":"http://127.0.0.1:8080","status":"active","message":"observed","tls_status":"not_configured","tls_message":"not configured"}]`}}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/deploy/example-app/domains", nil)
	setRemoteDeployDomainPath(request, nodeID, "example-app", "")
	response := httptest.NewRecorder()
	handleRemoteNodeDeployDomains(hub).ServeHTTP(response, request)
	if err := <-inventoryDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"upstream":"http://127.0.0.1:8080"`) {
		t.Fatalf("inventory=%d %s", response.Code, response.Body.String())
	}

	createDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDeployDomainAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if len(task.Payload) != 3 || task.Payload["target"] != "example-app" || task.Payload["domain"] != "app.example.com" || task.Payload["action"] != "create" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		if task.Payload["host_port"] != "" || task.Payload["upstream"] != "" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return remoteDeployDomainReceipt(), nil
	})
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/deploy/example-app/domains", strings.NewReader(`{"domain":" App.Example.COM. "}`))
	setRemoteDeployDomainPath(request, nodeID, "example-app", "")
	response = httptest.NewRecorder()
	handleRemoteNodeDeployDomainCreate(hub).ServeHTTP(response, request)
	if err := <-createDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"domain":"app.example.com"`) {
		t.Fatalf("create=%d %s", response.Code, response.Body.String())
	}

	tlsDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDeployDomainAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if len(task.Payload) != 4 || task.Payload["action"] != "tls-enable" || task.Payload["email"] != "admin@example.com" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return remoteDeployDomainReceipt(), nil
	})
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/deploy/example-app/domains/app.example.com/tls", strings.NewReader(`{"email":"admin@example.com"}`))
	setRemoteDeployDomainPath(request, nodeID, "example-app", "app.example.com")
	response = httptest.NewRecorder()
	handleRemoteNodeDeployDomainTLSEnable(hub).ServeHTTP(response, request)
	if err := <-tlsDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("tls=%d %s", response.Code, response.Body.String())
	}

	healthDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDeployDomainHealth, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if task.Payload["domain"] != "app.example.com" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `{"domain":"app.example.com","upstream":"http://127.0.0.1:8080","status":"healthy","status_code":204,"latency_ms":2,"message":"ok","checked_at":"2026-08-26T23:00:00Z"}`}}, nil
	})
	request = httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/deploy/example-app/domains/app.example.com/health", nil)
	setRemoteDeployDomainPath(request, nodeID, "example-app", "app.example.com")
	response = httptest.NewRecorder()
	handleRemoteNodeDeployDomainHealth(hub).ServeHTTP(response, request)
	if err := <-healthDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"healthy"`) {
		t.Fatalf("health=%d %s", response.Code, response.Body.String())
	}

	deleteDone := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDeployDomainAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if len(task.Payload) != 3 || task.Payload["action"] != "delete" {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "removed"}}, nil
	})
	request = httptest.NewRequest(http.MethodDelete, "/api/nodes/"+nodeID+"/deploy/example-app/domains/app.example.com", nil)
	setRemoteDeployDomainPath(request, nodeID, "example-app", "app.example.com")
	response = httptest.NewRecorder()
	handleRemoteNodeDeployDomainDelete(hub).ServeHTTP(response, request)
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("delete=%d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/deploy/example-app/domains/app.example.com/tls", strings.NewReader(`{"email":"Display Name <admin@example.com>"}`))
	setRemoteDeployDomainPath(request, nodeID, "example-app", "app.example.com")
	response = httptest.NewRecorder()
	handleRemoteNodeDeployDomainTLSEnable(hub).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid email=%d %s", response.Code, response.Body.String())
	}
}

func TestRemoteDeployDomainEnsureUsesStrictBodyPayloadAndTypedReceipt(t *testing.T) {
	const nodeID = "project-domain-ensure-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Project Domain Ensure Agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityDeployDomainAction},
		Hostname:        "ensure.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 64)
	done := completeNextCronTask(t, hub, nodeID, registered.Token, agenthub.TaskDeployDomainAction, func(task *agenthub.Task) (agenthub.TaskResultRequest, error) {
		if len(task.Payload) != 4 || task.Payload["target"] != "example-app" || task.Payload["domain"] != "app.example.com" || task.Payload["action"] != "ensure" || task.Payload["expected_revision"] != revision {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		if _, forwarded := task.Payload["confirmed"]; forwarded {
			return agenthub.TaskResultRequest{}, context.Canceled
		}
		return remoteDeployDomainEnsureReceipt(revision), nil
	})
	request := httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/deploy/example-app/domains/app.example.com", strings.NewReader(`{"expected_revision":"`+revision+`","confirmed":true}`))
	request.SetPathValue("node_id", nodeID)
	request.SetPathValue("target_id", "example-app")
	request.SetPathValue("domain", "app.example.com")
	response := httptest.NewRecorder()
	handleRemoteNodeDeployDomainEnsure(hub).ServeHTTP(response, request)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"changed":true`) || !strings.Contains(response.Body.String(), `"observation"`) || !strings.Contains(response.Body.String(), `"enabled":true`) || !strings.Contains(response.Body.String(), `"revision":"`+revision+`"`) {
		t.Fatalf("ensure=%d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"task"`) || strings.Contains(response.Body.String(), `"error"`) {
		t.Fatalf("ensure leaked task/error fields: %s", response.Body.String())
	}

	tasksBefore, err := hub.ListTasksForNode(nodeID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"expected_revision":"` + revision + `","confirmed":true,"unknown":1}`,
		`{"expected_revision":"` + revision + `","confirmed":true,"confirmed":true}`,
		`{"expected_revision":"` + revision + `","confirmed":false}`,
		`{"expected_revision":"` + strings.Repeat("A", 64) + `","confirmed":true}`,
	} {
		request = httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/deploy/example-app/domains/app.example.com", strings.NewReader(body))
		request.SetPathValue("node_id", nodeID)
		request.SetPathValue("target_id", "example-app")
		request.SetPathValue("domain", "app.example.com")
		response = httptest.NewRecorder()
		handleRemoteNodeDeployDomainEnsure(hub).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid ensure body %s: status=%d body=%s", body, response.Code, response.Body.String())
		}
	}
	tasksAfter, err := hub.ListTasksForNode(nodeID, 20)
	if err != nil || len(tasksAfter) != len(tasksBefore) {
		t.Fatalf("invalid ensure bodies changed task count: before=%d after=%d err=%v", len(tasksBefore), len(tasksAfter), err)
	}
}

func TestRemoteDeployDomainEnsureFailureMappings(t *testing.T) {
	tests := []struct {
		code   string
		status int
	}{
		{"stale", http.StatusConflict},
		{"stale_observation", http.StatusConflict},
		{"domain_drift", http.StatusConflict},
		{"domain_conflict", http.StatusConflict},
		{"offline", http.StatusConflict},
		{"capability", http.StatusConflict},
		{"invalid_local_plan", http.StatusUnprocessableEntity},
		{"nginx_test", http.StatusBadGateway},
		{"nginx_reload", http.StatusBadGateway},
		{"nginx_rollback", http.StatusBadGateway},
		{"invalid_receipt", http.StatusBadGateway},
		{"timeout", http.StatusGatewayTimeout},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		writeRemoteDeployDomainEnsureTaskError(response, &agenthub.Task{Status: agenthub.TaskStatusFailed, Result: map[string]string{"code": test.code}}, errors.New("agent detail"))
		if response.Code != test.status {
			t.Fatalf("failure code %q status=%d body=%s, want %d", test.code, response.Code, response.Body.String(), test.status)
		}
		if strings.Contains(response.Body.String(), "agent detail") {
			t.Fatalf("failure code %q leaked agent detail: %s", test.code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	writeRemoteDeployDomainEnsureTaskError(response, nil, context.DeadlineExceeded)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout without task status=%d body=%s", response.Code, response.Body.String())
	}
}

func remoteDeployDomainReceipt() agenthub.TaskResultRequest {
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": `{"target_id":"example-app","domain":"app.example.com","host_port":8080,"desired_host_port":8080,"upstream":"http://127.0.0.1:8080","status":"active","message":"observed","tls_status":"healthy","tls_message":"valid"}`}}
}

func remoteDeployDomainEnsureReceipt(revision string) agenthub.TaskResultRequest {
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": fmt.Sprintf(`{"changed":true,"observation":{"target_id":"example-app","domain":"app.example.com","host_port":8080,"desired_host_port":8080,"upstream":"http://127.0.0.1:8080","status":"active","message":"observed","tls_status":"healthy","tls_message":"valid","enabled":true,"revision":"%s"}}`, revision)}}
}

func setRemoteDeployDomainPath(request *http.Request, node, target, domain string) {
	request.SetPathValue("id", node)
	request.SetPathValue("target", target)
	request.SetPathValue("node_id", node)
	request.SetPathValue("target_id", target)
	if domain != "" {
		request.SetPathValue("domain", domain)
	}
}
