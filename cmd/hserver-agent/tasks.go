package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/integrationstate"
	deployservice "github.com/IamYGT/heyserver/internal/services/deploy"
	"github.com/IamYGT/heyserver/internal/services/systemactions"
)

type taskExecutor struct {
	services            serviceController
	host                hostActionExecutor
	disk                diskCleanupExecutor
	logs                logReader
	containers          containerExecutor
	nginx               nginxExecutor
	php                 phpExecutor
	pm2                 pm2Executor
	cron                cronExecutor
	firewall            firewallExecutor
	domains             domainExecutor
	ssl                 sslExecutor
	databases           databaseExecutor
	backups             backupExecutor
	files               fileExecutor
	deploys             deployExecutor
	deployDomains       deployDomainExecutor
	agentUpdates        agentUpdateExecutor
	profileApply        profileApplyExecutor
	integrationPM2      managedIntegrationProbe
	integrationDocker   managedIntegrationProbe
	metrics             metricsReader
	observed            map[string]struct{}
	allowed             map[string]struct{}
	allowedHostActions  map[string]struct{}
	allowProcessSignals bool
	allowAgentUpdates   bool
}

type hostActionExecutor interface {
	OptimizeMemory(context.Context) (string, error)
	ResetSwap(context.Context) (string, error)
	CleanTemporaryFiles(context.Context) (string, error)
	ScheduleReboot(context.Context) (string, error)
	CancelScheduledReboot(context.Context) (string, error)
	TerminateProcess(int, string, uint64) (systemactions.ProcessSignalResult, error)
}

type logReader interface {
	Read(context.Context, string, int) ([]journalEntry, error)
}

type containerExecutor interface {
	List(context.Context) ([]containerState, error)
	Action(context.Context, string, string) (string, error)
}

type nginxExecutor interface {
	Action(context.Context, string) (string, error)
	List() ([]nginxConfig, error)
	Read(string) (nginxConfig, error)
	Write(context.Context, string, []byte, string, bool) (string, error)
}

type phpExecutor interface {
	Inventory(context.Context) ([]phpFPMVersion, error)
	Read(string, string) (managedFileContent, error)
	Write(context.Context, string, string, []byte, string, bool) (string, error)
	Action(context.Context, string, string) (string, error)
}

type pm2Executor interface {
	List(context.Context) ([]pm2Process, error)
	Logs(context.Context, string, int) (string, error)
	Action(context.Context, string, string) (string, error)
}

type managedIntegrationProbe func(context.Context) (integrationstate.State, error)

type metricsReader interface {
	Collect(context.Context) (agenthub.MetricsSnapshot, error)
}

type cronExecutor interface {
	Inventory(context.Context) (cronInventory, error)
	Create(context.Context, cronJob, string) (string, error)
	Update(context.Context, cronJob, string) error
	Delete(context.Context, string, string) error
	Run(context.Context, string) (string, error)
}

type firewallExecutor interface {
	Inventory(context.Context) (firewallInventory, error)
	Add(context.Context, firewallRule, string) (string, error)
	Delete(context.Context, string, string) error
}

type domainExecutor interface {
	Inventory() ([]managedDomain, error)
	Action(context.Context, string, string) (string, error)
}

type sslExecutor interface {
	Inventory() ([]managedCertificate, error)
	Action(context.Context, string, string) (string, error)
}

type databaseExecutor interface {
	Inventory(context.Context) ([]managedDatabaseEngine, error)
	Action(context.Context, string, string) (string, error)
}

type backupExecutor interface {
	Inventory(context.Context) ([]managedBackupPlan, error)
	Run(context.Context, string) (string, error)
}

type fileExecutor interface {
	Browse(context.Context, string) ([]managedFileEntry, error)
	Read(context.Context, string) (managedFileContent, error)
	Write(context.Context, string, []byte, string) (string, error)
}

type deployExecutor interface {
	Inventory(context.Context) ([]managedDeployTarget, error)
	Run(context.Context, string, string) (string, string, error)
}

type deployDomainExecutor interface {
	Inventory(context.Context, string) ([]managedDeployDomain, error)
	Health(context.Context, string, string) (managedDeployDomainHealth, error)
	Create(context.Context, string, string) (managedDeployDomain, error)
	Ensure(context.Context, string, string, string) (managedDeployDomainEnsureReceipt, error)
	Delete(context.Context, string, string) error
	EnableTLS(context.Context, string, string, string) (managedDeployDomain, error)
	DisableTLS(context.Context, string, string) (managedDeployDomain, error)
	RenewTLS(context.Context, string, string) (managedDeployDomain, error)
}

type agentUpdateExecutor interface {
	Status(context.Context) (managedAgentUpdateStatus, error)
	Upgrade(context.Context, string) (managedAgentUpdateStatus, error)
	Rollback(context.Context) (managedAgentUpdateStatus, error)
}

type profileApplyExecutor interface {
	apply(context.Context, map[string]string) (profileApplyOutcome, string)
}

func newTaskExecutor(services serviceController, host hostActionExecutor, disk diskCleanupExecutor, logs logReader, observed []string, allowed, allowedHostActions map[string]struct{}, allowProcessSignals bool) taskExecutor {
	observedSet := make(map[string]struct{}, len(observed)+len(allowed))
	for _, service := range observed {
		observedSet[service] = struct{}{}
	}
	for service := range allowed {
		observedSet[service] = struct{}{}
	}
	return taskExecutor{services: services, host: host, disk: disk, logs: logs, observed: observedSet, allowed: allowed, allowedHostActions: allowedHostActions, allowProcessSignals: allowProcessSignals}
}

func (e taskExecutor) execute(ctx context.Context, task *agenthub.Task) agenthub.TaskResultRequest {
	if task == nil {
		return failedResult(errors.New("missing task"))
	}

	switch task.Kind {
	case profileApplyTaskKind:
		if e.profileApply == nil {
			return failedProfileTaskResult(profileErrorNotReady)
		}
		outcome, code := e.profileApply.apply(ctx, task.Payload)
		if code != "" {
			return failedProfileTaskResult(code)
		}
		return profileApplyTaskResult(outcome)

	case agenthub.TaskAgentUpdateStatus:
		if len(task.Payload) != 0 || e.agentUpdates == nil {
			return failedResult(errors.New("agent update status is not enabled locally"))
		}
		status, err := e.agentUpdates.Status(ctx)
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(status)

	case agenthub.TaskAgentUpdateAction:
		if e.agentUpdates == nil || !e.allowAgentUpdates {
			return failedResult(errors.New("agent update actions are not enabled locally"))
		}
		action := task.Payload["action"]
		var (
			status managedAgentUpdateStatus
			err    error
		)
		switch action {
		case "upgrade":
			if len(task.Payload) != 2 || task.Payload["version"] == "" {
				return failedResult(errors.New("agent upgrade requires exactly action and version fields"))
			}
			status, err = e.agentUpdates.Upgrade(ctx, task.Payload["version"])
		case "rollback":
			if len(task.Payload) != 1 {
				return failedResult(errors.New("agent rollback accepts only the action field"))
			}
			status, err = e.agentUpdates.Rollback(ctx)
		default:
			return failedResult(errors.New("unsupported agent lifecycle action"))
		}
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(status)

	case agenthub.TaskIntegrationStatus:
		return e.executeManagedIntegrationStatus(ctx, task)

	case agenthub.TaskMetricsRead:
		if len(task.Payload) != 0 || e.metrics == nil {
			return agenthub.TaskResultRequest{Status: agenthub.TaskStatusFailed, Error: agenthub.MetricsUnavailableError}
		}
		snapshot, err := e.metrics.Collect(ctx)
		if err != nil {
			return agenthub.TaskResultRequest{Status: agenthub.TaskStatusFailed, Error: agenthub.MetricsUnavailableError}
		}
		return jsonTaskResult(snapshot)

	case agenthub.TaskServiceStatus:
		service, ok := exactPayload(task.Payload, "service")
		if !ok {
			return failedResult(errors.New("service.status requires exactly the service payload field"))
		}
		if _, permitted := e.observed[service]; !permitted {
			return failedResult(errors.New("service is not in the observed allowlist"))
		}
		state, err := e.services.status(ctx, service)
		if err != nil {
			return failedResult(err)
		}
		return agenthub.TaskResultRequest{
			Status: "completed",
			Result: map[string]string{"service": state.Name, "active": state.Active, "sub": state.Sub},
		}

	case agenthub.TaskServiceAction:
		service, action, ok := exactActionPayload(task.Payload)
		if !ok {
			return failedResult(errors.New("service.action requires exactly service and action payload fields"))
		}
		if _, permitted := e.allowed[service]; !permitted {
			return failedResult(errors.New("service is not in the action allowlist"))
		}
		if err := e.services.action(ctx, service, action); err != nil {
			return failedResult(err)
		}
		state, err := e.services.status(ctx, service)
		if err != nil {
			return failedResult(fmt.Errorf("action completed but status check failed: %w", err))
		}
		return agenthub.TaskResultRequest{
			Status: "completed",
			Result: map[string]string{"service": state.Name, "action": action, "active": state.Active, "sub": state.Sub},
		}

	case agenthub.TaskHostAction:
		action, ok := exactPayload(task.Payload, "action")
		if !ok {
			return failedResult(errors.New("host.action requires exactly the action payload field"))
		}
		if _, permitted := e.allowedHostActions[action]; !permitted {
			return failedResult(errors.New("host action is not in the local allowlist"))
		}
		if e.host == nil {
			return failedResult(errors.New("host action executor is unavailable"))
		}
		var (
			message string
			err     error
		)
		switch action {
		case "memory-optimize":
			message, err = e.host.OptimizeMemory(ctx)
		case "swap-reset":
			message, err = e.host.ResetSwap(ctx)
		case "temp-clean":
			message, err = e.host.CleanTemporaryFiles(ctx)
		case "reboot":
			message, err = e.host.ScheduleReboot(ctx)
		case "reboot-cancel":
			message, err = e.host.CancelScheduledReboot(ctx)
		default:
			return failedResult(errors.New("unsupported host action"))
		}
		if err != nil {
			return failedResult(err)
		}
		return agenthub.TaskResultRequest{
			Status: "completed",
			Result: map[string]string{"action": action, "message": message},
		}

	case agenthub.TaskProcessSignal:
		if !e.allowProcessSignals {
			return failedResult(errors.New("process signals are not enabled locally"))
		}
		pid, pidErr := strconv.Atoi(task.Payload["pid"])
		startTime, startErr := strconv.ParseUint(task.Payload["start_time"], 10, 64)
		signal := task.Payload["signal"]
		if len(task.Payload) != 3 || pidErr != nil || pid <= 1 || startErr != nil || startTime == 0 || (signal != "term" && signal != "kill") {
			return failedResult(errors.New("process.signal requires a valid pid, start_time, and signal"))
		}
		if e.host == nil {
			return failedResult(errors.New("process signal executor is unavailable"))
		}
		result, err := e.host.TerminateProcess(pid, signal, startTime)
		if err != nil {
			return failedResult(err)
		}
		return agenthub.TaskResultRequest{
			Status: "completed",
			Result: map[string]string{
				"message":   result.Message,
				"exited":    strconv.FormatBool(result.Exited),
				"confirmed": strconv.FormatBool(result.Confirmed),
			},
		}

	case agenthub.TaskDiskCleanupScan:
		if len(task.Payload) != 0 || e.disk == nil {
			return failedResult(errors.New("disk cleanup scan is not enabled locally"))
		}
		targets, err := e.disk.Scan(ctx)
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(targets)

	case agenthub.TaskDiskCleanupExecute:
		targets, ok := exactPayload(task.Payload, "targets")
		if !ok || e.disk == nil {
			return failedResult(errors.New("disk cleanup execution is not enabled locally"))
		}
		execution, err := e.disk.Execute(ctx, strings.Split(targets, ","))
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(execution)

	case agenthub.TaskLogsRead:
		lines, linesErr := strconv.Atoi(task.Payload["lines"])
		source := task.Payload["source"]
		if len(task.Payload) != 2 || linesErr != nil || lines < 1 || lines > maxLogLines || e.logs == nil {
			return failedResult(errors.New("log reading is not enabled locally"))
		}
		entries, err := e.logs.Read(ctx, source, lines)
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(entries)

	case agenthub.TaskContainerList:
		if len(task.Payload) != 0 || e.containers == nil {
			return failedResult(errors.New("container inventory is not enabled locally"))
		}
		containers, err := e.containers.List(ctx)
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(containers)

	case agenthub.TaskContainerAction:
		container, action, ok := exactContainerPayload(task.Payload)
		if !ok || e.containers == nil {
			return failedResult(errors.New("container action is not enabled locally"))
		}
		message, err := e.containers.Action(ctx, container, action)
		if err != nil {
			return failedResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message}}

	case agenthub.TaskNginxAction:
		action, ok := exactPayload(task.Payload, "action")
		if !ok || e.nginx == nil || (action != "test" && action != "reload") {
			return failedResult(errors.New("Nginx action is not enabled locally"))
		}
		message, err := e.nginx.Action(ctx, action)
		if err != nil {
			return failedResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message}}

	case agenthub.TaskNginxConfigList:
		if len(task.Payload) != 0 || e.nginx == nil {
			return failedResult(errors.New("Nginx configuration reading is not enabled locally"))
		}
		configs, err := e.nginx.List()
		if err != nil {
			return failedNginxResult(err)
		}
		return jsonTaskResult(configs)

	case agenthub.TaskNginxConfigRead:
		name, ok := exactPayload(task.Payload, "name")
		if !ok || e.nginx == nil {
			return failedResult(errors.New("Nginx configuration reading is not enabled locally"))
		}
		config, err := e.nginx.Read(name)
		if err != nil {
			return failedNginxResult(err)
		}
		return jsonTaskResult(config)

	case agenthub.TaskNginxConfigWrite:
		if len(task.Payload) != 4 || e.nginx == nil {
			return failedResult(errors.New("Nginx configuration writing is not enabled locally"))
		}
		content, decodeErr := base64.StdEncoding.DecodeString(task.Payload["content_b64"])
		reload, boolErr := strconv.ParseBool(task.Payload["reload"])
		if decodeErr != nil || boolErr != nil || !agentNginxConfigNamePattern.MatchString(task.Payload["name"]) || !agentSHA256Pattern.MatchString(task.Payload["checksum"]) {
			return failedResult(errors.New("invalid Nginx configuration write request"))
		}
		backup, err := e.nginx.Write(ctx, task.Payload["name"], content, task.Payload["checksum"], reload)
		if err != nil {
			return failedNginxResult(err)
		}
		message := "Nginx configuration saved and tested"
		if reload {
			message = "Nginx configuration saved, tested and reloaded"
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message, "backup": backup}}

	case agenthub.TaskPHPInventory:
		if len(task.Payload) != 0 || e.php == nil {
			return failedResult(errors.New("PHP-FPM inventory is not enabled locally"))
		}
		inventory, err := e.php.Inventory(ctx)
		if err != nil {
			return failedPHPResult(err)
		}
		return jsonTaskResult(inventory)

	case agenthub.TaskPHPConfigRead:
		if len(task.Payload) != 2 || e.php == nil {
			return failedResult(errors.New("PHP-FPM configuration reading is not enabled locally"))
		}
		config, err := e.php.Read(task.Payload["version"], task.Payload["pool"])
		if err != nil {
			return failedPHPResult(err)
		}
		return jsonTaskResult(config)

	case agenthub.TaskPHPConfigWrite:
		if len(task.Payload) != 5 || e.php == nil {
			return failedResult(errors.New("PHP-FPM configuration writing is not enabled locally"))
		}
		content, decodeErr := base64.StdEncoding.DecodeString(task.Payload["content_b64"])
		reload, boolErr := strconv.ParseBool(task.Payload["reload"])
		if decodeErr != nil || boolErr != nil || !agentPHPVersionPattern.MatchString(task.Payload["version"]) || !agentPHPPoolNamePattern.MatchString(task.Payload["pool"]) || !agentSHA256Pattern.MatchString(task.Payload["checksum"]) {
			return failedResult(errors.New("invalid PHP-FPM configuration write request"))
		}
		backup, err := e.php.Write(ctx, task.Payload["version"], task.Payload["pool"], content, task.Payload["checksum"], reload)
		if err != nil {
			return failedPHPResult(err)
		}
		message := "PHP-FPM pool saved and tested"
		if reload {
			message = "PHP-FPM pool saved, tested and reloaded"
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message, "backup": backup}}

	case agenthub.TaskPHPAction:
		if len(task.Payload) != 2 || e.php == nil {
			return failedResult(errors.New("PHP-FPM action is not enabled locally"))
		}
		message, err := e.php.Action(ctx, task.Payload["version"], task.Payload["action"])
		if err != nil {
			return failedPHPResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message}}

	case agenthub.TaskPM2List:
		if len(task.Payload) != 0 || e.pm2 == nil {
			return failedResult(errors.New("PM2 inventory is not enabled locally"))
		}
		processes, err := e.pm2.List(ctx)
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(processes)

	case agenthub.TaskPM2Logs:
		lines, linesErr := strconv.Atoi(task.Payload["lines"])
		if len(task.Payload) != 2 || e.pm2 == nil || linesErr != nil || lines < 1 || lines > 500 || !agentPM2NamePattern.MatchString(task.Payload["name"]) {
			return failedResult(errors.New("PM2 log reading is not enabled locally"))
		}
		logs, err := e.pm2.Logs(ctx, task.Payload["name"], lines)
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(logs)

	case agenthub.TaskPM2Action:
		if len(task.Payload) != 2 || e.pm2 == nil || !agentPM2NamePattern.MatchString(task.Payload["name"]) {
			return failedResult(errors.New("PM2 action is not enabled locally"))
		}
		message, err := e.pm2.Action(ctx, task.Payload["name"], task.Payload["action"])
		if err != nil {
			return failedResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message}}

	case agenthub.TaskCronInventory:
		if len(task.Payload) != 0 || e.cron == nil {
			return failedResult(errors.New("cron inventory is not enabled locally"))
		}
		inventory, err := e.cron.Inventory(ctx)
		if err != nil {
			return failedCronResult(err)
		}
		return jsonTaskResult(inventory)

	case agenthub.TaskCronCreate, agenthub.TaskCronUpdate:
		if len(task.Payload) != 2 || e.cron == nil || !agentSHA256Pattern.MatchString(task.Payload["revision"]) {
			return failedResult(errors.New("cron writing is not enabled locally"))
		}
		raw, decodeErr := base64.StdEncoding.DecodeString(task.Payload["job_b64"])
		var job cronJob
		if decodeErr != nil || len(raw) > maxCronStateBytes || json.Unmarshal(raw, &job) != nil {
			return failedResult(errors.New("invalid cron job payload"))
		}
		if task.Kind == agenthub.TaskCronCreate {
			id, err := e.cron.Create(ctx, job, task.Payload["revision"])
			if err != nil {
				return failedCronResult(err)
			}
			return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"id": id, "message": "Cron job created and validated"}}
		}
		if err := e.cron.Update(ctx, job, task.Payload["revision"]); err != nil {
			return failedCronResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Cron job updated and validated"}}

	case agenthub.TaskCronDelete:
		if len(task.Payload) != 2 || e.cron == nil {
			return failedResult(errors.New("cron deletion is not enabled locally"))
		}
		if err := e.cron.Delete(ctx, task.Payload["id"], task.Payload["revision"]); err != nil {
			return failedCronResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Cron job deleted"}}

	case agenthub.TaskCronRun:
		if len(task.Payload) != 1 || e.cron == nil {
			return failedResult(errors.New("manual cron execution is not enabled locally"))
		}
		output, err := e.cron.Run(ctx, task.Payload["id"])
		if err != nil {
			return failedCronResult(err)
		}
		result := jsonTaskResult(output)
		result.Result["message"] = "Cron job completed"
		return result

	case agenthub.TaskFirewallInventory:
		if len(task.Payload) != 0 || e.firewall == nil {
			return failedResult(errors.New("firewall inventory is not enabled locally"))
		}
		inventory, err := e.firewall.Inventory(ctx)
		if err != nil {
			return failedFirewallResult(err)
		}
		return jsonTaskResult(inventory)

	case agenthub.TaskFirewallAdd:
		if len(task.Payload) != 2 || e.firewall == nil || !agentSHA256Pattern.MatchString(task.Payload["revision"]) {
			return failedResult(errors.New("firewall writing is not enabled locally"))
		}
		raw, decodeErr := base64.StdEncoding.DecodeString(task.Payload["rule_b64"])
		var rule firewallRule
		if decodeErr != nil || len(raw) > 1<<10 || json.Unmarshal(raw, &rule) != nil {
			return failedResult(errors.New("invalid firewall rule payload"))
		}
		id, err := e.firewall.Add(ctx, rule, task.Payload["revision"])
		if err != nil {
			return failedFirewallResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"id": id, "message": "Firewall rule added and persisted"}}

	case agenthub.TaskFirewallDelete:
		if len(task.Payload) != 2 || e.firewall == nil {
			return failedResult(errors.New("firewall deletion is not enabled locally"))
		}
		if err := e.firewall.Delete(ctx, task.Payload["id"], task.Payload["revision"]); err != nil {
			return failedFirewallResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Firewall rule deleted and persisted"}}

	case agenthub.TaskDomainInventory:
		if len(task.Payload) != 0 || e.domains == nil {
			return failedResult(errors.New("domain inventory is not enabled locally"))
		}
		domains, err := e.domains.Inventory()
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(domains)

	case agenthub.TaskDomainAction:
		if len(task.Payload) != 2 || e.domains == nil {
			return failedResult(errors.New("domain actions are not enabled locally"))
		}
		message, err := e.domains.Action(ctx, task.Payload["config"], task.Payload["action"])
		if err != nil {
			return failedNginxResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message}}

	case agenthub.TaskSSLInventory:
		if len(task.Payload) != 0 || e.ssl == nil {
			return failedResult(errors.New("SSL certificate inventory is not enabled locally"))
		}
		certificates, err := e.ssl.Inventory()
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(certificates)

	case agenthub.TaskSSLAction:
		if len(task.Payload) != 2 || e.ssl == nil {
			return failedResult(errors.New("SSL certificate actions are not enabled locally"))
		}
		message, err := e.ssl.Action(ctx, task.Payload["name"], task.Payload["action"])
		if err != nil {
			return failedResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message}}

	case agenthub.TaskDatabaseInventory:
		if len(task.Payload) != 0 || e.databases == nil {
			return failedResult(errors.New("database inventory is not enabled locally"))
		}
		engines, err := e.databases.Inventory(ctx)
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(engines)

	case agenthub.TaskDatabaseAction:
		if len(task.Payload) != 2 || e.databases == nil {
			return failedResult(errors.New("database actions are not enabled locally"))
		}
		message, err := e.databases.Action(ctx, task.Payload["engine"], task.Payload["action"])
		if err != nil {
			return failedResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message}}

	case agenthub.TaskBackupInventory:
		if len(task.Payload) != 0 || e.backups == nil {
			return failedResult(errors.New("backup inventory is not enabled locally"))
		}
		plans, err := e.backups.Inventory(ctx)
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(plans)

	case agenthub.TaskBackupRun:
		if len(task.Payload) != 1 || e.backups == nil {
			return failedResult(errors.New("backup execution is not enabled locally"))
		}
		message, err := e.backups.Run(ctx, task.Payload["plan"])
		if err != nil {
			return failedResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message}}

	case agenthub.TaskFilesBrowse:
		if len(task.Payload) != 1 || e.files == nil {
			return failedResult(errors.New("file browsing is not enabled locally"))
		}
		entries, err := e.files.Browse(ctx, task.Payload["path"])
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(entries)

	case agenthub.TaskFilesRead:
		if len(task.Payload) != 1 || e.files == nil {
			return failedResult(errors.New("file reading is not enabled locally"))
		}
		file, err := e.files.Read(ctx, task.Payload["path"])
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(file)

	case agenthub.TaskFilesWrite:
		if len(task.Payload) != 3 || e.files == nil {
			return failedResult(errors.New("file writing is not enabled locally"))
		}
		content, err := base64.StdEncoding.DecodeString(task.Payload["content_b64"])
		if err != nil {
			return failedResult(errors.New("invalid file content"))
		}
		backup, err := e.files.Write(ctx, task.Payload["path"], content, task.Payload["checksum"])
		if err != nil {
			return failedNginxResult(err)
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "File saved", "backup": backup}}

	case agenthub.TaskDeployInventory:
		if len(task.Payload) != 0 || e.deploys == nil {
			return failedResult(errors.New("deploy inventory is not enabled locally"))
		}
		targets, err := e.deploys.Inventory(ctx)
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(targets)

	case agenthub.TaskDeployAction:
		if len(task.Payload) != 2 || e.deploys == nil || !agentNginxConfigNamePattern.MatchString(task.Payload["target"]) || !validAgentDeployAction(task.Payload["action"]) {
			return failedResult(errors.New("deploy action request is invalid or disabled locally"))
		}
		message, output, err := e.deploys.Run(ctx, task.Payload["target"], task.Payload["action"])
		if err != nil {
			return agenthub.TaskResultRequest{Status: agenthub.TaskStatusFailed, Error: err.Error(), Result: map[string]string{"output": output}}
		}
		return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": message, "output": output}}

	case agenthub.TaskDeployDomainInventory:
		if len(task.Payload) != 1 || e.deployDomains == nil || !agentNginxConfigNamePattern.MatchString(task.Payload["target"]) {
			return failedResult(errors.New("project domain inventory request is invalid or disabled locally"))
		}
		domains, err := e.deployDomains.Inventory(ctx, task.Payload["target"])
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(domains)

	case agenthub.TaskDeployDomainHealth:
		if len(task.Payload) != 2 || e.deployDomains == nil || !agentNginxConfigNamePattern.MatchString(task.Payload["target"]) {
			return failedResult(errors.New("project domain health request is invalid or disabled locally"))
		}
		health, err := e.deployDomains.Health(ctx, task.Payload["target"], task.Payload["domain"])
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(health)

	case agenthub.TaskDeployDomainAction:
		if e.deployDomains == nil || !agentNginxConfigNamePattern.MatchString(task.Payload["target"]) {
			return failedResult(errors.New("project domain action request is invalid or disabled locally"))
		}
		target, domain, action := task.Payload["target"], task.Payload["domain"], task.Payload["action"]
		var managed managedDeployDomain
		var err error
		switch action {
		case "create":
			if len(task.Payload) != 3 {
				return failedResult(errors.New("project domain create request is invalid"))
			}
			managed, err = e.deployDomains.Create(ctx, target, domain)
		case "ensure":
			if len(task.Payload) != 4 || task.Payload["expected_revision"] == "" || !validAgentDeployDomainRevision(task.Payload["expected_revision"]) {
				return failedResult(errors.New("project domain ensure request requires exactly target, domain, action, and expected_revision"))
			}
			var receipt managedDeployDomainEnsureReceipt
			receipt, err = e.deployDomains.Ensure(ctx, target, domain, task.Payload["expected_revision"])
			if err != nil {
				return failedDeployDomainResult(err)
			}
			return jsonTaskResult(receipt)
		case "delete":
			if len(task.Payload) != 3 {
				return failedResult(errors.New("project domain delete request is invalid"))
			}
			err = e.deployDomains.Delete(ctx, target, domain)
			if err == nil {
				return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"message": "Managed project domain removed"}}
			}
		case "tls-enable":
			if len(task.Payload) != 3 && len(task.Payload) != 4 {
				return failedResult(errors.New("project TLS enable request is invalid"))
			}
			managed, err = e.deployDomains.EnableTLS(ctx, target, domain, task.Payload["email"])
		case "tls-disable":
			if len(task.Payload) != 3 {
				return failedResult(errors.New("project TLS disable request is invalid"))
			}
			managed, err = e.deployDomains.DisableTLS(ctx, target, domain)
		case "tls-renew":
			if len(task.Payload) != 3 {
				return failedResult(errors.New("project TLS renewal request is invalid"))
			}
			managed, err = e.deployDomains.RenewTLS(ctx, target, domain)
		default:
			return failedResult(errors.New("unsupported project domain action"))
		}
		if err != nil {
			return failedResult(err)
		}
		return jsonTaskResult(managed)

	default:
		return failedResult(errors.New("unsupported task kind"))
	}
}

func jsonTaskResult(value any) agenthub.TaskResultRequest {
	data, err := json.Marshal(value)
	if err != nil {
		return failedResult(err)
	}
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusCompleted, Result: map[string]string{"data": string(data)}}
}

func exactPayload(payload map[string]string, key string) (string, bool) {
	if len(payload) != 1 {
		return "", false
	}
	value := payload[key]
	return value, value != ""
}

func exactActionPayload(payload map[string]string) (service, action string, ok bool) {
	if len(payload) != 2 {
		return "", "", false
	}
	service = payload["service"]
	action = payload["action"]
	if service == "" {
		return "", "", false
	}
	switch action {
	case "start", "stop", "restart":
		return service, action, true
	default:
		return "", "", false
	}
}

func exactContainerPayload(payload map[string]string) (container, action string, ok bool) {
	if len(payload) != 2 {
		return "", "", false
	}
	container = payload["container"]
	action = payload["action"]
	if !agentContainerNamePattern.MatchString(container) {
		return "", "", false
	}
	switch action {
	case "start", "stop", "restart":
		return container, action, true
	default:
		return "", "", false
	}
}

func failedResult(err error) agenthub.TaskResultRequest {
	return agenthub.TaskResultRequest{Status: "failed", Error: err.Error()}
}

func failedNginxResult(err error) agenthub.TaskResultRequest {
	code := "nginx_operation_failed"
	switch {
	case errors.Is(err, errNginxConfigChanged):
		code = "config_changed"
	case errors.Is(err, errNginxConfigInvalid):
		code = "config_invalid"
	case errors.Is(err, errNginxConfigTooLarge), errors.Is(err, errManagedFileTooLarge):
		code = "config_too_large"
	}
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusFailed, Error: err.Error(), Result: map[string]string{"code": code}}
}

func failedPHPResult(err error) agenthub.TaskResultRequest {
	code := "php_operation_failed"
	switch {
	case errors.Is(err, errPHPConfigChanged):
		code = "config_changed"
	case errors.Is(err, errPHPConfigInvalid):
		code = "config_invalid"
	case errors.Is(err, errPHPConfigTooLarge), errors.Is(err, errManagedFileTooLarge):
		code = "config_too_large"
	}
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusFailed, Error: err.Error(), Result: map[string]string{"code": code}}
}

func failedCronResult(err error) agenthub.TaskResultRequest {
	code := "cron_operation_failed"
	switch {
	case errors.Is(err, errCronChanged):
		code = "cron_changed"
	case errors.Is(err, errCronNotFound):
		code = "cron_not_found"
	case errors.Is(err, errCronInvalid):
		code = "cron_invalid"
	}
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusFailed, Error: err.Error(), Result: map[string]string{"code": code}}
}

func failedFirewallResult(err error) agenthub.TaskResultRequest {
	code := "firewall_operation_failed"
	switch {
	case errors.Is(err, errFirewallChanged):
		code = "firewall_changed"
	case errors.Is(err, errFirewallNotFound):
		code = "firewall_not_found"
	case errors.Is(err, errFirewallInvalid):
		code = "firewall_invalid"
	case errors.Is(err, errFirewallProtected):
		code = "firewall_protected"
	case errors.Is(err, errFirewallPersistence):
		code = "firewall_persistence_failed"
	}
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusFailed, Error: err.Error(), Result: map[string]string{"code": code}}
}

func failedDeployDomainResult(err error) agenthub.TaskResultRequest {
	code := "domain_operation_failed"
	switch {
	case errors.Is(err, errManagedDeployDomainStale):
		code = "stale_observation"
	case errors.Is(err, errManagedDeployDomainDrift):
		code = "domain_drift"
	case errors.Is(err, errManagedDeployDomainConflict):
		code = "domain_conflict"
	case errors.Is(err, errManagedDeployDomainCleanup), errors.Is(err, deployservice.ErrProjectDomainCleanup):
		code = "domain_cleanup_failed"
	case errors.Is(err, errManagedDeployDomainObservation):
		code = "domain_observation_failed"
	}
	return agenthub.TaskResultRequest{Status: agenthub.TaskStatusFailed, Error: code}
}
