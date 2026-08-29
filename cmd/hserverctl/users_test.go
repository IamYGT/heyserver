package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunUsersListUsesBoundedPagination(t *testing.T) {
	t.Parallel()
	tokenFile := writeCLISecretFixture(t, "token", "test-bearer\n")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/users" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.URL.Query().Get("limit") != "75" || request.URL.Query().Get("offset") != "25" {
			t.Errorf("query = %q", request.URL.RawQuery)
		}
		assertCLIUserAuthorization(t, request)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[],"total":0,"limit":75,"offset":25}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "--token-file", tokenFile,
		"users", "list", "--limit", "75", "--offset", "25",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"limit": 75`) || !strings.Contains(out.String(), `"data": []`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunUsersCreateReadsProtectedPasswordWithoutLeakingIt(t *testing.T) {
	t.Parallel()
	tokenFile := writeCLISecretFixture(t, "token", "test-bearer\n")
	passwordFile := writeCLISecretFixture(t, "password", "correct horse battery staple\n")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/users" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		assertCLIUserAuthorization(t, request)
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"email": "new@example.com", "name": "New Operator",
			"password": "correct horse battery staple", "role": "manager",
		}
		if !reflect.DeepEqual(payload, want) {
			t.Errorf("payload = %#v; want %#v", payload, want)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"id":7,"email":"new@example.com","name":"New Operator","role":"manager","totp_enabled":false,"createdAt":"2026-08-28T00:00:00Z","updatedAt":"2026-08-28T00:00:00Z"}`))
	}))
	defer server.Close()

	var out, promptOut bytes.Buffer
	err := runWithInput(context.Background(), []string{
		"--server", server.URL, "--token-file", tokenFile,
		"users", "create", "--confirm", "--email", "new@example.com",
		"--name", "New Operator", "--role", "manager", "--password-file", passwordFile,
	}, bytes.NewBuffer(nil), &out, &promptOut, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	combined := out.String() + promptOut.String()
	if strings.Contains(combined, "correct horse battery staple") || strings.Contains(combined, "test-bearer") {
		t.Fatalf("secret leaked to output: %q", combined)
	}
	if !strings.Contains(out.String(), `"email": "new@example.com"`) || !strings.Contains(out.String(), `"role": "manager"`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunUsersUpdateSendsOnlyVisitedFields(t *testing.T) {
	t.Parallel()
	tokenFile := writeCLISecretFixture(t, "token", "test-bearer\n")
	passwordFile := writeCLISecretFixture(t, "password", "replacement-secret\n")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/api/users/12" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		assertCLIUserAuthorization(t, request)
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"name": "Renamed", "password": "replacement-secret"}
		if !reflect.DeepEqual(payload, want) {
			t.Errorf("payload = %#v; want %#v", payload, want)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":12,"email":"existing@example.com","name":"Renamed","role":"viewer","totp_enabled":false,"createdAt":"2026-08-28T00:00:00Z","updatedAt":"2026-08-28T00:01:00Z"}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "--token-file", tokenFile,
		"users", "update", "--confirm", "--name", "Renamed", "--password-file", passwordFile, "12",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "replacement-secret") || !strings.Contains(out.String(), `"name": "Renamed"`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunUsersDeleteRequiresConfirmationAndPrintsReceipt(t *testing.T) {
	t.Parallel()
	tokenFile := writeCLISecretFixture(t, "token", "test-bearer\n")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodDelete || request.URL.Path != "/api/users/23" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		assertCLIUserAuthorization(t, request)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	base := []string{"--server", server.URL, "--token-file", tokenFile, "users", "delete"}
	err := run(context.Background(), append(append([]string{}, base...), "23"), &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "requires explicit --confirm") {
		t.Fatalf("unconfirmed error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("unconfirmed deletion made %d requests", requests.Load())
	}

	var out bytes.Buffer
	err = run(context.Background(), append(append([]string{}, base...), "--confirm", "23"), &out, &bytes.Buffer{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || !strings.Contains(out.String(), `"status": "deleted"`) || !strings.Contains(out.String(), `"user_id": 23`) {
		t.Fatalf("requests=%d output=%q", requests.Load(), out.String())
	}
}

func TestRunUsersRejectsUnsafeInputsBeforeRequest(t *testing.T) {
	t.Parallel()
	tokenFile := writeCLISecretFixture(t, "token", "test-bearer\n")
	passwordFile := writeCLISecretFixture(t, "password", "short\n")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	tests := [][]string{
		{"users", "list", "--limit", "201"},
		{"users", "create", "--confirm", "--email", "not-an-email", "--name", "New", "--password-file", passwordFile},
		{"users", "create", "--confirm", "--email", "new@example.com", "--name", "New", "--role", "owner", "--password-file", passwordFile},
		{"users", "create", "--confirm", "--email", "new@example.com", "--name", "New", "--password-file", passwordFile},
		{"users", "update", "--confirm", "01"},
		{"users", "update", "--confirm", "12"},
	}
	for _, command := range tests {
		args := append([]string{"--server", server.URL, "--token-file", tokenFile}, command...)
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" }); err == nil {
			t.Errorf("%v unexpectedly succeeded", command)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid inputs made %d requests", requests.Load())
	}

	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "users") {
		t.Fatalf("completion does not expose users: %q", completion.String())
	}
}

func writeCLISecretFixture(t *testing.T, name, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertCLIUserAuthorization(t *testing.T, request *http.Request) {
	t.Helper()
	if got := request.Header.Get("Authorization"); got != "Bearer test-bearer" {
		t.Errorf("Authorization = %q", got)
	}
}
