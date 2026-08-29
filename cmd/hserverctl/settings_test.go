package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const settingsTestBundle = `{"schema_version":1,"exported_at":"2026-08-27T12:00:00Z","source_version":"v1.0.0","settings":{"hostnameDisplay":"Community server","notifyOnError":"true","timezone":"Europe/Istanbul"}}`

func settingsTestEnvironment(key string) string {
	if key == "HSERVER_TOKEN" {
		return "settings-test-token"
	}
	return ""
}

func TestRunSettingsCoversAllRoutesWithExactRequests(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if got := request.Header.Get("Authorization"); got != "Bearer settings-test-token" {
			t.Errorf("Authorization = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if len(body) != 0 && request.Method != http.MethodPut && request.Method != http.MethodPost {
			t.Errorf("unexpected body for %s %s: %q", request.Method, request.URL.Path, body)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/settings":
			if len(body) != 0 {
				t.Errorf("settings list body = %q", body)
			}
			_, _ = io.WriteString(writer, `{"hostnameDisplay":"Current","mail_access_state":"not_configured"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/settings/hostnameDisplay":
			if len(body) != 0 {
				t.Errorf("settings get body = %q", body)
			}
			_, _ = io.WriteString(writer, `{"key":"hostnameDisplay","value":"Current"}`)
		case request.Method == http.MethodPut && request.URL.Path == "/api/settings":
			var payload map[string]string
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("settings set body: %v", err)
			}
			want := map[string]string{"hostnameDisplay": "Community server", "notifyOnError": "true"}
			if !reflect.DeepEqual(payload, want) {
				t.Errorf("settings set body = %#v, want %#v", payload, want)
			}
			_, _ = io.WriteString(writer, `{"status":"saved"}`)
		case request.Method == http.MethodDelete && request.URL.Path == "/api/settings/timezone":
			if len(body) != 0 {
				t.Errorf("settings delete body = %q", body)
			}
			_, _ = io.WriteString(writer, `{"status":"deleted"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/settings/portable":
			if len(body) != 0 {
				t.Errorf("settings export body = %q", body)
			}
			_, _ = io.WriteString(writer, settingsTestBundle)
		case request.Method == http.MethodPost && request.URL.Path == "/api/settings/portable/preview":
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("settings preview body: %v", err)
			}
			if payload["schema_version"] != float64(1) || payload["source_version"] != "v1.0.0" {
				t.Errorf("settings preview body = %#v", payload)
			}
			if _, exists := payload["bundle"]; exists {
				t.Errorf("settings preview unexpectedly wrapped bundle: %#v", payload)
			}
			_, _ = io.WriteString(writer, `{"schema_version":1,"imported_keys":3,"changed_keys":2,"unchanged_keys":1,"changes":[]}`)
		case request.Method == http.MethodPost && request.URL.Path == "/api/settings/portable/import":
			var payload struct {
				Bundle    map[string]any `json:"bundle"`
				Confirmed bool           `json:"confirmed"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("settings import body: %v", err)
			}
			if !payload.Confirmed || payload.Bundle["schema_version"] != float64(1) {
				t.Errorf("settings import body = %#v", payload)
			}
			_, _ = io.WriteString(writer, `{"schema_version":1,"imported_keys":3,"changed_keys":2,"unchanged_keys":1,"changes":[]}`)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.RequestURI())
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	exportPath := filepath.Join(t.TempDir(), "portable-settings.json")
	commands := [][]string{
		{"settings", "list"},
		{"settings", "get", "hostnameDisplay"},
		{"settings", "set", "hostnameDisplay=Community server", "notifyOnError=true"},
		{"settings", "delete", "--confirm", "timezone"},
		{"settings", "export", "--output", exportPath},
		{"settings", "preview", "--file", ""},
		{"settings", "import", "--file", "", "--confirm"},
	}

	// Export first creates the input used by the preview/import routes. The
	// command list remains in route order so the request assertions are exact.
	var output bytes.Buffer
	for index, command := range commands {
		if index == 5 {
			command[3] = exportPath
		}
		if index == 6 {
			command[3] = exportPath
		}
		args := append([]string{"--server", server.URL}, command...)
		output.Reset()
		if err := run(context.Background(), args, &output, &bytes.Buffer{}, settingsTestEnvironment); err != nil {
			t.Fatalf("%s: %v", strings.Join(command, " "), err)
		}
	}

	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != settingsTestBundle {
		t.Fatalf("export file = %q, want %q", data, settingsTestBundle)
	}
	info, err := os.Stat(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("export file mode = %o, want 600", got)
	}
	if strings.Contains(output.String(), "Community server") {
		t.Fatalf("portable settings value leaked to stdout: %q", output.String())
	}
	if got, want := requests.Load(), int32(len(commands)); got != want {
		t.Fatalf("requests = %d, want %d", got, want)
	}
}

func TestRunSettingsRequiresConfirmationAndRejectsUnsafeInputBeforeRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	directory := t.TempDir()
	validPath := filepath.Join(directory, "valid.json")
	if err := os.WriteFile(validPath, []byte(settingsTestBundle), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(directory, "link.json")
	if err := os.Symlink(validPath, linkPath); err != nil {
		t.Fatal(err)
	}
	overlargePath := filepath.Join(directory, "oversized.json")
	if err := os.WriteFile(overlargePath, bytes.Repeat([]byte("x"), maxSettingsPortableBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	trailingPath := filepath.Join(directory, "trailing.json")
	if err := os.WriteFile(trailingPath, []byte(settingsTestBundle+" null"), 0o600); err != nil {
		t.Fatal(err)
	}
	unknownPath := filepath.Join(directory, "unknown.json")
	if err := os.WriteFile(unknownPath, []byte(strings.Replace(settingsTestBundle, `"settings":`, `"unknown":1,"settings":`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "set unknown key", args: []string{"settings", "set", "password=secret"}, want: "unknown editable setting"},
		{name: "set duplicate key", args: []string{"settings", "set", "timezone=Europe/Istanbul", "timezone=UTC"}, want: "duplicate editable setting"},
		{name: "set invalid value", args: []string{"settings", "set", "notifyOnError=yes"}, want: "invalid value for editable setting"},
		{name: "delete without confirmation", args: []string{"settings", "delete", "timezone"}, want: "explicit --confirm"},
		{name: "import without confirmation", args: []string{"settings", "import", "--file", validPath}, want: "explicit --confirm"},
		{name: "preview symlink", args: []string{"settings", "preview", "--file", linkPath}, want: "regular file"},
		{name: "preview oversized", args: []string{"settings", "preview", "--file", overlargePath}, want: "exceeds"},
		{name: "preview trailing JSON", args: []string{"settings", "preview", "--file", trailingPath}, want: "trailing"},
		{name: "preview unknown field", args: []string{"settings", "preview", "--file", unknownPath}, want: "invalid portable settings JSON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--server", server.URL}, test.args...)
			err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, settingsTestEnvironment)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("rejected settings commands made %d request(s)", got)
	}
}

func TestRunSettingsExportRefusesExistingOutputAndDoesNotDumpToStdout(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(writer, settingsTestBundle)
	}))
	defer server.Close()

	directory := t.TempDir()
	existing := filepath.Join(directory, "existing.json")
	if err := os.WriteFile(existing, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{"", "-", existing} {
		args := []string{"--server", server.URL, "settings", "export"}
		if output != "" {
			args = append(args, "--output", output)
		}
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, settingsTestEnvironment)
		if err == nil {
			t.Fatalf("output %q unexpectedly succeeded", output)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unsafe exports made %d request(s)", got)
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("existing output changed: %q (%v)", data, err)
	}
}

func TestSettingsHelpAndCompletionExposeCanonicalRoutes(t *testing.T) {
	t.Parallel()

	var help bytes.Buffer
	if err := run(context.Background(), []string{"help", "settings"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"Usage: hserverctl settings COMMAND",
		"settings list",
		"settings get KEY",
		"settings set KEY=VALUE...",
		"settings delete --confirm KEY",
		"settings export --output FILE",
		"settings preview --file FILE",
		"settings import --file FILE --confirm",
	} {
		if !strings.Contains(help.String(), fragment) {
			t.Errorf("help does not contain %q: %q", fragment, help.String())
		}
	}

	catalog := cliCompletionCatalogFromHelp()
	wantChildren := []string{"list", "get", "set", "delete", "export", "preview", "import"}
	if got := catalog.children["settings"]; !reflect.DeepEqual(got, wantChildren) {
		t.Fatalf("settings completion children = %#v, want %#v", got, wantChildren)
	}
	for _, path := range []string{"settings delete", "settings export", "settings preview", "settings import"} {
		if len(catalog.flags[path]) == 0 {
			t.Fatalf("completion flags missing for %q", path)
		}
	}
	var completion bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"settings", "--output", "--file", "--confirm"} {
		if !strings.Contains(completion.String(), fragment) {
			t.Errorf("completion does not contain %q", fragment)
		}
	}
}
