package notify

import (
	"github.com/IamYGT/heyserver/internal/alertrule"
	"github.com/IamYGT/heyserver/internal/models"
)

func NormalizeAlertType(value string) string {
	return alertrule.NormalizeAlertType(value)
}

func ValidateAndNormalizeAlertRule(rule models.AlertRule) (models.AlertRule, error) {
	return alertrule.ValidateAndNormalizeAlertRule(rule)
}
