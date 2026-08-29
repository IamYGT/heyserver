package api

import (
	"encoding/json"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestManagedNodeResponseIncludesCompatibilityMetadata(t *testing.T) {
	response := newManagedNodeResponse(agenthub.Node{
		ID:              "edge-1",
		AgentVersion:    "1.3.0",
		ProtocolVersion: agenthub.ProtocolVersion,
	}, "1.4.0", true)

	if response.Compatibility.AgentVersionState != agenthub.AgentVersionBehind {
		t.Fatalf("AgentVersionState = %q, want %q", response.Compatibility.AgentVersionState, agenthub.AgentVersionBehind)
	}
	if !response.Compatibility.ProtocolCompatible {
		t.Fatal("expected protocol to be compatible")
	}
	if !response.Online {
		t.Fatal("expected server-observed online state")
	}

	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := document["id"]; !ok {
		t.Fatal("node fields were not embedded in the response")
	}
	if _, ok := document["compatibility"]; !ok {
		t.Fatal("compatibility field is missing from the response")
	}
	if _, ok := document["online"]; !ok {
		t.Fatal("online field is missing from the response")
	}
}
