package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSSLControllerInventoriesChecksAndRenewsCertificates(t *testing.T) {
	root := t.TempDir()
	configDir, workDir, logsDir := filepath.Join(root, "config"), filepath.Join(root, "work"), filepath.Join(root, "logs")
	live, renewal := filepath.Join(configDir, "live"), filepath.Join(configDir, "renewal")
	certDir := filepath.Join(live, "example.com")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(renewal, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	certificatePEM := testCertificatePEM(t, now.Add(-24*time.Hour), now.Add(30*24*time.Hour), []string{"example.com", "www.example.com"})
	for _, name := range []string{"cert.pem", "chain.pem"} {
		if err := os.WriteFile(filepath.Join(certDir, name), certificatePEM, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(renewal, "example.com.conf"), []byte("version = 2.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: [][]byte{[]byte("ok"), nil, nil, nil}}
	nginx := newNginxController(runner, nil, false, false, filepath.Join(root, "available"), filepath.Join(root, "enabled"))
	controller := newSSLController(runner, nginx, true, true, configDir, workDir, logsDir, "/usr/bin/certbot", "/usr/bin/openssl", "/etc/ssl/certs/ca-certificates.crt")
	controller.now = func() time.Time { return now }

	certificates, err := controller.Inventory()
	if err != nil || len(certificates) != 1 {
		t.Fatalf("Inventory = (%#v, %v)", certificates, err)
	}
	certificate := certificates[0]
	if certificate.Name != "example.com" || !reflect.DeepEqual(certificate.Domains, []string{"example.com", "www.example.com"}) || certificate.DaysRemaining != 30 || !certificate.AutoRenew {
		t.Fatalf("certificate = %#v", certificate)
	}
	if message, err := controller.Action(context.Background(), "example.com", "check"); err != nil || message != "Certificate chain is valid" {
		t.Fatalf("check = (%q, %v)", message, err)
	}
	if message, err := controller.Action(context.Background(), "example.com", "renew"); err != nil || message != "Certificate checked and renewed if due; Nginx reloaded" {
		t.Fatalf("renew = (%q, %v)", message, err)
	}
	if got := runner.commands; len(got) != 4 || got[0].name != "/usr/bin/openssl" || got[1].name != "/usr/bin/certbot" || !reflect.DeepEqual(got[1].args, []string{"renew", "--cert-name", "example.com", "--non-interactive", "--config-dir", configDir, "--work-dir", workDir, "--logs-dir", logsDir}) || got[2].name != "nginx" || got[3].name != "systemctl" {
		t.Fatalf("commands = %#v", got)
	}
}

func TestSSLControllerRejectsActionsWithoutLocalOptIn(t *testing.T) {
	controller := newSSLController(&fakeRunner{}, nginxController{}, false, false, "/etc/letsencrypt", "/var/lib/letsencrypt", "/var/log/letsencrypt", "/usr/bin/certbot", "/usr/bin/openssl", "/etc/ssl/certs/ca-certificates.crt")
	if _, err := controller.Inventory(); err == nil {
		t.Fatal("Inventory succeeded without read opt-in")
	}
	if _, err := controller.Action(context.Background(), "example.com", "renew"); err == nil {
		t.Fatal("Action succeeded without action opt-in")
	}
}

func testCertificatePEM(t *testing.T, notBefore, notAfter time.Time, dnsNames []string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: dnsNames[0]}, Issuer: pkix.Name{CommonName: "Heyserver Test CA"},
		NotBefore: notBefore, NotAfter: notAfter, DNSNames: dnsNames, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
