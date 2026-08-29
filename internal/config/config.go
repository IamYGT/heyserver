package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Build-time variables — injected via -ldflags during compilation.
var (
	Version     = "dev"
	BuildCommit = "dev"
	BuildDate   = "unknown"
	ProjectURL  = ""
)

type Config struct {
	Port                int
	DBPath              string
	JWTSecret           string
	LogLevel            slog.Level
	DataDir             string
	AdminEmail          string
	AdminPass           string
	PM2User             string   // unprivileged PM2 owner (HSERVER_PM2_USER)
	PM2Home             string   // optional PM2_HOME override
	PM2Bin              string   // PM2 command name or absolute path
	PM2AllowedRoots     []string // installation-owned roots accepted for PM2 deploy paths
	VhostsRoot          string   // installation-owned domain root (HSERVER_VHOSTS_ROOT)
	PHPConfigRoot       string   // PHP-FPM configuration root (HSERVER_PHP_CONFIG_ROOT)
	PHPBinaryRoot       string   // PHP-FPM binary root (HSERVER_PHP_BINARY_ROOT)
	NginxSitesAvailable string   // native Nginx config root (HSERVER_NGINX_SITES_AVAILABLE)
	NginxSitesEnabled   string   // native Nginx enabled-site root (HSERVER_NGINX_SITES_ENABLED)
	NginxSnippetsDir    string   // HServer-managed Nginx include root (HSERVER_NGINX_SNIPPETS_DIR)
	CertbotBin          string   // Certbot command or absolute path (HSERVER_CERTBOT_BIN)
	CertbotConfigDir    string   // Certbot certificate state root (HSERVER_CERTBOT_CONFIG_DIR)
	ACMEWebroot         string   // HTTP-01 challenge webroot (HSERVER_ACME_WEBROOT)

	// Optional provider-neutral release discovery manifest.
	UpdateManifestURL        string // HSERVER_UPDATE_MANIFEST_URL
	UpdateManifestPublicKeys string // HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS
	UpdatePanelBinaryPath    string // HSERVER_UPDATE_PANEL_BINARY_PATH
	UpdateCLIBinaryPath      string // HSERVER_UPDATE_CLI_BINARY_PATH

	// Stalwart Mail Server
	StalwartURL       string // Stalwart management API base URL (optional)
	StalwartAPIKey    string // Bearer token for management API
	StalwartAdminUser string // optional fallback: admin username for Basic auth
	StalwartAdminPass string // fallback: admin password for Basic auth
	StalwartService   string // systemd unit name (HSERVER_STALWART_SERVICE)
	StalwartConfig    string // config path (HSERVER_STALWART_CONFIG_PATH)
	StalwartBinary    string // binary path or command (HSERVER_STALWART_BIN)

	// Cloudflare — supports both API Token (Bearer) and Global API Key modes.
	// If CF_API_EMAIL is set, Global API Key auth is used (X-Auth-Key + X-Auth-Email).
	// Otherwise, Bearer token auth is used.
	CloudflareAPIToken string // API Token or Global API Key (HSERVER_CF_API_TOKEN)
	CloudflareAPIEmail string // Account email for Global API Key auth (HSERVER_CF_API_EMAIL)
	DomainDNSOrigin    string // optional A/AAAA origin for domain provisioning
	DomainDNSProxied   bool   // whether provisioned address records use the Cloudflare proxy
	MailDNSHostname    string // MX target (HSERVER_MAIL_DNS_HOSTNAME)
	MailDNSPublicIP    string // optional A/AAAA and generated SPF address
	MailDNSSPFRecord   string // optional explicit SPF record
	MailDNSDMARCRecord string // optional explicit DMARC record
	MailDNSMXPriority  int

	// Google Drive offsite backup (OAuth + rclone)
	GDriveClientID     string // HSERVER_GDRIVE_CLIENT_ID
	GDriveClientSecret string // HSERVER_GDRIVE_CLIENT_SECRET
	GDriveRedirectURI  string // HSERVER_GDRIVE_REDIRECT_URI — public OAuth callback (panel)
	RcloneBin          string // HSERVER_RCLONE_BIN (default: rclone)
	CronSecret         string // HSERVER_CRON_SECRET — protects internal cron backup endpoint
	ResticPassword     string // HSERVER_RESTIC_PASSWORD — encrypts every incremental snapshot repository
	ResticBin          string // HSERVER_RESTIC_BIN (default: restic)

	// Optional S3-compatible destination for client-side encrypted restic snapshots.
	S3Endpoint      string // HSERVER_S3_ENDPOINT
	S3Bucket        string // HSERVER_S3_BUCKET
	S3Region        string // HSERVER_S3_REGION
	S3AccessKeyFile string // HSERVER_S3_ACCESS_KEY_FILE
	S3SecretKeyFile string // HSERVER_S3_SECRET_KEY_FILE
	S3BucketLookup  string // HSERVER_S3_BUCKET_LOOKUP (auto, dns, path)
}

// Load reads configuration from environment variables and applies security guards.
// The process will exit with code 1 if critical security requirements are not met.
func Load() *Config {
	secret := validateJWTSecret()

	return &Config{
		Port:                getEnvInt("HSERVER_PORT", 3085),
		DBPath:              getEnv("HSERVER_DB_PATH", "/var/lib/hserver/hserver.db"),
		JWTSecret:           secret,
		LogLevel:            getLogLevel(getEnv("HSERVER_LOG_LEVEL", "info")),
		DataDir:             getEnv("HSERVER_DATA_DIR", "/var/lib/hserver"),
		AdminEmail:          getEnv("HSERVER_ADMIN_EMAIL", "admin@localhost"),
		AdminPass:           getEnv("HSERVER_ADMIN_PASS", ""),
		PM2User:             getEnv("HSERVER_PM2_USER", ""),
		PM2Home:             getEnv("HSERVER_PM2_HOME", ""),
		PM2Bin:              getEnv("HSERVER_PM2_BIN", "pm2"),
		PM2AllowedRoots:     getEnvList("HSERVER_PM2_ALLOWED_ROOTS", nil),
		VhostsRoot:          getEnv("HSERVER_VHOSTS_ROOT", ""),
		PHPConfigRoot:       getEnv("HSERVER_PHP_CONFIG_ROOT", "/etc/php"),
		PHPBinaryRoot:       getEnv("HSERVER_PHP_BINARY_ROOT", "/usr/sbin"),
		NginxSitesAvailable: getEnv("HSERVER_NGINX_SITES_AVAILABLE", "/etc/nginx/sites-available"),
		NginxSitesEnabled:   getEnv("HSERVER_NGINX_SITES_ENABLED", "/etc/nginx/sites-enabled"),
		NginxSnippetsDir:    getEnv("HSERVER_NGINX_SNIPPETS_DIR", "/etc/nginx/snippets"),
		CertbotBin:          getEnv("HSERVER_CERTBOT_BIN", "certbot"),
		CertbotConfigDir:    getEnv("HSERVER_CERTBOT_CONFIG_DIR", "/etc/letsencrypt"),
		ACMEWebroot:         getEnv("HSERVER_ACME_WEBROOT", "/var/www/hserver-acme"),

		UpdateManifestURL:        getEnv("HSERVER_UPDATE_MANIFEST_URL", ""),
		UpdateManifestPublicKeys: getEnv("HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS", ""),
		UpdatePanelBinaryPath:    getEnv("HSERVER_UPDATE_PANEL_BINARY_PATH", ""),
		UpdateCLIBinaryPath:      getEnv("HSERVER_UPDATE_CLI_BINARY_PATH", ""),

		StalwartURL:       getEnv("STALWART_URL", ""),
		StalwartAPIKey:    getEnv("STALWART_API_KEY", ""),
		StalwartAdminUser: getEnv("STALWART_ADMIN_USER", ""),
		StalwartAdminPass: getEnv("STALWART_ADMIN_PASS", ""),
		StalwartService:   getEnv("HSERVER_STALWART_SERVICE", ""),
		StalwartConfig:    getEnv("HSERVER_STALWART_CONFIG_PATH", ""),
		StalwartBinary:    getEnv("HSERVER_STALWART_BIN", ""),

		CloudflareAPIToken: getEnv("HSERVER_CF_API_TOKEN", ""),
		CloudflareAPIEmail: getEnv("HSERVER_CF_API_EMAIL", ""),
		DomainDNSOrigin:    getEnv("HSERVER_DOMAIN_DNS_ORIGIN", ""),
		DomainDNSProxied:   getEnvBool("HSERVER_DOMAIN_DNS_PROXIED", false),
		MailDNSHostname:    getEnv("HSERVER_MAIL_DNS_HOSTNAME", ""),
		MailDNSPublicIP:    getEnv("HSERVER_MAIL_DNS_PUBLIC_IP", ""),
		MailDNSSPFRecord:   getEnv("HSERVER_MAIL_DNS_SPF", ""),
		MailDNSDMARCRecord: getEnv("HSERVER_MAIL_DNS_DMARC", ""),
		MailDNSMXPriority:  getEnvInt("HSERVER_MAIL_DNS_MX_PRIORITY", 10),

		GDriveClientID:     getEnv("HSERVER_GDRIVE_CLIENT_ID", ""),
		GDriveClientSecret: getEnv("HSERVER_GDRIVE_CLIENT_SECRET", ""),
		GDriveRedirectURI:  getEnv("HSERVER_GDRIVE_REDIRECT_URI", ""),
		RcloneBin:          getEnv("HSERVER_RCLONE_BIN", "rclone"),
		CronSecret:         getEnv("HSERVER_CRON_SECRET", ""),
		ResticPassword:     getEnv("HSERVER_RESTIC_PASSWORD", ""),
		ResticBin:          getEnv("HSERVER_RESTIC_BIN", "restic"),
		S3Endpoint:         getEnv("HSERVER_S3_ENDPOINT", ""),
		S3Bucket:           getEnv("HSERVER_S3_BUCKET", ""),
		S3Region:           getEnv("HSERVER_S3_REGION", ""),
		S3AccessKeyFile:    getEnv("HSERVER_S3_ACCESS_KEY_FILE", ""),
		S3SecretKeyFile:    getEnv("HSERVER_S3_SECRET_KEY_FILE", ""),
		S3BucketLookup:     getEnv("HSERVER_S3_BUCKET_LOOKUP", "auto"),
	}
}

// validateJWTSecret enforces that HSERVER_JWT_SECRET is set to a non-default,
// sufficiently long random value. If the check fails the process exits immediately
// to prevent starting with a known/weak secret (C-3 fix).
func validateJWTSecret() string {
	const minLength = 32
	secret := os.Getenv("HSERVER_JWT_SECRET")

	if secret == "" || secret == "change-me-in-production" {
		fmt.Fprintln(os.Stderr,
			"FATAL: HSERVER_JWT_SECRET is not set or uses the insecure default value.\n"+
				"       Generate a secret with: openssl rand -hex 32\n"+
				"       Then set it in your environment or .env file.")
		os.Exit(1)
	}
	if len(secret) < minLength {
		fmt.Fprintf(os.Stderr,
			"FATAL: HSERVER_JWT_SECRET is too short (%d bytes). Minimum required: %d bytes.\n"+
				"       Generate a stronger secret with: openssl rand -hex 32\n",
			len(secret), minLength)
		os.Exit(1)
	}
	return secret
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvList(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return append([]string(nil), fallback...)
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func getLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
