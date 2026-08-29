package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHTTPStatusErrorPreservesOptionalRecoveryFields(t *testing.T) {
	err := httpStatusError(http.StatusServiceUnavailable, []byte(`{"error":"Cloudflare is not configured","state":"not_configured","next_action":"configure the integration"}`))

	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *apiError", err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", apiErr.StatusCode, http.StatusServiceUnavailable)
	}
	if apiErr.Message != "Cloudflare is not configured" {
		t.Fatalf("message = %q", apiErr.Message)
	}
	if apiErr.State != "not_configured" {
		t.Fatalf("state = %q", apiErr.State)
	}
	if apiErr.NextAction != "configure the integration" {
		t.Fatalf("next action = %q", apiErr.NextAction)
	}
	if got, want := err.Error(), "HTTP 503: Cloudflare is not configured"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	for _, want := range []string{"HTTP 503: Cloudflare is not configured", "state: not_configured", "next: configure the integration"} {
		if !strings.Contains(apiErr.ActionableMessage(), want) {
			t.Fatalf("actionable message %q does not contain %q", apiErr.ActionableMessage(), want)
		}
	}
}

func TestHTTPStatusErrorUsesIntegrationStateHeaderWhenBodyOmitsIt(t *testing.T) {
	err := newAPIHTTPError(http.StatusBadGateway, []byte(`{"error":"provider unavailable"}`), "unavailable")
	if err.State != "unavailable" {
		t.Fatalf("state = %q, want unavailable", err.State)
	}
}

func TestHTTPStatusErrorKeepsMessageWhenOptionalFieldHasUnexpectedType(t *testing.T) {
	err := httpStatusError(http.StatusServiceUnavailable, []byte(`{"error":"dependency unavailable","state":{"raw":"unavailable"},"next_action":false}`))
	if got, want := err.Error(), "HTTP 503: dependency unavailable"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	apiErr := err.(*apiError)
	if apiErr.State != "" || apiErr.NextAction != "" {
		t.Fatalf("unexpected optional fields: state=%q next=%q", apiErr.State, apiErr.NextAction)
	}
}

func TestClientHTTPRecoveryActionMapsBoundedStatuses(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "hserverctl login"},
		{http.StatusForbidden, "hserverctl doctor"},
		{http.StatusConflict, "resolve the conflict"},
		{http.StatusBadGateway, "upstream connectivity"},
		{http.StatusServiceUnavailable, "integration configuration"},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			err := httpStatusError(test.status, []byte(`{"error":"server error"}`))
			apiErr := err.(*apiError)
			if got := apiErr.RecoveryAction(); !strings.Contains(got, test.want) {
				t.Fatalf("recovery action = %q, want substring %q", got, test.want)
			}
		})
	}
	if got := httpStatusError(http.StatusInternalServerError, nil).(*apiError).RecoveryAction(); got != "" {
		t.Fatalf("unexpected fallback for HTTP 500: %q", got)
	}
}

func TestClientTransportRefusalAndTimeoutAreTypedAndActionable(t *testing.T) {
	tests := []struct {
		name       string
		cause      error
		wantAction string
	}{
		{
			name:       "refused",
			cause:      &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			wantAction: "network reachability",
		},
		{
			name:       "timeout",
			cause:      clientTimeoutError("i/o timeout"),
			wantAction: "then retry",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := newAPIClient("https://panel.example.test", "bearer-secret-value", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient = &http.Client{Transport: clientRoundTripper(func(*http.Request) (*http.Response, error) {
				return nil, test.cause
			})}
			_, err = client.request(context.Background(), http.MethodGet, "/api/health?token=secret-query", nil, true)
			if err == nil {
				t.Fatal("request unexpectedly succeeded")
			}
			var apiErr *apiError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error type = %T, want *apiError", err)
			}
			if apiErr.kind == apiErrorHTTP {
				t.Fatalf("transport failure classified as HTTP: %#v", apiErr)
			}
			if !strings.Contains(apiErr.RecoveryAction(), test.wantAction) {
				t.Fatalf("recovery action = %q, want %q", apiErr.RecoveryAction(), test.wantAction)
			}
			message := clientErrorMessage(err)
			if !strings.Contains(message, "https://panel.example.test") {
				t.Fatalf("message = %q, selected server URL is missing", message)
			}
			if strings.Contains(message, "bearer-secret-value") || strings.Contains(message, "Authorization") || strings.Contains(message, "secret-query") {
				t.Fatalf("message leaked a request secret: %q", message)
			}
		})
	}
}

func TestClientStatusErrorDoesNotLeakResponseSecretsOrDump(t *testing.T) {
	const token = "bearer-secret-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("X-HServer-Integration-State", "unavailable")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"upstream echoed Bearer bearer-secret-value; password=hunter2","state":"unavailable","next_action":"replace token=bearer-secret-value","debug":"do not print this response"}`)
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, token, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.request(context.Background(), http.MethodGet, "/api/health", nil, true)
	if err == nil {
		t.Fatal("request unexpectedly succeeded")
	}
	for _, message := range []string{err.Error(), clientErrorMessage(err)} {
		for _, forbidden := range []string{token, "hunter2", "do not print this response", `"debug"`} {
			if strings.Contains(message, forbidden) {
				t.Fatalf("message leaked %q: %q", forbidden, message)
			}
		}
	}
	if !strings.Contains(clientErrorMessage(err), "state: unavailable") {
		t.Fatalf("typed state missing from actionable message: %q", clientErrorMessage(err))
	}
}

type clientRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip clientRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type clientTimeoutError string

func (err clientTimeoutError) Error() string   { return string(err) }
func (err clientTimeoutError) Timeout() bool   { return true }
func (err clientTimeoutError) Temporary() bool { return true }
