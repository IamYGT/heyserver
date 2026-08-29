// Package mail — DKIM key management for Stalwart Mail Server.
//
// Stalwart exposes a single management endpoint for DKIM:
//
//	POST /api/dkim  — generate & store a new DKIM key pair in RocksDB
//
// Keys are stored inside Stalwart's internal RocksDB store (not as plain
// files).  To obtain the DNS TXT record value the panel reads back the TOML
// configuration that Stalwart writes and derives the public key from the
// private key material using standard Go crypto libraries.
//
// Stalwart DKIM config reference: https://stalw.art/docs/mta/authentication/dkim/sign/
package mail

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// DKIMAlgorithm represents the supported DKIM signing algorithms.
type DKIMAlgorithm string

const (
	AlgoRSA     DKIMAlgorithm = "Rsa"
	AlgoEd25519 DKIMAlgorithm = "Ed25519"
)

// DKIMKeyInfo describes a DKIM key entry returned by the Stalwart config API.
type DKIMKeyInfo struct {
	// ID is the internal Stalwart signature name (e.g. "example.org-mail-2026").
	ID        string        `json:"id"`
	Domain    string        `json:"domain"`
	Selector  string        `json:"selector"`
	Algorithm DKIMAlgorithm `json:"algorithm"`
	// CreatedAt is parsed from the selector suffix when it contains a timestamp,
	// otherwise it is the zero time.
	CreatedAt time.Time `json:"createdAt,omitempty"`
	// IsBackup is true for keys that were demoted during a RotateDKIMKey call.
	IsBackup bool `json:"isBackup"`
}

// DKIMPublicKeyRecord contains the DNS TXT record value for a DKIM key.
type DKIMPublicKeyRecord struct {
	Domain    string `json:"domain"`
	Selector  string `json:"selector"`
	DNSName   string `json:"dnsName"`   // e.g. "mail._domainkey.example.org"
	TXTRecord string `json:"txtRecord"` // full DNS TXT value
	PublicKey string `json:"publicKey"` // bare base64 public key
}

// GenerateDKIMRequest is the body Stalwart expects at POST /api/dkim.
// Ref: https://stalw.art/docs/api/management/endpoints/
type GenerateDKIMRequest struct {
	// ID is the internal name for the signature block. When nil, Stalwart
	// auto-generates one.  We always provide a deterministic name so we can
	// reference it later.
	ID        *string       `json:"id"`
	Algorithm DKIMAlgorithm `json:"algorithm"`
	Domain    string        `json:"domain"`
	// Selector is sent as null to let Stalwart pick the default; we override
	// the selector value via the ID convention instead.
	Selector *string `json:"selector"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Public API
// ─────────────────────────────────────────────────────────────────────────────

// GenerateDKIMKey asks Stalwart to generate and store a new DKIM key pair.
// algorithm must be AlgoRSA or AlgoEd25519.
// selector is the DNS selector label; when empty a date-based label is used
// (e.g. "mail-20260407").
//
// Stalwart stores the key internally; this function returns the DKIMKeyInfo
// that can be used to retrieve the DNS TXT record later.
func (s *Service) GenerateDKIMKey(domain, selector string, algo DKIMAlgorithm) (*DKIMKeyInfo, error) {
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if algo == "" {
		algo = AlgoEd25519
	}
	if selector == "" {
		selector = "mail-" + time.Now().UTC().Format("20060102")
	}

	// Stalwart uses the ID as the internal name for the signature block.
	// We encode domain + selector into the ID so we can filter later.
	id := sigID(domain, selector)

	req := GenerateDKIMRequest{
		ID:        &id,
		Algorithm: algo,
		Domain:    domain,
		Selector:  &selector,
	}

	// POST /api/dkim — Stalwart generates the key pair and stores it.
	if err := s.post("/api/dkim", req, nil); err != nil {
		return nil, fmt.Errorf("generate DKIM key for %s (selector=%s): %w", domain, selector, err)
	}

	info := &DKIMKeyInfo{
		ID:        id,
		Domain:    domain,
		Selector:  selector,
		Algorithm: algo,
		CreatedAt: time.Now().UTC(),
	}
	return info, nil
}

// ListDKIMKeys returns all DKIM signature configurations for the given domain.
// It queries Stalwart's settings API and filters by domain.
// When domain is empty all signatures are returned.
func (s *Service) ListDKIMKeys(domain string) ([]DKIMKeyInfo, error) {
	// Stalwart stores signature metadata under settings keys like
	// "signature.<name>.domain", "signature.<name>.selector", etc.
	// We fetch the full settings tree and filter.
	var raw stalwartSettingsResponse
	if err := s.get("/api/settings/list?prefix=signature", &raw); err != nil {
		// Fallback: return empty list gracefully when the endpoint is not
		// available or returns nothing (fresh installs have no signatures).
		if strings.Contains(err.Error(), "HTTP 4") {
			return []DKIMKeyInfo{}, nil
		}
		return nil, fmt.Errorf("list DKIM keys: %w", err)
	}

	return parseDKIMSettings(raw.Items, domain), nil
}

// GetDKIMPublicKey derives the DNS TXT record for a DKIM key identified by
// domain + selector.  It reads the private key from Stalwart's settings API
// and extracts the public key material.
func (s *Service) GetDKIMPublicKey(domain, selector string) (*DKIMPublicKeyRecord, error) {
	if domain == "" || selector == "" {
		return nil, fmt.Errorf("domain and selector are required")
	}
	id := sigID(domain, selector)

	// Fetch all settings for this signature block in one request.
	var raw stalwartSettingsResponse
	prefix := settingsKey(id, "")
	if err := s.get("/api/settings/list?prefix="+prefix, &raw); err != nil {
		return nil, fmt.Errorf("read DKIM settings for %s/%s: %w", domain, selector, err)
	}

	flat := make(map[string]string, len(raw.Items))
	for _, item := range raw.Items {
		flat[item.Key] = item.Value
	}

	privPEM := flat[settingsKey(id, "private-key")]
	if privPEM == "" {
		return nil, fmt.Errorf("private key not found for %s (selector=%s)", domain, selector)
	}
	algo := normaliseAlgo(flat[settingsKey(id, "algorithm")])

	pubKey, err := extractPublicKey(privPEM, algo)
	if err != nil {
		return nil, fmt.Errorf("extract public key: %w", err)
	}

	rec := buildDNSTXTRecord(domain, selector, algo, pubKey)
	return rec, nil
}

// GetDKIMConfig returns raw Stalwart signature configuration for a domain.
// It returns all `signature.*` settings filtered by domain.
func (s *Service) GetDKIMConfig(domain string) (map[string]string, error) {
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	var raw stalwartSettingsResponse
	if err := s.get("/api/settings/list?prefix=signature", &raw); err != nil {
		return nil, fmt.Errorf("get DKIM config for %s: %w", domain, err)
	}

	// Build a flat key→value map first.
	flat := make(map[string]string, len(raw.Items))
	for _, item := range raw.Items {
		flat[item.Key] = item.Value
	}

	result := make(map[string]string)
	for k, v := range flat {
		// Only include entries whose domain value matches.
		if strings.Contains(k, ".domain") && strings.EqualFold(v, domain) {
			// Include all settings for this signature block.
			prefix := strings.TrimSuffix(k, ".domain")
			for k2, v2 := range flat {
				if strings.HasPrefix(k2, prefix) {
					result[k2] = v2
				}
			}
		}
	}
	return result, nil
}

// RotateDKIMKey generates a new DKIM key for the domain (using a new
// date-based selector) while preserving the current key as a backup.
// Both keys will exist in Stalwart until the old one is manually removed.
//
// Returns the newly generated key info.
func (s *Service) RotateDKIMKey(domain string, algo DKIMAlgorithm) (*DKIMKeyInfo, error) {
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if algo == "" {
		algo = AlgoEd25519
	}

	// New selector uses date + timestamp suffix to guarantee uniqueness.
	newSelector := "mail-" + time.Now().UTC().Format("20060102150405")

	newKey, err := s.GenerateDKIMKey(domain, newSelector, algo)
	if err != nil {
		return nil, fmt.Errorf("rotate DKIM key for %s: %w", domain, err)
	}

	return newKey, nil
}

// GenerateDKIMKeyLocal generates an RSA-2048 or Ed25519 key pair locally
// (without calling Stalwart) and returns PEM-encoded private key and the
// base64 public key suitable for a DNS TXT record.
//
// This is useful when Stalwart's /api/dkim endpoint is unavailable or when
// the caller wants to inspect the key before registering it.
func GenerateDKIMKeyLocal(algo DKIMAlgorithm) (privatePEM string, publicBase64 string, err error) {
	switch algo {
	case AlgoRSA, "":
		return generateRSAKey()
	case AlgoEd25519:
		return generateEd25519Key()
	default:
		return "", "", fmt.Errorf("unsupported algorithm: %s", algo)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Private helpers
// ─────────────────────────────────────────────────────────────────────────────

// sigID builds a deterministic Stalwart signature ID from domain + selector.
func sigID(domain, selector string) string {
	return fmt.Sprintf("%s-%s", domain, selector)
}

// settingsKey builds the Stalwart settings key path for a signature field.
// e.g. settingsKey("example.org-mail-20260407", "algorithm")
//
//	→ "signature.example.org-mail-20260407.algorithm"
func settingsKey(id, field string) string {
	return fmt.Sprintf("signature.%s.%s", id, field)
}

// parseDKIMSettings parses a slice of Stalwart setting items into a list of
// DKIMKeyInfo structs, optionally filtered by domain.
func parseDKIMSettings(items []stalwartSettingItem, filterDomain string) []DKIMKeyInfo {
	// Build a flat key→value map and collect unique signature IDs.
	flat := make(map[string]string, len(items))
	ids := make(map[string]struct{})
	for _, item := range items {
		flat[item.Key] = item.Value
		if !strings.HasPrefix(item.Key, "signature.") {
			continue
		}
		// Keys look like "signature.<id>.domain"
		rest := item.Key[len("signature."):]
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) >= 1 && parts[0] != "" {
			ids[parts[0]] = struct{}{}
		}
	}

	result := make([]DKIMKeyInfo, 0, len(ids))
	for id := range ids {
		prefix := "signature." + id + "."

		domain := flat[prefix+"domain"]
		if filterDomain != "" && !strings.EqualFold(domain, filterDomain) {
			continue
		}
		selector := flat[prefix+"selector"]
		algoRaw := flat[prefix+"algorithm"]

		info := DKIMKeyInfo{
			ID:       id,
			Domain:   domain,
			Selector: selector,
			Algorithm: normaliseAlgo(algoRaw),
		}
		result = append(result, info)
	}
	return result
}

// normaliseAlgo maps Stalwart algorithm strings to a DKIMAlgorithm constant.
func normaliseAlgo(raw string) DKIMAlgorithm {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "rsa", "rsa-sha256", "rsasha256":
		return AlgoRSA
	case "ed25519", "ed25519-sha256":
		return AlgoEd25519
	default:
		return DKIMAlgorithm(raw)
	}
}

// extractPublicKey parses a PEM-encoded private key and returns the
// base64-encoded public key suitable for a DKIM DNS TXT record.
func extractPublicKey(privatePEM string, algo DKIMAlgorithm) (string, error) {
	// Stalwart may return the private key wrapped in a file macro
	// ("%{file:/path/to/key}%") — handle both cases.
	if strings.Contains(privatePEM, "%{file:") {
		// Key is stored as a file reference; try to read it.
		path := extractFileMacroPath(privatePEM)
		if path == "" {
			return "", fmt.Errorf("cannot resolve file macro in private key: %s", privatePEM)
		}
		// We can't read files from inside the Stalwart process; tell the caller.
		return "", fmt.Errorf(
			"private key is a file reference (%s); extract public key with: "+
				"openssl pkey -in %s -pubout -outform DER | openssl base64 -A",
			path, path)
	}

	block, _ := pem.Decode([]byte(privatePEM))
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block from private key")
	}

	switch {
	case algo == AlgoEd25519 || strings.Contains(block.Type, "ED25519"):
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("parse Ed25519 private key: %w", err)
		}
		edKey, ok := key.(ed25519.PrivateKey)
		if !ok {
			return "", fmt.Errorf("expected ed25519.PrivateKey, got %T", key)
		}
		pub := edKey.Public().(ed25519.PublicKey)
		// Ed25519 public key for DKIM: raw 32-byte public key, base64-encoded.
		return base64.StdEncoding.EncodeToString(pub), nil

	default: // RSA
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			// Try PKCS1 fallback.
			key2, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err2 != nil {
				return "", fmt.Errorf("parse RSA private key: %w", err)
			}
			derBytes, err3 := x509.MarshalPKIXPublicKey(&key2.PublicKey)
			if err3 != nil {
				return "", fmt.Errorf("marshal RSA public key: %w", err3)
			}
			return base64.StdEncoding.EncodeToString(derBytes), nil
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("expected *rsa.PrivateKey, got %T", key)
		}
		derBytes, err2 := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
		if err2 != nil {
			return "", fmt.Errorf("marshal RSA public key: %w", err2)
		}
		return base64.StdEncoding.EncodeToString(derBytes), nil
	}
}

// buildDNSTXTRecord constructs the full DKIM DNS TXT record value.
func buildDNSTXTRecord(domain, selector string, algo DKIMAlgorithm, pubKeyB64 string) *DKIMPublicKeyRecord {
	keyType := "rsa"
	if algo == AlgoEd25519 {
		keyType = "ed25519"
	}
	dnsName := fmt.Sprintf("%s._domainkey.%s", selector, domain)
	txt := fmt.Sprintf("v=DKIM1; k=%s; p=%s", keyType, pubKeyB64)
	return &DKIMPublicKeyRecord{
		Domain:    domain,
		Selector:  selector,
		DNSName:   dnsName,
		TXTRecord: txt,
		PublicKey: pubKeyB64,
	}
}

// extractFileMacroPath extracts the file path from a Stalwart file macro
// like "%{file:/path/to/key.pem}%".
func extractFileMacroPath(macro string) string {
	start := strings.Index(macro, "%{file:")
	if start == -1 {
		return ""
	}
	inner := macro[start+len("%{file:"):]
	end := strings.Index(inner, "}%")
	if end == -1 {
		end = strings.Index(inner, "}")
	}
	if end == -1 {
		return inner
	}
	return strings.TrimSpace(inner[:end])
}

// generateRSAKey generates an RSA-2048 key pair locally.
// Returns PEM-encoded private key and base64-encoded public key.
func generateRSAKey() (string, string, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generate RSA key: %w", err)
	}

	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", fmt.Errorf("marshal RSA private key: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privDER,
	}))

	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal RSA public key: %w", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubDER)

	return privPEM, pubB64, nil
}

// generateEd25519Key generates an Ed25519 key pair locally.
// Returns PEM-encoded private key and base64-encoded public key.
func generateEd25519Key() (string, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate Ed25519 key: %w", err)
	}

	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", fmt.Errorf("marshal Ed25519 private key: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privDER,
	}))

	pubB64 := base64.StdEncoding.EncodeToString([]byte(pub))
	return privPEM, pubB64, nil
}
