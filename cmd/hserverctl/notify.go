package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/IamYGT/heyserver/internal/alertrule"
	"github.com/IamYGT/heyserver/internal/models"
)

type cliNotificationChannel struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Config  string `json:"config"`
	Enabled bool   `json:"enabled"`
}

type notifyMutationOptions struct {
	Confirm         bool
	Name            string
	ChannelType     string
	CredentialFile  string
	ClearCredential bool
	Enabled         string
	SMTPHost        string
	SMTPPort        int
	SMTPUser        string
	SMTPFrom        string
	SMTPTo          string
	SMTPTLS         string
	ChatID          string
	Username        string
	Channel         string
	visited         map[string]bool
}

type notifyRuleMutationOptions struct {
	Confirm      bool
	Name         string
	RuleType     string
	Threshold    float64
	DurationMins int
	CooldownMins int
	Target       string
	Enabled      string
	visited      map[string]bool
}

func runNotify(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl notify channels|channel|create|update|test|delete|rules|rule|rule-create|rule-update|rule-delete|history")
	}
	switch args[0] {
	case "channels":
		if len(args) != 1 {
			return errors.New("usage: hserverctl notify channels")
		}
		return printRequest(ctx, client, out, "GET", "/api/notify/channels", nil, true)
	case "channel":
		if len(args) != 2 {
			return errors.New("usage: hserverctl notify channel ID")
		}
		id, err := positiveNotifyID(args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, "GET", notifyChannelPath(id), nil, true)
	case "create":
		return runNotifyCreate(ctx, client, args[1:], out)
	case "update":
		return runNotifyUpdate(ctx, client, args[1:], out)
	case "test":
		return runNotifyAction(ctx, client, "test", args[1:], out)
	case "delete":
		return runNotifyAction(ctx, client, "delete", args[1:], out)
	case "rules":
		if len(args) != 1 {
			return errors.New("usage: hserverctl notify rules")
		}
		return printRequest(ctx, client, out, "GET", "/api/notify/rules", nil, true)
	case "rule":
		if len(args) != 2 {
			return errors.New("usage: hserverctl notify rule ID")
		}
		id, err := positiveNotifyRuleID(args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, "GET", notifyRulePath(id), nil, true)
	case "rule-create":
		return runNotifyRuleCreate(ctx, client, args[1:], out)
	case "rule-update":
		return runNotifyRuleUpdate(ctx, client, args[1:], out)
	case "rule-delete":
		return runNotifyRuleDelete(ctx, client, args[1:], out)
	case "history":
		return runNotifyHistory(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown notify command %q", args[0])
	}
}

func newNotifyRuleMutationFlags(name string) (*flag.FlagSet, *notifyRuleMutationOptions) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := &notifyRuleMutationOptions{}
	flags.BoolVar(&options.Confirm, "confirm", false, "confirm alert rule mutation")
	flags.StringVar(&options.Name, "name", "", "alert rule name")
	flags.StringVar(&options.RuleType, "type", "", "alert rule type")
	flags.Float64Var(&options.Threshold, "threshold", 0, "alert threshold")
	flags.IntVar(&options.DurationMins, "duration-mins", 0, "continuous breach duration in minutes")
	flags.IntVar(&options.CooldownMins, "cooldown-mins", 15, "minimum minutes between alerts")
	flags.StringVar(&options.Target, "target", "", "mount path, certificate domain, or systemd unit")
	flags.StringVar(&options.Enabled, "enabled", "", "true or false")
	return flags, options
}

func parseNotifyRuleMutationFlags(name string, args []string) (*notifyRuleMutationOptions, []string, error) {
	flags, options := newNotifyRuleMutationFlags(name)
	if err := flags.Parse(args); err != nil {
		return nil, nil, err
	}
	options.visited = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { options.visited[item.Name] = true })
	return options, flags.Args(), nil
}

func runNotifyRuleCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, positional, err := parseNotifyRuleMutationFlags("notify rule-create", args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return errors.New("usage: hserverctl notify rule-create --confirm --name NAME --type TYPE --threshold VALUE [OPTIONS]")
	}
	if !options.Confirm {
		return errors.New("alert rule creation requires explicit --confirm")
	}
	if err := validateNotifyRuleOptionSyntax(options); err != nil {
		return err
	}
	if !options.visited["name"] || !options.visited["type"] {
		return errors.New("alert rule creation requires --name and --type")
	}
	ruleType := alertrule.NormalizeAlertType(options.RuleType)
	if ruleType != models.AlertServiceDown && !options.visited["threshold"] {
		return errors.New("alert rule creation requires --threshold for this type")
	}
	rule := models.AlertRule{
		Name:         options.Name,
		Type:         ruleType,
		Threshold:    options.Threshold,
		DurationMins: options.DurationMins,
		Target:       options.Target,
		Enabled:      true,
		CooldownMins: options.CooldownMins,
	}
	if options.visited["enabled"] {
		rule.Enabled, err = parseNotifyBool("enabled", options.Enabled)
		if err != nil {
			return err
		}
	}
	normalized, err := alertrule.ValidateAndNormalizeAlertRule(rule)
	if err != nil {
		return fmt.Errorf("invalid alert rule: %w", err)
	}
	return printRequest(ctx, client, out, "POST", "/api/notify/rules", notifyRulePayload(normalized), true)
}

func runNotifyRuleUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, positional, err := parseNotifyRuleMutationFlags("notify rule-update", args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: hserverctl notify rule-update --confirm [OPTIONS] ID")
	}
	if !options.Confirm {
		return errors.New("alert rule update requires explicit --confirm")
	}
	if err := validateNotifyRuleOptionSyntax(options); err != nil {
		return err
	}
	if !notifyRuleOptionsHaveUpdate(options) {
		return errors.New("alert rule update requires at least one changed field")
	}
	id, err := positiveNotifyRuleID(positional[0])
	if err != nil {
		return err
	}
	existing, err := requestJSON[models.AlertRule](ctx, client, "GET", notifyRulePath(id), nil, true)
	if err != nil {
		return err
	}
	applyNotifyRuleOptions(&existing, options)
	if options.visited["enabled"] {
		existing.Enabled, err = parseNotifyBool("enabled", options.Enabled)
		if err != nil {
			return err
		}
	}
	normalized, err := alertrule.ValidateAndNormalizeAlertRule(existing)
	if err != nil {
		return fmt.Errorf("invalid alert rule update: %w", err)
	}
	payload := notifyRulePartialPayload(normalized, options.visited)
	return printRequest(ctx, client, out, "PUT", notifyRulePath(id), payload, true)
}

func runNotifyRuleDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("notify rule-delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm alert rule deletion")
	wait := flags.Duration("wait", 30*time.Second, "maximum alert rule deletion wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl notify rule-delete --confirm [--wait DURATION] ID")
	}
	if !*confirmed {
		return errors.New("alert rule deletion requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("alert rule deletion wait must be greater than zero")
	}
	id, err := positiveNotifyRuleID(flags.Args()[0])
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, "DELETE", notifyRulePath(id), nil, true)
}

func runNotifyHistory(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("notify history", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	limitValue := flags.String("limit", "50", "history page size from 1 to 200")
	offsetValue := flags.String("offset", "0", "non-negative history offset")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl notify history [--limit 1-200] [--offset N]")
	}
	limit, err := canonicalNotifyInteger("history limit", *limitValue, 1, 200)
	if err != nil {
		return err
	}
	offset, err := canonicalNotifyInteger("history offset", *offsetValue, 0, int(^uint(0)>>1))
	if err != nil {
		return err
	}
	endpoint := "/api/notify/history?limit=" + strconv.Itoa(limit) + "&offset=" + strconv.Itoa(offset)
	return printRequest(ctx, client, out, "GET", endpoint, nil, true)
}

func applyNotifyRuleOptions(rule *models.AlertRule, options *notifyRuleMutationOptions) {
	if options.visited["name"] {
		rule.Name = options.Name
	}
	if options.visited["type"] {
		rule.Type = alertrule.NormalizeAlertType(options.RuleType)
	}
	if options.visited["threshold"] {
		rule.Threshold = options.Threshold
	}
	if options.visited["duration-mins"] {
		rule.DurationMins = options.DurationMins
	}
	if options.visited["cooldown-mins"] {
		rule.CooldownMins = options.CooldownMins
	}
	if options.visited["target"] {
		rule.Target = options.Target
	}
}

func notifyRulePayload(rule models.AlertRule) map[string]any {
	return map[string]any{
		"name": rule.Name, "type": rule.Type, "threshold": rule.Threshold,
		"durationMins": rule.DurationMins, "target": rule.Target,
		"enabled": rule.Enabled, "cooldownMins": rule.CooldownMins,
	}
}

func notifyRulePartialPayload(rule models.AlertRule, visited map[string]bool) map[string]any {
	payload := make(map[string]any)
	if visited["name"] {
		payload["name"] = rule.Name
	}
	if visited["type"] {
		payload["type"] = rule.Type
	}
	if visited["threshold"] {
		payload["threshold"] = rule.Threshold
	}
	if visited["duration-mins"] {
		payload["durationMins"] = rule.DurationMins
	}
	if visited["cooldown-mins"] {
		payload["cooldownMins"] = rule.CooldownMins
	}
	if visited["target"] {
		payload["target"] = rule.Target
	}
	if visited["enabled"] {
		payload["enabled"] = rule.Enabled
	}
	return payload
}

func notifyRuleOptionsHaveUpdate(options *notifyRuleMutationOptions) bool {
	for name := range options.visited {
		if name != "confirm" {
			return true
		}
	}
	return false
}

func validateNotifyRuleOptionSyntax(options *notifyRuleMutationOptions) error {
	if options.visited["name"] {
		if _, err := validateNotifyText("alert rule name", options.Name, 128, true); err != nil {
			return err
		}
	}
	if options.visited["type"] {
		switch alertrule.NormalizeAlertType(options.RuleType) {
		case models.AlertCPUUsage, models.AlertMemoryUsage, models.AlertDiskUsage,
			models.AlertSSLExpiry, models.AlertServiceDown, models.AlertFailedLogins:
		default:
			return errors.New("alert rule type must be cpu_usage, memory_usage, disk_usage, ssl_expiry, service_down, or failed_logins")
		}
	}
	if options.visited["threshold"] && (math.IsNaN(options.Threshold) || math.IsInf(options.Threshold, 0)) {
		return errors.New("alert rule threshold must be finite")
	}
	if options.visited["duration-mins"] && (options.DurationMins < 0 || options.DurationMins > 1440) {
		return errors.New("alert rule duration must be between 0 and 1440 minutes")
	}
	if options.visited["cooldown-mins"] && (options.CooldownMins < 1 || options.CooldownMins > 10080) {
		return errors.New("alert rule cooldown must be between 1 and 10080 minutes")
	}
	if options.visited["target"] {
		if _, err := validateNotifyText("alert rule target", options.Target, 4096, false); err != nil {
			return err
		}
	}
	if options.visited["enabled"] {
		if _, err := parseNotifyBool("enabled", options.Enabled); err != nil {
			return err
		}
	}
	return nil
}

func positiveNotifyRuleID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, errors.New("alert rule ID must be a positive canonical integer")
	}
	return id, nil
}

func notifyRulePath(id int64) string {
	return "/api/notify/rules/" + strconv.FormatInt(id, 10)
}

func canonicalNotifyInteger(name, value string, minimum, maximum int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum || strconv.Itoa(parsed) != value {
		return 0, fmt.Errorf("%s must be a canonical integer between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
}

func newNotifyMutationFlags(name string) (*flag.FlagSet, *notifyMutationOptions) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := &notifyMutationOptions{}
	flags.BoolVar(&options.Confirm, "confirm", false, "confirm notification channel mutation")
	flags.StringVar(&options.Name, "name", "", "channel display name")
	flags.StringVar(&options.ChannelType, "type", "", "email, telegram, discord, or slack")
	flags.StringVar(&options.CredentialFile, "credential-file", "", "protected password, token, or webhook file")
	flags.BoolVar(&options.ClearCredential, "clear-credential", false, "remove the stored credential")
	flags.StringVar(&options.Enabled, "enabled", "", "true or false")
	flags.StringVar(&options.SMTPHost, "smtp-host", "", "SMTP host")
	flags.IntVar(&options.SMTPPort, "smtp-port", 0, "SMTP port")
	flags.StringVar(&options.SMTPUser, "smtp-user", "", "SMTP username")
	flags.StringVar(&options.SMTPFrom, "from", "", "SMTP from address")
	flags.StringVar(&options.SMTPTo, "to", "", "comma-separated SMTP recipients")
	flags.StringVar(&options.SMTPTLS, "smtp-tls", "", "true for implicit TLS, false otherwise")
	flags.StringVar(&options.ChatID, "chat-id", "", "Telegram chat ID")
	flags.StringVar(&options.Username, "username", "", "Discord or Slack display username")
	flags.StringVar(&options.Channel, "channel", "", "Slack channel override")
	return flags, options
}

func parseNotifyMutationFlags(name string, args []string) (*notifyMutationOptions, []string, error) {
	flags, options := newNotifyMutationFlags(name)
	if err := flags.Parse(args); err != nil {
		return nil, nil, err
	}
	options.visited = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { options.visited[item.Name] = true })
	return options, flags.Args(), nil
}

func runNotifyCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, positional, err := parseNotifyMutationFlags("notify create", args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return errors.New("usage: hserverctl notify create --confirm --name NAME --type TYPE [OPTIONS]")
	}
	if !options.Confirm {
		return errors.New("notification channel creation requires explicit --confirm")
	}
	if options.ClearCredential {
		return errors.New("--clear-credential is only valid when updating a notification channel")
	}
	name, err := validateNotifyText("channel name", options.Name, 128, true)
	if err != nil {
		return err
	}
	channelType, err := validateNotifyType(options.ChannelType)
	if err != nil {
		return err
	}
	config, err := buildNotifyConfig(channelType, options, true)
	if err != nil {
		return err
	}
	enabled := true
	if options.visited["enabled"] {
		enabled, err = parseNotifyBool("enabled", options.Enabled)
		if err != nil {
			return err
		}
	}
	payload := map[string]any{
		"name": name, "type": channelType, "config": config, "enabled": enabled,
	}
	return printRequest(ctx, client, out, "POST", "/api/notify/channels", payload, true)
}

func runNotifyUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, positional, err := parseNotifyMutationFlags("notify update", args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: hserverctl notify update --confirm [OPTIONS] ID")
	}
	if !options.Confirm {
		return errors.New("notification channel update requires explicit --confirm")
	}
	if options.visited["type"] {
		return errors.New("notification channel type cannot be changed")
	}
	if options.ClearCredential && options.visited["credential-file"] {
		return errors.New("--credential-file and --clear-credential cannot be combined")
	}
	if !notifyOptionsHaveUpdate(options) {
		return errors.New("notification channel update requires at least one changed field")
	}
	id, err := positiveNotifyID(positional[0])
	if err != nil {
		return err
	}
	existing, err := requestJSON[cliNotificationChannel](ctx, client, "GET", notifyChannelPath(id), nil, true)
	if err != nil {
		return err
	}
	channelType, err := validateNotifyType(existing.Type)
	if err != nil {
		return fmt.Errorf("server returned an invalid notification channel type: %w", err)
	}
	name := existing.Name
	if options.visited["name"] {
		name, err = validateNotifyText("channel name", options.Name, 128, true)
		if err != nil {
			return err
		}
	}
	config, err := buildNotifyConfig(channelType, options, false)
	if err != nil {
		return err
	}
	payload := map[string]any{"name": name, "type": channelType, "config": config}
	if options.visited["enabled"] {
		enabled, parseErr := parseNotifyBool("enabled", options.Enabled)
		if parseErr != nil {
			return parseErr
		}
		payload["enabled"] = enabled
	}
	if options.ClearCredential {
		payload["clearSecret"] = true
	}
	return printRequest(ctx, client, out, "PUT", notifyChannelPath(id), payload, true)
}

func runNotifyAction(ctx context.Context, client *apiClient, action string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("notify "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm notification action")
	wait := flags.Duration("wait", 30*time.Second, "maximum notification action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return fmt.Errorf("usage: hserverctl notify %s --confirm [--wait DURATION] ID", action)
	}
	if !*confirmed {
		return fmt.Errorf("notification channel %s requires explicit --confirm", action)
	}
	if *wait <= 0 {
		return errors.New("notification action wait must be greater than zero")
	}
	id, err := positiveNotifyID(flags.Args()[0])
	if err != nil {
		return err
	}
	endpoint := notifyChannelPath(id)
	method := "DELETE"
	if action == "test" {
		endpoint += "/test"
		method = "POST"
	}
	return printRequest(ctx, client.withTimeout(*wait), out, method, endpoint, nil, true)
}

func buildNotifyConfig(channelType string, options *notifyMutationOptions, create bool) (string, error) {
	allowed := map[string]bool{"name": true, "confirm": true, "credential-file": true, "enabled": true}
	if create {
		allowed["type"] = true
	}
	if !create {
		allowed["clear-credential"] = true
	}
	config := make(map[string]any)
	switch channelType {
	case models.ChannelEmail:
		for _, name := range []string{"smtp-host", "smtp-port", "smtp-user", "from", "to", "smtp-tls"} {
			allowed[name] = true
		}
		if create || options.visited["smtp-host"] {
			value, err := validateNotifyText("SMTP host", options.SMTPHost, 253, true)
			if err != nil {
				return "", err
			}
			if strings.ContainsAny(value, " /@") {
				return "", errors.New("SMTP host must be a hostname or IP address without spaces")
			}
			config["host"] = value
		}
		if create || options.visited["smtp-port"] {
			if options.SMTPPort < 1 || options.SMTPPort > 65535 {
				return "", errors.New("SMTP port must be between 1 and 65535")
			}
			config["port"] = options.SMTPPort
		}
		if options.visited["smtp-user"] {
			value, err := validateNotifyText("SMTP username", options.SMTPUser, 320, false)
			if err != nil {
				return "", err
			}
			config["username"] = value
		}
		if create || options.visited["from"] {
			value, err := validateNotifyText("SMTP from address", options.SMTPFrom, 320, true)
			if err != nil {
				return "", err
			}
			config["from"] = value
		}
		if create || options.visited["to"] {
			value, err := validateNotifyText("SMTP recipient list", options.SMTPTo, 2048, true)
			if err != nil {
				return "", err
			}
			config["to"] = value
		}
		if create || options.visited["smtp-tls"] {
			value, err := parseNotifyBool("smtp-tls", options.SMTPTLS)
			if err != nil {
				return "", err
			}
			config["tls"] = value
		}
	case models.ChannelTelegram:
		allowed["chat-id"] = true
		if create || options.visited["chat-id"] {
			chatID, err := strconv.ParseInt(strings.TrimSpace(options.ChatID), 10, 64)
			if err != nil || chatID == 0 {
				return "", errors.New("Telegram chat ID must be a non-zero integer")
			}
			config["chatId"] = chatID
		}
	case models.ChannelDiscord:
		allowed["username"] = true
		if options.visited["username"] {
			value, err := validateNotifyText("Discord username", options.Username, 80, false)
			if err != nil {
				return "", err
			}
			config["username"] = value
		}
	case models.ChannelSlack:
		allowed["username"] = true
		allowed["channel"] = true
		if options.visited["username"] {
			value, err := validateNotifyText("Slack username", options.Username, 80, false)
			if err != nil {
				return "", err
			}
			config["username"] = value
		}
		if options.visited["channel"] {
			value, err := validateNotifyText("Slack channel", options.Channel, 80, false)
			if err != nil {
				return "", err
			}
			config["channel"] = value
		}
	default:
		return "", fmt.Errorf("unsupported notification channel type %q", channelType)
	}
	for name := range options.visited {
		if !allowed[name] {
			return "", fmt.Errorf("--%s is not valid for a %s notification channel", name, channelType)
		}
	}
	if options.visited["credential-file"] {
		credential, err := readSecretFile(options.CredentialFile, 64<<10)
		if err != nil {
			return "", fmt.Errorf("read notification credential file: %w", err)
		}
		if strings.ContainsAny(credential, "\r\n\x00") {
			return "", errors.New("notification credential file must contain exactly one control-free value")
		}
		switch channelType {
		case models.ChannelEmail:
			config["password"] = credential
		case models.ChannelTelegram:
			if strings.ContainsAny(credential, " \t") {
				return "", errors.New("Telegram bot token cannot contain whitespace")
			}
			config["botToken"] = credential
		case models.ChannelDiscord, models.ChannelSlack:
			if err := validateNotifyWebhookURL(credential); err != nil {
				return "", err
			}
			config["webhookUrl"] = credential
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func notifyOptionsHaveUpdate(options *notifyMutationOptions) bool {
	for name := range options.visited {
		switch name {
		case "confirm":
		default:
			return true
		}
	}
	return false
}

func positiveNotifyID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, errors.New("notification channel ID must be a positive canonical integer")
	}
	return id, nil
}

func notifyChannelPath(id int64) string {
	return "/api/notify/channels/" + strconv.FormatInt(id, 10)
}

func validateNotifyType(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case models.ChannelEmail, models.ChannelTelegram, models.ChannelDiscord, models.ChannelSlack:
		return value, nil
	default:
		return "", errors.New("notification channel type must be email, telegram, discord, or slack")
	}
}

func parseNotifyBool(name, value string) (bool, error) {
	switch strings.TrimSpace(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func validateNotifyText(name, value string, maximum int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if utf8.RuneCountInString(value) > maximum {
		return "", fmt.Errorf("%s must be at most %d characters", name, maximum)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("%s cannot contain control characters", name)
		}
	}
	return value, nil
}

func validateNotifyWebhookURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("notification webhook credential must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return errors.New("notification webhook credential cannot contain user info or a fragment")
	}
	return nil
}
