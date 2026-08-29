package releaseupdates

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/releaseversion"
)

const testSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestCheckDistinguishesNotConfigured(t *testing.T) {
	checker := newChecker("", "1.0.0", "linux_amd64", nil)
	result := checker.Check(context.Background())

	if result.Status != StatusNotConfigured || result.UpdateAvailable || result.Platform != "linux_amd64" {
		t.Fatalf("result = %#v", result)
	}
}

func TestCheckReturnsChecksumBoundArtifactForNewerRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "HServer-Panel/1.2.3" {
			t.Errorf("User-Agent = %q", got)
		}
		fmt.Fprint(w, `{
			"schema_version":1,
			"version":"v1.3.0",
			"published_at":"2026-08-26T18:00:00Z",
			"release_notes_url":"https://releases.example.com/v1.3.0",
			"artifacts":{"linux_amd64":{"url":"https://releases.example.com/hserver-linux-amd64.tar.gz","sha256":"`+testSHA256+`","size_bytes":1234}}
		}`)
	}))
	defer server.Close()

	checker := newChecker(server.URL, "1.2.3", "linux_amd64", server.Client())
	checker.now = func() time.Time { return time.Date(2026, 8, 26, 18, 5, 0, 0, time.UTC) }
	result := checker.Check(context.Background())

	if result.Status != StatusHealthy || !result.UpdateAvailable || result.LatestVersionState != releaseversion.Ahead {
		t.Fatalf("result = %#v", result)
	}
	if result.Artifact == nil || result.Artifact.SHA256 != testSHA256 || result.Artifact.SizeBytes != 1234 {
		t.Fatalf("artifact = %#v", result.Artifact)
	}
	if result.CheckedAt.Format(time.RFC3339) != "2026-08-26T18:05:00Z" {
		t.Fatalf("CheckedAt = %s", result.CheckedAt)
	}
}

func TestCheckVerifiesDetachedEd25519ManifestSignature(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	manifest := []byte(`{"schema_version":1,"version":"v1.3.0","artifacts":{"linux_amd64":{"url":"https://releases.example.com/archive.tar.gz","sha256":"` + testSHA256 + `"}}}`)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifest))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release-manifest.json":
			_, _ = w.Write(manifest)
		case "/release-manifest.json.sig":
			if got := r.Header.Get("Accept"); got != "application/vnd.hserver.ed25519-signature" {
				t.Errorf("signature Accept = %q", got)
			}
			fmt.Fprintln(w, signature)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	checker := newChecker(server.URL+"/release-manifest.json?channel=stable", "1.2.3", "linux_amd64", server.Client())
	WithManifestPublicKeys(base64.StdEncoding.EncodeToString(publicKey))(checker)
	result := checker.Check(context.Background())

	if result.Status != StatusHealthy || result.SignatureStatus != SignatureVerified || !result.UpdateAvailable {
		t.Fatalf("result = %#v", result)
	}
}

func TestCheckRejectsInvalidOrMissingConfiguredManifestSignature(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	manifest := []byte(`{"schema_version":1,"version":"v1.3.0","artifacts":{"linux_amd64":{"url":"https://releases.example.com/archive.tar.gz","sha256":"` + testSHA256 + `"}}}`)

	tests := []struct {
		name          string
		publicKey     string
		signatureCode int
		signature     string
	}{
		{name: "malformed public key", publicKey: "not-base64", signatureCode: http.StatusOK},
		{name: "missing signature", publicKey: base64.StdEncoding.EncodeToString(publicKey), signatureCode: http.StatusNotFound},
		{name: "wrong signature", publicKey: base64.StdEncoding.EncodeToString(publicKey), signatureCode: http.StatusOK, signature: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".sig") {
					w.WriteHeader(test.signatureCode)
					fmt.Fprintln(w, test.signature)
					return
				}
				_, _ = w.Write(manifest)
			}))
			defer server.Close()

			checker := newChecker(server.URL+"/release-manifest.json", "1.2.3", "linux_amd64", server.Client())
			WithManifestPublicKeys(test.publicKey)(checker)
			result := checker.Check(context.Background())
			if result.Status != StatusUnavailable || result.SignatureStatus != SignatureUnavailable || result.UpdateAvailable || result.Artifact != nil {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestCheckSupportsOverlappingManifestKeysDuringRotation(t *testing.T) {
	oldPrivate := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	newSeed := make([]byte, ed25519.SeedSize)
	newSeed[0] = 1
	newPrivate := ed25519.NewKeyFromSeed(newSeed)
	manifest := []byte(`{"schema_version":1,"version":"v1.3.0","artifacts":{"linux_amd64":{"url":"https://releases.example.com/archive.tar.gz","sha256":"` + testSHA256 + `"}}}`)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(newPrivate, manifest))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sig") {
			fmt.Fprintln(w, signature)
			return
		}
		_, _ = w.Write(manifest)
	}))
	defer server.Close()

	keys := base64.StdEncoding.EncodeToString(oldPrivate.Public().(ed25519.PublicKey)) + "," + base64.StdEncoding.EncodeToString(newPrivate.Public().(ed25519.PublicKey))
	checker := newChecker(server.URL+"/release-manifest.json", "1.2.3", "linux_amd64", server.Client())
	WithManifestPublicKeys(keys)(checker)
	result := checker.Check(context.Background())
	if result.Status != StatusHealthy || result.SignatureStatus != SignatureVerified {
		t.Fatalf("result = %#v", result)
	}
}

func TestCheckKeepsDevelopmentBuildOrderingUnknown(t *testing.T) {
	server := manifestServer(t, "1.3.0", "linux_amd64", http.StatusOK)
	defer server.Close()

	result := newChecker(server.URL, "dev", "linux_amd64", server.Client()).Check(context.Background())
	if result.Status != StatusHealthy || result.UpdateAvailable || result.LatestVersionState != releaseversion.Unknown {
		t.Fatalf("result = %#v", result)
	}
}

func TestCheckReportsUnavailableWithoutLeakingConfiguredURL(t *testing.T) {
	tests := []struct {
		name        string
		manifest    string
		platform    string
		statusCode  int
		wantMessage string
	}{
		{name: "HTTP failure", manifest: "1.3.0", platform: "linux_amd64", statusCode: http.StatusServiceUnavailable, wantMessage: "HTTP 503"},
		{name: "missing platform", manifest: "1.3.0", platform: "linux_arm64", statusCode: http.StatusOK, wantMessage: "does not contain an artifact"},
		{name: "unstable manifest version", manifest: "1.3.0-rc.1", platform: "linux_amd64", statusCode: http.StatusOK, wantMessage: "manifest is invalid"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := manifestServer(t, test.manifest, "linux_amd64", test.statusCode)
			defer server.Close()
			result := newChecker(server.URL+"?token=must-not-leak", "1.2.3", test.platform, server.Client()).Check(context.Background())
			if result.Status != StatusUnavailable || !strings.Contains(result.Message, test.wantMessage) {
				t.Fatalf("result = %#v", result)
			}
			if strings.Contains(result.Message, "must-not-leak") {
				t.Fatalf("message leaked configured URL: %q", result.Message)
			}
		})
	}
}

func manifestServer(t *testing.T, version, platform string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(statusCode)
		if statusCode == http.StatusOK {
			fmt.Fprintf(w, `{"schema_version":1,"version":%q,"artifacts":{%q:{"url":"https://releases.example.com/archive.tar.gz","sha256":%q}}}`, version, platform, testSHA256)
		}
	}))
}
