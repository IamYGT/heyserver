package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDomainControllerInventoriesAndTogglesManagedNginxSites(t *testing.T) {
	root := t.TempDir()
	available, enabled := filepath.Join(root, "available"), filepath.Join(root, "enabled")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `server {
  listen 443 ssl;
  server_name www.example.com example.com;
  root /srv/example/public;
  ssl_certificate /etc/letsencrypt/live/example.com/fullchain.pem;
  fastcgi_pass unix:/run/php/php8.3-fpm.sock;
}`
	if err := os.WriteFile(filepath.Join(available, "example.conf"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: [][]byte{nil, nil, nil, nil}}
	nginx := newNginxController(runner, nil, true, true, available, enabled)
	controller := newDomainController(nginx, true, true)
	domains, err := controller.Inventory()
	if err != nil || len(domains) != 1 || domains[0].Name != "example.com" || domains[0].Kind != "php" || !domains[0].SSL || domains[0].CertificateName != "example.com" || domains[0].Enabled {
		t.Fatalf("Inventory = (%#v, %v)", domains, err)
	}
	message, err := controller.Action(context.Background(), "example.conf", "enable")
	if err != nil || message != "Domain enabled and Nginx reloaded" || !symlinkResolvesTo(filepath.Join(enabled, "example.conf"), filepath.Join(available, "example.conf")) {
		t.Fatalf("enable = (%q, %v)", message, err)
	}
	message, err = controller.Action(context.Background(), "example.conf", "disable")
	if err != nil || message != "Domain disabled and Nginx reloaded" {
		t.Fatalf("disable = (%q, %v)", message, err)
	}
	if _, err := os.Lstat(filepath.Join(enabled, "example.conf")); !os.IsNotExist(err) {
		t.Fatalf("enabled link remains: %v", err)
	}
	if len(runner.commands) != 4 || runner.commands[0].name != "nginx" || runner.commands[1].name != "systemctl" {
		t.Fatalf("commands = %#v", runner.commands)
	}
}
