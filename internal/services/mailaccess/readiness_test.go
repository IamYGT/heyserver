package mailaccess

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

func validSettings() map[string]string {
	return map[string]string{
		WebmailURLKey:           "https://webmail.example.test/login",
		MailAdminURLKey:         "https://admin.example.test/",
		MailServerHostKey:       "mail.example.test",
		MailIMAPPortKey:         "993",
		MailSMTPStartTLSPortKey: "587",
		MailSMTPSSLPortKey:      "465",
	}
}

func TestValidateSettingsRequiresSixStrictCanonicalValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "nil", mutate: func(values map[string]string) { values[WebmailURLKey] = "" }},
		{name: "missing", mutate: func(values map[string]string) { delete(values, MailAdminURLKey) }},
		{name: "userinfo", mutate: func(values map[string]string) { values[WebmailURLKey] = "https://user:secret@example.test" }},
		{name: "non-http", mutate: func(values map[string]string) { values[WebmailURLKey] = "file:///etc/passwd" }},
		{name: "malformed-url-port", mutate: func(values map[string]string) { values[MailAdminURLKey] = "https://admin.example.test:bad" }},
		{name: "empty-url-port", mutate: func(values map[string]string) { values[MailAdminURLKey] = "https://admin.example.test:" }},
		{name: "out-of-range-url-port", mutate: func(values map[string]string) { values[MailAdminURLKey] = "https://admin.example.test:65536" }},
		{name: "invalid-host", mutate: func(values map[string]string) { values[MailServerHostKey] = "mail..example.test" }},
		{name: "host-with-port", mutate: func(values map[string]string) { values[MailServerHostKey] = "mail.example.test:993" }},
		{name: "invalid-port", mutate: func(values map[string]string) { values[MailIMAPPortKey] = "0" }},
		{name: "leading-zero-port", mutate: func(values map[string]string) { values[MailSMTPSSLPortKey] = "0465" }},
		{name: "fragment", mutate: func(values map[string]string) { values[MailAdminURLKey] = "https://admin.example.test/#admin" }},
		{name: "outer-space", mutate: func(values map[string]string) { values[MailServerHostKey] = " mail.example.test" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := validSettings()
			test.mutate(values)
			if err := ValidateSettings(values); !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("ValidateSettings() = %v, want ErrNotConfigured", err)
			}
		})
	}

	values := validSettings()
	values["unrelated_setting"] = "ignored by this six-key snapshot contract"
	if err := ValidateSettings(values); err != nil {
		t.Fatalf("ValidateSettings(valid snapshot with unrelated setting) = %v", err)
	}
	if err := ValidateSettings(nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ValidateSettings(nil) = %v, want ErrNotConfigured", err)
	}
}

type fakeHTTPDoer struct {
	mu          sync.Mutex
	status      map[string]int
	requests    []*http.Request
	closed      int
	closeCalled bool
}

func (f *fakeHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, request)
	status := f.status[request.URL.String()]
	f.mu.Unlock()
	return &http.Response{
		StatusCode: status,
		Body:       &fakeBody{onClose: func() { f.mu.Lock(); f.closed++; f.mu.Unlock() }},
		Request:    request,
	}, nil
}

func (f *fakeHTTPDoer) CloseIdleConnections() {
	f.mu.Lock()
	f.closeCalled = true
	f.mu.Unlock()
}

type fakeBody struct {
	onClose func()
}

func (b *fakeBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b *fakeBody) Close() error {
	if b.onClose != nil {
		b.onClose()
	}
	return nil
}

type fakeConn struct {
	mu     sync.Mutex
	closed bool
}

func (c *fakeConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *fakeConn) Write(data []byte) (int, error)   { return len(data), nil }
func (c *fakeConn) LocalAddr() net.Addr              { return fakeAddr("local") }
func (c *fakeConn) RemoteAddr() net.Addr             { return fakeAddr("remote") }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }
func (c *fakeConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func TestProbeReadinessContextRunsFiveChecksConcurrentlyAndClosesResources(t *testing.T) {
	values := validSettings()
	httpDoer := &fakeHTTPDoer{status: map[string]int{
		values[WebmailURLKey]:   http.StatusFound,
		values[MailAdminURLKey]: http.StatusForbidden,
	}}
	var mu sync.Mutex
	var addresses []string
	var connections []*fakeConn
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Errorf("dial network = %q, want tcp", network)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("dial context has no deadline")
		}
		connection := &fakeConn{}
		mu.Lock()
		addresses = append(addresses, address)
		connections = append(connections, connection)
		mu.Unlock()
		return connection, nil
	}

	state, err := ProbeReadinessContextWithDependencies(context.Background(), values, Dependencies{
		HTTPClient:  httpDoer,
		DialContext: dial,
	})
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("probe = state %q, err %v; want healthy", state, err)
	}
	if len(httpDoer.requests) != 2 {
		t.Fatalf("HTTP requests = %d, want 2", len(httpDoer.requests))
	}
	for _, request := range httpDoer.requests {
		if request.Method != http.MethodGet {
			t.Errorf("HTTP method = %q, want GET", request.Method)
		}
		if request.Body != nil {
			t.Error("HTTP request unexpectedly has a body")
		}
		if request.Header.Get("Authorization") != "" {
			t.Error("HTTP request unexpectedly has Authorization")
		}
		if _, ok := request.Context().Deadline(); !ok {
			t.Error("HTTP context has no deadline")
		}
	}
	if httpDoer.closed != 2 {
		t.Fatalf("HTTP bodies closed = %d, want 2", httpDoer.closed)
	}
	if !httpDoer.closeCalled {
		t.Error("HTTP idle connections were not closed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(addresses) != 3 {
		t.Fatalf("TCP dials = %d, want 3", len(addresses))
	}
	wantAddresses := map[string]bool{
		"mail.example.test:993": true,
		"mail.example.test:587": true,
		"mail.example.test:465": true,
	}
	for _, address := range addresses {
		if !wantAddresses[address] {
			t.Errorf("unexpected TCP address %q", address)
		}
	}
	for _, connection := range connections {
		connection.mu.Lock()
		closed := connection.closed
		connection.mu.Unlock()
		if !closed {
			t.Error("TCP connection was not closed immediately")
		}
	}
}

func TestProbeReadinessContextTreatsAnyNonFiveHundredHTTPStatusAsReachable(t *testing.T) {
	for _, status := range []int{http.StatusContinue, http.StatusNotModified, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests} {
		t.Run(strconvStatus(status), func(t *testing.T) {
			values := validSettings()
			httpDoer := &fakeHTTPDoer{status: map[string]int{
				values[WebmailURLKey]:   status,
				values[MailAdminURLKey]: status,
			}}
			state, err := ProbeReadinessContextWithDependencies(context.Background(), values, Dependencies{
				HTTPClient: httpDoer,
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					return &fakeConn{}, nil
				},
			})
			if err != nil || state != integrationstate.Healthy {
				t.Fatalf("status %d probe = state %q, err %v; want healthy", status, state, err)
			}
		})
	}
}

func TestProbeReadinessContextMapsHTTPAndDialFailuresWithoutLeak(t *testing.T) {
	values := validSettings()
	secret := "transport-secret-value"
	httpDoer := &fakeHTTPDoer{status: map[string]int{
		values[WebmailURLKey]:   http.StatusServiceUnavailable,
		values[MailAdminURLKey]: http.StatusOK,
	}}
	dial := func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New(secret + " mail.example.test:993")
	}
	state, err := ProbeReadinessContextWithDependencies(context.Background(), values, Dependencies{HTTPClient: httpDoer, DialContext: dial})
	if state != integrationstate.Unavailable || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed probe = state %q, err %v; want unavailable/ErrUnavailable", state, err)
	}
	for _, forbidden := range []string{secret, values[WebmailURLKey], values[MailServerHostKey], "mail.example.test:993"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("public error leaked %q: %v", forbidden, err)
		}
	}
}

func TestProbeReadinessContextCancellationDrainsAllChecks(t *testing.T) {
	values := validSettings()
	started := make(chan struct{}, readinessCheckCount)
	finished := make(chan struct{}, readinessCheckCount)
	blockingHTTP := &blockingHTTPDoer{started: started, finished: finished}
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		started <- struct{}{}
		<-ctx.Done()
		finished <- struct{}{}
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	state, err := ProbeReadinessContextWithDependencies(ctx, values, Dependencies{HTTPClient: blockingHTTP, DialContext: dial})
	if state != integrationstate.Unavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled probe = state %q, err %v; want unavailable/deadline", state, err)
	}
	if len(started) != readinessCheckCount {
		t.Fatalf("started checks = %d, want %d", len(started), readinessCheckCount)
	}
	if len(finished) != readinessCheckCount {
		t.Fatalf("finished checks = %d, want %d", len(finished), readinessCheckCount)
	}
}

type blockingHTTPDoer struct {
	started  chan<- struct{}
	finished chan<- struct{}
}

func (b *blockingHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	b.started <- struct{}{}
	<-request.Context().Done()
	b.finished <- struct{}{}
	return nil, request.Context().Err()
}

func strconvStatus(status int) string {
	return map[int]string{
		http.StatusContinue:        "100",
		http.StatusNotModified:     "304",
		http.StatusUnauthorized:    "401",
		http.StatusForbidden:       "403",
		http.StatusNotFound:        "404",
		http.StatusTooManyRequests: "429",
	}[status]
}
