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
)

func TestNodeProfileHandlersExposeDesiredAndObservedState(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	const nodeID = "profile-handler-agent"
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Profile Handler Agent"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "v1.0.0",
		Capabilities: []string{
			agenthub.CapabilityInventory,
			agenthub.CapabilityDeployRead,
			agenthub.CapabilityDeployAction,
		},
		Hostname: "profile-agent.example",
		SentAt:   now,
	}); err != nil {
		t.Fatal(err)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/profile", nil)
	get.SetPathValue("id", nodeID)
	getResponse := httptest.NewRecorder()
	handleNodeProfileGet(hub).ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("initial GET status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	initialBody := getResponse.Body.String()
	for _, fragment := range []string{
		`"nodeId":"profile-handler-agent"`,
		`"state":"not_configured"`,
		`"revision":0`,
		`"profile":null`,
		`"profileState":"not_reported"`,
		`"agentVersion":"v1.0.0"`,
		`"protocolVersion":"v1"`,
		`"online":true`,
		`"apply":{"state":"manual_required","reason":"self_apply_not_supported"}`,
	} {
		if !strings.Contains(initialBody, fragment) {
			t.Fatalf("initial GET missing %s in %s", fragment, initialBody)
		}
	}

	put := httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/profile", strings.NewReader(`{
  "profile": {
    "allowDeployRead": true,
    "allowDeployActions": true,
    "allowDeployDomainRead": false,
    "allowDeployDomainActions": false,
    "deployPlansFile": "/etc/hserver/deploy-plans.json",
    "deployAcmeWebroot": "/var/www/hserver-acme",
    "deployWriteRoots": ["/srv/z", "/srv/a"]
  },
  "expectedRevision": 0
}`))
	put.SetPathValue("id", nodeID)
	putResponse := httptest.NewRecorder()
	handleNodeProfilePut(hub).ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", putResponse.Code, putResponse.Body.String())
	}
	putBody := putResponse.Body.String()
	for _, fragment := range []string{
		`"nodeId":"profile-handler-agent"`,
		`"state":"configured"`,
		`"revision":1`,
		`"deployWriteRoots":["/srv/a","/srv/z"]`,
		`"profileState":"not_reported"`,
		`"apply":{"state":"manual_required","reason":"self_apply_not_supported"}`,
	} {
		if !strings.Contains(putBody, fragment) {
			t.Fatalf("PUT missing %s in %s", fragment, putBody)
		}
	}

	var taskCount int
	if err := db.Instance().QueryRow(`SELECT COUNT(*) FROM agent_tasks WHERE node_id = ?`, nodeID).Scan(&taskCount); err != nil {
		t.Fatalf("task count: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("profile PUT created %d agent tasks", taskCount)
	}

	stale := httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/profile", strings.NewReader(`{"profile":{"allowDeployRead":false,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":[]},"expectedRevision":0}`))
	stale.SetPathValue("id", nodeID)
	staleResponse := httptest.NewRecorder()
	handleNodeProfilePut(hub).ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict || !strings.Contains(staleResponse.Body.String(), `"error":"stale_profile_revision"`) {
		t.Fatalf("stale PUT status=%d body=%s", staleResponse.Code, staleResponse.Body.String())
	}

	unknown := httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/profile", strings.NewReader(`{"profile":{"allowDeployRead":false,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":[]},"expectedRevision":1,"unexpected":true}`))
	unknown.SetPathValue("id", nodeID)
	unknownResponse := httptest.NewRecorder()
	handleNodeProfilePut(hub).ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown field PUT status=%d body=%s", unknownResponse.Code, unknownResponse.Body.String())
	}

	stringForm := httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/profile", strings.NewReader(`{"profile":{"allowDeployRead":false,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":"/srv/a"},"expectedRevision":1}`))
	stringForm.SetPathValue("id", nodeID)
	stringFormResponse := httptest.NewRecorder()
	handleNodeProfilePut(hub).ServeHTTP(stringFormResponse, stringForm)
	if stringFormResponse.Code != http.StatusBadRequest {
		t.Fatalf("string deployWriteRoots status=%d body=%s, want %d", stringFormResponse.Code, stringFormResponse.Body.String(), http.StatusBadRequest)
	}
}

func TestNodeProfileHandlersRejectMissingNode(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/missing-profile-agent/profile", nil)
	request.SetPathValue("id", "missing-profile-agent")
	response := httptest.NewRecorder()
	handleNodeProfileGet(hub).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing GET status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNodeProfileHandlersRejectIncompleteProfileWithoutMutation(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	const nodeID = "profile-strict-agent"
	if _, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Profile Strict Agent"}); err != nil {
		t.Fatal(err)
	}

	valid := validProfileFields()
	validBody := profileRequestBody(valid, json.RawMessage(`0`), true, true)
	cases := []struct {
		name string
		body string
	}{
		{name: "missing profile", body: `{"expectedRevision":0}`},
		{name: "null profile", body: `{"profile":null,"expectedRevision":0}`},
		{name: "missing expected revision", body: profileRequestBody(validProfileFields(), nil, true, false)},
		{name: "null expected revision", body: `{"profile":{"allowDeployRead":false,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":[]},"expectedRevision":null}`},
		{name: "profile wrong type", body: `{"profile":42,"expectedRevision":0}`},
		{name: "expected revision wrong type", body: `{"profile":{"allowDeployRead":false,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":[]},"expectedRevision":"0"}`},
		{name: "unknown envelope field", body: validBody[:len(validBody)-1] + `,"unexpected":true}`},
		{name: "unknown profile field", body: profileRequestBody(withProfileField(valid, "unexpected", `true`), json.RawMessage(`0`), true, true)},
		{name: "trailing JSON", body: validBody + `{}`},
		{name: "wrong boolean type", body: profileRequestBody(withProfileField(valid, "allowDeployRead", `"false"`), json.RawMessage(`0`), true, true)},
		{name: "wrong string type", body: profileRequestBody(withProfileField(valid, "deployPlansFile", `42`), json.RawMessage(`0`), true, true)},
		{name: "wrong array type", body: profileRequestBody(withProfileField(valid, "deployWriteRoots", `"/srv/a"`), json.RawMessage(`0`), true, true)},
		{name: "wrong array element type", body: profileRequestBody(withProfileField(valid, "deployWriteRoots", `[42]`), json.RawMessage(`0`), true, true)},
	}
	for _, field := range []string{"allowDeployRead", "allowDeployActions", "allowDeployDomainRead", "allowDeployDomainActions", "deployPlansFile", "deployAcmeWebroot", "deployWriteRoots"} {
		cases = append(cases, struct {
			name string
			body string
		}{name: "null " + field, body: profileRequestBody(withProfileField(valid, field, `null`), json.RawMessage(`0`), true, true)})
	}
	for _, field := range []string{"allowDeployRead", "allowDeployActions", "allowDeployDomainRead", "allowDeployDomainActions"} {
		cases = append(cases, struct {
			name string
			body string
		}{name: "missing " + field, body: profileRequestBody(withoutProfileField(valid, field), json.RawMessage(`0`), true, true)})
	}
	for _, field := range []string{"deployPlansFile", "deployAcmeWebroot", "deployWriteRoots"} {
		cases = append(cases, struct {
			name string
			body string
		}{name: "missing " + field, body: profileRequestBody(withoutProfileField(valid, field), json.RawMessage(`0`), true, true)})
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/profile", strings.NewReader(test.body))
			request.SetPathValue("id", nodeID)
			response := httptest.NewRecorder()
			handleNodeProfilePut(hub).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), http.StatusBadRequest)
			}
		})
	}

	state, err := hub.GetNodeProfile(nodeID)
	if err != nil {
		t.Fatalf("GetNodeProfile after rejected requests: %v", err)
	}
	if state.Desired.State != "not_configured" || state.Desired.Revision != 0 || state.Desired.Profile != nil {
		t.Fatalf("rejected requests mutated desired state: %#v", state.Desired)
	}
	var taskCount int
	if err := db.Instance().QueryRow(`SELECT COUNT(*) FROM agent_tasks WHERE node_id = ?`, nodeID).Scan(&taskCount); err != nil {
		t.Fatalf("task count: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("rejected profile requests created %d tasks", taskCount)
	}
}

func TestNodeProfileApplyHandlerStrictLifecycleAndGenericTaskRejection(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatal(err)
	}
	const nodeID = "profile-apply-handler-agent"
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: nodeID, Name: "Profile Apply Handler Agent"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "agent-apply",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityProfileApply},
		Hostname:        "profile-apply-handler.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	put := httptest.NewRequest(http.MethodPut, "/api/nodes/"+nodeID+"/profile", strings.NewReader(`{"profile":{"allowDeployRead":true,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"/etc/hserver/plans.json","deployAcmeWebroot":"","deployWriteRoots":[]},"expectedRevision":0}`))
	put.SetPathValue("id", nodeID)
	putResponse := httptest.NewRecorder()
	handleNodeProfilePut(hub).ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", putResponse.Code, putResponse.Body.String())
	}

	var rejectedCount int
	for _, body := range []string{
		`{"expectedRevision":1,"confirmed":false}`,
		`{"expectedRevision":0,"confirmed":true}`,
		`{"expectedRevision":1,"confirmed":true,"profile":{}}`,
		`{"expectedRevision":1}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/profile/apply", strings.NewReader(body))
		request.SetPathValue("id", nodeID)
		response := httptest.NewRecorder()
		handleNodeProfileApply(hub).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid apply body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
	if err := db.Instance().QueryRow(`SELECT COUNT(*) FROM agent_tasks WHERE node_id = ?`, nodeID).Scan(&rejectedCount); err != nil {
		t.Fatalf("rejected task count: %v", err)
	}
	if rejectedCount != 0 {
		t.Fatalf("invalid apply requests created %d tasks", rejectedCount)
	}

	apply := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/profile/apply", strings.NewReader(`{"expectedRevision":1,"confirmed":true}`))
	apply.SetPathValue("id", nodeID)
	applyResponse := httptest.NewRecorder()
	handleNodeProfileApply(hub).ServeHTTP(applyResponse, apply)
	if applyResponse.Code != http.StatusAccepted || !strings.Contains(applyResponse.Body.String(), `"state":"queued"`) || !strings.Contains(applyResponse.Body.String(), `"taskId":`) {
		t.Fatalf("first apply status=%d body=%s", applyResponse.Code, applyResponse.Body.String())
	}
	var queued agenthub.AgentProfileResponse
	if err := json.Unmarshal(applyResponse.Body.Bytes(), &queued); err != nil {
		t.Fatalf("decode first apply: %v", err)
	}
	if queued.Apply.TaskID == nil {
		t.Fatalf("first apply task id is nil: %#v", queued.Apply)
	}
	duplicate := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/profile/apply", strings.NewReader(`{"expectedRevision":1,"confirmed":true}`))
	duplicate.SetPathValue("id", nodeID)
	duplicateResponse := httptest.NewRecorder()
	handleNodeProfileApply(hub).ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusAccepted || !strings.Contains(duplicateResponse.Body.String(), `"state":"queued"`) || !strings.Contains(duplicateResponse.Body.String(), `"taskId":`+strconv.FormatInt(*queued.Apply.TaskID, 10)) {
		t.Fatalf("duplicate apply status=%d body=%s", duplicateResponse.Code, duplicateResponse.Body.String())
	}

	generic := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/tasks", strings.NewReader(`{"kind":"agent.profile.apply","payload":{"revision":"1","profile_json_b64":"ignored"},"confirmed":true}`))
	generic.SetPathValue("id", nodeID)
	genericResponse := httptest.NewRecorder()
	handleNodeTaskCreate(hub).ServeHTTP(genericResponse, generic)
	if genericResponse.Code != http.StatusBadRequest {
		t.Fatalf("generic profile task status=%d body=%s", genericResponse.Code, genericResponse.Body.String())
	}

	claimed, err := hub.PollTask(nodeID, registered.Token)
	if err != nil || claimed == nil || claimed.ID != *queued.Apply.TaskID {
		t.Fatalf("claimed profile task=%#v err=%v", claimed, err)
	}
	if _, err := hub.CompleteTask(nodeID, registered.Token, claimed.ID, agenthub.TaskResultRequest{
		Status: agenthub.TaskStatusCompleted,
		Result: map[string]string{"state": agenthub.ProfileApplyResultRestartScheduled, "revision": "1"},
	}); err != nil {
		t.Fatalf("complete profile task: %v", err)
	}
	awaiting := httptest.NewRequest(http.MethodGet, "/api/nodes/"+nodeID+"/profile", nil)
	awaiting.SetPathValue("id", nodeID)
	awaitingResponse := httptest.NewRecorder()
	handleNodeProfileGet(hub).ServeHTTP(awaitingResponse, awaiting)
	if awaitingResponse.Code != http.StatusOK || !strings.Contains(awaitingResponse.Body.String(), `"state":"awaiting_heartbeat"`) {
		t.Fatalf("awaiting GET status=%d body=%s", awaitingResponse.Code, awaitingResponse.Body.String())
	}
	if _, err := hub.Heartbeat(nodeID, registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          nodeID,
		AgentVersion:    "agent-apply",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityProfileApply},
		Hostname:        "profile-apply-handler.example",
		SentAt:          now,
		Profile:         &agenthub.AgentProfileObservation{State: agenthub.ProfileObservationApplied, Revision: 1},
	}); err != nil {
		t.Fatalf("applied heartbeat: %v", err)
	}
	alreadyApplied := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/profile/apply", strings.NewReader(`{"expectedRevision":1,"confirmed":true}`))
	alreadyApplied.SetPathValue("id", nodeID)
	alreadyAppliedResponse := httptest.NewRecorder()
	handleNodeProfileApply(hub).ServeHTTP(alreadyAppliedResponse, alreadyApplied)
	if alreadyAppliedResponse.Code != http.StatusOK || !strings.Contains(alreadyAppliedResponse.Body.String(), `"state":"applied"`) {
		t.Fatalf("already applied status=%d body=%s", alreadyAppliedResponse.Code, alreadyAppliedResponse.Body.String())
	}
	if err := db.Instance().QueryRow(`SELECT COUNT(*) FROM agent_tasks WHERE node_id = ?`, nodeID).Scan(&rejectedCount); err != nil {
		t.Fatalf("final task count: %v", err)
	}
	if rejectedCount != 1 {
		t.Fatalf("duplicate/generic/already-applied requests changed task count to %d", rejectedCount)
	}
}

func TestNodeProfileApplyRouteIsAdminOnlyInManifest(t *testing.T) {
	for _, route := range AllRoutes() {
		if route.Method == http.MethodPost && route.Path == "/api/nodes/{id}/profile/apply" {
			if route.Auth != RouteAdmin {
				t.Fatalf("profile apply route auth=%q, want %q", route.Auth, RouteAdmin)
			}
			return
		}
	}
	t.Fatal("profile apply route missing from route manifest")
}

func validProfileFields() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"allowDeployRead":          json.RawMessage(`false`),
		"allowDeployActions":       json.RawMessage(`false`),
		"allowDeployDomainRead":    json.RawMessage(`false`),
		"allowDeployDomainActions": json.RawMessage(`false`),
		"deployPlansFile":          json.RawMessage(`""`),
		"deployAcmeWebroot":        json.RawMessage(`""`),
		"deployWriteRoots":         json.RawMessage(`[]`),
	}
}

func withProfileField(fields map[string]json.RawMessage, field, value string) map[string]json.RawMessage {
	copy := cloneProfileFields(fields)
	copy[field] = json.RawMessage(value)
	return copy
}

func withoutProfileField(fields map[string]json.RawMessage, field string) map[string]json.RawMessage {
	copy := cloneProfileFields(fields)
	delete(copy, field)
	return copy
}

func cloneProfileFields(fields map[string]json.RawMessage) map[string]json.RawMessage {
	copy := make(map[string]json.RawMessage, len(fields))
	for field, value := range fields {
		copy[field] = value
	}
	return copy
}

func profileRequestBody(fields map[string]json.RawMessage, revision json.RawMessage, includeProfile, includeRevision bool) string {
	envelope := make(map[string]json.RawMessage, 2)
	if includeProfile {
		profile, err := json.Marshal(fields)
		if err != nil {
			panic(err)
		}
		envelope["profile"] = profile
	}
	if includeRevision {
		envelope["expectedRevision"] = revision
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return string(body)
}
