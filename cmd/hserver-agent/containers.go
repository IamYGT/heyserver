package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const maxContainers = 256

var agentContainerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type containerState struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
	Ports  string `json:"ports"`
}

type containerController struct {
	runner         commandRunner
	allowRead      bool
	allowedActions map[string]struct{}
}

func newContainerController(runner commandRunner, allowRead bool, allowedActions map[string]struct{}) containerController {
	return containerController{runner: runner, allowRead: allowRead, allowedActions: allowedActions}
}

func (c containerController) List(ctx context.Context) ([]containerState, error) {
	if !c.allowRead {
		return nil, errors.New("container inventory is not enabled locally")
	}
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := c.runner.run(commandCtx, "docker", "ps", "-a", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("docker container list failed: %w", err)
	}
	return parseContainerStates(output)
}

// Probe performs a bounded docker-info observation for managed integration
// status. The command output is intentionally discarded; no container
// inventory, paths, or daemon diagnostics cross the task boundary.
func (c containerController) Probe(ctx context.Context) (integrationstate.State, error) {
	if !c.allowRead {
		return integrationstate.NotConfigured, errors.New("Docker integration is not configured locally")
	}
	if c.runner == nil {
		return integrationstate.Unavailable, errors.New("Docker status probe is unavailable")
	}
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := c.runner.run(commandCtx, "docker", "info"); err != nil {
		return integrationstate.Unavailable, errors.New("Docker status probe failed")
	}
	return integrationstate.Healthy, nil
}

func (c containerController) Action(ctx context.Context, container, action string) (string, error) {
	if !agentContainerNamePattern.MatchString(container) {
		return "", errors.New("invalid container identity")
	}
	if _, allowed := c.allowedActions[action]; !allowed {
		return "", errors.New("container action is not in the local allowlist")
	}
	commandCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if _, err := c.runner.run(commandCtx, "docker", action, "--", container); err != nil {
		return "", fmt.Errorf("docker container %s failed: %w", action, err)
	}
	return fmt.Sprintf("Container %s completed for %s", action, container), nil
}

func parseContainerStates(output []byte) ([]containerState, error) {
	containers := make([]containerState, 0, 32)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 64*1024), maxCommandOutputBytes)
	for scanner.Scan() {
		var row struct {
			ID     string `json:"ID"`
			Names  string `json:"Names"`
			Image  string `json:"Image"`
			State  string `json:"State"`
			Status string `json:"Status"`
			Ports  string `json:"Ports"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.ID == "" {
			continue
		}
		containers = append(containers, containerState{ID: row.ID, Name: row.Names, Image: row.Image, State: row.State, Status: row.Status, Ports: row.Ports})
		if len(containers) == maxContainers {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse docker container list: %w", err)
	}
	return containers, nil
}
