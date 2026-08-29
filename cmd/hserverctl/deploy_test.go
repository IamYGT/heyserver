package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

func TestRunDeployCommandsUseFixedEndpoints(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	environmentValuePath := filepath.Join(tempDir, "database-url")
	const environmentValue = "postgres://db/app?sslmode=require"
	if err := os.WriteFile(environmentValuePath, []byte(environmentValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/templates":
			_, _ = writer.Write([]byte(`{"status":"healthy","templates":[{"id":"compose","name":"Docker Compose"}],"issues":[]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/targets":
			_, _ = writer.Write([]byte(`[{"id":12,"name":"app"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/targets/12/revision":
			_, _ = writer.Write([]byte(`{"targetId":12,"state":"ready"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/targets/12/preflight":
			_, _ = writer.Write([]byte(`{"targetId":12,"eligible":true,"checks":[]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/targets/12/environment":
			_, _ = writer.Write([]byte(`{"configured":true,"variables":[{"key":"APP_MODE"}]}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/deploy/targets/12/environment":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode environment request: %v", err)
			}
			if body["key"] != "DATABASE_URL" || body["value"] != environmentValue {
				t.Errorf("environment request = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"configured":true,"variables":[{"key":"APP_MODE"},{"key":"DATABASE_URL"}]}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/deploy/targets/12/environment/DATABASE_URL":
			_, _ = writer.Write([]byte(`{"configured":true,"variables":[{"key":"APP_MODE"}]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/targets/12/domains":
			_, _ = writer.Write([]byte(`[{"id":41,"targetId":12,"domain":"app.example.com","service":"web","hostPort":8080,"upstream":"http://127.0.0.1:8080","tlsStatus":"not_configured","tlsMessage":"not configured"}]`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/deploy/targets/12/domains":
			var body struct {
				Domain   string `json:"domain"`
				Service  string `json:"service"`
				HostPort int    `json:"hostPort"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode project domain request: %v", err)
			}
			if body.Domain != "app.example.com" || body.Service != "web" || body.HostPort != 8080 {
				t.Errorf("project domain request = %#v", body)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":41,"targetId":12,"domain":"app.example.com","service":"web","hostPort":8080,"upstream":"http://127.0.0.1:8080","tlsStatus":"not_configured","tlsMessage":"not configured"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/targets/12/domains/41/health":
			_, _ = writer.Write([]byte(`{"domain":"app.example.com","upstream":"http://127.0.0.1:8080","status":"healthy","statusCode":200,"latencyMs":4,"message":"ready","checkedAt":"2026-08-28T12:00:00Z"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/deploy/targets/12/domains/41/tls":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode project domain TLS request: %v", err)
			}
			if body["email"] != "admin@example.com" {
				t.Errorf("project domain TLS request = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"id":41,"targetId":12,"domain":"app.example.com","service":"web","hostPort":8080,"upstream":"http://127.0.0.1:8080","tlsStatus":"healthy","tlsMessage":"valid"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/deploy/targets/12/domains/41/tls":
			_, _ = writer.Write([]byte(`{"id":41,"targetId":12,"domain":"app.example.com","service":"web","hostPort":8080,"upstream":"http://127.0.0.1:8080","tlsStatus":"not_configured","tlsMessage":"not configured"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/api/deploy/targets/12/domains/41":
			writer.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && request.URL.Path == "/api/deploy/targets/12/staging":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode staging request: %v", err)
			}
			if body["name"] != "App Preview" || body["branch"] != "develop" || body["projectDir"] != "/srv/apps/app-preview" {
				t.Errorf("staging request = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"target":{"id":13,"environment":"staging","sourceTargetId":12},"storageBoundary":"isolated_project_directory"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/history":
			if request.URL.Query().Get("targetId") != "12" || request.URL.Query().Get("limit") != "25" {
				t.Errorf("history query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`[{"id":71,"targetId":12,"status":"success"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/history/71/logs":
			_, _ = writer.Write([]byte(`{"logs":"ready"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/targets/12/services":
			_, _ = writer.Write([]byte(`[{"service":"web","state":"running"}]`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/targets/12/services/web/logs":
			if request.URL.Query().Get("tail") != "250" {
				t.Errorf("tail = %q", request.URL.Query().Get("tail"))
			}
			_, _ = writer.Write([]byte(`{"logs":"web ready","tail":250}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/deploy/manual/12":
			_, _ = writer.Write([]byte(`{"message":"deployment queued","runId":72}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/deploy/rollback/12":
			_, _ = writer.Write([]byte(`{"message":"rollback queued","runId":73}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/deploy/targets/12/services/web/recreate":
			_, _ = writer.Write([]byte(`{"status":"ok","service":"web","action":"recreate"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	commands := [][]string{
		{"deploy", "templates"},
		{"deploy", "targets"},
		{"deploy", "revision", "12"},
		{"deploy", "preflight", "12"},
		{"deploy", "environment", "list", "12"},
		{"deploy", "environment", "set", "--confirm", "--value-file", environmentValuePath, "12", "DATABASE_URL"},
		{"deploy", "environment", "delete", "--confirm", "12", "DATABASE_URL"},
		{"deploy", "domains", "12"},
		{"deploy", "domain", "create", "--confirm", "--service", "web", "--host-port", "8080", "12", "app.example.com"},
		{"deploy", "domain", "health", "12", "41"},
		{"deploy", "domain", "tls", "enable", "--confirm", "--email", "admin@example.com", "12", "41"},
		{"deploy", "domain", "tls", "disable", "--confirm", "12", "41"},
		{"deploy", "domain", "delete", "--confirm", "12", "41"},
		{"deploy", "staging", "create", "--confirm", "--name", "App Preview", "--branch", "develop", "--project-dir", "/srv/apps/app-preview", "12"},
		{"deploy", "history", "--target", "12", "--limit", "25"},
		{"deploy", "logs", "71"},
		{"deploy", "services", "12"},
		{"deploy", "service", "logs", "--tail", "250", "12", "web"},
		{"deploy", "run", "--confirm", "12"},
		{"deploy", "rollback", "--confirm", "12"},
		{"deploy", "service", "action", "--confirm", "12", "web", "recreate"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
		if strings.Contains(output.String(), environmentValue) {
			t.Fatalf("%s output disclosed environment value", strings.Join(command, " "))
		}
	}
	if requests.Load() != int32(len(commands)) {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunDeployRemoteCommandsUseEscapedPathsAuthAndEmptyActionBody(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.RequestURI {
		case "GET /api/nodes/edge%20west/deploy":
			_, _ = writer.Write([]byte(`[{"id":"target/blue","name":"Blue","eligible":true,"actions":["preflight"]}]`))
		case "GET /api/nodes/edge%20west/deploy/jobs":
			_, _ = writer.Write([]byte(`[{"id":"job-1","target_id":"target/blue","action":"preflight","status":"queued"}]`))
		case "POST /api/nodes/edge%20west/deploy/target%2Fblue/actions/preflight":
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read action body: %v", err)
			}
			if len(body) != 0 {
				t.Errorf("action body = %q, want empty", body)
			}
			_, _ = writer.Write([]byte(`{"id":"job-2","target_id":"target/blue","action":"preflight","status":"queued","message":"accepted"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	commands := [][]string{
		{"deploy", "remote", "targets", "--node", "edge west"},
		{"deploy", "remote", "jobs", "--node", "edge west"},
		{"deploy", "remote", "action", "--confirm", "--node", "edge west", "target/blue", "preflight"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
	}
	if requests.Load() != int32(len(commands)+1) {
		t.Fatalf("requests = %d, want %d", requests.Load(), len(commands)+1)
	}
}

func TestRunDeployRemoteActionRefreshesInventoryBeforeMutation(t *testing.T) {
	t.Parallel()
	var inventoryReads atomic.Int32
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			posts.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/nodes/edge-1/deploy":
			if inventoryReads.Add(1) == 1 {
				_, _ = writer.Write([]byte(`[{"id":"app","name":"App","eligible":true,"actions":["deploy"]}]`))
				return
			}
			// A changed eligibility observation must block the second mutation;
			// the command may not reuse the first inventory response.
			_, _ = writer.Write([]byte(`[{"id":"app","name":"App","eligible":false,"actions":["deploy"]}]`))
		case "POST /api/nodes/edge-1/deploy/app/actions/deploy":
			_, _ = writer.Write([]byte(`{"id":"job-1","target_id":"app","action":"deploy","status":"queued"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	for index, wantError := range []bool{false, true} {
		var output bytes.Buffer
		args := []string{"--server", server.URL, "deploy", "remote", "action", "--confirm", "--node", "edge-1", "app", "deploy"}
		err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv)
		if wantError {
			if err == nil || !strings.Contains(err.Error(), "not eligible") {
				t.Fatalf("attempt %d error = %v", index+1, err)
			}
			continue
		}
		if err != nil || !json.Valid(output.Bytes()) {
			t.Fatalf("attempt %d output=%q err=%v", index+1, output.String(), err)
		}
	}
	if inventoryReads.Load() != 2 {
		t.Fatalf("inventory reads = %d, want 2", inventoryReads.Load())
	}
	if posts.Load() != 1 {
		t.Fatalf("posts = %d, want 1", posts.Load())
	}
}

func TestRunDeployRemoteActionRejectsBeforePost(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method == http.MethodPost {
			posts.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[{"id":"app","name":"App","eligible":true,"actions":["deploy"]}]`))
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	cases := []struct {
		args  []string
		want  string
		reads int32
	}{
		{args: []string{"deploy", "remote", "action", "--node", "edge-1", "app", "deploy"}, want: "explicit --confirm"},
		{args: []string{"deploy", "remote", "action", "--confirm", "--node", " ", "app", "deploy"}, want: "non-empty --node"},
		{args: []string{"deploy", "remote", "action", "--confirm", "--node", "edge-1", " ", "deploy"}, want: "non-empty target"},
		{args: []string{"deploy", "remote", "action", "--confirm", "--node", "edge-1", "app", "restart-now"}, want: "unsupported remote deployment action", reads: 1},
		{args: []string{"deploy", "remote", "action", "--confirm", "--node", "edge-1", "missing", "deploy"}, want: "not found", reads: 1},
		{args: []string{"deploy", "remote", "action", "--confirm", "--node", "edge-1", "app", "rollback"}, want: "not advertised", reads: 1},
	}
	var totalReads int32
	for _, item := range cases {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v, want %q", strings.Join(item.args, " "), err, item.want)
		}
		totalReads += item.reads
	}
	if requests.Load() != totalReads || posts.Load() != 0 {
		t.Fatalf("requests = %d (want %d), posts = %d", requests.Load(), totalReads, posts.Load())
	}
}

func TestRunDeployRemoteDomainCommandsUseExactRoutesAndFreshInventory(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	var inventoryReads atomic.Int32
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.RequestURI {
		case "GET /api/nodes/edge%20west":
			_, _ = writer.Write([]byte(`{"id":"edge west","online":true,"capabilities":["deploy.domain.read","deploy.domain.action"]}`))
		case "GET /api/nodes/edge%20west/deploy":
			inventoryReads.Add(1)
			_, _ = writer.Write([]byte(`[{"id":"target/blue","name":"Blue","eligible":true}]`))
		case "GET /api/nodes/edge%20west/deploy/target%2Fblue/domains":
			_, _ = writer.Write([]byte(`[{"target_id":"target/blue","domain":"app.example.com","host_port":8080,"desired_host_port":8080,"upstream":"http://127.0.0.1:8080","status":"active"}]`))
		case "POST /api/nodes/edge%20west/deploy/target%2Fblue/domains":
			mutations.Add(1)
			var body remotenodes.CreateRemoteDeployDomainRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode domain-create request: %v", err)
			}
			if body.Domain != "app.example.com" {
				t.Errorf("domain-create request = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"target_id":"target/blue","domain":"app.example.com","host_port":8080,"desired_host_port":8080,"upstream":"http://127.0.0.1:8080","status":"active"}`))
		case "GET /api/nodes/edge%20west/deploy/target%2Fblue/domains/app.example.com/health":
			_, _ = writer.Write([]byte(`{"domain":"app.example.com","upstream":"http://127.0.0.1:8080","status":"healthy","status_code":200}`))
		case "POST /api/nodes/edge%20west/deploy/target%2Fblue/domains/app.example.com/tls":
			mutations.Add(1)
			var body remotenodes.EnableRemoteDeployDomainTLSRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode tls-provision request: %v", err)
			}
			if body.Email != "admin@example.com" {
				t.Errorf("tls-provision request = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"target_id":"target/blue","domain":"app.example.com","host_port":8080,"desired_host_port":8080,"upstream":"http://127.0.0.1:8080","tls_status":"healthy"}`))
		case "POST /api/nodes/edge%20west/deploy/target%2Fblue/domains/app.example.com/tls/renew":
			mutations.Add(1)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read tls-renew body: %v", err)
			}
			if len(body) != 0 {
				t.Errorf("tls-renew body = %q, want empty", body)
			}
			_, _ = writer.Write([]byte(`{"target_id":"target/blue","domain":"app.example.com","tls_status":"healthy"}`))
		case "DELETE /api/nodes/edge%20west/deploy/target%2Fblue/domains/app.example.com/tls":
			mutations.Add(1)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read tls-delete body: %v", err)
			}
			if len(body) != 0 {
				t.Errorf("tls-delete body = %q, want empty", body)
			}
			_, _ = writer.Write([]byte(`{"target_id":"target/blue","domain":"app.example.com","tls_status":"not_configured"}`))
		case "DELETE /api/nodes/edge%20west/deploy/target%2Fblue/domains/app.example.com":
			mutations.Add(1)
			_, _ = writer.Write([]byte(`{"message":"Managed project domain removed"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	commands := [][]string{
		{"deploy", "remote", "domains", "--node", "edge west", "target/blue"},
		{"deploy", "remote", "domain-create", "--confirm", "--node", "edge west", "target/blue", "app.example.com"},
		{"deploy", "remote", "domain-health", "--node", "edge west", "target/blue", "app.example.com"},
		{"deploy", "remote", "tls-provision", "--confirm", "--node", "edge west", "--email", "admin@example.com", "target/blue", "app.example.com"},
		{"deploy", "remote", "tls-renew", "--confirm", "--node", "edge west", "target/blue", "app.example.com"},
		{"deploy", "remote", "tls-delete", "--confirm", "--node", "edge west", "target/blue", "app.example.com"},
		{"deploy", "remote", "domain-delete", "--confirm", "--node", "edge west", "target/blue", "app.example.com"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
	}
	if requests.Load() != 17 {
		t.Fatalf("requests = %d, want 17", requests.Load())
	}
	if inventoryReads.Load() != 5 {
		t.Fatalf("inventory reads = %d, want 5", inventoryReads.Load())
	}
	if mutations.Load() != 5 {
		t.Fatalf("mutations = %d, want 5", mutations.Load())
	}
}

func TestRunDeployRemoteDomainMutationRejectsWithoutConfirmationOrEligibleTarget(t *testing.T) {
	t.Parallel()
	var inventoryReads atomic.Int32
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost || request.Method == http.MethodDelete {
			mutations.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge-1" {
			_, _ = writer.Write([]byte(`{"id":"edge-1","online":true,"capabilities":["deploy.domain.read","deploy.domain.action"]}`))
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge-1/deploy" {
			if inventoryReads.Add(1) == 1 {
				_, _ = writer.Write([]byte(`[{"id":"app","name":"App","eligible":true}]`))
				return
			}
			_, _ = writer.Write([]byte(`[{"id":"app","name":"App","eligible":false}]`))
			return
		}
		_, _ = writer.Write([]byte(`{"id":"job-1","status":"queued"}`))
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	var output bytes.Buffer
	args := []string{"--server", server.URL, "deploy", "remote", "domain-create", "--node", "edge-1", "app", "app.example.com"}
	if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err == nil || !strings.Contains(err.Error(), "explicit --confirm") {
		t.Fatalf("missing confirmation error = %v", err)
	}
	if inventoryReads.Load() != 0 || mutations.Load() != 0 {
		t.Fatalf("missing confirmation issued inventory=%d mutations=%d", inventoryReads.Load(), mutations.Load())
	}
	output.Reset()
	args = []string{"--server", server.URL, "deploy", "remote", "domain-create", "--confirm", "--node", "edge-1", "app", "app.example.com"}
	if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("eligible mutation: %v", err)
	}
	output.Reset()
	if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err == nil || !strings.Contains(err.Error(), "not eligible") {
		t.Fatalf("ineligible mutation error = %v", err)
	}
	if inventoryReads.Load() != 2 {
		t.Fatalf("inventory reads = %d, want 2", inventoryReads.Load())
	}
	if mutations.Load() != 1 {
		t.Fatalf("mutations = %d, want 1", mutations.Load())
	}
}

func TestRunDeployRemoteDomainEnsureUsesFreshInventoryRevisionAndTypedReceipt(t *testing.T) {
	t.Parallel()
	existingRevision := strings.Repeat("a", 64)
	createdRevision := strings.Repeat("b", 64)
	var inventoryReads atomic.Int32
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.RequestURI {
		case "GET /api/nodes/edge%20west/deploy/target%2Fblue/domains":
			if inventoryReads.Add(1) == 1 {
				_, _ = fmt.Fprintf(writer, `[{"domain":"app.example.com","revision":%q,"raw":{"secret":"must-not-leak"}}]`, existingRevision)
				return
			}
			_, _ = writer.Write([]byte(`[]`))
		case "PUT /api/nodes/edge%20west/deploy/target%2Fblue/domains/app.example.com":
			expectedRevision := existingRevision
			if mutations.Add(1) > 1 {
				expectedRevision = "absent"
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read domain-ensure request: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("decode domain-ensure request: %v", err)
			}
			if len(payload) != 2 || payload["expected_revision"] != expectedRevision || payload["confirmed"] != true {
				t.Errorf("domain-ensure request = %s", body)
			}
			if string(body) != fmt.Sprintf(`{"expected_revision":%q,"confirmed":true}`, expectedRevision) {
				t.Errorf("domain-ensure request body = %q", body)
			}
			_, _ = fmt.Fprintf(writer, `{"changed":true,"observation":{"target_id":"target/blue","domain":"app.example.com","host_port":8080,"desired_host_port":8080,"upstream":"http://127.0.0.1:8080","status":"active","enabled":true,"revision":%q,"message":"Observed on managed node","tls_status":"not_configured","tls_message":"TLS is not configured","updated_at":"2026-08-29T12:00:00Z","raw":{"secret":"must-not-leak"}},"raw":{"token":"must-not-leak"}}`, createdRevision)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}

	var output bytes.Buffer
	args := []string{"--server", server.URL, "deploy", "remote", "domain-ensure", "--confirm", "--node", "edge west", "target/blue", "app.example.com"}
	if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("existing domain ensure: %v", err)
	}
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatalf("decode typed receipt: %v; output=%q", err, output.String())
	}
	if len(receipt) != 2 {
		t.Fatalf("receipt fields = %v, want only changed and observation", receipt)
	}
	if _, ok := receipt["raw"]; ok {
		t.Fatalf("receipt leaked top-level raw field: %q", output.String())
	}
	var observation map[string]json.RawMessage
	if err := json.Unmarshal(receipt["observation"], &observation); err != nil {
		t.Fatalf("decode typed observation: %v", err)
	}
	if _, ok := observation["raw"]; ok {
		t.Fatalf("receipt leaked nested raw field: %q", output.String())
	}
	if string(observation["domain"]) != `"app.example.com"` || string(observation["revision"]) != fmt.Sprintf("%q", createdRevision) {
		t.Fatalf("observation = %s", receipt["observation"])
	}

	output.Reset()
	args = []string{"--server", server.URL, "deploy", "remote", "domain-ensure", "--confirm", "--node", "edge west", "--format", "table", "target/blue", "app.example.com"}
	if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("absent domain ensure: %v", err)
	}
	if !strings.Contains(output.String(), "changed\ttrue") {
		t.Fatalf("table receipt = %q", output.String())
	}
	if inventoryReads.Load() != 2 || mutations.Load() != 2 {
		t.Fatalf("inventory reads=%d mutations=%d, want 2 each", inventoryReads.Load(), mutations.Load())
	}
}

func TestRunDeployRemoteDomainEnsureRejectsUnsafeArgsBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"deploy", "remote", "domain-ensure", "--node", "edge-1", "app", "app.example.com"}, want: "explicit --confirm"},
		{args: []string{"deploy", "remote", "domain-ensure", "--confirm", "--node", " ", "app", "app.example.com"}, want: "non-empty --node"},
		{args: []string{"deploy", "remote", "domain-ensure", "--confirm", "--node", "edge-1", "app", "localhost"}, want: "valid ASCII hostname"},
		{args: []string{"deploy", "remote", "domain-ensure", "--confirm", "--node", "edge-1", "--format", "yaml", "app", "app.example.com"}, want: "format must be"},
	}
	for _, item := range cases {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error=%v, want %q", strings.Join(item.args, " "), err, item.want)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid domain-ensure arguments made %d network requests", requests.Load())
	}
}

func TestRunDeployRemoteDomainEnsureDoesNotRetryStaleRevision(t *testing.T) {
	t.Parallel()
	revision := strings.Repeat("c", 64)
	var requests atomic.Int32
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			_, _ = fmt.Fprintf(writer, `[{"domain":"app.example.com","revision":%q}]`, revision)
		case http.MethodPut:
			mutations.Add(1)
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"error":"stale_observation","raw":"must-not-leak"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	var output bytes.Buffer
	args := []string{"--server", server.URL, "deploy", "remote", "domain-ensure", "--confirm", "--node", "edge-1", "--wait", "1s", "app", "app.example.com"}
	err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv)
	if err == nil || !strings.Contains(err.Error(), "refresh the project-domain inventory") {
		t.Fatalf("stale ensure error=%v, want refresh guidance", err)
	}
	if strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("stale ensure error leaked response data: %v", err)
	}
	if requests.Load() != 2 || mutations.Load() != 1 {
		t.Fatalf("requests=%d mutations=%d, want 2 requests and one PUT", requests.Load(), mutations.Load())
	}
}

func TestReadCLIDeployEnvironmentValueAllowsExplicitEmptyValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty-value")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readCLIDeployEnvironmentValue(path)
	if err != nil || value != "" {
		t.Fatalf("value=%q err=%v", value, err)
	}
}

func TestRunDeployTargetLifecycleUsesFixedEndpoints(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "webhook-token")
	if err := os.WriteFile(tokenPath, []byte("fixture-webhook-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(tempDir, "deploy.sh")
	const deployScript = "#!/bin/sh\nset -eu\nprintf 'deploy ready\\n'\n"
	if err := os.WriteFile(scriptPath, []byte(deployScript), 0o600); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/deploy/targets":
			_, _ = writer.Write([]byte(`[{"id":21,"name":"App","repoUrl":"https://github.com/example/app.git","branch":"main","projectDir":"/srv/apps/app","environment":"production","deploymentKind":"compose","composeFile":"deploy/compose.yaml","deployScript":"","webhookProvider":"github","webhookStatus":"healthy","autoDeploy":true,"isActive":true,"updatedAt":"2026-08-28T12:00:00Z"}]`))
		case request.Method == http.MethodPost && request.URL.Path == "/api/deploy/targets":
			var body struct {
				Name            string `json:"name"`
				RepoURL         string `json:"repoUrl"`
				Branch          string `json:"branch"`
				ProjectDir      string `json:"projectDir"`
				DeploymentKind  string `json:"deploymentKind"`
				ComposeFile     string `json:"composeFile"`
				DeployScript    string `json:"deployScript"`
				WebhookProvider string `json:"webhookProvider"`
				WebhookToken    string `json:"webhookToken"`
				AutoDeploy      bool   `json:"autoDeploy"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode target request: %v", err)
			}
			switch body.DeploymentKind {
			case "compose":
				if body.Name != "App" || body.RepoURL != "https://github.com/example/app.git" || body.Branch != "main" ||
					body.ProjectDir != "/srv/apps/app" || body.ComposeFile != "deploy/compose.yaml" || body.DeployScript != "" ||
					body.WebhookProvider != "github" || body.WebhookToken != "fixture-webhook-secret" || !body.AutoDeploy {
					t.Errorf("compose target request = %#v", body)
				}
				_, _ = writer.Write([]byte(`{"id":21,"name":"App","deploymentKind":"compose","webhookStatus":"healthy"}`))
			case "script":
				if body.Name != "Worker" || body.RepoURL != "git@example.com:team/worker.git" || body.Branch != "release" ||
					body.ProjectDir != "/srv/apps/worker" || body.ComposeFile != "" || body.DeployScript != deployScript ||
					body.WebhookProvider != "github" || body.WebhookToken != "" || body.AutoDeploy {
					t.Errorf("script target request = %#v", body)
				}
				_, _ = writer.Write([]byte(`{"id":22,"name":"Worker","deploymentKind":"script","webhookStatus":"not_configured"}`))
			default:
				t.Errorf("deployment kind = %q", body.DeploymentKind)
				http.Error(writer, "unexpected kind", http.StatusBadRequest)
			}
		case request.Method == http.MethodPut && request.URL.Path == "/api/deploy/targets/21":
			var body struct {
				Name              string `json:"name"`
				Branch            string `json:"branch"`
				DeploymentKind    string `json:"deploymentKind"`
				ComposeFile       string `json:"composeFile"`
				WebhookProvider   string `json:"webhookProvider"`
				WebhookToken      string `json:"webhookToken"`
				ClearWebhookToken bool   `json:"clearWebhookToken"`
				AutoDeploy        bool   `json:"autoDeploy"`
				IsActive          bool   `json:"isActive"`
				ExpectedUpdatedAt string `json:"expectedUpdatedAt"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode target update: %v", err)
			}
			if body.ExpectedUpdatedAt != "2026-08-28T12:00:00Z" || body.DeploymentKind != "compose" || body.ComposeFile != "deploy/compose.yaml" || body.WebhookProvider != "github" || body.WebhookToken != "" {
				t.Errorf("target update base = %#v", body)
			}
			if body.ClearWebhookToken {
				if body.Name != "App" || body.Branch != "main" || body.AutoDeploy || !body.IsActive {
					t.Errorf("clear target update = %#v", body)
				}
				_, _ = writer.Write([]byte(`{"id":21,"name":"App","webhookStatus":"not_configured","autoDeploy":false,"updatedAt":"2026-08-28T12:02:00Z"}`))
			} else {
				if body.Name != "App Updated" || body.Branch != "stable" || !body.AutoDeploy || !body.IsActive {
					t.Errorf("preserve target update = %#v", body)
				}
				_, _ = writer.Write([]byte(`{"id":21,"name":"App Updated","branch":"stable","webhookStatus":"healthy","autoDeploy":true,"updatedAt":"2026-08-28T12:01:00Z"}`))
			}
		case request.Method == http.MethodDelete && request.URL.Path == "/api/deploy/targets/21":
			_, _ = writer.Write([]byte(`{"message":"deployment target deleted"}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	commands := [][]string{
		{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/apps/app", "--repo", "https://github.com/example/app.git", "--compose-file", "deploy/compose.yaml", "--webhook-token-file", tokenPath, "--auto-deploy"},
		{"deploy", "target", "create", "--confirm", "--name", "Worker", "--project-dir", "/srv/apps/worker", "--type", "script", "--repo", "git@example.com:team/worker.git", "--branch", "release", "--script-file", scriptPath},
		{"deploy", "target", "update", "--confirm", "--name", "App Updated", "--branch", "stable", "21"},
		{"deploy", "target", "update", "--confirm", "--clear-webhook-token", "--auto-deploy=false", "21"},
		{"deploy", "target", "delete", "--confirm", "21"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
		if !json.Valid(output.Bytes()) {
			t.Fatalf("%s output is not JSON: %q", strings.Join(command, " "), output.String())
		}
		if strings.Contains(output.String(), "fixture-webhook-secret") {
			t.Fatalf("%s output disclosed webhook token", strings.Join(command, " "))
		}
	}
	if requests.Load() != int32(len(commands)+2) {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunDeployRejectsUnsafeActionsBeforeRequest(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	scriptPath := filepath.Join(tempDir, "deploy.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ntrue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	openTokenPath := filepath.Join(tempDir, "open-token")
	if err := os.WriteFile(openTokenPath, []byte("fixture-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(openTokenPath, 0o644); err != nil {
		t.Fatal(err)
	}
	invalidGitLabTokenPath := filepath.Join(tempDir, "gitlab-token")
	if err := os.WriteFile(invalidGitLabTokenPath, []byte("not-a-signing-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	multilineEnvironmentPath := filepath.Join(tempDir, "multiline-environment")
	if err := os.WriteFile(multilineEnvironmentPath, []byte("line-one\nline-two"), 0o600); err != nil {
		t.Fatal(err)
	}
	quotedEnvironmentPath := filepath.Join(tempDir, "quoted-environment")
	if err := os.WriteFile(quotedEnvironmentPath, []byte("can't-encode"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"deploy", "target", "create", "--name", "App", "--project-dir", "/srv/app"}, want: "explicit --confirm"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App\nProd", "--project-dir", "/srv/app"}, want: "one line"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "srv/app"}, want: "must be absolute"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--repo", "https://user:pass@example.com/app.git"}, want: "must not contain credentials"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--branch", "feature..broken"}, want: "branch is invalid"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--type", "archive"}, want: "type must be compose or script"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--webhook-provider", "custom"}, want: "provider must be github or gitlab"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--script-file", scriptPath}, want: "only valid for script"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--compose-file", "../compose.yaml"}, want: "relative path"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--type", "script"}, want: "require --script-file"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--type", "script", "--script-file", scriptPath, "--compose-file", "compose.yaml"}, want: "only valid for compose"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--webhook-token-file", openTokenPath}, want: "must not be accessible"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--auto-deploy"}, want: "requires --webhook-token-file"},
		{args: []string{"deploy", "target", "create", "--confirm", "--name", "App", "--project-dir", "/srv/app", "--webhook-provider", "gitlab", "--webhook-token-file", invalidGitLabTokenPath}, want: "valid whsec_"},
		{args: []string{"deploy", "target", "update", "--name", "App", "21"}, want: "explicit --confirm"},
		{args: []string{"deploy", "target", "update", "--confirm", "21"}, want: "at least one replacement option"},
		{args: []string{"deploy", "target", "update", "--confirm", "--name", "App", "0"}, want: "positive integer"},
		{args: []string{"deploy", "target", "update", "--confirm", "--repo", "https://user:pass@example.com/app.git", "21"}, want: "must not contain credentials"},
		{args: []string{"deploy", "target", "update", "--confirm", "--webhook-provider", "custom", "21"}, want: "provider must be github or gitlab"},
		{args: []string{"deploy", "target", "update", "--confirm", "--webhook-token-file", invalidGitLabTokenPath, "--clear-webhook-token", "21"}, want: "cannot be used together"},
		{args: []string{"deploy", "target", "delete", "21"}, want: "explicit --confirm"},
		{args: []string{"deploy", "target", "delete", "--confirm", "0"}, want: "positive integer"},
		{args: []string{"deploy", "environment", "list", "0"}, want: "positive integer"},
		{args: []string{"deploy", "environment", "set", "--value-file", invalidGitLabTokenPath, "12", "APP_MODE"}, want: "explicit --confirm"},
		{args: []string{"deploy", "environment", "set", "--confirm", "12", "APP_MODE"}, want: "requires --value-file"},
		{args: []string{"deploy", "environment", "set", "--confirm", "--value-file", invalidGitLabTokenPath, "12", "WITH-DASH"}, want: "invalid deployment environment key"},
		{args: []string{"deploy", "environment", "set", "--confirm", "--value-file", openTokenPath, "12", "APP_MODE"}, want: "must not be accessible"},
		{args: []string{"deploy", "environment", "set", "--confirm", "--value-file", multilineEnvironmentPath, "12", "APP_MODE"}, want: "unsupported"},
		{args: []string{"deploy", "environment", "set", "--confirm", "--value-file", quotedEnvironmentPath, "12", "APP_MODE"}, want: "unsupported"},
		{args: []string{"deploy", "environment", "delete", "12", "APP_MODE"}, want: "explicit --confirm"},
		{args: []string{"deploy", "environment", "delete", "--confirm", "12", "WITH-DASH"}, want: "invalid deployment environment key"},
		{args: []string{"deploy", "domains", "0"}, want: "positive integer"},
		{args: []string{"deploy", "domain", "create", "--service", "web", "--host-port", "8080", "12", "app.example.com"}, want: "explicit --confirm"},
		{args: []string{"deploy", "domain", "create", "--confirm", "--service", "web", "--host-port", "8080", "12", "localhost"}, want: "valid ASCII hostname"},
		{args: []string{"deploy", "domain", "create", "--confirm", "--service", "web..api", "--host-port", "8080", "12", "app.example.com"}, want: "invalid Compose service"},
		{args: []string{"deploy", "domain", "create", "--confirm", "--service", "web", "--host-port", "0", "12", "app.example.com"}, want: "between 1 and 65535"},
		{args: []string{"deploy", "domain", "create", "--confirm", "--service", "web", "--host-port", "8080", "--wait", "0s", "12", "app.example.com"}, want: "greater than zero"},
		{args: []string{"deploy", "domain", "health", "12", "0"}, want: "positive integer"},
		{args: []string{"deploy", "domain", "tls", "enable", "12", "41"}, want: "explicit --confirm"},
		{args: []string{"deploy", "domain", "tls", "enable", "--confirm", "--email", "Admin <admin@example.com>", "12", "41"}, want: "plain valid address"},
		{args: []string{"deploy", "domain", "tls", "enable", "--confirm", "--wait", "0s", "12", "41"}, want: "greater than zero"},
		{args: []string{"deploy", "domain", "tls", "disable", "--confirm", "--email", "admin@example.com", "12", "41"}, want: "only valid when enabling"},
		{args: []string{"deploy", "domain", "tls", "disable", "12", "41"}, want: "explicit --confirm"},
		{args: []string{"deploy", "domain", "delete", "12", "41"}, want: "explicit --confirm"},
		{args: []string{"deploy", "domain", "delete", "--confirm", "12", "0"}, want: "positive integer"},
		{args: []string{"deploy", "run", "12"}, want: "explicit --confirm"},
		{args: []string{"deploy", "rollback", "--confirm", "0"}, want: "positive integer"},
		{args: []string{"deploy", "staging", "create", "--project-dir", "/srv/staging", "12"}, want: "explicit --confirm"},
		{args: []string{"deploy", "staging", "create", "--confirm", "--project-dir", "srv/staging", "12"}, want: "must be absolute"},
		{args: []string{"deploy", "history", "--limit", "501"}, want: "between 1 and 500"},
		{args: []string{"deploy", "service", "logs", "--tail", "0", "12", "web"}, want: "between 1 and 1000"},
		{args: []string{"deploy", "service", "action", "12", "web", "stop"}, want: "explicit --confirm"},
		{args: []string{"deploy", "service", "action", "--confirm", "12", "web", "exec"}, want: "unsupported Compose"},
		{args: []string{"deploy", "service", "action", "--confirm", "12", "../web", "stop"}, want: "invalid Compose service"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected commands sent %d request(s)", requests.Load())
	}
}
