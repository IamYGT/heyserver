package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestHandleNodeTaskCreateRequiresExplicitConfirmationBeforeQueueing(t *testing.T) {
	const nodeID = "generic-task-confirmation-agent"
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Generic Task Confirmation Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityServiceStatus},
		Hostname:        "generic-task-confirmation.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "missing confirmation", body: `{"kind":"service.status","payload":{"service":"nginx.service"}}`},
		{name: "false confirmation", body: `{"kind":"service.status","payload":{"service":"nginx.service"},"confirmed":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/tasks", strings.NewReader(test.body))
			request.SetPathValue("id", nodeID)
			response := httptest.NewRecorder()
			handleNodeTaskCreate(hub).ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), "confirmed:true") {
				t.Fatalf("body = %q, want confirmed:true guidance", response.Body.String())
			}
			tasks, err := hub.ListTasksForNode(nodeID, 10)
			if err != nil {
				t.Fatalf("ListTasksForNode: %v", err)
			}
			if len(tasks) != 0 {
				t.Fatalf("unconfirmed request enqueued %d task(s): %#v", len(tasks), tasks)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/tasks", strings.NewReader(`{"kind":"service.status","payload":{"service":"nginx.service"},"confirmed":true}`))
	request.SetPathValue("id", nodeID)
	response := httptest.NewRecorder()
	handleNodeTaskCreate(hub).ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("confirmed status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var created agenthub.Task
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode confirmed response: %v", err)
	}
	if created.NodeID != nodeID || created.Kind != agenthub.TaskServiceStatus || created.Payload["service"] != "nginx.service" {
		t.Fatalf("created task = %#v", created)
	}
	tasks, err := hub.ListTasksForNode(nodeID, 10)
	if err != nil {
		t.Fatalf("ListTasksForNode after confirmed request: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != created.ID {
		t.Fatalf("queued tasks = %#v, want task #%d", tasks, created.ID)
	}
}

func TestNodeTaskRoutesEnforceAdminMutationBoundary(t *testing.T) {
	const nodeID = "generic-task-role-agent"
	deps := contractTestDeps(t)
	hub := deps.AgentHub
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Generic Task Role Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityServiceStatus, agenthub.CapabilityHostAction},
		Hostname:        "generic-task-role.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)
	managerToken := testutil.MakeToken(t, testutil.MakeUser(201, "task-manager@test.com", models.RoleManager))
	adminToken := testutil.MakeToken(t, testutil.MakeUser(202, "task-admin@test.com", models.RoleAdmin))

	managerCreate := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/tasks", strings.NewReader(`{"kind":"service.status","payload":{"service":"nginx.service"},"confirmed":true}`))
	managerCreate.Header.Set("Authorization", "Bearer "+managerToken)
	managerCreateResponse := httptest.NewRecorder()
	handler.ServeHTTP(managerCreateResponse, managerCreate)
	if managerCreateResponse.Code != http.StatusForbidden {
		t.Fatalf("manager create status = %d, want %d; body=%s", managerCreateResponse.Code, http.StatusForbidden, managerCreateResponse.Body.String())
	}
	tasks, err := hub.ListTasksForNode(nodeID, 10)
	if err != nil {
		t.Fatalf("ListTasksForNode after manager create: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("manager create enqueued %d task(s): %#v", len(tasks), tasks)
	}

	adminCreate := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/tasks", strings.NewReader(`{"kind":"service.status","payload":{"service":"nginx.service"},"confirmed":true}`))
	adminCreate.Header.Set("Authorization", "Bearer "+adminToken)
	adminCreateResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminCreateResponse, adminCreate)
	if adminCreateResponse.Code != http.StatusCreated {
		t.Fatalf("admin create status = %d, want %d; body=%s", adminCreateResponse.Code, http.StatusCreated, adminCreateResponse.Body.String())
	}
	var created agenthub.Task
	if err := json.Unmarshal(adminCreateResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode admin create response: %v", err)
	}
	if created.NodeID != nodeID || created.Kind != agenthub.TaskServiceStatus || created.Status != agenthub.TaskStatusQueued {
		t.Fatalf("created task = %#v", created)
	}

	managerList := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/tasks?limit=10", nil)
	managerList.Header.Set("Authorization", "Bearer "+managerToken)
	managerListResponse := httptest.NewRecorder()
	handler.ServeHTTP(managerListResponse, managerList)
	if managerListResponse.Code != http.StatusOK {
		t.Fatalf("manager list status = %d, want %d; body=%s", managerListResponse.Code, http.StatusOK, managerListResponse.Body.String())
	}
	var history []agenthub.Task
	if err := json.Unmarshal(managerListResponse.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode manager task history: %v", err)
	}
	if len(history) != 1 || history[0].ID != created.ID {
		t.Fatalf("manager task history = %#v, want task #%d", history, created.ID)
	}
	managerGet := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/tasks/"+strconv.FormatInt(created.ID, 10), nil)
	managerGet.Header.Set("Authorization", "Bearer "+managerToken)
	managerGetResponse := httptest.NewRecorder()
	handler.ServeHTTP(managerGetResponse, managerGet)
	if managerGetResponse.Code != http.StatusOK {
		t.Fatalf("manager get status = %d, want %d; body=%s", managerGetResponse.Code, http.StatusOK, managerGetResponse.Body.String())
	}
	var fetched agenthub.Task
	if err := json.Unmarshal(managerGetResponse.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode manager task get: %v", err)
	}
	if fetched.ID != created.ID {
		t.Fatalf("manager fetched task = %#v, want task #%d", fetched, created.ID)
	}

	managerDedicatedAction := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/actions/swap-reset", nil)
	managerDedicatedAction.Header.Set("Authorization", "Bearer "+managerToken)
	managerDedicatedActionResponse := httptest.NewRecorder()
	handler.ServeHTTP(managerDedicatedActionResponse, managerDedicatedAction)
	if managerDedicatedActionResponse.Code != http.StatusForbidden {
		t.Fatalf("manager dedicated action status = %d, want %d; body=%s", managerDedicatedActionResponse.Code, http.StatusForbidden, managerDedicatedActionResponse.Body.String())
	}

	invalidKind := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/tasks", strings.NewReader(`{"kind":"unsupported.kind","confirmed":true}`))
	invalidKind.Header.Set("Authorization", "Bearer "+adminToken)
	invalidKindResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidKindResponse, invalidKind)
	if invalidKindResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid kind status = %d, want %d; body=%s", invalidKindResponse.Code, http.StatusBadRequest, invalidKindResponse.Body.String())
	}
	tasks, err = hub.ListTasksForNode(nodeID, 10)
	if err != nil {
		t.Fatalf("ListTasksForNode after rejected requests: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != created.ID {
		t.Fatalf("rejected requests changed task history: %#v", tasks)
	}
}
