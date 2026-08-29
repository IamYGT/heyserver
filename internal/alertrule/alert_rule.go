package alertrule

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/IamYGT/heyserver/internal/models"
)

const (
	maxAlertNameLength = 128
	maxAlertDuration   = 24 * 60
	maxAlertCooldown   = 7 * 24 * 60
)

var (
	dnsLabelPattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	systemdUnitPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@:-]{0,254}\.(?:service|socket|timer|mount|path|target)$`)
)

// NormalizeAlertType maps legacy public API values to the canonical evaluator
// values. The aliases are kept here so existing self-hosted databases and API
// clients can be upgraded in place.
func NormalizeAlertType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "cpu":
		return models.AlertCPUUsage
	case "memory":
		return models.AlertMemoryUsage
	case "disk":
		return models.AlertDiskUsage
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

// ValidateAndNormalizeAlertRule applies the same portable rule contract to API,
// CLI, migrations, and evaluator inputs.
func ValidateAndNormalizeAlertRule(rule models.AlertRule) (models.AlertRule, error) {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return models.AlertRule{}, fmt.Errorf("name is required")
	}
	if len([]rune(rule.Name)) > maxAlertNameLength || containsControl(rule.Name) {
		return models.AlertRule{}, fmt.Errorf("name must be 1-%d control-free characters", maxAlertNameLength)
	}

	rule.Type = NormalizeAlertType(rule.Type)
	if !isSupportedAlertType(rule.Type) {
		return models.AlertRule{}, fmt.Errorf("unsupported alert type %q", rule.Type)
	}
	if math.IsNaN(rule.Threshold) || math.IsInf(rule.Threshold, 0) {
		return models.AlertRule{}, fmt.Errorf("threshold must be finite")
	}
	if rule.DurationMins < 0 || rule.DurationMins > maxAlertDuration {
		return models.AlertRule{}, fmt.Errorf("durationMins must be between 0 and %d", maxAlertDuration)
	}
	if rule.CooldownMins < 1 || rule.CooldownMins > maxAlertCooldown {
		return models.AlertRule{}, fmt.Errorf("cooldownMins must be between 1 and %d", maxAlertCooldown)
	}

	rule.Target = strings.TrimSpace(rule.Target)
	if containsControl(rule.Target) {
		return models.AlertRule{}, fmt.Errorf("target must not contain control characters")
	}

	switch rule.Type {
	case models.AlertCPUUsage, models.AlertMemoryUsage:
		if err := validatePercentageThreshold(rule.Threshold); err != nil {
			return models.AlertRule{}, err
		}
		rule.Target = ""
	case models.AlertDiskUsage:
		if err := validatePercentageThreshold(rule.Threshold); err != nil {
			return models.AlertRule{}, err
		}
		if rule.Target == "" {
			rule.Target = "/"
		}
		if !filepath.IsAbs(rule.Target) {
			return models.AlertRule{}, fmt.Errorf("target must be an absolute mount path")
		}
		rule.Target = filepath.Clean(rule.Target)
	case models.AlertSSLExpiry:
		if rule.Threshold < 0 || rule.Threshold > 3650 {
			return models.AlertRule{}, fmt.Errorf("threshold must be between 0 and 3650 days")
		}
		rule.Target = strings.ToLower(rule.Target)
		if !validDNSName(rule.Target) {
			return models.AlertRule{}, fmt.Errorf("target must be a valid DNS name")
		}
	case models.AlertServiceDown:
		if !systemdUnitPattern.MatchString(rule.Target) {
			return models.AlertRule{}, fmt.Errorf("target must be a valid systemd unit name")
		}
		rule.Threshold = 1
	case models.AlertFailedLogins:
		if rule.Threshold < 1 || rule.Threshold > 1_000_000 || math.Trunc(rule.Threshold) != rule.Threshold {
			return models.AlertRule{}, fmt.Errorf("threshold must be a whole number between 1 and 1000000")
		}
		rule.Target = ""
	}

	return rule, nil
}

func isSupportedAlertType(value string) bool {
	switch value {
	case models.AlertCPUUsage, models.AlertMemoryUsage, models.AlertDiskUsage,
		models.AlertSSLExpiry, models.AlertServiceDown, models.AlertFailedLogins:
		return true
	default:
		return false
	}
}

func validatePercentageThreshold(value float64) error {
	if value < 0 || value > 100 {
		return fmt.Errorf("threshold must be between 0 and 100 percent")
	}
	return nil
}

func validDNSName(value string) bool {
	if value == "" || len(value) > 253 || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if !dnsLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
