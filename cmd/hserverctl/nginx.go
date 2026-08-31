package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const maxNginxConfigBytes = 2 << 20

var (
	nginxConfigNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)
	nginxPHPVersionPattern = regexp.MustCompile(`^[0-9]{1,2}\.[0-9]{1,2}$`)
	nginxPHPPoolPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	sha256Pattern          = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func runNginx(ctx context.Context, client *apiClient, args []string, out, errOut io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl nginx status|configs|archives|backups|snippets|get|create|edit|enable|disable|archive|restore|rollback|test|reload|save")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: hserverctl nginx status")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/nginx/status", nil, true)
	case "configs":
		node, err := parseOptionalNode("nginx configs", args[1:])
		if err != nil {
			return err
		}
		endpoint := "/api/nginx/configs"
		if node != "" {
			endpoint = "/api/nodes/" + url.PathEscape(node) + "/nginx/configs"
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "archives":
		if len(args) != 1 {
			return errors.New("usage: hserverctl nginx archives")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/nginx/archives", nil, true)
	case "backups":
		if len(args) != 1 {
			return errors.New("usage: hserverctl nginx backups")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/nginx/backups", nil, true)
	case "snippets":
		if len(args) != 1 {
			return errors.New("usage: hserverctl nginx snippets")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/nginx/snippets", nil, true)
	case "get":
		flags := flag.NewFlagSet("nginx get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 1 {
			return errors.New("usage: hserverctl nginx get [--node NODE] CONFIG")
		}
		name, err := validateNginxConfigName(flags.Args()[0])
		if err != nil {
			return err
		}
		endpoint := "/api/nginx/configs/" + url.PathEscape(name)
		if strings.TrimSpace(*node) != "" {
			endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/nginx/configs/" + url.PathEscape(name)
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "edit":
		return runNginxEdit(ctx, client, args[1:], out, errOut, getenv)
	case "create":
		return runNginxCreate(ctx, client, args[1:], out)
	case "enable", "disable":
		return runNginxState(ctx, client, args[1:], out, args[0] == "enable")
	case "archive":
		return runNginxArchive(ctx, client, args[1:], out)
	case "restore":
		return runNginxArchiveRestore(ctx, client, args[1:], out)
	case "rollback":
		return runNginxBackupRollback(ctx, client, args[1:], out)
	case "test":
		return runNginxAction(ctx, client, args[1:], out, "test", false)
	case "reload":
		return runNginxAction(ctx, client, args[1:], out, "reload", true)
	case "save":
		return runNginxSave(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown nginx command %q", args[0])
	}
}

type cliNginxBackup struct {
	Backup   string `json:"backup"`
	Filename string `json:"filename"`
	Checksum string `json:"checksum"`
}

func runNginxBackupRollback(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("nginx rollback", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	backupChecksumFlag := flags.String("backup-checksum", "", "exact observed backup SHA-256; fetched automatically when omitted")
	currentChecksumFlag := flags.String("current-checksum", "", "exact observed current config SHA-256; fetched automatically when omitted")
	confirmed := flags.Bool("confirm", false, "confirm checksum-locked Nginx config rollback")
	wait := flags.Duration("wait", 2*time.Minute, "maximum rollback and validation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl nginx rollback --confirm [--backup-checksum SHA256] [--current-checksum SHA256] [--wait DURATION] BACKUP")
	}
	if !*confirmed {
		return errors.New("Nginx config rollback requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("Nginx rollback wait must be greater than zero")
	}
	backup, filename, err := validateNginxBackupName(flags.Args()[0])
	if err != nil {
		return err
	}
	backupChecksum := strings.TrimSpace(*backupChecksumFlag)
	if backupChecksum == "" {
		backups, err := requestJSON[[]cliNginxBackup](ctx, client.withTimeout(45*time.Second), http.MethodGet, "/api/nginx/backups", nil, true)
		if err != nil {
			return err
		}
		for _, observed := range backups {
			if observed.Backup == backup {
				if observed.Filename != "" && observed.Filename != filename {
					return errors.New("server resolved the Nginx backup to a different config identity; refresh inventory and retry")
				}
				backupChecksum = strings.TrimSpace(observed.Checksum)
				break
			}
		}
		if backupChecksum == "" {
			return fmt.Errorf("Nginx configuration backup %q was not found in the observed inventory", backup)
		}
	}
	if !sha256Pattern.MatchString(backupChecksum) {
		return errors.New("Nginx config rollback requires a lowercase backup SHA-256 checksum")
	}
	currentChecksum := strings.TrimSpace(*currentChecksumFlag)
	if currentChecksum == "" {
		current, err := requestJSON[cliNginxConfig](ctx, client.withTimeout(45*time.Second), http.MethodGet, "/api/nginx/configs/"+url.PathEscape(filename), nil, true)
		if err != nil {
			return err
		}
		if (current.Filename != "" && current.Filename != filename) || (current.Name != "" && current.Name != filename) {
			return errors.New("server resolved the current Nginx configuration to a different identity; refresh inventory and retry")
		}
		currentChecksum = strings.TrimSpace(current.Checksum)
	}
	if !sha256Pattern.MatchString(currentChecksum) {
		return errors.New("Nginx config rollback requires a lowercase current config SHA-256 checksum")
	}
	endpoint := "/api/nginx/backups/" + url.PathEscape(backup) + "/restore"
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, map[string]any{
		"backupChecksum":  backupChecksum,
		"currentChecksum": currentChecksum,
	}, true)
}

type cliNginxArchive struct {
	Archive  string `json:"archive"`
	Checksum string `json:"checksum"`
}

func runNginxArchiveRestore(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("nginx restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	checksumFlag := flags.String("checksum", "", "exact observed archive SHA-256; fetched automatically when omitted")
	confirmed := flags.Bool("confirm", false, "confirm disabled Nginx config recovery")
	wait := flags.Duration("wait", 2*time.Minute, "maximum restore and validation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl nginx restore --confirm [--checksum SHA256] [--wait DURATION] ARCHIVE")
	}
	if !*confirmed {
		return errors.New("Nginx config restore requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("Nginx restore wait must be greater than zero")
	}
	archive, err := validateNginxArchiveName(flags.Args()[0])
	if err != nil {
		return err
	}
	checksum := strings.TrimSpace(*checksumFlag)
	if checksum == "" {
		archives, err := requestJSON[[]cliNginxArchive](ctx, client.withTimeout(45*time.Second), http.MethodGet, "/api/nginx/archives", nil, true)
		if err != nil {
			return err
		}
		for _, observed := range archives {
			if observed.Archive == archive {
				checksum = strings.TrimSpace(observed.Checksum)
				break
			}
		}
		if checksum == "" {
			return fmt.Errorf("Nginx configuration archive %q was not found in the observed inventory", archive)
		}
	}
	if !sha256Pattern.MatchString(checksum) {
		return errors.New("Nginx config restore requires a lowercase SHA-256 checksum")
	}
	endpoint := "/api/nginx/archives/" + url.PathEscape(archive) + "/restore"
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, map[string]any{"checksum": checksum}, true)
}

func runNginxArchive(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("nginx archive", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	checksumFlag := flags.String("checksum", "", "exact observed SHA-256; fetched automatically when omitted")
	confirmed := flags.Bool("confirm", false, "confirm recovery-copy-first Nginx config archival")
	wait := flags.Duration("wait", 2*time.Minute, "maximum archive and validation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl nginx archive --confirm [--checksum SHA256] [--wait DURATION] CONFIG")
	}
	if !*confirmed {
		return errors.New("Nginx config archive requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("Nginx archive wait must be greater than zero")
	}
	name, err := validateNginxConfigName(flags.Args()[0])
	if err != nil {
		return err
	}
	endpoint := "/api/nginx/configs/" + url.PathEscape(name)
	checksum := strings.TrimSpace(*checksumFlag)
	if checksum == "" {
		current, err := requestJSON[cliNginxConfig](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return err
		}
		if (current.Filename != "" && current.Filename != name) || (current.Name != "" && current.Name != name) {
			return errors.New("server resolved the Nginx configuration to a different identity; refresh inventory and retry")
		}
		checksum = strings.TrimSpace(current.Checksum)
	}
	if !sha256Pattern.MatchString(checksum) {
		return errors.New("Nginx config archive requires a lowercase SHA-256 checksum")
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, endpoint, map[string]any{"checksum": checksum}, true)
}

func validateNginxArchiveName(value string) (string, error) {
	value = strings.TrimSpace(value)
	const marker = ".hserver-archive-"
	index := strings.LastIndex(value, marker)
	if index <= 0 || strings.Contains(value, "/") || strings.Contains(value, "..") {
		return "", errors.New("Nginx archive must be a portable Heyserver archive identity")
	}
	if _, err := validateNginxConfigName(value[:index]); err != nil {
		return "", errors.New("Nginx archive must contain a portable config filename")
	}
	if _, err := time.Parse("20060102T150405.000000000Z", value[index+len(marker):]); err != nil {
		return "", errors.New("Nginx archive must include its exact Heyserver UTC timestamp")
	}
	return value, nil
}

func validateNginxBackupName(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	const marker = ".hserver-backup-"
	index := strings.LastIndex(value, marker)
	if index <= 0 || strings.Contains(value, "/") || strings.Contains(value, "..") {
		return "", "", errors.New("Nginx backup must be a portable Heyserver backup identity")
	}
	filename, err := validateNginxConfigName(value[:index])
	if err != nil {
		return "", "", errors.New("Nginx backup must contain a portable config filename")
	}
	if _, err := time.Parse("20060102T150405.000000000Z", value[index+len(marker):]); err != nil {
		return "", "", errors.New("Nginx backup must include its exact Heyserver UTC timestamp")
	}
	return value, filename, nil
}

func runNginxState(ctx context.Context, client *apiClient, args []string, out io.Writer, enabled bool) error {
	action := map[bool]string{true: "enable", false: "disable"}[enabled]
	flags := flag.NewFlagSet("nginx "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm the explicit Nginx site state")
	wait := flags.Duration("wait", 2*time.Minute, "maximum state-change wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return fmt.Errorf("usage: hserverctl nginx %s --confirm [--node NODE] [--wait DURATION] CONFIG", action)
	}
	if !*confirmed {
		return fmt.Errorf("Nginx %s requires explicit --confirm", action)
	}
	if *wait <= 0 {
		return errors.New("Nginx state wait must be greater than zero")
	}
	name, err := validateNginxConfigName(flags.Args()[0])
	if err != nil {
		return err
	}
	selectedNode := strings.TrimSpace(*node)
	if selectedNode != "" {
		endpoint := "/api/nodes/" + url.PathEscape(selectedNode) + "/domains/" + url.PathEscape(name) + "/actions/" + action
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
	}
	endpoint := "/api/nginx/configs/" + url.PathEscape(name) + "/state"
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, endpoint, map[string]any{"enabled": enabled}, true)
}

func runNginxCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("nginx create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	kind := flags.String("type", "", "site type: php, static, proxy, or redirect")
	docRoot := flags.String("doc-root", "", "absolute document root for PHP or static sites")
	phpVersion := flags.String("php-version", "", "PHP-FPM major.minor version; defaults to 8.4")
	phpPool := flags.String("php-pool", "", "PHP-FPM pool identity; defaults from the domain")
	proxyPass := flags.String("proxy-pass", "", "HTTP, HTTPS, or unix upstream for proxy sites")
	redirectTo := flags.String("redirect-to", "", "redirect target for redirect sites")
	useSSL := flags.Bool("ssl", false, "generate HTTPS listeners and an HTTP-to-HTTPS redirect")
	certPath := flags.String("cert-path", "", "absolute custom TLS certificate path")
	keyPath := flags.String("key-path", "", "absolute custom TLS private-key path")
	confirmed := flags.Bool("confirm", false, "confirm creation of the validated Nginx site")
	wait := flags.Duration("wait", 2*time.Minute, "maximum creation and validation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*kind) == "" {
		return errors.New("usage: hserverctl nginx create --confirm --type php|static|proxy|redirect [OPTIONS] DOMAIN")
	}
	if !*confirmed {
		return errors.New("Nginx site creation requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("Nginx create wait must be greater than zero")
	}
	domain, err := validateDomainName(flags.Args()[0])
	if err != nil {
		return err
	}
	request, err := nginxCreatePayload(strings.TrimSpace(*kind), domain, strings.TrimSpace(*docRoot), strings.TrimSpace(*phpVersion), strings.TrimSpace(*phpPool), strings.TrimSpace(*proxyPass), strings.TrimSpace(*redirectTo), *useSSL, strings.TrimSpace(*certPath), strings.TrimSpace(*keyPath))
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/nginx/configs", request, true)
}

func nginxCreatePayload(kind, domain, docRoot, phpVersion, phpPool, proxyPass, redirectTo string, useSSL bool, certPath, keyPath string) (map[string]any, error) {
	if (certPath == "") != (keyPath == "") {
		return nil, errors.New("Nginx create requires --cert-path and --key-path together")
	}
	if (certPath != "" || keyPath != "") && !useSSL {
		return nil, errors.New("custom certificate paths require --ssl")
	}
	for label, value := range map[string]string{"document root": docRoot, "certificate path": certPath, "private-key path": keyPath} {
		if value != "" && (!strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.ContainsAny(value, ";|&`$(){}\\'\"\n\r\t")) {
			return nil, fmt.Errorf("Nginx %s must be a safe absolute path", label)
		}
	}
	payload := map[string]any{"domain": domain, "type": kind, "useSSL": useSSL}
	switch kind {
	case "php":
		if proxyPass != "" || redirectTo != "" {
			return nil, errors.New("PHP sites do not accept --proxy-pass or --redirect-to")
		}
		if phpVersion != "" && !nginxPHPVersionPattern.MatchString(phpVersion) {
			return nil, errors.New("PHP version must use the MAJOR.MINOR numeric form")
		}
		if phpPool != "" && !nginxPHPPoolPattern.MatchString(phpPool) {
			return nil, errors.New("PHP pool must be a portable pool identity")
		}
		setOptionalString(payload, "docRoot", docRoot)
		setOptionalString(payload, "phpVersion", phpVersion)
		setOptionalString(payload, "phpPool", phpPool)
	case "static":
		if phpVersion != "" || phpPool != "" || proxyPass != "" || redirectTo != "" {
			return nil, errors.New("static sites do not accept PHP, proxy, or redirect options")
		}
		setOptionalString(payload, "docRoot", docRoot)
	case "proxy":
		if proxyPass == "" {
			return nil, errors.New("proxy sites require --proxy-pass")
		}
		if docRoot != "" || phpVersion != "" || phpPool != "" || redirectTo != "" {
			return nil, errors.New("proxy sites do not accept document-root, PHP, or redirect options")
		}
		if (!strings.HasPrefix(proxyPass, "http://") && !strings.HasPrefix(proxyPass, "https://") && !strings.HasPrefix(proxyPass, "unix:")) || strings.ContainsAny(proxyPass, ";|&`$(){}\\'\"\n\r\t") {
			return nil, errors.New("proxy upstream must be a safe HTTP, HTTPS, or unix target")
		}
		payload["proxyPass"] = proxyPass
	case "redirect":
		if redirectTo == "" {
			return nil, errors.New("redirect sites require --redirect-to")
		}
		if docRoot != "" || phpVersion != "" || phpPool != "" || proxyPass != "" {
			return nil, errors.New("redirect sites do not accept document-root, PHP, or proxy options")
		}
		if strings.Contains(redirectTo, "..") || strings.ContainsAny(redirectTo, ";|&`$(){}\\'\"\n\r\t") {
			return nil, errors.New("redirect target contains unsafe characters")
		}
		payload["redirectTo"] = redirectTo
	default:
		return nil, errors.New("Nginx site type must be php, static, proxy, or redirect")
	}
	setOptionalString(payload, "certPath", certPath)
	setOptionalString(payload, "keyPath", keyPath)
	return payload, nil
}

func setOptionalString(payload map[string]any, key, value string) {
	if value != "" {
		payload[key] = value
	}
}

type cliNginxConfig struct {
	Filename string `json:"filename"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Checksum string `json:"checksum"`
}

func runNginxEdit(ctx context.Context, client *apiClient, args []string, out, errOut io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("nginx edit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	editor := flags.String("editor", "", "editor executable; defaults to HSERVER_EDITOR, VISUAL, or EDITOR")
	confirmed := flags.Bool("confirm", false, "confirm checksum-protected Nginx replacement after editing")
	wait := flags.Duration("wait", 2*time.Minute, "maximum save and validation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl nginx edit --confirm [--node NODE] [--editor EXECUTABLE] [--wait DURATION] CONFIG")
	}
	if !*confirmed {
		return errors.New("Nginx config edit requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("Nginx edit wait must be greater than zero")
	}
	name, err := validateNginxConfigName(flags.Args()[0])
	if err != nil {
		return err
	}
	selectedNode := strings.TrimSpace(*node)
	endpoint := "/api/nginx/configs/" + url.PathEscape(name)
	if selectedNode != "" {
		endpoint = "/api/nodes/" + url.PathEscape(selectedNode) + "/nginx/configs/" + url.PathEscape(name)
	}
	current, err := requestJSON[cliNginxConfig](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	if (current.Filename != "" && current.Filename != name) || (current.Name != "" && current.Name != name) {
		return errors.New("server resolved the Nginx configuration to a different identity; refresh inventory and retry")
	}
	checksum := strings.TrimSpace(current.Checksum)
	if !sha256Pattern.MatchString(checksum) {
		return errors.New("server returned an invalid Nginx configuration checksum")
	}
	edited, changed, err := editCLIText(ctx, current.Content, *editor, "hserver-nginx-*.conf", maxNginxConfigBytes, errOut, getenv)
	if err != nil {
		return err
	}
	if !changed {
		return printJSONValue(out, map[string]any{"changed": false, "checksum": checksum, "message": "Nginx configuration was not changed", "name": name})
	}
	payload := map[string]any{"content": edited, "checksum": checksum}
	if selectedNode != "" {
		payload["reload"] = false
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, endpoint, payload, true)
}

func parseOptionalNode(command string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if len(flags.Args()) != 0 {
		return "", fmt.Errorf("usage: hserverctl %s [--node NODE]", command)
	}
	return strings.TrimSpace(*node), nil
}

func runNginxAction(ctx context.Context, client *apiClient, args []string, out io.Writer, action string, mutation bool) error {
	flags := flag.NewFlagSet("nginx "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm the Nginx mutation")
	wait := flags.Duration("wait", 2*time.Minute, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return fmt.Errorf("usage: hserverctl nginx %s %s[--node NODE] [--wait DURATION]", action, map[bool]string{true: "--confirm ", false: ""}[mutation])
	}
	if mutation && !*confirmed {
		return errors.New("Nginx reload requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("Nginx action wait must be greater than zero")
	}
	endpoint := "/api/nginx/" + action
	if strings.TrimSpace(*node) != "" {
		endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/nginx/actions/" + action
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
}

func runNginxSave(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("nginx save", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	contentFile := flags.String("content-file", "", "regular UTF-8 file containing the complete Nginx config")
	checksum := flags.String("checksum", "", "current config SHA-256 checksum")
	confirmed := flags.Bool("confirm", false, "confirm replacement of the Nginx config")
	wait := flags.Duration("wait", 2*time.Minute, "maximum save wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*contentFile) == "" {
		return errors.New("usage: hserverctl nginx save --confirm [--node NODE] --content-file PATH --checksum SHA256 [--wait DURATION] CONFIG")
	}
	if !*confirmed {
		return errors.New("Nginx config save requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("Nginx save wait must be greater than zero")
	}
	name, err := validateNginxConfigName(flags.Args()[0])
	if err != nil {
		return err
	}
	content, err := readNginxConfigFile(strings.TrimSpace(*contentFile))
	if err != nil {
		return err
	}
	expected := strings.TrimSpace(*checksum)
	if !sha256Pattern.MatchString(expected) {
		return errors.New("Nginx save requires the lowercase SHA-256 from nginx get via --checksum")
	}
	endpoint := "/api/nginx/configs/" + url.PathEscape(name)
	payload := map[string]any{"content": content, "checksum": expected}
	if strings.TrimSpace(*node) != "" {
		endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/nginx/configs/" + url.PathEscape(name)
		payload["reload"] = false
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, endpoint, payload, true)
}

func validateNginxConfigName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !nginxConfigNamePattern.MatchString(value) {
		return "", errors.New("Nginx config identity must be a portable config filename")
	}
	return value, nil
}

func readNginxConfigFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read Nginx content file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Nginx content file must be a regular file and not a symlink")
	}
	if info.Size() > maxNginxConfigBytes {
		return "", fmt.Errorf("Nginx content file exceeds %d bytes", maxNginxConfigBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read Nginx content file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxNginxConfigBytes+1))
	if err != nil {
		return "", fmt.Errorf("read Nginx content file: %w", err)
	}
	if len(data) > maxNginxConfigBytes {
		return "", fmt.Errorf("Nginx content file exceeds %d bytes", maxNginxConfigBytes)
	}
	if len(data) == 0 {
		return "", errors.New("Nginx content file is empty")
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return "", errors.New("Nginx content file must be valid UTF-8 text without NUL bytes")
	}
	return string(data), nil
}
