package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	deployservice "github.com/IamYGT/heyserver/internal/services/deploy"
	"github.com/IamYGT/heyserver/internal/services/systemactions"
)

var agentVersion = "dev"

const httpRequestTimeout = 20 * time.Second

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("hserver-agent %s\n", agentVersion)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("configuration rejected", "error", err)
		os.Exit(1)
	}

	hostname, err := os.Hostname()
	if err != nil {
		logger.Error("hostname unavailable", "error", err)
		os.Exit(1)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.ExpectContinueTimeout = time.Second
	client := hubClient{
		baseURL: cfg.hubURL,
		nodeID:  cfg.nodeID,
		token:   cfg.token,
		http:    &http.Client{Transport: transport, Timeout: httpRequestTimeout},
	}
	services := serviceController{runner: execRunner{}}
	collector := newInventoryCollector(services)
	executor := newTaskExecutor(services, systemactions.New(), newDiskCleanupController(execRunner{}, cfg.allowedDiskCleanup), newJournalReader(execRunner{}, cfg.allowedLogSources), cfg.observedServices, cfg.allowedServices, cfg.allowedHostActions, cfg.allowProcessSignals)
	executor.metrics = newMetricsCollector()
	executor.profileApply = newProfileApplyController(cfg.agentStateDir, cfg.agentLifecycleInstaller, cfg.systemdRunBinary, execRunner{})
	containers := newContainerController(execRunner{}, cfg.allowContainerRead, cfg.allowedContainerActions)
	executor.containers = containers
	executor.integrationDocker = containers.Probe
	nginx := newNginxController(execRunner{}, cfg.allowedNginxActions, cfg.allowNginxConfigRead, cfg.allowNginxConfigWrite, cfg.nginxSitesAvailable, cfg.nginxSitesEnabled)
	executor.nginx = nginx
	executor.domains = newDomainController(nginx, cfg.allowDomainRead, cfg.allowDomainActions)
	executor.ssl = newSSLController(execRunner{}, nginx, cfg.allowSSLRead, cfg.allowSSLActions, cfg.certificateConfigDir, cfg.certificateWorkDir, cfg.certificateLogsDir, cfg.certbotBinary, cfg.openSSLBinary, cfg.caBundle)
	executor.databases = newDatabaseController(execRunner{}, cfg.allowDatabaseRead, cfg.allowedDatabaseRestarts, cfg.mariaDBBinary, cfg.mariaDBAdminBinary, cfg.pgClustersBinary, cfg.psqlBinary, cfg.pgIsReadyBinary, cfg.runuserBinary)
	executor.backups = newBackupController(execRunner{}, cfg.allowBackupRead, cfg.allowBackupRun, cfg.backupPlansPath)
	deploys := newDeployController(execDeployProcessRunner{}, cfg.allowDeployRead, cfg.allowDeployActions, cfg.deployPlansPath)
	executor.deploys = deploys
	projectDomains := newDeployDomainController(
		deploys,
		deployservice.NewNginxProjectDomainRuntimeWithCertbotStorage(cfg.nginxSitesAvailable, cfg.nginxSitesEnabled, cfg.certbotBinary, cfg.certificateConfigDir, cfg.certificateWorkDir, cfg.certificateLogsDir, cfg.deployACMEWebroot),
		cfg.allowDeployDomainRead, cfg.allowDeployDomainActions, cfg.nginxSitesAvailable, cfg.nginxSitesEnabled, nginx.mu,
	)
	executor.deployDomains = projectDomains
	if cfg.allowAgentUpdateRead {
		agentUpdates := newAgentUpdateController(agentVersion, cfg.agentUpdateManifestURL, cfg.agentUpdatePublicKeys, cfg.agentStateDir, cfg.agentLifecycleInstaller, cfg.systemdRunBinary, cfg.systemctlBinary, execRunner{})
		executor.agentUpdates = agentUpdates
		executor.allowAgentUpdates = cfg.allowAgentUpdateActions
	}
	executor.files = newFileController(cfg.fileReadRoots, cfg.fileWriteRoots)
	executor.php = newPHPController(execRunner{}, cfg.allowedPHPActions, cfg.allowPHPConfigRead, cfg.allowPHPConfigWrite, cfg.phpConfigRoot, cfg.phpBinaryRoot)
	pm2 := newPM2Controller(execRunner{}, cfg.allowPM2Read, cfg.allowedPM2Actions, cfg.pm2Binary, cfg.pm2Home, cfg.pm2User)
	executor.pm2 = pm2
	executor.integrationPM2 = pm2.Probe
	executor.cron = newCronController(execRunner{}, cfg.allowCronRead, cfg.allowCronWrite, cfg.allowCronRun, cfg.cronStatePath, cfg.cronFilePath, cfg.cronLockPath, cfg.crontabBinary, cfg.runuserBinary, cfg.cronShell, cfg.cronService)
	executor.firewall = newFirewallController(execRunner{}, cfg.allowFirewallRead, cfg.allowFirewallWrite, cfg.iptablesBinary, cfg.firewallSaveBinary, cfg.firewallLockPath, cfg.firewallService, cfg.firewallProtectedSources, cfg.firewallProtectedPorts)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("agent started", "node_id", cfg.nodeID, "interval", cfg.interval.String())
	if cfg.allowTerminal {
		go runTerminalConnector(ctx, logger, cfg)
	}
	if cfg.allowDeployDomainRead && cfg.allowDeployDomainActions {
		go runDeployDomainTLSMaintenance(ctx, logger, projectDomains, 12*time.Hour)
	}
	run(ctx, logger, cfg, hostname, collector, executor, client)
	logger.Info("agent stopped")
}

type agentClient interface {
	heartbeat(context.Context, agenthub.HeartbeatRequest) error
	poll(context.Context) (*agenthub.Task, error)
	report(context.Context, int64, agenthub.TaskResultRequest) error
}

func run(ctx context.Context, logger *slog.Logger, cfg config, hostname string, collector inventoryCollector, executor taskExecutor, client agentClient) {
	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()
	for {
		runCycle(ctx, logger, cfg, hostname, collector, executor, client)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runCycle(ctx context.Context, logger *slog.Logger, cfg config, hostname string, collector inventoryCollector, executor taskExecutor, client agentClient) {
	inv, inventoryErr := collector.collect(ctx, cfg.observedServices)
	if inventoryErr != nil {
		logger.Warn("inventory partially collected", "error", inventoryErr)
	}
	inv.LogSources = configuredLogSources(cfg.allowedLogSources)
	inv.FileReadRoots, inv.FileWriteRoots = configuredManagedFileRoots(cfg)
	heartbeat := agenthub.HeartbeatRequest{
		ProtocolVersion: agenthub.ProtocolVersion,
		NodeID:          cfg.nodeID,
		AgentVersion:    agentVersion,
		Capabilities:    advertisedCapabilities(cfg),
		Hostname:        hostname,
		SentAt:          time.Now().UTC(),
		Inventory:       inv,
	}
	if profileApplyReady(cfg) {
		observation := cfg.profileObservation
		if observer, ok := executor.profileApply.(interface{ observation() profileObservation }); ok {
			observation = observer.observation()
		}
		attachProfileObservation(&heartbeat, observation)
	}
	if err := client.heartbeat(ctx, heartbeat); err != nil {
		logger.Error("heartbeat failed", "error", err)
	}

	task, err := client.poll(ctx)
	if err != nil {
		logger.Error("task poll failed", "error", err)
		return
	}
	if task == nil {
		return
	}
	result := executor.execute(ctx, task)
	if err := client.report(ctx, task.ID, result); err != nil {
		logger.Error("task result report failed", "task_id", task.ID, "kind", task.Kind, "error", err)
		return
	}
	logger.Info("task completed", "task_id", task.ID, "kind", task.Kind, "status", result.Status)
}

func configuredManagedFileRoots(cfg config) ([]string, []string) {
	return append([]string(nil), cfg.fileReadRoots...), append([]string(nil), cfg.fileWriteRoots...)
}

func configuredLogSources(allowed map[string]struct{}) []string {
	sources := make([]string, 0, len(allowed))
	for source := range allowed {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	return sources
}

func advertisedCapabilities(cfg config) []string {
	capabilities := []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead}
	if len(cfg.observedServices) > 0 || len(cfg.allowedServices) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityServiceStatus)
	}
	if len(cfg.allowedServices) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityServiceAction)
	}
	if len(cfg.allowedHostActions) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityHostAction)
	}
	if cfg.allowProcessSignals {
		capabilities = append(capabilities, agenthub.CapabilityProcessSignal)
	}
	if cfg.allowTerminal {
		capabilities = append(capabilities, agenthub.CapabilityTerminal)
	}
	if len(cfg.allowedDiskCleanup) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityDiskCleanup)
	}
	if len(cfg.allowedLogSources) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityLogsRead)
	}
	if cfg.allowContainerRead {
		capabilities = append(capabilities, agenthub.CapabilityContainerRead)
	}
	if len(cfg.allowedContainerActions) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityContainerAction)
	}
	if len(cfg.allowedNginxActions) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityNginxAction)
	}
	if cfg.allowNginxConfigRead {
		capabilities = append(capabilities, agenthub.CapabilityNginxConfigRead)
	}
	if cfg.allowNginxConfigWrite {
		capabilities = append(capabilities, agenthub.CapabilityNginxConfigWrite)
	}
	if cfg.allowPHPConfigRead {
		capabilities = append(capabilities, agenthub.CapabilityPHPRead)
	}
	if cfg.allowPHPConfigWrite {
		capabilities = append(capabilities, agenthub.CapabilityPHPWrite)
	}
	if len(cfg.allowedPHPActions) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityPHPAction)
	}
	if cfg.allowPM2Read {
		capabilities = append(capabilities, agenthub.CapabilityPM2Read)
	}
	if len(cfg.allowedPM2Actions) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityPM2Action)
	}
	if cfg.allowCronRead {
		capabilities = append(capabilities, agenthub.CapabilityCronRead)
	}
	if cfg.allowCronWrite {
		capabilities = append(capabilities, agenthub.CapabilityCronWrite)
	}
	if cfg.allowCronRun {
		capabilities = append(capabilities, agenthub.CapabilityCronRun)
	}
	if cfg.allowFirewallRead {
		capabilities = append(capabilities, agenthub.CapabilityFirewallRead)
	}
	if cfg.allowFirewallWrite {
		capabilities = append(capabilities, agenthub.CapabilityFirewallWrite)
	}
	if cfg.allowDomainRead {
		capabilities = append(capabilities, agenthub.CapabilityDomainRead)
	}
	if cfg.allowDomainActions {
		capabilities = append(capabilities, agenthub.CapabilityDomainAction)
	}
	if cfg.allowSSLRead {
		capabilities = append(capabilities, agenthub.CapabilitySSLRead)
	}
	if cfg.allowSSLActions {
		capabilities = append(capabilities, agenthub.CapabilitySSLAction)
	}
	if cfg.allowDatabaseRead {
		capabilities = append(capabilities, agenthub.CapabilityDatabaseRead)
	}
	if len(cfg.allowedDatabaseRestarts) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityDatabaseAction)
	}
	if cfg.allowBackupRead {
		capabilities = append(capabilities, agenthub.CapabilityBackupRead)
	}
	if cfg.allowBackupRun {
		capabilities = append(capabilities, agenthub.CapabilityBackupRun)
	}
	if len(cfg.fileReadRoots) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityFilesRead)
	}
	if len(cfg.fileWriteRoots) > 0 {
		capabilities = append(capabilities, agenthub.CapabilityFilesWrite)
	}
	if cfg.allowDeployRead {
		capabilities = append(capabilities, agenthub.CapabilityDeployRead)
	}
	if cfg.allowDeployActions {
		capabilities = append(capabilities, agenthub.CapabilityDeployAction)
	}
	if cfg.allowDeployDomainRead {
		capabilities = append(capabilities, agenthub.CapabilityDeployDomainRead)
	}
	if cfg.allowDeployDomainActions {
		capabilities = append(capabilities, agenthub.CapabilityDeployDomainAction)
	}
	if cfg.allowAgentUpdateRead {
		capabilities = append(capabilities, agenthub.CapabilityAgentUpdateRead)
	}
	if cfg.allowAgentUpdateActions {
		capabilities = append(capabilities, agenthub.CapabilityAgentUpdateAction)
	}
	if profileApplyReady(cfg) {
		capabilities = append(capabilities, profileApplyCapability)
	}
	capabilities = append(capabilities, agenthub.CapabilityIntegrationStatus, agenthub.CapabilityMetricsRead)
	return capabilities
}
