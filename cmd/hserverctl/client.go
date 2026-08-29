package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const maxResponseBytes = 8 << 20

const (
	apiIntegrationStateHeader = "X-HServer-Integration-State"
	maxAPIErrorFieldBytes     = 512
)

var (
	apiBearerSecretPattern = regexp.MustCompile(`(?i)\bbearer[[:space:]]+[^[:space:],;\)\]}]+`)
	apiSecretKVPattern     = regexp.MustCompile(`(?i)(^|[^[:alnum:]])(authorization|password|passwd|secret|token|credential|passphrase|api[_-]?key|access[_-]?key|private[_-]?key)((?:[[:space:]]*[:=][[:space:]]*))("[^"]*"|'[^']*'|[^[:space:],;\)\]}]+)`)
)

type apiClient struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
}

// apiError is the bounded error envelope returned by the panel API.  Message
// deliberately contains only the server's JSON "error" field; the response
// body is never retained or rendered by the CLI.
//
// Error keeps the historical `HTTP <status>: <message>` representation so
// existing CLI scripts and TUI notices remain stable.  ActionableMessage is
// the opt-in presentation for callers that want the safe client-side recovery
// hint and the selected server context.
type apiError struct {
	StatusCode int
	Message    string
	State      string
	NextAction string

	// ServerURL is populated only when the error came from an apiClient that
	// already has a parsed base URL. It is operational context, never a token.
	ServerURL string

	// Cause is reserved for transport failures. HTTP errors have no wrapped
	// cause, allowing errors.As to distinguish them without parsing strings.
	Cause error

	kind apiErrorKind
}

// APIError is an exported alias for integrations that need to inspect the
// typed HTTP failure without depending on the command package's private name.
// The CLI itself continues to use apiError internally.
type APIError = apiError

type apiErrorKind uint8

const (
	apiErrorHTTP apiErrorKind = iota
	apiErrorTransportRefused
	apiErrorTransportTimeout
	apiErrorTransport
)

func (e *apiError) Error() string {
	if e == nil {
		return "request failed"
	}
	if e.StatusCode > 0 {
		message := strings.TrimSpace(e.Message)
		if message == "" {
			message = http.StatusText(e.StatusCode)
		}
		if message == "" {
			message = "request failed"
		}
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, message)
	}
	if message := strings.TrimSpace(e.Message); message != "" {
		return sanitizeAPIErrorText(message)
	}
	if e.Cause != nil {
		return sanitizeAPIErrorText(transportCauseMessage(e.Cause))
	}
	return "request failed"
}

func (e *apiError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// HTTPStatus exposes the status while keeping Error's established text
// stable. It returns zero for a transport failure.
func (e *apiError) HTTPStatus() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

// RecoveryAction returns a server-provided next_action when one was actually
// supplied. Otherwise it returns only a deterministic client-side fallback
// for the statuses and transport classes that have a safe generic remedy.
// No server-side next_action is invented here.
func (e *apiError) RecoveryAction() string {
	if e == nil {
		return ""
	}
	if e.NextAction != "" {
		return e.NextAction
	}
	if e.StatusCode == 0 && e.Cause != nil {
		if isAPITransportTimeout(e.Cause) {
			return "check the selected server with hserverctl doctor, then retry"
		}
		if isAPITransportRefused(e.Cause) {
			return "check the selected server URL and network reachability, then run hserverctl doctor"
		}
		return "check the selected server connection with hserverctl doctor, then retry"
	}
	switch e.kind {
	case apiErrorTransportRefused:
		return "check the selected server URL and network reachability, then run hserverctl doctor"
	case apiErrorTransportTimeout:
		return "check the selected server with hserverctl doctor, then retry"
	case apiErrorTransport:
		return "check the selected server connection with hserverctl doctor, then retry"
	case apiErrorHTTP:
		return clientHTTPRecoveryAction(e.StatusCode)
	default:
		return ""
	}
}

// Remediation is a concise alias useful to TUI and CLI presentation code.
func (e *apiError) Remediation() string {
	return e.RecoveryAction()
}

// ActionableMessage keeps the raw HTTP status/error and appends bounded,
// non-secret recovery context. It is intentionally separate from Error so
// existing machine-facing and TUI error assertions remain compatible.
func (e *apiError) ActionableMessage() string {
	if e == nil {
		return "request failed"
	}
	message := e.Error()
	if e.State != "" {
		message += "; state: " + e.State
	}
	if action := e.RecoveryAction(); action != "" {
		message += "; next: " + action
	}
	if e.ServerURL != "" {
		message += "; server: " + e.ServerURL
	}
	return sanitizeAPIErrorText(message)
}

// clientErrorMessage is the common safe presentation hook for CLI and TUI
// callers. Non-API errors are preserved but still stripped of control
// characters so an upstream error cannot inject terminal output.
func clientErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.ActionableMessage()
	}
	return sanitizeAPIErrorText(err.Error())
}

func clientHTTPRecoveryAction(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "run hserverctl login or verify the selected context"
	case http.StatusForbidden:
		return "run hserverctl doctor to check account permissions and the selected context"
	case http.StatusConflict:
		return "run hserverctl doctor to refresh the selected context, resolve the conflict, then retry"
	case http.StatusBadGateway:
		return "run hserverctl doctor to check server and upstream connectivity, then retry"
	case http.StatusServiceUnavailable:
		return "run hserverctl doctor to check server availability and integration configuration, then retry"
	default:
		return ""
	}
}

func (e *apiError) withClientContext(client *apiClient) *apiError {
	if e == nil || client == nil {
		return e
	}
	clone := *e
	if client.baseURL != nil {
		clone.ServerURL = safeAPIBaseURL(client.baseURL)
	}
	if token := strings.TrimSpace(client.token); token != "" {
		clone.Message = strings.ReplaceAll(clone.Message, token, "[redacted]")
		clone.State = strings.ReplaceAll(clone.State, token, "[redacted]")
		clone.NextAction = strings.ReplaceAll(clone.NextAction, token, "[redacted]")
	}
	return &clone
}

func safeAPIBaseURL(base *url.URL) string {
	if base == nil {
		return ""
	}
	// newAPIClient rejects userinfo, query, fragment, and paths. Re-checking
	// those conditions here keeps manually constructed apiClients from leaking
	// credential-like URL parts into an error message.
	clone := *base
	clone.User = nil
	clone.RawQuery = ""
	clone.Fragment = ""
	clone.Path = ""
	clone.RawPath = ""
	return clone.String()
}

func (c *apiClient) withTimeout(timeout time.Duration) *apiClient {
	clone := *c
	httpClient := *c.httpClient
	httpClient.Timeout = timeout
	clone.httpClient = &httpClient
	return &clone
}

func newAPIClient(rawURL, token string, timeout time.Duration) (*apiClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("server URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, errors.New("server URL must include a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("server URL must not include credentials, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("server URL must not include a path")
	}
	if timeout <= 0 {
		return nil, errors.New("timeout must be greater than zero")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return &apiClient{
		baseURL:    parsed,
		token:      strings.TrimSpace(token),
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

func (c *apiClient) request(ctx context.Context, method, endpoint string, payload any, authenticated bool) ([]byte, error) {
	ref, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("build request URL: %w", err)
	}
	target := c.baseURL.ResolveReference(ref)

	var body io.Reader
	if payload != nil {
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			return nil, fmt.Errorf("encode request: %w", encodeErr)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		if c.token == "" {
			return nil, errors.New("authentication token is not configured; run login or set HSERVER_TOKEN_FILE")
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, newAPITransportError(c, method, endpoint, err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newAPIHTTPError(resp.StatusCode, responseBody, resp.Header.Get(apiIntegrationStateHeader)).withClientContext(c)
	}
	return responseBody, nil
}

func httpStatusError(status int, body []byte) error {
	return newAPIHTTPError(status, body, "")
}

func newAPIHTTPError(status int, body []byte, headerState string) *apiError {
	var response map[string]json.RawMessage
	message := http.StatusText(status)
	if message == "" {
		message = "request failed"
	}
	if json.Unmarshal(body, &response) == nil {
		if value := optionalAPIString(response, "error"); strings.TrimSpace(value) != "" {
			message = value
		}
	}
	state := optionalAPIString(response, "state")
	if strings.TrimSpace(state) == "" {
		state = headerState
	}
	return &apiError{
		StatusCode: status,
		Message:    sanitizeAPIErrorText(message),
		State:      sanitizeAPIErrorField(state),
		NextAction: sanitizeAPIErrorField(optionalAPIString(response, "next_action")),
		kind:       apiErrorHTTP,
	}
}

func optionalAPIString(object map[string]json.RawMessage, key string) string {
	if len(object) == 0 {
		return ""
	}
	raw, ok := object[key]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func newAPITransportError(client *apiClient, method, endpoint string, cause error) *apiError {
	kind := apiErrorTransport
	if isAPITransportTimeout(cause) {
		kind = apiErrorTransportTimeout
	} else if isAPITransportRefused(cause) {
		kind = apiErrorTransportRefused
	}
	return (&apiError{
		Message: sanitizeAPIErrorText(transportCauseMessage(cause)),
		Cause:   cause,
		kind:    kind,
	}).withClientContext(client).withRequestEndpoint(method, endpoint)
}

// withRequestEndpoint retains only a safe path and method. Query strings are
// intentionally discarded: callers occasionally pass secret-bearing values
// there, and they must never become an accidental error sink.
func (e *apiError) withRequestEndpoint(method, endpoint string) *apiError {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Message = sanitizeAPIErrorText(fmt.Sprintf("request %s %s: %s", strings.ToUpper(strings.TrimSpace(method)), safeAPIEndpoint(endpoint), e.Message))
	return &clone
}

func safeAPIEndpoint(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err == nil {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.User = nil
		if parsed.Path != "" {
			return parsed.Path
		}
	}
	if index := strings.IndexAny(endpoint, "?#"); index >= 0 {
		endpoint = endpoint[:index]
	}
	return strings.TrimSpace(endpoint)
}

func isAPITransportTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

func isAPITransportRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	// Some custom RoundTrippers only expose a textual network error. Keep this
	// narrow to the canonical refusal wording rather than classifying every
	// failed request as a refused connection.
	return strings.Contains(strings.ToLower(transportCauseMessage(err)), "connection refused")
}

func transportCauseMessage(err error) string {
	if err == nil {
		return "transport failure"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	return sanitizeAPIErrorText(err.Error())
}

func sanitizeAPIErrorField(value string) string {
	return truncateAPIErrorText(sanitizeAPIErrorText(value))
}

func sanitizeAPIErrorText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	// Error fields are informational text, not terminal or JSON output. Keep
	// them on one line and remove common credential-bearing forms before they
	// can be surfaced by either CLI or TUI.
	value = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	value = redactAPISecrets(value)
	return truncateAPIErrorText(strings.Join(strings.Fields(value), " "))
}

func truncateAPIErrorText(value string) string {
	if len(value) <= maxAPIErrorFieldBytes {
		return value
	}
	return value[:maxAPIErrorFieldBytes-3] + "..."
}

func redactAPISecrets(value string) string {
	// Bearer credentials are the only credential form owned directly by this
	// client. The generic key/value pass covers accidental server echoes of
	// password, token, secret, and API-key values without retaining a response
	// dump.
	value = apiBearerSecretPattern.ReplaceAllString(value, "Bearer [redacted]")
	return apiSecretKVPattern.ReplaceAllString(value, "$1$2$3[redacted]")
}

func prettyJSON(out io.Writer, raw []byte) error {
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		return fmt.Errorf("server returned invalid JSON: %w", err)
	}
	formatted.WriteByte('\n')
	_, err := formatted.WriteTo(out)
	return err
}

func readSecretFile(path string, maxBytes int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("secret file must be a regular file and not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("secret file must not be accessible by group or others")
	}
	if info.Size() > maxBytes {
		return "", fmt.Errorf("secret file exceeds %d bytes", maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("secret file exceeds %d bytes", maxBytes)
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", errors.New("secret file is empty")
	}
	return value, nil
}

func loadToken(path, environmentToken string) (string, error) {
	if path != "" {
		_, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if token := strings.TrimSpace(environmentToken); token != "" {
					return token, nil
				}
				return "", nil
			}
			return "", fmt.Errorf("read token file %s: %w", path, err)
		}
		token, err := readSecretFile(path, 64<<10)
		if err != nil {
			return "", fmt.Errorf("read token file %s: %w", path, err)
		}
		return strings.TrimSpace(token), nil
	}
	if token := strings.TrimSpace(environmentToken); token != "" {
		return token, nil
	}
	return "", nil
}

func writeTokenFile(path, token string, replace bool) error {
	if path == "" {
		return errors.New("token file path is required")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("refusing to write an empty token")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("token file must be a regular file and not a symlink")
		}
		if !replace {
			return errors.New("token file already exists; use --replace to overwrite it")
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, ".hserver-token-*")
	if err != nil {
		return fmt.Errorf("create temporary token file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := io.WriteString(temp, strings.TrimSpace(token)+"\n"); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace token file: %w", err)
	}
	return nil
}

func removeTokenFile(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("token file path is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect token file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("token file must be a regular file and not a symlink")
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("remove token file %s: %w", path, err)
	}
	return true, nil
}
