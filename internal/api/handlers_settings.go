package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/services/settings"
)

const mailAccessStateKey = "mail_access_state"

var requiredMailAccessSettings = [...]string{
	"webmail_url",
	"mail_admin_url",
	"mail_server_host",
	"mail_imap_port",
	"mail_smtp_starttls_port",
	"mail_smtp_ssl_port",
}

// mailAccessAvailability reports configuration readiness separately from
// provider reachability. EditableSettings has already validated each value;
// this endpoint does not probe the external webmail or IMAP/SMTP provider, so
// complete settings are unavailable rather than healthy.
func mailAccessAvailability(values map[string]string) integrationstate.State {
	configured := true
	for _, key := range requiredMailAccessSettings {
		if strings.TrimSpace(values[key]) == "" {
			configured = false
			break
		}
	}
	return integrationstate.FromObservation(integrationstate.Observation{Configured: configured})
}

func settingsUnavailableResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{
		"error":            message,
		mailAccessStateKey: string(integrationstate.Unavailable),
	})
}

func handleSettingsGetAll(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			settingsUnavailableResponse(w, http.StatusServiceUnavailable, "settings not initialized")
			return
		}
		all, err := svc.EditableSettings()
		if err != nil {
			slog.Error("settings handler error", "err", err)
			settingsUnavailableResponse(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if all == nil {
			all = make(map[string]string)
		}
		all[mailAccessStateKey] = string(mailAccessAvailability(all))
		jsonResponse(w, http.StatusOK, all)
	}
}

func handleSettingsGet(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil { jsonError(w, http.StatusServiceUnavailable, "settings not initialized"); return }
		key := r.PathValue("key")
		if key == "" { jsonError(w, http.StatusBadRequest, "key required"); return }
		if !settings.IsEditableSetting(key) { jsonError(w, http.StatusNotFound, "setting not found"); return }
		val, err := svc.Get(key, "")
		if err != nil { slog.Error("settings handler error", "err", err); jsonError(w, http.StatusInternalServerError, "internal server error"); return }
		jsonResponse(w, http.StatusOK, map[string]string{"key": key, "value": val})
	}
}

func handleSettingsSet(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil { jsonError(w, http.StatusServiceUnavailable, "settings not initialized"); return }
		var pairs map[string]string
		r.Body = http.MaxBytesReader(w, r.Body, portableSettingsRequestLimit)
		if err := decodeStrictJSON(r, &pairs); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) { jsonError(w, http.StatusRequestEntityTooLarge, "settings request body is too large") } else { jsonError(w, http.StatusBadRequest, "invalid settings request") }
			return
		}
		if err := settings.ValidateEditableSettings(pairs); err != nil { jsonError(w, http.StatusBadRequest, err.Error()); return }
		if err := svc.SetMany(pairs); err != nil { slog.Error("settings handler error", "err", err); jsonError(w, http.StatusInternalServerError, "internal server error"); return }
		jsonResponse(w, http.StatusOK, map[string]string{"status": "saved"})
	}
}

func handleSettingsDelete(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil { jsonError(w, http.StatusServiceUnavailable, "settings not initialized"); return }
		key := r.PathValue("key")
		if key == "" { jsonError(w, http.StatusBadRequest, "key required"); return }
		if !settings.IsEditableSetting(key) { jsonError(w, http.StatusNotFound, "setting not found"); return }
		if err := requireEmptyRequestBody(r); err != nil { jsonError(w, http.StatusBadRequest, err.Error()); return }
		if err := svc.Delete(key); err != nil { slog.Error("settings handler error", "err", err); jsonError(w, http.StatusInternalServerError, "internal server error"); return }
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func handleSystemInfo(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil { jsonError(w, http.StatusServiceUnavailable, "settings not initialized"); return }
		jsonResponse(w, http.StatusOK, svc.SystemInfo())
	}
}

func handleHealth(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil { jsonError(w, http.StatusServiceUnavailable, "settings not initialized"); return }
		jsonResponse(w, http.StatusOK, svc.Health())
	}
}

const onboardingMaxStep = 5

// handleOnboardingGet returns the current onboarding state.
// Response: {"completed": bool, "step": int}
func handleOnboardingGet(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil { jsonError(w, http.StatusServiceUnavailable, "settings not initialized"); return }
		completed, err := svc.Get("onboarding_completed", "false")
		if err != nil { jsonError(w, http.StatusInternalServerError, "internal server error"); return }
		step, err := svc.Get("onboarding_step", "0")
		if err != nil { jsonError(w, http.StatusInternalServerError, "internal server error"); return }
		stepInt, parseErr := strconv.Atoi(step)
		if parseErr != nil || stepInt < 0 || stepInt > onboardingMaxStep { stepInt = 0 }
		jsonResponse(w, http.StatusOK, map[string]any{
			"completed": completed == "true",
			"step":      stepInt,
		})
	}
}

// handleOnboardingSet saves the current onboarding step and optional completion.
// Body: {"completed": bool, "step": int}
func handleOnboardingSet(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil { jsonError(w, http.StatusServiceUnavailable, "settings not initialized"); return }
		var body struct {
			Completed *bool `json:"completed"`
			Step      *int  `json:"step"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := decodeStrictJSON(r, &body); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				jsonError(w, http.StatusRequestEntityTooLarge, "onboarding request body is too large")
			} else {
				jsonError(w, http.StatusBadRequest, "invalid request body")
			}
			return
		}
		if body.Completed == nil || body.Step == nil {
			jsonError(w, http.StatusBadRequest, "completed and step are required")
			return
		}
		if *body.Step < 0 || *body.Step > onboardingMaxStep {
			jsonError(w, http.StatusBadRequest, "step must be between 0 and 5")
			return
		}
		stepStr := fmt.Sprintf("%d", *body.Step)
		completedStr := "false"
		if *body.Completed { completedStr = "true" }
		if err := svc.SetMany(map[string]string{
			"onboarding_completed": completedStr,
			"onboarding_step":      stepStr,
		}); err != nil {
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "saved"})
	}
}
