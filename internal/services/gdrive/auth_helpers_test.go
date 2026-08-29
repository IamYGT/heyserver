package gdrive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

func TestIsAuthFailure(t *testing.T) {
	cases := []struct {
		err  string
		want bool
	}{
		{"drive about API: status 401", true},
		{"token refresh failed: invalid_grant", true},
		{"token refresh returned invalid expiry", true},
		{"couldn't find root directory ID: Invalid Credentials", true},
		{"context deadline exceeded", false},
		{"connection reset by peer", false},
	}
	for _, tc := range cases {
		if got := isAuthFailure(errors.New(tc.err)); got != tc.want {
			t.Fatalf("isAuthFailure(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestTokenNeedsRefresh_zeroExpiry(t *testing.T) {
	td := &tokenData{
		AccessToken:  "x",
		RefreshToken: "rt",
		Expiry:       time.Time{},
	}
	if !tokenNeedsRefresh(td) {
		t.Fatal("zero expiry should need refresh")
	}
}

func TestTokenNeedsRefresh_freshToken(t *testing.T) {
	td := &tokenData{
		AccessToken:  "x",
		RefreshToken: "rt",
		Expiry:       time.Now().Add(2 * time.Hour),
	}
	if tokenNeedsRefresh(td) {
		t.Fatal("fresh token should not need refresh")
	}
}

func TestRefreshSeedToken_zeroExpiryIsForcedExpired(t *testing.T) {
	td := &tokenData{
		AccessToken:  "stale-access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
	}
	tok := refreshSeedToken(td)
	if tok.Valid() {
		t.Fatal("refresh seed must be expired so oauth2 exchanges the refresh token")
	}
	if tok.RefreshToken != td.RefreshToken || tok.AccessToken != td.AccessToken {
		t.Fatal("refresh seed must preserve stored token material")
	}
}

func TestHandleAuthError_401_preservesToken(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t)
	s := &Service{
		dataDir:      dir,
		oauth:        newOAuthManager(dir),
		rclone:       newRcloneRunner(dir, "rclone"),
		settingsRepo: store.NewSettingsRepository(db),
	}
	token := &tokenData{
		AccessToken:  "access",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(2 * time.Hour),
	}
	if err := s.oauth.saveToken(token); err != nil {
		t.Fatal(err)
	}
	s.handleAuthError(errors.New("drive about API: status 401"))
	loaded, err := s.oauth.loadToken(context.Background())
	if err != nil || loaded == nil {
		t.Fatal("401 must not delete stored OAuth token")
	}
}

func TestHandleAuthError_invalidGrant_deletesToken(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t)
	s := &Service{
		dataDir:      dir,
		oauth:        newOAuthManager(dir),
		rclone:       newRcloneRunner(dir, "rclone"),
		settingsRepo: store.NewSettingsRepository(db),
	}
	token := &tokenData{
		AccessToken:  "access",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(2 * time.Hour),
	}
	if err := s.oauth.saveToken(token); err != nil {
		t.Fatal(err)
	}
	s.handleAuthError(errors.New("token refresh failed: invalid_grant"))
	if loaded, _ := s.oauth.loadToken(context.Background()); loaded != nil {
		t.Fatal("invalid_grant should delete stored OAuth token")
	}
}

func TestEffectiveRedirectURI_prefersToken(t *testing.T) {
	td := &tokenData{RedirectURI: "https://panel.example/cb"}
	if got := effectiveRedirectURI(td, "http://127.0.0.1:3085/cb"); got != td.RedirectURI {
		t.Fatalf("got %q want %q", got, td.RedirectURI)
	}
}

func TestStatus_fetchAbout401_notConnected(t *testing.T) {
	st := &Status{}
	if st.Connected {
		t.Fatal("connected must be false before probe")
	}
	err := errors.New("drive about API: status 401")
	st.ReconnectRequired = isAuthFailure(err)
	st.Settings.LastError = err.Error()
	if st.Connected || !st.ReconnectRequired {
		t.Fatalf("401 should mark reconnect required, connected=%v reconnect=%v", st.Connected, st.ReconnectRequired)
	}
}
