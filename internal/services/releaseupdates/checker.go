package releaseupdates

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/releaseversion"
)

const (
	StatusNotConfigured    = "not_configured"
	StatusUnavailable      = "unavailable"
	StatusHealthy          = "healthy"
	SignatureNotConfigured = "not_configured"
	SignatureUnavailable   = "unavailable"
	SignatureVerified      = "verified"

	manifestSchemaVersion     = 1
	maxManifestBytes          = 512 << 10
	maxManifestSignatureBytes = 1024
	maxManifestPublicKeys     = 8
	defaultTimeout            = 5 * time.Second
)

type Artifact struct {
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

type Manifest struct {
	SchemaVersion   int                 `json:"schema_version"`
	Version         string              `json:"version"`
	PublishedAt     string              `json:"published_at,omitempty"`
	ReleaseNotesURL string              `json:"release_notes_url,omitempty"`
	Artifacts       map[string]Artifact `json:"artifacts"`
}

type Result struct {
	Status             string               `json:"status"`
	CurrentVersion     string               `json:"current_version"`
	LatestVersion      string               `json:"latest_version,omitempty"`
	LatestVersionState releaseversion.State `json:"latest_version_state,omitempty"`
	UpdateAvailable    bool                 `json:"update_available"`
	Platform           string               `json:"platform"`
	Artifact           *Artifact            `json:"artifact,omitempty"`
	PublishedAt        string               `json:"published_at,omitempty"`
	ReleaseNotesURL    string               `json:"release_notes_url,omitempty"`
	Message            string               `json:"message"`
	CheckedAt          time.Time            `json:"checked_at"`
	SignatureStatus    string               `json:"signature_status"`
}

type Option func(*Checker)

func WithManifestPublicKeys(encoded string) Option {
	return func(checker *Checker) {
		checker.manifestPublicKeys = strings.TrimSpace(encoded)
	}
}

type Checker struct {
	manifestURL        string
	currentVersion     string
	platform           string
	client             *http.Client
	now                func() time.Time
	manifestPublicKeys string
}

func New(manifestURL, currentVersion string, options ...Option) *Checker {
	checker := newChecker(manifestURL, currentVersion, runtime.GOOS+"_"+runtime.GOARCH, &http.Client{Timeout: defaultTimeout})
	for _, option := range options {
		option(checker)
	}
	return checker
}

func newChecker(manifestURL, currentVersion, platform string, client *http.Client) *Checker {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Checker{
		manifestURL:    strings.TrimSpace(manifestURL),
		currentVersion: currentVersion,
		platform:       platform,
		client:         client,
		now:            time.Now,
	}
}

func (c *Checker) Check(ctx context.Context) Result {
	result := Result{
		CurrentVersion:  c.currentVersion,
		Platform:        c.platform,
		CheckedAt:       c.now().UTC(),
		SignatureStatus: SignatureNotConfigured,
	}
	if c.manifestURL == "" {
		result.Status = StatusNotConfigured
		result.Message = "Configure HSERVER_UPDATE_MANIFEST_URL to enable release discovery."
		return result
	}
	if !validHTTPURL(c.manifestURL) {
		result.Status = StatusUnavailable
		result.Message = "The configured update manifest URL must use HTTP or HTTPS."
		return result
	}
	publicKeys, err := decodeManifestPublicKeys(c.manifestPublicKeys)
	if err != nil {
		result.SignatureStatus = SignatureUnavailable
		return unavailable(result, "The configured release signing public key is invalid.")
	}
	if len(publicKeys) > 0 {
		result.SignatureStatus = SignatureUnavailable
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.manifestURL, nil)
	if err != nil {
		return unavailable(result, "The configured update manifest URL is invalid.")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "HServer-Panel/"+c.currentVersion)
	response, err := c.client.Do(request)
	if err != nil {
		return unavailable(result, "Could not reach the configured update manifest.")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return unavailable(result, fmt.Sprintf("The update manifest returned HTTP %d.", response.StatusCode))
	}

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxManifestBytes+1))
	if err != nil || len(payload) > maxManifestBytes {
		return unavailable(result, "The update manifest could not be read within the size limit.")
	}
	if len(publicKeys) > 0 {
		if err := c.verifyManifestSignature(ctx, payload, publicKeys); err != nil {
			return unavailable(result, "Could not verify the update manifest signature.")
		}
		result.SignatureStatus = SignatureVerified
	}
	manifest, err := decodeManifest(payload)
	if err != nil {
		return unavailable(result, "The configured update manifest is invalid.")
	}
	artifact, ok := manifest.Artifacts[c.platform]
	if !ok {
		return unavailable(result, "The update manifest does not contain an artifact for this platform.")
	}

	state := releaseversion.Compare(manifest.Version, c.currentVersion)
	result.Status = StatusHealthy
	result.LatestVersion = manifest.Version
	result.LatestVersionState = state
	result.UpdateAvailable = state == releaseversion.Ahead
	result.Artifact = &artifact
	result.PublishedAt = manifest.PublishedAt
	result.ReleaseNotesURL = manifest.ReleaseNotesURL
	switch state {
	case releaseversion.Ahead:
		result.Message = "A newer HServer release is available."
	case releaseversion.Current:
		result.Message = "This HServer release is current."
	case releaseversion.Behind:
		result.Message = "This panel is newer than the configured release manifest."
	default:
		result.Message = "The running build cannot be ordered against stable releases."
	}
	return result
}

func (c *Checker) verifyManifestSignature(ctx context.Context, payload []byte, publicKeys []ed25519.PublicKey) error {
	signatureURL, err := detachedSignatureURL(c.manifestURL)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, signatureURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.hserver.ed25519-signature")
	request.Header.Set("User-Agent", "HServer-Panel/"+c.currentVersion)
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("signature returned HTTP %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxManifestSignatureBytes+1))
	if err != nil || len(encoded) > maxManifestSignatureBytes {
		return fmt.Errorf("signature exceeds size limit")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature encoding")
	}
	for _, publicKey := range publicKeys {
		if ed25519.Verify(publicKey, payload, signature) {
			return nil
		}
	}
	return fmt.Errorf("manifest signature verification failed")
}

func decodeManifestPublicKeys(encoded string) ([]ed25519.PublicKey, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	parts := strings.Split(encoded, ",")
	if len(parts) > maxManifestPublicKeys {
		return nil, fmt.Errorf("too many Ed25519 public keys")
	}
	keys := make([]ed25519.PublicKey, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		decoded, err := base64.StdEncoding.DecodeString(part)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid Ed25519 public key")
		}
		if _, exists := seen[part]; exists {
			return nil, fmt.Errorf("duplicate Ed25519 public key")
		}
		seen[part] = struct{}{}
		keys = append(keys, ed25519.PublicKey(decoded))
	}
	return keys, nil
}

func detachedSignatureURL(manifestURL string) (string, error) {
	parsed, err := url.ParseRequestURI(manifestURL)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid manifest URL")
	}
	parsed.Path += ".sig"
	return parsed.String(), nil
}

func decodeManifest(payload []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return manifest, fmt.Errorf("trailing manifest data")
	}
	if manifest.SchemaVersion != manifestSchemaVersion {
		return manifest, fmt.Errorf("unsupported manifest schema")
	}
	if releaseversion.Compare(manifest.Version, manifest.Version) != releaseversion.Current {
		return manifest, fmt.Errorf("manifest version is not a stable release")
	}
	if len(manifest.Artifacts) == 0 {
		return manifest, fmt.Errorf("manifest has no artifacts")
	}
	if manifest.ReleaseNotesURL != "" && !validHTTPURL(manifest.ReleaseNotesURL) {
		return manifest, fmt.Errorf("invalid release notes URL")
	}
	for platform, artifact := range manifest.Artifacts {
		if strings.TrimSpace(platform) == "" || !validHTTPURL(artifact.URL) || !validSHA256(artifact.SHA256) || artifact.SizeBytes < 0 {
			return manifest, fmt.Errorf("invalid artifact")
		}
	}
	return manifest, nil
}

func validHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func unavailable(result Result, message string) Result {
	result.Status = StatusUnavailable
	result.Message = message
	return result
}
