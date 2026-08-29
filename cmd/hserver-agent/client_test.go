package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestHubClientUsesBoundedAgentContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret-token" || r.Header.Get("X-HServer-Node-ID") != "contabo" {
			t.Errorf("request method/auth/node = %s/%q/%q", r.Method, r.Header.Get("Authorization"), r.Header.Get("X-HServer-Node-ID"))
		}
		switch r.URL.Path {
		case "/prefix/api/agent/v1/heartbeat":
			var request agenthub.HeartbeatRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.NodeID != "contabo" {
				t.Errorf("heartbeat request = %#v, error = %v", request, err)
			}
			_ = json.NewEncoder(w).Encode(agenthub.HeartbeatResponse{Accepted: true})
		case "/prefix/api/agent/v1/tasks/poll":
			_ = json.NewEncoder(w).Encode(agenthub.TaskPollResponse{Task: &agenthub.Task{ID: 42, Kind: agenthub.TaskServiceStatus}})
		case "/prefix/api/agent/v1/tasks/42/result":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	base, _ := url.Parse(server.URL + "/prefix")
	client := hubClient{baseURL: base, nodeID: "contabo", token: "secret-token", http: server.Client()}
	if err := client.heartbeat(context.Background(), agenthub.HeartbeatRequest{NodeID: "contabo"}); err != nil {
		t.Fatalf("heartbeat() error = %v", err)
	}
	task, err := client.poll(context.Background())
	if err != nil || task == nil || task.ID != 42 {
		t.Fatalf("poll() task = %#v, error = %v", task, err)
	}
	if err := client.report(context.Background(), task.ID, agenthub.TaskResultRequest{Status: "completed"}); err != nil {
		t.Fatalf("report() error = %v", err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestHubClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxHTTPBodyBytes+1)))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := hubClient{baseURL: base, token: "secret-token", http: server.Client()}
	_, err := client.poll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("poll() error = %v", err)
	}
}
