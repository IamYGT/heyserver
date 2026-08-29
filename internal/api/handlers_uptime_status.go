package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	uptime "github.com/IamYGT/heyserver/internal/services/uptime"
	"github.com/IamYGT/heyserver/internal/store"
)

const uptimeStatusPageRequestBodyLimit = 64 << 10

var uptimeStatusPageSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

type uptimeStatusPageRequest struct {
	Slug        string                         `json:"slug"`
	Title       string                         `json:"title"`
	Description string                         `json:"description"`
	Theme       string                         `json:"theme"`
	LogoURL     string                         `json:"logo_url"`
	IsPublic    *bool                          `json:"is_public"`
	HistoryDays int                            `json:"history_days"`
	Monitors    []store.StatusPageMonitorEntry `json:"monitors"`
}

type uptimeStatusPageUpdateRequest struct {
	Slug        *string                         `json:"slug"`
	Title       *string                         `json:"title"`
	Description *string                         `json:"description"`
	Theme       *string                         `json:"theme"`
	LogoURL     *string                         `json:"logo_url"`
	IsPublic    *bool                           `json:"is_public"`
	HistoryDays *int                            `json:"history_days"`
	Monitors    *[]store.StatusPageMonitorEntry `json:"monitors"`
}

// ── Status Pages ─────────────────────────────────────────────────────────────

func handleUptimeStatusPageList(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		pages, err := engine.Repo().ListStatusPages()
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if pages == nil {
			pages = []store.UptimeStatusPage{}
		}
		jsonResponse(w, http.StatusOK, pages)
	}
}

func handleUptimeStatusPageCreate(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, uptimeStatusPageRequestBodyLimit)
		var request uptimeStatusPageRequest
		if err := decodeStrictJSON(r, &request); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid status page request")
			return
		}
		if request.Theme == "" {
			request.Theme = "auto"
		}
		if request.HistoryDays <= 0 {
			request.HistoryDays = 90
		}
		isPublic := true
		if request.IsPublic != nil {
			isPublic = *request.IsPublic
		}
		sp, err := normalizeUptimeStatusPage(request, isPublic)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validateUptimeStatusPageMonitors(engine, sp.Monitors); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing, err := engine.Repo().GetStatusPage(sp.Slug)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to inspect status page identity")
			return
		}
		if existing != nil {
			jsonError(w, http.StatusConflict, "status page slug already exists")
			return
		}
		if err := engine.Repo().CreateStatusPage(sp); err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		jsonResponse(w, http.StatusCreated, sp)
	}
}

func handleUptimeStatusPageUpdate(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		existing, err := engine.Repo().GetStatusPage(id)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if existing == nil {
			jsonError(w, http.StatusNotFound, "status page not found")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, uptimeStatusPageRequestBodyLimit)
		var patch uptimeStatusPageUpdateRequest
		if err := decodeStrictJSON(r, &patch); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid status page request")
			return
		}
		if patch.Slug == nil && patch.Title == nil && patch.Description == nil && patch.Theme == nil &&
			patch.LogoURL == nil && patch.IsPublic == nil && patch.HistoryDays == nil && patch.Monitors == nil {
			jsonError(w, http.StatusBadRequest, "at least one status page field is required")
			return
		}
		request := uptimeStatusPageRequest{
			Slug: existing.Slug, Title: existing.Title, Description: existing.Description,
			Theme: existing.Theme, LogoURL: existing.LogoURL, IsPublic: boolPointer(existing.IsPublic),
			HistoryDays: existing.HistoryDays, Monitors: existing.Monitors,
		}
		if patch.Slug != nil {
			request.Slug = *patch.Slug
		}
		if patch.Title != nil {
			request.Title = *patch.Title
		}
		if patch.Description != nil {
			request.Description = *patch.Description
		}
		if patch.Theme != nil {
			request.Theme = *patch.Theme
		}
		if patch.LogoURL != nil {
			request.LogoURL = *patch.LogoURL
		}
		if patch.IsPublic != nil {
			request.IsPublic = patch.IsPublic
		}
		if patch.HistoryDays != nil {
			request.HistoryDays = *patch.HistoryDays
		}
		if patch.Monitors != nil {
			request.Monitors = *patch.Monitors
		}
		replacement, err := normalizeUptimeStatusPage(request, *request.IsPublic)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validateUptimeStatusPageMonitors(engine, replacement.Monitors); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		conflict, err := engine.Repo().GetStatusPage(replacement.Slug)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to inspect status page identity")
			return
		}
		if conflict != nil && conflict.ID != id {
			jsonError(w, http.StatusConflict, "status page slug already exists")
			return
		}
		replacement.ID = id
		replacement.CreatedAt = existing.CreatedAt
		if err := engine.Repo().UpdateStatusPage(replacement); err != nil {
			slog.Error("uptime handler error", "err", err)
			if errors.Is(err, sql.ErrNoRows) {
				jsonError(w, http.StatusNotFound, "status page not found")
				return
			}
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		jsonResponse(w, http.StatusOK, replacement)
	}
}

func handleUptimeStatusPageDelete(engine *uptime.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if engine == nil {
			jsonError(w, http.StatusServiceUnavailable, "uptime engine not initialized")
			return
		}
		id, err := pathInt64(r, "id")
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid id")
			return
		}
		existing, err := engine.Repo().GetStatusPage(id)
		if err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if existing == nil {
			jsonError(w, http.StatusNotFound, "status page not found")
			return
		}
		if err := engine.Repo().DeleteStatusPage(id); err != nil {
			slog.Error("uptime handler error", "err", err)
			jsonError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func normalizeUptimeStatusPage(request uptimeStatusPageRequest, isPublic bool) (*store.UptimeStatusPage, error) {
	slug := strings.ToLower(strings.TrimSpace(request.Slug))
	if !uptimeStatusPageSlugPattern.MatchString(slug) {
		return nil, errors.New("status page slug must use 1-64 lowercase letters, numbers, or interior hyphens")
	}
	title, err := boundedStatusPageText("status page title", request.Title, 128, true)
	if err != nil {
		return nil, err
	}
	description, err := boundedStatusPageText("status page description", request.Description, 2048, false)
	if err != nil {
		return nil, err
	}
	theme := strings.ToLower(strings.TrimSpace(request.Theme))
	if theme != "auto" && theme != "light" && theme != "dark" {
		return nil, errors.New("status page theme must be auto, light, or dark")
	}
	if request.HistoryDays < 1 || request.HistoryDays > 3650 {
		return nil, errors.New("status page history_days must be between 1 and 3650")
	}
	logoURL, err := validateUptimeStatusPageLogo(request.LogoURL)
	if err != nil {
		return nil, err
	}
	if len(request.Monitors) > 128 {
		return nil, errors.New("status page accepts at most 128 monitors")
	}
	monitors := make([]store.StatusPageMonitorEntry, 0, len(request.Monitors))
	seen := make(map[int64]struct{}, len(request.Monitors))
	for index, monitor := range request.Monitors {
		if monitor.MonitorID <= 0 {
			return nil, errors.New("status page monitor IDs must be positive integers")
		}
		if _, exists := seen[monitor.MonitorID]; exists {
			return nil, fmt.Errorf("status page monitor %d is duplicated", monitor.MonitorID)
		}
		seen[monitor.MonitorID] = struct{}{}
		displayName, err := boundedStatusPageText("status page monitor display name", monitor.DisplayName, 128, false)
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, store.StatusPageMonitorEntry{
			MonitorID: monitor.MonitorID, DisplayName: displayName, SortOrder: index + 1,
		})
	}
	return &store.UptimeStatusPage{
		Slug: slug, Title: title, Description: description, Theme: theme,
		LogoURL: logoURL, IsPublic: isPublic, HistoryDays: request.HistoryDays,
		Monitors: monitors,
	}, nil
}

func validateUptimeStatusPageMonitors(engine *uptime.Engine, monitors []store.StatusPageMonitorEntry) error {
	for _, monitor := range monitors {
		item, err := engine.Repo().GetMonitor(monitor.MonitorID)
		if err != nil {
			return errors.New("failed to validate status page monitors")
		}
		if item == nil {
			return fmt.Errorf("uptime monitor %d was not found", monitor.MonitorID)
		}
	}
	return nil
}

func validateUptimeStatusPageLogo(value string) (string, error) {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return "", nil
	}
	if len(clean) > 2048 || strings.ContainsAny(clean, "\r\n\x00") {
		return "", errors.New("status page logo_url must be at most 2048 bytes")
	}
	parsed, err := url.Parse(clean)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("status page logo_url must be an absolute http:// or https:// URL without credentials or fragments")
	}
	return parsed.String(), nil
}

func boundedStatusPageText(label, value string, maxBytes int, required bool) (string, error) {
	clean := strings.TrimSpace(value)
	if required && clean == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if !utf8.ValidString(clean) || len(clean) > maxBytes {
		return "", fmt.Errorf("%s must be valid UTF-8 of at most %d bytes", label, maxBytes)
	}
	for _, character := range clean {
		if unicode.IsControl(character) && character != '\t' {
			return "", fmt.Errorf("%s contains unsupported control characters", label)
		}
	}
	return clean, nil
}

func boolPointer(value bool) *bool { return &value }
