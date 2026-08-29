package api

import (
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/store"
)

func TestValidateMonitorTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		monitor store.UptimeMonitor
		wantErr bool
		errSub  string
	}{
		{
			name:    "valid https",
			monitor: store.UptimeMonitor{Type: "http", URL: "https://example.com/"},
		},
		{
			name:    "loopback rejected",
			monitor: store.UptimeMonitor{Type: "http", URL: "http://127.0.0.1/"},
			wantErr: true,
			errSub:  "blocked",
		},
		{
			name:    "tcp hostname",
			monitor: store.UptimeMonitor{Type: "tcp", Hostname: "example.com", Port: 443},
		},
		{
			name:    "dns hostname",
			monitor: store.UptimeMonitor{Type: "dns", Hostname: "example.com"},
		},
		{
			name:    "missing tcp port",
			monitor: store.UptimeMonitor{Type: "tcp", Hostname: "example.com"},
			wantErr: true,
			errSub:  "port",
		},
		{
			name:    "dns loopback rejected",
			monitor: store.UptimeMonitor{Type: "dns", Hostname: "127.0.0.1"},
			wantErr: true,
			errSub:  "blocked",
		},
		{
			name:    "unsupported type",
			monitor: store.UptimeMonitor{Type: "smtp", Hostname: "example.com"},
			wantErr: true,
			errSub:  "unsupported",
		},
		{
			name:    "missing url for http",
			monitor: store.UptimeMonitor{Type: "http"},
			wantErr: true,
			errSub:  "url",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateMonitorTarget(&tc.monitor)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tc.errSub != "" && !strings.Contains(strings.ToLower(err.Error()), tc.errSub) {
					t.Errorf("error %q should contain %q", err.Error(), tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
