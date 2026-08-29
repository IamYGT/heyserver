package deploy

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

var deployDiffStatPattern = regexp.MustCompile(`^\s*(?:(\d+) files? changed)?(?:,\s*(\d+) insertions?\(\+\))?(?:,\s*(\d+) deletions?\(-\))?\s*$`)

// RevisionComparison returns a read-only view of the local checkout and its
// latest successful deployment records. Remote refs are not fetched, so this
// endpoint cannot claim that a newer upstream commit is available.
func (s *Service) RevisionComparison(targetID int64) (*models.DeployRevisionComparison, error) {
	target, err := s.GetTarget(targetID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("%w: %d", ErrDeployTargetNotFound, targetID)
	}

	report := &models.DeployRevisionComparison{
		TargetID:  target.ID,
		State:     "not_deployed",
		Branch:    target.Branch,
		Message:   "Project checkout has not been provisioned yet.",
		CheckedAt: time.Now().UTC(),
	}
	info, statErr := os.Stat(target.ProjectDir)
	if errors.Is(statErr, os.ErrNotExist) {
		return report, nil
	}
	if statErr != nil || !info.IsDir() {
		report.State = "unavailable"
		report.Message = "Project checkout path is unavailable."
		return report, nil
	}

	current, err := gitRevParse(target.ProjectDir, "HEAD")
	if err != nil {
		report.State = "unavailable"
		report.Message = "Project directory is not a readable Git checkout."
		return report, nil
	}
	report.State = "ready"
	report.CurrentCommit = current
	report.Message = "Local checkout revision is available. Remote refs were not fetched."

	trackedOut, trackedErr := runCmdTimeout(target.ProjectDir, 30*time.Second, "git", "diff", "--quiet", "HEAD", "--")
	if trackedErr != nil {
		if exitCode(trackedErr) == 1 {
			report.TrackedChanges = true
		} else {
			report.State = "unavailable"
			report.Message = "Local checkout changes could not be inspected."
			if detail := boundedProcessDetail(trackedOut); detail != "" {
				report.Message += " " + detail
			}
			return report, nil
		}
	}

	report.DeployedCommit, err = s.latestSuccessfulCommit(targetID)
	if err != nil {
		return nil, err
	}
	report.RollbackCommit, err = s.latestRollbackCommit(targetID)
	if err != nil {
		return nil, err
	}
	report.MatchesDeployed = report.DeployedCommit != "" && report.DeployedCommit == report.CurrentCommit
	report.RollbackAvailable = report.RollbackCommit != ""
	if !report.RollbackAvailable {
		return report, nil
	}

	countOut, countErr := runCmdTimeout(target.ProjectDir, 30*time.Second, "git", "rev-list", "--left-right", "--count", report.RollbackCommit+"..."+report.CurrentCommit)
	if countErr != nil {
		report.State = "unavailable"
		report.Message = "Rollback revision is no longer available in the local checkout."
		return report, nil
	}
	counts := strings.Fields(countOut)
	if len(counts) != 2 {
		return nil, errors.New("deploy: invalid Git revision count output")
	}
	behind, err := strconv.Atoi(counts[0])
	if err != nil {
		return nil, errors.New("deploy: invalid Git revision count output")
	}
	ahead, err := strconv.Atoi(counts[1])
	if err != nil {
		return nil, errors.New("deploy: invalid Git revision count output")
	}
	report.CommitsAheadRollback = ahead
	report.CommitsBehindRollback = behind

	statOut, statErr := runCmdTimeout(target.ProjectDir, 30*time.Second, "git", "diff", "--shortstat", "--no-renames", report.RollbackCommit, report.CurrentCommit, "--")
	if statErr != nil {
		return nil, fmt.Errorf("deploy: compare revisions: %w", statErr)
	}
	if err := parseDeployDiffStat(statOut, report); err != nil {
		return nil, err
	}
	return report, nil
}

func (s *Service) latestSuccessfulCommit(targetID int64) (string, error) {
	var commit string
	err := s.db.QueryRow(`
		SELECT "commit" FROM deploy_runs
		WHERE target_id = ? AND status = 'success' AND "commit" != ''
		ORDER BY id DESC LIMIT 1
	`, targetID).Scan(&commit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return commit, err
}

func (s *Service) latestRollbackCommit(targetID int64) (string, error) {
	var commit string
	err := s.db.QueryRow(`
		SELECT prev_commit FROM deploy_runs
		WHERE target_id = ? AND status = 'success' AND prev_commit != ''
		ORDER BY id DESC LIMIT 1
	`, targetID).Scan(&commit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return commit, err
}

func parseDeployDiffStat(output string, report *models.DeployRevisionComparison) error {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	matches := deployDiffStatPattern.FindStringSubmatch(trimmed)
	if matches == nil {
		return errors.New("deploy: invalid Git diff summary output")
	}
	values := []*int{&report.FilesChanged, &report.Insertions, &report.Deletions}
	for index, raw := range matches[1:] {
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil {
			return errors.New("deploy: invalid Git diff summary output")
		}
		*values[index] = value
	}
	return nil
}

func exitCode(err error) int {
	var exitErr interface{ ExitCode() int }
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
