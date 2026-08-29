package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var cliFail2BanJailPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type cliSecurityCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type cliSecurityScore struct {
	Score    int                `json:"score"`
	MaxScore int                `json:"maxScore"`
	Checks   []cliSecurityCheck `json:"checks"`
}

type cliFail2BanJail struct {
	Name            string   `json:"name"`
	Filter          string   `json:"filter"`
	Actions         string   `json:"actions"`
	LogPath         string   `json:"logPath"`
	CurrentlyFailed int      `json:"currentlyFailed"`
	CurrentlyBanned int      `json:"currentlyBanned"`
	TotalBanned     int      `json:"totalBanned"`
	BannedIPs       []string `json:"bannedIPs"`
}

type cliFail2BanStatus struct {
	Available      bool              `json:"available"`
	Installed      bool              `json:"installed"`
	Running        bool              `json:"running"`
	State          string            `json:"state"`
	DaemonState    string            `json:"daemonState"`
	Error          string            `json:"error"`
	AvailableJails []string          `json:"availableJails"`
	Jails          []cliFail2BanJail `json:"jails"`
}

// cliSecurityIPEntry mirrors the panel's IPEntry wire shape without coupling
// the CLI package to the service implementation. The access-list endpoints
// are panel-local admin resources and return these entries as JSON.
type cliSecurityIPEntry struct {
	ID        int64      `json:"id"`
	IP        string     `json:"ip"`
	ListType  string     `json:"listType"`
	Comment   string     `json:"comment"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type cliSecurityIPDeleteReceipt struct {
	Status   string `json:"status"`
	IP       string `json:"ip"`
	ListType string `json:"listType"`
}

func runSecurity(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl security score|fail2ban|ip-blacklist|ip-whitelist")
	}
	switch args[0] {
	case "score":
		if len(args) != 1 {
			return errors.New("usage: hserverctl security score")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/security/score", nil, true)
	case "fail2ban":
		return runSecurityFail2Ban(ctx, client, args[1:], out)
	case "ip-blacklist", "ip-whitelist":
		return runSecurityIPList(ctx, client, args[0], args[1:], out)
	default:
		return fmt.Errorf("unknown security command %q", args[0])
	}
}

func runSecurityIPList(ctx context.Context, client *apiClient, listType string, args []string, out io.Writer) error {
	if listType != "ip-blacklist" && listType != "ip-whitelist" {
		return fmt.Errorf("unknown security IP list %q", listType)
	}
	// A bare access-list command is a convenient read-only alias for list. It
	// also keeps an accidental omission of the action from becoming a mutation.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return runSecurityIPListList(ctx, client, listType, args, out)
	}
	switch args[0] {
	case "list":
		return runSecurityIPListList(ctx, client, listType, args[1:], out)
	case "add":
		return runSecurityIPListAdd(ctx, client, listType, args[1:], out)
	case "delete":
		return runSecurityIPListDelete(ctx, client, listType, args[1:], out)
	default:
		return fmt.Errorf("unknown %s command %q", listType, args[0])
	}
}

func runSecurityIPListList(ctx context.Context, client *apiClient, listType string, args []string, out io.Writer) error {
	format, err := parseSecurityIPListFormat("security "+listType+" list", args)
	if err != nil {
		return err
	}
	endpoint := "/api/security/" + listType
	entries, err := requestJSON[[]cliSecurityIPEntry](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []cliSecurityIPEntry{}
	}
	if format == "text" {
		return writeSecurityIPListText(out, listType, entries)
	}
	return printJSONValue(out, entries)
}

func runSecurityIPListAdd(ctx context.Context, client *apiClient, listType string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("security "+listType+" add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm the security IP-list mutation")
	ipFlag := flags.String("ip", "", "IP address or CIDR")
	comment := flags.String("comment", "", "optional access-list comment")
	expiresInMinutes := flags.Int("expires-in-minutes", 0, "optional expiration in minutes; zero means no expiration")
	formatValue := flags.String("format", "json", "output format: json or text")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) > 1 {
		return fmt.Errorf("usage: hserverctl security %s add --confirm [--ip IP|CIDR] [--comment TEXT] [--expires-in-minutes MINUTES] [--format json|text] [IP|CIDR]", listType)
	}
	if !*confirmed {
		return errors.New("security IP-list addition requires explicit --confirm")
	}
	if *expiresInMinutes < 0 {
		return errors.New("security IP-list expiration must be zero or greater")
	}
	if flagWasSet(flags, "ip") && len(flags.Args()) != 0 {
		return errors.New("security IP-list add accepts the IP/CIDR either as --ip or as one positional argument, not both")
	}
	rawIP := *ipFlag
	if len(flags.Args()) == 1 {
		rawIP = flags.Args()[0]
	}
	ip, err := validateCLISecurityIPOrCIDR(rawIP)
	if err != nil {
		return err
	}
	format, err := normalizeSecurityIPListFormat(*formatValue)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"ip":      ip,
		"comment": *comment,
	}
	if flagWasSet(flags, "expires-in-minutes") {
		payload["expiresInMinutes"] = *expiresInMinutes
	}
	endpoint := "/api/security/" + listType
	entry, err := requestJSON[cliSecurityIPEntry](ctx, client, http.MethodPost, endpoint, payload, true)
	if err != nil {
		return err
	}
	if format == "text" {
		return writeSecurityIPEntryText(out, "Added security IP entry", listType, entry)
	}
	return printJSONValue(out, entry)
}

func runSecurityIPListDelete(ctx context.Context, client *apiClient, listType string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("security "+listType+" delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm the security IP-list mutation")
	ipFlag := flags.String("ip", "", "IP address or CIDR")
	formatValue := flags.String("format", "json", "output format: json or text")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) > 1 {
		return fmt.Errorf("usage: hserverctl security %s delete --confirm [--ip IP|CIDR] [--format json|text] [IP|CIDR]", listType)
	}
	if !*confirmed {
		return errors.New("security IP-list deletion requires explicit --confirm")
	}
	if flagWasSet(flags, "ip") && len(flags.Args()) != 0 {
		return errors.New("security IP-list delete accepts the IP/CIDR either as --ip or as one positional argument, not both")
	}
	rawIP := *ipFlag
	if len(flags.Args()) == 1 {
		rawIP = flags.Args()[0]
	}
	ip, err := validateCLISecurityIPOrCIDR(rawIP)
	if err != nil {
		return err
	}
	format, err := normalizeSecurityIPListFormat(*formatValue)
	if err != nil {
		return err
	}
	endpoint := "/api/security/" + listType + "/" + url.PathEscape(ip)
	if _, err := client.request(ctx, http.MethodDelete, endpoint, nil, true); err != nil {
		return err
	}
	receipt := cliSecurityIPDeleteReceipt{Status: "deleted", IP: ip, ListType: strings.TrimPrefix(listType, "ip-")}
	if format == "text" {
		fmt.Fprintf(out, "Deleted security IP entry: %s from %s\n", securityIPTextValue(ip), securityIPTextValue(receipt.ListType))
		return nil
	}
	return printJSONValue(out, receipt)
}

func parseSecurityIPListFormat(command string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", "json", "output format: json or text")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if len(flags.Args()) != 0 {
		return "", fmt.Errorf("usage: hserverctl %s [--format json|text]", command)
	}
	return normalizeSecurityIPListFormat(*format)
}

func normalizeSecurityIPListFormat(value string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(value))
	if format != "json" && format != "text" {
		return "", errors.New("security IP-list format must be json or text")
	}
	return format, nil
}

func validateCLISecurityIPOrCIDR(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "/") {
		if _, _, err := net.ParseCIDR(value); err != nil {
			return "", fmt.Errorf("invalid CIDR: %s", value)
		}
		return value, nil
	}
	if net.ParseIP(value) == nil {
		return "", fmt.Errorf("invalid IP address: %s", value)
	}
	return value, nil
}

func writeSecurityIPListText(out io.Writer, listType string, entries []cliSecurityIPEntry) error {
	label := strings.TrimPrefix(listType, "ip-")
	fmt.Fprintf(out, "Security IP %s (%d entries)\n", securityIPTextValue(label), len(entries))
	if len(entries) == 0 {
		fmt.Fprintln(out, "No entries.")
		return nil
	}
	fmt.Fprintln(out, "ID\tIP\tCOMMENT\tCREATED_AT\tEXPIRES_AT")
	for _, entry := range entries {
		fmt.Fprintf(out, "%d\t%s\t%s\t%s\t%s\n",
			entry.ID,
			securityIPTextValue(entry.IP),
			securityIPTextValue(entry.Comment),
			securityIPEntryTimeText(entry.CreatedAt, "N/A"),
			securityIPEntryExpiryText(entry.ExpiresAt),
		)
	}
	return nil
}

func writeSecurityIPEntryText(out io.Writer, heading, listType string, entry cliSecurityIPEntry) error {
	fmt.Fprintln(out, heading)
	fmt.Fprintf(out, "ID: %d\n", entry.ID)
	fmt.Fprintf(out, "IP: %s\n", securityIPTextValue(entry.IP))
	fmt.Fprintf(out, "List: %s\n", securityIPTextValue(strings.TrimPrefix(listType, "ip-")))
	fmt.Fprintf(out, "Comment: %s\n", securityIPTextValue(entry.Comment))
	fmt.Fprintf(out, "Created: %s\n", securityIPEntryTimeText(entry.CreatedAt, "N/A"))
	fmt.Fprintf(out, "Expires: %s\n", securityIPEntryExpiryText(entry.ExpiresAt))
	return nil
}

func securityIPEntryTimeText(value time.Time, empty string) string {
	if value.IsZero() {
		return empty
	}
	return value.UTC().Format(time.RFC3339)
}

func securityIPEntryExpiryText(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "never"
	}
	return value.UTC().Format(time.RFC3339)
}

func securityIPTextValue(value string) string {
	value = strings.Join(strings.Fields(sanitizeTUIText(value)), " ")
	if value == "" {
		return "N/A"
	}
	return value
}

func runSecurityFail2Ban(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl security fail2ban status|jail|ban|unban")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: hserverctl security fail2ban status")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/security/fail2ban/status", nil, true)
	case "jail":
		if len(args) != 2 {
			return errors.New("usage: hserverctl security fail2ban jail JAIL")
		}
		jail, err := validateCLIFail2BanJail(args[1])
		if err != nil {
			return err
		}
		if _, err := requireCLIFail2BanJail(ctx, client, jail); err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/security/fail2ban/jails/"+url.PathEscape(jail), nil, true)
	case "ban", "unban":
		return runSecurityFail2BanMutation(ctx, client, args[0], args[1:], out)
	default:
		return fmt.Errorf("unknown fail2ban command %q", args[0])
	}
}

func runSecurityFail2BanMutation(ctx context.Context, client *apiClient, action string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("security fail2ban "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm the Fail2Ban mutation")
	wait := flags.Duration("wait", 30*time.Second, "maximum Fail2Ban action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return fmt.Errorf("usage: hserverctl security fail2ban %s --confirm [--wait DURATION] JAIL IP", action)
	}
	if !*confirmed {
		return errors.New("Fail2Ban IP mutation requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("Fail2Ban action wait must be greater than zero")
	}
	jail, err := validateCLIFail2BanJail(flags.Args()[0])
	if err != nil {
		return err
	}
	ip, err := validateCLIFail2BanIP(flags.Args()[1])
	if err != nil {
		return err
	}
	status, err := requireCLIFail2BanJail(ctx, client, jail)
	if err != nil {
		return err
	}
	if action == "unban" && !fail2BanStatusHasIP(status, jail, ip) {
		return errors.New("IP is not present in the current banned inventory for this jail")
	}
	payload := map[string]string{"jail": jail, "ip": ip}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/security/fail2ban/"+action, payload, true)
}

func loadCLIFail2BanStatus(ctx context.Context, client *apiClient) (cliFail2BanStatus, error) {
	return requestJSON[cliFail2BanStatus](ctx, client, http.MethodGet, "/api/security/fail2ban/status", nil, true)
}

func requireCLIFail2BanJail(ctx context.Context, client *apiClient, jail string) (cliFail2BanStatus, error) {
	status, err := loadCLIFail2BanStatus(ctx, client)
	if err != nil {
		return cliFail2BanStatus{}, err
	}
	if !status.Available || status.State != "healthy" {
		detail := strings.TrimSpace(status.Error)
		if detail == "" {
			detail = "state " + valueOrNA(status.State)
		}
		return cliFail2BanStatus{}, errors.New("Fail2Ban is not ready for jail operations: " + detail)
	}
	for _, observed := range status.AvailableJails {
		if observed == jail {
			return status, nil
		}
	}
	for _, observed := range status.Jails {
		if observed.Name == jail {
			return status, nil
		}
	}
	return cliFail2BanStatus{}, errors.New("Fail2Ban jail is not present in the current observed inventory")
}

func validateCLIFail2BanJail(value string) (string, error) {
	jail := strings.TrimSpace(value)
	if !cliFail2BanJailPattern.MatchString(jail) {
		return "", errors.New("Fail2Ban jail must be a 1-64 character portable name")
	}
	return jail, nil
}

func validateCLIFail2BanIP(value string) (string, error) {
	ip := strings.TrimSpace(value)
	parsed := net.ParseIP(ip)
	if parsed == nil || strings.Contains(ip, "%") {
		return "", errors.New("Fail2Ban target must be a plain IPv4 or IPv6 address")
	}
	return ip, nil
}

func fail2BanStatusHasIP(status cliFail2BanStatus, jail, ip string) bool {
	needle := net.ParseIP(ip)
	for _, item := range status.Jails {
		if item.Name != jail {
			continue
		}
		for _, banned := range item.BannedIPs {
			if observed := net.ParseIP(strings.TrimSpace(banned)); observed != nil && observed.Equal(needle) {
				return true
			}
		}
	}
	return false
}
