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
	"time"
	"unicode"
	"unicode/utf8"

	cfservice "github.com/IamYGT/heyserver/internal/services/cloudflare"
)

type cliCloudflareRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority int    `json:"priority,omitempty"`
}

type cloudflareRecordOptions struct {
	Confirm  bool
	Type     string
	Name     string
	Content  string
	TTL      int
	Proxied  string
	Priority int
	Wait     time.Duration
	visited  map[string]bool
}

func runCloudflare(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl cloudflare zones|zone|records|email-routing|record-create|record-update|record-proxy|record-delete|purge|mail-autofix")
	}
	switch args[0] {
	case "zones":
		if len(args) != 1 {
			return errors.New("usage: hserverctl cloudflare zones")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/cloudflare/zones", nil, true)
	case "zone":
		if len(args) != 2 {
			return errors.New("usage: hserverctl cloudflare zone ZONE_ID")
		}
		zoneID, err := validateCloudflareIdentifier("zone ID", args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, cloudflareZonePath(zoneID), nil, true)
	case "records":
		return runCloudflareRecords(ctx, client, args[1:], out)
	case "email-routing":
		if len(args) != 2 {
			return errors.New("usage: hserverctl cloudflare email-routing ZONE_ID")
		}
		zoneID, err := validateCloudflareIdentifier("zone ID", args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, cloudflareZonePath(zoneID)+"/email-routing", nil, true)
	case "record-create":
		return runCloudflareRecordCreate(ctx, client, args[1:], out)
	case "record-update":
		return runCloudflareRecordUpdate(ctx, client, args[1:], out)
	case "record-proxy":
		return runCloudflareRecordProxy(ctx, client, args[1:], out)
	case "record-delete":
		return runCloudflareRecordDelete(ctx, client, args[1:], out)
	case "purge":
		return runCloudflarePurge(ctx, client, args[1:], out)
	case "mail-autofix":
		return runCloudflareMailAutoFix(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown cloudflare command %q", args[0])
	}
}

func runCloudflareRecords(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cloudflare records", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	recordType := flags.String("type", "", "optional DNS record type")
	name := flags.String("name", "", "optional exact DNS record name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl cloudflare records [--type TYPE] [--name NAME] ZONE_ID")
	}
	zoneID, err := validateCloudflareIdentifier("zone ID", flags.Args()[0])
	if err != nil {
		return err
	}
	query := url.Values{}
	if strings.TrimSpace(*recordType) != "" {
		normalized, typeErr := validateCloudflareRecordType(*recordType)
		if typeErr != nil {
			return typeErr
		}
		query.Set("type", normalized)
	}
	if strings.TrimSpace(*name) != "" {
		normalized, nameErr := validateCloudflareText("record name", *name, 253, true)
		if nameErr != nil {
			return nameErr
		}
		query.Set("name", normalized)
	}
	endpoint := cloudflareZonePath(zoneID) + "/records"
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
}

func newCloudflareRecordFlags(name string) (*flag.FlagSet, *cloudflareRecordOptions) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := &cloudflareRecordOptions{TTL: 1, Wait: 30 * time.Second}
	flags.BoolVar(&options.Confirm, "confirm", false, "confirm Cloudflare DNS mutation")
	flags.StringVar(&options.Type, "type", "", "DNS record type")
	flags.StringVar(&options.Name, "name", "", "DNS record name")
	flags.StringVar(&options.Content, "content", "", "DNS record content")
	flags.IntVar(&options.TTL, "ttl", 1, "DNS TTL in seconds; 1 means automatic")
	flags.StringVar(&options.Proxied, "proxied", "", "true or false")
	flags.IntVar(&options.Priority, "priority", 0, "MX or SRV priority")
	flags.DurationVar(&options.Wait, "wait", 30*time.Second, "maximum mutation wait")
	return flags, options
}

func parseCloudflareRecordFlags(name string, args []string) (*cloudflareRecordOptions, []string, error) {
	flags, options := newCloudflareRecordFlags(name)
	if err := flags.Parse(args); err != nil {
		return nil, nil, err
	}
	options.visited = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { options.visited[item.Name] = true })
	return options, flags.Args(), nil
}

func runCloudflareRecordCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, positional, err := parseCloudflareRecordFlags("cloudflare record-create", args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: hserverctl cloudflare record-create --confirm --type TYPE --name NAME --content VALUE [OPTIONS] ZONE_ID")
	}
	if !options.Confirm {
		return errors.New("Cloudflare DNS record creation requires explicit --confirm")
	}
	if !options.visited["type"] || !options.visited["name"] || !options.visited["content"] {
		return errors.New("Cloudflare DNS record creation requires --type, --name, and --content")
	}
	zoneID, err := validateCloudflareIdentifier("zone ID", positional[0])
	if err != nil {
		return err
	}
	record, err := cloudflareRecordFromOptions(cliCloudflareRecord{TTL: 1}, options, true)
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPost, cloudflareZonePath(zoneID)+"/records", cloudflareRecordPayload(record), true)
}

func runCloudflareRecordUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, positional, err := parseCloudflareRecordFlags("cloudflare record-update", args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		return errors.New("usage: hserverctl cloudflare record-update --confirm [OPTIONS] ZONE_ID RECORD_ID")
	}
	if !options.Confirm {
		return errors.New("Cloudflare DNS record update requires explicit --confirm")
	}
	if !cloudflareRecordOptionsHaveUpdate(options) {
		return errors.New("Cloudflare DNS record update requires at least one changed field")
	}
	zoneID, err := validateCloudflareIdentifier("zone ID", positional[0])
	if err != nil {
		return err
	}
	recordID, err := validateCloudflareIdentifier("record ID", positional[1])
	if err != nil {
		return err
	}
	path := cloudflareRecordPath(zoneID, recordID)
	existing, err := getCloudflareRecord(ctx, client, zoneID, recordID)
	if err != nil {
		return err
	}
	record, err := cloudflareRecordFromOptions(existing, options, false)
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPut, path, cloudflareRecordPayload(record), true)
}

func runCloudflareRecordProxy(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cloudflare record-proxy", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm Cloudflare proxy mutation")
	proxiedValue := flags.String("proxied", "", "true or false")
	wait := flags.Duration("wait", 30*time.Second, "maximum mutation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return errors.New("usage: hserverctl cloudflare record-proxy --confirm --proxied true|false [--wait DURATION] ZONE_ID RECORD_ID")
	}
	if !*confirmed {
		return errors.New("Cloudflare DNS proxy update requires explicit --confirm")
	}
	visited := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "proxied" {
			visited = true
		}
	})
	if !visited {
		return errors.New("Cloudflare DNS proxy update requires --proxied true|false")
	}
	proxied, err := parseNotifyBool("proxied", *proxiedValue)
	if err != nil {
		return err
	}
	zoneID, recordID, err := validateCloudflareRecordPath(flags.Args())
	if err != nil {
		return err
	}
	if *wait <= 0 {
		return errors.New("Cloudflare mutation wait must be greater than zero")
	}
	record, err := getCloudflareRecord(ctx, client, zoneID, recordID)
	if err != nil {
		return err
	}
	if record.Type != "A" && record.Type != "AAAA" && record.Type != "CNAME" {
		return errors.New("Cloudflare proxy can be changed only for A, AAAA, or CNAME records")
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, cloudflareRecordPath(zoneID, recordID)+"/proxy", map[string]bool{"proxied": proxied}, true)
}

func runCloudflareRecordDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cloudflare record-delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm Cloudflare DNS deletion")
	wait := flags.Duration("wait", 30*time.Second, "maximum mutation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return errors.New("usage: hserverctl cloudflare record-delete --confirm [--wait DURATION] ZONE_ID RECORD_ID")
	}
	if !*confirmed {
		return errors.New("Cloudflare DNS record deletion requires explicit --confirm")
	}
	zoneID, recordID, err := validateCloudflareRecordPath(flags.Args())
	if err != nil {
		return err
	}
	if *wait <= 0 {
		return errors.New("Cloudflare mutation wait must be greater than zero")
	}
	if _, err := client.withTimeout(*wait).request(ctx, http.MethodDelete, cloudflareRecordPath(zoneID, recordID), nil, true); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, `{"status":"deleted"}`)
	return err
}

func runCloudflarePurge(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cloudflare purge", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm complete zone cache purge")
	wait := flags.Duration("wait", 30*time.Second, "maximum mutation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl cloudflare purge --confirm [--wait DURATION] ZONE_ID")
	}
	if !*confirmed {
		return errors.New("Cloudflare complete cache purge requires explicit --confirm")
	}
	zoneID, err := validateCloudflareIdentifier("zone ID", flags.Args()[0])
	if err != nil {
		return err
	}
	if *wait <= 0 {
		return errors.New("Cloudflare mutation wait must be greater than zero")
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, cloudflareZonePath(zoneID)+"/purge", nil, true)
}

func runCloudflareMailAutoFix(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("cloudflare mail-autofix", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm installation-owned mail DNS reconciliation")
	wait := flags.Duration("wait", 2*time.Minute, "maximum reconciliation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl cloudflare mail-autofix --confirm [--wait DURATION] DOMAIN")
	}
	if !*confirmed {
		return errors.New("Cloudflare mail DNS reconciliation requires explicit --confirm")
	}
	domain, err := validateCloudflareDomain(flags.Args()[0])
	if err != nil {
		return err
	}
	if *wait <= 0 {
		return errors.New("Cloudflare mutation wait must be greater than zero")
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/cloudflare/mail-autofix/"+url.PathEscape(domain), nil, true)
}

func cloudflareRecordFromOptions(record cliCloudflareRecord, options *cloudflareRecordOptions, create bool) (cliCloudflareRecord, error) {
	if options.Wait <= 0 {
		return record, errors.New("Cloudflare mutation wait must be greater than zero")
	}
	if options.visited["type"] {
		record.Type = strings.ToUpper(strings.TrimSpace(options.Type))
	}
	if options.visited["name"] {
		record.Name = strings.TrimSpace(options.Name)
	}
	if options.visited["content"] {
		record.Content = strings.TrimSpace(options.Content)
	}
	if options.visited["ttl"] || create {
		record.TTL = options.TTL
	}
	if options.visited["proxied"] {
		proxied, err := parseNotifyBool("proxied", options.Proxied)
		if err != nil {
			return record, err
		}
		record.Proxied = proxied
	}
	if options.visited["priority"] {
		record.Priority = options.Priority
	}
	normalized, err := cfservice.ValidateAndNormalizeRecordRequest(cfservice.CreateRecordRequest{
		Type: record.Type, Name: record.Name, Content: record.Content, TTL: record.TTL,
		Proxied: record.Proxied, Priority: record.Priority,
	})
	if err != nil {
		return record, fmt.Errorf("invalid Cloudflare DNS record: %w", err)
	}
	record.Type = normalized.Type
	record.Name = normalized.Name
	record.Content = normalized.Content
	record.TTL = normalized.TTL
	record.Proxied = normalized.Proxied
	record.Priority = normalized.Priority
	return record, nil
}

func cloudflareRecordPayload(record cliCloudflareRecord) map[string]any {
	return map[string]any{
		"type": record.Type, "name": record.Name, "content": record.Content,
		"ttl": record.TTL, "proxied": record.Proxied, "priority": record.Priority,
	}
}

func cloudflareRecordOptionsHaveUpdate(options *cloudflareRecordOptions) bool {
	for name := range options.visited {
		switch name {
		case "type", "name", "content", "ttl", "proxied", "priority":
			return true
		}
	}
	return false
}

func getCloudflareRecord(ctx context.Context, client *apiClient, zoneID, recordID string) (cliCloudflareRecord, error) {
	records, err := requestJSON[[]cliCloudflareRecord](ctx, client, http.MethodGet, cloudflareZonePath(zoneID)+"/records", nil, true)
	if err != nil {
		return cliCloudflareRecord{}, err
	}
	for _, record := range records {
		if record.ID == recordID {
			record.Type = strings.ToUpper(strings.TrimSpace(record.Type))
			return record, nil
		}
	}
	return cliCloudflareRecord{}, fmt.Errorf("Cloudflare DNS record %q was not found in zone %q", recordID, zoneID)
}

func validateCloudflareRecordType(value string) (string, error) {
	normalized, err := cfservice.NormalizeRecordType(value)
	if err != nil {
		return "", fmt.Errorf("invalid Cloudflare DNS record type: %w", err)
	}
	return normalized, nil
}

func validateCloudflareText(name, value string, maximum int, required bool) (string, error) {
	trimmed := strings.TrimSpace(value)
	if required && trimmed == "" {
		return "", fmt.Errorf("Cloudflare %s is required", name)
	}
	if !utf8.ValidString(trimmed) || utf8.RuneCountInString(trimmed) > maximum {
		return "", fmt.Errorf("Cloudflare %s must contain at most %d valid UTF-8 characters", name, maximum)
	}
	for _, character := range trimmed {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("Cloudflare %s must not contain control characters", name)
		}
	}
	return trimmed, nil
}

func validateCloudflareIdentifier(name, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > 128 || trimmed != value {
		return "", fmt.Errorf("Cloudflare %s must contain 1 to 128 unpadded identifier characters", name)
	}
	for _, character := range trimmed {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_') {
			return "", fmt.Errorf("Cloudflare %s contains an unsupported character", name)
		}
	}
	return trimmed, nil
}

func validateCloudflareDomain(value string) (string, error) {
	normalized, err := cfservice.NormalizeDomain(value)
	if err != nil {
		return "", fmt.Errorf("invalid Cloudflare domain: %w", err)
	}
	return normalized, nil
}

func validateCloudflareRecordPath(values []string) (string, string, error) {
	zoneID, err := validateCloudflareIdentifier("zone ID", values[0])
	if err != nil {
		return "", "", err
	}
	recordID, err := validateCloudflareIdentifier("record ID", values[1])
	if err != nil {
		return "", "", err
	}
	return zoneID, recordID, nil
}

func cloudflareZonePath(zoneID string) string {
	return "/api/cloudflare/zones/" + url.PathEscape(zoneID)
}

func cloudflareRecordPath(zoneID, recordID string) string {
	return cloudflareZonePath(zoneID) + "/records/" + url.PathEscape(recordID)
}
