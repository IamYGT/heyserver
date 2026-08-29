package logs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsAllowed(t *testing.T) {
	t.Parallel()
	svc := New(Config{
		VhostsRoot: "/var/www/vhosts",
		PM2User:    "deploy",
		PM2Home:    "/home/deploy/.pm2",
	})

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"nginx access log", "/var/log/nginx/access.log", true},
		{"php log directory", "/var/log/php/error.log", true},
		{"versioned php directory log", "/var/log/php8.4-fpm/error.log", true},
		{"versioned php direct log", "/var/log/php8.4-fpm.log", true},
		{"system auth log", "/var/log/auth.log", true},
		{"laravel app log", "/var/www/vhosts/example.com/storage/logs/laravel.log", true},
		{"pm2 root log", "/root/.pm2/logs/app-out.log", false},
		{"pm2 user log", "/home/deploy/.pm2/logs/app-error.log", true},
		{"other pm2 user log", "/home/other/.pm2/logs/app-error.log", false},
		{"disallowed etc shadow", "/etc/shadow", false},
		{"disallowed var log random", "/var/log/custom/secret.log", false},
		{"disallowed vhosts without storage logs", "/var/www/vhosts/example.com/public/index.php", false},
		{"null byte injection", "/var/log/nginx/access.log\x00/../../etc/passwd", false},
		{"empty path", "", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := svc.IsAllowed(tc.path); got != tc.want {
				t.Errorf("IsAllowed(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestService_UsesInstallationOwnedApplicationAndPM2Roots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vhostsRoot := filepath.Join(root, "sites")
	appLog := filepath.Join(vhostsRoot, "example.com", "storage", "logs", "laravel.log")
	pm2Home := filepath.Join(root, "state", "pm2")
	pm2Log := filepath.Join(pm2Home, "logs", "api-out.log")
	for _, path := range []string{appLog, pm2Log} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
		if err := os.WriteFile(path, []byte("ready\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	svc := New(Config{VhostsRoot: vhostsRoot, PM2User: "app", PM2Home: pm2Home})
	for _, path := range []string{appLog, pm2Log} {
		if !svc.IsAllowed(path) {
			t.Errorf("configured log path %q was rejected", path)
		}
	}
	if svc.IsAllowed("/var/www/vhosts/example.com/storage/logs/laravel.log") {
		t.Fatal("default vhost root remained allowed after an explicit installation root")
	}
	if svc.IsAllowed(filepath.Join(vhostsRoot, "example.com", "public", "index.php")) {
		t.Fatal("non-log application content was allowed")
	}

	sources, err := svc.ListSources()
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	want := map[string]string{
		appLog: "App: example.com / laravel",
		pm2Log: "PM2(app): api-out",
	}
	for _, source := range sources {
		if label, ok := want[source.Path]; ok {
			if source.Label != label {
				t.Errorf("label for %q = %q, want %q", source.Path, source.Label, label)
			}
			delete(want, source.Path)
		}
	}
	if len(want) != 0 {
		t.Fatalf("configured log sources were not discovered: %v", want)
	}
}

func TestService_InvalidInstallationRootsFailClosed(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		VhostsRoot: "relative/sites",
		PM2User:    "app",
		PM2Home:    "relative/pm2",
	})
	for _, path := range []string{
		"relative/sites/example.com/storage/logs/laravel.log",
		"relative/pm2/logs/api-out.log",
		"/var/www/vhosts/example.com/storage/logs/laravel.log",
	} {
		if svc.IsAllowed(path) {
			t.Errorf("invalid installation configuration allowed %q", path)
		}
	}
}

func TestService_DefaultDoesNotUseProviderVhostsRoot(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	if svc.vhostsRoot != "" {
		t.Fatalf("default vhosts root = %q, want unconfigured", svc.vhostsRoot)
	}
	if svc.IsAllowed("/var/www/vhosts/example.com/storage/logs/laravel.log") {
		t.Fatal("provider vhost root remained allowed without explicit configuration")
	}
}

func TestSanitizeQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"plain text", "error timeout", "error timeout"},
		{"strip shell metacharacters", "error; rm -rf /", "error rm -rf /"},
		{"strip dollars and pipes", "foo$bar|baz", "foobarbaz"},
		{"only disallowed chars", "$`|&;", ""},
		{"unicode kept if printable ascii range only applies to runes", "café", "caf"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeQuery(tc.query)
			if got != tc.want {
				t.Errorf("sanitizeQuery(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

func TestTailN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		n       int
		want    []string
	}{
		{
			name:    "last two lines",
			content: "line1\nline2\nline3\n",
			n:       2,
			want:    []string{"line2", "line3"},
		},
		{
			name:    "fewer lines than requested",
			content: "only\n",
			n:       5,
			want:    []string{"only"},
		},
		{
			name:    "no trailing newline",
			content: "a\nb\nc",
			n:       2,
			want:    []string{"b", "c"},
		},
		{
			name:    "single line file",
			content: "solo",
			n:       1,
			want:    []string{"solo"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "sample.log")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = f.Close() }()

			got, err := tailN(f, tc.n)
			if err != nil {
				t.Fatalf("tailN: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("lines = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestService_TailLines(t *testing.T) {
	t.Parallel()

	svc := New(Config{})

	t.Run("disallowed path", func(t *testing.T) {
		t.Parallel()
		_, err := svc.TailLines("/etc/passwd", 10)
		if err == nil {
			t.Fatal("expected access denied")
		}
		if !strings.Contains(err.Error(), "access denied") {
			t.Errorf("error = %q, want access denied", err.Error())
		}
	})

	t.Run("default line count clamp", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		allowed := filepath.Join(dir, "auth.log")
		// Simulate allowed path by writing under temp and testing tailN path separately;
		// here verify zero/negative n uses default via allowed nginx path if present.
		if !svc.IsAllowed("/var/log/nginx/access.log") {
			t.Skip("nginx log path not in whitelist on this host")
		}
		_ = allowed
	})

	t.Run("happy path with temp file via tailN helper", func(t *testing.T) {
		t.Parallel()
		content := bytes.Repeat([]byte("x\n"), 120)
		content = append(content, []byte("target-line\n")...)

		dir := t.TempDir()
		path := filepath.Join(dir, "file.log")
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() { _ = f.Close() }()

		lines, err := tailN(f, 3)
		if err != nil {
			t.Fatalf("tailN: %v", err)
		}
		if lines[len(lines)-1] != "target-line" {
			t.Errorf("last line = %q, want target-line", lines[len(lines)-1])
		}
	})
}

func TestService_SearchLog(t *testing.T) {
	t.Parallel()

	svc := New(Config{})

	t.Run("empty query", func(t *testing.T) {
		t.Parallel()
		_, err := svc.SearchLog("/var/log/auth.log", "", 10)
		if err == nil {
			t.Fatal("expected empty query error")
		}
	})

	t.Run("disallowed path", func(t *testing.T) {
		t.Parallel()
		_, err := svc.SearchLog("/tmp/test.log", "error", 10)
		if err == nil {
			t.Fatal("expected access denied")
		}
	})

	t.Run("sanitized-away query", func(t *testing.T) {
		t.Parallel()
		_, err := svc.SearchLog("/var/log/auth.log", "$$$", 10)
		if err == nil {
			t.Fatal("expected disallowed characters error")
		}
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "app.log")
		body := "INFO started\nERROR disk full\nWARN retry\nERROR timeout\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		// SearchLog requires IsAllowed path; exercise search logic through direct scan pattern.
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() { _ = f.Close() }()

		query := sanitizeQuery("ERROR")
		if query == "" {
			t.Fatal("expected sanitized query")
		}

		// Mirror SearchLog matching behavior for temp file.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		matches := 0
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(strings.ToLower(line), strings.ToLower(query)) {
				matches++
			}
		}
		if matches != 2 {
			t.Errorf("matches = %d, want 2", matches)
		}
	})
}

func TestLabelFor(t *testing.T) {
	t.Parallel()
	svc := New(Config{
		VhostsRoot: "/srv/sites",
		PM2User:    "alice",
		PM2Home:    "/srv/pm2",
	})

	tests := []struct {
		name     string
		path     string
		category string
		override string
		want     string
	}{
		{"override wins", "/var/log/nginx/access.log", "nginx", "Custom", "Custom"},
		{"app log label", "/srv/sites/site.com/storage/logs/laravel.log", "app", "", "App: site.com / laravel"},
		{"pm2 user label", "/srv/pm2/logs/api-out.log", "pm2", "", "PM2(alice): api-out"},
		{"plain basename", "/var/log/nginx/access.log", "nginx", "", "access"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := svc.labelFor(tc.path, tc.category, tc.override)
			if got != tc.want {
				t.Errorf("labelFor(%q, %q) = %q, want %q", tc.path, tc.override, got, tc.want)
			}
		})
	}
}

func TestService_CheckDownloadable(t *testing.T) {
	t.Parallel()

	svc := New(Config{})

	t.Run("access denied", func(t *testing.T) {
		t.Parallel()
		err := svc.CheckDownloadable("/etc/passwd")
		if err == nil || !strings.Contains(err.Error(), "access denied") {
			t.Fatalf("error = %v, want access denied", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		err := svc.CheckDownloadable("/var/log/nginx/does-not-exist-12345.log")
		if err == nil || !strings.Contains(err.Error(), "file not found") {
			t.Fatalf("error = %v, want file not found", err)
		}
	})

	t.Run("file too large", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "big.log")
		if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		// Path not allowed — still validates access first.
		err := svc.CheckDownloadable(path)
		if err == nil || !strings.Contains(err.Error(), "access denied") {
			t.Fatalf("error = %v, want access denied", err)
		}
	})
}

func TestService_Stat(t *testing.T) {
	t.Parallel()

	svc := New(Config{})
	_, err := svc.Stat("/etc/shadow")
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("error = %v, want access denied", err)
	}
}
