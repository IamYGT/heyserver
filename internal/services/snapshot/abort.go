package snapshot

import (
	"context"
	"os/exec"
	"time"
)

// AbortRunning stops in-flight restic workers and clears the run lock (e.g. after job dismiss).
func (s *Service) AbortRunning() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	s.invalidateStatusCache()
	killResticWorkers()
}

func killResticWorkers() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "pkill", "-TERM", "-f", "restic backup").Run()
	_ = exec.CommandContext(ctx, "pkill", "-TERM", "-f", "restic restore").Run()
	time.Sleep(500 * time.Millisecond)
	_ = exec.CommandContext(ctx, "pkill", "-KILL", "-f", "restic backup").Run()
	_ = exec.CommandContext(ctx, "pkill", "-KILL", "-f", "restic restore").Run()
}
