// Command gen-api-routes generates the complete public route inventory from the
// authoritative Go route manifest.
//
//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

type route struct {
	method string
	path   string
	auth   string
}

var routePattern = regexp.MustCompile(`\{"([A-Z]+)", "([^"]+)", (Route[A-Za-z]+)\},`)

func main() {
	check := flag.Bool("check", false, "fail when the generated inventory differs from the committed file")
	flag.Parse()

	root := os.Getenv("HSERVER_ROOT")
	if root == "" {
		root = "."
	}
	manifestPath := filepath.Join(root, "internal/api/routes_manifest.go")
	markdownPath := filepath.Join(root, "docs/api-routes.md")
	openAPIPath := filepath.Join(root, "docs/openapi.json")

	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		fail("read route manifest", err)
	}
	routes := extractRoutes(manifest)
	if len(routes) == 0 {
		fail("extract route manifest", fmt.Errorf("no routes found"))
	}
	if err := validateRoutes(routes); err != nil {
		fail("validate route manifest", err)
	}
	markdown := renderMarkdown(routes)
	openAPI := renderOpenAPI(routes)

	if *check {
		checkGenerated(markdownPath, markdown)
		checkGenerated(openAPIPath, openAPI)
		fmt.Printf("API route inventory is current (%d routes)\n", len(routes))
		return
	}

	writeGenerated(markdownPath, markdown)
	writeGenerated(openAPIPath, openAPI)
	fmt.Printf("generated %s and %s (%d routes)\n", markdownPath, openAPIPath, len(routes))
}

func extractRoutes(source []byte) []route {
	matches := routePattern.FindAllSubmatch(source, -1)
	routes := make([]route, 0, len(matches))
	for _, match := range matches {
		routes = append(routes, route{
			method: string(match[1]),
			path:   string(match[2]),
			auth:   authLabel(string(match[3])),
		})
	}
	return routes
}

func authLabel(value string) string {
	switch value {
	case "RoutePublic":
		return "Public"
	case "RouteProtected":
		return "Authenticated"
	case "RouteManager":
		return "Manager or admin"
	case "RouteAdmin":
		return "Admin"
	case "RouteAgent":
		return "Managed-node agent"
	case "RouteInternalCron":
		return "Local internal trigger"
	default:
		return value
	}
}

func renderMarkdown(routes []route) []byte {
	var output bytes.Buffer
	fmt.Fprintln(&output, "# Heyserver API Route Inventory")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "> Code generated from `internal/api/routes_manifest.go`; do not edit by hand.")
	fmt.Fprintln(&output, "> Regenerate with `make gen-api-docs`.")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "Total routes: **%d**\n\n", len(routes))
	fmt.Fprintln(&output, "The inventory is the complete routing and access-level contract. Request and")
	fmt.Fprintln(&output, "response guidance for common workflows remains in the curated")
	fmt.Fprintln(&output, "[API reference](api-reference.md).")
	fmt.Fprintln(&output, "The generated [OpenAPI 3.1 contract](openapi.json) exposes the same routes,")
	fmt.Fprintln(&output, "path parameters, and access boundaries to clients and development tools.")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "| Method | Path | Access |")
	fmt.Fprintln(&output, "| --- | --- | --- |")
	for _, item := range routes {
		fmt.Fprintf(&output, "| `%s` | `%s` | %s |\n", item.method, item.path, item.auth)
	}
	return output.Bytes()
}

func renderOpenAPI(routes []route) []byte {
	paths := make(map[string]map[string]any, len(routes))
	for _, item := range routes {
		operation := map[string]any{
			"operationId":      operationID(item),
			"summary":          item.method + " " + item.path,
			"description":      "Generated route and access contract. Request and response payload schemas remain in the curated API reference until they are promoted into this specification.",
			"tags":             []string{routeTag(item.path)},
			"x-hserver-access": item.auth,
			"responses": map[string]any{
				"default": map[string]any{
					"description": "Endpoint response. Consult docs/api-reference.md for curated payload and status details.",
				},
			},
		}
		if parameters := pathParameters(item.path); len(parameters) > 0 {
			operation["parameters"] = parameters
		}
		if item.auth == "Managed-node agent" {
			parameters, _ := operation["parameters"].([]map[string]any)
			operation["parameters"] = append(parameters, map[string]any{
				"name":        "X-HServer-Node-ID",
				"in":          "header",
				"required":    true,
				"description": "Enrolled managed-node identifier.",
				"schema":      map[string]any{"type": "string"},
			})
		}
		if item.auth == "Local internal trigger" {
			operation["x-hserver-loopback-only"] = true
		}
		promotePayloadContract(item, operation)
		operation["security"] = routeSecurity(item.auth)
		pathItem := paths[item.path]
		if pathItem == nil {
			pathItem = make(map[string]any)
			paths[item.path] = pathItem
		}
		pathItem[strings.ToLower(item.method)] = operation
	}

	document := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Heyserver API",
			"version":     "0.1.0",
			"description": "Generated provider-neutral route, path-parameter, and access-level contract for the self-hosted Heyserver API. Payload schemas are added incrementally from the curated API reference.",
		},
		"servers": []map[string]any{{
			"url":         "/",
			"description": "Current self-hosted Heyserver installation",
		}},
		"paths": paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"panelBearer": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
					"description":  "Heyserver panel JWT supplied by an API client.",
				},
				"panelSession": map[string]any{
					"type":        "apiKey",
					"in":          "cookie",
					"name":        "hserver_token",
					"description": "Same-origin HttpOnly panel session cookie.",
				},
				"agentBearer": map[string]any{
					"type":        "http",
					"scheme":      "bearer",
					"description": "Enrollment-issued managed-node token; X-HServer-Node-ID is also required.",
				},
				"cronSecret": map[string]any{
					"type":        "apiKey",
					"in":          "header",
					"name":        "X-Cron-Secret",
					"description": "Installation-owned secret accepted only by the local internal trigger boundary.",
				},
			},
			"schemas": promotedSchemas(),
		},
		"x-hserver-contract-version": 71,
		"x-hserver-route-count":      len(routes),
		"x-hserver-schema-count":     len(promotedSchemas()),
	}
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		fail("encode OpenAPI document", err)
	}
	return append(content, '\n')
}

func promotePayloadContract(item route, operation map[string]any) {
	switch item.method + " " + item.path {
	case "GET /api/health":
		operation["description"] = "Return the running panel release identity and process uptime. An uninitialized settings boundary is unavailable rather than a false healthy response."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Running panel health and release identity.", "HealthStatus"),
			"503": jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
		}
	case "GET /api/onboarding":
		operation["description"] = "Return the authenticated installation onboarding state with a normalized step from 0 through 5."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Current installation onboarding state.", "OnboardingState"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The onboarding state could not be read.", "ErrorResponse"),
		}
	case "GET /api/integrations/catalog":
		operation["description"] = "Return the schema-v1 optional-integration catalog as metadata only. The response documents configuration names, secret names, status mappings, and evidence references; it does not perform live health probes or report live integration health. The relative $schema value is a local source marker and is not promised as a dereferenceable URI."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Schema-v1 integration metadata catalog; it contains at least 15 required core entries; additive entries allowed.", "IntegrationCatalog"),
			"401": jsonResponseSchema("The authenticated panel session is absent or invalid.", "ErrorResponse"),
			"500": jsonResponseSchema("The embedded integration catalog could not be loaded.", "ErrorResponse"),
		}
	case "GET /api/integrations/status":
		operation["description"] = "Return a fresh schema-v1 local-only, read-only aggregate for every local integration entry in the catalog. The fifteen required core probes are process.pm2/pm2_inventory, cloudflare.dns/cloudflare_zone_list, container.docker/docker_info, web.nginx/nginx_readiness, firewall.ufw/ufw_readiness, tls.certbot/certbot_readiness, dns.bind9/bind9_readiness, runtime.php_fpm/php_fpm_readiness, database.local/database_readiness, storage.smartmontools/smartmontools_readiness, stalwart.mail/stalwart_readiness, mail.access/mail_access_readiness, backup.gdrive/gdrive_readiness, backup.snapshot.restic/restic_readiness, and notification.delivery/notification_readiness. Reviewed additive catalog entries may contribute explicitly code-owned probes through the panel's compile-time registration seam; entries without a registered local probe remain unprobed. Healthy always requires a fresh successful observation; notification delivery remains unavailable until a persisted fresh successful delivery receipt exists. Failures and timeouts remain item-level results in HTTP 200. Only an unavailable embedded catalog returns HTTP 500. This endpoint does not report live managed-node status, and raw provider errors, command output, paths, and secrets never cross the API boundary."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Fresh schema-v1 local integration status aggregate with safe per-item results for registered catalog probes and an explicit unprobed list for catalog entries without a code-owned probe.", "IntegrationStatusResponse"),
			"401": jsonResponseSchema("The authenticated panel session is absent or invalid.", "ErrorResponse"),
			"500": jsonResponseSchema("The local integration status aggregate could not be collected.", "ErrorResponse"),
		}
	case "POST /api/onboarding":
		operation["description"] = "Atomically persist the complete administrator-selected onboarding state. Both fields are required; unknown, trailing, or oversized JSON is rejected."
		operation["requestBody"] = jsonRequestSchema("Complete onboarding state replacement.", "OnboardingSetRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Onboarding state saved.", "OnboardingSaveResult"),
			"400":     jsonResponseSchema("The strict body, completion state, or bounded step is invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 4096 bytes.", "ErrorResponse"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The onboarding state could not be saved.", "ErrorResponse"),
		}
	case "POST /api/auth/login":
		operation["description"] = "Validate one local panel identity. Success returns either a no-store session token and credential-free user or an explicit TOTP challenge. Unknown, trailing, or oversized JSON is rejected, and failed attempts share the route's active per-IP limiter."
		operation["requestBody"] = jsonRequestSchema("Email address and password.", "AuthLoginRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Authenticated session or explicit TOTP challenge.", "AuthLoginResult"),
			"400":     jsonResponseSchema("The strict body, email, or password bounds are invalid.", "ErrorResponse"),
			"401":     jsonResponseSchema("The credentials are invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 4096 bytes.", "ErrorResponse"),
			"429":     jsonResponseSchema("The per-IP login limit or temporary failure ban is active.", "ErrorResponse"),
			"default": jsonResponseSchema("The session token could not be generated.", "ErrorResponse"),
		}
	case "POST /api/auth/totp-verify":
		operation["description"] = "Complete a TOTP-protected login with the same bounded email/password identity and an exact six-digit code. The response and cookie use the canonical no-store session policy."
		operation["requestBody"] = jsonRequestSchema("Email, password, and current six-digit TOTP code.", "AuthTOTPLoginRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Authenticated no-store panel session.", "AuthLoginResponse"),
			"400":     jsonResponseSchema("The strict request fields or TOTP code shape are invalid.", "ErrorResponse"),
			"401":     jsonResponseSchema("The credentials or TOTP code are invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 4096 bytes.", "ErrorResponse"),
			"429":     jsonResponseSchema("The per-IP login limit or temporary failure ban is active.", "ErrorResponse"),
			"default": jsonResponseSchema("The session token could not be generated.", "ErrorResponse"),
		}
	case "POST /api/auth/2fa/recovery":
		operation["description"] = "Consume one exact single-use recovery_code for a TOTP-enabled account after validating its email and password. Lowercase recovery codes normalize to uppercase; the canonical no-store session cookie policy is used."
		operation["requestBody"] = jsonRequestSchema("Email, password, and single-use recovery code.", "AuthRecoveryRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Authenticated no-store panel session.", "AuthLoginResponse"),
			"400":     jsonResponseSchema("The strict request fields or recovery-code shape are invalid.", "ErrorResponse"),
			"401":     jsonResponseSchema("The credentials or recovery code are invalid, already used, or not available for this account.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 4096 bytes.", "ErrorResponse"),
			"429":     jsonResponseSchema("The per-IP login limit or temporary failure ban is active.", "ErrorResponse"),
			"default": jsonResponseSchema("The recovery code could not be checked or the session token could not be generated.", "ErrorResponse"),
		}
	case "POST /api/auth/logout":
		operation["description"] = "Clear the canonical local session cookie. The request body must be empty."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Local session cookie cleared.", "AuthLogoutResponse"),
			"400": jsonResponseSchema("The request contains a body.", "ErrorResponse"),
		}
	case "GET /api/auth/me":
		operation["description"] = "Return the current credential-free panel user from fresh persisted state. Password hashes and TOTP secrets are never serialized."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Current credential-free panel user.", "User"),
			"401": jsonResponseSchema("The session is absent, invalid, or no longer maps to a panel user.", "ErrorResponse"),
		}
	case "GET /api/auth/2fa/status":
		operation["description"] = "Return whether TOTP is enabled or a persisted setup is pending, without exposing the TOTP secret or recovery codes."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Current no-store TOTP state.", "AuthTOTPStatus"),
			"401": jsonResponseSchema("The session is absent, invalid, or no longer maps to a panel user.", "ErrorResponse"),
		}
	case "POST /api/auth/2fa/setup":
		operation["description"] = "Start TOTP setup for the current user and return the new secret, QR payload, and eight single-display recovery codes. The request body must be empty and the response is never cacheable."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("No-store TOTP enrollment material displayed once.", "AuthTOTPSetup"),
			"400":     jsonResponseSchema("The request contains a body.", "ErrorResponse"),
			"401":     jsonResponseSchema("The session is absent, invalid, or no longer maps to a panel user.", "ErrorResponse"),
			"409":     jsonResponseSchema("TOTP is already enabled for the current user.", "ErrorResponse"),
			"default": jsonResponseSchema("TOTP enrollment material could not be created or persisted.", "ErrorResponse"),
		}
	case "POST /api/auth/2fa/verify":
		operation["description"] = "Verify one exact six-digit code against the persisted pending secret and enable TOTP. Unknown, trailing, or oversized JSON is rejected."
		operation["requestBody"] = jsonRequestSchema("Current six-digit TOTP code.", "AuthTOTPCodeRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("TOTP enabled.", "AuthTOTPEnabledResponse"),
			"400":     jsonResponseSchema("The strict code body is invalid or setup has not started.", "ErrorResponse"),
			"401":     jsonResponseSchema("The session, user, or TOTP code is invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 4096 bytes.", "ErrorResponse"),
			"default": jsonResponseSchema("TOTP could not be enabled.", "ErrorResponse"),
		}
	case "POST /api/auth/2fa/disable":
		operation["description"] = "Verify one exact six-digit code against the active secret before clearing TOTP from the current user. Unknown, trailing, or oversized JSON is rejected."
		operation["requestBody"] = jsonRequestSchema("Current six-digit TOTP code.", "AuthTOTPCodeRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("TOTP disabled.", "AuthTOTPDisabledResponse"),
			"400":     jsonResponseSchema("The strict code body is invalid or TOTP is not enabled.", "ErrorResponse"),
			"401":     jsonResponseSchema("The session, user, or TOTP code is invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 4096 bytes.", "ErrorResponse"),
			"default": jsonResponseSchema("TOTP could not be disabled.", "ErrorResponse"),
		}
	case "GET /api/settings":
		operation["description"] = "Return only validated installation-portable settings editable through the generic panel form. Internal service records and credential-bearing values remain behind dedicated masked APIs."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Validated editable panel settings; the object is empty when none are configured.", "SettingsValues"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Editable settings could not be read.", "ErrorResponse"),
		}
	case "PUT /api/settings":
		operation["description"] = "Atomically update one or more allowlisted installation-portable settings. Internal service records, onboarding state, unknown keys, invalid values, trailing JSON, and oversized bodies are rejected."
		operation["requestBody"] = jsonRequestSchema("One or more validated editable setting replacements.", "SettingsUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Editable settings saved.", "SettingsSaveResult"),
			"400":     jsonResponseSchema("The strict body, setting key, or setting value is invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 131072 bytes.", "ErrorResponse"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Editable settings could not be saved.", "ErrorResponse"),
		}
	case "POST /api/uptime/monitors":
		operation["description"] = "Create one active uptime monitor. The strict JSON body accepts HTTP, TCP, ping, or DNS targets, applies persisted interval and timeout defaults when omitted, and validates target-specific fields, retry bounds, alert channels, status-code ranges, and text byte limits before persistence."
		operation["requestBody"] = jsonRequestSchema("Strict typed uptime monitor creation request; the body is limited to 131072 bytes.", "UptimeMonitorCreateRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Created active uptime monitor.", "UptimeMonitor"),
			"400":     jsonResponseSchema("The strict body, request size, target, type-specific fields, ranges, or text bounds are invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The uptime engine is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The uptime monitor could not be created.", "ErrorResponse"),
		}
	case "PUT /api/uptime/monitors/{id}":
		operation["description"] = "Overlay the provided mutable fields onto one existing uptime monitor, then validate and normalize the complete merged monitor. Omitted fields remain unchanged; explicit empty strings clear nullable text or target fields when the resulting type remains valid. Monitor identity, creation time, and observed runtime statistics remain server-owned."
		operation["requestBody"] = jsonRequestSchema("Strict partial uptime monitor update with at least one mutable field; the body is limited to 131072 bytes.", "UptimeMonitorUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Updated uptime monitor.", "UptimeMonitor"),
			"400":     jsonResponseSchema("The monitor identifier, strict body, request size, merged target, type-specific fields, ranges, or text bounds are invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The uptime monitor does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The uptime engine is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The uptime monitor could not be updated.", "ErrorResponse"),
		}
	case "GET /api/uptime/settings":
		operation["description"] = "Return the effective uptime settings object with exactly five string-valued keys: retention days, compaction threshold, default interval, default timeout, and default notification channel IDs. Missing values use installation defaults; persisted values and cross-field constraints are validated before being returned."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Effective uptime settings.", "UptimeSettings"),
			"503":     jsonResponseSchema("The uptime settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Uptime settings could not be read.", "ErrorResponse"),
		}
	case "PUT /api/uptime/settings":
		operation["description"] = "Merge one or more strict uptime setting replacements into the effective five-key policy, normalize decimal values and notification IDs, and validate retention/compaction and interval/timeout relationships before atomic persistence. Unknown keys, empty updates, malformed JSON, invalid ranges, and oversized bodies are rejected."
		operation["requestBody"] = jsonRequestSchema("Strict partial uptime settings update; values are decimal strings and default channels is a JSON string array.", "UptimeSettingsUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Effective uptime settings after the atomic update.", "UptimeSettings"),
			"400":     jsonResponseSchema("The strict body, setting value, JSON channel list, cross-field relationship, or request size is invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The uptime settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Uptime settings could not be updated.", "ErrorResponse"),
		}
	case "GET /api/uptime/status-pages":
		operation["description"] = "List persisted uptime status pages with their ordered monitor mappings. An empty inventory is returned as a JSON array rather than null."
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Uptime status-page inventory.", "UptimeStatusPage"),
			"503":     jsonResponseSchema("The uptime engine is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Uptime status pages could not be read.", "ErrorResponse"),
		}
	case "POST /api/uptime/status-pages":
		operation["description"] = "Create one uptime status page after strict validation of the lowercase slug, title and description byte bounds, theme, history window, optional credential-free HTTP(S) logo URL, visibility, and at most 128 unique existing monitors. Omitted theme, history_days, and is_public use auto, 90 days, and public defaults."
		operation["requestBody"] = jsonRequestSchema("Strict status-page creation request; the body is limited to 65536 bytes.", "UptimeStatusPageCreateRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Created uptime status page.", "UptimeStatusPage"),
			"400":     jsonResponseSchema("The strict body, slug, text, theme, history window, logo URL, monitor mapping, or request size is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("The status-page slug already exists.", "ErrorResponse"),
			"503":     jsonResponseSchema("The uptime engine is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The uptime status page could not be created.", "ErrorResponse"),
		}
	case "PUT /api/uptime/status-pages/{id}":
		operation["description"] = "Strictly overlay the provided uptime status-page fields onto one existing page. Omitted fields, including is_public and monitors, remain unchanged; explicit empty description or logo_url values clear those optional fields, and an explicit empty monitors array clears mappings. Supplied monitor mappings are rewritten in request order with server-assigned sort_order values."
		operation["requestBody"] = jsonRequestSchema("Strict partial status-page update; omitted fields remain unchanged and the body is limited to 65536 bytes.", "UptimeStatusPageUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Updated uptime status page.", "UptimeStatusPage"),
			"400":     jsonResponseSchema("The status-page identifier, strict partial body, slug, text, theme, history window, logo URL, monitor mapping, or request size is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The uptime status page does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The replacement slug belongs to another status page.", "ErrorResponse"),
			"503":     jsonResponseSchema("The uptime engine is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The uptime status page could not be replaced.", "ErrorResponse"),
		}
	case "DELETE /api/uptime/status-pages/{id}":
		operation["description"] = "Delete one exact uptime status page. Missing pages return not found rather than a false deletion receipt."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Uptime status page deleted.", "DeleteStatus"),
			"400":     jsonResponseSchema("The status-page identifier is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The uptime status page does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The uptime engine is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The uptime status page could not be deleted.", "ErrorResponse"),
		}
	case "GET /api/settings/{key}":
		operation["description"] = "Return one allowlisted installation-portable setting. Internal records and unknown keys return not found instead of exposing their existence or value."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Editable setting value; an unset allowlisted key has an empty value.", "SettingValueResponse"),
			"404":     jsonResponseSchema("The key is not part of the editable settings contract.", "ErrorResponse"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The editable setting could not be read.", "ErrorResponse"),
		}
	case "DELETE /api/settings/{key}":
		operation["description"] = "Delete one allowlisted installation-portable setting. The request body must be empty; internal service records cannot be addressed through this endpoint."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Editable setting deleted.", "SettingsDeleteResult"),
			"400":     jsonResponseSchema("The request contains a body.", "ErrorResponse"),
			"404":     jsonResponseSchema("The key is not part of the editable settings contract.", "ErrorResponse"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The editable setting could not be deleted.", "ErrorResponse"),
		}
	case "GET /api/settings/portable":
		operation["description"] = "Export a no-store schema-v1 bundle containing only currently valid installation-portable settings. Credential-bearing and internal records are never included."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Downloadable schema-v1 portable configuration bundle.", "PortableSettingsBundle"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Portable configuration export failed.", "ErrorResponse"),
		}
	case "POST /api/settings/portable/preview":
		operation["description"] = "Validate a complete schema-v1 portable bundle and return deterministic changes without mutating the installation. Unknown, trailing, and oversized JSON is rejected."
		operation["requestBody"] = jsonRequestSchema("Schema-v1 portable configuration bundle to preview.", "PortableSettingsBundleRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Validated non-mutating portable configuration comparison.", "PortableSettingsPreview"),
			"400":     jsonResponseSchema("The strict bundle schema or any portable setting is invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 131072 bytes.", "ErrorResponse"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Portable configuration preview failed.", "ErrorResponse"),
		}
	case "POST /api/settings/portable/import":
		operation["description"] = "Apply one previously reviewed schema-v1 portable bundle only with explicit confirmation. The same validation runs again before the atomic settings update."
		operation["requestBody"] = jsonRequestSchema("Confirmed schema-v1 portable configuration import.", "PortableSettingsImportRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Applied portable configuration comparison.", "PortableSettingsPreview"),
			"400":     jsonResponseSchema("The strict bundle, confirmation, or any portable setting is invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 131072 bytes.", "ErrorResponse"),
			"429":     jsonResponseSchema("The authenticated mutation rate limit is active.", "ErrorResponse"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Portable configuration import failed.", "ErrorResponse"),
		}
	case "GET /api/users":
		operation["description"] = "List panel users without password hashes or TOTP secrets. Pagination defaults to 20 and accepts up to 200 records."
		operation["parameters"] = []map[string]any{
			{
				"name": "limit", "in": "query", "required": false,
				"description": "Requested page size; valid values are 1 through 200.",
				"schema":      map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 20},
			},
			{
				"name": "offset", "in": "query", "required": false,
				"description": "Non-negative result offset.",
				"schema":      map[string]any{"type": "integer", "minimum": 0, "default": 0},
			},
		}
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Paginated panel-user inventory.", "UserListResponse"),
			"default": jsonResponseSchema("The user inventory could not be read.", "ErrorResponse"),
		}
	case "POST /api/users":
		operation["description"] = "Create one panel user. The role defaults to viewer when omitted; unknown or trailing JSON is rejected."
		operation["requestBody"] = jsonRequestSchema("Identity, initial password, and optional role.", "UserCreateRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Panel user created without credential material in the response.", "User"),
			"400":     jsonResponseSchema("The body, identity, password, or role is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("The email address is already in use.", "ErrorResponse"),
			"default": jsonResponseSchema("The panel user could not be created.", "ErrorResponse"),
		}
	case "PUT /api/users/{id}":
		operation["description"] = "Strictly and atomically update at least one panel-user field. Every supplied field is validated before persistence; unknown or trailing JSON is rejected, and the final administrator cannot be demoted."
		operation["requestBody"] = jsonRequestSchema("One or more complete replacement fields.", "UserUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Updated panel user without credential material.", "User"),
			"400":     jsonResponseSchema("The user ID, body, supplied field, password, or role is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The panel user does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The email address is already in use or the mutation would remove the final administrator role.", "ErrorResponse"),
			"default": jsonResponseSchema("The panel user could not be updated.", "ErrorResponse"),
		}
	case "DELETE /api/users/{id}":
		operation["description"] = "Delete one other panel user while preserving at least one administrator account. Administrators cannot delete their own current account."
		operation["responses"] = map[string]any{
			"204":     map[string]any{"description": "Panel user deleted; response body is empty."},
			"400":     jsonResponseSchema("The user ID is invalid or identifies the current administrator.", "ErrorResponse"),
			"404":     jsonResponseSchema("The panel user does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The mutation would delete the final administrator account.", "ErrorResponse"),
			"default": jsonResponseSchema("The panel user could not be deleted.", "ErrorResponse"),
		}
	case "GET /api/backups":
		operation["description"] = "List completed, invalid, and orphaned local backup artifacts with storage pressure and artifact-health totals."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Local backup inventory and storage summary.", "BackupListResponse"),
			"default": jsonResponseSchema("The inventory could not be read.", "ErrorResponse"),
		}
	case "POST /api/backups":
		operation["description"] = "Start one local full, database, or files backup. File-bearing backups use only the installation-owned vhost root and optional observed portable site identities."
		operation["requestBody"] = jsonRequestSchema("Backup scope, observed site identities, compression, and retention options.", "BackupCreateRequest", true)
		operation["responses"] = map[string]any{
			"202":     jsonResponseSchema("Backup job accepted.", "AsyncJobAccepted"),
			"400":     jsonResponseSchema("The body, backup options, vhost root, or observed site identities are invalid or unavailable.", "ErrorResponse"),
			"409":     jsonResponseSchema("Another local backup is already running.", "ErrorResponse"),
			"default": jsonResponseSchema("The backup request could not be accepted.", "ErrorResponse"),
		}
	case "POST /api/backups/restore/{id}":
		operation["description"] = "Start a restore from a completed local artifact. Use the validation endpoint before presenting or automating confirmation."
		operation["responses"] = map[string]any{
			"202":     jsonResponseSchema("Restore job accepted.", "AsyncJobAccepted"),
			"400":     jsonResponseSchema("The backup identifier is missing or invalid.", "ErrorResponse"),
			"default": jsonResponseSchema("The restore request could not be accepted.", "ErrorResponse"),
		}
	case "GET /api/backups/restore/{id}/validate":
		operation["description"] = "Read and validate the complete local artifact without mutating its restore target. The response declares database recovery and file rollback capabilities."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Artifact validation and restore capability boundary.", "RestoreValidation"),
			"400":     jsonResponseSchema("The backup identifier is missing or invalid.", "ErrorResponse"),
			"422":     jsonResponseSchema("The artifact failed complete validation.", "ErrorResponse"),
			"default": jsonResponseSchema("The artifact could not be validated.", "ErrorResponse"),
		}
	case "GET /api/backups/schedules":
		operation["description"] = "List Heyserver-managed backup schedules observed in the panel service user's crontab. Retention values represent artifact counts, not elapsed days."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed managed backup schedules.", "BackupScheduleListResponse"),
			"503":     jsonResponseSchema("The service user's crontab could not be observed safely.", "ErrorResponse"),
			"default": jsonResponseSchema("Backup schedules could not be read.", "ErrorResponse"),
		}
	case "POST /api/backups/schedules":
		operation["description"] = "Add or replace one Heyserver-managed backup schedule after validating an exact cron expression or a UI frequency/time pair."
		operation["requestBody"] = jsonRequestSchema("Exact schedule source, backup scope, and retention count.", "BackupScheduleSetRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Backup schedule saved.", "BackupScheduleMutationResult"),
			"400":     jsonResponseSchema("The body, schedule source, backup scope, or retention count is invalid or ambiguous.", "ErrorResponse"),
			"503":     jsonResponseSchema("The service user's crontab could not be observed or updated safely.", "ErrorResponse"),
			"default": jsonResponseSchema("The backup schedule could not be saved.", "ErrorResponse"),
		}
	case "DELETE /api/backups/schedules":
		operation["description"] = "Delete the exact Heyserver-managed schedule line previously observed by the client while preserving unrelated crontab entries."
		operation["requestBody"] = jsonRequestSchema("Exact observed managed schedule identity.", "BackupScheduleDeleteRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Backup schedule deleted.", "BackupScheduleMutationResult"),
			"400":     jsonResponseSchema("The body is invalid or rawLine is not an Heyserver-managed schedule target.", "ErrorResponse"),
			"404":     jsonResponseSchema("The previously observed schedule no longer exists.", "ErrorResponse"),
			"503":     jsonResponseSchema("The service user's crontab could not be observed or updated safely.", "ErrorResponse"),
			"default": jsonResponseSchema("The backup schedule could not be deleted.", "ErrorResponse"),
		}
	case "PUT /api/backups/gdrive/oauth-app":
		operation["description"] = "Save bounded operator-supplied OAuth application metadata. Secrets remain write-only, and omitted clientSecret preserves the existing panel-managed secret."
		operation["requestBody"] = jsonRequestSchema("OAuth application metadata or a project hint for setup links.", "GDriveOAuthAppUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("OAuth application metadata saved.", "GDriveMutationResult"),
			"400":     jsonResponseSchema("The body or OAuth application metadata is invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The optional Google Drive service is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("OAuth application metadata could not be saved.", "ErrorResponse"),
		}
	case "POST /api/backups/gdrive/oauth/start":
		operation["description"] = "Create a short-lived OAuth state bound to the authenticated admin and return the Google authorization URL. This operation accepts no request body."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("OAuth browser flow created.", "GDriveOAuthStartResponse"),
			"400":     jsonResponseSchema("The request contains a body or OAuth is not configured.", "ErrorResponse"),
			"503":     jsonResponseSchema("The optional Google Drive service is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The OAuth browser flow could not be created.", "ErrorResponse"),
		}
	case "POST /api/backups/gdrive/oauth/complete":
		operation["description"] = "Complete the exact pending hexadecimal OAuth state for the same authenticated admin who started it."
		operation["requestBody"] = jsonRequestSchema("Exact pending OAuth state.", "GDriveOAuthCompleteRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("OAuth connection completed.", "GDriveMutationResult"),
			"400":     jsonResponseSchema("The body or pending OAuth state is invalid, expired, or belongs to another user.", "ErrorResponse"),
			"503":     jsonResponseSchema("The optional Google Drive service is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The OAuth connection could not be completed.", "ErrorResponse"),
		}
	case "POST /api/backups/gdrive/disconnect", "POST /api/backups/gdrive/dismiss-error":
		operation["description"] = "Run the fixed Google Drive account-state mutation. This operation accepts no request body."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Google Drive account state updated.", "GDriveMutationResult"),
			"400":     jsonResponseSchema("The request contains a body.", "ErrorResponse"),
			"503":     jsonResponseSchema("The optional Google Drive service is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("Google Drive account state could not be updated.", "ErrorResponse"),
		}
	case "PUT /api/backups/gdrive/settings":
		operation["description"] = "Replace the complete operator-controlled Drive folder, automatic-upload, retention, and notification policy while preserving server-owned upload result fields."
		operation["requestBody"] = jsonRequestSchema("Complete Google Drive backup policy replacement.", "GDriveSettingsUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Google Drive backup policy replaced.", "GDriveMutationResult"),
			"400":     jsonResponseSchema("The body, relative Drive folder, or retention value is invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The optional Google Drive service is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("Google Drive backup policy could not be updated.", "ErrorResponse"),
		}
	case "POST /api/backups/gdrive/test":
		operation["description"] = "Verify that the configured OAuth grant and rclone profile can access the installation-owned Drive folder. This operation accepts no request body."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Google Drive connection verified.", "GDriveConnectionTestResult"),
			"400":     jsonResponseSchema("The request contains a body or the connection check failed.", "ErrorResponse"),
			"503":     jsonResponseSchema("The optional Google Drive service is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The Google Drive connection could not be verified.", "ErrorResponse"),
		}
	case "POST /api/backups/gdrive/restore":
		operation["description"] = "Download one exact observed safe backup filename from the configured Drive folder into the installation-owned local backup directory."
		operation["requestBody"] = jsonRequestSchema("Exact observed portable backup filename.", "GDriveRestoreRequest", true)
		operation["responses"] = map[string]any{
			"202":     jsonResponseSchema("Google Drive download accepted.", "GDriveRestoreAccepted"),
			"400":     jsonResponseSchema("The body, backup filename, or Drive connection is invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The optional Google Drive service is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The Google Drive download could not be accepted.", "ErrorResponse"),
		}
	case "GET /api/backups/snapshot/status":
		operation["description"] = "Observe restic, client-side encryption, selected destination readiness, purge capability, manifest, and cached repository metadata without exposing credentials."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Snapshot subsystem and selected destination state.", "SnapshotStatus"),
			"503":     jsonResponseSchema("The persisted snapshot policy cannot be observed safely.", "ErrorResponse"),
			"default": jsonResponseSchema("Snapshot status could not be read.", "ErrorResponse"),
		}
	case "GET /api/backups/snapshot/list":
		operation["description"] = "List recent restic snapshots from the explicitly selected healthy destination."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Recent encrypted snapshots.", "SnapshotListResponse"),
			"400":     jsonResponseSchema("The selected repository is not initialized or cannot be read.", "ErrorResponse"),
			"503":     jsonResponseSchema("The persisted policy or selected optional destination is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("Snapshot inventory could not be read.", "ErrorResponse"),
		}
	case "GET /api/backups/snapshot/settings":
		operation["description"] = "Read the complete persisted operator-controlled snapshot policy. Runtime result fields remain server-owned."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Current snapshot policy and server-owned runtime fields.", "SnapshotSettings"),
			"503":     jsonResponseSchema("The persisted snapshot policy is unreadable or malformed.", "ErrorResponse"),
			"default": jsonResponseSchema("Snapshot settings could not be read.", "ErrorResponse"),
		}
	case "PUT /api/backups/snapshot/settings":
		operation["description"] = "Atomically replace the complete destination, repository, manifest, retention, and password-acknowledgement policy. Provider credentials remain installation-owned and are never accepted."
		operation["requestBody"] = jsonRequestSchema("Complete snapshot policy replacement.", "SnapshotSettingsUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Snapshot policy replaced atomically.", "SnapshotSettingsMutationResult"),
			"400":     jsonResponseSchema("The body, repository path, manifest identities, or retention values are invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The existing snapshot policy could not be observed safely.", "ErrorResponse"),
			"default": jsonResponseSchema("Snapshot settings could not be updated.", "ErrorResponse"),
		}
	case "POST /api/backups/snapshot/run":
		operation["description"] = "Start one incremental, client-side encrypted restic snapshot on the selected destination using only the persisted installation-owned policy. This operation accepts no request body."
		operation["responses"] = map[string]any{
			"202":     jsonResponseSchema("Incremental snapshot accepted.", "SnapshotRunAccepted"),
			"400":     jsonResponseSchema("The request contains a body or snapshot prerequisites are not ready.", "ErrorResponse"),
			"503":     jsonResponseSchema("The persisted snapshot policy is unreadable or malformed.", "ErrorResponse"),
			"default": jsonResponseSchema("The incremental snapshot could not be started.", "ErrorResponse"),
		}
	case "POST /api/backups/snapshot/restore":
		operation["description"] = "Restore an exact observed hexadecimal snapshot identity from the selected destination into the fixed local staging root, optionally selecting logical manifest identities or provider-neutral vhost names."
		operation["requestBody"] = jsonRequestSchema("Observed snapshot identity and optional bounded logical selectors.", "SnapshotRestoreRequest", true)
		operation["responses"] = map[string]any{
			"202":     jsonResponseSchema("Snapshot restore accepted into staging.", "SnapshotRestoreAccepted"),
			"400":     jsonResponseSchema("The body, snapshot identity, or include paths are invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The persisted snapshot policy is unreadable or malformed.", "ErrorResponse"),
			"default": jsonResponseSchema("The snapshot restore could not be started.", "ErrorResponse"),
		}
	case "POST /api/backups/snapshot/purge-repo":
		operation["description"] = "Permanently delete the currently configured Google Drive snapshot repository after matching its observed installation-owned identity and a fixed destructive confirmation. S3-compatible destinations report this capability as unsupported."
		operation["requestBody"] = jsonRequestSchema("Observed snapshot repository identity and fixed destructive confirmation.", "SnapshotPurgeRepositoryRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Snapshot repository deleted.", "SnapshotSettingsMutationResult"),
			"400":     jsonResponseSchema("The body, observed repository identity, or fixed confirmation is invalid or stale.", "ErrorResponse"),
			"409":     jsonResponseSchema("Another snapshot operation is active.", "ErrorResponse"),
			"422":     jsonResponseSchema("The selected destination does not support repository purge through Heyserver.", "ErrorResponse"),
			"503":     jsonResponseSchema("The persisted snapshot policy or selected optional destination is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The snapshot repository could not be deleted.", "ErrorResponse"),
		}
	case "GET /api/system/info":
		operation["description"] = "Return observed local operating-system, runtime, network-interface, and panel build identities. Optional runtime inventories are non-null arrays; unavailable command-line runtimes are reported as not found."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed local host and panel build information.", "SystemInfo"),
			"503":     jsonResponseSchema("The settings service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("System information could not be read.", "ErrorResponse"),
		}
	case "GET /api/system/stats":
		operation["description"] = "Return the current local CPU, memory, swap, filesystem, load, uptime, host, operating-system, and network counters. Disk and network inventories are always JSON arrays."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Current local system metrics.", "SystemStats"),
			"default": jsonResponseSchema("System metrics could not be collected.", "ErrorResponse"),
		}
	case "GET /api/system/services":
		operation["description"] = "Return observed systemd state for the installation's fixed local service inventory and its optional explicitly configured unprivileged PM2 unit."
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Observed local service states.", "SystemServiceStatus"),
			"default": jsonResponseSchema("Local service states could not be collected.", "ErrorResponse"),
		}
	case "GET /api/system/services/{service}/logs":
		operation["description"] = "Read bounded recent journald entries for one service in the same fixed local control allowlist. The optional lines query must appear exactly once."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, parameter := range parameters {
			if parameter["name"] == "service" && parameter["in"] == "path" {
				parameter["schema"] = map[string]any{
					"type": "string", "minLength": 3, "maxLength": 36,
					"pattern": `^(?:nginx|php8\.4-fpm|php8\.5-fpm|php7\.4-fpm|postgresql|mariadb|redis-server|pm2-[a-z_][a-z0-9_-]{0,31})$`,
				}
			}
		}
		operation["parameters"] = append(parameters, map[string]any{
			"name": "lines", "in": "query", "required": false,
			"description": "Maximum journal entries; repeated or empty values are rejected.",
			"schema":      map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "default": 100},
		})
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Bounded journal entries for the selected local service.", "SystemServiceLogsResponse"),
			"400":     jsonResponseSchema("The service is not configured in the fixed allowlist or lines is invalid, empty, or repeated.", "ErrorResponse"),
			"default": jsonResponseSchema("The service journal could not be read.", "ErrorResponse"),
		}
	case "GET /api/system/update":
		operation["description"] = "Check the optional installation-owned stable release manifest and return an explicit not_configured, unavailable, or healthy discovery state without mutating the host."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Current release discovery and signature-verification state.", "ReleaseUpdateStatus"),
		}
	case "GET /api/system/update/stage":
		operation["description"] = "Return the latest verified local release stage and its durable lifecycle state, or stage=null when no release has been staged."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Latest local release stage, including an explicit null empty state.", "ReleaseUpdateStageResponse"),
			"400":     jsonResponseSchema("The persisted latest-stage identity or record is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The persisted latest-stage marker references a missing stage.", "ErrorResponse"),
			"default": jsonResponseSchema("The latest release stage could not be read.", "ErrorResponse"),
		}
	case "POST /api/system/update/stage":
		operation["description"] = "Download, checksum, inspect, and persist the newer stable release selected by verified discovery. The request body must be empty."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Release archive verified and staged for explicit installation confirmation.", "ReleaseUpdateStage"),
			"400":     jsonResponseSchema("The request contains a body or the discovered stage identity is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("No newer stable release exists or the stage conflicts with persisted state.", "ErrorResponse"),
			"503":     jsonResponseSchema("Release discovery is not configured, unavailable, or did not provide a usable artifact.", "ErrorResponse"),
			"default": jsonResponseSchema("The release archive could not be staged safely.", "ErrorResponse"),
		}
	case "POST /api/system/update/install":
		operation["description"] = "Revalidate every executable in one observed stage and schedule its detached upgrade only after exact version and explicit confirmation. Unknown, trailing, or oversized JSON is rejected."
		operation["requestBody"] = jsonRequestSchema("Observed stage identity, exact version, and explicit confirmation.", "ReleaseUpdateInstallRequest", true)
		operation["responses"] = map[string]any{
			"202":     jsonResponseSchema("Verified release stage scheduled for detached installation.", "ReleaseUpdateStage"),
			"400":     jsonResponseSchema("The strict request, stage identity, version, or confirmation is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected release stage does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The stage state conflicts with installation or its integrity check failed.", "ErrorResponse"),
			"413":     jsonResponseSchema("The request body exceeds 4096 bytes.", "ErrorResponse"),
			"503":     jsonResponseSchema("The detached systemd upgrade could not be scheduled.", "ErrorResponse"),
			"default": jsonResponseSchema("The release installation could not be scheduled safely.", "ErrorResponse"),
		}
	case "GET /api/system/actions/status", "GET /api/nodes/{id}/actions/status":
		operation["description"] = "Return the currently running bounded maintenance action, or running=false when the host is idle."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Current maintenance action state.", "HostActionStatus"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"default": jsonResponseSchema("Maintenance state could not be read.", "ErrorResponse"),
		}
	case "GET /api/system/actions/reboot-status", "GET /api/nodes/{id}/actions/reboot-status":
		operation["description"] = "Return the bounded reboot schedule and remaining countdown without scheduling a reboot."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Current reboot schedule.", "RebootStatus"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"default": jsonResponseSchema("Reboot state could not be read.", "ErrorResponse"),
		}
	case "POST /api/system/actions/memory-optimize", "POST /api/system/actions/swap-reset", "POST /api/system/actions/temp-clean", "POST /api/system/actions/reboot-cancel":
		operation["description"] = "Run one fixed local-host maintenance action under the shared maintenance lock."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Maintenance action completed or cancellation requested.", "ActionMessage"),
			"409":     jsonResponseSchema("Another maintenance action is running or the measured precondition is unsafe.", "ErrorResponse"),
			"default": jsonResponseSchema("The maintenance action failed.", "ErrorResponse"),
		}
	case "POST /api/system/actions/reboot":
		operation["description"] = "Schedule a local-host reboot through the bounded delayed reboot mechanism."
		operation["responses"] = map[string]any{
			"202":     jsonResponseSchema("Reboot accepted and scheduled.", "ActionMessage"),
			"409":     jsonResponseSchema("Another maintenance action is running.", "ErrorResponse"),
			"default": jsonResponseSchema("The reboot could not be scheduled.", "ErrorResponse"),
		}
	case "POST /api/system/actions/service":
		operation["description"] = "Run one start, stop, or restart action against the panel's fixed local systemd service allowlist."
		operation["requestBody"] = jsonRequestSchema("Fixed service identity and bounded control action.", "ServiceControlRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Service action completed.", "ActionMessage"),
			"400":     jsonResponseSchema("The service or action is outside the fixed allowlist.", "ErrorResponse"),
			"default": jsonResponseSchema("The service action failed.", "ErrorResponse"),
		}
	case "POST /api/system/actions/process":
		operation["description"] = "Send an explicit TERM or KILL signal to the observed process identity after validating both PID and start time."
		operation["requestBody"] = jsonRequestSchema("Observed process identity and explicit bounded signal.", "ProcessSignalRequest", true)
		operation["responses"] = map[string]any{
			"400":     jsonResponseSchema("The PID, start time, or signal is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The observed process no longer exists.", "ErrorResponse"),
			"409":     jsonResponseSchema("The process is protected or its identity changed.", "ErrorResponse"),
			"default": jsonResponseSchema("The process signal could not be completed.", "ErrorResponse"),
		}
	case "POST /api/php/versions/{version}/actions/{action}":
		operation["description"] = "Test, gracefully reload, or restart one installed local PHP-FPM version. Reload and restart always validate the complete configuration before calling systemd."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, parameter := range parameters {
			if parameter["name"] == "action" && parameter["in"] == "path" {
				parameter["schema"] = map[string]any{"type": "string", "enum": []string{"test", "reload", "restart"}}
			}
		}
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Validated local PHP-FPM action receipt.", "ActionMessage"),
			"400":     jsonResponseSchema("The PHP version or fixed action is invalid.", "ErrorResponse"),
			"422":     jsonResponseSchema("The complete PHP-FPM configuration is invalid; systemd was not called.", "ErrorResponse"),
			"502":     jsonResponseSchema("The validated reload or restart failed at the local systemd boundary.", "ErrorResponse"),
			"default": jsonResponseSchema("The local PHP-FPM action could not be completed.", "ErrorResponse"),
		}
	case "POST /api/php/versions/{version}/restart", "POST /api/php/restart/{version}":
		operation["description"] = "Deprecated backwards-compatible alias for the validated local PHP-FPM restart action. New clients should use POST /api/php/versions/{version}/actions/restart."
		operation["deprecated"] = true
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Validated local PHP-FPM restart receipt.", "ActionMessage"),
			"400":     jsonResponseSchema("The PHP version is invalid.", "ErrorResponse"),
			"422":     jsonResponseSchema("The complete PHP-FPM configuration is invalid; systemd was not called.", "ErrorResponse"),
			"502":     jsonResponseSchema("The validated restart failed at the local systemd boundary.", "ErrorResponse"),
			"default": jsonResponseSchema("The local PHP-FPM restart could not be completed.", "ErrorResponse"),
		}
	case "GET /api/php/pools/{version}/{domain}/config":
		operation["description"] = "Read one observed regular local PHP-FPM pool file with its canonical path, SHA-256 checksum, size, mode, and modification time."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Checksum-bound local PHP-FPM pool configuration.", "PHPPoolConfigContent"),
			"400":     jsonResponseSchema("The PHP version or pool identity is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected pool configuration does not exist.", "ErrorResponse"),
			"413":     jsonResponseSchema("The selected pool configuration exceeds the bounded text size.", "ErrorResponse"),
			"default": jsonResponseSchema("The pool configuration could not be read safely.", "ErrorResponse"),
		}
	case "PUT /api/php/pools/{version}/{domain}/config":
		operation["description"] = "Replace one observed regular local PHP-FPM pool file under a SHA-256 lock, create a recovery backup, validate the complete PHP-FPM configuration, restore on validation or reload failure, and optionally reload the service."
		operation["requestBody"] = jsonRequestSchema("NUL-free UTF-8 replacement, exact observed SHA-256, and optional reload request.", "PHPPoolConfigReplaceRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Validated local pool replacement receipt.", "PHPPoolConfigReplaceReceipt"),
			"400":     jsonResponseSchema("The body, PHP identity, checksum, or replacement text is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected pool configuration does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The pool checksum changed after it was observed.", "ErrorResponse"),
			"413":     jsonResponseSchema("The replacement exceeds the bounded text size.", "ErrorResponse"),
			"422":     jsonResponseSchema("PHP-FPM rejected the candidate and the previous file was restored.", "ErrorResponse"),
			"502":     jsonResponseSchema("The requested reload failed and the previous file was restored.", "ErrorResponse"),
			"default": jsonResponseSchema("The pool configuration could not be replaced safely.", "ErrorResponse"),
		}
	case "POST /api/nginx/configs":
		operation["description"] = "Generate one provider-neutral local PHP, static, reverse-proxy, or redirect site configuration, create it exclusively, validate the complete Nginx configuration, and remove the candidate when validation fails. HTTP-only requests never invent TLS listeners; TLS remains explicit."
		operation["requestBody"] = jsonRequestSchema("Exact site type, portable domain, type-specific settings, and optional explicit TLS paths.", "NginxConfigCreateRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Validated local Nginx site configuration.", "NginxConfigContent"),
			"400":     jsonResponseSchema("The body, domain, type, or type-specific fields are invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("A configuration for the selected domain already exists.", "ErrorResponse"),
			"422":     jsonResponseSchema("Nginx rejected the generated candidate and Heyserver removed it.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx paths or managed snippets are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Nginx site could not be created safely.", "ErrorResponse"),
		}
	case "PUT /api/nginx/configs/{filename}/state", "POST /api/nginx/configs/{filename}/toggle":
		operation["description"] = "Apply an explicit idempotent enabled state to one local Nginx site configuration. Retries preserve the requested state; Heyserver only creates or removes an exact managed symlink and refuses foreign entries."
		if item.method == "POST" {
			operation["description"] = "Deprecated backwards-compatible alias for PUT /api/nginx/configs/{filename}/state. The exact desired-state body is still required; this route never performs an implicit flip."
			operation["deprecated"] = true
		}
		operation["requestBody"] = jsonRequestSchema("Exact desired Nginx site state.", "NginxConfigStateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed Nginx site state after the idempotent operation.", "NginxConfigStateReceipt"),
			"400":     jsonResponseSchema("The body, filename, or enabled-site entry is invalid or foreign.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected sites-available configuration does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The desired Nginx site state could not be applied safely.", "ErrorResponse"),
		}
	case "GET /api/nginx/configs/{filename}":
		operation["description"] = "Read one observed regular local Nginx site configuration with its canonical filename, enabled state, SHA-256 checksum, size, and modification time."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Checksum-bound local Nginx site configuration.", "NginxConfigContent"),
			"400":     jsonResponseSchema("The Nginx configuration identity is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected Nginx configuration does not exist.", "ErrorResponse"),
			"413":     jsonResponseSchema("The selected Nginx configuration exceeds the bounded text size.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx configuration paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Nginx configuration could not be read safely.", "ErrorResponse"),
		}
	case "PUT /api/nginx/configs/{filename}":
		operation["description"] = "Replace one observed regular local Nginx site configuration under a SHA-256 lock, create a same-directory recovery backup, atomically install the candidate, validate the complete Nginx configuration, and restore the previous file when validation fails. Reload remains a separate explicit action."
		operation["requestBody"] = jsonRequestSchema("NUL-free UTF-8 replacement and the exact observed SHA-256 checksum.", "NginxConfigReplaceRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Validated local Nginx replacement receipt.", "NginxConfigReplaceReceipt"),
			"400":     jsonResponseSchema("The body, configuration identity, checksum, or replacement text is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected Nginx configuration does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The Nginx configuration checksum changed after it was observed.", "ErrorResponse"),
			"413":     jsonResponseSchema("The replacement exceeds the bounded text size.", "ErrorResponse"),
			"422":     jsonResponseSchema("Nginx rejected the candidate and the previous file was restored.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx configuration paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Nginx configuration could not be replaced safely.", "ErrorResponse"),
		}
	case "DELETE /api/nginx/configs/{filename}":
		operation["description"] = "Archive one disabled regular local Nginx site configuration under an exact observed SHA-256 lock. Heyserver creates a same-directory recovery copy before removing the config from inventory, validates the complete Nginx configuration, and restores the original on validation failure. The document root is never removed and reload remains explicit."
		operation["requestBody"] = jsonRequestSchema("Exact observed SHA-256 checksum for the disabled configuration.", "NginxConfigArchiveRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Validated Nginx configuration archive receipt.", "NginxConfigArchiveReceipt"),
			"400":     jsonResponseSchema("The body, configuration identity, checksum, or enabled-site entry is invalid or foreign.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected Nginx configuration does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The configuration is enabled or its checksum changed after observation.", "ErrorResponse"),
			"413":     jsonResponseSchema("The selected configuration exceeds the bounded text size.", "ErrorResponse"),
			"422":     jsonResponseSchema("Nginx rejected the archived state and the original configuration was restored.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx configuration paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Nginx configuration could not be archived safely.", "ErrorResponse"),
		}
	case "GET /api/nginx/archives":
		operation["description"] = "List validated Heyserver-owned local Nginx configuration recovery copies without exposing arbitrary files, archive content, or absolute host paths."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Checksum-bound Nginx configuration archive inventory.", "NginxConfigArchiveList"),
			"413":     jsonResponseSchema("A matching archive exceeds the bounded text size.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx configuration paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Nginx configuration archive inventory could not be read safely.", "ErrorResponse"),
		}
	case "POST /api/nginx/archives/{archive}/restore":
		operation["description"] = "Restore one missing disabled local Nginx site configuration from an exact observed Heyserver archive and SHA-256 checksum. Existing configs are never overwritten, the archive remains retained, the complete configuration is validated, and a rejected candidate is removed. Reload remains explicit."
		operation["requestBody"] = jsonRequestSchema("Exact observed SHA-256 checksum for the selected archive.", "NginxConfigArchiveRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Validated disabled Nginx configuration recovery receipt.", "NginxConfigArchiveRestoreReceipt"),
			"400":     jsonResponseSchema("The body, archive identity, checksum, or enabled-site entry is invalid or foreign.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected Nginx configuration archive does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The target config already exists, is enabled, or the archive checksum changed after observation.", "ErrorResponse"),
			"413":     jsonResponseSchema("The selected archive exceeds the bounded text size.", "ErrorResponse"),
			"422":     jsonResponseSchema("Nginx rejected the restored candidate and Heyserver removed it.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx configuration paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Nginx configuration archive could not be restored safely.", "ErrorResponse"),
		}
	case "GET /api/nginx/backups":
		operation["description"] = "List validated Heyserver-owned local Nginx pre-edit recovery copies without exposing arbitrary files, backup content, or absolute host paths."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Checksum-bound Nginx configuration edit-backup inventory.", "NginxConfigBackupList"),
			"413":     jsonResponseSchema("A matching backup exceeds the bounded text size.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx configuration paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Nginx configuration edit-backup inventory could not be read safely.", "ErrorResponse"),
		}
	case "POST /api/nginx/backups/{backup}/restore":
		operation["description"] = "Roll one existing local Nginx configuration back from an exact observed pre-edit backup under separate backup and current-target SHA-256 locks. Heyserver retains a fresh pre-restore recovery, validates the complete configuration, restores the previous target on rejection, preserves enabled state, and leaves reload explicit."
		operation["requestBody"] = jsonRequestSchema("Exact observed SHA-256 checksums for the selected backup and current target.", "NginxConfigBackupRestoreRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Validated Nginx configuration rollback receipt.", "NginxConfigBackupRestoreReceipt"),
			"400":     jsonResponseSchema("The body, backup identity, checksums, or enabled-site entry is invalid or foreign.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected backup or current Nginx configuration does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The backup or current config checksum changed after observation.", "ErrorResponse"),
			"413":     jsonResponseSchema("The selected backup or current config exceeds the bounded text size.", "ErrorResponse"),
			"422":     jsonResponseSchema("Nginx rejected the rollback candidate and the previous current config was restored.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx configuration paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Nginx configuration backup could not be restored safely.", "ErrorResponse"),
		}
	case "GET /api/domains":
		operation["description"] = "List the complete observed local Nginx domain inventory. Stable string identities are normalized hostnames; a missing configured inventory is unavailable rather than a false empty list."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Complete local domain inventory; domains is always an array.", "LocalDomainList"),
			"503":     jsonResponseSchema("The installation-owned Nginx inventory paths are absent or invalid.", "ErrorResponse"),
			"default": jsonResponseSchema("The local domain inventory could not be read completely.", "ErrorResponse"),
		}
	case "GET /api/domains/provisioning":
		operation["description"] = "Report installation-owned domain paths and the honest not_configured, unavailable, or healthy state of optional Cloudflare DNS provisioning. This read never mutates DNS or the host."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Local domain provisioning paths and optional DNS-provider capability.", "DomainProvisioningCapabilities"),
		}
	case "GET /api/domains/{id}":
		operation["description"] = "Read one exact normalized local domain identity with observed Nginx content, aliases, runtime hints, certificate paths, and log paths."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed local domain detail.", "LocalDomainDetail"),
			"400":     jsonResponseSchema("The domain identity is not a normalized ASCII hostname.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected local domain configuration does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx inventory paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The local domain configuration could not be read.", "ErrorResponse"),
		}
	case "POST /api/domains/check":
		operation["description"] = "Analyze one candidate hostname without mutating the host. Malformed, unknown, trailing, or oversized JSON is rejected; a syntactically invalid candidate is represented by valid=false with non-null result arrays."
		operation["requestBody"] = jsonRequestSchema("Candidate local domain identity.", "DomainCheckRequest", true)
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Read-only local domain preflight result.", "DomainCheckResult"),
			"400": jsonResponseSchema("The strict JSON body or required domain value is missing or malformed.", "ErrorResponse"),
			"413": jsonResponseSchema("The domain preflight request exceeds 64 KiB.", "ErrorResponse"),
		}
	case "POST /api/domains":
		operation["description"] = "Create one exact local PHP, reverse-proxy, or static domain without replacing an existing Nginx configuration. The strict request is bounded to 64 KiB; optional DNS and certificate failures after HTTP activation are returned explicitly as a partial result."
		operation["requestBody"] = jsonRequestSchema("Validated local domain provisioning choices.", "DomainCreateRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Local domain created successfully.", "DomainCreateResult"),
			"207":     jsonResponseSchema("The HTTP domain is active, but a certificate or DNS follow-up produced a warning.", "DomainCreateResult"),
			"400":     jsonResponseSchema("The strict body, hostname, domain type, runtime, path, port, certificate, or provider input is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("A local configuration for the exact domain already exists and was not replaced.", "ErrorResponse"),
			"413":     jsonResponseSchema("The domain create request exceeds 64 KiB.", "ErrorResponse"),
			"429":     jsonResponseSchema("The shared local mutation rate limit was exceeded.", "ErrorResponse"),
			"503":     jsonResponseSchema("Required local paths, runtime configuration, certificate tooling, or optional DNS configuration is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("Domain provisioning failed; inspect the error because a post-activation runtime step may require recovery.", "ErrorResponse"),
		}
	case "DELETE /api/domains/{id}":
		operation["description"] = "Delete one exact observed local domain. The body must be empty; deleteFiles is an explicit single boolean and removes only the domain's root-confined site tree. Missing domains are never reported as deleted."
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters, map[string]any{
			"name": "deleteFiles", "in": "query", "required": false,
			"description": "Also remove the root-confined domain site tree; repeated, empty, or non-boolean values are rejected.",
			"schema":      map[string]any{"type": "boolean", "default": false},
		})
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Exact local domain deletion receipt.", "DomainDeleteResult"),
			"400":     jsonResponseSchema("The domain identity, request body, or query parameters are invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected local domain configuration does not exist.", "ErrorResponse"),
			"429":     jsonResponseSchema("The shared local mutation rate limit was exceeded.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned domain paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The exact local domain could not be deleted safely.", "ErrorResponse"),
		}
	case "POST /api/domains/{id}/toggle":
		operation["description"] = "Idempotently enable or disable one exact observed local domain using an explicit boolean desired state. The strict JSON body is bounded to 4 KiB."
		operation["requestBody"] = jsonRequestSchema("Explicit desired domain activation state.", "DomainToggleRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed local domain activation receipt.", "DomainToggleResult"),
			"400":     jsonResponseSchema("The domain identity or desired state is missing or invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected local domain configuration does not exist.", "ErrorResponse"),
			"413":     jsonResponseSchema("The domain toggle request exceeds 4 KiB.", "ErrorResponse"),
			"429":     jsonResponseSchema("The shared local mutation rate limit was exceeded.", "ErrorResponse"),
			"503":     jsonResponseSchema("The installation-owned Nginx domain paths are not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The domain state could not be changed.", "ErrorResponse"),
		}
	case "GET /api/cron/status":
		operation["description"] = "Report whether local cron management is healthy, missing, stopped, or unavailable before clients enable scheduled-task mutations."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Observed local cron client and daemon readiness.", "CronServiceStatus"),
		}
	case "GET /api/cron/jobs":
		operation["description"] = "List all readable user crontabs as one complete local inventory. An unavailable crontab client or unreadable user crontab is never converted into an empty or partial list."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Complete local cron-job inventory.", "CronJobListResponse"),
			"503":     jsonResponseSchema("The local crontab client is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The complete cron-job inventory could not be read.", "ErrorResponse"),
		}
	case "POST /api/cron/jobs":
		operation["description"] = "Create one active local cron job. The owning user defaults to root when omitted."
		operation["requestBody"] = jsonRequestSchema("Cron schedule, command, optional owner, and optional description.", "CronJobCreateRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Created cron job and mutation receipt.", "CronJobMutationResult"),
			"400":     jsonResponseSchema("The JSON body, schedule, command, or owning user is invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The local crontab client is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The cron job could not be created.", "ErrorResponse"),
		}
	case "PUT /api/cron/jobs/{id}":
		operation["description"] = "Replace an existing local cron job with an explicit schedule, command, description, and active state."
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters, cronUserQueryParameter())
		operation["requestBody"] = jsonRequestSchema("Complete replacement state for the selected cron job.", "CronJobUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Updated cron job and mutation receipt.", "CronJobMutationResult"),
			"400":     jsonResponseSchema("The JSON body or required replacement fields are invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The cron job does not exist for the selected user.", "ErrorResponse"),
			"503":     jsonResponseSchema("The local crontab client is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The cron job could not be updated.", "ErrorResponse"),
		}
	case "DELETE /api/cron/jobs/{id}":
		operation["description"] = "Delete one local cron job owned by the selected system user."
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters, cronUserQueryParameter())
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Deleted cron-job identity and mutation receipt.", "CronJobDeleteResult"),
			"400":     jsonResponseSchema("The cron-job identity is missing or invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The cron job does not exist for the selected user.", "ErrorResponse"),
			"503":     jsonResponseSchema("The local crontab client is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The cron job could not be deleted.", "ErrorResponse"),
		}
	case "GET /api/cron/system":
		operation["description"] = "List bounded metadata and parsed entries for system cron directories and user crontabs."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("System cron-file inventory.", "CronSystemFileListResponse"),
			"default": jsonResponseSchema("The system cron-file inventory could not be read.", "ErrorResponse"),
		}
	case "GET /api/databases":
		operation["description"] = "List local PostgreSQL and MariaDB databases with per-engine readiness. An unavailable engine is retained in sources instead of hiding healthy inventory from the other engine."
		operation["parameters"] = []map[string]any{databaseEngineQueryParameter()}
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Observed local database inventory and source readiness.", "DatabaseInventory"),
			"400": jsonResponseSchema("The optional engine alias is invalid.", "ErrorResponse"),
		}
	case "GET /api/mail/accounts":
		operation["description"] = "List individual mailbox principals through the configured optional mail integration. An optional exact domain filter narrows the inventory. The response is a credential-free projection: passwords, secrets, hashes, and raw provider response bodies never cross this API boundary."
		operation["parameters"] = []map[string]any{mailDomainQueryParameter()}
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Safe mail account inventory.", "MailAccount"),
			"502":     jsonResponseSchema("The mail provider rejected or could not complete the account inventory request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The mail integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("Mail account inventory could not be read.", "ErrorResponse"),
		}
	case "GET /api/mail/aliases":
		operation["description"] = "List alias principals through the configured optional mail integration. An optional exact domain filter narrows the inventory. The response is a credential-free projection: passwords, secrets, hashes, and raw provider response bodies never cross this API boundary."
		operation["parameters"] = []map[string]any{mailDomainQueryParameter()}
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Safe mail alias inventory.", "MailAlias"),
			"502":     jsonResponseSchema("The mail provider rejected or could not complete the alias inventory request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The mail integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("Mail alias inventory could not be read.", "ErrorResponse"),
		}
	case "GET /api/mail/service/status", "GET /api/mail/status":
		operation["description"] = "Return the configured Stalwart mail service's local systemd observation. The response is always HTTP 200; status is not_configured until an explicit service unit is supplied and may otherwise be running, stopped, failed, or unknown. PID and uptime are included only when systemd returns them; this read never starts or changes the service."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Observed Stalwart mail service status; not_configured is a truthful optional-integration state.", "MailServiceStatus"),
		}
	case "GET /api/mail/service/overview":
		operation["description"] = "Return one read-only Stalwart runtime overview. The top-level response remains HTTP 200 even when an optional source is not configured or unavailable; sources carries each source's availability, state, and existing error text. Local service status is observed separately from config, binary, listener, and storage discovery; no action or configuration mutation occurs."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Read-only Stalwart service overview with per-source availability.", "MailServiceOverview"),
		}
	case "GET /api/mail/config":
		operation["description"] = "Read selected Stalwart configuration sections from the configured local file. The 200 payload intentionally remains unmodeled in the generated strict schemas because its values map is raw provider configuration and may contain installation-specific or secret-bearing material; clients must not treat this route as a redacted contract. Missing config-path configuration returns 503, while other file and read failures retain the handler's 500 boundary."
		operation["responses"] = map[string]any{
			"200": map[string]any{
				"description": "Selected Stalwart configuration sections; no generated response schema is published for the raw values map.",
			},
			"500": jsonResponseSchema("The configured Stalwart configuration file could not be opened or read.", "ErrorResponse"),
			"503": jsonResponseSchema("The Stalwart configuration path is not configured.", "ErrorResponse"),
		}
	case "GET /api/mail/version":
		operation["description"] = "Read the configured Stalwart binary version. The response includes the parsed version and the binary's raw --version output only; configuration and log content are not returned. Missing binary configuration returns 503, while a configured binary that cannot execute retains the handler's 500 boundary."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Parsed Stalwart binary version and raw command output.", "MailServiceVersion"),
			"500": jsonResponseSchema("The configured Stalwart binary could not be executed.", "ErrorResponse"),
			"503": jsonResponseSchema("The Stalwart binary path is not configured.", "ErrorResponse"),
		}
	case "GET /api/mail/listeners":
		operation["description"] = "Read listener metadata parsed from selected Stalwart configuration sections. The response exposes only listener identity, protocol, bind address, optional port, and TLS state; raw configuration and credentials are not returned. Missing config-path configuration returns 503, while other file and read failures retain the handler's 500 boundary."
		operation["responses"] = map[string]any{
			"200": jsonArrayResponseSchema("Parsed Stalwart listener metadata.", "MailServiceListener"),
			"500": jsonResponseSchema("The configured Stalwart listener configuration could not be opened or read.", "ErrorResponse"),
			"503": jsonResponseSchema("The Stalwart configuration path is not configured.", "ErrorResponse"),
		}
	case "GET /api/mail/storage":
		operation["description"] = "Read Stalwart storage backend metadata parsed from the configured local file and best-effort local byte usage. The response exposes backend, path, and sizeBytes only; raw configuration and credentials are not returned. Missing config-path configuration returns 503, while other file and read failures retain the handler's 500 boundary."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Parsed Stalwart storage metadata and best-effort local usage.", "MailServiceStorage"),
			"500": jsonResponseSchema("The configured Stalwart storage configuration could not be opened or read.", "ErrorResponse"),
			"503": jsonResponseSchema("The Stalwart configuration path is not configured.", "ErrorResponse"),
		}
	case "POST /api/databases":
		operation["description"] = "Create one local PostgreSQL or MariaDB database using bounded identifiers. Unknown fields, trailing JSON, and bodies above 16 KiB are rejected."
		operation["requestBody"] = jsonRequestSchema("Canonical engine, database name, and optional PostgreSQL owner.", "DatabaseCreateRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Database created.", "DatabaseCreateResult"),
			"400":     jsonResponseSchema("The body, engine, database name, or owner is invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The JSON body exceeds 16 KiB.", "ErrorResponse"),
			"default": jsonResponseSchema("The local database client rejected the creation request.", "ErrorResponse"),
		}
	case "GET /api/databases/credentials":
		operation["description"] = "Return the installation's legacy pgm_metadata credential inventory to an administrator. The response is secret-bearing and uses Cache-Control: no-store."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Secret-bearing PGM credential inventory.", "PGMCredentialList"),
			"default": jsonResponseSchema("The pgm_metadata credential store could not be read.", "ErrorResponse"),
		}
	case "GET /api/databases/credentials/{name}":
		operation["description"] = "Return one exact pgm_metadata credential to an administrator. The response is secret-bearing and uses Cache-Control: no-store."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Secret-bearing PGM credential.", "PGMCredential"),
			"400":     jsonResponseSchema("The database name is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The credential does not exist.", "ErrorResponse"),
			"default": jsonResponseSchema("The pgm_metadata credential store could not be read.", "ErrorResponse"),
		}
	case "GET /api/databases/pgm-backup-files/{name}":
		operation["description"] = "List sorted .sql and .sql.gz basenames inside one bounded installation-owned PGM backup directory."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Restorable SQL backup basenames.", "PGMBackupFileList"),
			"400":     jsonResponseSchema("The backup directory name is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The backup directory does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The configured PGM backup root is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The backup directory could not be read.", "ErrorResponse"),
		}
	case "GET /api/databases/pgm-backups":
		operation["description"] = "List installation-owned PGM backup directories newest first, including their configured absolute restore path and observed SQL file count."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed PGM backup directory inventory.", "PGMBackupList"),
			"503":     jsonResponseSchema("The configured PGM backup root is missing, inaccessible, or not a directory.", "ErrorResponse"),
			"default": jsonResponseSchema("The backup inventory failed unexpectedly.", "ErrorResponse"),
		}
	case "GET /api/databases/pgm-credentials":
		operation["description"] = "Compatibility alias for the administrator-only pgm_metadata credential inventory. The response is secret-bearing and uses Cache-Control: no-store."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Secret-bearing PGM credential inventory.", "PGMCredentialList"),
			"default": jsonResponseSchema("The pgm_metadata credential store could not be read.", "ErrorResponse"),
		}
	case "POST /api/databases/pgm-restore":
		operation["description"] = "Restore one existing PostgreSQL database from a regular .sql or .sql.gz file below the configured installation-owned backup root. Lexical escapes, symlinks, unknown fields, trailing JSON, and bodies above 16 KiB are rejected."
		operation["requestBody"] = jsonRequestSchema("Target database identity and exact path returned by the PGM backup inventory.", "PGMRestoreRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Database restore completed.", "PGMRestoreResult"),
			"400":     jsonResponseSchema("The body, database identity, file type, or configured-root boundary is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected backup file does not exist.", "ErrorResponse"),
			"413":     jsonResponseSchema("The JSON body exceeds 16 KiB.", "ErrorResponse"),
			"503":     jsonResponseSchema("The configured PGM backup root is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The PostgreSQL restore command failed.", "ErrorResponse"),
		}
	case "GET /api/databases/users":
		operation["description"] = "List local PostgreSQL and MariaDB users with per-engine readiness. An unavailable engine does not hide healthy users from the other engine."
		operation["parameters"] = []map[string]any{databaseEngineQueryParameter()}
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Observed database-user inventory and source readiness.", "DatabaseUserInventory"),
			"400": jsonResponseSchema("The optional engine alias is invalid.", "ErrorResponse"),
		}
	case "DELETE /api/databases/{engine}/{name}":
		operation["description"] = "Permanently drop one local database after an exact DROP <name> confirmation receipt. Unknown fields, trailing JSON, and bodies above 16 KiB are rejected."
		operation["requestBody"] = jsonRequestSchema("Exact database deletion confirmation.", "DatabaseDropRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Database dropped.", "DatabaseDropResult"),
			"400":     jsonResponseSchema("The engine, database identity, body, or confirmation is invalid.", "ErrorResponse"),
			"413":     jsonResponseSchema("The JSON body exceeds 16 KiB.", "ErrorResponse"),
			"default": jsonResponseSchema("The local database client rejected the drop request.", "ErrorResponse"),
		}
	case "POST /api/databases/{engine}/{name}/query":
		operation["description"] = "Execute one server-validated SELECT or WITH statement inside an engine-enforced read-only transaction. Query text is valid NUL-free UTF-8 capped at 64 KiB; write_mode is a compatibility field that must remain false."
		operation["requestBody"] = jsonRequestSchema("One bounded read-only query and an optional false write_mode compatibility value.", "DatabaseQueryRequest", true)
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Non-null tabular query result.", "DatabaseQueryResponse"),
			"400": jsonResponseSchema("The engine, database identity, body, query, or write_mode is invalid, or the database rejected the query.", "ErrorResponse"),
			"413": jsonResponseSchema("The JSON body exceeds 128 KiB.", "ErrorResponse"),
		}
	case "GET /api/databases/{engine}/{name}/tables":
		operation["description"] = "List tables and observed size/row metadata for one bounded local database. Engine aliases are accepted and the response always uses the canonical engine identity."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed table inventory.", "DatabaseTableInventory"),
			"400":     jsonResponseSchema("The engine or database identity is invalid.", "ErrorResponse"),
			"default": jsonResponseSchema("The local database table inventory could not be read.", "ErrorResponse"),
		}
	case "GET /api/firewall/status":
		operation["description"] = "Return structured local firewall readiness, observed backend, policies, logging, and a non-null rule inventory. UFW is the only mutable backend; iptables fallback remains read-only."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed local firewall readiness and rule inventory.", "FirewallStatus"),
			"500":     jsonResponseSchema("The local firewall state could not be observed.", "ErrorResponse"),
			"default": jsonResponseSchema("The local firewall status could not be read.", "ErrorResponse"),
		}
	case "GET /api/firewall/rules":
		operation["description"] = "Return the observed local firewall rules as a non-null array, including read-only iptables fallback visibility when UFW is missing."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed local firewall rule inventory.", "FirewallRuleListResponse"),
			"500":     jsonResponseSchema("The local firewall rules could not be observed.", "ErrorResponse"),
			"default": jsonResponseSchema("The local firewall rule inventory could not be read.", "ErrorResponse"),
		}
	case "POST /api/firewall/rules":
		operation["description"] = "Add one bounded UFW rule using explicit firewall field names."
		operation["requestBody"] = jsonRequestSchema("UFW rule action and optional direction, protocol, port, source, destination, and comment.", "FirewallRuleCreateRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Firewall rule added.", "ActionMessage"),
			"400":     jsonResponseSchema("The rule body or one of its bounded fields is invalid.", "ErrorResponse"),
			"default": jsonResponseSchema("The firewall rule could not be added.", "ErrorResponse"),
		}
	case "DELETE /api/firewall/rules/{number}":
		operation["description"] = "Delete one observed positive UFW rule number while preserving the last inbound SSH allow rule."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, parameter := range parameters {
			if parameter["name"] == "number" && parameter["in"] == "path" {
				parameter["schema"] = map[string]any{"type": "integer", "minimum": 1}
			}
		}
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Deleted UFW rule number and mutation receipt.", "FirewallRuleDeleteResult"),
			"400":     jsonResponseSchema("The rule number is not a positive integer.", "ErrorResponse"),
			"403":     jsonResponseSchema("The rule is the last inbound SSH allow rule.", "ErrorResponse"),
			"500":     jsonResponseSchema("The selected rule could not be deleted from the manageable UFW backend.", "ErrorResponse"),
			"default": jsonResponseSchema("The firewall rule could not be deleted.", "ErrorResponse"),
		}
	case "POST /api/firewall/toggle":
		operation["description"] = "Enable or disable UFW using an explicit boolean desired state."
		operation["requestBody"] = jsonRequestSchema("Explicit desired UFW enabled state.", "FirewallToggleRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Applied UFW desired state and mutation receipt.", "FirewallToggleResult"),
			"400":     jsonResponseSchema("The explicit enable field is missing or invalid.", "ErrorResponse"),
			"500":     jsonResponseSchema("The UFW desired state could not be applied.", "ErrorResponse"),
			"default": jsonResponseSchema("The firewall state could not be changed.", "ErrorResponse"),
		}
	case "GET /api/files":
		operation["description"] = "List the configured file roots when path is omitted, or list entries below one requested allowed directory when path is provided. A failed or out-of-bound directory observation is reported as a client error."
		operation["parameters"] = []map[string]any{filePathQueryParameter(false)}
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Configured file roots or entries for the requested directory.", "FileListResponse"),
			"400": jsonResponseSchema("The requested path is not an allowed readable directory.", "ErrorResponse"),
		}
	case "GET /api/files/read":
		operation["description"] = "Read the text content of one file below the configured allowed roots. The path query parameter is required; directories, sensitive paths, unavailable files, and files over the service read limit return a client error."
		operation["parameters"] = []map[string]any{filePathQueryParameter(true)}
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Requested file path and text content.", "FileReadResponse"),
			"400": jsonResponseSchema("The path is missing or the file cannot be read within the allowed boundary.", "ErrorResponse"),
		}
	case "PUT /api/files/write":
		operation["description"] = "Write text content to one file below the configured allowed roots, creating it when absent and preserving existing permissions when present. The path is required; an omitted content field is decoded as empty text by the current runtime."
		operation["requestBody"] = jsonRequestSchema("File path and optional text content.", "FileWriteRequest", true)
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("File write status and path.", "FileWriteResponse"),
			"400": jsonResponseSchema("The JSON body is invalid, the path is missing, or the file cannot be written within the allowed boundary.", "ErrorResponse"),
		}
	case "POST /api/files/create":
		operation["description"] = "Create one file or directory below the configured allowed roots. The path is required; type file creates a file, while directory and its compatibility alias dir create a directory."
		operation["requestBody"] = jsonRequestSchema("File path and creation type.", "FileCreateRequest", true)
		operation["responses"] = map[string]any{
			"201": jsonResponseSchema("Created file or directory status and path.", "FileCreateResponse"),
			"400": jsonResponseSchema("The JSON body is invalid, the path is missing, or the target cannot be created within the allowed boundary.", "ErrorResponse"),
		}
	case "DELETE /api/files":
		operation["description"] = "Recursively delete one file or directory below the configured allowed roots. The path query parameter is required."
		operation["parameters"] = []map[string]any{filePathQueryParameter(true)}
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Deleted file or directory status and path.", "FileDeleteResponse"),
			"400": jsonResponseSchema("The path is missing or the target cannot be deleted within the allowed boundary.", "ErrorResponse"),
		}
	case "POST /api/files/rename":
		operation["description"] = "Rename or move one file or directory below the configured allowed roots. The body accepts the current src/dst names and the legacy old_path/new_path names; when both conventions are supplied, src and dst take precedence independently."
		operation["requestBody"] = jsonRequestSchema("Source and destination paths using either the src/dst or legacy old_path/new_path names.", "FileRenameRequest", true)
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Renamed file or directory status and effective source and destination paths.", "FileRenameResponse"),
			"400": jsonResponseSchema("The JSON body is invalid, a source or destination is missing, or the move cannot be performed within the allowed boundary.", "ErrorResponse"),
		}
	case "GET /api/pm2/processes":
		operation["description"] = "List every process observed through the installation-owned unprivileged PM2 context. An empty array is distinct from an unconfigured or failed PM2 integration."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed local PM2 process inventory.", "PM2ProcessList"),
			"500":     jsonResponseSchema("PM2 was configured but its complete process inventory could not be read.", "ErrorResponse"),
			"503":     jsonResponseSchema("The unprivileged PM2 integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The PM2 process inventory could not be read.", "ErrorResponse"),
		}
	case "GET /api/pm2/processes/{id}":
		operation["description"] = "Return one PM2 process selected by numeric PM2 ID or exact process name through the configured unprivileged owner."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed local PM2 process.", "PM2Process"),
			"400":     jsonResponseSchema("The PM2 process identity is missing.", "ErrorResponse"),
			"404":     jsonResponseSchema("The PM2 process could not be found or observed.", "ErrorResponse"),
			"503":     jsonResponseSchema("The unprivileged PM2 integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The PM2 process could not be read.", "ErrorResponse"),
		}
	case "GET /api/pm2/processes/{id}/logs":
		operation["description"] = "Return a bounded line-based PM2 log snapshot. The line count defaults to 100 and invalid, zero, or oversized values fail instead of silently falling back."
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters, map[string]any{
			"name":        "lines",
			"in":          "query",
			"required":    false,
			"description": "Requested PM2 log line count.",
			"schema":      map[string]any{"type": "integer", "minimum": 1, "maximum": 5000, "default": 100},
		})
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Bounded PM2 process log snapshot.", "PM2LogsResponse"),
			"400":     jsonResponseSchema("The process identity or requested line count is invalid.", "ErrorResponse"),
			"500":     jsonResponseSchema("PM2 could not read the selected process logs.", "ErrorResponse"),
			"503":     jsonResponseSchema("The unprivileged PM2 integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The PM2 process logs could not be read.", "ErrorResponse"),
		}
	case "POST /api/pm2/deploy":
		operation["description"] = "Start one script under the configured unprivileged PM2 owner using bounded paths, mode, and instance count."
		operation["requestBody"] = jsonRequestSchema("PM2 application identity and bounded start options.", "PM2DeployRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Started PM2 process and command receipt.", "PM2DeployResult"),
			"400":     jsonResponseSchema("The request body or deploy options are invalid.", "ErrorResponse"),
			"500":     jsonResponseSchema("PM2 rejected or failed the bounded deployment.", "ErrorResponse"),
			"503":     jsonResponseSchema("The unprivileged PM2 integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The PM2 process could not be started.", "ErrorResponse"),
		}
	case "POST /api/pm2/processes/{id}/{action}":
		operation["description"] = "Run one fixed local PM2 lifecycle action under the configured unprivileged owner."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, parameter := range parameters {
			if parameter["name"] == "action" && parameter["in"] == "path" {
				parameter["schema"] = map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "reload", "delete"}}
			}
		}
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Completed PM2 lifecycle action and command receipt.", "PM2ControlResult"),
			"400":     jsonResponseSchema("The process identity or fixed action is invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The unprivileged PM2 integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The PM2 lifecycle action failed.", "ErrorResponse"),
		}
	case "POST /api/pm2/save":
		operation["description"] = "Persist the current process inventory through the installation-owned unprivileged PM2 context."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Persisted PM2 process-list receipt.", "PM2SaveResult"),
			"500":     jsonResponseSchema("PM2 could not persist the current process list.", "ErrorResponse"),
			"503":     jsonResponseSchema("The unprivileged PM2 integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The PM2 process list could not be persisted.", "ErrorResponse"),
		}
	case "POST /api/nodes/{id}/pm2/{name}/actions/{action}":
		operation["description"] = "Run one fixed PM2 lifecycle action through the managed node capability and its local action allowlist."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, parameter := range parameters {
			if parameter["name"] == "action" && parameter["in"] == "path" {
				parameter["schema"] = map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "reload"}}
			}
		}
		operation["responses"] = map[string]any{
			"200":     map[string]any{"description": "Managed-node PM2 action completed and its process list saved."},
			"400":     jsonResponseSchema("The process name or fixed action is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("The node is offline or does not advertise PM2 action capability.", "ErrorResponse"),
			"502":     jsonResponseSchema("The agent rejected or failed the locally allowlisted action.", "ErrorResponse"),
			"504":     jsonResponseSchema("The managed PM2 action did not finish within the bounded wait.", "ErrorResponse"),
			"default": jsonResponseSchema("The managed PM2 action failed.", "ErrorResponse"),
		}
	case "GET /api/docker/status":
		operation["description"] = "Report the observed local Docker CLI and daemon state with stable container and image counts. A missing CLI or stopped daemon remains a successful structured readiness response."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed local Docker readiness and inventory counts.", "DockerStatus"),
			"502":     jsonResponseSchema("Docker was reached but the complete status inventory could not be observed.", "ErrorResponse"),
			"default": jsonResponseSchema("Docker readiness could not be read.", "ErrorResponse"),
		}
	case "GET /api/docker/containers":
		operation["description"] = "List every observed local Docker container with normalized state, ports, and best-effort resource metrics. An empty array is distinct from an unavailable Docker inventory."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Local Docker container inventory.", "DockerContainerList"),
			"503":     jsonResponseSchema("The local Docker container inventory is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The local Docker container inventory could not be read.", "ErrorResponse"),
		}
	case "GET /api/docker/containers/{id}/logs":
		operation["description"] = "Return timestamped logs for one validated local container. The line request defaults to 200, accepts 1 through 1000, and the response reports when its one-MiB byte cap truncated older output."
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters, map[string]any{
			"name":        "tail",
			"in":          "query",
			"required":    false,
			"description": "Requested trailing line count before the independent response-byte cap.",
			"schema":      map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "default": 200},
		})
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Bounded local Docker container logs.", "DockerContainerLogs"),
			"400":     jsonResponseSchema("The container identity or tail value is invalid.", "ErrorResponse"),
			"502":     jsonResponseSchema("Docker could not read the selected container logs.", "ErrorResponse"),
			"default": jsonResponseSchema("The selected container logs could not be read.", "ErrorResponse"),
		}
	case "GET /api/docker/images":
		operation["description"] = "List deduplicated local Docker images with all observed repository tags, human-readable size, and creation time. An empty array is distinct from an unavailable Docker inventory."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Local Docker image inventory.", "DockerImageList"),
			"503":     jsonResponseSchema("The local Docker image inventory is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The local Docker image inventory could not be read.", "ErrorResponse"),
		}
	case "POST /api/docker/images/pull":
		operation["description"] = "Pull one validated image reference through the local Docker CLI without accepting additional Docker arguments."
		operation["requestBody"] = jsonRequestSchema("Exact Docker image reference.", "DockerImagePullRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Pulled image identity and mutation receipt.", "DockerImageMutationResult"),
			"400":     jsonResponseSchema("The request body or image reference is invalid.", "ErrorResponse"),
			"502":     jsonResponseSchema("Docker rejected or failed the bounded image pull.", "ErrorResponse"),
			"default": jsonResponseSchema("The Docker image could not be pulled.", "ErrorResponse"),
		}
	case "POST /api/docker/containers/{id}/{action}":
		operation["description"] = "Run one fixed local Docker container lifecycle action without arbitrary CLI arguments."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, parameter := range parameters {
			if parameter["name"] == "action" && parameter["in"] == "path" {
				parameter["schema"] = map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "pause", "unpause", "remove"}}
			}
		}
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Completed container action and exact target receipt.", "DockerContainerActionResult"),
			"400":     jsonResponseSchema("The container identity or fixed action is invalid.", "ErrorResponse"),
			"502":     jsonResponseSchema("Docker rejected or failed the bounded container action.", "ErrorResponse"),
			"default": jsonResponseSchema("The Docker container action failed.", "ErrorResponse"),
		}
	case "DELETE /api/docker/images/{id}":
		operation["description"] = "Remove one validated local Docker image without force flags or additional CLI arguments."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Removed image identity and mutation receipt.", "DockerImageMutationResult"),
			"400":     jsonResponseSchema("The image identity is invalid.", "ErrorResponse"),
			"502":     jsonResponseSchema("Docker rejected or failed the bounded image removal.", "ErrorResponse"),
			"default": jsonResponseSchema("The Docker image could not be removed.", "ErrorResponse"),
		}
	case "POST /api/nodes/{id}/containers/{container}/actions/{action}":
		operation["description"] = "Run one fixed Docker container action through the managed node capability and its local action allowlist."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, parameter := range parameters {
			if parameter["name"] == "action" && parameter["in"] == "path" {
				parameter["schema"] = map[string]any{"type": "string", "enum": []string{"start", "stop", "restart"}}
			}
		}
		operation["responses"] = map[string]any{
			"200":     map[string]any{"description": "Managed-node container action completed."},
			"400":     jsonResponseSchema("The container identity or fixed action is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("The node is offline or does not advertise container action capability.", "ErrorResponse"),
			"502":     jsonResponseSchema("The agent rejected or failed the locally allowlisted action.", "ErrorResponse"),
			"504":     jsonResponseSchema("The managed container action did not finish within the bounded wait.", "ErrorResponse"),
			"default": jsonResponseSchema("The managed container action failed.", "ErrorResponse"),
		}
	case "POST /api/nodes/{id}/actions/{action}":
		operation["description"] = "Run one fixed host action through the managed node's advertised capability and server-local allowlist, then wait for its structured result."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, parameter := range parameters {
			if parameter["name"] == "action" && parameter["in"] == "path" {
				parameter["schema"] = map[string]any{"type": "string", "enum": hostActions()}
			}
		}
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Managed-node action completed.", "ActionMessage"),
			"202":     jsonResponseSchema("Managed-node reboot accepted and scheduled.", "ActionMessage"),
			"400":     jsonResponseSchema("The fixed action is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The node is offline or does not advertise the required host-action capability.", "ErrorResponse"),
			"502":     jsonResponseSchema("The managed agent failed the action, including a server-local allowlist denial.", "ErrorResponse"),
			"504":     jsonResponseSchema("The managed action did not complete before the bounded wait expired.", "ErrorResponse"),
			"default": jsonResponseSchema("The managed action could not be completed.", "ErrorResponse"),
		}
	case "GET /api/disk/cleanup/scan":
		operation["description"] = "Measure the local host's fixed cleanup targets without deleting data. Only returned target IDs can be executed."
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Measured local cleanup targets.", "LocalDiskCleanupTarget"),
			"default": jsonResponseSchema("Cleanup targets could not be measured.", "ErrorResponse"),
		}
	case "POST /api/disk/cleanup/execute":
		operation["description"] = "Execute one to twenty selected local fixed cleanup targets under the shared maintenance lock and return measured reclaimed bytes."
		operation["requestBody"] = jsonRequestSchema("Selected fixed local cleanup target IDs.", "DiskCleanupExecuteRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Local cleanup execution receipt.", "LocalDiskCleanupExecution"),
			"400":     jsonResponseSchema("The target selection is empty, duplicated, unknown, or over the bound.", "ErrorResponse"),
			"409":     jsonResponseSchema("Another maintenance action is running.", "ErrorResponse"),
			"default": jsonResponseSchema("The cleanup could not be executed.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}/disk/cleanup":
		operation["description"] = "Ask the online managed agent to measure only its installation-owned fixed cleanup targets."
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Measured managed-node cleanup targets.", "DiskCleanupTarget"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The node is offline or does not advertise disk cleanup.", "ErrorResponse"),
			"502":     jsonResponseSchema("The managed agent returned invalid or failed scan data.", "ErrorResponse"),
			"504":     jsonResponseSchema("The managed scan did not complete before the bounded wait expired.", "ErrorResponse"),
			"default": jsonResponseSchema("Managed cleanup targets could not be measured.", "ErrorResponse"),
		}
	case "POST /api/nodes/{id}/disk/cleanup":
		operation["description"] = "Execute one to four explicitly confirmed fixed cleanup targets through the managed agent and return its measured receipt."
		operation["requestBody"] = jsonRequestSchema("Confirmed managed-node cleanup selection.", "ManagedDiskCleanupExecuteRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Managed-node cleanup execution receipt.", "ManagedDiskCleanupExecution"),
			"400":     jsonResponseSchema("Confirmation or the bounded target selection is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The node is offline or does not advertise disk cleanup.", "ErrorResponse"),
			"502":     jsonResponseSchema("The managed agent returned invalid or failed cleanup data.", "ErrorResponse"),
			"504":     jsonResponseSchema("The managed cleanup did not complete before the bounded wait expired.", "ErrorResponse"),
			"default": jsonResponseSchema("Managed cleanup could not be executed.", "ErrorResponse"),
		}
	case "POST /api/nodes":
		operation["description"] = "Enroll a provider-neutral managed-node identity and return its bearer token exactly once. Only the token digest is persisted."
		operation["requestBody"] = jsonRequestSchema("Managed-node identity and display name.", "ManagedNodeRegisterRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Managed node enrolled; protect the one-time token immediately.", "ManagedNodeRegistration"),
			"400":     jsonResponseSchema("The node identity or name is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("The node identity is already enrolled.", "ErrorResponse"),
			"default": jsonResponseSchema("The node could not be enrolled.", "ErrorResponse"),
		}
	case "GET /api/deploy/templates":
		operation["description"] = "Read installation-owned deployment templates from the fixed Heyserver data directory. The API cannot create templates, select another directory, or return repository credentials. Status distinguishes an absent or empty inventory from invalid or unreadable files."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed deployment template inventory, including bounded per-file issues.", "DeployTemplateInventory"),
			"default": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "GET /api/deploy/targets":
		operation["description"] = "List every local deployment target. Webhook signing secrets are always stripped, and an empty inventory is returned as an array rather than null."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Local deployment target inventory without webhook secrets.", "DeployTargetList"),
			"503":     jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The deployment target inventory could not be read.", "ErrorResponse"),
		}
	case "POST /api/deploy/targets":
		operation["description"] = "Create one local script or Docker Compose deployment target. The payload is strict: unknown fields and trailing JSON values are rejected. Webhook tokens are write-only and are never echoed in the response."
		operation["requestBody"] = jsonRequestSchema("Complete local deployment target configuration.", "CreateDeployTargetRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Deployment target created without webhook secret material.", "DeployTarget"),
			"400":     jsonResponseSchema("The body or deployment target configuration is invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The deployment target could not be created.", "ErrorResponse"),
		}
	case "PUT /api/deploy/targets/{id}":
		operation["description"] = "Replace the mutable configuration of one local deployment target using the required expectedUpdatedAt observation. A stale observation returns a conflict instead of overwriting newer state. An empty webhookToken preserves the existing protected secret, a non-empty value replaces it, and clearWebhookToken explicitly removes it. Unknown fields and trailing JSON values are rejected; webhook tokens remain write-only."
		operation["requestBody"] = jsonRequestSchema("Replacement deployment target configuration.", "UpdateDeployTargetRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Updated deployment target without webhook secret material.", "DeployTarget"),
			"400":     jsonResponseSchema("The target identity, body, or replacement configuration is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The target changed after the caller observed it; refresh before retrying.", "ErrorResponse"),
			"503":     jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The deployment target could not be updated.", "ErrorResponse"),
		}
	case "DELETE /api/deploy/targets/{id}":
		operation["description"] = "Delete one local deployment target and its deployment history. Attached project domains or derived staging targets must be removed first."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Deployment target deleted.", "ActionMessage"),
			"400":     jsonResponseSchema("The deployment target identity is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("Project domains or staging targets are still attached.", "ErrorResponse"),
			"503":     jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The deployment target could not be deleted.", "ErrorResponse"),
		}
	case "GET /api/deploy/targets/{id}/preflight":
		operation["description"] = "Run read-only target, Git checkout, and executor readiness checks. Optional dependencies are represented by pass, pending, or fail checks instead of being reported as internal API failures."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Observed deployment eligibility and individual readiness checks.", "DeployPreflight"),
			"400": jsonResponseSchema("The deployment target identity is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target does not exist or could not be read.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "POST /api/deploy/manual/{targetId}":
		operation["description"] = "Repeat deployment preflight and queue one asynchronous manual deployment. The response identifies the persisted run used for history and log polling."
		operation["responses"] = map[string]any{
			"202":     jsonResponseSchema("Manual deployment queued.", "DeployQueueResult"),
			"400":     jsonResponseSchema("The deployment target identity is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("Deployment preflight is not eligible.", "ErrorResponse"),
			"503":     jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The manual deployment could not be queued.", "ErrorResponse"),
		}
	case "POST /api/deploy/rollback/{targetId}":
		operation["description"] = "Queue an asynchronous rollback to the newest available previous successful deployment revision after rollback eligibility checks."
		operation["responses"] = map[string]any{
			"202":     jsonResponseSchema("Rollback queued.", "DeployQueueResult"),
			"400":     jsonResponseSchema("The deployment target identity is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("Rollback preflight is not eligible.", "ErrorResponse"),
			"503":     jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The rollback could not be queued.", "ErrorResponse"),
		}
	case "GET /api/deploy/history":
		operation["description"] = "List newest deployment runs without log blobs. targetId must be a positive integer when supplied; limit defaults to 50 and accepts values from 1 through 500."
		operation["parameters"] = []map[string]any{
			{
				"name": "targetId", "in": "query", "required": false,
				"description": "Optional positive deployment target identity.",
				"schema":      map[string]any{"type": "integer", "minimum": 1},
			},
			{
				"name": "limit", "in": "query", "required": false,
				"description": "Requested history size; valid values are 1 through 500.",
				"schema":      map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "default": 50},
			},
		}
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Deployment run history without log bodies.", "DeployRunList"),
			"400":     jsonResponseSchema("targetId or limit is outside the accepted bounds.", "ErrorResponse"),
			"503":     jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Deployment history could not be read.", "ErrorResponse"),
		}
	case "GET /api/deploy/history/{id}/logs":
		operation["description"] = "Read the complete captured output for one persisted deployment run. List responses intentionally omit this log body."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Captured deployment run output.", "DeployRunLogs"),
			"400": jsonResponseSchema("The deployment run identity is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment run does not exist or its logs could not be read.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "POST /api/deploy/targets/{id}/staging":
		operation["description"] = "Derive one staging target from a canonical production target. Repository and executor intent are inherited, while the project directory remains isolated and environment values, webhook signing secrets, auto-deploy, domains, TLS, and DNS state are not copied."
		operation["requestBody"] = jsonRequestSchema("Staging-owned name, branch, and isolated absolute project directory.", "CreateDeployStagingRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Staging target and explicit non-copy receipt.", "DeployStagingReceipt"),
			"400":     jsonResponseSchema("The source is not production or the requested identity, branch, or storage boundary is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The source deployment target does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The staging target could not be created.", "ErrorResponse"),
		}
	case "GET /api/deploy/targets/{id}/revision":
		operation["description"] = "Compare the exact local checkout HEAD with the latest successful deployment and rollback candidate without fetching a remote or mutating the checkout. Distinct not_deployed and unavailable states prevent absent or unreadable checkouts from appearing healthy."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Read-only local deployment revision comparison.", "DeployRevisionComparison"),
			"400":     jsonResponseSchema("The deployment target identity is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"default": jsonResponseSchema("The deployment revision comparison could not be read.", "ErrorResponse"),
		}
	case "GET /api/deploy/targets/{id}/services":
		operation["description"] = "Observe every container in one configured local Docker Compose project. An empty project is returned as an array rather than null; Heyserver never broadens the command outside the persisted project directory and Compose file."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Observed project-scoped Compose service inventory.", "ComposeServiceList"),
			"400": jsonResponseSchema("The deployment target identity is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed or its project directory is unavailable.", "ErrorResponse"),
			"502": jsonResponseSchema("Docker Compose inventory failed or returned invalid output.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "GET /api/deploy/targets/{id}/services/{service}/logs":
		operation["description"] = "Read timestamped logs for one validated Compose service. The line request defaults to 200, accepts 1 through 1000, and the response retains at most the newest 1 MiB."
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters, map[string]any{
			"name": "tail", "in": "query", "required": false,
			"description": "Requested newest log lines; valid values are 1 through 1000.",
			"schema":      map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "default": 200},
		})
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Bounded Compose service logs and truncation state.", "ComposeServiceLogs"),
			"400": jsonResponseSchema("The target identity, service name, or tail value is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed or its project directory is unavailable.", "ErrorResponse"),
			"502": jsonResponseSchema("Docker Compose logs could not be read.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "POST /api/deploy/targets/{id}/services/{service}/{action}":
		operation["description"] = "Run one fixed project-scoped Compose service action. The body must be empty; arbitrary commands, force flags, and project-wide down operations are not accepted."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, parameter := range parameters {
			if parameter["name"] == "action" {
				parameter["schema"] = map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "recreate"}}
			}
		}
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Compose service action completed.", "ComposeServiceActionResult"),
			"400": jsonResponseSchema("The target identity, service, action, or non-empty request body is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed or its project directory is unavailable.", "ErrorResponse"),
			"502": jsonResponseSchema("The fixed Docker Compose action failed.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "GET /api/deploy/targets/{id}/environment":
		operation["description"] = "List only configured Compose environment variable names. Values are never returned; configured distinguishes an absent protected environment file from an existing file."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Write-only Compose environment metadata.", "DeployEnvironment"),
			"400": jsonResponseSchema("The deployment target identity is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed.", "ErrorResponse"),
			"502": jsonResponseSchema("The protected environment metadata could not be read.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service or protected environment store is unavailable.", "ErrorResponse"),
		}
	case "PUT /api/deploy/targets/{id}/environment":
		operation["description"] = "Atomically create or replace one Compose environment variable in the installation-owned protected file. The value is write-only; unknown fields and trailing JSON values are rejected."
		operation["requestBody"] = jsonRequestSchema("One validated environment key and write-only value.", "DeployEnvironmentSetRequest", true)
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Updated environment metadata without values.", "DeployEnvironment"),
			"400": jsonResponseSchema("The target identity, JSON body, key, or value is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed.", "ErrorResponse"),
			"413": jsonResponseSchema("The environment variable request exceeds the accepted body size.", "ErrorResponse"),
			"502": jsonResponseSchema("The protected environment file could not be updated.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service or protected environment store is unavailable.", "ErrorResponse"),
		}
	case "DELETE /api/deploy/targets/{id}/environment/{key}":
		operation["description"] = "Delete one validated Compose environment variable. The protected file is removed when no variables remain, and no value is returned."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Updated environment metadata without values.", "DeployEnvironment"),
			"400": jsonResponseSchema("The target identity or environment key is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed.", "ErrorResponse"),
			"502": jsonResponseSchema("The protected environment file could not be updated.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service or protected environment store is unavailable.", "ErrorResponse"),
		}
	case "GET /api/deploy/targets/{id}/domains":
		operation["description"] = "List installation-owned Nginx mappings for one local Compose target. Each response exposes only the fixed loopback upstream and observed TLS state; an empty inventory is an array rather than null."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Project domain inventory with fixed loopback upstreams and observed TLS state.", "DeployDomainList"),
			"400": jsonResponseSchema("The deployment target identity is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed.", "ErrorResponse"),
			"500": jsonResponseSchema("The project domain inventory could not be read.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "POST /api/deploy/targets/{id}/domains":
		operation["description"] = "Create one installation-owned Nginx mapping from a validated ASCII hostname and Compose service to an explicitly published host port. Unknown fields, trailing JSON values, arbitrary upstreams, and oversized bodies are rejected."
		operation["requestBody"] = jsonRequestSchema("Validated hostname, Compose service, and published host port.", "CreateDeployDomainRequest", true)
		operation["responses"] = map[string]any{
			"201": jsonResponseSchema("Project domain mapping activated and persisted.", "DeployDomain"),
			"400": jsonResponseSchema("The target identity, body, hostname, service, or host port is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed or the hostname already exists.", "ErrorResponse"),
			"413": jsonResponseSchema("The project domain request exceeds 8 KiB.", "ErrorResponse"),
			"500": jsonResponseSchema("The Nginx mapping could not be activated or persisted.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "DELETE /api/deploy/targets/{id}/domains/{domainId}":
		operation["description"] = "Transactionally deactivate and delete one installation-owned project domain mapping. The body must be empty; unrelated Nginx configuration is never accepted or removed."
		operation["responses"] = map[string]any{
			"204": map[string]any{"description": "Project domain mapping deleted; response body is empty."},
			"400": jsonResponseSchema("The target identity, domain identity, or non-empty request body is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target or project domain does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed.", "ErrorResponse"),
			"500": jsonResponseSchema("The Nginx mapping could not be deactivated or deleted.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "GET /api/deploy/targets/{id}/domains/{domainId}/health":
		operation["description"] = "Actively probe the fixed loopback upstream with redirects disabled and a three-second deadline. HTTP 200 through 399 is healthy, another response is unhealthy, and no response is unavailable."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Observed upstream response state and latency.", "DeployDomainHealth"),
			"400": jsonResponseSchema("The target identity or domain identity is invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target or project domain does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed.", "ErrorResponse"),
			"500": jsonResponseSchema("The bounded health probe could not be prepared.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "POST /api/deploy/targets/{id}/domains/{domainId}/tls":
		operation["description"] = "Obtain or reuse a certificate through the installation-owned Certbot HTTP-01 boundary, verify hostname coverage, and transactionally activate HTTPS. The optional strict JSON body supplies only an ACME email address."
		operation["requestBody"] = jsonRequestSchema("Optional bounded ACME account email; an empty body uses Certbot's no-email registration mode.", "EnableDeployDomainTLSRequest", false)
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Project domain with observed active TLS state.", "DeployDomain"),
			"400": jsonResponseSchema("The identities, JSON body, or email address are invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target or project domain does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed.", "ErrorResponse"),
			"413": jsonResponseSchema("The project domain TLS request exceeds 8 KiB.", "ErrorResponse"),
			"500": jsonResponseSchema("Certificate issuance, validation, Nginx activation, or persistence failed.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "DELETE /api/deploy/targets/{id}/domains/{domainId}/tls":
		operation["description"] = "Transactionally restore the installation-owned mapping to HTTP while preserving certificate files for rollback or reuse. The request body must be empty."
		operation["responses"] = map[string]any{
			"200": jsonResponseSchema("Project domain with observed TLS state disabled.", "DeployDomain"),
			"400": jsonResponseSchema("The identities or non-empty request body are invalid.", "ErrorResponse"),
			"404": jsonResponseSchema("The deployment target or project domain does not exist.", "ErrorResponse"),
			"409": jsonResponseSchema("The target is not Compose-backed.", "ErrorResponse"),
			"500": jsonResponseSchema("The HTTP mapping could not be restored or persisted.", "ErrorResponse"),
			"503": jsonResponseSchema("The deployment service is not initialized.", "ErrorResponse"),
		}
	case "GET /api/dns/status":
		operation["description"] = "Observe the provider-neutral readiness of the local BIND installation without installing, starting, or modifying it."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Observed local BIND readiness and capability boundary.", "BindServiceStatus"),
			"default": jsonResponseSchema("BIND readiness could not be observed.", "ErrorResponse"),
		}
	case "GET /api/dns/zones":
		operation["description"] = "List local master zones declared in the installation-owned BIND configuration with observed serials and record counts."
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Observed local BIND zones.", "BindZone"),
			"default": jsonResponseSchema("The local BIND zone inventory is unavailable.", "ErrorResponse"),
		}
	case "GET /api/dns/zones/{domain}":
		operation["description"] = "Read one validated local BIND zone and its parsed records."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Local BIND zone detail.", "BindZoneDetail"),
			"400":     jsonResponseSchema("The zone domain is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected zone does not exist or cannot be read.", "ErrorResponse"),
			"default": jsonResponseSchema("The local BIND zone could not be read.", "ErrorResponse"),
		}
	case "POST /api/dns/zones":
		operation["description"] = "Create one normalized local master zone through the durable two-file validation, journal, reload, and rollback transaction."
		operation["requestBody"] = jsonRequestSchema("Canonical zone domain and initial IPv4 address.", "BindCreateZoneRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Created local BIND zone detail.", "BindZoneDetail"),
			"400":     jsonResponseSchema("The strict body, zone domain, or IPv4 address is invalid.", "ErrorResponse"),
			"default": jsonResponseSchema("The zone transaction failed or was rolled back.", "ErrorResponse"),
		}
	case "DELETE /api/dns/zones/{domain}":
		operation["description"] = "Delete one normalized local master zone through the durable two-file validation, journal, reload, and rollback transaction. The request body must be empty."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Deleted local BIND zone receipt.", "ActionMessage"),
			"400":     jsonResponseSchema("The zone domain is invalid or the request contains a body.", "ErrorResponse"),
			"default": jsonResponseSchema("The zone deletion failed or was rolled back.", "ErrorResponse"),
		}
	case "GET /api/dns/zones/{domain}/export":
		operation["description"] = "Export the exact raw local zone file after validating the zone identity."
		operation["responses"] = map[string]any{
			"200": map[string]any{
				"description": "Raw BIND zone file.",
				"content":     map[string]any{"text/plain": map[string]any{"schema": map[string]any{"type": "string"}}},
			},
			"400":     jsonResponseSchema("The zone domain is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected zone does not exist or cannot be read.", "ErrorResponse"),
			"default": jsonResponseSchema("The zone file could not be exported.", "ErrorResponse"),
		}
	case "GET /api/dns/zones/{domain}/soa":
		operation["description"] = "Read the parsed SOA fields for one validated local BIND zone."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Current local zone SOA record.", "BindSOA"),
			"400":     jsonResponseSchema("The zone domain is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected zone or SOA record does not exist.", "ErrorResponse"),
			"default": jsonResponseSchema("The SOA record could not be read.", "ErrorResponse"),
		}
	case "PUT /api/dns/zones/{domain}/soa":
		operation["description"] = "Replace the complete SOA fields through a staged validation, atomic zone-file replacement, runtime rollback, and serial increment."
		operation["requestBody"] = jsonRequestSchema("Complete normalized SOA replacement fields.", "BindSOAUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("SOA update receipt.", "ActionMessage"),
			"400":     jsonResponseSchema("The strict body, zone domain, DNS names, or timing values are invalid.", "ErrorResponse"),
			"default": jsonResponseSchema("The SOA transaction failed or was rolled back.", "ErrorResponse"),
		}
	case "GET /api/dns/zones/{domain}/records":
		operation["description"] = "List parsed DNS resource records for one validated local BIND zone."
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Local BIND resource records.", "BindRecord"),
			"400":     jsonResponseSchema("The zone domain is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The selected zone does not exist or cannot be read.", "ErrorResponse"),
			"default": jsonResponseSchema("The zone records could not be read.", "ErrorResponse"),
		}
	case "POST /api/dns/zones/{domain}/records":
		operation["description"] = "Append one normalized resource record through a validate-before-replace zone transaction and optional zone reload rollback."
		operation["requestBody"] = jsonRequestSchema("Normalized record owner, type, value, TTL, priority, and reload policy.", "BindAddRecordRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Record creation receipt.", "ActionMessage"),
			"400":     jsonResponseSchema("The strict body, zone domain, or record fields are invalid.", "ErrorResponse"),
			"default": jsonResponseSchema("The record transaction failed or was rolled back.", "ErrorResponse"),
		}
	case "PUT /api/dns/zones/{domain}/records":
		operation["description"] = "Replace the first exact name, type, and old-value record match through the validated atomic zone transaction."
		operation["requestBody"] = jsonRequestSchema("Exact record identity and complete replacement fields.", "BindUpdateRecordRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Record update receipt.", "ActionMessage"),
			"400":     jsonResponseSchema("The strict body, zone domain, or record fields are invalid.", "ErrorResponse"),
			"default": jsonResponseSchema("The record was not found or its transaction failed.", "ErrorResponse"),
		}
	case "DELETE /api/dns/zones/{domain}/records":
		operation["description"] = "Delete the first exact record match using either the strict JSON identity body or the legacy exact query fields, never both, through the validated atomic zone transaction."
		operation["requestBody"] = jsonRequestSchema("Optional strict JSON record identity; omit it when using query parameters.", "BindDeleteRecordRequest", false)
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters, bindRecordDeleteQueryParameters()...)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Record deletion receipt.", "ActionMessage"),
			"400":     jsonResponseSchema("The zone, body, query source, or exact record identity is invalid or ambiguous.", "ErrorResponse"),
			"default": jsonResponseSchema("The record was not found or its transaction failed.", "ErrorResponse"),
		}
	case "POST /api/dns/reload":
		operation["description"] = "Run the fixed local rndc reload action after a readiness check. The request body must be empty."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("BIND reload receipt.", "ActionMessage"),
			"400":     jsonResponseSchema("The request contains a body.", "ErrorResponse"),
			"default": jsonResponseSchema("BIND is not ready or reload failed.", "ErrorResponse"),
		}
	case "POST /api/dns/check":
		operation["description"] = "Run named-checkconf and per-zone named-checkzone diagnostics. A failed configuration remains a successful diagnostic response with ok=false; the request body must be empty."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Complete BIND validation diagnostics.", "BindCheckResult"),
			"400":     jsonResponseSchema("The request contains a body.", "ErrorResponse"),
			"default": jsonResponseSchema("BIND diagnostics could not run.", "ErrorResponse"),
		}
	case "GET /api/dns/lookup":
		operation["description"] = "Query Google, Cloudflare, and the host resolver independently for one normalized DNS name and record type."
		operation["parameters"] = bindLookupQueryParameters()
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Per-resolver DNS lookup observations.", "BindLookupResponse"),
			"400":     jsonResponseSchema("The exact domain or optional type query is invalid or ambiguous.", "ErrorResponse"),
			"default": jsonResponseSchema("The lookup operation could not be started.", "ErrorResponse"),
		}
	case "GET /api/cloudflare/zones":
		operation["description"] = "List zones visible to the optional installation-owned Cloudflare credential. A missing integration is distinct from an empty provider inventory."
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Cloudflare zone inventory.", "CloudflareZone"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The Cloudflare integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("Cloudflare zones could not be read.", "ErrorResponse"),
		}
	case "GET /api/cloudflare/zones/{zoneId}":
		operation["description"] = "Return one Cloudflare zone by its provider identity."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Cloudflare zone.", "CloudflareZone"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The Cloudflare integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Cloudflare zone could not be read.", "ErrorResponse"),
		}
	case "GET /api/cloudflare/zones/{zoneId}/records":
		operation["description"] = "List Cloudflare DNS records for one exact zone, optionally filtered by provider record type and exact name."
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters,
			map[string]any{"name": "type", "in": "query", "required": false, "schema": map[string]any{"type": "string", "pattern": "^[A-Za-z]{1,16}$"}},
			map[string]any{"name": "name", "in": "query", "required": false, "schema": map[string]any{"type": "string", "minLength": 1, "maxLength": 253}},
		)
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Cloudflare DNS record inventory.", "CloudflareRecord"),
			"400":     jsonResponseSchema("A query filter is unknown, repeated, empty, or invalid.", "ErrorResponse"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The Cloudflare integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("Cloudflare DNS records could not be read.", "ErrorResponse"),
		}
	case "POST /api/cloudflare/zones/{zoneId}/records":
		operation["description"] = "Create one strictly validated Cloudflare DNS record. Record types normalize to uppercase; proxying is limited to A, AAAA, and CNAME."
		operation["requestBody"] = jsonRequestSchema("Complete Cloudflare DNS record definition.", "CloudflareRecordMutationRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Cloudflare DNS record created.", "CloudflareRecord"),
			"400":     jsonResponseSchema("The strict record payload is invalid.", "ErrorResponse"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The Cloudflare integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Cloudflare DNS record could not be created.", "ErrorResponse"),
		}
	case "PUT /api/cloudflare/zones/{zoneId}/records/{recordId}":
		operation["description"] = "Fully replace one Cloudflare DNS record with a strict complete payload. Clients performing partial user edits must first preserve every omitted field."
		operation["requestBody"] = jsonRequestSchema("Complete replacement Cloudflare DNS record definition.", "CloudflareRecordMutationRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Cloudflare DNS record replaced.", "CloudflareRecord"),
			"400":     jsonResponseSchema("The strict record payload is invalid.", "ErrorResponse"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The Cloudflare integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Cloudflare DNS record could not be replaced.", "ErrorResponse"),
		}
	case "DELETE /api/cloudflare/zones/{zoneId}/records/{recordId}":
		operation["description"] = "Delete one exact Cloudflare DNS record. The request body must be empty."
		operation["responses"] = map[string]any{
			"204":     map[string]any{"description": "Cloudflare DNS record deleted."},
			"400":     jsonResponseSchema("The request body is not empty.", "ErrorResponse"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The Cloudflare integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Cloudflare DNS record could not be deleted.", "ErrorResponse"),
		}
	case "PUT /api/cloudflare/zones/{zoneId}/records/{recordId}/proxy":
		operation["description"] = "Set one exact Cloudflare DNS record proxy state through a strict explicit boolean payload."
		operation["requestBody"] = jsonRequestSchema("Explicit Cloudflare proxy state.", "CloudflareProxyRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Cloudflare DNS proxy state updated.", "CloudflareRecord"),
			"400":     jsonResponseSchema("The strict proxy payload is invalid.", "ErrorResponse"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The Cloudflare integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Cloudflare DNS proxy state could not be updated.", "ErrorResponse"),
		}
	case "POST /api/cloudflare/zones/{zoneId}/purge":
		operation["description"] = "Purge the complete Cloudflare cache for one exact zone. The request body must be empty."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Cloudflare zone cache purged.", "CloudflarePurgeReceipt"),
			"400":     jsonResponseSchema("The request body is not empty.", "ErrorResponse"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The Cloudflare integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("The Cloudflare zone cache could not be purged.", "ErrorResponse"),
		}
	case "GET /api/cloudflare/zones/{zoneId}/email-routing":
		operation["description"] = "Read Cloudflare email-routing state for one exact zone."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Cloudflare email-routing state.", "CloudflareEmailRouting"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("The Cloudflare integration is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("Cloudflare email-routing state could not be read.", "ErrorResponse"),
		}
	case "POST /api/cloudflare/mail-autofix/{domain}":
		operation["description"] = "Reconcile bounded mail DNS records for one normalized DNS domain from the installation-owned mail contract. The request body must be empty."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Cloudflare mail DNS reconciliation receipt.", "CloudflareMailDNSReconcileResult"),
			"400":     jsonResponseSchema("The domain is invalid or the request body is not empty.", "ErrorResponse"),
			"502":     jsonResponseSchema("Cloudflare rejected or could not complete the provider request.", "ErrorResponse"),
			"503":     jsonResponseSchema("Cloudflare or the installation-owned mail DNS contract is not configured.", "ErrorResponse"),
			"default": jsonResponseSchema("Cloudflare mail DNS could not be reconciled.", "ErrorResponse"),
		}
	case "GET /api/notify/channels":
		operation["description"] = "List configured email, Telegram, Discord, and Slack notification channels. The response is a credential-free projection: provider passwords, bot tokens, webhook URLs, and raw provider payloads never cross this API boundary. Each redacted config string contains only editable non-secret fields and a secret_configured marker, while state and detail report canonical delivery availability."
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Redacted notification-channel inventory with canonical delivery state.", "NotificationChannel"),
			"503":     jsonResponseSchema("The notification channel repository or protected config store is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("Notification channels could not be read.", "ErrorResponse"),
		}
	case "GET /api/notify/channels/{id}":
		operation["description"] = "Return one redacted notification channel without returning its SMTP password, bot token, or webhook URL. The config string contains only editable non-secret fields and a secret_configured marker; state and detail report canonical delivery availability."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Redacted notification channel with canonical delivery state.", "NotificationChannel"),
			"400":     jsonResponseSchema("The notification channel identifier is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The notification channel does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The notification channel repository or protected config store is unavailable.", "ErrorResponse"),
			"default": jsonResponseSchema("The notification channel could not be read.", "ErrorResponse"),
		}
	case "GET /api/notify/rules":
		operation["description"] = "List canonical local alert rules. Legacy cpu, memory, and disk values are migrated in place to the evaluator type names."
		operation["responses"] = map[string]any{
			"200":     jsonArrayResponseSchema("Canonical alert rule inventory.", "AlertRule"),
			"503":     jsonResponseSchema("The notification subsystem is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Alert rules could not be read.", "ErrorResponse"),
		}
	case "GET /api/notify/rules/{id}":
		operation["description"] = "Return one canonical local alert rule."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Canonical alert rule.", "AlertRule"),
			"400":     jsonResponseSchema("The rule identifier is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The alert rule does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The notification subsystem is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The alert rule could not be read.", "ErrorResponse"),
		}
	case "POST /api/notify/rules":
		operation["description"] = "Create one strictly validated canonical alert rule. CPU, memory, disk, and failed-login rules trigger at or above the threshold; SSL expiry triggers at or below it; service-down rules trigger when the target unit is inactive."
		operation["requestBody"] = jsonRequestSchema("Complete type-specific alert rule definition.", "AlertRuleCreateRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Alert rule created.", "AlertRule"),
			"400":     jsonResponseSchema("The JSON body or type-specific alert rule definition is invalid.", "ErrorResponse"),
			"503":     jsonResponseSchema("The notification subsystem is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The alert rule could not be created.", "ErrorResponse"),
		}
	case "PUT /api/notify/rules/{id}":
		operation["description"] = "Overlay one or more provided fields onto the current alert rule, then validate and normalize the complete merged rule. Omitted fields remain unchanged."
		operation["requestBody"] = jsonRequestSchema("Strict partial alert rule update with at least one field.", "AlertRuleUpdateRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Alert rule updated.", "AlertRule"),
			"400":     jsonResponseSchema("The identifier, JSON body, or merged type-specific alert rule is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The alert rule does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The notification subsystem is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The alert rule could not be updated.", "ErrorResponse"),
		}
	case "DELETE /api/notify/rules/{id}":
		operation["description"] = "Delete one exact alert rule. Missing identifiers return not found rather than a false deletion receipt."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Alert rule deleted.", "DeleteStatus"),
			"400":     jsonResponseSchema("The rule identifier is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The alert rule does not exist.", "ErrorResponse"),
			"503":     jsonResponseSchema("The notification subsystem is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("The alert rule could not be deleted.", "ErrorResponse"),
		}
	case "GET /api/notify/history":
		operation["description"] = "Read alert dispatch history with strict canonical pagination. Limit defaults to 50 and accepts 1-200; offset defaults to zero."
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters,
			map[string]any{"name": "limit", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50}},
			map[string]any{"name": "offset", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 0, "default": 0}},
		)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Paginated alert history.", "AlertHistoryPage"),
			"400":     jsonResponseSchema("Pagination is non-canonical, repeated, unsupported, or outside the accepted range.", "ErrorResponse"),
			"503":     jsonResponseSchema("The notification subsystem is not initialized.", "ErrorResponse"),
			"default": jsonResponseSchema("Alert history could not be read.", "ErrorResponse"),
		}
	case "POST /api/deploy/webhook/{targetId}":
		operation["description"] = "Receive one provider-signed GitHub or GitLab webhook delivery without panel JWT authentication. GitHub push events require X-Hub-Signature-256 and X-GitHub-Delivery. GitLab Standard Webhooks require webhook-id, webhook-timestamp, and webhook-signature. Authenticated push delivery identities are persisted before eligibility checks so retries cannot queue the same deployment twice."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, header := range []struct {
			name        string
			description string
		}{
			{name: "X-GitHub-Event", description: "GitHub event name; push and ping have defined behavior."},
			{name: "X-GitHub-Delivery", description: "Unique GitHub delivery identity required for push events."},
			{name: "X-Hub-Signature-256", description: "GitHub HMAC-SHA256 signature over the exact request body."},
			{name: "X-Gitlab-Event", description: "GitLab event name; Push Hook is normalized to a push event."},
			{name: "webhook-id", description: "Stable GitLab Standard Webhooks delivery identity."},
			{name: "webhook-timestamp", description: "GitLab signed Unix timestamp accepted within five minutes."},
			{name: "webhook-signature", description: "One or more GitLab Standard Webhooks v1 signatures."},
		} {
			parameters = append(parameters, map[string]any{
				"name": header.name, "in": "header", "required": false,
				"description": header.description, "schema": map[string]any{"type": "string"},
			})
		}
		operation["parameters"] = parameters
		operation["requestBody"] = map[string]any{
			"description": "Exact provider webhook payload. Authenticated push events must contain a valid refs/heads/* ref.",
			"required":    false,
			"content": map[string]any{"application/json": map[string]any{
				"schema": map[string]any{"type": "object", "additionalProperties": true},
			}},
		}
		operation["responses"] = map[string]any{
			"200":     map[string]any{"description": "Authenticated ping, ignored event, disabled or non-target push, unknown target, or already processed delivery."},
			"202":     map[string]any{"description": "Authenticated matching push accepted and deployment queued."},
			"400":     map[string]any{"description": "Provider headers, delivery identity, timestamp, JSON body, or push ref are invalid."},
			"401":     map[string]any{"description": "Provider signature or signed timestamp could not be authenticated."},
			"403":     map[string]any{"description": "The target has no configured webhook signing secret."},
			"409":     map[string]any{"description": "The target failed deployment preflight."},
			"413":     map[string]any{"description": "The signed request body exceeds 10 MiB."},
			"503":     map[string]any{"description": "The installation-owned signing secret file is unavailable."},
			"default": map[string]any{"description": "The webhook delivery could not be processed."},
		}
	case "POST /api/agent/v1/heartbeat":
		operation["description"] = "Accept one bounded managed-node observation using the enrollment token, matching X-HServer-Node-ID identity, protocol v1, and an agent timestamp within five minutes of the hub clock."
		operation["requestBody"] = jsonRequestSchema("Protocol, node identity, advertised capabilities, observed host inventory, and agent timestamp.", "AgentHeartbeatRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Heartbeat accepted using the hub clock.", "AgentHeartbeatResponse"),
			"400":     jsonResponseSchema("The body, protocol, timestamp, capability set, or bounded inventory is invalid.", "ErrorResponse"),
			"401":     jsonResponseSchema("The node identity or enrollment token is invalid or mismatched.", "ErrorResponse"),
			"default": jsonResponseSchema("The heartbeat could not be persisted.", "ErrorResponse"),
		}
	case "POST /api/agent/v1/tasks/poll":
		operation["description"] = "Claim at most one queued task for the enrolled managed node. An empty object means no task is currently queued."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Claimed task or an empty poll receipt.", "AgentTaskPollResponse"),
			"401":     jsonResponseSchema("The node identity or enrollment token is invalid or mismatched.", "ErrorResponse"),
			"default": jsonResponseSchema("The next task could not be polled.", "ErrorResponse"),
		}
	case "POST /api/agent/v1/tasks/{id}/result":
		operation["description"] = "Complete one running task claimed by the authenticated managed node. Completed results remain bounded string maps; failed results require non-whitespace error text."
		operation["requestBody"] = jsonRequestSchema("Terminal task status plus a bounded result map or failure text.", "AgentTaskResultRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Persisted terminal task receipt.", "AgentTask"),
			"400":     jsonResponseSchema("The task identity, body, status, result, failure text, or claimed-task ownership is invalid.", "ErrorResponse"),
			"401":     jsonResponseSchema("The node identity or enrollment token is invalid or mismatched.", "ErrorResponse"),
			"default": jsonResponseSchema("The task result could not be persisted.", "ErrorResponse"),
		}
	case "GET /api/nodes":
		operation["description"] = "List enrolled managed nodes with server-observed online state, advertised capabilities, inventory, and panel compatibility."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Managed-node fleet snapshot.", "ManagedNodeList"),
			"default": jsonResponseSchema("The managed-node fleet could not be read.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}":
		operation["description"] = "Return one managed node. Online state is computed from the hub clock and the server-observed heartbeat."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Managed-node snapshot.", "ManagedNode"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"default": jsonResponseSchema("The managed node could not be read.", "ErrorResponse"),
		}
	case "PUT /api/nodes/{node_id}/deploy/{target_id}/domains/{domain}":
		operation["description"] = "Admin-only, revision-aware ensure of one managed project-domain mapping. The strict body carries only the expected lowercase SHA-256 revision (or absent) and confirmed=true; the agent resolves the target's installation-owned deploy plan and its fixed loopback upstream. Supplying the current revision makes retries compare-and-swap safe: an already active, enabled mapping returns changed=false as an idempotent no-op, while stale observations, drift, or a competing desired revision return a conflict. Nginx content, upstream URLs, certificate paths, Certbot arguments, shell commands, and other arbitrary input are never accepted from the caller."
		operation["requestBody"] = jsonRequestSchema("Strict managed project-domain ensure body with an expected revision and explicit confirmation.", "EnsureRemoteDeployDomainRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Typed managed project-domain ensure receipt with changed and the active observed mapping.", "EnsureRemoteDeployDomainResponse"),
			"400":     jsonResponseSchema("The strict body, confirmation, revision, target, or hostname is invalid.", "ErrorResponse"),
			"409":     jsonResponseSchema("The managed node is offline, lacks deploy.domain.action, has a stale or conflicting revision, or the observed mapping drifted; no unsafe desired input is applied.", "ErrorResponse"),
			"422":     jsonResponseSchema("The installation-owned managed deploy plan is invalid or cannot satisfy the domain operation.", "ErrorResponse"),
			"502":     jsonResponseSchema("The managed agent failed or returned an invalid project-domain receipt; raw agent and Nginx details are not exposed.", "ErrorResponse"),
			"504":     jsonResponseSchema("The fixed managed project-domain task did not complete before the bounded 75-second wait expired.", "ErrorResponse"),
			"default": jsonResponseSchema("The managed project-domain ensure operation could not be completed safely.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}/profile":
		operation["description"] = "Return the admin-only desired deployment capability profile, latest raw managed-node observation, and explicit apply lifecycle. An unconfigured desired profile is represented by desired.profile=null. A completed agent.profile.apply task is not application proof; observed.profileState=applied with observed.profileRevision matching desired.revision is authoritative."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Top-level nodeId with desired profile, raw managed-node observation, and current apply lifecycle.", "AgentProfileResponse"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"default": jsonResponseSchema("The managed-node profile could not be read.", "ErrorResponse"),
		}
	case "POST /api/nodes/{id}/profile/apply":
		operation["description"] = "Admin-only explicit application of the panel-owned desired deployment capability profile revision on an online managed node that advertises agent.profile.apply. The strict body contains exactly expectedRevision (at least 1) and confirmed=true; the hub reads the persisted desired profile and constructs the fixed task, so callers cannot provide a profile, task payload, command, path, environment value, or secret value. HTTP 200 means a heartbeat already verified observed.profileState=applied with the requested revision; HTTP 202 means the task is queued or still pending heartbeat confirmation. A completed task is only a transport acknowledgement until that matching heartbeat arrives."
		operation["requestBody"] = jsonRequestSchema("Strict profile-apply confirmation for one positive desired-profile revision.", "AgentProfileApplyRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("The latest accepted heartbeat verifies the requested profile revision as applied; no duplicate task was created.", "AgentProfileResponse"),
			"202":     jsonResponseSchema("The profile task was queued or remains pending until a heartbeat verifies the requested revision.", "AgentProfileResponse"),
			"400":     jsonResponseSchema("The strict body is invalid; expectedRevision must be at least 1 and confirmed must be true.", "ErrorResponse"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The profile is not configured, the desired revision is stale, an apply is already in flight, the capability is unavailable, or the node is offline; no new task is created.", "ErrorResponse"),
			"429":     jsonResponseSchema("The authenticated mutation rate limit is active.", "ErrorResponse"),
			"default": jsonResponseSchema("The managed-node profile apply operation failed.", "ErrorResponse"),
		}
	case "PUT /api/nodes/{id}/profile":
		operation["description"] = "Atomically replace the admin-only desired deployment capability profile with compare-and-swap revision semantics. The strict body contains exactly profile and expectedRevision; the profile contains fixed capability booleans and optional clean absolute paths only. Saving desired state creates no agent task and never claims agent application; use the explicit profile/apply operation after reviewing the saved revision."
		operation["requestBody"] = jsonRequestSchema("Strict desired profile replacement with a non-negative compare-and-swap revision.", "AgentProfilePutRequest", true)
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Top-level nodeId with the saved desired profile; no agent task was created.", "AgentProfileResponse"),
			"400":     jsonResponseSchema("The strict body, fixed profile fields, path bounds, capability dependencies, or non-negative revision is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The expected profile revision is stale; no newer desired state was overwritten.", "ErrorResponse"),
			"429":     jsonResponseSchema("The authenticated mutation rate limit is active.", "ErrorResponse"),
			"default": jsonResponseSchema("The managed-node profile could not be saved.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}/integrations/status":
		operation["description"] = "Return one fresh schema-v1, read-only managed-node integration observation. The online node's integration.status capability runs exactly one bounded batched task containing the fixed process.pm2/pm2_inventory and container.docker/docker_info probes; concurrent requests for the same node coalesce onto one queued or running task and wait at most 45 seconds. The typed response contains only canonical states, safe error codes, durations, and the managed-node target identity: process/container inventory, raw probe or task errors, command output, paths, and secrets never cross this endpoint."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Fresh typed managed-node integration status with exactly the PM2 and Docker probe results.", "ManagedIntegrationStatusResponse"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The managed node is offline or does not advertise integration.status; no task is created. The safe error is managed_node_offline or capability_unavailable.", "ErrorResponse"),
			"502":     jsonResponseSchema("The managed agent failed, returned an incomplete task, or returned invalid typed status data; the safe error is managed_status_failed and raw task or probe errors are not exposed.", "ErrorResponse"),
			"504":     jsonResponseSchema("The single managed-node status task did not complete before the bounded 45-second wait expired; the safe error is managed_status_timeout.", "ErrorResponse"),
			"default": jsonResponseSchema("Managed-node integration status could not be collected safely.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}/metrics":
		operation["description"] = "Return one fresh, read-only typed metrics snapshot from an online managed node. The node must advertise metrics.read; the hub queues one fixed empty-payload metrics.read task and waits up to 45 seconds. Success contains only the bounded CPU, load, memory, network, root-disk, and observation timestamp fields; task envelopes, command output, paths, provider data, and secrets never cross this endpoint."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Fresh typed managed-node metrics snapshot.", "ManagedNodeMetrics"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The managed node is offline or does not advertise metrics.read; no metrics task is created.", "ErrorResponse"),
			"502":     jsonResponseSchema("The managed metrics task failed or returned invalid typed snapshot data.", "ErrorResponse"),
			"504":     jsonResponseSchema("The managed metrics task did not complete before the bounded 45-second wait expired.", "ErrorResponse"),
			"default": jsonResponseSchema("Managed-node metrics could not be read.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}/php":
		operation["description"] = "Return one bounded, read-only PHP-FPM inventory from an online managed node. The node must advertise php.read; the hub queues one php.inventory task and waits up to 35 seconds. Success is limited to the credential-free typed version and pool projection, with no arbitrary provider record, task envelope, command output, or secret field in the response."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Bounded credential-free managed-node PHP-FPM version and pool inventory.", "ManagedPHPFPMVersionList"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The managed node is offline or does not advertise php.read; no PHP-FPM inventory task is created.", "ErrorResponse"),
			"502":     jsonResponseSchema("The managed PHP-FPM task failed or returned invalid typed inventory data.", "ErrorResponse"),
			"504":     jsonResponseSchema("The managed PHP-FPM inventory task did not complete before the bounded 35-second wait expired.", "ErrorResponse"),
			"default": jsonResponseSchema("Managed-node PHP-FPM inventory could not be read.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}/pm2":
		operation["description"] = "Return one bounded, read-only PM2 process inventory from an online managed node. The node must advertise pm2.read; the hub queues one pm2.list task and waits up to 35 seconds. Success is limited to the credential-free typed process projection, with no arbitrary provider record, task envelope, command output, or secret field in the response."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Bounded credential-free managed-node PM2 process inventory.", "ManagedPM2ProcessList"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The managed node is offline or does not advertise pm2.read; no PM2 inventory task is created.", "ErrorResponse"),
			"502":     jsonResponseSchema("The managed PM2 task failed or returned invalid typed inventory data.", "ErrorResponse"),
			"504":     jsonResponseSchema("The managed PM2 inventory task did not complete before the bounded 35-second wait expired.", "ErrorResponse"),
			"default": jsonResponseSchema("Managed-node PM2 inventory could not be read.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}/containers":
		operation["description"] = "Return one bounded, read-only Docker container inventory from an online managed node. The node must advertise container.read; the hub queues one container.list task and waits up to 30 seconds. Success is limited to the credential-free typed container projection, with no arbitrary provider record, task envelope, command output, or secret field in the response."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Bounded credential-free managed-node Docker container inventory.", "ManagedContainerList"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The managed node is offline or does not advertise container.read; no container inventory task is created.", "ErrorResponse"),
			"502":     jsonResponseSchema("The managed container task failed or returned invalid typed inventory data.", "ErrorResponse"),
			"504":     jsonResponseSchema("The managed container inventory task did not complete before the bounded 30-second wait expired.", "ErrorResponse"),
			"default": jsonResponseSchema("Managed-node Docker container inventory could not be read.", "ErrorResponse"),
		}
	case "POST /api/nodes/{id}/tasks":
		operation["description"] = "Queue one explicitly confirmed structured task after validating its fixed kind, payload, advertised capability, and server-observed node connectivity."
		operation["requestBody"] = jsonRequestSchema("Explicitly confirmed fixed managed-node task kind and bounded string payload.", "AgentTaskRequest", true)
		operation["responses"] = map[string]any{
			"201":     jsonResponseSchema("Managed-node task queued.", "AgentTask"),
			"400":     jsonResponseSchema("The confirmation, task kind, or payload is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"409":     jsonResponseSchema("The node is offline or does not advertise the required capability; no task was created.", "ErrorResponse"),
			"default": jsonResponseSchema("The task could not be queued.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}/tasks":
		operation["description"] = "List newest managed-node tasks. The optional positive limit defaults to 20 and is capped to 50."
		parameters, _ := operation["parameters"].([]map[string]any)
		operation["parameters"] = append(parameters, map[string]any{
			"name":        "limit",
			"in":          "query",
			"required":    false,
			"description": "Positive requested history size; the server caps values above 50.",
			"schema":      map[string]any{"type": "integer", "minimum": 1, "default": 20},
		})
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Managed-node task history.", "AgentTaskList"),
			"400":     jsonResponseSchema("The task limit is invalid.", "ErrorResponse"),
			"404":     jsonResponseSchema("The managed node does not exist.", "ErrorResponse"),
			"default": jsonResponseSchema("The task history could not be read.", "ErrorResponse"),
		}
	case "GET /api/nodes/{id}/tasks/{taskID}":
		operation["description"] = "Return one task owned by the selected managed node."
		operation["responses"] = map[string]any{
			"200":     jsonResponseSchema("Managed-node task.", "AgentTask"),
			"404":     jsonResponseSchema("The task does not exist for this managed node.", "ErrorResponse"),
			"default": jsonResponseSchema("The task could not be read.", "ErrorResponse"),
		}
	}
}

func bindLookupQueryParameters() []map[string]any {
	return []map[string]any{
		{
			"name": "domain", "in": "query", "required": true,
			"description": "Normalized DNS lookup name; underscore service labels are supported.",
			"schema":      map[string]any{"type": "string", "minLength": 1, "maxLength": 253},
		},
		{
			"name": "type", "in": "query", "required": false,
			"description": "One ASCII-letter DNS record type; defaults to A.",
			"schema":      map[string]any{"type": "string", "pattern": "^[A-Za-z]{1,16}$", "default": "A"},
		},
	}
}

func bindRecordDeleteQueryParameters() []map[string]any {
	return []map[string]any{
		{
			"name": "name", "in": "query", "required": false,
			"description": "Exact record owner name. Required when the JSON body is omitted.",
			"schema":      map[string]any{"type": "string", "minLength": 1, "maxLength": 253},
		},
		{
			"name": "type", "in": "query", "required": false,
			"description": "Exact record type. Required when the JSON body is omitted.",
			"schema":      map[string]any{"type": "string", "pattern": "^[A-Za-z]{1,16}$"},
		},
		{
			"name": "value", "in": "query", "required": false,
			"description": "Exact record value. Required when the JSON body is omitted.",
			"schema":      map[string]any{"type": "string", "minLength": 1, "maxLength": 4096},
		},
		{
			"name": "autoReload", "in": "query", "required": false,
			"description": "Whether to reload the zone after deletion.",
			"schema":      map[string]any{"type": "boolean", "default": false},
		},
	}
}

func cronUserQueryParameter() map[string]any {
	return map[string]any{
		"name":        "user",
		"in":          "query",
		"required":    false,
		"description": "Owning system user; defaults to root when omitted.",
		"schema": map[string]any{
			"type":      "string",
			"pattern":   `^[a-zA-Z_][a-zA-Z0-9_.-]{0,31}$`,
			"default":   "root",
			"minLength": 1,
			"maxLength": 32,
		},
	}
}

func integrationCatalogSchemas() map[string]any {
	const catalogSchemaMarker = "./catalog.schema.json"
	canonicalStates := []string{"not_configured", "unavailable", "healthy"}

	return map[string]any{
		"IntegrationCatalog": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"schema_version", "documentation", "entries"},
			"description":          "Schema-v1 optional-integration metadata catalog. This is not a live-health response.",
			"properties": map[string]any{
				"$schema": map[string]any{
					"type":        "string",
					"const":       catalogSchemaMarker,
					"description": "Optional relative source marker; clients must treat it as a local marker, not as a dereferenceable URI.",
				},
				"schema_version": map[string]any{
					"type":        "integer",
					"const":       1,
					"description": "Catalog wire schema version.",
				},
				"documentation": schemaRef("IntegrationCatalogDocumentation"),
				"entries": map[string]any{
					"type":        "array",
					"minItems":    15,
					"items":       schemaRef("IntegrationCatalogEntry"),
					"description": "The reviewed catalog contains at least 15 required core entries; additive entries allowed in schema v1.",
				},
			},
		},
		"IntegrationCatalogDocumentation": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"table_path", "table_header", "marker_prefix", "marker_convention"},
			"properties": map[string]any{
				"table_path": map[string]any{
					"type":  "string",
					"const": "docs/optional-integrations.md",
				},
				"table_header": map[string]any{
					"type":  "string",
					"const": "Integration",
				},
				"marker_prefix": map[string]any{
					"type":  "string",
					"const": "optional-integrations:v1:",
				},
				"marker_convention": map[string]any{
					"type":  "string",
					"const": "marker_prefix + slug(display_name), where slug lowercases and joins non-alphanumeric runs with a hyphen",
				},
			},
		},
		"IntegrationCatalogEntry": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"id", "display_name", "purpose", "requirement", "docs_row_marker",
				"classes", "targets", "configuration", "status", "evidence",
			},
			"properties": map[string]any{
				"id": map[string]any{
					"type":    "string",
					"pattern": "^[a-z0-9]+(?:[._-][a-z0-9]+)*$",
				},
				"display_name": map[string]any{
					"type":      "string",
					"minLength": 1,
				},
				"purpose": map[string]any{
					"type":      "string",
					"minLength": 1,
				},
				"requirement": map[string]any{
					"type": "string",
					"enum": []string{"optional", "feature_specific"},
				},
				"docs_row_marker": map[string]any{
					"type":    "string",
					"pattern": "^optional-integrations:v1:[a-z0-9]+(?:-[a-z0-9]+)*$",
				},
				"classes": map[string]any{
					"type":        "array",
					"minItems":    1,
					"uniqueItems": true,
					"items": map[string]any{
						"type": "string",
						"enum": []string{"local_capability", "managed_node_capability", "provider_adapter", "client_surface"},
					},
				},
				"targets": map[string]any{
					"type":        "array",
					"minItems":    1,
					"uniqueItems": true,
					"items": map[string]any{
						"type": "string",
						"enum": []string{"local_host", "managed_node"},
					},
				},
				"configuration": schemaRef("IntegrationConfiguration"),
				"status":        schemaRef("IntegrationStatus"),
				"agent":         schemaRef("IntegrationAgent"),
				"evidence":      schemaRef("IntegrationEvidence"),
			},
			"allOf": []any{
				map[string]any{
					"if": map[string]any{
						"properties": map[string]any{
							"classes": map[string]any{
								"contains": map[string]any{"const": "managed_node_capability"},
							},
						},
					},
					"then": map[string]any{"required": []string{"agent"}},
				},
				map[string]any{
					"if": map[string]any{
						"properties": map[string]any{
							"targets": map[string]any{
								"contains": map[string]any{"const": "managed_node"},
							},
						},
					},
					"then": map[string]any{"required": []string{"agent"}},
				},
			},
		},
		"IntegrationConfiguration": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"non_secret_keys", "secret_key_names", "secret_file_refs", "boundary"},
			"properties": map[string]any{
				"non_secret_keys": map[string]any{
					"type":        "array",
					"uniqueItems": true,
					"items":       map[string]any{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_.-]*$"},
					"description": "Installation-owned non-secret configuration names; values are not returned.",
				},
				"secret_key_names": map[string]any{
					"type":        "array",
					"uniqueItems": true,
					"items":       map[string]any{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_.-]*$"},
					"description": "Secret names only; secret values are never returned by this metadata contract.",
				},
				"secret_file_refs": map[string]any{
					"type":        "array",
					"uniqueItems": true,
					"items":       map[string]any{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_./-]*$"},
					"description": "Protected secret file names or references only; file contents are never returned.",
				},
				"boundary": map[string]any{
					"type":        "string",
					"minLength":   1,
					"description": "Narrative boundary for installation-owned configuration and secret handling; it contains no secret value.",
				},
			},
		},
		"IntegrationStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"canonical_states", "raw_state_mappings", "api_route_prefixes"},
			"properties": map[string]any{
				"canonical_states": map[string]any{
					"type":        "array",
					"const":       canonicalStates,
					"minItems":    3,
					"maxItems":    3,
					"description": "The exact canonical state trio, in this order.",
					"items":       map[string]any{"type": "string", "enum": canonicalStates},
				},
				"raw_state_mappings": map[string]any{
					"type":        "array",
					"minItems":    3,
					"items":       schemaRef("IntegrationRawStateMapping"),
					"description": "Implementation-specific observations mapped to canonical states; this is metadata, not live health.",
				},
				"api_route_prefixes": map[string]any{
					"type":        "array",
					"minItems":    1,
					"uniqueItems": true,
					"items":       map[string]any{"type": "string", "pattern": "^/api(?:/|$)"},
				},
			},
		},
		"IntegrationRawStateMapping": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"raw", "canonical", "meaning"},
			"properties": map[string]any{
				"raw": map[string]any{
					"type":      "string",
					"minLength": 1,
				},
				"canonical": map[string]any{
					"type": "string",
					"enum": canonicalStates,
				},
				"meaning": map[string]any{
					"type":      "string",
					"minLength": 1,
				},
			},
		},
		"IntegrationAgent": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"tasks", "capabilities", "evidence"},
			"properties": map[string]any{
				"tasks": map[string]any{
					"type":        "array",
					"minItems":    1,
					"uniqueItems": true,
					"items":       map[string]any{"type": "string", "pattern": "^Task[A-Za-z0-9_]+$"},
				},
				"capabilities": map[string]any{
					"type":        "array",
					"minItems":    1,
					"uniqueItems": true,
					"items":       map[string]any{"type": "string", "pattern": "^Capability[A-Za-z0-9_]+$"},
				},
				"evidence": schemaRef("IntegrationAgentEvidence"),
			},
		},
		"IntegrationAgentEvidence": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"tasks", "capabilities"},
			"properties": map[string]any{
				"tasks": map[string]any{
					"type":     "array",
					"minItems": 1,
					"items":    schemaRef("IntegrationEvidenceItem"),
				},
				"capabilities": map[string]any{
					"type":     "array",
					"minItems": 1,
					"items":    schemaRef("IntegrationEvidenceItem"),
				},
			},
		},
		"IntegrationEvidence": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"web", "docs", "tests"},
			"properties": map[string]any{
				"web":   evidenceItemArraySchema(),
				"docs":  evidenceItemArraySchema(),
				"tests": evidenceItemArraySchema(),
			},
		},
		"IntegrationEvidenceItem": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"path", "claim"},
			"properties": map[string]any{
				"path": map[string]any{
					"type":    "string",
					"pattern": "^(?!/)(?!.*\\.\\./).+",
				},
				"claim": map[string]any{
					"type":      "string",
					"minLength": 1,
				},
			},
		},
	}
}

func integrationStatusSchemas() map[string]any {
	canonicalStates := []string{"not_configured", "unavailable", "healthy"}
	errorCodes := []string{"not_configured", "probe_failed", "timeout"}

	return map[string]any{
		"IntegrationStatusResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"schema_version", "observed_at", "target", "results", "unprobed", "partial"},
			"description":          "Schema-v1 fresh local-only integration status aggregate. It contains safe probe results for every registered local catalog entry, including reviewed additive entries, and reports catalog entries without a code-owned probe in unprobed. Notification delivery stays unavailable without a persisted fresh successful receipt; this is not a managed-node status response.",
			"properties": map[string]any{
				"schema_version": map[string]any{
					"type":        "integer",
					"const":       1,
					"description": "Status response wire schema version.",
				},
				"observed_at": map[string]any{
					"type":        "string",
					"format":      "date-time",
					"description": "UTC RFC3339 timestamp captured when this fresh aggregate observation started.",
				},
				"target": schemaRef("IntegrationStatusTarget"),
				"results": map[string]any{
					"type":        "array",
					"minItems":    0,
					"description": "Safe results for each registered local read-only probe whose ID is present in the current catalog. The fifteen required core probes remain stable and reviewed additive entries may add further results; healthy requires a fresh successful observation, and an item failure remains represented here rather than failing the aggregate.",
					"items":       schemaRef("IntegrationStatusResult"),
				},
				"unprobed": map[string]any{
					"type":        "array",
					"minItems":    0,
					"uniqueItems": true,
					"description": "Catalog integration IDs without a registered code-owned local probe. This list is normally empty for the fifteen core entries and may contain reviewed additive entries until their implementation is wired.",
					"items": map[string]any{
						"type":    "string",
						"pattern": "^[a-z0-9]+(?:[._-][a-z0-9]+)*$",
					},
				},
				"partial": map[string]any{
					"type":        "boolean",
					"description": "True when an integration remains unprobed or a probe failed or timed out.",
				},
			},
		},
		"IntegrationStatusTarget": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"scope"},
			"properties": map[string]any{
				"scope": map[string]any{
					"type":        "string",
					"const":       "local_host",
					"description": "The only target scope supported by this aggregate.",
				},
			},
		},
		"IntegrationStatusResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "state", "probe"},
			"description":          "One safe local integration probe result. Provider error details and secrets are never serialized.",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"pattern":     "^[a-z0-9]+(?:[._-][a-z0-9]+)*$",
					"description": "Catalog entry ID. The ID must be present in the current catalog; the pattern is the catalog's provider-neutral syntax for core and reviewed additive entries.",
				},
				"state": map[string]any{
					"type":        "string",
					"enum":        canonicalStates,
					"description": "Exact canonical availability state.",
				},
				"probe": map[string]any{
					"type":        "string",
					"pattern":     "^[a-z][a-z0-9_]*$",
					"description": "Safe code-owned probe identifier. Core names remain fixed; additive names are deterministic ID-derived tokens, and command output and provider details are not returned.",
				},
				"error_code": map[string]any{
					"type":        "string",
					"enum":        errorCodes,
					"description": "Optional bounded failure classification; raw provider errors are never returned.",
				},
				"duration_ms": map[string]any{
					"type":        "integer",
					"format":      "int64",
					"minimum":     0,
					"description": "Optional non-negative wall-clock probe duration in milliseconds.",
				},
			},
		},
	}
}

func managedIntegrationStatusSchemas() map[string]any {
	canonicalStates := []string{"not_configured", "unavailable", "healthy"}
	resultIDs := []string{"process.pm2", "container.docker"}
	probeIDs := []string{"pm2_inventory", "docker_info"}
	errorCodes := []string{"not_configured", "probe_failed", "timeout"}

	return map[string]any{
		"ManagedIntegrationStatusResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"schema_version", "observed_at", "target", "results", "partial"},
			"description":          "Schema-v1 fresh read-only managed-node integration status aggregate. Exactly one bounded batched task observes the fixed PM2 and Docker probes; only safe typed observations cross the panel API boundary.",
			"properties": map[string]any{
				"schema_version": map[string]any{
					"type":        "integer",
					"const":       1,
					"description": "Managed integration status wire schema version.",
				},
				"observed_at": map[string]any{
					"type":        "string",
					"format":      "date-time",
					"description": "UTC RFC3339 timestamp for the managed-node observation.",
				},
				"target": schemaRef("ManagedIntegrationStatusTarget"),
				"results": map[string]any{
					"type":        "array",
					"minItems":    2,
					"maxItems":    2,
					"description": "Exactly the process.pm2/pm2_inventory and container.docker/docker_info results; process or container inventory is never embedded.",
					"items":       schemaRef("ManagedIntegrationStatusResult"),
				},
				"partial": map[string]any{
					"type":        "boolean",
					"description": "True when either fixed probe is not_configured or unavailable; false only when both probes are healthy.",
				},
			},
		},
		"ManagedIntegrationStatusTarget": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"scope", "node_id"},
			"description":          "The managed node whose fixed integration probes were observed.",
			"properties": map[string]any{
				"scope": map[string]any{
					"type":        "string",
					"const":       "managed_node",
					"description": "The only target scope supported by this aggregate.",
				},
				"node_id": map[string]any{
					"type":        "string",
					"pattern":     `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`,
					"description": "Enrolled managed-node identifier.",
				},
			},
		},
		"ManagedIntegrationStatusResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "state", "probe"},
			"description":          "One safe managed-node integration probe result. Canonical state, bounded safe error code, and duration are exposed; raw provider errors, command output, paths, secrets, and inventory are never serialized.",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"enum":        resultIDs,
					"description": "Exact managed integration identifier.",
				},
				"state": map[string]any{
					"type":        "string",
					"enum":        canonicalStates,
					"description": "Exact canonical availability state.",
				},
				"probe": map[string]any{
					"type":        "string",
					"enum":        probeIDs,
					"description": "Fixed safe probe identifier paired with the result ID.",
				},
				"error_code": map[string]any{
					"type":        "string",
					"enum":        errorCodes,
					"description": "Optional bounded safe failure classification; raw provider and task errors are never returned.",
				},
				"duration_ms": map[string]any{
					"type":        "integer",
					"format":      "int64",
					"minimum":     0,
					"description": "Optional non-negative wall-clock probe duration in milliseconds.",
				},
			},
		},
	}
}

func evidenceItemArraySchema() map[string]any {
	return map[string]any{
		"type":     "array",
		"minItems": 1,
		"items":    schemaRef("IntegrationEvidenceItem"),
	}
}

func databaseEngineSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        []string{"postgres", "postgresql", "mariadb", "mysql"},
		"description": "PostgreSQL and MariaDB aliases accepted by the local API; responses use postgres or mariadb.",
	}
}

func databaseEngineQueryParameter() map[string]any {
	return map[string]any{
		"name":        "engine",
		"in":          "query",
		"required":    false,
		"description": "Optional local engine filter.",
		"schema":      databaseEngineSchema(),
	}
}

func mailDomainQueryParameter() map[string]any {
	return map[string]any{
		"name":        "domain",
		"in":          "query",
		"required":    false,
		"description": "Optional exact mail domain filter.",
		"schema":      map[string]any{"type": "string", "maxLength": 253},
	}
}

func mailAddressSchema() map[string]any {
	return map[string]any{"type": "string", "maxLength": 254}
}

func mailAccountResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"email", "name", "domain", "quota", "usedStorage", "isEnabled", "aliases"},
		"description":          "Credential-free mailbox account projection; passwords, secrets, hashes, and provider response bodies are never included.",
		"properties": map[string]any{
			"email":       mailAddressSchema(),
			"name":        map[string]any{"type": "string"},
			"domain":      map[string]any{"type": "string", "maxLength": 253},
			"quota":       map[string]any{"type": "integer", "format": "int64"},
			"usedStorage": map[string]any{"type": "integer", "format": "int64"},
			"isEnabled":   map[string]any{"type": "boolean"},
			"aliases":     map[string]any{"type": "array", "items": mailAddressSchema()},
		},
	}
}

func mailAliasResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"id", "address", "destinations"},
		"description":          "Credential-free mail alias projection; passwords, secrets, hashes, and provider response bodies are never included.",
		"properties": map[string]any{
			"id":           map[string]any{"type": "string", "maxLength": 254},
			"address":      mailAddressSchema(),
			"destinations": map[string]any{"type": "array", "items": mailAddressSchema()},
			"description":  map[string]any{"type": "string"},
		},
	}
}

func mailServiceStatusSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"running", "status"},
		"description":          "Read-only local systemd status for the optional Stalwart mail service; it contains no configuration, credential, or log body.",
		"properties": map[string]any{
			"running": map[string]any{"type": "boolean"},
			"status":  map[string]any{"type": "string", "enum": []string{"running", "stopped", "failed", "unknown", "not_configured"}},
			"pid":     map[string]any{"type": "string"},
			"uptime":  map[string]any{"type": "string"},
		},
	}
}

func mailServiceVersionSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"raw", "version"},
		"description":          "Parsed Stalwart binary version; raw is only the binary's --version output, not configuration or log content.",
		"properties": map[string]any{
			"raw":     map[string]any{"type": "string"},
			"version": map[string]any{"type": "string"},
		},
	}
}

func mailServiceListenerSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"id", "protocol", "tls"},
		"description":          "Parsed Stalwart listener metadata without raw configuration or credentials.",
		"properties": map[string]any{
			"id":       map[string]any{"type": "string"},
			"protocol": map[string]any{"type": "string"},
			"bind":     map[string]any{"type": "string"},
			"port":     map[string]any{"type": "integer"},
			"tls":      map[string]any{"type": "boolean"},
		},
	}
}

func mailServiceStorageSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"backend"},
		"description":          "Parsed Stalwart storage metadata and best-effort local usage without raw configuration or credentials.",
		"properties": map[string]any{
			"backend":   map[string]any{"type": "string"},
			"path":      map[string]any{"type": "string"},
			"sizeBytes": map[string]any{"type": "integer", "format": "int64", "minimum": 0},
		},
	}
}

func mailServiceSourceSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"available", "state"},
		"description":          "Availability of one Stalwart overview discovery source; unavailable and not_configured remain distinct.",
		"properties": map[string]any{
			"available": map[string]any{"type": "boolean"},
			"state":     map[string]any{"type": "string", "enum": []string{"healthy", "unavailable", "not_configured"}},
			"error":     map[string]any{"type": "string"},
		},
	}
}

func mailServiceStatusSourceSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"available", "state"},
		"description":          "Availability of the local systemd status source; its state uses the same status labels as MailServiceStatus.",
		"properties": map[string]any{
			"available": map[string]any{"type": "boolean"},
			"state":     map[string]any{"type": "string", "enum": []string{"running", "stopped", "failed", "unknown", "not_configured"}},
			"error":     map[string]any{"type": "string"},
		},
	}
}

func mailServiceOverviewSourcesSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"status", "version", "listeners", "storage"},
		"properties": map[string]any{
			"status":    schemaRef("MailServiceStatusSource"),
			"version":   schemaRef("MailServiceSource"),
			"listeners": schemaRef("MailServiceSource"),
			"storage":   schemaRef("MailServiceSource"),
		},
	}
}

func mailServiceOverviewSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"status", "version", "listeners", "storage", "sources"},
		"description":          "Read-only Stalwart service overview. Source availability and errors are represented under sources; raw configuration and log bodies are not included.",
		"properties": map[string]any{
			"status":  schemaRef("MailServiceStatus"),
			"version": schemaRef("MailServiceVersion"),
			"listeners": map[string]any{
				"type":  []string{"array", "null"},
				"items": schemaRef("MailServiceListener"),
			},
			"storage": schemaRef("MailServiceStorage"),
			"sources": schemaRef("MailServiceOverviewSources"),
		},
	}
}

func notificationChannelResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"id", "name", "type", "config", "enabled", "createdAt", "updatedAt", "state", "detail"},
		"description":          "Credential-free notification-channel projection; config is redacted and never contains passwords, bot tokens, webhook URLs, or provider response bodies.",
		"properties": map[string]any{
			"id":        map[string]any{"type": "integer", "format": "int64", "minimum": 1},
			"name":      map[string]any{"type": "string", "minLength": 1},
			"type":      map[string]any{"type": "string", "enum": []string{"email", "telegram", "discord", "slack"}},
			"config":    map[string]any{"type": "string", "description": "Redacted JSON string containing editable non-secret fields and secret_configured only."},
			"enabled":   map[string]any{"type": "boolean"},
			"createdAt": map[string]any{"type": "string", "format": "date-time"},
			"updatedAt": map[string]any{"type": "string", "format": "date-time"},
			"state":     map[string]any{"type": "string", "enum": []string{"not_configured", "unavailable", "healthy"}},
			"detail": map[string]any{
				"type": "string",
				"enum": []string{"not_configured", "config_unavailable", "configured_disabled", "degraded", "probe_unverified", "delivery_confirmed", "delivery_failed", "delivery_stale"},
			},
		},
	}
}

func databaseIdentifierSchema() map[string]any {
	return map[string]any{
		"type":      "string",
		"minLength": 1,
		"maxLength": 64,
		"pattern":   `^[A-Za-z0-9_-]{1,64}$`,
	}
}

func databaseBackupNameSchema() map[string]any {
	return map[string]any{
		"type":      "string",
		"minLength": 1,
		"maxLength": 128,
		"pattern":   `^[A-Za-z0-9_-]{1,128}$`,
	}
}

func filePathQueryParameter(required bool) map[string]any {
	schema := map[string]any{"type": "string"}
	if required {
		schema["minLength"] = 1
	}
	return map[string]any{
		"name":        "path",
		"in":          "query",
		"required":    required,
		"description": "Installation-owned file or directory path.",
		"schema":      schema,
	}
}

func jsonResponseSchema(description, schemaName string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schemaRef(schemaName),
			},
		},
	}
}

func jsonRequestSchema(description, schemaName string, required bool) map[string]any {
	return map[string]any{
		"description": description,
		"required":    required,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schemaRef(schemaName),
			},
		},
	}
}

func jsonArrayResponseSchema(description, itemSchemaName string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"type": "array", "items": schemaRef(itemSchemaName)},
			},
		},
	}
}

func schemaRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func uptimeMonitorTypeSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        []string{"http", "tcp", "ping", "dns"},
		"description": "Monitor target type. HTTP uses url; TCP, ping, and DNS use hostname.",
	}
}

func uptimeMonitorHTTPURLSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"format":      "uri",
		"pattern":     `^https?://`,
		"minLength":   1,
		"maxLength":   2048,
		"description": "Absolute HTTP(S) target URL; outbound validation rejects loopback, private, link-local, and metadata destinations.",
	}
}

func uptimeMonitorHostnameSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"minLength":   1,
		"maxLength":   253,
		"description": "Target hostname or IP address; outbound validation rejects loopback, private, link-local, and metadata destinations.",
	}
}

func uptimeMonitorMutationProperties() map[string]any {
	return map[string]any{
		"name": map[string]any{
			"type": "string", "minLength": 1, "maxLength": 128,
			"description": "Trimmed monitor display name; valid UTF-8 and at most 128 bytes.",
		},
		"type": uptimeMonitorTypeSchema(),
		"url": map[string]any{
			"type": "string", "maxLength": 2048,
			"description": "HTTP target URL. Required for HTTP monitors; non-HTTP values are cleared during normalization.",
		},
		"hostname": map[string]any{
			"type": "string", "maxLength": 253,
			"description": "TCP, ping, or DNS target hostname. Required for those monitor types; HTTP values are cleared during normalization.",
		},
		"port": map[string]any{
			"type": "integer", "minimum": 1, "maximum": 65535,
			"description": "TCP target port.",
		},
		"method": map[string]any{
			"type": "string", "enum": []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, "default": "GET",
		},
		"interval_secs": map[string]any{
			"type": "integer", "minimum": 10, "maximum": 86400, "default": 60,
			"description": "Check interval in seconds; accepted range is 10 through 86400.",
		},
		"timeout_secs": map[string]any{
			"type": "integer", "minimum": 1, "maximum": 300, "default": 30,
			"description": "Per-check timeout in seconds; accepted range is 1 through 300.",
		},
		"retries": map[string]any{
			"type": "integer", "minimum": 1, "maximum": 10, "default": 1,
			"description": "Consecutive failures before a monitor is considered down.",
		},
		"retry_interval": map[string]any{
			"type": "integer", "minimum": 1, "maximum": 3600, "default": 30,
			"description": "Retry interval in seconds; accepted range is 1 through 3600.",
		},
		"accepted_statuscodes": map[string]any{
			"type": "string", "minLength": 1, "maxLength": 4096, "default": "200-299",
			"description": "Comma-separated HTTP status codes/ranges or a JSON string array; 1 through 32 entries, each from 100 through 599.",
		},
		"keyword": map[string]any{
			"type": "string", "maxLength": 512,
			"description": "HTTP response keyword, or DNS record type for DNS monitors.",
		},
		"keyword_invert": map[string]any{
			"type": "boolean", "default": false,
		},
		"req_headers": map[string]any{
			"type": "string", "maxLength": 32768,
			"description": "HTTP request headers; valid UTF-8, at most 32768 bytes, without CR or NUL.",
		},
		"req_body": map[string]any{
			"type": "string", "maxLength": 65536,
			"description": "HTTP request body, or DNS expected value; valid UTF-8 and at most 65536 bytes for HTTP (253 bytes for DNS).",
		},
		"tls_check": map[string]any{
			"type": "boolean", "default": false,
		},
		"tls_expiry_warn_days": map[string]any{
			"type": "integer", "minimum": 1, "maximum": 365, "default": 14,
			"description": "HTTP TLS expiry warning threshold in days; accepted range is 1 through 365.",
		},
		"description": map[string]any{
			"type": "string", "maxLength": 2048,
			"description": "Optional valid UTF-8 monitor description of at most 2048 bytes.",
		},
		"max_redirects": map[string]any{
			"type": "integer", "minimum": 1, "maximum": 20, "default": 5,
			"description": "HTTP redirect limit; accepted range is 1 through 20.",
		},
		"alert_channel_ids": map[string]any{
			"type":        []string{"array", "null"},
			"maxItems":    128,
			"items":       map[string]any{"type": "integer", "format": "int64", "minimum": 1},
			"description": "Up to 128 positive notification channel IDs; duplicate IDs normalize to one ID.",
		},
		"alert_reminder_mins": map[string]any{
			"type": "integer", "minimum": 0, "maximum": 10080, "default": 0,
			"description": "Repeated-down alert reminder interval in minutes; accepted range is 0 through 10080.",
		},
		"is_active": map[string]any{
			"type": "boolean", "default": true,
			"description": "Accepted for client compatibility; create always activates the monitor.",
		},
	}
}

func uptimeMonitorCreateRequestSchema() map[string]any {
	properties := uptimeMonitorMutationProperties()
	branch := func(monitorType string, required []string, overrides map[string]any) map[string]any {
		branchProperties := map[string]any{"type": map[string]any{"const": monitorType}}
		for name, schema := range overrides {
			branchProperties[name] = schema
		}
		return map[string]any{"required": required, "properties": branchProperties}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "type"},
		"properties":           properties,
		"oneOf": []any{
			branch("http", []string{"type", "url"}, map[string]any{"url": uptimeMonitorHTTPURLSchema()}),
			branch("tcp", []string{"type", "hostname", "port"}, map[string]any{
				"hostname": uptimeMonitorHostnameSchema(),
				"port":     map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			}),
			branch("ping", []string{"type", "hostname"}, map[string]any{"hostname": uptimeMonitorHostnameSchema()}),
			branch("dns", []string{"type", "hostname"}, map[string]any{
				"hostname": uptimeMonitorHostnameSchema(),
				"keyword":  map[string]any{"type": "string", "enum": []string{"A", "AAAA", "MX", "CNAME"}, "default": "A"},
				"req_body": map[string]any{"type": "string", "maxLength": 253},
			}),
		},
	}
}

func uptimeMonitorUpdateRequestSchema() map[string]any {
	properties := uptimeMonitorMutationProperties()
	properties["is_active"].(map[string]any)["description"] = "Whether the monitor is active. Dedicated pause/resume routes are preferred for state-only changes."
	properties["maintenance_mode"] = map[string]any{
		"type":        "boolean",
		"description": "Whether the monitor is in maintenance mode.",
	}
	properties["group_id"] = map[string]any{
		"type": []string{"integer", "null"}, "format": "int64", "minimum": 1,
		"description": "Optional positive monitor group ID; null is accepted to clear the group.",
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"minProperties":        1,
		"properties":           properties,
	}
}

func uptimeMonitorResponseSchema() map[string]any {
	properties := uptimeMonitorMutationProperties()
	properties["url"] = uptimeMonitorHTTPURLSchema()
	properties["hostname"] = uptimeMonitorHostnameSchema()
	properties["port"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 65535}
	properties["alert_channel_ids"] = map[string]any{
		"type":        "array",
		"maxItems":    128,
		"uniqueItems": true,
		"items":       map[string]any{"type": "integer", "format": "int64", "minimum": 1},
	}
	properties["is_active"] = map[string]any{"type": "boolean"}
	properties["maintenance_mode"] = map[string]any{"type": "boolean"}
	properties["group_id"] = map[string]any{"type": "integer", "format": "int64", "minimum": 1}
	properties["id"] = map[string]any{"type": "integer", "format": "int64", "minimum": 1}
	properties["created_at"] = map[string]any{"type": "string", "format": "date-time"}
	properties["updated_at"] = map[string]any{"type": "string", "format": "date-time"}
	properties["current_status"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 4}
	properties["uptime_24h"] = map[string]any{"type": "number", "minimum": 0, "maximum": 100}
	properties["last_check_at"] = map[string]any{"type": "string", "format": "date-time"}
	properties["avg_ping_ms"] = map[string]any{"type": "number", "minimum": 0}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"id", "name", "type", "method", "interval_secs", "timeout_secs", "retries", "retry_interval",
			"accepted_statuscodes", "keyword_invert", "tls_check", "tls_expiry_warn_days", "is_active",
			"maintenance_mode", "max_redirects", "alert_reminder_mins", "created_at", "updated_at",
		},
		"properties": properties,
	}
}

func uptimeSettingIntegerStringSchema(minimum, maximum int, description string) map[string]any {
	pattern := `^[0-9]+$`
	switch {
	case minimum == 2 && maximum == 3650:
		pattern = `^(?:[2-9]|[1-9][0-9]|[1-9][0-9]{2}|[12][0-9]{3}|3[0-5][0-9]{2}|36[0-4][0-9]|3650)$`
	case minimum == 1 && maximum == 365:
		pattern = `^(?:[1-9]|[1-9][0-9]|[12][0-9]{2}|3[0-5][0-9]|36[0-5])$`
	case minimum == 10 && maximum == 86400:
		pattern = `^(?:[1-9][0-9]|[1-9][0-9]{2}|[1-9][0-9]{3}|[1-7][0-9]{4}|8[0-5][0-9]{3}|86[0-3][0-9]{2}|86400)$`
	case minimum == 1 && maximum == 300:
		pattern = `^(?:[1-9]|[1-9][0-9]|[12][0-9]{2}|300)$`
	}
	return map[string]any{
		"type": "string", "pattern": pattern,
		"description": description,
	}
}

func uptimeDefaultChannelsStringSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"maxLength":   4096,
		"pattern":     `^(?:\[\]|\[[1-9][0-9]*(?:,[1-9][0-9]*)*\])$`,
		"description": "Canonical JSON string containing an array of at most 128 positive channel IDs; duplicates are removed before persistence.",
	}
}

func uptimeSettingsProperties() map[string]any {
	return map[string]any{
		"uptime_retention_days":     uptimeSettingIntegerStringSchema(2, 3650, "Canonical decimal string from 2 through 3650 days."),
		"uptime_compact_after_days": uptimeSettingIntegerStringSchema(1, 365, "Canonical decimal string from 1 through 365 days; must be less than retention days."),
		"uptime_default_interval":   uptimeSettingIntegerStringSchema(10, 86400, "Canonical decimal string from 10 through 86400 seconds."),
		"uptime_default_timeout":    uptimeSettingIntegerStringSchema(1, 300, "Canonical decimal string from 1 through 300 seconds; must not exceed the default interval."),
		"uptime_default_channels":   uptimeDefaultChannelsStringSchema(),
	}
}

func uptimeSettingsResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"uptime_retention_days", "uptime_compact_after_days", "uptime_default_interval",
			"uptime_default_timeout", "uptime_default_channels",
		},
		"properties": uptimeSettingsProperties(),
	}
}

func uptimeSettingsUpdateRequestSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"minProperties":        1,
		"properties":           uptimeSettingsProperties(),
	}
}

func uptimeStatusPageSlugSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"minLength":   1,
		"maxLength":   64,
		"pattern":     `^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`,
		"description": "Lowercase ASCII slug with interior hyphens only.",
	}
}

func uptimeStatusPageLogoURLSchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			map[string]any{"const": ""},
			map[string]any{
				"type": "string", "format": "uri", "pattern": `^https?://`, "maxLength": 2048,
				"description": "Absolute HTTP(S) URL without embedded credentials or a fragment.",
			},
		},
		"description": "Optional logo URL; empty clears the value.",
	}
}

func uptimeStatusPageMonitorSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"monitor_id", "sort_order"},
		"properties": map[string]any{
			"monitor_id":   map[string]any{"type": "integer", "format": "int64", "minimum": 1},
			"display_name": map[string]any{"type": "string", "maxLength": 128},
			"sort_order":   map[string]any{"type": "integer", "minimum": 1, "maximum": 128},
		},
	}
}

func uptimeStatusPageMonitorRequestSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"monitor_id"},
		"properties": map[string]any{
			"monitor_id":   map[string]any{"type": "integer", "format": "int64", "minimum": 1},
			"display_name": map[string]any{"type": "string", "maxLength": 128},
			"sort_order": map[string]any{
				"type": "integer", "minimum": 1, "maximum": 128,
				"description": "Optional client hint; the server rewrites sort_order to request order.",
			},
		},
	}
}

func uptimeStatusPageProperties() map[string]any {
	return map[string]any{
		"id":           map[string]any{"type": "integer", "format": "int64", "minimum": 1},
		"slug":         uptimeStatusPageSlugSchema(),
		"title":        map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"description":  map[string]any{"type": "string", "maxLength": 2048},
		"theme":        map[string]any{"type": "string", "enum": []string{"auto", "light", "dark"}},
		"logo_url":     uptimeStatusPageLogoURLSchema(),
		"is_public":    map[string]any{"type": "boolean"},
		"history_days": map[string]any{"type": "integer", "minimum": 1, "maximum": 3650},
		"monitors": map[string]any{
			"type":     "array",
			"maxItems": 128,
			"items":    schemaRef("UptimeStatusPageMonitor"),
		},
		"created_at": map[string]any{"type": "string", "format": "date-time"},
	}
}

func uptimeStatusPageResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"id", "slug", "title", "theme", "is_public", "history_days", "created_at"},
		"properties":           uptimeStatusPageProperties(),
	}
}

func uptimeStatusPageRequestProperties() map[string]any {
	return map[string]any{
		"slug":         uptimeStatusPageSlugSchema(),
		"title":        map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"description":  map[string]any{"type": "string", "maxLength": 2048, "description": "Optional description; empty clears the value."},
		"logo_url":     uptimeStatusPageLogoURLSchema(),
		"theme":        map[string]any{"type": "string", "enum": []string{"auto", "light", "dark"}},
		"is_public":    map[string]any{"type": "boolean"},
		"history_days": map[string]any{"type": "integer", "minimum": 1, "maximum": 3650},
		"monitors": map[string]any{
			"type":        []string{"array", "null"},
			"maxItems":    128,
			"items":       schemaRef("UptimeStatusPageMonitorRequest"),
			"description": "At most 128 unique existing monitor mappings; omission or null clears mappings on replacement.",
		},
	}
}

func uptimeStatusPageCreateRequestSchema() map[string]any {
	properties := uptimeStatusPageRequestProperties()
	properties["theme"].(map[string]any)["default"] = "auto"
	properties["history_days"].(map[string]any)["default"] = 90
	properties["is_public"] = map[string]any{
		"type": []string{"boolean", "null"}, "default": true,
		"description": "Visibility; omitted or null defaults to public.",
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"slug", "title"},
		"properties":           properties,
	}
}

func uptimeStatusPageUpdateRequestSchema() map[string]any {
	properties := uptimeStatusPageRequestProperties()
	properties["theme"].(map[string]any)["description"] = "Optional replacement theme; must be auto, light, or dark when supplied."
	properties["history_days"].(map[string]any)["description"] = "Optional replacement history window in days; must be 1 through 3650 when supplied."
	properties["is_public"].(map[string]any)["description"] = "Optional replacement visibility; omission preserves the current value, while false is a valid explicit replacement."
	properties["monitors"].(map[string]any)["description"] = "Optional ordered monitor mappings; omission preserves the current mappings, while an explicit empty array clears them."
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"minProperties":        1,
		"properties":           properties,
	}
}

func alertRuleTypeSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{"cpu_usage", "memory_usage", "disk_usage", "ssl_expiry", "service_down", "failed_logins"},
	}
}

func alertRuleMutationProperties() map[string]any {
	return map[string]any{
		"name":         map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"type":         alertRuleTypeSchema(),
		"threshold":    map[string]any{"type": "number"},
		"durationMins": map[string]any{"type": "integer", "minimum": 0, "maximum": 1440},
		"target":       map[string]any{"type": "string", "maxLength": 4096},
		"enabled":      map[string]any{"type": "boolean"},
		"cooldownMins": map[string]any{"type": "integer", "minimum": 1, "maximum": 10080},
	}
}

func alertRuleCreateRequestSchema() map[string]any {
	branch := func(ruleType string, threshold map[string]any, targetRequired bool) map[string]any {
		required := []string{"type"}
		properties := map[string]any{"type": map[string]any{"const": ruleType}}
		if threshold != nil {
			required = append(required, "threshold")
			properties["threshold"] = threshold
		}
		if targetRequired {
			required = append(required, "target")
			properties["target"] = map[string]any{"type": "string", "minLength": 1}
		}
		return map[string]any{"required": required, "properties": properties}
	}
	percentage := map[string]any{"type": "number", "minimum": 0, "maximum": 100}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "type"},
		"properties":           alertRuleMutationProperties(),
		"oneOf": []any{
			branch("cpu_usage", percentage, false),
			branch("memory_usage", percentage, false),
			branch("disk_usage", percentage, false),
			branch("ssl_expiry", map[string]any{"type": "number", "minimum": 0, "maximum": 3650}, true),
			branch("service_down", nil, true),
			branch("failed_logins", map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000}, false),
		},
	}
}

func deployTargetMutationProperties(includeActive bool) map[string]any {
	properties := map[string]any{
		"name": map[string]any{
			"type": "string", "minLength": 1, "maxLength": 128,
			"description": "Single-line deployment target name.",
		},
		"repoUrl": map[string]any{
			"type": "string", "maxLength": 2048,
			"description": "Optional credential-free HTTPS, SSH, or SCP-like Git repository URL.",
		},
		"branch": map[string]any{
			"type": "string", "minLength": 1, "maxLength": 255, "default": "main",
		},
		"projectDir": map[string]any{
			"type": "string", "minLength": 1, "pattern": `^/`,
			"description": "Absolute local project directory owned by this installation.",
		},
		"deploymentKind": map[string]any{
			"type": "string", "enum": []string{"script", "compose"}, "default": "script",
		},
		"composeFile": map[string]any{
			"type":        "string",
			"description": "Relative Compose file below projectDir; valid only for compose targets. Empty uses Docker Compose discovery.",
		},
		"deployScript": map[string]any{
			"type": "string", "maxLength": 65536,
			"description": "Installation-owned script body for script targets; compose targets reject this field when non-empty.",
		},
		"webhookProvider": map[string]any{
			"type": "string", "enum": []string{"github", "gitlab"}, "default": "github",
		},
		"webhookToken": map[string]any{
			"type": "string", "maxLength": 4096, "writeOnly": true,
			"description": "Provider signing secret stored outside response payloads.",
		},
		"autoDeploy": map[string]any{
			"type": "boolean", "default": false,
		},
	}
	if includeActive {
		properties["isActive"] = map[string]any{"type": "boolean", "default": false}
		properties["clearWebhookToken"] = map[string]any{
			"type": "boolean", "default": false,
			"description": "Explicitly remove the protected signing secret. Cannot be combined with webhookToken or enabled autoDeploy.",
		}
		properties["expectedUpdatedAt"] = map[string]any{
			"type": "string", "format": "date-time",
			"description": "Exact updatedAt value from the caller's latest target observation; stale values return HTTP 409.",
		}
		properties["webhookToken"].(map[string]any)["description"] = "Non-empty replaces the protected signing secret; empty preserves it unless clearWebhookToken is true. Never returned in responses."
	}
	return properties
}

func composeServiceNameSchema() map[string]any {
	return map[string]any{
		"type": "string", "minLength": 1, "maxLength": 128,
		"pattern": `^(?!.*\.\.)[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`,
	}
}

func deployEnvironmentKeySchema() map[string]any {
	return map[string]any{
		"type": "string", "minLength": 1, "maxLength": 128,
		"pattern": `^[A-Za-z_][A-Za-z0-9_]{0,127}$`,
	}
}

func deployDomainNameSchema() map[string]any {
	return map[string]any{
		"type": "string", "minLength": 3, "maxLength": 253,
		"pattern":     `^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`,
		"description": "Normalized lowercase ASCII hostname with at least two labels.",
	}
}

func deployDomainInputSchema() map[string]any {
	return map[string]any{
		"type": "string", "minLength": 3, "maxLength": 254,
		"pattern":     `^(?=.{1,254}$)(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.?$`,
		"description": "ASCII hostname; Heyserver trims, lowercases, and removes one trailing dot before persistence.",
	}
}

func localDomainNameSchema() map[string]any {
	schema := deployDomainNameSchema()
	schema["description"] = "Exact normalized lowercase ASCII hostname used as the stable local domain identity."
	return schema
}

func localDomainProperties() map[string]any {
	return map[string]any{
		"id":         localDomainNameSchema(),
		"name":       localDomainNameSchema(),
		"type":       map[string]any{"type": "string", "enum": []string{"php", "proxy", "static"}},
		"root":       map[string]any{"type": "string"},
		"phpVersion": map[string]any{"type": "string", "enum": []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"}},
		"proxyPort":  map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
		"sslEnabled": map[string]any{"type": "boolean"},
		"isActive":   map[string]any{"type": "boolean"},
		"createdAt":  map[string]any{"type": "string", "format": "date-time"},
		"updatedAt":  map[string]any{"type": "string", "format": "date-time"},
	}
}

func localDomainDetailProperties() map[string]any {
	properties := localDomainProperties()
	for name, schema := range map[string]any{
		"serverNames":      map[string]any{"type": "array", "items": localDomainNameSchema()},
		"nginxConfig":      map[string]any{"type": "string", "minLength": 1},
		"nginxContent":     map[string]any{"type": "string"},
		"phpPoolPath":      map[string]any{"type": "string"},
		"phpPoolContent":   map[string]any{"type": "string"},
		"sslCertPath":      map[string]any{"type": "string"},
		"sslKeyPath":       map[string]any{"type": "string"},
		"accessLogPath":    map[string]any{"type": "string", "minLength": 1},
		"errorLogPath":     map[string]any{"type": "string", "minLength": 1},
		"sslDaysRemaining": map[string]any{"type": "integer"},
	} {
		properties[name] = schema
	}
	return properties
}

func domainCreateRequestProperties() map[string]any {
	absoluteOrEmpty := func() map[string]any {
		return map[string]any{"oneOf": []any{
			map[string]any{"const": ""},
			map[string]any{"type": "string", "pattern": `^/`, "maxLength": 4096},
		}}
	}
	return map[string]any{
		"domain":            localDomainNameSchema(),
		"type":              map[string]any{"type": "string", "enum": []string{"php", "proxy", "static"}, "default": "php"},
		"phpVersion":        map[string]any{"type": "string", "enum": []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"}, "default": "8.4"},
		"proxyPort":         map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
		"webRoot":           absoluteOrEmpty(),
		"fpmPreset":         map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}, "default": "medium"},
		"spaMode":           map[string]any{"type": "boolean", "default": false},
		"wwwRedirect":       map[string]any{"type": "boolean", "default": false},
		"issueSSL":          map[string]any{"type": "boolean", "default": false},
		"sslEmail":          map[string]any{"type": "string", "format": "email", "maxLength": 254},
		"existingCertName":  map[string]any{"type": "string", "minLength": 1, "maxLength": 253, "pattern": `^[A-Za-z0-9._-]+$`},
		"createDnsRecord":   map[string]any{"type": "boolean", "default": false},
		"pm2_app":           map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": `^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`},
		"pm2_script":        map[string]any{"type": "string", "minLength": 1, "maxLength": 4096},
		"pm2_cwd":           absoluteOrEmpty(),
		"pm2_port":          map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
		"nodeEnv":           map[string]any{"type": "string", "enum": []string{"production", "development"}, "default": "production"},
		"isolatedLinuxUser": map[string]any{"type": "boolean", "default": false},
	}
}

func editableSettingKeys() []string {
	return []string{
		"hostnameDisplay", "adminEmail", "notifyOnLogin", "notifyOnError",
		"notifyOnDeployment", "webmail_url", "mail_admin_url", "mail_server_host",
		"mail_imap_port", "mail_smtp_starttls_port", "mail_smtp_ssl_port", "timezone",
	}
}

func editableSettingKeySchema() map[string]any {
	return map[string]any{"type": "string", "enum": editableSettingKeys()}
}

func editableSettingsProperties() map[string]any {
	booleanString := map[string]any{"type": "string", "enum": []string{"true", "false"}}
	urlString := map[string]any{
		"oneOf": []any{
			map[string]any{"const": ""},
			map[string]any{"type": "string", "format": "uri", "pattern": `^https?://`, "maxLength": 2048, "description": "HTTP(S) URL without embedded credentials."},
		},
	}
	portString := map[string]any{
		"oneOf": []any{
			map[string]any{"const": ""},
			map[string]any{"type": "string", "pattern": `^[1-9][0-9]{0,4}$`, "description": "Canonical decimal TCP port from 1 through 65535."},
		},
	}
	return map[string]any{
		"hostnameDisplay":         map[string]any{"type": "string", "maxLength": 128},
		"adminEmail":              map[string]any{"oneOf": []any{map[string]any{"const": ""}, map[string]any{"type": "string", "format": "email", "maxLength": 254}}},
		"notifyOnLogin":           booleanString,
		"notifyOnError":           booleanString,
		"notifyOnDeployment":      booleanString,
		"webmail_url":             urlString,
		"mail_admin_url":          urlString,
		"mail_server_host":        map[string]any{"type": "string", "maxLength": 253, "pattern": `^$|^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`},
		"mail_imap_port":          portString,
		"mail_smtp_starttls_port": portString,
		"mail_smtp_ssl_port":      portString,
		"timezone":                map[string]any{"type": "string", "maxLength": 2048, "description": "Empty or a timezone accepted by the installed Go timezone database."},
	}
}

func promotedSchemas() map[string]any {
	nonNegativeInteger := func() map[string]any {
		return map[string]any{"type": "integer", "minimum": 0}
	}
	nonNegativeNumber := func() map[string]any {
		return map[string]any{"type": "number", "minimum": 0}
	}
	stringMap := func(nullable bool) map[string]any {
		schemaType := any("object")
		if nullable {
			schemaType = []string{"object", "null"}
		}
		return map[string]any{"type": schemaType, "additionalProperties": map[string]any{"type": "string"}}
	}
	nullableArray := func(items map[string]any) map[string]any {
		return map[string]any{"type": []string{"array", "null"}, "items": items}
	}
	storageProperties := map[string]any{
		"directory":              map[string]any{"type": "string"},
		"legacyDirectories":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"totalBytes":             nonNegativeInteger(),
		"activeBytes":            nonNegativeInteger(),
		"completedBytes":         nonNegativeInteger(),
		"invalidBytes":           nonNegativeInteger(),
		"orphanedBytes":          nonNegativeInteger(),
		"legacyOrphanedBytes":    nonNegativeInteger(),
		"completedCount":         nonNegativeInteger(),
		"invalidCount":           nonNegativeInteger(),
		"orphanedCount":          nonNegativeInteger(),
		"legacyOrphanedCount":    nonNegativeInteger(),
		"rootSize":               nonNegativeInteger(),
		"rootUsed":               nonNegativeInteger(),
		"rootAvailable":          nonNegativeInteger(),
		"rootUsePercent":         nonNegativeNumber(),
		"backupVolumeSize":       nonNegativeInteger(),
		"backupVolumeUsed":       nonNegativeInteger(),
		"backupVolumeAvailable":  nonNegativeInteger(),
		"backupVolumeUsePercent": nonNegativeNumber(),
	}
	storageRequired := []string{
		"directory", "legacyDirectories", "totalBytes", "activeBytes", "completedBytes",
		"invalidBytes", "orphanedBytes", "legacyOrphanedBytes", "completedCount",
		"invalidCount", "orphanedCount", "legacyOrphanedCount", "rootSize", "rootUsed",
		"rootAvailable", "rootUsePercent", "backupVolumeSize", "backupVolumeUsed",
		"backupVolumeAvailable", "backupVolumeUsePercent",
	}
	snapshotManifestIDs := []string{
		"vhosts", "nginx", "letsencrypt", "postgresql-cfg", "mysql-cfg", "php",
		"hserver-data", "cron-d", "systemd", "root-crontab",
	}
	snapshotRestoreManifestIDs := snapshotManifestIDs[:len(snapshotManifestIDs)-1]
	bindDNSName := map[string]any{"type": "string", "minLength": 1, "maxLength": 253}
	bindRecordType := map[string]any{"type": "string", "pattern": "^[A-Za-z]{1,16}$"}
	bindRecordValue := map[string]any{"type": "string", "minLength": 1, "maxLength": 4096}
	bindTTLString := map[string]any{"type": "string", "pattern": `^[0-9]{1,10}$`}
	bindSOATimer := map[string]any{"type": "integer", "minimum": 0, "maximum": 2147483647}
	schemas := map[string]any{
		"ErrorResponse": map[string]any{
			"type":       "object",
			"required":   []string{"error"},
			"properties": map[string]any{"error": map[string]any{"type": "string"}},
		},
		"MailAccount":                    mailAccountResponseSchema(),
		"MailAlias":                      mailAliasResponseSchema(),
		"MailServiceListener":            mailServiceListenerSchema(),
		"MailServiceOverview":            mailServiceOverviewSchema(),
		"MailServiceOverviewSources":     mailServiceOverviewSourcesSchema(),
		"MailServiceSource":              mailServiceSourceSchema(),
		"MailServiceStatus":              mailServiceStatusSchema(),
		"MailServiceStatusSource":        mailServiceStatusSourceSchema(),
		"MailServiceStorage":             mailServiceStorageSchema(),
		"MailServiceVersion":             mailServiceVersionSchema(),
		"NotificationChannel":            notificationChannelResponseSchema(),
		"UptimeMonitor":                  uptimeMonitorResponseSchema(),
		"UptimeMonitorCreateRequest":     uptimeMonitorCreateRequestSchema(),
		"UptimeMonitorUpdateRequest":     uptimeMonitorUpdateRequestSchema(),
		"UptimeSettings":                 uptimeSettingsResponseSchema(),
		"UptimeSettingsUpdateRequest":    uptimeSettingsUpdateRequestSchema(),
		"UptimeStatusPage":               uptimeStatusPageResponseSchema(),
		"UptimeStatusPageCreateRequest":  uptimeStatusPageCreateRequestSchema(),
		"UptimeStatusPageMonitor":        uptimeStatusPageMonitorSchema(),
		"UptimeStatusPageMonitorRequest": uptimeStatusPageMonitorRequestSchema(),
		"UptimeStatusPageUpdateRequest":  uptimeStatusPageUpdateRequestSchema(),
		"HealthStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "version", "uptime"},
			"properties": map[string]any{
				"status":       map[string]any{"type": "string", "const": "ok"},
				"version":      map[string]any{"type": "string"},
				"uptime":       map[string]any{"type": "integer", "minimum": 0},
				"build_commit": map[string]any{"type": "string"},
			},
		},
		"OnboardingState": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"completed", "step"},
			"properties": map[string]any{
				"completed": map[string]any{"type": "boolean"},
				"step":      map[string]any{"type": "integer", "minimum": 0, "maximum": 5},
			},
		},
		"OnboardingSetRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"completed", "step"},
			"properties": map[string]any{
				"completed": map[string]any{"type": "boolean"},
				"step":      map[string]any{"type": "integer", "minimum": 0, "maximum": 5},
			},
		},
		"OnboardingSaveResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "saved"},
			},
		},
		"AuthLoginRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"email", "password"},
			"properties": map[string]any{
				"email":    map[string]any{"type": "string", "format": "email", "minLength": 3, "maxLength": 254},
				"password": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "writeOnly": true},
			},
		},
		"AuthTOTPLoginRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"email", "password", "code"},
			"properties": map[string]any{
				"email":    map[string]any{"type": "string", "format": "email", "minLength": 3, "maxLength": 254},
				"password": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "writeOnly": true},
				"code":     map[string]any{"type": "string", "pattern": `^[0-9]{6}$`, "writeOnly": true},
			},
		},
		"AuthRecoveryRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"email", "password", "recovery_code"},
			"properties": map[string]any{
				"email":         map[string]any{"type": "string", "format": "email", "minLength": 3, "maxLength": 254},
				"password":      map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "writeOnly": true},
				"recovery_code": map[string]any{"type": "string", "pattern": `^[A-Fa-f0-9]{5}-[A-Fa-f0-9]{5}$`, "writeOnly": true},
			},
		},
		"AuthTOTPCodeRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"code"},
			"properties": map[string]any{
				"code": map[string]any{"type": "string", "pattern": `^[0-9]{6}$`, "writeOnly": true},
			},
		},
		"AuthLoginResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"token", "user"},
			"properties": map[string]any{
				"token": map[string]any{"type": "string", "minLength": 1, "maxLength": 8192},
				"user":  schemaRef("User"),
			},
		},
		"AuthTOTPRequiredResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"requires_totp", "email"},
			"properties": map[string]any{
				"requires_totp": map[string]any{"type": "boolean", "const": true},
				"email":         map[string]any{"type": "string", "format": "email", "minLength": 3, "maxLength": 254},
			},
		},
		"AuthLoginResult": map[string]any{
			"oneOf": []any{schemaRef("AuthLoginResponse"), schemaRef("AuthTOTPRequiredResponse")},
		},
		"AuthLogoutResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string", "const": "logged out"},
			},
		},
		"AuthTOTPStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"enabled", "setup_pending"},
			"properties": map[string]any{
				"enabled":       map[string]any{"type": "boolean"},
				"setup_pending": map[string]any{"type": "boolean"},
			},
		},
		"AuthTOTPSetup": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"secret", "otpAuthUrl", "qrCode", "recoveryCodes"},
			"properties": map[string]any{
				"secret":        map[string]any{"type": "string", "pattern": `^[A-Z2-7]+$`},
				"otpAuthUrl":    map[string]any{"type": "string", "pattern": `^otpauth://totp/`},
				"qrCode":        map[string]any{"type": "string", "contentEncoding": "base64", "maxLength": 1048576},
				"recoveryCodes": map[string]any{"type": "array", "minItems": 8, "maxItems": 8, "items": map[string]any{"type": "string", "pattern": `^[A-F0-9]{5}-[A-F0-9]{5}$`}},
			},
		},
		"AuthTOTPEnabledResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"enabled"},
			"properties": map[string]any{
				"enabled": map[string]any{"type": "boolean", "const": true},
			},
		},
		"AuthTOTPDisabledResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"disabled"},
			"properties": map[string]any{
				"disabled": map[string]any{"type": "boolean", "const": true},
			},
		},
		"SettingsValues": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"maxProperties":        12,
			"properties":           editableSettingsProperties(),
		},
		"SettingsUpdateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"minProperties":        1,
			"maxProperties":        12,
			"properties":           editableSettingsProperties(),
		},
		"SettingValueResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"key", "value"},
			"properties": map[string]any{
				"key":   editableSettingKeySchema(),
				"value": map[string]any{"type": "string", "maxLength": 2048},
			},
		},
		"SettingsSaveResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status"},
			"properties":           map[string]any{"status": map[string]any{"type": "string", "const": "saved"}},
		},
		"SettingsDeleteResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status"},
			"properties":           map[string]any{"status": map[string]any{"type": "string", "const": "deleted"}},
		},
		"PortableSettingsBundle": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"schema_version", "exported_at", "source_version", "settings"},
			"properties": map[string]any{
				"schema_version": map[string]any{"type": "integer", "const": 1},
				"exported_at":    map[string]any{"type": "string", "format": "date-time"},
				"source_version": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048},
				"settings":       schemaRef("SettingsValues"),
				"warnings":       map[string]any{"type": "array", "maxItems": 32, "items": map[string]any{"type": "string", "maxLength": 256}},
			},
		},
		"PortableSettingsBundleRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"schema_version", "exported_at", "source_version", "settings"},
			"properties": map[string]any{
				"schema_version": map[string]any{"type": "integer", "const": 1},
				"exported_at":    map[string]any{"type": "string", "format": "date-time"},
				"source_version": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048},
				"settings":       schemaRef("SettingsUpdateRequest"),
				"warnings":       map[string]any{"type": "array", "maxItems": 32, "items": map[string]any{"type": "string", "maxLength": 256}},
			},
		},
		"PortableSettingsImportRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"bundle", "confirmed"},
			"properties": map[string]any{
				"bundle":    schemaRef("PortableSettingsBundleRequest"),
				"confirmed": map[string]any{"type": "boolean", "const": true},
			},
		},
		"PortableSettingsChange": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"key", "current", "proposed"},
			"properties": map[string]any{
				"key":      editableSettingKeySchema(),
				"current":  map[string]any{"type": "string", "maxLength": 2048},
				"proposed": map[string]any{"type": "string", "maxLength": 2048},
			},
		},
		"PortableSettingsPreview": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"schema_version", "imported_keys", "changed_keys", "unchanged_keys", "changes"},
			"properties": map[string]any{
				"schema_version": map[string]any{"type": "integer", "const": 1},
				"imported_keys":  map[string]any{"type": "integer", "minimum": 0, "maximum": 12},
				"changed_keys":   map[string]any{"type": "integer", "minimum": 0, "maximum": 12},
				"unchanged_keys": map[string]any{"type": "integer", "minimum": 0, "maximum": 12},
				"changes":        map[string]any{"type": "array", "maxItems": 12, "items": schemaRef("PortableSettingsChange")},
			},
		},
		"DatabaseSourceStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"available", "state"},
			"properties": map[string]any{
				"available": map[string]any{"type": "boolean"},
				"state":     map[string]any{"type": "string", "enum": []string{"healthy", "client-missing", "stopped", "authentication-failed", "unavailable"}},
				"error":     map[string]any{"type": "string"},
			},
		},
		"DatabaseSources": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"postgresql": schemaRef("DatabaseSourceStatus"),
				"mariadb":    schemaRef("DatabaseSourceStatus"),
			},
		},
		"Database": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "engine", "owner", "size", "tables"},
			"properties": map[string]any{
				"name":   databaseIdentifierSchema(),
				"engine": map[string]any{"type": "string", "enum": []string{"postgres", "mariadb"}},
				"owner":  map[string]any{"type": "string"},
				"size":   map[string]any{"type": "string"},
				"tables": map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"DatabaseInventory": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"databases", "sources"},
			"properties": map[string]any{
				"databases": map[string]any{"type": "array", "items": schemaRef("Database")},
				"sources":   schemaRef("DatabaseSources"),
			},
		},
		"DatabaseCreateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"engine", "name"},
			"properties": map[string]any{
				"engine": databaseEngineSchema(),
				"name":   databaseIdentifierSchema(),
				"owner": map[string]any{
					"oneOf":       []any{map[string]any{"const": ""}, databaseIdentifierSchema()},
					"description": "Optional PostgreSQL owner; empty uses the engine default owner.",
				},
			},
		},
		"DatabaseCreateResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "name", "engine"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string", "const": "database created successfully"},
				"name":    databaseIdentifierSchema(),
				"engine":  map[string]any{"type": "string", "enum": []string{"postgres", "mariadb"}},
			},
		},
		"DatabaseDropRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"confirm"},
			"properties": map[string]any{
				"confirm": map[string]any{"type": "string", "minLength": 6, "maxLength": 69, "description": "Exact literal DROP <name> for the selected path identity."},
			},
		},
		"DatabaseDropResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "name"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string", "const": "database dropped"},
				"name":    databaseIdentifierSchema(),
			},
		},
		"DatabaseTable": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "schema", "rowsEstimate", "size", "tableType"},
			"properties": map[string]any{
				"name":         map[string]any{"type": "string"},
				"schema":       map[string]any{"type": "string"},
				"rowsEstimate": map[string]any{"type": "integer", "format": "int64"},
				"size":         map[string]any{"type": "string"},
				"tableType":    map[string]any{"type": "string"},
			},
		},
		"DatabaseTableInventory": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"database", "engine", "tables"},
			"properties": map[string]any{
				"database": databaseIdentifierSchema(),
				"engine":   map[string]any{"type": "string", "enum": []string{"postgres", "mariadb"}},
				"tables":   map[string]any{"type": "array", "items": schemaRef("DatabaseTable")},
			},
		},
		"DatabaseQueryRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"query"},
			"properties": map[string]any{
				"query":      map[string]any{"type": "string", "minLength": 1, "maxLength": 65536, "description": "NUL-free UTF-8 SELECT or WITH statement."},
				"write_mode": map[string]any{"type": "boolean", "const": false, "default": false, "description": "Compatibility field; arbitrary SQL write mode is not exposed."},
			},
		},
		"DatabaseQueryResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"columns", "rows", "rowCount"},
			"properties": map[string]any{
				"columns":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"rows":     map[string]any{"type": "array", "items": map[string]any{"type": "array", "items": map[string]any{}}},
				"rowCount": map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"DatabaseQueryResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"result"},
			"properties": map[string]any{
				"result": schemaRef("DatabaseQueryResult"),
			},
		},
		"DatabaseUser": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "engine", "superUser", "canLogin", "createDb"},
			"properties": map[string]any{
				"name":       map[string]any{"type": "string"},
				"engine":     map[string]any{"type": "string", "enum": []string{"postgres", "mariadb"}},
				"superUser":  map[string]any{"type": "boolean"},
				"canLogin":   map[string]any{"type": "boolean"},
				"createDb":   map[string]any{"type": "boolean"},
				"validUntil": map[string]any{"type": "string"},
			},
		},
		"DatabaseUserInventory": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"users", "sources"},
			"properties": map[string]any{
				"users":   map[string]any{"type": "array", "items": schemaRef("DatabaseUser")},
				"sources": schemaRef("DatabaseSources"),
			},
		},
		"PGMCredential": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "dbName", "dbUser", "dbPassword", "dbHost", "dbPort", "isActive", "createdAt"},
			"properties": map[string]any{
				"id":               map[string]any{"type": "integer"},
				"dbName":           map[string]any{"type": "string"},
				"dbUser":           map[string]any{"type": "string"},
				"dbPassword":       map[string]any{"type": "string", "format": "password", "x-sensitive": true},
				"dbHost":           map[string]any{"type": "string"},
				"dbPort":           map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
				"connectionString": map[string]any{"type": "string", "x-sensitive": true},
				"notes":            map[string]any{"type": "string"},
				"isActive":         map[string]any{"type": "boolean"},
				"createdAt":        map[string]any{"type": "string"},
			},
		},
		"PGMCredentialList": map[string]any{
			"type":  "array",
			"items": schemaRef("PGMCredential"),
		},
		"PGMBackup": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "path", "size", "databases", "createdAt"},
			"properties": map[string]any{
				"name":      databaseBackupNameSchema(),
				"path":      map[string]any{"type": "string", "pattern": `^/`, "description": "Absolute path below the configured HSERVER_PGM_BACKUP_DIR root."},
				"size":      map[string]any{"type": "string"},
				"databases": map[string]any{"type": "integer", "minimum": 0},
				"createdAt": map[string]any{"type": "string"},
			},
		},
		"PGMBackupList": map[string]any{
			"type":  "array",
			"items": schemaRef("PGMBackup"),
		},
		"PGMBackupFileList": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "string", "pattern": `^[^/]+\.sql(?:\.gz)?$`,
			},
		},
		"PGMRestoreRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"database", "backupPath"},
			"properties": map[string]any{
				"database":   databaseIdentifierSchema(),
				"backupPath": map[string]any{"type": "string", "minLength": 1, "maxLength": 4096, "pattern": `^/.*\.sql(?:\.gz)?$`},
			},
		},
		"PGMRestoreResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "database"},
			"properties": map[string]any{
				"message":  map[string]any{"type": "string", "const": "restore completed successfully"},
				"database": databaseIdentifierSchema(),
			},
		},
		"SystemNetworkInterface": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "addrs", "isUp"},
			"properties": map[string]any{
				"name":  map[string]any{"type": "string", "minLength": 1},
				"addrs": map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1}},
				"isUp":  map[string]any{"type": "boolean"},
			},
		},
		"SystemInfo": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"os", "kernel", "boot_id", "hostname", "arch", "nginx", "php", "postgresql",
				"interfaces", "go_version", "node_version", "build_commit", "build_date", "panel_version",
			},
			"properties": map[string]any{
				"os":            map[string]any{"type": "string"},
				"kernel":        map[string]any{"type": "string"},
				"boot_id":       map[string]any{"type": "string"},
				"hostname":      map[string]any{"type": "string"},
				"arch":          map[string]any{"type": "string"},
				"nginx":         map[string]any{"type": "string"},
				"php":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"postgresql":    map[string]any{"type": "string"},
				"interfaces":    map[string]any{"type": "array", "items": schemaRef("SystemNetworkInterface")},
				"go_version":    map[string]any{"type": "string"},
				"node_version":  map[string]any{"type": "string"},
				"build_commit":  map[string]any{"type": "string"},
				"build_date":    map[string]any{"type": "string"},
				"panel_version": map[string]any{"type": "string"},
				"project_url":   map[string]any{"type": "string", "format": "uri"},
			},
		},
		"SystemCPUStats": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"usage", "cores", "model"},
			"properties": map[string]any{
				"usage": map[string]any{"type": "number", "minimum": 0},
				"cores": map[string]any{"type": "integer", "minimum": 1},
				"model": map[string]any{"type": "string"},
			},
		},
		"SystemMemoryStats": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"total", "used", "free", "percentage", "buffers", "cached", "available",
				"swapTotal", "swapUsed", "swapFree", "swapPercentage",
			},
			"properties": map[string]any{
				"total":          nonNegativeInteger(),
				"used":           nonNegativeInteger(),
				"free":           nonNegativeInteger(),
				"percentage":     nonNegativeNumber(),
				"buffers":        nonNegativeInteger(),
				"cached":         nonNegativeInteger(),
				"available":      nonNegativeInteger(),
				"swapTotal":      nonNegativeInteger(),
				"swapUsed":       nonNegativeInteger(),
				"swapFree":       nonNegativeInteger(),
				"swapPercentage": nonNegativeNumber(),
			},
		},
		"SystemDiskStats": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"mount", "total", "used", "free", "percentage"},
			"properties": map[string]any{
				"mount":      map[string]any{"type": "string", "minLength": 1},
				"total":      nonNegativeInteger(),
				"used":       nonNegativeInteger(),
				"free":       nonNegativeInteger(),
				"percentage": nonNegativeNumber(),
			},
		},
		"SystemNetworkStats": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"interface", "bytesIn", "bytesOut"},
			"properties": map[string]any{
				"interface": map[string]any{"type": "string", "minLength": 1},
				"bytesIn":   nonNegativeInteger(),
				"bytesOut":  nonNegativeInteger(),
			},
		},
		"SystemStats": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"cpu", "memory", "disk", "load", "uptime", "hostname", "os", "network"},
			"properties": map[string]any{
				"cpu":      schemaRef("SystemCPUStats"),
				"memory":   schemaRef("SystemMemoryStats"),
				"disk":     map[string]any{"type": "array", "items": schemaRef("SystemDiskStats")},
				"load":     map[string]any{"type": "array", "minItems": 3, "maxItems": 3, "items": nonNegativeNumber()},
				"uptime":   nonNegativeInteger(),
				"hostname": map[string]any{"type": "string"},
				"os":       map[string]any{"type": "string"},
				"network":  map[string]any{"type": "array", "items": schemaRef("SystemNetworkStats")},
			},
		},
		"SystemServiceStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "status"},
			"properties": map[string]any{
				"name":   map[string]any{"type": "string", "minLength": 1},
				"status": map[string]any{"type": "string", "minLength": 1},
				"pid":    map[string]any{"type": "integer", "minimum": 1},
				"uptime": map[string]any{"type": "string", "minLength": 1},
				"detail": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"SystemServiceLogEntry": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"timestamp", "unit", "priority", "message"},
			"properties": map[string]any{
				"timestamp": map[string]any{"type": "string"},
				"unit":      map[string]any{"type": "string"},
				"priority":  map[string]any{"type": "integer", "minimum": 0, "maximum": 7},
				"message":   map[string]any{"type": "string", "minLength": 1},
			},
		},
		"SystemServiceLogsResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"service", "lines"},
			"properties": map[string]any{
				"service": map[string]any{"type": "string", "minLength": 1},
				"lines":   map[string]any{"type": "array", "maxItems": 500, "items": schemaRef("SystemServiceLogEntry")},
			},
		},
		"ReleaseArtifact": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"url", "sha256"},
			"properties": map[string]any{
				"url":        map[string]any{"type": "string", "format": "uri", "pattern": `^https?://`},
				"sha256":     map[string]any{"type": "string", "pattern": `^[a-f0-9]{64}$`},
				"size_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": 1073741824},
			},
		},
		"ReleaseUpdateStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"status", "current_version", "update_available", "platform", "message", "checked_at", "signature_status",
			},
			"properties": map[string]any{
				"status":               map[string]any{"type": "string", "enum": []string{"not_configured", "unavailable", "healthy"}},
				"current_version":      map[string]any{"type": "string"},
				"latest_version":       map[string]any{"type": "string"},
				"latest_version_state": map[string]any{"type": "string", "enum": []string{"current", "behind", "ahead", "unknown"}},
				"update_available":     map[string]any{"type": "boolean"},
				"platform":             map[string]any{"type": "string", "pattern": `^[a-z0-9]+_[a-z0-9]+$`},
				"artifact":             schemaRef("ReleaseArtifact"),
				"published_at":         map[string]any{"type": "string", "format": "date-time"},
				"release_notes_url":    map[string]any{"type": "string", "format": "uri"},
				"message":              map[string]any{"type": "string", "minLength": 1},
				"checked_at":           map[string]any{"type": "string", "format": "date-time"},
				"signature_status":     map[string]any{"type": "string", "enum": []string{"not_configured", "unavailable", "verified"}},
			},
		},
		"ReleaseUpdateStage": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"id", "version", "current_version", "platform", "sha256", "size_bytes", "status", "status_detail", "created_at", "updated_at",
			},
			"properties": map[string]any{
				"id":              map[string]any{"type": "string", "minLength": 3, "maxLength": 96, "pattern": `^[A-Za-z0-9._-]+$`},
				"version":         map[string]any{"type": "string", "minLength": 1},
				"current_version": map[string]any{"type": "string", "minLength": 1},
				"platform":        map[string]any{"type": "string", "enum": []string{"linux_amd64", "linux_arm64"}},
				"sha256":          map[string]any{"type": "string", "pattern": `^[a-f0-9]{64}$`},
				"size_bytes":      map[string]any{"type": "integer", "minimum": 1, "maximum": 1073741824},
				"status":          map[string]any{"type": "string", "enum": []string{"staged", "scheduled", "running", "completed", "failed"}},
				"status_detail":   map[string]any{"type": "string", "minLength": 1},
				"created_at":      map[string]any{"type": "string", "format": "date-time"},
				"updated_at":      map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"ReleaseUpdateStageResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"stage"},
			"properties": map[string]any{
				"stage": map[string]any{"oneOf": []any{schemaRef("ReleaseUpdateStage"), map[string]any{"type": "null"}}},
			},
		},
		"ReleaseUpdateInstallRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"stage_id", "version", "confirmed"},
			"properties": map[string]any{
				"stage_id":  map[string]any{"type": "string", "minLength": 3, "maxLength": 96, "pattern": `^[A-Za-z0-9._-]+$`},
				"version":   map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
				"confirmed": map[string]any{"type": "boolean", "const": true},
			},
		},
		"ActionMessage": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
		},
		"DeleteStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "deleted"},
			},
		},
		"User": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "email", "name", "role", "totp_enabled", "createdAt", "updatedAt"},
			"properties": map[string]any{
				"id":           map[string]any{"type": "integer", "minimum": 1},
				"email":        map[string]any{"type": "string", "format": "email", "minLength": 3, "maxLength": 254},
				"name":         map[string]any{"type": "string", "minLength": 1, "maxLength": 100},
				"role":         map[string]any{"type": "string", "enum": []string{"admin", "manager", "viewer"}},
				"totp_enabled": map[string]any{"type": "boolean"},
				"createdAt":    map[string]any{"type": "string", "format": "date-time"},
				"updatedAt":    map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"UserListResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"data", "total", "limit", "offset"},
			"properties": map[string]any{
				"data":   map[string]any{"type": "array", "items": schemaRef("User")},
				"total":  nonNegativeInteger(),
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
				"offset": nonNegativeInteger(),
			},
		},
		"UserCreateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"email", "name", "password"},
			"properties": map[string]any{
				"email":    map[string]any{"type": "string", "format": "email", "minLength": 3, "maxLength": 254},
				"name":     map[string]any{"type": "string", "minLength": 1, "maxLength": 100},
				"password": map[string]any{"type": "string", "minLength": 8, "maxLength": 128, "writeOnly": true},
				"role":     map[string]any{"type": "string", "enum": []string{"admin", "manager", "viewer"}, "default": "viewer"},
			},
		},
		"UserUpdateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"minProperties":        1,
			"properties": map[string]any{
				"email":    map[string]any{"type": "string", "format": "email", "minLength": 3, "maxLength": 254},
				"name":     map[string]any{"type": "string", "minLength": 1, "maxLength": 100},
				"password": map[string]any{"type": "string", "minLength": 8, "maxLength": 128, "writeOnly": true},
				"role":     map[string]any{"type": "string", "enum": []string{"admin", "manager", "viewer"}},
			},
		},
		"BindServiceStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"available", "installed", "state", "active", "serviceState", "configAvailable",
				"checkToolsAvailable", "reloadAvailable", "zoneManagementReady", "recoveryPending",
			},
			"properties": map[string]any{
				"available":           map[string]any{"type": "boolean"},
				"installed":           map[string]any{"type": "boolean"},
				"state":               map[string]any{"type": "string", "enum": []string{"healthy", "not-installed", "not-configured", "stopped", "unavailable"}},
				"active":              map[string]any{"type": "boolean"},
				"serviceState":        map[string]any{"type": "string"},
				"version":             map[string]any{"type": "string"},
				"configAvailable":     map[string]any{"type": "boolean"},
				"checkToolsAvailable": map[string]any{"type": "boolean"},
				"reloadAvailable":     map[string]any{"type": "boolean"},
				"zoneManagementReady": map[string]any{"type": "boolean"},
				"recoveryPending":     map[string]any{"type": "boolean"},
				"error":               map[string]any{"type": "string"},
			},
		},
		"BindZone": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "file", "serial", "recordCount"},
			"properties": map[string]any{
				"domain": bindDNSName, "file": map[string]any{"type": "string"},
				"serial":      map[string]any{"type": "integer", "minimum": 0, "maximum": 4294967295},
				"recordCount": nonNegativeInteger(),
			},
		},
		"BindRecord": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "type", "value"},
			"properties": map[string]any{
				"name": bindDNSName, "ttl": map[string]any{"type": "string"}, "class": map[string]any{"type": "string"},
				"type": bindRecordType, "value": bindRecordValue,
				"priority": map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
			},
		},
		"BindZoneDetail": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "file", "serial", "recordCount", "records"},
			"properties": map[string]any{
				"domain": bindDNSName, "file": map[string]any{"type": "string"},
				"serial":      map[string]any{"type": "integer", "minimum": 0, "maximum": 4294967295},
				"recordCount": nonNegativeInteger(),
				"records":     map[string]any{"type": "array", "items": schemaRef("BindRecord")},
			},
		},
		"BindSOA": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"primaryNs", "hostmaster", "serial", "refresh", "retry", "expire", "minimum"},
			"properties": map[string]any{
				"primaryNs": bindDNSName, "hostmaster": bindDNSName,
				"serial":  map[string]any{"type": "integer", "minimum": 0, "maximum": 4294967295},
				"refresh": bindSOATimer, "retry": bindSOATimer, "expire": bindSOATimer, "minimum": bindSOATimer,
			},
		},
		"BindCreateZoneRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "ip"},
			"properties": map[string]any{
				"domain": bindDNSName,
				"ip":     map[string]any{"type": "string", "format": "ipv4"},
			},
		},
		"BindAddRecordRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"type", "value"},
			"properties": map[string]any{
				"name": bindDNSName, "ttl": bindTTLString, "type": bindRecordType, "value": bindRecordValue,
				"priority":   map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
				"autoReload": map[string]any{"type": "boolean", "default": false},
			},
		},
		"BindUpdateRecordRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "type", "oldValue", "newValue"},
			"properties": map[string]any{
				"name": bindDNSName, "type": bindRecordType,
				"oldValue": bindRecordValue, "newValue": bindRecordValue, "newTtl": bindTTLString,
				"priority":   map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
				"autoReload": map[string]any{"type": "boolean", "default": false},
			},
		},
		"BindDeleteRecordRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "type", "value"},
			"properties": map[string]any{
				"name": bindDNSName, "type": bindRecordType, "value": bindRecordValue,
				"autoReload": map[string]any{"type": "boolean", "default": false},
			},
		},
		"BindSOAUpdateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"primaryNs", "hostmaster", "refresh", "retry", "expire", "minimum"},
			"properties": map[string]any{
				"primaryNs": bindDNSName, "hostmaster": bindDNSName,
				"refresh": bindSOATimer, "retry": bindSOATimer, "expire": bindSOATimer, "minimum": bindSOATimer,
			},
		},
		"BindZoneCheckResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "ok", "output"},
			"properties": map[string]any{
				"domain": bindDNSName, "ok": map[string]any{"type": "boolean"}, "output": map[string]any{"type": "string"},
			},
		},
		"BindCheckResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"ok", "output"},
			"properties": map[string]any{
				"ok": map[string]any{"type": "boolean"}, "output": map[string]any{"type": "string"},
				"zoneChecks": map[string]any{"type": "array", "items": schemaRef("BindZoneCheckResult")},
			},
		},
		"BindLookupQuery": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "type"},
			"properties":           map[string]any{"domain": bindDNSName, "type": bindRecordType},
		},
		"BindLookupResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"resolver", "records", "ttl"},
			"properties": map[string]any{
				"resolver": map[string]any{"type": "string"},
				"records":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"ttl":      map[string]any{"type": "integer", "minimum": 0, "maximum": 4294967295},
				"error":    map[string]any{"type": "string"},
			},
		},
		"BindLookupResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"query", "results"},
			"properties": map[string]any{
				"query":   schemaRef("BindLookupQuery"),
				"results": map[string]any{"type": "array", "items": schemaRef("BindLookupResult")},
			},
		},
		"CloudflarePlan": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name"},
			"properties": map[string]any{
				"id":   map[string]any{"type": "string"},
				"name": map[string]any{"type": "string"},
			},
		},
		"CloudflareZone": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name", "status", "plan", "name_servers"},
			"properties": map[string]any{
				"id":           map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"name":         map[string]any{"type": "string", "minLength": 1, "maxLength": 253},
				"status":       map[string]any{"type": "string"},
				"plan":         schemaRef("CloudflarePlan"),
				"name_servers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		"CloudflareRecord": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "type", "name", "content", "ttl", "proxied"},
			"properties": map[string]any{
				"id":       map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"type":     map[string]any{"type": "string", "pattern": "^[A-Z]{1,16}$"},
				"name":     map[string]any{"type": "string", "minLength": 1, "maxLength": 253},
				"content":  map[string]any{"type": "string", "minLength": 1, "maxLength": 4096},
				"ttl":      map[string]any{"type": "integer", "minimum": 1, "maximum": 86400},
				"proxied":  map[string]any{"type": "boolean"},
				"priority": map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
			},
		},
		"CloudflareRecordMutationRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"type", "name", "content", "ttl", "proxied"},
			"properties": map[string]any{
				"type":    map[string]any{"type": "string", "pattern": "^[A-Za-z]{1,16}$"},
				"name":    map[string]any{"type": "string", "minLength": 1, "maxLength": 253},
				"content": map[string]any{"type": "string", "minLength": 1, "maxLength": 4096},
				"ttl": map[string]any{"oneOf": []any{
					map[string]any{"type": "integer", "const": 1},
					map[string]any{"type": "integer", "minimum": 30, "maximum": 86400},
				}},
				"proxied":  map[string]any{"type": "boolean"},
				"priority": map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
			},
		},
		"CloudflareProxyRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"proxied"},
			"properties": map[string]any{
				"proxied": map[string]any{"type": "boolean"},
			},
		},
		"CloudflareEmailRouting": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"tag", "name", "enabled"},
			"properties": map[string]any{
				"tag":      map[string]any{"type": "string"},
				"name":     map[string]any{"type": "string"},
				"enabled":  map[string]any{"type": "boolean"},
				"created":  map[string]any{"type": "string"},
				"modified": map[string]any{"type": "string"},
				"status":   map[string]any{"type": "string"},
			},
		},
		"CloudflarePurgeReceipt": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "purged"},
			},
		},
		"CloudflareMailDNSRecordAction": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"action", "type", "name", "newValue"},
			"properties": map[string]any{
				"action":   map[string]any{"type": "string", "enum": []string{"created", "updated", "skipped"}},
				"type":     map[string]any{"type": "string", "pattern": "^[A-Z]{1,16}$"},
				"name":     map[string]any{"type": "string"},
				"oldValue": map[string]any{"type": "string"},
				"newValue": map[string]any{"type": "string"},
			},
		},
		"CloudflareMailDNSReconcileResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "zoneId", "changes", "dkimPublished"},
			"properties": map[string]any{
				"domain":        map[string]any{"type": "string", "minLength": 1, "maxLength": 253},
				"zoneId":        map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"changes":       map[string]any{"type": "array", "items": schemaRef("CloudflareMailDNSRecordAction")},
				"dkimPublished": map[string]any{"type": "boolean"},
				"dkimNote":      map[string]any{"type": "string"},
			},
		},
		"AlertRule": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"id", "name", "type", "threshold", "durationMins", "target",
				"enabled", "cooldownMins", "createdAt", "updatedAt",
			},
			"properties": map[string]any{
				"id":           map[string]any{"type": "integer", "minimum": 1},
				"name":         map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"type":         alertRuleTypeSchema(),
				"threshold":    map[string]any{"type": "number"},
				"durationMins": map[string]any{"type": "integer", "minimum": 0, "maximum": 1440},
				"target":       map[string]any{"type": "string"},
				"enabled":      map[string]any{"type": "boolean"},
				"cooldownMins": map[string]any{"type": "integer", "minimum": 1, "maximum": 10080},
				"createdAt":    map[string]any{"type": "string", "format": "date-time"},
				"updatedAt":    map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"AlertRuleCreateRequest": alertRuleCreateRequestSchema(),
		"AlertRuleUpdateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"minProperties":        1,
			"properties":           alertRuleMutationProperties(),
		},
		"AlertHistory": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "ruleId", "ruleName", "type", "message", "value", "firedAt"},
			"properties": map[string]any{
				"id":       map[string]any{"type": "integer", "minimum": 1},
				"ruleId":   map[string]any{"type": "integer", "minimum": 1},
				"ruleName": map[string]any{"type": "string"},
				"type":     alertRuleTypeSchema(),
				"message":  map[string]any{"type": "string"},
				"value":    map[string]any{"type": "number"},
				"firedAt":  map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"AlertHistoryPage": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"items", "total", "limit", "offset"},
			"properties": map[string]any{
				"items":  map[string]any{"type": "array", "items": schemaRef("AlertHistory")},
				"total":  nonNegativeInteger(),
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
				"offset": nonNegativeInteger(),
			},
		},
		"CreateDeployDomainRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "service", "hostPort"},
			"properties": map[string]any{
				"domain":   deployDomainInputSchema(),
				"service":  composeServiceNameSchema(),
				"hostPort": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			},
		},
		"EnableDeployDomainTLSRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"email": map[string]any{"type": "string", "format": "email", "maxLength": 254},
			},
		},
		"DeployDomain": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "targetId", "domain", "service", "hostPort", "upstream", "tlsStatus", "tlsMessage", "createdAt", "updatedAt"},
			"properties": map[string]any{
				"id":           map[string]any{"type": "integer", "minimum": 1},
				"targetId":     map[string]any{"type": "integer", "minimum": 1},
				"domain":       deployDomainNameSchema(),
				"service":      composeServiceNameSchema(),
				"hostPort":     map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
				"upstream":     map[string]any{"type": "string", "pattern": `^http://127\.0\.0\.1:[1-9][0-9]{0,4}$`},
				"tlsStatus":    map[string]any{"type": "string", "enum": []string{"not_configured", "healthy", "expiring", "expired", "unavailable"}},
				"tlsExpiresAt": map[string]any{"type": "string", "format": "date-time"},
				"tlsDaysRemaining": map[string]any{
					"type": "integer",
				},
				"tlsMessage": map[string]any{"type": "string"},
				"createdAt":  map[string]any{"type": "string", "format": "date-time"},
				"updatedAt":  map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"DeployDomainList": map[string]any{
			"type":  "array",
			"items": schemaRef("DeployDomain"),
		},
		"DeployDomainHealth": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "upstream", "status", "latencyMs", "message", "checkedAt"},
			"properties": map[string]any{
				"domain":     deployDomainNameSchema(),
				"upstream":   map[string]any{"type": "string", "pattern": `^http://127\.0\.0\.1:[1-9][0-9]{0,4}$`},
				"status":     map[string]any{"type": "string", "enum": []string{"healthy", "unhealthy", "unavailable"}},
				"statusCode": map[string]any{"type": "integer", "minimum": 100, "maximum": 999},
				"latencyMs":  map[string]any{"type": "integer", "minimum": 0},
				"message":    map[string]any{"type": "string"},
				"checkedAt":  map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"EnsureRemoteDeployDomainRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"expected_revision", "confirmed"},
			"description":          "Strict confirmation envelope for one revision-aware managed project-domain ensure; no Nginx content, upstream URL, path, command, or secret is accepted.",
			"properties": map[string]any{
				"expected_revision": map[string]any{
					"type":        "string",
					"pattern":     `^(?:absent|[0-9a-f]{64})$`,
					"description": "Current lowercase SHA-256 observation revision, or absent when the mapping is expected not to exist.",
				},
				"confirmed": map[string]any{
					"type":        "boolean",
					"const":       true,
					"description": "Must be true to authorize this managed mutation.",
				},
			},
		},
		"EnsureRemoteDeployDomainResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"changed", "observation"},
			"description":          "Typed managed project-domain ensure receipt. changed=false is an idempotent no-op when the observed active mapping already matches the requested revision.",
			"properties": map[string]any{
				"changed":     map[string]any{"type": "boolean", "description": "Whether the agent had to apply the installation-owned project-domain mapping."},
				"observation": schemaRef("RemoteDeployDomain"),
			},
		},
		"RemoteDeployDomain": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"target_id", "domain", "host_port", "desired_host_port", "upstream", "status",
				"message", "tls_status", "tls_message", "enabled", "revision",
			},
			"description": "Credential-free typed observation of an active, enabled managed project-domain mapping. The loopback upstream and all Nginx/TLS details come from the installation-owned agent plan; they are not caller input.",
			"properties": map[string]any{
				"target_id": map[string]any{
					"type":      "string",
					"minLength": 1,
					"maxLength": 255,
					"pattern":   `^[A-Za-z0-9][A-Za-z0-9._-]*$`,
				},
				"domain":             deployDomainNameSchema(),
				"host_port":          map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
				"desired_host_port":  map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
				"upstream":           map[string]any{"type": "string", "pattern": `^http://127\.0\.0\.1:[1-9][0-9]{0,4}$`, "description": "Observed fixed loopback upstream derived from host_port; arbitrary upstream URLs are not accepted."},
				"status":             map[string]any{"type": "string", "const": "active"},
				"message":            map[string]any{"type": "string"},
				"tls_status":         map[string]any{"type": "string", "enum": []string{"not_configured", "healthy", "expiring", "expired", "unavailable"}},
				"tls_expires_at":     map[string]any{"type": "string", "format": "date-time"},
				"tls_days_remaining": map[string]any{"type": "integer"},
				"tls_message":        map[string]any{"type": "string"},
				"updated_at":         map[string]any{"type": "string", "format": "date-time"},
				"enabled":            map[string]any{"type": "boolean", "const": true},
				"revision":           map[string]any{"type": "string", "pattern": `^[0-9a-f]{64}$`},
			},
		},
		"ComposeService": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"service", "container", "image", "state", "exitCode", "ports"},
			"properties": map[string]any{
				"service":   composeServiceNameSchema(),
				"container": map[string]any{"type": "string", "minLength": 1},
				"image":     map[string]any{"type": "string"},
				"state":     map[string]any{"type": "string"},
				"health":    map[string]any{"type": "string"},
				"exitCode":  map[string]any{"type": "integer"},
				"ports":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		"ComposeServiceList": map[string]any{
			"type":  "array",
			"items": schemaRef("ComposeService"),
		},
		"ComposeServiceLogs": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"logs", "tail", "truncated"},
			"properties": map[string]any{
				"logs":      map[string]any{"type": "string", "maxLength": 1048576},
				"tail":      map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
				"truncated": map[string]any{"type": "boolean"},
			},
		},
		"ComposeServiceActionResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "service", "action"},
			"properties": map[string]any{
				"status":  map[string]any{"type": "string", "const": "ok"},
				"service": composeServiceNameSchema(),
				"action":  map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "recreate"}},
			},
		},
		"DeployEnvironmentVariable": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"key"},
			"properties": map[string]any{
				"key": deployEnvironmentKeySchema(),
			},
		},
		"DeployEnvironment": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"configured", "variables"},
			"properties": map[string]any{
				"configured": map[string]any{"type": "boolean"},
				"variables":  map[string]any{"type": "array", "items": schemaRef("DeployEnvironmentVariable")},
			},
		},
		"DeployEnvironmentSetRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"key", "value"},
			"properties": map[string]any{
				"key": deployEnvironmentKeySchema(),
				"value": map[string]any{
					"type": "string", "writeOnly": true,
					"description": "UTF-8 value capped at 64 KiB and restricted to the one-variable-per-line protected storage format.",
				},
			},
		},
		"CreateDeployTargetRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "projectDir"},
			"properties":           deployTargetMutationProperties(false),
		},
		"UpdateDeployTargetRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "projectDir", "expectedUpdatedAt"},
			"properties":           deployTargetMutationProperties(true),
		},
		"DeployTargetList": map[string]any{
			"type":  "array",
			"items": schemaRef("DeployTarget"),
		},
		"DeployPreflightCheck": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "status", "message"},
			"properties": map[string]any{
				"id":      map[string]any{"type": "string", "minLength": 1},
				"status":  map[string]any{"type": "string", "enum": []string{"pass", "pending", "fail"}},
				"message": map[string]any{"type": "string"},
			},
		},
		"DeployPreflight": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"targetId", "deploymentKind", "eligible", "checks"},
			"properties": map[string]any{
				"targetId":       map[string]any{"type": "integer", "minimum": 1},
				"deploymentKind": map[string]any{"type": "string", "enum": []string{"script", "compose"}},
				"eligible":       map[string]any{"type": "boolean"},
				"checks":         map[string]any{"type": "array", "items": schemaRef("DeployPreflightCheck")},
			},
		},
		"DeployRun": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "targetId", "trigger", "branch", "commit", "prevCommit", "status", "startedAt"},
			"properties": map[string]any{
				"id":         map[string]any{"type": "integer", "minimum": 1},
				"targetId":   map[string]any{"type": "integer", "minimum": 1},
				"trigger":    map[string]any{"type": "string", "enum": []string{"webhook", "manual", "rollback"}},
				"branch":     map[string]any{"type": "string", "minLength": 1, "maxLength": 255},
				"commit":     map[string]any{"type": "string", "pattern": `^(?:[a-f0-9]{40,64})?$`},
				"prevCommit": map[string]any{"type": "string", "pattern": `^(?:[a-f0-9]{40,64})?$`},
				"status":     map[string]any{"type": "string", "enum": []string{"pending", "running", "success", "failed"}},
				"startedAt":  map[string]any{"type": "string", "format": "date-time"},
				"finishedAt": map[string]any{"type": "string", "format": "date-time"},
				"durationMs": map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"DeployRunList": map[string]any{
			"type":  "array",
			"items": schemaRef("DeployRun"),
		},
		"DeployQueueResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "runId"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string", "enum": []string{"deployment queued", "rollback queued"}},
				"runId":   map[string]any{"type": "integer", "minimum": 1},
			},
		},
		"DeployRunLogs": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"logs"},
			"properties": map[string]any{
				"logs": map[string]any{"type": "string"},
			},
		},
		"DeployRevisionComparison": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"targetId", "state", "branch", "trackedChanges", "matchesDeployed",
				"rollbackAvailable", "commitsAheadRollback", "commitsBehindRollback",
				"filesChanged", "insertions", "deletions", "message", "checkedAt",
			},
			"properties": map[string]any{
				"targetId":              map[string]any{"type": "integer", "minimum": 1},
				"state":                 map[string]any{"type": "string", "enum": []string{"not_deployed", "ready", "unavailable"}},
				"branch":                map[string]any{"type": "string"},
				"currentCommit":         map[string]any{"type": "string", "pattern": "^[a-f0-9]{40,64}$"},
				"deployedCommit":        map[string]any{"type": "string", "pattern": "^[a-f0-9]{40,64}$"},
				"rollbackCommit":        map[string]any{"type": "string", "pattern": "^[a-f0-9]{40,64}$"},
				"trackedChanges":        map[string]any{"type": "boolean"},
				"matchesDeployed":       map[string]any{"type": "boolean"},
				"rollbackAvailable":     map[string]any{"type": "boolean"},
				"commitsAheadRollback":  nonNegativeInteger(),
				"commitsBehindRollback": nonNegativeInteger(),
				"filesChanged":          nonNegativeInteger(),
				"insertions":            nonNegativeInteger(),
				"deletions":             nonNegativeInteger(),
				"message":               map[string]any{"type": "string"},
				"checkedAt":             map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"CreateDeployStagingRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"projectDir"},
			"properties": map[string]any{
				"name":       map[string]any{"type": "string", "maxLength": 128},
				"branch":     map[string]any{"type": "string", "maxLength": 255},
				"projectDir": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"DeployTarget": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"id", "name", "repoUrl", "branch", "projectDir", "environment",
				"deploymentKind", "composeFile", "deployScript", "webhookProvider",
				"webhookStatus", "webhookToken", "autoDeploy", "isActive", "createdAt", "updatedAt",
			},
			"properties": map[string]any{
				"id":              map[string]any{"type": "integer", "minimum": 1},
				"name":            map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"repoUrl":         map[string]any{"type": "string", "maxLength": 2048},
				"branch":          map[string]any{"type": "string", "minLength": 1, "maxLength": 255},
				"projectDir":      map[string]any{"type": "string", "minLength": 1},
				"environment":     map[string]any{"type": "string", "enum": []string{"production", "staging"}},
				"sourceTargetId":  map[string]any{"type": "integer", "minimum": 1},
				"deploymentKind":  map[string]any{"type": "string", "enum": []string{"compose", "script"}},
				"composeFile":     map[string]any{"type": "string"},
				"deployScript":    map[string]any{"type": "string"},
				"webhookProvider": map[string]any{"type": "string", "enum": []string{"github", "gitlab"}},
				"webhookStatus":   map[string]any{"type": "string", "enum": []string{"not_configured", "healthy", "unavailable"}},
				"webhookToken":    map[string]any{"type": "string", "maxLength": 0},
				"autoDeploy":      map[string]any{"type": "boolean"},
				"isActive":        map[string]any{"type": "boolean"},
				"createdAt":       map[string]any{"type": "string", "format": "date-time"},
				"updatedAt":       map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"DeployStagingReceipt": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"target", "storageBoundary", "environmentValuesCopied", "webhookSecretCopied", "domainsCopied", "dnsConfigured",
			},
			"properties": map[string]any{
				"target":                  schemaRef("DeployTarget"),
				"storageBoundary":         map[string]any{"type": "string", "const": "isolated_project_directory"},
				"environmentValuesCopied": map[string]any{"type": "boolean", "const": false},
				"webhookSecretCopied":     map[string]any{"type": "boolean", "const": false},
				"domainsCopied":           map[string]any{"type": "boolean", "const": false},
				"dnsConfigured":           map[string]any{"type": "boolean", "const": false},
			},
		},
		"DeployTemplate": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name", "description", "branch", "deploymentKind", "composeFile", "deployScript"},
			"properties": map[string]any{
				"id":             map[string]any{"type": "string", "pattern": `^[a-z0-9][a-z0-9-]{0,63}$`},
				"name":           map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"description":    map[string]any{"type": "string", "maxLength": 512},
				"branch":         map[string]any{"type": "string", "minLength": 1},
				"deploymentKind": map[string]any{"type": "string", "enum": []string{"compose", "script"}},
				"composeFile":    map[string]any{"type": "string"},
				"deployScript":   map[string]any{"type": "string"},
			},
		},
		"DeployTemplateIssue": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"file", "message"},
			"properties": map[string]any{
				"file":    map[string]any{"type": "string", "minLength": 1},
				"message": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"DeployTemplateInventory": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "directory", "templates", "issues"},
			"properties": map[string]any{
				"status":    map[string]any{"type": "string", "enum": []string{"not_configured", "healthy", "unavailable"}},
				"directory": map[string]any{"type": "string"},
				"templates": map[string]any{"type": "array", "maxItems": 128, "items": schemaRef("DeployTemplate")},
				"issues":    map[string]any{"type": "array", "maxItems": 129, "items": schemaRef("DeployTemplateIssue")},
			},
		},
		"HostActionStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"running"},
			"properties": map[string]any{
				"running":    map[string]any{"type": "boolean"},
				"action":     map[string]any{"type": "string", "enum": append(hostActions(), "disk-cleanup")},
				"started_at": map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"RebootStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"pending"},
			"properties": map[string]any{
				"pending":           map[string]any{"type": "boolean"},
				"scheduled_for":     map[string]any{"type": "string", "format": "date-time"},
				"remaining_seconds": nonNegativeInteger(),
			},
		},
		"ServiceControlRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"service", "action"},
			"properties": map[string]any{
				"service": map[string]any{"type": "string", "minLength": 1},
				"action":  map[string]any{"type": "string", "enum": []string{"start", "stop", "restart"}},
			},
		},
		"ProcessSignalRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"pid", "startTime", "signal"},
			"properties": map[string]any{
				"pid":       map[string]any{"type": "integer", "minimum": 2},
				"startTime": map[string]any{"type": "integer", "minimum": 1},
				"signal":    map[string]any{"type": "string", "enum": []string{"term", "kill"}},
			},
		},
		"PHPPoolConfigContent": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"path", "content", "checksum", "size", "mode", "modified_at"},
			"properties": map[string]any{
				"path":        map[string]any{"type": "string"},
				"content":     map[string]any{"type": "string"},
				"checksum":    map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
				"size":        map[string]any{"type": "integer", "minimum": 0, "maximum": 2097152},
				"mode":        map[string]any{"type": "string", "pattern": "^[0-7]{4}$"},
				"modified_at": map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"PHPPoolConfigReplaceRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"content", "checksum"},
			"properties": map[string]any{
				"content":  map[string]any{"type": "string", "maxLength": 2097152},
				"checksum": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
				"reload":   map[string]any{"type": "boolean", "default": false},
			},
		},
		"PHPPoolConfigReplaceReceipt": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "backup", "checksum", "reloaded"},
			"properties": map[string]any{
				"message":  map[string]any{"type": "string"},
				"backup":   map[string]any{"type": "string"},
				"checksum": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
				"reloaded": map[string]any{"type": "boolean"},
			},
		},
		"NginxConfigContent": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"filename", "domain", "type", "isEnabled", "content", "checksum", "size", "modifiedAt"},
			"properties": map[string]any{
				"filename":   map[string]any{"type": "string", "minLength": 1},
				"domain":     map[string]any{"type": "string"},
				"type":       map[string]any{"type": "string"},
				"isEnabled":  map[string]any{"type": "boolean"},
				"content":    map[string]any{"type": "string", "maxLength": 2097152},
				"checksum":   map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
				"size":       map[string]any{"type": "integer", "minimum": 0, "maximum": 2097152},
				"modifiedAt": map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"NginxConfigCreateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "type"},
			"properties": map[string]any{
				"domain":     map[string]any{"type": "string", "minLength": 1, "maxLength": 253},
				"type":       map[string]any{"type": "string", "enum": []string{"php", "static", "proxy", "redirect"}},
				"phpVersion": map[string]any{"type": "string", "pattern": "^[0-9]{1,2}\\.[0-9]{1,2}$"},
				"phpPool":    map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$"},
				"docRoot":    map[string]any{"type": "string", "pattern": "^/"},
				"proxyPass":  map[string]any{"type": "string", "minLength": 1},
				"redirectTo": map[string]any{"type": "string", "minLength": 1},
				"useSSL":     map[string]any{"type": "boolean", "default": false},
				"certPath":   map[string]any{"type": "string", "pattern": "^/"},
				"keyPath":    map[string]any{"type": "string", "pattern": "^/"},
			},
		},
		"NginxConfigStateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"enabled"},
			"properties": map[string]any{
				"enabled": map[string]any{"type": "boolean"},
			},
		},
		"NginxConfigStateReceipt": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"filename", "isEnabled", "message"},
			"properties": map[string]any{
				"filename":  map[string]any{"type": "string", "minLength": 1},
				"isEnabled": map[string]any{"type": "boolean"},
				"message":   map[string]any{"type": "string"},
			},
		},
		"NginxConfigReplaceRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"content", "checksum"},
			"properties": map[string]any{
				"content":  map[string]any{"type": "string", "minLength": 1, "maxLength": 2097152},
				"checksum": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
			},
		},
		"NginxConfigReplaceReceipt": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "backup", "checksum"},
			"properties": map[string]any{
				"message":  map[string]any{"type": "string"},
				"backup":   map[string]any{"type": "string"},
				"checksum": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
			},
		},
		"NginxConfigArchiveRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"checksum"},
			"properties": map[string]any{
				"checksum": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
			},
		},
		"NginxConfigArchiveReceipt": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "archive", "checksum"},
			"properties": map[string]any{
				"message":  map[string]any{"type": "string"},
				"archive":  map[string]any{"type": "string"},
				"checksum": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
			},
		},
		"NginxConfigArchive": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"archive", "filename", "checksum", "size", "archivedAt", "modifiedAt"},
			"properties": map[string]any{
				"archive":    map[string]any{"type": "string", "minLength": 1},
				"filename":   map[string]any{"type": "string", "minLength": 1},
				"checksum":   map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
				"size":       map[string]any{"type": "integer", "minimum": 0, "maximum": 2097152},
				"archivedAt": map[string]any{"type": "string", "format": "date-time"},
				"modifiedAt": map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"NginxConfigArchiveList": map[string]any{
			"type":  "array",
			"items": map[string]any{"$ref": "#/components/schemas/NginxConfigArchive"},
		},
		"NginxConfigArchiveRestoreReceipt": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "archive", "filename", "checksum", "isEnabled"},
			"properties": map[string]any{
				"message":   map[string]any{"type": "string"},
				"archive":   map[string]any{"type": "string", "minLength": 1},
				"filename":  map[string]any{"type": "string", "minLength": 1},
				"checksum":  map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
				"isEnabled": map[string]any{"type": "boolean", "const": false},
			},
		},
		"NginxConfigBackup": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"backup", "filename", "checksum", "size", "createdAt", "modifiedAt"},
			"properties": map[string]any{
				"backup":     map[string]any{"type": "string", "minLength": 1},
				"filename":   map[string]any{"type": "string", "minLength": 1},
				"checksum":   map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
				"size":       map[string]any{"type": "integer", "minimum": 0, "maximum": 2097152},
				"createdAt":  map[string]any{"type": "string", "format": "date-time"},
				"modifiedAt": map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"NginxConfigBackupList": map[string]any{
			"type":  "array",
			"items": map[string]any{"$ref": "#/components/schemas/NginxConfigBackup"},
		},
		"NginxConfigBackupRestoreRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"backupChecksum", "currentChecksum"},
			"properties": map[string]any{
				"backupChecksum":  map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
				"currentChecksum": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
			},
		},
		"NginxConfigBackupRestoreReceipt": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "backup", "recovery", "filename", "checksum", "isEnabled"},
			"properties": map[string]any{
				"message":   map[string]any{"type": "string"},
				"backup":    map[string]any{"type": "string", "minLength": 1},
				"recovery":  map[string]any{"type": "string", "minLength": 1},
				"filename":  map[string]any{"type": "string", "minLength": 1},
				"checksum":  map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
				"isEnabled": map[string]any{"type": "boolean"},
			},
		},
		"LocalDomain": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name", "type", "root", "sslEnabled", "isActive", "createdAt", "updatedAt"},
			"properties":           localDomainProperties(),
		},
		"LocalDomainList": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domains"},
			"properties": map[string]any{
				"domains": map[string]any{"type": "array", "items": schemaRef("LocalDomain")},
			},
		},
		"LocalDomainDetail": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"id", "name", "type", "root", "sslEnabled", "isActive", "createdAt", "updatedAt",
				"serverNames", "nginxConfig", "nginxContent", "accessLogPath", "errorLogPath",
			},
			"properties": localDomainDetailProperties(),
		},
		"DomainDNSCapability": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"provider", "status", "proxied", "message"},
			"properties": map[string]any{
				"provider":   map[string]any{"type": "string", "const": "cloudflare"},
				"status":     map[string]any{"type": "string", "enum": []string{"not_configured", "unavailable", "healthy"}},
				"origin":     map[string]any{"type": "string"},
				"recordType": map[string]any{"type": "string", "enum": []string{"A", "AAAA"}},
				"proxied":    map[string]any{"type": "boolean"},
				"message":    map[string]any{"type": "string"},
			},
		},
		"DomainProvisioningCapabilities": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"vhostsRoot", "nginxSitesAvailable", "nginxSitesEnabled", "nginxSnippetsDir", "dns"},
			"properties": map[string]any{
				"vhostsRoot":          map[string]any{"type": "string"},
				"nginxSitesAvailable": map[string]any{"type": "string"},
				"nginxSitesEnabled":   map[string]any{"type": "string"},
				"nginxSnippetsDir":    map[string]any{"type": "string"},
				"dns":                 schemaRef("DomainDNSCapability"),
			},
		},
		"DomainCreateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain"},
			"properties":           domainCreateRequestProperties(),
		},
		"DomainDNSRecordAction": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"action", "type", "name", "newValue"},
			"properties": map[string]any{
				"action":   map[string]any{"type": "string", "enum": []string{"created", "updated", "skipped"}},
				"type":     map[string]any{"type": "string", "enum": []string{"A", "AAAA"}},
				"name":     localDomainNameSchema(),
				"oldValue": map[string]any{"type": "string"},
				"newValue": map[string]any{"type": "string"},
			},
		},
		"DomainDNSResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "zoneId", "recordType", "change"},
			"properties": map[string]any{
				"domain":     localDomainNameSchema(),
				"zoneId":     map[string]any{"type": "string", "minLength": 1},
				"recordType": map[string]any{"type": "string", "enum": []string{"A", "AAAA"}},
				"change":     schemaRef("DomainDNSRecordAction"),
			},
		},
		"DomainCreateResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "domain"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string", "const": "domain created"},
				"domain":  localDomainNameSchema(),
				"dns":     schemaRef("DomainDNSResult"),
				"warning": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"DomainCheckRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain"},
			"properties": map[string]any{
				"domain": map[string]any{"type": "string", "minLength": 1, "maxLength": 65536},
			},
		},
		"DomainCheckResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"valid", "is_subdomain", "parent_exists", "suggested_webroot", "suggested_port", "dns_zones", "conflicts",
			},
			"properties": map[string]any{
				"valid":             map[string]any{"type": "boolean"},
				"is_subdomain":      map[string]any{"type": "boolean"},
				"parent_domain":     localDomainNameSchema(),
				"parent_exists":     map[string]any{"type": "boolean"},
				"suggested_webroot": map[string]any{"type": "string"},
				"suggested_port":    map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
				"dns_zones":         map[string]any{"type": "array", "items": localDomainNameSchema()},
				"conflicts":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		"DomainDeleteResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "domain"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string", "const": "domain deleted"},
				"domain":  localDomainNameSchema(),
			},
		},
		"DomainToggleRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"active"},
			"properties": map[string]any{
				"active": map[string]any{"type": "boolean"},
			},
		},
		"DomainToggleResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"domain", "active"},
			"properties": map[string]any{
				"domain": localDomainNameSchema(),
				"active": map[string]any{"type": "boolean"},
			},
		},
		"CronJobCreateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"schedule", "command"},
			"properties": map[string]any{
				"user":        map[string]any{"type": "string", "pattern": `^[a-zA-Z_][a-zA-Z0-9_.-]{0,31}$`, "minLength": 1, "maxLength": 32, "default": "root"},
				"schedule":    map[string]any{"type": "string", "minLength": 1},
				"command":     map[string]any{"type": "string", "minLength": 1},
				"description": map[string]any{"type": "string"},
			},
		},
		"CronServiceStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"available", "installed", "running", "state", "daemonState"},
			"properties": map[string]any{
				"available":   map[string]any{"type": "boolean"},
				"installed":   map[string]any{"type": "boolean"},
				"running":     map[string]any{"type": "boolean"},
				"state":       map[string]any{"type": "string", "enum": []string{"healthy", "not-installed", "stopped", "unavailable"}},
				"daemonState": map[string]any{"type": "string", "minLength": 1},
				"error":       map[string]any{"type": "string"},
			},
		},
		"CronJob": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "user", "schedule", "command", "description", "isActive", "humanSchedule"},
			"properties": map[string]any{
				"id":            map[string]any{"type": "string", "pattern": "^[a-f0-9]{16}$"},
				"user":          map[string]any{"type": "string", "pattern": `^[a-zA-Z_][a-zA-Z0-9_.-]{0,31}$`},
				"schedule":      map[string]any{"type": "string", "minLength": 1},
				"command":       map[string]any{"type": "string", "minLength": 1},
				"description":   map[string]any{"type": "string"},
				"isActive":      map[string]any{"type": "boolean"},
				"humanSchedule": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"CronJobListResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"jobs", "total"},
			"properties": map[string]any{
				"jobs":  map[string]any{"type": "array", "items": schemaRef("CronJob")},
				"total": map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"CronJobMutationResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"job", "message"},
			"properties": map[string]any{
				"job":     schemaRef("CronJob"),
				"message": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"CronJobDeleteResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "message"},
			"properties": map[string]any{
				"id":      map[string]any{"type": "string", "minLength": 1},
				"message": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"CronSystemFile": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"path", "name", "dir", "entries", "size"},
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "minLength": 1},
				"name":    map[string]any{"type": "string", "minLength": 1},
				"dir":     map[string]any{"type": "string", "minLength": 1},
				"entries": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"size":    map[string]any{"type": "integer", "format": "int64", "minimum": 0},
			},
		},
		"CronSystemFileListResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"files", "total"},
			"properties": map[string]any{
				"files": map[string]any{"type": "array", "items": schemaRef("CronSystemFile")},
				"total": map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"CronJobUpdateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"schedule", "command", "description", "isActive"},
			"properties": map[string]any{
				"schedule":    map[string]any{"type": "string", "minLength": 1},
				"command":     map[string]any{"type": "string", "minLength": 1},
				"description": map[string]any{"type": "string"},
				"isActive":    map[string]any{"type": "boolean"},
			},
		},
		"FirewallRule": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"number", "to", "action", "direction", "from", "isIPv6"},
			"properties": map[string]any{
				"number":    map[string]any{"type": "integer", "minimum": 1},
				"to":        map[string]any{"type": "string"},
				"action":    map[string]any{"type": "string"},
				"direction": map[string]any{"type": "string"},
				"from":      map[string]any{"type": "string"},
				"protocol":  map[string]any{"type": "string"},
				"isIPv6":    map[string]any{"type": "boolean"},
				"comment":   map[string]any{"type": "string"},
			},
		},
		"FirewallStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"available", "state", "backend", "active", "defaultIncoming", "defaultOutgoing", "defaultRouted", "loggingLevel", "rules"},
			"properties": map[string]any{
				"available":       map[string]any{"type": "boolean"},
				"state":           map[string]any{"type": "string", "enum": []string{"healthy", "ufw-missing", "unavailable"}},
				"backend":         map[string]any{"type": "string", "enum": []string{"ufw", "iptables", "none"}},
				"error":           map[string]any{"type": "string"},
				"active":          map[string]any{"type": "boolean"},
				"defaultIncoming": map[string]any{"type": "string"},
				"defaultOutgoing": map[string]any{"type": "string"},
				"defaultRouted":   map[string]any{"type": "string"},
				"loggingLevel":    map[string]any{"type": "string"},
				"rules":           map[string]any{"type": "array", "items": schemaRef("FirewallRule")},
			},
		},
		"FirewallRuleListResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"rules"},
			"properties": map[string]any{
				"rules": map[string]any{"type": "array", "items": schemaRef("FirewallRule")},
			},
		},
		"FirewallRuleDeleteResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "ruleNumber"},
			"properties": map[string]any{
				"message":    map[string]any{"type": "string", "const": "firewall rule deleted"},
				"ruleNumber": map[string]any{"type": "integer", "minimum": 1},
			},
		},
		"FirewallToggleResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"message", "status"},
			"properties": map[string]any{
				"message": map[string]any{"type": "string", "enum": []string{"UFW firewall enabled", "UFW firewall disabled"}},
				"status":  map[string]any{"type": "string", "enum": []string{"enabled", "disabled"}},
			},
		},
		"FirewallRuleCreateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"action"},
			"properties": map[string]any{
				"action":    map[string]any{"type": "string", "enum": []string{"allow", "deny", "reject", "limit"}},
				"direction": map[string]any{"type": "string", "enum": []string{"in", "out"}, "default": "in"},
				"protocol":  map[string]any{"type": "string", "enum": []string{"tcp", "udp", "any"}},
				"port":      map[string]any{"type": "string", "maxLength": 20, "pattern": `^[a-zA-Z0-9:/_-]{0,20}$`},
				"from":      map[string]any{"type": "string"},
				"to":        map[string]any{"type": "string"},
				"comment":   map[string]any{"type": "string"},
			},
		},
		"FirewallToggleRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"enable"},
			"properties": map[string]any{
				"enable": map[string]any{"type": "boolean"},
			},
		},
		"FileEntry": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "path", "type", "size", "permissions", "owner", "group", "modified"},
			"properties": map[string]any{
				"name":        map[string]any{"type": "string", "minLength": 1},
				"path":        map[string]any{"type": "string", "minLength": 1},
				"type":        map[string]any{"type": "string", "enum": []string{"file", "directory", "symlink"}},
				"size":        map[string]any{"type": "integer", "format": "int64", "minimum": 0},
				"permissions": map[string]any{"type": "string", "minLength": 1},
				"owner":       map[string]any{"type": "string", "minLength": 1},
				"group":       map[string]any{"type": "string", "minLength": 1},
				"modified":    map[string]any{"type": "string", "format": "date-time"},
				"target":      map[string]any{"type": "string"},
			},
		},
		"FileListResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"entries"},
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "minLength": 1, "description": "Returned when a directory path was requested."},
				"roots":   map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1}, "description": "Configured roots returned when path is omitted."},
				"entries": map[string]any{"type": "array", "items": schemaRef("FileEntry")},
			},
		},
		"FileReadResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"path", "content"},
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "minLength": 1},
				"content": map[string]any{"type": "string"},
			},
		},
		"FileWriteRequest": map[string]any{
			"type":     "object",
			"required": []string{"path"},
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "minLength": 1},
				"content": map[string]any{"type": "string", "description": "Text to write; omitted content is decoded as empty text by the current handler."},
			},
		},
		"FileWriteResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "path"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "ok"},
				"path":   map[string]any{"type": "string", "minLength": 1},
			},
		},
		"FileCreateRequest": map[string]any{
			"type":     "object",
			"required": []string{"path"},
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "minLength": 1},
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{"file", "directory", "dir"},
					"default":     "file",
					"description": "file creates a file; directory and dir create a directory.",
				},
			},
		},
		"FileCreateResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "path"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "created"},
				"path":   map[string]any{"type": "string", "minLength": 1},
			},
		},
		"FileDeleteResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "path"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "deleted"},
				"path":   map[string]any{"type": "string", "minLength": 1},
			},
		},
		"FileRenameRequest": map[string]any{
			"type": "object",
			"anyOf": []any{
				map[string]any{"required": []string{"src", "dst"}},
				map[string]any{"required": []string{"old_path", "new_path"}},
				map[string]any{"required": []string{"src", "new_path"}},
				map[string]any{"required": []string{"old_path", "dst"}},
			},
			"properties": map[string]any{
				"src":      map[string]any{"type": "string", "description": "Current source path; takes precedence over old_path when non-empty."},
				"dst":      map[string]any{"type": "string", "description": "Destination path; takes precedence over new_path when non-empty."},
				"old_path": map[string]any{"type": "string", "description": "Legacy source-path field used when src is empty."},
				"new_path": map[string]any{"type": "string", "description": "Legacy destination-path field used when dst is empty."},
			},
		},
		"FileRenameResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "src", "dst"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "renamed"},
				"src":    map[string]any{"type": "string", "minLength": 1},
				"dst":    map[string]any{"type": "string", "minLength": 1},
			},
		},
		"PM2Process": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name", "status", "cpu", "memory", "uptime", "restarts", "mode", "pid"},
			"properties": map[string]any{
				"id":       map[string]any{"type": "integer", "minimum": 0},
				"name":     map[string]any{"type": "string", "minLength": 1},
				"status":   map[string]any{"type": "string", "minLength": 1},
				"cpu":      map[string]any{"type": "number", "minimum": 0},
				"memory":   map[string]any{"type": "number", "minimum": 0, "description": "Observed memory in mebibytes."},
				"uptime":   map[string]any{"type": "integer", "format": "int64", "minimum": 0, "description": "Observed uptime in seconds."},
				"restarts": map[string]any{"type": "integer", "minimum": 0},
				"mode":     map[string]any{"type": "string"},
				"pid":      map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"PM2ProcessList": map[string]any{
			"type":  "array",
			"items": schemaRef("PM2Process"),
		},
		"PM2LogsResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "lines", "output"},
			"properties": map[string]any{
				"id":     map[string]any{"type": "string", "minLength": 1},
				"lines":  map[string]any{"type": "integer", "minimum": 1, "maximum": 5000},
				"output": map[string]any{"type": "string"},
			},
		},
		"PM2ControlResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "output"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "minLength": 1},
				"output": map[string]any{"type": "string"},
			},
		},
		"PM2DeployResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "name", "output"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "deployed"},
				"name":   map[string]any{"type": "string", "minLength": 1},
				"output": map[string]any{"type": "string"},
			},
		},
		"PM2SaveResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "output"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "saved"},
				"output": map[string]any{"type": "string"},
			},
		},
		"PM2DeployRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "script"},
			"properties": map[string]any{
				"name":      map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": `^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`},
				"script":    map[string]any{"type": "string", "minLength": 1, "description": "Absolute script path below an HSERVER_PM2_ALLOWED_ROOTS entry."},
				"cwd":       map[string]any{"type": "string", "description": "Optional absolute working directory below an HSERVER_PM2_ALLOWED_ROOTS entry."},
				"instances": map[string]any{"type": "integer", "minimum": 1, "maximum": 64, "default": 1},
				"exec_mode": map[string]any{"type": "string", "enum": []string{"fork", "cluster"}, "default": "fork"},
				"node_env":  map[string]any{"type": "string", "enum": []string{"production", "development"}},
			},
		},
		"DockerStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"installed", "running", "containersTotal", "containersRunning", "imageCount"},
			"properties": map[string]any{
				"installed":         map[string]any{"type": "boolean"},
				"running":           map[string]any{"type": "boolean"},
				"version":           map[string]any{"type": "string"},
				"containersTotal":   map[string]any{"type": "integer", "minimum": 0},
				"containersRunning": map[string]any{"type": "integer", "minimum": 0},
				"imageCount":        map[string]any{"type": "integer", "minimum": 0},
				"error":             map[string]any{"type": "string"},
			},
		},
		"DockerContainer": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name", "image", "status", "detail", "ports", "cpuPercent", "memoryUsage", "memoryLimit", "created"},
			"properties": map[string]any{
				"id":          map[string]any{"type": "string", "minLength": 1},
				"name":        map[string]any{"type": "string"},
				"image":       map[string]any{"type": "string"},
				"status":      map[string]any{"type": "string", "enum": []string{"running", "paused", "restarting", "exited", "stopped"}},
				"detail":      map[string]any{"type": "string"},
				"ports":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"cpuPercent":  map[string]any{"type": "number", "minimum": 0},
				"memoryUsage": map[string]any{"type": "integer", "format": "int64", "minimum": 0},
				"memoryLimit": map[string]any{"type": "integer", "format": "int64", "minimum": 0},
				"created":     map[string]any{"type": "string"},
			},
		},
		"DockerContainerList": map[string]any{
			"type":  "array",
			"items": schemaRef("DockerContainer"),
		},
		"DockerContainerLogs": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"logs", "tail", "truncated"},
			"properties": map[string]any{
				"logs":      map[string]any{"type": "string", "maxLength": 1048576},
				"tail":      map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
				"truncated": map[string]any{"type": "boolean"},
			},
		},
		"DockerContainerActionResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "action", "container"},
			"properties": map[string]any{
				"status":    map[string]any{"type": "string", "const": "ok"},
				"action":    map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "pause", "unpause", "remove"}},
				"container": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"DockerImage": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "repoTags", "size", "created"},
			"properties": map[string]any{
				"id":       map[string]any{"type": "string", "minLength": 1},
				"repoTags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"size":     map[string]any{"type": "string"},
				"created":  map[string]any{"type": "string"},
			},
		},
		"DockerImageList": map[string]any{
			"type":  "array",
			"items": schemaRef("DockerImage"),
		},
		"DockerImageMutationResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "image"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "ok"},
				"image":  map[string]any{"type": "string", "minLength": 1},
			},
		},
		"DockerImagePullRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name"},
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"minLength":   1,
					"maxLength":   256,
					"pattern":     `^[A-Za-z0-9][A-Za-z0-9_.:/@-]{0,255}$`,
					"description": "Image name, tag, or digest; parent-path markers are rejected by the server.",
				},
			},
		},
		"DiskCleanupTarget": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name", "description", "size", "risk"},
			"properties": map[string]any{
				"id":          map[string]any{"type": "string", "minLength": 1},
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"size":        nonNegativeInteger(),
				"risk":        map[string]any{"type": "string", "enum": []string{"low", "medium"}},
				"scope":       map[string]any{"type": "string"},
			},
		},
		"LocalDiskCleanupTarget": map[string]any{
			"allOf": []any{
				schemaRef("DiskCleanupTarget"),
				map[string]any{
					"type":     "object",
					"required": []string{"scope"},
					"properties": map[string]any{
						"scope": map[string]any{"type": "string"},
					},
				},
			},
		},
		"DiskCleanupExecuteRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"targets"},
			"properties": map[string]any{
				"targets": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "uniqueItems": true, "items": map[string]any{"type": "string", "minLength": 1}},
			},
		},
		"ManagedDiskCleanupExecuteRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"targets", "confirmed"},
			"properties": map[string]any{
				"targets":   map[string]any{"type": "array", "minItems": 1, "maxItems": 4, "uniqueItems": true, "items": map[string]any{"type": "string", "minLength": 1}},
				"confirmed": map[string]any{"type": "boolean", "const": true},
			},
		},
		"DiskCleanupResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "status", "message", "reclaimed"},
			"properties": map[string]any{
				"id":        map[string]any{"type": "string"},
				"status":    map[string]any{"type": "string", "enum": []string{"ok", "error"}},
				"message":   map[string]any{"type": "string"},
				"reclaimed": nonNegativeInteger(),
			},
		},
		"LocalDiskCleanupExecution": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"results", "root_available_before", "root_available_after"},
			"properties": map[string]any{
				"results":               map[string]any{"type": "array", "items": schemaRef("DiskCleanupResult")},
				"root_available_before": nonNegativeInteger(),
				"root_available_after":  nonNegativeInteger(),
			},
		},
		"ManagedDiskCleanupExecution": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"results"},
			"properties": map[string]any{
				"results":    map[string]any{"type": "array", "items": schemaRef("DiskCleanupResult")},
				"scan_error": map[string]any{"type": "string"},
			},
		},
		"BackupArtifact": map[string]any{
			"type":     "object",
			"required": []string{"id", "name", "type", "path", "size", "status", "createdAt"},
			"properties": map[string]any{
				"id":        map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
				"type":      map[string]any{"type": "string", "enum": []string{"full", "database", "files"}},
				"path":      map[string]any{"type": "string"},
				"size":      nonNegativeInteger(),
				"diskSize":  nonNegativeInteger(),
				"status":    map[string]any{"type": "string", "enum": []string{"completed", "invalid", "orphaned"}},
				"createdAt": map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"BackupStorageSummary": map[string]any{
			"type":       "object",
			"required":   storageRequired,
			"properties": storageProperties,
		},
		"BackupListResponse": map[string]any{
			"type":     "object",
			"required": []string{"backups", "storage"},
			"properties": map[string]any{
				"backups": map[string]any{"type": "array", "items": schemaRef("BackupArtifact")},
				"storage": schemaRef("BackupStorageSummary"),
			},
		},
		"BackupCreateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"type":        map[string]any{"type": "string", "enum": []string{"full", "database", "files"}, "default": "full"},
				"name":        map[string]any{"type": "string"},
				"engine":      map[string]any{"type": "string", "enum": []string{"postgresql", "mariadb"}, "default": "postgresql"},
				"database":    map[string]any{"type": "string"},
				"compression": map[string]any{"type": "integer", "default": 6, "description": "gzip level 1-9; omitted or out-of-range values use 6"},
				"retention":   map[string]any{"type": "integer"},
				"vhosts": map[string]any{
					"type": "array", "maxItems": 16, "uniqueItems": true,
					"items":       map[string]any{"type": "string", "minLength": 1, "maxLength": 253, "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`},
					"description": "Observed site folder identities resolved below the installation-owned vhost root.",
				},
			},
		},
		"BackupSchedule": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"retention_count", "retention_days", "cron", "type", "rawLine"},
			"properties": map[string]any{
				"frequency":       map[string]any{"type": "string", "enum": []string{"daily", "weekly", "monthly"}},
				"time":            map[string]any{"type": "string", "pattern": `^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`},
				"retention_count": map[string]any{"type": "integer"},
				"retention_days": map[string]any{
					"type":        "integer",
					"deprecated":  true,
					"description": "Compatibility alias for retention_count; the value is an artifact count, not elapsed days.",
				},
				"cron":    map[string]any{"type": "string", "minLength": 9},
				"type":    map[string]any{"type": "string"},
				"rawLine": map[string]any{"type": "string", "minLength": 1},
			},
		},
		"BackupScheduleListResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"schedules"},
			"properties": map[string]any{
				"schedules": map[string]any{"type": "array", "items": schemaRef("BackupSchedule")},
			},
		},
		"BackupScheduleSetRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"oneOf": []any{
				map[string]any{
					"required": []string{"cron"},
					"not": map[string]any{"anyOf": []any{
						map[string]any{"required": []string{"frequency"}},
						map[string]any{"required": []string{"time"}},
					}},
				},
				map[string]any{
					"required": []string{"frequency", "time"},
					"not":      map[string]any{"required": []string{"cron"}},
				},
			},
			"allOf": []any{
				map[string]any{"not": map[string]any{"anyOf": []any{
					map[string]any{"required": []string{"retention_count", "retentionCount"}},
					map[string]any{"required": []string{"retention_count", "retention_days"}},
					map[string]any{"required": []string{"retentionCount", "retention_days"}},
				}}},
			},
			"properties": map[string]any{
				"cron":      map[string]any{"type": "string", "minLength": 9, "description": "Safe five-field cron expression; mutually exclusive with frequency/time."},
				"frequency": map[string]any{"type": "string", "enum": []string{"daily", "weekly", "monthly"}},
				"time":      map[string]any{"type": "string", "pattern": `^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`},
				"type":      map[string]any{"type": "string", "enum": []string{"full", "database", "files", "snapshot"}, "default": "full"},
				"database":  map[string]any{"type": "string", "pattern": `^[A-Za-z0-9_.-]*$`},
				"retention_count": map[string]any{
					"type": "integer", "minimum": 1, "maximum": 365, "default": 10,
				},
				"retentionCount": map[string]any{
					"type": "integer", "minimum": 1, "maximum": 365, "deprecated": true,
				},
				"retention_days": map[string]any{
					"type": "integer", "minimum": 1, "maximum": 365, "deprecated": true,
					"description": "Compatibility alias; the value is an artifact count, not elapsed days.",
				},
			},
		},
		"BackupScheduleDeleteRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"rawLine"},
			"properties": map[string]any{
				"rawLine": map[string]any{"type": "string", "minLength": 1, "description": "Exact rawLine value returned by GET /api/backups/schedules."},
			},
		},
		"BackupScheduleMutationResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"success"},
			"properties": map[string]any{
				"success": map[string]any{"type": "boolean", "const": true},
			},
		},
		"GDriveOAuthAppUpdateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"anyOf": []any{
				map[string]any{"required": []string{"clientId"}},
				map[string]any{"required": []string{"gcpProjectId"}},
			},
			"properties": map[string]any{
				"clientId":     map[string]any{"type": "string", "minLength": 1, "maxLength": 512},
				"clientSecret": map[string]any{"type": "string", "maxLength": 4096, "writeOnly": true},
				"gcpProjectId": map[string]any{"type": "string", "minLength": 1, "maxLength": 255},
			},
		},
		"GDriveOAuthCompleteRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"state"},
			"properties": map[string]any{
				"state": map[string]any{"type": "string", "pattern": `^[a-f0-9]{32}$`},
			},
		},
		"GDriveSettingsUpdateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"folder", "autoUpload", "remoteRetentionDays", "notifyOnSuccess", "notifyOnFailure"},
			"properties": map[string]any{
				"folder": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 255,
					"pattern": `^(?!.*(?:^|/)\.{1,2}(?:/|$))[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*$`,
				},
				"autoUpload":          map[string]any{"type": "boolean"},
				"remoteRetentionDays": map[string]any{"type": "integer", "minimum": 0, "maximum": 365, "description": "Zero disables remote age-based deletion."},
				"notifyOnSuccess":     map[string]any{"type": "boolean"},
				"notifyOnFailure":     map[string]any{"type": "boolean"},
			},
		},
		"GDriveRestoreRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"fileName"},
			"properties": map[string]any{
				"fileName": map[string]any{
					"type": "string", "minLength": 8, "maxLength": 255,
					"pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,247}(?:\.tar\.gz|\.sql\.gz)$`,
				},
			},
		},
		"GDriveMutationResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"success"},
			"properties": map[string]any{
				"success": map[string]any{"type": "boolean", "const": true},
			},
		},
		"GDriveOAuthStartResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"authUrl", "state"},
			"properties": map[string]any{
				"authUrl": map[string]any{"type": "string", "format": "uri"},
				"state":   map[string]any{"type": "string", "pattern": `^[a-f0-9]{32}$`},
			},
		},
		"GDriveRestoreAccepted": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "fileName"},
			"properties": map[string]any{
				"status":   map[string]any{"type": "string", "const": "downloading"},
				"fileName": map[string]any{"type": "string"},
				"jobId":    map[string]any{"type": "string"},
			},
		},
		"GDriveConnectionTestResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "message"},
			"properties": map[string]any{
				"status":  map[string]any{"type": "string", "const": "ok"},
				"message": map[string]any{"type": "string"},
			},
		},
		"SnapshotSettings": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"destination", "repoFolder", "enabledPaths", "keepDaily", "keepWeekly", "keepMonthly"},
			"properties": map[string]any{
				"destination":  map[string]any{"type": "string", "enum": []string{"gdrive", "s3"}},
				"repoFolder":   map[string]any{"type": "string"},
				"enabledPaths": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string", "enum": snapshotManifestIDs}, "uniqueItems": true},
				"keepDaily":    map[string]any{"type": "integer"},
				"keepWeekly":   map[string]any{"type": "integer"},
				"keepMonthly":  map[string]any{"type": "integer"},
				"lastRunAt":    map[string]any{"type": "string", "format": "date-time"},
				"lastSnapshotId": map[string]any{
					"type": "string",
				},
				"lastError":            map[string]any{"type": "string"},
				"passwordAcknowledged": map[string]any{"type": "boolean"},
			},
		},
		"SnapshotSettingsUpdateRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"destination", "repoFolder", "enabledPaths", "keepDaily", "keepWeekly", "keepMonthly", "passwordAcknowledged"},
			"properties": map[string]any{
				"destination": map[string]any{"type": "string", "enum": []string{"gdrive", "s3"}},
				"repoFolder": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 128,
					"pattern": `^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`,
				},
				"enabledPaths": map[string]any{
					"type": "array", "maxItems": len(snapshotManifestIDs), "uniqueItems": true,
					"items": map[string]any{"type": "string", "enum": snapshotManifestIDs},
				},
				"keepDaily":            map[string]any{"type": "integer", "minimum": 1, "maximum": 365},
				"keepWeekly":           map[string]any{"type": "integer", "minimum": 1, "maximum": 260},
				"keepMonthly":          map[string]any{"type": "integer", "minimum": 1, "maximum": 120},
				"passwordAcknowledged": map[string]any{"type": "boolean"},
			},
		},
		"SnapshotSettingsMutationResult": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"success"},
			"properties": map[string]any{
				"success": map[string]any{"type": "boolean", "const": true},
			},
		},
		"SnapshotManifestEntry": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "path", "label", "required", "enabled"},
			"properties": map[string]any{
				"id":        map[string]any{"type": "string", "enum": snapshotManifestIDs},
				"path":      map[string]any{"type": "string"},
				"label":     map[string]any{"type": "string"},
				"required":  map[string]any{"type": "boolean"},
				"enabled":   map[string]any{"type": "boolean"},
				"available": map[string]any{"type": "boolean"},
				"exclude":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		"SnapshotRepoStats": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"snapshotCount", "totalSize", "totalFileSize"},
			"properties": map[string]any{
				"snapshotCount": map[string]any{"type": "integer", "minimum": 0},
				"totalSize":     map[string]any{"type": "integer", "minimum": 0},
				"totalFileSize": map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"ResticSnapshot": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "time", "hostname", "paths"},
			"properties": map[string]any{
				"id":       map[string]any{"type": "string", "pattern": `^[A-Fa-f0-9]{8,64}$`},
				"time":     map[string]any{"type": "string", "format": "date-time"},
				"hostname": map[string]any{"type": "string"},
				"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"paths":    map[string]any{"type": "integer", "minimum": 0},
				"size":     map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"SnapshotStatus": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required": []string{
				"resticFound", "repoInitialized", "passwordSet", "destination", "destinationStatus",
				"canPurgeRepository", "driveConnected", "settings", "manifest",
			},
			"properties": map[string]any{
				"resticFound":        map[string]any{"type": "boolean"},
				"repoInitialized":    map[string]any{"type": "boolean"},
				"passwordSet":        map[string]any{"type": "boolean"},
				"destination":        map[string]any{"type": "string", "enum": []string{"gdrive", "s3"}},
				"destinationStatus":  map[string]any{"type": "string", "enum": []string{"not_configured", "unavailable", "healthy"}},
				"destinationMessage": map[string]any{"type": "string"},
				"canPurgeRepository": map[string]any{"type": "boolean"},
				"driveConnected": map[string]any{
					"type": "boolean", "description": "Compatibility field for older Google Drive clients.",
				},
				"settings":      map[string]any{"$ref": "#/components/schemas/SnapshotSettings"},
				"manifest":      map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/SnapshotManifestEntry"}},
				"repoStats":     map[string]any{"$ref": "#/components/schemas/SnapshotRepoStats"},
				"lastSnapshots": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/ResticSnapshot"}},
			},
		},
		"SnapshotListResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"snapshots"},
			"properties": map[string]any{
				"snapshots": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/ResticSnapshot"}},
			},
		},
		"SnapshotRestoreRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"snapshotId"},
			"properties": map[string]any{
				"snapshotId": map[string]any{"type": "string", "pattern": `^[A-Fa-f0-9]{8,64}$`},
				"manifestIds": map[string]any{
					"type": "array", "maxItems": len(snapshotRestoreManifestIDs), "uniqueItems": true,
					"items":       map[string]any{"type": "string", "enum": snapshotRestoreManifestIDs},
					"description": "Logical manifest identities resolved to installation-owned paths by the server.",
				},
				"vhosts": map[string]any{
					"type": "array", "maxItems": 16, "uniqueItems": true,
					"items":       map[string]any{"type": "string", "minLength": 1, "maxLength": 253, "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`},
					"description": "Vhost directory names resolved below the installation-owned vhost root.",
				},
			},
		},
		"SnapshotPurgeRepositoryRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"repoFolder", "confirmation"},
			"properties": map[string]any{
				"repoFolder": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 128,
					"pattern": `^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`,
				},
				"confirmation": map[string]any{"type": "string", "const": "purge-snapshot-repository"},
			},
		},
		"SnapshotRunAccepted": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status", "message"},
			"properties": map[string]any{
				"status":  map[string]any{"type": "string", "const": "running"},
				"message": map[string]any{"type": "string", "const": "incremental snapshot started"},
				"jobId":   map[string]any{"type": "string"},
			},
		},
		"SnapshotRestoreAccepted": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "const": "restoring"},
				"jobId":  map[string]any{"type": "string"},
			},
		},
		"AsyncJobAccepted": map[string]any{
			"type":     "object",
			"required": []string{"jobId", "status", "message"},
			"properties": map[string]any{
				"jobId":   map[string]any{"type": "string"},
				"status":  map[string]any{"type": "string", "const": "pending"},
				"message": map[string]any{"type": "string"},
			},
		},
		"RestoreValidation": map[string]any{
			"type":     "object",
			"required": []string{"id", "name", "type", "artifactBytes", "includesDatabase", "includesFiles", "databaseRecovery", "filesRollback"},
			"properties": map[string]any{
				"id":               map[string]any{"type": "string"},
				"name":             map[string]any{"type": "string"},
				"type":             map[string]any{"type": "string", "enum": []string{"full", "database", "files"}},
				"artifactBytes":    nonNegativeInteger(),
				"includesDatabase": map[string]any{"type": "boolean"},
				"includesFiles":    map[string]any{"type": "boolean"},
				"databaseEngine":   map[string]any{"type": "string", "enum": []string{"postgresql", "mariadb"}},
				"databaseTarget":   map[string]any{"type": "string"},
				"databaseRecovery": map[string]any{"type": "boolean"},
				"filesRollback":    map[string]any{"type": "boolean"},
			},
		},
		"ManagedNodeServiceState": map[string]any{
			"type":     "object",
			"required": []string{"name", "active"},
			"properties": map[string]any{
				"name":   map[string]any{"type": "string"},
				"active": map[string]any{"type": "string"},
				"sub":    map[string]any{"type": "string"},
			},
		},
		"ManagedNodeDiskMount": map[string]any{
			"type":     "object",
			"required": []string{"filesystem", "size", "used", "available", "use_percent", "mountpoint"},
			"properties": map[string]any{
				"filesystem":  map[string]any{"type": "string"},
				"size":        nonNegativeInteger(),
				"used":        nonNegativeInteger(),
				"available":   nonNegativeInteger(),
				"use_percent": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
				"mountpoint":  map[string]any{"type": "string"},
			},
		},
		"ManagedNodeProcess": map[string]any{
			"type":     "object",
			"required": []string{"pid", "startTime", "user", "cpu", "memory", "rss", "command"},
			"properties": map[string]any{
				"pid":       map[string]any{"type": "integer", "minimum": 1},
				"startTime": map[string]any{"type": "integer", "minimum": 1},
				"user":      map[string]any{"type": "string"},
				"cpu":       nonNegativeNumber(),
				"memory":    nonNegativeNumber(),
				"rss":       nonNegativeInteger(),
				"command":   map[string]any{"type": "string"},
			},
		},
		"ManagedNodeInventory": map[string]any{
			"type": "object",
			"required": []string{
				"os", "kernel", "boot_id", "uptime_seconds", "load_1", "memory_total_bytes",
				"memory_available_bytes", "swap_total_bytes", "swap_used_bytes", "swap_free_bytes",
				"swap_reset_eligible", "disk_total_bytes", "disk_used_bytes", "disk_available_bytes",
				"disk_use_percent", "plesk_present", "services", "processes",
			},
			"properties": map[string]any{
				"os":                     map[string]any{"type": "string"},
				"arch":                   map[string]any{"type": "string", "maxLength": 32, "description": "Agent binary architecture when reported by current agents; omitted by legacy agents."},
				"kernel":                 map[string]any{"type": "string"},
				"boot_id":                map[string]any{"type": "string"},
				"uptime_seconds":         map[string]any{"type": "integer"},
				"load_1":                 map[string]any{"type": "number"},
				"memory_total_bytes":     nonNegativeInteger(),
				"memory_available_bytes": nonNegativeInteger(),
				"swap_total_bytes":       nonNegativeInteger(),
				"swap_used_bytes":        nonNegativeInteger(),
				"swap_free_bytes":        nonNegativeInteger(),
				"swap_reset_eligible":    map[string]any{"type": "boolean"},
				"swap_reset_reason":      map[string]any{"type": "string"},
				"disk_total_bytes":       nonNegativeInteger(),
				"disk_used_bytes":        nonNegativeInteger(),
				"disk_available_bytes":   nonNegativeInteger(),
				"disk_use_percent":       map[string]any{"type": "number", "minimum": 0, "maximum": 100},
				"disk_mounts":            nullableArray(schemaRef("ManagedNodeDiskMount")),
				"plesk_present":          map[string]any{"type": "boolean"},
				"services":               nullableArray(schemaRef("ManagedNodeServiceState")),
				"processes":              nullableArray(schemaRef("ManagedNodeProcess")),
				"log_sources":            nullableArray(map[string]any{"type": "string"}),
				"file_read_roots":        nullableArray(map[string]any{"type": "string"}),
				"file_write_roots":       nullableArray(map[string]any{"type": "string"}),
			},
		},
		"ManagedNodeCompatibility": map[string]any{
			"type":     "object",
			"required": []string{"panel_version", "expected_protocol", "protocol_compatible", "agent_version_state"},
			"properties": map[string]any{
				"panel_version":       map[string]any{"type": "string"},
				"expected_protocol":   map[string]any{"type": "string", "const": agenthub.ProtocolVersion},
				"protocol_compatible": map[string]any{"type": "boolean"},
				"agent_version_state": map[string]any{"type": "string", "enum": []string{string(agenthub.AgentVersionCurrent), string(agenthub.AgentVersionBehind), string(agenthub.AgentVersionAhead), string(agenthub.AgentVersionUnknown)}},
			},
		},
		"ManagedNodeRecord": map[string]any{
			"type":     "object",
			"required": []string{"id", "name", "hostname", "agent_version", "protocol_version", "capabilities", "inventory", "created_at", "updated_at"},
			"properties": map[string]any{
				"id":               map[string]any{"type": "string"},
				"name":             map[string]any{"type": "string"},
				"hostname":         map[string]any{"type": "string"},
				"agent_version":    map[string]any{"type": "string"},
				"protocol_version": map[string]any{"type": "string"},
				"capabilities":     nullableArray(map[string]any{"type": "string", "enum": managedNodeCapabilities()}),
				"inventory":        schemaRef("ManagedNodeInventory"),
				"last_seen_at":     map[string]any{"type": "string", "format": "date-time"},
				"created_at":       map[string]any{"type": "string", "format": "date-time"},
				"updated_at":       map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"ManagedNode": map[string]any{
			"allOf": []any{
				schemaRef("ManagedNodeRecord"),
				map[string]any{
					"type":     "object",
					"required": []string{"compatibility", "online"},
					"properties": map[string]any{
						"compatibility": schemaRef("ManagedNodeCompatibility"),
						"online":        map[string]any{"type": "boolean", "description": "Hub-computed connectivity from the server-observed heartbeat."},
					},
				},
			},
		},
		"ManagedNodeList": map[string]any{
			"type":  "array",
			"items": schemaRef("ManagedNode"),
		},
		"ManagedNodeMetricsCPU": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"usage_percent", "core_count"},
			"description":          "Credential-free typed CPU observation from a managed node.",
			"properties": map[string]any{
				"usage_percent": map[string]any{"type": "number", "minimum": 0, "maximum": 100},
				"core_count":    map[string]any{"type": "integer", "minimum": 1, "maximum": 65536},
			},
		},
		"ManagedNodeMetricsLoad": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"one", "five", "fifteen"},
			"description":          "Credential-free typed load-average observation from a managed node.",
			"properties": map[string]any{
				"one":     map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
				"five":    map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
				"fifteen": map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
			},
		},
		"ManagedNodeMetricsMemory": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"total_bytes", "used_bytes", "available_bytes", "usage_percent"},
			"description":          "Credential-free typed memory observation from a managed node.",
			"properties": map[string]any{
				"total_bytes":     map[string]any{"type": "integer", "minimum": 1},
				"used_bytes":      map[string]any{"type": "integer", "minimum": 0},
				"available_bytes": map[string]any{"type": "integer", "minimum": 0},
				"usage_percent":   map[string]any{"type": "number", "minimum": 0, "maximum": 100},
			},
		},
		"ManagedNodeMetricsNetwork": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"rx_bytes", "tx_bytes"},
			"description":          "Credential-free typed network-byte counters from a managed node.",
			"properties": map[string]any{
				"rx_bytes": map[string]any{"type": "integer", "minimum": 0},
				"tx_bytes": map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"ManagedNodeMetricsFilesystem": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"total_bytes", "used_bytes", "available_bytes", "usage_percent"},
			"description":          "Credential-free typed root-filesystem observation from a managed node.",
			"properties": map[string]any{
				"total_bytes":     map[string]any{"type": "integer", "minimum": 1},
				"used_bytes":      map[string]any{"type": "integer", "minimum": 0},
				"available_bytes": map[string]any{"type": "integer", "minimum": 0},
				"usage_percent":   map[string]any{"type": "number", "minimum": 0, "maximum": 100},
			},
		},
		"ManagedNodeMetrics": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"observed_at", "cpu", "load", "memory", "network", "root_disk"},
			"description":          "Fresh credential-free typed metrics snapshot from a managed node; task transport data, command output, paths, provider data, and secrets are not part of this schema.",
			"properties": map[string]any{
				"observed_at": map[string]any{"type": "string", "format": "date-time", "description": "UTC RFC3339 observation timestamp."},
				"cpu":         schemaRef("ManagedNodeMetricsCPU"),
				"load":        schemaRef("ManagedNodeMetricsLoad"),
				"memory":      schemaRef("ManagedNodeMetricsMemory"),
				"network":     schemaRef("ManagedNodeMetricsNetwork"),
				"root_disk":   schemaRef("ManagedNodeMetricsFilesystem"),
			},
		},
		"ManagedPHPFPMPool": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "path"},
			"description":          "Credential-free typed PHP-FPM pool projection from a managed node; arbitrary provider configuration and secret fields are not part of this schema.",
			"properties": map[string]any{
				"name":         map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"path":         map[string]any{"type": "string", "minLength": 1},
				"user":         map[string]any{"type": "string"},
				"group":        map[string]any{"type": "string"},
				"listen":       map[string]any{"type": "string"},
				"pm":           map[string]any{"type": "string"},
				"max_children": map[string]any{"type": "integer"},
			},
		},
		"ManagedPHPFPMVersion": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"version", "unit", "active", "enabled", "masked", "pools"},
			"description":          "Credential-free typed PHP-FPM version projection from a managed node; binary identity is optional and no arbitrary provider record or secret field is accepted.",
			"properties": map[string]any{
				"version": map[string]any{"type": "string", "pattern": `^[0-9]+\.[0-9]+$`},
				"unit":    map[string]any{"type": "string"},
				"active":  map[string]any{"type": "string"},
				"enabled": map[string]any{"type": "string"},
				"masked":  map[string]any{"type": "boolean"},
				"binary":  map[string]any{"type": "string", "minLength": 1},
				"pools":   map[string]any{"type": "array", "maxItems": 512, "items": schemaRef("ManagedPHPFPMPool")},
			},
		},
		"ManagedPHPFPMVersionList": map[string]any{
			"type":        "array",
			"maxItems":    32,
			"items":       schemaRef("ManagedPHPFPMVersion"),
			"description": "Bounded managed-node PHP-FPM inventory; an empty array means the read succeeded and no supported version was observed.",
		},
		"ManagedPM2Process": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name", "status", "pid", "cpu", "memory", "uptime", "restarts", "mode", "cwd", "script", "version"},
			"description":          "Credential-free typed PM2 process projection from a managed node; arbitrary provider records, environment values, and secret fields are not part of this schema.",
			"properties": map[string]any{
				"id":       map[string]any{"type": "integer", "minimum": 0},
				"name":     map[string]any{"type": "string", "maxLength": 128},
				"status":   map[string]any{"type": "string", "maxLength": 64},
				"pid":      map[string]any{"type": "integer", "minimum": 0},
				"cpu":      map[string]any{"type": "number", "minimum": 0},
				"memory":   map[string]any{"type": "integer", "format": "int64", "minimum": 0},
				"uptime":   map[string]any{"type": "integer", "format": "int64", "minimum": 0},
				"restarts": map[string]any{"type": "integer", "minimum": 0},
				"mode":     map[string]any{"type": "string", "maxLength": 128},
				"cwd":      map[string]any{"type": "string", "maxLength": 4096},
				"script":   map[string]any{"type": "string", "maxLength": 4096},
				"version":  map[string]any{"type": "string", "maxLength": 128},
			},
		},
		"ManagedPM2ProcessList": map[string]any{
			"type":        "array",
			"maxItems":    512,
			"items":       schemaRef("ManagedPM2Process"),
			"description": "Bounded managed-node PM2 process inventory; an empty array means the read succeeded and no process was observed.",
		},
		"ManagedContainer": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name", "image", "state", "status", "ports"},
			"description":          "Credential-free typed Docker container projection from a managed node; arbitrary provider records, environment values, and secret fields are not part of this schema.",
			"properties": map[string]any{
				"id":     map[string]any{"type": "string", "minLength": 1},
				"name":   map[string]any{"type": "string", "maxLength": 128},
				"image":  map[string]any{"type": "string"},
				"state":  map[string]any{"type": "string"},
				"status": map[string]any{"type": "string"},
				"ports":  map[string]any{"type": "string"},
			},
		},
		"ManagedContainerList": map[string]any{
			"type":        "array",
			"maxItems":    256,
			"items":       schemaRef("ManagedContainer"),
			"description": "Bounded managed-node Docker container inventory; an empty array means the read succeeded and no container was observed.",
		},
		"AgentProfile": agentProfileSchema(),
		"AgentProfilePutRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"profile", "expectedRevision"},
			"properties": map[string]any{
				"profile":          schemaRef("AgentProfile"),
				"expectedRevision": map[string]any{"type": "integer", "format": "int64", "minimum": 0, "description": "Non-negative compare-and-swap revision; use 0 when no profile is stored."},
			},
		},
		"AgentProfileApplyRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"expectedRevision", "confirmed"},
			"description":          "Strict admin apply envelope. It carries only a positive desired-profile revision and explicit confirmation; the desired profile and agent task payload are resolved by the hub.",
			"properties": map[string]any{
				"expectedRevision": map[string]any{"type": "integer", "format": "int64", "minimum": 1, "description": "Positive panel-owned desired-profile revision to apply."},
				"confirmed":        map[string]any{"type": "boolean", "const": true, "description": "Must be true to authorize this remote mutation."},
			},
		},
		"AgentProfileObservation": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"state", "revision"},
			"description":          "Optional bounded profile observation accepted from a managed-node heartbeat. Raw paths, command output, environment values, and secrets are not part of this wire shape.",
			"properties": map[string]any{
				"state": map[string]any{
					"type":        "string",
					"enum":        profileObservationStates(),
					"description": "Agent-reported profile state; absence of an observation is exposed by the panel as not_reported.",
				},
				"revision": map[string]any{"type": "integer", "format": "int64", "minimum": 0, "description": "Non-negative profile revision observed by the agent."},
				"error_code": map[string]any{
					"type":        "string",
					"enum":        profileErrorCodes(),
					"description": "Optional closed safe error code; raw agent diagnostics never cross the heartbeat boundary.",
				},
			},
		},
		"AgentProfileDesiredResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"state", "revision", "profile"},
			"description":          "Panel-owned desired profile state. An unconfigured node has revision 0 and profile null; a configured profile is returned even when every capability is disabled.",
			"properties": map[string]any{
				"state": map[string]any{
					"type": "string", "enum": []string{"configured", "not_configured"},
					"description": "Whether the panel has persisted a desired profile for this node.",
				},
				"revision": map[string]any{"type": "integer", "format": "int64", "minimum": 0, "description": "Monotonic desired-profile revision used by PUT compare-and-swap."},
				"profile": map[string]any{
					"oneOf":       []any{schemaRef("AgentProfile"), map[string]any{"type": "null"}},
					"description": "The configured fixed seven-field profile, or null when state is not_configured.",
				},
			},
		},
		"AgentProfileObservedResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"capabilities", "online", "lastSeenAt", "agentVersion", "protocolVersion", "profileState", "profileRevision", "profileErrorCode"},
			"description":          "Raw server-observed node snapshot. Capabilities are returned as advertised by the node without being converted into desired profile state. Pointer fields are null when the hub has no corresponding observation.",
			"properties": map[string]any{
				"capabilities": map[string]any{
					"type":        []string{"array", "null"},
					"uniqueItems": true,
					"items":       map[string]any{"type": "string"},
					"description": "Raw capability identifiers advertised by the managed node; no profile or task data is inferred.",
				},
				"online":           map[string]any{"type": "boolean"},
				"lastSeenAt":       map[string]any{"type": []string{"string", "null"}, "format": "date-time"},
				"agentVersion":     map[string]any{"type": "string"},
				"protocolVersion":  map[string]any{"type": "string"},
				"profileState":     map[string]any{"type": "string", "enum": profileObservedResponseStates(), "description": "Observed profile lifecycle state; not_reported means no current observation is available."},
				"profileRevision":  map[string]any{"type": []string{"integer", "null"}, "format": "int64", "minimum": 0, "description": "Observed non-negative profile revision, or null when no observation is available."},
				"profileErrorCode": map[string]any{"type": []string{"string", "null"}, "enum": nullableEnum(profileErrorCodes()), "description": "Observed closed safe error code, empty when no error was reported, or null when no observation is available."},
			},
		},
		"AgentProfileApplyResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"state", "reason"},
			"description":          "Resolved apply lifecycle. A completed agent task is only a transport acknowledgement; applied is authoritative only after a heartbeat reports the requested revision.",
			"properties": map[string]any{
				"state": map[string]any{
					"type":        "string",
					"enum":        profileApplyStates(),
					"description": "Apply state. manual_required is used when the node does not advertise agent.profile.apply.",
				},
				"reason": map[string]any{
					"type":        "string",
					"enum":        profileApplyReasons(),
					"description": "Empty or closed safe reason; raw task and agent diagnostics are never returned.",
				},
				"desiredRevision":  map[string]any{"type": []string{"integer", "null"}, "format": "int64", "minimum": 0, "description": "Panel-owned desired revision, or null when no profile is configured."},
				"taskId":           map[string]any{"type": []string{"integer", "null"}, "format": "int64", "minimum": 1, "description": "Profile apply task identifier, or null when no task is queued or recorded for the desired revision."},
				"observedRevision": map[string]any{"type": []string{"integer", "null"}, "format": "int64", "minimum": 0, "description": "Revision last observed from the capable agent, or null before a profile observation."},
				"observedState":    map[string]any{"type": []string{"string", "null"}, "enum": nullableEnum(profileObservedAgentStates()), "description": "Last agent-reported profile state, or null before a profile observation."},
			},
			"allOf": []any{
				map[string]any{
					"if": map[string]any{
						"required":   []string{"state"},
						"properties": map[string]any{"state": map[string]any{"const": "manual_required"}},
					},
					"then": map[string]any{"properties": map[string]any{"reason": map[string]any{"const": "self_apply_not_supported"}}},
				},
				map[string]any{
					"if": map[string]any{
						"required": []string{"state"},
						"properties": map[string]any{"state": map[string]any{
							"enum": []string{"not_requested", "queued", "running", "awaiting_heartbeat", "applied", "failed", "drifted"},
						}},
					},
					"then": map[string]any{"required": []string{"desiredRevision", "taskId", "observedRevision", "observedState"}},
				},
			},
		},
		"AgentProfileResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"nodeId", "desired", "observed", "apply"},
			"description":          "Admin-only managed-node profile response. It contains the top-level node identity, desired state, raw observation, and bounded apply lifecycle; no secrets, arbitrary profile fields, or raw agent task data are included.",
			"properties": map[string]any{
				"nodeId":   map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`},
				"desired":  schemaRef("AgentProfileDesiredResponse"),
				"observed": schemaRef("AgentProfileObservedResponse"),
				"apply":    schemaRef("AgentProfileApplyResponse"),
			},
		},
		"ManagedNodeRegisterRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name"},
			"properties": map[string]any{
				"id":   map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`},
				"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 255},
			},
		},
		"ManagedNodeRegistration": map[string]any{
			"type":     "object",
			"required": []string{"node", "token"},
			"properties": map[string]any{
				"node":  schemaRef("ManagedNodeRecord"),
				"token": map[string]any{"type": "string", "description": "One-time enrollment bearer token; never returned again."},
			},
		},
		"AgentTaskRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"kind", "confirmed"},
			"properties": map[string]any{
				"kind":      map[string]any{"type": "string", "enum": managedNodeTaskRequestKinds(), "description": "Generic task kinds accepted by POST /api/nodes/{id}/tasks; agent.profile.apply is created only by the dedicated profile apply operation."},
				"payload":   map[string]any{"type": []string{"object", "null"}, "maxProperties": 6, "additionalProperties": map[string]any{"type": "string"}, "description": "Per-kind bounded payload; consult the agent-hub contract for exact fields."},
				"confirmed": map[string]any{"type": "boolean", "const": true},
			},
		},
		"AgentTask": map[string]any{
			"type":     "object",
			"required": []string{"id", "node_id", "kind", "payload", "status", "created_at"},
			"properties": map[string]any{
				"id":           map[string]any{"type": "integer", "minimum": 1},
				"node_id":      map[string]any{"type": "string"},
				"kind":         map[string]any{"type": "string", "enum": managedNodeTaskKinds()},
				"payload":      stringMap(true),
				"status":       map[string]any{"type": "string", "enum": []string{agenthub.TaskStatusQueued, agenthub.TaskStatusRunning, agenthub.TaskStatusCompleted, agenthub.TaskStatusFailed}},
				"result":       stringMap(true),
				"error":        map[string]any{"type": "string"},
				"created_at":   map[string]any{"type": "string", "format": "date-time"},
				"started_at":   map[string]any{"type": "string", "format": "date-time"},
				"completed_at": map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"AgentTaskList": map[string]any{
			"type":  "array",
			"items": schemaRef("AgentTask"),
		},
		"AgentHeartbeatRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"protocol_version", "node_id", "sent_at"},
			"properties": map[string]any{
				"protocol_version": map[string]any{"type": "string", "const": agenthub.ProtocolVersion},
				"node_id":          map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`},
				"agent_version":    map[string]any{"type": "string", "maxLength": 128},
				"capabilities": map[string]any{
					"type": []string{"array", "null"}, "uniqueItems": true,
					"items": map[string]any{"type": "string", "enum": managedNodeCapabilities()},
				},
				"hostname":  map[string]any{"type": "string", "maxLength": 255},
				"sent_at":   map[string]any{"type": "string", "format": "date-time"},
				"inventory": schemaRef("ManagedNodeInventory"),
				"profile":   map[string]any{"oneOf": []any{schemaRef("AgentProfileObservation"), map[string]any{"type": "null"}}, "description": "Optional profile observation from an agent advertising agent.profile.apply; absence is accepted for older agents."},
			},
		},
		"AgentHeartbeatResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"accepted", "server_at"},
			"properties": map[string]any{
				"accepted":  map[string]any{"type": "boolean", "const": true},
				"server_at": map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"AgentTaskPollResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"task": schemaRef("AgentTask"),
			},
		},
		"AgentTaskResultRequest": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"status"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{agenthub.TaskStatusCompleted, agenthub.TaskStatusFailed}},
				"result": map[string]any{
					"type": []string{"object", "null"}, "maxProperties": 100,
					"propertyNames":        map[string]any{"type": "string", "minLength": 1, "maxLength": 255},
					"additionalProperties": map[string]any{"type": "string", "maxLength": 5 << 20},
				},
				"error": map[string]any{"type": "string", "maxLength": 4096},
			},
			"allOf": []any{map[string]any{
				"if": map[string]any{
					"required":   []string{"status"},
					"properties": map[string]any{"status": map[string]any{"const": agenthub.TaskStatusFailed}},
				},
				"then": map[string]any{
					"required": []string{"error"},
					"properties": map[string]any{
						"error": map[string]any{"type": "string", "minLength": 1, "maxLength": 4096, "pattern": `.*\S.*`},
					},
				},
			}},
		},
	}
	for name, schema := range integrationCatalogSchemas() {
		schemas[name] = schema
	}
	for name, schema := range integrationStatusSchemas() {
		schemas[name] = schema
	}
	for name, schema := range managedIntegrationStatusSchemas() {
		schemas[name] = schema
	}
	return schemas
}

func agentProfileSchema() map[string]any {
	// The runtime accepts only this fixed ASCII grammar for profile-supplied
	// paths. The installer remains authoritative for local protected-root and
	// symlink boundaries that cannot be represented in a portable schema.
	const cleanPathPattern = `^$|^/(?!$)(?!\.{1,2}(?:/|$))(?!.*//)(?!.*\/\.{1,2}(?:/|$))[A-Za-z0-9._+:-]+(?:/[A-Za-z0-9._+:-]+)*$`
	profilePath := func(file bool) map[string]any {
		description := "Optional clean absolute path other than / using only ASCII letters, digits, dot, underscore, slash, plus, colon, and hyphen. The value is limited to 4096 bytes; the agent installer also rejects local protected-root and symlink overlap."
		if file {
			description += " This file path must not have a trailing slash."
		}
		return map[string]any{
			"type":        "string",
			"maxLength":   4096,
			"pattern":     cleanPathPattern,
			"description": description,
		}
	}
	writeRoot := profilePath(false)
	writeRoot["minLength"] = 1
	writeRoot["description"] = "One non-empty clean absolute write root other than /; roots are unique and limited to 16 entries."

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"allowDeployRead", "allowDeployActions", "allowDeployDomainRead",
			"allowDeployDomainActions", "deployPlansFile", "deployAcmeWebroot", "deployWriteRoots",
		},
		"description": "Fixed desired deployment capability profile. No secret, executable, arbitrary key, or agent task is accepted.",
		"allOf": []any{
			map[string]any{
				"if":   map[string]any{"required": []string{"allowDeployActions"}, "properties": map[string]any{"allowDeployActions": map[string]any{"const": true}}},
				"then": map[string]any{"properties": map[string]any{"allowDeployRead": map[string]any{"const": true}}},
			},
			map[string]any{
				"if":   map[string]any{"required": []string{"allowDeployDomainRead"}, "properties": map[string]any{"allowDeployDomainRead": map[string]any{"const": true}}},
				"then": map[string]any{"properties": map[string]any{"allowDeployRead": map[string]any{"const": true}}},
			},
			map[string]any{
				"if":   map[string]any{"required": []string{"allowDeployDomainActions"}, "properties": map[string]any{"allowDeployDomainActions": map[string]any{"const": true}}},
				"then": map[string]any{"properties": map[string]any{"allowDeployDomainRead": map[string]any{"const": true}}},
			},
		},
		"properties": map[string]any{
			"allowDeployRead":          map[string]any{"type": "boolean"},
			"allowDeployActions":       map[string]any{"type": "boolean", "description": "Requires allowDeployRead=true."},
			"allowDeployDomainRead":    map[string]any{"type": "boolean", "description": "Requires allowDeployRead=true."},
			"allowDeployDomainActions": map[string]any{"type": "boolean", "description": "Requires allowDeployDomainRead=true."},
			"deployPlansFile":          profilePath(true),
			"deployAcmeWebroot":        profilePath(false),
			"deployWriteRoots": map[string]any{
				"type":        "array",
				"maxItems":    16,
				"uniqueItems": true,
				"items":       writeRoot,
				"description": "Array of at most 16 unique clean absolute write roots; an empty array disables configured write roots.",
			},
		},
	}
}

func profileObservationStates() []string {
	return []string{
		agenthub.ProfileObservationNotConfigured,
		agenthub.ProfileObservationPendingRestart,
		agenthub.ProfileObservationApplied,
		agenthub.ProfileObservationFailed,
	}
}

func profileObservedResponseStates() []string {
	return append([]string{"not_reported"}, profileObservationStates()...)
}

func profileObservedAgentStates() []string {
	return profileObservationStates()
}

func profileApplyStates() []string {
	return []string{
		"manual_required",
		"not_requested",
		"queued",
		"running",
		"awaiting_heartbeat",
		"applied",
		"failed",
		"drifted",
	}
}

func profileErrorCodes() []string {
	return []string{
		"",
		agenthub.ProfileErrorCodeNotConfigured,
		agenthub.ProfileErrorCodeInvalidProfile,
		agenthub.ProfileErrorCodePermissionDenied,
		agenthub.ProfileErrorCodeWriteFailed,
		agenthub.ProfileErrorCodeRestartFailed,
		agenthub.ProfileErrorCodeApplyFailed,
		agenthub.ProfileErrorCodeProfileApplyFailed,
		agenthub.ProfileErrorCodeUnsupported,
		agenthub.ProfileErrorCodeNotSupported,
		agenthub.ProfileErrorCodeTimeout,
		agenthub.ProfileErrorCodeStaleRevision,
		agenthub.ProfileErrorCodeInvalidRevision,
		agenthub.ProfileErrorCodeAgentError,
		agenthub.ProfileErrorCodeUnknown,
		agenthub.ProfileErrorCodeMissing,
		agenthub.ProfileErrorCodeCorrupt,
		agenthub.ProfileErrorCodeStateCorrupt,
		agenthub.ProfileErrorCodeRevisionInvalid,
		agenthub.ProfileErrorCodePayloadInvalid,
		agenthub.ProfileErrorCodePayloadTooLarge,
		agenthub.ProfileErrorCodeApplyUnavailable,
		agenthub.ProfileErrorCodeScheduleFailed,
		agenthub.ProfileErrorCodeStoreFailed,
		agenthub.ProfileErrorCodeSuperseded,
	}
}

func profileApplyReasons() []string {
	return append([]string{"self_apply_not_supported", "profile_revision_drift"}, profileErrorCodes()...)
}

func nullableEnum(values []string) []any {
	result := make([]any, 0, len(values)+1)
	result = append(result, nil)
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func managedNodeCapabilities() []string {
	return []string{
		agenthub.CapabilityInventory, agenthub.CapabilityServiceStatus, agenthub.CapabilityServiceAction,
		agenthub.CapabilityHostAction, agenthub.CapabilityProcessRead, agenthub.CapabilityProcessSignal,
		agenthub.CapabilityTerminal, agenthub.CapabilityDiskCleanup, agenthub.CapabilityLogsRead,
		agenthub.CapabilityContainerRead, agenthub.CapabilityContainerAction, agenthub.CapabilityNginxAction,
		agenthub.CapabilityNginxConfigRead, agenthub.CapabilityNginxConfigWrite, agenthub.CapabilityPHPRead,
		agenthub.CapabilityPHPWrite, agenthub.CapabilityPHPAction, agenthub.CapabilityPM2Read,
		agenthub.CapabilityPM2Action, agenthub.CapabilityCronRead, agenthub.CapabilityCronWrite,
		agenthub.CapabilityCronRun, agenthub.CapabilityFirewallRead, agenthub.CapabilityFirewallWrite,
		agenthub.CapabilityDomainRead, agenthub.CapabilityDomainAction, agenthub.CapabilitySSLRead,
		agenthub.CapabilitySSLAction, agenthub.CapabilityDatabaseRead, agenthub.CapabilityDatabaseAction,
		agenthub.CapabilityBackupRead, agenthub.CapabilityBackupRun, agenthub.CapabilityFilesRead,
		agenthub.CapabilityFilesWrite, agenthub.CapabilityDeployRead, agenthub.CapabilityDeployAction,
		agenthub.CapabilityDeployDomainRead, agenthub.CapabilityDeployDomainAction,
		agenthub.CapabilityAgentUpdateRead, agenthub.CapabilityAgentUpdateAction,
		agenthub.CapabilityIntegrationStatus, agenthub.CapabilityProfileApply,
	}
}

func hostActions() []string {
	return []string{"memory-optimize", "swap-reset", "temp-clean", "reboot", "reboot-cancel"}
}

func managedNodeTaskKinds() []string {
	return []string{
		agenthub.TaskServiceStatus, agenthub.TaskServiceAction, agenthub.TaskHostAction,
		agenthub.TaskProcessSignal, agenthub.TaskDiskCleanupScan, agenthub.TaskDiskCleanupExecute,
		agenthub.TaskLogsRead, agenthub.TaskContainerList, agenthub.TaskContainerAction,
		agenthub.TaskNginxAction, agenthub.TaskNginxConfigList, agenthub.TaskNginxConfigRead,
		agenthub.TaskNginxConfigWrite, agenthub.TaskPHPInventory, agenthub.TaskPHPConfigRead,
		agenthub.TaskPHPConfigWrite, agenthub.TaskPHPAction, agenthub.TaskPM2List,
		agenthub.TaskPM2Logs, agenthub.TaskPM2Action, agenthub.TaskCronInventory,
		agenthub.TaskCronCreate, agenthub.TaskCronUpdate, agenthub.TaskCronDelete,
		agenthub.TaskCronRun, agenthub.TaskFirewallInventory, agenthub.TaskFirewallAdd,
		agenthub.TaskFirewallDelete, agenthub.TaskDomainInventory, agenthub.TaskDomainAction,
		agenthub.TaskSSLInventory, agenthub.TaskSSLAction, agenthub.TaskDatabaseInventory,
		agenthub.TaskDatabaseAction, agenthub.TaskBackupInventory, agenthub.TaskBackupRun,
		agenthub.TaskFilesBrowse, agenthub.TaskFilesRead, agenthub.TaskFilesWrite,
		agenthub.TaskDeployInventory, agenthub.TaskDeployAction, agenthub.TaskDeployDomainInventory,
		agenthub.TaskDeployDomainHealth, agenthub.TaskDeployDomainAction, agenthub.TaskAgentUpdateStatus,
		agenthub.TaskAgentUpdateAction, agenthub.TaskProfileApply,
	}
}

func managedNodeTaskRequestKinds() []string {
	kinds := managedNodeTaskKinds()
	return kinds[:len(kinds)-1]
}

func operationID(item route) string {
	value := strings.ToLower(item.method) + "-" + strings.Trim(item.path, "/")
	value = strings.NewReplacer("/", "-", "{", "", "}", "", ".", "-", "_", "-").Replace(value)
	value = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(value, "-")
	value = regexp.MustCompile(`-+`).ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}

func validateRoutes(routes []route) error {
	routeKeys := make(map[string]struct{}, len(routes))
	operationIDs := make(map[string]string, len(routes))
	for _, item := range routes {
		if !strings.HasPrefix(item.path, "/") {
			return fmt.Errorf("route path %q must begin with /", item.path)
		}
		key := item.method + " " + item.path
		if _, exists := routeKeys[key]; exists {
			return fmt.Errorf("duplicate route %s", key)
		}
		routeKeys[key] = struct{}{}
		operationID := operationID(item)
		if previous, exists := operationIDs[operationID]; exists {
			return fmt.Errorf("operationId %q is shared by %s and %s", operationID, previous, key)
		}
		operationIDs[operationID] = key
	}
	return nil
}

func routeTag(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "root"
	}
	if parts[0] == "api" && len(parts) > 1 {
		return parts[1]
	}
	return parts[0]
}

var pathParameterPattern = regexp.MustCompile(`\{([^{}]+)\}`)

func pathParameters(path string) []map[string]any {
	matches := pathParameterPattern.FindAllStringSubmatch(path, -1)
	parameters := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		schema := map[string]any{"type": "string"}
		if strings.HasPrefix(path, "/api/deploy/") {
			switch match[1] {
			case "id", "targetId", "domainId":
				schema = map[string]any{"type": "integer", "minimum": 1}
			case "service":
				schema = composeServiceNameSchema()
			case "key":
				schema = deployEnvironmentKeySchema()
			}
		}
		if strings.HasPrefix(path, "/api/databases/") {
			switch match[1] {
			case "engine":
				schema = databaseEngineSchema()
			case "name":
				if strings.Contains(path, "/pgm-backup-files/") {
					schema = databaseBackupNameSchema()
				} else {
					schema = databaseIdentifierSchema()
				}
			}
		}
		if path == "/api/settings/{key}" && match[1] == "key" {
			schema = editableSettingKeySchema()
		}
		if strings.HasPrefix(path, "/api/domains/") && match[1] == "id" {
			schema = localDomainNameSchema()
		}
		if strings.HasPrefix(path, "/api/uptime/") && match[1] == "id" {
			schema = map[string]any{"type": "integer", "format": "int64", "minimum": 1}
		}
		parameters = append(parameters, map[string]any{
			"name":     match[1],
			"in":       "path",
			"required": true,
			"schema":   schema,
		})
	}
	return parameters
}

func routeSecurity(auth string) []map[string][]string {
	switch auth {
	case "Public":
		return []map[string][]string{}
	case "Managed-node agent":
		return []map[string][]string{{"agentBearer": {}}}
	case "Local internal trigger":
		return []map[string][]string{{"cronSecret": {}}}
	default:
		return []map[string][]string{{"panelBearer": {}}, {"panelSession": {}}}
	}
}

func checkGenerated(path string, generated []byte) {
	current, err := os.ReadFile(path)
	if err != nil {
		fail("read generated API documentation", err)
	}
	if !bytes.Equal(current, generated) {
		fmt.Fprintf(os.Stderr, "%s is stale; run make gen-api-docs\n", path)
		os.Exit(1)
	}
}

func writeGenerated(path string, generated []byte) {
	if err := os.WriteFile(path, generated, 0o644); err != nil {
		fail("write API documentation", err)
	}
}

func fail(operation string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", operation, err)
	os.Exit(1)
}
