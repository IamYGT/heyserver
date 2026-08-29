package snapshot

import "testing"

func TestStatusCache_invalidate(t *testing.T) {
	s := &Service{}
	s.setRepoStatusCache(repoStatusCache{repoInitialized: true})
	if _, ok := s.getRepoStatusCache(); !ok {
		t.Fatal("expected cache hit")
	}
	s.invalidateStatusCache()
	if _, ok := s.getRepoStatusCache(); ok {
		t.Fatal("expected miss after invalidate")
	}
}
