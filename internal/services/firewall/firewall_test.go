package firewall

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

type fakeReadinessRunner struct {
	path        string
	lookPathErr error
	runErrs     map[string]error
	calls       []string
	run         func(context.Context, string, ...string) error
}

func (f *fakeReadinessRunner) LookPath(_ string) (string, error) {
	if f.lookPathErr != nil {
		return "", f.lookPathErr
	}
	return f.path, nil
}

func (f *fakeReadinessRunner) Run(ctx context.Context, name string, args ...string) error {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if f.run != nil {
		return f.run(ctx, name, args...)
	}
	if f.runErrs != nil {
		return f.runErrs[strings.Join(args, " ")]
	}
	return nil
}

func TestProbeContextMissingBinaryIsNotConfigured(t *testing.T) {
	runner := &fakeReadinessRunner{lookPathErr: exec.ErrNotFound}

	state, err := probeContext(context.Background(), runner)
	if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing UFW = state %q, err %v; want not_configured", state, err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("missing UFW invoked commands: %#v", runner.calls)
	}
}

func TestProbeContextRequiresBothUFWObservations(t *testing.T) {
	runner := &fakeReadinessRunner{path: "/usr/sbin/ufw"}

	state, err := probeContext(context.Background(), runner)
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("healthy UFW = state %q, err %v; want healthy", state, err)
	}
	wantCalls := []string{
		"/usr/sbin/ufw status verbose",
		"/usr/sbin/ufw status numbered",
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("UFW calls = %#v, want %#v", runner.calls, wantCalls)
	}
}

func TestProbeContextNumberedFailureIsUnavailableAndSafe(t *testing.T) {
	secret := "/srv/private/ufw-token"
	runner := &fakeReadinessRunner{
		path: "/usr/sbin/ufw",
		runErrs: map[string]error{
			"status numbered": errors.New("permission denied: " + secret),
		},
	}

	state, err := probeContext(context.Background(), runner)
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("numbered failure = state %q, err %v; want unavailable", state, err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("numbered failure leaked command detail: %v", err)
	}
	wantCalls := []string{
		"/usr/sbin/ufw status verbose",
		"/usr/sbin/ufw status numbered",
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("UFW calls = %#v, want %#v", runner.calls, wantCalls)
	}
}

func TestProbeContextPermissionFailureIsUnavailableAndSafe(t *testing.T) {
	secret := "/etc/ufw/private.conf"
	runner := &fakeReadinessRunner{
		path: "/usr/sbin/ufw",
		runErrs: map[string]error{
			"status verbose": errors.New("permission denied while reading " + secret),
		},
	}

	state, err := probeContext(context.Background(), runner)
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("permission failure = state %q, err %v; want unavailable", state, err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("permission failure leaked command detail: %v", err)
	}
	if !reflect.DeepEqual(runner.calls, []string{"/usr/sbin/ufw status verbose"}) {
		t.Fatalf("UFW calls = %#v, want only verbose status", runner.calls)
	}
}

func TestProbeContextCancellationIsUnavailableAndReachesRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	runner := &fakeReadinessRunner{
		path: "/usr/sbin/ufw",
		run: func(ctx context.Context, _ string, _ ...string) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}

	type result struct {
		state integrationstate.State
		err   error
	}
	done := make(chan result, 1)
	go func() {
		state, err := probeContext(ctx, runner)
		done <- result{state: state, err: err}
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("UFW probe did not reach the command runner")
	}

	select {
	case got := <-done:
		if got.state != integrationstate.Unavailable || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("cancelled UFW probe = state %q, err %v; want unavailable/canceled", got.state, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled UFW probe did not return")
	}
	if !reflect.DeepEqual(runner.calls, []string{"/usr/sbin/ufw status verbose"}) {
		t.Fatalf("UFW calls = %#v, want only verbose status", runner.calls)
	}
}

func TestClassifyUFWError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ReadinessState
	}{
		{name: "healthy", want: StateHealthy},
		{name: "missing executable", err: errors.New(`exec: "ufw": executable file not found in $PATH`), want: StateUFWMissing},
		{name: "missing shell command", err: errors.New("ufw: command not found"), want: StateUFWMissing},
		{name: "status unavailable", err: errors.New("ufw status: permission denied"), want: StateUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyUFWError(tc.err); got != tc.want {
				t.Fatalf("ClassifyUFWError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestParseNumberedRules(t *testing.T) {
	input := `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 22/tcp                     ALLOW IN    Anywhere
[ 2] 80/tcp                     ALLOW IN    Anywhere
[ 3] 443/tcp                    ALLOW IN    Anywhere
[ 4] 22/tcp (v6)                ALLOW IN    Anywhere (v6)
[ 5] 80/tcp (v6)                ALLOW IN    Anywhere (v6)`

	rules := parseNumberedRules(input)
	if len(rules) != 5 {
		t.Fatalf("expected 5 rules, got %d", len(rules))
	}

	if rules[0].Number != 1 {
		t.Errorf("rule 0 number: want 1, got %d", rules[0].Number)
	}
	if rules[0].To != "22" {
		t.Errorf("rule 0 to: want '22', got %q", rules[0].To)
	}
	if rules[0].Protocol != "tcp" {
		t.Errorf("rule 0 proto: want 'tcp', got %q", rules[0].Protocol)
	}
	if rules[0].Action != "ALLOW" {
		t.Errorf("rule 0 action: want 'ALLOW', got %q", rules[0].Action)
	}
	if rules[0].IsIPv6 {
		t.Errorf("rule 0 should not be IPv6")
	}

	if !rules[3].IsIPv6 {
		t.Errorf("rule 3 should be IPv6")
	}
}

func TestParseVerboseHeader(t *testing.T) {
	input := `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip`

	s := &Status{}
	parseVerboseHeader(input, s)

	if !s.Active {
		t.Error("expected Active=true")
	}
	if s.DefaultIncoming != "deny" {
		t.Errorf("DefaultIncoming: want 'deny', got %q", s.DefaultIncoming)
	}
	if s.DefaultOutgoing != "allow" {
		t.Errorf("DefaultOutgoing: want 'allow', got %q", s.DefaultOutgoing)
	}
	if s.DefaultRouted != "disabled" {
		t.Errorf("DefaultRouted: want 'disabled', got %q", s.DefaultRouted)
	}
	if s.LoggingLevel != "on (low)" {
		t.Errorf("LoggingLevel: want 'on (low)', got %q", s.LoggingLevel)
	}
}

func TestCheckSSHSafety_LastRule(t *testing.T) {
	rules := []Rule{
		{Number: 1, To: "22", Protocol: "tcp", Action: "ALLOW", Direction: "IN"},
	}
	err := checkSSHSafety(1, rules)
	if err == nil {
		t.Error("expected error when deleting last SSH rule, got nil")
	}
}

func TestCheckSSHSafety_MultipleSSHRules(t *testing.T) {
	rules := []Rule{
		{Number: 1, To: "22", Protocol: "tcp", Action: "ALLOW", Direction: "IN"},
		{Number: 2, To: "22", Protocol: "tcp", Action: "ALLOW", Direction: "IN", IsIPv6: true},
	}
	err := checkSSHSafety(1, rules)
	if err != nil {
		t.Errorf("should allow deletion when multiple SSH rules exist, got: %v", err)
	}
}

func TestCheckSSHSafety_NoSSHRules(t *testing.T) {
	rules := []Rule{
		{Number: 1, To: "80", Protocol: "tcp", Action: "ALLOW", Direction: "IN"},
	}
	err := checkSSHSafety(1, rules)
	if err != nil {
		t.Errorf("should allow deletion when no SSH rules exist, got: %v", err)
	}
}

func TestValidateAddRequest_ValidAllow(t *testing.T) {
	req := AddRuleRequest{
		Action:    "allow",
		Direction: "in",
		Port:      "80",
		Protocol:  "tcp",
	}
	if err := validateAddRequest(req); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAddRequest_InvalidAction(t *testing.T) {
	req := AddRuleRequest{Action: "open"}
	if err := validateAddRequest(req); err == nil {
		t.Error("expected error for invalid action")
	}
}

func TestValidateAddRequest_InvalidDirection(t *testing.T) {
	req := AddRuleRequest{Action: "allow", Direction: "sideways"}
	if err := validateAddRequest(req); err == nil {
		t.Error("expected error for invalid direction")
	}
}

func TestValidateAddRequest_InvalidPort(t *testing.T) {
	req := AddRuleRequest{Action: "allow", Port: "80; rm -rf /"}
	if err := validateAddRequest(req); err == nil {
		t.Error("expected error for shell-injection port")
	}
}

func TestValidateAddRequest_InvalidFrom(t *testing.T) {
	req := AddRuleRequest{Action: "allow", From: "evil$(rm -rf /)"}
	if err := validateAddRequest(req); err == nil {
		t.Error("expected error for malicious from field")
	}
}

func TestSanitizeComment(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"SSH access", "SSH access"},
		{"rule; rm -rf", "rule rm -rf"},
		{"ok_rule-1.0", "ok_rule-1.0"},
		{"$(evil)", "evil"},
	}
	for _, c := range cases {
		got := sanitizeComment(c.input)
		if got != c.want {
			t.Errorf("sanitizeComment(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestExtractPolicy(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"deny (incoming)", "deny"},
		{"allow (outgoing)", "allow"},
		{"disabled (routed)", "disabled"},
	}
	for _, c := range cases {
		got := extractPolicy(c.input)
		if got != c.want {
			t.Errorf("extractPolicy(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
