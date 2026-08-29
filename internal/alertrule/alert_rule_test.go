package alertrule

import (
	"math"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
)

func TestValidateAndNormalizeAlertRuleCanonicalizesSupportedRules(t *testing.T) {
	tests := []struct {
		name       string
		rule       models.AlertRule
		wantType   string
		wantTarget string
		wantValue  float64
	}{
		{"legacy cpu", models.AlertRule{Name: " CPU high ", Type: "cpu", Threshold: 90, CooldownMins: 15}, models.AlertCPUUsage, "", 90},
		{"legacy memory", models.AlertRule{Name: "Memory high", Type: "memory", Threshold: 80, CooldownMins: 15}, models.AlertMemoryUsage, "", 80},
		{"legacy disk", models.AlertRule{Name: "Disk high", Type: "disk", Threshold: 85, CooldownMins: 15}, models.AlertDiskUsage, "/", 85},
		{"disk path", models.AlertRule{Name: "Data disk", Type: models.AlertDiskUsage, Threshold: 75, Target: "/srv/../srv/data", CooldownMins: 15}, models.AlertDiskUsage, "/srv/data", 75},
		{"ssl", models.AlertRule{Name: "Certificate", Type: models.AlertSSLExpiry, Threshold: 14, Target: "EXAMPLE.COM", CooldownMins: 15}, models.AlertSSLExpiry, "example.com", 14},
		{"service", models.AlertRule{Name: "Web", Type: models.AlertServiceDown, Threshold: 0, Target: "nginx.service", CooldownMins: 15}, models.AlertServiceDown, "nginx.service", 1},
		{"failed logins", models.AlertRule{Name: "SSH", Type: models.AlertFailedLogins, Threshold: 5, Target: "ignored", CooldownMins: 15}, models.AlertFailedLogins, "", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateAndNormalizeAlertRule(tt.rule)
			if err != nil {
				t.Fatalf("ValidateAndNormalizeAlertRule() error = %v", err)
			}
			if got.Type != tt.wantType || got.Target != tt.wantTarget || got.Threshold != tt.wantValue {
				t.Fatalf("normalized rule = %#v, want type=%q target=%q threshold=%v", got, tt.wantType, tt.wantTarget, tt.wantValue)
			}
		})
	}
}

func TestValidateAndNormalizeAlertRuleRejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name string
		rule models.AlertRule
	}{
		{"empty name", models.AlertRule{Type: models.AlertCPUUsage, Threshold: 90, CooldownMins: 15}},
		{"long name", models.AlertRule{Name: strings.Repeat("a", 129), Type: models.AlertCPUUsage, Threshold: 90, CooldownMins: 15}},
		{"control name", models.AlertRule{Name: "CPU\nrule", Type: models.AlertCPUUsage, Threshold: 90, CooldownMins: 15}},
		{"unknown type", models.AlertRule{Name: "Rule", Type: "load_average", Threshold: 1, CooldownMins: 15}},
		{"non finite", models.AlertRule{Name: "Rule", Type: models.AlertCPUUsage, Threshold: math.Inf(1), CooldownMins: 15}},
		{"negative duration", models.AlertRule{Name: "Rule", Type: models.AlertCPUUsage, Threshold: 90, DurationMins: -1, CooldownMins: 15}},
		{"long duration", models.AlertRule{Name: "Rule", Type: models.AlertCPUUsage, Threshold: 90, DurationMins: 1441, CooldownMins: 15}},
		{"zero cooldown", models.AlertRule{Name: "Rule", Type: models.AlertCPUUsage, Threshold: 90}},
		{"long cooldown", models.AlertRule{Name: "Rule", Type: models.AlertCPUUsage, Threshold: 90, CooldownMins: 10081}},
		{"percentage over limit", models.AlertRule{Name: "Rule", Type: models.AlertMemoryUsage, Threshold: 101, CooldownMins: 15}},
		{"relative mount", models.AlertRule{Name: "Rule", Type: models.AlertDiskUsage, Threshold: 90, Target: "var", CooldownMins: 15}},
		{"path traversal domain", models.AlertRule{Name: "Rule", Type: models.AlertSSLExpiry, Threshold: 14, Target: "../example.com", CooldownMins: 15}},
		{"invalid domain", models.AlertRule{Name: "Rule", Type: models.AlertSSLExpiry, Threshold: 14, Target: "example_com", CooldownMins: 15}},
		{"option service", models.AlertRule{Name: "Rule", Type: models.AlertServiceDown, Target: "--user.service", CooldownMins: 15}},
		{"invalid unit suffix", models.AlertRule{Name: "Rule", Type: models.AlertServiceDown, Target: "nginx", CooldownMins: 15}},
		{"fractional login count", models.AlertRule{Name: "Rule", Type: models.AlertFailedLogins, Threshold: 2.5, CooldownMins: 15}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ValidateAndNormalizeAlertRule(tt.rule); err == nil {
				t.Fatalf("ValidateAndNormalizeAlertRule(%#v) unexpectedly succeeded", tt.rule)
			}
		})
	}
}
