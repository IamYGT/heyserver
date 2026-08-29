package agentterminal

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type fakeTransport struct {
	reads  chan Message
	writes chan Message
	done   chan struct{}
	once   sync.Once
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{reads: make(chan Message, 8), writes: make(chan Message, 8), done: make(chan struct{})}
}

func (f *fakeTransport) ReadJSON(target any) error {
	select {
	case message := <-f.reads:
		*(target.(*Message)) = message
		return nil
	case <-f.done:
		return io.EOF
	}
}

func (f *fakeTransport) WriteJSON(value any) error {
	select {
	case f.writes <- value.(Message):
		return nil
	case <-f.done:
		return io.EOF
	}
}

func (f *fakeTransport) Close() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

func TestHubMultiplexesSessionsOverOutboundAgent(t *testing.T) {
	hub := NewHub()
	transport := newFakeTransport()
	connection := hub.Attach("node-1", transport)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- connection.Run(ctx) }()

	session, err := hub.Open("node-1", "session-1", 120, 40)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if message := <-transport.writes; message.Type != TypeOpen || message.SessionID != "session-1" || message.Cols != 120 || message.Rows != 40 {
		t.Fatalf("open message = %#v", message)
	}
	if err := session.Send(Message{Type: TypeInput, Data: "pwd\r"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if message := <-transport.writes; message.Type != TypeInput || message.SessionID != "session-1" || message.Data != "pwd\r" {
		t.Fatalf("input message = %#v", message)
	}
	transport.reads <- Message{Type: TypeOutput, SessionID: "session-1", Data: "L3Jvb3QK"}
	select {
	case message := <-session.Events():
		if message.Type != TypeOutput || message.Data != "L3Jvb3QK" {
			t.Fatalf("event = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal output was not routed")
	}

	session.Close()
	if message := <-transport.writes; message.Type != TypeClose || message.SessionID != "session-1" {
		t.Fatalf("close message = %#v", message)
	}
	if err := session.Send(Message{Type: TypeInput, Data: "x"}); !errors.Is(err, ErrSessionMissing) {
		t.Fatalf("send after close = %v", err)
	}
	cancel()
	<-runDone
}

func TestHubReplacesStaleAgentConnection(t *testing.T) {
	hub := NewHub()
	first := newFakeTransport()
	hub.Attach("node-1", first)
	second := newFakeTransport()
	hub.Attach("node-1", second)
	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("previous transport was not closed")
	}
	if !hub.Connected("node-1") {
		t.Fatal("replacement agent is not connected")
	}
	if _, err := hub.Open("unknown", "session", 80, 24); !errors.Is(err, ErrAgentOffline) {
		t.Fatalf("offline Open = %v", err)
	}
}
