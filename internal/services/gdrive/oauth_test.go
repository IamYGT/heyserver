package gdrive

import (
	"strings"
	"testing"
	"time"
)

func TestOAuthPendingFlow_wrongUserRejected(t *testing.T) {
	o := newOAuthManager(t.TempDir())
	o.setCredentials("client-id", "client-secret")
	redirect := "http://localhost/api/backups/gdrive/oauth/callback"

	resp, err := o.start(redirect, 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.storeCallbackCode("auth-code-123", resp.State); err != nil {
		t.Fatal(err)
	}
	_, err = o.complete(resp.State, 99)
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
}

func TestOAuthPendingFlow_exchangeWithoutGoogle(t *testing.T) {
	o := newOAuthManager(t.TempDir())
	o.setCredentials("client-id", "client-secret")
	redirect := "http://localhost/api/backups/gdrive/oauth/callback"
	resp, err := o.start(redirect, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.storeCallbackCode("fake-code", resp.State); err != nil {
		t.Fatal(err)
	}
	_, err = o.complete(resp.State, 7)
	if err == nil {
		t.Fatal("expected Google exchange error in unit test")
	}
}

func TestOAuthStateExpires(t *testing.T) {
	o := newOAuthManager(t.TempDir())
	o.setCredentials("id", "secret")
	o.mu.Lock()
	o.states["expired"] = oauthState{redirectURI: "http://x", userID: 1, createdAt: time.Now().Add(-oauthStateTTL - time.Minute)}
	o.mu.Unlock()

	err := o.storeCallbackCode("code", "expired")
	if err == nil {
		t.Error("expected expired state error")
	}
}

func TestOAuthCallbackHTML_escapesMessage(t *testing.T) {
	out := OAuthCallbackHTML(false, `<script>alert(1)</script>`, "")
	if strings.Contains(out, "<script>alert") {
		t.Error("message should be HTML-escaped")
	}
}

func TestOAuthCallbackHTML_notifiesOpenerAndBroadcastChannel(t *testing.T) {
	out := OAuthCallbackHTML(true, "connected", "state-1")
	for _, expected := range []string{
		`window.opener.postMessage(payload,window.location.origin)`,
		`new BroadcastChannel("hserver-gdrive-oauth")`,
		`state:"state-1"`,
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("callback HTML missing %q", expected)
		}
	}
}
