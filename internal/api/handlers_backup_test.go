package api

import (
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/services/backup"
)

func TestNormalizeJobResponse_mapsDoneToCompleted(t *testing.T) {
	job := &backup.Job{
		ID:        "full-123",
		Type:      "full",
		Status:    backup.JobDone,
		Progress:  100,
		Phase:     backup.PhaseDone,
		Message:   "backup completed successfully",
		StartedAt: time.Now(),
	}
	resp := normalizeJobResponse(job)
	if resp["status"] != "completed" {
		t.Errorf("status = %v, want completed", resp["status"])
	}
	if resp["progress"] != 100 {
		t.Errorf("progress = %v, want 100", resp["progress"])
	}
	if resp["jobId"] != job.ID {
		t.Errorf("jobId = %v", resp["jobId"])
	}
}

func TestNormalizeJobResponse_failedIncludesError(t *testing.T) {
	job := &backup.Job{
		ID:      "full-err",
		Status:  backup.JobFailed,
		Message: "disk full",
	}
	resp := normalizeJobResponse(job)
	if resp["status"] != "failed" {
		t.Errorf("status = %v", resp["status"])
	}
	if resp["error"] != "disk full" {
		t.Errorf("error = %v", resp["error"])
	}
}
