package php

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const (
	phpFPMReadinessTimeout = 5 * time.Second

	// The readiness inventory is deliberately shallow and bounded. PHP
	// installations are represented by one directory per version and one
	// pool.d directory below it; walking anything deeper would make a status
	// request depend on an unbounded installation-owned tree.
	maxPHPFPMReadinessVersions = 64
	maxPHPFPMReadinessPools    = 256
)

var (
	// ErrNotConfigured identifies an installation without a usable PHP-FPM
	// configuration root. It intentionally contains no host path, command, or
	// command output because readiness errors may cross an aggregate API seam.
	ErrNotConfigured = errors.New("PHP-FPM integration is not configured")

	// errPHPFPMReadinessUnavailable is the safe error for a configured PHP-FPM
	// root whose version, pool, binary, service, or syntax observation failed.
	errPHPFPMReadinessUnavailable = errors.New("PHP-FPM readiness is unavailable")
)

// ProbeReadiness performs one fresh, read-only local PHP-FPM readiness
// observation.
func (s *Service) ProbeReadiness() (integrationstate.State, error) {
	return s.ProbeReadinessContext(context.Background())
}

// ProbeReadinessContext observes the configured PHP-FPM installation without
// mutating it. The configuration root is not_configured when it is absent or
// structurally invalid. Once that root exists, a healthy result requires one
// complete version: a regular pool configuration, an executable php-fpm
// binary, an active matching systemd unit, and a successful `-t` check.
//
// Version and pool discovery is deterministic (os.ReadDir returns lexical
// order) and bounded. Commands are sent through Service's context-aware seam;
// their output and detailed errors never leave this method.
func (s *Service) ProbeReadinessContext(parent context.Context) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, phpFPMReadinessTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if s == nil {
		return integrationstate.NotConfigured, ErrNotConfigured
	}

	if err := phpFPMConfigRootAvailable(s.configRoot); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errPHPFPMReadinessUnavailable
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	versions, err := discoverPHPFPMVersions(ctx, s.configRoot)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errPHPFPMReadinessUnavailable
	}
	if len(versions) == 0 {
		return integrationstate.Unavailable, errPHPFPMReadinessUnavailable
	}

	// A configured PHP binary root is part of the installation boundary. Its
	// absence is unavailable, not not_configured: the PHP configuration root
	// already proved that this integration is installed/configured.
	if err := phpFPMDirectoryAvailable(s.binaryRoot); err != nil {
		return integrationstate.Unavailable, errPHPFPMReadinessUnavailable
	}

	for _, version := range versions {
		if err := ctx.Err(); err != nil {
			return integrationstate.Unavailable, err
		}

		hasPool, err := regularPHPFPMPoolConfig(ctx, s.configRoot, version)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return integrationstate.Unavailable, contextErr
			}
			// One broken version must not hide another complete installed
			// version. The final state remains unavailable when none complete.
			continue
		}
		if !hasPool {
			continue
		}

		binary := filepath.Join(s.binaryRoot, "php-fpm"+version)
		if !executablePHPFPMBinary(binary) {
			continue
		}

		serviceOutput, err := s.runLifecycleCommandContext(ctx, "systemctl", "is-active", "php"+version+"-fpm.service")
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		if err != nil || strings.TrimSpace(string(serviceOutput)) != "active" {
			continue
		}

		_, err = s.runLifecycleCommandContext(ctx, binary, "-t")
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		if err != nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return integrationstate.Unavailable, err
		}
		return integrationstate.Healthy, nil
	}

	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	return integrationstate.Unavailable, errPHPFPMReadinessUnavailable
}

// ProbeContext and Probe are backwards-compatible short names used by other
// local integration services. They intentionally share the same read-only
// implementation and state contract as ProbeReadinessContext.
func (s *Service) ProbeContext(parent context.Context) (integrationstate.State, error) {
	return s.ProbeReadinessContext(parent)
}

func (s *Service) Probe() (integrationstate.State, error) {
	return s.ProbeReadiness()
}

func phpFPMConfigRootAvailable(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return ErrNotConfigured
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotConfigured
		}
		return errPHPFPMReadinessUnavailable
	}
	if !info.IsDir() {
		return errPHPFPMReadinessUnavailable
	}
	return nil
}

func phpFPMDirectoryAvailable(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return errPHPFPMReadinessUnavailable
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return errPHPFPMReadinessUnavailable
	}
	return nil
}

func discoverPHPFPMVersions(ctx context.Context, configRoot string) ([]string, error) {
	entries, err := os.ReadDir(configRoot)
	if err != nil {
		return nil, errPHPFPMReadinessUnavailable
	}

	versions := make([]string, 0, min(len(entries), maxPHPFPMReadinessVersions))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(versions) >= maxPHPFPMReadinessVersions {
			break
		}
		if !entry.IsDir() || !validPHPFPMVersion(entry.Name()) {
			continue
		}
		versions = append(versions, entry.Name())
	}
	return versions, nil
}

func regularPHPFPMPoolConfig(ctx context.Context, configRoot, version string) (bool, error) {
	poolDir := filepath.Join(configRoot, version, "fpm", "pool.d")
	info, err := os.Stat(poolDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, errPHPFPMReadinessUnavailable
	}
	if !info.IsDir() {
		return false, nil
	}

	entries, err := os.ReadDir(poolDir)
	if err != nil {
		return false, errPHPFPMReadinessUnavailable
	}
	inspected := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if inspected >= maxPHPFPMReadinessPools {
			break
		}
		if !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		inspected++
		poolPath := filepath.Join(poolDir, entry.Name())
		poolInfo, err := os.Lstat(poolPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, errPHPFPMReadinessUnavailable
		}
		if poolInfo.Mode().IsRegular() {
			return true, nil
		}
	}
	return false, nil
}

func executablePHPFPMBinary(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func validPHPFPMVersion(version string) bool {
	if version == "" || len(version) > 32 || version[0] == '.' || version[len(version)-1] == '.' {
		return false
	}
	dots := 0
	previousDot := false
	for _, character := range version {
		switch {
		case character == '.':
			if previousDot {
				return false
			}
			dots++
			previousDot = true
		case character >= '0' && character <= '9':
			previousDot = false
		default:
			return false
		}
	}
	return dots > 0 && !previousDot
}
