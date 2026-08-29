package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateComposerProjectDirUsesConfiguredVhostsRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := filepath.Join(root, "example.org", "httpdocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	svc := NewWithConfig(ServiceConfig{VhostsRoot: root})

	if err := svc.validateComposerProjectDir(project); err != nil {
		t.Fatalf("configured project rejected: %v", err)
	}
	outside := t.TempDir()
	if err := svc.validateComposerProjectDir(outside); err == nil || !strings.Contains(err.Error(), "outside configured vhosts root") {
		t.Fatalf("outside project error = %v", err)
	}
}

func TestValidateComposerProjectDirRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escaped-project")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	svc := NewWithConfig(ServiceConfig{VhostsRoot: root})

	err := svc.validateComposerProjectDir(link)
	if err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("symlink escape error = %v", err)
	}
}

func TestValidateComposerProjectDirRejectsRelativeVhostsRoot(t *testing.T) {
	t.Parallel()

	svc := NewWithConfig(ServiceConfig{VhostsRoot: "relative/sites"})
	err := svc.validateComposerProjectDir("/srv/sites/example.org")
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative root error = %v", err)
	}
}
