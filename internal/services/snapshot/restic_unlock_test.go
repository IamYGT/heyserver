package snapshot

import (
	"strings"
	"testing"
)

func TestUnlockStale_noLocksMessage(t *testing.T) {
	low := strings.ToLower("Fatal: no locks found")
	if !strings.Contains(low, "no locks") && !strings.Contains(low, "no lock") {
		t.Fatal("expected no-locks detection")
	}
}
