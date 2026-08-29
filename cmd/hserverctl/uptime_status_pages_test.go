package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunUptimeStatusPageCreateUsesAuthenticatedPayload(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/api/uptime/status-pages" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer status-page-token" {
			t.Errorf("Authorization = %q", authorization)
		}
		var payload struct {
			Slug        string                       `json:"slug"`
			Title       string                       `json:"title"`
			Description string                       `json:"description"`
			LogoURL     string                       `json:"logo_url"`
			Theme       string                       `json:"theme"`
			HistoryDays int                          `json:"history_days"`
			IsPublic    bool                         `json:"is_public"`
			Monitors    []uptimeStatusPageMonitorCLI `json:"monitors"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode status-page create payload: %v", err)
		}
		wantMonitors := []uptimeStatusPageMonitorCLI{
			{MonitorID: 7, SortOrder: 1},
			{MonitorID: 9, SortOrder: 2},
		}
		if payload.Slug != "ops-page" || payload.Title != "Operations" || payload.Description != "public health" ||
			payload.LogoURL != "https://example.com/logo.svg" || payload.Theme != "dark" || payload.HistoryDays != 30 ||
			payload.IsPublic || !reflect.DeepEqual(payload.Monitors, wantMonitors) {
			t.Errorf("create payload = %#v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"id":12,"slug":"ops-page","title":"Operations","is_public":false,"history_days":30}`))
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "status-page-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runUptimeStatusPage(context.Background(), client, []string{
		"create", "--confirm", "--slug", " Ops-Page ", "--title", " Operations ",
		"--description", "public health", "--logo-url", "https://example.com/logo.svg",
		"--theme", "dark", "--history-days", "30", "--private", "--monitor", "7", "--monitor", "9",
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("request count = %d, want 1", requests.Load())
	}
	if !json.Valid(output.Bytes()) {
		t.Fatalf("output is not JSON: %q", output.String())
	}
}

func TestRunUptimeStatusPageUpdateFetchesThenSendsFullReplacementAndClears(t *testing.T) {
	t.Parallel()
	var methods []string
	var putPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer update-token" {
			t.Errorf("Authorization = %q", authorization)
		}
		methods = append(methods, request.Method+" "+request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			_, _ = writer.Write([]byte(`[{
				"id":41,"slug":"operations","title":"Operations","description":"old description",
				"logo_url":"https://example.com/old.svg","theme":"light","is_public":true,"history_days":90,
				"monitors":[{"monitor_id":7,"display_name":"API","sort_order":1},{"monitor_id":9,"sort_order":2}]
			}]`))
		case http.MethodPut:
			if err := json.NewDecoder(request.Body).Decode(&putPayload); err != nil {
				t.Errorf("decode status-page replacement payload: %v", err)
			}
			_, _ = writer.Write([]byte(`{"id":41,"slug":"operations","title":"Operations updated"}`))
		default:
			t.Errorf("unexpected request %s", request.Method)
		}
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "update-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runUptimeStatusPage(context.Background(), client, []string{
		"update", "--confirm", "--title", "Operations updated", "--clear-description", "--clear-logo-url", "--clear-monitors", "41",
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{"GET /api/uptime/status-pages", "PUT /api/uptime/status-pages/41"}) {
		t.Fatalf("request sequence = %#v", methods)
	}
	if got, want := putPayload["slug"], "operations"; got != want {
		t.Errorf("replacement slug = %#v, want %q", got, want)
	}
	if got, want := putPayload["title"], "Operations updated"; got != want {
		t.Errorf("replacement title = %#v, want %q", got, want)
	}
	if got, want := putPayload["description"], ""; got != want {
		t.Errorf("replacement description = %#v, want empty string", got)
	}
	if got, want := putPayload["logo_url"], ""; got != want {
		t.Errorf("replacement logo_url = %#v, want empty string", got)
	}
	if got, want := putPayload["theme"], "light"; got != want {
		t.Errorf("replacement theme = %#v, want %q", got, want)
	}
	if got, want := putPayload["history_days"], float64(90); got != want {
		t.Errorf("replacement history_days = %#v, want %v", got, want)
	}
	if got, want := putPayload["is_public"], true; got != want {
		t.Errorf("replacement is_public = %#v, want %v", got, want)
	}
	monitors, ok := putPayload["monitors"].([]any)
	if !ok || len(monitors) != 0 {
		t.Errorf("replacement monitors = %#v, want []", putPayload["monitors"])
	}
	if !json.Valid(output.Bytes()) {
		t.Fatalf("output is not JSON: %q", output.String())
	}
}

func TestRunUptimeStatusPageUpdateAllowsExplicitEmptyOptionalText(t *testing.T) {
	t.Parallel()
	var putPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			_, _ = writer.Write([]byte(`[{"id":5,"slug":"ops","title":"Ops","description":"old","logo_url":"https://example.com/a.svg","theme":"auto","is_public":false,"history_days":7,"monitors":[]}]`))
			return
		}
		if request.Method != http.MethodPut {
			t.Errorf("unexpected request %s", request.Method)
		}
		if err := json.NewDecoder(request.Body).Decode(&putPayload); err != nil {
			t.Errorf("decode replacement payload: %v", err)
		}
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := runUptimeStatusPage(context.Background(), client, []string{
		"update", "--confirm", "--description", "", "--logo-url", "", "5",
	}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if putPayload["description"] != "" || putPayload["logo_url"] != "" {
		t.Fatalf("empty optional text was not preserved: %#v", putPayload)
	}
}

func TestRunUptimeStatusPageDeleteUsesFixedAuthenticatedEndpoint(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodDelete || request.URL.Path != "/api/uptime/status-pages/41" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer delete-token" {
			t.Errorf("Authorization = %q", authorization)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"deleted"}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "delete-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runUptimeStatusPage(context.Background(), client, []string{"delete", "--confirm", "41"}, &output); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || !json.Valid(output.Bytes()) {
		t.Fatalf("requests=%d output=%q", requests.Load(), output.String())
	}
}

func TestRunUptimeStatusPageRejectsInvalidInputBeforeHTTP(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[]`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	longTitle := strings.Repeat("x", 129)
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"create", "--slug", "ops", "--title", "Ops"}, want: "explicit --confirm"},
		{args: []string{"create", "--confirm", "--slug", "bad_slug", "--title", "Ops"}, want: "lowercase letters"},
		{args: []string{"create", "--confirm", "--slug", "ops", "--title", ""}, want: "title is required"},
		{args: []string{"create", "--confirm", "--slug", "ops", "--title", longTitle}, want: "at most 128"},
		{args: []string{"create", "--confirm", "--slug", "ops", "--title", "Ops", "--theme", "sepia"}, want: "auto, light, or dark"},
		{args: []string{"create", "--confirm", "--slug", "ops", "--title", "Ops", "--history-days", "3651"}, want: "between 1 and 3650"},
		{args: []string{"create", "--confirm", "--slug", "ops", "--title", "Ops", "--logo-url", "https://user:pass@example.com/logo.svg"}, want: "without credentials"},
		{args: []string{"create", "--confirm", "--slug", "ops", "--title", "Ops", "--monitor", "4", "--monitor", "4"}, want: "duplicated"},
		{args: []string{"update", "--confirm", "--title", "New", "0"}, want: "positive integer"},
		{args: []string{"update", "--confirm", "41"}, want: "at least one changed field"},
		{args: []string{"update", "--confirm", "--monitor", "0", "41"}, want: "positive integers"},
		{args: []string{"delete", "41"}, want: "explicit --confirm"},
		{args: []string{"delete", "--confirm", "0"}, want: "positive integer"},
	}
	for _, test := range cases {
		err := runUptimeStatusPage(context.Background(), client, test.args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("runUptimeStatusPage(%q) error = %v, want containing %q", test.args, err, test.want)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("rejected commands made %d HTTP request(s)", got)
	}
}
