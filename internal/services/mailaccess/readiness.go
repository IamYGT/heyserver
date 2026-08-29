// Package mailaccess provides the provider-neutral readiness observation for
// the optional mail.access catalog integration.
//
// Mail access is deliberately a credentials-free reachability check.  It
// never authenticates, performs an IMAP/SMTP handshake, or writes to a
// provider.  A complete settings snapshot is healthy only when both HTTP
// endpoints and all three mail TCP endpoints are reachable in the same
// invocation.
package mailaccess

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const (
	WebmailURLKey           = "webmail_url"
	MailAdminURLKey         = "mail_admin_url"
	MailServerHostKey       = "mail_server_host"
	MailIMAPPortKey         = "mail_imap_port"
	MailSMTPStartTLSPortKey = "mail_smtp_starttls_port"
	MailSMTPSSLPortKey      = "mail_smtp_ssl_port"

	// ReadinessTimeout bounds one complete external observation.  A caller's
	// earlier deadline or cancellation always wins over this upper bound.
	ReadinessTimeout = 5 * time.Second
	// DialTimeout bounds each TCP connect and each HTTP transport connection.
	// The parent readiness context remains the final bound for every check.
	DialTimeout = 2 * time.Second

	maxSettingLength = 2048
	maxHostLength    = 253
)

// CanonicalSettingsKeys is the six-key input contract for this package.  A
// caller may pass a larger settings snapshot; only these keys are consumed.
var CanonicalSettingsKeys = [...]string{
	WebmailURLKey,
	MailAdminURLKey,
	MailServerHostKey,
	MailIMAPPortKey,
	MailSMTPStartTLSPortKey,
	MailSMTPSSLPortKey,
}

var canonicalSettingsKeys = CanonicalSettingsKeys

var (
	// ErrNotConfigured is the only public validation failure.  It intentionally
	// does not include a setting value, URL, host, or parser diagnostic.
	ErrNotConfigured = errors.New("mail access is not configured")
	// ErrReadinessNotConfigured is the descriptive readiness alias used by
	// callers that keep several optional integration sentinels together.
	ErrReadinessNotConfigured = ErrNotConfigured
	// ErrUnavailable is the sanitized public result for a configured snapshot
	// whose fresh reachability observation did not complete successfully.
	ErrUnavailable = errors.New("mail access is unavailable")
	// ErrReadinessUnavailable is the descriptive readiness alias for the safe
	// unavailable sentinel.
	ErrReadinessUnavailable = ErrUnavailable
)

// HTTPDoer is the narrow HTTP seam used by the readiness probe.  Production
// uses a bounded *http.Client; tests can inject a fake without network I/O.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPClient is a compatibility alias for callers that prefer the concrete
// seam's descriptive name.
type HTTPClient = HTTPDoer

// DialContextFunc is the narrow TCP seam used by all three mail checks.
// Implementations must honor ctx.  The production implementation is
// net.Dialer.DialContext.
type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Dependencies contains optional test seams.  A nil field selects the
// bounded production implementation; no provider credentials are accepted.
type Dependencies struct {
	HTTPClient  HTTPDoer
	DialContext DialContextFunc
}

// ProbeDependencies is a descriptive alias for Dependencies.
type ProbeDependencies = Dependencies

// ProbeReadiness performs one fresh mail.access observation with a background
// context.
func ProbeReadiness(settings map[string]string) (State, error) {
	return ProbeReadinessContext(context.Background(), settings)
}

// Probe is the short compatibility form used by local integration registries.
func Probe(settings map[string]string) (State, error) {
	return ProbeReadiness(settings)
}

// ProbeContext is the short context-aware compatibility form.
func ProbeContext(parent context.Context, settings map[string]string) (State, error) {
	return ProbeReadinessContext(parent, settings)
}

// ProbeReadinessContext performs one fresh, bounded, read-only observation.
//
// Missing, partial, or invalid settings return not_configured without making a
// network request.  Complete valid settings launch exactly five concurrent
// checks: webmail HTTP, admin HTTP, IMAP TCP, SMTP STARTTLS TCP, and SMTP SSL
// TCP.  HTTP status codes in the 2xx, 3xx, and 4xx ranges count as reachable;
// all 5xx responses and transport failures are unavailable.  No request has
// credentials or a body, and every successful connection is closed promptly.
func ProbeReadinessContext(parent context.Context, settings map[string]string) (State, error) {
	return probeReadinessContext(parent, settings, Dependencies{})
}

// ProbeReadinessContextWithDependencies is the injectable form used by
// focused tests and by callers that own a bounded transport.  Dependencies
// are transport seams only; settings still undergo this package's strict
// validation before any seam is called.
func ProbeReadinessContextWithDependencies(parent context.Context, settings map[string]string, deps Dependencies) (State, error) {
	return probeReadinessContext(parent, settings, deps)
}

// ProbeContextWithDependencies is the short alias for the injectable form.
func ProbeContextWithDependencies(parent context.Context, settings map[string]string, deps Dependencies) (State, error) {
	return ProbeReadinessContextWithDependencies(parent, settings, deps)
}

// ProbeReadinessContextWithDeps is a short alias for the injectable form.
func ProbeReadinessContextWithDeps(parent context.Context, settings map[string]string, deps Dependencies) (State, error) {
	return ProbeReadinessContextWithDependencies(parent, settings, deps)
}

// ProbeContextWithDeps is the short alias for ProbeReadinessContextWithDeps.
func ProbeContextWithDeps(parent context.Context, settings map[string]string, deps Dependencies) (State, error) {
	return ProbeReadinessContextWithDependencies(parent, settings, deps)
}

// ValidateSettings validates the six canonical values without contacting any
// endpoint.  It is safe for a settings API to call because failures expose no
// raw URL, host, port parser output, or other input text.
func ValidateSettings(values map[string]string) error {
	_, err := validatedSettings(values)
	return err
}

// ValidateSettingsSnapshot is the explicit snapshot-named alias for
// ValidateSettings.
func ValidateSettingsSnapshot(values map[string]string) error {
	return ValidateSettings(values)
}

// State is the canonical optional-integration state returned by this package.
// It is an alias so consumers can use the same state type as other services.
type State = integrationstate.State

type settingsSnapshot struct {
	webmailURL       string
	adminURL         string
	serverHost       string
	imapPort         string
	smtpStartTLSPort string
	smtpSSLPort      string
}

type readinessResult struct {
	err error
}

const readinessCheckCount = 5

func probeReadinessContext(parent context.Context, values map[string]string, deps Dependencies) (integrationstate.State, error) {
	settings, err := validatedSettings(values)
	if err != nil {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, ReadinessTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	client, cleanupClient := readinessHTTPClient(deps.HTTPClient)
	defer cleanupClient()
	dialContext := deps.DialContext
	if dialContext == nil {
		dialer := &net.Dialer{Timeout: DialTimeout}
		dialContext = dialer.DialContext
	}

	checks := []func(context.Context) error{
		func(ctx context.Context) error { return checkHTTP(ctx, client, settings.webmailURL) },
		func(ctx context.Context) error { return checkHTTP(ctx, client, settings.adminURL) },
		func(ctx context.Context) error {
			return checkTCP(ctx, dialContext, settings.serverHost, settings.imapPort)
		},
		func(ctx context.Context) error {
			return checkTCP(ctx, dialContext, settings.serverHost, settings.smtpStartTLSPort)
		},
		func(ctx context.Context) error {
			return checkTCP(ctx, dialContext, settings.serverHost, settings.smtpSSLPort)
		},
	}

	// The channel is as large as the fan-out.  A caller cancellation therefore
	// cannot strand a completed check trying to publish its result while this
	// function drains the other context-aware checks.
	results := make(chan readinessResult, readinessCheckCount)
	for _, check := range checks {
		go func(check func(context.Context) error) {
			results <- readinessResult{err: check(ctx)}
		}(check)
	}

	failed := false
	var cancelled error
	for completed := 0; completed < readinessCheckCount; {
		var done <-chan struct{}
		if cancelled == nil {
			done = ctx.Done()
		}
		select {
		case result := <-results:
			completed++
			if result.err != nil {
				failed = true
			}
		case <-done:
			// Disable this select arm after observing cancellation and drain all
			// five results.  The production dialer/client and test seams are
			// context-aware, so this guarantees no probe goroutine survives the
			// return path.
			cancelled = ctx.Err()
		}
	}
	if cancelled != nil {
		return integrationstate.Unavailable, cancelled
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if failed {
		return integrationstate.Unavailable, ErrUnavailable
	}
	return integrationstate.Healthy, nil
}

func validatedSettings(values map[string]string) (settingsSnapshot, error) {
	if values == nil {
		return settingsSnapshot{}, ErrNotConfigured
	}
	for _, key := range canonicalSettingsKeys {
		value, ok := values[key]
		if !ok || !validRequiredValue(value) {
			return settingsSnapshot{}, ErrNotConfigured
		}
	}

	result := settingsSnapshot{
		webmailURL:       values[WebmailURLKey],
		adminURL:         values[MailAdminURLKey],
		serverHost:       values[MailServerHostKey],
		imapPort:         values[MailIMAPPortKey],
		smtpStartTLSPort: values[MailSMTPStartTLSPortKey],
		smtpSSLPort:      values[MailSMTPSSLPortKey],
	}
	if !validHTTPURL(result.webmailURL) || !validHTTPURL(result.adminURL) ||
		!validEndpointHost(result.serverHost) ||
		!validPort(result.imapPort) || !validPort(result.smtpStartTLSPort) || !validPort(result.smtpSSLPort) {
		return settingsSnapshot{}, ErrNotConfigured
	}
	return result, nil
}

func validRequiredValue(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxSettingLength {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validHTTPURL(value string) bool {
	if !validRequiredValue(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) {
			return false
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.User != nil || parsed.Host == "" || parsed.Opaque != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return validURLAuthority(parsed.Host)
}

func validURLAuthority(authority string) bool {
	if authority == "" || strings.ContainsAny(authority, "/\\@") {
		return false
	}
	if strings.HasPrefix(authority, "[") {
		closing := strings.IndexByte(authority, ']')
		if closing <= 1 || net.ParseIP(authority[1:closing]) == nil {
			return false
		}
		rest := authority[closing+1:]
		if rest == "" {
			return true
		}
		return strings.HasPrefix(rest, ":") && validPort(rest[1:])
	}
	if strings.ContainsAny(authority, "[]") {
		return false
	}
	host := authority
	if colon := strings.LastIndexByte(authority, ':'); colon >= 0 {
		if strings.Contains(authority[:colon], ":") {
			return false
		}
		host = authority[:colon]
		if !validPort(authority[colon+1:]) {
			return false
		}
	}
	return validEndpointHost(host)
}

func validEndpointHost(host string) bool {
	if host == "" || len(host) > maxHostLength || strings.Contains(host, "..") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && strconv.Itoa(port) == value && port >= 1 && port <= 65535
}

func readinessHTTPClient(injected HTTPDoer) (HTTPDoer, func()) {
	if injected != nil {
		return injected, func() { closeIdleConnections(injected) }
	}
	dialer := &net.Dialer{Timeout: DialTimeout}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   DialTimeout,
		ResponseHeaderTimeout: DialTimeout,
		ExpectContinueTimeout: DialTimeout,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   ReadinessTimeout,
		// A redirect is itself a non-5xx response and therefore proves that
		// the configured endpoint answered.  Do not follow it to another
		// destination or turn a reachable auth redirect into a false failure.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return client, func() { transport.CloseIdleConnections() }
}

func closeIdleConnections(client HTTPDoer) {
	if closer, ok := client.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func checkHTTP(ctx context.Context, client HTTPDoer, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ErrUnavailable
	}
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return ErrUnavailable
	}
	if response == nil {
		return ErrUnavailable
	}
	status := response.StatusCode
	if response.Body != nil {
		_ = response.Body.Close()
	}
	if status >= http.StatusInternalServerError && status <= 599 {
		return ErrUnavailable
	}
	return nil
}

func checkTCP(ctx context.Context, dialContext DialContextFunc, host, port string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	connection, err := dialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return ErrUnavailable
	}
	if connection == nil {
		return ErrUnavailable
	}
	// No IMAP/SMTP protocol bytes are written.  A successful TCP connect is
	// the complete observation, and closing it immediately avoids an idle
	// connection or provider-side session.
	_ = connection.Close()
	return nil
}
