package agenthub

import "testing"

func TestCompatibilityClassifiesProtocolAndReleaseDrift(t *testing.T) {
	tests := []struct {
		name         string
		agentVersion string
		panelVersion string
		protocol     string
		wantVersion  AgentVersionState
		wantProtocol bool
	}{
		{name: "current", agentVersion: "v1.2.3", panelVersion: "1.2.3", protocol: ProtocolVersion, wantVersion: AgentVersionCurrent, wantProtocol: true},
		{name: "behind patch", agentVersion: "1.2.2", panelVersion: "1.2.3", protocol: ProtocolVersion, wantVersion: AgentVersionBehind, wantProtocol: true},
		{name: "behind major", agentVersion: "0.99.99", panelVersion: "1.0.0", protocol: ProtocolVersion, wantVersion: AgentVersionBehind, wantProtocol: true},
		{name: "ahead", agentVersion: "2.0.0", panelVersion: "1.9.9", protocol: ProtocolVersion, wantVersion: AgentVersionAhead, wantProtocol: true},
		{name: "development build", agentVersion: "dev", panelVersion: "dev", protocol: ProtocolVersion, wantVersion: AgentVersionUnknown, wantProtocol: true},
		{name: "commit build", agentVersion: "ci-a1b2c3", panelVersion: "1.2.3", protocol: ProtocolVersion, wantVersion: AgentVersionUnknown, wantProtocol: true},
		{name: "prerelease", agentVersion: "1.2.3-rc.1", panelVersion: "1.2.3", protocol: ProtocolVersion, wantVersion: AgentVersionUnknown, wantProtocol: true},
		{name: "protocol mismatch", agentVersion: "1.2.3", panelVersion: "1.2.3", protocol: "v0", wantVersion: AgentVersionCurrent, wantProtocol: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Compatibility(test.agentVersion, test.protocol, test.panelVersion)
			if got.AgentVersionState != test.wantVersion {
				t.Fatalf("AgentVersionState = %q, want %q", got.AgentVersionState, test.wantVersion)
			}
			if got.ProtocolCompatible != test.wantProtocol {
				t.Fatalf("ProtocolCompatible = %t, want %t", got.ProtocolCompatible, test.wantProtocol)
			}
			if got.ExpectedProtocol != ProtocolVersion || got.PanelVersion != test.panelVersion {
				t.Fatalf("metadata = %#v", got)
			}
		})
	}
}
