package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRunNodeProfileGetEscapesNodeAndPreservesAuth(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("profile-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.RequestURI != "/api/nodes/edge%20west/profile" {
			t.Errorf("request = %s %s", r.Method, r.RequestURI)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer profile-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"desired":{"state":"not_configured","revision":0,"profile":null},"observed":{},"apply":{}}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"--token-file", tokenFile,
		"nodes", "profile", "get", "edge west",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"state": "not_configured"`) || !strings.Contains(out.String(), `"profile": null`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunNodeProfileSetUsesFreshRevisionAndCAS(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("profile-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := cliNodeProfile{
		AllowDeployRead:          true,
		AllowDeployActions:       true,
		AllowDeployDomainRead:    true,
		AllowDeployDomainActions: true,
		DeployPlansFile:          "/etc/hserver/plans.json",
		DeployAcmeWebroot:        "/srv/acme",
		DeployWriteRoots:         []string{"/srv/apps", "/srv/sites"},
	}
	profilePath := filepath.Join(t.TempDir(), "profile.json")
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, profileJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/nodes/edge%20west/profile" {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer profile-token" {
			t.Errorf("Authorization = %q", got)
		}
		mu.Lock()
		requests = append(requests, r.Method)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"desired":{"state":"configured","revision":7,"profile":{"allowDeployRead":false,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":[]}},"observed":{},"apply":{}}`))
		case http.MethodPut:
			var payload cliNodeProfilePutRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			if payload.ExpectedRevision != 7 {
				t.Errorf("expectedRevision = %d", payload.ExpectedRevision)
			}
			if !reflect.DeepEqual(payload.Profile, profile) {
				t.Errorf("profile payload = %#v", payload.Profile)
			}
			_, _ = w.Write([]byte(`{"desired":{"state":"configured","revision":8,"profile":{"allowDeployRead":true,"allowDeployActions":true,"allowDeployDomainRead":true,"allowDeployDomainActions":true,"deployPlansFile":"/etc/hserver/plans.json","deployAcmeWebroot":"/srv/acme","deployWriteRoots":["/srv/apps","/srv/sites"]}},"observed":{},"apply":{}}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	err = run(context.Background(), []string{
		"--server", server.URL,
		"--token-file", tokenFile,
		"nodes", "profile", "set", "--confirm", "--profile-file", profilePath, "edge west",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotRequests := append([]string(nil), requests...)
	mu.Unlock()
	if fmt.Sprint(gotRequests) != "[GET PUT]" {
		t.Fatalf("request methods = %v", gotRequests)
	}
	if !strings.Contains(out.String(), `"revision": 8`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunNodeProfileSetRejectsInvalidFileBeforeHTTP(t *testing.T) {
	t.Parallel()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	validProfile := `{"allowDeployRead":false,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":[]}`
	tests := []struct {
		name string
		body string
		args []string
	}{
		{name: "unknown field", body: strings.TrimSuffix(validProfile, "}") + `,"unexpected":true}`, args: []string{"--confirm"}},
		{name: "missing field", body: `{"allowDeployRead":false}`, args: []string{"--confirm"}},
		{name: "trailing json", body: validProfile + ` {}`, args: []string{"--confirm"}},
		{name: "null field", body: strings.Replace(validProfile, `"deployPlansFile":""`, `"deployPlansFile":null`, 1), args: []string{"--confirm"}},
		{name: "string roots", body: strings.Replace(validProfile, `"deployWriteRoots":[]`, `"deployWriteRoots":"/srv/apps"`, 1), args: []string{"--confirm"}},
		{name: "empty root", body: strings.Replace(validProfile, `"deployWriteRoots":[]`, `"deployWriteRoots":[""]`, 1), args: []string{"--confirm"}},
		{name: "duplicate roots", body: strings.Replace(validProfile, `"deployWriteRoots":[]`, `"deployWriteRoots":["/srv/apps","/srv/apps"]`, 1), args: []string{"--confirm"}},
		{name: "relative root", body: strings.Replace(validProfile, `"deployWriteRoots":[]`, `"deployWriteRoots":["srv/apps"]`, 1), args: []string{"--confirm"}},
		{name: "space in path", body: strings.Replace(validProfile, `"deployPlansFile":""`, `"deployPlansFile":"/srv/deploy plans.json"`, 1), args: []string{"--confirm"}},
		{name: "unicode path", body: strings.Replace(validProfile, `"deployPlansFile":""`, `"deployPlansFile":"/srv/dağıtım.json"`, 1), args: []string{"--confirm"}},
		{name: "percent path", body: strings.Replace(validProfile, `"deployPlansFile":""`, `"deployPlansFile":"/srv/deploy%20plans.json"`, 1), args: []string{"--confirm"}},
		{name: "dependency", body: `{"allowDeployRead":false,"allowDeployActions":true,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":[]}`, args: []string{"--confirm"}},
		{name: "confirmation", body: validProfile, args: nil},
	}
	tooManyRoots := make([]string, maxNodeProfileWriteRoots+1)
	for index := range tooManyRoots {
		tooManyRoots[index] = fmt.Sprintf("/srv/profile-%d", index)
	}
	tooManyProfile := cliNodeProfile{
		DeployPlansFile:   "",
		DeployAcmeWebroot: "",
		DeployWriteRoots:  tooManyRoots,
	}
	tooManyJSON, err := json.Marshal(tooManyProfile)
	if err != nil {
		t.Fatal(err)
	}
	tests = append(tests, struct {
		name string
		body string
		args []string
	}{name: "too many roots", body: string(tooManyJSON), args: []string{"--confirm"}})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profilePath := filepath.Join(t.TempDir(), "profile.json")
			if err := os.WriteFile(profilePath, []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--server", server.URL, "nodes", "profile", "set"}
			args = append(args, test.args...)
			args = append(args, "--profile-file", profilePath, "edge-1")
			if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" }); err == nil {
				t.Fatal("expected local validation error")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("invalid input sent %d HTTP requests", requests)
	}

	overSizePath := filepath.Join(t.TempDir(), "oversize.json")
	if err := os.WriteFile(overSizePath, bytes.Repeat([]byte("x"), maxNodeProfileFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{
		"--server", server.URL,
		"nodes", "profile", "set", "--confirm", "--profile-file", overSizePath, "edge-1",
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" }); err == nil {
		t.Fatal("expected oversized file error")
	}
	if requests != 0 {
		t.Fatalf("oversized input sent %d HTTP requests", requests)
	}
}

func TestReadCLINodeProfileFileUsesInclusive16KiBBoundary(t *testing.T) {
	valid := []byte(`{"allowDeployRead":false,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":[]}`)
	boundary := append(append([]byte{}, valid...), bytes.Repeat([]byte(" "), maxNodeProfileFileBytes-len(valid))...)
	boundaryPath := filepath.Join(t.TempDir(), "boundary.json")
	if err := os.WriteFile(boundaryPath, boundary, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCLINodeProfileFile(boundaryPath); err != nil {
		t.Fatalf("read exact %d-byte profile file: %v", maxNodeProfileFileBytes, err)
	}

	oversizePath := filepath.Join(t.TempDir(), "oversize.json")
	if err := os.WriteFile(oversizePath, append(boundary, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCLINodeProfileFile(oversizePath); err == nil || !strings.Contains(err.Error(), "exceeds 16384 bytes") {
		t.Fatalf("oversized profile error = %v, want 16384-byte rejection", err)
	}

	if err := validateCLIProfilePath("/"+strings.Repeat("a", maxNodeProfilePathBytes-1), "deployAcmeWebroot", false); err != nil {
		t.Fatalf("inclusive %d-byte path: %v", maxNodeProfilePathBytes, err)
	}
}

func TestRunNodeProfileExportWritesExactlySevenEnvLines(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("profile-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := cliNodeProfile{
		AllowDeployRead:          true,
		AllowDeployActions:       false,
		AllowDeployDomainRead:    true,
		AllowDeployDomainActions: false,
		DeployPlansFile:          "/etc/hserver/plans.json",
		DeployAcmeWebroot:        "/srv/acme",
		DeployWriteRoots:         []string{"/srv/apps", "/srv/sites"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/nodes/edge-1/profile" {
			t.Errorf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(cliNodeProfileResponse{Desired: cliNodeProfileDesired{
			State: "configured", Revision: int64PtrForNodeProfileTest(3), Profile: &profile,
		}})
		_, _ = w.Write(body)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"--token-file", tokenFile,
		"nodes", "profile", "export", "edge-1", "--format", "env-fragment",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	want := "HSERVER_AGENT_ALLOW_DEPLOY_READ=true\n" +
		"HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=false\n" +
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=true\n" +
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=false\n" +
		"HSERVER_AGENT_DEPLOY_PLANS_FILE=/etc/hserver/plans.json\n" +
		"HSERVER_AGENT_DEPLOY_ACME_WEBROOT=/srv/acme\n" +
		"HSERVER_AGENT_DEPLOY_WRITE_ROOTS=/srv/apps,/srv/sites\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestRunNodeProfileExportRejectsNotConfiguredOrNull(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "not configured",
			body: `{"desired":{"state":"not_configured","revision":0,"profile":null}}`,
			want: "not configured",
		},
		{
			name: "null configured profile",
			body: `{"desired":{"state":"configured","revision":1,"profile":null}}`,
			want: "null profile",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokenFile := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(tokenFile, []byte("profile-token\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			var out bytes.Buffer
			err := run(context.Background(), []string{
				"--server", server.URL,
				"--token-file", tokenFile,
				"nodes", "profile", "export", "edge-1",
			}, &out, &bytes.Buffer{}, func(string) string { return "" })
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if out.Len() != 0 {
				t.Fatalf("output = %q", out.String())
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
}

func int64PtrForNodeProfileTest(value int64) *int64 { return &value }

func TestRunNodeProfileApplyUsesFreshRevisionEscapedPathAuthAndExactBody(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("profile-apply-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var getRequests, postRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "/api/nodes/edge%20west/profile" && r.RequestURI != "/api/nodes/edge%20west/profile/apply" {
			t.Errorf("request URI = %q", r.RequestURI)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer profile-apply-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			getRequests++
			_, _ = w.Write(nodeProfileApplyResponseFixture("edge west", "configured", 7, true, []string{"agent.profile.apply"}, "not_requested"))
		case http.MethodPost:
			postRequests++
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode apply body: %v", err)
			}
			if len(body) != 2 || string(body["expectedRevision"]) != "7" || string(body["confirmed"]) != "true" {
				t.Errorf("apply body = %s", mustCompactJSONForNodeProfileTest(body))
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"receipt":"raw-queued","apply":{"state":"queued","reason":""}}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"--token-file", tokenFile,
		"nodes", "profile", "apply", "--confirm", "edge west",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if getRequests != 1 || postRequests != 1 {
		t.Fatalf("requests = GET %d POST %d, want GET 1 POST 1", getRequests, postRequests)
	}
	if !strings.Contains(out.String(), `"receipt": "raw-queued"`) || !strings.Contains(out.String(), `"state": "queued"`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunNodeProfileApplyRejectsUnsafeFreshStatesBeforePOST(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		desiredState string
		revision     int64
		online       bool
		capabilities []string
		applyState   string
		legacyRaw    bool
	}{
		{name: "not configured", desiredState: "not_configured", revision: 0, online: true, capabilities: []string{"agent.profile.apply"}, applyState: "not_requested"},
		{name: "zero revision", desiredState: "configured", revision: 0, online: true, capabilities: []string{"agent.profile.apply"}, applyState: "not_requested"},
		{name: "offline", desiredState: "configured", revision: 7, online: false, capabilities: []string{"agent.profile.apply"}, applyState: "not_requested"},
		{name: "capability missing", desiredState: "configured", revision: 7, online: true, capabilities: []string{"inventory"}, applyState: "not_requested"},
		{name: "phase one manual required", desiredState: "configured", revision: 7, online: true, capabilities: []string{"agent.profile.apply"}, applyState: "manual_required"},
		{name: "queued conflict", desiredState: "configured", revision: 7, online: true, capabilities: []string{"agent.profile.apply"}, applyState: "queued"},
		{name: "running conflict", desiredState: "configured", revision: 7, online: true, capabilities: []string{"agent.profile.apply"}, applyState: "running"},
		{name: "heartbeat conflict", desiredState: "configured", revision: 7, online: true, capabilities: []string{"agent.profile.apply"}, applyState: "awaiting_heartbeat"},
		{name: "missing required field", desiredState: "configured", revision: 7, online: true, capabilities: []string{"agent.profile.apply"}, applyState: "not_requested", legacyRaw: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tokenFile := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(tokenFile, []byte("profile-apply-token\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var postRequests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					postRequests++
				}
				w.Header().Set("Content-Type", "application/json")
				if test.legacyRaw {
					_, _ = w.Write([]byte(`{"nodeId":"edge-1","desired":{"state":"configured","revision":7,"profile":{"allowDeployRead":false,"allowDeployActions":false,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"","deployAcmeWebroot":"","deployWriteRoots":[]}},"observed":{"online":true,"lastSeenAt":null,"agentVersion":"v2","protocolVersion":"v1","profileState":"observed"},"apply":{"state":"not_requested","reason":""}}`))
					return
				}
				_, _ = w.Write(nodeProfileApplyResponseFixture("edge-1", test.desiredState, test.revision, test.online, test.capabilities, test.applyState))
			}))
			defer server.Close()

			err := run(context.Background(), []string{
				"--server", server.URL,
				"--token-file", tokenFile,
				"nodes", "profile", "apply", "--confirm", "edge-1",
			}, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" })
			if err == nil {
				t.Fatal("expected preflight rejection")
			}
			if postRequests != 0 {
				t.Fatalf("POST requests = %d, want 0", postRequests)
			}
		})
	}
}

func TestRunNodeProfileApplyWaitsForServerReportedTerminalState(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("profile-apply-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var getRequests, postRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			getRequests++
			state := "not_requested"
			switch getRequests {
			case 2:
				state = "running"
			case 3:
				state = "applied"
			}
			_, _ = w.Write(nodeProfileApplyResponseFixture("edge-1", "configured", 7, true, []string{"agent.profile.apply"}, state))
		case http.MethodPost:
			postRequests++
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"receipt":"queued","apply":{"state":"queued","reason":""}}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"--token-file", tokenFile,
		"nodes", "profile", "apply", "--confirm", "--wait", "1s", "edge-1",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if getRequests != 3 || postRequests != 1 {
		t.Fatalf("requests = GET %d POST %d, want GET 3 POST 1", getRequests, postRequests)
	}
	if !strings.Contains(out.String(), `"state": "applied"`) {
		t.Fatalf("output = %q", out.String())
	}
}

func nodeProfileApplyResponseFixture(nodeID, desiredState string, revision int64, online bool, capabilities []string, applyState string) []byte {
	var profile *cliNodeProfile
	if desiredState == "configured" {
		profileValue := cliNodeProfile{DeployWriteRoots: []string{}}
		profile = &profileValue
	}
	response := cliNodeProfileResponse{
		NodeID: nodeID,
		Desired: cliNodeProfileDesired{
			State: desiredState, Revision: int64PtrForNodeProfileTest(revision), Profile: profile,
		},
		Observed: cliNodeProfileObserved{
			Capabilities: capabilities, Online: online, LastSeenAt: json.RawMessage("null"),
			AgentVersion: "v2", ProtocolVersion: "v1", ProfileState: "observed",
		},
		Apply: cliNodeProfileApply{State: applyState, Reason: ""},
	}
	body, _ := json.Marshal(response)
	return body
}

func mustCompactJSONForNodeProfileTest(body map[string]json.RawMessage) string {
	encoded, _ := json.Marshal(body)
	return string(encoded)
}
