package config_test

import (
	"github.com/IamYGT/heyserver/internal/config"
	"log/slog"
	"os"
	"reflect"
	"testing"
)

const validSecret = "test-secret-32-bytes-for-testing!!"

func setValidSecret(t *testing.T) { t.Helper(); t.Setenv("HSERVER_JWT_SECRET", validSecret) }

func TestLoad_Defaults(t *testing.T) {
	setValidSecret(t)
	for _, k := range []string{"HSERVER_PORT", "HSERVER_DB_PATH", "HSERVER_LOG_LEVEL", "HSERVER_DATA_DIR", "HSERVER_ADMIN_EMAIL", "HSERVER_ADMIN_PASS", "HSERVER_MAIL_DNS_HOSTNAME", "HSERVER_MAIL_DNS_PUBLIC_IP", "HSERVER_MAIL_DNS_SPF", "HSERVER_MAIL_DNS_DMARC", "HSERVER_MAIL_DNS_MX_PRIORITY", "HSERVER_PM2_USER", "HSERVER_PM2_HOME", "HSERVER_PM2_BIN", "HSERVER_PM2_ALLOWED_ROOTS", "HSERVER_VHOSTS_ROOT", "HSERVER_PHP_CONFIG_ROOT", "HSERVER_PHP_BINARY_ROOT", "HSERVER_NGINX_SNIPPETS_DIR", "STALWART_URL", "STALWART_API_KEY", "STALWART_ADMIN_USER", "STALWART_ADMIN_PASS", "HSERVER_STALWART_SERVICE", "HSERVER_STALWART_CONFIG_PATH", "HSERVER_STALWART_BIN", "HSERVER_UPDATE_MANIFEST_URL", "HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS", "HSERVER_UPDATE_PANEL_BINARY_PATH", "HSERVER_UPDATE_CLI_BINARY_PATH", "HSERVER_S3_ENDPOINT", "HSERVER_S3_BUCKET", "HSERVER_S3_REGION", "HSERVER_S3_ACCESS_KEY_FILE", "HSERVER_S3_SECRET_KEY_FILE", "HSERVER_S3_BUCKET_LOOKUP"} {
		_ = os.Unsetenv(k)
	}
	cfg := config.Load()
	if cfg.Port != 3085 {
		t.Errorf("Port: got %d want 3085", cfg.Port)
	}
	if cfg.DBPath != "/var/lib/hserver/hserver.db" {
		t.Errorf("DBPath wrong")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel: got %v want info", cfg.LogLevel)
	}
	if cfg.DataDir != "/var/lib/hserver" {
		t.Errorf("DataDir wrong")
	}
	if cfg.AdminEmail != "admin@localhost" {
		t.Errorf("AdminEmail wrong")
	}
	if cfg.AdminPass != "" {
		t.Errorf("AdminPass should be empty")
	}
	if cfg.MailDNSHostname != "" || cfg.MailDNSPublicIP != "" || cfg.MailDNSSPFRecord != "" || cfg.MailDNSDMARCRecord != "" {
		t.Error("mail DNS installation values should default to empty")
	}
	if cfg.MailDNSMXPriority != 10 {
		t.Errorf("MailDNSMXPriority: got %d want 10", cfg.MailDNSMXPriority)
	}
	if cfg.PM2User != "" || cfg.PM2Home != "" {
		t.Error("PM2 owner and home should default to empty")
	}
	if cfg.PM2Bin != "pm2" {
		t.Errorf("PM2Bin: got %q want pm2", cfg.PM2Bin)
	}
	if len(cfg.PM2AllowedRoots) != 0 {
		t.Errorf("PM2AllowedRoots: got %#v want empty", cfg.PM2AllowedRoots)
	}
	if cfg.VhostsRoot != "" {
		t.Errorf("VhostsRoot: got %q want empty", cfg.VhostsRoot)
	}
	if cfg.PHPConfigRoot != "/etc/php" {
		t.Errorf("PHPConfigRoot: got %q want /etc/php", cfg.PHPConfigRoot)
	}
	if cfg.PHPBinaryRoot != "/usr/sbin" {
		t.Errorf("PHPBinaryRoot: got %q want /usr/sbin", cfg.PHPBinaryRoot)
	}
	if cfg.NginxSnippetsDir != "/etc/nginx/snippets" {
		t.Errorf("NginxSnippetsDir: got %q", cfg.NginxSnippetsDir)
	}
	if cfg.StalwartURL != "" || cfg.StalwartAdminUser != "" || cfg.StalwartService != "" || cfg.StalwartConfig != "" || cfg.StalwartBinary != "" {
		t.Errorf("Stalwart integration should default to not configured: URL=%q user=%q service=%q config=%q binary=%q", cfg.StalwartURL, cfg.StalwartAdminUser, cfg.StalwartService, cfg.StalwartConfig, cfg.StalwartBinary)
	}
	if cfg.UpdateManifestURL != "" {
		t.Errorf("UpdateManifestURL should default to empty, got %q", cfg.UpdateManifestURL)
	}
	if cfg.UpdateManifestPublicKeys != "" {
		t.Errorf("UpdateManifestPublicKeys should default to empty, got %q", cfg.UpdateManifestPublicKeys)
	}
	if cfg.UpdatePanelBinaryPath != "" || cfg.UpdateCLIBinaryPath != "" {
		t.Errorf("update binary paths should default to observed layout, got panel=%q CLI=%q", cfg.UpdatePanelBinaryPath, cfg.UpdateCLIBinaryPath)
	}
	if cfg.S3Endpoint != "" || cfg.S3Bucket != "" || cfg.S3Region != "" || cfg.S3AccessKeyFile != "" || cfg.S3SecretKeyFile != "" {
		t.Error("S3-compatible destination must default to not configured")
	}
	if cfg.S3BucketLookup != "auto" {
		t.Errorf("S3BucketLookup: got %q want auto", cfg.S3BucketLookup)
	}
}

func TestLoad_PM2AllowedRoots(t *testing.T) {
	setValidSecret(t)
	t.Setenv("HSERVER_VHOSTS_ROOT", "/srv/hserver/sites")
	t.Setenv("HSERVER_PM2_ALLOWED_ROOTS", " /srv/hserver/sites , /home/apps, ,/opt/services ")

	if got, want := config.Load().PM2AllowedRoots, []string{"/srv/hserver/sites", "/home/apps", "/opt/services"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PM2AllowedRoots: got %#v want %#v", got, want)
	}
	if got := config.Load().VhostsRoot; got != "/srv/hserver/sites" {
		t.Fatalf("VhostsRoot: got %q want /srv/hserver/sites", got)
	}
}

func TestLoad_PHPRootsUseConfiguredValueAndFallbackForEmpty(t *testing.T) {
	setValidSecret(t)
	t.Setenv("HSERVER_PHP_CONFIG_ROOT", "")
	t.Setenv("HSERVER_PHP_BINARY_ROOT", "php/bin")

	cfg := config.Load()
	if cfg.PHPConfigRoot != "/etc/php" {
		t.Fatalf("PHPConfigRoot: got %q want /etc/php for an empty environment value", cfg.PHPConfigRoot)
	}
	if cfg.PHPBinaryRoot != "php/bin" {
		t.Fatalf("PHPBinaryRoot: got %q want php/bin", cfg.PHPBinaryRoot)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	tests := []struct {
		name, key, val string
		check          func(*config.Config) any
		want           any
	}{
		{"port", "HSERVER_PORT", "9090", func(c *config.Config) any { return c.Port }, 9090},
		{"dbpath", "HSERVER_DB_PATH", "/tmp/x.db", func(c *config.Config) any { return c.DBPath }, "/tmp/x.db"},
		{"datadir", "HSERVER_DATA_DIR", "/tmp/data", func(c *config.Config) any { return c.DataDir }, "/tmp/data"},
		{"email", "HSERVER_ADMIN_EMAIL", "r@x.com", func(c *config.Config) any { return c.AdminEmail }, "r@x.com"},
		{"pass", "HSERVER_ADMIN_PASS", "s3cr3t", func(c *config.Config) any { return c.AdminPass }, "s3cr3t"},
		{"mail hostname", "HSERVER_MAIL_DNS_HOSTNAME", "mail.example.com", func(c *config.Config) any { return c.MailDNSHostname }, "mail.example.com"},
		{"mail public IP", "HSERVER_MAIL_DNS_PUBLIC_IP", "203.0.113.10", func(c *config.Config) any { return c.MailDNSPublicIP }, "203.0.113.10"},
		{"mail SPF", "HSERVER_MAIL_DNS_SPF", "v=spf1 mx -all", func(c *config.Config) any { return c.MailDNSSPFRecord }, "v=spf1 mx -all"},
		{"mail DMARC", "HSERVER_MAIL_DNS_DMARC", "v=DMARC1; p=none", func(c *config.Config) any { return c.MailDNSDMARCRecord }, "v=DMARC1; p=none"},
		{"mail MX priority", "HSERVER_MAIL_DNS_MX_PRIORITY", "20", func(c *config.Config) any { return c.MailDNSMXPriority }, 20},
		{"PM2 user", "HSERVER_PM2_USER", "app", func(c *config.Config) any { return c.PM2User }, "app"},
		{"PM2 home", "HSERVER_PM2_HOME", "/home/app/.pm2", func(c *config.Config) any { return c.PM2Home }, "/home/app/.pm2"},
		{"PM2 binary", "HSERVER_PM2_BIN", "/usr/local/bin/pm2", func(c *config.Config) any { return c.PM2Bin }, "/usr/local/bin/pm2"},
		{"vhosts root", "HSERVER_VHOSTS_ROOT", "/srv/hserver/sites", func(c *config.Config) any { return c.VhostsRoot }, "/srv/hserver/sites"},
		{"PHP config root", "HSERVER_PHP_CONFIG_ROOT", "/srv/php/config", func(c *config.Config) any { return c.PHPConfigRoot }, "/srv/php/config"},
		{"PHP binary root", "HSERVER_PHP_BINARY_ROOT", "/srv/php/bin", func(c *config.Config) any { return c.PHPBinaryRoot }, "/srv/php/bin"},
		{"Nginx snippets", "HSERVER_NGINX_SNIPPETS_DIR", "/srv/hserver/nginx/snippets", func(c *config.Config) any { return c.NginxSnippetsDir }, "/srv/hserver/nginx/snippets"},
		{"Stalwart URL", "STALWART_URL", "https://mail.example.com", func(c *config.Config) any { return c.StalwartURL }, "https://mail.example.com"},
		{"Stalwart admin user", "STALWART_ADMIN_USER", "mail-admin", func(c *config.Config) any { return c.StalwartAdminUser }, "mail-admin"},
		{"Stalwart service", "HSERVER_STALWART_SERVICE", "stalwart-mail", func(c *config.Config) any { return c.StalwartService }, "stalwart-mail"},
		{"Stalwart config", "HSERVER_STALWART_CONFIG_PATH", "/etc/stalwart/config.toml", func(c *config.Config) any { return c.StalwartConfig }, "/etc/stalwart/config.toml"},
		{"Stalwart binary", "HSERVER_STALWART_BIN", "/usr/bin/stalwart", func(c *config.Config) any { return c.StalwartBinary }, "/usr/bin/stalwart"},
		{"update manifest", "HSERVER_UPDATE_MANIFEST_URL", "https://releases.example.com/manifest.json", func(c *config.Config) any { return c.UpdateManifestURL }, "https://releases.example.com/manifest.json"},
		{"update manifest public keys", "HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", func(c *config.Config) any { return c.UpdateManifestPublicKeys }, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		{"update panel binary", "HSERVER_UPDATE_PANEL_BINARY_PATH", "/srv/hserver/bin/hserver-panel", func(c *config.Config) any { return c.UpdatePanelBinaryPath }, "/srv/hserver/bin/hserver-panel"},
		{"update CLI binary", "HSERVER_UPDATE_CLI_BINARY_PATH", "/srv/hserver/bin/hserverctl", func(c *config.Config) any { return c.UpdateCLIBinaryPath }, "/srv/hserver/bin/hserverctl"},
		{"S3 endpoint", "HSERVER_S3_ENDPOINT", "https://objects.example.com", func(c *config.Config) any { return c.S3Endpoint }, "https://objects.example.com"},
		{"S3 bucket", "HSERVER_S3_BUCKET", "hserver-backups", func(c *config.Config) any { return c.S3Bucket }, "hserver-backups"},
		{"S3 region", "HSERVER_S3_REGION", "eu-central-1", func(c *config.Config) any { return c.S3Region }, "eu-central-1"},
		{"S3 access key file", "HSERVER_S3_ACCESS_KEY_FILE", "/etc/hserver/secrets/s3-access-key", func(c *config.Config) any { return c.S3AccessKeyFile }, "/etc/hserver/secrets/s3-access-key"},
		{"S3 secret key file", "HSERVER_S3_SECRET_KEY_FILE", "/etc/hserver/secrets/s3-secret-key", func(c *config.Config) any { return c.S3SecretKeyFile }, "/etc/hserver/secrets/s3-secret-key"},
		{"S3 bucket lookup", "HSERVER_S3_BUCKET_LOOKUP", "path", func(c *config.Config) any { return c.S3BucketLookup }, "path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setValidSecret(t)
			t.Setenv(tc.key, tc.val)
			if got := tc.check(config.Load()); got != tc.want {
				t.Errorf("%s: got %v want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestLoad_JWTSecretOverride(t *testing.T) {
	secret := "custom-jwt-secret-exactly-32-bytes!"
	t.Setenv("HSERVER_JWT_SECRET", secret)
	cfg := config.Load()
	if cfg.JWTSecret != secret {
		t.Errorf("JWTSecret: got %q want %q", cfg.JWTSecret, secret)
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	setValidSecret(t)
	t.Setenv("HSERVER_PORT", "nope")
	if config.Load().Port != 3085 {
		t.Error("invalid port should fallback to 3085")
	}
}

func TestLoad_LogLevel(t *testing.T) {
	tests := []struct {
		name, input string
		want        slog.Level
	}{
		{"debug", "debug", slog.LevelDebug}, {"info", "info", slog.LevelInfo},
		{"warn", "warn", slog.LevelWarn}, {"error", "error", slog.LevelError},
		{"unknown", "verbose", slog.LevelInfo}, {"UPPER", "INFO", slog.LevelInfo},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setValidSecret(t)
			t.Setenv("HSERVER_LOG_LEVEL", tc.input)
			if got := config.Load().LogLevel; got != tc.want {
				t.Errorf("level %q: got %v want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	if config.Version == "" {
		t.Error("Version must not be empty")
	}
}
