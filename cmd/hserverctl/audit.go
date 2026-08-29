package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var validCLIAuditServer = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type auditListOptions struct {
	Limit    int
	Offset   int
	Server   string
	User     string
	Action   string
	Resource string
	From     string
	To       string
}

func runAudit(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New("usage: hserverctl audit list [--limit 1-200] [--offset N] [--server local|NODE] [--user TEXT] [--action TEXT] [--resource NAME] [--from RFC3339] [--to RFC3339]")
	}
	flags := flag.NewFlagSet("audit list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := auditListOptions{}
	flags.IntVar(&options.Limit, "limit", 50, "maximum audit entries")
	flags.IntVar(&options.Offset, "offset", 0, "pagination offset")
	flags.StringVar(&options.Server, "server", "", "local host or managed-node audit scope")
	flags.StringVar(&options.User, "user", "", "case-insensitive user-name filter")
	flags.StringVar(&options.Action, "action", "", "case-insensitive action-name filter")
	flags.StringVar(&options.Resource, "resource", "", "exact resource filter")
	flags.StringVar(&options.From, "from", "", "inclusive RFC3339 lower bound")
	flags.StringVar(&options.To, "to", "", "inclusive RFC3339 upper bound")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("audit list does not accept positional arguments")
	}
	endpoint, err := buildAuditListPath(options)
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
}

func buildAuditListPath(options auditListOptions) (string, error) {
	if options.Limit < 1 || options.Limit > 200 {
		return "", errors.New("audit limit must be between 1 and 200")
	}
	if options.Offset < 0 {
		return "", errors.New("audit offset cannot be negative")
	}
	options.Server = strings.TrimSpace(options.Server)
	if options.Server != "" && options.Server != "local" && !validCLIAuditServer.MatchString(options.Server) {
		return "", errors.New("audit server must be local or a valid managed-node ID")
	}
	for label, value := range map[string]string{
		"user": options.User, "action": options.Action, "resource": options.Resource,
	} {
		if err := validateAuditFilterText(label, value); err != nil {
			return "", err
		}
	}

	from, err := parseOptionalAuditTime("from", options.From)
	if err != nil {
		return "", err
	}
	to, err := parseOptionalAuditTime("to", options.To)
	if err != nil {
		return "", err
	}
	if from != nil && to != nil && from.After(*to) {
		return "", errors.New("audit from time cannot be after to time")
	}

	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", options.Limit))
	query.Set("offset", fmt.Sprintf("%d", options.Offset))
	if options.Server != "" {
		query.Set("server", options.Server)
	}
	if value := strings.TrimSpace(options.User); value != "" {
		query.Set("user", value)
	}
	if value := strings.TrimSpace(options.Action); value != "" {
		query.Set("action_contains", value)
	}
	if value := strings.TrimSpace(options.Resource); value != "" {
		query.Set("resource", value)
	}
	if value := strings.TrimSpace(options.From); value != "" {
		query.Set("from", value)
	}
	if value := strings.TrimSpace(options.To); value != "" {
		query.Set("to", value)
	}
	return "/api/audit?" + query.Encode(), nil
}

func validateAuditFilterText(label, value string) error {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > 128 {
		return fmt.Errorf("audit %s filter must be at most 128 characters", label)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("audit %s filter cannot contain control characters", label)
		}
	}
	return nil
}

func parseOptionalAuditTime(label, value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("audit %s must be RFC3339: %w", label, err)
	}
	return &parsed, nil
}
