package mail

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWithRuntimeUsesConfiguredStalwartConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "stalwart.toml")
	configBody := []byte("[server.listener.submission]\nprotocol = \"smtp\"\nbind = \"127.0.0.1\"\nport = \"2525\"\n")
	if err := os.WriteFile(configPath, configBody, 0o600); err != nil {
		t.Fatal(err)
	}

	service := New("", "", "").WithRuntime("stalwart-custom", configPath, "/usr/local/bin/stalwart")
	listeners, err := service.GetListenerInfo()
	if err != nil {
		t.Fatal(err)
	}
	if service.serviceName != "stalwart-custom" || service.binary != "/usr/local/bin/stalwart" {
		t.Fatalf("runtime = service %q binary %q", service.serviceName, service.binary)
	}
	if len(listeners) != 1 || listeners[0].ID != "submission" || listeners[0].Port != 2525 {
		t.Fatalf("listeners = %#v", listeners)
	}
}

func TestNewLeavesOptionalRuntimeUnconfigured(t *testing.T) {
	service := New("", "", "")
	if service.baseURL != "" || service.username != "" || service.serviceName != "" || service.configPath != "" || service.binary != "" {
		t.Fatalf("default runtime = baseURL=%q username=%q service=%q config=%q binary=%q; want all optional values empty", service.baseURL, service.username, service.serviceName, service.configPath, service.binary)
	}
	if status := service.GetStatus(); status.Status != "not_configured" {
		t.Fatalf("unconfigured status = %#v, want not_configured", status)
	}
	if err := service.StartService(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unconfigured StartService() = %v, want ErrNotConfigured", err)
	}
}
