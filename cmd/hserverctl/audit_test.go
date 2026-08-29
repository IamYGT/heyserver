package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunAuditListUsesBoundedEncodedFilters(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/audit" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		query := request.URL.Query()
		for key, expected := range map[string]string{
			"limit": "75", "offset": "25", "server": "edge-1", "user": "Example Operator",
			"action_contains": "swap reset", "resource": "system",
			"from": "2026-08-01T00:00:00Z", "to": "2026-08-27T23:59:59Z",
		} {
			if query.Get(key) != expected {
				t.Errorf("query[%s] = %q", key, query.Get(key))
			}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"data":  []map[string]any{{"id": 9, "action": "swap_reset", "resource": "system"}},
			"total": 1, "limit": 75, "offset": 25,
		})
	}))
	defer server.Close()

	var output bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "audit", "list",
		"--limit", "75", "--offset", "25", "--server", "edge-1",
		"--user", "Example Operator", "--action", "swap reset", "--resource", "system",
		"--from", "2026-08-01T00:00:00Z", "--to", "2026-08-27T23:59:59Z",
	}, &output, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(output.Bytes()) || !strings.Contains(output.String(), `"action": "swap_reset"`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunAuditListRejectsInvalidFiltersBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"audit", "list", "--limit", "0"}, want: "between 1 and 200"},
		{args: []string{"audit", "list", "--limit", "201"}, want: "between 1 and 200"},
		{args: []string{"audit", "list", "--offset", "-1"}, want: "cannot be negative"},
		{args: []string{"audit", "list", "--server", "../edge"}, want: "valid managed-node ID"},
		{args: []string{"audit", "list", "--action", "bad\nfilter"}, want: "control characters"},
		{args: []string{"audit", "list", "--from", "yesterday"}, want: "must be RFC3339"},
		{args: []string{"audit", "list", "--from", "2026-08-28T00:00:00Z", "--to", "2026-08-27T00:00:00Z"}, want: "cannot be after"},
		{args: []string{"audit", "unknown"}, want: "usage:"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
			if key == "HSERVER_TOKEN" {
				return "test-token"
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected audit filters sent %d request(s)", requests.Load())
	}
}
