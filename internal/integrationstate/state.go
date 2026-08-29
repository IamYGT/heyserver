// Package integrationstate defines the small wire contract shared by optional
// provider integrations. Runtime-specific states such as "stopped" and UI
// presentation values such as "not-configured" deliberately do not belong to
// this package's wire state.
package integrationstate

import (
	"errors"
	"fmt"
	"strings"
)

// State is the canonical availability state sent over the API.
type State string

const (
	NotConfigured State = "not_configured"
	Unavailable   State = "unavailable"
	Healthy       State = "healthy"
)

// ErrInvalidState identifies a value outside the optional-integration wire
// contract.
var ErrInvalidState = errors.New("invalid optional integration state")

// Observation is the caller-owned result of checking an optional
// integration. Healthy is returned only when Configured and Successful are
// both explicitly true; configuration alone is never evidence of health.
type Observation struct {
	Configured bool
	Successful bool
}

// IsValid reports whether s is one of the exact wire values in this package.
func (s State) IsValid() bool {
	switch s {
	case NotConfigured, Unavailable, Healthy:
		return true
	default:
		return false
	}
}

// IsValid reports whether raw is an exact optional-integration wire value.
func IsValid(raw string) bool {
	return State(raw).IsValid()
}

// Validate accepts only the exact wire spellings. It rejects presentation or
// runtime aliases such as "not-configured" and "stopped" rather than
// silently changing their meaning.
func Validate(raw string) error {
	if State(raw).IsValid() {
		return nil
	}
	return fmt.Errorf("%w %q (want %q, %q, or %q)", ErrInvalidState, raw, NotConfigured, Unavailable, Healthy)
}

// Normalize validates and canonicalizes an incoming state for wire output.
// Unknown, presentation-only, and runtime-only values return an error instead
// of being promoted to a healthy integration.
func Normalize(raw string) (State, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if err := Validate(normalized); err != nil {
		return "", err
	}
	return State(normalized), nil
}

// FromObservation derives the wire state from an explicit caller-provided
// observation. A configured integration without a successful observation is
// unavailable, not healthy.
func FromObservation(observation Observation) State {
	if !observation.Configured {
		return NotConfigured
	}
	if !observation.Successful {
		return Unavailable
	}
	return Healthy
}
