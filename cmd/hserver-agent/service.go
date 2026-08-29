package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

const commandTimeout = 15 * time.Second

const maxCommandOutputBytes = 512 << 10

type commandRunner interface {
	run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var output boundedBuffer
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return nil, err
	}
	return output.bytes, nil
}

type boundedBuffer struct {
	bytes []byte
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	remaining := maxCommandOutputBytes - len(b.bytes)
	if remaining <= 0 {
		return 0, errors.New("command output exceeds size limit")
	}
	if len(data) > remaining {
		b.bytes = append(b.bytes, data[:remaining]...)
		return remaining, errors.New("command output exceeds size limit")
	}
	b.bytes = append(b.bytes, data...)
	return len(data), nil
}

var _ io.Writer = (*boundedBuffer)(nil)

type serviceController struct {
	runner commandRunner
}

func (s serviceController) status(ctx context.Context, service string) (agenthub.ServiceState, error) {
	if !servicePattern.MatchString(service) {
		return agenthub.ServiceState{}, errors.New("invalid service name")
	}
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := s.runner.run(commandCtx, "systemctl", "show", "--property=ActiveState,SubState", "--value", service)
	if err != nil {
		return agenthub.ServiceState{}, fmt.Errorf("systemctl status failed: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || lines[0] == "" {
		return agenthub.ServiceState{}, errors.New("systemctl returned an invalid status")
	}
	return agenthub.ServiceState{Name: service, Active: lines[0], Sub: lines[1]}, nil
}

func (s serviceController) action(ctx context.Context, service, action string) error {
	if !servicePattern.MatchString(service) {
		return errors.New("invalid service name")
	}
	switch action {
	case "start", "stop", "restart":
	default:
		return errors.New("unsupported service action")
	}
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if _, err := s.runner.run(commandCtx, "systemctl", action, service); err != nil {
		return fmt.Errorf("systemctl action failed: %w", err)
	}
	return nil
}
