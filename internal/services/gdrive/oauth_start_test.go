package gdrive

import (
	"net/url"
	"strings"
	"testing"
)

func TestOAuthStart_requestsOfflineConsent(t *testing.T) {
	o := newOAuthManager(t.TempDir())
	o.setCredentials("client-id", "client-secret")
	resp, err := o.start("https://hserver.example.com/api/backups/gdrive/oauth/callback", 1)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(resp.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("access_type") != "offline" {
		t.Fatalf("access_type=%q want offline", q.Get("access_type"))
	}
	prompt := q.Get("prompt")
	if !strings.Contains(prompt, "consent") {
		t.Fatalf("prompt=%q must include consent for refresh_token", prompt)
	}
}
