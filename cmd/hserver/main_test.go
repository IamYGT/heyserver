package main

import (
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/IamYGT/heyserver/internal/config"
)

// TestMainPackageCompiles ensures cmd/hserver and its embed/import graph build.
func TestMainPackageCompiles(t *testing.T) {
	t.Parallel()
	if _, err := webAssets.ReadDir("web/dist"); err != nil {
		t.Fatalf("embedded web/dist unreadable: %v", err)
	}
}

// TestVersionConstantWired verifies config.Version is set without starting the server.
func TestVersionConstantWired(t *testing.T) {
	t.Parallel()
	if config.Version == "" {
		t.Fatal("config.Version must not be empty")
	}
}

// TestEmbeddedWebDistPresent confirms the SPA bundle is embedded for production builds.
func TestEmbeddedWebDistPresent(t *testing.T) {
	t.Parallel()
	sub, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		t.Fatalf("fs.Sub(web/dist): %v", err)
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		t.Fatalf("embedded index.html missing: %v", err)
	}
}

func TestEmbeddedOpenAPIContractPresent(t *testing.T) {
	t.Parallel()
	sub, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		t.Fatalf("fs.Sub(web/dist): %v", err)
	}
	content, err := fs.ReadFile(sub, "openapi.json")
	if err != nil {
		t.Fatalf("embedded openapi.json missing: %v", err)
	}
	var contract struct {
		OpenAPI    string `json:"openapi"`
		RouteCount int    `json:"x-hserver-route-count"`
	}
	if err := json.Unmarshal(content, &contract); err != nil {
		t.Fatalf("embedded openapi.json is invalid: %v", err)
	}
	if contract.OpenAPI != "3.1.0" || contract.RouteCount == 0 {
		t.Fatalf("embedded OpenAPI contract=%#v", contract)
	}
}
