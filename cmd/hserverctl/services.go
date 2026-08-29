package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

var serviceIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,127}$`)

type managedServiceTarget struct {
	ID           string             `json:"id"`
	Online       bool               `json:"online"`
	Capabilities []string           `json:"capabilities"`
	Inventory    agenthub.Inventory `json:"inventory"`
}

func runServices(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl services list|logs|action")
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("services list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return errors.New("usage: hserverctl services list [--node NODE]")
		}
		nodeID := strings.TrimSpace(*node)
		if nodeID == "" {
			return printRequest(ctx, client, out, http.MethodGet, "/api/system/services", nil, true)
		}
		target, err := getManagedServiceTarget(ctx, client, nodeID)
		if err != nil {
			return err
		}
		services := target.Inventory.Services
		if services == nil {
			services = []agenthub.ServiceState{}
		}
		response := struct {
			Node                 string                  `json:"node"`
			Online               bool                    `json:"online"`
			ObservationAvailable bool                    `json:"observationAvailable"`
			ActionsAvailable     bool                    `json:"actionsAvailable"`
			Services             []agenthub.ServiceState `json:"services"`
		}{
			Node:                 target.ID,
			Online:               target.Online,
			ObservationAvailable: target.Online && hasServiceCapability(target.Capabilities, agenthub.CapabilityServiceStatus),
			ActionsAvailable:     target.Online && hasServiceCapability(target.Capabilities, agenthub.CapabilityServiceAction),
			Services:             services,
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return fmt.Errorf("encode managed service inventory: %w", err)
		}
		return prettyJSON(out, encoded)
	case "logs":
		flags := flag.NewFlagSet("services logs", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		lines := flags.Int("lines", 100, "number of latest journal entries")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 1 {
			return errors.New("usage: hserverctl services logs [--lines N] SERVICE")
		}
		service, err := validateServiceIdentity(flags.Args()[0])
		if err != nil {
			return err
		}
		if *lines < 1 || *lines > 500 {
			return errors.New("service log line count must be between 1 and 500")
		}
		query := url.Values{"lines": []string{fmt.Sprint(*lines)}}
		endpoint := "/api/system/services/" + url.PathEscape(service) + "/logs?" + query.Encode()
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "action":
		flags := flag.NewFlagSet("services action", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		confirmed := flags.Bool("confirm", false, "confirm the service mutation")
		wait := flags.Duration("wait", 2*time.Minute, "maximum action wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 2 {
			return errors.New("usage: hserverctl services action --confirm [--node NODE] [--wait DURATION] SERVICE start|stop|restart")
		}
		if !*confirmed {
			return errors.New("service action requires explicit --confirm")
		}
		if *wait <= 0 || *wait > 7*time.Minute {
			return errors.New("service action wait must be greater than zero and at most 7m")
		}
		service, err := validateServiceIdentity(flags.Args()[0])
		if err != nil {
			return err
		}
		action, err := validateServiceAction(flags.Args()[1])
		if err != nil {
			return err
		}
		nodeID := strings.TrimSpace(*node)
		if nodeID == "" {
			return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/system/actions/service", map[string]string{
				"service": service,
				"action":  action,
			}, true)
		}
		return runManagedServiceAction(ctx, client.withTimeout(*wait), out, nodeID, service, action, *wait)
	default:
		return fmt.Errorf("unknown services command %q", args[0])
	}
}

func validateServiceIdentity(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !serviceIdentityPattern.MatchString(value) {
		return "", errors.New("service identity must be 1-128 portable systemd name characters")
	}
	return value, nil
}

func validateServiceAction(value string) (string, error) {
	switch value {
	case "start", "stop", "restart":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported service action %q", value)
	}
}

func getManagedServiceTarget(ctx context.Context, client *apiClient, nodeID string) (managedServiceTarget, error) {
	target, err := requestJSON[managedServiceTarget](ctx, client, http.MethodGet, "/api/nodes/"+url.PathEscape(nodeID), nil, true)
	if err != nil {
		return target, err
	}
	if target.ID != nodeID {
		return target, fmt.Errorf("managed-node response identity %q does not match requested node %q", target.ID, nodeID)
	}
	return target, nil
}

func hasServiceCapability(capabilities []string, expected string) bool {
	for _, capability := range capabilities {
		if capability == expected {
			return true
		}
	}
	return false
}

func runManagedServiceAction(ctx context.Context, client *apiClient, out io.Writer, nodeID, service, action string, wait time.Duration) error {
	target, err := getManagedServiceTarget(ctx, client, nodeID)
	if err != nil {
		return err
	}
	if !target.Online {
		return errors.New("managed node is offline")
	}
	if !hasServiceCapability(target.Capabilities, agenthub.CapabilityServiceAction) {
		return errors.New("managed agent does not advertise service.action")
	}

	actionContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	endpoint := "/api/nodes/" + url.PathEscape(nodeID) + "/tasks"
	task, err := requestJSON[agenthub.Task](actionContext, client, http.MethodPost, endpoint, map[string]any{
		"kind":    agenthub.TaskServiceAction,
		"payload": map[string]string{"service": service, "action": action},
	}, true)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		switch task.Status {
		case agenthub.TaskStatusCompleted:
			encoded, err := json.Marshal(task)
			if err != nil {
				return fmt.Errorf("encode managed service task: %w", err)
			}
			return prettyJSON(out, encoded)
		case agenthub.TaskStatusFailed:
			message := strings.TrimSpace(task.Error)
			if message == "" {
				message = "managed service action failed"
			}
			return fmt.Errorf("managed service task %d failed: %s", task.ID, message)
		case agenthub.TaskStatusQueued, agenthub.TaskStatusRunning:
		default:
			return fmt.Errorf("managed service task %d returned unsupported status %q", task.ID, task.Status)
		}
		select {
		case <-actionContext.Done():
			return fmt.Errorf("managed service task %d did not complete: %w", task.ID, actionContext.Err())
		case <-ticker.C:
		}
		task, err = requestJSON[agenthub.Task](actionContext, client, http.MethodGet, endpoint+"/"+fmt.Sprint(task.ID), nil, true)
		if err != nil {
			return err
		}
	}
}
