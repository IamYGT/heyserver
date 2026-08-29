package terminal

import (
	crypto_rand "crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const ( MaxSessions = 20; IdleTimeout = 30*time.Minute; KeepalivePeriod = 25*time.Second )
type Session struct { ID string; UserID int64; UserName string; StartedAt time.Time; lastSeen time.Time; mu sync.Mutex }
func (s *Session) TouchLastSeen() { s.mu.Lock(); s.lastSeen = time.Now(); s.mu.Unlock() }
type Manager struct { mu sync.Mutex; sessions map[string]*Session }
func New() *Manager { return &Manager{sessions: make(map[string]*Session)} }
func (m *Manager) Create(userID int64, userName string) (*Session, error) {
	m.mu.Lock(); defer m.mu.Unlock()
	if len(m.sessions) >= MaxSessions { return nil, fmt.Errorf("max sessions") }
	b := make([]byte, 8)
	crypto_rand.Read(b)
	id := fmt.Sprintf("%d-%s", userID, hex.EncodeToString(b))
	s := &Session{ID: id, UserID: userID, UserName: userName, StartedAt: time.Now(), lastSeen: time.Now()}
	m.sessions[id] = s; return s, nil
}
func (m *Manager) Remove(id string) { m.mu.Lock(); delete(m.sessions, id); m.mu.Unlock() }
