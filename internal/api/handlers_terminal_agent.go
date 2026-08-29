package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/agentterminal"
	"github.com/IamYGT/heyserver/internal/services/terminal"
	"github.com/gorilla/websocket"
)

const maxAgentTerminalMessageBytes = 128 << 10

var agentTerminalUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true },
}

func handleAgentTerminal(hub *agenthub.Service, terminals *agentterminal.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID, token, ok := agentCredentials(r)
		if !ok {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		node, err := hub.AuthenticateNode(nodeID, token)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		if !nodeHasCapability(node, agenthub.CapabilityTerminal) {
			jsonError(w, http.StatusConflict, "node does not advertise terminal capability")
			return
		}
		connection, err := agentTerminalUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connection.SetReadLimit(maxAgentTerminalMessageBytes)
		relay := terminals.Attach(nodeID, connection)
		slog.Info("terminal: agent relay connected", "node_id", nodeID)
		if err := relay.Run(r.Context()); err != nil && r.Context().Err() == nil {
			slog.Info("terminal: agent relay ended", "node_id", nodeID, "error", err)
		}
	}
}

func handleRemoteTerminalWS(
	w http.ResponseWriter,
	r *http.Request,
	userID int64,
	userName string,
	nodeID string,
	shutdownCtx context.Context,
	hub *agenthub.Service,
	terminals *agentterminal.Hub,
) {
	if hub == nil || terminals == nil {
		jsonError(w, http.StatusServiceUnavailable, "managed terminal relay is unavailable")
		return
	}
	node, err := hub.GetNode(nodeID)
	if err != nil {
		writeAgentHubError(w, err)
		return
	}
	if !nodeHasCapability(node, agenthub.CapabilityTerminal) {
		jsonError(w, http.StatusConflict, "managed node does not advertise terminal capability")
		return
	}
	if !terminals.Connected(nodeID) {
		jsonError(w, http.StatusServiceUnavailable, "managed terminal agent is offline")
		return
	}

	sess, err := terminalManager.Create(userID, userName)
	if err != nil {
		jsonError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	connection, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		terminalManager.Remove(sess.ID)
		return
	}
	defer func() {
		_ = connection.Close()
		terminalManager.Remove(sess.ID)
	}()
	connection.SetReadLimit(maxAgentTerminalMessageBytes)

	cols, rows := terminalSize(r.URL.Query().Get("cols"), r.URL.Query().Get("rows"))
	remote, err := terminals.Open(nodeID, sess.ID, cols, rows)
	if err != nil {
		writeWS(connection, serverMsg{Type: "error", Data: err.Error()})
		return
	}
	defer remote.Close()
	base64Output := r.URL.Query().Get("encoding") == "base64"

	slog.Info("terminal: session opened",
		"sessionId", sess.ID,
		"user", userName,
		"target", nodeID,
		"ip", r.RemoteAddr,
	)

	closeCh := make(chan struct{})
	var closeOnce sync.Once
	triggerClose := func() { closeOnce.Do(func() { close(closeCh) }) }
	streamCtx, cancelStream := requestStreamContext(r.Context(), shutdownCtx)
	defer cancelStream()
	go func() {
		select {
		case <-streamCtx.Done():
		case <-closeCh:
			return
		}
		triggerClose()
		_ = connection.Close()
	}()

	go forwardRemoteTerminalOutput(connection, remote, base64Output, triggerClose)

	idleTimer := time.NewTimer(terminal.IdleTimeout)
	defer idleTimer.Stop()
	_ = connection.SetReadDeadline(time.Now().Add(terminal.IdleTimeout))
	for {
		select {
		case <-closeCh:
			goto done
		case <-idleTimer.C:
			writeWS(connection, serverMsg{Type: "close", Data: "session timed out"})
			goto done
		default:
		}

		_, raw, readErr := connection.ReadMessage()
		if readErr != nil {
			goto done
		}
		sess.TouchLastSeen()
		_ = connection.SetReadDeadline(time.Now().Add(terminal.IdleTimeout))
		resetT(idleTimer, terminal.IdleTimeout)

		var message clientMsg
		if err := json.Unmarshal(raw, &message); err != nil {
			if err := remote.Send(agentterminal.Message{Type: agentterminal.TypeInput, Data: base64.StdEncoding.EncodeToString(raw)}); err != nil {
				goto done
			}
			continue
		}
		switch message.Type {
		case "input":
			if err := remote.Send(agentterminal.Message{Type: agentterminal.TypeInput, Data: base64.StdEncoding.EncodeToString([]byte(message.Data))}); err != nil {
				goto done
			}
		case "resize":
			cols, rows := clampSz(message.Cols, message.Rows)
			if err := remote.Send(agentterminal.Message{Type: agentterminal.TypeResize, Cols: cols, Rows: rows}); err != nil {
				goto done
			}
		case "ping":
			writeWS(connection, serverMsg{Type: "pong"})
		}
	}

done:
	triggerClose()
	slog.Info("terminal: session ended",
		"sessionId", sess.ID,
		"user", userName,
		"target", nodeID,
		"duration", time.Since(sess.StartedAt).Round(time.Second).String(),
	)
}

func forwardRemoteTerminalOutput(connection *websocket.Conn, remote *agentterminal.Session, base64Output bool, closeSession func()) {
	var normalizer terminalOutputNormalizer
	for {
		select {
		case message := <-remote.Events():
			if forwardRemoteTerminalMessage(connection, message, base64Output, &normalizer) {
				closeSession()
				_ = connection.Close()
				return
			}
		case <-remote.Done():
			select {
			case message := <-remote.Events():
				_ = forwardRemoteTerminalMessage(connection, message, base64Output, &normalizer)
			default:
				writeWS(connection, serverMsg{Type: "close", Data: "agent terminal disconnected"})
			}
			closeSession()
			_ = connection.Close()
			return
		}
	}
}

func forwardRemoteTerminalMessage(connection *websocket.Conn, message agentterminal.Message, base64Output bool, normalizer *terminalOutputNormalizer) bool {
	switch message.Type {
	case agentterminal.TypeReady:
		writeWS(connection, serverMsg{Type: "ready", SessionID: message.SessionID})
	case agentterminal.TypeOutput:
		output, err := base64.StdEncoding.DecodeString(message.Data)
		if err != nil {
			writeWS(connection, serverMsg{Type: "error", Data: "agent returned invalid terminal output"})
			return true
		}
		if base64Output {
			output = normalizer.Normalize(output)
		}
		writeWS(connection, terminalOutputMessage(output, base64Output))
	case agentterminal.TypeError:
		writeWS(connection, serverMsg{Type: "error", Data: message.Data})
		return true
	case agentterminal.TypeClose:
		reason := message.Data
		if reason == "" {
			reason = "process exited"
		}
		writeWS(connection, serverMsg{Type: "close", Data: reason})
		return true
	}
	return false
}

func nodeHasCapability(node *agenthub.Node, capability string) bool {
	if node == nil {
		return false
	}
	for _, candidate := range node.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}
