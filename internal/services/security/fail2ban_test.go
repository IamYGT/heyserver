package security

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFail2BanStatusDistinguishesRuntimeStates(t *testing.T) {
	tests := []struct {
		name      string
		lookPath  func(string) (string, error)
		run       func(string, ...string) (string, error)
		wantState Fail2BanReadinessState
		installed bool
		running   bool
		available bool
		wantJails int
	}{
		{
			name: "not installed",
			lookPath: func(name string) (string, error) {
				return "", fmt.Errorf("%s not found", name)
			},
			run:       func(string, ...string) (string, error) { return "", nil },
			wantState: Fail2BanStateNotInstalled,
		},
		{
			name: "stopped",
			lookPath: func(name string) (string, error) {
				return "/usr/bin/" + name, nil
			},
			run: func(name string, args ...string) (string, error) {
				return "inactive\n", errors.New("exit status 3")
			},
			wantState: Fail2BanStateStopped,
			installed: true,
		},
		{
			name: "systemd unavailable",
			lookPath: func(name string) (string, error) {
				if name == "fail2ban-client" {
					return "/usr/bin/fail2ban-client", nil
				}
				return "", fmt.Errorf("%s not found", name)
			},
			run:       func(string, ...string) (string, error) { return "", nil },
			wantState: Fail2BanStateUnavailable,
			installed: true,
		},
		{
			name: "healthy",
			lookPath: func(name string) (string, error) {
				return "/usr/bin/" + name, nil
			},
			run: func(name string, args ...string) (string, error) {
				command := strings.Join(append([]string{name}, args...), " ")
				switch command {
				case "systemctl is-active fail2ban":
					return "active\n", nil
				case "fail2ban-client status":
					return "Status\n`- Jail list:\tsshd", nil
				case "fail2ban-client status sshd":
					return "Currently banned: 0\nTotal banned: 3\n", nil
				default:
					return "", fmt.Errorf("unexpected command %s", command)
				}
			},
			wantState: Fail2BanStateHealthy,
			installed: true,
			running:   true,
			available: true,
			wantJails: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Fail2BanService{lookPath: tc.lookPath, run: tc.run}
			got, err := svc.Status()
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.wantState || got.Installed != tc.installed || got.Running != tc.running || got.Available != tc.available || len(got.Jails) != tc.wantJails {
				t.Fatalf("Status() = %#v", got)
			}
		})
	}
}

func TestFail2BanMutationsRejectUnavailableRuntime(t *testing.T) {
	svc := &Fail2BanService{
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		run:      func(string, ...string) (string, error) { return "", nil },
	}
	if err := svc.BanIP("sshd", "192.0.2.10"); !errors.Is(err, ErrFail2BanUnavailable) {
		t.Fatalf("BanIP() error = %v, want ErrFail2BanUnavailable", err)
	}
	if err := svc.UnbanIP("bad jail", "192.0.2.10"); !errors.Is(err, ErrInvalidFail2BanInput) {
		t.Fatalf("UnbanIP() error = %v, want ErrInvalidFail2BanInput", err)
	}
}

func TestParseJailList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"single", "Status\n`- Jail list:\tsshd", []string{"sshd"}},
		{"multi", "`- Jail list:\tsshd, nginx, postfix", []string{"sshd", "nginx", "postfix"}},
		{"empty", "`- Jail list:\t", nil},
		{"no line", "Status\nother\n", nil},
		{"spaces", "Jail list:   a ,  b  , c  ", []string{"a", "b", "c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseJailList(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v (len=%d) want %v (len=%d)", got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got %q want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseJailStatus_Full(t *testing.T) {
	t.Parallel()
	out := "Status for the jail: sshd\n" +
		"Currently banned: 2\n" +
		"Total banned: 15\n" +
		"File list: /var/log/auth.log\n" +
		"Banned IP list: 1.2.3.4 5.6.7.8\n"
	j := parseJailStatus("sshd", out)
	if j.Name != "sshd" {
		t.Errorf("Name: got %q", j.Name)
	}
	if j.Currently != 2 {
		t.Errorf("Currently: got %d", j.Currently)
	}
	if j.Total != 15 {
		t.Errorf("Total: got %d", j.Total)
	}
	if len(j.BannedIPs) != 2 {
		t.Errorf("BannedIPs: got %v", j.BannedIPs)
	}
	if j.LogPath != "/var/log/auth.log" {
		t.Errorf("LogPath: got %q", j.LogPath)
	}
}

func TestParseJailStatus_Empty(t *testing.T) {
	t.Parallel()
	j := parseJailStatus("nginx", "")
	if j.Name != "nginx" {
		t.Errorf("Name: got %q", j.Name)
	}
	if j.BannedIPs == nil {
		t.Error("BannedIPs must be non-nil")
	}
}

func TestValidateIPArg(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, ip string
		wantErr  bool
	}{
		{"ok ipv4", "192.168.1.1", false}, {"ok ipv6", "2001:db8::1", false},
		{"trimmed spaces", "  10.0.0.1  ", false},
		{"empty", "", true}, {"semicolon", "1.2.3.4; rm", true},
		{"pipe", "1.2.3.4|cat", true}, {"amp", "1.2.3.4&&echo", true},
		{"backtick", "`id`", true}, {"dollar", "$(id)", true},
		{"newline", "1.2.3.4\n", false}, {"tab", "1.2\t3.4", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateIPArg(tc.ip)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateIPArg(%q): gotErr=%v wantErr=%v", tc.ip, err != nil, tc.wantErr)
			}
		})
	}
}

func TestAfterColon(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Key: value", " value"}, {"No colon", ""}, {"a:b:c", "b:c"}, {":", ""},
	}
	for _, tc := range tests {
		if got := afterColon(tc.in); got != tc.want {
			t.Errorf("afterColon(%q): got %q want %q", tc.in, got, tc.want)
		}
	}
}
