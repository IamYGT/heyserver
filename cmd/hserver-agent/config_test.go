package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"HSERVER_AGENT_HUB_URL":                      "https://10.0.0.1:8443/base/",
		"HSERVER_AGENT_NODE_ID":                      "contabo-1",
		"HSERVER_AGENT_TOKEN_FILE":                   "/run/secrets/hserver-agent-token",
		"HSERVER_AGENT_INTERVAL":                     "45s",
		"HSERVER_AGENT_OBSERVED_SERVICES":            "nginx.service, ssh.service,nginx.service",
		"HSERVER_AGENT_ALLOWED_SERVICES":             "nginx.service",
		"HSERVER_AGENT_ALLOWED_HOST_ACTIONS":         "memory-optimize,swap-reset,reboot",
		"HSERVER_AGENT_ALLOW_PROCESS_SIGNALS":        "true",
		"HSERVER_AGENT_ALLOW_TERMINAL":               "true",
		"HSERVER_AGENT_ALLOWED_DISK_CLEANUP":         "apt-cache,journal",
		"HSERVER_AGENT_ALLOWED_LOG_SOURCES":          "system,nginx,php",
		"HSERVER_AGENT_ALLOW_CONTAINER_READ":         "true",
		"HSERVER_AGENT_ALLOWED_CONTAINER_ACTIONS":    "start,restart",
		"HSERVER_AGENT_ALLOWED_NGINX_ACTIONS":        "test,reload",
		"HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ":      "true",
		"HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE":     "true",
		"HSERVER_AGENT_NGINX_SITES_AVAILABLE":        "/srv/nginx/available",
		"HSERVER_AGENT_NGINX_SITES_ENABLED":          "/srv/nginx/enabled",
		"HSERVER_AGENT_ALLOW_DOMAIN_READ":            "true",
		"HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS":         "true",
		"HSERVER_AGENT_ALLOW_SSL_READ":               "true",
		"HSERVER_AGENT_ALLOW_SSL_ACTIONS":            "true",
		"HSERVER_AGENT_CERTBOT_CONFIG_DIR":           "/srv/letsencrypt/config",
		"HSERVER_AGENT_CERTBOT_WORK_DIR":             "/srv/letsencrypt/work",
		"HSERVER_AGENT_CERTBOT_LOGS_DIR":             "/srv/letsencrypt/logs",
		"HSERVER_AGENT_CERTBOT_BINARY":               "/opt/certbot/bin/certbot",
		"HSERVER_AGENT_OPENSSL_BINARY":               "/opt/openssl/bin/openssl",
		"HSERVER_AGENT_CA_BUNDLE":                    "/srv/ssl/ca-bundle.crt",
		"HSERVER_AGENT_ALLOW_DATABASE_READ":          "true",
		"HSERVER_AGENT_ALLOWED_DATABASE_RESTARTS":    "mariadb,postgresql",
		"HSERVER_AGENT_MARIADB_BINARY":               "/opt/database/mariadb",
		"HSERVER_AGENT_MARIADB_ADMIN_BINARY":         "/opt/database/mariadb-admin",
		"HSERVER_AGENT_PG_LSCLUSTERS_BINARY":         "/opt/database/pg_lsclusters",
		"HSERVER_AGENT_PSQL_BINARY":                  "/opt/database/psql",
		"HSERVER_AGENT_PG_ISREADY_BINARY":            "/opt/database/pg_isready",
		"HSERVER_AGENT_ALLOW_BACKUP_READ":            "true",
		"HSERVER_AGENT_ALLOW_BACKUP_RUN":             "true",
		"HSERVER_AGENT_BACKUP_PLANS_FILE":            "/srv/hserver/backup-plans.json",
		"HSERVER_AGENT_ALLOW_DEPLOY_READ":            "true",
		"HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS":         "true",
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ":     "true",
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS":  "true",
		"HSERVER_AGENT_DEPLOY_PLANS_FILE":            "/srv/hserver/deploy-plans.json",
		"HSERVER_AGENT_DEPLOY_ACME_WEBROOT":          "/srv/hserver/acme",
		"HSERVER_AGENT_DEPLOY_WRITE_ROOTS":           "/srv/releases,/var/lib/example",
		"HSERVER_AGENT_ALLOW_UPDATE_READ":            "true",
		"HSERVER_AGENT_ALLOW_UPDATE_ACTIONS":         "true",
		"HSERVER_AGENT_UPDATE_MANIFEST_URL":          "https://releases.example.com/latest/agent.json",
		"HSERVER_AGENT_UPDATE_MANIFEST_PUBLIC_KEYS":  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HSERVER_AGENT_STATE_DIR":                    "/srv/hserver/agent-state",
		"HSERVER_AGENT_LIFECYCLE_INSTALLER":          "/opt/hserver/bin/agent-install",
		"HSERVER_AGENT_SYSTEMD_RUN_BINARY":           "/opt/systemd/bin/systemd-run",
		"HSERVER_AGENT_SYSTEMCTL_BINARY":             "/opt/systemd/bin/systemctl",
		"HSERVER_AGENT_FILE_READ_ROOTS":              "/srv/apps,/var/log/apps,/srv/apps",
		"HSERVER_AGENT_FILE_WRITE_ROOTS":             "/srv/apps",
		"HSERVER_AGENT_ALLOWED_PHP_ACTIONS":          "test,reload,restart",
		"HSERVER_AGENT_ALLOW_PHP_CONFIG_READ":        "true",
		"HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE":       "true",
		"HSERVER_AGENT_PHP_CONFIG_ROOT":              "/srv/php/config",
		"HSERVER_AGENT_PHP_BINARY_ROOT":              "/srv/php/bin",
		"HSERVER_AGENT_ALLOW_PM2_READ":               "true",
		"HSERVER_AGENT_ALLOWED_PM2_ACTIONS":          "start,restart,reload,stop",
		"HSERVER_AGENT_PM2_BINARY":                   "/opt/pm2/bin/pm2",
		"HSERVER_AGENT_PM2_HOME":                     "/srv/pm2/home",
		"HSERVER_AGENT_PM2_USER":                     "deploy",
		"HSERVER_AGENT_ALLOW_CRON_READ":              "true",
		"HSERVER_AGENT_ALLOW_CRON_WRITE":             "true",
		"HSERVER_AGENT_ALLOW_CRON_RUN":               "true",
		"HSERVER_AGENT_CRON_STATE_PATH":              "/srv/hserver/cron.json",
		"HSERVER_AGENT_CRON_FILE_PATH":               "/srv/cron/hserver-managed",
		"HSERVER_AGENT_CRON_LOCK_PATH":               "/run/lock/hserver-cron.lock",
		"HSERVER_AGENT_CRONTAB_BINARY":               "/opt/cron/bin/crontab",
		"HSERVER_AGENT_RUNUSER_BINARY":               "/opt/util/bin/runuser",
		"HSERVER_AGENT_CRON_SHELL":                   "/bin/sh",
		"HSERVER_AGENT_CRON_SERVICE":                 "crond.service",
		"HSERVER_AGENT_ALLOW_FIREWALL_READ":          "true",
		"HSERVER_AGENT_ALLOW_FIREWALL_WRITE":         "true",
		"HSERVER_AGENT_IPTABLES_BINARY":              "/opt/firewall/iptables",
		"HSERVER_AGENT_FIREWALL_SAVE_BINARY":         "/opt/firewall/save",
		"HSERVER_AGENT_FIREWALL_LOCK_PATH":           "/run/lock/custom-firewall.lock",
		"HSERVER_AGENT_FIREWALL_PERSISTENCE_SERVICE": "firewall-save.service",
		"HSERVER_AGENT_FIREWALL_PERSISTENCE_PATH":    "/srv/firewall/state",
		"HSERVER_AGENT_FIREWALL_PROTECTED_SOURCES":   "192.0.2.10,198.51.100.0/24,192.0.2.10/32",
		"HSERVER_AGENT_FIREWALL_PROTECTED_PORTS":     "22,443,22",
	}
	lookup := func(key string) (string, bool) { value, ok := env[key]; return value, ok }
	readFile := func(path string) ([]byte, error) {
		if path != "/run/secrets/hserver-agent-token" {
			t.Fatalf("unexpected token path %q", path)
		}
		return []byte("top-secret-token\n"), nil
	}
	cfg, err := loadConfigFromEnv(lookup, readFile)
	if err != nil {
		t.Fatalf("loadConfigFromEnv() error = %v", err)
	}
	if cfg.hubURL.String() != "https://10.0.0.1:8443/base" || cfg.nodeID != "contabo-1" || cfg.token != "top-secret-token" || cfg.interval != 45*time.Second {
		t.Fatalf("unexpected config: URL=%s node=%s interval=%s token_length=%d", cfg.hubURL, cfg.nodeID, cfg.interval, len(cfg.token))
	}
	if !reflect.DeepEqual(cfg.observedServices, []string{"nginx.service", "ssh.service"}) {
		t.Fatalf("observed services = %#v", cfg.observedServices)
	}
	if _, ok := cfg.allowedServices["nginx.service"]; !ok {
		t.Fatal("nginx.service missing from action allowlist")
	}
	if _, ok := cfg.allowedHostActions["swap-reset"]; !ok || len(cfg.allowedHostActions) != 3 {
		t.Fatalf("host action allowlist = %#v", cfg.allowedHostActions)
	}
	if !cfg.allowProcessSignals {
		t.Fatal("process signals were not enabled")
	}
	if !cfg.allowTerminal {
		t.Fatal("terminal was not enabled")
	}
	if len(cfg.allowedDiskCleanup) != 2 {
		t.Fatalf("disk cleanup allowlist = %#v", cfg.allowedDiskCleanup)
	}
	if len(cfg.allowedLogSources) != 3 {
		t.Fatalf("log source allowlist = %#v", cfg.allowedLogSources)
	}
	if !cfg.allowContainerRead || len(cfg.allowedContainerActions) != 2 {
		t.Fatalf("container controls: read=%t actions=%#v", cfg.allowContainerRead, cfg.allowedContainerActions)
	}
	if len(cfg.allowedNginxActions) != 2 {
		t.Fatalf("Nginx action allowlist = %#v", cfg.allowedNginxActions)
	}
	if !cfg.allowNginxConfigRead || !cfg.allowNginxConfigWrite || cfg.nginxSitesAvailable != "/srv/nginx/available" || cfg.nginxSitesEnabled != "/srv/nginx/enabled" {
		t.Fatalf("Nginx config controls: read=%t write=%t available=%q enabled=%q", cfg.allowNginxConfigRead, cfg.allowNginxConfigWrite, cfg.nginxSitesAvailable, cfg.nginxSitesEnabled)
	}
	if !cfg.allowDomainRead || !cfg.allowDomainActions {
		t.Fatalf("domain controls: read=%t actions=%t", cfg.allowDomainRead, cfg.allowDomainActions)
	}
	if !cfg.allowSSLRead || !cfg.allowSSLActions || cfg.certificateConfigDir != "/srv/letsencrypt/config" || cfg.certificateWorkDir != "/srv/letsencrypt/work" || cfg.certificateLogsDir != "/srv/letsencrypt/logs" || cfg.certbotBinary != "/opt/certbot/bin/certbot" || cfg.openSSLBinary != "/opt/openssl/bin/openssl" || cfg.caBundle != "/srv/ssl/ca-bundle.crt" {
		t.Fatalf("SSL controls: read=%t actions=%t config=%q work=%q logs=%q certbot=%q openssl=%q ca=%q", cfg.allowSSLRead, cfg.allowSSLActions, cfg.certificateConfigDir, cfg.certificateWorkDir, cfg.certificateLogsDir, cfg.certbotBinary, cfg.openSSLBinary, cfg.caBundle)
	}
	if !cfg.allowDatabaseRead || len(cfg.allowedDatabaseRestarts) != 2 || cfg.mariaDBBinary != "/opt/database/mariadb" || cfg.mariaDBAdminBinary != "/opt/database/mariadb-admin" || cfg.pgClustersBinary != "/opt/database/pg_lsclusters" || cfg.psqlBinary != "/opt/database/psql" || cfg.pgIsReadyBinary != "/opt/database/pg_isready" {
		t.Fatalf("database controls: read=%t restarts=%#v mariadb=%q admin=%q clusters=%q psql=%q ready=%q", cfg.allowDatabaseRead, cfg.allowedDatabaseRestarts, cfg.mariaDBBinary, cfg.mariaDBAdminBinary, cfg.pgClustersBinary, cfg.psqlBinary, cfg.pgIsReadyBinary)
	}
	if !cfg.allowBackupRead || !cfg.allowBackupRun || cfg.backupPlansPath != "/srv/hserver/backup-plans.json" {
		t.Fatalf("backup controls: read=%t run=%t plans=%q", cfg.allowBackupRead, cfg.allowBackupRun, cfg.backupPlansPath)
	}
	if !cfg.allowDeployRead || !cfg.allowDeployActions || !cfg.allowDeployDomainRead || !cfg.allowDeployDomainActions || cfg.deployPlansPath != "/srv/hserver/deploy-plans.json" || cfg.deployACMEWebroot != "/srv/hserver/acme" || !reflect.DeepEqual(cfg.deployWriteRoots, []string{"/srv/releases", "/var/lib/example"}) {
		t.Fatalf("deploy controls: read=%t actions=%t domain_read=%t domain_actions=%t plans=%q acme=%q roots=%#v", cfg.allowDeployRead, cfg.allowDeployActions, cfg.allowDeployDomainRead, cfg.allowDeployDomainActions, cfg.deployPlansPath, cfg.deployACMEWebroot, cfg.deployWriteRoots)
	}
	if !cfg.allowAgentUpdateRead || !cfg.allowAgentUpdateActions || cfg.agentUpdateManifestURL != "https://releases.example.com/latest/agent.json" || cfg.agentUpdatePublicKeys != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" || cfg.agentStateDir != "/srv/hserver/agent-state" || cfg.agentLifecycleInstaller != "/opt/hserver/bin/agent-install" || cfg.systemdRunBinary != "/opt/systemd/bin/systemd-run" || cfg.systemctlBinary != "/opt/systemd/bin/systemctl" {
		t.Fatalf("agent update controls: read=%t actions=%t manifest=%q public_keys=%q state=%q installer=%q systemd_run=%q systemctl=%q", cfg.allowAgentUpdateRead, cfg.allowAgentUpdateActions, cfg.agentUpdateManifestURL, cfg.agentUpdatePublicKeys, cfg.agentStateDir, cfg.agentLifecycleInstaller, cfg.systemdRunBinary, cfg.systemctlBinary)
	}
	if !reflect.DeepEqual(cfg.fileReadRoots, []string{"/srv/apps", "/var/log/apps"}) || !reflect.DeepEqual(cfg.fileWriteRoots, []string{"/srv/apps"}) {
		t.Fatalf("file roots: read=%#v write=%#v", cfg.fileReadRoots, cfg.fileWriteRoots)
	}
	if len(cfg.allowedPHPActions) != 3 || !cfg.allowPHPConfigRead || !cfg.allowPHPConfigWrite || cfg.phpConfigRoot != "/srv/php/config" || cfg.phpBinaryRoot != "/srv/php/bin" {
		t.Fatalf("PHP-FPM controls: actions=%#v read=%t write=%t config=%q binary=%q", cfg.allowedPHPActions, cfg.allowPHPConfigRead, cfg.allowPHPConfigWrite, cfg.phpConfigRoot, cfg.phpBinaryRoot)
	}
	if !cfg.allowPM2Read || len(cfg.allowedPM2Actions) != 4 || cfg.pm2Binary != "/opt/pm2/bin/pm2" || cfg.pm2Home != "/srv/pm2/home" || cfg.pm2User != "deploy" {
		t.Fatalf("PM2 controls: read=%t actions=%#v binary=%q home=%q user=%q", cfg.allowPM2Read, cfg.allowedPM2Actions, cfg.pm2Binary, cfg.pm2Home, cfg.pm2User)
	}
	if !cfg.allowCronRead || !cfg.allowCronWrite || !cfg.allowCronRun || cfg.cronStatePath != "/srv/hserver/cron.json" || cfg.cronFilePath != "/srv/cron/hserver-managed" || cfg.crontabBinary != "/opt/cron/bin/crontab" || cfg.cronService != "crond.service" {
		t.Fatalf("cron controls: read=%t write=%t run=%t state=%q file=%q binary=%q service=%q", cfg.allowCronRead, cfg.allowCronWrite, cfg.allowCronRun, cfg.cronStatePath, cfg.cronFilePath, cfg.crontabBinary, cfg.cronService)
	}
	if !cfg.allowFirewallRead || !cfg.allowFirewallWrite || cfg.iptablesBinary != "/opt/firewall/iptables" || cfg.firewallSaveBinary != "/opt/firewall/save" || cfg.firewallService != "firewall-save.service" || cfg.firewallPersistencePath != "/srv/firewall/state" || !reflect.DeepEqual(cfg.firewallProtectedSources, []string{"192.0.2.10/32", "198.51.100.0/24"}) || len(cfg.firewallProtectedPorts) != 2 {
		t.Fatalf("firewall controls: read=%t write=%t iptables=%q save=%q service=%q sources=%#v ports=%#v", cfg.allowFirewallRead, cfg.allowFirewallWrite, cfg.iptablesBinary, cfg.firewallSaveBinary, cfg.firewallService, cfg.firewallProtectedSources, cfg.firewallProtectedPorts)
	}
}

func TestLoadConfigPM2DefaultsAreNotConfigured(t *testing.T) {
	env := map[string]string{
		"HSERVER_AGENT_HUB_URL":        "https://hub.invalid",
		"HSERVER_AGENT_NODE_ID":        "node-1",
		"HSERVER_AGENT_TOKEN":          "value",
		"HSERVER_AGENT_ALLOW_PM2_READ": "false",
	}
	lookup := func(key string) (string, bool) { value, ok := env[key]; return value, ok }
	cfg, err := loadConfigFromEnv(lookup, func(string) ([]byte, error) { return nil, errors.New("not read") })
	if err != nil {
		t.Fatalf("loadConfigFromEnv() error = %v", err)
	}
	if cfg.allowPM2Read || len(cfg.allowedPM2Actions) != 0 || cfg.pm2Binary != "" || cfg.pm2Home != "" || cfg.pm2User != "" {
		t.Fatalf("PM2 should be not configured by default: read=%t actions=%#v binary=%q home=%q user=%q", cfg.allowPM2Read, cfg.allowedPM2Actions, cfg.pm2Binary, cfg.pm2Home, cfg.pm2User)
	}
}

func TestLoadConfigRequiresExplicitPM2IdentityWhenEnabled(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "read requires binary",
			env:  map[string]string{"HSERVER_AGENT_ALLOW_PM2_READ": "true"},
			want: "HSERVER_AGENT_PM2_BINARY is required",
		},
		{
			name: "actions require binary",
			env:  map[string]string{"HSERVER_AGENT_ALLOWED_PM2_ACTIONS": "restart"},
			want: "HSERVER_AGENT_PM2_BINARY is required",
		},
		{
			name: "read requires home",
			env: map[string]string{
				"HSERVER_AGENT_ALLOW_PM2_READ": "true",
				"HSERVER_AGENT_PM2_BINARY":     "/usr/local/bin/pm2",
			},
			want: "HSERVER_AGENT_PM2_HOME is required",
		},
		{
			name: "read requires user",
			env: map[string]string{
				"HSERVER_AGENT_ALLOW_PM2_READ": "true",
				"HSERVER_AGENT_PM2_BINARY":     "/usr/local/bin/pm2",
				"HSERVER_AGENT_PM2_HOME":       "/srv/pm2",
			},
			want: "HSERVER_AGENT_PM2_USER is required",
		},
		{
			name: "root identity is rejected",
			env: map[string]string{
				"HSERVER_AGENT_ALLOW_PM2_READ": "true",
				"HSERVER_AGENT_PM2_BINARY":     "/usr/local/bin/pm2",
				"HSERVER_AGENT_PM2_HOME":       "/srv/pm2",
				"HSERVER_AGENT_PM2_USER":       "root",
			},
			want: "unprivileged account",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{
				"HSERVER_AGENT_HUB_URL": "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID": "node-1",
				"HSERVER_AGENT_TOKEN":   "value",
			}
			for key, value := range tc.env {
				env[key] = value
			}
			lookup := func(key string) (string, bool) { value, ok := env[key]; return value, ok }
			_, err := loadConfigFromEnv(lookup, func(string) ([]byte, error) { return nil, errors.New("not read") })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestLoadConfigRejectsAmbiguousTokenAndUnsafeService(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "both token sources",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":    "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":    "node-1",
				"HSERVER_AGENT_TOKEN":      "value",
				"HSERVER_AGENT_TOKEN_FILE": "/secret",
			},
			want: "only one",
		},
		{
			name: "agent update action requires read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":              "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":              "node-1",
				"HSERVER_AGENT_TOKEN":                "value",
				"HSERVER_AGENT_ALLOW_UPDATE_ACTIONS": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_UPDATE_READ=true",
		},
		{
			name: "agent update manifest rejects credentials",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":             "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":             "node-1",
				"HSERVER_AGENT_TOKEN":               "value",
				"HSERVER_AGENT_UPDATE_MANIFEST_URL": "https://user:pass@releases.invalid/latest.json",
			},
			want: "without credentials",
		},
		{
			name: "unsafe service",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":          "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":          "node-1",
				"HSERVER_AGENT_TOKEN":            "value",
				"HSERVER_AGENT_ALLOWED_SERVICES": "nginx.service --now",
			},
			want: "invalid systemd unit",
		},
		{
			name: "unsafe token header value",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL": "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID": "node-1",
				"HSERVER_AGENT_TOKEN":   "value with spaces",
			},
			want: "visible-ASCII",
		},
		{
			name: "unsupported host action",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":              "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":              "node-1",
				"HSERVER_AGENT_TOKEN":                "value",
				"HSERVER_AGENT_ALLOWED_HOST_ACTIONS": "arbitrary-shell",
			},
			want: "unsupported host action",
		},
		{
			name: "invalid process signal toggle",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":               "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":               "node-1",
				"HSERVER_AGENT_TOKEN":                 "value",
				"HSERVER_AGENT_ALLOW_PROCESS_SIGNALS": "yes",
			},
			want: "must be true or false",
		},
		{
			name: "invalid terminal toggle",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":        "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":        "node-1",
				"HSERVER_AGENT_TOKEN":          "value",
				"HSERVER_AGENT_ALLOW_TERMINAL": "enabled",
			},
			want: "must be true or false",
		},
		{
			name: "invalid disk cleanup target",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":              "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":              "node-1",
				"HSERVER_AGENT_TOKEN":                "value",
				"HSERVER_AGENT_ALLOWED_DISK_CLEANUP": "root-files",
			},
			want: "unsupported disk cleanup target",
		},
		{
			name: "invalid log source",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":             "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":             "node-1",
				"HSERVER_AGENT_TOKEN":               "value",
				"HSERVER_AGENT_ALLOWED_LOG_SOURCES": "arbitrary-file",
			},
			want: "unsupported log source",
		},
		{
			name: "invalid container read toggle",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":              "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":              "node-1",
				"HSERVER_AGENT_TOKEN":                "value",
				"HSERVER_AGENT_ALLOW_CONTAINER_READ": "yes",
			},
			want: "must be true or false",
		},
		{
			name: "invalid container action",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":                   "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":                   "node-1",
				"HSERVER_AGENT_TOKEN":                     "value",
				"HSERVER_AGENT_ALLOWED_CONTAINER_ACTIONS": "exec",
			},
			want: "unsupported container action",
		},
		{
			name: "invalid Nginx action",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":               "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":               "node-1",
				"HSERVER_AGENT_TOKEN":                 "value",
				"HSERVER_AGENT_ALLOWED_NGINX_ACTIONS": "restart",
			},
			want: "unsupported Nginx action",
		},
		{
			name: "Nginx write requires read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":                  "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":                  "node-1",
				"HSERVER_AGENT_TOKEN":                    "value",
				"HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ=true",
		},
		{
			name: "invalid Nginx config directory",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":               "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":               "node-1",
				"HSERVER_AGENT_TOKEN":                 "value",
				"HSERVER_AGENT_NGINX_SITES_AVAILABLE": "relative/path",
			},
			want: "clean absolute directory",
		},
		{
			name: "invalid PHP-FPM action",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":             "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":             "node-1",
				"HSERVER_AGENT_TOKEN":               "value",
				"HSERVER_AGENT_ALLOWED_PHP_ACTIONS": "stop",
			},
			want: "unsupported PHP-FPM action",
		},
		{
			name: "PHP-FPM write requires read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":                "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":                "node-1",
				"HSERVER_AGENT_TOKEN":                  "value",
				"HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_PHP_CONFIG_READ=true",
		},
		{
			name: "invalid PM2 action",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":             "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":             "node-1",
				"HSERVER_AGENT_TOKEN":               "value",
				"HSERVER_AGENT_ALLOWED_PM2_ACTIONS": "delete",
			},
			want: "unsupported PM2 action",
		},
		{
			name: "invalid PM2 binary",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":    "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":    "node-1",
				"HSERVER_AGENT_TOKEN":      "value",
				"HSERVER_AGENT_PM2_BINARY": "pm2",
			},
			want: "clean absolute file path",
		},
		{
			name: "invalid PM2 user",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":  "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":  "node-1",
				"HSERVER_AGENT_TOKEN":    "value",
				"HSERVER_AGENT_PM2_USER": "deploy;id",
			},
			want: "valid system user",
		},
		{
			name: "cron write requires read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":          "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":          "node-1",
				"HSERVER_AGENT_TOKEN":            "value",
				"HSERVER_AGENT_ALLOW_CRON_WRITE": "true",
			},
			want: "cron write and run require",
		},
		{
			name: "firewall write requires read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":              "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":              "node-1",
				"HSERVER_AGENT_TOKEN":                "value",
				"HSERVER_AGENT_ALLOW_FIREWALL_WRITE": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_FIREWALL_READ=true",
		},
		{
			name: "domain actions require read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":              "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":              "node-1",
				"HSERVER_AGENT_TOKEN":                "value",
				"HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_DOMAIN_READ=true",
		},
		{
			name: "SSL actions require read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":           "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":           "node-1",
				"HSERVER_AGENT_TOKEN":             "value",
				"HSERVER_AGENT_ALLOW_SSL_ACTIONS": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_SSL_READ=true",
		},
		{
			name: "database restarts require read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":                   "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":                   "node-1",
				"HSERVER_AGENT_TOKEN":                     "value",
				"HSERVER_AGENT_ALLOWED_DATABASE_RESTARTS": "mariadb",
			},
			want: "requires HSERVER_AGENT_ALLOW_DATABASE_READ=true",
		},
		{
			name: "backup run requires read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":          "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":          "node-1",
				"HSERVER_AGENT_TOKEN":            "value",
				"HSERVER_AGENT_ALLOW_BACKUP_RUN": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_BACKUP_READ=true",
		},
		{
			name: "deploy actions require read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":              "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":              "node-1",
				"HSERVER_AGENT_TOKEN":                "value",
				"HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_DEPLOY_READ=true",
		},
		{
			name: "deploy domain read requires deploy read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":                  "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":                  "node-1",
				"HSERVER_AGENT_TOKEN":                    "value",
				"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_DEPLOY_READ=true",
		},
		{
			name: "deploy domain actions require domain read",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":                     "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":                     "node-1",
				"HSERVER_AGENT_TOKEN":                       "value",
				"HSERVER_AGENT_ALLOW_DEPLOY_READ":           "true",
				"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS": "true",
			},
			want: "requires HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=true",
		},
		{
			name: "file write roots require read containment",
			env: map[string]string{
				"HSERVER_AGENT_HUB_URL":          "https://hub.invalid",
				"HSERVER_AGENT_NODE_ID":          "node-1",
				"HSERVER_AGENT_TOKEN":            "value",
				"HSERVER_AGENT_FILE_READ_ROOTS":  "/srv/apps",
				"HSERVER_AGENT_FILE_WRITE_ROOTS": "/etc",
			},
			want: "must be contained by HSERVER_AGENT_FILE_READ_ROOTS",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(key string) (string, bool) { value, ok := tc.env[key]; return value, ok }
			_, err := loadConfigFromEnv(lookup, func(string) ([]byte, error) { return nil, errors.New("not read") })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
