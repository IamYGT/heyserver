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
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	contextsConfigVersion   = 1
	maxContextsConfigBytes  = 256 << 10
	maxContextStatusTargets = 64
	contextStatusWorkers    = 8
)

var contextNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

type cliContext struct {
	// Name is populated only on a context selected for a command. It is kept
	// out of the persisted config so the context map remains a server/token
	// record and callers can still retain the name that resolved it.
	Name      string `json:"-"`
	Server    string `json:"server"`
	TokenFile string `json:"token_file"`
}

type cliContextsConfig struct {
	Version  int                   `json:"version"`
	Current  string                `json:"current,omitempty"`
	Contexts map[string]cliContext `json:"contexts"`
}

type cliContextView struct {
	Name      string `json:"name"`
	Server    string `json:"server"`
	TokenFile string `json:"token_file"`
	Current   bool   `json:"current"`
}

// cliContextStatus is deliberately a connection-level view. It identifies
// the context and panel health without including its token-file reference or
// any authenticated response data.
type cliContextStatus struct {
	Name      string `json:"name"`
	Server    string `json:"server"`
	Status    string `json:"status"`
	Current   bool   `json:"current"`
	LatencyMS int64  `json:"latency_ms"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

func defaultContextsFile(getenv func(string) string) string {
	if configured := strings.TrimSpace(getenv("HSERVER_CONTEXT_FILE")); configured != "" {
		return configured
	}
	if configHome := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); configHome != "" {
		return filepath.Join(configHome, "hserver", "contexts.json")
	}
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return filepath.Join(home, ".config", "hserver", "contexts.json")
	}
	return ""
}

func resolveCLIContext(path, requested string) (*cliContext, error) {
	requested = strings.TrimSpace(requested)
	config, exists, err := readContextsConfig(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		if requested != "" {
			return nil, fmt.Errorf("Heyserver context %q does not exist; run hserverctl context add", requested)
		}
		return nil, nil
	}
	name := requested
	if name == "" {
		name = config.Current
	}
	if name == "" {
		return nil, nil
	}
	selected, ok := config.Contexts[name]
	if !ok {
		return nil, fmt.Errorf("Heyserver context %q does not exist; run hserverctl context list", name)
	}
	selected.Name = name
	return &selected, nil
}

// effectiveCLIContextLabel returns the identity that may be shown in the TUI.
// A direct server override is intentionally not attributed to a named context,
// even when that context was also requested for validation or token lookup.
func effectiveCLIContextLabel(selected *cliContext, serverOverridden bool) string {
	if selected != nil && !serverOverridden && strings.TrimSpace(selected.Name) != "" {
		return selected.Name
	}
	return "direct"
}

func runContexts(args []string, path string, out io.Writer) error {
	return runContextsWithContext(context.Background(), args, path, 30*time.Second, out)
}

func runContextsWithContext(ctx context.Context, args []string, path string, timeout time.Duration, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl context list|current|status|add|use|remove")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("context list does not accept arguments")
		}
		return listContexts(path, out)
	case "current":
		if len(args) != 1 {
			return errors.New("context current does not accept arguments")
		}
		return printCurrentContext(path, out)
	case "status":
		return statusContexts(ctx, args[1:], path, timeout, out)
	case "add":
		return addContext(args[1:], path, out)
	case "use":
		if len(args) != 2 {
			return errors.New("usage: hserverctl context use NAME")
		}
		return useContext(args[1], path, out)
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: hserverctl context remove NAME")
		}
		return removeContext(args[1], path, out)
	default:
		return fmt.Errorf("unknown context command %q", args[0])
	}
}

func statusContexts(ctx context.Context, args []string, path string, timeout time.Duration, out io.Writer) error {
	flags := flag.NewFlagSet("context status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outputFormat := flags.String("format", "text", "output format: json or text")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if timeout <= 0 {
		return errors.New("context status timeout must be greater than zero")
	}
	format := strings.ToLower(strings.TrimSpace(*outputFormat))
	if format != "json" && format != "text" {
		return errors.New("context status format must be json or text")
	}
	names := flags.Args()
	if err := validateUniqueValues("context", names, maxContextStatusTargets); err != nil {
		return err
	}

	config, _, err := readContextsConfig(path)
	if err != nil {
		return err
	}
	selected, err := contextStatusSelection(config, names)
	if err != nil {
		return err
	}
	statuses := probeContextStatuses(ctx, selected, config.Current, timeout)
	if format == "json" {
		raw, marshalErr := json.Marshal(statuses)
		if marshalErr != nil {
			return fmt.Errorf("encode context status: %w", marshalErr)
		}
		if err := prettyJSON(out, raw); err != nil {
			return err
		}
	} else if err := writeContextStatusText(out, statuses); err != nil {
		return err
	}

	failed := 0
	for _, status := range statuses {
		if status.Status != "healthy" {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("context status reported %d unhealthy context(s)", failed)
	}
	return nil
}

func contextStatusSelection(config cliContextsConfig, names []string) ([]cliContextView, error) {
	if len(names) == 0 {
		sortedNames := make([]string, 0, len(config.Contexts))
		for name := range config.Contexts {
			sortedNames = append(sortedNames, name)
		}
		sort.Strings(sortedNames)
		names = sortedNames
	}
	selected := make([]cliContextView, 0, len(names))
	for _, name := range names {
		if err := validateContextName(name); err != nil {
			return nil, fmt.Errorf("invalid context %q: %w", name, err)
		}
		item, ok := config.Contexts[name]
		if !ok {
			return nil, fmt.Errorf("Heyserver context %q does not exist; run hserverctl context list", name)
		}
		selected = append(selected, cliContextView{Name: name, Server: item.Server, Current: name == config.Current})
	}
	return selected, nil
}

func probeContextStatuses(ctx context.Context, contexts []cliContextView, current string, timeout time.Duration) []cliContextStatus {
	statuses := make([]cliContextStatus, len(contexts))
	if len(contexts) == 0 {
		return statuses
	}

	workers := contextStatusWorkers
	if len(contexts) < workers {
		workers = len(contexts)
	}
	jobs := make(chan int)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for index := 0; index < workers; index++ {
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				statuses[index] = probeOneContext(ctx, contexts[index], current, timeout)
			}
		}()
	}
	for index := range contexts {
		jobs <- index
	}
	close(jobs)
	waitGroup.Wait()
	return statuses
}

func probeOneContext(ctx context.Context, contextView cliContextView, current string, timeout time.Duration) cliContextStatus {
	started := time.Now()
	status := cliContextStatus{
		Name: contextView.Name, Server: contextView.Server, Status: "unavailable",
		Current: contextView.Name == current,
	}
	setLatency := func() {
		status.LatencyMS = time.Since(started).Milliseconds()
	}
	client, err := newAPIClient(contextView.Server, "", timeout)
	if err != nil {
		status.Status = "invalid"
		status.Error = sanitizeAPIErrorText(err.Error())
		setLatency()
		return status
	}
	raw, err := client.request(ctx, http.MethodGet, "/api/health", nil, false)
	if err != nil {
		status.Error = clientErrorMessage(err)
		setLatency()
		return status
	}
	var health doctorHealthResponse
	if err := json.Unmarshal(raw, &health); err != nil {
		status.Status = "invalid"
		status.Error = fmt.Sprintf("decode /api/health: %s", sanitizeAPIErrorText(err.Error()))
		setLatency()
		return status
	}
	if health.Status != "ok" || health.Version == "" || health.Uptime < 0 {
		status.Status = "unhealthy"
		status.Error = "health endpoint returned an invalid or unhealthy payload"
	} else {
		status.Status = "healthy"
		status.Version = contextStatusTextValue(health.Version)
	}
	setLatency()
	return status
}

func writeContextStatusText(out io.Writer, statuses []cliContextStatus) error {
	fmt.Fprintln(out, "Heyserver context status")
	fmt.Fprintln(out, "NAME\tSTATUS\tLATENCY_MS\tSERVER")
	if len(statuses) == 0 {
		fmt.Fprintln(out, "none")
		return nil
	}
	for _, status := range statuses {
		fmt.Fprintf(out, "%s\t%s\t%d\t%s", contextStatusTextValue(status.Name), contextStatusTextValue(status.Status), status.LatencyMS, contextStatusTextValue(status.Server))
		if status.Current {
			fmt.Fprint(out, "\tcurrent")
		}
		if status.Version != "" {
			fmt.Fprintf(out, "\tversion=%s", contextStatusTextValue(status.Version))
		}
		fmt.Fprintln(out)
		if status.Error != "" {
			fmt.Fprintf(out, "  error: %s\n", contextStatusTextValue(status.Error))
		}
	}
	return nil
}

func contextStatusTextValue(value string) string {
	value = strings.Join(strings.Fields(sanitizeAPIErrorText(value)), " ")
	if value == "" {
		return "N/A"
	}
	return value
}

func listContexts(path string, out io.Writer) error {
	config, _, err := readContextsConfig(path)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(config.Contexts))
	for name := range config.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	views := make([]cliContextView, 0, len(names))
	for _, name := range names {
		item := config.Contexts[name]
		views = append(views, cliContextView{Name: name, Server: item.Server, TokenFile: item.TokenFile, Current: name == config.Current})
	}
	raw, err := json.Marshal(views)
	if err != nil {
		return err
	}
	return prettyJSON(out, raw)
}

func printCurrentContext(path string, out io.Writer) error {
	config, _, err := readContextsConfig(path)
	if err != nil {
		return err
	}
	if config.Current == "" {
		return errors.New("no active Heyserver context; run hserverctl context use NAME")
	}
	item := config.Contexts[config.Current]
	raw, err := json.Marshal(cliContextView{Name: config.Current, Server: item.Server, TokenFile: item.TokenFile, Current: true})
	if err != nil {
		return err
	}
	return prettyJSON(out, raw)
}

func addContext(args []string, path string, out io.Writer) error {
	flags := flag.NewFlagSet("context add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "", "Heyserver base URL")
	tokenFile := flags.String("token-file", "", "protected bearer-token file")
	use := flags.Bool("use", false, "select the context after adding it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	positional := flags.Args()
	if len(positional) != 1 || strings.TrimSpace(*server) == "" {
		return errors.New("usage: hserverctl context add --server URL [--token-file PATH] [--use] NAME")
	}
	name := positional[0]
	if err := validateContextName(name); err != nil {
		return err
	}
	client, err := newAPIClient(*server, "", time.Second)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("context file path is unavailable; set HOME, XDG_CONFIG_HOME, or HSERVER_CONTEXT_FILE")
	}
	selectedTokenFile := strings.TrimSpace(*tokenFile)
	if selectedTokenFile == "" {
		selectedTokenFile = filepath.Join(filepath.Dir(path), "tokens", name)
	}
	selectedTokenFile, err = filepath.Abs(selectedTokenFile)
	if err != nil {
		return fmt.Errorf("resolve context token file: %w", err)
	}
	config, _, err := readContextsConfig(path)
	if err != nil {
		return err
	}
	if _, exists := config.Contexts[name]; exists {
		return fmt.Errorf("Heyserver context %q already exists; remove it before replacing it", name)
	}
	config.Contexts[name] = cliContext{Server: client.baseURL.String(), TokenFile: selectedTokenFile}
	if *use || config.Current == "" {
		config.Current = name
	}
	if err := writeContextsConfig(path, config); err != nil {
		return err
	}
	fmt.Fprintf(out, "Added Heyserver context %q for %s\n", name, client.baseURL.String())
	if config.Current == name {
		fmt.Fprintf(out, "Current context: %s\n", name)
	}
	fmt.Fprintf(out, "Token file: %s\n", selectedTokenFile)
	return nil
}

func useContext(name, path string, out io.Writer) error {
	if err := validateContextName(name); err != nil {
		return err
	}
	config, exists, err := readContextsConfig(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("Heyserver context %q does not exist; run hserverctl context add", name)
	}
	item, ok := config.Contexts[name]
	if !ok {
		return fmt.Errorf("Heyserver context %q does not exist; run hserverctl context list", name)
	}
	config.Current = name
	if err := writeContextsConfig(path, config); err != nil {
		return err
	}
	fmt.Fprintf(out, "Using Heyserver context %q (%s)\n", name, item.Server)
	return nil
}

func removeContext(name, path string, out io.Writer) error {
	if err := validateContextName(name); err != nil {
		return err
	}
	config, exists, err := readContextsConfig(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("Heyserver context %q does not exist", name)
	}
	item, ok := config.Contexts[name]
	if !ok {
		return fmt.Errorf("Heyserver context %q does not exist", name)
	}
	delete(config.Contexts, name)
	if config.Current == name {
		config.Current = ""
	}
	if err := writeContextsConfig(path, config); err != nil {
		return err
	}
	fmt.Fprintf(out, "Removed Heyserver context %q; token file was not deleted: %s\n", name, item.TokenFile)
	return nil
}

func readContextsConfig(path string) (cliContextsConfig, bool, error) {
	empty := cliContextsConfig{Version: contextsConfigVersion, Contexts: make(map[string]cliContext)}
	if strings.TrimSpace(path) == "" {
		return empty, false, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, false, nil
		}
		return empty, false, fmt.Errorf("inspect context file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return empty, false, errors.New("context file must be a regular file and not a symlink")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return empty, false, errors.New("context file must not be writable by group or others")
	}
	if info.Size() > maxContextsConfigBytes {
		return empty, false, fmt.Errorf("context file exceeds %d bytes", maxContextsConfigBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty, false, fmt.Errorf("read context file %s: %w", path, err)
	}
	var config cliContextsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return empty, false, fmt.Errorf("decode context file %s: %w", path, err)
	}
	if config.Version != contextsConfigVersion {
		return empty, false, fmt.Errorf("unsupported context file version %d", config.Version)
	}
	if config.Contexts == nil {
		config.Contexts = make(map[string]cliContext)
	}
	for name, item := range config.Contexts {
		if err := validateContextName(name); err != nil {
			return empty, false, fmt.Errorf("invalid stored context: %w", err)
		}
		if _, err := newAPIClient(item.Server, "", time.Second); err != nil {
			return empty, false, fmt.Errorf("invalid server URL for context %q: %w", name, err)
		}
		if strings.TrimSpace(item.TokenFile) == "" || !filepath.IsAbs(item.TokenFile) {
			return empty, false, fmt.Errorf("context %q token file must be an absolute path", name)
		}
	}
	if config.Current != "" {
		if _, ok := config.Contexts[config.Current]; !ok {
			return empty, false, fmt.Errorf("active context %q does not exist in the context file", config.Current)
		}
	}
	return config, true, nil
}

func writeContextsConfig(path string, config cliContextsConfig) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("context file path is required")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("context file must be a regular file and not a symlink")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	config.Version = contextsConfigVersion
	if config.Contexts == nil {
		config.Contexts = make(map[string]cliContext)
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode context file: %w", err)
	}
	data = append(data, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, ".hserver-contexts-*")
	if err != nil {
		return fmt.Errorf("create temporary context file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace context file: %w", err)
	}
	return nil
}

func validateContextName(name string) error {
	if !contextNamePattern.MatchString(name) {
		return errors.New("context name must be 1-64 characters using letters, numbers, dot, underscore, or hyphen")
	}
	return nil
}
