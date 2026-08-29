package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxJobLogLines = 200
const maxJobLogLineLen = 800

// JobPhase is a human-readable step within a backup operation.
type JobPhase string

const (
	PhasePreparing     JobPhase = "preparing"
	PhaseDatabase      JobPhase = "database"
	PhaseFiles         JobPhase = "files"
	PhaseArchive       JobPhase = "archive"
	PhaseRetention     JobPhase = "retention"
	PhaseGDriveUpload  JobPhase = "gdrive_upload"
	PhaseGDriveRestore JobPhase = "gdrive_restore"
	PhaseRestore       JobPhase = "restore"
	PhaseVerify        JobPhase = "verify"
	PhaseDone          JobPhase = "done"
)

const jobRetention = 24 * time.Hour
const jobStaleAfter = 15 * time.Minute
const jobMaxRuntime = 6 * time.Hour

// Job tracks an async backup, restore, upload, or maintenance operation.
type Job struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Source       string    `json:"source"` // manual | scheduled | auto
	Status       JobStatus `json:"status"`
	Phase        JobPhase  `json:"phase"`
	Progress     int       `json:"progress"`
	Message      string    `json:"message"`
	Error        string    `json:"error,omitempty"`
	StartedAt    time.Time `json:"startedAt"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
	DoneAt       time.Time `json:"doneAt,omitempty"`
	ETASeconds   int       `json:"etaSeconds,omitempty"`
	BytesDone    int64     `json:"bytesDone,omitempty"`
	BytesTotal   int64     `json:"bytesTotal,omitempty"`
	SizeEstimate int64     `json:"sizeEstimate,omitempty"`
	OutputFile   string    `json:"outputFile,omitempty"`
	Speed        string    `json:"speed,omitempty"`
	Command      string    `json:"command,omitempty"`
	Logs         []string  `json:"logs,omitempty"`
}

func (j *Job) clone() Job {
	cp := *j
	if len(j.Logs) > 0 {
		cp.Logs = append([]string(nil), j.Logs...)
	}
	return cp
}

func (j *Job) isActive() bool {
	return j.Status == JobPending || j.Status == JobRunning
}

func (s *Service) jobsFile() string {
	return filepath.Join(s.backupDir, ".hserver-jobs.json")
}

func (s *Service) loadPersistedJobs() {
	path := s.jobsFile()
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var list []Job
	if json.Unmarshal(raw, &list) != nil {
		return
	}
	cutoff := time.Now().Add(-jobRetention)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range list {
		j := list[i]
		if j.StartedAt.Before(cutoff) {
			continue
		}
		s.jobs[j.ID] = &j
	}
	s.reconcileOrphanedOnStartupLocked()
	s.persistJobsLocked()
}

func (s *Service) persistJobsLocked() {
	list := make([]Job, 0, len(s.jobs))
	cutoff := time.Now().Add(-jobRetention)
	for _, j := range s.jobs {
		if j.StartedAt.Before(cutoff) {
			continue
		}
		list = append(list, j.clone())
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return
	}
	_ = os.WriteFile(s.jobsFile(), raw, 0o600)
}

func (s *Service) pruneJobsLocked() {
	cutoff := time.Now().Add(-jobRetention)
	for id, j := range s.jobs {
		if j.StartedAt.Before(cutoff) && !j.isActive() {
			delete(s.jobs, id)
		}
	}
}

func (s *Service) computeETA(j *Job) {
	if j.Progress <= 0 || j.Progress >= 100 {
		j.ETASeconds = 0
		return
	}
	elapsed := time.Since(j.StartedAt).Seconds()
	if elapsed < 1 {
		return
	}
	j.ETASeconds = int(elapsed * float64(100-j.Progress) / float64(j.Progress))
}

func (s *Service) broadcastLocked(j *Job) {
	if s.jobSubs == nil {
		return
	}
	snap := j.clone()
	for ch := range s.jobSubs {
		select {
		case ch <- snap:
		default:
		}
	}
}

// SubscribeJobs registers an SSE/push subscriber. Caller must call unsubscribe on disconnect.
func (s *Service) SubscribeJobs() (<-chan Job, func()) {
	ch := make(chan Job, 32)
	s.mu.Lock()
	if s.jobSubs == nil {
		s.jobSubs = make(map[chan Job]struct{})
	}
	s.jobSubs[ch] = struct{}{}
	s.mu.Unlock()
	unsub := func() {
		s.mu.Lock()
		delete(s.jobSubs, ch)
		close(ch)
		s.mu.Unlock()
	}
	return ch, unsub
}

// ListJobs returns jobs started after `since`, newest first.
func (s *Service) ListJobs(since time.Time) []Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		if j.StartedAt.Before(since) {
			continue
		}
		out = append(out, j.clone())
	}
	sortJobsByTime(out)
	return out
}

// ListActiveJobs returns pending/running jobs.
func (s *Service) ListActiveJobs() []Job {
	s.mu.Lock()
	s.reconcileStaleJobsLocked()
	s.persistJobsLocked()
	s.mu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Job
	for _, j := range s.jobs {
		if j.isActive() {
			out = append(out, j.clone())
		}
	}
	sortJobsByTime(out)
	return out
}

func (s *Service) reconcileOrphanedOnStartupLocked() {
	now := time.Now()
	msg := "Panel yeniden başlatıldı — işlem yarım kaldı"
	for _, j := range s.jobs {
		if !j.isActive() {
			continue
		}
		j.Status = JobFailed
		j.Phase = PhaseDone
		j.Progress = 100
		j.Error = msg
		j.Message = msg
		j.DoneAt = now
		j.ETASeconds = 0
		j.Logs = append(j.Logs, fmt.Sprintf("[%s] ✗ %s", now.Format("15:04:05.000"), msg))
		s.broadcastLocked(j)
	}
}

func (s *Service) reconcileStaleJobsLocked() {
	now := time.Now()
	for _, j := range s.jobs {
		if !j.isActive() {
			continue
		}
		updated := j.UpdatedAt
		if updated.IsZero() {
			updated = j.StartedAt
		}
		stale := now.Sub(updated) > jobStaleAfter
		expired := now.Sub(j.StartedAt) > jobMaxRuntime
		if !stale && !expired {
			continue
		}
		msg := "İşlem yanıt vermiyor — panel yeniden başlatıldı veya süreç sonlandı"
		if expired {
			msg = "İşlem zaman aşımına uğradı (6 saat)"
		}
		j.Status = JobFailed
		j.Phase = PhaseDone
		j.Progress = 100
		j.Error = msg
		j.Message = msg
		j.DoneAt = now
		j.ETASeconds = 0
		j.Logs = append(j.Logs, fmt.Sprintf("[%s] ✗ %s", now.Format("15:04:05.000"), msg))
		s.broadcastLocked(j)
	}
}

func sortJobsByTime(jobs []Job) {
	for i := 0; i < len(jobs); i++ {
		for k := i + 1; k < len(jobs); k++ {
			if jobs[k].StartedAt.After(jobs[i].StartedAt) {
				jobs[i], jobs[k] = jobs[k], jobs[i]
			}
		}
	}
}

func (s *Service) newJob(jobType, source string) *Job {
	id := jobType + "-" + time.Now().Format("20060102150405") + "-" + randomSuffix()
	job := &Job{
		ID:        id,
		Type:      jobType,
		Source:    source,
		Status:    JobPending,
		Phase:     PhasePreparing,
		Progress:  0,
		Message:   "Hazırlanıyor…",
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if source == "" {
		job.Source = "manual"
	}
	s.mu.Lock()
	s.jobs[id] = job
	s.broadcastLocked(job)
	s.persistJobsLocked()
	s.mu.Unlock()
	return job
}

func randomSuffix() string {
	return time.Now().Format("000000000")
}

func truncateJobMessage(msg string, n int) string {
	msg = strings.TrimSpace(msg)
	if len(msg) <= n {
		return msg
	}
	return msg[:n] + "…"
}

func (s *Service) updateJob(id string, phase JobPhase, progress int, message string) {
	s.updateJobDetail(id, phase, progress, message, 0, 0, "")
}

func (s *Service) appendJobLog(id, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(line) > maxJobLogLineLen {
		line = line[:maxJobLogLineLen] + "…"
	}
	entry := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05.000"), line)
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return
	}
	j.UpdatedAt = time.Now()
	j.Logs = append(j.Logs, entry)
	if len(j.Logs) > maxJobLogLines {
		j.Logs = j.Logs[len(j.Logs)-maxJobLogLines:]
	}
	s.broadcastLocked(j)
}

func (s *Service) setJobCommand(id, cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return
	}
	j.Command = cmd
	s.broadcastLocked(j)
}

// AppendJobLog implements gdrive.JobTracker verbose logging.
func (s *Service) AppendJobLog(id, line string) { s.appendJobLog(id, line) }

// SetJobCommand implements gdrive.JobTracker command display.
func (s *Service) SetJobCommand(id, cmd string) { s.setJobCommand(id, cmd) }

func (s *Service) updateJobDetail(id string, phase JobPhase, progress int, message string, bytesDone, bytesTotal int64, speed string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	j.Status = JobRunning
	j.Phase = phase
	j.Progress = progress
	if message != "" {
		j.Message = message
	}
	if bytesDone > 0 {
		j.BytesDone = bytesDone
	}
	if bytesTotal > 0 {
		j.BytesTotal = bytesTotal
	}
	if speed != "" {
		j.Speed = speed
	}
	j.UpdatedAt = time.Now()
	s.computeETA(j)
	s.broadcastLocked(j)
}

func (s *Service) setJob(id string, status JobStatus, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return
	}
	j.Status = status
	j.Message = msg
	if status == JobDone {
		j.Phase = PhaseDone
		j.Progress = 100
		j.DoneAt = time.Now()
		j.ETASeconds = 0
		j.Logs = append(j.Logs, fmt.Sprintf("[%s] ✓ %s", time.Now().Format("15:04:05.000"), msg))
	} else if status == JobFailed {
		j.Phase = PhaseDone
		j.Progress = 100
		j.Error = truncateJobMessage(msg, 600)
		j.Message = truncateJobMessage(msg, 200)
		j.DoneAt = time.Now()
		j.ETASeconds = 0
		j.Logs = append(j.Logs, fmt.Sprintf("[%s] ✗ %s", time.Now().Format("15:04:05.000"), msg))
	} else if status == JobRunning {
		j.Status = JobRunning
	}
	s.broadcastLocked(j)
	s.persistJobsLocked()
	s.pruneJobsLocked()
}

func (s *Service) setJobOutput(id, outputFile string, sizeEstimate int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.OutputFile = outputFile
		if sizeEstimate > 0 {
			j.SizeEstimate = sizeEstimate
		}
		s.broadcastLocked(j)
	}
}

// StartJob implements external job tracking (e.g. Google Drive uploads).
func (s *Service) StartJob(jobType, source, message string) string {
	job := s.newJob(jobType, source)
	if message != "" {
		s.updateJob(job.ID, PhasePreparing, 2, message)
	}
	return job.ID
}

// UpdateJobProgress is called by external integrations.
func (s *Service) UpdateJobProgress(id string, phase JobPhase, progress int, message string, bytesDone, bytesTotal int64, speed string) {
	s.updateJobDetail(id, phase, progress, message, bytesDone, bytesTotal, speed)
	s.mu.Lock()
	s.persistJobsLocked()
	s.mu.Unlock()
}

// HasActiveJobTypes reports whether any pending/running job matches one of the types.
func (s *Service) HasActiveJobTypes(types ...string) bool {
	if len(types) == 0 {
		return false
	}
	allowed := make(map[string]bool, len(types))
	for _, t := range types {
		allowed[t] = true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, j := range s.jobs {
		if j.isActive() && allowed[j.Type] {
			return true
		}
	}
	return false
}

// DismissJob marks an active job failed (user dismissed stale/zombie UI entry).
func (s *Service) DismissJob(id, reason string) error {
	if reason == "" {
		reason = "Kullanıcı tarafından kapatıldı"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job not found")
	}
	if !j.isActive() {
		return fmt.Errorf("job is not active")
	}
	now := time.Now()
	j.Status = JobFailed
	j.Phase = PhaseDone
	j.Progress = 100
	j.Error = reason
	j.Message = reason
	j.DoneAt = now
	j.ETASeconds = 0
	j.Logs = append(j.Logs, fmt.Sprintf("[%s] ✗ %s", now.Format("15:04:05.000"), reason))
	s.broadcastLocked(j)
	s.persistJobsLocked()
	return nil
}

// CompleteJob marks an externally tracked job done or failed.
func (s *Service) CompleteJob(id string, success bool, message, outputFile string) {
	if success {
		if outputFile != "" {
			s.setJobOutput(id, outputFile, 0)
		}
		s.setJob(id, JobDone, message)
	} else {
		s.setJob(id, JobFailed, message)
	}
}
