package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxAgentCertificates = 512
	maxCertificateBytes  = 4 << 20
)

type managedCertificate struct {
	Name          string   `json:"name"`
	Domains       []string `json:"domains"`
	Issuer        string   `json:"issuer"`
	Serial        string   `json:"serial"`
	NotBefore     string   `json:"not_before"`
	NotAfter      string   `json:"not_after"`
	DaysRemaining int      `json:"days_remaining"`
	AutoRenew     bool     `json:"auto_renew"`
}

type sslController struct {
	runner       commandRunner
	nginx        nginxController
	allowRead    bool
	allowActions bool
	liveDir      string
	renewalDir   string
	configDir    string
	workDir      string
	logsDir      string
	certbot      string
	openssl      string
	caBundle     string
	now          func() time.Time
}

func newSSLController(runner commandRunner, nginx nginxController, allowRead, allowActions bool, configDir, workDir, logsDir, certbot, openssl, caBundle string) sslController {
	return sslController{
		runner: runner, nginx: nginx, allowRead: allowRead, allowActions: allowActions,
		liveDir: filepath.Join(configDir, "live"), renewalDir: filepath.Join(configDir, "renewal"),
		configDir: configDir, workDir: workDir, logsDir: logsDir, certbot: certbot, openssl: openssl, caBundle: caBundle,
		now: time.Now,
	}
}

func (c sslController) Inventory() ([]managedCertificate, error) {
	if !c.allowRead {
		return nil, errors.New("SSL certificate reading is not enabled locally")
	}
	entries, err := os.ReadDir(c.liveDir)
	if err != nil {
		return nil, fmt.Errorf("read certificate directory: %w", err)
	}
	if len(entries) > maxAgentCertificates {
		return nil, errors.New("certificate directory exceeds the inventory limit")
	}
	certificates := make([]managedCertificate, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !agentNginxConfigNamePattern.MatchString(name) {
			continue
		}
		certificate, readErr := c.readCertificate(name)
		if readErr != nil {
			continue
		}
		certificates = append(certificates, certificate)
	}
	sort.Slice(certificates, func(i, j int) bool {
		if certificates[i].DaysRemaining == certificates[j].DaysRemaining {
			return strings.ToLower(certificates[i].Name) < strings.ToLower(certificates[j].Name)
		}
		return certificates[i].DaysRemaining < certificates[j].DaysRemaining
	})
	return certificates, nil
}

func (c sslController) readCertificate(name string) (managedCertificate, error) {
	path := filepath.Join(c.liveDir, name, "cert.pem")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCertificateBytes {
		return managedCertificate{}, errors.New("certificate file is unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxCertificateBytes {
		return managedCertificate{}, errors.New("certificate file is unavailable")
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return managedCertificate{}, errors.New("certificate PEM is invalid")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return managedCertificate{}, errors.New("certificate is invalid")
	}
	_, renewErr := os.Stat(filepath.Join(c.renewalDir, name+".conf"))
	return managedCertificate{
		Name: name, Domains: append([]string(nil), cert.DNSNames...), Issuer: cert.Issuer.String(),
		Serial: strings.ToUpper(cert.SerialNumber.Text(16)), NotBefore: cert.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:      cert.NotAfter.UTC().Format(time.RFC3339),
		DaysRemaining: int(math.Floor(cert.NotAfter.Sub(c.now().UTC()).Hours() / 24)), AutoRenew: renewErr == nil,
	}, nil
}

func (c sslController) Action(ctx context.Context, name, action string) (string, error) {
	if !c.allowActions {
		return "", errors.New("SSL certificate actions are not enabled locally")
	}
	if !agentNginxConfigNamePattern.MatchString(name) {
		return "", errors.New("invalid certificate name")
	}
	base := filepath.Join(c.liveDir, name)
	switch action {
	case "check":
		if !regularCertificateFile(filepath.Join(base, "cert.pem")) || !regularCertificateFile(filepath.Join(base, "chain.pem")) {
			return "", errors.New("certificate chain is unavailable")
		}
		commandCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		if _, err := c.runner.run(commandCtx, c.openssl, "verify", "-CAfile", c.caBundle, "-untrusted", filepath.Join(base, "chain.pem"), filepath.Join(base, "cert.pem")); err != nil {
			return "", fmt.Errorf("certificate chain verification failed: %w", err)
		}
		return "Certificate chain is valid", nil
	case "renew":
		if !regularCertificateFile(filepath.Join(c.renewalDir, name+".conf")) {
			return "", errors.New("certificate renewal configuration is unavailable")
		}
		c.nginx.mu.Lock()
		defer c.nginx.mu.Unlock()
		commandCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		if _, err := c.runner.run(commandCtx, c.certbot, "renew", "--cert-name", name, "--non-interactive", "--config-dir", c.configDir, "--work-dir", c.workDir, "--logs-dir", c.logsDir); err != nil {
			return "", fmt.Errorf("certificate renewal failed: %w", err)
		}
		if err := c.nginx.testLocked(ctx); err != nil {
			return "", err
		}
		if err := c.nginx.reloadLocked(ctx); err != nil {
			return "", err
		}
		return "Certificate checked and renewed if due; Nginx reloaded", nil
	default:
		return "", errors.New("unsupported certificate action")
	}
}

func regularCertificateFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() <= maxCertificateBytes
}
