package portaluser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxUsernameLen = 32
	usernamePrefix = "etp_"
	defaultGroup   = "www-data"
)

var domainSanitizer = regexp.MustCompile(`[^a-z0-9.-]`)

// PortalRootFromWebRoot returns the Pemm/ETP install root (parent of public/ or public_html/).
func PortalRootFromWebRoot(webRoot string) string {
	base := filepath.Clean(webRoot)
	leaf := filepath.Base(base)
	if leaf == "public" || leaf == "public_html" {
		return filepath.Dir(base)
	}
	return base
}

// UsernameForDomain returns a stable Linux username (<=32 chars) for a portal FQDN.
func UsernameForDomain(domain string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(domain))))
	suffix := hex.EncodeToString(hash[:4])

	label := domainSanitizer.ReplaceAllString(strings.ToLower(domain), "")
	if idx := strings.Index(label, "."); idx > 0 {
		label = label[:idx]
	}
	if len(label) > 14 {
		label = label[:14]
	}
	if label == "" {
		label = "portal"
	}

	user := usernamePrefix + label + "_" + suffix
	if len(user) > maxUsernameLen {
		user = usernamePrefix + hex.EncodeToString(hash[:8])
	}
	if len(user) > maxUsernameLen {
		user = user[:maxUsernameLen]
	}
	return user
}

// EnsureUser creates a system user with home = portalRoot if it does not exist.
func EnsureUser(username, portalRoot string) error {
	if username == "" || portalRoot == "" {
		return fmt.Errorf("username and portalRoot are required")
	}
	if err := os.MkdirAll(portalRoot, 0o755); err != nil {
		return fmt.Errorf("ensure portal root: %w", err)
	}

	if idRes, err := exec.Command("id", "-u", username).CombinedOutput(); err == nil && strings.TrimSpace(string(idRes)) != "" {
		return nil
	}

	cmd := exec.Command(
		"useradd",
		"-r",
		"-d", portalRoot,
		"-s", "/usr/sbin/nologin",
		"-M",
		"-c", "ETP portal isolated user",
		username,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("useradd %s: %w (%s)", username, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ChownPortalTree sets owner to the portal user and group www-data (nginx/FPM socket compat).
func ChownPortalTree(portalRoot, username string) error {
	if portalRoot == "" || username == "" {
		return fmt.Errorf("portalRoot and username are required")
	}
	cmd := exec.Command("chown", "-R", username+":"+defaultGroup, portalRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("chown %s: %w (%s)", portalRoot, err, strings.TrimSpace(string(out)))
	}

	writable := []string{"files", "cache", "logs", "public/uploads", "tmp_images"}
	for _, rel := range writable {
		p := filepath.Join(portalRoot, rel)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		_ = os.Chmod(p, 0o2775)
	}
	return nil
}

// OpenBasedirForPortal returns PHP open_basedir for a single install root.
func OpenBasedirForPortal(portalRoot string) string {
	root := filepath.Clean(portalRoot)
	return root + ":/tmp/:/usr/share/php/"
}
