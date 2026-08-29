package uptime

import (
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

func TestTestCheck_unknownType(t *testing.T) {
	t.Parallel()

	m := &store.UptimeMonitor{
		ID:   42,
		Name: "bad-type",
		Type: "websocket",
	}

	before := time.Now()
	result := TestCheck(m)
	after := time.Now()

	if result.MonitorID != 42 {
		t.Errorf("MonitorID = %d, want 42", result.MonitorID)
	}
	if result.Status != StatusDown {
		t.Errorf("Status = %d, want StatusDown (%d)", result.Status, StatusDown)
	}
	if !strings.Contains(result.Msg, "unknown monitor type") {
		t.Errorf("Msg = %q, want unknown type message", result.Msg)
	}
	if result.CheckedAt.Before(before) || result.CheckedAt.After(after) {
		t.Errorf("CheckedAt %v outside [%v, %v]", result.CheckedAt, before, after)
	}
}

func TestTestCheck_preservesMonitorID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		monitorTyp string
		wantStatus int
		wantMsgSub string
	}{
		{
			name:       "empty type",
			monitorTyp: "",
			wantStatus: StatusDown,
			wantMsgSub: "unknown monitor type",
		},
		{
			name:       "invalid type",
			monitorTyp: "smtp",
			wantStatus: StatusDown,
			wantMsgSub: "unknown monitor type: smtp",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := &store.UptimeMonitor{ID: 7, Type: tc.monitorTyp}
			result := TestCheck(m)

			if result.MonitorID != 7 {
				t.Errorf("MonitorID = %d, want 7", result.MonitorID)
			}
			if result.Status != tc.wantStatus {
				t.Errorf("Status = %d, want %d", result.Status, tc.wantStatus)
			}
			if !strings.Contains(result.Msg, tc.wantMsgSub) {
				t.Errorf("Msg = %q, want substring %q", result.Msg, tc.wantMsgSub)
			}
		})
	}
}

func TestStatusConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value int
		want  int
	}{
		{"StatusDown", StatusDown, 0},
		{"StatusUp", StatusUp, 1},
		{"StatusPending", StatusPending, 2},
		{"StatusMaintenance", StatusMaintenance, 3},
		{"StatusTLSWarn", StatusTLSWarn, 4},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.value != tc.want {
				t.Errorf("%s = %d, want %d", tc.name, tc.value, tc.want)
			}
		})
	}
}
