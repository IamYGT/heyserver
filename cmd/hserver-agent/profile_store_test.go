package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestProfileWrapperStrictAndCanonical(t *testing.T) {
	valid := `{"schema_version":1,"revision":4,"profile":{"allowDeployRead":true,"allowDeployActions":true,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"/srv/plans.json","deployAcmeWebroot":"/srv/acme","deployWriteRoots":["/srv/a","/srv/z"]}}`
	wrapper, err := decodeProfileWrapper([]byte(valid))
	if err != nil {
		t.Fatalf("decodeProfileWrapper() error = %v", err)
	}
	if wrapper.Revision != 4 || !reflect.DeepEqual(wrapper.Profile.DeployWriteRoots, []string{"/srv/a", "/srv/z"}) {
		t.Fatalf("decoded wrapper = %#v", wrapper)
	}
	canonical, err := marshalProfileWrapper(wrapper)
	if err != nil {
		t.Fatalf("marshalProfileWrapper() error = %v", err)
	}
	if string(canonical) != `{"schema_version":1,"revision":4,"profile":{"allowDeployRead":true,"allowDeployActions":true,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"/srv/plans.json","deployAcmeWebroot":"/srv/acme","deployWriteRoots":["/srv/a","/srv/z"]}}` {
		t.Fatalf("canonical wrapper = %q", canonical)
	}

	for _, input := range []string{
		`{"schema_version":1,"revision":4,"profile":{},"extra":true}`,
		`{"schema_version":1,"revision":4,"revision":5,"profile":{}}`,
		`{"schema_version":2,"revision":4,"profile":{}}`,
		` {"schema_version":1,"revision":4,"profile":{"allowDeployRead":true,"allowDeployActions":true,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"/srv/plans.json","deployAcmeWebroot":"/srv/acme","deployWriteRoots":["/srv/a","/srv/z"]}} `,
		`{"schema_version":1,"revision":4,"profile":{"allowDeployRead":true,"allowDeployActions":true,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"/srv/plans.json","deployAcmeWebroot":"/srv/acme","deployWriteRoots":["/srv/z","/srv/a"]}}`,
	} {
		if _, err := decodeProfileWrapper([]byte(input)); err == nil {
			t.Fatalf("decodeProfileWrapper(%s) unexpectedly succeeded", input)
		}
	}
}

func TestWriteAtomicProfileFileUsesPrivateDurablePublication(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	filename := filepath.Join(directory, "active.json")
	if err := writeAtomicProfileFile(filename, []byte("profile\n")); err != nil {
		t.Fatalf("writeAtomicProfileFile() error = %v", err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("profile directory: %v", err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("profile directory mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("profile file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("profile file mode = %o, want 600", got)
	}
	if files, err := os.ReadDir(directory); err != nil {
		t.Fatalf("read profile directory: %v", err)
	} else if len(files) != 1 || files[0].Name() != "active.json" {
		t.Fatalf("profile directory entries = %#v, want only active.json", files)
	}
}

func TestProfileStoreMissingActiveFallsBackWithoutError(t *testing.T) {
	store := newProfileStore(filepath.Join(t.TempDir(), "state"))
	_, observation, err := store.loadActiveProfile()
	if !errors.Is(err, errProfileMissing) {
		t.Fatalf("loadActiveProfile() error = %v, want errProfileMissing", err)
	}
	if observation.State != profileObservationMissing || observation.Revision != 0 || observation.ErrorCode != "" {
		t.Fatalf("missing profile observation = %#v", observation)
	}
}

func TestMissingProfileObservationIsNotConfiguredOnHeartbeat(t *testing.T) {
	store := newProfileStore(filepath.Join(t.TempDir(), "state"))
	_, observation, err := store.loadActiveProfile()
	if !errors.Is(err, errProfileMissing) {
		t.Fatalf("loadActiveProfile() error = %v, want errProfileMissing", err)
	}
	request := agenthub.HeartbeatRequest{}
	attachProfileObservation(&request, observation)
	if request.Profile == nil || request.Profile.State != agenthub.ProfileObservationNotConfigured || request.Profile.Revision != 0 || request.Profile.ErrorCode != "" {
		t.Fatalf("missing profile heartbeat observation = %#v", request.Profile)
	}
}

func TestProfileStoreReportsPendingCandidateBeforeFirstActivation(t *testing.T) {
	store := newProfileStore(filepath.Join(t.TempDir(), "state"))
	if err := store.ensureDirectory(); err != nil {
		t.Fatalf("ensure profile directory: %v", err)
	}
	if err := store.writeState(profileStateDocument{SchemaVersion: profileSchemaVersion, State: profileStatePendingRestart, Revision: 3}); err != nil {
		t.Fatalf("write pending state: %v", err)
	}
	_, observation, err := store.loadActiveProfile()
	if err != nil || observation.State != profileObservationPending || observation.Revision != 3 || observation.ErrorCode != "" {
		t.Fatalf("pending candidate observation = %#v, err=%v", observation, err)
	}
}

func TestAgentStartupOverlaysActiveProfileAndFallsBackOnCorruption(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	store := newProfileStore(stateDir)
	if err := store.ensureDirectory(); err != nil {
		t.Fatalf("ensure profile directory: %v", err)
	}
	active := profileWrapper{
		SchemaVersion: profileSchemaVersion,
		Revision:      12,
		Profile: agenthub.AgentProfile{
			AllowDeployActions:       false,
			AllowDeployDomainRead:    false,
			AllowDeployDomainActions: false,
			DeployPlansFile:          "/profile/plans.json",
			DeployAcmeWebroot:        "/profile/acme",
			DeployWriteRoots:         []string{"/profile/releases"},
		},
	}
	if err := store.writeWrapper(store.activePath(), active); err != nil {
		t.Fatalf("write active profile: %v", err)
	}
	env := map[string]string{
		"HSERVER_AGENT_HUB_URL":              "https://hub.invalid",
		"HSERVER_AGENT_NODE_ID":              "profile-node",
		"HSERVER_AGENT_TOKEN":                "token",
		"HSERVER_AGENT_ALLOW_DEPLOY_READ":    "true",
		"HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS": "true",
		"HSERVER_AGENT_DEPLOY_PLANS_FILE":    "/env/plans.json",
		"HSERVER_AGENT_DEPLOY_ACME_WEBROOT":  "/env/acme",
		"HSERVER_AGENT_DEPLOY_WRITE_ROOTS":   "/env/releases",
		"HSERVER_AGENT_STATE_DIR":            stateDir,
	}
	lookup := func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}
	cfg, err := loadConfigFromEnv(lookup, os.ReadFile)
	if err != nil {
		t.Fatalf("loadConfigFromEnv() with active profile: %v", err)
	}
	if cfg.allowDeployRead || cfg.allowDeployActions || cfg.deployPlansPath != "/profile/plans.json" || cfg.deployACMEWebroot != "/profile/acme" || !reflect.DeepEqual(cfg.deployWriteRoots, []string{"/profile/releases"}) {
		t.Fatalf("active profile did not overlay deployment fields: %#v", cfg)
	}
	if cfg.profileObservation.State != profileObservationActive || cfg.profileObservation.Revision != 12 || cfg.profileObservation.ErrorCode != "" {
		t.Fatalf("active profile observation = %#v", cfg.profileObservation)
	}

	if err := os.WriteFile(store.activePath(), []byte("not-json\n"), 0o600); err != nil {
		t.Fatalf("corrupt active profile: %v", err)
	}
	fallback, err := loadConfigFromEnv(lookup, os.ReadFile)
	if err != nil {
		t.Fatalf("loadConfigFromEnv() with corrupt profile: %v", err)
	}
	if !fallback.allowDeployRead || !fallback.allowDeployActions || fallback.deployPlansPath != "/env/plans.json" || fallback.deployACMEWebroot != "/env/acme" || !reflect.DeepEqual(fallback.deployWriteRoots, []string{"/env/releases"}) {
		t.Fatalf("corrupt profile did not preserve environment fallback: %#v", fallback)
	}
	if fallback.profileObservation.State != profileObservationFailed || fallback.profileObservation.ErrorCode != profileErrorCorrupt {
		t.Fatalf("corrupt profile observation = %#v", fallback.profileObservation)
	}

	if err := store.writeWrapper(store.activePath(), active); err != nil {
		t.Fatalf("restore active profile: %v", err)
	}
	if err := os.WriteFile(store.statePath(), []byte(`{"schema_version":1,"state":"active","revision":12,"error_code":"unexpected"}`), 0o600); err != nil {
		t.Fatalf("corrupt profile state: %v", err)
	}
	stateFallback, err := loadConfigFromEnv(lookup, os.ReadFile)
	if err != nil {
		t.Fatalf("loadConfigFromEnv() with corrupt profile state: %v", err)
	}
	if !stateFallback.allowDeployRead || stateFallback.deployPlansPath != "/env/plans.json" || stateFallback.profileObservation.State != profileObservationFailed || stateFallback.profileObservation.ErrorCode != profileErrorStateCorrupt {
		t.Fatalf("corrupt profile state fallback = %#v", stateFallback)
	}
}

func profileTestWrapper(t *testing.T, revision int64) profileWrapper {
	t.Helper()
	profile := agenthub.AgentProfile{
		AllowDeployRead:  true,
		DeployPlansFile:  "/srv/plans.json",
		DeployWriteRoots: []string{"/srv/releases"},
	}
	return profileWrapper{SchemaVersion: profileSchemaVersion, Revision: revision, Profile: profile}
}

func profileTestPayload(t *testing.T, wrapper profileWrapper) map[string]string {
	t.Helper()
	data, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatalf("marshal profile test wrapper: %v", err)
	}
	return map[string]string{
		"revision":         formatProfileRevision(wrapper.Revision),
		"profile_json_b64": encodeProfileJSON(data),
	}
}

func formatProfileRevision(revision int64) string {
	return strconv.FormatInt(revision, 10)
}

func encodeProfileJSON(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
