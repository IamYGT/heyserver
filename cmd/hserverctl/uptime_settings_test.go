package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunUptimeSettingsGetUsesFixedAuthenticatedEndpoint(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/api/uptime/settings" || request.URL.RawQuery != "" {
			t.Errorf("request = %s %s", request.Method, request.URL.RequestURI())
		}
		if got := request.Header.Get("Authorization"); got != "Bearer settings-token" {
			t.Errorf("Authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"uptime_retention_days":"90","uptime_compact_after_days":"30","uptime_default_interval":"60","uptime_default_timeout":"30","uptime_default_channels":"[]"}`)
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "settings-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runUptimeSettings(context.Background(), client, nil, &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]string
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("output is not JSON: %v; output=%q", err, output.String())
	}
	if response["uptime_retention_days"] != "90" || response["uptime_default_channels"] != "[]" {
		t.Fatalf("response = %#v", response)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestRunUptimeSettingsUpdateSendsStringValuesAndDeduplicatesChannels(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPut || request.URL.Path != "/api/uptime/settings" || request.URL.RawQuery != "" {
			t.Errorf("request = %s %s", request.Method, request.URL.RequestURI())
		}
		if got := request.Header.Get("Authorization"); got != "Bearer settings-token" {
			t.Errorf("Authorization = %q", got)
		}
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			return
		}
		want := map[string]string{
			"uptime_retention_days":     "120",
			"uptime_compact_after_days": "20",
			"uptime_default_interval":   "60",
			"uptime_default_timeout":    "15",
			"uptime_default_channels":   "[7,9]",
		}
		if len(payload) != len(want) {
			t.Errorf("payload = %#v, want %#v", payload, want)
		}
		for key, expected := range want {
			if payload[key] != expected {
				t.Errorf("payload[%q] = %q, want %q; payload=%#v", key, payload[key], expected, payload)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"status":"ok"}`)
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "settings-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{
		"update", "--confirm",
		"--retention-days", "120",
		"--compact-after-days", "20",
		"--default-interval-secs", "60",
		"--default-timeout-secs", "15",
		"--default-channel", "7", "--default-channel", "9", "--default-channel", "7",
	}
	if err := runUptimeSettings(context.Background(), client, args, &output); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(output.Bytes()) {
		t.Fatalf("output is not JSON: %q", output.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestRunUptimeSettingsUpdateClearDefaultChannels(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPut || request.URL.Path != "/api/uptime/settings" {
			t.Errorf("request = %s %s", request.Method, request.URL.RequestURI())
		}
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			return
		}
		if len(payload) != 1 || payload["uptime_default_channels"] != "[]" {
			t.Errorf("clear payload = %#v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"status":"ok"}`)
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "settings-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := runUptimeSettings(context.Background(), client, []string{"update", "--confirm", "--clear-default-channels"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestRunUptimeSettingsRejectsInvalidUpdatesBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "settings-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tooManyChannels := []string{"update", "--confirm"}
	for id := 1; id <= maximumUptimeDefaultChannels+1; id++ {
		tooManyChannels = append(tooManyChannels, "--default-channel", strconv.Itoa(id))
	}
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"update", "--retention-days", "120"}, want: "explicit --confirm"},
		{args: []string{"update", "--confirm"}, want: "at least one changed field"},
		{args: []string{"update", "--confirm", "--retention-days", "1"}, want: "between 2 and 3650"},
		{args: []string{"update", "--confirm", "--compact-after-days", "366"}, want: "between 1 and 365"},
		{args: []string{"update", "--confirm", "--default-interval-secs", "9"}, want: "between 10 and 86400"},
		{args: []string{"update", "--confirm", "--default-timeout-secs", "301"}, want: "between 1 and 300"},
		{args: []string{"update", "--confirm", "--retention-days", "30", "--compact-after-days", "30"}, want: "less than uptime_retention_days"},
		{args: []string{"update", "--confirm", "--default-interval-secs", "10", "--default-timeout-secs", "11"}, want: "must not exceed uptime_default_interval"},
		{args: []string{"update", "--confirm", "--default-channel", "0"}, want: "positive channel IDs"},
		{args: []string{"update", "--confirm", "--default-channel", "not-an-id"}, want: "positive channel IDs"},
		{args: []string{"update", "--confirm", "--default-channel", "1", "--clear-default-channels"}, want: "cannot be combined"},
		{args: tooManyChannels, want: "at most 128 channel IDs"},
	}
	for _, test := range cases {
		test := test
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			err := runUptimeSettings(context.Background(), client, test.args, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runUptimeSettings(%q) error = %v, want containing %q", test.args, err, test.want)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid updates made %d requests", requests.Load())
	}
}
