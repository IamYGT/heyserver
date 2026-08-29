package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func generatedCompletionScript(t *testing.T, shell string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"completion", shell}, &out, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatalf("%s completion: %v", shell, err)
	}
	return out.String()
}

func completionTestShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func runGeneratedBashCompletion(t *testing.T, script string, words ...string) []string {
	t.Helper()
	var command strings.Builder
	command.WriteString(script)
	command.WriteString("\nCOMP_WORDS=(")
	for _, word := range words {
		command.WriteByte(' ')
		command.WriteString(completionTestShellQuote(word))
	}
	command.WriteString(")\n")
	fmt.Fprintf(&command, "COMP_CWORD=%d\n", len(words)-1)
	command.WriteString("_hserverctl_completions\nprintf '%s\\n' \"${COMPREPLY[@]}\"\n")

	output, err := exec.Command("bash", "-c", command.String()).CombinedOutput()
	if err != nil {
		t.Fatalf("bash completion for %q: %v\n%s", words, err, output)
	}
	lines := strings.Fields(string(output))
	return lines
}

func completionContains(candidates []string, expected string) bool {
	for _, candidate := range candidates {
		if candidate == expected {
			return true
		}
	}
	return false
}

func TestGeneratedCompletionUsesCanonicalNestedPathsAndGlobalPrefixes(t *testing.T) {
	t.Parallel()
	bash := generatedCompletionScript(t, "bash")

	bashSyntax := exec.Command("bash", "-n")
	bashSyntax.Stdin = strings.NewReader(bash)
	if output, err := bashSyntax.CombinedOutput(); err != nil {
		t.Fatalf("generated bash syntax: %v\n%s", err, output)
	}

	tests := []struct {
		name     string
		words    []string
		expected []string
	}{
		{
			name:     "services children",
			words:    []string{"hserverctl", "services", ""},
			expected: []string{"list", "logs", "action"},
		},
		{
			name:     "global context prefix",
			words:    []string{"hserverctl", "--context", "staging", "services", ""},
			expected: []string{"list", "logs", "action"},
		},
		{
			name:     "context commands",
			words:    []string{"hserverctl", "context", ""},
			expected: []string{"list", "current", "status", "add", "use", "remove"},
		},
		{
			name:     "services mutation flags",
			words:    []string{"hserverctl", "services", "action", "--"},
			expected: []string{"--confirm", "--node", "--wait"},
		},
		{
			name:     "updates agent children",
			words:    []string{"hserverctl", "updates", "agent", ""},
			expected: []string{"status", "upgrade", "rollback"},
		},
		{
			name:     "deploy domain children",
			words:    []string{"hserverctl", "deploy", "domain", ""},
			expected: []string{"create", "health", "tls", "delete"},
		},
		{
			name:     "snapshot children",
			words:    []string{"hserverctl", "backups", "snapshot", ""},
			expected: []string{"status", "list", "vhosts", "run", "restore", "destination", "purge"},
		},
		{
			name:     "snapshot destination values",
			words:    []string{"hserverctl", "backups", "snapshot", "destination", ""},
			expected: []string{"gdrive", "s3"},
		},
		{
			name:     "integration IDs",
			words:    []string{"hserverctl", "integrations", "show", ""},
			expected: []string{"cloudflare.dns", "notification.delivery"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := runGeneratedBashCompletion(t, bash, test.words...)
			for _, expected := range test.expected {
				if !completionContains(candidates, expected) {
					t.Errorf("completion for %q = %v; missing %q", test.words, candidates, expected)
				}
			}
		})
	}
}

func TestGeneratedCompletionRetainsCanonicalDataAcrossShells(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"bash", "zsh", "fish"} {
		shell := shell
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			script := generatedCompletionScript(t, shell)
			fragments := []string{
				"services",
				"--context",
				"updates agent",
				"status upgrade rollback",
				"deploy domain",
				"create health tls delete",
				"backups snapshot",
				"status list vhosts run restore destination purge",
				"cloudflare.dns",
				"notification.delivery",
			}
			if shell == "fish" {
				fragments = append(fragments, "-l confirm", "-l node", "-l wait")
			} else {
				fragments = append(fragments, "--confirm", "--node", "--wait")
			}
			for _, fragment := range fragments {
				if !strings.Contains(script, fragment) {
					t.Errorf("%s completion does not contain generated fragment %q", shell, fragment)
				}
			}
		})
	}
}
