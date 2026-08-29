package bind

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const readinessTimeout = 5 * time.Second

// ErrNotConfigured identifies an installation where BIND or one of the
// read-only readiness prerequisites is absent. It intentionally carries no
// host path or command output because readiness errors may be consumed by an
// aggregate API.
var ErrNotConfigured = errors.New("BIND integration is not configured")

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Probe performs one fresh, read-only BIND readiness observation.
func (s *Service) Probe() (integrationstate.State, error) {
	return s.ProbeContext(context.Background())
}

// ProbeContext verifies that the native BIND executable, its standard local
// configuration, and named-checkconf are present, then observes the systemd
// unit and validates the loaded configuration (including registered zones)
// with the existing `named-checkconf -z` behavior. It never mutates or reloads
// BIND. Only a canonical integration state and a sanitized internal error are
// returned; command output and host paths never cross this seam.
func (s *Service) ProbeContext(parent context.Context) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, readinessTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if s == nil {
		return integrationstate.NotConfigured, ErrNotConfigured
	}

	configPath := s.configPath
	if configPath == "" {
		configPath = namedConf
	}
	if err := readinessConfigAvailable(configPath); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errors.New("BIND configuration is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	lookPath := s.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	for _, prerequisite := range []string{namedBin, namedCheckBin, namedCheckZone, rndcBin} {
		resolved, err := lookPath(prerequisite)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
				return integrationstate.NotConfigured, ErrNotConfigured
			}
			return integrationstate.Unavailable, errors.New("BIND prerequisite discovery failed")
		}
		if strings.TrimSpace(resolved) == "" {
			return integrationstate.Unavailable, errors.New("BIND prerequisite discovery failed")
		}
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	runner := s.runner
	if runner == nil {
		return integrationstate.Unavailable, errors.New("BIND readiness command runner is unavailable")
	}

	serviceState, err := observeBindServiceContext(ctx, runner)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errors.New("BIND service state could not be observed")
	}
	if serviceState != "active" {
		return integrationstate.Unavailable, errors.New("BIND service is not active")
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	if _, err := runner.Run(ctx, namedCheckBin, "-z"); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errors.New("BIND configuration validation failed")
	}
	return integrationstate.Healthy, nil
}

// ProbeReadiness and ProbeReadinessContext mirror the naming used by other
// local service probes while keeping ProbeContext as the small aggregate seam.
func (s *Service) ProbeReadiness() (integrationstate.State, error) {
	return s.ProbeContext(context.Background())
}

func (s *Service) ProbeReadinessContext(ctx context.Context) (integrationstate.State, error) {
	return s.ProbeContext(ctx)
}

func readinessConfigAvailable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotConfigured
		}
		return fmt.Errorf("inspect BIND configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ErrNotConfigured
	}
	return nil
}

func observeBindServiceContext(ctx context.Context, runner commandRunner) (string, error) {
	var lastErr error
	for _, unit := range []string{"named", "bind9"} {
		output, err := runner.Run(ctx, "systemctl", "is-active", unit)
		if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		}
		state := strings.TrimSpace(string(output))
		if err == nil {
			if knownServiceState(state) {
				return state, nil
			}
			return "", errors.New("BIND service state was not recognized")
		}
		if knownServiceState(state) {
			// systemctl uses a non-zero exit status for inactive/failed units;
			// that is a valid stopped observation, not a missing observer.
			if state == "active" {
				return "", errors.New("BIND service observation failed")
			}
			if state != "unknown" {
				return state, nil
			}
			lastErr = errors.New("BIND service unit was not found")
			continue
		}
		// A unit-not-found response is the only failed first observation for
		// which the alternate distro unit is worth trying. Other failures are
		// genuine systemd observation failures and remain unavailable. `unknown`
		// is the token systemctl emits for a unit that does not exist.
		if unit == "named" && isMissingBindUnitObservation(state) {
			lastErr = errors.New("BIND service unit was not found")
			continue
		}
		return "", errors.New("BIND service state could not be observed")
	}
	if lastErr == nil {
		lastErr = errors.New("BIND service state could not be observed")
	}
	return "", lastErr
}

func isMissingBindUnitObservation(output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	return lower == "unknown" || strings.Contains(lower, "not-found") || strings.Contains(lower, "not found")
}
