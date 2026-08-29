package notify

import (
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
)

func TestNormalizeChannelConfig_AcceptsWebFieldsAndPreservesProtectedSecret(t *testing.T) {
	current := `{"botToken":"123456:existing-secret","chatId":-1001}`
	incoming := `{"bot_token":"","chat_id":"-2002"}`
	normalized, err := NormalizeChannelConfig(models.ChannelTelegram, incoming, current)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(normalized, `"botToken":"123456:existing-secret"`) || !strings.Contains(normalized, `"chatId":-2002`) {
		t.Fatalf("normalized config = %s", normalized)
	}
	redacted, err := RedactChannelConfig(models.ChannelTelegram, normalized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(redacted, "existing-secret") || !strings.Contains(redacted, `"secret_configured":true`) {
		t.Fatalf("redacted config = %s", redacted)
	}
}

func TestNormalizeChannelConfig_MapsEmailWebFields(t *testing.T) {
	normalized, err := NormalizeChannelConfig(models.ChannelEmail, `{"smtp_host":"smtp.example.com","smtp_port":"587","smtp_user":"ops","smtp_pass":"secret","from_address":"alerts@example.com","to_address":"admin@example.com"}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"host":"smtp.example.com"`, `"port":587`, `"username":"ops"`, `"password":"secret"`, `"from":"alerts@example.com"`, `"to":"admin@example.com"`} {
		if !strings.Contains(normalized, expected) {
			t.Fatalf("normalized config %s missing %s", normalized, expected)
		}
	}
}

func TestClearChannelSecret_RequiresExplicitOperation(t *testing.T) {
	current := `{"webhookUrl":"https://hooks.example.com/secret","username":"hserver"}`
	preserved, err := NormalizeChannelConfig(models.ChannelSlack, `{"webhook_url":""}`, current)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preserved, "hooks.example.com/secret") {
		t.Fatalf("empty update did not preserve secret: %s", preserved)
	}
	cleared, err := ClearChannelSecret(models.ChannelSlack, preserved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cleared, "hooks.example.com/secret") || !strings.Contains(cleared, `"webhookUrl":""`) {
		t.Fatalf("explicit clear result = %s", cleared)
	}
}
