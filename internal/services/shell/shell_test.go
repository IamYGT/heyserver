package shell_test

import (
	"testing"
	"time"
	"github.com/IamYGT/heyserver/internal/services/shell"
)

func TestExecute_AllowedCommands(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, cmd string; args []string }{
		{"hostname","hostname",nil},{"uptime","uptime",nil},
		{"nproc","nproc",nil},{"df","df",[]string{"-h"}},
		{"iptables","iptables",[]string{"--version"}},{"sudo","sudo",[]string{"--version"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := shell.Execute(tc.cmd, tc.args...)
			if err != nil { t.Fatalf("Execute(%q): %v", tc.cmd, err) }
			if result == nil { t.Fatal("nil result") }
		})
	}
}

func TestExecute_BlockedCommands(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"bash","sh","wget","curl","nc","python3","dd"} {
		cmd := cmd
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			_, err := shell.Execute(cmd)
			if err == nil { t.Errorf("Execute(%q) should be blocked", cmd) }
		})
	}
}

func TestExecute_InjectionPrevention(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, arg string }{
		{"semicolon","; rm -rf /"},{"pipe","| cat /etc/passwd"},
		{"ampersand","&& curl evil.com"},{"backtick","`id`"},
		{"dollar","$(whoami)"},{"dollarvar","$PATH"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := shell.Execute("hostname", tc.arg)
			if err == nil { t.Errorf("Execute(hostname, %q) should be rejected", tc.arg) }
		})
	}
}

func TestExecute_SafeArgs(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, cmd string; args []string }{
		{"df path","df",[]string{"-h","/tmp"}},
		{"ls tmp","ls",[]string{"/tmp"}},
		{"wc null","wc",[]string{"-l","/dev/null"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := shell.Execute(tc.cmd, tc.args...)
			if err != nil { t.Fatalf("Execute(%q %v): %v", tc.cmd, tc.args, err) }
		})
	}
}

func TestExecuteWithTimeout_Respected(t *testing.T) {
	t.Parallel()
	start := time.Now()
	result, err := shell.ExecuteWithTimeout(200*time.Millisecond, "tail", "-f", "/dev/null")
	elapsed := time.Since(start)
	if err == nil && result != nil && result.ExitCode == 0 {
		t.Error("should not succeed: process must be killed by timeout")
	}
	if elapsed > 3*time.Second { t.Errorf("not killed in time (took %v)", elapsed) }
}

func TestExecuteRaw(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		t.Parallel()
		out, err := shell.ExecuteRaw("hostname")
		if err != nil { t.Fatalf("ExecuteRaw(hostname): %v", err) }
		if out == "" { t.Error("empty output") }
	})
	t.Run("blocked", func(t *testing.T) {
		t.Parallel()
		_, err := shell.ExecuteRaw("bash")
		if err == nil { t.Error("bash should be blocked") }
	})
}
