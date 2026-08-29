package agentterminal

// Message is the bounded multiplexing envelope used between one managed-node
// agent connection and any number of browser terminal sessions.
type Message struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Data      string `json:"data,omitempty"`
	Cols      uint16 `json:"cols,omitempty"`
	Rows      uint16 `json:"rows,omitempty"`
}

const (
	TypeOpen   = "open"
	TypeReady  = "ready"
	TypeInput  = "input"
	TypeOutput = "output"
	TypeResize = "resize"
	TypeClose  = "close"
	TypeError  = "error"
	TypePing   = "ping"
	TypePong   = "pong"
)
