package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/firewall"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestIntegration_FirewallStatusReadiness(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleFirewallStatus().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/firewall/status", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/firewall/status status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var status firewall.Status
	testutil.ParseJSON(t, recorder, &status)
	if status.State == "" {
		t.Fatal("firewall status has no structured readiness state")
	}
	if status.Backend == "" {
		t.Fatal("firewall status has no observed backend")
	}
	if status.Rules == nil {
		t.Fatal("firewall status returned a null rule inventory")
	}
	if status.Available && (status.State != firewall.StateHealthy || status.Backend != "ufw") {
		t.Fatalf("manageable firewall status is inconsistent: %#v", status)
	}
}

func TestFirewallMutationHandlersRejectUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		h    http.HandlerFunc
		body string
	}{
		{
			name: "add rule",
			h:    handleFirewallAdd(),
			body: `{"action":"allow","source":"203.0.113.10"}`,
		},
		{
			name: "toggle",
			h:    handleFirewallToggle(),
			body: `{"enable":true,"unexpected":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			tt.h(recorder, httptest.NewRequest(http.MethodPost, "/api/firewall", bytes.NewBufferString(tt.body)))
			if recorder.Code != http.StatusBadRequest || !bytes.Contains(recorder.Body.Bytes(), []byte("invalid request body")) {
				t.Fatalf("response = %d %s, want 400 invalid request body", recorder.Code, recorder.Body.String())
			}
		})
	}
}
