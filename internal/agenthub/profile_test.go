package agenthub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAgentProfileRejectsUnsafeOrInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		profile AgentProfile
	}{
		{name: "actions require deploy read", profile: AgentProfile{AllowDeployActions: true}},
		{name: "domain read requires deploy read", profile: AgentProfile{AllowDeployDomainRead: true}},
		{name: "domain actions require domain read", profile: AgentProfile{AllowDeployDomainActions: true}},
		{name: "relative file", profile: AgentProfile{DeployPlansFile: "etc/plans.json"}},
		{name: "root file", profile: AgentProfile{DeployPlansFile: "/"}},
		{name: "unclean file", profile: AgentProfile{DeployPlansFile: "/etc/../plans.json"}},
		{name: "file trailing slash", profile: AgentProfile{DeployPlansFile: "/etc/plans.json/"}},
		{name: "space", profile: AgentProfile{DeployPlansFile: "/srv/deploy plans.json"}},
		{name: "unicode", profile: AgentProfile{DeployPlansFile: "/srv/dağıtım.json"}},
		{name: "percent", profile: AgentProfile{DeployPlansFile: "/srv/deploy%20plans.json"}},
		{name: "newline", profile: AgentProfile{DeployAcmeWebroot: "/srv/acme\nnext"}},
		{name: "nul", profile: AgentProfile{DeployAcmeWebroot: "/srv/acme\x00next"}},
		{name: "oversized path", profile: AgentProfile{DeployAcmeWebroot: "/" + strings.Repeat("a", maxProfilePathBytes)}},
		{name: "root directory", profile: AgentProfile{DeployWriteRoots: []string{"/"}}},
		{name: "duplicate roots", profile: AgentProfile{DeployWriteRoots: []string{"/srv/releases", "/srv/releases"}}},
		{name: "too many roots", profile: AgentProfile{DeployWriteRoots: profileTestRoots(17)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeAgentProfile(test.profile); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("NormalizeAgentProfile error = %v, want ErrInvalidInput", err)
			}
		})
	}

	normalized, err := NormalizeAgentProfile(AgentProfile{DeployWriteRoots: []string{"/srv/z", "/srv/a"}})
	if err != nil {
		t.Fatalf("NormalizeAgentProfile valid roots: %v", err)
	}
	if !slices.Equal(normalized.DeployWriteRoots, []string{"/srv/a", "/srv/z"}) {
		t.Fatalf("normalized roots = %#v, want sorted canonical roots", normalized.DeployWriteRoots)
	}
	if _, err := NormalizeAgentProfile(AgentProfile{}); err != nil {
		t.Fatalf("all-false empty profile: %v", err)
	}
	if _, err := NormalizeAgentProfile(AgentProfile{DeployAcmeWebroot: "/" + strings.Repeat("a", maxProfilePathBytes-1)}); err != nil {
		t.Fatalf("inclusive %d-byte profile path: %v", maxProfilePathBytes, err)
	}
}

func TestSaveNodeProfileEnforcesCanonicalApplyDocumentLimitBeforeWrite(t *testing.T) {
	service, db, _ := newTestService(t)
	registerTestNode(t, service, "profile-envelope-agent")

	boundary := profileWithApplyDocumentSize(t, 1, maxProfileJSONBytes)
	updated, err := service.UpdateNodeProfile("profile-envelope-agent", boundary, 0)
	if err != nil {
		t.Fatalf("UpdateNodeProfile exact %d-byte apply document: %v", maxProfileJSONBytes, err)
	}
	if updated.Desired.Revision != 1 {
		t.Fatalf("boundary desired revision = %d, want 1", updated.Desired.Revision)
	}

	oversize := profileWithApplyDocumentSize(t, 2, maxProfileJSONBytes+1)
	if _, err := service.UpdateNodeProfile("profile-envelope-agent", oversize, 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized UpdateNodeProfile error = %v, want ErrInvalidInput", err)
	}
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM agent_node_profiles WHERE node_id = ?`, "profile-envelope-agent").Scan(&revision); err != nil {
		t.Fatalf("read desired revision after rejected update: %v", err)
	}
	if revision != 1 {
		t.Fatalf("desired revision advanced to %d after rejected oversized wrapper", revision)
	}
}

func profileWithApplyDocumentSize(t *testing.T, revision int64, target int) AgentProfile {
	t.Helper()
	profile := AgentProfile{
		AllowDeployRead:    true,
		AllowDeployActions: true,
		DeployPlansFile:    "/srv/plans.json",
		DeployWriteRoots:   profileTestRoots(maxProfileWriteRoots),
	}
	marshal := func() []byte {
		t.Helper()
		document, err := json.Marshal(profileApplyDocument{SchemaVersion: profileApplySchemaVersion, Revision: revision, Profile: profile})
		if err != nil {
			t.Fatalf("marshal sized profile document: %v", err)
		}
		return document
	}
	remaining := target - len(marshal())
	if remaining < 0 {
		t.Fatalf("target %d is below base document size", target)
	}
	for index := range profile.DeployWriteRoots {
		capacity := maxProfilePathBytes - len(profile.DeployWriteRoots[index])
		if capacity > remaining {
			capacity = remaining
		}
		profile.DeployWriteRoots[index] += strings.Repeat("a", capacity)
		remaining -= capacity
	}
	if remaining != 0 || len(marshal()) != target {
		t.Fatalf("could not build %d-byte apply document: remaining=%d size=%d", target, remaining, len(marshal()))
	}
	return profile
}

func TestNodeProfileCASAndConfiguredDisabledState(t *testing.T) {
	service, db, _ := newTestService(t)
	registerTestNode(t, service, "profile-agent")

	initial, err := service.GetNodeProfile("profile-agent")
	if err != nil {
		t.Fatalf("GetNodeProfile absent: %v", err)
	}
	if initial.Desired.State != profileStateNotConfigured || initial.Desired.Revision != 0 || initial.Observed.ProfileState != profileObservedNotReported {
		t.Fatalf("initial profile response = %#v", initial)
	}
	if initial.Apply.State != profileApplyManualRequired || initial.Apply.Reason != profileApplySelfApplyReason {
		t.Fatalf("initial apply response = %#v", initial.Apply)
	}

	profile := AgentProfile{
		AllowDeployRead:          true,
		AllowDeployActions:       true,
		AllowDeployDomainRead:    true,
		AllowDeployDomainActions: true,
		DeployPlansFile:          "/etc/hserver/deploy-plans.json",
		DeployAcmeWebroot:        "/var/www/hserver-acme",
		DeployWriteRoots:         []string{"/srv/z", "/srv/a"},
	}
	updated, err := service.UpdateNodeProfile("profile-agent", profile, 0)
	if err != nil {
		t.Fatalf("UpdateNodeProfile revision zero: %v", err)
	}
	if updated.Desired.State != profileStateConfigured || updated.Desired.Revision != 1 || updated.Desired.Profile == nil || !slices.Equal(updated.Desired.Profile.DeployWriteRoots, []string{"/srv/a", "/srv/z"}) {
		t.Fatalf("updated profile response = %#v", updated.Desired)
	}

	if _, err := service.UpdateNodeProfile("profile-agent", AgentProfile{}, 0); !errors.Is(err, ErrProfileRevisionStale) {
		t.Fatalf("stale UpdateNodeProfile error = %v, want ErrProfileRevisionStale", err)
	}
	var profileJSON string
	var revision int64
	if err := db.QueryRow(`SELECT profile_json, revision FROM agent_node_profiles WHERE node_id = ?`, "profile-agent").Scan(&profileJSON, &revision); err != nil {
		t.Fatalf("profile row: %v", err)
	}
	if revision != 1 || !strings.Contains(profileJSON, `"allowDeployActions":true`) || !strings.Contains(profileJSON, `"deployWriteRoots":["/srv/a","/srv/z"]`) {
		t.Fatalf("stored profile revision/json = %d/%s", revision, profileJSON)
	}
	var taskCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_tasks WHERE node_id = ?`, "profile-agent").Scan(&taskCount); err != nil {
		t.Fatalf("task count: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("profile update created %d agent tasks", taskCount)
	}

	updated, err = service.UpdateNodeProfile("profile-agent", AgentProfile{}, 1)
	if err != nil {
		t.Fatalf("UpdateNodeProfile disabled profile: %v", err)
	}
	if updated.Desired.State != profileStateConfigured || updated.Desired.Revision != 2 || updated.Desired.Profile == nil || !reflect.DeepEqual(*updated.Desired.Profile, AgentProfile{DeployWriteRoots: []string{}}) {
		t.Fatalf("disabled profile response = %#v", updated.Desired)
	}

	if _, err := service.GetNodeProfile("missing-profile-agent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing GetNodeProfile error = %v, want ErrNotFound", err)
	}
	if _, err := service.UpdateNodeProfile("missing-profile-agent", AgentProfile{}, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing UpdateNodeProfile error = %v, want ErrNotFound", err)
	}

}

func TestNodeProfileForeignKeyCascade(t *testing.T) {
	service, db, _ := newTestService(t)
	registerTestNode(t, service, "profile-cascade-agent")
	if _, err := service.UpdateNodeProfile("profile-cascade-agent", AgentProfile{}, 0); err != nil {
		t.Fatalf("UpdateNodeProfile: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM agent_nodes WHERE id = ?`, "profile-cascade-agent"); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_node_profiles WHERE node_id = ?`, "profile-cascade-agent").Scan(&count); err != nil {
		t.Fatalf("profile count: %v", err)
	}
	if count != 0 {
		t.Fatalf("profile row survived node delete: %d", count)
	}
}

func TestNodeProfileObservationForeignKeyCascadeAndSafeWireValidation(t *testing.T) {
	service, db, now := newTestService(t)
	registered := registerTestNode(t, service, "profile-observation-cascade-agent")
	if _, err := service.Heartbeat("profile-observation-cascade-agent", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "profile-observation-cascade-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityInventory, CapabilityProfileApply},
		Hostname:        "profile-observation-cascade.example",
		SentAt:          now,
		Profile:         &AgentProfileObservation{State: ProfileObservationFailed, Revision: 1, ErrorCode: ProfileErrorCodeCorrupt},
	}); err != nil {
		t.Fatalf("profile observation heartbeat: %v", err)
	}
	observation, err := service.repo.GetNodeProfileObservation("profile-observation-cascade-agent")
	if err != nil || observation == nil || observation.State != ProfileObservationFailed || observation.Revision != 1 || observation.ErrorCode != ProfileErrorCodeCorrupt {
		t.Fatalf("stored observation = %#v, err=%v", observation, err)
	}
	if _, err := db.Exec(`DELETE FROM agent_nodes WHERE id = ?`, "profile-observation-cascade-agent"); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_node_profile_observations WHERE node_id = ?`, "profile-observation-cascade-agent").Scan(&count); err != nil {
		t.Fatalf("observation count: %v", err)
	}
	if count != 0 {
		t.Fatalf("observation row survived node delete: %d", count)
	}

	for _, raw := range []string{
		`{"state":"applied","revision":1,"error_code":"/etc/secret"}`,
		`{"state":"applied","revision":-1}`,
		`{"state":"applied","revision":1,"unexpected":true}`,
		`{"state":"applied","state":"failed","revision":1}`,
	} {
		var observation AgentProfileObservation
		if err := json.Unmarshal([]byte(raw), &observation); err == nil {
			t.Fatalf("unsafe profile observation %s decoded as %#v", raw, observation)
		}
	}
}

func TestProfileCapableAgentPersistsNotConfiguredObservation(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "profile-observation-missing-agent")
	if _, err := service.Heartbeat("profile-observation-missing-agent", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "profile-observation-missing-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityInventory, CapabilityProfileApply},
		Hostname:        "profile-observation-missing.example",
		SentAt:          now,
		Profile:         &AgentProfileObservation{State: ProfileObservationNotConfigured, Revision: 0},
	}); err != nil {
		t.Fatalf("not-configured profile observation heartbeat: %v", err)
	}

	observation, err := service.repo.GetNodeProfileObservation("profile-observation-missing-agent")
	if err != nil || observation == nil {
		t.Fatalf("stored not-configured observation = %#v, err=%v", observation, err)
	}
	if observation.State != ProfileObservationNotConfigured || observation.Revision != 0 || observation.ErrorCode != "" {
		t.Fatalf("stored not-configured observation = %#v", observation)
	}

	response, err := service.GetNodeProfile("profile-observation-missing-agent")
	if err != nil {
		t.Fatalf("GetNodeProfile with not-configured observation: %v", err)
	}
	if response.Observed.ProfileState != ProfileObservationNotConfigured || response.Observed.ProfileRevision == nil || *response.Observed.ProfileRevision != 0 || response.Observed.ProfileErrorCode == nil || *response.Observed.ProfileErrorCode != "" {
		t.Fatalf("observed profile response = %#v", response.Observed)
	}
	if response.Apply.State != profileApplyNotRequested {
		t.Fatalf("apply state for capable unconfigured agent = %#v, want %q", response.Apply, profileApplyNotRequested)
	}
}

func TestGetNodeProfileAcceptsLegacyStringRoots(t *testing.T) {
	service, db, _ := newTestService(t)
	registerTestNode(t, service, "profile-legacy-agent")
	legacyJSON := `{"allowDeployRead":true,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"/etc/hserver/deploy-plans.json","deployAcmeWebroot":"/var/www/hserver-acme","deployWriteRoots":"/srv/z, /srv/a"}`
	if _, err := db.Exec(`
		INSERT INTO agent_node_profiles (node_id, profile_json, revision, updated_at)
		VALUES (?, ?, ?, ?)
	`, "profile-legacy-agent", legacyJSON, 7, formatTime(time.Now().UTC())); err != nil {
		t.Fatalf("insert legacy profile: %v", err)
	}

	response, err := service.GetNodeProfile("profile-legacy-agent")
	if err != nil {
		t.Fatalf("GetNodeProfile legacy row: %v", err)
	}
	if response.Desired.Revision != 7 || response.Desired.Profile == nil || !slices.Equal(response.Desired.Profile.DeployWriteRoots, []string{"/srv/a", "/srv/z"}) {
		t.Fatalf("legacy profile response = %#v", response.Desired)
	}
}

func TestProfileApplyQueuesCanonicalPayloadAndUsesHeartbeatObservation(t *testing.T) {
	service, db, now := newTestService(t)
	registered := registerTestNode(t, service, "profile-apply-agent")
	if _, err := service.Heartbeat("profile-apply-agent", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "profile-apply-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityInventory, CapabilityProfileApply},
		Hostname:        "profile-apply-agent.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("initial heartbeat: %v", err)
	}
	profile := AgentProfile{
		AllowDeployRead:  true,
		DeployPlansFile:  "/etc/hserver/deploy-plans.json",
		DeployWriteRoots: []string{"/srv/z", "/srv/a"},
	}
	if _, err := service.UpdateNodeProfile("profile-apply-agent", profile, 0); err != nil {
		t.Fatalf("UpdateNodeProfile: %v", err)
	}

	task, err := service.ApplyNodeProfile("profile-apply-agent", 1)
	if err != nil {
		t.Fatalf("ApplyNodeProfile: %v", err)
	}
	if task == nil || task.Kind != TaskProfileApply || task.Status != TaskStatusQueued {
		t.Fatalf("apply task = %#v", task)
	}
	if len(task.Payload) != 2 || task.Payload["revision"] != "1" || task.Payload["profile_json_b64"] == "" {
		t.Fatalf("apply payload = %#v, want exactly revision/profile_json_b64", task.Payload)
	}
	profileJSON, err := base64.StdEncoding.DecodeString(task.Payload["profile_json_b64"])
	if err != nil {
		t.Fatalf("decode profile payload: %v", err)
	}
	canonical, err := json.Marshal(profileApplyDocument{
		SchemaVersion: profileApplySchemaVersion,
		Revision:      1,
		Profile:       AgentProfile{AllowDeployRead: true, DeployPlansFile: "/etc/hserver/deploy-plans.json", DeployWriteRoots: []string{"/srv/a", "/srv/z"}},
	})
	if err != nil || string(profileJSON) != string(canonical) {
		t.Fatalf("profile JSON = %s, want canonical %s", profileJSON, canonical)
	}

	coalesced, err := service.ApplyNodeProfile("profile-apply-agent", 1)
	if err != nil || coalesced == nil || coalesced.ID != task.ID {
		t.Fatalf("queued coalesce = %#v, err=%v", coalesced, err)
	}
	claimed, err := service.PollTask("profile-apply-agent", registered.Token)
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim profile task = %#v, err=%v", claimed, err)
	}
	running, err := service.ApplyNodeProfile("profile-apply-agent", 1)
	if err != nil || running == nil || running.ID != task.ID {
		t.Fatalf("running coalesce = %#v, err=%v", running, err)
	}
	if _, err := service.CompleteTask("profile-apply-agent", registered.Token, task.ID, TaskResultRequest{
		Status: TaskStatusCompleted,
		Result: map[string]string{"state": ProfileApplyResultRestartScheduled, "revision": "1"},
	}); err != nil {
		t.Fatalf("complete profile task: %v", err)
	}
	awaiting, err := service.GetNodeProfile("profile-apply-agent")
	if err != nil {
		t.Fatalf("GetNodeProfile awaiting heartbeat: %v", err)
	}
	if awaiting.Apply.State != profileApplyAwaiting || awaiting.Apply.TaskID == nil || *awaiting.Apply.TaskID != task.ID {
		t.Fatalf("completed task state = %#v, want awaiting_heartbeat", awaiting.Apply)
	}
	awaitingAgain, err := service.ApplyNodeProfile("profile-apply-agent", 1)
	if err != nil || awaitingAgain == nil || awaitingAgain.ID != task.ID {
		t.Fatalf("awaiting apply coalesce = %#v, err=%v; want original task", awaitingAgain, err)
	}

	observed := AgentProfileObservation{State: ProfileObservationApplied, Revision: 1}
	if _, err := service.Heartbeat("profile-apply-agent", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "profile-apply-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityInventory, CapabilityProfileApply},
		Hostname:        "profile-apply-agent.example",
		SentAt:          now,
		Profile:         &observed,
	}); err != nil {
		t.Fatalf("applied heartbeat: %v", err)
	}
	applied, err := service.GetNodeProfile("profile-apply-agent")
	if err != nil {
		t.Fatalf("GetNodeProfile applied: %v", err)
	}
	if applied.Observed.ProfileState != ProfileObservationApplied || applied.Observed.ProfileRevision == nil || *applied.Observed.ProfileRevision != 1 || applied.Apply.State != profileApplyApplied {
		t.Fatalf("applied profile state = %#v", applied)
	}
	if again, err := service.ApplyNodeProfile("profile-apply-agent", 1); err != nil || again != nil {
		t.Fatalf("already applied result = %#v, err=%v; want nil task", again, err)
	}

	// An additive heartbeat without the profile capability/observation must
	// clear the prior applied row rather than leave stale applied state.
	if _, err := service.Heartbeat("profile-apply-agent", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "profile-apply-agent",
		AgentVersion:    "legacy-agent",
		Capabilities:    []string{CapabilityInventory},
		Hostname:        "profile-apply-agent.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("legacy heartbeat: %v", err)
	}
	cleared, err := service.GetNodeProfile("profile-apply-agent")
	if err != nil {
		t.Fatalf("GetNodeProfile after legacy heartbeat: %v", err)
	}
	if cleared.Observed.ProfileState != profileObservedNotReported || cleared.Observed.ProfileRevision != nil || cleared.Apply.State == profileApplyApplied {
		t.Fatalf("stale applied observation survived: %#v", cleared)
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_node_profile_observations WHERE node_id = ?`, "profile-apply-agent").Scan(&rows); err != nil {
		t.Fatalf("observation count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("legacy heartbeat left %d observation rows", rows)
	}
}

func TestProfileApplyRejectsFailuresWithoutCreatingTasks(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "profile-apply-failure-agent")
	countTasks := func() int {
		t.Helper()
		tasks, err := service.ListTasksForNode("profile-apply-failure-agent", 50)
		if err != nil {
			t.Fatalf("ListTasksForNode: %v", err)
		}
		return len(tasks)
	}
	if _, err := service.ApplyNodeProfile("profile-apply-failure-agent", 1); !errors.Is(err, ErrProfileNotConfigured) {
		t.Fatalf("missing desired profile error = %v", err)
	}
	if countTasks() != 0 {
		t.Fatal("missing desired profile created task")
	}
	if _, err := service.Heartbeat("profile-apply-failure-agent", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "profile-apply-failure-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityInventory},
		Hostname:        "profile-apply-failure-agent.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := service.UpdateNodeProfile("profile-apply-failure-agent", AgentProfile{}, 0); err != nil {
		t.Fatalf("UpdateNodeProfile: %v", err)
	}
	if _, err := service.ApplyNodeProfile("profile-apply-failure-agent", 1); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("missing capability error = %v", err)
	}
	if countTasks() != 0 {
		t.Fatal("missing capability created task")
	}
	if _, err := service.EnqueueTask("profile-apply-failure-agent", TaskRequest{Kind: TaskProfileApply, Payload: map[string]string{}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("generic profile task error = %v, want ErrInvalidInput", err)
	}
	if countTasks() != 0 {
		t.Fatal("generic profile task rejection created task")
	}
}

func TestProfileApplyRejectsRawTaskErrorsAndDifferentInFlightRevision(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "profile-apply-result-agent")
	if _, err := service.Heartbeat("profile-apply-result-agent", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "profile-apply-result-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityProfileApply},
		Hostname:        "profile-apply-result-agent.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := service.UpdateNodeProfile("profile-apply-result-agent", AgentProfile{}, 0); err != nil {
		t.Fatalf("UpdateNodeProfile rev1: %v", err)
	}
	first, err := service.ApplyNodeProfile("profile-apply-result-agent", 1)
	if err != nil {
		t.Fatalf("ApplyNodeProfile rev1: %v", err)
	}
	if _, err := service.PollTask("profile-apply-result-agent", registered.Token); err != nil {
		t.Fatalf("poll rev1: %v", err)
	}
	if _, err := service.UpdateNodeProfile("profile-apply-result-agent", AgentProfile{AllowDeployRead: true}, 1); err != nil {
		t.Fatalf("UpdateNodeProfile rev2: %v", err)
	}
	if _, err := service.ApplyNodeProfile("profile-apply-result-agent", 2); !errors.Is(err, ErrProfileApplyInFlight) {
		t.Fatalf("different in-flight revision error = %v", err)
	}
	if _, err := service.CompleteTask("profile-apply-result-agent", registered.Token, first.ID, TaskResultRequest{Status: TaskStatusFailed, Error: "/etc/private/profile.env"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("raw profile task error = %v, want ErrInvalidInput", err)
	}
}

func TestProfileApplyResultRequiresAgentAcknowledgementShape(t *testing.T) {
	service, _, now := newTestService(t)
	registered := registerTestNode(t, service, "profile-apply-result-shape-agent")
	if _, err := service.Heartbeat("profile-apply-result-shape-agent", registered.Token, HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		NodeID:          "profile-apply-result-shape-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{CapabilityInventory, CapabilityProfileApply},
		Hostname:        "profile-apply-result-shape.example",
		SentAt:          now,
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := service.UpdateNodeProfile("profile-apply-result-shape-agent", AgentProfile{}, 0); err != nil {
		t.Fatalf("UpdateNodeProfile: %v", err)
	}
	task, err := service.ApplyNodeProfile("profile-apply-result-shape-agent", 1)
	if err != nil {
		t.Fatalf("ApplyNodeProfile: %v", err)
	}
	if _, err := service.PollTask("profile-apply-result-shape-agent", registered.Token); err != nil {
		t.Fatalf("PollTask: %v", err)
	}
	for _, result := range []TaskResultRequest{
		{Status: TaskStatusCompleted, Result: map[string]string{"revision": "1"}},
		{Status: TaskStatusCompleted, Result: map[string]string{"state": ProfileObservationApplied, "revision": "1"}},
		{Status: TaskStatusCompleted, Result: map[string]string{"state": ProfileApplyResultRestartScheduled, "revision": "2"}},
	} {
		if err := ValidateProfileApplyTaskResult(*task, result); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("result %#v validation error=%v, want ErrInvalidInput", result, err)
		}
	}
}

func profileTestRoots(count int) []string {
	roots := make([]string, count)
	for i := range roots {
		roots[i] = fmt.Sprintf("/srv/profile-%d", i)
	}
	return roots
}
