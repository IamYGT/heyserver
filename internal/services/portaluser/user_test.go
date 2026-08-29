package portaluser

import (
	"strings"
	"testing"
)

func TestUsernameForDomainStableAndShort(t *testing.T) {
	domain := "portal.example.com"
	u1 := UsernameForDomain(domain)
	u2 := UsernameForDomain(domain)
	if u1 != u2 {
		t.Fatalf("expected stable username, got %q and %q", u1, u2)
	}
	if len(u1) > maxUsernameLen {
		t.Fatalf("username too long: %q", u1)
	}
	if !strings.HasPrefix(u1, usernamePrefix) {
		t.Fatalf("expected prefix %q, got %q", usernamePrefix, u1)
	}
}

func TestUsernameForDomainDiffersPerDomain(t *testing.T) {
	a := UsernameForDomain("a.example.com")
	b := UsernameForDomain("b.example.com")
	if a == b {
		t.Fatalf("expected different usernames for different domains")
	}
}

func TestPortalRootFromWebRoot(t *testing.T) {
	cases := map[string]string{
		"/var/www/vhosts/example.com/portal.example.com/public":   "/var/www/vhosts/example.com/portal.example.com",
		"/var/www/vhosts/example.net/sub.example.net/public_html": "/var/www/vhosts/example.net/sub.example.net",
		"/var/www/portal": "/var/www/portal",
	}
	for webRoot, want := range cases {
		if got := PortalRootFromWebRoot(webRoot); got != want {
			t.Fatalf("PortalRootFromWebRoot(%q) = %q, want %q", webRoot, got, want)
		}
	}
}

func TestOpenBasedirForPortal(t *testing.T) {
	got := OpenBasedirForPortal("/var/www/vhosts/example.com/trial.example.com")
	want := "/var/www/vhosts/example.com/trial.example.com:/tmp/:/usr/share/php/"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
