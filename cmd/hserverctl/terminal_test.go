package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTerminalWebSocketURL(t *testing.T) {
	t.Parallel()
	base, err := url.Parse("https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	got := terminalWebSocketURL(base, "edge west", 132, 41)
	if got.Scheme != "wss" || got.Host != "panel.example.com" || got.Path != "/api/terminal/ws" {
		t.Fatalf("terminal URL = %s", got)
	}
	query := got.Query()
	if query.Get("node") != "edge west" || query.Get("cols") != "132" || query.Get("rows") != "41" || query.Get("encoding") != "base64" {
		t.Fatalf("terminal query = %#v", query)
	}
}

func TestRunTerminalSessionRelaysAuthenticatedInputOutputAndResize(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{
		Subprotocols: []string{terminalWebSocketProtocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	serverErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer cli-secret" {
			serverErrors <- &terminalTestError{"Authorization", got}
			return
		}
		if r.URL.Query().Get("node") != "edge-1" || r.URL.Query().Get("encoding") != "base64" {
			serverErrors <- &terminalTestError{"query", r.URL.RawQuery}
			return
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.Close()
		if err := connection.WriteJSON(cliTerminalMessage{Type: "ready"}); err != nil {
			serverErrors <- err
			return
		}
		gotInput := false
		gotResize := false
		for !gotInput || !gotResize {
			var message cliTerminalMessage
			if err := connection.ReadJSON(&message); err != nil {
				serverErrors <- err
				return
			}
			switch message.Type {
			case "input":
				if message.Data != "printf test\\n\n" {
					serverErrors <- &terminalTestError{"input", message.Data}
					return
				}
				gotInput = true
			case "resize":
				if message.Cols != 144 || message.Rows != 52 {
					serverErrors <- &terminalTestError{"resize", message}
					return
				}
				gotResize = true
			}
		}
		output := []byte{'o', 'k', '\r', '\n', 0x1b, '[', '3', '2', 'm'}
		if err := connection.WriteJSON(cliTerminalMessage{Type: "output", Data: base64.StdEncoding.EncodeToString(output), Encoding: "base64"}); err != nil {
			serverErrors <- err
			return
		}
		if err := connection.WriteJSON(cliTerminalMessage{Type: "close", Data: "process exited"}); err != nil {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "cli-secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resize := make(chan cliTerminalSize, 1)
	resize <- cliTerminalSize{Cols: 144, Rows: 52}
	var output bytes.Buffer
	if err := runTerminalSession(context.Background(), client, "edge-1", 80, 24, strings.NewReader("printf test\\n\n"), &output, resize); err != nil {
		t.Fatal(err)
	}
	if got, want := output.Bytes(), []byte{'o', 'k', '\r', '\n', 0x1b, '[', '3', '2', 'm'}; !bytes.Equal(got, want) {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestRunTerminalSessionReturnsBoundedHandshakeError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "terminal denied"})
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "cli-secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	err = runTerminalSession(context.Background(), client, "local", 80, 24, strings.NewReader(""), &bytes.Buffer{}, nil)
	if err == nil || err.Error() != "HTTP 403: terminal denied" {
		t.Fatalf("error = %v", err)
	}
}

func TestPumpTerminalInputStopsAtLocalEscape(t *testing.T) {
	t.Parallel()
	messages := make(chan cliTerminalMessage, 1)
	err := pumpTerminalInput(context.Background(), bytes.NewReader([]byte{'e', 'x', 'i', 't', terminalLocalEscape, 'x'}), messages)
	if err != errTerminalLocalEscape {
		t.Fatalf("error = %v", err)
	}
	message := <-messages
	if message.Type != "input" || message.Data != "exit" {
		t.Fatalf("message = %#v", message)
	}
}

type terminalTestError struct {
	field string
	value any
}

func (e *terminalTestError) Error() string {
	return e.field + " = " + strings.TrimSpace(toTerminalTestJSON(e.value))
}

func toTerminalTestJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
