package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultInterval       = 30 * time.Second
	minInterval           = 5 * time.Second
	maxInterval           = time.Hour
	maxServices           = 128
	maxHostActions        = 5
	maxDiskCleanupTargets = 4
	maxLogSources         = 7
	maxContainerActions   = 3
	maxNginxActions       = 2
	maxPHPActions         = 3
	maxPM2Actions         = 4
	maxFileRoots          = 16
	maxTokenBytes         = 64 << 10
	defaultNginxAvailable = "/etc/nginx/sites-available"
	defaultNginxEnabled   = "/etc/nginx/sites-enabled"
	defaultCertConfigDir  = "/etc/letsencrypt"
	defaultCertWorkDir    = "/var/lib/letsencrypt"
	defaultCertLogsDir    = "/var/log/letsencrypt"
	defaultCertbotBinary  = "/usr/bin/certbot"
	defaultOpenSSLBinary  = "/usr/bin/openssl"
	defaultCABundle       = "/etc/ssl/certs/ca-certificates.crt"
	defaultMariaDBBinary  = "/usr/bin/mariadb"
	defaultMariaAdmin     = "/usr/bin/mariadb-admin"
	defaultPGClusters     = "/usr/bin/pg_lsclusters"
	defaultPSQLBinary     = "/usr/bin/psql"
	defaultPGIsReady      = "/usr/bin/pg_isready"
	defaultBackupPlans    = "/etc/hserver/backup-plans.json"
	defaultDeployPlans    = "/etc/hserver/deploy-plans.json"
	defaultDeployACMERoot = "/var/www/hserver-acme"
	defaultAgentStateDir  = "/var/lib/hserver-agent"
	defaultLifecycleTool  = "/usr/local/libexec/hserver-agent-install"
	defaultSystemdRun     = "/usr/bin/systemd-run"
	defaultSystemctl      = "/usr/bin/systemctl"
	defaultPHPConfigRoot  = "/etc/php"
	defaultPHPBinaryRoot  = "/usr/sbin"
	defaultCronStatePath  = "/etc/hserver/cron-jobs.json"
	defaultCronFilePath   = "/etc/cron.d/hserver-managed"
	defaultCronLockPath   = "/run/lock/hserver-cron.lock"
	defaultCrontabBinary  = "/usr/bin/crontab"
	defaultRunuserBinary  = "/usr/sbin/runuser"
	defaultCronShell      = "/bin/bash"
	defaultCronService    = "cron.service"
	defaultIPTablesBinary = "/usr/sbin/iptables"
	defaultFirewallSave   = "/usr/sbin/netfilter-persistent"
	defaultFirewallLock   = "/run/lock/hserver-firewall.lock"
	defaultFirewallSvc    = "netfilter-persistent.service"
	defaultFirewallState  = "/etc/iptables"
)

var (
	nodeIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	servicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.@-]{0,254}$`)
)

type config struct {
	hubURL                   *url.URL
	nodeID                   string
	token                    string
	interval                 time.Duration
	observedServices         []string
	allowedServices          map[string]struct{}
	allowedHostActions       map[string]struct{}
	allowProcessSignals      bool
	allowTerminal            bool
	allowedDiskCleanup       map[string]struct{}
	allowedLogSources        map[string]struct{}
	allowContainerRead       bool
	allowedContainerActions  map[string]struct{}
	allowedNginxActions      map[string]struct{}
	allowNginxConfigRead     bool
	allowNginxConfigWrite    bool
	allowDomainRead          bool
	allowDomainActions       bool
	allowSSLRead             bool
	allowSSLActions          bool
	certificateConfigDir     string
	certificateWorkDir       string
	certificateLogsDir       string
	certbotBinary            string
	openSSLBinary            string
	caBundle                 string
	allowDatabaseRead        bool
	allowedDatabaseRestarts  map[string]struct{}
	mariaDBBinary            string
	mariaDBAdminBinary       string
	pgClustersBinary         string
	psqlBinary               string
	pgIsReadyBinary          string
	allowBackupRead          bool
	allowBackupRun           bool
	backupPlansPath          string
	allowDeployRead          bool
	allowDeployActions       bool
	allowDeployDomainRead    bool
	allowDeployDomainActions bool
	deployPlansPath          string
	deployACMEWebroot        string
	deployWriteRoots         []string
	allowAgentUpdateRead     bool
	allowAgentUpdateActions  bool
	agentUpdateManifestURL   string
	agentUpdatePublicKeys    string
	agentStateDir            string
	agentLifecycleInstaller  string
	systemdRunBinary         string
	systemctlBinary          string
	fileReadRoots            []string
	fileWriteRoots           []string
	nginxSitesAvailable      string
	nginxSitesEnabled        string
	allowedPHPActions        map[string]struct{}
	allowPHPConfigRead       bool
	allowPHPConfigWrite      bool
	phpConfigRoot            string
	phpBinaryRoot            string
	allowPM2Read             bool
	allowedPM2Actions        map[string]struct{}
	pm2Binary                string
	pm2Home                  string
	pm2User                  string
	allowCronRead            bool
	allowCronWrite           bool
	allowCronRun             bool
	cronStatePath            string
	cronFilePath             string
	cronLockPath             string
	crontabBinary            string
	runuserBinary            string
	cronShell                string
	cronService              string
	allowFirewallRead        bool
	allowFirewallWrite       bool
	iptablesBinary           string
	firewallSaveBinary       string
	firewallLockPath         string
	firewallService          string
	firewallPersistencePath  string
	firewallProtectedSources []string
	firewallProtectedPorts   map[int]struct{}
	profileObservation       profileObservation
}

func loadConfig() (config, error) {
	return loadConfigFromEnv(os.LookupEnv, os.ReadFile)
}

func loadConfigFromEnv(lookup func(string) (string, bool), readFile func(string) ([]byte, error)) (config, error) {
	var cfg config

	rawURL := envValue(lookup, "HSERVER_AGENT_HUB_URL")
	if rawURL == "" {
		return cfg, errors.New("HSERVER_AGENT_HUB_URL is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return cfg, errors.New("HSERVER_AGENT_HUB_URL must be an http(s) origin without credentials, query, or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	cfg.hubURL = u

	cfg.nodeID = envValue(lookup, "HSERVER_AGENT_NODE_ID")
	if !nodeIDPattern.MatchString(cfg.nodeID) {
		return cfg, errors.New("HSERVER_AGENT_NODE_ID is required and must contain only letters, digits, dot, underscore, or hyphen")
	}

	tokenValue := envValue(lookup, "HSERVER_AGENT_TOKEN")
	tokenFile := envValue(lookup, "HSERVER_AGENT_TOKEN_FILE")
	if tokenValue != "" && tokenFile != "" {
		return cfg, errors.New("configure only one of HSERVER_AGENT_TOKEN or HSERVER_AGENT_TOKEN_FILE")
	}
	if tokenFile != "" {
		data, readErr := readFile(tokenFile)
		if readErr != nil {
			return cfg, fmt.Errorf("read HSERVER_AGENT_TOKEN_FILE: %w", readErr)
		}
		if len(data) > maxTokenBytes {
			return cfg, errors.New("HSERVER_AGENT_TOKEN_FILE exceeds the size limit")
		}
		tokenValue = strings.TrimSpace(string(data))
	}
	if !validToken(tokenValue) {
		return cfg, errors.New("a non-empty visible-ASCII agent token within the size limit is required")
	}
	cfg.token = tokenValue

	cfg.interval = defaultInterval
	if rawInterval := envValue(lookup, "HSERVER_AGENT_INTERVAL"); rawInterval != "" {
		cfg.interval, err = time.ParseDuration(rawInterval)
		if err != nil || cfg.interval < minInterval || cfg.interval > maxInterval {
			return cfg, fmt.Errorf("HSERVER_AGENT_INTERVAL must be between %s and %s", minInterval, maxInterval)
		}
	}

	cfg.observedServices, err = parseServices(envValue(lookup, "HSERVER_AGENT_OBSERVED_SERVICES"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_OBSERVED_SERVICES: %w", err)
	}
	allowed, err := parseServices(envValue(lookup, "HSERVER_AGENT_ALLOWED_SERVICES"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOWED_SERVICES: %w", err)
	}
	cfg.allowedServices = make(map[string]struct{}, len(allowed))
	for _, service := range allowed {
		cfg.allowedServices[service] = struct{}{}
	}
	hostActions, err := parseHostActions(envValue(lookup, "HSERVER_AGENT_ALLOWED_HOST_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOWED_HOST_ACTIONS: %w", err)
	}
	cfg.allowedHostActions = hostActions
	allowProcessSignals, err := parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_PROCESS_SIGNALS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_PROCESS_SIGNALS: %w", err)
	}
	cfg.allowProcessSignals = allowProcessSignals
	allowTerminal, err := parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_TERMINAL"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_TERMINAL: %w", err)
	}
	cfg.allowTerminal = allowTerminal
	allowedDiskCleanup, err := parseDiskCleanupTargets(envValue(lookup, "HSERVER_AGENT_ALLOWED_DISK_CLEANUP"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOWED_DISK_CLEANUP: %w", err)
	}
	cfg.allowedDiskCleanup = allowedDiskCleanup
	allowedLogSources, err := parseLogSources(envValue(lookup, "HSERVER_AGENT_ALLOWED_LOG_SOURCES"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOWED_LOG_SOURCES: %w", err)
	}
	cfg.allowedLogSources = allowedLogSources
	allowContainerRead, err := parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_CONTAINER_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_CONTAINER_READ: %w", err)
	}
	cfg.allowContainerRead = allowContainerRead
	allowedContainerActions, err := parseContainerActions(envValue(lookup, "HSERVER_AGENT_ALLOWED_CONTAINER_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOWED_CONTAINER_ACTIONS: %w", err)
	}
	cfg.allowedContainerActions = allowedContainerActions
	allowedNginxActions, err := parseNginxActions(envValue(lookup, "HSERVER_AGENT_ALLOWED_NGINX_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOWED_NGINX_ACTIONS: %w", err)
	}
	cfg.allowedNginxActions = allowedNginxActions
	allowNginxConfigRead, err := parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ: %w", err)
	}
	allowNginxConfigWrite, err := parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE: %w", err)
	}
	if allowNginxConfigWrite && !allowNginxConfigRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE requires HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ=true")
	}
	cfg.allowNginxConfigRead = allowNginxConfigRead
	cfg.allowNginxConfigWrite = allowNginxConfigWrite
	cfg.nginxSitesAvailable, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_NGINX_SITES_AVAILABLE"), defaultNginxAvailable)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_NGINX_SITES_AVAILABLE: %w", err)
	}
	cfg.nginxSitesEnabled, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_NGINX_SITES_ENABLED"), defaultNginxEnabled)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_NGINX_SITES_ENABLED: %w", err)
	}
	cfg.allowDomainRead, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_DOMAIN_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_DOMAIN_READ: %w", err)
	}
	cfg.allowDomainActions, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS: %w", err)
	}
	if cfg.allowDomainActions && !cfg.allowDomainRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS requires HSERVER_AGENT_ALLOW_DOMAIN_READ=true")
	}
	cfg.allowSSLRead, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_SSL_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_SSL_READ: %w", err)
	}
	cfg.allowSSLActions, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_SSL_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_SSL_ACTIONS: %w", err)
	}
	if cfg.allowSSLActions && !cfg.allowSSLRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_SSL_ACTIONS requires HSERVER_AGENT_ALLOW_SSL_READ=true")
	}
	cfg.certificateConfigDir, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_CERTBOT_CONFIG_DIR"), defaultCertConfigDir)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_CERTBOT_CONFIG_DIR: %w", err)
	}
	cfg.certificateWorkDir, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_CERTBOT_WORK_DIR"), defaultCertWorkDir)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_CERTBOT_WORK_DIR: %w", err)
	}
	cfg.certificateLogsDir, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_CERTBOT_LOGS_DIR"), defaultCertLogsDir)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_CERTBOT_LOGS_DIR: %w", err)
	}
	for key, item := range map[string]struct {
		raw      string
		fallback string
		target   *string
	}{
		"HSERVER_AGENT_CERTBOT_BINARY": {envValue(lookup, "HSERVER_AGENT_CERTBOT_BINARY"), defaultCertbotBinary, &cfg.certbotBinary},
		"HSERVER_AGENT_OPENSSL_BINARY": {envValue(lookup, "HSERVER_AGENT_OPENSSL_BINARY"), defaultOpenSSLBinary, &cfg.openSSLBinary},
		"HSERVER_AGENT_CA_BUNDLE":      {envValue(lookup, "HSERVER_AGENT_CA_BUNDLE"), defaultCABundle, &cfg.caBundle},
	} {
		*item.target, err = parseAbsoluteFilePath(item.raw, item.fallback)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", key, err)
		}
	}
	cfg.allowBackupRead, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_BACKUP_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_BACKUP_READ: %w", err)
	}
	cfg.allowBackupRun, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_BACKUP_RUN"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_BACKUP_RUN: %w", err)
	}
	if cfg.allowBackupRun && !cfg.allowBackupRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_BACKUP_RUN requires HSERVER_AGENT_ALLOW_BACKUP_READ=true")
	}
	cfg.backupPlansPath, err = parseAbsoluteFilePath(envValue(lookup, "HSERVER_AGENT_BACKUP_PLANS_FILE"), defaultBackupPlans)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_BACKUP_PLANS_FILE: %w", err)
	}
	cfg.allowDeployRead, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_DEPLOY_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_DEPLOY_READ: %w", err)
	}
	cfg.allowDeployActions, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS: %w", err)
	}
	if cfg.allowDeployActions && !cfg.allowDeployRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS requires HSERVER_AGENT_ALLOW_DEPLOY_READ=true")
	}
	cfg.allowDeployDomainRead, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ: %w", err)
	}
	if cfg.allowDeployDomainRead && !cfg.allowDeployRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ requires HSERVER_AGENT_ALLOW_DEPLOY_READ=true")
	}
	cfg.allowDeployDomainActions, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS: %w", err)
	}
	if cfg.allowDeployDomainActions && !cfg.allowDeployDomainRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS requires HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=true")
	}
	cfg.deployPlansPath, err = parseAbsoluteFilePath(envValue(lookup, "HSERVER_AGENT_DEPLOY_PLANS_FILE"), defaultDeployPlans)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_DEPLOY_PLANS_FILE: %w", err)
	}
	cfg.deployACMEWebroot, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_DEPLOY_ACME_WEBROOT"), defaultDeployACMERoot)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_DEPLOY_ACME_WEBROOT: %w", err)
	}
	cfg.deployWriteRoots, err = parseAbsoluteDirectories(envValue(lookup, "HSERVER_AGENT_DEPLOY_WRITE_ROOTS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_DEPLOY_WRITE_ROOTS: %w", err)
	}
	cfg.allowAgentUpdateRead, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_UPDATE_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_UPDATE_READ: %w", err)
	}
	cfg.allowAgentUpdateActions, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_UPDATE_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_UPDATE_ACTIONS: %w", err)
	}
	if cfg.allowAgentUpdateActions && !cfg.allowAgentUpdateRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_UPDATE_ACTIONS requires HSERVER_AGENT_ALLOW_UPDATE_READ=true")
	}
	cfg.agentUpdateManifestURL = strings.TrimSpace(envValue(lookup, "HSERVER_AGENT_UPDATE_MANIFEST_URL"))
	if cfg.agentUpdateManifestURL != "" && !validAgentUpdateManifestURL(cfg.agentUpdateManifestURL) {
		return cfg, errors.New("HSERVER_AGENT_UPDATE_MANIFEST_URL must be an HTTP(S) URL without credentials")
	}
	cfg.agentUpdatePublicKeys = strings.TrimSpace(envValue(lookup, "HSERVER_AGENT_UPDATE_MANIFEST_PUBLIC_KEYS"))
	cfg.agentStateDir, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_STATE_DIR"), defaultAgentStateDir)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_STATE_DIR: %w", err)
	}
	for key, item := range map[string]struct {
		raw      string
		fallback string
		target   *string
	}{
		"HSERVER_AGENT_LIFECYCLE_INSTALLER": {envValue(lookup, "HSERVER_AGENT_LIFECYCLE_INSTALLER"), defaultLifecycleTool, &cfg.agentLifecycleInstaller},
		"HSERVER_AGENT_SYSTEMD_RUN_BINARY":  {envValue(lookup, "HSERVER_AGENT_SYSTEMD_RUN_BINARY"), defaultSystemdRun, &cfg.systemdRunBinary},
		"HSERVER_AGENT_SYSTEMCTL_BINARY":    {envValue(lookup, "HSERVER_AGENT_SYSTEMCTL_BINARY"), defaultSystemctl, &cfg.systemctlBinary},
	} {
		*item.target, err = parseAbsoluteFilePath(item.raw, item.fallback)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", key, err)
		}
	}
	cfg.fileReadRoots, err = parseAbsoluteDirectories(envValue(lookup, "HSERVER_AGENT_FILE_READ_ROOTS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_FILE_READ_ROOTS: %w", err)
	}
	cfg.fileWriteRoots, err = parseAbsoluteDirectories(envValue(lookup, "HSERVER_AGENT_FILE_WRITE_ROOTS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_FILE_WRITE_ROOTS: %w", err)
	}
	for _, root := range cfg.fileWriteRoots {
		if !pathWithinAnyRoot(root, cfg.fileReadRoots) {
			return cfg, errors.New("HSERVER_AGENT_FILE_WRITE_ROOTS must be contained by HSERVER_AGENT_FILE_READ_ROOTS")
		}
	}
	cfg.allowDatabaseRead, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_DATABASE_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_DATABASE_READ: %w", err)
	}
	cfg.allowedDatabaseRestarts, err = parseDatabaseEngines(envValue(lookup, "HSERVER_AGENT_ALLOWED_DATABASE_RESTARTS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOWED_DATABASE_RESTARTS: %w", err)
	}
	if len(cfg.allowedDatabaseRestarts) > 0 && !cfg.allowDatabaseRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOWED_DATABASE_RESTARTS requires HSERVER_AGENT_ALLOW_DATABASE_READ=true")
	}
	for key, item := range map[string]struct {
		raw      string
		fallback string
		target   *string
	}{
		"HSERVER_AGENT_MARIADB_BINARY":       {envValue(lookup, "HSERVER_AGENT_MARIADB_BINARY"), defaultMariaDBBinary, &cfg.mariaDBBinary},
		"HSERVER_AGENT_MARIADB_ADMIN_BINARY": {envValue(lookup, "HSERVER_AGENT_MARIADB_ADMIN_BINARY"), defaultMariaAdmin, &cfg.mariaDBAdminBinary},
		"HSERVER_AGENT_PG_LSCLUSTERS_BINARY": {envValue(lookup, "HSERVER_AGENT_PG_LSCLUSTERS_BINARY"), defaultPGClusters, &cfg.pgClustersBinary},
		"HSERVER_AGENT_PSQL_BINARY":          {envValue(lookup, "HSERVER_AGENT_PSQL_BINARY"), defaultPSQLBinary, &cfg.psqlBinary},
		"HSERVER_AGENT_PG_ISREADY_BINARY":    {envValue(lookup, "HSERVER_AGENT_PG_ISREADY_BINARY"), defaultPGIsReady, &cfg.pgIsReadyBinary},
	} {
		*item.target, err = parseAbsoluteFilePath(item.raw, item.fallback)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", key, err)
		}
	}
	allowedPHPActions, err := parsePHPActions(envValue(lookup, "HSERVER_AGENT_ALLOWED_PHP_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOWED_PHP_ACTIONS: %w", err)
	}
	cfg.allowedPHPActions = allowedPHPActions
	allowPHPConfigRead, err := parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_PHP_CONFIG_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_PHP_CONFIG_READ: %w", err)
	}
	allowPHPConfigWrite, err := parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE: %w", err)
	}
	if allowPHPConfigWrite && !allowPHPConfigRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE requires HSERVER_AGENT_ALLOW_PHP_CONFIG_READ=true")
	}
	cfg.allowPHPConfigRead = allowPHPConfigRead
	cfg.allowPHPConfigWrite = allowPHPConfigWrite
	cfg.phpConfigRoot, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_PHP_CONFIG_ROOT"), defaultPHPConfigRoot)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_PHP_CONFIG_ROOT: %w", err)
	}
	cfg.phpBinaryRoot, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_PHP_BINARY_ROOT"), defaultPHPBinaryRoot)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_PHP_BINARY_ROOT: %w", err)
	}
	allowPM2Read, err := parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_PM2_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_PM2_READ: %w", err)
	}
	cfg.allowPM2Read = allowPM2Read
	allowedPM2Actions, err := parsePM2Actions(envValue(lookup, "HSERVER_AGENT_ALLOWED_PM2_ACTIONS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOWED_PM2_ACTIONS: %w", err)
	}
	cfg.allowedPM2Actions = allowedPM2Actions
	if rawPM2Binary := envValue(lookup, "HSERVER_AGENT_PM2_BINARY"); rawPM2Binary != "" {
		cfg.pm2Binary, err = parseAbsoluteFilePath(rawPM2Binary, "")
		if err != nil {
			return cfg, fmt.Errorf("HSERVER_AGENT_PM2_BINARY: %w", err)
		}
	}
	if rawPM2Home := envValue(lookup, "HSERVER_AGENT_PM2_HOME"); rawPM2Home != "" {
		cfg.pm2Home, err = parseAbsoluteDirectory(rawPM2Home, "")
		if err != nil {
			return cfg, fmt.Errorf("HSERVER_AGENT_PM2_HOME: %w", err)
		}
	}
	if rawPM2User := envValue(lookup, "HSERVER_AGENT_PM2_USER"); rawPM2User != "" {
		cfg.pm2User, err = parseSystemUser(rawPM2User, "")
		if err != nil {
			return cfg, fmt.Errorf("HSERVER_AGENT_PM2_USER: %w", err)
		}
		if cfg.pm2User == "root" {
			return cfg, errors.New("HSERVER_AGENT_PM2_USER must be an unprivileged account")
		}
	}
	if allowPM2Read || len(cfg.allowedPM2Actions) > 0 {
		if cfg.pm2Binary == "" {
			return cfg, errors.New("HSERVER_AGENT_PM2_BINARY is required when PM2 read or actions are enabled")
		}
		if cfg.pm2Home == "" {
			return cfg, errors.New("HSERVER_AGENT_PM2_HOME is required when PM2 read or actions are enabled")
		}
		if cfg.pm2User == "" {
			return cfg, errors.New("HSERVER_AGENT_PM2_USER is required when PM2 read or actions are enabled")
		}
	}
	cfg.allowCronRead, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_CRON_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_CRON_READ: %w", err)
	}
	cfg.allowCronWrite, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_CRON_WRITE"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_CRON_WRITE: %w", err)
	}
	cfg.allowCronRun, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_CRON_RUN"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_CRON_RUN: %w", err)
	}
	if (cfg.allowCronWrite || cfg.allowCronRun) && !cfg.allowCronRead {
		return cfg, errors.New("cron write and run require HSERVER_AGENT_ALLOW_CRON_READ=true")
	}
	for key, item := range map[string]struct {
		raw      string
		fallback string
		target   *string
	}{
		"HSERVER_AGENT_CRON_STATE_PATH": {envValue(lookup, "HSERVER_AGENT_CRON_STATE_PATH"), defaultCronStatePath, &cfg.cronStatePath},
		"HSERVER_AGENT_CRON_FILE_PATH":  {envValue(lookup, "HSERVER_AGENT_CRON_FILE_PATH"), defaultCronFilePath, &cfg.cronFilePath},
		"HSERVER_AGENT_CRON_LOCK_PATH":  {envValue(lookup, "HSERVER_AGENT_CRON_LOCK_PATH"), defaultCronLockPath, &cfg.cronLockPath},
		"HSERVER_AGENT_CRONTAB_BINARY":  {envValue(lookup, "HSERVER_AGENT_CRONTAB_BINARY"), defaultCrontabBinary, &cfg.crontabBinary},
		"HSERVER_AGENT_RUNUSER_BINARY":  {envValue(lookup, "HSERVER_AGENT_RUNUSER_BINARY"), defaultRunuserBinary, &cfg.runuserBinary},
		"HSERVER_AGENT_CRON_SHELL":      {envValue(lookup, "HSERVER_AGENT_CRON_SHELL"), defaultCronShell, &cfg.cronShell},
	} {
		*item.target, err = parseAbsoluteFilePath(item.raw, item.fallback)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", key, err)
		}
	}
	cfg.cronService = envValue(lookup, "HSERVER_AGENT_CRON_SERVICE")
	if cfg.cronService == "" {
		cfg.cronService = defaultCronService
	}
	if !servicePattern.MatchString(cfg.cronService) {
		return cfg, errors.New("HSERVER_AGENT_CRON_SERVICE must be a valid systemd unit")
	}
	cfg.allowFirewallRead, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_FIREWALL_READ"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_FIREWALL_READ: %w", err)
	}
	cfg.allowFirewallWrite, err = parseOptionalBool(envValue(lookup, "HSERVER_AGENT_ALLOW_FIREWALL_WRITE"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_ALLOW_FIREWALL_WRITE: %w", err)
	}
	if cfg.allowFirewallWrite && !cfg.allowFirewallRead {
		return cfg, errors.New("HSERVER_AGENT_ALLOW_FIREWALL_WRITE requires HSERVER_AGENT_ALLOW_FIREWALL_READ=true")
	}
	for key, item := range map[string]struct {
		raw      string
		fallback string
		target   *string
	}{
		"HSERVER_AGENT_IPTABLES_BINARY":      {envValue(lookup, "HSERVER_AGENT_IPTABLES_BINARY"), defaultIPTablesBinary, &cfg.iptablesBinary},
		"HSERVER_AGENT_FIREWALL_SAVE_BINARY": {envValue(lookup, "HSERVER_AGENT_FIREWALL_SAVE_BINARY"), defaultFirewallSave, &cfg.firewallSaveBinary},
		"HSERVER_AGENT_FIREWALL_LOCK_PATH":   {envValue(lookup, "HSERVER_AGENT_FIREWALL_LOCK_PATH"), defaultFirewallLock, &cfg.firewallLockPath},
	} {
		*item.target, err = parseAbsoluteFilePath(item.raw, item.fallback)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", key, err)
		}
	}
	cfg.firewallService = envValue(lookup, "HSERVER_AGENT_FIREWALL_PERSISTENCE_SERVICE")
	if cfg.firewallService == "" {
		cfg.firewallService = defaultFirewallSvc
	}
	if !servicePattern.MatchString(cfg.firewallService) {
		return cfg, errors.New("HSERVER_AGENT_FIREWALL_PERSISTENCE_SERVICE must be a valid systemd unit")
	}
	cfg.firewallPersistencePath, err = parseAbsoluteDirectory(envValue(lookup, "HSERVER_AGENT_FIREWALL_PERSISTENCE_PATH"), defaultFirewallState)
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_FIREWALL_PERSISTENCE_PATH: %w", err)
	}
	cfg.firewallProtectedSources, err = parseFirewallSources(envValue(lookup, "HSERVER_AGENT_FIREWALL_PROTECTED_SOURCES"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_FIREWALL_PROTECTED_SOURCES: %w", err)
	}
	cfg.firewallProtectedPorts, err = parseFirewallPorts(envValue(lookup, "HSERVER_AGENT_FIREWALL_PROTECTED_PORTS"))
	if err != nil {
		return cfg, fmt.Errorf("HSERVER_AGENT_FIREWALL_PROTECTED_PORTS: %w", err)
	}

	// An active profile is a local, versioned overlay for exactly the seven
	// deployment fields.  Missing or corrupt profile state never prevents the
	// agent from booting: the environment remains the fallback and the bounded
	// observation is reported in the next heartbeat.
	profileStore := newProfileStoreWithReader(cfg.agentStateDir, readFile)
	active, observation, profileErr := profileStore.loadActiveProfile()
	cfg.profileObservation = observation
	if profileErr == nil && active.Revision > 0 {
		applyProfileOverlay(&cfg, active.Profile)
	}

	return cfg, nil
}

func parseFirewallSources(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 32 {
		return nil, errors.New("at most 32 protected IPv4 sources are allowed")
	}
	seen := make(map[string]struct{}, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		network, err := normalizeIPv4Network(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		if _, exists := seen[network]; exists {
			continue
		}
		seen[network] = struct{}{}
		result = append(result, network)
	}
	return result, nil
}

func parseFirewallPorts(raw string) (map[int]struct{}, error) {
	result := make(map[int]struct{})
	if raw == "" {
		return result, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 32 {
		return nil, errors.New("at most 32 protected ports are allowed")
	}
	for _, part := range parts {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("protected ports must be comma-separated integers between 1 and 65535")
		}
		result[port] = struct{}{}
	}
	return result, nil
}

func normalizeIPv4Network(raw string) (string, error) {
	if ip := net.ParseIP(raw); ip != nil && ip.To4() != nil {
		return ip.To4().String() + "/32", nil
	}
	ip, network, err := net.ParseCIDR(raw)
	if err != nil || ip.To4() == nil {
		return "", errors.New("protected sources must be IPv4 addresses or CIDR networks")
	}
	return network.String(), nil
}

func parsePM2Actions(raw string) (map[string]struct{}, error) {
	actions := make(map[string]struct{})
	if raw == "" {
		return actions, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxPM2Actions {
		return nil, fmt.Errorf("at most %d PM2 actions are allowed", maxPM2Actions)
	}
	for _, part := range parts {
		action := strings.TrimSpace(part)
		switch action {
		case "start", "stop", "restart", "reload":
			actions[action] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported PM2 action %q", action)
		}
	}
	return actions, nil
}

func parseDatabaseEngines(raw string) (map[string]struct{}, error) {
	engines := make(map[string]struct{})
	if raw == "" {
		return engines, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 2 {
		return nil, errors.New("at most two database engines are allowed")
	}
	for _, part := range parts {
		engine := strings.TrimSpace(part)
		if engine != "mariadb" && engine != "postgresql" {
			return nil, fmt.Errorf("unsupported database engine %q", engine)
		}
		engines[engine] = struct{}{}
	}
	return engines, nil
}

func parsePHPActions(raw string) (map[string]struct{}, error) {
	actions := make(map[string]struct{})
	if raw == "" {
		return actions, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxPHPActions {
		return nil, fmt.Errorf("at most %d PHP-FPM actions are allowed", maxPHPActions)
	}
	for _, part := range parts {
		action := strings.TrimSpace(part)
		switch action {
		case "test", "reload", "restart":
			actions[action] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported PHP-FPM action %q", action)
		}
	}
	return actions, nil
}

func parseAbsoluteDirectory(raw, fallback string) (string, error) {
	if raw == "" {
		raw = fallback
	}
	if len(raw) > 4096 || !filepath.IsAbs(raw) || filepath.Clean(raw) != raw || raw == string(filepath.Separator) {
		return "", errors.New("must be a clean absolute directory other than root")
	}
	return raw, nil
}

func parseAbsoluteDirectories(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxFileRoots {
		return nil, fmt.Errorf("at most %d roots are allowed", maxFileRoots)
	}
	seen := make(map[string]struct{}, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		root, err := parseAbsoluteDirectory(strings.TrimSpace(part), "")
		if err != nil {
			return nil, err
		}
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}
		result = append(result, root)
	}
	return result, nil
}

func pathWithinAnyRoot(candidate string, roots []string) bool {
	for _, root := range roots {
		if candidate == root || strings.HasPrefix(candidate, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func parseAbsoluteFilePath(raw, fallback string) (string, error) {
	if raw == "" {
		raw = fallback
	}
	if len(raw) > 4096 || !filepath.IsAbs(raw) || filepath.Clean(raw) != raw || raw == string(filepath.Separator) || strings.HasSuffix(raw, string(filepath.Separator)) {
		return "", errors.New("must be a clean absolute file path")
	}
	return raw, nil
}

func parseSystemUser(raw, fallback string) (string, error) {
	if raw == "" {
		raw = fallback
	}
	if len(raw) > 64 {
		return "", errors.New("must be a valid system user")
	}
	for index, char := range raw {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_' || (index > 0 && ((char >= '0' && char <= '9') || char == '-')) {
			continue
		}
		return "", errors.New("must be a valid system user")
	}
	return raw, nil
}

func parseNginxActions(raw string) (map[string]struct{}, error) {
	actions := make(map[string]struct{})
	if raw == "" {
		return actions, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxNginxActions {
		return nil, fmt.Errorf("at most %d Nginx actions are allowed", maxNginxActions)
	}
	for _, part := range parts {
		action := strings.TrimSpace(part)
		switch action {
		case "test", "reload":
			actions[action] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported Nginx action %q", action)
		}
	}
	return actions, nil
}

func parseContainerActions(raw string) (map[string]struct{}, error) {
	actions := make(map[string]struct{})
	if raw == "" {
		return actions, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxContainerActions {
		return nil, fmt.Errorf("at most %d container actions are allowed", maxContainerActions)
	}
	for _, part := range parts {
		action := strings.TrimSpace(part)
		switch action {
		case "start", "stop", "restart":
			actions[action] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported container action %q", action)
		}
	}
	return actions, nil
}

func parseLogSources(raw string) (map[string]struct{}, error) {
	sources := make(map[string]struct{})
	if raw == "" {
		return sources, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxLogSources {
		return nil, fmt.Errorf("at most %d log sources are allowed", maxLogSources)
	}
	for _, part := range parts {
		source := strings.TrimSpace(part)
		if !validAgentLogSource(source) {
			return nil, fmt.Errorf("unsupported log source %q", source)
		}
		sources[source] = struct{}{}
	}
	return sources, nil
}

func validAgentLogSource(source string) bool {
	switch source {
	case "system", "nginx", "php", "mariadb", "postgresql", "pm2", "docker":
		return true
	default:
		return false
	}
}

func parseDiskCleanupTargets(raw string) (map[string]struct{}, error) {
	targets := make(map[string]struct{})
	if raw == "" {
		return targets, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxDiskCleanupTargets {
		return nil, fmt.Errorf("at most %d disk cleanup targets are allowed", maxDiskCleanupTargets)
	}
	for _, part := range parts {
		target := strings.TrimSpace(part)
		switch target {
		case "apt-cache", "journal", "tmp-old", "rotated-logs":
			targets[target] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported disk cleanup target %q", target)
		}
	}
	return targets, nil
}

func parseOptionalBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errors.New("must be true or false")
	}
}

func validAgentUpdateManifestURL(raw string) bool {
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil
}

func parseHostActions(raw string) (map[string]struct{}, error) {
	actions := make(map[string]struct{})
	if raw == "" {
		return actions, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxHostActions {
		return nil, fmt.Errorf("at most %d host actions are allowed", maxHostActions)
	}
	for _, part := range parts {
		action := strings.TrimSpace(part)
		switch action {
		case "memory-optimize", "swap-reset", "temp-clean", "reboot", "reboot-cancel":
			actions[action] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported host action %q", action)
		}
	}
	return actions, nil
}

func validToken(value string) bool {
	if value == "" || len(value) > maxTokenBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func envValue(lookup func(string) (string, bool), key string) string {
	value, _ := lookup(key)
	return strings.TrimSpace(value)
}

func parseServices(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxServices {
		return nil, fmt.Errorf("at most %d services are allowed", maxServices)
	}
	seen := make(map[string]struct{}, len(parts))
	services := make([]string, 0, len(parts))
	for _, part := range parts {
		service := strings.TrimSpace(part)
		if !servicePattern.MatchString(service) {
			return nil, fmt.Errorf("invalid systemd unit %q", service)
		}
		if _, exists := seen[service]; exists {
			continue
		}
		seen[service] = struct{}{}
		services = append(services, service)
	}
	return services, nil
}
