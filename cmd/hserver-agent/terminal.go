package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/agentterminal"
	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

const (
	agentTerminalPath       = "/api/agent/v1/terminal"
	agentNodeHeader         = "X-HServer-Node-ID"
	maxTerminalSessions     = 8
	maxTerminalInputBytes   = 64 << 10
	maxTerminalMessageBytes = 128 << 10
)

var terminalSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type agentTerminalTransport interface {
	ReadJSON(any) error
	WriteJSON(any) error
	Close() error
}

type terminalProcess interface {
	io.Reader
	io.Writer
	Resize(cols, rows uint16) error
	Close() error
}

type terminalSpawner func(cols, rows uint16) (terminalProcess, error)

type agentTerminalServer struct {
	transport agentTerminalTransport
	spawn     terminalSpawner
	writeMu   sync.Mutex
	mu        sync.Mutex
	sessions  map[string]terminalProcess
}

func runTerminalConnector(ctx context.Context, logger *slog.Logger, cfg config) {
	endpoint := terminalWebSocketURL(cfg.hubURL)
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 10 * time.Second,
	}
	backoff := time.Second
	for ctx.Err() == nil {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+cfg.token)
		headers.Set(agentNodeHeader, cfg.nodeID)
		connection, response, err := dialer.DialContext(ctx, endpoint.String(), headers)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			logger.Warn("terminal relay connection failed", "error", err)
			if !waitForReconnect(ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second
		connection.SetReadLimit(maxTerminalMessageBytes)
		logger.Info("terminal relay connected", "node_id", cfg.nodeID)
		err = serveAgentTerminal(ctx, connection, spawnPTY)
		_ = connection.Close()
		if ctx.Err() != nil {
			return
		}
		logger.Warn("terminal relay disconnected", "error", err)
		if !waitForReconnect(ctx, backoff) {
			return
		}
	}
}

func terminalWebSocketURL(base *url.URL) *url.URL {
	endpoint := *base
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + agentTerminalPath
	return &endpoint
}

func waitForReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func serveAgentTerminal(ctx context.Context, transport agentTerminalTransport, spawn terminalSpawner) error {
	server := &agentTerminalServer{
		transport: transport,
		spawn:     spawn,
		sessions:  make(map[string]terminalProcess),
	}
	done := make(chan struct{})
	defer close(done)
	defer server.closeAll()
	go func() {
		select {
		case <-ctx.Done():
			_ = transport.Close()
		case <-done:
		}
	}()

	for {
		var message agentterminal.Message
		if err := transport.ReadJSON(&message); err != nil {
			return err
		}
		switch message.Type {
		case agentterminal.TypeOpen:
			server.open(message)
		case agentterminal.TypeInput:
			server.input(message)
		case agentterminal.TypeResize:
			server.resize(message)
		case agentterminal.TypeClose:
			server.closeSession(message.SessionID, false, "")
		case agentterminal.TypePing:
			_ = server.send(agentterminal.Message{Type: agentterminal.TypePong})
		}
	}
}

func (s *agentTerminalServer) open(message agentterminal.Message) {
	if !terminalSessionIDPattern.MatchString(message.SessionID) {
		_ = s.sendError(message.SessionID, "invalid terminal session identifier")
		return
	}
	cols, rows := terminalSize(message.Cols, message.Rows)
	s.mu.Lock()
	if len(s.sessions) >= maxTerminalSessions {
		s.mu.Unlock()
		_ = s.sendError(message.SessionID, "terminal session limit reached")
		return
	}
	if _, exists := s.sessions[message.SessionID]; exists {
		s.mu.Unlock()
		_ = s.sendError(message.SessionID, "terminal session already exists")
		return
	}
	process, err := s.spawn(cols, rows)
	if err != nil {
		s.mu.Unlock()
		_ = s.sendError(message.SessionID, "unable to start terminal")
		return
	}
	s.sessions[message.SessionID] = process
	s.mu.Unlock()

	if err := s.send(agentterminal.Message{Type: agentterminal.TypeReady, SessionID: message.SessionID}); err != nil {
		s.closeSession(message.SessionID, false, "")
		return
	}
	go s.forwardOutput(message.SessionID, process)
}

func (s *agentTerminalServer) input(message agentterminal.Message) {
	process := s.session(message.SessionID)
	if process == nil {
		return
	}
	if len(message.Data) > base64.StdEncoding.EncodedLen(maxTerminalInputBytes) {
		_ = s.sendError(message.SessionID, "terminal input exceeds the size limit")
		return
	}
	data, err := base64.StdEncoding.DecodeString(message.Data)
	if err != nil || len(data) > maxTerminalInputBytes {
		_ = s.sendError(message.SessionID, "terminal input is not valid base64")
		return
	}
	if _, err := process.Write(data); err != nil {
		s.closeSession(message.SessionID, true, "terminal input failed")
	}
}

func (s *agentTerminalServer) resize(message agentterminal.Message) {
	process := s.session(message.SessionID)
	if process == nil {
		return
	}
	cols, rows := terminalSize(message.Cols, message.Rows)
	if err := process.Resize(cols, rows); err != nil {
		s.closeSession(message.SessionID, true, "terminal resize failed")
	}
}

func (s *agentTerminalServer) forwardOutput(sessionID string, process terminalProcess) {
	buffer := make([]byte, 4096)
	for {
		count, err := process.Read(buffer)
		if count > 0 {
			message := agentterminal.Message{
				Type:      agentterminal.TypeOutput,
				SessionID: sessionID,
				Data:      base64.StdEncoding.EncodeToString(buffer[:count]),
			}
			if sendErr := s.send(message); sendErr != nil {
				s.closeSession(sessionID, false, "")
				return
			}
		}
		if err != nil {
			reason := terminalReadCloseReason(err)
			s.closeSession(sessionID, true, reason)
			return
		}
	}
}

func terminalReadCloseReason(err error) string {
	if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EIO) {
		return ""
	}
	return "terminal process ended unexpectedly"
}

func (s *agentTerminalServer) session(sessionID string) terminalProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionID]
}

func (s *agentTerminalServer) closeSession(sessionID string, notify bool, reason string) {
	s.mu.Lock()
	process := s.sessions[sessionID]
	delete(s.sessions, sessionID)
	s.mu.Unlock()
	if process == nil {
		return
	}
	_ = process.Close()
	if notify {
		_ = s.send(agentterminal.Message{Type: agentterminal.TypeClose, SessionID: sessionID, Data: reason})
	}
}

func (s *agentTerminalServer) closeAll() {
	s.mu.Lock()
	sessions := s.sessions
	s.sessions = make(map[string]terminalProcess)
	s.mu.Unlock()
	for _, process := range sessions {
		_ = process.Close()
	}
}

func (s *agentTerminalServer) sendError(sessionID, reason string) error {
	return s.send(agentterminal.Message{Type: agentterminal.TypeError, SessionID: sessionID, Data: reason})
}

func (s *agentTerminalServer) send(message agentterminal.Message) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.transport.WriteJSON(message)
}

func terminalSize(cols, rows uint16) (uint16, uint16) {
	if cols < 20 {
		cols = 120
	} else if cols > 500 {
		cols = 500
	}
	if rows < 5 {
		rows = 32
	} else if rows > 200 {
		rows = 200
	}
	return cols, rows
}

type ptyProcess struct {
	file *os.File
	cmd  *exec.Cmd
	once sync.Once
}

func spawnPTY(cols, rows uint16) (terminalProcess, error) {
	shell, err := resolveTerminalShell()
	if err != nil {
		return nil, err
	}
	command := exec.Command(shell, "-l")
	command.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	if home, err := os.UserHomeDir(); err == nil {
		command.Dir = home
	} else {
		command.Dir = "/"
	}
	file, err := pty.StartWithSize(command, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}
	return &ptyProcess{file: file, cmd: command}, nil
}

func resolveTerminalShell() (string, error) {
	candidates := []string{strings.TrimSpace(os.Getenv("SHELL")), "/bin/bash", "/bin/sh"}
	for _, candidate := range candidates {
		if !strings.HasPrefix(candidate, "/") {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", errors.New("no executable terminal shell found")
}

func (p *ptyProcess) Read(buffer []byte) (int, error)  { return p.file.Read(buffer) }
func (p *ptyProcess) Write(buffer []byte) (int, error) { return p.file.Write(buffer) }
func (p *ptyProcess) Resize(cols, rows uint16) error {
	return pty.Setsize(p.file, &pty.Winsize{Cols: cols, Rows: rows})
}
func (p *ptyProcess) Close() error {
	var closeErr error
	p.once.Do(func() {
		closeErr = p.file.Close()
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		_ = p.cmd.Wait()
	})
	return closeErr
}
