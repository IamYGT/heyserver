package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/IamYGT/heyserver/internal/services/settings"
)

const uptimeSettingsRequestBodyLimit = 8 << 10

var uptimeSettingsDefaults = map[string]string{
	"uptime_retention_days":     "90",
	"uptime_compact_after_days": "30",
	"uptime_default_interval":   "60",
	"uptime_default_timeout":    "30",
	"uptime_default_channels":   "[]",
}

type uptimeSettingsUpdateRequest struct {
	RetentionDays    *string `json:"uptime_retention_days"`
	CompactAfterDays *string `json:"uptime_compact_after_days"`
	DefaultInterval  *string `json:"uptime_default_interval"`
	DefaultTimeout   *string `json:"uptime_default_timeout"`
	DefaultChannels  *string `json:"uptime_default_channels"`
}

// ── Settings ────────────────────────────────────────────────────────────────

func handleUptimeSettingsGet(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			jsonError(w, http.StatusServiceUnavailable, "settings service not initialized")
			return
		}
		result, err := readEffectiveUptimeSettings(svc)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to read uptime settings")
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}

func handleUptimeSettingsSet(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			jsonError(w, http.StatusServiceUnavailable, "settings service not initialized")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, uptimeSettingsRequestBodyLimit)
		var request uptimeSettingsUpdateRequest
		if err := decodeStrictJSON(r, &request); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid uptime settings request")
			return
		}
		updates, err := normalizeUptimeSettingsUpdate(request)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(updates) == 0 {
			jsonError(w, http.StatusBadRequest, "at least one uptime setting is required")
			return
		}
		effective, err := readStoredUptimeSettings(svc)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to read uptime settings")
			return
		}
		for key, value := range updates {
			effective[key] = value
		}
		effective, err = normalizeEffectiveUptimeSettings(effective)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validateEffectiveUptimeSettings(effective); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := svc.SetMany(updates); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to save uptime settings")
			return
		}
		jsonResponse(w, http.StatusOK, effective)
	}
}

func readEffectiveUptimeSettings(svc *settings.Service) (map[string]string, error) {
	result, err := readStoredUptimeSettings(svc)
	if err != nil {
		return nil, err
	}
	return normalizeEffectiveUptimeSettings(result)
}

func readStoredUptimeSettings(svc *settings.Service) (map[string]string, error) {
	result := make(map[string]string, len(uptimeSettingsDefaults))
	for key, defaultValue := range uptimeSettingsDefaults {
		value, err := svc.Get(key, defaultValue)
		if err != nil {
			return nil, err
		}
		if value == "" {
			value = defaultValue
		}
		result[key] = value
	}
	return result, nil
}

func normalizeEffectiveUptimeSettings(result map[string]string) (map[string]string, error) {
	updates, err := normalizeUptimeSettingsUpdate(uptimeSettingsUpdateRequest{
		RetentionDays:    stringPointer(result["uptime_retention_days"]),
		CompactAfterDays: stringPointer(result["uptime_compact_after_days"]),
		DefaultInterval:  stringPointer(result["uptime_default_interval"]),
		DefaultTimeout:   stringPointer(result["uptime_default_timeout"]),
		DefaultChannels:  stringPointer(result["uptime_default_channels"]),
	})
	if err != nil {
		return nil, fmt.Errorf("invalid persisted uptime settings: %w", err)
	}
	if err := validateEffectiveUptimeSettings(updates); err != nil {
		return nil, fmt.Errorf("invalid persisted uptime settings: %w", err)
	}
	return updates, nil
}

func normalizeUptimeSettingsUpdate(request uptimeSettingsUpdateRequest) (map[string]string, error) {
	updates := make(map[string]string, 5)
	for _, item := range []struct {
		key     string
		value   *string
		minimum int
		maximum int
	}{
		{key: "uptime_retention_days", value: request.RetentionDays, minimum: 2, maximum: 3650},
		{key: "uptime_compact_after_days", value: request.CompactAfterDays, minimum: 1, maximum: 365},
		{key: "uptime_default_interval", value: request.DefaultInterval, minimum: 10, maximum: 86400},
		{key: "uptime_default_timeout", value: request.DefaultTimeout, minimum: 1, maximum: 300},
	} {
		if item.value == nil {
			continue
		}
		number, err := strconv.Atoi(*item.value)
		if err != nil || number < item.minimum || number > item.maximum {
			return nil, fmt.Errorf("%s must be an integer between %d and %d", item.key, item.minimum, item.maximum)
		}
		updates[item.key] = strconv.Itoa(number)
	}
	if request.DefaultChannels != nil {
		var ids []int64
		if err := json.Unmarshal([]byte(*request.DefaultChannels), &ids); err != nil {
			return nil, errors.New("uptime_default_channels must be a JSON array of positive integers")
		}
		if len(ids) > 128 {
			return nil, errors.New("uptime_default_channels accepts at most 128 channel IDs")
		}
		seen := make(map[int64]struct{}, len(ids))
		normalized := make([]int64, 0, len(ids))
		for _, id := range ids {
			if id <= 0 {
				return nil, errors.New("uptime_default_channels must contain only positive channel IDs")
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			normalized = append(normalized, id)
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return nil, err
		}
		updates["uptime_default_channels"] = string(encoded)
	}
	return updates, nil
}

func validateEffectiveUptimeSettings(values map[string]string) error {
	retention, _ := strconv.Atoi(values["uptime_retention_days"])
	compactAfter, _ := strconv.Atoi(values["uptime_compact_after_days"])
	interval, _ := strconv.Atoi(values["uptime_default_interval"])
	timeout, _ := strconv.Atoi(values["uptime_default_timeout"])
	if compactAfter >= retention {
		return errors.New("uptime_compact_after_days must be less than uptime_retention_days")
	}
	if timeout > interval {
		return errors.New("uptime_default_timeout must not exceed uptime_default_interval")
	}
	return nil
}

func stringPointer(value string) *string { return &value }
