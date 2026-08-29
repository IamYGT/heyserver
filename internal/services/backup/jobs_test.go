package backup

import (
	"testing"
	"time"
)

func TestJobHub_broadcastAndList(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job), jobSubs: make(map[chan Job]struct{})}
	ch, unsub := s.SubscribeJobs()
	defer unsub()

	job := s.newJob("full", "manual")
	// Drain initial pending broadcast
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no initial broadcast")
	}

	s.updateJob(job.ID, PhaseDatabase, 25, "db")
	select {
	case snap := <-ch:
		if snap.Progress != 25 || snap.Phase != PhaseDatabase {
			t.Fatalf("snapshot = %+v", snap)
		}
	case <-time.After(time.Second):
		t.Fatal("no progress broadcast")
	}

	active := s.ListActiveJobs()
	if len(active) != 1 {
		t.Fatalf("active jobs = %d", len(active))
	}
}

func TestGetJob_returnsSnapshot(t *testing.T) {
	s := &Service{jobs: map[string]*Job{
		"job-1": {ID: "job-1", Status: JobRunning, Logs: []string{"started"}},
	}}

	got := s.GetJob("job-1")
	got.Status = JobDone
	got.Logs[0] = "changed"

	current := s.GetJob("job-1")
	if current.Status != JobRunning || current.Logs[0] != "started" {
		t.Fatalf("GetJob exposed mutable service state: %+v", current)
	}
}

func TestDismissJob_activeOnly(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	job := &Job{ID: "snap-1", Status: JobRunning, StartedAt: time.Now(), UpdatedAt: time.Now()}
	s.jobs[job.ID] = job
	if err := s.DismissJob("snap-1", "test"); err != nil {
		t.Fatal(err)
	}
	if job.Status != JobFailed {
		t.Fatalf("expected failed, got %s", job.Status)
	}
}

func TestHasActiveJobTypes(t *testing.T) {
	s := &Service{jobs: map[string]*Job{
		"a": {ID: "a", Type: "snapshot", Status: JobRunning, StartedAt: time.Now()},
	}}
	if !s.HasActiveJobTypes("snapshot") {
		t.Fatal("expected active snapshot")
	}
	if s.HasActiveJobTypes("database") {
		t.Fatal("unexpected match")
	}
}

func TestReconcileOrphanedOnStartup_failsAllActive(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	job := &Job{ID: "snap-1", Status: JobRunning, StartedAt: time.Now()}
	s.jobs[job.ID] = job
	s.reconcileOrphanedOnStartupLocked()
	if job.Status != JobFailed {
		t.Fatalf("expected failed on startup, got %s", job.Status)
	}
}

func TestReconcileStaleJobs_marksZombieFailed(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	job := &Job{
		ID:        "snapshot-zombie",
		Type:      "snapshot",
		Status:    JobRunning,
		Phase:     PhaseDatabase,
		Progress:  15,
		StartedAt: time.Now().Add(-20 * time.Minute),
	}
	s.jobs[job.ID] = job
	s.reconcileStaleJobsLocked()
	if job.Status != JobFailed {
		t.Fatalf("expected failed, got %s", job.Status)
	}
}

func TestComputeETA(t *testing.T) {
	s := &Service{}
	j := &Job{StartedAt: time.Now().Add(-10 * time.Second), Progress: 50}
	s.computeETA(j)
	if j.ETASeconds <= 0 {
		t.Fatalf("eta = %d", j.ETASeconds)
	}
}
