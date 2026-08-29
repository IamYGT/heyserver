package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

const maxComposeLogBytes = 1 << 20

var (
	ErrDeployTargetNotFound      = errors.New("deploy target not found")
	ErrComposeTargetRequired     = errors.New("Docker Compose target required")
	ErrComposeProjectUnavailable = errors.New("Compose project directory is unavailable")
	composeServicePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// ComposeServices returns observed containers for one configured Compose
// target. Commands always run inside the persisted project directory and use
// only the target's already validated relative Compose file.
func (s *Service) ComposeServices(targetID int64) ([]models.ComposeService, error) {
	target, err := s.composeTarget(targetID)
	if err != nil {
		return nil, err
	}
	args, err := s.composeTargetCommandArgs(target, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	out, err := runCmdTimeout(target.ProjectDir, 30*time.Second, "docker", args...)
	if err != nil {
		return nil, composeCommandError("list Compose services", out, err)
	}
	services, err := decodeComposeServices(out)
	if err != nil {
		return nil, fmt.Errorf("decode Compose service inventory: %w", err)
	}
	return services, nil
}

// ComposeServiceLogs returns bounded timestamped logs across every replica of
// one Compose service.
func (s *Service) ComposeServiceLogs(targetID int64, service string, tail int) (models.ComposeServiceLogs, error) {
	target, err := s.composeTarget(targetID)
	if err != nil {
		return models.ComposeServiceLogs{}, err
	}
	if !validComposeService(service) {
		return models.ComposeServiceLogs{}, errors.New("invalid Compose service name")
	}
	if tail < 1 || tail > 1000 {
		return models.ComposeServiceLogs{}, errors.New("tail must be between 1 and 1000")
	}
	args, err := s.composeTargetCommandArgs(target, "logs", "--no-color", "--timestamps", "--tail", strconv.Itoa(tail), service)
	if err != nil {
		return models.ComposeServiceLogs{}, err
	}
	out, err := runCmdTimeout(target.ProjectDir, 30*time.Second, "docker", args...)
	if err != nil {
		return models.ComposeServiceLogs{}, composeCommandError("read Compose service logs", out, err)
	}
	truncated := len(out) > maxComposeLogBytes
	if truncated {
		out = out[len(out)-maxComposeLogBytes:]
	}
	return models.ComposeServiceLogs{Logs: out, Tail: tail, Truncated: truncated}, nil
}

// ComposeServiceAction executes one fixed service-scoped lifecycle operation.
// It deliberately exposes no project-wide down command and never adds force
// flags or accepts an arbitrary command from the request.
func (s *Service) ComposeServiceAction(targetID int64, service, action string) error {
	target, err := s.composeTarget(targetID)
	if err != nil {
		return err
	}
	if !validComposeService(service) {
		return errors.New("invalid Compose service name")
	}
	var (
		command []string
		timeout = 2 * time.Minute
	)
	switch action {
	case "start", "stop", "restart":
		command = []string{action, service}
	case "recreate":
		command = []string{"up", "-d", "--build", "--no-deps", service}
		timeout = 10 * time.Minute
	default:
		return errors.New("invalid Compose service action")
	}
	args, err := s.composeTargetCommandArgs(target, command...)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := runCmdTimeout(target.ProjectDir, timeout, "docker", args...)
	if err != nil {
		return composeCommandError("run Compose service action", out, err)
	}
	return nil
}

func (s *Service) composeTarget(targetID int64) (*models.DeployTarget, error) {
	target, err := s.configuredComposeTarget(targetID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target.ProjectDir)
	if err != nil || !info.IsDir() {
		return nil, ErrComposeProjectUnavailable
	}
	return target, nil
}

func validComposeService(service string) bool {
	return composeServicePattern.MatchString(service) && !strings.Contains(service, "..")
}

func composeCommandError(operation, output string, err error) error {
	if detail := boundedProcessDetail(output); detail != "" {
		return fmt.Errorf("%s: %s", operation, detail)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

type composePublisher struct {
	URL           string `json:"URL"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

type composeServiceJSON struct {
	Service    string             `json:"Service"`
	Name       string             `json:"Name"`
	Image      string             `json:"Image"`
	State      string             `json:"State"`
	Health     string             `json:"Health"`
	ExitCode   int                `json:"ExitCode"`
	Publishers []composePublisher `json:"Publishers"`
}

func decodeComposeServices(output string) ([]models.ComposeService, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return []models.ComposeService{}, nil
	}
	raw := []composeServiceJSON{}
	if strings.HasPrefix(output, "[") {
		if err := json.Unmarshal([]byte(output), &raw); err != nil {
			return nil, err
		}
	} else {
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var item composeServiceJSON
			if err := json.Unmarshal([]byte(line), &item); err != nil {
				return nil, err
			}
			raw = append(raw, item)
		}
	}
	services := make([]models.ComposeService, 0, len(raw))
	for _, item := range raw {
		if !validComposeService(item.Service) || strings.TrimSpace(item.Name) == "" {
			return nil, errors.New("service inventory contains an invalid identity")
		}
		ports := make([]string, 0, len(item.Publishers))
		for _, publisher := range item.Publishers {
			protocol := strings.TrimSpace(publisher.Protocol)
			if protocol == "" {
				protocol = "tcp"
			}
			if publisher.PublishedPort > 0 {
				host := strings.TrimSpace(publisher.URL)
				if host == "" {
					host = "0.0.0.0"
				}
				ports = append(ports, fmt.Sprintf("%s:%d->%d/%s", host, publisher.PublishedPort, publisher.TargetPort, protocol))
			} else if publisher.TargetPort > 0 {
				ports = append(ports, fmt.Sprintf("%d/%s", publisher.TargetPort, protocol))
			}
		}
		sort.Strings(ports)
		services = append(services, models.ComposeService{
			Service: item.Service, Container: item.Name, Image: item.Image,
			State:    strings.ToLower(strings.TrimSpace(item.State)),
			Health:   strings.ToLower(strings.TrimSpace(item.Health)),
			ExitCode: item.ExitCode, Ports: ports,
		})
	}
	sort.SliceStable(services, func(i, j int) bool {
		if services[i].Service == services[j].Service {
			return services[i].Container < services[j].Container
		}
		return services[i].Service < services[j].Service
	})
	return services, nil
}
