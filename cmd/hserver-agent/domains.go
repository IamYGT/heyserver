package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const maxManagedDomains = 512

var (
	domainServerNamePattern  = regexp.MustCompile(`(?m)\bserver_name\s+([^;]+);`)
	domainRootPattern        = regexp.MustCompile(`(?m)\broot\s+([^;]+);`)
	domainProxyPattern       = regexp.MustCompile(`(?m)\bproxy_pass\s+([^;]+);`)
	domainCertificatePattern = regexp.MustCompile(`(?m)\bssl_certificate\s+/etc/letsencrypt/live/([^/]+)/fullchain\.pem\s*;`)
	domainSSLListenPattern   = regexp.MustCompile(`(?m)\blisten\s+[^;]*\bssl\b`)
)

type managedDomain struct {
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases"`
	Config          string   `json:"config"`
	Enabled         bool     `json:"enabled"`
	SSL             bool     `json:"ssl"`
	CertificateName string   `json:"certificate_name,omitempty"`
	Root            string   `json:"root,omitempty"`
	ProxyTarget     string   `json:"proxy_target,omitempty"`
	Kind            string   `json:"kind"`
}

type domainController struct {
	nginx        nginxController
	allowRead    bool
	allowActions bool
}

func newDomainController(nginx nginxController, allowRead, allowActions bool) domainController {
	return domainController{nginx: nginx, allowRead: allowRead, allowActions: allowActions}
}

func (c domainController) Inventory() ([]managedDomain, error) {
	if !c.allowRead {
		return nil, errors.New("domain inventory is not enabled locally")
	}
	c.nginx.mu.Lock()
	defer c.nginx.mu.Unlock()
	entries, err := os.ReadDir(c.nginx.availableDir)
	if err != nil {
		return nil, fmt.Errorf("read Nginx domain directory: %w", err)
	}
	if len(entries) > maxManagedDomains*4 {
		return nil, errors.New("Nginx domain directory exceeds the inventory limit")
	}
	domains := make([]managedDomain, 0, len(entries))
	for _, entry := range entries {
		if len(domains) >= maxManagedDomains {
			break
		}
		if !agentNginxConfigNamePattern.MatchString(entry.Name()) || strings.Contains(entry.Name(), ".hserver-backup-") {
			continue
		}
		_, content, _, err := readManagedFile(filepath.Join(c.nginx.availableDir, entry.Name()))
		if err != nil {
			continue
		}
		domains = append(domains, parseManagedDomain(entry.Name(), string(content), c.nginx.enabled(entry.Name())))
	}
	sort.Slice(domains, func(i, j int) bool {
		if domains[i].Enabled != domains[j].Enabled {
			return domains[i].Enabled
		}
		return strings.ToLower(domains[i].Name) < strings.ToLower(domains[j].Name)
	})
	return domains, nil
}

func (c domainController) Action(ctx context.Context, name, action string) (string, error) {
	if !c.allowActions {
		return "", errors.New("domain actions are not enabled locally")
	}
	if !agentNginxConfigNamePattern.MatchString(name) || action != "enable" && action != "disable" {
		return "", errors.New("invalid domain action")
	}
	c.nginx.mu.Lock()
	defer c.nginx.mu.Unlock()
	target := filepath.Join(c.nginx.availableDir, name)
	if _, err := regularManagedFile(target); err != nil {
		return "", fmt.Errorf("domain configuration is unavailable: %w", err)
	}
	if err := os.MkdirAll(c.nginx.enabledDir, 0o755); err != nil {
		return "", err
	}
	link := filepath.Join(c.nginx.enabledDir, name)
	info, statErr := os.Lstat(link)
	if action == "enable" {
		if statErr == nil {
			if info.Mode()&os.ModeSymlink == 0 || !symlinkResolvesTo(link, target) {
				return "", errors.New("enabled domain entry is not the managed symlink")
			}
			return "Domain is already enabled", nil
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		if err := os.Symlink(target, link); err != nil {
			return "", err
		}
		if err := c.nginx.testLocked(ctx); err != nil {
			_ = os.Remove(link)
			return "", err
		}
		if err := c.nginx.reloadLocked(ctx); err != nil {
			_ = os.Remove(link)
			return "", err
		}
		return "Domain enabled and Nginx reloaded", nil
	}
	if os.IsNotExist(statErr) {
		return "Domain is already disabled", nil
	}
	if statErr != nil {
		return "", statErr
	}
	if info.Mode()&os.ModeSymlink == 0 || !symlinkResolvesTo(link, target) {
		return "", errors.New("enabled domain entry is not the managed symlink")
	}
	original, err := os.Readlink(link)
	if err != nil {
		return "", err
	}
	if err := os.Remove(link); err != nil {
		return "", err
	}
	restore := func() { _ = os.Symlink(original, link) }
	if err := c.nginx.testLocked(ctx); err != nil {
		restore()
		return "", err
	}
	if err := c.nginx.reloadLocked(ctx); err != nil {
		restore()
		return "", err
	}
	return "Domain disabled and Nginx reloaded", nil
}

func symlinkResolvesTo(link, target string) bool {
	resolved, err := filepath.EvalSymlinks(link)
	return err == nil && resolved == target
}

func parseManagedDomain(config, raw string, enabled bool) managedDomain {
	lines := make([]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		lines = append(lines, strings.SplitN(line, "#", 2)[0])
	}
	active := strings.Join(lines, "\n")
	names := make([]string, 0)
	seen := make(map[string]struct{})
	for _, match := range domainServerNamePattern.FindAllStringSubmatch(active, -1) {
		for _, name := range strings.Fields(match[1]) {
			if name == "_" || name == "" {
				continue
			}
			if _, exists := seen[name]; !exists {
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	primary := config
	for _, name := range names {
		if !strings.HasPrefix(name, "www.") && !strings.Contains(name, "*") {
			primary = name
			break
		}
	}
	if primary == config && len(names) > 0 {
		primary = names[0]
	}
	root, proxy, certificate := firstDomainMatch(domainRootPattern, active), firstDomainMatch(domainProxyPattern, active), firstDomainMatch(domainCertificatePattern, active)
	kind := "static"
	if proxy != "" {
		kind = "proxy"
	} else if strings.Contains(active, "fastcgi_pass") {
		kind = "php"
	}
	return managedDomain{Name: primary, Aliases: names, Config: config, Enabled: enabled, SSL: certificate != "" || domainSSLListenPattern.MatchString(active), CertificateName: certificate, Root: root, ProxyTarget: proxy, Kind: kind}
}

func firstDomainMatch(pattern *regexp.Regexp, value string) string {
	match := pattern.FindStringSubmatch(value)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}
