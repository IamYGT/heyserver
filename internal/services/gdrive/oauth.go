package gdrive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	driveFileScope    = "https://www.googleapis.com/auth/drive.file"
	driveAboutURL     = "https://www.googleapis.com/drive/v3/about?fields=user,storageQuota"
	driveAboutTimeout = 15 * time.Second
	maxTokenBytes     = 64 << 10
)

type oauthState struct {
	redirectURI string
	userID      int64
	createdAt   time.Time
}

// pendingOAuth holds an authorization code until an authenticated admin completes the flow.
type pendingOAuth struct {
	code        string
	redirectURI string
	userID      int64
	createdAt   time.Time
}

type oauthManager struct {
	clientID     string
	clientSecret string
	dataDir      string
	states       map[string]oauthState
	pending      map[string]pendingOAuth
	mu           sync.Mutex
	refreshMu    sync.Mutex
	httpClient   *http.Client
	aboutURL     string
}

func newOAuthManager(dataDir string) *oauthManager {
	return &oauthManager{
		dataDir:    dataDir,
		states:     make(map[string]oauthState),
		pending:    make(map[string]pendingOAuth),
		httpClient: http.DefaultClient,
		aboutURL:   driveAboutURL,
	}
}

func (o *oauthManager) setCredentials(clientID, clientSecret string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.clientID = clientID
	o.clientSecret = clientSecret
}

func (o *oauthManager) credentials() (clientID, clientSecret string) {
	if o == nil {
		return "", ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.clientID, o.clientSecret
}

func (o *oauthManager) configured() bool {
	clientID, clientSecret := o.credentials()
	return clientID != "" && clientSecret != ""
}

func (o *oauthManager) oauthConfig(redirectURI string) *oauth2.Config {
	clientID, clientSecret := o.credentials()
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       []string{driveFileScope},
		Endpoint:     google.Endpoint,
	}
}

// Start binds OAuth state to the initiating admin user.
func (o *oauthManager) start(redirectURI string, userID int64) (*OAuthStartResponse, error) {
	if !o.configured() {
		return nil, fmt.Errorf("Google OAuth not configured — set HSERVER_GDRIVE_CLIENT_ID and HSERVER_GDRIVE_CLIENT_SECRET")
	}
	state, err := randomState()
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	o.purgeExpired()
	o.states[state] = oauthState{redirectURI: redirectURI, userID: userID, createdAt: time.Now()}
	o.mu.Unlock()

	cfg := o.oauthConfig(redirectURI)
	// prompt=consent is required for refresh_token on re-authorize; select_account alone
	// makes Google skip consent and omit refresh_token (offline access unusable).
	return &OAuthStartResponse{
		AuthURL: cfg.AuthCodeURL(state,
			oauth2.AccessTypeOffline,
			oauth2.SetAuthURLParam("prompt", "consent select_account"),
		),
		State: state,
	}, nil
}

// StoreCallbackCode saves the authorization code from the public callback — does NOT exchange yet.
func (o *oauthManager) storeCallbackCode(code, state string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	st, ok := o.states[state]
	if !ok || time.Since(st.createdAt) > oauthStateTTL {
		return fmt.Errorf("invalid or expired OAuth state")
	}
	delete(o.states, state)
	o.pending[state] = pendingOAuth{
		code:        code,
		redirectURI: st.redirectURI,
		userID:      st.userID,
		createdAt:   time.Now(),
	}
	return nil
}

// Complete exchanges a stored code — only the admin who started the flow may complete it.
func (o *oauthManager) complete(state string, userID int64) (*tokenData, error) {
	o.mu.Lock()
	p, ok := o.pending[state]
	if ok {
		delete(o.pending, state)
	}
	o.mu.Unlock()
	if !ok || time.Since(p.createdAt) > oauthStateTTL {
		return nil, fmt.Errorf("invalid or expired OAuth pending session")
	}
	if p.userID != userID {
		return nil, fmt.Errorf("OAuth session does not belong to this user")
	}
	return o.exchangeCode(p.code, p.redirectURI)
}

func (o *oauthManager) exchangeCode(code, redirectURI string) (*tokenData, error) {
	cfg := o.oauthConfig(redirectURI)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	if tok.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token received — Google hesabında uygulama erişimini kaldırın (myaccount.google.com/permissions), panelden tekrar bağlanın ve izin ekranında Tümünü onayla seçin")
	}
	return &tokenData{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Expiry:       tok.Expiry,
		RedirectURI:  redirectURI,
	}, nil
}

func effectiveRedirectURI(td *tokenData, fallback string) string {
	if td != nil && td.RedirectURI != "" {
		return td.RedirectURI
	}
	return fallback
}

func (o *oauthManager) tokenPath() string {
	return filepath.Join(o.dataDir, tokenFileName)
}

// loadToken performs a bounded, cancellation-aware read. Regular file reads
// cannot be interrupted by a context on all supported filesystems, so the
// seam checks before opening and after the bounded read rather than spawning a
// goroutine that could outlive the caller.
func (o *oauthManager) loadToken(parent context.Context) (*tokenData, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(o.tokenPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if err := parent.Err(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxTokenBytes+1))
	if err != nil {
		return nil, err
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if len(data) > maxTokenBytes {
		return nil, fmt.Errorf("token file exceeds readiness size limit")
	}
	var td tokenData
	if err := json.Unmarshal(data, &td); err != nil {
		return nil, err
	}
	if td.RefreshToken == "" {
		return nil, nil
	}
	return &td, nil
}

func (o *oauthManager) saveToken(td *tokenData) error {
	if err := os.MkdirAll(o.dataDir, 0o750); err != nil {
		return err
	}
	raw, err := json.Marshal(td)
	if err != nil {
		return err
	}
	return os.WriteFile(o.tokenPath(), raw, 0o600)
}

func (o *oauthManager) deleteToken() error {
	err := os.Remove(o.tokenPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// forceRefresh always obtains a fresh access token (for long restic uploads).
func (o *oauthManager) forceRefresh(td *tokenData, redirectURI string) (*tokenData, error) {
	if td == nil {
		return nil, fmt.Errorf("not connected")
	}
	stale := *td
	stale.Expiry = time.Time{}
	return o.refreshIfNeeded(&stale, redirectURI)
}

func (o *oauthManager) refreshIfNeeded(td *tokenData, redirectURI string) (*tokenData, error) {
	if td == nil {
		return nil, fmt.Errorf("not connected")
	}
	redirectURI = effectiveRedirectURI(td, redirectURI)
	if !tokenNeedsRefresh(td) {
		return td, nil
	}

	o.refreshMu.Lock()
	defer o.refreshMu.Unlock()

	// Another goroutine may have refreshed while we waited.
	if fresh, err := o.loadToken(context.Background()); err == nil && fresh != nil && fresh.RefreshToken != "" {
		if !tokenNeedsRefresh(fresh) {
			return fresh, nil
		}
		td = fresh
		redirectURI = effectiveRedirectURI(td, redirectURI)
	}

	cfg := o.oauthConfig(redirectURI)
	// refreshIfNeeded is reached only for a missing/expired access token. oauth2
	// treats a zero Expiry as "does not expire", so passing the persisted zero
	// value would return the stale token without contacting Google's token
	// endpoint. Seed TokenSource with an explicitly expired token to guarantee
	// that the refresh token is actually exchanged.
	src := cfg.TokenSource(context.Background(), refreshSeedToken(td))
	tok, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}
	updated := &tokenData{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Expiry:       tok.Expiry,
		RedirectURI:  redirectURI,
	}
	if updated.RefreshToken == "" {
		updated.RefreshToken = td.RefreshToken
	}
	if updated.RedirectURI == "" {
		updated.RedirectURI = td.RedirectURI
	}
	if updated.AccessToken == "" {
		return nil, fmt.Errorf("token refresh returned empty access token")
	}
	if updated.Expiry.IsZero() || updated.Expiry.Year() < 2000 {
		return nil, fmt.Errorf("token refresh returned invalid expiry")
	}
	if err := o.saveToken(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func refreshSeedToken(td *tokenData) *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  td.AccessToken,
		RefreshToken: td.RefreshToken,
		TokenType:    td.TokenType,
		Expiry:       time.Now().Add(-time.Hour),
	}
}

func (o *oauthManager) fetchAbout(accessToken string) (email, displayName string, quota *StorageQuota, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), driveAboutTimeout)
	defer cancel()
	return o.fetchAboutContext(ctx, accessToken)
}

// fetchAboutContext performs one bounded read-only Drive about observation.
// The caller's context is retained through request creation and transport so a
// readiness aggregate can cancel the provider request. The response body is
// limited and parsed only into the existing account/quota projection; raw
// provider output never becomes part of the readiness error.
func (o *oauthManager) fetchAboutContext(parent context.Context, accessToken string) (email, displayName string, quota *StorageQuota, err error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, driveAboutTimeout)
	defer cancel()

	endpoint := driveAboutURL
	if o != nil && strings.TrimSpace(o.aboutURL) != "" {
		endpoint = o.aboutURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := http.DefaultClient
	if o != nil && o.httpClient != nil {
		client = o.httpClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", nil, fmt.Errorf("drive about API: status %d", resp.StatusCode)
	}

	var parsed struct {
		User struct {
			EmailAddress string `json:"emailAddress"`
			DisplayName  string `json:"displayName"`
		} `json:"user"`
		StorageQuota struct {
			Limit        string `json:"limit"`
			Usage        string `json:"usage"`
			UsageInDrive string `json:"usageInDrive"`
		} `json:"storageQuota"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", nil, err
	}
	if strings.TrimSpace(parsed.User.EmailAddress) == "" {
		return "", "", nil, fmt.Errorf("drive about response missing user email")
	}

	email = parsed.User.EmailAddress
	displayName = parsed.User.DisplayName
	limit := parseInt64(parsed.StorageQuota.Limit)
	usage := parseInt64(parsed.StorageQuota.Usage)
	usageDrive := parseInt64(parsed.StorageQuota.UsageInDrive)

	var pct float64
	if limit > 0 {
		pct = float64(usage) / float64(limit) * 100
	}
	quota = &StorageQuota{
		Limit:           limit,
		Usage:           usage,
		UsageInDrive:    usageDrive,
		UsagePercentage: pct,
	}
	return email, displayName, quota, nil
}

func (o *oauthManager) purgeExpired() {
	cutoff := time.Now().Add(-oauthStateTTL)
	for k, v := range o.states {
		if v.createdAt.Before(cutoff) {
			delete(o.states, k)
		}
	}
	for k, v := range o.pending {
		if v.createdAt.Before(cutoff) {
			delete(o.pending, k)
		}
	}
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func parseInt64(s string) int64 {
	if s == "" {
		return 0
	}
	var n int64
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

// BuildRedirectURIFromRequest constructs the OAuth callback URL from the incoming request.
func BuildRedirectURIFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd == "https" {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s://%s/api/backups/gdrive/oauth/callback", scheme, host)
}

// BuildInternalRedirectURI builds a localhost callback URI for server-side token refresh.
func BuildInternalRedirectURI(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/api/backups/gdrive/oauth/callback", port)
}

// BuildInternalRedirectURIFromPort builds localhost callback for server-side token refresh.
func BuildInternalRedirectURIFromPort(port int) string {
	if port <= 0 {
		port = 3085
	}
	return BuildInternalRedirectURI(port)
}

// OAuthCallbackHTML returns a minimal page shown after OAuth callback in browser.
func OAuthCallbackHTML(success bool, message, state string) string {
	color := "#22c55e"
	title := "Bağlantı başarılı"
	if !success {
		color = "#ef4444"
		title = "Bağlantı başarısız"
	}
	escaped := html.EscapeString(message)
	stateJS := html.EscapeString(state)
	script := ""
	if success && state != "" {
		script = fmt.Sprintf(`<script>
var payload={type:"gdrive-oauth",state:"%s"};
try{if(window.opener){window.opener.postMessage(payload,window.location.origin);}}catch(e){}
try{var channel=new BroadcastChannel("hserver-gdrive-oauth");channel.postMessage(payload);channel.close();}catch(e){}
setTimeout(function(){window.close();},3000);
</script>`, stateJS)
	}
	return fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>HServer — Google Drive</title>
<style>body{font-family:system-ui;background:#18181b;color:#fff;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#27272a;border-radius:12px;padding:2rem;max-width:400px;text-align:center}
h1{color:%s;font-size:1.25rem}</style></head><body><div class="card"><h1>%s</h1><p>%s</p>
<p style="color:#71717a;margin-top:1rem">Panel otomatik tamamlayacak — pencere kapanabilir.</p></div>%s</body></html>`,
		color, title, escaped, script)
}
