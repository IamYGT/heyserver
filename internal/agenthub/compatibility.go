package agenthub

import "github.com/IamYGT/heyserver/internal/releaseversion"

type AgentVersionState = releaseversion.State

const (
	AgentVersionCurrent = releaseversion.Current
	AgentVersionBehind  = releaseversion.Behind
	AgentVersionAhead   = releaseversion.Ahead
	AgentVersionUnknown = releaseversion.Unknown
)

// NodeCompatibility describes whether a managed agent can speak the panel's
// protocol and whether its release version has drifted from the panel release.
// Development and commit-derived versions are deliberately reported as
// unknown because they do not have a reliable ordering.
type NodeCompatibility struct {
	PanelVersion       string            `json:"panel_version"`
	ExpectedProtocol   string            `json:"expected_protocol"`
	ProtocolCompatible bool              `json:"protocol_compatible"`
	AgentVersionState  AgentVersionState `json:"agent_version_state"`
}

func Compatibility(agentVersion, protocolVersion, panelVersion string) NodeCompatibility {
	return NodeCompatibility{
		PanelVersion:       panelVersion,
		ExpectedProtocol:   ProtocolVersion,
		ProtocolCompatible: protocolVersion == ProtocolVersion,
		AgentVersionState:  releaseversion.Compare(agentVersion, panelVersion),
	}
}
