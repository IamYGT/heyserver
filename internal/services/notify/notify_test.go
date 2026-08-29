package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
)

func TestEscapeMarkdownV2(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello world", "hello world"},
		{"asterisk", "down*now", "down\\*now"},
		{"underscore", "a_b", "a\\_b"},
		{"dot and bang", "alert.go!", "alert\\.go\\!"},
		{"multiple reserved", "a.b-c", "a\\.b\\-c"},
		{"empty", "", ""},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := escapeMarkdownV2(tc.input)
			if got != tc.want {
				t.Errorf("escapeMarkdownV2(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSplitTrim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		sep  string
		want []string
	}{
		{"comma separated", "a,b, c", ",", []string{"a", "b", "c"}},
		{"empty entries skipped", "a,, b ,", ",", []string{"a", "b"}},
		{"all empty", " , , ", ",", nil},
		{"single value", "only@example.com", ",", []string{"only@example.com"}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := splitTrim(tc.s, tc.sep)
			if len(got) != len(tc.want) {
				t.Fatalf("splitTrim(%q) = %v, want %v", tc.s, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestRenderSubjectAndBody(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 6, 8, 12, 30, 0, 0, time.UTC)
	e := Event{
		Type:    "cpu_usage",
		Host:    "web-01",
		Target:  "/",
		Message: "threshold exceeded",
		Value:   91.25,
		Time:    when,
	}

	subject := RenderSubject(e)
	if !strings.Contains(subject, "cpu_usage") || !strings.Contains(subject, "web-01") {
		t.Errorf("RenderSubject() = %q, want host and type", subject)
	}

	body := RenderBody(e)
	for _, want := range []string{"web-01", "cpu_usage", "threshold exceeded", "91.25", "2026-06-08 12:30:00 UTC"} {
		if !strings.Contains(body, want) {
			t.Errorf("RenderBody() missing %q in %q", want, body)
		}
	}
}

func TestBuildChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		channel models.NotificationChannel
		wantErr bool
		errSub  string
	}{
		{
			name: "email valid config",
			channel: models.NotificationChannel{
				Type:   models.ChannelEmail,
				Config: `{"host":"smtp.example.com","port":587,"from":"a@x.com","to":"b@y.com"}`,
			},
		},
		{
			name: "telegram valid config",
			channel: models.NotificationChannel{
				Type:   models.ChannelTelegram,
				Config: `{"botToken":"123:abc","chatId":99}`,
			},
		},
		{
			name: "invalid json",
			channel: models.NotificationChannel{
				Type:   models.ChannelSlack,
				Config: `{not-json`,
			},
			wantErr: true,
		},
		{
			name: "unknown channel type",
			channel: models.NotificationChannel{
				Type:   "pagerduty",
				Config: `{}`,
			},
			wantErr: true,
			errSub:  "unknown channel type",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch, err := buildChannel(tc.channel)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errSub != "" && !strings.Contains(err.Error(), tc.errSub) {
					t.Errorf("error %q should contain %q", err.Error(), tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ch == nil {
				t.Fatal("expected channel instance")
			}
		})
	}
}

func TestEmailChannel_noRecipients(t *testing.T) {
	t.Parallel()

	ch := NewEmailChannel(models.EmailConfig{
		Host: "smtp.example.com",
		Port: 587,
		From: "from@example.com",
		To:   "  ,  ",
	})
	err := ch.Send("subject", "body")
	if err == nil {
		t.Fatal("expected error for empty recipients")
	}
	if !strings.Contains(err.Error(), "no recipients") {
		t.Errorf("error = %q, want no recipients", err.Error())
	}
}

func TestDiscordChannel_invalidWebhook(t *testing.T) {
	t.Parallel()

	ch := NewDiscordChannel(models.DiscordConfig{WebhookURL: "http://127.0.0.1/hook"})
	err := ch.Send("x", "y")
	if err == nil {
		t.Fatal("expected SSRF validation error")
	}
	if !strings.Contains(err.Error(), "discord:") {
		t.Errorf("error = %q, want discord prefix", err.Error())
	}
}

func TestSlackChannel_invalidWebhook(t *testing.T) {
	t.Parallel()

	ch := NewSlackChannel(models.SlackConfig{WebhookURL: "http://127.0.0.1/hook"})
	err := ch.Send("alert", "details")
	if err == nil {
		t.Fatal("expected SSRF validation error")
	}
	if !strings.Contains(err.Error(), "slack:") {
		t.Errorf("error = %q, want slack prefix", err.Error())
	}
}

func TestDispatcher_Send_skipsDisabled(t *testing.T) {
	t.Parallel()

	slackCfg, _ := json.Marshal(models.SlackConfig{WebhookURL: "https://hooks.example.com/abc"})
	d := NewDispatcher([]models.NotificationChannel{
		{Name: "disabled", Type: models.ChannelSlack, Config: string(slackCfg), Enabled: false},
	})

	if err := d.Send("subject", "body"); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestDispatcher_Send_buildError(t *testing.T) {
	t.Parallel()

	d := NewDispatcher([]models.NotificationChannel{
		{Name: "broken", Type: models.ChannelEmail, Config: `{`, Enabled: true},
	})
	err := d.Send("subject", "body")
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	if !strings.Contains(err.Error(), "notify errors") {
		t.Errorf("error = %q, want notify errors prefix", err.Error())
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error = %q, want channel name", err.Error())
	}
}

func TestChannelAvailabilityDoesNotInferHealthyFromConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		channel models.NotificationChannel
		state   integrationstate.State
		detail  string
	}{
		{
			name:    "empty config is not configured",
			channel: models.NotificationChannel{Type: models.ChannelSlack, Config: `{}`, Enabled: true},
			state:   integrationstate.NotConfigured,
			detail:  "not_configured",
		},
		{
			name:    "configured channel has no durable probe",
			channel: models.NotificationChannel{Type: models.ChannelSlack, Config: `{"webhook_url":"https://hooks.example.com/abc"}`, Enabled: true},
			state:   integrationstate.Unavailable,
			detail:  "probe_unverified",
		},
		{
			name:    "disabled channel remains unavailable",
			channel: models.NotificationChannel{Type: models.ChannelTelegram, Config: `{"bot_token":"123:abc","chat_id":42}`, Enabled: false},
			state:   integrationstate.Unavailable,
			detail:  "configured_disabled",
		},
		{
			name:    "unreadable protected reference is unavailable",
			channel: models.NotificationChannel{Type: models.ChannelEmail, Config: `file:channel-7.json`, Enabled: true},
			state:   integrationstate.Unavailable,
			detail:  "config_unavailable",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ChannelAvailability(test.channel)
			if got.State != test.state || got.Detail != test.detail {
				t.Fatalf("ChannelAvailability() = %#v, want state=%q detail=%q", got, test.state, test.detail)
			}
			if got.State == integrationstate.Healthy {
				t.Fatal("configuration alone must never produce healthy")
			}
		})
	}
}

func TestChannelsAvailabilityKeepsAggregateDetailsOutsideCanonicalState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		channels []models.NotificationChannel
		state    integrationstate.State
		detail   string
	}{
		{name: "empty inventory", channels: nil, state: integrationstate.NotConfigured, detail: "not_configured"},
		{
			name:     "all disabled",
			channels: []models.NotificationChannel{{Type: models.ChannelSlack, Config: `{"webhook_url":"https://hooks.example.com/abc"}`, Enabled: false}},
			state:    integrationstate.Unavailable,
			detail:   "configured_disabled",
		},
		{
			name:     "configured without persisted probe",
			channels: []models.NotificationChannel{{Type: models.ChannelSlack, Config: `{"webhook_url":"https://hooks.example.com/abc"}`, Enabled: true}},
			state:    integrationstate.Unavailable,
			detail:   "degraded",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ChannelsAvailability(test.channels)
			if got.State != test.state || got.Detail != test.detail {
				t.Fatalf("ChannelsAvailability() = %#v, want state=%q detail=%q", got, test.state, test.detail)
			}
			if got.Detail == string(integrationstate.Healthy) {
				t.Fatal("aggregate detail must not become a canonical healthy state")
			}
		})
	}
}
