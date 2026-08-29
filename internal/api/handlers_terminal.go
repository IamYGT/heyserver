package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/agentterminal"
	"github.com/IamYGT/heyserver/internal/services/terminal"
)

var (
	terminalManager = terminal.New()

	wsUpgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		Subprotocols:    []string{"hserver-terminal"},
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // non-browser clients (curl, etc.)
			}
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return u.Host == r.Host
		},
	}
)

// clientMsg is the JSON payload sent from the browser to the server.
type clientMsg struct {
	Type string `json:"type"` // "input" | "resize" | "ping"
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// serverMsg is the JSON payload sent from the server to the browser.
type serverMsg struct {
	Type      string `json:"type"`
	Data      string `json:"data,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

// handleTerminalWS upgrades the HTTP connection to WebSocket and spawns a
// PTY-backed bash session. Requires ADMIN role (enforced in router.go).
//
// Protocol:
//
//	Client → Server: { type: "input"|"resize"|"ping", data, cols, rows }
//	Server → Client: { type: "ready"|"output"|"error"|"close"|"pong" }
func handleTerminalWS(shutdownCtx context.Context, agentHub *agenthub.Service, agentTerminals *agentterminal.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getUserFromContext(r.Context())
		if user == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		nodeID := r.URL.Query().Get("node")
		if nodeID != "" && nodeID != "local" {
			handleRemoteTerminalWS(w, r, user.ID, user.Name, nodeID, shutdownCtx, agentHub, agentTerminals)
			return
		}
		cmd, terminalTarget, err := terminalCommand(nodeID)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		sess, err := terminalManager.Create(user.ID, user.Name)
		if err != nil {
			jsonError(w, http.StatusServiceUnavailable, err.Error())
			return
		}

		conn, upgradeErr := wsUpgrader.Upgrade(w, r, nil)
		if upgradeErr != nil {
			terminalManager.Remove(sess.ID)
			return
		}
		defer func() {
			_ = conn.Close()
			terminalManager.Remove(sess.ID)
		}()
		base64Output := r.URL.Query().Get("encoding") == "base64"

		slog.Info("terminal: session opened",
			"sessionId", sess.ID,
			"user", user.Name,
			"target", terminalTarget,
			"ip", r.RemoteAddr,
		)

		cmd.Env = append(os.Environ(),
			"TERM=xterm-256color",
			"COLORTERM=truecolor",
		)
		cols, rows := terminalSize(r.URL.Query().Get("cols"), r.URL.Query().Get("rows"))
		ptmx, ptyErr := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
		if ptyErr != nil {
			slog.Error("terminal: pty.Start failed", "sessionId", sess.ID, "error", ptyErr)
			writeWS(conn, serverMsg{Type: "error", Data: "failed to start shell: " + ptyErr.Error()})
			return
		}
		defer func() {
			_ = ptmx.Close()
			cmd.Process.Kill() //nolint:errcheck
			cmd.Wait()         //nolint:errcheck
		}()

		writeWS(conn, serverMsg{Type: "ready", SessionID: sess.ID})

		closeCh := make(chan struct{})
		var closeOnce sync.Once
		triggerClose := func() { closeOnce.Do(func() { close(closeCh) }) }
		streamCtx, cancelStream := requestStreamContext(r.Context(), shutdownCtx)
		defer cancelStream()
		go func() {
			<-streamCtx.Done()
			triggerClose()
			_ = conn.Close()
		}()

		// PTY → WebSocket
		var outputNormalizer terminalOutputNormalizer
		go func() {
			buf := make([]byte, 4096)
			for {
				n, readErr := ptmx.Read(buf)
				if n > 0 {
					output := buf[:n]
					if base64Output {
						output = outputNormalizer.Normalize(output)
					}
					writeWS(conn, terminalOutputMessage(output, base64Output))
				}
				if readErr != nil {
					if readErr != io.EOF {
						slog.Debug("terminal: pty read ended", "sessionId", sess.ID, "err", readErr)
					}
					writeWS(conn, serverMsg{Type: "close", Data: "process exited"})
					triggerClose()
					_ = conn.Close()
					return
				}
			}
		}()

		// Keepalive pings
		go func() {
			ticker := time.NewTicker(terminal.KeepalivePeriod)
			defer ticker.Stop()
			for {
				select {
				case <-closeCh:
					return
				case <-ticker.C:
					writeWS(conn, serverMsg{Type: "pong"})
				}
			}
		}()

		// WebSocket → PTY
		idleTimer := time.NewTimer(terminal.IdleTimeout)
		defer idleTimer.Stop()
		conn.SetReadDeadline(time.Now().Add(terminal.IdleTimeout)) //nolint:errcheck

		for {
			select {
			case <-closeCh:
				goto done
			case <-idleTimer.C:
				writeWS(conn, serverMsg{Type: "close", Data: "session timed out"})
				goto done
			default:
			}

			_, raw, readErr := conn.ReadMessage()
			if readErr != nil {
				goto done
			}

			sess.TouchLastSeen()
			conn.SetReadDeadline(time.Now().Add(terminal.IdleTimeout)) //nolint:errcheck
			resetT(idleTimer, terminal.IdleTimeout)

			var msg clientMsg
			if jsonErr := json.Unmarshal(raw, &msg); jsonErr != nil {
				ptmx.Write(raw) //nolint:errcheck
				continue
			}

			switch msg.Type {
			case "input":
				if _, wErr := ptmx.Write([]byte(msg.Data)); wErr != nil {
					goto done
				}
			case "resize":
				cols, rows := clampSz(msg.Cols, msg.Rows)
				pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows}) //nolint:errcheck
			case "ping":
				writeWS(conn, serverMsg{Type: "pong"})
			}
		}

	done:
		slog.Info("terminal: session ended",
			"sessionId", sess.ID,
			"user", user.Name,
			"target", terminalTarget,
			"duration", time.Since(sess.StartedAt).Round(time.Second).String(),
		)
	}
}

func terminalOutputMessage(data []byte, base64Output bool) serverMsg {
	if base64Output {
		return serverMsg{
			Type:     "output",
			Data:     base64.StdEncoding.EncodeToString(data),
			Encoding: "base64",
		}
	}
	return serverMsg{Type: "output", Data: string(data)}
}

// terminalOutputNormalizer rewrites standalone 8-bit C1 controls to their
// equivalent 7-bit ESC sequences. xterm.js decodes Uint8Array writes as UTF-8,
// so a bare C1 ST byte (0x9c) would otherwise become a replacement character
// and leave DCS payloads unterminated. Valid UTF-8 continuation bytes are kept.
type terminalOutputNormalizer struct {
	utf8Continuation int
}

func (n *terminalOutputNormalizer) Normalize(data []byte) []byte {
	output := make([]byte, 0, len(data)+8)
	for _, value := range data {
		if n.utf8Continuation > 0 {
			if value >= 0x80 && value <= 0xbf {
				output = append(output, value)
				n.utf8Continuation--
				continue
			}
			n.utf8Continuation = 0
		}

		switch {
		case value >= 0xc2 && value <= 0xdf:
			n.utf8Continuation = 1
			output = append(output, value)
		case value >= 0xe0 && value <= 0xef:
			n.utf8Continuation = 2
			output = append(output, value)
		case value >= 0xf0 && value <= 0xf4:
			n.utf8Continuation = 3
			output = append(output, value)
		case value >= 0x80 && value <= 0x9f:
			output = append(output, 0x1b, value-0x40)
		default:
			output = append(output, value)
		}
	}
	return output
}

func terminalCommand(nodeID string) (*exec.Cmd, string, error) {
	switch nodeID {
	case "", "local":
		return exec.Command(resolveShell()), "local", nil
	default:
		return nil, "", fmt.Errorf("managed terminal node %q requires the agent relay", nodeID)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

var wsMu sync.Mutex

func writeWS(conn *websocket.Conn, msg serverMsg) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	wsMu.Lock()
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	conn.WriteMessage(websocket.TextMessage, data)          //nolint:errcheck
	wsMu.Unlock()
}

func clampSz(cols, rows uint16) (uint16, uint16) {
	if cols < 10 {
		cols = 10
	}
	if rows < 4 {
		rows = 4
	}
	if cols > 500 {
		cols = 500
	}
	if rows > 200 {
		rows = 200
	}
	return cols, rows
}

func terminalSize(rawCols, rawRows string) (uint16, uint16) {
	cols, rows := uint16(80), uint16(24)
	if parsed, err := strconv.ParseUint(rawCols, 10, 16); err == nil {
		cols = uint16(parsed)
	}
	if parsed, err := strconv.ParseUint(rawRows, 10, 16); err == nil {
		rows = uint16(parsed)
	}
	return clampSz(cols, rows)
}

func resetT(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func resolveShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		if _, err := exec.LookPath(sh); err == nil {
			return sh
		}
	}
	if _, err := exec.LookPath("/bin/bash"); err == nil {
		return "/bin/bash"
	}
	return "/bin/sh"
}
