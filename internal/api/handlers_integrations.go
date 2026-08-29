package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/extensions"
	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/integrationstate"
	bindsvc "github.com/IamYGT/heyserver/internal/services/bind"
	"github.com/IamYGT/heyserver/internal/services/cloudflare"
	databasesvc "github.com/IamYGT/heyserver/internal/services/database"
	disksvc "github.com/IamYGT/heyserver/internal/services/disk"
	"github.com/IamYGT/heyserver/internal/services/dockerctl"
	"github.com/IamYGT/heyserver/internal/services/firewall"
	"github.com/IamYGT/heyserver/internal/services/gdrive"
	"github.com/IamYGT/heyserver/internal/services/integrationstatus"
	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
	"github.com/IamYGT/heyserver/internal/services/mailaccess"
	nginxsvc "github.com/IamYGT/heyserver/internal/services/nginx"
	"github.com/IamYGT/heyserver/internal/services/notify"
	phpsvc "github.com/IamYGT/heyserver/internal/services/php"
	"github.com/IamYGT/heyserver/internal/services/pm2"
	"github.com/IamYGT/heyserver/internal/services/snapshot"
	sslsvc "github.com/IamYGT/heyserver/internal/services/ssl"
)

// handleIntegrationCatalog returns the provider-neutral integration metadata
// catalog. It deliberately reports catalog data only; installation secrets and
// live integration health belong to their respective protected APIs.
func handleIntegrationCatalog() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		catalog, err := extensions.LoadCatalog()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "integration catalog unavailable")
			return
		}
		jsonResponse(w, http.StatusOK, catalog)
	}
}

// newIntegrationStatusService wires the fifteen implemented local, read-only
// probes plus any explicitly injected, code-owned additive probes. The
// aggregate package derives every other catalog ID as unprobed; no
// installation-specific metadata or provider response is copied into the
// status payload.
func newIntegrationStatusService(cfg *config.Config, dependencies ...*Deps) *integrationstatus.Service {
	return newIntegrationStatusServiceWithCatalog(cfg, extensions.LoadCatalog, dependencies...)
}

// newIntegrationStatusServiceWithCatalog keeps the catalog loader injectable
// for focused constructor tests while production always uses the embedded
// extensions catalog through newIntegrationStatusService. Probe definitions
// remain explicit Go values; catalog metadata never selects executable code.
func newIntegrationStatusServiceWithCatalog(cfg *config.Config, loader integrationstatus.CatalogLoader, dependencies ...*Deps) *integrationstatus.Service {
	var deps *Deps
	if len(dependencies) > 0 {
		deps = dependencies[0]
	}
	var driveService *gdrive.Service
	var snapshotService *snapshot.Service
	if deps != nil {
		driveService = deps.GDrive
		snapshotService = deps.Snapshot
	}
	var pm2Config pm2.Config
	var cloudflareToken, cloudflareEmail string
	var nginxConfig nginxsvc.ServiceConfig
	var phpConfig phpsvc.ServiceConfig
	if cfg != nil {
		pm2Config = pm2.Config{
			User:         cfg.PM2User,
			Home:         cfg.PM2Home,
			Bin:          cfg.PM2Bin,
			AllowedRoots: cfg.PM2AllowedRoots,
		}
		cloudflareToken = strings.TrimSpace(cfg.CloudflareAPIToken)
		cloudflareEmail = strings.TrimSpace(cfg.CloudflareAPIEmail)
		nginxConfig = nginxsvc.ServiceConfig{
			SitesAvailable: cfg.NginxSitesAvailable,
			SitesEnabled:   cfg.NginxSitesEnabled,
			VhostsRoot:     cfg.VhostsRoot,
			SnippetsDir:    cfg.NginxSnippetsDir,
		}
		phpConfig = phpsvc.ServiceConfig{
			ConfigRoot: cfg.PHPConfigRoot,
			BinaryRoot: cfg.PHPBinaryRoot,
		}
	}

	pm2Service, pm2Err := pm2.New(pm2Config)
	pm2Probe := func(ctx context.Context) (integrationstate.State, error) {
		if pm2Err != nil {
			return pm2.ClassifyInventoryError(pm2Err), pm2Err
		}
		inventory, err := pm2Service.ProbeProcessesContext(ctx)
		return inventory.State, err
	}

	cloudflareService := cloudflare.New(cloudflareToken, cloudflareEmail)
	cloudflareProbe := func(ctx context.Context) (integrationstate.State, error) {
		inventory, err := cloudflareService.ProbeZonesContext(ctx)
		return inventory.State, err
	}

	dockerService := dockerctl.New()
	dockerProbe := func(ctx context.Context) (integrationstate.State, error) {
		return dockerService.ProbeInfoContext(ctx)
	}

	nginxService := nginxsvc.NewWithConfig(nginxConfig)
	nginxProbe := func(ctx context.Context) (integrationstate.State, error) {
		return nginxService.ProbeReadinessContext(ctx)
	}
	ufwProbe := func(ctx context.Context) (integrationstate.State, error) {
		return firewall.ProbeContext(ctx)
	}
	certbotProbe := func(ctx context.Context) (integrationstate.State, error) {
		return sslsvc.ProbeContext(ctx)
	}
	bindService := bindsvc.New()
	bindProbe := func(ctx context.Context) (integrationstate.State, error) {
		return bindService.ProbeContext(ctx)
	}
	phpService := phpsvc.NewWithConfig(phpConfig)
	phpProbe := func(ctx context.Context) (integrationstate.State, error) {
		return phpService.ProbeReadinessContext(ctx)
	}
	databaseProbe := func(ctx context.Context) (integrationstate.State, error) {
		return databasesvc.ProbeReadinessContext(ctx)
	}
	smartProbe := func(ctx context.Context) (integrationstate.State, error) {
		return disksvc.ProbeRootSmartContext(ctx)
	}
	stalwartProbe := func(ctx context.Context) (integrationstate.State, error) {
		if cfg == nil {
			return integrationstate.NotConfigured, mailsvc.ErrNotConfigured
		}
		return newMailService(cfg).ProbeReadinessContext(ctx)
	}
	mailAccessProbe := func(ctx context.Context) (integrationstate.State, error) {
		if deps == nil || deps.Settings == nil {
			return integrationstate.NotConfigured, mailaccess.ErrNotConfigured
		}
		values, err := deps.Settings.EditableSettingsContext(ctx)
		if err != nil {
			return integrationstate.Unavailable, mailaccess.ErrUnavailable
		}
		return mailaccess.ProbeReadinessContext(ctx, values)
	}
	gdriveProbe := func(ctx context.Context) (integrationstate.State, error) {
		if driveService == nil {
			return integrationstate.NotConfigured, nil
		}
		return driveService.ProbeReadinessContext(ctx)
	}
	resticProbe := func(ctx context.Context) (integrationstate.State, error) {
		if snapshotService == nil {
			return integrationstate.NotConfigured, nil
		}
		return snapshotService.ProbeReadinessContext(ctx)
	}
	notificationProbe := func(ctx context.Context) (integrationstate.State, error) {
		if err := ctx.Err(); err != nil {
			return integrationstate.Unavailable, err
		}
		if deps == nil || deps.ChannelRepo == nil {
			return integrationstate.NotConfigured, nil
		}
		channels, err := deps.ChannelRepo.ListContext(ctx)
		if err != nil {
			return integrationstate.Unavailable, err
		}
		receipts, err := notificationDeliveryReceiptMap(ctx, deps.DeliveryRepo)
		if err != nil {
			return integrationstate.Unavailable, err
		}
		return notify.ChannelsAvailabilityWithReceipts(channels, receipts, time.Now().UTC()).State, nil
	}

	probes := []integrationstatus.Probe{
		{ID: integrationstatus.ProcessPM2ID, Run: pm2Probe},
		{ID: integrationstatus.CloudflareDNSID, Run: cloudflareProbe},
		{ID: integrationstatus.DockerID, Run: dockerProbe},
		{ID: integrationstatus.NginxID, Run: nginxProbe},
		{ID: integrationstatus.FirewallUFWID, Run: ufwProbe},
		{ID: integrationstatus.CertbotTLSID, Run: certbotProbe},
		{ID: integrationstatus.Bind9DNSID, Run: bindProbe},
		{ID: integrationstatus.PHPFPMRuntimeID, Run: phpProbe},
		{ID: integrationstatus.DatabaseLocalID, Run: databaseProbe},
		{ID: integrationstatus.SmartmontoolsID, Run: smartProbe},
		{ID: integrationstatus.StalwartMailID, Run: stalwartProbe},
		{ID: integrationstatus.MailAccessID, Run: mailAccessProbe},
		{ID: integrationstatus.GDriveBackupID, Run: gdriveProbe},
		{ID: integrationstatus.ResticSnapshotID, Run: resticProbe},
		{ID: integrationstatus.NotificationDeliveryID, Run: notificationProbe},
	}
	if deps != nil {
		probes = append(probes, deps.IntegrationStatusProbes...)
	}
	return integrationstatus.NewWithCatalog(loader, probes...)
}

// handleIntegrationStatus returns the fresh local integration observation.
// Individual provider failures are represented as HTTP 200 item results by
// integrationstatus.Service.  Only an unusable embedded catalog is a 500, and
// its implementation error is deliberately not exposed to the caller.
func handleIntegrationStatus(service *integrationstatus.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			jsonError(w, http.StatusInternalServerError, "integration status unavailable")
			return
		}
		status, err := service.Status(r.Context())
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "integration status unavailable")
			return
		}
		jsonResponse(w, http.StatusOK, status)
	}
}
