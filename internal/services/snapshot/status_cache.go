package snapshot

import "time"

const statusCacheTTL = 2 * time.Minute

type repoStatusCache struct {
	at                 time.Time
	repoInitialized    bool
	lastSnapshots      []Snapshot
	repoStats          *RepoStats
	destinationStatus  DestinationStatus
	destinationMessage string
}

func (s *Service) getRepoStatusCache() (repoStatusCache, bool) {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	if s.repoStatusCache == nil {
		return repoStatusCache{}, false
	}
	c := *s.repoStatusCache
	if time.Since(c.at) > statusCacheTTL {
		return repoStatusCache{}, false
	}
	return c, true
}

func (s *Service) setRepoStatusCache(c repoStatusCache) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	c.at = time.Now()
	cp := c
	s.repoStatusCache = &cp
}

func (s *Service) invalidateStatusCache() {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.repoStatusCache = nil
}
