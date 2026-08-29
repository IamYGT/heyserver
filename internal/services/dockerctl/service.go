// Package dockerctl provides bounded local Docker inventory and actions.
package dockerctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const maxLogBytes = 1 << 20

var objectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@-]{0,255}$`)

// ErrNotConfigured identifies a local Docker prerequisite that is explicitly
// absent. A Docker CLI that exists but cannot reach its daemon is unavailable,
// not not-configured.
var ErrNotConfigured = errors.New("Docker integration not configured")

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.Bytes(), err
}

// Service owns the local Docker CLI boundary.
type Service struct {
	runner   commandRunner
	lookPath func(string) (string, error)
}

func New() *Service {
	return &Service{runner: execRunner{}, lookPath: exec.LookPath}
}

type Status struct {
	Installed         bool   `json:"installed"`
	Running           bool   `json:"running"`
	Version           string `json:"version,omitempty"`
	ContainersTotal   int    `json:"containersTotal"`
	ContainersRunning int    `json:"containersRunning"`
	ImageCount        int    `json:"imageCount"`
	Error             string `json:"error,omitempty"`
}

type Container struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Image       string   `json:"image"`
	Status      string   `json:"status"`
	Detail      string   `json:"detail"`
	Ports       []string `json:"ports"`
	CPUPercent  float64  `json:"cpuPercent"`
	MemoryUsage uint64   `json:"memoryUsage"`
	MemoryLimit uint64   `json:"memoryLimit"`
	Created     string   `json:"created"`
}

type Image struct {
	ID       string   `json:"id"`
	RepoTags []string `json:"repoTags"`
	Size     string   `json:"size"`
	Created  string   `json:"created"`
}

type Logs struct {
	Logs      string `json:"logs"`
	Tail      int    `json:"tail"`
	Truncated bool   `json:"truncated"`
}

// ProbeInfo performs the one fresh, read-only Docker observation used by the
// local integration-status aggregate.
func (s *Service) ProbeInfo() (integrationstate.State, error) {
	return s.ProbeInfoContext(context.Background())
}

// ProbeInfoContext verifies the Docker CLI prerequisite and then runs only a
// bounded `docker info`. It intentionally does not infer health from the CLI
// being installed, and it does not collect inventory or mutate Docker state.
func (s *Service) ProbeInfoContext(parent context.Context) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	if s == nil {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	if s.lookPath == nil {
		return integrationstate.Unavailable, errors.New("Docker CLI prerequisite check is unavailable")
	}
	if _, err := s.lookPath("docker"); err != nil {
		return integrationstate.NotConfigured, fmt.Errorf("%w: Docker CLI is not installed", ErrNotConfigured)
	}
	if s.runner == nil {
		return integrationstate.Unavailable, errors.New("Docker command runner is unavailable")
	}
	if _, err := s.run(parent, 5*time.Second, "info"); err != nil {
		return integrationstate.Unavailable, err
	}
	return integrationstate.Healthy, nil
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	if _, err := s.lookPath("docker"); err != nil {
		return Status{Error: "Docker CLI is not installed"}, nil
	}
	status := Status{Installed: true}
	versionOut, err := s.run(ctx, 20*time.Second, "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		status.Error = "Docker daemon is unavailable"
		if clientOut, clientErr := s.run(ctx, 10*time.Second, "--version"); clientErr == nil {
			status.Version = strings.TrimSpace(string(clientOut))
		}
		return status, nil
	}
	status.Running = true
	status.Version = strings.TrimSpace(string(versionOut))

	all, err := s.run(ctx, 20*time.Second, "ps", "-aq")
	if err != nil {
		return status, fmt.Errorf("list Docker containers: %w", err)
	}
	running, err := s.run(ctx, 20*time.Second, "ps", "-q")
	if err != nil {
		return status, fmt.Errorf("list running Docker containers: %w", err)
	}
	images, err := s.run(ctx, 20*time.Second, "images", "-q")
	if err != nil {
		return status, fmt.Errorf("list Docker images: %w", err)
	}
	status.ContainersTotal = lineCount(all)
	status.ContainersRunning = lineCount(running)
	status.ImageCount = uniqueLineCount(images)
	return status, nil
}

func (s *Service) Containers(ctx context.Context) ([]Container, error) {
	out, err := s.run(ctx, 30*time.Second, "ps", "-a", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("list Docker containers: %w", err)
	}
	stats, _ := s.containerStats(ctx)
	containers := make([]Container, 0)
	for _, line := range nonEmptyLines(out) {
		var raw struct {
			ID        string
			Names     string
			Image     string
			State     string
			Status    string
			Ports     string
			CreatedAt string
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil || raw.ID == "" {
			return nil, errors.New("decode Docker container inventory")
		}
		metric := stats[raw.ID]
		ports := []string{}
		for _, port := range strings.Split(raw.Ports, ",") {
			if value := strings.TrimSpace(port); value != "" {
				ports = append(ports, value)
			}
		}
		containers = append(containers, Container{
			ID: raw.ID, Name: raw.Names, Image: raw.Image,
			Status: normalizeContainerState(raw.State), Detail: raw.Status,
			Ports: ports, CPUPercent: metric.cpuPercent,
			MemoryUsage: metric.memoryUsage, MemoryLimit: metric.memoryLimit,
			Created: raw.CreatedAt,
		})
	}
	return containers, nil
}

func (s *Service) Images(ctx context.Context) ([]Image, error) {
	out, err := s.run(ctx, 30*time.Second, "images", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("list Docker images: %w", err)
	}
	byID := map[string]*Image{}
	order := []string{}
	for _, line := range nonEmptyLines(out) {
		var raw struct {
			ID         string
			Repository string
			Tag        string
			Size       string
			CreatedAt  string
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil || raw.ID == "" {
			return nil, errors.New("decode Docker image inventory")
		}
		image := byID[raw.ID]
		if image == nil {
			image = &Image{ID: raw.ID, RepoTags: []string{}, Size: raw.Size, Created: raw.CreatedAt}
			byID[raw.ID] = image
			order = append(order, raw.ID)
		}
		if raw.Repository != "<none>" && raw.Tag != "<none>" {
			image.RepoTags = append(image.RepoTags, raw.Repository+":"+raw.Tag)
		}
	}
	images := make([]Image, 0, len(order))
	for _, id := range order {
		sort.Strings(byID[id].RepoTags)
		images = append(images, *byID[id])
	}
	return images, nil
}

func (s *Service) ContainerLogs(ctx context.Context, id string, tail int) (Logs, error) {
	if !ValidObjectID(id) {
		return Logs{}, errors.New("invalid container ID")
	}
	if tail < 1 || tail > 1000 {
		return Logs{}, errors.New("tail must be between 1 and 1000")
	}
	out, err := s.run(ctx, 30*time.Second, "logs", "--tail", strconv.Itoa(tail), "--timestamps", "--", id)
	if err != nil {
		return Logs{}, fmt.Errorf("read Docker container logs: %w", err)
	}
	truncated := len(out) > maxLogBytes
	if truncated {
		out = out[len(out)-maxLogBytes:]
	}
	return Logs{Logs: string(out), Tail: tail, Truncated: truncated}, nil
}

func (s *Service) ContainerAction(ctx context.Context, id, action string) error {
	if !ValidObjectID(id) {
		return errors.New("invalid container ID")
	}
	if !ValidContainerAction(action) {
		return errors.New("invalid container action")
	}
	args := []string{action, "--", id}
	if action == "remove" {
		args = []string{"rm", "--", id}
	}
	if _, err := s.run(ctx, 2*time.Minute, args...); err != nil {
		return fmt.Errorf("Docker container %s failed: %w", action, err)
	}
	return nil
}

// ValidContainerAction reports whether action belongs to the fixed local
// Docker lifecycle vocabulary.
func ValidContainerAction(action string) bool {
	switch action {
	case "start", "stop", "restart", "pause", "unpause", "remove":
		return true
	default:
		return false
	}
}

func (s *Service) PullImage(ctx context.Context, reference string) error {
	if !ValidObjectID(reference) {
		return errors.New("invalid image reference")
	}
	if _, err := s.run(ctx, 10*time.Minute, "pull", "--", reference); err != nil {
		return fmt.Errorf("Docker image pull failed: %w", err)
	}
	return nil
}

func (s *Service) RemoveImage(ctx context.Context, reference string) error {
	if !ValidObjectID(reference) {
		return errors.New("invalid image reference")
	}
	if _, err := s.run(ctx, 2*time.Minute, "image", "rm", "--", reference); err != nil {
		return fmt.Errorf("Docker image removal failed: %w", err)
	}
	return nil
}

func ValidObjectID(value string) bool {
	return objectIDPattern.MatchString(value) && !strings.Contains(value, "..")
}

func (s *Service) run(parent context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	out, err := s.runner.Run(ctx, "docker", args...)
	if contextErr := ctx.Err(); contextErr != nil {
		if errors.Is(contextErr, context.DeadlineExceeded) {
			return out, fmt.Errorf("Docker command timed out: %w", contextErr)
		}
		return out, contextErr
	}
	return out, err
}

type containerMetric struct {
	cpuPercent  float64
	memoryUsage uint64
	memoryLimit uint64
}

func (s *Service) containerStats(ctx context.Context) (map[string]containerMetric, error) {
	out, err := s.run(ctx, 30*time.Second, "stats", "--no-stream", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	metrics := map[string]containerMetric{}
	for _, line := range nonEmptyLines(out) {
		var raw struct {
			Container string
			CPUPerc   string
			MemUsage  string
		}
		if json.Unmarshal([]byte(line), &raw) != nil || raw.Container == "" {
			continue
		}
		parts := strings.SplitN(raw.MemUsage, "/", 2)
		metric := containerMetric{cpuPercent: parsePercent(raw.CPUPerc)}
		if len(parts) == 2 {
			metric.memoryUsage = parseDockerBytes(strings.TrimSpace(parts[0]))
			metric.memoryLimit = parseDockerBytes(strings.TrimSpace(parts[1]))
		}
		metrics[raw.Container] = metric
	}
	return metrics, nil
}

func normalizeContainerState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "running":
		return "running"
	case "paused":
		return "paused"
	case "restarting":
		return "restarting"
	case "exited", "dead":
		return "exited"
	default:
		return "stopped"
	}
}

func parsePercent(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "%")), 64)
	return parsed
}

func parseDockerBytes(value string) uint64 {
	value = strings.TrimSpace(value)
	index := 0
	for index < len(value) && ((value[index] >= '0' && value[index] <= '9') || value[index] == '.') {
		index++
	}
	if index == 0 {
		return 0
	}
	number, err := strconv.ParseFloat(value[:index], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToUpper(strings.TrimSpace(value[index:]))
	multiplier := float64(1)
	switch unit {
	case "KB":
		multiplier = 1e3
	case "KIB":
		multiplier = 1 << 10
	case "MB":
		multiplier = 1e6
	case "MIB":
		multiplier = 1 << 20
	case "GB":
		multiplier = 1e9
	case "GIB":
		multiplier = 1 << 30
	case "TB":
		multiplier = 1e12
	case "TIB":
		multiplier = 1 << 40
	}
	return uint64(number * multiplier)
}

func lineCount(value []byte) int { return len(nonEmptyLines(value)) }

func uniqueLineCount(value []byte) int {
	unique := map[string]struct{}{}
	for _, line := range nonEmptyLines(value) {
		unique[line] = struct{}{}
	}
	return len(unique)
}

func nonEmptyLines(value []byte) []string {
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(value)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
