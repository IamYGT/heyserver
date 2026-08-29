package api

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/agentterminal"
	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/services/filemanager"
	"github.com/IamYGT/heyserver/internal/services/gdrive"
	"github.com/IamYGT/heyserver/internal/services/integrationstatus"
	"github.com/IamYGT/heyserver/internal/services/logs"
	nginxsvc "github.com/IamYGT/heyserver/internal/services/nginx"
	phpsvc "github.com/IamYGT/heyserver/internal/services/php"
	"github.com/IamYGT/heyserver/internal/services/releaseupdates"
	"github.com/IamYGT/heyserver/internal/services/security"
	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/services/snapshot"
	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
)

// Deps holds optional dependencies injected into the router.
type Deps struct {
	ChannelRepo       *store.NotificationChannelRepository
	DeliveryRepo      *store.NotificationDeliveryReceiptRepository
	RuleRepo          *store.AlertRuleRepository
	HistoryRepo       *store.AlertHistoryRepository
	Settings          *settings.Service
	UptimeEngine      *uptime.Engine
	MetricsRepo       *store.MetricsRepository
	AgentHub          *agenthub.Service
	AgentTerminals    *agentterminal.Hub
	IntegrationStatus *integrationstatus.Service
	// IntegrationStatusProbes contains reviewed, code-owned local probes for
	// additive catalog entries. The default constructor always supplies the
	// fifteen core probes; contributors inject additional definitions here
	// instead of relying on runtime plugin discovery or catalog metadata.
	IntegrationStatusProbes []integrationstatus.Probe
	GDrive                  *gdrive.Service
	Snapshot                *snapshot.Service
	AppCtx                  context.Context
	ShutdownCtx             context.Context
}

func NewRouter(cfg *config.Config, webFS fs.FS, deps ...*Deps) http.Handler {
	var d *Deps
	if len(deps) > 0 && deps[0] != nil {
		d = deps[0]
	} else {
		d = &Deps{}
	}
	agentTerminals := d.AgentTerminals
	if agentTerminals == nil {
		agentTerminals = agentterminal.NewHub()
	}
	integrationStatus := d.IntegrationStatus
	if integrationStatus == nil {
		integrationStatus = newIntegrationStatusService(cfg, d)
	}

	mux := http.NewServeMux()
	fileSvc := filemanager.NewWithAllowedRoots([]string{
		cfg.VhostsRoot,
		"/etc/nginx",
		cfg.PHPConfigRoot,
		"/var/log",
		"/home",
	})
	logSvc := logs.New(logs.Config{
		VhostsRoot: cfg.VhostsRoot,
		PM2User:    cfg.PM2User,
		PM2Home:    cfg.PM2Home,
	})
	nginxService := nginxsvc.NewWithConfig(nginxsvc.ServiceConfig{
		SitesAvailable: cfg.NginxSitesAvailable,
		SitesEnabled:   cfg.NginxSitesEnabled,
		VhostsRoot:     cfg.VhostsRoot,
		SnippetsDir:    cfg.NginxSnippetsDir,
	})
	phpService := phpsvc.NewWithConfig(phpsvc.ServiceConfig{
		VhostsRoot: cfg.VhostsRoot,
		ConfigRoot: cfg.PHPConfigRoot,
		BinaryRoot: cfg.PHPBinaryRoot,
	})
	releaseUpdateChecker := releaseupdates.New(cfg.UpdateManifestURL, config.Version, releaseupdates.WithManifestPublicKeys(cfg.UpdateManifestPublicKeys))
	releaseUpdateManager := releaseupdates.NewManager(
		releaseUpdateChecker,
		cfg.DataDir,
		cfg.UpdatePanelBinaryPath,
		cfg.UpdateCLIBinaryPath,
	)

	// H-1: Per-IP rate limiter for login endpoint — 5 attempts per minute, ban after 10 failures.
	loginLimiter := security.NewRateLimiter()

	// mutateLimiter throttles destructive/state-changing operations:
	// user management, firewall, SSL issuance, domain provisioning, backup/restore, deploys.
	// Shared across all mutation endpoints; 1 req/2 s per IP, burst 5.
	mutateLimiter := security.NewRateLimiter()

	// webhookLimiter throttles the unauthenticated deploy webhook endpoint.
	webhookLimiter := security.NewRateLimiter()

	// API routes
	mux.HandleFunc("GET /api/integrations/catalog", withAuth(cfg, handleIntegrationCatalog()))
	mux.HandleFunc("GET /api/integrations/status", withAuth(cfg, handleIntegrationStatus(integrationStatus)))
	mux.HandleFunc("POST /api/auth/login", loginLimiter.Middleware(security.LimitLogin)(handleLogin(cfg, loginLimiter)))
	mux.HandleFunc("POST /api/auth/logout", handleLogout())
	mux.HandleFunc("GET /api/auth/me", withAuth(cfg, handleMe()))

	// TOTP login — completes the two-step flow (email + password + code → JWT)
	mux.HandleFunc("POST /api/auth/totp-verify", loginLimiter.Middleware(security.LimitLogin)(handleTOTPLogin(cfg, loginLimiter)))
	// Recovery code login — single-use backup when authenticator is unavailable
	mux.HandleFunc("POST /api/auth/2fa/recovery", loginLimiter.Middleware(security.LimitLogin)(handleTOTPRecovery(cfg, loginLimiter)))

	// 2FA management (requires existing session)
	mux.HandleFunc("GET /api/auth/2fa/status", withAuth(cfg, handleTOTPStatus()))
	mux.HandleFunc("POST /api/auth/2fa/setup", withAuth(cfg, handleTOTPSetup(cfg)))
	mux.HandleFunc("POST /api/auth/2fa/verify", withAuth(cfg, handleTOTPVerify(cfg)))
	mux.HandleFunc("POST /api/auth/2fa/disable", withAuth(cfg, handleTOTPDisable(cfg)))

	// System stats
	mux.HandleFunc("GET /api/system/stats", withAuth(cfg, handleSystemStats()))
	mux.HandleFunc("GET /api/system/services", withAuth(cfg, handleServiceStatus()))
	mux.HandleFunc("GET /api/system/services/{service}/logs", withAuth(cfg, handleServiceLogs(hostSystemActions)))
	mux.HandleFunc("POST /api/system/actions/process", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleProcessTerminate(hostSystemActions)))))
	mux.HandleFunc("POST /api/system/actions/service", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleServiceControl(hostSystemActions)))))
	mux.HandleFunc("POST /api/system/actions/swap-reset", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleSwapReset(hostSystemActions)))))
	mux.HandleFunc("POST /api/system/actions/memory-optimize", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleMemoryOptimize(hostSystemActions)))))
	mux.HandleFunc("POST /api/system/actions/temp-clean", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleTemporaryFilesClean(hostSystemActions)))))
	mux.HandleFunc("POST /api/system/actions/reboot", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleServerReboot(hostSystemActions)))))
	mux.HandleFunc("POST /api/system/actions/reboot-cancel", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleServerRebootCancel(hostSystemActions)))))
	mux.HandleFunc("GET /api/system/actions/reboot-status", withAuth(cfg, requireRole(RoleAdmin,
		handleServerRebootStatus(hostSystemActions))))
	mux.HandleFunc("GET /api/system/actions/status", withAuth(cfg, requireRole(RoleAdmin,
		handleSystemActionStatus(hostSystemActions))))

	// Nginx
	mux.HandleFunc("GET /api/nginx/status", withAuth(cfg, handleNginxStatus(nginxService)))
	mux.HandleFunc("GET /api/nginx/configs", withAuth(cfg, handleNginxList(nginxService)))
	mux.HandleFunc("GET /api/nginx/archives", withAuth(cfg, handleNginxArchiveList(nginxService)))
	mux.HandleFunc("POST /api/nginx/archives/{archive}/restore", withAuth(cfg, requireRole(RoleManager, handleNginxArchiveRestore(nginxService))))
	mux.HandleFunc("GET /api/nginx/backups", withAuth(cfg, handleNginxBackupList(nginxService)))
	mux.HandleFunc("POST /api/nginx/backups/{backup}/restore", withAuth(cfg, requireRole(RoleManager, handleNginxBackupRestore(nginxService))))
	mux.HandleFunc("GET /api/nginx/configs/{filename}", withAuth(cfg, handleNginxGet(nginxService)))
	mux.HandleFunc("PUT /api/nginx/configs/{filename}", withAuth(cfg, requireRole(RoleManager, handleNginxSave(nginxService))))
	mux.HandleFunc("DELETE /api/nginx/configs/{filename}", withAuth(cfg, requireRole(RoleManager, handleNginxArchive(nginxService))))
	mux.HandleFunc("POST /api/nginx/configs", withAuth(cfg, requireRole(RoleManager, handleNginxCreate(nginxService))))
	mux.HandleFunc("PUT /api/nginx/configs/{filename}/state", withAuth(cfg, requireRole(RoleManager, handleNginxState(nginxService))))
	mux.HandleFunc("POST /api/nginx/configs/{filename}/toggle", withAuth(cfg, requireRole(RoleManager, handleNginxState(nginxService))))
	mux.HandleFunc("POST /api/nginx/test", withAuth(cfg, handleNginxTest(nginxService)))
	mux.HandleFunc("POST /api/nginx/reload", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handleNginxReload(nginxService)))))
	mux.HandleFunc("GET /api/nginx/snippets", withAuth(cfg, handleNginxSnippets(nginxService)))

	// PHP — Versions
	mux.HandleFunc("GET /api/php/versions", withAuth(cfg, handlePHPVersions(phpService)))
	mux.HandleFunc("POST /api/php/versions/{version}/actions/{action}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPVersionAction(phpService)))))
	mux.HandleFunc("POST /api/php/versions/{version}/restart", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPVersionRestart(phpService)))))

	// PHP — Pool management (literal sub-paths before {domain} wildcard)
	mux.HandleFunc("GET /api/php/presets", withAuth(cfg, handlePHPPresets(phpService)))
	mux.HandleFunc("GET /api/php/pools", withAuth(cfg, handlePHPPools(phpService)))
	mux.HandleFunc("POST /api/php/pools", withAuth(cfg, requireRole(RoleManager, handlePHPPoolCreateLegacy(phpService))))
	mux.HandleFunc("POST /api/php/pools/switch-version", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPSwitchVersion(phpService)))))
	mux.HandleFunc("POST /api/php/pools/auto-tune", withAuth(cfg, handlePHPAutoTune(phpService)))
	mux.HandleFunc("GET /api/php/pools/{version}/{domain}/config", withAuth(cfg, handlePHPPoolConfigGet(phpService)))
	mux.HandleFunc("PUT /api/php/pools/{version}/{domain}/config", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPPoolConfigSave(phpService)))))
	mux.HandleFunc("GET /api/php/pools/{version}/{domain}", withAuth(cfg, handlePHPPoolGet(phpService)))
	mux.HandleFunc("POST /api/php/pools/{version}/{domain}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPPoolSave(phpService)))))
	mux.HandleFunc("PUT /api/php/pools/{version}/{domain}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPPoolSaveLegacy(phpService)))))
	mux.HandleFunc("DELETE /api/php/pools/{version}/{domain}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPPoolDelete(phpService)))))
	mux.HandleFunc("POST /api/php/pools/{version}/{domain}/restart", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPPoolRestart(phpService)))))
	mux.HandleFunc("POST /api/php/pools/{version}/{domain}/preset", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPPoolApplyPreset(phpService)))))

	// PHP — php.ini management (literal sub-paths diff/directives before {domain} wildcard)
	mux.HandleFunc("GET /api/php/ini/{version}/diff", withAuth(cfg, handlePHPINIDiff(phpService)))
	mux.HandleFunc("GET /api/php/ini/{version}/directives", withAuth(cfg, handlePHPINIDirectives(phpService)))
	mux.HandleFunc("GET /api/php/ini/{version}", withAuth(cfg, handlePHPINIGet(phpService)))
	mux.HandleFunc("PUT /api/php/ini/{version}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPINISet(phpService)))))
	mux.HandleFunc("GET /api/php/ini/{version}/{domain}", withAuth(cfg, handlePHPDomainINIGet(phpService)))
	mux.HandleFunc("PUT /api/php/ini/{version}/{domain}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPDomainINISet(phpService)))))
	mux.HandleFunc("DELETE /api/php/ini/{version}/{domain}/{key}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPDomainINIReset(phpService)))))

	// PHP — Extensions
	mux.HandleFunc("GET /api/php/extensions/{version}", withAuth(cfg, handlePHPExtensionList(phpService)))
	mux.HandleFunc("POST /api/php/extensions/{version}/{name}/enable", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPExtensionEnable(phpService)))))
	mux.HandleFunc("POST /api/php/extensions/{version}/{name}/disable", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPExtensionDisable(phpService)))))

	// PHP — Monitoring & OPcache
	mux.HandleFunc("GET /api/php/status/{version}", withAuth(cfg, handlePHPStatusAll(phpService)))
	mux.HandleFunc("GET /api/php/status/{version}/{domain}", withAuth(cfg, handlePHPStatusPool(phpService)))
	mux.HandleFunc("GET /api/php/opcache/{version}", withAuth(cfg, handlePHPOPcacheGet(phpService)))
	mux.HandleFunc("POST /api/php/opcache/{version}/reset", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPOPcacheReset(phpService)))))

	// PHP — Logs
	mux.HandleFunc("GET /api/php/logs/{version}/error", withAuth(cfg, handlePHPErrorLog(phpService)))
	mux.HandleFunc("GET /api/php/logs/{version}/{domain}/slow", withAuth(cfg, handlePHPSlowLog(phpService)))

	// PHP — Security profiles
	mux.HandleFunc("GET /api/php/security/profiles", withAuth(cfg, handlePHPSecurityProfiles(phpService)))
	mux.HandleFunc("GET /api/php/security/{version}/{domain}", withAuth(cfg, handlePHPSecurityGet(phpService)))
	mux.HandleFunc("POST /api/php/security/{version}/{domain}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPSecurityApply(phpService)))))

	// PHP — Composer
	mux.HandleFunc("GET /api/php/composer/version", withAuth(cfg, handlePHPComposerVersion(phpService)))
	mux.HandleFunc("POST /api/php/composer/{version}/install", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPComposerInstall(phpService)))))
	mux.HandleFunc("POST /api/php/composer/{version}/update", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPComposerUpdate(phpService)))))
	mux.HandleFunc("POST /api/php/composer/{version}/require", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPComposerRequire(phpService)))))
	mux.HandleFunc("POST /api/php/composer/{version}/outdated", withAuth(cfg, requireRole(RoleManager, handlePHPComposerOutdated(phpService))))

	// PHP — Legacy restart route (kept for backwards compat)
	mux.HandleFunc("POST /api/php/restart/{version}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handlePHPVersionRestart(phpService)))))

	// SSL — certificate issuance triggers external certbot; rate-limit to prevent abuse.
	mux.HandleFunc("GET /api/ssl/status", withAuth(cfg, handleSSLStatus()))
	mux.HandleFunc("GET /api/ssl/certificates", withAuth(cfg, handleSSLList()))
	mux.HandleFunc("GET /api/ssl/certificates/{domain}", withAuth(cfg, handleSSLGet()))
	mux.HandleFunc("POST /api/ssl/renew/{domain}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handleSSLRenew()))))
	mux.HandleFunc("POST /api/ssl/issue", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleSSLIssue()))))

	// PM2
	mux.HandleFunc("GET /api/pm2/processes", withAuth(cfg, handlePM2List(cfg)))
	mux.HandleFunc("GET /api/pm2/processes/{id}", withAuth(cfg, handlePM2Get(cfg)))
	mux.HandleFunc("POST /api/pm2/processes/{id}/{action}", withAuth(cfg, requireRole(RoleManager, handlePM2Control(cfg))))
	mux.HandleFunc("GET /api/pm2/processes/{id}/logs", withAuth(cfg, handlePM2Logs(cfg)))
	mux.HandleFunc("POST /api/pm2/deploy", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePM2Deploy(cfg)))))
	mux.HandleFunc("POST /api/pm2/save", withAuth(cfg, requireRole(RoleManager, handlePM2Save(cfg))))

	// Database
	mux.HandleFunc("GET /api/databases", withAuth(cfg, handleDBList()))
	mux.HandleFunc("POST /api/databases", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDBCreate()))))
	mux.HandleFunc("DELETE /api/databases/{engine}/{name}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDBDrop()))))
	mux.HandleFunc("GET /api/databases/{engine}/{name}/tables", withAuth(cfg, handleDBTables()))
	mux.HandleFunc("POST /api/databases/{engine}/{name}/query", withAuth(cfg, requireRole(RoleManager, handleDBQuery())))
	mux.HandleFunc("GET /api/databases/users", withAuth(cfg, handleDBUsers()))
	mux.HandleFunc("GET /api/databases/credentials", withAuth(cfg, requireRole(RoleAdmin, handlePGMCredentials())))
	mux.HandleFunc("GET /api/databases/credentials/{name}", withAuth(cfg, requireRole(RoleAdmin, handlePGMCredentialGet())))
	mux.HandleFunc("GET /api/databases/pgm-credentials", withAuth(cfg, requireRole(RoleAdmin, handlePGMCredentialsList())))
	mux.HandleFunc("GET /api/databases/pgm-backups", withAuth(cfg, handlePGMBackups()))
	mux.HandleFunc("GET /api/databases/pgm-backup-files/{name}", withAuth(cfg, handlePGMBackupFiles()))
	mux.HandleFunc("POST /api/databases/pgm-restore", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePGMRestore()))))

	// Firewall — each rule change modifies iptables; rate-limit to prevent flooding.
	mux.HandleFunc("GET /api/firewall/status", withAuth(cfg, handleFirewallStatus()))
	mux.HandleFunc("GET /api/firewall/rules", withAuth(cfg, handleFirewallRules()))
	mux.HandleFunc("POST /api/firewall/rules", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleFirewallAdd()))))
	mux.HandleFunc("DELETE /api/firewall/rules/{number}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleFirewallDelete()))))
	mux.HandleFunc("POST /api/firewall/toggle", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleFirewallToggle()))))

	// File Manager
	mux.HandleFunc("GET /api/files", withAuth(cfg, handleFileList(fileSvc)))
	mux.HandleFunc("GET /api/files/read", withAuth(cfg, handleFileRead(fileSvc)))
	mux.HandleFunc("PUT /api/files/write", withAuth(cfg, requireRole(RoleManager, handleFileWrite(fileSvc))))
	mux.HandleFunc("POST /api/files/create", withAuth(cfg, requireRole(RoleManager, handleFileCreate(fileSvc))))
	mux.HandleFunc("DELETE /api/files", withAuth(cfg, requireRole(RoleManager, handleFileDelete(fileSvc))))
	mux.HandleFunc("POST /api/files/rename", withAuth(cfg, requireRole(RoleManager, handleFileRename(fileSvc))))

	// Backups — literal path segments before {id} wildcards (Go 1.22 mux conflict rules).
	mux.HandleFunc("GET /api/backups", withAuth(cfg, handleBackupList()))
	mux.HandleFunc("GET /api/backups/targets", withAuth(cfg, requireRole(RoleAdmin, handleBackupTargets())))
	mux.HandleFunc("POST /api/backups", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleBackupCreate()))))
	mux.HandleFunc("GET /api/backups/schedules", withAuth(cfg, requireRole(RoleAdmin, handleBackupScheduleList())))
	mux.HandleFunc("POST /api/backups/schedules", withAuth(cfg, requireRole(RoleAdmin, handleBackupScheduleSet())))
	mux.HandleFunc("DELETE /api/backups/schedules", withAuth(cfg, requireRole(RoleAdmin, handleBackupScheduleDelete())))
	mux.HandleFunc("GET /api/backups/jobs/stream", withAuth(cfg, requireRole(RoleAdmin, handleBackupJobStream(d.ShutdownCtx))))
	mux.HandleFunc("GET /api/backups/jobs", withAuth(cfg, requireRole(RoleAdmin, handleBackupJobList())))
	mux.HandleFunc("GET /api/backups/jobs/{id}", withAuth(cfg, requireRole(RoleAdmin, handleBackupJobStatus())))
	mux.HandleFunc("POST /api/backups/jobs/{id}/dismiss", withAuth(cfg, requireRole(RoleAdmin, handleBackupJobDismiss())))
	mux.HandleFunc("POST /api/backups/purge-invalid", withAuth(cfg, requireRole(RoleAdmin, handleBackupPurgeInvalid())))
	mux.HandleFunc("POST /api/backups/purge-orphaned", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleBackupPurgeOrphaned()))))

	// Google Drive offsite backup (OAuth + rclone)
	mux.HandleFunc("GET /api/backups/gdrive/oauth-app", withAuth(cfg, requireRole(RoleAdmin, handleGDriveOAuthAppGet(cfg))))
	mux.HandleFunc("PUT /api/backups/gdrive/oauth-app", withAuth(cfg, requireRole(RoleAdmin, handleGDriveOAuthAppSet())))
	mux.HandleFunc("GET /api/backups/gdrive/status", withAuth(cfg, requireRole(RoleAdmin, handleGDriveStatus(cfg))))
	mux.HandleFunc("POST /api/backups/gdrive/oauth/start", withAuth(cfg, requireRole(RoleAdmin, handleGDriveOAuthStart(cfg))))
	mux.HandleFunc("GET /api/backups/gdrive/oauth/callback", handleGDriveOAuthCallback())
	mux.HandleFunc("POST /api/backups/gdrive/oauth/complete", withAuth(cfg, requireRole(RoleAdmin, handleGDriveOAuthComplete())))
	mux.HandleFunc("POST /api/backups/gdrive/disconnect", withAuth(cfg, requireRole(RoleAdmin, handleGDriveDisconnect())))
	mux.HandleFunc("PUT /api/backups/gdrive/settings", withAuth(cfg, requireRole(RoleAdmin, handleGDriveUpdateSettings())))
	mux.HandleFunc("POST /api/backups/gdrive/test", withAuth(cfg, requireRole(RoleAdmin, handleGDriveTest(cfg))))
	mux.HandleFunc("POST /api/backups/gdrive/dismiss-error", withAuth(cfg, requireRole(RoleAdmin, handleGDriveDismissError())))
	mux.HandleFunc("GET /api/backups/gdrive/remote", withAuth(cfg, requireRole(RoleAdmin, handleGDriveListRemote(cfg))))
	mux.HandleFunc("POST /api/backups/gdrive/restore", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleGDriveRestore(cfg)))))

	// Incremental encrypted server snapshots (restic → selected destination)
	mux.HandleFunc("GET /api/backups/snapshot/status", withAuth(cfg, requireRole(RoleAdmin, handleSnapshotStatus(cfg))))
	mux.HandleFunc("POST /api/backups/snapshot/purge-repo", withAuth(cfg, requireRole(RoleAdmin, handleSnapshotPurgeRepo(cfg))))
	mux.HandleFunc("POST /api/backups/snapshot/run", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleSnapshotRun()))))
	mux.HandleFunc("GET /api/backups/snapshot/list", withAuth(cfg, requireRole(RoleAdmin, handleSnapshotList(cfg))))
	mux.HandleFunc("GET /api/backups/snapshot/vhosts", withAuth(cfg, requireRole(RoleAdmin, handleSnapshotVhosts())))
	mux.HandleFunc("POST /api/backups/snapshot/restore", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleSnapshotRestore(cfg)))))
	mux.HandleFunc("GET /api/backups/snapshot/settings", withAuth(cfg, requireRole(RoleAdmin, handleSnapshotSettings(cfg))))
	mux.HandleFunc("PUT /api/backups/snapshot/settings", withAuth(cfg, requireRole(RoleAdmin, handleSnapshotSettings(cfg))))

	mux.HandleFunc("GET /api/backups/download/{id}", withAuth(cfg, requireRole(RoleAdmin, handleBackupDownload())))
	mux.HandleFunc("GET /api/backups/restore/{id}/validate", withAuth(cfg, requireRole(RoleAdmin, handleBackupRestoreValidate())))
	mux.HandleFunc("POST /api/backups/restore/{id}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleBackupRestore()))))
	mux.HandleFunc("POST /api/backups/upload/{id}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleGDriveUpload(cfg)))))
	mux.HandleFunc("DELETE /api/backups/{id}", withAuth(cfg, requireRole(RoleAdmin, handleBackupDelete())))

	// Internal cron backup runner (localhost + shared secret)
	mux.HandleFunc("POST /api/internal/cron/backup", handleInternalCronBackup(cfg.CronSecret))
	mux.HandleFunc("GET /api/internal/deploy/preflight", handleInternalDeployPreflight(cfg.CronSecret))

	// Domains
	mux.HandleFunc("GET /api/domains", withAuth(cfg, handleDomainList(cfg)))
	mux.HandleFunc("GET /api/domains/provisioning", withAuth(cfg, handleDomainProvisioningCapabilities(cfg)))
	mux.HandleFunc("GET /api/domains/{id}", withAuth(cfg, handleDomainGet(cfg)))
	mux.HandleFunc("POST /api/domains/check", withAuth(cfg, requireRole(RoleManager, handleDomainCheck(cfg))))
	mux.HandleFunc("POST /api/domains", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDomainCreate(cfg, d.UptimeEngine)))))
	mux.HandleFunc("DELETE /api/domains/{id}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDomainDelete(cfg, d.UptimeEngine)))))
	mux.HandleFunc("POST /api/domains/{id}/toggle", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handleDomainToggle(cfg)))))

	// Mail (Stalwart) — service control & observability
	mux.HandleFunc("GET /api/mail/service/status", withAuth(cfg, handleMailServiceStatus(cfg)))
	mux.HandleFunc("POST /api/mail/service/{action}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleMailServiceAction(cfg)))))
	mux.HandleFunc("GET /api/mail/service/overview", withAuth(cfg, handleMailServiceOverview(cfg)))
	mux.HandleFunc("GET /api/mail/config", withAuth(cfg, handleMailConfig(cfg)))
	mux.HandleFunc("GET /api/mail/version", withAuth(cfg, handleMailVersion(cfg)))
	mux.HandleFunc("GET /api/mail/listeners", withAuth(cfg, handleMailListeners(cfg)))
	mux.HandleFunc("GET /api/mail/storage", withAuth(cfg, handleMailStorage(cfg)))
	mux.HandleFunc("GET /api/mail/logs", withAuth(cfg, handleMailLogs(cfg)))
	mux.HandleFunc("GET /api/mail/logs/search", withAuth(cfg, handleMailLogsSearch(cfg)))
	mux.HandleFunc("GET /api/mail/logs/delivery", withAuth(cfg, handleMailDeliveryLog(cfg)))

	// Mail (Stalwart) — existing account / domain management
	mux.HandleFunc("GET /api/mail/status", withAuth(cfg, handleMailStatus(cfg)))
	// Domain CRUD
	mux.HandleFunc("GET /api/mail/domains", withAuth(cfg, handleMailDomains(cfg)))
	mux.HandleFunc("POST /api/mail/domains", withAuth(cfg, requireRole(RoleManager, handleMailDomainCreate(cfg))))
	mux.HandleFunc("DELETE /api/mail/domains/{domain}", withAuth(cfg, requireRole(RoleAdmin, handleMailDomainDelete(cfg))))
	// Account CRUD
	mux.HandleFunc("GET /api/mail/accounts", withAuth(cfg, handleMailAccountList(cfg)))
	mux.HandleFunc("POST /api/mail/accounts", withAuth(cfg, requireRole(RoleManager, handleMailAccountCreate(cfg))))
	mux.HandleFunc("GET /api/mail/accounts/{email}", withAuth(cfg, handleMailAccountGet(cfg)))
	mux.HandleFunc("DELETE /api/mail/accounts/{email}", withAuth(cfg, requireRole(RoleAdmin, handleMailAccountDelete(cfg))))
	mux.HandleFunc("GET /api/mail/accounts/{email}/password", withAuth(cfg, requireRole(RoleAdmin, handleMailAccountGetPassword(cfg))))
	mux.HandleFunc("PUT /api/mail/accounts/{email}/password", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handleMailAccountPassword(cfg)))))
	mux.HandleFunc("PUT /api/mail/accounts/{email}/quota", withAuth(cfg, requireRole(RoleManager, handleMailAccountQuota(cfg))))
	// Aliases (type=list principals)
	mux.HandleFunc("GET /api/mail/aliases", withAuth(cfg, handleMailAliasList(cfg)))
	mux.HandleFunc("POST /api/mail/aliases", withAuth(cfg, requireRole(RoleManager, handleMailAliasCreate(cfg))))
	mux.HandleFunc("DELETE /api/mail/aliases/{id}", withAuth(cfg, requireRole(RoleAdmin, handleMailAliasDelete(cfg))))
	// Groups (type=group principals)
	mux.HandleFunc("GET /api/mail/groups", withAuth(cfg, handleMailGroupList(cfg)))
	mux.HandleFunc("POST /api/mail/groups", withAuth(cfg, requireRole(RoleManager, handleMailGroupCreate(cfg))))
	mux.HandleFunc("PATCH /api/mail/groups/{name}/members", withAuth(cfg, requireRole(RoleManager, handleMailGroupUpdateMembers(cfg))))
	// Queue management
	mux.HandleFunc("GET /api/mail/queue", withAuth(cfg, handleMailQueue(cfg)))
	mux.HandleFunc("POST /api/mail/queue/{id}/retry", withAuth(cfg, requireRole(RoleManager, handleMailQueueRetry(cfg))))
	mux.HandleFunc("DELETE /api/mail/queue/{id}", withAuth(cfg, requireRole(RoleAdmin, handleMailQueueDelete(cfg))))
	// DNS
	mux.HandleFunc("GET /api/mail/dns-check/{domain}", withAuth(cfg, handleMailDNSCheck(cfg)))

	// DKIM key management
	mux.HandleFunc("GET /api/mail/dkim/{domain}", withAuth(cfg, handleMailDKIMList(cfg)))
	mux.HandleFunc("POST /api/mail/dkim/{domain}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handleMailDKIMGenerate(cfg)))))
	mux.HandleFunc("GET /api/mail/dkim/{domain}/{selector}/dns", withAuth(cfg, handleMailDKIMDNS(cfg)))
	mux.HandleFunc("POST /api/mail/dkim/{domain}/rotate", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handleMailDKIMRotate(cfg)))))
	mux.HandleFunc("GET /api/mail/dkim/{domain}/config", withAuth(cfg, handleMailDKIMConfig(cfg)))

	// Mail statistics (auth required)
	mux.HandleFunc("GET /api/mail/stats", withAuth(cfg, handleMailStats(cfg)))
	mux.HandleFunc("GET /api/mail/stats/top-senders", withAuth(cfg, handleMailTopSenders(cfg)))
	mux.HandleFunc("GET /api/mail/stats/top-recipients", withAuth(cfg, handleMailTopRecipients(cfg)))
	mux.HandleFunc("GET /api/mail/stats/volume", withAuth(cfg, handleMailVolume(cfg)))
	mux.HandleFunc("GET /api/mail/stats/storage", withAuth(cfg, handleMailStorageUsage(cfg)))
	mux.HandleFunc("GET /api/mail/stats/deliverability", withAuth(cfg, handleMailDeliverability(cfg)))

	// Mail auto-discovery — NO auth (Thunderbird / Outlook call these before login)
	mux.HandleFunc("GET /mail/autoconfig/mail/config-v1.1.xml", handleMailAutoconfig(cfg))
	mux.HandleFunc("GET /.well-known/autoconfig/mail/config-v1.1.xml", handleMailAutoconfig(cfg))
	mux.HandleFunc("POST /autodiscover/autodiscover.xml", handleMailAutodiscover(cfg))

	// Mail spam filtering
	mux.HandleFunc("GET /api/mail/spam/config", withAuth(cfg, handleMailSpamConfig(cfg)))
	mux.HandleFunc("PUT /api/mail/spam/config", withAuth(cfg, requireRole(RoleAdmin, handleMailSpamConfigUpdate(cfg))))
	mux.HandleFunc("GET /api/mail/spam/blocklist", withAuth(cfg, handleMailBlocklist(cfg)))
	mux.HandleFunc("POST /api/mail/spam/blocklist", withAuth(cfg, requireRole(RoleManager, handleMailBlocklist(cfg))))
	mux.HandleFunc("DELETE /api/mail/spam/blocklist/{pattern}", withAuth(cfg, requireRole(RoleManager, handleMailBlocklistDelete(cfg))))
	mux.HandleFunc("GET /api/mail/spam/allowlist", withAuth(cfg, handleMailAllowlist(cfg)))
	mux.HandleFunc("POST /api/mail/spam/allowlist", withAuth(cfg, requireRole(RoleManager, handleMailAllowlist(cfg))))
	mux.HandleFunc("DELETE /api/mail/spam/allowlist/{pattern}", withAuth(cfg, requireRole(RoleManager, handleMailAllowlistDelete(cfg))))

	// Mail security
	mux.HandleFunc("GET /api/mail/security/tls", withAuth(cfg, handleMailTLSConfig(cfg)))
	mux.HandleFunc("GET /api/mail/security/rate-limits", withAuth(cfg, handleMailRateLimits(cfg)))
	mux.HandleFunc("PUT /api/mail/security/rate-limits", withAuth(cfg, requireRole(RoleAdmin, handleMailRateLimits(cfg))))
	mux.HandleFunc("GET /api/mail/security/failed-logins", withAuth(cfg, requireRole(RoleAdmin, handleMailFailedLogins(cfg))))
	mux.HandleFunc("GET /api/mail/security/connections", withAuth(cfg, handleMailConnectionStats(cfg)))

	// Monitoring (real-time)
	mux.HandleFunc("GET /api/monitoring/stats", withAuth(cfg, handleMonitoringStats()))
	mux.HandleFunc("GET /api/monitoring/processes", withAuth(cfg, handleMonitoringProcesses()))

	// Metrics (historical)
	if d.MetricsRepo != nil {
		mux.HandleFunc("GET /api/metrics/history", withAuth(cfg, handleMetricsHistory(d.MetricsRepo)))
		mux.HandleFunc("GET /api/metrics/processes/timestamps", withAuth(cfg, handleMetricsProcessTimestamps(d.MetricsRepo)))
		mux.HandleFunc("GET /api/metrics/processes", withAuth(cfg, handleMetricsProcesses(d.MetricsRepo)))
		mux.HandleFunc("GET /api/metrics/services/history", withAuth(cfg, handleMetricsServiceHistory(d.MetricsRepo)))
		mux.HandleFunc("GET /api/metrics/summary", withAuth(cfg, handleMetricsSummary(d.MetricsRepo)))
	}

	// Disk management (admin only)
	mux.HandleFunc("GET /api/disk/overview", withAuth(cfg, requireRole(RoleAdmin, handleDiskOverview())))
	mux.HandleFunc("GET /api/disk/io", withAuth(cfg, requireRole(RoleAdmin, handleDiskIO())))
	mux.HandleFunc("GET /api/disk/smart/{device}", withAuth(cfg, requireRole(RoleAdmin, handleDiskSmart())))
	mux.HandleFunc("GET /api/disk/list", withAuth(cfg, requireRole(RoleAdmin, handleDiskList())))
	mux.HandleFunc("GET /api/disk/dirsize", withAuth(cfg, requireRole(RoleAdmin, handleDiskDirSize())))
	mux.HandleFunc("GET /api/disk/usage", withAuth(cfg, requireRole(RoleAdmin, handleDiskUsage())))
	mux.HandleFunc("GET /api/disk/largest", withAuth(cfg, requireRole(RoleAdmin, handleDiskLargest())))
	mux.HandleFunc("GET /api/disk/analysis/status", withAuth(cfg, requireRole(RoleAdmin, handleDiskAnalysisStatus())))
	mux.HandleFunc("POST /api/disk/analysis/start", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDiskAnalysisStart()))))
	mux.HandleFunc("GET /api/disk/cleanup/scan", withAuth(cfg, requireRole(RoleAdmin, handleDiskCleanupScan())))
	mux.HandleFunc("POST /api/disk/cleanup/execute", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDiskCleanupExecute(hostSystemActions)))))
	mux.HandleFunc("GET /api/disk/mounts", withAuth(cfg, requireRole(RoleAdmin, handleDiskMounts())))

	// Terminal (WebSocket)
	mux.HandleFunc("GET /api/terminal/ws", withAuth(cfg, requireRole(RoleAdmin,
		handleTerminalWS(d.ShutdownCtx, d.AgentHub, agentTerminals))))

	// Audit log
	mux.HandleFunc("GET /api/audit", withAuth(cfg, handleAuditList()))

	// Users (admin only) — create/delete/update rate-limited to prevent enumeration.
	mux.HandleFunc("GET /api/users", withAuth(cfg, requireRole(RoleAdmin, handleUserList())))
	mux.HandleFunc("POST /api/users", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleUserCreate(cfg)))))
	mux.HandleFunc("PUT /api/users/{id}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleUserUpdate()))))
	mux.HandleFunc("DELETE /api/users/{id}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleUserDelete()))))

	// Deploy — target management
	mux.HandleFunc("GET /api/deploy/templates", withAuth(cfg, requireRole(RoleAdmin, handleDeployTemplates())))
	mux.HandleFunc("GET /api/deploy/targets", withAuth(cfg, handleDeployTargetList()))
	mux.HandleFunc("POST /api/deploy/targets", withAuth(cfg, requireRole(RoleAdmin, handleDeployTargetCreate())))
	mux.HandleFunc("POST /api/deploy/targets/{id}/staging", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployStagingCreate()))))
	mux.HandleFunc("GET /api/deploy/targets/{id}/preflight", withAuth(cfg, handleDeployTargetPreflight()))
	mux.HandleFunc("GET /api/deploy/targets/{id}/revision", withAuth(cfg, handleDeployTargetRevision()))
	mux.HandleFunc("GET /api/deploy/targets/{id}/services", withAuth(cfg, handleDeployComposeServices()))
	mux.HandleFunc("GET /api/deploy/targets/{id}/services/{service}/logs", withAuth(cfg, handleDeployComposeServiceLogs()))
	mux.HandleFunc("POST /api/deploy/targets/{id}/services/{service}/{action}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployComposeServiceAction()))))
	mux.HandleFunc("GET /api/deploy/targets/{id}/environment", withAuth(cfg, requireRole(RoleAdmin, handleDeployEnvironmentGet())))
	mux.HandleFunc("PUT /api/deploy/targets/{id}/environment", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployEnvironmentSet()))))
	mux.HandleFunc("DELETE /api/deploy/targets/{id}/environment/{key}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployEnvironmentDelete()))))
	mux.HandleFunc("GET /api/deploy/targets/{id}/domains", withAuth(cfg, handleDeployProjectDomains()))
	mux.HandleFunc("POST /api/deploy/targets/{id}/domains", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployProjectDomainCreate()))))
	mux.HandleFunc("GET /api/deploy/targets/{id}/domains/{domainId}/health", withAuth(cfg, handleDeployProjectDomainHealth()))
	mux.HandleFunc("POST /api/deploy/targets/{id}/domains/{domainId}/tls", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployProjectDomainTLSEnable()))))
	mux.HandleFunc("DELETE /api/deploy/targets/{id}/domains/{domainId}/tls", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployProjectDomainTLSDisable()))))
	mux.HandleFunc("DELETE /api/deploy/targets/{id}/domains/{domainId}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployProjectDomainDelete()))))
	mux.HandleFunc("PUT /api/deploy/targets/{id}", withAuth(cfg, requireRole(RoleAdmin, handleDeployTargetUpdate())))
	mux.HandleFunc("DELETE /api/deploy/targets/{id}", withAuth(cfg, requireRole(RoleAdmin, handleDeployTargetDelete())))

	// Deploy — provider-signed webhook (NO JWT; rate-limited separately)
	mux.HandleFunc("POST /api/deploy/webhook/{targetId}",
		webhookLimiter.Middleware(security.LimitWebhook)(handleDeployWebhook()))

	// Deploy — operations
	mux.HandleFunc("POST /api/deploy/manual/{targetId}", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployManual()))))
	mux.HandleFunc("GET /api/deploy/history", withAuth(cfg, handleDeployHistory()))
	mux.HandleFunc("GET /api/deploy/history/{id}/logs", withAuth(cfg, handleDeployRunLogs()))
	mux.HandleFunc("POST /api/deploy/rollback/{targetId}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleDeployRollback()))))

	// Cron Jobs
	mux.HandleFunc("GET /api/cron/status", withAuth(cfg, handleCronStatus()))
	mux.HandleFunc("GET /api/cron/jobs", withAuth(cfg, handleCronList()))
	mux.HandleFunc("POST /api/cron/jobs", withAuth(cfg, requireRole(RoleManager, handleCronCreate())))
	mux.HandleFunc("PUT /api/cron/jobs/{id}", withAuth(cfg, requireRole(RoleManager, handleCronUpdate())))
	mux.HandleFunc("DELETE /api/cron/jobs/{id}", withAuth(cfg, requireRole(RoleAdmin, handleCronDelete())))
	mux.HandleFunc("GET /api/cron/system", withAuth(cfg, handleCronSystemList()))

	// BIND9 DNS zone management (local nameserver) — /api/dns/ prefix
	mux.HandleFunc("GET /api/dns/zones", withAuth(cfg, handleBindZoneList()))
	mux.HandleFunc("GET /api/dns/zones/{domain}", withAuth(cfg, handleBindZoneGet()))
	mux.HandleFunc("POST /api/dns/zones", withAuth(cfg, requireRole(RoleAdmin, handleBindZoneCreate())))
	mux.HandleFunc("DELETE /api/dns/zones/{domain}", withAuth(cfg, requireRole(RoleAdmin, handleBindZoneDelete())))
	mux.HandleFunc("GET /api/dns/zones/{domain}/export", withAuth(cfg, handleBindZoneExport()))
	mux.HandleFunc("GET /api/dns/zones/{domain}/soa", withAuth(cfg, handleBindSOAGet()))
	mux.HandleFunc("PUT /api/dns/zones/{domain}/soa", withAuth(cfg, requireRole(RoleManager, handleBindSOAUpdate())))
	mux.HandleFunc("GET /api/dns/zones/{domain}/records", withAuth(cfg, handleBindRecordList()))
	mux.HandleFunc("POST /api/dns/zones/{domain}/records", withAuth(cfg, requireRole(RoleManager, handleBindRecordAdd())))
	mux.HandleFunc("PUT /api/dns/zones/{domain}/records", withAuth(cfg, requireRole(RoleManager, handleBindRecordUpdate())))
	mux.HandleFunc("DELETE /api/dns/zones/{domain}/records", withAuth(cfg, requireRole(RoleManager, handleBindRecordDelete())))
	mux.HandleFunc("POST /api/dns/reload", withAuth(cfg, requireRole(RoleManager, handleBindReload())))
	mux.HandleFunc("POST /api/dns/check", withAuth(cfg, handleBindCheck()))
	mux.HandleFunc("GET /api/dns/status", withAuth(cfg, handleBindStatus()))
	mux.HandleFunc("GET /api/dns/lookup", withAuth(cfg, handleBindDNSLookup()))

	// Log viewer
	mux.HandleFunc("GET /api/logs/sources", withAuth(cfg, handleLogSources(logSvc)))
	mux.HandleFunc("GET /api/logs/read", withAuth(cfg, handleLogRead(logSvc)))
	mux.HandleFunc("GET /api/logs/search", withAuth(cfg, handleLogSearch(logSvc)))
	mux.HandleFunc("GET /api/logs/stream", withAuth(cfg, handleLogStream(logSvc, d.ShutdownCtx)))
	mux.HandleFunc("GET /api/logs/download", withAuth(cfg, handleLogDownload(logSvc)))

	// Docker
	mux.HandleFunc("GET /api/docker/status", withAuth(cfg, handleDockerStatus()))
	mux.HandleFunc("GET /api/docker/containers", withAuth(cfg, handleDockerContainers()))
	mux.HandleFunc("GET /api/docker/containers/{id}/logs", withAuth(cfg, handleDockerContainerLogs()))
	mux.HandleFunc("POST /api/docker/containers/{id}/{action}", withAuth(cfg, requireRole(RoleAdmin, handleDockerContainerControl())))
	mux.HandleFunc("GET /api/docker/images", withAuth(cfg, handleDockerImages()))
	mux.HandleFunc("POST /api/docker/images/pull", withAuth(cfg, requireRole(RoleAdmin, handleDockerImagePull())))
	mux.HandleFunc("DELETE /api/docker/images/{id}", withAuth(cfg, requireRole(RoleAdmin, handleDockerImageDelete())))

	// Cloudflare DNS management
	mux.HandleFunc("GET /api/cloudflare/zones", withAuth(cfg, handleCFZoneList(cfg)))
	mux.HandleFunc("GET /api/cloudflare/zones/{zoneId}", withAuth(cfg, handleCFZoneGet(cfg)))
	mux.HandleFunc("GET /api/cloudflare/zones/{zoneId}/records", withAuth(cfg, handleCFRecordList(cfg)))
	mux.HandleFunc("POST /api/cloudflare/zones/{zoneId}/records", withAuth(cfg, requireRole(RoleManager, handleCFRecordCreate(cfg))))
	mux.HandleFunc("PUT /api/cloudflare/zones/{zoneId}/records/{recordId}", withAuth(cfg, requireRole(RoleManager, handleCFRecordUpdate(cfg))))
	mux.HandleFunc("DELETE /api/cloudflare/zones/{zoneId}/records/{recordId}", withAuth(cfg, requireRole(RoleAdmin, handleCFRecordDelete(cfg))))
	mux.HandleFunc("PUT /api/cloudflare/zones/{zoneId}/records/{recordId}/proxy", withAuth(cfg, requireRole(RoleManager, handleCFRecordToggleProxy(cfg))))
	mux.HandleFunc("POST /api/cloudflare/zones/{zoneId}/purge", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleCFPurgeCache(cfg)))))
	mux.HandleFunc("GET /api/cloudflare/zones/{zoneId}/email-routing", withAuth(cfg, handleCFEmailRouting(cfg)))
	mux.HandleFunc("POST /api/cloudflare/mail-autofix/{domain}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleCFMailAutoFix(cfg)))))

	// Security — score + fail2ban + IP access lists
	mux.HandleFunc("GET /api/security/score", withAuth(cfg, handleSecurityScore()))
	mux.HandleFunc("GET /api/security/fail2ban/status", withAuth(cfg, requireRole(RoleAdmin, handleFail2BanStatus())))
	mux.HandleFunc("GET /api/security/fail2ban/jails/{jail}", withAuth(cfg, requireRole(RoleAdmin, handleFail2BanJail())))
	mux.HandleFunc("POST /api/security/fail2ban/ban", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleFail2BanBan()))))
	mux.HandleFunc("POST /api/security/fail2ban/unban", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleFail2BanUnban()))))
	mux.HandleFunc("GET /api/security/ip-blacklist", withAuth(cfg, requireRole(RoleAdmin, handleIPBlacklistList(cfg))))
	mux.HandleFunc("POST /api/security/ip-blacklist", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleIPBlacklistAdd(cfg)))))
	mux.HandleFunc("DELETE /api/security/ip-blacklist/{ip}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleIPBlacklistDelete(cfg)))))
	mux.HandleFunc("GET /api/security/ip-whitelist", withAuth(cfg, requireRole(RoleAdmin, handleIPWhitelistList(cfg))))
	mux.HandleFunc("POST /api/security/ip-whitelist", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleIPWhitelistAdd(cfg)))))
	mux.HandleFunc("DELETE /api/security/ip-whitelist/{ip}", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleIPWhitelistDelete(cfg)))))

	// Health check — no auth required
	mux.HandleFunc("GET /api/health", handleHealth(d.Settings))

	// Agent hub — outbound-only managed-node control plane.
	if d.AgentHub != nil {
		mux.HandleFunc("GET /api/agent/v1/terminal", handleAgentTerminal(d.AgentHub, agentTerminals))
		mux.HandleFunc("POST /api/agent/v1/heartbeat", handleAgentHeartbeat(d.AgentHub))
		mux.HandleFunc("POST /api/agent/v1/tasks/poll", handleAgentTaskPoll(d.AgentHub))
		mux.HandleFunc("POST /api/agent/v1/tasks/{id}/result", handleAgentTaskResult(d.AgentHub))
		mux.HandleFunc("POST /api/nodes", withAuth(cfg, requireRole(RoleAdmin, handleNodeRegister(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes", withAuth(cfg, handleNodeList(d.AgentHub)))
		mux.HandleFunc("GET /api/nodes/{id}", withAuth(cfg, handleNodeGet(d.AgentHub)))
		mux.HandleFunc("GET /api/nodes/{id}/integrations/status", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeIntegrationStatus(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/metrics", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeMetrics(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/tasks", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleNodeTaskCreate(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/tasks", withAuth(cfg, requireRole(RoleManager,
			handleNodeTaskList(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/tasks/{taskID}", withAuth(cfg, requireRole(RoleManager,
			handleNodeTaskGet(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/agent-update", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteAgentUpdateStatus(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/agent-update/upgrade", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteAgentUpgrade(d.AgentHub)))))
		mux.HandleFunc("POST /api/nodes/{id}/agent-update/rollback", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteAgentRollback(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/profile", withAuth(cfg, requireRole(RoleAdmin,
			handleNodeProfileGet(d.AgentHub))))
		mux.HandleFunc("PUT /api/nodes/{id}/profile", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleNodeProfilePut(d.AgentHub)))))
		mux.HandleFunc("POST /api/nodes/{id}/profile/apply", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleNodeProfileApply(d.AgentHub)))))
		mux.HandleFunc("POST /api/nodes/{id}/actions/{action}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeAction(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/actions/reboot-status", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeRebootStatus(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/actions/status", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeActionStatus(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/processes", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeProcesses(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/memory", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeMemory(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/disk", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeDisk(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/disk/cleanup", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeDiskCleanupScan(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/disk/cleanup", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDiskCleanupExecute(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/domains", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeDomains(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/domains/{config}/actions/{action}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDomainAction(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/certificates", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeCertificates(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/certificates/{name}/actions/{action}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeCertificateAction(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/php", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodePHPFPM(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/php/{version}/pools/{pool}", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodePHPFPMConfigGet(d.AgentHub))))
		mux.HandleFunc("PUT /api/nodes/{id}/php/{version}/pools/{pool}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodePHPFPMConfigSave(d.AgentHub)))))
		mux.HandleFunc("POST /api/nodes/{id}/php/{version}/actions/{action}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodePHPFPMAction(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/pm2", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodePM2(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/pm2/{name}/logs", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodePM2Logs(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/pm2/{name}/actions/{action}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodePM2Action(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/cron", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeCronList(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/cron", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeCronCreate(d.AgentHub)))))
		mux.HandleFunc("PUT /api/nodes/{id}/cron/{job}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeCronUpdate(d.AgentHub)))))
		mux.HandleFunc("DELETE /api/nodes/{id}/cron/{job}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeCronDelete(d.AgentHub)))))
		mux.HandleFunc("POST /api/nodes/{id}/cron/{job}/run", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeCronRun(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/firewall", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeFirewallList(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/firewall", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeFirewallAdd(d.AgentHub)))))
		mux.HandleFunc("DELETE /api/nodes/{id}/firewall/{rule}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeFirewallDelete(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/databases", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeDatabases(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/databases/{engine}/actions/{action}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDatabaseAction(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/backups", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeBackups(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/backups/{plan}/run", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeBackupRun(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/deploy", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeDeployTargets(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/deploy/jobs", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeDeployJobs(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/deploy/{target}/actions/{action}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDeployAction(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/deploy/{target}/domains", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeDeployDomains(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/deploy/{target}/domains", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDeployDomainCreate(d.AgentHub)))))
		mux.HandleFunc("PUT /api/nodes/{node_id}/deploy/{target_id}/domains/{domain}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDeployDomainEnsure(d.AgentHub)))))
		mux.HandleFunc("DELETE /api/nodes/{id}/deploy/{target}/domains/{domain}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDeployDomainDelete(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/deploy/{target}/domains/{domain}/health", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeDeployDomainHealth(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/deploy/{target}/domains/{domain}/tls", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDeployDomainTLSEnable(d.AgentHub)))))
		mux.HandleFunc("DELETE /api/nodes/{id}/deploy/{target}/domains/{domain}/tls", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDeployDomainTLSAction(d.AgentHub, "tls-disable")))))
		mux.HandleFunc("POST /api/nodes/{id}/deploy/{target}/domains/{domain}/tls/renew", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeDeployDomainTLSAction(d.AgentHub, "tls-renew")))))
		mux.HandleFunc("GET /api/nodes/{id}/files", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeFiles(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/file", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeFileGet(d.AgentHub))))
		mux.HandleFunc("PUT /api/nodes/{id}/file", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeFileSave(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/logs", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeLogs(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/nginx/configs", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeNginxConfigs(d.AgentHub))))
		mux.HandleFunc("GET /api/nodes/{id}/nginx/configs/{name}", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeNginxConfigGet(d.AgentHub))))
		mux.HandleFunc("PUT /api/nodes/{id}/nginx/configs/{name}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeNginxConfigSave(d.AgentHub)))))
		mux.HandleFunc("POST /api/nodes/{id}/nginx/actions/{action}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeNginxAction(d.AgentHub)))))
		mux.HandleFunc("GET /api/nodes/{id}/containers", withAuth(cfg, requireRole(RoleAdmin,
			handleRemoteNodeContainers(d.AgentHub))))
		mux.HandleFunc("POST /api/nodes/{id}/containers/{container}/actions/{action}", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeContainerAction(d.AgentHub)))))
		mux.HandleFunc("POST /api/nodes/{id}/processes/signal", withAuth(cfg, requireRole(RoleAdmin,
			mutateLimiter.Middleware(security.LimitMutate)(handleRemoteNodeProcessSignal(d.AgentHub)))))
	}

	// System info
	mux.HandleFunc("GET /api/system/info", withAuth(cfg, handleSystemInfo(d.Settings)))
	mux.HandleFunc("GET /api/system/update", withAuth(cfg, handleReleaseUpdateCheck(releaseUpdateChecker)))
	mux.HandleFunc("GET /api/system/update/stage", withAuth(cfg, requireRole(RoleAdmin,
		handleReleaseUpdateStageStatus(releaseUpdateManager))))
	mux.HandleFunc("POST /api/system/update/stage", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleReleaseUpdateStage(releaseUpdateManager)))))
	mux.HandleFunc("POST /api/system/update/install", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handleReleaseUpdateInstall(releaseUpdateManager)))))

	// Notification channels
	mux.HandleFunc("GET /api/notify/channels", withAuth(cfg, handleNotifyChannelListWithReceipts(d.ChannelRepo, d.DeliveryRepo)))
	mux.HandleFunc("GET /api/notify/channels/{id}", withAuth(cfg, handleNotifyChannelGetWithReceipts(d.ChannelRepo, d.DeliveryRepo)))
	mux.HandleFunc("POST /api/notify/channels", withAuth(cfg, requireRole(RoleManager, handleNotifyChannelCreateWithReceipts(d.ChannelRepo, d.DeliveryRepo))))
	mux.HandleFunc("PUT /api/notify/channels/{id}", withAuth(cfg, requireRole(RoleManager, handleNotifyChannelUpdateWithReceipts(d.ChannelRepo, d.DeliveryRepo))))
	mux.HandleFunc("DELETE /api/notify/channels/{id}", withAuth(cfg, requireRole(RoleAdmin, handleNotifyChannelDelete(d.ChannelRepo))))
	mux.HandleFunc("POST /api/notify/channels/{id}/test", withAuth(cfg, requireRole(RoleManager, handleNotifyChannelTest(d.ChannelRepo, d.DeliveryRepo))))

	// Alert rules
	mux.HandleFunc("GET /api/notify/rules", withAuth(cfg, handleAlertRuleList(d.RuleRepo)))
	mux.HandleFunc("GET /api/notify/rules/{id}", withAuth(cfg, handleAlertRuleGet(d.RuleRepo)))
	mux.HandleFunc("POST /api/notify/rules", withAuth(cfg, requireRole(RoleManager, handleAlertRuleCreate(d.RuleRepo))))
	mux.HandleFunc("PUT /api/notify/rules/{id}", withAuth(cfg, requireRole(RoleManager, handleAlertRuleUpdate(d.RuleRepo))))
	mux.HandleFunc("DELETE /api/notify/rules/{id}", withAuth(cfg, requireRole(RoleAdmin, handleAlertRuleDelete(d.RuleRepo))))

	// Alert history
	mux.HandleFunc("GET /api/notify/history", withAuth(cfg, handleAlertHistoryList(d.HistoryRepo)))

	// Settings
	mux.HandleFunc("GET /api/settings", withAuth(cfg, handleSettingsGetAll(d.Settings)))
	mux.HandleFunc("GET /api/settings/portable", withAuth(cfg, requireRole(RoleAdmin, handlePortableSettingsExport(d.Settings))))
	mux.HandleFunc("POST /api/settings/portable/preview", withAuth(cfg, requireRole(RoleAdmin, handlePortableSettingsPreview(d.Settings))))
	mux.HandleFunc("POST /api/settings/portable/import", withAuth(cfg, requireRole(RoleAdmin,
		mutateLimiter.Middleware(security.LimitMutate)(handlePortableSettingsImport(d.Settings)))))
	mux.HandleFunc("GET /api/settings/{key}", withAuth(cfg, handleSettingsGet(d.Settings)))
	mux.HandleFunc("PUT /api/settings", withAuth(cfg, requireRole(RoleManager, handleSettingsSet(d.Settings))))
	mux.HandleFunc("DELETE /api/settings/{key}", withAuth(cfg, requireRole(RoleAdmin, handleSettingsDelete(d.Settings))))

	// Onboarding wizard state
	mux.HandleFunc("GET /api/onboarding", withAuth(cfg, handleOnboardingGet(d.Settings)))
	mux.HandleFunc("POST /api/onboarding", withAuth(cfg, requireRole(RoleAdmin, handleOnboardingSet(d.Settings))))

	// Uptime monitoring — monitor CRUD
	mux.HandleFunc("GET /api/uptime/monitors", withAuth(cfg, handleUptimeMonitorList(d.UptimeEngine)))
	mux.HandleFunc("POST /api/uptime/monitors", withAuth(cfg, requireRole(RoleManager, handleUptimeMonitorCreate(d.UptimeEngine, d.Settings))))
	// Literal sub-paths MUST be registered before {id} wildcard routes.
	mux.HandleFunc("POST /api/uptime/monitors/bulk-from-domains", withAuth(cfg, requireRole(RoleAdmin, handleUptimeBulkFromDomains(cfg, d.UptimeEngine, d.Settings))))
	mux.HandleFunc("GET /api/uptime/monitor-by-domain/{domain}", withAuth(cfg, handleUptimeMonitorByDomain(d.UptimeEngine)))
	mux.HandleFunc("POST /api/uptime/monitors/test", withAuth(cfg, requireRole(RoleManager, handleUptimeMonitorTest(d.Settings))))
	mux.HandleFunc("GET /api/uptime/monitors/summary", withAuth(cfg, handleUptimeSummary(d.UptimeEngine)))
	mux.HandleFunc("GET /api/uptime/monitors/{id}", withAuth(cfg, handleUptimeMonitorGet(d.UptimeEngine)))
	mux.HandleFunc("PUT /api/uptime/monitors/{id}", withAuth(cfg, requireRole(RoleManager, handleUptimeMonitorUpdate(d.UptimeEngine))))
	mux.HandleFunc("DELETE /api/uptime/monitors/{id}", withAuth(cfg, requireRole(RoleAdmin, handleUptimeMonitorDelete(d.UptimeEngine))))
	mux.HandleFunc("POST /api/uptime/monitors/{id}/pause", withAuth(cfg, requireRole(RoleManager, handleUptimeMonitorPause(d.UptimeEngine))))
	mux.HandleFunc("POST /api/uptime/monitors/{id}/resume", withAuth(cfg, requireRole(RoleManager, handleUptimeMonitorResume(d.UptimeEngine))))
	mux.HandleFunc("POST /api/uptime/monitors/{id}/check-now", withAuth(cfg, requireRole(RoleManager,
		mutateLimiter.Middleware(security.LimitMutate)(handleUptimeMonitorCheckNow(d.UptimeEngine)))))

	// Uptime monitoring — stats & data
	mux.HandleFunc("GET /api/uptime/monitors/{id}/heartbeats", withAuth(cfg, handleUptimeHeartbeats(d.UptimeEngine)))
	mux.HandleFunc("GET /api/uptime/monitors/{id}/uptime", withAuth(cfg, handleUptimeUptime(d.UptimeEngine)))
	mux.HandleFunc("GET /api/uptime/monitors/{id}/incidents", withAuth(cfg, handleUptimeIncidents(d.UptimeEngine)))
	mux.HandleFunc("GET /api/uptime/incidents", withAuth(cfg, handleUptimeIncidents(d.UptimeEngine)))

	// Uptime monitoring — settings
	mux.HandleFunc("GET /api/uptime/settings", withAuth(cfg, handleUptimeSettingsGet(d.Settings)))
	mux.HandleFunc("PUT /api/uptime/settings", withAuth(cfg, requireRole(RoleAdmin, handleUptimeSettingsSet(d.Settings))))

	// Uptime monitoring — status pages (admin)
	mux.HandleFunc("GET /api/uptime/status-pages", withAuth(cfg, handleUptimeStatusPageList(d.UptimeEngine)))
	mux.HandleFunc("POST /api/uptime/status-pages", withAuth(cfg, requireRole(RoleManager, handleUptimeStatusPageCreate(d.UptimeEngine))))
	mux.HandleFunc("PUT /api/uptime/status-pages/{id}", withAuth(cfg, requireRole(RoleManager, handleUptimeStatusPageUpdate(d.UptimeEngine))))
	mux.HandleFunc("DELETE /api/uptime/status-pages/{id}", withAuth(cfg, requireRole(RoleAdmin, handleUptimeStatusPageDelete(d.UptimeEngine))))

	// Uptime monitoring — public status pages (NO auth — registered before SPA catch-all)
	mux.HandleFunc("GET /status/{slug}", handlePublicStatusPage(d.UptimeEngine))
	mux.HandleFunc("GET /api/status/{slug}", handlePublicStatusAPI(d.UptimeEngine))

	// Serve embedded React SPA for all non-API routes
	fileServer := http.FileServer(http.FS(webFS))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Never intercept API routes
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		// Try to serve static file first (assets, fonts, icons)
		if r.URL.Path != "/" {
			f, err := webFS.Open(r.URL.Path[1:])
			if err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
			// Static assets that don't exist should 404, not fallback to index.html
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				http.NotFound(w, r)
				return
			}
		}
		// Fallback to index.html for SPA routing (client-side routes)
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	return withCORS(withBodyLimit(withLogging(withBoundedOperationWriteDeadline(mux))))
}
