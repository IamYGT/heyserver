package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	phpsvc "github.com/IamYGT/heyserver/internal/services/php"
)

// ── PHP Versions ──────────────────────────────────────────────────────────────

// handlePHPVersions returns all installed PHP versions with status and pool counts.
// GET /api/php/versions
func handlePHPVersions(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		versions, err := phpService.DetectVersions()
		if err != nil {
			slog.Error("php versions detect failed", "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to detect PHP versions")
			return
		}
		jsonResponse(w, http.StatusOK, versions)
	}
}

type phpVersionActionService interface {
	TestFPM(version string) error
	ReloadFPM(version string) error
	RestartFPM(version string) error
}

// handlePHPVersionAction validates or controls one local PHP-FPM version.
// POST /api/php/versions/{version}/actions/{action}
func handlePHPVersionAction(phpService phpVersionActionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runLocalPHPVersionAction(w, r, phpService, r.PathValue("version"), r.PathValue("action"))
	}
}

// handlePHPVersionRestart preserves the original restart endpoint.
// POST /api/php/versions/{version}/restart
func handlePHPVersionRestart(phpService phpVersionActionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runLocalPHPVersionAction(w, r, phpService, r.PathValue("version"), "restart")
	}
}

func runLocalPHPVersionAction(w http.ResponseWriter, r *http.Request, phpService phpVersionActionService, version, action string) {
	if err := phpsvc.ValidateVersion(version); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid PHP version")
		return
	}

	var (
		err     error
		message string
	)
	switch action {
	case "test":
		err = phpService.TestFPM(version)
		message = "PHP-FPM configuration is valid"
	case "reload":
		err = phpService.ReloadFPM(version)
		message = "php" + version + "-fpm reloaded after configuration validation"
	case "restart":
		err = phpService.RestartFPM(version)
		message = "php" + version + "-fpm restarted after configuration validation"
	default:
		jsonError(w, http.StatusBadRequest, "PHP-FPM action must be test, reload, or restart")
		return
	}

	details := "PHP " + version + " " + action
	if err != nil {
		slog.Error("local php fpm action failed", "version", version, "action", action, "error", err)
		auditHostActionFailure(r, "local_php_fpm_action", details, err)
		switch {
		case errors.Is(err, phpsvc.ErrFPMConfigInvalid):
			jsonError(w, http.StatusUnprocessableEntity, phpsvc.ErrFPMConfigInvalid.Error())
		case errors.Is(err, phpsvc.ErrFPMLifecycleAction):
			jsonError(w, http.StatusBadGateway, phpsvc.ErrFPMLifecycleAction.Error())
		default:
			jsonError(w, http.StatusInternalServerError, "failed to run PHP-FPM action")
		}
		return
	}

	auditHostAction(r, "local_php_fpm_action", details)
	jsonResponse(w, http.StatusOK, map[string]string{"message": message})
}

// ── Pool Management ───────────────────────────────────────────────────────────

// handlePHPPools lists all pools across all versions (optional ?version= filter).
// GET /api/php/pools
func handlePHPPools(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.URL.Query().Get("version")
		if version != "" {
			if err := phpsvc.ValidateVersion(version); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid PHP version")
				return
			}
			pools, err := phpService.ListPools(version)
			if err != nil {
				slog.Error("php list pools failed", "version", version, "error", err)
				jsonError(w, http.StatusInternalServerError, "failed to list pools")
				return
			}
			jsonResponse(w, http.StatusOK, pools)
			return
		}
		pools, err := phpService.ListAllPools()
		if err != nil {
			slog.Error("php list all pools failed", "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to list pools")
			return
		}
		jsonResponse(w, http.StatusOK, pools)
	}
}

// handlePHPPoolGet returns the rich PoolConfig for a domain.
// GET /api/php/pools/{version}/{domain}
func handlePHPPoolGet(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		cfg, err := phpService.GetPoolConfig(version, domain)
		if err != nil {
			slog.Error("php pool get failed", "version", version, "domain", domain, "error", err)
			jsonError(w, http.StatusNotFound, "pool not found")
			return
		}
		jsonResponse(w, http.StatusOK, cfg)
	}
}

// handlePHPPoolConfigGet returns the checksum-bound raw local pool file.
// GET /api/php/pools/{version}/{domain}/config
func handlePHPPoolConfigGet(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		content, err := phpService.ReadPoolConfig(r.PathValue("version"), r.PathValue("domain"))
		if err != nil {
			writeLocalPHPPoolConfigError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, content)
	}
}

// handlePHPPoolConfigSave performs a checksum-locked raw local pool replace.
// PUT /api/php/pools/{version}/{domain}/config
func handlePHPPoolConfigSave(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, (2<<20)+(16<<10))
		var body struct {
			Content  string `json:"content"`
			Checksum string `json:"checksum"`
			Reload   bool   `json:"reload"`
		}
		if err := decodeStrictJSON(r, &body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		version := r.PathValue("version")
		domain := r.PathValue("domain")
		receipt, err := phpService.ReplacePoolConfig(r.Context(), version, domain, []byte(body.Content), body.Checksum, body.Reload)
		if err != nil {
			writeLocalPHPPoolConfigError(w, err)
			return
		}
		auditHostAction(r, "local_php_fpm_config_save", "PHP "+version+" "+domain)
		jsonResponse(w, http.StatusOK, receipt)
	}
}

func writeLocalPHPPoolConfigError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "PHP-FPM pool configuration operation failed"
	switch {
	case errors.Is(err, phpsvc.ErrPoolConfigChanged):
		status, message = http.StatusConflict, "PHP-FPM pool checksum changed; read the current configuration and retry"
	case errors.Is(err, phpsvc.ErrPoolConfigInvalid):
		status, message = http.StatusUnprocessableEntity, "PHP-FPM configuration test failed; the previous pool configuration was restored"
	case errors.Is(err, phpsvc.ErrPoolConfigReload):
		status, message = http.StatusBadGateway, "PHP-FPM reload failed; the previous pool configuration was restored"
	case errors.Is(err, phpsvc.ErrPoolConfigTooLarge):
		status, message = http.StatusRequestEntityTooLarge, "PHP-FPM pool configuration exceeds the size limit"
	case errors.Is(err, os.ErrNotExist):
		status, message = http.StatusNotFound, "PHP-FPM pool configuration was not found"
	case strings.Contains(err.Error(), "invalid PHP") || strings.Contains(err.Error(), "checksum must") || strings.Contains(err.Error(), "NUL-free UTF-8"):
		status, message = http.StatusBadRequest, err.Error()
	}
	if status == http.StatusInternalServerError {
		slog.Error("local PHP-FPM pool configuration operation failed", "error", err)
	}
	jsonError(w, status, message)
}

// handlePHPPoolSave creates or updates a pool config for a domain.
// POST /api/php/pools/{version}/{domain}
func handlePHPPoolSave(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		var cfg phpsvc.PoolConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		cfg.Version = version
		cfg.Domain = domain

		if err := phpService.SavePoolConfig(&cfg); err != nil {
			slog.Error("php pool save failed", "version", version, "domain", domain, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to save pool config")
			return
		}

		updated, err := phpService.GetPoolConfig(version, domain)
		if err != nil {
			jsonResponse(w, http.StatusOK, map[string]string{"message": "pool saved"})
			return
		}
		jsonResponse(w, http.StatusOK, updated)
	}
}

// handlePHPPoolDelete removes a pool config file.
// DELETE /api/php/pools/{version}/{domain}
func handlePHPPoolDelete(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		if err := phpService.DeletePool(version, domain); err != nil {
			slog.Error("php pool delete failed", "version", version, "domain", domain, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to delete pool")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": "pool deleted"})
	}
}

// handlePHPPoolRestart reloads PHP-FPM for the pool's version (graceful worker reload).
// POST /api/php/pools/{version}/{domain}/restart
func handlePHPPoolRestart(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		if err := phpService.RestartPool(version, domain); err != nil {
			slog.Error("php pool restart failed", "version", version, "domain", domain, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to restart pool")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": "pool reloaded"})
	}
}

// handlePHPPresets returns the list of built-in pool presets.
// GET /api/php/presets
func handlePHPPresets(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, phpsvc.GetPresets())
	}
}

// handlePHPPoolApplyPreset applies a named preset to a pool, keeping security fields.
// POST /api/php/pools/{version}/{domain}/preset
func handlePHPPoolApplyPreset(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		var req struct {
			Preset string `json:"preset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Preset == "" {
			jsonError(w, http.StatusBadRequest, "preset field required")
			return
		}

		if err := phpService.ApplyPreset(version, domain, req.Preset); err != nil {
			slog.Error("php preset apply failed", "version", version, "domain", domain, "preset", req.Preset, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to apply preset")
			return
		}

		cfg, err := phpService.GetPoolConfig(version, domain)
		if err != nil {
			jsonResponse(w, http.StatusOK, map[string]string{"message": "preset applied"})
			return
		}
		jsonResponse(w, http.StatusOK, cfg)
	}
}

// handlePHPSwitchVersion moves a domain's pool to a different PHP version.
// POST /api/php/pools/switch-version
func handlePHPSwitchVersion(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Domain      string `json:"domain"`
			FromVersion string `json:"from_version"`
			ToVersion   string `json:"to_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Domain == "" || req.FromVersion == "" || req.ToVersion == "" {
			jsonError(w, http.StatusBadRequest, "domain, from_version, and to_version are required")
			return
		}
		if err := phpsvc.ValidateVersion(req.FromVersion); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid from_version")
			return
		}
		if err := phpsvc.ValidateVersion(req.ToVersion); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid to_version")
			return
		}

		if err := phpService.SwitchDomainVersion(req.Domain, req.FromVersion, req.ToVersion); err != nil {
			slog.Error("php switch version failed", "domain", req.Domain, "from", req.FromVersion, "to", req.ToVersion, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to switch PHP version")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{
			"message": "switched " + req.Domain + " from php" + req.FromVersion + " to php" + req.ToVersion,
		})
	}
}

// handlePHPAutoTune calculates recommended pool settings for a domain.
// POST /api/php/pools/auto-tune
func handlePHPAutoTune(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Version  string `json:"version"`
			Domain   string `json:"domain"`
			MemoryMB int    `json:"memory_mb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Version == "" {
			jsonError(w, http.StatusBadRequest, "version is required")
			return
		}
		if err := phpsvc.ValidateVersion(req.Version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		maxChildren, err := phpService.CalculateMaxChildren(req.Version, req.MemoryMB)
		if err != nil {
			slog.Error("php auto-tune failed", "version", req.Version, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to calculate settings")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"version":      req.Version,
			"domain":       req.Domain,
			"max_children": maxChildren,
		})
	}
}

// ── php.ini Management ────────────────────────────────────────────────────────

// handlePHPINIGet returns the global php.ini key=value map for a version.
// GET /api/php/ini/{version}
func handlePHPINIGet(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		settings, err := phpService.GetGlobalINI(version)
		if err != nil {
			slog.Error("php ini get failed", "version", version, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to read php.ini")
			return
		}
		jsonResponse(w, http.StatusOK, settings)
	}
}

// handlePHPINISet updates a single directive in the global php.ini.
// PUT /api/php/ini/{version}
func handlePHPINISet(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		var req struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
			jsonError(w, http.StatusBadRequest, "key and value are required")
			return
		}

		if err := phpService.SetGlobalINI(version, req.Key, req.Value); err != nil {
			slog.Error("php ini set failed", "version", version, "key", req.Key, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to update php.ini")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": "php.ini updated"})
	}
}

// handlePHPDomainINIGet returns per-domain pool INI overrides.
// GET /api/php/ini/{version}/{domain}
func handlePHPDomainINIGet(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		settings, err := phpService.GetDomainINI(version, domain)
		if err != nil {
			slog.Error("php domain ini get failed", "version", version, "domain", domain, "error", err)
			jsonError(w, http.StatusNotFound, "domain pool not found or unreadable")
			return
		}
		jsonResponse(w, http.StatusOK, settings)
	}
}

// handlePHPDomainINISet applies one or more INI overrides to a domain pool.
// PUT /api/php/ini/{version}/{domain}
func handlePHPDomainINISet(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		// Accept either {"key":"val"} single or {"settings":{"k":"v"}} bulk.
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		// Prefer bulk "settings" map; fall back to direct key/value.
		var settings map[string]string
		if raw, ok := body["settings"]; ok {
			if m, ok := raw.(map[string]interface{}); ok {
				settings = make(map[string]string, len(m))
				for k, v := range m {
					settings[k] = v.(string)
				}
			}
		} else if key, ok := body["key"].(string); ok {
			val, _ := body["value"].(string)
			settings = map[string]string{key: val}
		}

		if len(settings) == 0 {
			jsonError(w, http.StatusBadRequest, "settings map or key/value required")
			return
		}

		if err := phpService.BulkSetDomainINI(version, domain, settings); err != nil {
			slog.Error("php domain ini set failed", "version", version, "domain", domain, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to update domain INI overrides")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": "domain INI overrides updated"})
	}
}

// handlePHPDomainINIReset removes a single INI override from a domain pool.
// DELETE /api/php/ini/{version}/{domain}/{key}
func handlePHPDomainINIReset(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")
		key := r.PathValue("key")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		if err := phpService.ResetDomainINI(version, domain, key); err != nil {
			slog.Error("php domain ini reset failed", "version", version, "domain", domain, "key", key, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to reset INI override")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": "override removed"})
	}
}

// handlePHPINIDiff returns directives that differ from PHP compiled-in defaults.
// GET /api/php/ini/{version}/diff
func handlePHPINIDiff(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		diffs, err := phpService.GetINIDiff(version)
		if err != nil {
			slog.Error("php ini diff failed", "version", version, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to compute ini diff")
			return
		}
		jsonResponse(w, http.StatusOK, diffs)
	}
}

// handlePHPINIDirectives returns all available directives with metadata.
// GET /api/php/ini/{version}/directives
func handlePHPINIDirectives(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		directives, err := phpService.ListINIDirectives(version)
		if err != nil {
			slog.Error("php ini directives failed", "version", version, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to list INI directives")
			return
		}
		jsonResponse(w, http.StatusOK, directives)
	}
}

// ── Extensions ────────────────────────────────────────────────────────────────

// handlePHPExtensionList returns enabled and available extensions for a version.
// GET /api/php/extensions/{version}
func handlePHPExtensionList(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		exts, err := phpService.ListExtensions(version)
		if err != nil {
			slog.Error("php extension list failed", "version", version, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to list extensions")
			return
		}
		jsonResponse(w, http.StatusOK, exts)
	}
}

// handlePHPExtensionEnable enables a PHP extension via phpenmod.
// POST /api/php/extensions/{version}/{name}/enable
func handlePHPExtensionEnable(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		name := r.PathValue("name")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		if err := phpService.EnableExtension(version, name); err != nil {
			slog.Error("php extension enable failed", "version", version, "name", name, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to enable extension")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": name + " enabled"})
	}
}

// handlePHPExtensionDisable disables a PHP extension via phpdismod.
// POST /api/php/extensions/{version}/{name}/disable
func handlePHPExtensionDisable(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		name := r.PathValue("name")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		if err := phpService.DisableExtension(version, name); err != nil {
			slog.Error("php extension disable failed", "version", version, "name", name, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to disable extension")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": name + " disabled"})
	}
}

// ── Monitoring ────────────────────────────────────────────────────────────────

// handlePHPStatusAll returns FPM status for all pools of a version.
// GET /api/php/status/{version}
func handlePHPStatusAll(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		statuses, err := phpService.GetAllPoolsStatus(version)
		if err != nil {
			slog.Error("php status all failed", "version", version, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to get pool statuses")
			return
		}
		jsonResponse(w, http.StatusOK, statuses)
	}
}

// handlePHPStatusPool returns detailed FPM status for a single pool.
// GET /api/php/status/{version}/{domain}
func handlePHPStatusPool(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		status, err := phpService.GetPoolStatus(version, domain)
		if err != nil {
			if errors.Is(err, phpsvc.ErrStatusNotConfigured) {
				jsonError(w, http.StatusUnprocessableEntity, "pool has no pm.status_path configured")
				return
			}
			slog.Error("php pool status failed", "version", version, "domain", domain, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to get pool status")
			return
		}
		jsonResponse(w, http.StatusOK, status)
	}
}

// handlePHPOPcacheGet returns OPcache statistics for a PHP version.
// GET /api/php/opcache/{version}
func handlePHPOPcacheGet(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		stats, err := phpService.GetOPcacheStatus(version)
		if err != nil {
			slog.Error("php opcache get failed", "version", version, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to get OPcache status")
			return
		}
		jsonResponse(w, http.StatusOK, stats)
	}
}

// handlePHPOPcacheReset resets the OPcache for a PHP version.
// POST /api/php/opcache/{version}/reset
func handlePHPOPcacheReset(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		if err := phpService.ResetOPcache(version); err != nil {
			slog.Error("php opcache reset failed", "version", version, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to reset OPcache")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": "OPcache reset"})
	}
}

// handlePHPErrorLog returns the last N lines of the FPM error log.
// GET /api/php/logs/{version}/error   ?lines=100
func handlePHPErrorLog(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		lines := parseQueryInt(r, "lines", 100)
		entries, err := phpService.GetErrorLog(version, lines)
		if err != nil {
			slog.Error("php error log failed", "version", version, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to read error log")
			return
		}
		jsonResponse(w, http.StatusOK, entries)
	}
}

// handlePHPSlowLog returns slow log entries for a specific pool.
// GET /api/php/logs/{version}/{domain}/slow   ?lines=50
func handlePHPSlowLog(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}
		lines := parseQueryInt(r, "lines", 50)
		entries, err := phpService.GetSlowLog(version, domain, lines)
		if err != nil {
			slog.Error("php slow log failed", "version", version, "domain", domain, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to read slow log")
			return
		}
		jsonResponse(w, http.StatusOK, entries)
	}
}

// ── Security ──────────────────────────────────────────────────────────────────

// handlePHPSecurityProfiles lists all available predefined security profiles.
// GET /api/php/security/profiles
func handlePHPSecurityProfiles(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, phpService.ListSecurityProfiles())
	}
}

// handlePHPSecurityGet returns the security score and current profile for a domain.
// GET /api/php/security/{version}/{domain}
func handlePHPSecurityGet(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		score, err := phpService.GetSecurityScore(version, domain)
		if err != nil {
			jsonError(w, http.StatusNotFound, "pool not found")
			return
		}

		profile, _ := phpService.GetDomainSecurityProfile(version, domain)

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"score":   score,
			"profile": profile,
		})
	}
}

// handlePHPSecurityApply applies a named security profile to a domain's pool.
// POST /api/php/security/{version}/{domain}
func handlePHPSecurityApply(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		domain := r.PathValue("domain")

		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		var req struct {
			Profile string `json:"profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Profile == "" {
			jsonError(w, http.StatusBadRequest, "profile field required")
			return
		}

		if err := phpService.ApplySecurityProfile(version, domain, req.Profile); err != nil {
			slog.Error("php security apply failed", "version", version, "domain", domain, "profile", req.Profile, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to apply security profile")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"message": "security profile applied"})
	}
}

// ── Composer ──────────────────────────────────────────────────────────────────

// handlePHPComposerVersion returns the installed Composer version string.
// GET /api/php/composer/version
func handlePHPComposerVersion(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ver, err := phpService.ComposerVersion()
		if err != nil {
			slog.Error("composer version check failed", "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to get Composer version")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"version": ver})
	}
}

// handlePHPComposerInstall runs composer install in the given project directory.
// POST /api/php/composer/{version}/install
func handlePHPComposerInstall(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		var req struct {
			ProjectDir string `json:"project_dir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectDir == "" {
			jsonError(w, http.StatusBadRequest, "project_dir is required")
			return
		}

		result, err := phpService.ComposerInstall(req.ProjectDir, version)
		if err != nil {
			slog.Error("composer install failed", "version", version, "dir", req.ProjectDir, "error", err)
			if errors.Is(err, phpsvc.ErrComposerProjectPath) {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, "composer install failed")
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}

// handlePHPComposerUpdate runs composer update in the given project directory.
// POST /api/php/composer/{version}/update
func handlePHPComposerUpdate(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		var req struct {
			ProjectDir string `json:"project_dir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectDir == "" {
			jsonError(w, http.StatusBadRequest, "project_dir is required")
			return
		}

		result, err := phpService.ComposerUpdate(req.ProjectDir, version)
		if err != nil {
			slog.Error("composer update failed", "version", version, "dir", req.ProjectDir, "error", err)
			if errors.Is(err, phpsvc.ErrComposerProjectPath) {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, "composer update failed")
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}

// handlePHPComposerRequire adds a package via composer require.
// POST /api/php/composer/{version}/require
func handlePHPComposerRequire(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		var req struct {
			ProjectDir string `json:"project_dir"`
			Package    string `json:"package"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectDir == "" || req.Package == "" {
			jsonError(w, http.StatusBadRequest, "project_dir and package are required")
			return
		}

		result, err := phpService.ComposerRequire(req.ProjectDir, version, req.Package)
		if err != nil {
			slog.Error("composer require failed", "version", version, "package", req.Package, "error", err)
			if errors.Is(err, phpsvc.ErrComposerProjectPath) {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, "composer require failed")
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}

// handlePHPComposerOutdated checks for outdated packages.
// POST /api/php/composer/{version}/outdated
func handlePHPComposerOutdated(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if err := phpsvc.ValidateVersion(version); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid PHP version")
			return
		}

		var req struct {
			ProjectDir string `json:"project_dir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProjectDir == "" {
			jsonError(w, http.StatusBadRequest, "project_dir is required")
			return
		}

		packages, err := phpService.ComposerOutdated(req.ProjectDir, version)
		if err != nil {
			slog.Error("composer outdated failed", "version", version, "dir", req.ProjectDir, "error", err)
			if errors.Is(err, phpsvc.ErrComposerProjectPath) {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, "composer outdated check failed")
			return
		}
		jsonResponse(w, http.StatusOK, packages)
	}
}

// ── Legacy handlers kept for backwards compatibility ──────────────────────────
// These were registered as PUT /api/php/pools/{version}/{pool} and
// POST /api/php/pools in older versions of the router. They delegate to the
// richer service methods above.

// handlePHPPoolSaveLegacy wraps the legacy UpdatePool service method.
// PUT /api/php/pools/{version}/{pool}
func handlePHPPoolSaveLegacy(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		poolName := r.PathValue("pool")

		var req phpsvc.UpdatePoolRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		pool, err := phpService.UpdatePool(version, poolName, req)
		if err != nil {
			slog.Error("php pool update failed", "version", version, "pool", poolName, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to update pool")
			return
		}
		jsonResponse(w, http.StatusOK, pool)
	}
}

// handlePHPPoolCreateLegacy wraps the legacy CreatePool service method.
// POST /api/php/pools  (body: CreatePoolRequest)
func handlePHPPoolCreateLegacy(phpService *phpsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req phpsvc.CreatePoolRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Name == "" || req.Version == "" || req.User == "" {
			jsonError(w, http.StatusBadRequest, "name, version, and user are required")
			return
		}
		pool, err := phpService.CreatePool(req)
		if err != nil {
			slog.Error("php pool create failed", "version", req.Version, "name", req.Name, "error", err)
			jsonError(w, http.StatusInternalServerError, "failed to create pool")
			return
		}
		jsonResponse(w, http.StatusCreated, pool)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// parseQueryInt reads an integer query parameter, returning defaultVal on missing/invalid.
func parseQueryInt(r *http.Request, key string, defaultVal int) int {
	if s := r.URL.Query().Get(key); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}
