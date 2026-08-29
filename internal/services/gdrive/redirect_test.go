package gdrive

import "testing"

func TestService_effectiveRedirectURI_prefersConfigured(t *testing.T) {
	dir := t.TempDir()
	s := &Service{
		dataDir:            dir,
		oauth:              newOAuthManager(dir),
		configuredRedirect: "https://hserver.example/api/backups/gdrive/oauth/callback",
	}
	got := s.effectiveRedirectURI("http://127.0.0.1:3085/api/backups/gdrive/oauth/callback")
	if got != s.configuredRedirect {
		t.Fatalf("got %q want %q", got, s.configuredRedirect)
	}
}

func TestService_effectiveRedirectURI_prefersToken(t *testing.T) {
	dir := t.TempDir()
	s := &Service{
		dataDir:            dir,
		oauth:              newOAuthManager(dir),
		configuredRedirect: "https://hserver.example/cb",
	}
	token := &tokenData{
		AccessToken:  "a",
		RefreshToken: "r",
		RedirectURI:  "https://stored.example/cb",
	}
	if err := s.oauth.saveToken(token); err != nil {
		t.Fatal(err)
	}
	got := s.effectiveRedirectURI("http://127.0.0.1:3085/cb")
	if got != token.RedirectURI {
		t.Fatalf("got %q want token redirect %q", got, token.RedirectURI)
	}
}
