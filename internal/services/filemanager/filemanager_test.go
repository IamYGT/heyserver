package filemanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()

	root := t.TempDir()
	return NewWithAllowedRoots([]string{root}), root
}

func TestIsAllowed(t *testing.T) {
	t.Parallel()

	service, root := newTestService(t)
	tests := []struct {
		path string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "app"), true},
		{filepath.Join(root, "app", "public"), true},
		{root + "-sibling", false},
		{filepath.Dir(root), false},
		{"relative/path", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := service.isAllowed(tc.path); got != tc.want {
				t.Errorf("isAllowed(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsSensitive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{"/home/user/.ssh/id_rsa", true},
		{"/home/user/.ssh", true},
		{"/etc/shadow", true},
		{"/var/www/vhosts/site/ssl/private/key.pem", true},
		{"/var/www/vhosts/site/id_ed25519", true},
		{"/var/www/vhosts/site/public_html/index.php", false},
		{"/etc/nginx/nginx.conf", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := isSensitive(tc.path); got != tc.want {
				t.Errorf("isSensitive(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	t.Parallel()

	service, base := newTestService(t)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("mkdir test base: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	existingFile := filepath.Join(base, "existing.txt")
	if err := os.WriteFile(existingFile, []byte("ok"), 0644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	subDir := filepath.Join(base, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	outsideRoot := t.TempDir()
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"empty path", "", "path must not be empty"},
		{"outside allowed root", filepath.Join(outsideRoot, "outside.txt"), "not within an allowed directory"},
		{"sensitive ssh key", filepath.Join(base, ".ssh", "id_rsa"), "not permitted"},
		{"existing allowed file", existingFile, ""},
		{"existing allowed directory", subDir, ""},
		{
			"new file under allowed parent",
			filepath.Join(subDir, "new-file.txt"),
			"",
		},
		{
			"traversal cleaned to allowed",
			filepath.Join(base, "subdir/../existing.txt"),
			"",
		},
		{
			"parent outside allowed after clean",
			filepath.Join(base, "../outside.txt"),
			"not within an allowed directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := service.validatePath(tc.path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validatePath(%q) unexpected error: %v", tc.path, err)
				}
				if got == "" {
					t.Fatal("validatePath returned empty resolved path")
				}
				return
			}
			if err == nil {
				t.Fatalf("validatePath(%q) expected error containing %q", tc.path, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validatePath(%q) error = %q, want substring %q", tc.path, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidatePath_NonexistentParent(t *testing.T) {
	t.Parallel()

	service, root := newTestService(t)
	path := filepath.Join(root, "missing-parent", "nope.txt")
	if _, err := service.validatePath(path); err == nil {
		t.Fatal("expected error for nonexistent parent directory")
	} else if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewFailsClosed(t *testing.T) {
	t.Parallel()

	service := New()
	if roots := service.AllowedRoots(); len(roots) != 0 {
		t.Fatalf("New().AllowedRoots() = %v, want no implicit roots", roots)
	}
	if roots := AllowedRoots(); len(roots) != 0 {
		t.Fatalf("AllowedRoots() = %v, want no implicit roots", roots)
	}
	for _, path := range []string{"/var/www/vhosts", "/etc/nginx", "/home"} {
		if service.isAllowed(path) {
			t.Errorf("New().isAllowed(%q) = true, want false", path)
		}
	}
}

func TestAllowedRoots(t *testing.T) {
	t.Parallel()

	service, root := newTestService(t)
	roots := service.AllowedRoots()
	if len(roots) != 1 || roots[0] != root {
		t.Fatalf("AllowedRoots = %v, want [%s]", roots, root)
	}
	if strings.HasSuffix(roots[0], "/") {
		t.Errorf("root %q should not end with slash", roots[0])
	}
	if !service.isAllowed(roots[0]) {
		t.Errorf("AllowedRoots entry %q should be allowed", roots[0])
	}
}

func TestServiceUsesInstallationOwnedRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewWithAllowedRoots([]string{root, "relative/path"})
	if got := service.AllowedRoots(); len(got) != 1 || got[0] != root {
		t.Fatalf("AllowedRoots = %v, want [%s]", got, root)
	}
	if _, err := service.List(root); err != nil {
		t.Fatalf("List configured root: %v", err)
	}
	if _, err := service.List(filepath.Dir(root)); err == nil {
		t.Fatal("parent outside configured root should be rejected")
	}
}
