package backup

import (
	"fmt"
	"strconv"
	"strings"
)

// FrequencyToCron converts UI schedule fields to a 5-field cron expression.
func FrequencyToCron(frequency, timeHHMM string) (string, error) {
	parts := strings.Split(timeHHMM, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid time format, expected HH:MM")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return "", fmt.Errorf("invalid hour")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return "", fmt.Errorf("invalid minute")
	}
	switch strings.ToLower(frequency) {
	case "daily":
		return fmt.Sprintf("%d %d * * *", minute, hour), nil
	case "weekly":
		return fmt.Sprintf("%d %d * * 0", minute, hour), nil
	case "monthly":
		return fmt.Sprintf("%d %d 1 * *", minute, hour), nil
	default:
		return "", fmt.Errorf("unknown frequency: %s", frequency)
	}
}

// CronToFrequency converts the first schedule entry back to UI-friendly fields.
func CronToFrequency(cron string) (frequency, timeHHMM string, err error) {
	fields := strings.Fields(strings.TrimSpace(cron))
	if len(fields) != 5 {
		return "", "", fmt.Errorf("invalid cron")
	}
	minute, minuteErr := strconv.Atoi(fields[0])
	hour, hourErr := strconv.Atoi(fields[1])
	if minuteErr != nil || minute < 0 || minute > 59 || hourErr != nil || hour < 0 || hour > 23 {
		return "", "", fmt.Errorf("cron time cannot be represented as HH:MM")
	}
	dayOfMonth := fields[2]
	month := fields[3]
	dayOfWeek := fields[4]
	timeHHMM = fmt.Sprintf("%02d:%02d", hour, minute)
	switch {
	case dayOfMonth == "1" && month == "*" && dayOfWeek == "*":
		return "monthly", timeHHMM, nil
	case dayOfMonth == "*" && month == "*" && dayOfWeek == "0":
		return "weekly", timeHHMM, nil
	case dayOfMonth == "*" && month == "*" && dayOfWeek == "*":
		return "daily", timeHHMM, nil
	default:
		return "", "", fmt.Errorf("cron cannot be represented as a preset frequency")
	}
}
