package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agentterminal"
)

func TestTerminalWebSocketURLPreservesBasePath(t *testing.T) {
	base, err := url.Parse("https://panel.example.com/hserver")
	if err != nil {
		t.Fatal(err)
	}
	got := terminalWebSocketURL(base).String()
	if got != "wss://panel.example.com/hserver/api/agent/v1/terminal" {
		t.Fatalf("terminal URL = %q", got)
	}
}

func TestServeAgentTerminalMultiplexesPTYMessages(t *testing.T) {
	transport := newFakeAgentTerminalTransport()
	process := newFakeTerminalProcess()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveAgentTerminal(ctx, transport, func(cols, rows uint16) (terminalProcess, error) {
			if cols != 100 || rows != 40 {
				t.Errorf("spawn size = %dx%d", cols, rows)
			}
			return process, nil
		})
	}()

	transport.receive(agentterminal.Message{Type: agentterminal.TypeOpen, SessionID: "session-1", Cols: 100, Rows: 40})
	assertTerminalMessage(t, transport.sent, agentterminal.TypeReady, "session-1", "")

	input := []byte("printf hello\n")
	transport.receive(agentterminal.Message{Type: agentterminal.TypeInput, SessionID: "session-1", Data: base64.StdEncoding.EncodeToString(input)})
	process.waitForWrite(t, input)

	transport.receive(agentterminal.Message{Type: agentterminal.TypeResize, SessionID: "session-1", Cols: 140, Rows: 50})
	process.waitForResize(t, 140, 50)

	process.output <- []byte("hello\r\n")
	wantOutput := base64.StdEncoding.EncodeToString([]byte("hello\r\n"))
	assertTerminalMessage(t, transport.sent, agentterminal.TypeOutput, "session-1", wantOutput)

	transport.receive(agentterminal.Message{Type: agentterminal.TypeClose, SessionID: "session-1"})
	process.waitForClose(t)
	transport.shutdown()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("serve error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal server did not stop")
	}
}

func TestTerminalReadCloseReasonTreatsPTYExitAsNormal(t *testing.T) {
	t.Parallel()

	for _, err := range []error{io.EOF, os.ErrClosed, syscall.EIO} {
		if reason := terminalReadCloseReason(err); reason != "" {
			t.Fatalf("terminalReadCloseReason(%v) = %q, want normal close", err, reason)
		}
	}
	if reason := terminalReadCloseReason(errors.New("read failed")); reason != "terminal process ended unexpectedly" {
		t.Fatalf("unexpected read reason = %q", reason)
	}
}

func assertTerminalMessage(t *testing.T, messages <-chan agentterminal.Message, messageType, sessionID, data string) {
	t.Helper()
	select {
	case message := <-messages:
		if message.Type != messageType || message.SessionID != sessionID || message.Data != data {
			t.Fatalf("message = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", messageType)
	}
}

type fakeAgentTerminalTransport struct {
	in        chan agentterminal.Message
	sent      chan agentterminal.Message
	closed    chan struct{}
	closeOnce sync.Once
}

func newFakeAgentTerminalTransport() *fakeAgentTerminalTransport {
	return &fakeAgentTerminalTransport{
		in:     make(chan agentterminal.Message, 8),
		sent:   make(chan agentterminal.Message, 8),
		closed: make(chan struct{}),
	}
}

func (f *fakeAgentTerminalTransport) receive(message agentterminal.Message) { f.in <- message }
func (f *fakeAgentTerminalTransport) shutdown()                             { f.closeOnce.Do(func() { close(f.closed) }) }
func (f *fakeAgentTerminalTransport) Close() error                          { f.shutdown(); return nil }
func (f *fakeAgentTerminalTransport) WriteJSON(value any) error {
	message, ok := value.(agentterminal.Message)
	if !ok {
		return errors.New("unexpected message type")
	}
	f.sent <- message
	return nil
}
func (f *fakeAgentTerminalTransport) ReadJSON(value any) error {
	select {
	case message := <-f.in:
		pointer, ok := value.(*agentterminal.Message)
		if !ok {
			return errors.New("unexpected message target")
		}
		*pointer = message
		return nil
	case <-f.closed:
		return io.EOF
	}
}

type fakeTerminalProcess struct {
	mu      sync.Mutex
	written bytes.Buffer
	output  chan []byte
	resized chan [2]uint16
	closed  chan struct{}
	once    sync.Once
}

func newFakeTerminalProcess() *fakeTerminalProcess {
	return &fakeTerminalProcess{
		output:  make(chan []byte, 1),
		resized: make(chan [2]uint16, 1),
		closed:  make(chan struct{}),
	}
}

func (f *fakeTerminalProcess) Read(buffer []byte) (int, error) {
	select {
	case data := <-f.output:
		return copy(buffer, data), nil
	case <-f.closed:
		return 0, io.EOF
	}
}
func (f *fakeTerminalProcess) Write(data []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written.Write(data)
}
func (f *fakeTerminalProcess) Resize(cols, rows uint16) error {
	f.resized <- [2]uint16{cols, rows}
	return nil
}
func (f *fakeTerminalProcess) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}
func (f *fakeTerminalProcess) waitForWrite(t *testing.T, want []byte) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := append([]byte(nil), f.written.Bytes()...)
		f.mu.Unlock()
		if bytes.Equal(got, want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("terminal input was not written")
}
func (f *fakeTerminalProcess) waitForResize(t *testing.T, cols, rows uint16) {
	t.Helper()
	select {
	case got := <-f.resized:
		if got != [2]uint16{cols, rows} {
			t.Fatalf("resize = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal was not resized")
	}
}
func (f *fakeTerminalProcess) waitForClose(t *testing.T) {
	t.Helper()
	select {
	case <-f.closed:
	case <-time.After(time.Second):
		t.Fatal("terminal was not closed")
	}
}
