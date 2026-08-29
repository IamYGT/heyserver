package agentterminal

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrAgentOffline   = errors.New("managed terminal agent is offline")
	ErrSessionExists  = errors.New("managed terminal session already exists")
	ErrSessionMissing = errors.New("managed terminal session not found")
)

// Transport is implemented by gorilla/websocket.Conn and kept narrow so relay
// ownership, replacement, and multiplexing can be tested without a network.
type Transport interface {
	ReadJSON(any) error
	WriteJSON(any) error
	Close() error
}

type Hub struct {
	mu     sync.RWMutex
	agents map[string]*AgentConnection
}

func NewHub() *Hub {
	return &Hub{agents: make(map[string]*AgentConnection)}
}

type AgentConnection struct {
	hub       *Hub
	nodeID    string
	transport Transport
	writeMu   sync.Mutex
	mu        sync.RWMutex
	sessions  map[string]*Session
	closeOnce sync.Once
}

type Session struct {
	id     string
	agent  *AgentConnection
	events chan Message
	done   chan struct{}
	once   sync.Once
}

func (s *Session) Events() <-chan Message { return s.events }
func (s *Session) Done() <-chan struct{}  { return s.done }

func (h *Hub) Attach(nodeID string, transport Transport) *AgentConnection {
	connection := &AgentConnection{
		hub: h, nodeID: nodeID, transport: transport,
		sessions: make(map[string]*Session),
	}
	h.mu.Lock()
	previous := h.agents[nodeID]
	h.agents[nodeID] = connection
	h.mu.Unlock()
	if previous != nil {
		previous.shutdown("agent connection replaced")
	}
	return connection
}

func (h *Hub) Connected(nodeID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agents[nodeID] != nil
}

func (h *Hub) Open(nodeID, sessionID string, cols, rows uint16) (*Session, error) {
	h.mu.RLock()
	agent := h.agents[nodeID]
	h.mu.RUnlock()
	if agent == nil {
		return nil, ErrAgentOffline
	}
	return agent.open(sessionID, cols, rows)
}

func (c *AgentConnection) Run(ctx context.Context) error {
	defer c.shutdown("agent disconnected")
	go func() {
		<-ctx.Done()
		_ = c.transport.Close()
	}()
	for {
		var message Message
		if err := c.transport.ReadJSON(&message); err != nil {
			return err
		}
		if message.Type == TypePing {
			_ = c.write(Message{Type: TypePong})
			continue
		}
		if message.SessionID == "" {
			continue
		}
		c.mu.RLock()
		session := c.sessions[message.SessionID]
		c.mu.RUnlock()
		if session == nil {
			continue
		}
		select {
		case session.events <- message:
		case <-session.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		if message.Type == TypeClose || message.Type == TypeError {
			c.removeSession(message.SessionID, false)
			session.finish()
		}
	}
}

func (c *AgentConnection) open(sessionID string, cols, rows uint16) (*Session, error) {
	if sessionID == "" {
		return nil, ErrSessionMissing
	}
	session := &Session{id: sessionID, agent: c, events: make(chan Message, 64), done: make(chan struct{})}
	c.mu.Lock()
	if _, exists := c.sessions[sessionID]; exists {
		c.mu.Unlock()
		return nil, ErrSessionExists
	}
	c.sessions[sessionID] = session
	c.mu.Unlock()
	if err := c.write(Message{Type: TypeOpen, SessionID: sessionID, Cols: cols, Rows: rows}); err != nil {
		c.removeSession(sessionID, false)
		session.finish()
		return nil, err
	}
	return session, nil
}

func (s *Session) Send(message Message) error {
	select {
	case <-s.done:
		return ErrSessionMissing
	default:
	}
	message.SessionID = s.id
	return s.agent.write(message)
}

func (s *Session) Close() {
	s.agent.removeSession(s.id, true)
	s.finish()
}

func (s *Session) finish() {
	s.once.Do(func() { close(s.done) })
}

func (c *AgentConnection) removeSession(sessionID string, notify bool) {
	c.mu.Lock()
	session := c.sessions[sessionID]
	delete(c.sessions, sessionID)
	c.mu.Unlock()
	if notify && session != nil {
		_ = c.write(Message{Type: TypeClose, SessionID: sessionID})
	}
}

func (c *AgentConnection) write(message Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.transport.WriteJSON(message)
}

func (c *AgentConnection) shutdown(reason string) {
	c.closeOnce.Do(func() {
		c.hub.mu.Lock()
		if c.hub.agents[c.nodeID] == c {
			delete(c.hub.agents, c.nodeID)
		}
		c.hub.mu.Unlock()
		_ = c.transport.Close()

		c.mu.Lock()
		sessions := c.sessions
		c.sessions = make(map[string]*Session)
		c.mu.Unlock()
		for _, session := range sessions {
			select {
			case session.events <- Message{Type: TypeClose, SessionID: session.id, Data: reason}:
			default:
			}
			session.finish()
		}
	})
}
