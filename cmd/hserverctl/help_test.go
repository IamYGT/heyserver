package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCLICommandHelpServicesSupportsBothForms(t *testing.T) {
	for _, args := range [][]string{
		{"help", "services"},
		{"services", "--help"},
	} {
		args := args
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			var out bytes.Buffer
			if err := run(context.Background(), args, &out, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
				t.Fatalf("help returned error: %v", err)
			}
			for _, fragment := range []string{
				"Usage: hserverctl services COMMAND",
				"Subcommands:",
				"list",
				"logs",
				"action",
				"services action --confirm [--node NODE] [--wait DURATION] SERVICE start|stop|restart",
			} {
				if !strings.Contains(out.String(), fragment) {
					t.Errorf("help output does not contain %q: %q", fragment, out.String())
				}
			}
			if strings.Contains(out.String(), "nodes action") {
				t.Errorf("services help leaked another command family: %q", out.String())
			}
		})
	}
}

func TestCLICommandHelpNestedMutationIncludesSafetyFlags(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"services", "action", "--help"}, &out, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatalf("help returned error: %v", err)
	}
	for _, fragment := range []string{
		"Usage: hserverctl services action --confirm [--node NODE] [--wait DURATION] SERVICE start|stop|restart",
		"Flags:",
		"--confirm",
		"--node",
		"--wait",
		"Safety flags:",
	} {
		if !strings.Contains(out.String(), fragment) {
			t.Errorf("nested help output does not contain %q: %q", fragment, out.String())
		}
	}
}

func TestCLICommandHelpUnknownPathReturnsBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL,
		"help", "services", "missing",
	}, &out, &bytes.Buffer{}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), `unknown command path "services missing"`) {
		t.Fatalf("error = %v, output = %q", err, out.String())
	}
	if requests != 0 {
		t.Fatalf("unknown help path made %d network requests", requests)
	}
}

func TestCLICommandHelpDoesNotLoadAuthOrOpenNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	getenv := func(key string) string {
		if key == "HSERVER_URL" {
			return server.URL
		}
		if key == "HSERVER_TOKEN" {
			t.Fatal("command help attempted to load the environment token")
		}
		return ""
	}
	for _, args := range [][]string{
		{"help", "services", "action"},
		{"services", "action", "--help"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out, &bytes.Buffer{}, getenv); err != nil {
			t.Fatalf("%v help returned error: %v", args, err)
		}
	}
	if requests != 0 {
		t.Fatalf("command help made %d network requests", requests)
	}
}
