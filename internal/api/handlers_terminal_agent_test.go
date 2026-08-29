package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/agentterminal"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/gorilla/websocket"
)

func TestAgentTerminalRelayAuthenticatesAndTracksConnection(t *testing.T) {
	hub, err := agenthub.New(db.Instance())
	if err != nil {
		t.Fatalf("agenthub.New: %v", err)
	}
	registered, err := hub.RegisterNode(agenthub.RegisterNodeRequest{ID: "terminal-agent", Name: "Terminal Agent"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if _, err := hub.Heartbeat("terminal-agent", registered.Token, agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          "terminal-agent",
		AgentVersion:    "agent-test",
		Capabilities:    []string{agenthub.CapabilityInventory, agenthub.CapabilityTerminal},
		Hostname:        "terminal.example",
		SentAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	relays := agentterminal.NewHub()
	server := httptest.NewServer(handleAgentTerminal(hub, relays))
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")

	if connection, response, err := websocket.DefaultDialer.Dial(endpoint, nil); err == nil {
		_ = connection.Close()
		t.Fatal("relay accepted a connection without agent credentials")
	} else if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %#v, error=%v", response, err)
	}

	headers := http.Header{}
	headers.Set(agentHubIdentityHeader, "terminal-agent")
	headers.Set("Authorization", "Bearer "+registered.Token)
	connection, response, err := websocket.DefaultDialer.Dial(endpoint, headers)
	if err != nil {
		t.Fatalf("authorized relay dial: status=%v error=%v", response, err)
	}
	waitForRelayState(t, relays, "terminal-agent", true)
	_ = connection.Close()
	waitForRelayState(t, relays, "terminal-agent", false)
}

func waitForRelayState(t *testing.T, relays *agentterminal.Hub, nodeID string, connected bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if relays.Connected(nodeID) == connected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("relay connected state = %t, want %t", relays.Connected(nodeID), connected)
}
