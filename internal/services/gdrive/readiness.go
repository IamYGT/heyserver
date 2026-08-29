package gdrive

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const gdriveReadinessTimeout = 5 * time.Second

const readinessProtectedFileMode = 0o400

var (
	// ErrNotConfigured identifies an installation where the Google OAuth app,
	// rclone executable, or stored authorization token is absent. The message
	// deliberately contains no installation path, secret, URL, or provider
	// response because this error may cross an aggregate status boundary.
	ErrNotConfigured = errors.New("Google Drive integration is not configured")

	// ErrReadinessNotConfigured is the descriptive readiness alias for callers
	// that distinguish this probe from the transfer/status APIs.
	ErrReadinessNotConfigured = ErrNotConfigured

	// errReadinessUnavailable is the only non-context error returned for a
	// configured installation whose local or Drive observation failed. Detailed
	// subprocess, HTTP, and provider errors stay inside the service boundary.
	errReadinessUnavailable = errors.New("Google Drive readiness is unavailable")

	// ErrReadinessUnavailable is the exported safe sentinel for callers that
	// need to distinguish a configured but currently unavailable Drive probe.
	ErrReadinessUnavailable = errReadinessUnavailable
)

// ProbeReadiness performs one fresh, read-only Google Drive readiness
// observation.
func (s *Service) ProbeReadiness() (integrationstate.State, error) {
	return s.ProbeReadinessContext(context.Background())
}

// ProbeReadinessContext classifies the optional Google Drive integration from
// a bounded local rclone check, a stored OAuth token, and one fresh Drive
// about read:
//
//   - a missing OAuth client, rclone executable, or token is not_configured;
//   - a configured token whose Drive about observation fails is unavailable;
//   - only a successful fresh Drive about response is healthy.
//
// This seam is intentionally separate from Status, ensureReady, and
// RefreshSession. Those operational paths may refresh and persist a token or
// rewrite rclone.conf. A status/readiness observation must not do either: it
// uses the stored access token as-is and leaves installation state unchanged.
// An expired access token therefore reports unavailable until an explicit
// connection or transfer operation refreshes it.
func (s *Service) ProbeReadinessContext(parent context.Context) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, gdriveReadinessTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if s == nil || s.oauth == nil {
		return integrationstate.NotConfigured, ErrNotConfigured
	}

	// Credential synchronization only updates the in-memory OAuth client. It
	// reads the existing environment/vendor/panel configuration and does not
	// persist anything.
	if err := s.syncOAuthCredentialsContext(ctx); err != nil {
		return integrationstate.Unavailable, err
	}
	if !s.oauth.configured() {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if state, err := s.validateReadinessFiles(ctx); state != integrationstate.Healthy {
		return state, err
	}

	rcloneCheck := s.readinessRcloneCheck
	if rcloneCheck == nil {
		if s.rclone == nil {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		rcloneCheck = s.rclone.foundContext
	}
	if err := rcloneCheck(ctx); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		if errors.Is(err, ErrNotConfigured) || isRcloneMissingError(err) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errReadinessUnavailable
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	loadToken := s.readinessLoadToken
	if loadToken == nil {
		loadToken = s.oauth.loadToken
	}
	token, err := loadToken(ctx)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		if errors.Is(err, ErrNotConfigured) || errors.Is(err, os.ErrNotExist) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		// A token file that exists but cannot be read or decoded is a broken
		// configured installation, not evidence that the provider is absent.
		return integrationstate.Unavailable, errReadinessUnavailable
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	// loadToken already rejects an empty refresh token. Treat an incomplete
	// token record without an access token as not configured as well; probing
	// Drive with an empty bearer value would not be a meaningful observation.
	if token == nil || strings.TrimSpace(token.RefreshToken) == "" || strings.TrimSpace(token.AccessToken) == "" {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	fetchAbout := s.readinessFetchAbout
	if fetchAbout == nil {
		fetchAbout = func(ctx context.Context, accessToken string) error {
			_, _, _, err := s.oauth.fetchAboutContext(ctx, accessToken)
			return err
		}
	}
	if err := fetchAbout(ctx, token.AccessToken); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errReadinessUnavailable
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	return integrationstate.Healthy, nil
}

// ProbeContext and Probe are short compatibility forms used by callers that
// register local integrations under a common probe naming convention.
func (s *Service) ProbeContext(parent context.Context) (integrationstate.State, error) {
	return s.ProbeReadinessContext(parent)
}

func (s *Service) Probe() (integrationstate.State, error) {
	return s.ProbeReadiness()
}

// validateReadinessFiles enforces the local credential boundary before any
// subprocess or provider request. Lstat deliberately rejects symlinks, and a
// protected file must be a readable owner-only regular file. Missing files
// mean the optional integration is not configured; malformed permissions or a
// non-regular entry mean a configured installation is unavailable.
func (s *Service) validateReadinessFiles(ctx context.Context) (integrationstate.State, error) {
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if s == nil || s.oauth == nil || strings.TrimSpace(s.oauth.dataDir) == "" || s.rclone == nil || strings.TrimSpace(s.rclone.configPath) == "" {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	for _, path := range []string{s.oauth.tokenPath(), s.rclone.configPath} {
		state, err := validateReadinessFile(ctx, path)
		if state != integrationstate.Healthy {
			return state, err
		}
	}
	return integrationstate.Healthy, nil
}

func validateReadinessFile(ctx context.Context, path string) (integrationstate.State, error) {
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if strings.TrimSpace(path) == "" {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errReadinessUnavailable
	}
	mode := info.Mode()
	if !mode.IsRegular() || mode&os.ModeSymlink != 0 || mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || mode.Perm()&0o077 != 0 || mode.Perm()&readinessProtectedFileMode == 0 {
		return integrationstate.Unavailable, errReadinessUnavailable
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	return integrationstate.Healthy, nil
}
