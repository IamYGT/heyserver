package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/IamYGT/heyserver/internal/services/settings"
)

const portableSettingsRequestLimit = 128 << 10

type portableSettingsImportRequest struct {
	Bundle    settings.PortableBundle `json:"bundle"`
	Confirmed bool                    `json:"confirmed"`
}

func handlePortableSettingsExport(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			jsonError(w, http.StatusServiceUnavailable, "settings not initialized")
			return
		}
		bundle, err := svc.ExportPortable()
		if err != nil {
			slog.Error("portable settings export error", "err", err)
			jsonError(w, http.StatusInternalServerError, "portable configuration export failed")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="hserver-portable-config-v1.json"`)
		auditHostAction(r, "portable_config_export", fmt.Sprintf("exported %d allowlisted settings", len(bundle.Settings)))
		jsonResponse(w, http.StatusOK, bundle)
	}
}

func handlePortableSettingsPreview(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			jsonError(w, http.StatusServiceUnavailable, "settings not initialized")
			return
		}
		var bundle settings.PortableBundle
		if !decodePortableSettingsRequest(w, r, &bundle) {
			auditHostAction(r, "portable_config_preview", "rejected invalid portable configuration request")
			return
		}
		preview, err := svc.PreviewPortable(bundle)
		if err != nil {
			auditHostAction(r, "portable_config_preview", fmt.Sprintf("rejected bundle with %d settings", len(bundle.Settings)))
			writePortableSettingsError(w, err)
			return
		}
		auditHostAction(r, "portable_config_preview", fmt.Sprintf("previewed %d allowlisted settings; %d changed", preview.ImportedKeys, preview.ChangedKeys))
		jsonResponse(w, http.StatusOK, preview)
	}
}

func handlePortableSettingsImport(svc *settings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			jsonError(w, http.StatusServiceUnavailable, "settings not initialized")
			return
		}
		var body portableSettingsImportRequest
		if !decodePortableSettingsRequest(w, r, &body) {
			auditHostAction(r, "portable_config_import", "rejected invalid portable configuration request")
			return
		}
		preview, err := svc.ImportPortable(body.Bundle, body.Confirmed)
		if err != nil {
			auditHostAction(r, "portable_config_import", fmt.Sprintf("rejected bundle with %d settings", len(body.Bundle.Settings)))
			writePortableSettingsError(w, err)
			return
		}
		auditHostAction(r, "portable_config_import", fmt.Sprintf("applied %d of %d allowlisted settings", preview.ChangedKeys, preview.ImportedKeys))
		jsonResponse(w, http.StatusOK, preview)
	}
}

func decodePortableSettingsRequest(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, portableSettingsRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			jsonError(w, http.StatusRequestEntityTooLarge, "portable configuration request is too large")
		} else {
			jsonError(w, http.StatusBadRequest, "invalid portable configuration request")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		jsonError(w, http.StatusBadRequest, "invalid portable configuration request")
		return false
	}
	return true
}

func writePortableSettingsError(w http.ResponseWriter, err error) {
	if errors.Is(err, settings.ErrPortableSchema) || errors.Is(err, settings.ErrPortableSetting) || errors.Is(err, settings.ErrPortableEmpty) || errors.Is(err, settings.ErrPortableConfirmation) {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	slog.Error("portable settings handler error", "err", err)
	jsonError(w, http.StatusInternalServerError, "portable configuration operation failed")
}
