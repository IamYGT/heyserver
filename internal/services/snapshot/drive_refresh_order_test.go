package snapshot

import (
	"os"
	"strings"
	"testing"
)

// Contract: restic must not run with a stale embedded rclone token — refresh before initRepo.
func TestRunTracked_oauthRefreshBeforeResticInit(t *testing.T) {
	b, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "func (s *Service) runTracked(")
	if start < 0 {
		t.Fatal("runTracked not found")
	}
	end := strings.Index(src[start:], "func (s *Service) fail(")
	if end < 0 {
		t.Fatal("fail() not found after runTracked")
	}
	body := src[start : start+end]
	refresh := strings.Index(body, "refreshDestinationForRestic")
	init := strings.Index(body, "initRepo(ctx)")
	if refresh < 0 || init < 0 {
		t.Fatalf("markers missing destination refresh=%d init=%d", refresh, init)
	}
	if refresh > init {
		t.Fatal("refreshDestinationForRestic must be called before initRepo in runTracked")
	}
}
