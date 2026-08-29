package snapshot

import (
	"testing"
)

func TestAbortRunning_clearsRunLock(t *testing.T) {
	s := &Service{running: true}
	s.AbortRunning()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		t.Fatal("expected running=false after AbortRunning")
	}
}
