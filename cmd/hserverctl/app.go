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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/config"
)

const defaultServerURL = "http://127.0.0.1:3085"

type stringValues []string

func (values *stringValues) String() string { return strings.Join(*values, ",") }
func (values *stringValues) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	return presentCLIError(runWithInput(ctx, args, os.Stdin, stdout, stderr, getenv))
}

// cliErrorPresentation keeps the original error available to callers while
// making fmt-based CLI output use the safe, actionable client presentation.
// main.go prints run's result with %v, so this is the narrow boundary where
// API state, recovery advice, and the selected server become user-visible
// without changing the established Error() text used by scripts/tests.
type cliErrorPresentation struct {
	cause   error
	message string
}

func (e *cliErrorPresentation) Error() string {
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

func (e *cliErrorPresentation) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *cliErrorPresentation) Format(state fmt.State, verb rune) {
	if e == nil {
		return
	}
	// The CLI currently renders errors with %v. Treat %s as the same safe
	// presentation for callers that use a string-oriented formatter; retain
	// one bounded message for all other verbs rather than exposing the cause.
	_, _ = io.WriteString(state, e.message)
}

func presentCLIError(err error) error {
	if err == nil {
		return nil
	}
	var presented *cliErrorPresentation
	if errors.As(err, &presented) {
		return err
	}
	message := clientErrorMessage(err)
	if message == err.Error() {
		return err
	}
	return &cliErrorPresentation{cause: err, message: message}
}

func runWithInput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	global := flag.NewFlagSet("hserverctl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	server := global.String("server", envDefault(getenv("HSERVER_URL"), defaultServerURL), "Heyserver base URL")
	tokenFile := global.String("token-file", defaultTokenFile(getenv), "protected bearer-token file")
	contextName := global.String("context", strings.TrimSpace(getenv("HSERVER_CONTEXT")), "named Heyserver context")
	timeout := global.Duration("timeout", 30*time.Second, "request timeout")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			writeUsage(stdout)
			return nil
		}
		return err
	}
	remaining := global.Args()
	if len(remaining) == 0 {
		writeUsage(stdout)
		return nil
	}
	if helpArgs, ok := cliHelpRequest(remaining); ok {
		return writeCommandHelp(stdout, helpArgs)
	}

	command := remaining[0]
	commandArgs := remaining[1:]
	explicitFlags := make(map[string]bool)
	global.Visit(func(item *flag.Flag) { explicitFlags[item.Name] = true })
	if command == "context" {
		return runContextsWithContext(ctx, commandArgs, defaultContextsFile(getenv), *timeout, stdout)
	}
	if command == "connect" {
		connectTokenFile := ""
		if explicitFlags["token-file"] || strings.TrimSpace(getenv("HSERVER_TOKEN_FILE")) != "" {
			connectTokenFile = *tokenFile
		}
		return runConnect(ctx, commandArgs, defaultContextsFile(getenv), *server, connectTokenFile, *timeout, stdin, stdout, stderr)
	}
	if command == "version" {
		if len(commandArgs) != 0 {
			return errors.New("version does not accept arguments")
		}
		fmt.Fprintf(stdout, "hserverctl %s (%s, %s)\n", config.Version, config.BuildCommit, config.BuildDate)
		return nil
	}
	if command == "completion" {
		return runCompletion(commandArgs, stdout)
	}

	serverOverridden := explicitFlags["server"] || strings.TrimSpace(getenv("HSERVER_URL")) != ""
	contextRequested := explicitFlags["context"] || strings.TrimSpace(getenv("HSERVER_CONTEXT")) != ""
	var selectedContext *cliContext
	var err error
	if !serverOverridden || contextRequested {
		selectedContext, err = resolveCLIContext(defaultContextsFile(getenv), *contextName)
		if err != nil {
			return err
		}
	}
	if selectedContext != nil && !serverOverridden {
		*server = selectedContext.Server
		if !explicitFlags["token-file"] && strings.TrimSpace(getenv("HSERVER_TOKEN_FILE")) == "" {
			*tokenFile = selectedContext.TokenFile
		}
	}
	contextLabel := effectiveCLIContextLabel(selectedContext, serverOverridden)

	if command == "login" {
		client, err := newAPIClient(*server, "", *timeout)
		if err != nil {
			return err
		}
		return runLogin(ctx, client, commandArgs, *tokenFile, stdin, stdout, stderr)
	}
	if command == "logout" {
		return runLogout(commandArgs, *tokenFile, getenv("HSERVER_TOKEN"), stdout)
	}

	token := ""
	authenticated := command == "whoami" || command == "doctor" || command == "updates" || command == "ui" || command == "terminal" || command == "host" || command == "system" || command == "services" || command == "disk" || command == "nodes" || command == "tasks" || command == "containers" || command == "images" || command == "deploy" || command == "uptime" || command == "logs" || command == "pm2" || command == "processes" || command == "metrics" || command == "monitoring" || command == "nginx" || command == "domains" || command == "ssl" || command == "backups" || command == "firewall" || command == "cron" || command == "databases" || command == "files" || command == "php" || command == "security" || command == "audit" || command == "users" || command == "notify" || command == "cloudflare" || command == "dns" || command == "integrations" || command == "settings" || command == "mail"
	if authenticated {
		var err error
		token, err = loadToken(*tokenFile, getenv("HSERVER_TOKEN"))
		if err != nil {
			return err
		}
	}
	client, err := newAPIClient(*server, token, *timeout)
	if err != nil {
		return err
	}

	switch command {
	case "whoami":
		if len(commandArgs) != 0 {
			return errors.New("whoami does not accept arguments")
		}
		return printRequest(ctx, client, stdout, http.MethodGet, "/api/auth/me", nil, true)
	case "doctor":
		return runDoctor(ctx, client, commandArgs, stdout)
	case "updates":
		return runUpdates(ctx, client, commandArgs, stdout)
	case "ui":
		return runUIWithContext(ctx, client, commandArgs, contextLabel, stdout)
	case "terminal":
		return runTerminal(ctx, client, commandArgs, stdout)
	case "health":
		if len(commandArgs) != 0 {
			return errors.New("health does not accept arguments")
		}
		return printRequest(ctx, client, stdout, http.MethodGet, "/api/health", nil, false)
	case "openapi":
		if len(commandArgs) != 0 {
			return errors.New("openapi does not accept arguments")
		}
		return printRequest(ctx, client, stdout, http.MethodGet, "/openapi.json", nil, false)
	case "host":
		return runHost(ctx, client, commandArgs, stdout)
	case "system":
		return runSystem(ctx, client, commandArgs, stdout)
	case "services":
		return runServices(ctx, client, commandArgs, stdout)
	case "disk":
		return runDisk(ctx, client, commandArgs, stdout)
	case "nodes":
		return runNodes(ctx, client, commandArgs, stdout)
	case "tasks":
		return runTasks(ctx, client, commandArgs, stdout)
	case "containers":
		return runContainers(ctx, client, commandArgs, stdout)
	case "images":
		return runImages(ctx, client, commandArgs, stdout)
	case "deploy":
		return runDeploy(ctx, client, commandArgs, stdout)
	case "uptime":
		return runUptime(ctx, client, commandArgs, stdout)
	case "logs":
		return runLogs(ctx, client, commandArgs, stdout)
	case "pm2":
		return runPM2(ctx, client, commandArgs, stdout)
	case "processes":
		return runProcesses(ctx, client, commandArgs, stdout)
	case "metrics":
		return runMetrics(ctx, client, commandArgs, stdout)
	case "monitoring":
		return runMonitoring(ctx, client, commandArgs, stdout)
	case "nginx":
		return runNginx(ctx, client, commandArgs, stdout, stderr, getenv)
	case "domains":
		return runDomains(ctx, client, commandArgs, stdout)
	case "ssl":
		return runSSL(ctx, client, commandArgs, stdout)
	case "backups":
		return runBackups(ctx, client, commandArgs, stdout)
	case "firewall":
		return runFirewall(ctx, client, commandArgs, stdout)
	case "cron":
		return runCron(ctx, client, commandArgs, stdout)
	case "databases":
		return runDatabases(ctx, client, commandArgs, stdout)
	case "files":
		return runFiles(ctx, client, commandArgs, stdout)
	case "php":
		return runPHP(ctx, client, commandArgs, stdout, stderr, getenv)
	case "security":
		return runSecurity(ctx, client, commandArgs, stdout)
	case "audit":
		return runAudit(ctx, client, commandArgs, stdout)
	case "users":
		return runUsers(ctx, client, commandArgs, stdin, stdout, stderr)
	case "notify":
		return runNotify(ctx, client, commandArgs, stdout)
	case "cloudflare":
		return runCloudflare(ctx, client, commandArgs, stdout)
	case "dns":
		return runDNS(ctx, client, commandArgs, stdout)
	case "integrations":
		return runIntegrations(ctx, client, commandArgs, stdout)
	case "settings":
		return runSettings(ctx, client, commandArgs, stdout)
	case "mail":
		return runMail(ctx, client, commandArgs, stdout)
	default:
		return fmt.Errorf("unknown command %q; run hserverctl help", command)
	}
}

func cliHelpRequest(remaining []string) ([]string, bool) {
	if len(remaining) == 0 {
		return nil, false
	}

	command := remaining[0]
	if command == "help" {
		return removeHelpFlags(remaining[1:]), true
	}
	for _, arg := range remaining[1:] {
		if isCLIHelpFlag(arg) {
			return removeHelpFlags(append([]string{command}, remaining[1:]...)), true
		}
	}
	return nil, false
}

func isCLIHelpFlag(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "-help"
}

func removeHelpFlags(args []string) []string {
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if isCLIHelpFlag(arg) {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

func runNodes(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "enroll" {
		return runNodeEnroll(ctx, client, args[1:], out)
	}
	if len(args) > 0 && args[0] == "profile" {
		return runNodeProfile(ctx, client, args[1:], out)
	}
	if len(args) == 1 && args[0] == "list" {
		return printRequest(ctx, client, out, http.MethodGet, "/api/nodes", nil, true)
	}
	if len(args) == 2 && args[0] == "get" {
		return printRequest(ctx, client, out, http.MethodGet, "/api/nodes/"+url.PathEscape(args[1]), nil, true)
	}
	if len(args) > 0 && args[0] == "action" {
		flags := flag.NewFlagSet("nodes action", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm the managed-node mutation")
		wait := flags.Duration("wait", 7*time.Minute, "maximum action wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		positional := flags.Args()
		if len(positional) != 2 {
			return errors.New("usage: hserverctl nodes action --confirm [--wait DURATION] NODE ACTION")
		}
		if !*confirmed {
			return errors.New("managed-node action requires explicit --confirm")
		}
		if err := validateHostAction(positional[1]); err != nil {
			return err
		}
		if *wait <= 0 {
			return errors.New("action wait must be greater than zero")
		}
		endpoint := "/api/nodes/" + url.PathEscape(positional[0]) + "/actions/" + url.PathEscape(positional[1])
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
	}
	return errors.New("usage: hserverctl nodes list | nodes get NODE | nodes enroll --confirm --id ID --name NAME --agent-token-output PATH --agent-env-output PATH | nodes action --confirm NODE ACTION | nodes profile get NODE | nodes profile set --confirm --profile-file PATH NODE | nodes profile export NODE [--format env-fragment] | nodes profile apply --confirm [--wait DURATION] NODE")
}

func runHost(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 1 && args[0] == "status" {
		return printRequest(ctx, client, out, http.MethodGet, "/api/system/actions/status", nil, true)
	}
	if len(args) == 1 && args[0] == "reboot-status" {
		return printRequest(ctx, client, out, http.MethodGet, "/api/system/actions/reboot-status", nil, true)
	}
	if len(args) > 0 && args[0] == "action" {
		flags := flag.NewFlagSet("host action", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm the local-host mutation")
		wait := flags.Duration("wait", 7*time.Minute, "maximum action wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		positional := flags.Args()
		if len(positional) != 1 {
			return errors.New("usage: hserverctl host action --confirm [--wait DURATION] ACTION")
		}
		if !*confirmed {
			return errors.New("local-host action requires explicit --confirm")
		}
		if err := validateHostAction(positional[0]); err != nil {
			return err
		}
		if *wait <= 0 {
			return errors.New("action wait must be greater than zero")
		}
		endpoint := "/api/system/actions/" + url.PathEscape(positional[0])
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
	}
	return errors.New("usage: hserverctl host status | host reboot-status | host action --confirm ACTION")
}

func runDisk(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl disk overview|analysis|dirsize|io|largest|list|mounts|smart|usage|scan|clean")
	}
	switch args[0] {
	case "overview":
		return runDiskOverview(ctx, client, args[1:], out)
	case "analysis", "dirsize", "io", "largest", "list", "mounts", "smart", "usage":
		return runDiskDiagnostics(ctx, client, args, out)
	case "scan":
		flags := flag.NewFlagSet("disk scan", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return errors.New("usage: hserverctl disk scan [--node NODE]")
		}
		endpoint := "/api/disk/cleanup/scan"
		if *node != "" {
			endpoint = "/api/nodes/" + url.PathEscape(*node) + "/disk/cleanup"
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "clean":
		flags := flag.NewFlagSet("disk clean", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		confirmed := flags.Bool("confirm", false, "confirm cleanup of the selected fixed targets")
		wait := flags.Duration("wait", 7*time.Minute, "maximum cleanup wait")
		var targets stringValues
		flags.Var(&targets, "target", "fixed cleanup target ID (repeatable)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 || len(targets) == 0 {
			return errors.New("usage: hserverctl disk clean --confirm [--node NODE] --target ID [--target ID]...")
		}
		if !*confirmed {
			return errors.New("disk cleanup requires explicit --confirm")
		}
		if *wait <= 0 {
			return errors.New("cleanup wait must be greater than zero")
		}
		maximum := 20
		if *node != "" {
			maximum = 4
		}
		if err := validateUniqueValues("cleanup target", targets, maximum); err != nil {
			return err
		}
		endpoint := "/api/disk/cleanup/execute"
		request := map[string]any{"targets": []string(targets)}
		if *node != "" {
			endpoint = "/api/nodes/" + url.PathEscape(*node) + "/disk/cleanup"
			request["confirmed"] = true
		}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, request, true)
	default:
		return fmt.Errorf("unknown disk command %q", args[0])
	}
}

func runTasks(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl tasks list|get|create NODE")
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("tasks list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		limit := flags.Int("limit", 20, "maximum tasks")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		positional := flags.Args()
		if len(positional) != 1 {
			return errors.New("usage: hserverctl tasks list [--limit N] NODE")
		}
		if *limit < 1 || *limit > 50 {
			return errors.New("task limit must be between 1 and 50")
		}
		endpoint := "/api/nodes/" + url.PathEscape(positional[0]) + "/tasks?limit=" + strconv.Itoa(*limit)
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "get":
		if len(args) != 3 {
			return errors.New("usage: hserverctl tasks get NODE TASK_ID")
		}
		if _, err := positiveTaskID(args[2]); err != nil {
			return err
		}
		endpoint := "/api/nodes/" + url.PathEscape(args[1]) + "/tasks/" + args[2]
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "create":
		flags := flag.NewFlagSet("tasks create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm queueing a task on the managed node")
		kind := flags.String("kind", "", "fixed agent task kind")
		var payloadFlags stringValues
		flags.Var(&payloadFlags, "payload", "task payload KEY=VALUE (repeatable)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		positional := flags.Args()
		if len(positional) != 1 || strings.TrimSpace(*kind) == "" {
			return errors.New("usage: hserverctl tasks create --confirm --kind KIND [--payload KEY=VALUE]... NODE")
		}
		if !*confirmed {
			return fmt.Errorf("task creation for managed node %q requires explicit --confirm", positional[0])
		}
		payload, err := parsePayload(payloadFlags)
		if err != nil {
			return err
		}
		request := map[string]any{"kind": strings.TrimSpace(*kind), "confirmed": true}
		if len(payload) > 0 {
			request["payload"] = payload
		}
		endpoint := "/api/nodes/" + url.PathEscape(positional[0]) + "/tasks"
		return printRequest(ctx, client, out, http.MethodPost, endpoint, request, true)
	default:
		return fmt.Errorf("unknown tasks command %q", args[0])
	}
}

func runLogin(ctx context.Context, client *apiClient, args []string, tokenFile string, input io.Reader, out, promptOut io.Writer) error {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "administrator email")
	passwordFile := flags.String("password-file", "", "file containing the password")
	totpFile := flags.String("totp-file", "", "optional file containing the current TOTP code")
	replace := flags.Bool("replace", false, "replace an existing token file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || strings.TrimSpace(*email) == "" {
		return errors.New("usage: hserverctl [--token-file PATH] login --email EMAIL [--password-file PATH] [--totp-file PATH] [--replace]")
	}
	token, err := authenticateWithInput(ctx, client, *email, *passwordFile, *totpFile, input, promptOut)
	if err != nil {
		return err
	}
	if err := writeTokenFile(tokenFile, token, *replace); err != nil {
		return err
	}
	fmt.Fprintf(out, "Authenticated token stored in %s\n", tokenFile)
	return nil
}

func authenticateWithInput(ctx context.Context, client *apiClient, email, passwordFile, totpFile string, input io.Reader, promptOut io.Writer) (string, error) {
	var password string
	var err error
	if strings.TrimSpace(passwordFile) != "" {
		password, err = readSecretFile(passwordFile, 8<<10)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
	} else {
		password, err = readInteractiveSecret(input, promptOut, "Heyserver password: ", "--password-file", 8<<10)
		if err != nil {
			return "", err
		}
	}
	email = strings.TrimSpace(email)
	raw, err := client.request(ctx, http.MethodPost, "/api/auth/login", map[string]string{
		"email": email, "password": password,
	}, false)
	if err != nil {
		return "", err
	}
	var login struct {
		Token        string `json:"token"`
		RequiresTOTP bool   `json:"requires_totp"`
	}
	if err := json.Unmarshal(raw, &login); err != nil {
		return "", fmt.Errorf("decode login response: %w", err)
	}
	if login.RequiresTOTP {
		var code string
		if strings.TrimSpace(totpFile) != "" {
			code, err = readSecretFile(totpFile, 128)
			if err != nil {
				return "", fmt.Errorf("read TOTP file: %w", err)
			}
		} else {
			code, err = readInteractiveSecret(input, promptOut, "TOTP code: ", "--totp-file", 128)
			if err != nil {
				return "", fmt.Errorf("this account requires TOTP: %w", err)
			}
		}
		raw, err = client.request(ctx, http.MethodPost, "/api/auth/totp-verify", map[string]string{
			"email": email, "password": password, "code": strings.TrimSpace(code),
		}, false)
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(raw, &login); err != nil {
			return "", fmt.Errorf("decode TOTP response: %w", err)
		}
	}
	if strings.TrimSpace(login.Token) == "" {
		return "", errors.New("login response did not contain a token")
	}
	return strings.TrimSpace(login.Token), nil
}

func runLogout(args []string, tokenFile, environmentToken string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("logout does not accept arguments")
	}
	removed, err := removeTokenFile(tokenFile)
	if err != nil {
		return err
	}
	if removed {
		fmt.Fprintf(out, "Removed stored Heyserver token from %s\n", tokenFile)
	} else {
		fmt.Fprintf(out, "No stored Heyserver token found at %s\n", tokenFile)
	}
	if strings.TrimSpace(environmentToken) != "" {
		fmt.Fprintln(out, "HSERVER_TOKEN remains active in this environment; unset it to end that session.")
	}
	return nil
}

func printRequest(ctx context.Context, client *apiClient, out io.Writer, method, endpoint string, payload any, authenticated bool) error {
	raw, err := client.request(ctx, method, endpoint, payload, authenticated)
	if err != nil {
		return err
	}
	return prettyJSON(out, raw)
}

func parsePayload(values []string) (map[string]string, error) {
	if len(values) > 6 {
		return nil, errors.New("task payload accepts at most 6 fields")
	}
	payload := make(map[string]string, len(values))
	for _, value := range values {
		key, item, found := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, fmt.Errorf("invalid payload %q; expected KEY=VALUE", value)
		}
		if _, exists := payload[key]; exists {
			return nil, fmt.Errorf("duplicate payload key %q", key)
		}
		payload[key] = item
	}
	return payload, nil
}

func validateHostAction(action string) error {
	switch action {
	case "memory-optimize", "swap-reset", "temp-clean", "reboot", "reboot-cancel":
		return nil
	default:
		return fmt.Errorf("unsupported host action %q", action)
	}
}

func validateUniqueValues(name string, values []string, maximum int) error {
	if len(values) > maximum {
		return fmt.Errorf("%s accepts at most %d values", name, maximum)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", name)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func positiveTaskID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("task ID must be a positive integer")
	}
	return id, nil
}

func envDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func defaultTokenFile(getenv func(string) string) string {
	if configured := strings.TrimSpace(getenv("HSERVER_TOKEN_FILE")); configured != "" {
		return configured
	}
	if configHome := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); configHome != "" {
		return filepath.Join(configHome, "hserver", "token")
	}
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return filepath.Join(home, ".config", "hserver", "token")
	}
	return ""
}

type cliHelpEntry struct {
	usage string
	path  []string
}

// cliCommandUsage is the single command metadata source used by both the
// top-level usage and per-command help. Keep entries in the same order as the
// public CLI surface and put the complete synopsis on each entry.
const cliCommandUsage = `
version
health
openapi
context list
context current
context status [--format json|text] [NAME...]
context add --server URL [--token-file PATH] [--use] NAME
context use NAME
context remove NAME
connect --server URL --email EMAIL [--password-file PATH] [--totp-file PATH] [--token-file PATH] NAME
login --email EMAIL [--password-file PATH] [--totp-file PATH] [--replace]
whoami
logout
doctor [--format json|text] [--output PATH] [--node NODE] [--require-architecture amd64|arm64] [--require-capability NAME]...
updates status
updates stage-status
updates stage --confirm [--wait DURATION]
updates install --confirm [--wait DURATION]
updates agent status --node NODE [--wait DURATION]
updates agent upgrade --confirm --node NODE [--wait DURATION]
updates agent rollback --confirm --node NODE [--wait DURATION]
ui [--refresh DURATION]
terminal [--node NODE]
host status
host reboot-status
host action --confirm [--wait DURATION] ACTION
system info
system stats
system actions process --pid PID --start-time START --signal term|kill --confirm [--wait DURATION]
services list [--node NODE]
services logs [--lines N] SERVICE
services action --confirm [--node NODE] [--wait DURATION] SERVICE start|stop|restart
nodes list
nodes get NODE
nodes enroll --confirm --id ID --name NAME --agent-token-output PATH --agent-env-output PATH
nodes action --confirm [--wait DURATION] NODE ACTION
nodes profile get NODE
nodes profile set --confirm --profile-file PATH NODE
nodes profile export NODE [--format env-fragment]
nodes profile apply --confirm [--wait DURATION] NODE
disk overview [--node NODE]
disk analysis start --confirm [--format json|table]
disk analysis status [--format json|table]
disk dirsize [--format json|table] PATH
disk io [--format json|table]
disk largest [--limit N] [--format json|table] PATH
disk list [--format json|table] [PATH]
disk mounts [--format json|table]
disk smart [--format json|table] DEVICE
disk usage [--depth N] [--format json|table] PATH
disk scan [--node NODE]
disk clean --confirm [--node NODE] --target ID [--target ID]...
containers status
containers list [--node NODE]
containers logs [--tail N] CONTAINER
containers action --confirm [--node NODE] [--wait DURATION] CONTAINER ACTION
images list
images pull --confirm [--wait DURATION] IMAGE
images delete --confirm [--wait DURATION] IMAGE
deploy templates
deploy targets
deploy target create --confirm --name NAME --project-dir PATH [OPTIONS]
deploy target update --confirm [OPTIONS] TARGET
deploy target delete --confirm TARGET
deploy environment list TARGET
deploy environment set --confirm --value-file PATH TARGET KEY
deploy environment delete --confirm TARGET KEY
deploy domains TARGET
deploy domain create --confirm --service SERVICE --host-port PORT [--wait DURATION] TARGET DOMAIN
deploy domain health TARGET DOMAIN_ID
deploy domain tls enable --confirm [--email EMAIL] [--wait DURATION] TARGET DOMAIN_ID
deploy domain tls disable --confirm [--wait DURATION] TARGET DOMAIN_ID
deploy domain delete --confirm [--wait DURATION] TARGET DOMAIN_ID
deploy staging create --confirm --project-dir PATH [--name NAME] [--branch BRANCH] SOURCE_TARGET
deploy revision TARGET
deploy preflight TARGET
deploy history [--target TARGET] [--limit N]
deploy logs RUN
deploy run --confirm [--wait DURATION] TARGET
deploy rollback --confirm [--wait DURATION] TARGET
deploy services TARGET
deploy service logs [--tail N] TARGET SERVICE
deploy service action --confirm [--wait DURATION] TARGET SERVICE ACTION
deploy remote targets --node NODE
deploy remote jobs --node NODE
deploy remote action --confirm --node NODE TARGET ACTION
deploy remote domains --node NODE TARGET
deploy remote domain-ensure --confirm --node NODE [--wait DURATION] [--format json|text|table] TARGET DOMAIN
deploy remote domain-create --confirm --node NODE TARGET DOMAIN
deploy remote domain-delete --confirm --node NODE TARGET DOMAIN
deploy remote domain-health --node NODE TARGET DOMAIN
deploy remote tls-provision --confirm --node NODE [--email EMAIL] TARGET DOMAIN
deploy remote tls-renew --confirm --node NODE TARGET DOMAIN
deploy remote tls-delete --confirm --node NODE TARGET DOMAIN
uptime summary
uptime monitors
uptime monitor get ID
uptime monitor create --confirm --name NAME --type http|tcp|ping|dns [OPTIONS]
uptime monitor update --confirm [OPTIONS] ID
uptime monitor heartbeats [--hours N] ID
uptime monitor stats ID
uptime monitor check --confirm [--wait DURATION] ID
uptime monitor pause --confirm [--wait DURATION] ID
uptime monitor resume --confirm [--wait DURATION] ID
uptime monitor delete --confirm [--wait DURATION] ID
uptime incidents [--monitor ID] [--limit N]
uptime status-pages
uptime status-page create --confirm --slug SLUG --title TITLE [OPTIONS]
uptime status-page update --confirm [OPTIONS] ID
uptime status-page delete --confirm ID
uptime settings
uptime settings update --confirm [OPTIONS]
uptime import-domains --confirm
logs sources [--node NODE]
logs read [--node NODE] --source SOURCE [--lines N]
pm2 list [--node NODE]
pm2 get PROCESS
pm2 logs [--node NODE] [--lines N] PROCESS
pm2 action --confirm [--node NODE] [--wait DURATION] PROCESS ACTION
pm2 save --confirm
processes signal --node NODE --pid PID --start-time START --signal term|kill --confirm [--wait DURATION]
metrics history [--range 1h|6h|24h|7d|30d] [--format json|table]
metrics processes [--at RFC3339] [--format json|table]
metrics processes timestamps [--range 1h|6h|24h|7d|30d] [--format json|table]
metrics services history [--range 1h|6h|24h|7d|30d] [--format json|table]
metrics summary [--format json|table]
monitoring stats [--node NODE] [--format json|table]
monitoring processes [--format json|table]
nginx status
nginx configs [--node NODE]
nginx archives
nginx backups
nginx snippets
nginx get [--node NODE] CONFIG
nginx create --confirm --type php|static|proxy|redirect [OPTIONS] DOMAIN
nginx edit --confirm [--node NODE] [--editor EXECUTABLE] [--wait DURATION] CONFIG
nginx enable --confirm [--node NODE] [--wait DURATION] CONFIG
nginx disable --confirm [--node NODE] [--wait DURATION] CONFIG
nginx archive --confirm [--checksum SHA256] [--wait DURATION] CONFIG
nginx restore --confirm [--checksum SHA256] [--wait DURATION] ARCHIVE
nginx rollback --confirm [--backup-checksum SHA256] [--current-checksum SHA256] [--wait DURATION] BACKUP
nginx test [--node NODE] [--wait DURATION]
nginx reload --confirm [--node NODE] [--wait DURATION]
nginx save --confirm [--node NODE] --content-file PATH --checksum SHA256 CONFIG
domains list [--node NODE]
domains provisioning
domains get DOMAIN_ID
domains check DOMAIN
domains create --confirm [--type php|proxy|static] [--php-version VERSION] [--web-root PATH] [--fpm-preset low|medium|high] [--proxy-port PORT] [--spa] [--www-redirect] [--issue-ssl --ssl-email EMAIL] [--existing-cert NAME] [--create-dns-record] [--pm2-app NAME --pm2-script PATH] [--pm2-cwd PATH] [--pm2-port PORT] [--node-env production|development] [--isolated-linux-user] [--wait DURATION] DOMAIN
domains action --confirm [--node NODE] [--wait DURATION] TARGET enable|disable
domains delete --confirm [--delete-files] DOMAIN_ID
ssl status
ssl list [--node NODE]
ssl get DOMAIN
ssl action [--confirm] [--node NODE] [--wait DURATION] NAME check|renew
ssl issue --confirm --domain DOMAIN --email EMAIL [--challenge http-01|dns-01] [--wait DURATION]
backups list [--node NODE]
backups create --confirm [--type full|database|files] [--name NAME] [--engine postgresql|mariadb] [--database NAME] [--compression 1-9] [--retention 0-365] [--vhost NAME]... [--wait DURATION]
backups run --confirm --node NODE [--wait DURATION] PLAN
backups jobs [--hours N] [--active]
backups job JOB_ID
backups validate BACKUP_ID
backups restore --confirm --validated [--wait DURATION] BACKUP_ID
backups delete --confirm [--wait DURATION] BACKUP_ID
backups schedule list
backups schedule set --confirm (--cron EXPR|--frequency daily|weekly|monthly --time HH:MM) [--type full|database|files|snapshot] [--database NAME] [--retention-count 1-365]
backups schedule delete --confirm --raw-line LINE
backups snapshot status
backups snapshot list
backups snapshot vhosts
backups snapshot run --confirm [--wait DURATION]
backups snapshot restore --confirm (--all|--manifest ID...|--vhost NAME...) [--wait DURATION] SNAPSHOT_ID
backups snapshot destination gdrive|s3
backups snapshot purge --confirm --repository REPO_FOLDER [--wait DURATION]
firewall status [--node NODE]
firewall list [--node NODE]
firewall add --confirm [--node NODE] --action allow|deny|reject|limit [--protocol tcp|udp|any] [--port PORT] [--source IP|CIDR] [--direction in|out] [--to IP|CIDR] [--comment TEXT]
firewall delete --confirm [--node NODE] [--wait DURATION] RULE
firewall toggle --confirm [--wait DURATION] enable|disable
cron status
cron list [--node NODE]
cron system
cron create --confirm [--node NODE] --schedule SCHEDULE [--user USER] --command COMMAND [--description TEXT] [--disabled] [--wait DURATION]
cron update --confirm [--node NODE] --schedule SCHEDULE --user USER --command COMMAND [--description TEXT] [--disabled] [--wait DURATION] JOB
cron delete --confirm [--node NODE] [--wait DURATION] JOB
cron run --confirm --node NODE [--wait DURATION] JOB
databases list [--node NODE] [--engine postgres|mariadb]
databases users [--engine postgres|mariadb]
databases tables --engine postgres|mariadb DATABASE
databases query --engine postgres|mariadb --query-file PATH [--wait DURATION] DATABASE
databases create --confirm --engine postgres|mariadb [--owner USER] [--wait DURATION] DATABASE
databases drop --confirm --engine postgres|mariadb [--wait DURATION] DATABASE
databases restart --confirm --node NODE [--wait DURATION] postgresql|mariadb
files roots [--node NODE]
files list [--node NODE] PATH
files read [--node NODE] PATH
files save --confirm [--node NODE --checksum SHA256] --content-file PATH [--wait DURATION] TARGET
files create --confirm [--type file|directory] TARGET
files rename --confirm SOURCE TARGET
files delete --confirm [--wait DURATION] TARGET
php versions [--node NODE]
php pools [--node NODE] [--version VERSION]
php pool get VERSION POOL
php config get [--node NODE] VERSION POOL
php config edit --confirm [--node NODE] [--editor EXECUTABLE] [--reload] [--wait DURATION] VERSION POOL
php config save --confirm [--node NODE] --checksum SHA256 --content-file PATH [--reload] [--wait DURATION] VERSION POOL
php action --confirm [--node NODE] [--wait DURATION] VERSION test|reload|restart
php status VERSION [POOL]
php opcache get VERSION
php opcache reset --confirm VERSION
php extensions VERSION
php ini get VERSION [POOL]
php ini diff VERSION
php ini directives VERSION
php logs error [--lines N] VERSION
php logs slow [--lines N] VERSION POOL
php security profiles
php security status VERSION POOL
php composer version
php composer outdated --project-dir PATH VERSION
security score
security fail2ban status
security fail2ban jail JAIL
security fail2ban ban --confirm [--wait DURATION] JAIL IP
security fail2ban unban --confirm [--wait DURATION] JAIL IP
security ip-blacklist list [--format json|text]
security ip-blacklist add --confirm [--ip IP|CIDR] [--comment TEXT] [--expires-in-minutes MINUTES] [--format json|text] [IP|CIDR]
security ip-blacklist delete --confirm [--ip IP|CIDR] [--format json|text] [IP|CIDR]
security ip-whitelist list [--format json|text]
security ip-whitelist add --confirm [--ip IP|CIDR] [--comment TEXT] [--expires-in-minutes MINUTES] [--format json|text] [IP|CIDR]
security ip-whitelist delete --confirm [--ip IP|CIDR] [--format json|text] [IP|CIDR]
audit list [--limit 1-200] [--offset N] [--server local|NODE] [--user TEXT] [--action TEXT] [--resource NAME] [--from RFC3339] [--to RFC3339]
users list [--limit 1-200] [--offset N]
users create --confirm --email EMAIL --name NAME [--role admin|manager|viewer] [--password-file PATH]
users update --confirm [--email EMAIL] [--name NAME] [--role admin|manager|viewer] [--password-file PATH] ID
users delete --confirm ID
notify channels
notify channel ID
notify create --confirm --name NAME --type email|telegram|discord|slack [OPTIONS]
notify update --confirm [OPTIONS] ID
notify test --confirm [--wait DURATION] ID
notify delete --confirm [--wait DURATION] ID
notify rules
notify rule ID
notify rule-create --confirm --name NAME --type TYPE --threshold VALUE [OPTIONS]
notify rule-update --confirm [OPTIONS] ID
notify rule-delete --confirm [--wait DURATION] ID
notify history [--limit 1-200] [--offset N]
cloudflare zones
cloudflare zone ZONE_ID
cloudflare records [--type TYPE] [--name NAME] ZONE_ID
cloudflare email-routing ZONE_ID
cloudflare record-create --confirm --type TYPE --name NAME --content VALUE [OPTIONS] ZONE_ID
cloudflare record-update --confirm [OPTIONS] ZONE_ID RECORD_ID
cloudflare record-proxy --confirm --proxied true|false [--wait DURATION] ZONE_ID RECORD_ID
cloudflare record-delete --confirm [--wait DURATION] ZONE_ID RECORD_ID
cloudflare purge --confirm [--wait DURATION] ZONE_ID
cloudflare mail-autofix --confirm [--wait DURATION] DOMAIN
mail service status
mail service overview
mail status
mail logs [--lines N]
mail logs normal [--lines N]
mail logs search --query QUERY
mail logs delivery --email EMAIL
mail queue list [--limit N]
mail queue retry --confirm ID
mail queue delete --confirm ID
mail domains list
mail domains create --confirm DOMAIN
mail domains delete --confirm DOMAIN
mail accounts [--domain DOMAIN]
mail aliases [--domain DOMAIN]
dns status
dns zones
dns zone DOMAIN
dns records DOMAIN
dns soa DOMAIN
dns lookup [--type TYPE] DOMAIN
dns check
dns export [--output FILE] DOMAIN
dns zone-create --confirm --ip IPV4 [--wait DURATION] DOMAIN
dns zone-delete --confirm [--wait DURATION] DOMAIN
dns record-add --confirm --type TYPE --value VALUE [--name NAME] [--ttl SECONDS] [--priority N] [--auto-reload=true|false] [--wait DURATION] DOMAIN
dns record-update --confirm --name NAME --type TYPE --old-value VALUE --new-value VALUE [--new-ttl SECONDS] [--priority N] [--auto-reload=true|false] [--wait DURATION] DOMAIN
dns record-delete --confirm --name NAME --type TYPE --value VALUE [--auto-reload=true|false] [--wait DURATION] DOMAIN
dns soa-update --confirm [--primary-ns NAME] [--hostmaster NAME] [--refresh N] [--retry N] [--expire N] [--minimum N] [--wait DURATION] DOMAIN
dns reload --confirm [--wait DURATION]
integrations list [--format json|text]
integrations show [--format json|text] ID
integrations status [--format json|text] [--node NODE_ID]
settings list
settings get KEY
settings set KEY=VALUE...
settings delete --confirm KEY
settings export --output FILE
settings preview --file FILE
settings import --file FILE --confirm
tasks list [--limit N] NODE
tasks get NODE TASK_ID
tasks create --confirm --kind KIND [--payload KEY=VALUE]... NODE
completion bash|zsh|fish`

func cliHelpEntries() []cliHelpEntry {
	lines := strings.Split(strings.TrimSpace(cliCommandUsage), "\n")
	entries := make([]cliHelpEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entries = append(entries, cliHelpEntry{usage: line, path: cliHelpPathTokens(line)})
	}
	return entries
}

func cliHelpPathTokens(usage string) []string {
	fields := strings.Fields(usage)
	path := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "[]()")
		if field == "" || strings.HasPrefix(field, "--") || strings.ContainsAny(field, "|[]()") {
			break
		}
		// Positional placeholders are upper-case by convention in the public
		// synopsis (NODE, DOMAIN, TASK_ID, and so on).
		if field == strings.ToUpper(field) {
			break
		}
		path = append(path, field)
	}
	return path
}

func writeUsage(out io.Writer) {
	fmt.Fprint(out, `Usage: hserverctl [global flags] COMMAND

Global flags (must precede COMMAND):
  --server URL       Heyserver base URL (HSERVER_URL; default http://127.0.0.1:3085)
  --token-file PATH  protected token file (HSERVER_TOKEN_FILE; default user config path)
  --context NAME     named server context (HSERVER_CONTEXT; default active context)
  --timeout DURATION request timeout (default 30s)

Commands:
`)
	for _, line := range strings.Split(strings.TrimSpace(cliCommandUsage), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fmt.Fprintf(out, "  %s\n", line)
	}
}

func writeCommandHelp(out io.Writer, candidate []string) error {
	if len(candidate) == 0 {
		writeUsage(out)
		return nil
	}
	entries := cliHelpEntries()
	path, ok := resolveCLIHelpPath(candidate, entries)
	if !ok || len(path) == 0 {
		return fmt.Errorf("unknown command path %q; run hserverctl help", strings.Join(candidate, " "))
	}

	matches := cliHelpMatches(entries, path)
	if len(matches) == 0 {
		return fmt.Errorf("unknown command path %q; run hserverctl help", strings.Join(path, " "))
	}

	pathText := strings.Join(path, " ")
	fmt.Fprintf(out, "Usage: hserverctl %s", pathText)
	if !cliHelpHasExactEntry(matches, path) || len(matches) > 1 {
		fmt.Fprint(out, " COMMAND")
	} else {
		suffix := strings.TrimSpace(matches[0].usage[len(pathText):])
		if suffix != "" {
			fmt.Fprint(out, " ")
			fmt.Fprint(out, suffix)
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Synopsis:")
	if len(matches) == 1 && cliHelpPathEqual(matches[0].path, path) {
		fmt.Fprintf(out, "  hserverctl %s\n", matches[0].usage)
	} else {
		fmt.Fprintf(out, "  hserverctl %s COMMAND\n", pathText)
	}

	children := cliHelpChildren(matches, path)
	if len(children) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Subcommands:")
		for _, child := range children {
			fmt.Fprintf(out, "  %s\n", child)
		}
	}

	if len(matches) > 1 || !cliHelpHasExactEntry(matches, path) {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Commands:")
		for _, entry := range matches {
			fmt.Fprintf(out, "  %s\n", entry.usage)
		}
	}

	if len(matches) == 1 && cliHelpPathEqual(matches[0].path, path) {
		writeCLIFlagHelp(out, matches[0].usage)
	}
	return nil
}

func resolveCLIHelpPath(candidate []string, entries []cliHelpEntry) ([]string, bool) {
	if len(candidate) == 0 {
		return nil, true
	}
	path := make([]string, 0, len(candidate))
	for _, token := range candidate {
		if strings.HasPrefix(token, "-") {
			break
		}
		next := append(append([]string(nil), path...), token)
		if len(cliHelpMatches(entries, next)) == 0 {
			// Once a leaf command is identified, remaining positional values
			// belong to that command rather than extending its command path.
			if cliHelpHasExactEntry(entries, path) {
				return path, true
			}
			return candidate, false
		}
		path = next
	}
	if len(path) == 0 || len(cliHelpMatches(entries, path)) == 0 {
		return candidate, false
	}
	return path, true
}

func cliHelpMatches(entries []cliHelpEntry, path []string) []cliHelpEntry {
	matches := make([]cliHelpEntry, 0)
	for _, entry := range entries {
		if len(entry.path) < len(path) || !cliHelpPathEqual(entry.path[:len(path)], path) {
			continue
		}
		matches = append(matches, entry)
	}
	return matches
}

func cliHelpHasExactEntry(entries []cliHelpEntry, path []string) bool {
	for _, entry := range entries {
		if cliHelpPathEqual(entry.path, path) {
			return true
		}
	}
	return false
}

func cliHelpPathEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cliHelpChildren(entries []cliHelpEntry, path []string) []string {
	children := make([]string, 0)
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if len(entry.path) <= len(path) || !cliHelpPathEqual(entry.path[:len(path)], path) {
			continue
		}
		child := entry.path[len(path)]
		if _, exists := seen[child]; exists {
			continue
		}
		seen[child] = struct{}{}
		children = append(children, child)
	}
	return children
}

func writeCLIFlagHelp(out io.Writer, usage string) {
	flags := cliHelpFlagNames(usage)
	if len(flags) == 0 {
		return
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Flags:")
	for _, flag := range flags {
		fmt.Fprintf(out, "  %-28s %s\n", flag, cliHelpFlagDescription(flag))
	}
	for _, flag := range flags {
		if cliHelpFlagBase(flag) == "--confirm" {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Safety flags:")
			fmt.Fprintln(out, "  --confirm          required explicit confirmation for this mutation")
			break
		}
	}
}

func cliHelpFlagNames(usage string) []string {
	seen := make(map[string]struct{})
	flags := make([]string, 0)
	fields := strings.Fields(usage)
	for index, field := range fields {
		field = strings.Trim(field, "[](),")
		if !strings.HasPrefix(field, "--") {
			continue
		}
		options := []string{field}
		if !strings.Contains(field, "=") {
			options = strings.Split(field, "|")
		}
		for optionIndex, option := range options {
			if !strings.HasPrefix(option, "--") {
				continue
			}
			option = strings.TrimRight(option, ",")
			base := cliHelpFlagBase(option)
			if _, exists := seen[base]; exists {
				continue
			}
			seen[base] = struct{}{}
			display := option
			if !strings.Contains(option, "=") && optionIndex == len(options)-1 && index+1 < len(fields) {
				value := strings.Trim(fields[index+1], "[](),")
				if value != "" && !strings.HasPrefix(value, "--") {
					display += " " + value
				}
			}
			flags = append(flags, display)
		}
	}
	return flags
}

func cliHelpFlagBase(name string) string {
	if index := strings.IndexAny(name, " ="); index >= 0 {
		return name[:index]
	}
	return name
}

func cliHelpFlagDescription(name string) string {
	switch strings.TrimPrefix(cliHelpFlagBase(name), "--") {
	case "confirm":
		return "explicit safety confirmation"
	case "wait":
		return "maximum bounded request/action wait"
	case "node":
		return "managed node ID; omit for local host where supported"
	case "pid":
		return "stable process ID (must be greater than 1)"
	case "start-time":
		return "stable process start-time identity (positive integer)"
	case "signal":
		return "process signal: term or kill"
	case "server":
		return "Heyserver base URL"
	case "token-file":
		return "protected bearer-token file"
	case "context":
		return "named Heyserver context"
	case "timeout":
		return "request timeout"
	case "format":
		return "command output format (json, table, text, or env-fragment where supported)"
	case "profile-file":
		return "strict fixed managed-node profile JSON file"
	case "range":
		return "metrics history range"
	case "at":
		return "RFC3339 timestamp for the nearest process snapshot"
	case "tail":
		return "number of latest container log lines (1-1000)"
	default:
		return "command option"
	}
}

type cliCompletionFlag struct {
	name  string
	value string
}

type cliCompletionCatalog struct {
	root     []string
	known    []string
	children map[string][]string
	flags    map[string][]cliCompletionFlag
	values   map[string][]string
}

var cliCompletionBooleanFlags = map[string]bool{
	"--active":              true,
	"--all":                 true,
	"--confirm":             true,
	"--create-dns-record":   true,
	"--disabled":            true,
	"--isolated-linux-user": true,
	"--issue-ssl":           true,
	"--reload":              true,
	"--replace":             true,
	"--spa":                 true,
	"--use":                 true,
	"--validated":           true,
	"--www-redirect":        true,
}

func cliCompletionCatalogFromHelp() cliCompletionCatalog {
	catalog := cliCompletionCatalog{
		known:    []string{""},
		children: make(map[string][]string),
		flags:    make(map[string][]cliCompletionFlag),
		values:   make(map[string][]string),
	}

	for _, entry := range cliHelpEntries() {
		if len(entry.path) == 0 {
			continue
		}
		for index, token := range entry.path {
			parent := strings.Join(entry.path[:index], " ")
			children := catalog.children[parent]
			appendCLICompletionString(&children, token)
			catalog.children[parent] = children
			path := strings.Join(entry.path[:index+1], " ")
			appendCLICompletionString(&catalog.known, path)
		}
		path := strings.Join(entry.path, " ")
		for _, flag := range cliCompletionFlags(entry.usage) {
			flags := catalog.flags[path]
			appendCLICompletionFlag(&flags, flag)
			catalog.flags[path] = flags
		}
	}

	catalog.root = append([]string(nil), catalog.children[""]...)
	appendCLICompletionString(&catalog.root, "help")
	catalog.children[""] = append([]string(nil), catalog.root...)
	appendCLICompletionString(&catalog.known, "help")
	catalog.children["help"] = append([]string(nil), catalog.root...)

	// These are positional values that the existing completion handled as
	// dynamic nested cases rather than as command paths in the help synopsis.
	catalog.values["completion"] = []string{"bash", "zsh", "fish"}
	catalog.values["backups snapshot destination"] = []string{"gdrive", "s3"}
	catalog.values["integrations show"] = strings.Fields(integrationCompletionIDs)
	catalog.values["settings get"] = append([]string(nil), cliEditableSettingKeys...)
	catalog.values["settings delete"] = append([]string(nil), cliEditableSettingKeys...)
	return catalog
}

func appendCLICompletionString(values *[]string, value string) {
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}

func appendCLICompletionFlag(values *[]cliCompletionFlag, value cliCompletionFlag) {
	for index := range *values {
		if (*values)[index].name != value.name {
			continue
		}
		if (*values)[index].value == "" {
			(*values)[index].value = value.value
		}
		return
	}
	*values = append(*values, value)
}

func cliCompletionFlags(usage string) []cliCompletionFlag {
	flags := make([]cliCompletionFlag, 0)
	pending := -1
	for _, rawToken := range strings.Fields(usage) {
		token := strings.Trim(rawToken, "[](),")
		if token == "" || token == "OPTIONS" {
			continue
		}

		// Preserve an inline enum such as --auto-reload=true|false as one
		// value hint before splitting grouped alternatives on '|'.
		if strings.HasPrefix(token, "--") && strings.Contains(token, "=") {
			name, hint, ok := cliCompletionFlagToken(token)
			if ok {
				appendCLICompletionFlag(&flags, cliCompletionFlag{name: name, value: hint})
				pending = -1
				continue
			}
		}

		if pending >= 0 && !strings.Contains(token, "--") {
			flags[pending].value = cliCompletionHint(token)
			pending = -1
			continue
		}
		for _, rawPiece := range strings.Split(token, "|") {
			piece := cliCompletionHint(rawPiece)
			if piece == "" {
				continue
			}
			if strings.HasPrefix(piece, "--") {
				// A following flag terminates a pending value. This is
				// important for grouped forms such as --all|--manifest ID.
				pending = -1
				name, hint, ok := cliCompletionFlagToken(piece)
				if !ok {
					continue
				}
				appendCLICompletionFlag(&flags, cliCompletionFlag{name: name, value: hint})
				for index := range flags {
					if flags[index].name != name {
						continue
					}
					if hint != "" {
						flags[index].value = hint
					}
					if hint == "" && !cliCompletionBooleanFlags[name] {
						pending = index
					}
					break
				}
				continue
			}
			if pending >= 0 {
				flags[pending].value = piece
				pending = -1
			}
		}
	}
	return flags
}

func cliCompletionFlagToken(token string) (string, string, bool) {
	token = cliCompletionHint(token)
	if !strings.HasPrefix(token, "--") {
		return "", "", false
	}
	name := token
	hint := ""
	if index := strings.IndexByte(token, '='); index >= 0 {
		name = token[:index]
		hint = cliCompletionHint(token[index+1:])
	}
	if name == "--" {
		return "", "", false
	}
	return name, hint, true
}

func cliCompletionHint(value string) string {
	value = strings.Trim(value, "[](),")
	for strings.HasSuffix(value, "...") {
		value = strings.TrimSuffix(value, "...")
	}
	return strings.Trim(value, "[](),")
}

func cliCompletionEnumValues(hint string) []string {
	parts := strings.Split(hint, "|")
	if len(parts) < 2 {
		return nil
	}
	for index := range parts {
		parts[index] = cliCompletionHint(parts[index])
		if parts[index] == "" || strings.ToLower(parts[index]) != parts[index] {
			return nil
		}
	}
	return parts
}

func cliCompletionGlobalFlags() []cliCompletionFlag {
	return []cliCompletionFlag{
		{name: "--server", value: "URL"},
		{name: "--token-file", value: "PATH"},
		{name: "--context", value: "NAME"},
		{name: "--timeout", value: "DURATION"},
	}
}

func completionShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func completionCasePatterns(values []string) string {
	patterns := make([]string, 0, len(values))
	for _, value := range values {
		patterns = append(patterns, completionShellQuote(value))
	}
	return strings.Join(patterns, "|")
}

func writeBashCompletion(out io.Writer, catalog cliCompletionCatalog) {
	fmt.Fprintln(out, `# bash completion for hserverctl`)
	fmt.Fprintln(out, `# Command paths and flags are generated from cliCommandUsage.`)
	writeCompletionSpecialSummary(out, catalog)
	fmt.Fprintln(out, `_hserverctl_known_path() {`)
	fmt.Fprintln(out, `    case "$1" in`)
	fmt.Fprintf(out, "    %s) return 0 ;;\n", completionCasePatterns(catalog.known))
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `    return 1`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_children() {`)
	fmt.Fprintln(out, `    case "$1" in`)
	for _, path := range catalog.known {
		children := catalog.children[path]
		if len(children) == 0 {
			continue
		}
		fmt.Fprintf(out, "    %s)\n", completionShellQuote(path))
		writeBashValueList(out, "        ", children)
		fmt.Fprintln(out, "        ;;")
	}
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_flags() {`)
	fmt.Fprintln(out, `    case "$1" in`)
	fmt.Fprintln(out, `    '')`)
	writeBashFlagList(out, "        ", cliCompletionGlobalFlags())
	fmt.Fprintln(out, "        ;;")
	for _, path := range catalog.known {
		flags := catalog.flags[path]
		if len(flags) == 0 {
			continue
		}
		fmt.Fprintf(out, "    %s)\n", completionShellQuote(path))
		writeBashFlagList(out, "        ", flags)
		fmt.Fprintln(out, "        ;;")
	}
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_values() {`)
	fmt.Fprintln(out, `    case "$1" in`)
	for _, path := range catalog.known {
		values := catalog.values[path]
		if len(values) == 0 {
			continue
		}
		fmt.Fprintf(out, "    %s)\n", completionShellQuote(path))
		writeBashValueList(out, "        ", values)
		fmt.Fprintln(out, "        ;;")
	}
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_flag_values() {`)
	fmt.Fprintln(out, `    case "$1::$2" in`)
	for _, path := range catalog.known {
		for _, flag := range catalog.flags[path] {
			values := cliCompletionEnumValues(flag.value)
			if len(values) == 0 {
				continue
			}
			fmt.Fprintf(out, "    %s)\n", completionShellQuote(path+"::"+flag.name))
			writeBashValueList(out, "        ", values)
			fmt.Fprintln(out, "        ;;")
		}
	}
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_completion_path() {`)
	fmt.Fprintln(out, `    local path="" token candidate skip=0 index`)
	fmt.Fprintln(out, `    for (( index=1; index<COMP_CWORD; index++ )); do`)
	fmt.Fprintln(out, `        token="${COMP_WORDS[index]}"`)
	fmt.Fprintln(out, `        if (( skip )); then`)
	fmt.Fprintln(out, `            skip=0`)
	fmt.Fprintln(out, `            continue`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `        if [[ -z "$path" ]]; then`)
	fmt.Fprintln(out, `            case "$token" in`)
	fmt.Fprintln(out, `            --server|--token-file|--context|--timeout)`)
	fmt.Fprintln(out, `                skip=1`)
	fmt.Fprintln(out, `                continue`)
	fmt.Fprintln(out, `                ;;`)
	fmt.Fprintln(out, `            --server=*|--token-file=*|--context=*|--timeout=*)`)
	fmt.Fprintln(out, `                continue`)
	fmt.Fprintln(out, `                ;;`)
	fmt.Fprintln(out, `            --*)`)
	fmt.Fprintln(out, `                continue`)
	fmt.Fprintln(out, `                ;;`)
	fmt.Fprintln(out, `            esac`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `        if [[ "$token" == --* ]]; then`)
	fmt.Fprintln(out, `            continue`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `        candidate="$token"`)
	fmt.Fprintln(out, `        if [[ -n "$path" ]]; then`)
	fmt.Fprintln(out, `            candidate="$path $token"`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `        if _hserverctl_known_path "$candidate"; then`)
	fmt.Fprintln(out, `            path="$candidate"`)
	fmt.Fprintln(out, `        else`)
	fmt.Fprintln(out, `            break`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `    done`)
	fmt.Fprintln(out, `    _hserverctl_path_result="$path"`)
	fmt.Fprintln(out, `    _hserverctl_path_pending_global="$skip"`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_completions() {`)
	fmt.Fprintln(out, `    local cur path children values flags candidates previous`)
	fmt.Fprintln(out, `    cur="${COMP_WORDS[COMP_CWORD]}"`)
	fmt.Fprintln(out, `    _hserverctl_completion_path`)
	fmt.Fprintln(out, `    if (( _hserverctl_path_pending_global )); then`)
	fmt.Fprintln(out, `        COMPREPLY=()`)
	fmt.Fprintln(out, `        return`)
	fmt.Fprintln(out, `    fi`)
	fmt.Fprintln(out, `    path="$_hserverctl_path_result"`)
	fmt.Fprintln(out, `    children="$(_hserverctl_children "$path")"`)
	fmt.Fprintln(out, `    values="$(_hserverctl_values "$path")"`)
	fmt.Fprintln(out, `    flags="$(_hserverctl_flags "$path")"`)
	fmt.Fprintln(out, `    candidates=""`)
	fmt.Fprintln(out, `    if [[ "$cur" == -* ]]; then`)
	fmt.Fprintln(out, `        candidates="$flags"`)
	fmt.Fprintln(out, `    elif [[ -n "$children" ]]; then`)
	fmt.Fprintln(out, `        candidates="$children"`)
	fmt.Fprintln(out, `    elif [[ -n "$values" ]]; then`)
	fmt.Fprintln(out, `        candidates="$values"`)
	fmt.Fprintln(out, `    elif (( COMP_CWORD > 1 )); then`)
	fmt.Fprintln(out, `        previous="${COMP_WORDS[COMP_CWORD-1]}"`)
	fmt.Fprintln(out, `        if [[ "$previous" == --* ]]; then`)
	fmt.Fprintln(out, `            candidates="$(_hserverctl_flag_values "$path" "$previous")"`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `        if [[ -z "$candidates" && -n "$path" && "$cur" == "" && -z "$children" && -z "$values" ]]; then`)
	fmt.Fprintln(out, `            candidates="$flags"`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `    elif [[ -n "$path" && "$cur" == "" && -z "$children" && -z "$values" ]]; then`)
	fmt.Fprintln(out, `        candidates="$flags"`)
	fmt.Fprintln(out, `    fi`)
	fmt.Fprintln(out, `    if [[ -z "$path" && "$cur" != -* ]]; then`)
	fmt.Fprintln(out, `        candidates="$children $flags"`)
	fmt.Fprintln(out, `    fi`)
	fmt.Fprintln(out, `    if [[ -n "$candidates" ]]; then`)
	fmt.Fprintln(out, `        COMPREPLY=( $(compgen -W "$candidates" -- "$cur") )`)
	fmt.Fprintln(out, `    else`)
	fmt.Fprintln(out, `        COMPREPLY=()`)
	fmt.Fprintln(out, `    fi`)
	fmt.Fprintln(out, `}`)
	fmt.Fprintln(out, `complete -F _hserverctl_completions hserverctl`)
}

func writeBashValueList(out io.Writer, indent string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(out, "%sprintf '%%s\\n'", indent)
	for _, value := range values {
		fmt.Fprintf(out, " %s", completionShellQuote(value))
	}
	fmt.Fprintln(out)
}

func writeBashFlagList(out io.Writer, indent string, flags []cliCompletionFlag) {
	values := make([]string, 0, len(flags))
	for _, flag := range flags {
		values = append(values, flag.name)
	}
	writeBashValueList(out, indent, values)
}

func writeZshCompletion(out io.Writer, catalog cliCompletionCatalog) {
	fmt.Fprintln(out, `#compdef hserverctl`)
	fmt.Fprintln(out, `# Command paths and flags are generated from cliCommandUsage.`)
	writeCompletionSpecialSummary(out, catalog)
	fmt.Fprintln(out, `_hserverctl_known_path() {`)
	fmt.Fprintln(out, `    case "$1" in`)
	fmt.Fprintf(out, "    %s) return 0 ;;\n", completionCasePatterns(catalog.known))
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `    return 1`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_children() {`)
	fmt.Fprintln(out, `    case "$1" in`)
	for _, path := range catalog.known {
		children := catalog.children[path]
		if len(children) == 0 {
			continue
		}
		fmt.Fprintf(out, "    %s)\n", completionShellQuote(path))
		writeZshValueList(out, "        ", children)
		fmt.Fprintln(out, "        ;;")
	}
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_flags() {`)
	fmt.Fprintln(out, `    case "$1" in`)
	fmt.Fprintln(out, `    '')`)
	writeZshValueList(out, "        ", cliCompletionFlagNames(cliCompletionGlobalFlags()))
	fmt.Fprintln(out, "        ;;")
	for _, path := range catalog.known {
		flags := catalog.flags[path]
		if len(flags) == 0 {
			continue
		}
		fmt.Fprintf(out, "    %s)\n", completionShellQuote(path))
		writeZshValueList(out, "        ", cliCompletionFlagNames(flags))
		fmt.Fprintln(out, "        ;;")
	}
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_values() {`)
	fmt.Fprintln(out, `    case "$1" in`)
	for _, path := range catalog.known {
		values := catalog.values[path]
		if len(values) == 0 {
			continue
		}
		fmt.Fprintf(out, "    %s)\n", completionShellQuote(path))
		writeZshValueList(out, "        ", values)
		fmt.Fprintln(out, "        ;;")
	}
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_flag_values() {`)
	fmt.Fprintln(out, `    case "$1::$2" in`)
	for _, path := range catalog.known {
		for _, flag := range catalog.flags[path] {
			values := cliCompletionEnumValues(flag.value)
			if len(values) == 0 {
				continue
			}
			fmt.Fprintf(out, "    %s)\n", completionShellQuote(path+"::"+flag.name))
			writeZshValueList(out, "        ", values)
			fmt.Fprintln(out, "        ;;")
		}
	}
	fmt.Fprintln(out, `    esac`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl_completion_path() {`)
	fmt.Fprintln(out, `    local path="" token candidate skip=0 index`)
	fmt.Fprintln(out, `    for (( index=2; index<CURRENT; index++ )); do`)
	fmt.Fprintln(out, `        token="${words[index]}"`)
	fmt.Fprintln(out, `        if (( skip )); then`)
	fmt.Fprintln(out, `            skip=0`)
	fmt.Fprintln(out, `            continue`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `        if [[ -z "$path" ]]; then`)
	fmt.Fprintln(out, `            case "$token" in`)
	fmt.Fprintln(out, `            --server|--token-file|--context|--timeout)`)
	fmt.Fprintln(out, `                skip=1`)
	fmt.Fprintln(out, `                continue`)
	fmt.Fprintln(out, `                ;;`)
	fmt.Fprintln(out, `            --server=*|--token-file=*|--context=*|--timeout=*)`)
	fmt.Fprintln(out, `                continue`)
	fmt.Fprintln(out, `                ;;`)
	fmt.Fprintln(out, `            --*)`)
	fmt.Fprintln(out, `                continue`)
	fmt.Fprintln(out, `                ;;`)
	fmt.Fprintln(out, `            esac`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `        if [[ "$token" == --* ]]; then`)
	fmt.Fprintln(out, `            continue`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `        candidate="$token"`)
	fmt.Fprintln(out, `        if [[ -n "$path" ]]; then`)
	fmt.Fprintln(out, `            candidate="$path $token"`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `        if _hserverctl_known_path "$candidate"; then`)
	fmt.Fprintln(out, `            path="$candidate"`)
	fmt.Fprintln(out, `        else`)
	fmt.Fprintln(out, `            break`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `    done`)
	fmt.Fprintln(out, `    _hserverctl_path_result="$path"`)
	fmt.Fprintln(out, `    _hserverctl_path_pending_global="$skip"`)
	fmt.Fprintln(out, `}`)

	fmt.Fprintln(out, `_hserverctl() {`)
	fmt.Fprintln(out, `    local cur path previous`)
	fmt.Fprintln(out, `    local -a children values flags candidates`)
	fmt.Fprintln(out, `    cur="${words[CURRENT]}"`)
	fmt.Fprintln(out, `    _hserverctl_completion_path`)
	fmt.Fprintln(out, `    if (( _hserverctl_path_pending_global )); then`)
	fmt.Fprintln(out, `        return`)
	fmt.Fprintln(out, `    fi`)
	fmt.Fprintln(out, `    path="$_hserverctl_path_result"`)
	fmt.Fprintln(out, `    children=("${(@f)$(_hserverctl_children "$path")}")`)
	fmt.Fprintln(out, `    values=("${(@f)$(_hserverctl_values "$path")}")`)
	fmt.Fprintln(out, `    flags=("${(@f)$(_hserverctl_flags "$path")}")`)
	fmt.Fprintln(out, `    candidates=()`)
	fmt.Fprintln(out, `    if [[ "$cur" == -* ]]; then`)
	fmt.Fprintln(out, `        candidates=("${flags[@]}")`)
	fmt.Fprintln(out, `    elif (( ${#children[@]} > 0 )); then`)
	fmt.Fprintln(out, `        candidates=("${children[@]}")`)
	fmt.Fprintln(out, `    elif (( ${#values[@]} > 0 )); then`)
	fmt.Fprintln(out, `        candidates=("${values[@]}")`)
	fmt.Fprintln(out, `    elif (( CURRENT > 2 )) && [[ "${words[CURRENT-1]}" == --* ]]; then`)
	fmt.Fprintln(out, `        previous="${words[CURRENT-1]}"`)
	fmt.Fprintln(out, `        candidates=("${(@f)$(_hserverctl_flag_values "$path" "$previous")}")`)
	fmt.Fprintln(out, `        if (( ${#candidates[@]} == 0 )) && [[ -n "$path" && "$cur" == "" && ${#children[@]} -eq 0 && ${#values[@]} -eq 0 ]]; then`)
	fmt.Fprintln(out, `            candidates=("${flags[@]}")`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `    elif (( CURRENT > 2 )); then`)
	fmt.Fprintln(out, `        if [[ -n "$path" && "$cur" == "" && ${#children[@]} -eq 0 && ${#values[@]} -eq 0 ]]; then`)
	fmt.Fprintln(out, `            candidates=("${flags[@]}")`)
	fmt.Fprintln(out, `        fi`)
	fmt.Fprintln(out, `    fi`)
	fmt.Fprintln(out, `    if [[ -z "$path" && "$cur" != -* ]]; then`)
	fmt.Fprintln(out, `        candidates=("${children[@]}" "${flags[@]}")`)
	fmt.Fprintln(out, `    fi`)
	fmt.Fprintln(out, `    if (( ${#candidates[@]} > 0 )); then`)
	fmt.Fprintln(out, `        compadd -Q -- "${candidates[@]}"`)
	fmt.Fprintln(out, `    fi`)
	fmt.Fprintln(out, `}`)
	fmt.Fprintln(out, `compdef _hserverctl hserverctl`)
}

func writeZshValueList(out io.Writer, indent string, values []string) {
	for _, value := range values {
		fmt.Fprintf(out, "%sprint -r -- %s\n", indent, completionShellQuote(value))
	}
}

func cliCompletionFlagNames(flags []cliCompletionFlag) []string {
	values := make([]string, 0, len(flags))
	for _, flag := range flags {
		values = append(values, flag.name)
	}
	return values
}

func writeFishCompletion(out io.Writer, catalog cliCompletionCatalog) {
	fmt.Fprintln(out, `# fish completion for hserverctl`)
	fmt.Fprintln(out, `# Command paths and flags are generated from cliCommandUsage.`)
	writeCompletionSpecialSummary(out, catalog)
	fmt.Fprintln(out, `function __hserverctl_known_path`)
	fmt.Fprintln(out, `    switch "$argv[1]"`)
	fmt.Fprintf(out, "    case %s\n", fishCasePatterns(catalog.known))
	fmt.Fprintln(out, `        return 0`)
	fmt.Fprintln(out, `    end`)
	fmt.Fprintln(out, `    return 1`)
	fmt.Fprintln(out, `end`)

	fmt.Fprintln(out, `function __hserverctl_command_path`)
	fmt.Fprintln(out, `    set -l tokens (commandline -opc)`)
	fmt.Fprintln(out, `    if test (count $tokens) -gt 0; and test "$tokens[1]" = hserverctl`)
	fmt.Fprintln(out, `        set -e tokens[1]`)
	fmt.Fprintln(out, `    end`)
	fmt.Fprintln(out, `    set -l path ''`)
	fmt.Fprintln(out, `    set -l skip 0`)
	fmt.Fprintln(out, `    for token in $tokens`)
	fmt.Fprintln(out, `        if test $skip -eq 1`)
	fmt.Fprintln(out, `            set skip 0`)
	fmt.Fprintln(out, `            continue`)
	fmt.Fprintln(out, `        end`)
	fmt.Fprintln(out, `        if test -z "$path"`)
	fmt.Fprintln(out, `            switch "$token"`)
	fmt.Fprintln(out, `            case --server --token-file --context --timeout`)
	fmt.Fprintln(out, `                set skip 1`)
	fmt.Fprintln(out, `                continue`)
	fmt.Fprintln(out, `            case '--server=*' '--token-file=*' '--context=*' '--timeout=*'`)
	fmt.Fprintln(out, `                continue`)
	fmt.Fprintln(out, `            case '--*'`)
	fmt.Fprintln(out, `                continue`)
	fmt.Fprintln(out, `            end`)
	fmt.Fprintln(out, `        end`)
	fmt.Fprintln(out, `        if string match -q -- '--*' "$token"`)
	fmt.Fprintln(out, `            continue`)
	fmt.Fprintln(out, `        end`)
	fmt.Fprintln(out, `        set -l candidate "$token"`)
	fmt.Fprintln(out, `        if test -n "$path"`)
	fmt.Fprintln(out, `            set candidate "$path $token"`)
	fmt.Fprintln(out, `        end`)
	fmt.Fprintln(out, `        if __hserverctl_known_path "$candidate"`)
	fmt.Fprintln(out, `            set path "$candidate"`)
	fmt.Fprintln(out, `        else`)
	fmt.Fprintln(out, `            break`)
	fmt.Fprintln(out, `        end`)
	fmt.Fprintln(out, `    end`)
	fmt.Fprintf(out, "    printf '%%s\\n' \"$path\"\n")
	fmt.Fprintln(out, `end`)

	fmt.Fprintln(out, `function __hserverctl_at_path`)
	fmt.Fprintln(out, `    test "(__hserverctl_command_path)" = "$argv[1]"`)
	fmt.Fprintln(out, `end`)

	rootCondition := fishCompletionCondition("")
	fmt.Fprintf(out, "complete -c hserverctl -f -n %s -a %s\n", rootCondition, completionShellQuote(strings.Join(catalog.root, " ")))
	for _, path := range catalog.known {
		children := catalog.children[path]
		if len(children) == 0 || path == "" {
			continue
		}
		fmt.Fprintf(out, "complete -c hserverctl -f -n %s -a %s\n", fishCompletionCondition(path), completionShellQuote(strings.Join(children, " ")))
	}
	for _, flag := range cliCompletionGlobalFlags() {
		fmt.Fprintf(out, "complete -c hserverctl -l %s -r -d %s -n %s\n", strings.TrimPrefix(flag.name, "--"), completionShellQuote(cliHelpFlagDescription(flag.name)), rootCondition)
	}
	for _, path := range catalog.known {
		for _, flag := range catalog.flags[path] {
			line := fmt.Sprintf("complete -c hserverctl -l %s", strings.TrimPrefix(flag.name, "--"))
			if flag.value != "" {
				line += " -r"
				if values := cliCompletionEnumValues(flag.value); len(values) > 0 {
					line += " -a " + completionShellQuote(strings.Join(values, " "))
				}
			}
			line += " -d " + completionShellQuote(cliHelpFlagDescription(flag.name))
			line += " -n " + fishCompletionCondition(path)
			fmt.Fprintln(out, line)
		}
	}
	for _, path := range catalog.known {
		values := catalog.values[path]
		if len(values) == 0 {
			continue
		}
		fmt.Fprintf(out, "complete -c hserverctl -f -n %s -a %s\n", fishCompletionCondition(path), completionShellQuote(strings.Join(values, " ")))
	}
}

func fishCompletionCondition(path string) string {
	return `"__hserverctl_at_path ` + completionShellQuote(path) + `"`
}

func fishCasePatterns(values []string) string {
	patterns := make([]string, 0, len(values))
	for _, value := range values {
		patterns = append(patterns, completionShellQuote(value))
	}
	return strings.Join(patterns, " ")
}

func writeCompletionSpecialSummary(out io.Writer, catalog cliCompletionCatalog) {
	fmt.Fprintf(out, "# Nested command paths: updates agent (%s); deploy domain (%s); backups snapshot (%s); integrations (%s).\n",
		strings.Join(catalog.children["updates agent"], " "),
		strings.Join(catalog.children["deploy domain"], " "),
		strings.Join(catalog.children["backups snapshot"], " "),
		strings.Join(catalog.children["integrations"], " "))
}

func runCompletion(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: hserverctl completion bash|zsh|fish")
	}
	catalog := cliCompletionCatalogFromHelp()
	switch args[0] {
	case "bash":
		writeBashCompletion(out, catalog)
	case "zsh":
		writeZshCompletion(out, catalog)
	case "fish":
		writeFishCompletion(out, catalog)
	default:
		return fmt.Errorf("unsupported shell %q; use bash, zsh, or fish", args[0])
	}
	return nil
}
