package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/IamYGT/heyserver/internal/models"
)

// NormalizeChannelConfig accepts the legacy web field names and the canonical
// server model. Empty secret fields on update preserve the protected current
// value so API responses never need to return credentials to the browser.
func NormalizeChannelConfig(channelType, incomingRaw, currentRaw string) (string, error) {
	incoming, err := configObject(incomingRaw)
	if err != nil {
		return "", err
	}
	current, err := configObject(currentRaw)
	if err != nil {
		return "", err
	}

	marshal := func(value any) (string, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	switch channelType {
	case models.ChannelEmail:
		return marshal(models.EmailConfig{
			Host:     overlayString(incoming, current, false, "host", "smtp_host"),
			Port:     int(overlayInt64(incoming, current, "port", "smtp_port")),
			Username: overlayString(incoming, current, false, "username", "smtp_user"),
			Password: overlayString(incoming, current, true, "password", "smtp_pass"),
			From:     overlayString(incoming, current, false, "from", "from_address"),
			To:       overlayString(incoming, current, false, "to", "to_address"),
			TLS:      overlayBool(incoming, current, "tls"),
		})
	case models.ChannelTelegram:
		return marshal(models.TelegramConfig{
			BotToken: overlayString(incoming, current, true, "botToken", "bot_token"),
			ChatID:   overlayInt64(incoming, current, "chatId", "chat_id"),
		})
	case models.ChannelDiscord:
		return marshal(models.DiscordConfig{
			WebhookURL: overlayString(incoming, current, true, "webhookUrl", "webhook_url"),
			Username:   overlayString(incoming, current, false, "username"),
		})
	case models.ChannelSlack:
		return marshal(models.SlackConfig{
			WebhookURL: overlayString(incoming, current, true, "webhookUrl", "webhook_url"),
			Channel:    overlayString(incoming, current, false, "channel"),
			Username:   overlayString(incoming, current, false, "username"),
		})
	default:
		return "", fmt.Errorf("unknown channel type %q", channelType)
	}
}

// RedactChannelConfig returns only browser-editable non-secret fields and a
// boolean secret presence marker. The protected value never enters JSON output.
func RedactChannelConfig(channelType, raw string) (string, error) {
	normalized, err := NormalizeChannelConfig(channelType, raw, `{}`)
	if err != nil {
		return "", err
	}
	switch channelType {
	case models.ChannelEmail:
		var cfg models.EmailConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return "", err
		}
		return redactedJSON(map[string]any{
			"smtp_host": cfg.Host, "smtp_port": strconv.Itoa(cfg.Port), "smtp_user": cfg.Username,
			"from_address": cfg.From, "to_address": cfg.To, "secret_configured": cfg.Password != "",
		})
	case models.ChannelTelegram:
		var cfg models.TelegramConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return "", err
		}
		return redactedJSON(map[string]any{"chat_id": strconv.FormatInt(cfg.ChatID, 10), "secret_configured": cfg.BotToken != ""})
	case models.ChannelDiscord:
		var cfg models.DiscordConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return "", err
		}
		return redactedJSON(map[string]any{"username": cfg.Username, "secret_configured": cfg.WebhookURL != ""})
	case models.ChannelSlack:
		var cfg models.SlackConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return "", err
		}
		return redactedJSON(map[string]any{"channel": cfg.Channel, "username": cfg.Username, "secret_configured": cfg.WebhookURL != ""})
	default:
		return "", fmt.Errorf("unknown channel type %q", channelType)
	}
}

// ClearChannelSecret removes only the credential owned by the selected channel
// type. It is intentionally separate from an empty field, which means preserve.
func ClearChannelSecret(channelType, raw string) (string, error) {
	normalized, err := NormalizeChannelConfig(channelType, raw, `{}`)
	if err != nil {
		return "", err
	}
	marshal := func(value any) (string, error) {
		data, err := json.Marshal(value)
		return string(data), err
	}
	switch channelType {
	case models.ChannelEmail:
		var cfg models.EmailConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return "", err
		}
		cfg.Password = ""
		return marshal(cfg)
	case models.ChannelTelegram:
		var cfg models.TelegramConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return "", err
		}
		cfg.BotToken = ""
		return marshal(cfg)
	case models.ChannelDiscord:
		var cfg models.DiscordConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return "", err
		}
		cfg.WebhookURL = ""
		return marshal(cfg)
	case models.ChannelSlack:
		var cfg models.SlackConfig
		if err := json.Unmarshal([]byte(normalized), &cfg); err != nil {
			return "", err
		}
		cfg.WebhookURL = ""
		return marshal(cfg)
	default:
		return "", fmt.Errorf("unknown channel type %q", channelType)
	}
}

func redactedJSON(value map[string]any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func configObject(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		raw = `{}`
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("invalid notification channel config: %w", err)
	}
	if value == nil {
		return nil, errors.New("notification channel config must be a JSON object")
	}
	return value, nil
}

func lookupValue(config map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := config[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func stringValue(config map[string]any, keys ...string) (string, bool) {
	value, ok := lookupValue(config, keys...)
	if !ok || value == nil {
		return "", ok
	}
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case float64:
		return strconv.FormatInt(int64(typed), 10), true
	default:
		return "", true
	}
}

func overlayString(incoming, current map[string]any, preserveEmpty bool, keys ...string) string {
	if value, present := stringValue(incoming, keys...); present && (!preserveEmpty || strings.TrimSpace(value) != "") {
		return value
	}
	value, _ := stringValue(current, keys...)
	return value
}

func int64Value(config map[string]any, keys ...string) (int64, bool) {
	value, ok := lookupValue(config, keys...)
	if !ok || value == nil {
		return 0, ok
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, true
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed, true
	default:
		return 0, true
	}
}

func overlayInt64(incoming, current map[string]any, keys ...string) int64 {
	if value, present := int64Value(incoming, keys...); present {
		return value
	}
	value, _ := int64Value(current, keys...)
	return value
}

func boolValue(config map[string]any, keys ...string) (bool, bool) {
	value, ok := lookupValue(config, keys...)
	if !ok || value == nil {
		return false, ok
	}
	typed, valid := value.(bool)
	return typed, valid
}

func overlayBool(incoming, current map[string]any, keys ...string) bool {
	if value, present := boolValue(incoming, keys...); present {
		return value
	}
	value, _ := boolValue(current, keys...)
	return value
}
