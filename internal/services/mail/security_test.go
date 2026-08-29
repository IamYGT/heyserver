package mail

import (
	"testing"
)

func TestParseFailedLoginLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		line    string
		wantOK  bool
		wantIP  string
		wantAcc string
	}{
		{
			name:    "authentication_failed",
			line:    "1705320896 WARN Authentication failed for user@example.com from 1.2.3.4",
			wantOK:  true,
			wantIP:  "1.2.3.4",
			wantAcc: "user@example.com",
		},
		{
			name:    "login_failed",
			line:    "1705320896 ERROR Login failed from 2001:db8::1 for admin@example.org",
			wantOK:  true,
			wantIP:  "2001:db8::1",
			wantAcc: "admin@example.org",
		},
		{
			name:   "auth_error",
			line:   "1705320896 WARN Auth error from 10.0.0.5",
			wantOK: true,
			wantIP: "10.0.0.5",
		},
		{
			name:   "unrelated_line",
			line:   "2024-01-15T12:34:56Z INFO Message delivered",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry, ok := parseFailedLoginLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("parseFailedLoginLine() ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if entry.Timestamp != "1705320896" {
				t.Fatalf("Timestamp = %q", entry.Timestamp)
			}
			if entry.IP != tc.wantIP {
				t.Fatalf("IP = %q, want %q", entry.IP, tc.wantIP)
			}
			if entry.Account != tc.wantAcc {
				t.Fatalf("Account = %q, want %q", entry.Account, tc.wantAcc)
			}
			if entry.Reason != "authentication failed" {
				t.Fatalf("Reason = %q", entry.Reason)
			}
		})
	}
}

func TestIsIPLike(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{"1.2.3.4", true},
		{"1.2.3.4,", true},
		{"2001:db8::1", true},
		{"example.com", false},
		{"12.34", false},
		{"", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := isIPLike(tc.in); got != tc.want {
				t.Fatalf("isIPLike(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseIntSecurity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		def  int
		want int
	}{
		{"42", 10, 42},
		{"invalid", 10, 10},
		{"", 7, 7},
		{"0", 99, 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := parseInt(tc.in, tc.def); got != tc.want {
				t.Fatalf("parseInt(%q, %d) = %d, want %d", tc.in, tc.def, got, tc.want)
			}
		})
	}
}

func TestToInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   interface{}
		want int
	}{
		{"float64", float64(12.9), 12},
		{"int", 5, 5},
		{"int64", int64(8), 8},
		{"string", "nope", 0},
		{"nil", nil, 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := toInt(tc.in); got != tc.want {
				t.Fatalf("toInt() = %d, want %d", got, tc.want)
			}
		})
	}
}
