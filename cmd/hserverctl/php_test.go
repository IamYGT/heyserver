package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPHPCommandsUseObservedLocalAndManagedInventory(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contentFile := filepath.Join(directory, "www.conf")
	if err := os.WriteFile(contentFile, []byte("[www]\npm = ondemand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(directory, "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '[www]\\npm = ondemand\\n' >\"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	checksumBytes := sha256.Sum256([]byte("[www]\npm = dynamic\n"))
	checksum := hex.EncodeToString(checksumBytes[:])
	var localTests, localReloads, localRestarts, localSaves, remoteSaves, remoteActions, opcacheResets atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/versions":
			_, _ = io.WriteString(writer, `[{"version":"8.4","active":true,"info":"PHP 8.4","pool_dir":"/etc/php/8.4/fpm/pool.d","pool_count":1}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/pools":
			_, _ = io.WriteString(writer, `[{"name":"example.com","version":"8.4","config_file":"/etc/php/8.4/fpm/pool.d/example.com.conf","user":"example","group":"example","listen":"/run/php/example.sock","pm":"dynamic","pm_settings":{"max_children":10},"socket_exists":true}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/pools/8.4/example.com":
			_, _ = io.WriteString(writer, `{"domain":"example.com","version":"8.4","pm":"dynamic","max_children":10}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/pools/8.4/example.com/config":
			_, _ = io.WriteString(writer, `{"path":"/etc/php/8.4/fpm/pool.d/example.com.conf","content":"[www]\npm = dynamic\n","checksum":"`+checksum+`","size":19,"mode":"0644"}`)
		case request.Method == http.MethodPut && request.URL.Path == "/api/php/pools/8.4/example.com/config":
			localSaves.Add(1)
			var payload struct {
				Checksum string `json:"checksum"`
				Reload   bool   `json:"reload"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Checksum != checksum || !payload.Reload {
				t.Errorf("local save payload = %#v, err=%v", payload, err)
			}
			_, _ = io.WriteString(writer, `{"message":"PHP-FPM pool saved and tested and reloaded","backup":"/etc/php/8.4/fpm/pool.d/example.com.conf.hserver-backup"}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/php/versions/8.4/actions/test":
			localTests.Add(1)
			_, _ = io.WriteString(writer, `{"message":"PHP-FPM configuration is valid"}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/php/versions/8.4/actions/reload":
			localReloads.Add(1)
			_, _ = io.WriteString(writer, `{"message":"php8.4-fpm reloaded after configuration validation"}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/php/versions/8.4/actions/restart":
			localRestarts.Add(1)
			_, _ = io.WriteString(writer, `{"message":"php8.4-fpm restarted after configuration validation"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/status/8.4":
			_, _ = io.WriteString(writer, `[{"domain":"example.com","version":"8.4","health_score":100}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/status/8.4/example.com":
			_, _ = io.WriteString(writer, `{"domain":"example.com","version":"8.4","health_score":100}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/opcache/8.4":
			_, _ = io.WriteString(writer, `{"enabled":true,"hit_rate":99.1}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/php/opcache/8.4/reset":
			opcacheResets.Add(1)
			_, _ = io.WriteString(writer, `{"message":"OPcache reset successfully"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west":
			_, _ = io.WriteString(writer, `{"id":"edge west","online":true,"capabilities":["php.read","php.write","php.action"]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west/php":
			_, _ = io.WriteString(writer, `[{"version":"8.4","unit":"php8.4-fpm.service","active":"active","enabled":"enabled","masked":false,"binary":"/usr/sbin/php-fpm8.4","pools":[{"name":"example.com","path":"/etc/php/8.4/fpm/pool.d/example.com.conf","user":"example","group":"example","listen":"/run/php/example.sock","pm":"dynamic","max_children":10}]}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge west/php/8.4/pools/example.com":
			_, _ = io.WriteString(writer, `{"path":"/etc/php/8.4/fpm/pool.d/example.com.conf","content":"[www]\npm = dynamic\n","checksum":"`+checksum+`","size":19,"mode":"0644"}`)
		case request.Method == http.MethodPut && request.URL.Path == "/api/nodes/edge west/php/8.4/pools/example.com":
			remoteSaves.Add(1)
			var payload struct {
				Checksum string `json:"checksum"`
				Reload   bool   `json:"reload"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Checksum != checksum || !payload.Reload {
				t.Errorf("save payload = %#v, err=%v", payload, err)
			}
			_, _ = io.WriteString(writer, `{"message":"PHP-FPM pool saved, tested, and reloaded","backup":"/etc/php/8.4/fpm/pool.d/example.com.conf.hserver-backup"}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/nodes/edge west/php/8.4/actions/restart":
			remoteActions.Add(1)
			_, _ = io.WriteString(writer, `{"message":"PHP-FPM configuration tested and restarted"}`)
		default:
			http.Error(writer, request.Method+" "+request.URL.RequestURI(), http.StatusNotFound)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	commands := [][]string{
		{"php", "versions"},
		{"php", "pools", "--version", "8.4"},
		{"php", "pool", "get", "8.4", "example.com"},
		{"php", "config", "get", "8.4", "example.com"},
		{"php", "config", "edit", "--confirm", "--editor", editor, "--reload", "8.4", "example.com"},
		{"php", "config", "save", "--confirm", "--checksum", checksum, "--content-file", contentFile, "--reload", "8.4", "example.com"},
		{"php", "action", "--confirm", "8.4", "test"},
		{"php", "action", "--confirm", "8.4", "reload"},
		{"php", "action", "--confirm", "8.4", "restart"},
		{"php", "status", "8.4"},
		{"php", "status", "8.4", "example.com"},
		{"php", "opcache", "get", "8.4"},
		{"php", "opcache", "reset", "--confirm", "8.4"},
		{"php", "versions", "--node", "edge west"},
		{"php", "pools", "--node", "edge west", "--version", "8.4"},
		{"php", "config", "get", "--node", "edge west", "8.4", "example.com"},
		{"php", "config", "edit", "--confirm", "--editor", editor, "--node", "edge west", "--reload", "8.4", "example.com"},
		{"php", "config", "save", "--confirm", "--node", "edge west", "--checksum", checksum, "--content-file", contentFile, "--reload", "8.4", "example.com"},
		{"php", "action", "--confirm", "--node", "edge west", "8.4", "restart"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s failed: %v", strings.Join(command, " "), err)
		}
		if output.Len() == 0 {
			t.Fatalf("%s returned no JSON", strings.Join(command, " "))
		}
	}
	if localTests.Load() != 1 || localReloads.Load() != 1 || localRestarts.Load() != 1 || localSaves.Load() != 2 || remoteSaves.Load() != 2 || remoteActions.Load() != 1 || opcacheResets.Load() != 1 {
		t.Fatalf("mutations: local-test=%d local-reload=%d local-restart=%d local-save=%d remote-save=%d remote-action=%d opcache=%d", localTests.Load(), localReloads.Load(), localRestarts.Load(), localSaves.Load(), remoteSaves.Load(), remoteActions.Load(), opcacheResets.Load())
	}
}

func TestPHPReadOnlyDiagnosticsUseBoundedTypedOutput(t *testing.T) {
	t.Parallel()
	var outdatedProject string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/extensions/8.4":
			_, _ = io.WriteString(writer, `[{"name":"curl","enabled":true,"version":"8.4.0","type":"shared","ini_file":"/etc/php/8.4/fpm/conf.d/20-curl.ini","provider_secret":"secret-value\u001b[31m"}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/ini/8.4":
			_, _ = io.WriteString(writer, `{"memory_limit":"128M\u001b[31m","display_errors":"0"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/ini/8.4/portal":
			_, _ = io.WriteString(writer, `{"memory_limit":"256M","display_errors":"0"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/ini/8.4/diff":
			_, _ = io.WriteString(writer, `[{"key":"memory_limit","current":"128M","default":"128M","source":"global","provider_secret":"secret-value"}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/ini/8.4/directives":
			_, _ = io.WriteString(writer, `[{"key":"memory_limit","value":"128M","default_value":"128M","type":"bytes","section":"Resource Limits","description":"memory\u001b[31mlimit","changeable":"PHP_INI_SYSTEM","provider_secret":"secret-value"}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/logs/8.4/error":
			if got := request.URL.Query().Get("lines"); got != "2" {
				t.Errorf("error-log lines query = %q, want 2", got)
			}
			_, _ = io.WriteString(writer, `["first line\u001b[31m","second\tline\ncontinuation"]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/logs/8.4/portal/slow":
			if got := request.URL.Query().Get("lines"); got != "1" {
				t.Errorf("slow-log lines query = %q, want 1", got)
			}
			_, _ = io.WriteString(writer, `[{"timestamp":"2026-08-29T00:00:00Z","script":"/srv/app/index.php\u001b","duration":1.2,"backtrace":["foo\u001b","bar\nbaz"],"provider_secret":"secret-value"}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/security/profiles":
			_, _ = io.WriteString(writer, `[{"name":"Strict","description":"Maximum\u001b security","level":"strict","disable_functions":["exec\u001b","system"],"open_basedir":"{DOCROOT}:/tmp","allow_url_fopen":false,"allow_url_include":false,"expose_php":false,"display_errors":false,"log_errors":true,"session_cookie_secure":true,"session_cookie_httponly":true,"session_cookie_samesite":"Strict","provider_secret":"secret-value"}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/security/8.4/portal":
			_, _ = io.WriteString(writer, `{"score":{"score":80,"level":"strict","issues":[{"severity":"warning","setting":"display_errors","current":"0\u001b","recommended":"Off","description":"safe\ndescription"}],"provider_secret":"secret-value"},"profile":{"name":"Strict","description":"Maximum security","level":"strict","disable_functions":[],"open_basedir":"{DOCROOT}:/tmp","allow_url_fopen":false,"allow_url_include":false,"expose_php":false,"display_errors":false,"log_errors":true,"session_cookie_secure":true,"session_cookie_httponly":true,"session_cookie_samesite":"Strict"},"provider_secret":"secret-value"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/composer/version":
			_, _ = io.WriteString(writer, `{"version":"Composer version 2.8.0\u001b[31m","provider_secret":"secret-value"}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/php/composer/8.4/outdated":
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("Composer outdated payload decode: %v", err)
			} else {
				outdatedProject = payload["project_dir"]
			}
			_, _ = io.WriteString(writer, `[{"name":"vendor/package","current_version":"1.0.0","latest_version":"1.1.0","description":"Package\u001b description","abandoned":false,"provider_secret":"secret-value"}]`)
		default:
			http.Error(writer, request.Method+" "+request.URL.RequestURI(), http.StatusNotFound)
		}
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	commands := []struct {
		args     []string
		contains string
	}{
		{args: []string{"php", "extensions", "8.4"}, contains: "curl"},
		{args: []string{"php", "ini", "get", "8.4"}, contains: "memory_limit"},
		{args: []string{"php", "ini", "get", "8.4", "portal"}, contains: "256M"},
		{args: []string{"php", "ini", "diff", "8.4"}, contains: "global"},
		{args: []string{"php", "ini", "directives", "8.4"}, contains: "PHP_INI_SYSTEM"},
		{args: []string{"php", "logs", "error", "--lines", "2", "8.4"}, contains: "first line"},
		{args: []string{"php", "logs", "slow", "--lines", "1", "8.4", "portal"}, contains: "index.php"},
		{args: []string{"php", "security", "profiles"}, contains: "Strict"},
		{args: []string{"php", "security", "status", "8.4", "portal"}, contains: "80"},
		{args: []string{"php", "composer", "version"}, contains: "Composer version"},
		{args: []string{"php", "composer", "outdated", "--project-dir", "/var/www/vhosts/example.com", "8.4"}, contains: "vendor/package"},
	}
	for _, command := range commands {
		var output bytes.Buffer
		args := append([]string{"--server", server.URL}, command.args...)
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%s failed: %v", strings.Join(command.args, " "), err)
		}
		if !strings.Contains(output.String(), command.contains) {
			t.Fatalf("%s output does not contain %q: %s", strings.Join(command.args, " "), command.contains, output.String())
		}
		if strings.Contains(output.String(), "secret-value") || strings.Contains(output.String(), `\u001b`) || strings.Contains(output.String(), "\x1b") {
			t.Fatalf("%s exposed provider data or terminal controls: %s", strings.Join(command.args, " "), output.String())
		}
	}
	if outdatedProject != "/var/www/vhosts/example.com" {
		t.Fatalf("Composer outdated project_dir = %q", outdatedProject)
	}
}

func TestPHPConfigGetSanitizesReadOnlyContent(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/versions":
			_, _ = io.WriteString(writer, `[{"version":"8.4"}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/pools":
			_, _ = io.WriteString(writer, `[{"name":"portal","version":"8.4","config_file":"/etc/php/8.4/fpm/pool.d/portal.conf"}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/php/pools/8.4/portal/config":
			_, _ = io.WriteString(writer, `{"path":"/etc/php/8.4/fpm/pool.d/portal.conf\u001b","content":"[www]\r\npm = dynamic\u001b[31m\n","checksum":"abc\u001b","provider_secret":"secret-value"}`)
		default:
			http.Error(writer, request.Method+" "+request.URL.RequestURI(), http.StatusNotFound)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	err := run(context.Background(), []string{"--server", server.URL, "php", "config", "get", "8.4", "portal"}, &output, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "secret-value") || strings.Contains(output.String(), `\u001b`) {
		t.Fatalf("config output exposed provider data or terminal controls: %s", output.String())
	}
	if !strings.Contains(output.String(), `"content": "[www]\npm = dynamic[31m\n"`) {
		t.Fatalf("config output did not preserve safe line structure: %s", output.String())
	}
}

func TestPHPCommandsRejectUnsafeInputBeforeHTTP(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contentFile := filepath.Join(directory, "www.conf")
	if err := os.WriteFile(contentFile, []byte("[www]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"php", "pools", "--version", "latest"}, want: "MAJOR.MINOR"},
		{args: []string{"php", "pool", "get", "8.4", "../www"}, want: "portable name"},
		{args: []string{"php", "config", "get", "8.4", "../www"}, want: "portable name"},
		{args: []string{"php", "config", "edit", "--editor", "/bin/true", "8.4", "www"}, want: "explicit --confirm"},
		{args: []string{"php", "config", "save", "--checksum", strings.Repeat("a", 64), "--content-file", contentFile, "8.4", "www"}, want: "explicit --confirm"},
		{args: []string{"php", "config", "save", "--node", "edge-1", "--checksum", strings.Repeat("a", 64), "--content-file", contentFile, "8.4", "www"}, want: "explicit --confirm"},
		{args: []string{"php", "config", "save", "--confirm", "--node", "edge-1", "--content-file", contentFile, "8.4", "www"}, want: "64-character SHA-256"},
		{args: []string{"php", "action", "--confirm", "8.4", "stop"}, want: "must be test, reload, or restart"},
		{args: []string{"php", "action", "8.4", "restart"}, want: "explicit --confirm"},
		{args: []string{"php", "action", "--confirm", "--wait", "0s", "8.4", "restart"}, want: "greater than zero"},
		{args: []string{"php", "opcache", "reset", "8.4"}, want: "explicit --confirm"},
		{args: []string{"php", "extensions", "latest"}, want: "MAJOR.MINOR"},
		{args: []string{"php", "extensions", "8.4", "--node", "edge-1"}, want: "usage: hserverctl php extensions VERSION"},
		{args: []string{"php", "ini", "get", "8.4", "../www"}, want: "portable name"},
		{args: []string{"php", "logs", "error", "--lines", "0", "8.4"}, want: "between 1 and 5000"},
		{args: []string{"php", "logs", "slow", "--lines", "5001", "8.4", "www"}, want: "between 1 and 5000"},
		{args: []string{"php", "logs", "error", "--node", "edge-1", "8.4"}, want: "flag provided but not defined"},
		{args: []string{"php", "composer", "outdated", "8.4"}, want: "project-dir"},
		{args: []string{"php", "composer", "outdated", "--project-dir", "srv/app", "8.4"}, want: "clean absolute path"},
		{args: []string{"php", "composer", "outdated", "--project-dir", "/srv/app\nprod", "8.4"}, want: "control characters"},
		{args: []string{"php", "composer", "install", "8.4"}, want: "unknown php composer command"},
	}
	for _, item := range cases {
		args := append([]string{"--server", server.URL}, item.args...)
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, getenv)
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s error = %v", strings.Join(item.args, " "), err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected PHP commands sent %d request(s)", requests.Load())
	}
}

func TestPHPConfigSaveRejectsChangedChecksumBeforeWrite(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contentFile := filepath.Join(directory, "www.conf")
	if err := os.WriteFile(contentFile, []byte("[www]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge-1":
			_, _ = io.WriteString(writer, `{"id":"edge-1","online":true,"capabilities":["php.read","php.write"]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge-1/php":
			_, _ = io.WriteString(writer, `[{"version":"8.4","active":"active","binary":"/usr/sbin/php-fpm8.4","pools":[{"name":"www","path":"/etc/php/8.4/fpm/pool.d/www.conf"}]}]`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/nodes/edge-1/php/8.4/pools/www":
			_, _ = io.WriteString(writer, `{"path":"/etc/php/8.4/fpm/pool.d/www.conf","content":"newer","checksum":"`+strings.Repeat("b", 64)+`"}`)
		case request.Method == http.MethodPut:
			writes.Add(1)
			_, _ = io.WriteString(writer, `{}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	args := []string{"--server", server.URL, "php", "config", "save", "--confirm", "--node", "edge-1", "--checksum", strings.Repeat("a", 64), "--content-file", contentFile, "8.4", "www"}
	err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "checksum changed") || writes.Load() != 0 {
		t.Fatalf("error=%v writes=%d", err, writes.Load())
	}
}

func TestCLIHelpAndCompletionExposePHPCommands(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"php versions", "php pools", "php pool get", "php config get", "php config edit", "php config save", "php action", "php status", "php opcache", "php extensions", "php ini get", "php ini diff", "php ini directives", "php logs error", "php logs slow", "php security profiles", "php security status", "php composer version", "php composer outdated"} {
		if !strings.Contains(help.String(), command) {
			t.Fatalf("help does not expose %q: %q", command, help.String())
		}
	}
	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(completion.String(), "php") {
		t.Fatalf("completion does not expose php: %q", completion.String())
	}
}
