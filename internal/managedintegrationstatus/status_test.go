package managedintegrationstatus

import (
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

func validStatus() ManagedIntegrationStatusResponse {
	return ManagedIntegrationStatusResponse{
		SchemaVersion: SchemaVersion,
		ObservedAt:    time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
		Target:        ManagedIntegrationStatusTarget{Scope: ScopeManagedNode, NodeID: "node-1"},
		Results: []ManagedIntegrationStatusResult{
			{ID: ProcessPM2ID, State: integrationstate.Healthy, Probe: PM2InventoryProbe, DurationMS: 12},
			{ID: DockerID, State: integrationstate.NotConfigured, Probe: DockerInfoProbe, ErrorCode: ErrorCodeNotConfigured, DurationMS: 1},
		},
		Partial: true,
	}
}

func TestDecodeRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	raw, err := validStatus().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := Decode(append(raw[:len(raw)-1], []byte(`,"secret":"value"}`)...)); err == nil {
		t.Fatal("Decode accepted an unknown field")
	}
	if _, err := Decode(append(raw, []byte(` {}`)...)); err == nil {
		t.Fatal("Decode accepted trailing JSON")
	}
}

func TestValidateRejectsNonCanonicalStateAndInventoryShape(t *testing.T) {
	invalid := validStatus()
	invalid.Results[0].State = integrationstate.State("stopped")
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate accepted runtime-only state")
	}
	invalid = validStatus()
	invalid.Results = invalid.Results[:1]
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate accepted an incomplete result set")
	}
	if _, err := Decode([]byte(strings.Repeat("x", MaxResultBytes+1))); err == nil {
		t.Fatal("Decode accepted an oversized result")
	}
}
