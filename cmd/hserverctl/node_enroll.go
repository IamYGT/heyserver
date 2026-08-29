package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	nodeEnrollNameMaxBytes = 255
	nodeEnrollInterval     = "30s"
	nodeEnrollTokenPath    = "/etc/hserver-agent.token"
)

var nodeEnrollIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type nodeEnrollOptions struct {
	confirmed       bool
	nodeID          string
	name            string
	tokenPath       string
	environmentPath string
}

type nodeEnrollRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type nodeEnrollResponse struct {
	Node struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"node"`
	Token string `json:"token"`
}

type nodeEnrollReservation struct {
	path      string
	file      *os.File
	completed bool
}

func runNodeEnroll(ctx context.Context, client *apiClient, args []string, out io.Writer) (resultErr error) {
	options, err := parseNodeEnrollArgs(args)
	if err != nil {
		return err
	}
	if !options.confirmed {
		return errors.New("node enrollment requires explicit --confirm")
	}
	if err := validateNodeEnrollID(options.nodeID); err != nil {
		return err
	}
	name, err := validateNodeEnrollName(options.name)
	if err != nil {
		return err
	}
	tokenPath, err := validateNodeEnrollOutputPath(options.tokenPath, "agent token output")
	if err != nil {
		return err
	}
	environmentPath, err := validateNodeEnrollOutputPath(options.environmentPath, "agent environment output")
	if err != nil {
		return err
	}
	if same, err := nodeEnrollSamePath(tokenPath, environmentPath); err != nil {
		return fmt.Errorf("compare agent output paths: %w", err)
	} else if same {
		return errors.New("agent token and environment output paths must be distinct")
	}

	hubURL, err := nodeEnrollHubURL(client)
	if err != nil {
		return err
	}
	environment := nodeEnrollEnvironment(hubURL, options.nodeID)
	reservations, err := reserveNodeEnrollOutputs(tokenPath, environmentPath)
	if err != nil {
		return err
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if cleanupErr := cleanupNodeEnrollReservations(reservations); cleanupErr != nil {
			if resultErr != nil {
				resultErr = fmt.Errorf("%w; credential reservation cleanup failed: %v", resultErr, cleanupErr)
			} else {
				resultErr = fmt.Errorf("credential reservation cleanup failed: %w", cleanupErr)
			}
		}
	}()

	raw, err := client.request(ctx, http.MethodPost, "/api/nodes", nodeEnrollRequest{ID: options.nodeID, Name: name}, true)
	if err != nil {
		return err
	}
	response, err := decodeNodeEnrollResponse(raw, options.nodeID)
	if err != nil {
		return err
	}

	if err := reservations[0].write([]byte(response.Token + "\n")); err != nil {
		return fmt.Errorf("node created but credential persistence failed: %w", err)
	}
	if err := reservations[1].write(environment); err != nil {
		return fmt.Errorf("node created but credential persistence failed: %w", err)
	}

	// The deferred cleanup keeps failed reservations from surviving an error.
	// Both files are complete and closed here, so the successful receipt is the
	// only output beyond the local paths and requested node identity.
	fmt.Fprintf(out, "Enrolled node %s (%s); agent token: %s; agent environment: %s\n", options.nodeID, name, tokenPath, environmentPath)
	return nil
}

func parseNodeEnrollArgs(args []string) (nodeEnrollOptions, error) {
	flags := flag.NewFlagSet("nodes enroll", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm managed-node enrollment")
	nodeID := flags.String("id", "", "managed node ID")
	name := flags.String("name", "", "managed node display name")
	tokenPath := flags.String("agent-token-output", "", "new one-time agent token file")
	environmentPath := flags.String("agent-env-output", "", "new agent environment file")
	if err := flags.Parse(args); err != nil {
		return nodeEnrollOptions{}, err
	}
	if len(flags.Args()) != 0 {
		return nodeEnrollOptions{}, errors.New(nodeEnrollUsage)
	}
	return nodeEnrollOptions{
		confirmed:       *confirmed,
		nodeID:          *nodeID,
		name:            *name,
		tokenPath:       *tokenPath,
		environmentPath: *environmentPath,
	}, nil
}

const nodeEnrollUsage = "usage: hserverctl nodes enroll --confirm --id ID --name NAME --agent-token-output PATH --agent-env-output PATH"

func validateNodeEnrollID(value string) error {
	if !nodeEnrollIDPattern.MatchString(value) {
		return errors.New("node ID must start with a letter or digit and contain only letters, digits, dot, underscore, or hyphen (maximum 128 bytes)")
	}
	return nil
}

func validateNodeEnrollName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("node name must not be blank")
	}
	if !utf8.ValidString(value) {
		return "", errors.New("node name must be valid UTF-8")
	}
	if len(value) > nodeEnrollNameMaxBytes {
		return "", fmt.Errorf("node name must not exceed %d bytes", nodeEnrollNameMaxBytes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", errors.New("node name must not contain control characters")
		}
	}
	return value, nil
}

func validateNodeEnrollOutputPath(raw, label string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" || path == "-" {
		return "", fmt.Errorf("%s path is required and stdout is not allowed", label)
	}
	if strings.IndexByte(path, 0) >= 0 || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s path must not contain control characters", label)
	}
	cleanPath := filepath.Clean(path)
	if cleanPath != path {
		return "", fmt.Errorf("%s path must be clean", label)
	}
	path = cleanPath
	if path == "-" {
		return "", fmt.Errorf("%s path is required and stdout is not allowed", label)
	}
	parent := filepath.Dir(path)
	if err := validateNodeEnrollParent(parent, label); err != nil {
		return "", err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s path must not already exist, including as a symlink", label)
		}
		return "", fmt.Errorf("%s path already exists", label)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect %s path: %w", label, err)
	}
	return path, nil
}

func validateNodeEnrollParent(parent, label string) error {
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return fmt.Errorf("resolve %s parent: %w", label, err)
	}
	absParent = filepath.Clean(absParent)
	info, err := os.Lstat(absParent)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s parent directory does not exist", label)
		}
		return fmt.Errorf("inspect %s parent directory: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s parent directory must not be a symlink", label)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s parent path is not a directory", label)
	}
	if info.Mode().Perm()&0o222 == 0 || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s parent directory is not writable", label)
	}
	resolved, err := filepath.EvalSymlinks(absParent)
	if err != nil {
		return fmt.Errorf("inspect %s parent directory: %w", label, err)
	}
	if filepath.Clean(resolved) != absParent {
		return fmt.Errorf("%s parent path must not contain symlinks", label)
	}
	return nil
}

func nodeEnrollSamePath(first, second string) (bool, error) {
	firstAbs, err := filepath.Abs(first)
	if err != nil {
		return false, err
	}
	secondAbs, err := filepath.Abs(second)
	if err != nil {
		return false, err
	}
	return filepath.Clean(firstAbs) == filepath.Clean(secondAbs), nil
}

func reserveNodeEnrollOutputs(tokenPath, environmentPath string) ([]*nodeEnrollReservation, error) {
	token, err := reserveNodeEnrollOutput(tokenPath, "agent token output")
	if err != nil {
		return nil, err
	}
	environment, err := reserveNodeEnrollOutput(environmentPath, "agent environment output")
	if err != nil {
		_ = token.cleanup()
		return nil, err
	}
	return []*nodeEnrollReservation{token, environment}, nil
}

func reserveNodeEnrollOutput(path, label string) (*nodeEnrollReservation, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%s path already exists", label)
		}
		return nil, fmt.Errorf("reserve %s: %w", label, err)
	}
	reservation := &nodeEnrollReservation{path: path, file: file}
	if err := file.Chmod(0o600); err != nil {
		_ = reservation.cleanup()
		return nil, fmt.Errorf("protect %s: %w", label, err)
	}
	return reservation, nil
}

func (reservation *nodeEnrollReservation) write(data []byte) error {
	if reservation == nil || reservation.file == nil {
		return errors.New("reserved output is not open")
	}
	if err := reservation.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate reserved output: %w", err)
	}
	if _, err := reservation.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek reserved output: %w", err)
	}
	n, err := reservation.file.Write(data)
	if err != nil {
		return fmt.Errorf("write reserved output: %w", err)
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if err := reservation.file.Sync(); err != nil {
		return fmt.Errorf("sync reserved output: %w", err)
	}
	// The complete content is synced before close. If close itself reports an
	// error, retaining this completed file is safer than deleting a credential
	// that may already be durable.
	reservation.completed = true
	err = reservation.file.Close()
	reservation.file = nil
	if err != nil {
		return fmt.Errorf("close reserved output: %w", err)
	}
	return nil
}

func (reservation *nodeEnrollReservation) cleanup() error {
	if reservation == nil {
		return nil
	}
	var cleanupErrors []error
	if reservation.file != nil {
		if !reservation.completed {
			if err := reservation.file.Truncate(0); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		if err := reservation.file.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
		reservation.file = nil
	}
	if !reservation.completed {
		if err := os.Remove(reservation.path); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func cleanupNodeEnrollReservations(reservations []*nodeEnrollReservation) error {
	var cleanupErrors []error
	for _, reservation := range reservations {
		if err := reservation.cleanup(); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func decodeNodeEnrollResponse(raw []byte, requestedID string) (nodeEnrollResponse, error) {
	var response nodeEnrollResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nodeEnrollResponse{}, fmt.Errorf("decode node enrollment response: %w", err)
	}
	if response.Node.ID != requestedID {
		return nodeEnrollResponse{}, errors.New("node enrollment response did not identify the requested node")
	}
	token := strings.TrimSpace(response.Token)
	if token == "" {
		return nodeEnrollResponse{}, errors.New("node enrollment response did not contain a token")
	}
	if token != response.Token || strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return nodeEnrollResponse{}, errors.New("node enrollment response contained an invalid token")
	}
	if !utf8.ValidString(token) {
		return nodeEnrollResponse{}, errors.New("node enrollment response contained an invalid token")
	}
	response.Token = token
	return response, nil
}

func nodeEnrollHubURL(client *apiClient) (string, error) {
	if client == nil || client.baseURL == nil {
		return "", errors.New("HServer base URL is not configured")
	}
	base := *client.baseURL
	base.User = nil
	base.Path = ""
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	hubURL := strings.TrimRight(base.String(), "/")
	if hubURL == "" {
		return "", errors.New("HServer base URL is empty")
	}
	return hubURL, nil
}

func nodeEnrollEnvironment(hubURL, nodeID string) []byte {
	lines := []string{
		"# Optional agent capabilities are intentionally disabled.",
		"HSERVER_AGENT_HUB_URL=" + hubURL,
		"HSERVER_AGENT_NODE_ID=" + nodeID,
		"HSERVER_AGENT_TOKEN_FILE=" + nodeEnrollTokenPath,
		"HSERVER_AGENT_INTERVAL=" + nodeEnrollInterval,
		"HSERVER_AGENT_OBSERVED_SERVICES=",
		"HSERVER_AGENT_ALLOWED_SERVICES=",
		"HSERVER_AGENT_ALLOWED_HOST_ACTIONS=",
		"HSERVER_AGENT_ALLOW_PROCESS_SIGNALS=false",
		"HSERVER_AGENT_ALLOW_TERMINAL=false",
		"HSERVER_AGENT_ALLOWED_DISK_CLEANUP=",
		"HSERVER_AGENT_ALLOWED_LOG_SOURCES=",
		"HSERVER_AGENT_ALLOW_CONTAINER_READ=false",
		"HSERVER_AGENT_ALLOWED_CONTAINER_ACTIONS=",
		"HSERVER_AGENT_ALLOWED_NGINX_ACTIONS=",
		"HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ=false",
		"HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE=false",
		"HSERVER_AGENT_ALLOW_DOMAIN_READ=false",
		"HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS=false",
		"HSERVER_AGENT_ALLOW_SSL_READ=false",
		"HSERVER_AGENT_ALLOW_SSL_ACTIONS=false",
		"HSERVER_AGENT_ALLOW_DATABASE_READ=false",
		"HSERVER_AGENT_ALLOWED_DATABASE_RESTARTS=",
		"HSERVER_AGENT_ALLOW_BACKUP_READ=false",
		"HSERVER_AGENT_ALLOW_BACKUP_RUN=false",
		"HSERVER_AGENT_ALLOW_DEPLOY_READ=false",
		"HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=false",
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=false",
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=false",
		"HSERVER_AGENT_ALLOW_UPDATE_READ=false",
		"HSERVER_AGENT_ALLOW_UPDATE_ACTIONS=false",
		"HSERVER_AGENT_FILE_READ_ROOTS=",
		"HSERVER_AGENT_FILE_WRITE_ROOTS=",
		"HSERVER_AGENT_ALLOWED_PHP_ACTIONS=",
		"HSERVER_AGENT_ALLOW_PHP_CONFIG_READ=false",
		"HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE=false",
		"HSERVER_AGENT_ALLOW_PM2_READ=false",
		"HSERVER_AGENT_ALLOWED_PM2_ACTIONS=",
		"HSERVER_AGENT_ALLOW_CRON_READ=false",
		"HSERVER_AGENT_ALLOW_CRON_WRITE=false",
		"HSERVER_AGENT_ALLOW_CRON_RUN=false",
		"HSERVER_AGENT_ALLOW_FIREWALL_READ=false",
		"HSERVER_AGENT_ALLOW_FIREWALL_WRITE=false",
		"",
	}
	return []byte(strings.Join(lines, "\n"))
}
