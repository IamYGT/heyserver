package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

const (
	maxMailDomainBytes   = 253
	maxMailLogQueryBytes = 256
	maxMailLogLines      = 5000
	maxMailQueueLimit    = 1000
)

// Mail API handlers deliberately return small, stable projections. Keep the
// CLI projections typed as well: this prevents a provider response body from
// becoming an accidental secret or terminal-control output sink.
type mailServiceOverview struct {
	Status    mailsvc.ServiceStatus  `json:"status"`
	Version   mailsvc.VersionInfo    `json:"version"`
	Listeners []mailsvc.ListenerInfo `json:"listeners"`
	Storage   mailsvc.StorageInfo    `json:"storage"`
	Sources   map[string]mailSource  `json:"sources"`
}

type mailSource struct {
	Available bool   `json:"available"`
	State     string `json:"state,omitempty"`
	Error     string `json:"error,omitempty"`
}

type mailLogResponse struct {
	Lines   int                `json:"lines,omitempty"`
	Query   string             `json:"query,omitempty"`
	Count   int                `json:"count"`
	Entries []mailsvc.LogEntry `json:"entries"`
}

type mailDeliveryLogResponse struct {
	Email   string             `json:"email"`
	Count   int                `json:"count"`
	Entries []mailsvc.LogEntry `json:"entries"`
}

type mailMutationResponse struct {
	Status string `json:"status"`
}

func runMail(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl mail service|status|logs|queue|domains|accounts|aliases")
	}
	switch args[0] {
	case "service":
		return runMailService(ctx, client, args[1:], out)
	case "status":
		if len(args) != 1 {
			return errors.New("usage: hserverctl mail status")
		}
		return printMailJSON[mailsvc.ServiceStatus](ctx, client, out, http.MethodGet, "/api/mail/status", nil)
	case "logs":
		return runMailLogs(ctx, client, args[1:], out)
	case "queue":
		return runMailQueue(ctx, client, args[1:], out)
	case "domains":
		return runMailDomains(ctx, client, args[1:], out)
	case "accounts":
		return runMailAccounts(ctx, client, args[1:], out)
	case "aliases":
		return runMailAliases(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown mail command %q", args[0])
	}
}

func printMailJSON[T any](ctx context.Context, client *apiClient, out io.Writer, method, endpoint string, payload any) error {
	value, err := requestJSON[T](ctx, client, method, endpoint, payload, true)
	if err != nil {
		return err
	}
	return printJSONValue(out, value)
}

func printMailMutation(ctx context.Context, client *apiClient, out io.Writer, method, endpoint string) error {
	return printMailJSON[mailMutationResponse](ctx, client, out, method, endpoint, nil)
}

func runMailService(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: hserverctl mail service status|overview")
	}
	switch args[0] {
	case "status":
		return printMailJSON[mailsvc.ServiceStatus](ctx, client, out, http.MethodGet, "/api/mail/service/status", nil)
	case "overview":
		return printMailJSON[mailServiceOverview](ctx, client, out, http.MethodGet, "/api/mail/service/overview", nil)
	default:
		return fmt.Errorf("unknown mail service command %q", args[0])
	}
}

func runMailLogs(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "search":
			return runMailLogSearch(ctx, client, args[1:], out)
		case "delivery":
			return runMailLogDelivery(ctx, client, args[1:], out)
		case "normal":
			// Keep the explicit form useful for scripts while the short
			// `mail logs` form remains the documented default.
			return runMailLogList(ctx, client, args[1:], out)
		}
	}
	return runMailLogList(ctx, client, args, out)
}

func runMailLogList(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("mail logs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	lines := flags.Int("lines", 100, "number of latest mail service log entries")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl mail logs [--lines N]")
	}
	if *lines < 1 || *lines > maxMailLogLines {
		return fmt.Errorf("mail log line count must be between 1 and %d", maxMailLogLines)
	}
	query := url.Values{}
	query.Set("lines", fmt.Sprint(*lines))
	response, err := requestJSON[mailLogResponse](ctx, client, http.MethodGet, "/api/mail/logs?"+query.Encode(), nil, true)
	if err != nil {
		return err
	}
	if response.Entries == nil {
		response.Entries = []mailsvc.LogEntry{}
	}
	return printJSONValue(out, response)
}

func runMailLogSearch(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("mail logs search", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	query := flags.String("query", "", "search query for the mail service journal")
	shortQuery := flags.String("q", "", "alias for --query")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl mail logs search --query QUERY")
	}
	selected, err := selectMailQuery(*query, *shortQuery, "mail log search")
	if err != nil {
		return err
	}
	params := url.Values{}
	params.Set("q", selected)
	response, err := requestJSON[mailLogResponse](ctx, client, http.MethodGet, "/api/mail/logs/search?"+params.Encode(), nil, true)
	if err != nil {
		return err
	}
	if response.Entries == nil {
		response.Entries = []mailsvc.LogEntry{}
	}
	return printJSONValue(out, response)
}

func runMailLogDelivery(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("mail logs delivery", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "email address to search in the mail service journal")
	query := flags.String("query", "", "alias for --email")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl mail logs delivery --email EMAIL")
	}
	selected, err := selectMailQuery(*email, *query, "mail delivery log")
	if err != nil {
		return err
	}
	params := url.Values{}
	params.Set("email", selected)
	response, err := requestJSON[mailDeliveryLogResponse](ctx, client, http.MethodGet, "/api/mail/logs/delivery?"+params.Encode(), nil, true)
	if err != nil {
		return err
	}
	if response.Entries == nil {
		response.Entries = []mailsvc.LogEntry{}
	}
	return printJSONValue(out, response)
}

func selectMailQuery(primary, alias, label string) (string, error) {
	primary = strings.TrimSpace(primary)
	alias = strings.TrimSpace(alias)
	if primary != "" && alias != "" && primary != alias {
		return "", fmt.Errorf("%s accepts only one query flag", label)
	}
	value := primary
	if value == "" {
		value = alias
	}
	if value == "" {
		return "", fmt.Errorf("%s requires a non-empty query", label)
	}
	if !utf8.ValidString(value) || len([]byte(value)) > maxMailLogQueryBytes {
		return "", fmt.Errorf("%s query must be valid UTF-8 of at most %d bytes", label, maxMailLogQueryBytes)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s query must not contain control characters", label)
	}
	return value, nil
}

func runMailQueue(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl mail queue list|retry|delete")
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("mail queue list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		limit := flags.Int("limit", 100, "maximum queued messages to return")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return errors.New("usage: hserverctl mail queue list [--limit N]")
		}
		if *limit < 1 || *limit > maxMailQueueLimit {
			return fmt.Errorf("mail queue limit must be between 1 and %d", maxMailQueueLimit)
		}
		params := url.Values{}
		params.Set("limit", fmt.Sprint(*limit))
		messages, err := requestJSON[[]mailsvc.QueueMessage](ctx, client, http.MethodGet, "/api/mail/queue?"+params.Encode(), nil, true)
		if err != nil {
			return err
		}
		if messages == nil {
			messages = []mailsvc.QueueMessage{}
		}
		return printJSONValue(out, messages)
	case "retry", "delete":
		return runMailQueueMutation(ctx, client, args[0], args[1:], out)
	default:
		return fmt.Errorf("unknown mail queue command %q", args[0])
	}
}

func runMailQueueMutation(ctx context.Context, client *apiClient, action string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("mail queue "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm the queued-message mutation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return fmt.Errorf("usage: hserverctl mail queue %s --confirm ID", action)
	}
	if !*confirmed {
		return fmt.Errorf("mail queue %s requires explicit --confirm", action)
	}
	id, err := normalizeMailPathValue("mail queue message ID", flags.Args()[0])
	if err != nil {
		return err
	}
	endpoint := "/api/mail/queue/" + url.PathEscape(id)
	method := http.MethodDelete
	if action == "retry" {
		method = http.MethodPost
		endpoint += "/retry"
	}
	return printMailMutation(ctx, client, out, method, endpoint)
}

func runMailDomains(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl mail domains list|create|delete")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: hserverctl mail domains list")
		}
		domains, err := requestJSON[[]mailsvc.MailDomain](ctx, client, http.MethodGet, "/api/mail/domains", nil, true)
		if err != nil {
			return err
		}
		if domains == nil {
			domains = []mailsvc.MailDomain{}
		}
		return printJSONValue(out, domains)
	case "create":
		return runMailDomainMutation(ctx, client, "create", args[1:], out)
	case "delete":
		return runMailDomainMutation(ctx, client, "delete", args[1:], out)
	default:
		return fmt.Errorf("unknown mail domains command %q", args[0])
	}
}

func runMailDomainMutation(ctx context.Context, client *apiClient, action string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("mail domains "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm the mail-domain mutation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return fmt.Errorf("usage: hserverctl mail domains %s --confirm DOMAIN", action)
	}
	if !*confirmed {
		return fmt.Errorf("mail domain %s requires explicit --confirm", action)
	}
	domain, err := normalizeMailDomain(flags.Args()[0])
	if err != nil {
		return err
	}
	if domain == "" {
		return errors.New("mail domain must not be empty")
	}
	if action == "create" {
		return printMailJSON[mailMutationResponse](ctx, client, out, http.MethodPost, "/api/mail/domains", map[string]string{"domain": domain})
	}
	return printMailMutation(ctx, client, out, http.MethodDelete, "/api/mail/domains/"+url.PathEscape(domain))
}

func normalizeMailPathValue(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", label)
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must be valid UTF-8", label)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s must not contain control characters", label)
	}
	if len([]byte(value)) > 512 {
		return "", fmt.Errorf("%s must be at most 512 bytes", label)
	}
	return value, nil
}

func runMailAccounts(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	domain, err := parseMailListDomain("accounts", args)
	if err != nil {
		return err
	}
	endpoint := mailListEndpoint("accounts", domain)
	accounts, err := requestJSON[[]mailsvc.MailAccount](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	if accounts == nil {
		accounts = []mailsvc.MailAccount{}
	}
	for index := range accounts {
		if accounts[index].Aliases == nil {
			accounts[index].Aliases = []string{}
		}
	}
	return printJSONValue(out, accounts)
}

func runMailAliases(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	domain, err := parseMailListDomain("aliases", args)
	if err != nil {
		return err
	}
	endpoint := mailListEndpoint("aliases", domain)
	aliases, err := requestJSON[[]mailsvc.MailAlias](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	if aliases == nil {
		aliases = []mailsvc.MailAlias{}
	}
	for index := range aliases {
		if aliases[index].Destinations == nil {
			aliases[index].Destinations = []string{}
		}
	}
	return printJSONValue(out, aliases)
}

func parseMailListDomain(command string, args []string) (string, error) {
	flags := flag.NewFlagSet("mail "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	domain := flags.String("domain", "", "optional exact mail domain filter")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if len(flags.Args()) != 0 {
		return "", fmt.Errorf("usage: hserverctl mail %s [--domain DOMAIN]", command)
	}
	return normalizeMailDomain(*domain)
}

func normalizeMailDomain(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !utf8.ValidString(value) || len([]byte(value)) > maxMailDomainBytes {
		return "", errors.New("mail domain must be valid UTF-8 of at most 253 bytes")
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("mail domain must not contain control characters")
	}
	return value, nil
}

func mailListEndpoint(resource, domain string) string {
	endpoint := "/api/mail/" + resource
	if domain != "" {
		endpoint += "?domain=" + url.QueryEscape(domain)
	}
	return endpoint
}
