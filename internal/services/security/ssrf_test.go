package security_test

import (
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/security"
)

func TestValidateOutboundURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
		errSub  string
	}{
		{"empty URL", "", true, "URL must not be empty"},
		{"file scheme", "file:///etc/passwd", true, "scheme"},
		{"ftp scheme", "ftp://example.com", true, "scheme"},
		{"loopback literal", "https://127.0.0.1/health", true, "blocked range"},
		{"private class A", "http://10.0.0.1/api", true, "blocked range"},
		{"link-local metadata", "https://169.254.169.254/latest/meta-data", true, "blocked range"},
		{"no hostname", "https:///path", true, "no hostname"},
		{"public IPv4 literal 8.8.8.8", "https://8.8.8.8/dns-query", false, ""},
		{"public IPv4 literal 1.1.1.1", "https://1.1.1.1/", false, ""},
		{"IPv4-mapped loopback", "https://[::ffff:127.0.0.1]/", true, "blocked range"},
		{"public IPv6 literal", "https://[2001:4860:4860::8888]/dns-query", false, ""},
		{"TEST-NET-3 IPv6 doc", "https://[2001:db8::1]/", false, ""},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := security.ValidateOutboundURL(tc.rawURL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateOutboundURL(%q): expected error", tc.rawURL)
				}
				if tc.errSub != "" && !strings.Contains(err.Error(), tc.errSub) {
					t.Errorf("error %q should contain %q", err.Error(), tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateOutboundURL(%q): unexpected error: %v", tc.rawURL, err)
			}
		})
	}
}

func TestValidateWebhookURL(t *testing.T) {
	t.Parallel()

	t.Run("valid public IPv6", func(t *testing.T) {
		t.Parallel()
		if err := security.ValidateWebhookURL("https://[2001:4860:4860::8888]/hook"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wraps validation error", func(t *testing.T) {
		t.Parallel()
		err := security.ValidateWebhookURL("")
		if err == nil {
			t.Fatal("expected error for empty URL")
		}
		msg := err.Error()
		if !strings.Contains(msg, "webhook URL validation failed") {
			t.Errorf("error %q should contain webhook prefix", msg)
		}
		if !strings.Contains(msg, "URL must not be empty") {
			t.Errorf("error %q should wrap underlying ssrf error", msg)
		}
	})

	t.Run("wraps blocked IP error", func(t *testing.T) {
		t.Parallel()
		err := security.ValidateWebhookURL("http://127.0.0.1/callback")
		if err == nil {
			t.Fatal("expected error for loopback URL")
		}
		msg := err.Error()
		if !strings.Contains(msg, "webhook URL validation failed") {
			t.Errorf("error %q should contain webhook prefix", msg)
		}
		if !strings.Contains(msg, "blocked range") {
			t.Errorf("error %q should wrap blocked IP detail", msg)
		}
	})
}
