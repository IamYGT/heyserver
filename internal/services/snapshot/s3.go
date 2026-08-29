package snapshot

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxS3CredentialBytes = 16 * 1024

var (
	s3BucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	s3RegionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type s3Credentials struct {
	accessKey string
	secretKey string
}

func (c S3Config) normalized() S3Config {
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	c.Bucket = strings.TrimSpace(c.Bucket)
	c.Region = strings.TrimSpace(c.Region)
	c.AccessKeyFile = strings.TrimSpace(c.AccessKeyFile)
	c.SecretKeyFile = strings.TrimSpace(c.SecretKeyFile)
	c.BucketLookup = strings.ToLower(strings.TrimSpace(c.BucketLookup))
	if c.BucketLookup == "" {
		c.BucketLookup = "auto"
	}
	return c
}

func (c S3Config) configured() bool {
	c = c.normalized()
	return c.Endpoint != "" || c.Bucket != "" || c.AccessKeyFile != "" || c.SecretKeyFile != ""
}

func (c S3Config) validate() error {
	c = c.normalized()
	if err := c.validateMetadata(); err != nil {
		return err
	}
	if _, err := readProtectedCredential(c.AccessKeyFile, "HSERVER_S3_ACCESS_KEY_FILE"); err != nil {
		return err
	}
	if _, err := readProtectedCredential(c.SecretKeyFile, "HSERVER_S3_SECRET_KEY_FILE"); err != nil {
		return err
	}
	return nil
}

func (c S3Config) validateMetadata() error {
	var missing []string
	if c.Endpoint == "" {
		missing = append(missing, "HSERVER_S3_ENDPOINT")
	}
	if c.Bucket == "" {
		missing = append(missing, "HSERVER_S3_BUCKET")
	}
	if c.AccessKeyFile == "" {
		missing = append(missing, "HSERVER_S3_ACCESS_KEY_FILE")
	}
	if c.SecretKeyFile == "" {
		missing = append(missing, "HSERVER_S3_SECRET_KEY_FILE")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing installation configuration: %s", strings.Join(missing, ", "))
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("HSERVER_S3_ENDPOINT must be an absolute endpoint URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("HSERVER_S3_ENDPOINT must not contain userinfo, query, or fragment")
	}
	if u.Scheme != "https" {
		if u.Scheme != "http" || !isLoopbackS3Host(u.Hostname()) {
			return fmt.Errorf("HSERVER_S3_ENDPOINT requires HTTPS; HTTP is accepted only for loopback endpoints")
		}
	}
	if !s3BucketPattern.MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") ||
		strings.Contains(c.Bucket, ".-") || strings.Contains(c.Bucket, "-.") || net.ParseIP(c.Bucket) != nil {
		return fmt.Errorf("HSERVER_S3_BUCKET must be a portable 3-63 character S3 bucket name")
	}
	if c.Region != "" && !s3RegionPattern.MatchString(c.Region) {
		return fmt.Errorf("HSERVER_S3_REGION contains unsupported characters")
	}
	switch c.BucketLookup {
	case "auto", "dns", "path":
	default:
		return fmt.Errorf("HSERVER_S3_BUCKET_LOOKUP must be auto, dns, or path")
	}
	return nil
}

func (c S3Config) credentials() (s3Credentials, error) {
	c = c.normalized()
	if err := c.validate(); err != nil {
		return s3Credentials{}, err
	}
	accessKey, err := readProtectedCredential(c.AccessKeyFile, "HSERVER_S3_ACCESS_KEY_FILE")
	if err != nil {
		return s3Credentials{}, err
	}
	secretKey, err := readProtectedCredential(c.SecretKeyFile, "HSERVER_S3_SECRET_KEY_FILE")
	if err != nil {
		return s3Credentials{}, err
	}
	return s3Credentials{accessKey: accessKey, secretKey: secretKey}, nil
}

// readinessCredentials reads the same installation-owned S3 credential file
// references as credentials(), but routes both reads through the readiness
// context/file seam. It is used only by the non-mutating readiness probe.
func (c S3Config) readinessCredentials(ctx context.Context, reader snapshotReadinessFileReader) (s3Credentials, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c = c.normalized()
	if err := c.validateMetadata(); err != nil {
		return s3Credentials{}, err
	}
	accessKey, err := readProtectedCredentialContext(ctx, reader, c.AccessKeyFile, "HSERVER_S3_ACCESS_KEY_FILE")
	if err != nil {
		return s3Credentials{}, err
	}
	secretKey, err := readProtectedCredentialContext(ctx, reader, c.SecretKeyFile, "HSERVER_S3_SECRET_KEY_FILE")
	if err != nil {
		return s3Credentials{}, err
	}
	return s3Credentials{accessKey: accessKey, secretKey: secretKey}, nil
}

func (c S3Config) repository(repoFolder string) string {
	c = c.normalized()
	return "s3:" + c.Endpoint + "/" + c.Bucket + "/" + repoFolder
}

func isLoopbackS3Host(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func readProtectedCredential(name, setting string) (string, error) {
	if !filepath.IsAbs(name) {
		return "", fmt.Errorf("%s must reference an absolute credential file", setting)
	}
	info, err := os.Lstat(name)
	if err != nil {
		return "", fmt.Errorf("%s cannot be read: %w", setting, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must reference a regular file, not a symlink", setting)
	}
	if info.Mode().Perm()&0o400 == 0 {
		return "", fmt.Errorf("%s must be readable by the owner", setting)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s must not be accessible by group or other users", setting)
	}
	if info.Size() <= 0 || info.Size() > maxS3CredentialBytes {
		return "", fmt.Errorf("%s credential file size is invalid", setting)
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("%s cannot be read: %w", setting, err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("%s must contain one non-empty credential line", setting)
	}
	return value, nil
}

func readProtectedCredentialContext(ctx context.Context, reader snapshotReadinessFileReader, name, setting string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(name) {
		return "", fmt.Errorf("%s must reference an absolute credential file", setting)
	}
	if reader == nil {
		reader = osSnapshotReadinessFileReader{}
	}
	info, err := reader.Lstat(ctx, name)
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}
	if err != nil {
		return "", fmt.Errorf("%s cannot be read: %w", setting, err)
	}
	if info == nil {
		return "", fmt.Errorf("%s cannot be read", setting)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must reference a regular file, not a symlink", setting)
	}
	if info.Mode().Perm()&0o400 == 0 {
		return "", fmt.Errorf("%s must be readable by the owner", setting)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s must not be accessible by group or other users", setting)
	}
	if info.Size() <= 0 || info.Size() > maxS3CredentialBytes {
		return "", fmt.Errorf("%s credential file size is invalid", setting)
	}
	raw, err := reader.ReadFile(ctx, name, maxS3CredentialBytes)
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}
	if err != nil {
		return "", fmt.Errorf("%s cannot be read: %w", setting, err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("%s must contain one non-empty credential line", setting)
	}
	return value, nil
}
