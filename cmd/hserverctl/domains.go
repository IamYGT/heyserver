package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	domainResourceIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9*][A-Za-z0-9*._-]{0,254}$`)
	domainCertificateNamePattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,253}$`)
	domainPM2AppPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)
)

type domainCreatePayload struct {
	Domain            string `json:"domain"`
	Type              string `json:"type"`
	PHPVersion        string `json:"phpVersion,omitempty"`
	ProxyPort         int    `json:"proxyPort,omitempty"`
	WebRoot           string `json:"webRoot,omitempty"`
	FPMPreset         string `json:"fpmPreset,omitempty"`
	SPAMode           bool   `json:"spaMode,omitempty"`
	WWWRedirect       bool   `json:"wwwRedirect,omitempty"`
	IssueSSL          bool   `json:"issueSSL,omitempty"`
	SSLEmail          string `json:"sslEmail,omitempty"`
	ExistingCertName  string `json:"existingCertName,omitempty"`
	CreateDNSRecord   bool   `json:"createDnsRecord,omitempty"`
	PM2App            string `json:"pm2_app,omitempty"`
	PM2Script         string `json:"pm2_script,omitempty"`
	PM2Cwd            string `json:"pm2_cwd,omitempty"`
	PM2Port           int    `json:"pm2_port,omitempty"`
	NodeEnv           string `json:"nodeEnv,omitempty"`
	IsolatedLinuxUser bool   `json:"isolatedLinuxUser,omitempty"`
}

func runDomains(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl domains list|provisioning|get|check|create|action|delete")
	}
	switch args[0] {
	case "list":
		node, err := parseOptionalNode("domains list", args[1:])
		if err != nil {
			return err
		}
		endpoint := "/api/domains"
		if node != "" {
			endpoint = "/api/nodes/" + url.PathEscape(node) + "/domains"
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "provisioning":
		if len(args) != 1 {
			return errors.New("usage: hserverctl domains provisioning")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/domains/provisioning", nil, true)
	case "get":
		if len(args) != 2 {
			return errors.New("usage: hserverctl domains get DOMAIN_ID")
		}
		identity, err := validateLocalDomainName(args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/domains/"+url.PathEscape(identity), nil, true)
	case "check":
		if len(args) != 2 {
			return errors.New("usage: hserverctl domains check DOMAIN")
		}
		domain, err := validateLocalDomainName(args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodPost, "/api/domains/check", map[string]string{"domain": domain}, true)
	case "create":
		return runDomainCreate(ctx, client, args[1:], out)
	case "action":
		return runDomainAction(ctx, client, args[1:], out)
	case "delete":
		return runDomainDelete(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown domains command %q", args[0])
	}
}

func runDomainCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("domains create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm local domain provisioning")
	domainType := flags.String("type", "php", "runtime type: php, proxy, or static")
	phpVersion := flags.String("php-version", "8.4", "PHP-FPM version")
	webRoot := flags.String("web-root", "", "absolute document root")
	fpmPreset := flags.String("fpm-preset", "medium", "PHP-FPM preset: low, medium, or high")
	proxyPort := flags.Int("proxy-port", 0, "reverse-proxy upstream port; zero uses the server default")
	spaMode := flags.Bool("spa", false, "enable static SPA fallback")
	wwwRedirect := flags.Bool("www-redirect", false, "redirect www to the canonical hostname")
	issueSSL := flags.Bool("issue-ssl", false, "issue and activate a certificate")
	sslEmail := flags.String("ssl-email", "", "plain ACME account email")
	existingCert := flags.String("existing-cert", "", "existing Certbot certificate name")
	createDNSRecord := flags.Bool("create-dns-record", false, "reconcile the configured optional DNS provider")
	pm2App := flags.String("pm2-app", "", "PM2 application name")
	pm2Script := flags.String("pm2-script", "", "absolute script or path relative to --pm2-cwd")
	pm2Cwd := flags.String("pm2-cwd", "", "absolute PM2 working directory")
	pm2Port := flags.Int("pm2-port", 0, "PM2 application and reverse-proxy port")
	nodeEnv := flags.String("node-env", "production", "PM2 NODE_ENV: production or development")
	isolatedLinuxUser := flags.Bool("isolated-linux-user", false, "create a dedicated Linux owner for a PHP domain")
	wait := flags.Duration("wait", 20*time.Minute, "maximum provisioning wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl domains create --confirm [OPTIONS] DOMAIN")
	}
	if !*confirmed {
		return errors.New("domain creation requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("domain creation wait must be greater than zero")
	}
	domain, err := validateLocalDomainName(flags.Args()[0])
	if err != nil {
		return err
	}

	specified := make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { specified[item.Name] = true })
	kind := strings.ToLower(strings.TrimSpace(*domainType))
	if kind != "php" && kind != "proxy" && kind != "static" {
		return errors.New("domain type must be php, proxy, or static")
	}
	if *proxyPort < 0 || *proxyPort > 65535 || *pm2Port < 0 || *pm2Port > 65535 {
		return errors.New("domain ports must be between 1 and 65535 when supplied")
	}

	payload := domainCreatePayload{
		Domain:          domain,
		Type:            kind,
		WWWRedirect:     *wwwRedirect,
		CreateDNSRecord: *createDNSRecord,
	}
	trimmedWebRoot := strings.TrimSpace(*webRoot)
	if trimmedWebRoot != "" {
		if len(trimmedWebRoot) > 4096 || strings.ContainsAny(trimmedWebRoot, "\r\n\x00") || !filepath.IsAbs(trimmedWebRoot) {
			return errors.New("domain web root must be an absolute single-line path of at most 4096 bytes")
		}
		payload.WebRoot = filepath.Clean(trimmedWebRoot)
	}

	switch kind {
	case "php":
		if specified["proxy-port"] || specified["spa"] || specified["pm2-app"] || specified["pm2-script"] || specified["pm2-cwd"] || specified["pm2-port"] || specified["node-env"] {
			return errors.New("PHP domains do not accept proxy, SPA, or PM2 options")
		}
		version := strings.TrimSpace(*phpVersion)
		if version != "7.4" && version != "8.0" && version != "8.1" && version != "8.2" && version != "8.3" && version != "8.4" && version != "8.5" {
			return errors.New("PHP domain version must be 7.4 or 8.0 through 8.5")
		}
		preset := strings.ToLower(strings.TrimSpace(*fpmPreset))
		if preset != "low" && preset != "medium" && preset != "high" {
			return errors.New("PHP domain FPM preset must be low, medium, or high")
		}
		payload.PHPVersion = version
		payload.FPMPreset = preset
		payload.IsolatedLinuxUser = *isolatedLinuxUser
	case "static":
		if specified["php-version"] || specified["fpm-preset"] || specified["proxy-port"] || specified["pm2-app"] || specified["pm2-script"] || specified["pm2-cwd"] || specified["pm2-port"] || specified["node-env"] || specified["isolated-linux-user"] {
			return errors.New("static domains do not accept PHP, proxy, PM2, or isolated-user options")
		}
		payload.SPAMode = *spaMode
	case "proxy":
		if specified["php-version"] || specified["fpm-preset"] || specified["web-root"] || specified["spa"] || specified["isolated-linux-user"] {
			return errors.New("proxy domains do not accept PHP, web-root, SPA, or isolated-user options")
		}
		payload.ProxyPort = *proxyPort
		app := strings.TrimSpace(*pm2App)
		script := strings.TrimSpace(*pm2Script)
		cwd := strings.TrimSpace(*pm2Cwd)
		if (app == "") != (script == "") {
			return errors.New("proxy domain PM2 app and script must be supplied together")
		}
		if (specified["pm2-cwd"] || specified["pm2-port"] || specified["node-env"]) && app == "" {
			return errors.New("proxy domain PM2 cwd, port, and node environment require --pm2-app and --pm2-script")
		}
		if app != "" {
			if !domainPM2AppPattern.MatchString(app) {
				return errors.New("proxy domain PM2 app name is invalid")
			}
			if len(script) > 4096 || strings.ContainsAny(script, ";|&`$()\n\r\x00") {
				return errors.New("proxy domain PM2 script path is invalid")
			}
			if cwd != "" && (len(cwd) > 4096 || strings.ContainsAny(cwd, "\r\n\x00") || !filepath.IsAbs(cwd)) {
				return errors.New("proxy domain PM2 cwd must be an absolute single-line path of at most 4096 bytes")
			}
			if !filepath.IsAbs(script) && cwd == "" {
				return errors.New("a relative PM2 script requires --pm2-cwd")
			}
			environment := strings.ToLower(strings.TrimSpace(*nodeEnv))
			if environment != "production" && environment != "development" {
				return errors.New("proxy domain node environment must be production or development")
			}
			payload.PM2App = app
			payload.PM2Script = script
			if cwd != "" {
				payload.PM2Cwd = filepath.Clean(cwd)
			}
			payload.PM2Port = *pm2Port
			payload.NodeEnv = environment
		}
	}

	payload.IssueSSL = *issueSSL
	email := strings.TrimSpace(*sslEmail)
	if *issueSSL {
		parsed, emailErr := mail.ParseAddress(email)
		if email == "" || len(email) > 254 || emailErr != nil || parsed.Address != email {
			return errors.New("domain SSL issuance requires a plain valid --ssl-email")
		}
		payload.SSLEmail = email
	} else if specified["ssl-email"] {
		return errors.New("--ssl-email requires --issue-ssl")
	}
	certificateName := strings.TrimSpace(*existingCert)
	if certificateName != "" {
		if certificateName == "." || certificateName == ".." || !domainCertificateNamePattern.MatchString(certificateName) {
			return errors.New("existing certificate name is invalid")
		}
		payload.ExistingCertName = certificateName
	}

	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/domains", payload, true)
}

func runDomainAction(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("domains action", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm the domain state mutation")
	wait := flags.Duration("wait", 2*time.Minute, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return errors.New("usage: hserverctl domains action --confirm [--node NODE] [--wait DURATION] TARGET enable|disable")
	}
	if !*confirmed {
		return errors.New("domain action requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("domain action wait must be greater than zero")
	}
	identity := ""
	var err error
	if strings.TrimSpace(*node) == "" {
		identity, err = validateLocalDomainName(flags.Args()[0])
	} else {
		identity, err = validateDomainResourceIdentity(flags.Args()[0])
	}
	if err != nil {
		return err
	}
	action := flags.Args()[1]
	if action != "enable" && action != "disable" {
		return fmt.Errorf("unsupported domain action %q", action)
	}
	endpoint := "/api/domains/" + url.PathEscape(identity) + "/toggle"
	payload := any(map[string]bool{"active": action == "enable"})
	if strings.TrimSpace(*node) != "" {
		endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/domains/" +
			url.PathEscape(identity) + "/actions/" + action
		payload = nil
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, payload, true)
}

func runDomainDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("domains delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm deletion of the local domain configuration")
	deleteFiles := flags.Bool("delete-files", false, "also delete the domain document root")
	wait := flags.Duration("wait", 2*time.Minute, "maximum deletion wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl domains delete --confirm [--delete-files] [--wait DURATION] DOMAIN_ID")
	}
	if !*confirmed {
		return errors.New("domain deletion requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("domain deletion wait must be greater than zero")
	}
	identity, err := validateLocalDomainName(flags.Args()[0])
	if err != nil {
		return err
	}
	query := url.Values{"deleteFiles": []string{strconv.FormatBool(*deleteFiles)}}
	endpoint := "/api/domains/" + url.PathEscape(identity) + "?" + query.Encode()
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, endpoint, nil, true)
}

func validateDomainResourceIdentity(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !domainResourceIdentityPattern.MatchString(value) || strings.Contains(value, "..") {
		return "", errors.New("domain identity must be a portable domain or config name")
	}
	return value, nil
}

func validateLocalDomainName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 253 || !strings.Contains(value, ".") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return "", errors.New("local domain identity must be an exact portable DNS hostname")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("local domain identity must be an exact portable DNS hostname")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", errors.New("local domain identity must be an exact portable DNS hostname")
			}
		}
	}
	return value, nil
}

func validateDomainName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) == 0 || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") {
		return "", errors.New("domain must be a valid portable DNS name")
	}
	for index, label := range strings.Split(value, ".") {
		if index == 0 && label == "*" {
			continue
		}
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("domain must be a valid portable DNS name")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", errors.New("domain must be a valid portable DNS name")
			}
		}
	}
	return value, nil
}
