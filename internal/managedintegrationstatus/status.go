// Package managedintegrationstatus defines the narrow schema-v1 result shared
// by the managed-node agent and the panel control plane.
//
// This package deliberately contains no process or container inventory.  A
// managed-node status result is an allowlisted pair of health observations and
// safe error codes only.
package managedintegrationstatus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const (
	SchemaVersion = 1

	ScopeManagedNode = "managed_node"

	ProcessPM2ID = "process.pm2"
	DockerID     = "container.docker"

	PM2InventoryProbe = "pm2_inventory"
	DockerInfoProbe   = "docker_info"

	ErrorCodeNotConfigured = "not_configured"
	ErrorCodeProbeFailed   = "probe_failed"
	ErrorCodeTimeout       = "timeout"

	// FatalErrorCode is the only task-level error an agent may report for this
	// task kind. Probe failures stay item-level in the completed typed result.
	FatalErrorCode = "integration_status_failed"

	// MaxResultBytes keeps the task result bounded independently of the generic
	// task result limit. The actual schema is much smaller; this is a defensive
	// wire boundary rather than a reason to include raw command output.
	MaxResultBytes = 32 << 10
)

var (
	ErrInvalidResult  = errors.New("invalid managed integration status result")
	ErrResultTooLarge = errors.New("managed integration status result is too large")
)

// ManagedIntegrationStatusResponse is the exact schema-v1 managed-node
// aggregate returned by the read-only status endpoint.
type ManagedIntegrationStatusResponse struct {
	SchemaVersion int                              `json:"schema_version"`
	ObservedAt    time.Time                        `json:"observed_at"`
	Target        ManagedIntegrationStatusTarget   `json:"target"`
	Results       []ManagedIntegrationStatusResult `json:"results"`
	Partial       bool                             `json:"partial"`
}

type ManagedIntegrationStatusTarget struct {
	Scope  string `json:"scope"`
	NodeID string `json:"node_id"`
}

type ManagedIntegrationStatusResult struct {
	ID         string                 `json:"id"`
	State      integrationstate.State `json:"state"`
	Probe      string                 `json:"probe"`
	ErrorCode  string                 `json:"error_code,omitempty"`
	DurationMS int64                  `json:"duration_ms,omitempty"`
}

// Validate checks the complete allowlist and cross-field invariants for the
// typed result. It rejects unknown canonical states, probe/ID mismatches,
// unsafe error values, duplicate/missing observations, and inventory-shaped
// data accidentally sent in place of this schema.
func (r ManagedIntegrationStatusResponse) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrInvalidResult, SchemaVersion)
	}
	if r.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed_at is required", ErrInvalidResult)
	}
	if r.Target.Scope != ScopeManagedNode || !validNodeID(r.Target.NodeID) {
		return fmt.Errorf("%w: target must be a managed_node with a valid node_id", ErrInvalidResult)
	}
	if r.Results == nil || len(r.Results) != 2 {
		return fmt.Errorf("%w: exactly two managed integration results are required", ErrInvalidResult)
	}

	seen := make(map[string]struct{}, len(r.Results))
	partial := false
	for _, result := range r.Results {
		if _, ok := seen[result.ID]; ok {
			return fmt.Errorf("%w: duplicate result id", ErrInvalidResult)
		}
		seen[result.ID] = struct{}{}
		if result.DurationMS < 0 {
			return fmt.Errorf("%w: duration_ms must not be negative", ErrInvalidResult)
		}
		if !result.State.IsValid() {
			return fmt.Errorf("%w: invalid state", ErrInvalidResult)
		}
		switch result.ID {
		case ProcessPM2ID:
			if result.Probe != PM2InventoryProbe {
				return fmt.Errorf("%w: PM2 result has the wrong probe", ErrInvalidResult)
			}
		case DockerID:
			if result.Probe != DockerInfoProbe {
				return fmt.Errorf("%w: Docker result has the wrong probe", ErrInvalidResult)
			}
		default:
			return fmt.Errorf("%w: unknown result id", ErrInvalidResult)
		}
		switch result.State {
		case integrationstate.Healthy:
			if result.ErrorCode != "" {
				return fmt.Errorf("%w: healthy result must not carry an error code", ErrInvalidResult)
			}
		case integrationstate.NotConfigured:
			if result.ErrorCode != ErrorCodeNotConfigured {
				return fmt.Errorf("%w: not_configured result has an invalid error code", ErrInvalidResult)
			}
			partial = true
		case integrationstate.Unavailable:
			if result.ErrorCode != ErrorCodeProbeFailed && result.ErrorCode != ErrorCodeTimeout {
				return fmt.Errorf("%w: unavailable result has an invalid error code", ErrInvalidResult)
			}
			partial = true
		}
	}
	if _, ok := seen[ProcessPM2ID]; !ok {
		return fmt.Errorf("%w: PM2 result is required", ErrInvalidResult)
	}
	if _, ok := seen[DockerID]; !ok {
		return fmt.Errorf("%w: Docker result is required", ErrInvalidResult)
	}
	if r.Partial != partial {
		return fmt.Errorf("%w: partial does not match result states", ErrInvalidResult)
	}
	return nil
}

// Decode strictly decodes one schema-v1 response and rejects unknown fields
// or trailing JSON. The input is bounded before parsing.
func Decode(data []byte) (ManagedIntegrationStatusResponse, error) {
	if len(data) > MaxResultBytes {
		return ManagedIntegrationStatusResponse{}, ErrResultTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result ManagedIntegrationStatusResponse
	if err := decoder.Decode(&result); err != nil {
		return ManagedIntegrationStatusResponse{}, fmt.Errorf("%w: %v", ErrInvalidResult, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return ManagedIntegrationStatusResponse{}, fmt.Errorf("%w: trailing JSON", ErrInvalidResult)
		}
		return ManagedIntegrationStatusResponse{}, fmt.Errorf("%w: trailing JSON: %v", ErrInvalidResult, err)
	}
	if err := result.Validate(); err != nil {
		return ManagedIntegrationStatusResponse{}, err
	}
	return result, nil
}

// Marshal validates the response before putting it on the task wire.
func (r ManagedIntegrationStatusResponse) Marshal() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResult, err)
	}
	if len(data) > MaxResultBytes {
		return nil, ErrResultTooLarge
	}
	return data, nil
}

func validNodeID(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for i, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			if i == 0 && (char == '-' || char == '_' || char == '.') {
				return false
			}
			continue
		}
		return false
	}
	return true
}
