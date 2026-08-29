package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFilesTarTargets_UsesConfiguredVhostsRoot(t *testing.T) {
	t.Parallel()

	vhostsRoot := filepath.Join(t.TempDir(), "sites")
	for _, name := range []string{"example.com", "foo"} {
		if err := os.MkdirAll(filepath.Join(vhostsRoot, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewAtWithVhostsRoot(t.TempDir(), vhostsRoot)

	all := svc.resolveFilesTarTargets(CreateOptions{})
	if len(all) != 1 || all[0] != strings.TrimPrefix(vhostsRoot, "/") {
		t.Fatalf("all targets = %v", all)
	}

	opts := CreateOptions{Type: "files", Vhosts: []string{"example.com", "foo"}}
	if err := svc.ValidateCreateOptions(opts); err != nil {
		t.Fatalf("ValidateCreateOptions: %v", err)
	}
	selected := svc.resolveFilesTarTargets(opts)
	want := []string{
		strings.TrimPrefix(filepath.Join(vhostsRoot, "example.com"), "/"),
		strings.TrimPrefix(filepath.Join(vhostsRoot, "foo"), "/"),
	}
	if len(selected) != len(want) {
		t.Fatalf("selected targets = %v, want %v", selected, want)
	}
	for i := range want {
		if selected[i] != want[i] {
			t.Errorf("selected[%d] = %q, want %q", i, selected[i], want[i])
		}
	}
}

func TestListVhostTargetsReturnsOnlyObservedPortableDirectories(t *testing.T) {
	t.Parallel()

	vhostsRoot := filepath.Join(t.TempDir(), "sites")
	if err := os.MkdirAll(vhostsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Beta.example", "alpha.example", ".hidden"} {
		if err := os.Mkdir(filepath.Join(vhostsRoot, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(vhostsRoot, "not-a-site"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(vhostsRoot, "linked.example")); err != nil {
		t.Fatal(err)
	}

	svc := NewAtWithVhostsRoot(t.TempDir(), vhostsRoot)
	targets, err := svc.ListVhostTargets()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha.example", "Beta.example"}
	if len(targets) != len(want) {
		t.Fatalf("targets = %#v, want %#v", targets, want)
	}
	for index := range want {
		if targets[index] != want[index] {
			t.Fatalf("targets = %#v, want %#v", targets, want)
		}
	}
}

func TestValidateCreateOptions_RejectsCallerSelectedOrStaleVhostTargets(t *testing.T) {
	t.Parallel()

	vhostsRoot := filepath.Join(t.TempDir(), "sites")
	if err := os.MkdirAll(filepath.Join(vhostsRoot, "example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(vhostsRoot, "linked.example")); err != nil {
		t.Fatal(err)
	}
	svc := NewAtWithVhostsRoot(t.TempDir(), vhostsRoot)
	tooMany := make([]string, 17)
	for i := range tooMany {
		tooMany[i] = "site-" + string(rune('a'+i))
	}

	tests := []struct {
		name string
		opts CreateOptions
		want string
	}{
		{"path traversal", CreateOptions{Type: "files", Vhosts: []string{"../etc"}}, "invalid vhost identity"},
		{"absolute path", CreateOptions{Type: "files", Vhosts: []string{"/etc"}}, "invalid vhost identity"},
		{"duplicate", CreateOptions{Type: "files", Vhosts: []string{"example.com", "example.com"}}, "duplicate vhost identity"},
		{"too many", CreateOptions{Type: "files", Vhosts: tooMany}, "at most 16 vhosts"},
		{"stale identity", CreateOptions{Type: "files", Vhosts: []string{"missing.example"}}, "is unavailable"},
		{"symlink identity", CreateOptions{Type: "files", Vhosts: []string{"linked.example"}}, "must be a direct directory"},
		{"database selector", CreateOptions{Type: "database", Vhosts: []string{"example.com"}}, "cannot select vhosts"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.ValidateCreateOptions(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateCreateOptions_InvalidVhostsRootFailsClosed(t *testing.T) {
	t.Parallel()

	svc := NewAtWithVhostsRoot(t.TempDir(), "relative/sites")
	err := svc.ValidateCreateOptions(CreateOptions{Type: "files"})
	if err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("error = %v", err)
	}
	if _, err := svc.ListVhostTargets(); err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("list error = %v", err)
	}
}

func TestNewAtDoesNotSelectProviderVhostsRoot(t *testing.T) {
	t.Parallel()

	svc := NewAt(t.TempDir())
	if svc.vhostsRoot != "" {
		t.Fatalf("NewAt vhosts root = %q, want unconfigured", svc.vhostsRoot)
	}
	if err := svc.ValidateCreateOptions(CreateOptions{Type: "files"}); err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("ValidateCreateOptions error = %v, want unconfigured root", err)
	}
}
