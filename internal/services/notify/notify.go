package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gopkg.in/gomail.v2"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/security"
)

type Channel interface {
	Send(subject, body string) error
}

type emailChannel struct{ cfg models.EmailConfig }

func NewEmailChannel(cfg models.EmailConfig) Channel { return &emailChannel{cfg: cfg} }
func (c *emailChannel) Send(subject, body string) error {
	m := gomail.NewMessage()
	m.SetHeader("From", c.cfg.From)
	to := splitTrim(c.cfg.To, ",")
	if len(to) == 0 {
		return fmt.Errorf("email: no recipients")
	}
	m.SetHeader("To", to...)
	m.SetHeader("Subject", subject)
	m.SetBody("text/plain", body)
	d := gomail.NewDialer(c.cfg.Host, c.cfg.Port, c.cfg.Username, c.cfg.Password)
	d.SSL = c.cfg.TLS
	if !c.cfg.TLS {
		d.TLSConfig = nil
	}
	return d.DialAndSend(m)
}

type telegramChannel struct{ cfg models.TelegramConfig }

func NewTelegramChannel(cfg models.TelegramConfig) Channel { return &telegramChannel{cfg: cfg} }
func (c *telegramChannel) Send(subject, body string) error {
	bot, err := tgbotapi.NewBotAPI(c.cfg.BotToken)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	msg := tgbotapi.NewMessage(c.cfg.ChatID, "*"+escapeMarkdownV2(subject)+"*\n\n"+escapeMarkdownV2(body))
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	_, err = bot.Send(msg)
	return err
}
func escapeMarkdownV2(s string) string {
	reserved := []rune{'_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!', '\\'}
	var b strings.Builder
	for _, r := range s {
		for _, res := range reserved {
			if r == res {
				b.WriteRune('\\')
				break
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

type discordChannel struct {
	cfg    models.DiscordConfig
	client *http.Client
}

func NewDiscordChannel(cfg models.DiscordConfig) Channel {
	return &discordChannel{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}
}
func (c *discordChannel) Send(subject, body string) error {
	if err := security.ValidateWebhookURL(c.cfg.WebhookURL); err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	type embed struct {
		Title, Description, Timestamp string
		Color                         int
	}
	type payload struct {
		Username string  `json:"username,omitempty"`
		Embeds   []embed `json:"embeds"`
	}
	raw, _ := json.Marshal(payload{Username: c.cfg.Username, Embeds: []embed{{Title: subject, Description: body, Color: 0xFF4444, Timestamp: time.Now().UTC().Format(time.RFC3339)}}})
	resp, err := c.client.Post(c.cfg.WebhookURL, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord: status %d", resp.StatusCode)
	}
	return nil
}

type slackChannel struct {
	cfg    models.SlackConfig
	client *http.Client
}

func NewSlackChannel(cfg models.SlackConfig) Channel {
	return &slackChannel{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}
}
func (c *slackChannel) Send(subject, body string) error {
	if err := security.ValidateWebhookURL(c.cfg.WebhookURL); err != nil {
		return fmt.Errorf("slack: %w", err)
	}
	p := struct {
		Text      string `json:"text"`
		Username  string `json:"username,omitempty"`
		Channel   string `json:"channel,omitempty"`
		IconEmoji string `json:"icon_emoji,omitempty"`
	}{Text: "*" + subject + "*\n" + body, Username: c.cfg.Username, Channel: c.cfg.Channel, IconEmoji: ":warning:"}
	raw, _ := json.Marshal(p)
	resp, err := c.client.Post(c.cfg.WebhookURL, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack: status %d", resp.StatusCode)
	}
	return nil
}

type Dispatcher struct{ channels []models.NotificationChannel }

func NewDispatcher(channels []models.NotificationChannel) *Dispatcher {
	return &Dispatcher{channels: channels}
}
func (d *Dispatcher) Send(subject, body string) error {
	_, err := d.SendWithResults(subject, body)
	return err
}

// SendWithResults sends to every enabled channel and returns one bounded
// result for each attempted destination. Build failures are attempts too: the
// caller can persist their failure without retaining provider errors.
func (d *Dispatcher) SendWithResults(subject, body string) ([]DeliveryResult, error) {
	var results []DeliveryResult
	var errs []string
	for _, ch := range d.channels {
		if !ch.Enabled {
			continue
		}
		result := DeliveryResult{
			ChannelID:             ch.ID,
			ChannelConfigRevision: ch.ConfigRevision,
			Outcome:               models.NotificationDeliveryOutcomeFailure,
		}
		t, err := buildChannel(ch)
		if err != nil {
			results = append(results, result)
			errs = append(errs, fmt.Sprintf("[%s] build: %v", ch.Name, err))
			continue
		}
		if err := t.Send(subject, body); err != nil {
			results = append(results, result)
			errs = append(errs, fmt.Sprintf("[%s] send: %v", ch.Name, err))
			continue
		}
		result.Outcome = models.NotificationDeliveryOutcomeSuccess
		results = append(results, result)
	}
	if len(errs) > 0 {
		return results, fmt.Errorf("notify errors: %s", strings.Join(errs, "; "))
	}
	return results, nil
}

// AvailabilityStatus is the notification delivery availability reported by
// the API. State is deliberately limited to the shared optional-integration
// wire contract; Detail carries aggregate/UI-only distinctions such as
// configured_disabled and degraded without promoting them to wire states.
type AvailabilityStatus struct {
	State  integrationstate.State
	Detail string
}

const (
	notificationDetailNotConfigured      = "not_configured"
	notificationDetailConfigUnavailable  = "config_unavailable"
	notificationDetailConfiguredDisabled = "configured_disabled"
	notificationDetailDegraded           = "degraded"
	notificationDetailProbeUnverified    = "probe_unverified"
)

// ChannelAvailability derives an honest state from the currently observed
// channel only. Without a persisted receipt it intentionally remains
// unavailable even when configuration is present.
func ChannelAvailability(channel models.NotificationChannel) AvailabilityStatus {
	return ChannelAvailabilityWithReceipt(channel, nil, time.Now().UTC())
}

const notificationReceiptFreshness = 7 * 24 * time.Hour

const (
	notificationDetailDeliveryConfirmed = "delivery_confirmed"
	notificationDetailDeliveryFailed    = "delivery_failed"
	notificationDetailDeliveryStale     = "delivery_stale"
)

// ChannelAvailabilityWithReceipt reports channel health only when the latest
// persisted delivery observation belongs to this channel configuration and is
// still fresh. Configuration, disabled, and protected-config errors retain the
// pre-receipt semantics.
func ChannelAvailabilityWithReceipt(channel models.NotificationChannel, receipt *models.NotificationDeliveryReceipt, now time.Time) AvailabilityStatus {
	normalized, err := NormalizeChannelConfig(channel.Type, channel.Config, `{}`)
	if err != nil {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailConfigUnavailable}
	}
	configured, err := channelConfigPresent(channel.Type, normalized)
	if err != nil {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailConfigUnavailable}
	}
	if !configured {
		return AvailabilityStatus{State: integrationstate.NotConfigured, Detail: notificationDetailNotConfigured}
	}
	if !channel.Enabled {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailConfiguredDisabled}
	}

	if receipt == nil || receipt.ChannelID != channel.ID || receipt.ChannelConfigRevision != channel.ConfigRevision {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailProbeUnverified}
	}
	if receipt.ObservedAt.IsZero() || receipt.ObservedAt.After(now) {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailProbeUnverified}
	}
	if receipt.Outcome == models.NotificationDeliveryOutcomeFailure {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailDeliveryFailed}
	}
	if receipt.Outcome != models.NotificationDeliveryOutcomeSuccess {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailProbeUnverified}
	}
	if now.Sub(receipt.ObservedAt) > notificationReceiptFreshness {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailDeliveryStale}
	}
	return AvailabilityStatus{State: integrationstate.Healthy, Detail: notificationDetailDeliveryConfirmed}
}

// ChannelsAvailability summarizes a channel inventory without conflating
// configured/disabled or partially broken detail states with the canonical
// optional-integration state. It delegates to the receipt-aware aggregate with
// no receipts, preserving the pre-receipt behavior.
func ChannelsAvailability(channels []models.NotificationChannel) AvailabilityStatus {
	return ChannelsAvailabilityWithReceipts(channels, nil, time.Now().UTC())
}

// ChannelsAvailabilityWithReceipts applies the strict aggregate contract:
// every enabled configured channel needs a current success receipt. A single
// disabled, broken, unverified, stale, or failed channel keeps the aggregate
// unavailable.
func ChannelsAvailabilityWithReceipts(channels []models.NotificationChannel, receipts map[int64]models.NotificationDeliveryReceipt, now time.Time) AvailabilityStatus {
	if len(channels) == 0 {
		return AvailabilityStatus{State: integrationstate.NotConfigured, Detail: notificationDetailNotConfigured}
	}

	configuredCount := 0
	enabledConfiguredCount := 0
	hasUnavailable := false
	allHealthy := true
	for _, channel := range channels {
		var receipt *models.NotificationDeliveryReceipt
		if candidate, ok := receipts[channel.ID]; ok {
			receipt = &candidate
		}
		status := ChannelAvailabilityWithReceipt(channel, receipt, now)
		if status.State != integrationstate.NotConfigured {
			configuredCount++
		}
		if status.State == integrationstate.Unavailable {
			hasUnavailable = true
		}
		if status.Detail != notificationDetailConfiguredDisabled && status.State != integrationstate.NotConfigured {
			enabledConfiguredCount++
		}
		if status.State != integrationstate.Healthy && status.State != integrationstate.NotConfigured {
			allHealthy = false
		}
	}

	if configuredCount == 0 {
		return AvailabilityStatus{State: integrationstate.NotConfigured, Detail: notificationDetailNotConfigured}
	}
	if enabledConfiguredCount == 0 {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailConfiguredDisabled}
	}
	if hasUnavailable {
		return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailDegraded}
	}
	if allHealthy {
		return AvailabilityStatus{State: integrationstate.Healthy, Detail: notificationDetailDeliveryConfirmed}
	}
	return AvailabilityStatus{State: integrationstate.Unavailable, Detail: notificationDetailProbeUnverified}
}

// channelConfigPresent distinguishes an absent/empty config object from a
// configured-but-unverified destination. It does not validate provider
// reachability or claim that a credential works.
func channelConfigPresent(channelType, normalized string) (bool, error) {
	switch channelType {
	case models.ChannelEmail:
		var cfg models.EmailConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return false, err
		}
		return cfg.Host != "" || cfg.Port != 0 || cfg.Username != "" || cfg.Password != "" || cfg.From != "" || cfg.To != "" || cfg.TLS, nil
	case models.ChannelTelegram:
		var cfg models.TelegramConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return false, err
		}
		return cfg.BotToken != "" || cfg.ChatID != 0, nil
	case models.ChannelDiscord:
		var cfg models.DiscordConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return false, err
		}
		return cfg.WebhookURL != "" || cfg.Username != "", nil
	case models.ChannelSlack:
		var cfg models.SlackConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return false, err
		}
		return cfg.WebhookURL != "" || cfg.Channel != "" || cfg.Username != "", nil
	default:
		return false, fmt.Errorf("unknown channel type %q", channelType)
	}
}

func buildChannel(c models.NotificationChannel) (Channel, error) {
	normalized, err := NormalizeChannelConfig(c.Type, c.Config, `{}`)
	if err != nil {
		return nil, err
	}
	switch c.Type {
	case models.ChannelEmail:
		var cfg models.EmailConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return nil, err
		}
		return NewEmailChannel(cfg), nil
	case models.ChannelTelegram:
		var cfg models.TelegramConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return nil, err
		}
		return NewTelegramChannel(cfg), nil
	case models.ChannelDiscord:
		var cfg models.DiscordConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return nil, err
		}
		return NewDiscordChannel(cfg), nil
	case models.ChannelSlack:
		var cfg models.SlackConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return nil, err
		}
		return NewSlackChannel(cfg), nil
	default:
		return nil, fmt.Errorf("unknown channel type %q", c.Type)
	}
}

type Event struct {
	Type, Host, Target, Message string
	Value                       float64
	Time                        time.Time
}

var subjectTmpl = template.Must(template.New("s").Parse("[Heyserver] Alert: {{.Type}} on {{.Host}}"))
var bodyTmpl = template.Must(template.New("b").Parse("Heyserver Panel Alert\n====================\nHost   : {{.Host}}\nType   : {{.Type}}\nTarget : {{.Target}}\nValue  : {{printf \"%.2f\" .Value}}\nTime   : {{.Time.Format \"2006-01-02 15:04:05 UTC\"}}\n\n{{.Message}}\n"))

func RenderSubject(e Event) string {
	var b bytes.Buffer
	_ = subjectTmpl.Execute(&b, e)
	return b.String()
}
func RenderBody(e Event) string { var b bytes.Buffer; _ = bodyTmpl.Execute(&b, e); return b.String() }
func splitTrim(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
func hostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}
func FireManual(channel models.NotificationChannel) error {
	_, err := FireManualWithResult(channel)
	return err
}

func FireManualWithResult(channel models.NotificationChannel) (DeliveryResult, error) {
	channel.Enabled = true
	d := NewDispatcher([]models.NotificationChannel{channel})
	e := Event{Type: "test", Host: hostname(), Target: "manual test", Message: "This is a test notification from Heyserver Panel.", Time: time.Now().UTC()}
	results, err := d.SendWithResults(fmt.Sprintf("[Heyserver] Test Notification (%d %s)", channel.ID, channel.Name), RenderBody(e))
	if len(results) == 0 {
		return DeliveryResult{
			ChannelID:             channel.ID,
			ChannelConfigRevision: channel.ConfigRevision,
			Outcome:               models.NotificationDeliveryOutcomeFailure,
		}, err
	}
	return results[0], err
}
