package snapshot

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"time"

	"github.com/IamYGT/heyserver/internal/rcloneprofile"
)

// PurgeRemoteRepo deletes only the currently configured restic repository
// folder after the caller confirms the exact observed repository identity.
func (s *Service) PurgeRemoteRepo(req PurgeRequest, redirectURI string) error {
	st, err := s.loadSettings()
	if err != nil {
		return err
	}
	if !repoFolderPattern.MatchString(st.RepoFolder) || path.Clean(st.RepoFolder) != st.RepoFolder {
		return fmt.Errorf("%w: persisted repoFolder is not a safe relative repository path", ErrSettingsUnavailable)
	}
	if req.Confirmation != PurgeConfirmation {
		return fmt.Errorf("%w: confirmation must equal %q", ErrInvalidPurgeRequest, PurgeConfirmation)
	}
	if req.RepoFolder != st.RepoFolder {
		return fmt.Errorf("%w: repoFolder no longer matches the observed repository", ErrInvalidPurgeRequest)
	}
	if normalizedDestination(st.Destination) != DestinationGoogleDrive {
		return fmt.Errorf("%w: repository purge is not available for %s destinations", ErrUnsupportedCapability, normalizedDestination(st.Destination))
	}
	if s.drive == nil {
		return fmt.Errorf("%w: Google Drive is not configured", ErrDestinationUnavailable)
	}
	if err := s.drive.EnsureReady(redirectURI); err != nil {
		return fmt.Errorf("Google Drive not ready: %w", err)
	}
	r, err := s.runner(st)
	if err != nil {
		return err
	}
	remote := fmt.Sprintf("%s:%s", rcloneprofile.RemoteName, st.RepoFolder)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.rcloneBin,
		"--config", r.rcloneConfig,
		"purge", remote,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rclone purge %s: %w — %s", remote, err, truncate(string(out), 400))
	}
	s.invalidateStatusCache()
	return nil
}
