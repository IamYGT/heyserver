package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunDiskOverviewUsesLocalAndManagedReadOnlyEndpoints(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if len(body) != 0 {
			t.Errorf("read-only request body = %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.RequestURI {
		case "/api/disk/overview":
			_, _ = w.Write([]byte(`{"partitions":[{"mountPoint":"/","size":1000,"used":400,"available":600}],"ioStats":[],"totalSize":1000,"totalUsed":400,"totalFree":600}`))
		case "/api/nodes/edge%20west/disk":
			_, _ = w.Write([]byte(`[{"filesystem":"/dev/vda1","size":1000,"used":400,"available":600,"use_percent":40,"mountpoint":"/"}]`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.RequestURI)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	getenv := func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	}

	var localOut bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "disk", "overview",
	}, &localOut, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("local overview: %v", err)
	}
	if !strings.Contains(localOut.String(), `"totalFree": 600`) || strings.Contains(localOut.String(), "test-token") {
		t.Fatalf("local output = %q", localOut.String())
	}

	var managedOut bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "disk", "overview", "--node", "edge west",
	}, &managedOut, &bytes.Buffer{}, getenv); err != nil {
		t.Fatalf("managed overview: %v", err)
	}
	if !strings.Contains(managedOut.String(), `"mountpoint": "/"`) || strings.Contains(managedOut.String(), "test-token") {
		t.Fatalf("managed output = %q", managedOut.String())
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestRunDiskOverviewRejectsUnexpectedArgumentsBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL, "disk", "overview", "unexpected",
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "usage: hserverctl disk overview") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected command sent %d request(s)", requests.Load())
	}
}
