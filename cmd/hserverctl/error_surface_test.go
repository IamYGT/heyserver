package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunFormatsActionableAPIErrorForCLI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-HServer-Integration-State", "unavailable")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"error":"upstream unavailable","next_action":"replace token=server-secret"}`))
	}))
	defer server.Close()

	err := run(context.Background(), []string{"--server", server.URL, "health"}, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" })
	if err == nil {
		t.Fatal("health unexpectedly succeeded")
	}
	if got, want := err.Error(), "HTTP 503: upstream unavailable"; got != want {
		t.Fatalf("legacy error = %q, want %q", got, want)
	}
	formatted := fmt.Sprintf("%v", err)
	for _, want := range []string{
		"HTTP 503: upstream unavailable",
		"state: unavailable",
		"next: replace token=[redacted]",
		"server: " + server.URL,
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted CLI error %q does not contain %q", formatted, want)
		}
	}
	if strings.Contains(formatted, "server-secret") {
		t.Fatalf("formatted CLI error leaked response secret: %q", formatted)
	}
}

func TestTUIActionableErrorNoticeUsesSafeMessage(t *testing.T) {
	err := &apiError{
		StatusCode: http.StatusServiceUnavailable,
		Message:    "upstream unavailable",
		State:      "unavailable",
		NextAction: "replace token=server-secret",
		ServerURL:  "https://panel.example.test",
		kind:       apiErrorHTTP,
	}
	model := tuiModel{loading: true, width: 100, height: 30}
	updated, command := model.Update(tuiLoadMsg{Err: err})
	if command != nil {
		t.Fatal("error notice scheduled an unexpected command")
	}
	result, ok := updated.(tuiModel)
	if !ok {
		t.Fatalf("updated model type = %T, want tuiModel", updated)
	}
	if !result.noticeError {
		t.Fatal("TUI error notice is not marked as an error")
	}
	want := clientErrorMessage(err)
	if result.notice != want {
		t.Fatalf("notice = %q, want %q", result.notice, want)
	}
	rendered := result.renderStatus(100)
	for _, want := range []string{
		"HTTP 503: upstream unavailable",
		"state: unavailable",
		"next: replace token=[redacted]",
		"server: https://panel.example.test",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered TUI notice %q does not contain %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "server-secret") {
		t.Fatalf("rendered TUI notice leaked response secret: %q", rendered)
	}
}
