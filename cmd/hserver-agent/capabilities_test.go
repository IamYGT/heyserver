package main

import (
	"reflect"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestAdvertisedCapabilitiesFollowConfiguredAllowlist(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		want []string
	}{
		{
			name: "inventory only",
			cfg:  config{},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead},
		},
		{
			name: "observed services are read only",
			cfg:  config{observedServices: []string{"nginx.service"}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityServiceStatus},
		},
		{
			name: "allowed services enable actions and status",
			cfg:  config{allowedServices: map[string]struct{}{"nginx.service": {}}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityServiceStatus, agenthub.CapabilityServiceAction},
		},
		{
			name: "host actions require explicit allowlist",
			cfg:  config{allowedHostActions: map[string]struct{}{"swap-reset": {}}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityHostAction},
		},
		{
			name: "process signals require explicit toggle",
			cfg:  config{allowProcessSignals: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityProcessSignal},
		},
		{
			name: "terminal requires explicit toggle",
			cfg:  config{allowTerminal: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityTerminal},
		},
		{
			name: "disk cleanup follows local allowlist",
			cfg:  config{allowedDiskCleanup: map[string]struct{}{"journal": {}}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityDiskCleanup},
		},
		{
			name: "log reading follows local allowlist",
			cfg:  config{allowedLogSources: map[string]struct{}{"system": {}}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityLogsRead},
		},
		{
			name: "container controls follow local opt-ins",
			cfg:  config{allowContainerRead: true, allowedContainerActions: map[string]struct{}{"restart": {}}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityContainerRead, agenthub.CapabilityContainerAction},
		},
		{
			name: "Nginx actions follow local allowlist",
			cfg:  config{allowedNginxActions: map[string]struct{}{"reload": {}}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityNginxAction},
		},
		{
			name: "Nginx config read and write follow local opt-ins",
			cfg:  config{allowNginxConfigRead: true, allowNginxConfigWrite: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityNginxConfigRead, agenthub.CapabilityNginxConfigWrite},
		},
		{
			name: "PHP-FPM controls follow local opt-ins",
			cfg:  config{allowPHPConfigRead: true, allowPHPConfigWrite: true, allowedPHPActions: map[string]struct{}{"restart": {}}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityPHPRead, agenthub.CapabilityPHPWrite, agenthub.CapabilityPHPAction},
		},
		{
			name: "PM2 controls follow local opt-ins",
			cfg:  config{allowPM2Read: true, allowedPM2Actions: map[string]struct{}{"restart": {}}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityPM2Read, agenthub.CapabilityPM2Action},
		},
		{
			name: "cron controls follow local opt-ins",
			cfg:  config{allowCronRead: true, allowCronWrite: true, allowCronRun: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityCronRead, agenthub.CapabilityCronWrite, agenthub.CapabilityCronRun},
		},
		{
			name: "firewall controls follow local opt-ins",
			cfg:  config{allowFirewallRead: true, allowFirewallWrite: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityFirewallRead, agenthub.CapabilityFirewallWrite},
		},
		{
			name: "domain controls follow local opt-ins",
			cfg:  config{allowDomainRead: true, allowDomainActions: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityDomainRead, agenthub.CapabilityDomainAction},
		},
		{
			name: "SSL controls follow local opt-ins",
			cfg:  config{allowSSLRead: true, allowSSLActions: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilitySSLRead, agenthub.CapabilitySSLAction},
		},
		{
			name: "database controls follow local opt-ins",
			cfg:  config{allowDatabaseRead: true, allowedDatabaseRestarts: map[string]struct{}{"mariadb": {}}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityDatabaseRead, agenthub.CapabilityDatabaseAction},
		},
		{
			name: "backup controls follow local opt-ins",
			cfg:  config{allowBackupRead: true, allowBackupRun: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityBackupRead, agenthub.CapabilityBackupRun},
		},
		{
			name: "file controls follow local roots",
			cfg:  config{fileReadRoots: []string{"/srv/apps"}, fileWriteRoots: []string{"/srv/apps"}},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityFilesRead, agenthub.CapabilityFilesWrite},
		},
		{
			name: "deploy controls follow local opt-ins",
			cfg:  config{allowDeployRead: true, allowDeployActions: true, allowDeployDomainRead: true, allowDeployDomainActions: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityDeployRead, agenthub.CapabilityDeployAction, agenthub.CapabilityDeployDomainRead, agenthub.CapabilityDeployDomainAction},
		},
		{
			name: "agent lifecycle follows local opt-ins",
			cfg:  config{allowAgentUpdateRead: true, allowAgentUpdateActions: true},
			want: []string{agenthub.CapabilityInventory, agenthub.CapabilityProcessRead, agenthub.CapabilityAgentUpdateRead, agenthub.CapabilityAgentUpdateAction},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := append(append([]string(nil), test.want...), agenthub.CapabilityIntegrationStatus, agenthub.CapabilityMetricsRead)
			if got := advertisedCapabilities(test.cfg); !reflect.DeepEqual(got, want) {
				t.Fatalf("advertisedCapabilities() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestConfiguredManagedFileRootsAreReportedWithoutAliasingConfig(t *testing.T) {
	cfg := config{fileReadRoots: []string{"/srv/apps", "/etc/nginx"}, fileWriteRoots: []string{"/srv/apps"}}
	readRoots, writeRoots := configuredManagedFileRoots(cfg)
	if !reflect.DeepEqual(readRoots, cfg.fileReadRoots) || !reflect.DeepEqual(writeRoots, cfg.fileWriteRoots) {
		t.Fatalf("managed file roots = read:%#v write:%#v", readRoots, writeRoots)
	}
	readRoots[0] = "/changed"
	writeRoots[0] = "/changed"
	if cfg.fileReadRoots[0] != "/srv/apps" || cfg.fileWriteRoots[0] != "/srv/apps" {
		t.Fatal("reported roots mutated agent configuration")
	}
}
