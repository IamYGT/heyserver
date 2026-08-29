package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

func handleRemoteNodeAction(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		nodeID := r.PathValue("id")
		action := r.PathValue("action")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{
			Kind:    agenthub.TaskHostAction,
			Payload: map[string]string{"action": action},
		})
		if err != nil {
			auditHostActionFailure(r, "remote_system_action", nodeID+": "+action, err)
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			auditHostActionFailure(r, "remote_system_action", nodeID+": "+action, err)
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			taskErr := errors.New(task.Error)
			auditHostActionFailure(r, "remote_system_action", nodeID+": "+action, taskErr)
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		message := task.Result["message"]
		if message == "" {
			message = "Managed-node action completed"
		}
		auditHostAction(r, "remote_system_action", nodeID+": "+action+": "+message)
		status := http.StatusOK
		if action == "reboot" {
			status = http.StatusAccepted
		}
		jsonResponse(w, status, map[string]string{"message": message})
	}
}

func handleRemoteNodeRebootStatus(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tasks, err := hub.ListTasksForNode(r.PathValue("id"), 50)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		status := remotenodes.RebootStatus{}
		for _, task := range tasks {
			if task.Kind != agenthub.TaskHostAction || task.Status != agenthub.TaskStatusCompleted {
				continue
			}
			switch task.Payload["action"] {
			case "reboot-cancel":
				jsonResponse(w, http.StatusOK, status)
				return
			case "reboot":
				if task.CompletedAt != nil {
					scheduled := task.CompletedAt.Add(10 * time.Second)
					remaining := time.Until(scheduled)
					if remaining > 0 {
						status.Pending = true
						status.ScheduledFor = scheduled.UTC().Format(time.RFC3339)
						status.RemainingSeconds = int64((remaining + time.Second - 1) / time.Second)
					}
				}
				jsonResponse(w, http.StatusOK, status)
				return
			}
		}
		jsonResponse(w, http.StatusOK, status)
	}
}

func handleRemoteNodeActionStatus(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tasks, err := hub.ListTasksForNode(r.PathValue("id"), 50)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		status := remotenodes.ActionStatus{}
		for _, task := range tasks {
			if task.Kind == agenthub.TaskHostAction && (task.Status == agenthub.TaskStatusQueued || task.Status == agenthub.TaskStatusRunning) {
				status.Running = true
				status.Action = task.Payload["action"]
				started := task.CreatedAt
				if task.StartedAt != nil {
					started = *task.StartedAt
				}
				status.StartedAt = started.UTC().Format(time.RFC3339Nano)
				break
			}
		}
		jsonResponse(w, http.StatusOK, status)
	}
}

func handleRemoteNodeProcesses(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node, err := hub.GetNode(r.PathValue("id"))
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, node.Inventory.Processes)
	}
}

func handleRemoteNodeMemory(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node, err := hub.GetNode(r.PathValue("id"))
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		state := remotenodes.MemoryState{
			MemoryTotal:       node.Inventory.MemoryTotal,
			MemoryAvailable:   node.Inventory.MemoryAvailable,
			SwapTotal:         node.Inventory.SwapTotal,
			SwapUsed:          node.Inventory.SwapUsed,
			SwapFree:          node.Inventory.SwapFree,
			SwapResetEligible: node.Inventory.SwapResetEligible,
			SwapResetReason:   node.Inventory.SwapResetReason,
		}
		jsonResponse(w, http.StatusOK, state)
	}
}

func waitForManagedNodeTask(ctx context.Context, hub *agenthub.Service, nodeID string, taskID int64) (*agenthub.Task, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := hub.GetTaskForNode(nodeID, taskID)
		if err != nil {
			return nil, err
		}
		if task.Status == agenthub.TaskStatusCompleted || task.Status == agenthub.TaskStatusFailed {
			return task, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("managed-node task %d did not complete: %w", taskID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func handleRemoteNodeDisk(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node, err := hub.GetNode(r.PathValue("id"))
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		mounts := make([]remotenodes.DiskMount, 0, len(node.Inventory.DiskMounts))
		for _, mount := range node.Inventory.DiskMounts {
			mounts = append(mounts, remotenodes.DiskMount{
				Filesystem: mount.Filesystem, Size: mount.Size, Used: mount.Used,
				Available: mount.Available, UsePercent: mount.UsePercent, Mountpoint: mount.Mountpoint,
			})
		}
		if len(mounts) == 0 && node.Inventory.DiskTotal > 0 {
			mounts = append(mounts, remotenodes.DiskMount{
				Filesystem: "root", Size: node.Inventory.DiskTotal, Used: node.Inventory.DiskUsed,
				Available: node.Inventory.DiskAvailable, UsePercent: int(node.Inventory.DiskUsePercent), Mountpoint: "/",
			})
		}
		jsonResponse(w, http.StatusOK, mounts)
	}
}

func handleRemoteNodeDiskCleanupScan(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDiskCleanupScan, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var targets []remotenodes.DiskCleanupTarget
		if err := json.Unmarshal([]byte(task.Result["data"]), &targets); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid disk cleanup inventory")
			return
		}
		jsonResponse(w, http.StatusOK, targets)
	}
}

func handleRemoteNodeDiskCleanupExecute(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var body struct {
			Targets   []string `json:"targets"`
			Confirmed bool     `json:"confirmed"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if !body.Confirmed {
			jsonError(w, http.StatusBadRequest, "explicit remote disk cleanup confirmation is required")
			return
		}
		if len(body.Targets) == 0 || len(body.Targets) > 4 {
			jsonError(w, http.StatusBadRequest, "select between 1 and 4 cleanup targets")
			return
		}

		seen := make(map[string]struct{}, len(body.Targets))
		for _, target := range body.Targets {
			if _, exists := seen[target]; exists {
				jsonError(w, http.StatusBadRequest, "duplicate cleanup target: "+target)
				return
			}
			seen[target] = struct{}{}
		}

		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{
			Kind: agenthub.TaskDiskCleanupExecute, Payload: map[string]string{"targets": strings.Join(body.Targets, ",")},
		})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var execution remotenodes.DiskCleanupExecution
		if err := json.Unmarshal([]byte(task.Result["data"]), &execution); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid disk cleanup receipt")
			return
		}
		for _, result := range execution.Results {
			if result.Status == "ok" {
				auditHostAction(r, "remote_disk_cleanup", fmt.Sprintf("%s: %s reclaimed %d bytes", nodeID, result.ID, result.Reclaimed))
			} else {
				auditHostAction(r, "remote_disk_cleanup", fmt.Sprintf("%s: %s failed: %s", nodeID, result.ID, result.Message))
			}
		}

		jsonResponse(w, http.StatusOK, execution)
	}
}

func handleRemoteNodeFiles(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskFilesBrowse, Payload: map[string]string{"path": r.URL.Query().Get("path")}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var entries []remotenodes.FileEntry
		if err := json.Unmarshal([]byte(task.Result["data"]), &entries); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid file inventory")
			return
		}
		jsonResponse(w, http.StatusOK, entries)
	}
}

func handleRemoteNodeFileGet(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskFilesRead, Payload: map[string]string{"path": r.URL.Query().Get("path")}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var file remotenodes.FileContent
		if err := json.Unmarshal([]byte(task.Result["data"]), &file); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid file content")
			return
		}
		jsonResponse(w, http.StatusOK, &file)
	}
}

func handleRemoteNodeFileSave(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
		var body struct {
			Path     string `json:"path"`
			Content  string `json:"content"`
			Checksum string `json:"checksum"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskFilesWrite, Payload: map[string]string{"path": body.Path, "content_b64": base64.StdEncoding.EncodeToString([]byte(body.Content)), "checksum": body.Checksum}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedNginxTaskError(w, task)
			return
		}
		backup := task.Result["backup"]
		if backup == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid file save receipt")
			return
		}
		auditHostAction(r, "remote_file_save", nodeID+": "+body.Path)
		jsonResponse(w, http.StatusOK, map[string]string{"message": "Remote file saved", "backup": backup})
	}
}

func handleRemoteNodeNginxConfigs(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskNginxConfigList, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedNginxTask(r.Context(), hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedNginxTaskError(w, task)
			return
		}
		var configs []remotenodes.NginxConfig
		if err := json.Unmarshal([]byte(task.Result["data"]), &configs); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid Nginx configuration inventory")
			return
		}
		jsonResponse(w, http.StatusOK, configs)
	}
}

func handleRemoteNodeNginxConfigGet(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskNginxConfigRead, Payload: map[string]string{"name": r.PathValue("name")}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedNginxTask(r.Context(), hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedNginxTaskError(w, task)
			return
		}
		var config remotenodes.NginxConfig
		if err := json.Unmarshal([]byte(task.Result["data"]), &config); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid Nginx configuration")
			return
		}
		jsonResponse(w, http.StatusOK, config)
	}
}

func handleRemoteNodeNginxConfigSave(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
		var body struct {
			Content  string `json:"content"`
			Checksum string `json:"checksum"`
			Reload   bool   `json:"reload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		nodeID := r.PathValue("id")
		name := r.PathValue("name")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskNginxConfigWrite, Payload: map[string]string{
			"name": name, "content_b64": base64.StdEncoding.EncodeToString([]byte(body.Content)), "checksum": body.Checksum, "reload": strconv.FormatBool(body.Reload),
		}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedNginxTaskError(w, task)
			return
		}
		if task.Result["message"] == "" || task.Result["backup"] == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid Nginx save receipt")
			return
		}
		auditHostAction(r, "remote_nginx_config_save", nodeID+": "+name)
		jsonResponse(w, http.StatusOK, map[string]string{"message": task.Result["message"], "backup": task.Result["backup"]})
	}
}

func waitForManagedNginxTask(ctx context.Context, hub *agenthub.Service, nodeID string, taskID int64) (*agenthub.Task, error) {
	waitCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	return waitForManagedNodeTask(waitCtx, hub, nodeID, taskID)
}

func writeManagedNginxTaskError(w http.ResponseWriter, task *agenthub.Task) {
	status := http.StatusBadGateway
	switch task.Result["code"] {
	case "config_changed":
		status = http.StatusConflict
	case "config_invalid":
		status = http.StatusUnprocessableEntity
	case "config_too_large":
		status = http.StatusRequestEntityTooLarge
	}
	jsonError(w, status, task.Error)
}

func handleRemoteNodeNginxAction(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		action := r.PathValue("action")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskNginxAction, Payload: map[string]string{"action": action}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		message := task.Result["message"]
		if message == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid Nginx action receipt")
			return
		}
		auditHostAction(r, "remote_nginx_action", nodeID+": "+action)
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleRemoteNodeDomains(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDomainInventory, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var domains []remotenodes.Domain
		if err := json.Unmarshal([]byte(task.Result["data"]), &domains); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid domain inventory")
			return
		}
		jsonResponse(w, http.StatusOK, domains)
	}
}

func handleRemoteNodeDomainAction(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID, config, action := r.PathValue("id"), r.PathValue("config"), r.PathValue("action")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDomainAction, Payload: map[string]string{"config": config, "action": action}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedNginxTaskError(w, task)
			return
		}
		message := task.Result["message"]
		if message == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid domain action receipt")
			return
		}
		auditHostAction(r, "remote_domain_action", nodeID+": "+config+" "+action)
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleRemoteNodeCertificates(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskSSLInventory, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var certificates []remotenodes.Certificate
		if err := json.Unmarshal([]byte(task.Result["data"]), &certificates); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid SSL certificate inventory")
			return
		}
		jsonResponse(w, http.StatusOK, certificates)
	}
}

func handleRemoteNodeCertificateAction(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID, name, action := r.PathValue("id"), r.PathValue("name"), r.PathValue("action")
		if action == "renew" {
			allowLongSystemAction(w)
		}
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskSSLAction, Payload: map[string]string{"name": name, "action": action}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		timeout := 60 * time.Second
		if action == "renew" {
			timeout = 16 * time.Minute
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		message := task.Result["message"]
		if message == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid SSL certificate action receipt")
			return
		}
		auditHostAction(r, "remote_certificate_action", nodeID+": "+name+" "+action)
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleRemoteNodePHPFPM(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskPHPInventory, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedPHPTask(r.Context(), hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedPHPTaskError(w, task)
			return
		}
		var versions []remotenodes.PHPFPMVersion
		if err := json.Unmarshal([]byte(task.Result["data"]), &versions); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid PHP-FPM inventory")
			return
		}
		jsonResponse(w, http.StatusOK, versions)
	}
}

func handleRemoteNodePHPFPMConfigGet(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskPHPConfigRead, Payload: map[string]string{
			"version": r.PathValue("version"), "pool": r.PathValue("pool"),
		}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedPHPTask(r.Context(), hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedPHPTaskError(w, task)
			return
		}
		var config remotenodes.FileContent
		if err := json.Unmarshal([]byte(task.Result["data"]), &config); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid PHP-FPM configuration")
			return
		}
		jsonResponse(w, http.StatusOK, config)
	}
}

func handleRemoteNodePHPFPMConfigSave(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
		var body struct {
			Content  string `json:"content"`
			Checksum string `json:"checksum"`
			Reload   bool   `json:"reload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		nodeID := r.PathValue("id")
		version := r.PathValue("version")
		pool := r.PathValue("pool")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskPHPConfigWrite, Payload: map[string]string{
			"version": version, "pool": pool, "content_b64": base64.StdEncoding.EncodeToString([]byte(body.Content)), "checksum": body.Checksum, "reload": strconv.FormatBool(body.Reload),
		}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedPHPTaskError(w, task)
			return
		}
		if task.Result["message"] == "" || task.Result["backup"] == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid PHP-FPM save receipt")
			return
		}
		auditHostAction(r, "remote_php_fpm_config_save", nodeID+": PHP "+version+" "+pool)
		jsonResponse(w, http.StatusOK, map[string]string{"message": task.Result["message"], "backup": task.Result["backup"]})
	}
}

func handleRemoteNodePHPFPMAction(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		version := r.PathValue("version")
		action := r.PathValue("action")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskPHPAction, Payload: map[string]string{"version": version, "action": action}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedPHPTaskError(w, task)
			return
		}
		message := task.Result["message"]
		if message == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid PHP-FPM action receipt")
			return
		}
		auditHostAction(r, "remote_php_fpm_action", nodeID+": PHP "+version+" "+action)
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func waitForManagedPHPTask(ctx context.Context, hub *agenthub.Service, nodeID string, taskID int64) (*agenthub.Task, error) {
	waitCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	return waitForManagedNodeTask(waitCtx, hub, nodeID, taskID)
}

func writeManagedPHPTaskError(w http.ResponseWriter, task *agenthub.Task) {
	status := http.StatusBadGateway
	switch task.Result["code"] {
	case "config_changed":
		status = http.StatusConflict
	case "config_invalid":
		status = http.StatusUnprocessableEntity
	case "config_too_large":
		status = http.StatusRequestEntityTooLarge
	}
	jsonError(w, status, task.Error)
}

func handleRemoteNodePM2(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskPM2List, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedPM2Task(r.Context(), hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var processes []remotenodes.PM2Process
		if err := json.Unmarshal([]byte(task.Result["data"]), &processes); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid PM2 inventory")
			return
		}
		jsonResponse(w, http.StatusOK, processes)
	}
}

func handleRemoteNodePM2Action(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		name := r.PathValue("name")
		action := r.PathValue("action")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskPM2Action, Payload: map[string]string{"name": name, "action": action}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		message := task.Result["message"]
		if message == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid PM2 action receipt")
			return
		}
		auditHostAction(r, "remote_pm2_action", nodeID+": "+name+" "+action)
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleRemoteNodePM2Logs(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		if lines < 1 {
			lines = 200
		}
		if lines > 500 {
			lines = 500
		}
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskPM2Logs, Payload: map[string]string{"name": r.PathValue("name"), "lines": strconv.Itoa(lines)}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedPM2Task(r.Context(), hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var logs string
		if err := json.Unmarshal([]byte(task.Result["data"]), &logs); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid PM2 logs")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"logs": logs})
	}
}

func waitForManagedPM2Task(ctx context.Context, hub *agenthub.Service, nodeID string, taskID int64) (*agenthub.Task, error) {
	waitCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	return waitForManagedNodeTask(waitCtx, hub, nodeID, taskID)
}

func handleRemoteNodeCronList(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskCronInventory, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedCronTask(r.Context(), hub, nodeID, task.ID, 35*time.Second)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedCronTaskError(w, task)
			return
		}
		var inventory remotenodes.RemoteCronInventory
		if err := json.Unmarshal([]byte(task.Result["data"]), &inventory); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid cron inventory")
			return
		}
		jsonResponse(w, http.StatusOK, &inventory)
	}
}

func decodeRemoteCronJob(w http.ResponseWriter, r *http.Request) (remotenodes.RemoteCronJob, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var body struct {
		remotenodes.RemoteCronJob
		Revision string `json:"revision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return body.RemoteCronJob, "", false
	}
	return body.RemoteCronJob, body.Revision, true
}

func handleRemoteNodeCronCreate(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, revision, ok := decodeRemoteCronJob(w, r)
		if !ok {
			return
		}
		encoded, _ := json.Marshal(job)
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskCronCreate, Payload: map[string]string{"job_b64": base64.StdEncoding.EncodeToString(encoded), "revision": revision}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedCronTask(r.Context(), hub, nodeID, task.ID, 50*time.Second)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedCronTaskError(w, task)
			return
		}
		id := task.Result["id"]
		if id == "" || task.Result["message"] == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid cron create receipt")
			return
		}
		auditHostAction(r, "remote_cron_create", nodeID+": "+id)
		jsonResponse(w, http.StatusCreated, map[string]string{"id": id, "message": task.Result["message"]})
	}
}

func handleRemoteNodeCronUpdate(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, revision, ok := decodeRemoteCronJob(w, r)
		if !ok {
			return
		}
		job.ID = r.PathValue("job")
		encoded, _ := json.Marshal(job)
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskCronUpdate, Payload: map[string]string{"job_b64": base64.StdEncoding.EncodeToString(encoded), "revision": revision}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedCronTask(r.Context(), hub, nodeID, task.ID, 50*time.Second)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedCronTaskError(w, task)
			return
		}
		auditHostAction(r, "remote_cron_update", nodeID+": "+job.ID)
		jsonResponse(w, http.StatusOK, map[string]string{"message": task.Result["message"]})
	}
}

func handleRemoteNodeCronDelete(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		var body struct {
			Revision string `json:"revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		nodeID := r.PathValue("id")
		id := r.PathValue("job")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskCronDelete, Payload: map[string]string{"id": id, "revision": body.Revision}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedCronTask(r.Context(), hub, nodeID, task.ID, 50*time.Second)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedCronTaskError(w, task)
			return
		}
		auditHostAction(r, "remote_cron_delete", nodeID+": "+id)
		jsonResponse(w, http.StatusOK, map[string]string{"message": task.Result["message"]})
	}
}

func handleRemoteNodeCronRun(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		nodeID := r.PathValue("id")
		id := r.PathValue("job")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskCronRun, Payload: map[string]string{"id": id}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedCronTask(r.Context(), hub, nodeID, task.ID, 140*time.Second)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedCronTaskError(w, task)
			return
		}
		var output string
		if err := json.Unmarshal([]byte(task.Result["data"]), &output); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid cron output")
			return
		}
		auditHostAction(r, "remote_cron_run", nodeID+": "+id)
		jsonResponse(w, http.StatusOK, map[string]string{"message": task.Result["message"], "output": output})
	}
}

func waitForManagedCronTask(ctx context.Context, hub *agenthub.Service, nodeID string, taskID int64, timeout time.Duration) (*agenthub.Task, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return waitForManagedNodeTask(waitCtx, hub, nodeID, taskID)
}

func writeManagedCronTaskError(w http.ResponseWriter, task *agenthub.Task) {
	status := http.StatusBadGateway
	switch task.Result["code"] {
	case "cron_changed":
		status = http.StatusConflict
	case "cron_not_found":
		status = http.StatusNotFound
	case "cron_invalid":
		status = http.StatusUnprocessableEntity
	}
	jsonError(w, status, task.Error)
}

func handleRemoteNodeFirewallList(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskFirewallInventory, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedCronTask(r.Context(), hub, nodeID, task.ID, 35*time.Second)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedFirewallTaskError(w, task)
			return
		}
		var inventory remotenodes.RemoteFirewallInventory
		if err := json.Unmarshal([]byte(task.Result["data"]), &inventory); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid firewall inventory")
			return
		}
		jsonResponse(w, http.StatusOK, &inventory)
	}
}

func handleRemoteNodeFirewallAdd(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var body struct {
			Action   string `json:"action"`
			Protocol string `json:"protocol"`
			Port     int    `json:"port,omitempty"`
			Source   string `json:"source,omitempty"`
			Comment  string `json:"comment,omitempty"`
			Revision string `json:"revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		rule, _ := json.Marshal(struct {
			Action   string `json:"action"`
			Protocol string `json:"protocol"`
			Port     int    `json:"port,omitempty"`
			Source   string `json:"source,omitempty"`
			Comment  string `json:"comment,omitempty"`
		}{body.Action, body.Protocol, body.Port, body.Source, body.Comment})
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskFirewallAdd, Payload: map[string]string{"rule_b64": base64.StdEncoding.EncodeToString(rule), "revision": body.Revision}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedCronTask(r.Context(), hub, nodeID, task.ID, 50*time.Second)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedFirewallTaskError(w, task)
			return
		}
		id := task.Result["id"]
		if id == "" || task.Result["message"] == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid firewall add receipt")
			return
		}
		auditHostAction(r, "remote_firewall_add", nodeID+": "+id)
		jsonResponse(w, http.StatusCreated, map[string]string{"id": id, "message": task.Result["message"]})
	}
}

func handleRemoteNodeFirewallDelete(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		var body struct {
			Revision string `json:"revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		nodeID, id := r.PathValue("id"), r.PathValue("rule")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskFirewallDelete, Payload: map[string]string{"id": id, "revision": body.Revision}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForManagedCronTask(r.Context(), hub, nodeID, task.ID, 50*time.Second)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			writeManagedFirewallTaskError(w, task)
			return
		}
		auditHostAction(r, "remote_firewall_delete", nodeID+": "+id)
		jsonResponse(w, http.StatusOK, map[string]string{"message": task.Result["message"]})
	}
}

func writeManagedFirewallTaskError(w http.ResponseWriter, task *agenthub.Task) {
	status := http.StatusBadGateway
	switch task.Result["code"] {
	case "firewall_changed":
		status = http.StatusConflict
	case "firewall_not_found":
		status = http.StatusNotFound
	case "firewall_invalid", "firewall_protected":
		status = http.StatusUnprocessableEntity
	}
	jsonError(w, status, task.Error)
}

func handleRemoteNodeDatabases(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDatabaseInventory, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var engines []remotenodes.RemoteDatabaseEngine
		if err := json.Unmarshal([]byte(task.Result["data"]), &engines); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid database inventory")
			return
		}
		jsonResponse(w, http.StatusOK, engines)
	}
}

func handleRemoteNodeDatabaseAction(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		nodeID, engine, action := r.PathValue("id"), r.PathValue("engine"), r.PathValue("action")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDatabaseAction, Payload: map[string]string{"engine": engine, "action": action}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 130*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		message := task.Result["message"]
		if message == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid database action receipt")
			return
		}
		auditHostAction(r, "remote_database_action", nodeID+": "+engine+" "+action)
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleRemoteNodeBackups(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskBackupInventory, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 125*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var plans []remotenodes.RemoteBackupPlan
		if err := json.Unmarshal([]byte(task.Result["data"]), &plans); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid backup inventory")
			return
		}
		jsonResponse(w, http.StatusOK, plans)
	}
}

func handleRemoteNodeBackupRun(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowLongSystemAction(w)
		nodeID, plan := r.PathValue("id"), r.PathValue("plan")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskBackupRun, Payload: map[string]string{"plan": plan}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 21*time.Minute)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		message := task.Result["message"]
		if message == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid backup receipt")
			return
		}
		auditHostAction(r, "remote_backup_run", nodeID+": "+plan)
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleRemoteNodeDeployTargets(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDeployInventory, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var targets []remotenodes.RemoteDeployTarget
		if err := json.Unmarshal([]byte(task.Result["data"]), &targets); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid deploy inventory")
			return
		}
		jsonResponse(w, http.StatusOK, targets)
	}
}

func handleRemoteNodeDeployAction(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		targetID := r.PathValue("target")
		action := r.PathValue("action")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDeployAction, Payload: map[string]string{"target": targetID, "action": action}})
		if err != nil {
			auditHostAction(r, "remote_deploy_action", nodeID+": "+targetID+" "+action+" enqueue failed")
			writeAgentHubError(w, err)
			return
		}
		job := remoteDeployJobFromTask(*task)
		auditHostAction(r, "remote_deploy_action", nodeID+": "+targetID+" "+action+" queued as "+job.ID)
		jsonResponse(w, http.StatusAccepted, job)
	}
}

func handleRemoteNodeDeployJobs(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tasks, err := hub.ListTasksForNode(r.PathValue("id"), 50)
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		jobs := make([]remotenodes.RemoteDeployJob, 0, len(tasks))
		for _, task := range tasks {
			if task.Kind == agenthub.TaskDeployAction {
				jobs = append(jobs, remoteDeployJobFromTask(task))
			}
			if len(jobs) == 20 {
				break
			}
		}
		jsonResponse(w, http.StatusOK, jobs)
	}
}

func handleRemoteNodeDeployDomains(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID, targetID := r.PathValue("id"), r.PathValue("target")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDeployDomainInventory, Payload: map[string]string{"target": targetID}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForRemoteDeployDomainTask(r.Context(), hub, nodeID, task.ID, 40*time.Second)
		if err != nil {
			writeRemoteDeployDomainTaskError(w, task, err)
			return
		}
		var domains []remotenodes.RemoteDeployDomain
		if err := json.Unmarshal([]byte(task.Result["data"]), &domains); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid project domain inventory")
			return
		}
		for _, domain := range domains {
			if !validRemoteDeployDomain(domain, targetID, domain.Domain) {
				jsonError(w, http.StatusBadGateway, "managed agent returned invalid project domain inventory")
				return
			}
		}
		jsonResponse(w, http.StatusOK, domains)
	}
}

func handleRemoteNodeDeployDomainCreate(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var request remotenodes.CreateRemoteDeployDomainRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		domain, err := runRemoteDeployDomainAction(r, hub, "create", request.Domain, "", 75*time.Second)
		if err != nil {
			writeRemoteDeployDomainActionError(w, err)
			return
		}
		jsonResponse(w, http.StatusCreated, domain)
	}
}

func handleRemoteNodeDeployDomainDelete(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := runRemoteDeployDomainAction(r, hub, "delete", r.PathValue("domain"), "", 75*time.Second)
		if err != nil {
			writeRemoteDeployDomainActionError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": "Managed project domain removed"})
	}
}

func handleRemoteNodeDeployDomainEnsure(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, err := decodeRemoteDeployDomainEnsureRequest(w, r)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		nodeID, targetID, rawDomain := remoteDeployDomainPathValues(r)
		domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rawDomain), "."))
		task, err := hub.EnqueueDeployDomainEnsure(nodeID, targetID, domain, request.ExpectedRevision)
		if err != nil {
			writeRemoteDeployDomainEnsureEnqueueError(w, err)
			return
		}
		auditHostAction(r, "remote_deploy_domain_ensure", nodeID+": "+targetID+" "+domain+" queued")
		task, err = waitForRemoteDeployDomainTask(r.Context(), hub, nodeID, task.ID, 75*time.Second)
		if err != nil {
			writeRemoteDeployDomainEnsureTaskError(w, task, err)
			return
		}
		response, err := decodeRemoteDeployDomainEnsureReceipt(task, targetID, domain)
		if err != nil {
			auditHostActionFailure(r, "remote_deploy_domain_ensure", nodeID+": "+targetID+" "+domain, err)
			jsonError(w, http.StatusBadGateway, "invalid_receipt")
			return
		}
		auditHostAction(r, "remote_deploy_domain_ensure", nodeID+": "+targetID+" "+domain+" completed")
		jsonResponse(w, http.StatusOK, response)
	}
}

func decodeRemoteDeployDomainEnsureRequest(w http.ResponseWriter, r *http.Request) (remotenodes.EnsureRemoteDeployDomainRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	fields, err := decodeStrictJSONObjectFields(r.Body, map[string]struct{}{
		"expected_revision": {},
		"confirmed":         {},
	})
	if err != nil {
		return remotenodes.EnsureRemoteDeployDomainRequest{}, err
	}
	var request remotenodes.EnsureRemoteDeployDomainRequest
	if raw, ok := fields["confirmed"]; !ok || decodeJSONField(raw, &request.Confirmed) != nil || !request.Confirmed {
		return remotenodes.EnsureRemoteDeployDomainRequest{}, errors.New("confirmed must be true")
	}
	if raw, ok := fields["expected_revision"]; ok {
		if err := decodeJSONField(raw, &request.ExpectedRevision); err != nil ||
			(request.ExpectedRevision != "absent" && !validRemoteDeployDomainRevision(request.ExpectedRevision)) {
			return remotenodes.EnsureRemoteDeployDomainRequest{}, errors.New("expected_revision is invalid")
		}
	} else {
		request.ExpectedRevision = "absent"
	}
	return request, nil
}

func remoteDeployDomainPathValues(r *http.Request) (nodeID, targetID, domain string) {
	nodeID, targetID, domain = r.PathValue("node_id"), r.PathValue("target_id"), r.PathValue("domain")
	if nodeID == "" {
		nodeID = r.PathValue("id")
	}
	if targetID == "" {
		targetID = r.PathValue("target")
	}
	return nodeID, targetID, domain
}

func decodeStrictJSONObjectFields(reader io.Reader, allowed map[string]struct{}) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(reader)
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, errors.New("JSON object required")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("JSON object key required")
		}
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("unknown JSON field %q", key)
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, fmt.Errorf("duplicate JSON field %q", key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		fields[key] = raw
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("JSON object terminator required")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("trailing JSON")
		}
		return nil, err
	}
	return fields, nil
}

func decodeJSONField(raw json.RawMessage, target any) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("JSON null is not allowed")
	}
	return json.Unmarshal(raw, target)
}

func validRemoteDeployDomainRevision(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

func handleRemoteNodeDeployDomainHealth(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID, targetID, domain := r.PathValue("id"), r.PathValue("target"), r.PathValue("domain")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDeployDomainHealth, Payload: map[string]string{"target": targetID, "domain": domain}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		task, err = waitForRemoteDeployDomainTask(r.Context(), hub, nodeID, task.ID, 45*time.Second)
		if err != nil {
			writeRemoteDeployDomainTaskError(w, task, err)
			return
		}
		var health remotenodes.RemoteDeployDomainHealth
		if err := json.Unmarshal([]byte(task.Result["data"]), &health); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid project domain health")
			return
		}
		if !validRemoteDeployDomainHealth(health, domain) {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid project domain health")
			return
		}
		jsonResponse(w, http.StatusOK, health)
	}
}

func handleRemoteNodeDeployDomainTLSEnable(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var request remotenodes.EnableRemoteDeployDomainTLSRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		request.Email = strings.TrimSpace(request.Email)
		if request.Email != "" {
			address, err := mail.ParseAddress(request.Email)
			if err != nil || address.Address != request.Email || address.Name != "" {
				jsonError(w, http.StatusBadRequest, "invalid ACME account email")
				return
			}
		}
		domain, err := runRemoteDeployDomainAction(r, hub, "tls-enable", r.PathValue("domain"), request.Email, 6*time.Minute)
		if err != nil {
			writeRemoteDeployDomainActionError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, domain)
	}
}

func handleRemoteNodeDeployDomainTLSAction(hub *agenthub.Service, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain, err := runRemoteDeployDomainAction(r, hub, action, r.PathValue("domain"), "", 6*time.Minute)
		if err != nil {
			writeRemoteDeployDomainActionError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, domain)
	}
}

func runRemoteDeployDomainAction(r *http.Request, hub *agenthub.Service, action, domain, email string, timeout time.Duration) (*remotenodes.RemoteDeployDomain, error) {
	nodeID, targetID := r.PathValue("id"), r.PathValue("target")
	payload := map[string]string{"target": targetID, "domain": strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), ".")), "action": action}
	if email != "" {
		payload["email"] = email
	}
	task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskDeployDomainAction, Payload: payload})
	if err != nil {
		return nil, err
	}
	auditHostAction(r, "remote_deploy_domain_action", nodeID+": "+targetID+" "+payload["domain"]+" "+action+" queued")
	task, err = waitForRemoteDeployDomainTask(r.Context(), hub, nodeID, task.ID, timeout)
	if err != nil {
		auditHostActionFailure(r, "remote_deploy_domain_action", nodeID+": "+targetID+" "+payload["domain"]+" "+action, err)
		return nil, &remoteDeployDomainActionError{task: task, err: err}
	}
	if action == "delete" {
		auditHostAction(r, "remote_deploy_domain_action", nodeID+": "+targetID+" "+payload["domain"]+" "+action+" completed")
		return nil, nil
	}
	var result remotenodes.RemoteDeployDomain
	if err := json.Unmarshal([]byte(task.Result["data"]), &result); err != nil {
		return nil, errRemoteDeployDomainReceipt
	}
	if !validRemoteDeployDomain(result, targetID, payload["domain"]) {
		return nil, errRemoteDeployDomainReceipt
	}
	auditHostAction(r, "remote_deploy_domain_action", nodeID+": "+targetID+" "+payload["domain"]+" "+action+" completed")
	return &result, nil
}

func decodeRemoteDeployDomainEnsureReceipt(task *agenthub.Task, targetID, domain string) (remotenodes.EnsureRemoteDeployDomainResponse, error) {
	if task == nil || task.Status != agenthub.TaskStatusCompleted || task.Error != "" || len(task.Result) != 1 {
		return remotenodes.EnsureRemoteDeployDomainResponse{}, errors.New("invalid ensure receipt")
	}
	data, ok := task.Result["data"]
	if !ok || strings.TrimSpace(data) == "" {
		return remotenodes.EnsureRemoteDeployDomainResponse{}, errors.New("invalid ensure receipt data")
	}
	fields, err := decodeStrictJSONObjectFields(strings.NewReader(data), map[string]struct{}{
		"changed":     {},
		"observation": {},
	})
	if err != nil || len(fields) != 2 {
		return remotenodes.EnsureRemoteDeployDomainResponse{}, errors.New("invalid ensure receipt object")
	}
	var response remotenodes.EnsureRemoteDeployDomainResponse
	if err := decodeJSONField(fields["changed"], &response.Changed); err != nil {
		return remotenodes.EnsureRemoteDeployDomainResponse{}, errors.New("invalid ensure receipt changed")
	}
	observationFields, err := decodeStrictJSONObjectFields(bytes.NewReader(fields["observation"]), map[string]struct{}{
		"target_id":          {},
		"domain":             {},
		"host_port":          {},
		"desired_host_port":  {},
		"upstream":           {},
		"status":             {},
		"message":            {},
		"tls_status":         {},
		"tls_expires_at":     {},
		"tls_days_remaining": {},
		"tls_message":        {},
		"updated_at":         {},
		"enabled":            {},
		"revision":           {},
	})
	if err != nil {
		return remotenodes.EnsureRemoteDeployDomainResponse{}, errors.New("invalid ensure observation object")
	}
	for _, required := range []string{"target_id", "domain", "host_port", "desired_host_port", "upstream", "status", "message", "tls_status", "tls_message", "enabled", "revision"} {
		if _, ok := observationFields[required]; !ok {
			return remotenodes.EnsureRemoteDeployDomainResponse{}, errors.New("incomplete ensure observation")
		}
	}
	for _, raw := range observationFields {
		if err := decodeJSONField(raw, &struct{}{}); err != nil && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return remotenodes.EnsureRemoteDeployDomainResponse{}, errors.New("null ensure observation field")
		}
	}
	var observation remotenodes.RemoteDeployDomain
	if err := json.Unmarshal(fields["observation"], &observation); err != nil ||
		!validRemoteDeployDomain(observation, targetID, domain) || observation.Status != "active" || !observation.Enabled || !validRemoteDeployDomainRevision(observation.Revision) {
		return remotenodes.EnsureRemoteDeployDomainResponse{}, errors.New("invalid ensure observation")
	}
	response.Observation = observation
	return response, nil
}

func writeRemoteDeployDomainEnsureEnqueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agenthub.ErrInvalidInput):
		jsonError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, agenthub.ErrDeployDomainEnsureConflict):
		jsonError(w, http.StatusConflict, "domain_conflict")
	case errors.Is(err, agenthub.ErrCapabilityUnavailable):
		jsonError(w, http.StatusConflict, "capability")
	case errors.Is(err, agenthub.ErrNodeOffline):
		jsonError(w, http.StatusConflict, "offline")
	default:
		writeAgentHubError(w, err)
	}
}

func writeRemoteDeployDomainEnsureTaskError(w http.ResponseWriter, task *agenthub.Task, err error) {
	if task != nil && task.Status == agenthub.TaskStatusFailed {
		code := task.Result["code"]
		if code == "" {
			code = task.Error
		}
		switch code {
		case "stale", "stale_observation", "domain_drift", "domain_conflict", "offline", "capability", "capability_unavailable":
			jsonError(w, http.StatusConflict, ensureFailureResponseCode(code))
		case "invalid_local_plan":
			jsonError(w, http.StatusUnprocessableEntity, code)
		case "nginx_test", "nginx_test_failed", "nginx_reload", "nginx_reload_failed", "nginx_rollback", "nginx_rollback_failed", "invalid_receipt", "domain_cleanup_failed", "domain_observation_failed", "domain_operation_failed", "domain_operation":
			jsonError(w, http.StatusBadGateway, ensureFailureResponseCode(code))
		case "timeout":
			jsonError(w, http.StatusGatewayTimeout, "timeout")
		default:
			jsonError(w, http.StatusBadGateway, "remote_failure")
		}
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		jsonError(w, http.StatusGatewayTimeout, "timeout")
		return
	}
	jsonError(w, http.StatusBadGateway, "remote_failure")
}

func ensureFailureResponseCode(code string) string {
	switch code {
	case "stale_observation":
		return "stale"
	case "capability_unavailable":
		return "capability"
	default:
		return code
	}
}

var errRemoteDeployDomainReceipt = errors.New("managed agent returned an invalid project domain receipt")

func validRemoteDeployDomain(domain remotenodes.RemoteDeployDomain, targetID, expectedDomain string) bool {
	if domain.TargetID != targetID || domain.Domain != expectedDomain || domain.HostPort < 1 || domain.HostPort > 65535 || domain.DesiredHostPort < 1 || domain.DesiredHostPort > 65535 {
		return false
	}
	if err := agenthub.ValidateTaskRequest(agenthub.TaskRequest{Kind: agenthub.TaskDeployDomainHealth, Payload: map[string]string{"target": targetID, "domain": domain.Domain}}); err != nil {
		return false
	}
	if domain.Upstream != fmt.Sprintf("http://127.0.0.1:%d", domain.HostPort) {
		return false
	}
	if domain.Status != "active" && domain.Status != "drifted" {
		return false
	}
	switch domain.TLSStatus {
	case "not_configured", "healthy", "expiring", "expired", "unavailable":
		return true
	default:
		return false
	}
}

func validRemoteDeployDomainHealth(health remotenodes.RemoteDeployDomainHealth, expectedDomain string) bool {
	if health.Domain != expectedDomain || !strings.HasPrefix(health.Upstream, "http://127.0.0.1:") || health.LatencyMS < 0 {
		return false
	}
	port, err := strconv.Atoi(strings.TrimPrefix(health.Upstream, "http://127.0.0.1:"))
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, health.CheckedAt); err != nil {
		return false
	}
	switch health.Status {
	case "healthy":
		return health.StatusCode >= 200 && health.StatusCode < 400
	case "unhealthy":
		return health.StatusCode >= 100 && health.StatusCode <= 599 && (health.StatusCode < 200 || health.StatusCode >= 400)
	case "unavailable":
		return health.StatusCode == 0
	default:
		return false
	}
}

type remoteDeployDomainActionError struct {
	task *agenthub.Task
	err  error
}

func (err *remoteDeployDomainActionError) Error() string { return err.err.Error() }
func (err *remoteDeployDomainActionError) Unwrap() error { return err.err }

func waitForRemoteDeployDomainTask(parent context.Context, hub *agenthub.Service, nodeID string, taskID int64, timeout time.Duration) (*agenthub.Task, error) {
	waitCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	task, err := waitForManagedNodeTask(waitCtx, hub, nodeID, taskID)
	if err != nil {
		return task, err
	}
	if task.Status == agenthub.TaskStatusFailed {
		return task, errors.New(task.Error)
	}
	return task, nil
}

func writeRemoteDeployDomainTaskError(w http.ResponseWriter, task *agenthub.Task, err error) {
	if task != nil && task.Status == agenthub.TaskStatusFailed {
		jsonError(w, http.StatusBadGateway, task.Error)
		return
	}
	jsonError(w, http.StatusGatewayTimeout, err.Error())
}

func writeRemoteDeployDomainActionError(w http.ResponseWriter, err error) {
	if errors.Is(err, errRemoteDeployDomainReceipt) {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	var taskErr *remoteDeployDomainActionError
	if errors.As(err, &taskErr) {
		writeRemoteDeployDomainTaskError(w, taskErr.task, taskErr.err)
		return
	}
	if errors.Is(err, agenthub.ErrInvalidInput) {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAgentHubError(w, err)
}

func remoteDeployJobFromTask(task agenthub.Task) remotenodes.RemoteDeployJob {
	message := "Queued on managed node"
	if task.Status == agenthub.TaskStatusRunning {
		message = "Running on managed node"
	} else if task.Status == agenthub.TaskStatusCompleted {
		message = task.Result["message"]
	} else if task.Status == agenthub.TaskStatusFailed {
		message = task.Error
	}
	job := remotenodes.RemoteDeployJob{ID: fmt.Sprintf("task-%d", task.ID), TargetID: task.Payload["target"], Action: task.Payload["action"], Status: task.Status, Message: message, CreatedAt: task.CreatedAt.UTC().Format(time.RFC3339Nano), Output: strings.TrimSpace(task.Result["output"])}
	if task.StartedAt != nil {
		job.StartedAt = task.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if task.CompletedAt != nil {
		job.FinishedAt = task.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return job
}

func handleRemoteNodeLogs(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		if lines < 1 {
			lines = 200
		}
		if lines > 500 {
			lines = 500
		}
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskLogsRead, Payload: map[string]string{
			"source": r.URL.Query().Get("source"), "lines": strconv.Itoa(lines),
		}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var logs []remotenodes.LogEntry
		if err := json.Unmarshal([]byte(task.Result["data"]), &logs); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid journal data")
			return
		}
		jsonResponse(w, http.StatusOK, logs)
	}
}

func handleRemoteNodeContainers(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskContainerList, Payload: map[string]string{}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		var containers []remotenodes.Container
		if err := json.Unmarshal([]byte(task.Result["data"]), &containers); err != nil {
			jsonError(w, http.StatusBadGateway, "managed agent returned invalid container inventory")
			return
		}
		jsonResponse(w, http.StatusOK, containers)
	}
}

func handleRemoteNodeContainerAction(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.PathValue("id")
		container := r.PathValue("container")
		action := r.PathValue("action")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{Kind: agenthub.TaskContainerAction, Payload: map[string]string{
			"container": container, "action": action,
		}})
		if err != nil {
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		message := task.Result["message"]
		if message == "" {
			jsonError(w, http.StatusBadGateway, "managed agent returned an invalid container action receipt")
			return
		}
		auditHostAction(r, "remote_container_action", nodeID+": "+container+" "+action)
		jsonResponse(w, http.StatusOK, map[string]string{"message": message})
	}
}

func handleRemoteNodeProcessSignal(hub *agenthub.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PID       int    `json:"pid"`
			StartTime uint64 `json:"startTime"`
			Signal    string `json:"signal"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		nodeID := r.PathValue("id")
		task, err := hub.EnqueueTask(nodeID, agenthub.TaskRequest{
			Kind: agenthub.TaskProcessSignal,
			Payload: map[string]string{
				"pid":        strconv.Itoa(body.PID),
				"start_time": strconv.FormatUint(body.StartTime, 10),
				"signal":     body.Signal,
			},
		})
		if err != nil {
			auditHostActionFailure(r, "remote_process_signal", fmt.Sprintf("%s: PID %d start %d signal %s", nodeID, body.PID, body.StartTime, body.Signal), err)
			writeAgentHubError(w, err)
			return
		}
		waitCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		task, err = waitForManagedNodeTask(waitCtx, hub, nodeID, task.ID)
		if err != nil {
			auditHostActionFailure(r, "remote_process_signal", fmt.Sprintf("%s: PID %d start %d signal %s", nodeID, body.PID, body.StartTime, body.Signal), err)
			jsonError(w, http.StatusGatewayTimeout, err.Error())
			return
		}
		if task.Status == agenthub.TaskStatusFailed {
			taskErr := errors.New(task.Error)
			auditHostActionFailure(r, "remote_process_signal", fmt.Sprintf("%s: PID %d start %d signal %s", nodeID, body.PID, body.StartTime, body.Signal), taskErr)
			jsonError(w, http.StatusBadGateway, task.Error)
			return
		}
		result := remotenodes.ProcessSignalResult{Message: task.Result["message"]}
		result.Exited, _ = strconv.ParseBool(task.Result["exited"])
		result.Confirmed, _ = strconv.ParseBool(task.Result["confirmed"])
		outcome := "still-running"
		if result.Exited {
			outcome = "exited"
		} else if !result.Confirmed {
			outcome = "unconfirmed"
		}
		auditHostAction(r, "remote_process_signal", fmt.Sprintf("%s: PID %d start %d signal %s outcome %s", nodeID, body.PID, body.StartTime, body.Signal, outcome))
		jsonResponse(w, http.StatusOK, result)
	}
}
