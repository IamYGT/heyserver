package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/models"
)

type tuiPaletteItemKind int

const (
	tuiPaletteNavigate tuiPaletteItemKind = iota
	tuiPaletteTarget
	tuiPaletteOperation
	tuiPaletteUserCreate
)

type tuiPaletteItem struct {
	Kind        tuiPaletteItemKind
	Label       string
	Description string
	Keywords    string
	Tab         tuiTab
	TargetID    string
	Operation   tuiOperation
}

func (model *tuiModel) openPalette() {
	items := model.buildPaletteItems()
	model.dialog = tuiDialog{
		Mode: tuiDialogPalette, Title: "Quick actions", PaletteItems: items,
	}
}

func (model tuiModel) buildPaletteItems() []tuiPaletteItem {
	items := make([]tuiPaletteItem, 0, len(tuiTabLabels)+len(model.snapshot.Targets)+20)
	for index, label := range tuiTabLabels {
		items = append(items, tuiPaletteItem{
			Kind: tuiPaletteNavigate, Label: "Open " + label,
			Description: fmt.Sprintf("Go to section %d of %d", index+1, len(tuiTabLabels)),
			Keywords:    "navigate section tab", Tab: tuiTab(index),
		})
	}
	for _, target := range model.snapshot.Targets {
		state := "online"
		if !target.Local && !target.Online {
			state = "offline"
		}
		items = append(items, tuiPaletteItem{
			Kind: tuiPaletteTarget, Label: "Switch to " + target.label(),
			Description: state + " server", Keywords: "server node target " + target.Hostname,
			TargetID: target.ID,
		})
	}

	target := model.snapshot.Selected
	if targetAllows(target, agenthub.CapabilityHostAction) {
		for _, action := range tuiMaintenanceActions {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: action.Label,
				Description: action.Description, Keywords: "host maintenance " + action.ID,
				Operation: tuiOperation{
					Kind: tuiOperationHost, Target: target, Action: action.ID,
					Label: action.Label, Dangerous: action.Dangerous,
				},
			})
		}
	}
	if targetAllows(target, agenthub.CapabilityNginxAction) {
		for _, action := range []struct {
			id    string
			label string
			desc  string
		}{
			{id: "test", label: "Test Nginx configuration", desc: "Validate the active Nginx configuration"},
			{id: "reload", label: "Reload Nginx", desc: "Test and reload the Nginx service"},
		} {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: action.label, Description: action.desc, Keywords: "web nginx " + action.id,
				Operation: tuiOperation{
					Kind: tuiOperationWeb, Target: target, Action: action.id, Label: action.label,
					WebResource: tuiWebResource{Kind: tuiWebNginx, ID: "nginx", Name: "Nginx"},
				},
			})
		}
	}
	if target.Local {
		items = append(items, tuiPaletteItem{
			Kind: tuiPaletteOperation, Label: "Save PM2 process list",
			Description: "Persist the local PM2 process list for reboot recovery", Keywords: "pm2 save persist",
			Operation: tuiOperation{Kind: tuiOperationPM2, Target: target, Action: "save", Label: "Save PM2 process list"},
		})
	}
	if target.Local && model.dnsLoaded && model.dnsTarget == target.ID && model.dns.Status.ReloadAvailable {
		items = append(items, tuiPaletteItem{
			Kind: tuiPaletteOperation, Label: "Reload BIND configuration",
			Description: "Reload the observed local BIND configuration and zones", Keywords: "dns bind zone reload",
			Operation: tuiOperation{
				Kind: tuiOperationDNS, Target: target, Action: "reload", Label: "Reload BIND configuration",
			},
		})
	}
	if model.updatesLoaded && model.updatesTarget == target.ID {
		if model.updates.canStage() {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: "Stage panel release " + model.updates.LatestVersion,
				Description: "Download and verify the currently observed stable release", Keywords: "update release panel stage verify",
				Operation: tuiOperation{Kind: tuiOperationUpdate, Target: target, Action: "stage", Update: model.updates, Label: "Stage panel release " + model.updates.LatestVersion},
			})
		}
		if model.updates.canInstall() {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: "Install panel release " + model.updates.Stage.Version,
				Description: "Install the exact observed verified stage with automatic rollback", Keywords: "update release panel install restart rollback",
				Operation: tuiOperation{Kind: tuiOperationUpdate, Target: target, Action: "install", Update: model.updates, Label: "Install panel release " + model.updates.Stage.Version, Dangerous: true},
			})
		}
		if model.updates.canUpgradeAgent(target) {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: "Upgrade managed agent to " + model.updates.LatestVersion,
				Description: "Install the exact currently observed stable agent release", Keywords: "update release managed agent upgrade",
				Operation: tuiOperation{Kind: tuiOperationUpdate, Target: target, Action: "upgrade", Update: model.updates, Label: "Upgrade managed agent to " + model.updates.LatestVersion, Dangerous: true},
			})
		}
		if model.updates.canRollbackAgent(target) {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: "Rollback managed agent",
				Description: "Restore the agent from its currently observed rollback snapshot", Keywords: "update release managed agent rollback",
				Operation: tuiOperation{Kind: tuiOperationUpdate, Target: target, Action: "rollback", Update: model.updates, Label: "Rollback managed agent", Dangerous: true},
			})
		}
	}
	if model.deployLoaded && model.deployTarget == target.ID {
		if target.Local {
			for _, deployment := range model.deploy.Targets {
				if !deployment.IsActive {
					continue
				}
				label := "Deploy " + deployment.Name
				items = append(items, tuiPaletteItem{
					Kind: tuiPaletteOperation, Label: label,
					Description: "Fresh-preflight this local target and queue a manual deployment", Keywords: "deploy application release local target",
					Operation: tuiOperation{Kind: tuiOperationDeploy, Target: target, Action: "deploy", DeployTarget: deployment, Label: label},
				})
			}
		} else if targetAllows(target, agenthub.CapabilityDeployAction) {
			for _, deployment := range model.deploy.RemoteTargets {
				if !deployment.Eligible {
					continue
				}
				for _, action := range deployment.Actions {
					if !validTUIRemoteDeployAction(action) {
						continue
					}
					label := strings.ToUpper(action[:1]) + action[1:] + " " + deployment.Name
					items = append(items, tuiPaletteItem{
						Kind: tuiPaletteOperation, Label: label,
						Description: "Queue the exact freshly re-observed managed deployment plan action", Keywords: "deploy application managed plan " + action,
						Operation: tuiOperation{Kind: tuiOperationDeploy, Target: target, Action: action, RemoteDeployTarget: deployment, Label: label, Dangerous: action == "rollback" || action == "restart"},
					})
				}
			}
		}
	}
	if target.Local && model.alertsLoaded && model.alertsTarget == target.ID {
		for _, channel := range model.alerts.Channels {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: "Test notification channel " + channel.Name,
				Description: "Send one manual test through the exact observed channel", Keywords: "alert notification channel test",
				Operation: tuiOperation{Kind: tuiOperationAlert, Target: target, Action: "test", AlertResource: tuiAlertResourceChannel, AlertChannel: channel, Label: "Test notification channel " + channel.Name},
			})
			action, label := "enable", "Enable notification channel "
			if channel.Enabled {
				action, label = "disable", "Disable notification channel "
			}
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: label + channel.Name,
				Description: "Change only the enabled state of the exact observed channel", Keywords: "alert notification channel " + action,
				Operation: tuiOperation{Kind: tuiOperationAlert, Target: target, Action: action, AlertResource: tuiAlertResourceChannel, AlertChannel: channel, Label: label + channel.Name, Dangerous: action == "disable"},
			})
		}
		for _, rule := range model.alerts.Rules {
			action, label := "enable", "Enable alert rule "
			if rule.Enabled {
				action, label = "disable", "Disable alert rule "
			}
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: label + rule.Name,
				Description: "Change only the enabled state of the exact observed alert rule", Keywords: "alert notification rule " + action,
				Operation: tuiOperation{Kind: tuiOperationAlert, Target: target, Action: action, AlertResource: tuiAlertResourceRule, AlertRule: rule, Label: label + rule.Name, Dangerous: action == "disable"},
			})
		}
	}
	if target.Local && model.cloudflareLoaded && model.cloudflareTarget == target.ID && model.cloudflare.Supported {
		for _, zone := range model.cloudflare.Zones {
			items = append(items,
				tuiPaletteItem{
					Kind: tuiPaletteOperation, Label: "Purge Cloudflare cache for " + zone.Name,
					Description: "Purge the complete cache for the exact observed zone", Keywords: "cloudflare zone cache purge",
					Operation: tuiOperation{Kind: tuiOperationCloudflare, Target: target, Action: "purge", CloudflareResource: tuiCloudflareResourceZone, CloudflareZone: zone, Label: "Purge Cloudflare cache for " + zone.Name, Dangerous: true},
				},
				tuiPaletteItem{
					Kind: tuiPaletteOperation, Label: "Reconcile mail DNS for " + zone.Name,
					Description: "Apply the installation-owned mail DNS contract to the exact observed zone", Keywords: "cloudflare zone mail dns reconcile autofix",
					Operation: tuiOperation{Kind: tuiOperationCloudflare, Target: target, Action: "mail-autofix", CloudflareResource: tuiCloudflareResourceZone, CloudflareZone: zone, Label: "Reconcile mail DNS for " + zone.Name, Dangerous: true},
				},
			)
		}
		if model.cloudflare.Detail != nil {
			for _, record := range model.cloudflare.Detail.Records {
				if !cloudflareRecordSupportsProxy(record) {
					continue
				}
				label := "Enable Cloudflare proxy for " + record.Name
				if record.Proxied {
					label = "Disable Cloudflare proxy for " + record.Name
				}
				items = append(items, tuiPaletteItem{
					Kind: tuiPaletteOperation, Label: label,
					Description: "Toggle proxy state for the exact freshly re-observed DNS record", Keywords: "cloudflare dns record proxy toggle",
					Operation: tuiOperation{Kind: tuiOperationCloudflare, Target: target, Action: "toggle-proxy", CloudflareResource: tuiCloudflareResourceRecord, CloudflareZone: model.cloudflare.Detail.Zone, CloudflareRecord: record, Label: label, Dangerous: record.Proxied},
				})
			}
		}
	}
	if target.Local && model.usersLoaded && model.usersTarget == target.ID && model.users.Supported {
		items = append(items, tuiPaletteItem{
			Kind: tuiPaletteUserCreate, Label: "Create panel user",
			Description: "Open the masked central panel-user creation form", Keywords: "user account add create password",
		})
		for _, user := range model.users.Users {
			for _, role := range []models.Role{models.RoleAdmin, models.RoleManager, models.RoleViewer} {
				if user.Role == role {
					continue
				}
				label := "Set " + user.Name + " role to " + string(role)
				items = append(items, tuiPaletteItem{
					Kind:        tuiPaletteOperation,
					Label:       label,
					Description: "Change only the role of the exact freshly re-observed central panel user",
					Keywords:    "user account role permission " + user.Email + " " + string(role),
					Operation: tuiOperation{
						Kind: tuiOperationUser, Target: target, Action: "role-" + string(role), User: user,
						CurrentUserID: model.users.CurrentUserID, Label: label,
						Dangerous: user.Role == models.RoleAdmin && role != models.RoleAdmin,
					},
				})
			}
			if user.ID == model.users.CurrentUserID {
				continue
			}
			label := "Delete panel user " + user.Name
			items = append(items, tuiPaletteItem{
				Kind:        tuiPaletteOperation,
				Label:       label,
				Description: "Delete the exact freshly re-observed central panel user",
				Keywords:    "user account delete remove " + user.Email,
				Operation: tuiOperation{
					Kind: tuiOperationUser, Target: target, Action: "delete", User: user,
					CurrentUserID: model.users.CurrentUserID, Label: label, Dangerous: true,
				},
			})
		}
	}
	if model.webLoaded && model.webTarget == target.ID {
		for _, resource := range model.webResources {
			switch resource.Kind {
			case tuiWebDomain:
				if !targetAllows(target, agenthub.CapabilityDomainAction) {
					continue
				}
				action, label := "enable", "Enable domain "
				if resource.Enabled {
					action, label = "disable", "Disable domain "
				}
				items = append(items, tuiPaletteItem{
					Kind: tuiPaletteOperation, Label: label + resource.Name,
					Description: resource.Detail, Keywords: "web domain " + action,
					Operation: tuiOperation{
						Kind: tuiOperationWeb, Target: target, Action: action, WebResource: resource,
						Label: label + resource.Name, Dangerous: action == "disable",
					},
				})
			case tuiWebSSL:
				if !targetAllows(target, agenthub.CapabilitySSLAction) {
					continue
				}
				items = append(items, tuiPaletteItem{
					Kind: tuiPaletteOperation, Label: "Renew certificate " + resource.Name,
					Description: resource.Detail, Keywords: "web ssl tls renew certificate",
					Operation: tuiOperation{
						Kind: tuiOperationWeb, Target: target, Action: "renew", WebResource: resource,
						Label: "Renew certificate " + resource.Name,
					},
				})
			}
		}
	}
	if targetAllows(target, agenthub.CapabilityServiceAction) {
		for _, service := range model.snapshot.Services {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: "Restart service " + service.Name,
				Description: valueOrNA(service.State), Keywords: "systemd service restart",
				Operation: tuiOperation{
					Kind: tuiOperationService, Target: target, Action: "restart", Service: service.Name,
					Label: "Restart " + service.Name,
				},
			})
		}
	}
	if model.containersLoaded && model.containersTarget == target.ID && targetAllows(target, agenthub.CapabilityContainerAction) {
		for _, container := range model.containers {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: "Restart container " + container.Name,
				Description: container.Image, Keywords: "docker container restart",
				Operation: tuiOperation{
					Kind: tuiOperationContainer, Target: target, Action: "restart", Container: container,
					Label: "Restart " + container.Name,
				},
			})
		}
	}
	if model.pm2Loaded && model.pm2Target == target.ID && targetAllows(target, agenthub.CapabilityPM2Action) {
		for _, process := range model.pm2Processes {
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: "Restart PM2 " + process.Name,
				Description: valueOrNA(process.Status), Keywords: "pm2 process restart",
				Operation: tuiOperation{
					Kind: tuiOperationPM2, Target: target, Action: "restart", PM2Process: process,
					Label: "Restart " + process.Name,
				},
			})
		}
	}
	if target.Local && model.securityLoaded && model.securityTarget == target.ID && model.security.Fail2Ban.Available && model.security.Fail2Ban.State == "healthy" {
		for _, item := range model.security.Items {
			if item.Kind != tuiSecurityBannedIPItem {
				continue
			}
			label := "Unban " + item.IP + " from " + item.Jail
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: label,
				Description: "Remove this exact observed Fail2Ban block", Keywords: "security fail2ban jail ip unban",
				Operation: tuiOperation{
					Kind: tuiOperationSecurity, Target: target, Action: "unban", Security: item,
					Label: label, Dangerous: true,
				},
			})
		}
	}
	if model.firewallLoaded && model.firewallTarget == target.ID && model.firewall.Manageable && targetAllows(target, agenthub.CapabilityFirewallWrite) {
		for _, action := range []string{"firewall-add-ssh", "firewall-add-http", "firewall-add-https", "firewall-add-dns"} {
			spec, _ := firewallSpecForAction(action)
			label := fmt.Sprintf("Allow %s on %s/%d", spec.Comment, spec.Protocol, spec.Port)
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: label,
				Description: "Add a fixed inbound allow rule from any source", Keywords: "firewall add inbound port " + spec.Comment,
				Operation: tuiOperation{
					Kind: tuiOperationFirewall, Target: target, Action: "add", FirewallSpec: spec,
					FirewallState: model.firewall, Label: label,
				},
			})
		}
		if target.Local {
			action, label, dangerous := "enable", "Enable UFW firewall", false
			if model.firewall.Active {
				action, label, dangerous = "disable", "Disable UFW firewall", true
			}
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: label, Description: "Change the observed local UFW activation state", Keywords: "firewall ufw toggle",
				Operation: tuiOperation{
					Kind: tuiOperationFirewall, Target: target, Action: action, FirewallState: model.firewall,
					Label: label, Dangerous: dangerous,
				},
			})
		}
	}
	if model.cronLoaded && model.cronTarget == target.ID {
		for _, job := range model.cron.Jobs {
			if model.cron.Manageable && targetAllows(target, agenthub.CapabilityCronWrite) {
				action, label := "enable", "Enable cron job "
				if job.Enabled {
					action, label = "disable", "Disable cron job "
				}
				items = append(items, tuiPaletteItem{
					Kind: tuiPaletteOperation, Label: label + job.ID,
					Description: job.Schedule + " · " + job.User + " · " + truncateTUI(job.Command, 72), Keywords: "cron schedule job toggle " + action,
					Operation: tuiOperation{
						Kind: tuiOperationCron, Target: target, Action: action, CronJob: job, CronState: model.cron,
						Label: label + job.ID,
					},
				})
			}
			if !target.Local && model.cron.Runnable && targetAllows(target, agenthub.CapabilityCronRun) {
				items = append(items, tuiPaletteItem{
					Kind: tuiPaletteOperation, Label: "Run cron job " + job.ID,
					Description: job.Schedule + " · " + job.User + " · " + truncateTUI(job.Command, 72), Keywords: "cron schedule job run now",
					Operation: tuiOperation{
						Kind: tuiOperationCron, Target: target, Action: "run", CronJob: job, CronState: model.cron,
						Label: "Run cron job " + job.ID, Dangerous: true,
					},
				})
			}
		}
	}
	if model.databasesLoaded && model.databasesTarget == target.ID && !target.Local && model.databases.Restartable && targetAllows(target, agenthub.CapabilityDatabaseAction) {
		for _, item := range model.databases.Items {
			if item.Kind != tuiDatabaseEngineItem {
				continue
			}
			items = append(items, tuiPaletteItem{
				Kind: tuiPaletteOperation, Label: "Restart and health-check " + item.EngineName,
				Description: valueOrNA(item.Active) + " · " + valueOrNA(item.Version), Keywords: "database engine restart health check " + item.Engine,
				Operation: tuiOperation{
					Kind: tuiOperationDatabase, Target: target, Action: "restart", Database: item,
					Label: "Restart and health-check " + item.EngineName, Dangerous: true,
				},
			})
		}
	}
	return items
}

func targetAllows(target tuiTarget, capability string) bool {
	return target.Local || target.Online && target.capability(capability)
}

func filteredPaletteItems(items []tuiPaletteItem, query string) []tuiPaletteItem {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return items
	}
	filtered := make([]tuiPaletteItem, 0, len(items))
	for _, item := range items {
		haystack := strings.ToLower(strings.Join([]string{item.Label, item.Description, item.Keywords}, " "))
		matches := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matches = false
				break
			}
		}
		if matches {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (model tuiModel) updatePaletteKey(key string) (tea.Model, tea.Cmd) {
	filtered := filteredPaletteItems(model.dialog.PaletteItems, model.dialog.PaletteQuery)
	switch key {
	case "esc", "ctrl+k":
		model.dialog = tuiDialog{}
		return model, nil
	case "up":
		model.dialog.Cursor = wrapIndex(model.dialog.Cursor-1, len(filtered))
		return model, nil
	case "down":
		model.dialog.Cursor = wrapIndex(model.dialog.Cursor+1, len(filtered))
		return model, nil
	case "backspace", "ctrl+h":
		runes := []rune(model.dialog.PaletteQuery)
		if len(runes) > 0 {
			model.dialog.PaletteQuery = string(runes[:len(runes)-1])
			model.dialog.Cursor = 0
		}
		return model, nil
	case "ctrl+u":
		model.dialog.PaletteQuery = ""
		model.dialog.Cursor = 0
		return model, nil
	case "enter":
		if len(filtered) == 0 {
			return model, nil
		}
		item := filtered[minInt(model.dialog.Cursor, len(filtered)-1)]
		model.dialog = tuiDialog{}
		switch item.Kind {
		case tuiPaletteNavigate:
			model.tab = item.Tab
			model.cursor = 0
			return model.maybeLoadTabResource()
		case tuiPaletteTarget:
			return model.selectTarget(item.TargetID)
		case tuiPaletteOperation:
			model.openConfirmation(item.Operation, confirmationBody(item.Operation))
			return model, nil
		case tuiPaletteUserCreate:
			model.openUserCreateForm()
			return model, nil
		}
	}
	if utf8.RuneCountInString(key) == 1 {
		character, _ := utf8.DecodeRuneInString(key)
		if !unicode.IsControl(character) {
			model.dialog.PaletteQuery += key
			model.dialog.Cursor = 0
		}
	}
	return model, nil
}
