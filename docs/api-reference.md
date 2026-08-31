# Heyserver — API Reference

This is a curated guide to common request and response contracts. The complete,
generated routing and access-level contract is maintained in the
[API route inventory](api-routes.md) and machine-readable
[OpenAPI 3.1 contract](openapi.json). The OpenAPI document guarantees route,
path-parameter, and access-boundary coverage. The generated contract also
publishes machine-readable schemas for panel-user CRUD, deployment templates,
bootstrap health, onboarding, password login, TOTP login and recovery,
core local deployment targets, preflight, execution receipts, history, logs,
project-scoped Compose services, and write-only Compose environments,
project domains, active upstream health, and transactional TLS lifecycle,
local backup and restore workflows,
local and managed-node host maintenance, measured disk cleanup, managed-node
enrollment, inventory, server-observed connectivity, compatibility, desired
profile state, task creation, task history, and offline/capability conflicts.
This guide supplies payload and failure details that have not yet been promoted
endpoint by endpoint. Run
`make verify-api-docs` to detect drift.

## Auth Legend

| Label | Meaning |
|-------|---------|
| None | No authentication required |
| Auth | JWT required (`withAuth`) |
| Manager | JWT + role ≥ manager (`requireRole(RoleManager)`) |
| Admin | JWT + role = admin (`requireRole(RoleAdmin)`) |

---

## 1. Auth

### POST /api/auth/login
- **Auth**: None (rate-limited: 5 req/min per IP, ban after 10 failures)
- **Request**: Strict JSON `{"email":"admin@example.com","password":"..."}`, capped at 4096 bytes. Email is trimmed and limited to 254 bytes; password is limited to 128 bytes. Unknown fields, trailing JSON values, and oversized bodies return `400` or `413`.
- **Response**: Returns either a token plus credential-free user profile, or `{"requires_totp":true,"email":"admin@example.com"}` when a second factor is required. Invalid credentials return `401`; rate limits return `429`.
- **Session behavior**: Successful authentication returns the token for CLI clients and also sets the browser cookie `HttpOnly`, `SameSite=Strict`, with proxy-aware `Secure`. Authentication responses use `Cache-Control: no-store`.

### POST /api/auth/totp-verify
- **Auth**: None (shares the login failure limiter)
- **Request**: Strict JSON with `email`, `password`, and an exact six-digit `code`, capped at 4096 bytes.
- **Description**: Complete password-plus-TOTP login and return the same token, user, cookie, and no-store response contract as normal login.

### POST /api/auth/2fa/recovery
- **Auth**: None (shares the login failure limiter)
- **Request**: Strict JSON with `email`, `password`, and `recovery_code` in `AAAAA-BBBBB` hexadecimal form, capped at 4096 bytes. Lowercase input is normalized. The legacy field name `code` is not accepted.
- **Description**: Use one recovery code once for a TOTP-enabled account. Success consumes that code and returns the canonical authenticated session response; invalid credentials or recovery codes return `401`.

### POST /api/auth/logout
- **Auth**: None
- **Request**: No body.
- **Description**: Clear the auth cookie and return a no-store success response.

### GET /api/auth/me
- **Auth**: Auth
- **Description**: Return currently authenticated user profile.

### GET /api/auth/2fa/status
- **Auth**: Auth
- **Description**: Return `enabled` and `setup_pending` without exposing the TOTP secret. When recovery codes are available, only their remaining count is returned.

### POST /api/auth/2fa/setup
- **Auth**: Auth
- **Request**: No body.
- **Description**: Generate a TOTP secret, OTP Auth URL, base64 QR image, and exactly eight single-use recovery codes. These enrollment credentials are returned once in a no-store response. An already enabled account returns `409`.

### POST /api/auth/2fa/verify
- **Auth**: Auth
- **Request**: Strict `{"code":"123456"}` JSON, capped at 4096 bytes; the code must contain exactly six digits.
- **Description**: Verify the pending TOTP enrollment and activate 2FA on the account.

### POST /api/auth/2fa/disable
- **Auth**: Auth
- **Request**: Strict `{"code":"123456"}` JSON, capped at 4096 bytes; the code must contain exactly six digits.
- **Description**: Verify the active TOTP factor and disable 2FA for the current user.

---

## 2. Bootstrap

### GET /api/health
- **Auth**: None
- **Description**: Return `200 OK` only after the settings service is initialized. An uninitialized settings boundary returns `503 Service Unavailable`; it is never reported as a healthy panel.

### GET /api/onboarding
- **Auth**: Auth
- **Description**: Return the persisted first-run state as `completed` plus `step`, where step is always normalized to the inclusive range 0–5.

### POST /api/onboarding
- **Auth**: Admin
- **Request**: Strict JSON containing both `completed` and `step`, capped at 4096 bytes. Step must be an integer from 0 through 5; unknown fields, trailing values, missing fields, and out-of-range steps return `400`, while oversized bodies return `413`.
- **Description**: Persist one explicit first-run state transition and return the saved state.

---

## 3. System

### GET /api/system/stats
- **Auth**: Auth
- **Description**: Return real-time local CPU, RAM, swap, filesystem, three-value load average, uptime, hostname, OS, and network counters. `disk` and `network` are always arrays, including when empty.

### GET /api/system/services
- **Auth**: Auth
- **Description**: Return observed systemd state for the fixed local service inventory and the optional unprivileged PM2 unit selected by `HSERVER_PM2_USER`. Each item includes `name` and `status`, with optional PID, uptime, and degraded/failure detail.

### GET /api/system/services/{service}/logs
- **Auth**: Auth
- **Query**: Optional `lines` appears at most once, defaults to 100, and accepts 1–500. Empty, repeated, non-integer, or out-of-range values return `400 Bad Request`.
- **Description**: Return timestamp, systemd unit, journald priority, and message fields for one service in the same fixed local control allowlist. Arbitrary journal units are never accepted.

### GET /api/system/info
- **Auth**: Auth
- **Description**: Return observed OS, kernel, native `boot_id`, hostname, architecture, installed runtime versions, network interfaces, panel build metadata, and the optional build-injected `project_url`. PHP versions, network interfaces, and per-interface addresses are always arrays. Public CI builds derive the project URL from their own repository instead of hard-coding a maintainer fork. Provider-network acceptance compares this authenticated boot ID with the runner before claiming that a managed node uses a separate kernel.

### GET /api/system/actions/status
- **Auth**: Admin
- **Description**: Return the active bounded maintenance operation or `running: false`. Action values are the fixed host actions plus `disk-cleanup`.

### GET /api/system/actions/reboot-status
- **Auth**: Admin
- **Description**: Return the current delayed reboot state, optional scheduled timestamp, and remaining seconds without mutating the host.

### POST /api/system/actions/{fixed-action}
- **Auth**: Admin
- **Description**: Run one of `memory-optimize`, `swap-reset`, `temp-clean`, `reboot`, or `reboot-cancel` under the shared maintenance lock. Success returns a structured message; measured unsafe state or concurrent maintenance returns `409 Conflict`.

### POST /api/system/actions/service
- **Auth**: Admin
- **Body**: `{"service":"nginx","action":"restart"}` where `action` is exactly `start`, `stop`, or `restart` and `service` must belong to the panel's fixed local systemd allowlist.
- **Description**: Execute one bounded service action. Unknown JSON fields, arbitrary unit names, and unsupported actions return `400 Bad Request`.

### POST /api/system/actions/process
- **Auth**: Admin
- **Body**: `{"pid":1234,"startTime":987654,"signal":"term"}` where `signal` is exactly `term` or `kill`.
- **Description**: Signal the observed process identity only after both PID and start time still match. All three fields are required; unknown fields return `400`, a vanished process returns `404`, and a protected or changed process returns `409`.

### GET /api/disk/largest
- **Auth**: Admin
- **Query**: Required absolute `path` beneath an allowed local root and optional `limit`; limits outside 1–50 normalize to 20.
- **Description**: Recursively find the largest files on the selected filesystem without crossing mount boundaries. A missing `path` returns `400 Bad Request`; it never falls back to scanning `/`.

### GET /api/disk/overview
- **Auth**: Admin
- **Description**: Return mounted block devices, measured filesystem usage, I/O counters, and totals. Inventory walks the complete observed `lsblk` tree and preserves real device paths for whole disks, partitions, NVMe, encrypted, and LVM layouts; unmounted devices, loop/ROM devices, zram, and swap are not presented as mounted storage. An observation failure is an API error and is never normalized into an empty array or zero-capacity success.

### GET /api/disk/smart/{device}
- **Auth**: Admin
- **Path**: Use `root` to resolve the one physical disk behind the observed root filesystem, or provide an explicit validated Linux block-device basename.
- **Description**: Return SMART availability, definite health state, resolved device, optional model/serial data, and contextual message. Heyserver never defaults a missing device to `sda`; virtual, non-block, or multi-disk root storage returns `available: false` rather than choosing a device arbitrarily.

### GET /api/disk/cleanup/scan
- **Auth**: Admin
- **Description**: Measure the local fixed cleanup targets. Every result contains its opaque target ID, display metadata, byte size, filesystem scope, and risk; scanning does not delete data.

### POST /api/disk/cleanup/execute
- **Auth**: Admin
- **Request**: `{"targets":["journal","apt-cache"]}` with one to twenty unique IDs returned by the scan.
- **Description**: Execute only selected fixed targets under the maintenance lock and return per-target status/reclaimed bytes plus root free-space measurements before and after. Arbitrary paths and commands are not accepted.

### GET /api/system/update
- **Auth**: Auth
- **Description**: Read the optional provider-neutral release manifest and return `not_configured`, `unavailable`, or `healthy` without downloading or installing an update.
- **Response fields**: `current_version`, `latest_version`, `latest_version_state`, `update_available`, `platform`, optional checksum-bound `artifact`, optional `release_notes_url`, `message`, `checked_at`, and `signature_status` (`not_configured`, `verified`, or `unavailable`).
- **Configuration**: `HSERVER_UPDATE_MANIFEST_URL` plus optional `HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS`; a non-empty comma-separated Ed25519 trust set requires the adjacent detached `.sig`. Only stable `major.minor.patch` releases are ordered. Development and prerelease builds remain `unknown`.

### GET /api/system/update/stage
- **Auth**: Admin
- **Description**: Return the latest local verified-upgrade stage, or `{"stage":null}` when no release has been staged.
- **Response fields**: `stage.id`, `version`, `current_version`, `platform`, archive `sha256`, `size_bytes`, durable `status`, `status_detail`, `created_at`, and `updated_at`. Paths and runner internals are not exposed.
- **Recovery behavior**: For persisted `scheduled` or `running` states, the endpoint checks the panel-owned transient timer/service. A conclusively inactive job becomes `failed` with an interruption detail; an unavailable systemd query leaves the persisted state unchanged.

### POST /api/system/update/stage
- **Auth**: Admin
- **Request**: No body. Up to 4096 whitespace bytes are accepted; any other body returns `400 Bad Request` before release discovery runs.
- **Description**: Re-fetch release discovery server-side, download the newer host archive, verify size/SHA-256/tar paths/version/ELF architecture, and publish an idempotent local stage. The compressed archive is removed after verified extraction and bounded stage retention runs. This endpoint never installs or restarts Heyserver.
- **Response**: The verified stage. Returns `409` when no newer stable release exists and `503` when discovery or download is unavailable.

### POST /api/system/update/install
- **Auth**: Admin
- **Description**: Revalidate the fixed staged executables and schedule the packaged upgrade in a delayed transient systemd unit. The packaged installer snapshots current state, restarts Heyserver, health-checks the replacement, and automatically rolls back a failed health check.
- **Request**: `{"stage_id":"v1.3.0-0123456789ab","version":"v1.3.0","confirmed":true}`. All three fields are required. Unknown fields, trailing JSON, a missing confirmation, or a version/stage mismatch return `400`; bodies above 4096 bytes return `413 Payload Too Large`.
- **Response**: `202 Accepted` with stage status `scheduled`. Poll `GET /api/system/update/stage` through `running` to terminal `completed` or `failed`; temporary connection errors are expected while the panel restarts.

---

## 4. Domains

The generated OpenAPI contract publishes this complete local Domains surface.
Every domain identity is the normalized lowercase ASCII hostname itself, not a
database number or a silently rewritten approximation. Local mutations share
the authenticated mutation limiter.

### GET /api/domains
- **Auth**: Auth
- **Description**: List the complete observed `sites-available` inventory. Each
  item has a stable string `id` equal to its normalized hostname. `domains` is
  always an array; a missing or invalid configured inventory returns
  `503 Service Unavailable` rather than claiming there are no domains.

### GET /api/domains/provisioning
- **Auth**: Auth
- **Description**: Return installation-owned domain creation defaults and the optional DNS provider state. `dns.status` is `not_configured`, `unavailable`, or `healthy`; credentials are never returned.
- **Response fields**: Absolute `vhostsRoot`, `nginxSitesAvailable`, `nginxSitesEnabled`, and `nginxSnippetsDir` paths; invalid or relative paths are returned as empty so the UI can pause creation instead of displaying guessed host defaults. DNS fields are `dns.provider`, `dns.status`, optional `dns.origin` / `dns.recordType`, `dns.proxied`, and `dns.message`.

### GET /api/domains/{id}
- **Auth**: Auth
- **Path**: `id` is an exact normalized hostname such as `app.example.com`.
- **Description**: Read the observed domain, aliases, Nginx filename/content,
  optional PHP pool and certificate paths, and log paths. `serverNames` is
  always an array. Invalid identities return `400`, missing configurations
  return `404`, unconfigured installation paths return `503`, and another read
  failure remains an internal error rather than being mislabeled as missing.

### POST /api/domains/check
- **Auth**: Manager
- **Body**: Strict JSON `{"domain":"app.example.com"}`, capped at 64 KiB.
  Unknown fields, trailing JSON values, an empty domain, and oversized bodies
  return `400` or `413`.
- **Description**: Perform a read-only preflight for the candidate hostname,
  suggested root/port, parent domain, matching optional DNS zones, and observed
  conflicts. A syntactically invalid non-empty candidate returns `200` with
  `valid:false`; `dns_zones` and `conflicts` remain `[]`, never `null`.

### POST /api/domains
- **Auth**: Admin
- **Body boundary**: Strict JSON capped at 64 KiB. Unknown or trailing values
  return `400`; oversized bodies return `413`. Hostnames are trimmed and
  lowercased, but invalid characters are rejected instead of deleted. Supported
  PHP versions are `7.4` and `8.0` through `8.5`; supplied proxy/PM2 ports are
  bounded to `1–65535`, with zero meaning omitted.
- **Description**: Create a new Nginx domain and its requested runtime configuration. A request without `existingCertName` starts as a working HTTP site rather than generating an invalid HTTPS configuration that references missing certificate files. Set `createDnsRecord: true` to reconcile the domain's Cloudflare A/AAAA record against the explicitly configured installation origin.
- **Runtime options**: PHP domains accept `fpmPreset: "low" | "medium" | "high"`; an omitted value defaults to `medium`, while any other value is rejected with `400 Bad Request` before host mutation. The selected preset controls the generated pool process manager, worker limits, request recycling, memory, upload, and execution limits. Static domains accept `spaMode: true` to use `/index.html` as the Nginx fallback for client-side routes; the default remains a strict `404` fallback. Proxy domains accept `nodeEnv: "production" | "development"`; PM2 application and script fields must be supplied together, and a requested PM2 deployment defaults to `production` when `nodeEnv` is omitted.
- **Certificate flow**: `issueSSL: true` requires `sslEmail`. Heyserver first activates the local HTTP configuration and requested PHP/PM2 runtime. When `createDnsRecord` is also true, it then reconciles the configured Cloudflare address record before running the configured `HSERVER_CERTBOT_BIN` with `HSERVER_CERTBOT_CONFIG_DIR`. Certbot serves `/.well-known/acme-challenge/` from `HSERVER_ACME_WEBROOT`, and Heyserver promotes the site to HTTPS only after issuance succeeds. DNS reconciliation failure skips Certbot and keeps HTTP active; certificate issuance failure does the same. A failed HTTPS test or reload attempts to restore the HTTP configuration.
- **Configuration**: `HSERVER_NGINX_SITES_AVAILABLE`, `HSERVER_NGINX_SITES_ENABLED`, and `HSERVER_NGINX_SNIPPETS_DIR` bind domain inventory, mutations, and release-owned `hserver-*.conf` includes to installation-owned Nginx directories. `HSERVER_VHOSTS_ROOT` controls generated document-root defaults; an empty value keeps root-dependent domain operations `not_configured` and never selects a host-provider path. Invalid or relative installation paths return `503 Service Unavailable` instead of falling back to host defaults. Cloudflare DNS provisioning additionally requires `HSERVER_CF_API_TOKEN` and `HSERVER_DOMAIN_DNS_ORIGIN`; `HSERVER_DOMAIN_DNS_PROXIED` controls proxy mode.
- **Partial results**: If certificate issuance/activation or DNS provisioning fails after the HTTP domain becomes active, the endpoint returns `207 Multi-Status` with a combined `warning`; it does not report the already active domain as a total failure.
- **Conflict and availability**: An existing exact Nginx configuration returns
  `409 Conflict` and remains byte-for-byte untouched. Missing required local or
  optional provider configuration returns `503`; the shared mutation limiter
  may return `429`. A failure from a later PHP/PM2 step is not advertised as
  transactional—inspect the returned error and observed domain state.
- **Monitoring**: The automatically created uptime monitor uses HTTPS only when HTTPS activation succeeded; HTTP-only and partial-result domains are monitored over HTTP without a TLS expiry check.

### DELETE /api/domains/{id}
- **Auth**: Admin
- **Request**: The body must be empty. The only query is optional
  `deleteFiles=true|false`; it must appear once with a literal boolean value.
  Unknown, repeated, or empty query values return `400`.
- **Description**: Delete one exact observed domain configuration and matching
  PHP pool. A missing domain returns `404` and is never reported as deleted.
  With `deleteFiles=true`, only the domain's root-confined site tree is removed;
  the default nested subdomain layout remains under its configured parent
  directory. Uptime cleanup matches the exact monitor hostname or URL hostname,
  never a containing suffix. Success returns the deleted hostname in a typed
  receipt; the shared mutation limiter may return `429`.

### POST /api/domains/{id}/toggle
- **Auth**: Manager
- **Body**: Exactly `{"active":true}` or `{"active":false}`, capped at 4 KiB.
- **Description**: Idempotently apply the explicit desired domain state to an
  exact observed hostname. A missing `active` field, invalid hostname, unknown
  field, trailing JSON value, or oversized body returns `400` or `413`; a
  missing configuration returns `404`, unconfigured paths return `503`, and the
  shared mutation limiter may return `429`. An empty body can never be
  interpreted as a request to disable the domain.

---

## 5. Nginx

### GET /api/nginx/status
- **Auth**: Auth
- **Description**: Return structured nginx readiness without conflating a missing executable, an inactive unit, and an unavailable systemd probe.
- **Response fields**: `installed`, `status`, `statusAvailable`, `version`, `uptime`, and `configTest` (`ok`, `output`).

### GET /api/nginx/configs
- **Auth**: Auth
- **Description**: List site config files from the installation-owned `HSERVER_NGINX_SITES_AVAILABLE` directory and report enabled state from `HSERVER_NGINX_SITES_ENABLED`. Invalid relative installation paths fail closed instead of falling back to host defaults.
- **Configuration failures**: Inventory, read, save, create, toggle, and snippet handlers return `503 Service Unavailable` when installation-owned Nginx paths are missing or invalid; an unconfigured installation is not reported as an internal server error or missing site.

### GET /api/nginx/configs/{filename}
- **Auth**: Auth
- **Description**: Read one observed regular, non-symlink Nginx site configuration. The response binds the canonical `filename`, domain/type metadata, enabled state, raw `content`, lowercase SHA-256 `checksum`, byte `size`, and `modifiedAt` timestamp so a later replacement can detect concurrent changes.
- **Limits**: The configuration must be NUL-free UTF-8 text no larger than 2 MiB. Oversized files return `413 Payload Too Large`; missing or invalid installation-owned Nginx paths return `503 Service Unavailable`.

### PUT /api/nginx/configs/{filename}
- **Auth**: Manager
- **Body**: Exactly `{"content":"...","checksum":"CURRENT_SHA256"}`. Unknown fields, trailing JSON, empty content, invalid UTF-8, NUL bytes, and malformed checksums return `400 Bad Request`; content above 2 MiB returns `413 Payload Too Large`.
- **Description**: Replace the selected regular, non-symlink configuration only when `checksum` still matches the latest observed file. Heyserver creates a same-directory timestamped recovery backup, repeats the checksum check, atomically installs the candidate while preserving mode and ownership, and validates the complete configuration with `nginx -t`.
- **Conflict and rollback**: A concurrent change returns `409 Conflict` without overwriting it. If `nginx -t` rejects the candidate, Heyserver restores the previous file and returns `422 Unprocessable Entity`. Success returns `message`, the portable retained `backup` identity, and the replacement `checksum`; absolute host paths are not part of the receipt.
- **Reload boundary**: Saving never reloads Nginx. Call `POST /api/nginx/reload` separately after inspecting the validated save receipt.

### POST /api/nginx/configs
- **Auth**: Manager
- **Body**: A strict object with `domain`, `type` (`php`, `static`, `proxy`, or `redirect`), optional type-specific settings, and optional explicit TLS fields. Unknown fields and trailing JSON return `400 Bad Request`. Proxy sites require `proxyPass`; redirect sites require `redirectTo`; PHP runtime and pool identities are allowlisted. Custom `certPath` and `keyPath` must be safe absolute paths supplied together with `useSSL:true`.
- **Description**: Create a new provider-neutral site config exclusively in the configured available-site directory. When PHP or static requests omit `docRoot`, Heyserver derives it as `<HSERVER_VHOSTS_ROOT>/<domain>/httpdocs` only after an absolute `HSERVER_VHOSTS_ROOT` is configured; an empty root returns `503 Service Unavailable` rather than selecting a provider-specific document root. `useSSL:false` generates a real HTTP-only site without an unusable port-443 listener. TLS listeners and the HTTP-to-HTTPS redirect are generated only when explicitly requested.
- **Validation and recovery**: Heyserver temporarily exposes the new disabled site to the complete `nginx -t` validation boundary. A rejected candidate is removed and returns `422 Unprocessable Entity`; a concurrently existing domain returns `409 Conflict`. Success returns the canonical content, enabled state, checksum, size, and modification timestamp. Creation never enables the site or reloads Nginx.

### DELETE /api/nginx/configs/{filename}
- **Auth**: Manager
- **Body**: Exactly `{"checksum":"CURRENT_SHA256"}`. Unknown fields, trailing JSON, and malformed checksums return `400 Bad Request`.
- **Description**: Archive one disabled regular, non-symlink site configuration. Heyserver refuses enabled sites, creates an exclusive same-directory recovery copy, repeats the checksum comparison, removes only the configuration file from active inventory, and validates the complete Nginx configuration.
- **Conflict and rollback**: Enabled sites and concurrently changed checksums return `409 Conflict`. If `nginx -t` rejects the archived state, Heyserver restores the original file and returns `422 Unprocessable Entity`. Success returns the portable retained `archive` identity and exact archived `checksum`; absolute host paths are not part of the receipt.
- **Data and reload boundary**: Archival never deletes the site's document root, application files, certificates, logs, or database, and never reloads Nginx. Disable the site first, inspect the receipt, then reload separately when required.

### GET /api/nginx/archives
- **Auth**: Auth
- **Description**: List validated regular Heyserver recovery copies retained below the installation-owned available-site directory. Each entry contains a portable `archive` identity, target `filename`, lowercase SHA-256 `checksum`, byte `size`, encoded `archivedAt`, and filesystem `modifiedAt`; archive contents and absolute host paths are not exposed.
- **Failure boundary**: Unrelated files and malformed archive names are omitted. A matching Heyserver archive that is a symlink, unreadable, invalid UTF-8, or larger than 2 MiB makes the inventory fail instead of being presented as a healthy empty list.

### POST /api/nginx/archives/{archive}/restore
- **Auth**: Manager
- **Body**: Exactly `{"checksum":"OBSERVED_ARCHIVE_SHA256"}`. Unknown fields, trailing JSON, malformed archive identities, and malformed checksums return `400 Bad Request`.
- **Description**: Restore one missing local config from the exact observed archive. Heyserver refuses to overwrite an existing config or activate a dangling enabled-site entry, repeats the archive checksum comparison, preserves archive mode and ownership, creates the config exclusively, temporarily includes the disabled candidate in full `nginx -t`, and leaves the restored site disabled.
- **Conflict and recovery**: Existing targets, enabled targets, and stale checksums return `409 Conflict`. A syntax failure returns `422 Unprocessable Entity` after removing the restored candidate. The archive is retained after both success and validation failure, and Nginx is never reloaded automatically.

### GET /api/nginx/backups
- **Auth**: Auth
- **Description**: List validated regular Heyserver pre-edit backups retained below the installation-owned available-site directory. Each entry contains the portable `backup` identity, target `filename`, lowercase SHA-256 `checksum`, byte `size`, encoded `createdAt`, and filesystem `modifiedAt`; content and absolute paths are not exposed.
- **Failure boundary**: Unrelated files and malformed backup names are omitted. A matching Heyserver backup that is a symlink, unreadable, invalid UTF-8, or larger than 2 MiB makes the inventory fail rather than silently disappearing from a healthy response.

### POST /api/nginx/backups/{backup}/restore
- **Auth**: Manager
- **Body**: Exactly `{"backupChecksum":"OBSERVED_BACKUP_SHA256","currentChecksum":"FRESH_CURRENT_CONFIG_SHA256"}`. Both locks are required; unknown fields, trailing JSON, malformed backup identities, and malformed checksums return `400 Bad Request`.
- **Description**: Roll an existing config back to a selected pre-edit version without permitting a blind overwrite. Heyserver reopens both files, compares both checksums, preserves the current target's mode and ownership, retains a fresh pre-restore recovery copy, atomically installs the backup content, validates the complete `nginx -t`, and preserves the site's enabled or disabled state.
- **Conflict and rollback**: A changed backup or current target returns `409 Conflict`. A rejected candidate returns `422 Unprocessable Entity` after the previous current config is restored. The selected backup and new recovery remain retained, and Nginx is never reloaded automatically.

### PUT /api/nginx/configs/{filename}/state
- **Auth**: Manager
- **Body**: Exactly `{"enabled":true}` or `{"enabled":false}`. Missing `enabled`, unknown fields, and trailing JSON return `400 Bad Request`.
- **Description**: Canonical explicit, idempotent desired-state operation for the selected site. Retrying the same request preserves the requested state rather than flipping it.
- **Filesystem boundary**: Heyserver only creates or removes a symlink that resolves exactly to the selected regular file in the installation-owned available-site directory. A regular enabled-site entry or a symlink to a foreign target is rejected and never replaced or removed. A missing available-site config returns `404 Not Found`.
- **Reload boundary**: State changes do not reload Nginx; validate and reload separately.

### POST /api/nginx/configs/{filename}/toggle
- **Auth**: Manager
- **Deprecated alias**: Backwards-compatible alias for `PUT /api/nginx/configs/{filename}/state`. It requires the same exact desired-state body and never performs an implicit flip. New clients must use the canonical `/state` route.

### POST /api/nginx/test
- **Auth**: Auth
- **Description**: Run `nginx -t` to validate configuration syntax. Returns test output.

### POST /api/nginx/reload
- **Auth**: Manager
- **Description**: Reload nginx (`systemctl reload nginx`) after config changes.

### GET /api/nginx/snippets
- **Auth**: Auth
- **Description**: List read-only `.conf` snippets from the `snippets` directory adjacent to the configured available-site directory.

---

## 6. SSL

### GET /api/ssl/status
- **Auth**: Auth
- **Description**: Return Certbot readiness without exposing credential paths or values. `state` is `healthy`, `certbot-missing`, or `unavailable`. The response reports version, plugin-inventory availability, nginx/Cloudflare authenticator booleans, and whether the installation-owned DNS credential file is configured and readable.

### GET /api/ssl/certificates
- **Auth**: Auth
- **Description**: List readable certificate files under `/etc/letsencrypt/live` with expiry dates. This inventory remains independent from Certbot command readiness.

### GET /api/ssl/certificates/{domain}
- **Auth**: Auth
- **Description**: Get certificate details for a specific domain (issuer, expiry, SANs).

### POST /api/ssl/renew/{domain}
- **Auth**: Manager
- **Description**: Trigger certbot renewal for a specific domain certificate.

### POST /api/ssl/issue
- **Auth**: Admin
- **Description**: Issue a new Let's Encrypt certificate after server-side Certbot and authenticator preflight. `challengeType` must be `http-01` or `dns-01`. DNS-01 uses only `HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS`; the browser cannot supply a credential path.

---

## 7. PHP

### GET /api/php/versions
- **Auth**: Auth
- **Description**: List installed PHP versions (7.4, 8.4, 8.5, etc.) with FPM status.

### POST /api/php/versions/{version}/actions/{action}
- **Auth**: Manager
- **Actions**: `test`, `reload`, or `restart` only.
- **Description**: Validate or control one installed local PHP-FPM version. Reload and restart always run the version-matched `php-fpmMAJOR.MINOR -t` validator before calling systemd. Invalid configuration returns `422` without calling systemd; a validated systemd reload/restart failure returns `502`. Successful and failed attempts are written to the local host audit log.

### POST /api/php/versions/{version}/restart
- **Auth**: Manager
- **Deprecated**: Yes.
- **Description**: Backwards-compatible alias for the validated `restart` action. New integrations should use `/api/php/versions/{version}/actions/restart`. It shares the same validation, audit, `422`, and `502` behavior as the canonical action route.

### POST /api/php/restart/{version}
- **Auth**: Manager
- **Deprecated**: Yes.
- **Description**: Original backwards-compatible restart route retained for existing integrations. It now shares the canonical action handler, including version validation, pre-restart configuration testing, structured failures, and audit records. New integrations should use `/api/php/versions/{version}/actions/restart`.

### GET /api/php/pools
- **Auth**: Auth
- **Description**: List all PHP-FPM pool configurations across all PHP versions.

### GET /api/php/pools/{version}/{pool}
- **Auth**: Auth
- **Description**: Get the raw configuration content of a specific PHP-FPM pool.

### GET /api/php/pools/{version}/{pool}/config
- **Auth**: Auth
- **Description**: Return the exact observed regular local pool file with its canonical path, content, lowercase SHA-256 checksum, byte size, mode, and modification time. Symlinks, non-regular files, invalid UTF-8/control content, and files larger than 2 MiB are rejected rather than followed or truncated.

### PUT /api/php/pools/{version}/{pool}/config
- **Auth**: Manager
- **Body**: `{"content":"...","checksum":"<64 lowercase hex>","reload":false}`. Unknown fields and trailing JSON are rejected.
- **Description**: Reopen the exact observed regular file without following symlinks, verify the checksum, create a same-directory ownership/mode-preserving backup, atomically replace the file, and run the version-matched `php-fpmMAJOR.MINOR -t` validator. Validation or requested reload failure restores the previous file. A changed checksum returns `409`, an invalid candidate returns `422`, an oversized body returns `413`, and a reload failure after validation returns `502`. Success returns the backup path, replacement checksum, and reload state.

### PUT /api/php/pools/{version}/{pool}
- **Auth**: Manager
- **Description**: Save changes to a PHP-FPM pool configuration file.

### POST /api/php/pools
- **Auth**: Manager
- **Description**: Create a new PHP-FPM pool configuration. Generated `open_basedir` defaults use the installation's `HSERVER_VHOSTS_ROOT`; an empty, unavailable, or relative configured root fails closed instead of selecting another filesystem layout.

### GET /api/php/presets
- **Auth**: Auth
- **Description**: List the provider-neutral `low`, `medium`, `high`, `wordpress`, and `laravel` pool presets. Installation- or operator-specific presets are not part of the public distribution.

### DELETE /api/php/pools/{version}/{pool}
- **Auth**: Admin
- **Description**: Delete a PHP-FPM pool configuration file.

### POST /api/php/composer/{version}/install
- **Auth**: Manager
- **Body**: `{"project_dir":"<absolute existing project directory>"}`
- **Description**: Run a non-interactive Composer install as the project directory owner. The project must remain inside `HSERVER_VHOSTS_ROOT` before and after symlink resolution; caller-correctable path failures return `400 Bad Request`.

### POST /api/php/composer/{version}/update
- **Auth**: Admin
- **Body**: `{"project_dir":"<absolute existing project directory>"}`
- **Description**: Run a non-interactive Composer update under the same installation-owned project boundary.

### POST /api/php/composer/{version}/require
- **Auth**: Admin
- **Body**: `{"project_dir":"<absolute existing project directory>","package":"vendor/package[:constraint]"}`
- **Description**: Add one validated Composer package without accepting shell syntax; the project path uses the same installation-owned boundary.

### POST /api/php/composer/{version}/outdated
- **Auth**: Manager
- **Body**: `{"project_dir":"<absolute existing project directory>"}`
- **Description**: Return Composer's structured outdated-package inventory without modifying the project; the project path uses the same installation-owned boundary.

---

## 8. PM2

The generated OpenAPI contract publishes the complete local PM2 surface,
including strict process objects, bounded log queries, lifecycle/deployment/save
receipts, and the distinction between empty inventory, failed observation, and
an unconfigured PM2 owner.

### GET /api/pm2/processes
- **Auth**: Auth
- **Description**: List all PM2 processes with status, CPU, memory, and uptime.

### GET /api/pm2/processes/{id}
- **Auth**: Auth
- **Description**: Get detailed info for a specific PM2 process by ID or name.

### POST /api/pm2/processes/{id}/{action}
- **Auth**: Manager
- **Description**: Control a PM2 process under the configured unprivileged owner. Actions: `start`, `stop`, `restart`, `reload`, and `delete`. Delete stops and removes the process from the active PM2 list but does not remove application files or implicitly run `pm2 save`.

### GET /api/pm2/processes/{id}/logs
- **Auth**: Auth
- **Description**: Fetch recent log output (stdout/stderr) for a PM2 process.
  Optional `lines` defaults to `100` and accepts `1`–`5000`; invalid, zero, and
  oversized values return `400 Bad Request` instead of silently using the
  default.

### POST /api/pm2/deploy
- **Auth**: Admin
- **Description**: Start a new script under the explicitly configured unprivileged PM2 owner. This endpoint does not clone a repository or run `git pull`.
- **Body**: `{"name":"api","script":"/srv/hserver/sites/example.com/server.js","cwd":"/srv/hserver/sites/example.com","instances":1,"exec_mode":"fork","node_env":"production"}`. `name` and `script` are required. `exec_mode` defaults to `fork`; `instances` defaults to `1` and accepts `1`–`64`. Optional `node_env` accepts only `production` or `development` and is passed to the new process as `NODE_ENV`.
- **Boundaries**: Application names use the fixed portable name pattern documented in OpenAPI. Script and optional working-directory paths must resolve below a segment-safe absolute root from `HSERVER_PM2_ALLOWED_ROOTS`; relative roots and `/` fail closed. When the variable is empty, deployment returns `503 Service Unavailable` with the PM2 integration `not_configured` instead of falling back to `HSERVER_VHOSTS_ROOT`, `/home`, or `/opt`; set the allowlist explicitly for each installation. Unknown JSON fields, including the formerly ignored `env` field, and trailing JSON values are rejected.

### POST /api/pm2/save
- **Auth**: Manager
- **Description**: Run `pm2 save` to persist current process list across reboots.

Managed-node PM2 actions use the separate agent route and accept `start`,
`stop`, `restart`, or `reload` only when the node advertises PM2 action
capability and the same action is enabled in that agent's local allowlist.

---

## 9. Databases

### GET /api/databases
- **Auth**: Auth
- **Query**: Optional `engine=postgres|postgresql|mariadb|mysql`.
- **Description**: List databases for the requested engine and return its local source readiness. Each `sources` entry includes `available`, a stable `state` (`healthy`, `client-missing`, `stopped`, `authentication-failed`, or `unavailable`), and the preserved `error` when detection fails. A failed source is not an empty healthy inventory; database arrays are never `null`.

```json
{
  "databases": [],
  "sources": {
    "postgresql": {
      "available": false,
      "state": "stopped",
      "error": "could not connect to server: Connection refused"
    }
  }
}
```

### POST /api/databases
- **Auth**: Admin
- **Body**: `{"engine":"postgres","name":"portal","owner":"portal_user"}`. `owner` is optional and used only by PostgreSQL.
- **Description**: Create one local PostgreSQL or MariaDB database. Database and owner identities accept 1–64 alphanumeric, underscore, or hyphen characters. Unknown fields, trailing JSON, and bodies above 16 KiB are rejected.

### DELETE /api/databases/{engine}/{name}
- **Auth**: Admin
- **Body**: `{"confirm":"DROP portal"}` using the exact selected path name.
- **Description**: Permanently drop one local database after literal confirmation. The engine and database identity are revalidated; unknown fields, trailing JSON, and bodies above 16 KiB are rejected.

### GET /api/databases/{engine}/{name}/tables
- **Auth**: Auth
- **Description**: List table names, schemas, estimated rows, sizes, and table types for one bounded database identity. Accepted engine aliases are normalized to `postgres` or `mariadb` in the response.

### POST /api/databases/{engine}/{name}/query
- **Auth**: Manager
- **Body**: `{"query":"SELECT version();","write_mode":false}`. `write_mode` is an optional compatibility field and must remain `false`.
- **Description**: Execute one NUL-free UTF-8 `SELECT` or `WITH` statement of at most 64 KiB inside an engine-enforced read-only transaction. File-access functions and write/DDL keywords are rejected. Unknown fields, trailing JSON, and request bodies above 128 KiB are rejected; result `columns` and `rows` are never `null`. Use the separately authenticated writable terminal for database mutations.

### GET /api/databases/users
- **Auth**: Auth
- **Query**: Optional `engine=postgres|postgresql|mariadb|mysql`.
- **Description**: List database users for the requested engine. The response uses the same structured `sources` readiness contract as `GET /api/databases`, and its user array is never `null`.

### GET /api/databases/credentials
- **Auth**: Admin
- **Description**: List legacy `pgm_metadata` credential sets. This intentionally secret-bearing administrator response includes database passwords and optional connection strings, is never cached (`Cache-Control: no-store`), and returns `[]` when healthy but empty.

### GET /api/databases/credentials/{name}
- **Auth**: Admin
- **Description**: Get one exact, bounded `pgm_metadata` credential. The response is intentionally secret-bearing and uses `Cache-Control: no-store`; invalid names return `400`, missing credentials return `404`, and credential-store failures are not misreported as absence.

### GET /api/databases/pgm-credentials
- **Auth**: Admin
- **Description**: Compatibility alias for `GET /api/databases/credentials` with the same administrator-only, secret-bearing, no-store contract.

### GET /api/databases/pgm-backups
- **Auth**: Auth
- **Description**: List actionable backup directories below the configured `HSERVER_PGM_BACKUP_DIR` newest first. Invalid directory identities are omitted; each item contains its configured restore path, human size, SQL-file count, and parsed timestamp. Healthy empty inventory is `[]`; a missing, inaccessible, or non-directory configured root returns `503 Service Unavailable` instead of an invented empty inventory or `500`.

### GET /api/databases/pgm-backup-files/{name}
- **Auth**: Auth
- **Description**: List sorted `.sql` and `.sql.gz` basenames inside one 1–128 character backup directory identity. Invalid identities return `400`, a missing directory below an available root returns `404`, an unavailable configured root returns `503`, and healthy empty inventory is `[]`.

### POST /api/databases/pgm-restore
- **Auth**: Admin
- **Body**: `{"database":"portal","backupPath":"/var/lib/hserver/pgm-backups/20260828_030000/portal.sql.gz"}` using an exact path returned by the backup inventory.
- **Description**: Restore one existing PostgreSQL database from a regular `.sql` or `.sql.gz` file inside the configured backup root. Lexical escapes, symlink files, resolved-path escapes, unsupported suffixes, paths above 4096 characters, unknown/trailing JSON, and bodies above 16 KiB are rejected. Missing files below an available root return `404`, an unavailable configured root returns `503`, and restore-command failures remain server errors.

---

## 10. Backups

### GET /api/backups
- **Auth**: Auth
- **Description**: List all system backups with metadata (date, size, type).

### GET /api/backups/targets
- **Auth**: Admin
- **Description**: Discover portable direct website-folder identities observed below the installation-owned vhost root for selective file-bearing backups. The response includes `vhosts`, the `maxSelectedVhosts` limit (`16`), and `emptySelection=all-configured-vhosts`; it never returns the configured root path and omits files, symlinks, and unsupported names. An unavailable or invalid configured root returns `503 Service Unavailable` rather than an invented or empty healthy inventory.

### POST /api/backups
- **Auth**: Admin
- **Body**: A closed object with `type` (`full`, `database`, or `files`) plus optional name, database engine/name, compression, retention, and `vhosts`. `vhosts` accepts at most `16` unique portable site folder identities previously observed below the configured vhost root. Caller-selected filesystem roots such as `filesRoot` are rejected.
- **Description**: Start a durable asynchronous local backup. File-bearing backups read only the installation-owned `HSERVER_VHOSTS_ROOT`; an invalid root, path-like selector, duplicate selector, missing site, or database-only request containing vhosts returns `400 Bad Request` before a job starts.

### DELETE /api/backups/{id}
- **Auth**: Admin
- **Description**: Delete a specific backup by ID.

### GET /api/backups/restore/{id}/validate
- **Auth**: Admin
- **Description**: Fully read and validate a local restore artifact without mutating files or databases. Returns the database target, included restore stages, automatic database recovery availability, and automatic file rollback availability. Valid files and full artifacts report `filesRollback=true`; validation itself does not create a recovery archive. Database connectivity is verified later when the restore creates its recovery point.

### POST /api/backups/restore/{id}
- **Auth**: Admin
- **Description**: Start a background restore from a completed local backup. Database and full restores create a pre-mutation database recovery point and automatically roll back the database on failure. Files and full restores archive every existing path that will be overwritten into a completed `pre-restore-...-files.tar.gz` backup before extraction. If extraction fails, Heyserver restores those paths and removes paths created by the failed restore. The recovery archive remains listed by `GET /api/backups` after success and can be restored manually when an operator needs to reverse overwritten file content.

### GET /api/backups/schedules
- **Auth**: Admin
- **Description**: List backup schedules owned by Heyserver in the panel service user's crontab. Daily, weekly, and first-day monthly presets include `frequency` plus `time`; custom safe cron expressions expose their exact `cron` without a misleading preset label. `retention_count` is the number of newest matching backup artifacts preserved by pruning. The deprecated `retention_days` response alias contains the same count for backwards compatibility and does not represent elapsed days. A missing crontab is an empty successful result; a missing executable, permission failure, timeout, or other unreadable state returns `503 Service Unavailable` rather than a false empty schedule.

### POST /api/backups/schedules
- **Auth**: Admin
- **Body**: Use exactly one schedule source: either `{"frequency":"daily|weekly|monthly","time":"HH:MM"}` or `{"cron":"<safe five-field expression>"}`. The optional `type` is `full`, `database`, `files`, or `snapshot`; `database` is optional metadata for database schedules. `retention_count` defaults to `10` and accepts integers from `1` through `365`.
- **Compatibility**: `retentionCount` and the deprecated `retention_days` name remain accepted as count aliases, but a request must provide at most one of the three retention names. Unknown fields, trailing JSON, mixed cron plus frequency/time sources, and ambiguous retention aliases return `400 Bad Request`.
- **Description**: Add or replace a managed backup schedule while preserving unrelated crontab entries. Heyserver reads the complete current crontab before writing and returns `503 Service Unavailable` without changing the runner script or crontab when observation fails.
- **Validation**: The cron expression must use the safe five-field format and unsupported database metadata is rejected. Invalid input returns `400 Bad Request` before any host file or crontab is observed or changed.

### DELETE /api/backups/schedules
- **Auth**: Admin
- **Body**: `{"rawLine":"<exact rawLine returned by GET /api/backups/schedules>"}`. The body is required and rejects unknown fields or trailing JSON.
- **Description**: Remove an exact, currently observed Heyserver-managed backup schedule while preserving every unrelated crontab entry. The server no longer guesses the first schedule when identity is omitted. A non-managed target returns `400 Bad Request`, a valid target that is no longer present returns `404 Not Found`, and the mutation is refused with `503 Service Unavailable` when the current crontab cannot be observed safely.

### PUT /api/backups/gdrive/oauth-app
- **Auth**: Admin
- **Body**: A closed object containing `clientId` plus an optional write-only `clientSecret`, or `gcpProjectId` alone when only the setup-link project hint changes. Omitting `clientSecret` preserves an existing panel-managed secret. Client ID, secret, and project values are bounded to `512`, `4096`, and `255` characters respectively.
- **Description**: Store operator-supplied OAuth application metadata for the optional Google Drive adapter. Environment-provided credentials retain precedence and secrets are never returned by the read API.

### POST /api/backups/gdrive/oauth/start
- **Auth**: Admin
- **Body**: None. A non-empty body returns `400 Bad Request`.
- **Description**: Create a 10-minute hexadecimal OAuth state bound to the initiating admin and return the Google authorization URL.

### POST /api/backups/gdrive/oauth/complete
- **Auth**: Admin
- **Body**: `{"state":"<exact 32-character hexadecimal pending state>"}`. Unknown fields, malformed identities, and trailing JSON are rejected.
- **Description**: Exchange the pending authorization code only for the same authenticated admin who started the flow. Expired, unknown, reused, or cross-user state is rejected.

### POST /api/backups/gdrive/disconnect
- **Auth**: Admin
- **Body**: None. A non-empty body returns `400 Bad Request`.
- **Description**: Remove the local OAuth token and generated rclone profile without deleting local backups or remote Drive objects.

### PUT /api/backups/gdrive/settings
- **Auth**: Admin
- **Body**: Complete replacement object with required `folder`, `autoUpload`, `remoteRetentionDays`, `notifyOnSuccess`, and `notifyOnFailure`. The Drive folder is a safe relative path of at most `255` characters. Retention accepts `0–365`; zero explicitly disables age-based remote deletion.
- **Description**: Replace only the operator-controlled Drive policy while preserving server-owned `lastUploadAt`, `lastUploadFile`, and `lastError` observations. Unknown, omitted, unsafe, or out-of-range values return `400 Bad Request` without changing the policy.

### POST /api/backups/gdrive/test
- **Auth**: Admin
- **Body**: None. A non-empty body returns `400 Bad Request`.
- **Description**: Verify the current OAuth grant, rclone dependency, and configured installation-owned Drive folder. A configured URL alone is not reported as a successful connection.

### POST /api/backups/gdrive/dismiss-error
- **Auth**: Admin
- **Body**: None. A non-empty body returns `400 Bad Request`.
- **Description**: Clear only the persisted last-error observation; it does not reconnect OAuth or claim that the underlying provider is healthy.

### POST /api/backups/gdrive/restore
- **Auth**: Admin
- **Body**: `{"fileName":"<exact observed .tar.gz or .sql.gz filename>"}`. The filename is a portable basename of at most `255` characters; directories, traversal, whitespace rewriting, unsupported extensions, unknown fields, and trailing JSON are rejected.
- **Description**: Start a tracked download into the installation-owned local backup directory. The client cannot choose an arbitrary local destination or raw rclone argument.

### GET /api/backups/snapshot/status
- **Auth**: Admin
- **Description**: Report local restic availability, installation-owned encryption readiness, Drive connectivity, repository state, persisted retention settings, manifest paths, recent snapshots, and repository statistics. A missing `snapshot-settings.json` uses documented defaults. An unreadable or malformed settings file returns `503 Service Unavailable` rather than presenting default settings as observed state.

### GET /api/backups/snapshot/settings
- **Auth**: Admin
- **Description**: Read the persisted snapshot repository, retention, password-acknowledgement, enabled-path policy, and server-owned last-run result fields. A missing policy file returns installation defaults; an unreadable or malformed policy returns `503 Service Unavailable`.

### PUT /api/backups/snapshot/settings
- **Auth**: Admin
- **Body**: Complete replacement object with required `repoFolder`, `enabledPaths`, `keepDaily`, `keepWeekly`, `keepMonthly`, and `passwordAcknowledged` fields. Manifest IDs come from the fixed snapshot manifest. Retention limits are daily `1–365`, weekly `1–260`, and monthly `1–120`.
- **Description**: Atomically replace the operator-controlled snapshot policy while preserving server-owned last-run, last-snapshot, and last-error fields. Unknown or omitted fields, duplicate or unknown manifest IDs, unsafe repository paths, and out-of-range retention return `400 Bad Request` without replacing the current policy. An unreadable or malformed existing policy returns `503 Service Unavailable`.

### POST /api/backups/snapshot/run
- **Auth**: Admin
- **Body**: None. A non-empty body returns `400 Bad Request`; the operation runs only the persisted installation-owned policy.
- **Description**: Start one incremental restic snapshot after settings, encryption, local capacity, and remote repository readiness checks.

### GET /api/backups/snapshot/list
- **Auth**: Admin
- **Description**: List observed restic snapshots from the configured encrypted repository.

### POST /api/backups/snapshot/restore
- **Auth**: Admin
- **Body**: `snapshotId` is a required `8–64` character hexadecimal identity observed from the snapshot list. Optional `manifestIds` selects fixed logical manifest entries; optional `vhosts` accepts at most `16` unique portable vhost directory names. Heyserver resolves both selectors against the installation's configured data and vhost roots. Selecting the complete `vhosts` manifest together with individual `vhosts` is rejected as ambiguous. Absolute client paths, arbitrary restic arguments, glob expressions, unknown manifest IDs, and caller-selected restore targets are rejected.
- **Description**: Restore into Heyserver's fixed local staging directory. The API cannot target production paths directly; moving staged content into service paths remains a separate operator action.

### POST /api/backups/snapshot/purge-repo
- **Auth**: Admin
- **Body**: `repoFolder` must exactly match the currently observed repository path from snapshot settings, and `confirmation` must be the fixed literal `purge-snapshot-repository`. Unknown or trailing fields are rejected.
- **Description**: Permanently delete only the encrypted snapshot repository selected by the installation-owned persisted policy; the client cannot select a remote or arbitrary path. A stale repository identity or incorrect confirmation returns `400 Bad Request`, an active snapshot or restore returns `409 Conflict`, and malformed settings or unavailable Google Drive returns `503 Service Unavailable` without starting remote deletion.

---

## 11. Firewall

The generated OpenAPI contract publishes the complete local Firewall surface,
including strict readiness/rule objects, mutation receipts, positive rule
identities, SSH-lockout denial, and explicit UFW desired state. Both status and
rules endpoints return `[]`, never `null`, when no rules are observable.

### GET /api/firewall/status
- **Auth**: Auth
- **Description**: Return UFW readiness, active state, default policies, and rules. `available` is true only when the local UFW inventory is manageable. `state` is `healthy`, `ufw-missing`, or `unavailable`; `backend` is `ufw`, read-only `iptables`, or `none`. When UFW is missing, observable iptables rules may be returned without enabling any UFW mutation.

### GET /api/firewall/rules
- **Auth**: Auth
- **Description**: List all active firewall rules with ports, protocols, and actions.

### POST /api/firewall/rules
- **Auth**: Admin
- **Description**: Add a new firewall rule (port, protocol, source IP, action).
- **Body**: `{"action":"allow","direction":"in","protocol":"tcp","port":"443","from":"203.0.113.0/24","to":"any","comment":"HTTPS access"}`. Only `action` is required; supported actions are `allow`, `deny`, `reject`, and `limit`. Optional `direction` is `in` or `out`, and optional `protocol` is `tcp`, `udp`, or `any`.
- **Contract**: The source field is named `from`, matching the UFW rule model. Unknown JSON fields and trailing JSON values are rejected rather than ignored.

### DELETE /api/firewall/rules/{number}
- **Auth**: Admin
- **Description**: Delete a firewall rule by positive integer rule number. Heyserver rejects deletion of the last inbound SSH allow rule.

### POST /api/firewall/toggle
- **Auth**: Admin
- **Description**: Enable or disable UFW with explicit request intent. The JSON body must contain `{"enable": true}` or `{"enable": false}`; omission is rejected.
- **Contract**: Unknown JSON fields and trailing JSON values are rejected.

---

## 12. Files

### GET /api/files
- **Auth**: Auth
- **Query**: Optional `path`. When omitted, the response returns the configured `roots` and an empty `entries` array; when provided, it returns that `path` and its `entries`.
- **Response (`200 OK`)**: Each entry contains `name`, `path`, `type` (`file`, `directory`, or `symlink`), `size`, `permissions`, `owner`, `group`, and RFC3339 `modified`; symlink entries may also contain `target`.
- **Failure (`400 Bad Request`)**: The requested path is not an allowed readable directory.

### GET /api/files/read
- **Auth**: Auth
- **Query**: Required `path` for one file below a configured allowed root.
- **Response (`200 OK`)**: `{"path":"/allowed/file","content":"..."}`.
- **Failure (`400 Bad Request`)**: `path` is missing, the target is a directory or sensitive path, the file is unavailable, or it exceeds the file-manager read limit.

### PUT /api/files/write
- **Auth**: Manager
- **Body**: `{"path":"/allowed/file","content":"..."}`. `path` is required; `content` is text and may be empty. The handler creates a missing file and preserves existing permissions when overwriting.
- **Response (`200 OK`)**: `{"status":"ok","path":"/allowed/file"}`.
- **Failure (`400 Bad Request`)**: The JSON body is invalid, `path` is missing, or the file cannot be written within the allowed root boundary.

### POST /api/files/create
- **Auth**: Manager
- **Body**: `{"path":"/allowed/new-item","type":"file"}`. `type=file` creates a file; both `type=directory` and the compatibility alias `type=dir` create a directory. The current handler's default branch creates a file for an omitted or other type value.
- **Response (`201 Created`)**: `{"status":"created","path":"/allowed/new-item"}`.
- **Failure (`400 Bad Request`)**: The JSON body is invalid, `path` is missing, or the target cannot be created within the allowed root boundary.

### DELETE /api/files
- **Auth**: Manager
- **Query**: Required `path` for one file or directory below a configured allowed root. Directory deletion is recursive.
- **Response (`200 OK`)**: `{"status":"deleted","path":"/allowed/item"}`.
- **Failure (`400 Bad Request`)**: `path` is missing or the target cannot be deleted within the allowed root boundary.

### POST /api/files/rename
- **Auth**: Manager
- **Body**: Use `{"src":"/allowed/old","dst":"/allowed/new"}` or the legacy `{"old_path":"/allowed/old","new_path":"/allowed/new"}`. Mixed source/destination field pairs are also resolved independently; non-empty `src` and `dst` take precedence over their legacy counterparts.
- **Response (`200 OK`)**: `{"status":"renamed","src":"/allowed/old","dst":"/allowed/new"}`.
- **Failure (`400 Bad Request`)**: The JSON body is invalid, an effective source or destination is missing, or the move cannot be performed within the allowed root boundary.

---

## 13. Logs

### GET /api/logs/sources
- **Auth**: Auth
- **Description**: List readable Nginx, PHP, PostgreSQL, allowlisted system, application, and PM2 log files. Application discovery is restricted to `storage/logs` below the installation's configured vhost root. PM2 discovery is restricted to the single configured unprivileged PM2 owner and its configured or account-derived `PM2_HOME`; Heyserver does not scan every home directory or a root-owned PM2 daemon.

### GET /api/logs/read
- **Auth**: Auth
- **Description**: Read the last lines of one observed allowlisted log file. Query params: required absolute `path` and optional positive `lines` value, clamped to `5000`.

### GET /api/logs/search
- **Auth**: Auth
- **Description**: Search one observed allowlisted log file with a case-insensitive literal substring. Query params: required `path`, required `query`, and optional positive `limit`, clamped to `2000`.

### GET /api/logs/stream
- **Auth**: Auth
- **Description**: Stream newly appended lines from an observed allowlisted `path` in real time via Server-Sent Events.

### GET /api/logs/download
- **Auth**: Auth
- **Description**: Download one observed allowlisted `path` as a text attachment. Files larger than `50 MiB` are rejected.

---

## 14. Mail (Optional Stalwart)

Mail routes are optional and remain `not_configured` until the operator
explicitly supplies the Stalwart endpoint and credentials. Local service and
runtime discovery additionally require explicit `HSERVER_STALWART_SERVICE`,
`HSERVER_STALWART_CONFIG_PATH`, and `HSERVER_STALWART_BIN` values. A URL or
path alone does not prove `healthy`: a configured but unreachable endpoint or
local discovery source is `unavailable`, and the API preserves that state
instead of guessing provider defaults.

### Service & Observability

### GET /api/mail/service/status
- **Auth**: Auth
- **Description**: Return Stalwart mail service systemd status (active/inactive, uptime).

### POST /api/mail/service/{action}
- **Auth**: Admin
- **Description**: Control the mail service. Actions: `start`, `stop`, `restart`.

### GET /api/mail/service/overview
- **Auth**: Auth
- **Description**: Return high-level mail service overview (queues, connections, version).

### GET /api/mail/status
- **Auth**: Auth
- **Description**: Return simplified mail status summary for dashboard.

### GET /api/mail/config
- **Auth**: Auth
- **Description**: Read the Stalwart mail server configuration.

### GET /api/mail/version
- **Auth**: Auth
- **Description**: Return the Stalwart binary version.

### GET /api/mail/listeners
- **Auth**: Auth
- **Description**: List active mail listeners (SMTP, IMAP, JMAP ports and TLS state).

### GET /api/mail/storage
- **Auth**: Auth
- **Description**: Return mail storage usage statistics.

### GET /api/mail/logs
- **Auth**: Auth
- **Description**: Read recent Stalwart mail logs.

### GET /api/mail/logs/search
- **Auth**: Auth
- **Description**: Search Stalwart mail logs by keyword or pattern.

### GET /api/mail/logs/delivery
- **Auth**: Auth
- **Description**: Read delivery log entries (sent, deferred, bounced).

### Domains

### GET /api/mail/domains
- **Auth**: Auth
- **Description**: List all mail domains configured in Stalwart.

### POST /api/mail/domains
- **Auth**: Manager
- **Description**: Add a new mail domain to Stalwart.

### DELETE /api/mail/domains/{domain}
- **Auth**: Admin
- **Description**: Remove a mail domain from Stalwart.

### Accounts

### GET /api/mail/accounts
- **Auth**: Auth
- **Description**: List individual mailbox principals through the configured optional mail integration. The optional `domain` query parameter applies an exact domain filter.
- **Response (`200 OK`)**: JSON array of safe `MailAccount` projections: `email`, `name`, `domain`, `quota`, `usedStorage`, `isEnabled`, and `aliases`. Passwords, secrets, hashes, and raw provider response bodies are never returned.
- **Errors**: `502 Bad Gateway` when the mail provider rejects or cannot complete the request; `503 Service Unavailable` when the mail integration is not configured; other failures use the standard `ErrorResponse` envelope.

### POST /api/mail/accounts
- **Auth**: Manager
- **Description**: Create a new mail account.

### GET /api/mail/accounts/{email}
- **Auth**: Auth
- **Description**: Get details for a specific mail account.

### DELETE /api/mail/accounts/{email}
- **Auth**: Admin
- **Description**: Delete a mail account.

### GET /api/mail/accounts/{email}/password
- **Auth**: Admin
- **Description**: Retrieve the stored password for a mail account.

### PUT /api/mail/accounts/{email}/password
- **Auth**: Manager
- **Description**: Change the password for a mail account.

### PUT /api/mail/accounts/{email}/quota
- **Auth**: Manager
- **Description**: Update the mailbox quota for a specific account.

### Aliases

### GET /api/mail/aliases
- **Auth**: Auth
- **Description**: List alias principals through the configured optional mail integration. The optional `domain` query parameter applies an exact domain filter.
- **Response (`200 OK`)**: JSON array of safe `MailAlias` projections: `id`, `address`, `destinations`, and optional `description`. Passwords, secrets, hashes, and raw provider response bodies are never returned.
- **Errors**: `502 Bad Gateway` when the mail provider rejects or cannot complete the request; `503 Service Unavailable` when the mail integration is not configured; other failures use the standard `ErrorResponse` envelope.

### POST /api/mail/aliases
- **Auth**: Manager
- **Description**: Create a new mail alias.

### DELETE /api/mail/aliases/{id}
- **Auth**: Admin
- **Description**: Delete a mail alias.

### Groups

### GET /api/mail/groups
- **Auth**: Auth
- **Description**: List all mail groups (principals of type group).

### POST /api/mail/groups
- **Auth**: Manager
- **Description**: Create a new mail group.

### PATCH /api/mail/groups/{name}/members
- **Auth**: Manager
- **Description**: Update the member list of a mail group.

### Queue

### GET /api/mail/queue
- **Auth**: Auth
- **Description**: List messages currently in the mail queue with status and retry count.

### POST /api/mail/queue/{id}/retry
- **Auth**: Manager
- **Description**: Force immediate retry of a queued message.

### DELETE /api/mail/queue/{id}
- **Auth**: Admin
- **Description**: Remove a message from the mail queue.

### DNS Check

### GET /api/mail/dns-check/{domain}
- **Auth**: Auth
- **Description**: Check DNS records for a mail domain (MX, SPF, DKIM, DMARC, PTR).

### DKIM

### GET /api/mail/dkim/{domain}
- **Auth**: Auth
- **Description**: List DKIM signing keys configured for a domain.

### POST /api/mail/dkim/{domain}
- **Auth**: Manager
- **Description**: Generate a new DKIM key pair for a domain.

### GET /api/mail/dkim/{domain}/{selector}/dns
- **Auth**: Auth
- **Description**: Return the DNS TXT record value for a DKIM selector (for publishing).

### POST /api/mail/dkim/{domain}/rotate
- **Auth**: Manager
- **Description**: Rotate the DKIM signing key for a domain (generates new key, keeps old active briefly).

### GET /api/mail/dkim/{domain}/config
- **Auth**: Auth
- **Description**: Return the full DKIM signing configuration for a domain.

### Statistics

### GET /api/mail/stats
- **Auth**: Auth
- **Description**: Return aggregated mail statistics (sent, received, failed totals).

### GET /api/mail/stats/top-senders
- **Auth**: Auth
- **Description**: Return top sender addresses by message volume.

### GET /api/mail/stats/top-recipients
- **Auth**: Auth
- **Description**: Return top recipient addresses by message volume.

### GET /api/mail/stats/volume
- **Auth**: Auth
- **Description**: Return mail volume over time (hourly/daily breakdown).

### GET /api/mail/stats/storage
- **Auth**: Auth
- **Description**: Return per-account and total mailbox storage usage.

### GET /api/mail/stats/deliverability
- **Auth**: Auth
- **Description**: Return delivery success/failure/deferral rates.

### Spam Filtering

### GET /api/mail/spam/config
- **Auth**: Auth
- **Description**: Read spam filter configuration (thresholds, enabled modules).

### PUT /api/mail/spam/config
- **Auth**: Admin
- **Description**: Update spam filter configuration.

### GET /api/mail/spam/blocklist
- **Auth**: Auth
- **Description**: List blocked sender patterns.

### POST /api/mail/spam/blocklist
- **Auth**: Manager
- **Description**: Add a pattern to the spam blocklist.

### DELETE /api/mail/spam/blocklist/{pattern}
- **Auth**: Manager
- **Description**: Remove a pattern from the spam blocklist.

### GET /api/mail/spam/allowlist
- **Auth**: Auth
- **Description**: List allowed sender patterns (whitelist).

### POST /api/mail/spam/allowlist
- **Auth**: Manager
- **Description**: Add a pattern to the spam allowlist.

### DELETE /api/mail/spam/allowlist/{pattern}
- **Auth**: Manager
- **Description**: Remove a pattern from the spam allowlist.

### Security

### GET /api/mail/security/tls
- **Auth**: Auth
- **Description**: Return TLS configuration for mail listeners (protocols, ciphers, certificates).

### GET /api/mail/security/rate-limits
- **Auth**: Auth
- **Description**: Read mail service rate limit configuration.

### PUT /api/mail/security/rate-limits
- **Auth**: Admin
- **Description**: Update mail service rate limits (per-IP, per-account thresholds).

### GET /api/mail/security/failed-logins
- **Auth**: Admin
- **Description**: List recent failed IMAP/SMTP authentication attempts.

### GET /api/mail/security/connections
- **Auth**: Auth
- **Description**: Return active connection statistics (current SMTP/IMAP sessions).

### Auto-Discovery (No Auth)

### GET /mail/autoconfig/mail/config-v1.1.xml
- **Auth**: None
- **Description**: Thunderbird auto-configuration XML (Mozilla Autoconfig standard).

### GET /.well-known/autoconfig/mail/config-v1.1.xml
- **Auth**: None
- **Description**: Thunderbird auto-configuration XML (well-known path).

### POST /autodiscover/autodiscover.xml
- **Auth**: None
- **Description**: Outlook Autodiscover XML response for mail client auto-setup.

---

## 15. DNS / BIND

### GET /api/dns/zones
- **Auth**: Auth
- **Description**: List all BIND9 zones managed by the local nameserver.

### GET /api/dns/zones/{domain}
- **Auth**: Auth
- **Description**: Get zone details (type, file path, serial) for a specific domain.

### POST /api/dns/zones
- **Auth**: Admin
- **Description**: Strictly decode and normalize the zone domain plus IPv4 address, validate staged zone and configuration files, write a protected durable recovery journal, commit both without overwriting an existing zone path, globally reload BIND, and restore the previous configuration when commit or reload fails.

### DELETE /api/dns/zones/{domain}
- **Auth**: Admin
- **Description**: Persist the pre-change config and zone snapshots, stage the zone file as a reversible tombstone, validate and commit the configuration removal, globally reload BIND, and restore both files plus runtime state when deletion cannot finish. Startup recovery completes an interrupted journal before mutations resume.

### GET /api/dns/zones/{domain}/export
- **Auth**: Auth
- **Description**: Export the raw zone file content for a domain.

### GET /api/dns/zones/{domain}/soa
- **Auth**: Auth
- **Description**: Return the SOA (Start of Authority) record for a zone.

### PUT /api/dns/zones/{domain}/soa
- **Auth**: Manager
- **Description**: Require the complete primary nameserver, hostmaster, refresh, retry, expire, and minimum payload, validate the normalized staged SOA update, atomically replace the zone file, reload the zone, and restore plus reload the previous file when runtime reload fails.

### GET /api/dns/zones/{domain}/records
- **Auth**: Auth
- **Description**: List all DNS records in a zone.

### POST /api/dns/zones/{domain}/records
- **Auth**: Manager
- **Description**: Strictly decode and normalize a record through a validate-before-replace file transaction. With `autoReload`, a failed runtime reload restores and reloads the previous file.

### PUT /api/dns/zones/{domain}/records
- **Auth**: Manager
- **Description**: Update an existing record through the same validated atomic file and optional reload rollback boundary.

### DELETE /api/dns/zones/{domain}/records
- **Auth**: Manager
- **Description**: Delete one exact record through the same validated atomic file and optional reload rollback boundary. Use either the strict JSON body or the exact `name`, `type`, `value`, and optional `autoReload` query fields; mixed, unknown, repeated, or empty query input is rejected.

### POST /api/dns/reload
- **Auth**: Manager
- **Description**: Reload BIND9 (`rndc reload`) to apply zone changes. The request body must be empty.

### POST /api/dns/check
- **Auth**: Auth
- **Description**: Run `named-checkconf` / `named-checkzone` with an empty request body. A failed configuration remains an HTTP 200 diagnostic result with `ok=false` and complete output rather than becoming a generic transport error.

### GET /api/dns/status
- **Auth**: Auth
- **Description**: Return provider-neutral local BIND readiness. `state` is one of `healthy`, `not-installed`, `not-configured`, `stopped`, or `unavailable`; the response also reports `available`, `installed`, `active`, `serviceState`, `version`, `configAvailable`, `checkToolsAvailable`, `reloadAvailable`, `zoneManagementReady`, `recoveryPending`, and an optional diagnostic `error`. `recoveryPending=true` means a protected lifecycle journal still requires startup recovery and every BIND mutation is blocked. Journal paths and contents are never returned. The endpoint never installs a package, starts a service, or rewrites configuration.

### GET /api/dns/lookup
- **Auth**: Auth
- **Description**: Perform independent DNS lookups against Google, Cloudflare, and the host resolver. Exact query params are required `domain` and optional `type` (default `A`); unknown, repeated, empty, or invalid fields are rejected before lookup.

---

## 16. Cloudflare

### GET /api/cloudflare/zones
- **Auth**: Auth
- **Description**: List all Cloudflare zones accessible with the optional installation-owned credential. An unconfigured integration returns `503`; it is not represented as an empty provider inventory.

### GET /api/cloudflare/zones/{zoneId}
- **Auth**: Auth
- **Description**: Get details for a specific Cloudflare zone (name, status, nameservers).

### GET /api/cloudflare/zones/{zoneId}/records
- **Auth**: Auth
- **Description**: List DNS records for a Cloudflare zone. Optional `type` and exact `name` query parameters filter the provider request; unknown, repeated, empty, or invalid filters return `400` before provider access.

### POST /api/cloudflare/zones/{zoneId}/records
- **Auth**: Manager
- **Description**: Create a DNS record from a strict complete `type`, `name`, `content`, `ttl`, and `proxied` payload; optional `priority` is bounded to `0–65535`. Unknown fields and trailing JSON are rejected. Record types normalize to uppercase without a frozen provider enum. TTL is `1` for automatic or `30–86400` seconds; proxying is accepted only for `A`, `AAAA`, and `CNAME`.

### PUT /api/cloudflare/zones/{zoneId}/records/{recordId}
- **Auth**: Manager
- **Description**: Fully replace an existing DNS record through the same strict complete payload. A client offering partial edits must first preserve every omitted field; `hserverctl` performs that read-before-replace flow.

### DELETE /api/cloudflare/zones/{zoneId}/records/{recordId}
- **Auth**: Admin
- **Description**: Delete one exact DNS record. The request body must be empty and success is `204 No Content`.

### PUT /api/cloudflare/zones/{zoneId}/records/{recordId}/proxy
- **Auth**: Manager
- **Description**: Set the Cloudflare proxy state from the strict body `{"proxied": true|false}`. The field is mandatory; unknown fields and trailing JSON are rejected.

### POST /api/cloudflare/zones/{zoneId}/purge
- **Auth**: Admin
- **Description**: Purge the complete Cloudflare cache for one zone. The request body must be empty; selective URL purging is not exposed by this route.

### GET /api/cloudflare/zones/{zoneId}/email-routing
- **Auth**: Auth
- **Description**: Return Cloudflare Email Routing configuration and rules for a zone.

### POST /api/cloudflare/mail-autofix/{domain}
- **Auth**: Admin
- **Description**: Reconcile mail-related DNS records in Cloudflare for a normalized lower-case DNS domain (MX, SPF, optional Stalwart DKIM, DMARC, and an unproxied A/AAAA record when the configured mail hostname belongs to the zone). Invalid DNS labels and non-empty request bodies are rejected before provider access.
- **Configuration**: `HSERVER_MAIL_DNS_HOSTNAME` enables the action. Optional values are `HSERVER_MAIL_DNS_PUBLIC_IP`, `HSERVER_MAIL_DNS_MX_PRIORITY`, `HSERVER_MAIL_DNS_SPF`, and `HSERVER_MAIL_DNS_DMARC`. No installation hostname or IP is built into the application.

---

## 17. Cron

The generated OpenAPI contract publishes structured schemas for this complete
local Cron surface, including readiness states, both inventories, mutation
receipts, the `user` query default, and unavailable-client errors.

### GET /api/cron/status
- **Auth**: Auth
- **Description**: Report whether the local `crontab` client is installed and the systemd `cron` daemon is observable and active.
- **Response states**: `healthy`, `not-installed`, `stopped`, or `unavailable`. Mutating clients should require `available: true`; `installed: true` with `available: false` may still permit read-only inventory.

### GET /api/cron/jobs
- **Auth**: Auth
- **Description**: List all panel-managed cron jobs.
- **Failure**: Returns `503 Service Unavailable` when the `crontab` executable is absent instead of presenting a false empty inventory. An unreadable user crontab fails the complete request rather than returning a partial list.

### POST /api/cron/jobs
- **Auth**: Manager
- **Description**: Create a new cron job with schedule, command, and user.
- **Body**: `{"user":"deploy","schedule":"0 * * * *","command":"/usr/local/bin/task","description":"Hourly task"}`. The job is active when created; `user` defaults to `root` when omitted.
- **Contract**: `schedule` and `command` are required. `user` and `description` are optional. Unknown JSON fields and trailing JSON values are rejected.

### PUT /api/cron/jobs/{id}
- **Auth**: Manager
- **Description**: Replace the schedule, command, description, and active state of an existing cron job.
- **Query**: `user` identifies the owning system user and defaults to `root`. Clients managing jobs returned by the list endpoint should always pass that job's `user` value.
- **Body**: `{"schedule":"0 */2 * * *","command":"/usr/local/bin/task","description":"Every two hours","isActive":true}`. `schedule` and `command` are required, including for enable/disable actions.
- **Contract**: This is a complete replacement payload: `schedule`, `command`, `description`, and `isActive` must all be present. An empty description is valid. Unknown JSON fields and trailing JSON values are rejected, and omitting `isActive` cannot implicitly disable a job.

### DELETE /api/cron/jobs/{id}
- **Auth**: Admin
- **Description**: Delete a cron job.
- **Query**: `user` identifies the owning system user and defaults to `root`. Clients managing jobs returned by the list endpoint should always pass that job's `user` value.

### GET /api/cron/system
- **Auth**: Auth
- **Description**: List system-level cron jobs from `/etc/cron*` directories and user crontabs.

---

## 18. Docker

The generated OpenAPI contract publishes the complete local Docker surface,
including structured readiness, container and image inventories, bounded log
responses, fixed-action receipts, image mutation receipts, query bounds, and
the distinction between empty inventory and unavailable Docker state.

### GET /api/docker/status
- **Auth**: Auth
- **Description**: Return Docker CLI and daemon state with stable container and image counts.

### GET /api/docker/containers
- **Auth**: Auth
- **Description**: List all Docker containers with normalized state, ports, CPU, and memory data.

### GET /api/docker/containers/{id}/logs
- **Auth**: Auth
- **Description**: Return timestamped container logs. The optional `tail` parameter accepts `1`–`1000` lines (default `200`); responses are capped at 1 MiB and report truncation explicitly.

### POST /api/docker/containers/{id}/{action}
- **Auth**: Admin
- **Description**: Control a Docker container. Actions: `start`, `stop`, `restart`, `pause`, `unpause`, and non-force `remove`.
- **Contract**: The action is a fixed path enum; arbitrary Docker commands and additional CLI arguments are not accepted. `remove` maps to non-force `docker rm`.

### GET /api/docker/images
- **Auth**: Auth
- **Description**: List locally available Docker images with size and tags.

### POST /api/docker/images/pull
- **Auth**: Admin
- **Description**: Pull one validated image reference through the local Docker CLI.
- **Body**: `{"name":"nginx:1.27"}`. `name` is required and accepts a bounded image name, tag, or digest reference. Unknown JSON fields, trailing JSON values, option-like values, and parent-path markers are rejected.

### DELETE /api/docker/images/{id}
- **Auth**: Admin
- **Description**: Remove one validated local image ID without force flags.

Managed-node container actions use the separate agent route and expose only
`start`, `stop`, and `restart`; the node must advertise container-action
capability and enable the requested action in its local allowlist.

---

## 19. Deploy

### GET /api/deploy/templates
- **Auth**: Admin
- **Description**: Read reusable deployment templates from the fixed `${HSERVER_DATA_DIR}/deploy-templates` directory. The response distinguishes `not_configured`, `healthy`, and `unavailable`, retains valid templates when another file is rejected, and reports bounded issues by basename. The API cannot write templates, choose another directory, or return repository credentials, project directories, webhook secrets, or other undeclared fields.

### GET /api/deploy/targets
- **Auth**: Auth
- **Response**: A JSON array of strict target objects; an empty inventory is `[]`, never `null`. The `webhookToken` response field is always the empty string, while `webhookStatus` honestly distinguishes `not_configured`, `healthy`, and `unavailable`.
- **Description**: List all local deploy targets with repository, branch, environment, and script or Docker Compose configuration without returning signing secrets.

### POST /api/deploy/targets
- **Auth**: Admin
- **Body**: `name` and absolute `projectDir` are required. `branch` defaults to `main`, `deploymentKind` to `script`, `webhookProvider` to `github`, and `autoDeploy` to `false`. `webhookToken` is write-only and capped at 4096 bytes. Unknown fields and trailing JSON values are rejected.
- **Description**: Create a local script or Docker Compose deploy target. Target names are single-line and at most 128 bytes. Compose files must be relative to the project directory; script bodies are capped at 64 KiB. The `201 Created` response never echoes the signing secret.

### POST /api/deploy/targets/{id}/staging
- **Auth**: Admin
- **Body**: `{"name":"App Staging","branch":"develop","projectDir":"/srv/apps/app-staging"}`. `projectDir` is required and must be an absolute directory that does not overlap the Heyserver data directory or any deployment target after symlink resolution. `name` and `branch` default from the production source when omitted. Unknown and trailing JSON fields are rejected.
- **Description**: Derive a staging target from one canonical production target. Repository and executor configuration are inherited. Environment values, webhook signing secrets, auto-deploy, domains, TLS, and DNS state are not copied. The `201 Created` receipt repeats these non-copy guarantees explicitly. Staging targets cannot be used as another staging source.
- **Conflicts**: A production target with attached staging targets cannot be deleted. A production or staging project directory cannot later be moved across a staging storage boundary.

### GET /api/deploy/targets/{id}/preflight
- **Auth**: Auth
- **Response**: `eligible` plus a strict `checks` array whose status is `pass`, `pending`, or `fail`.
- **Description**: Run read-only readiness checks for the project directory, Git checkout or deferred first-clone provisioning, selected executor, and Compose configuration when applicable. An unavailable optional executor is reported as observed readiness rather than a false internal success.

### GET /api/deploy/targets/{id}/revision
- **Auth**: Auth
- **Description**: Compare the exact local checkout HEAD with the newest successful deployment and the revision currently available to rollback. The response includes tracked working-tree state, commit distance, and a bounded file/insertion/deletion summary. It never fetches remote refs or changes the checkout, and distinguishes `not_deployed`, `ready`, and `unavailable`.

### GET /api/deploy/targets/{id}/services
- **Auth**: Auth
- **Response**: A strict array containing service identity, container identity, image, state, optional health, exit code, and published ports. An empty project is `[]`, never `null`.
- **Description**: List containers observed inside one configured Docker Compose project. Commands remain bound to the persisted project directory and validated relative Compose file.

### GET /api/deploy/targets/{id}/services/{service}/logs
- **Auth**: Auth
- **Query**: Optional `tail` accepts `1`–`1000`, default `200`; invalid supplied values return `400 Bad Request`.
- **Description**: Return timestamped Compose service logs for a validated service identity. Responses retain at most the newest 1 MiB and return the effective `tail` plus `truncated` state.

### POST /api/deploy/targets/{id}/services/{service}/{action}
- **Auth**: Manager
- **Response**: `{"status":"ok","service":"web","action":"restart"}` after the fixed action completes.
- **Description**: Run a fixed service-scoped Compose action: `start`, `stop`, `restart`, or `recreate`. The request body must be empty. Arbitrary commands, force flags, and project-wide `down` are not exposed.

### GET /api/deploy/targets/{id}/environment
- **Auth**: Admin
- **Response**: `configured` plus a sorted `variables` array containing names only. Stored values are never returned.
- **Description**: Distinguish an absent protected environment file from configured project environment variable names without creating a secret retrieval API.

### PUT /api/deploy/targets/{id}/environment
- **Auth**: Admin
- **Body**: `{"key":"APP_MODE","value":"production"}`. Keys match `^[A-Za-z_][A-Za-z0-9_]{0,127}$`; UTF-8 values are capped at 64 KiB and cannot cross the protected one-variable-per-line storage boundary. Unknown fields, trailing JSON values, and oversized requests are rejected.
- **Description**: Atomically create or replace one write-only project environment value outside the Git checkout. The response contains names only.

### DELETE /api/deploy/targets/{id}/environment/{key}
- **Auth**: Admin
- **Description**: Remove one validated project environment variable; the protected file is removed when no keys remain. Missing variables remain an idempotent metadata result rather than exposing their prior values.

### GET /api/deploy/targets/{id}/domains
- **Auth**: Auth
- **Response**: A strict array containing the normalized hostname, Compose service, published host port, fixed `http://127.0.0.1:PORT` upstream, and observed TLS state. An empty inventory is `[]`.
- **Description**: List the target's installation-owned Nginx project-domain mappings. `tlsStatus` is one of `not_configured`, `healthy`, `expiring`, `expired`, or `unavailable`; no arbitrary upstream URL is accepted.

### POST /api/deploy/targets/{id}/domains
- **Auth**: Admin
- **Body**: `{"domain":"app.example.com","service":"web","hostPort":8080}`. All fields are required; the host port is `1–65535`. Unknown fields, trailing JSON values, arbitrary upstreams, and bodies above 8 KiB are rejected.
- **Description**: Bind a validated ASCII hostname and Compose service label to an explicitly published host port. Heyserver normalizes the hostname, writes an owned HTTP virtual host, runs `nginx -t`, reloads Nginx, and rolls back persistence plus generated files if activation fails.

### GET /api/deploy/targets/{id}/domains/{domainId}/health
- **Auth**: Auth
- **Response**: The hostname, fixed upstream, `healthy | unhealthy | unavailable`, optional HTTP status, measured latency, message, and observation timestamp.
- **Description**: Actively probe the fixed `http://127.0.0.1:PORT/` upstream with a three-second deadline and redirects disabled. HTTP 200–399 is `healthy`, another HTTP response is `unhealthy`, and no response is `unavailable` without pretending an internal API failure occurred.

### POST /api/deploy/targets/{id}/domains/{domainId}/tls
- **Auth**: Admin
- **Body**: Optional. An empty body uses Certbot's no-email registration mode; otherwise send `{"email":"admin@example.com"}`. Email is capped at 254 bytes. Unknown fields, trailing JSON values, display-name addresses, and bodies above 8 KiB are rejected.
- **Description**: Obtain or reuse a single-host certificate with Certbot's fixed HTTP-01 webroot flow, verify that the resulting certificate covers the mapped domain, then transactionally switch the owned Nginx virtual host to HTTPS. Arbitrary Certbot arguments, certificate names, challenge roots, and domains are not accepted.

### DELETE /api/deploy/targets/{id}/domains/{domainId}/tls
- **Auth**: Admin
- **Description**: With an empty request body, transactionally restore the owned virtual host to HTTP while preserving Certbot certificate files for rollback or later reuse. It does not revoke or delete the certificate.

### DELETE /api/deploy/targets/{id}/domains/{domainId}
- **Auth**: Admin
- **Description**: With an empty request body, transactionally disable and remove one Heyserver-owned project-domain virtual host. Success is `204 No Content`; unrelated Nginx configuration is never deleted.

### PUT /api/deploy/targets/{id}
- **Auth**: Admin
- **Description**: Replace a deploy target's mutable configuration. The positive integer target ID, target name, absolute project directory, repository/branch, executor, webhook provider/token, activation, and auto-deploy rules are revalidated before persistence. Unknown fields and trailing JSON values are rejected; webhook tokens remain write-only.

### DELETE /api/deploy/targets/{id}
- **Auth**: Admin
- **Description**: Delete a target and its run history after its project domains and staging children have been removed. Staging children must be deleted before their production source so an active Nginx mapping or staging ownership boundary cannot be orphaned.

### POST /api/deploy/webhook/{targetId}
- **Auth**: None (provider signature verification; no panel JWT)
- **Description**: Provider-aware GitHub and GitLab webhook receiver. A target selects exactly one provider. GitHub push requests require `X-GitHub-Event: push`, a valid `X-Hub-Signature-256`, and a bounded unique `X-GitHub-Delivery`. GitLab Standard Webhooks require `X-Gitlab-Event: Push Hook`, `webhook-id`, a fresh `webhook-timestamp`, and at least one valid `webhook-signature` generated from the configured `whsec_` signing token. The GitLab timestamp window is five minutes.
- **Replay behavior**: The authenticated provider delivery identity is persisted before branch, active-state, or preflight checks. A repeated delivery returns HTTP 200 with `delivery already processed` and cannot queue another run, including after a panel restart.
- **Body and branch behavior**: The exact signed body is capped at 10 MiB. Push bodies must be valid JSON and contain a validated `refs/heads/*` ref. Non-target branches, disabled targets, non-push events, and provider retries receive non-retryable HTTP 200 outcomes without deployment.
- **Secret state**: `not_configured`, `healthy`, and `unavailable` are distinct. Native installations store signing values below `${HSERVER_DATA_DIR}/deploy-webhook-secrets` with directory mode `0700` and file mode `0600`; SQLite retains only the deterministic file reference. Existing SQLite values are migrated in place at service startup.
- **Provider references**: [GitHub webhook signature validation](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries), [GitLab Standard Webhooks signing and delivery headers](https://docs.gitlab.com/user/project/integrations/webhooks/), and [GitLab push event format](https://docs.gitlab.com/user/project/integrations/webhook_events/).

### POST /api/deploy/manual/{targetId}
- **Auth**: Manager
- **Response**: `202 Accepted` with `{"message":"deployment queued","runId":123}`. The positive run ID is used for history and log polling.
- **Description**: Repeat preflight and asynchronously trigger a manual deployment for an eligible positive integer target ID. Ineligible targets return `409 Conflict` without queueing a run.

### GET /api/deploy/history
- **Auth**: Auth
- **Query**: Optional positive integer `targetId`; optional `limit` from `1` through `500`, default `50`. Invalid supplied values return `400 Bad Request` instead of being ignored.
- **Description**: List newest deployment runs with trigger, branch, revision, status, and timestamps. Log blobs are intentionally absent from list items, and an empty result is `[]`.

### GET /api/deploy/history/{id}/logs
- **Auth**: Auth
- **Description**: Get `{"logs":"..."}` containing the captured output for a positive integer deployment run ID. Missing runs return `404 Not Found`.

### POST /api/deploy/rollback/{targetId}
- **Auth**: Admin
- **Response**: `202 Accepted` with `{"message":"rollback queued","runId":124}`.
- **Description**: Asynchronously roll back an eligible positive integer deploy target to the newest available previous successful deployment revision. Ineligible targets return `409 Conflict` without queueing a run.

---

## 20. Terminal

### GET /api/terminal/ws
- **Auth**: Admin (WebSocket upgrade)
- **Description**: Establish a WebSocket connection to an interactive PTY on the local server or on the `node` query parameter through its outbound agent relay. A remote node must be online and advertise `terminal`.

### GET /api/agent/v1/terminal
- **Auth**: Managed-node ID header and agent bearer token (WebSocket upgrade)
- **Description**: Attach one outbound agent connection that multiplexes bounded PTY sessions. The authenticated node must advertise `terminal`; this endpoint never opens inbound SSH.

---

## 21. Monitoring

### GET /api/monitoring/stats
- **Auth**: Auth
- **Description**: Return time-series system metrics (CPU, RAM, network I/O, disk I/O).

### GET /api/monitoring/processes
- **Auth**: Auth
- **Description**: List running OS processes with CPU and memory usage (top-like output).

---

## 22. Security

### GET /api/security/score
- **Auth**: Auth
- **Description**: Return an overall security score with per-category breakdown (firewall, SSH, SSL, etc.).
- **Optional dependency semantics**: A missing Fail2Ban installation is reported as `warn` with `Not installed (optional)`; a verified stopped daemon remains `fail`, while an indeterminate runtime is `warn` rather than a false stopped state.

### GET /api/security/fail2ban/status
- **Auth**: Admin
- **Description**: Return Fail2Ban client, daemon, and complete jail-inventory readiness without treating an optional missing installation as a transport failure.
- **Response states**: `healthy`, `not-installed`, `stopped`, or `unavailable`. Ban and unban clients should require `available: true`; `running: true` alone is insufficient when the jail inventory could not be read completely.

### GET /api/security/fail2ban/jails/{jail}
- **Auth**: Admin
- **Description**: Get details for a specific fail2ban jail (banned IPs, filter stats).

### POST /api/security/fail2ban/ban
- **Auth**: Admin
- **Description**: Manually ban an IP address via fail2ban.
- **Failures**: Invalid jail/IP input returns `400`; an unavailable client, daemon, or jail inventory returns `503`.

### POST /api/security/fail2ban/unban
- **Auth**: Admin
- **Description**: Remove an IP address ban from fail2ban.
- **Failures**: Invalid jail/IP input returns `400`; an unavailable client, daemon, or jail inventory returns `503`.

### GET /api/security/ip-blacklist
- **Auth**: Admin
- **Description**: List all IPs in the panel-managed IP blacklist.

### POST /api/security/ip-blacklist
- **Auth**: Admin
- **Description**: Add an IP address to the blacklist.

### DELETE /api/security/ip-blacklist/{ip}
- **Auth**: Admin
- **Description**: Remove an IP address from the blacklist.

### GET /api/security/ip-whitelist
- **Auth**: Admin
- **Description**: List all IPs in the panel-managed IP whitelist.

### POST /api/security/ip-whitelist
- **Auth**: Admin
- **Description**: Add an IP address or CIDR to the whitelist.

### DELETE /api/security/ip-whitelist/{ip}
- **Auth**: Admin
- **Description**: Remove an IP address or CIDR from the whitelist.

---

## 23. Notifications

### Channels

### GET /api/notify/channels
- **Auth**: Auth
- **Description**: List configured email, Telegram, Discord, and Slack channels. Provider credentials are never returned; each redacted `config` reports only editable non-secret fields and `secret_configured`. Each item also includes canonical `state` (`not_configured`, `unavailable`, or `healthy`) and an exact delivery `detail`.
- **Delivery state**: `delivery_confirmed` is `healthy` only when a persisted successful receipt matches the channel's current internal `config_revision` and is no more than seven days old. `delivery_failed`, `delivery_stale`, and `probe_unverified` are `unavailable`; the latter covers a missing or revision-mismatched receipt. A configured channel never becomes healthy from config presence alone.
- **Aggregate rule**: Notification delivery is strict: any configured disabled, failed, stale, unavailable, or unverified channel keeps the aggregate unavailable. All configured channels must have current `delivery_confirmed` receipts for a healthy aggregate.

### GET /api/notify/channels/{id}
- **Auth**: Auth
- **Description**: Get redacted details for one notification channel without returning its SMTP password, bot token, or webhook URL.

### POST /api/notify/channels
- **Auth**: Manager
- **Description**: Create a channel and move its complete config into an installation-owned protected file. SQLite persists only the deterministic file reference plus the internal configuration revision used to fence delivery receipts; the response is redacted and starts as `unavailable` with `probe_unverified` until a successful receipt exists.

### PUT /api/notify/channels/{id}
- **Auth**: Manager
- **Description**: Update a channel without changing its type. An omitted or empty secret field preserves the current protected credential, allowing the browser to edit non-secret fields without reading the secret first. `clearSecret: true` is the explicit credential-removal operation. Every successful configuration update advances the internal `config_revision`, so an older receipt no longer proves the new configuration; the channel returns `unavailable` with `probe_unverified` until a fresh receipt matches.

### DELETE /api/notify/channels/{id}
- **Auth**: Admin
- **Description**: Delete a notification channel and its protected local config file after explicit admin authorization.

### POST /api/notify/channels/{id}/test
- **Auth**: Manager
- **Description**: Send a test notification through a channel to verify connectivity. After the provider call, Heyserver persists one bounded `success` or `failure` receipt for the channel and its current configuration revision; receipts never contain the subject, body, provider error, secret, or raw provider payload.
- **Success (`200 OK`)**: `{"status":"sent","state":"healthy","detail":"delivery_confirmed"}` is returned only when the provider call succeeds and the receipt is persisted. The successful receipt remains fresh for seven days.
- **Provider failure (`502 Bad Gateway`)**: `{"error":"test delivery failed","state":"unavailable"}`; the failed attempt is still persisted as `delivery_failed` when the receipt store is available.
- **Receipt persistence failure (`503 Service Unavailable`)**: `{"error":"test sent but delivery receipt could not be stored","state":"unavailable"}`; a provider send is not presented as healthy without durable evidence.

Alert, uptime, and backup notification sends use the same bounded per-channel
receipt contract after their provider calls. Their provider errors are logged
or returned through their existing operation boundary, while the receipt keeps
only the outcome, source, channel, configuration revision, and observation
time.

### Alert Rules

### GET /api/notify/rules
- **Auth**: Auth
- **Description**: List canonical alert rules. Empty inventory is `[]`, not `null`. Existing `cpu`, `memory`, and `disk` rows are migrated in place to `cpu_usage`, `memory_usage`, and `disk_usage`.

### GET /api/notify/rules/{id}
- **Auth**: Auth
- **Description**: Get a specific alert rule configuration.

### POST /api/notify/rules
- **Auth**: Manager
- **Description**: Create a strictly validated rule. Supported types are `cpu_usage`, `memory_usage`, `disk_usage`, `ssl_expiry`, `service_down`, and `failed_logins`; the three legacy aliases remain accepted and normalize in the response. Name is required and bounded to 128 control-free characters. Duration accepts `0–1440` minutes and cooldown defaults to 15 and accepts `1–10080`.
- **Type semantics**: CPU, memory, and disk trigger at or above `0–100%`; disk defaults to target `/` and otherwise requires an absolute mount path. SSL expiry triggers at or below `0–3650` remaining days and requires a DNS-name target. Service-down requires a bounded systemd unit target and canonicalizes threshold to `1`. Failed logins require a whole `1–1000000` count observed over one minute.
- **Body**: Unknown fields, trailing JSON, and missing type-specific fields return `400`. Explicit `enabled: false` is preserved.

### PUT /api/notify/rules/{id}
- **Auth**: Manager
- **Description**: Strict partial update. At least one field is required; only provided fields overlay the stored row. The complete merged rule is then normalized and validated using the create semantics, so `{ "enabled": false }` cannot erase its name, type, threshold, target, duration, or cooldown.

### DELETE /api/notify/rules/{id}
- **Auth**: Admin
- **Description**: Delete an exact alert rule. A missing rule returns `404` instead of a false deletion receipt.

### Alert History

### GET /api/notify/history
- **Auth**: Auth
- **Description**: List alert dispatch history as `{items,total,limit,offset}`. `limit` defaults to 50 and accepts canonical integers `1–200`; `offset` defaults to zero and accepts canonical non-negative integers. Repeated, unknown, empty, signed, zero-padded, or out-of-range query values return `400`. Empty `items` is `[]`, not `null`.

---

## 24. Users

### GET /api/users
- **Auth**: Admin
- **Query**: Optional `limit` defaults to 20 and accepts 1–200; optional `offset` defaults to zero and must be non-negative.
- **Response**: `{"data":[User],"total":N,"limit":N,"offset":N}`. Returned users contain `id`, `email`, `name`, `role`, `totp_enabled`, `createdAt`, and `updatedAt`; password hashes and TOTP secrets are never returned.
- **Description**: List the paginated central panel-user inventory.

### POST /api/users
- **Auth**: Admin
- **Body**: `{"email":"operator@example.com","name":"Operator","password":"at-least-8-characters","role":"viewer"}`. `email`, `name`, and `password` are required; `role` is optional and defaults to `viewer`.
- **Description**: Create one panel user. Roles are exactly `admin`, `manager`, or `viewer`; names are 1–100 characters and passwords are 8–128 characters. Unknown fields, trailing JSON, invalid values, and duplicate email addresses return `400` or `409` rather than being ignored. Success returns `201` with the user and no credential material.

### PUT /api/users/{id}
- **Auth**: Admin
- **Body**: A closed JSON object containing at least one of `email`, `name`, `role`, or `password`; for example `{"role":"manager"}`. Values use the same bounds as creation.
- **Description**: Atomically update the selected panel user. Every supplied field, including a replacement password, is validated before persistence; profile and password changes commit together or both roll back. Empty objects, unknown fields, trailing JSON, and invalid values return `400`; duplicate email addresses or demoting the final administrator return `409`. Success returns the updated user without credential material.

### DELETE /api/users/{id}
- **Auth**: Admin
- **Description**: Delete another panel user and return an empty `204 No Content`. An administrator cannot delete their own current account, and the final administrator account cannot be deleted. Invalid IDs return `400`, missing users return `404`, and the final-administrator invariant returns `409`.

---

## 25. Settings

### GET /api/settings
- **Auth**: Auth
- **Description**: Return only currently valid settings from the fixed 12-key installation-portable allowlist. Internal service records, provider credentials, onboarding state, tokens, and secrets are never returned by this generic endpoint. An installation with no stored editable settings returns `{}`.

### GET /api/settings/{key}
- **Auth**: Auth
- **Path**: `key` must be one of the 12 keys in the [portable configuration contract](portable-configuration.md).
- **Description**: Return `{"key":"adminEmail","value":"admin@example.com"}` for one editable key. An unset allowlisted key returns an empty value; an unknown or internal key returns `404` without exposing its stored value.

### PUT /api/settings
- **Auth**: Manager
- **Body**: A strict non-empty object containing only the 12 editable keys and their validated string values, capped at 128 KiB. Unknown keys, internal service keys, onboarding keys, invalid URLs/email/host/port/timezone/boolean values, trailing JSON, and oversized bodies return `400` or `413`.
- **Description**: Atomically update the supplied editable settings without changing omitted keys. This generic endpoint cannot overwrite service-owned credential records.

### DELETE /api/settings/{key}
- **Auth**: Admin
- **Request**: No body; `key` must belong to the editable allowlist.
- **Description**: Delete one editable value so its consumer falls back to the installation default. Internal and unknown keys return `404` and remain unchanged.

### GET /api/settings/portable
- **Auth**: Admin
- **Description**: Export schema-v1 installation preferences through the fixed non-secret allowlist. The response is marked `no-store` and uses the attachment filename `hserver-portable-config-v1.json`.

### POST /api/settings/portable/preview
- **Auth**: Admin
- **Description**: Validate one raw schema-v1 bundle without mutation and return imported, changed, and unchanged key counts plus the proposed allowlisted changes. Unknown keys, fields, schemas, invalid values, and trailing JSON return `400`; bodies over 128 KiB return `413`.

### POST /api/settings/portable/import
- **Auth**: Admin
- **Description**: Transactionally apply an overlay request shaped as `{"bundle": { ... }, "confirmed": true}`. Missing keys are not deleted. The server validates the bundle again and requires literal `true` confirmation even after preview; the authenticated mutation limiter can return `429`.

See the [portable configuration contract](portable-configuration.md) for the
exact allowlist, exclusions, file format, and recovery behavior.

---

## 26. Audit

### GET /api/audit
- **Auth**: Auth
- **Description**: List audit log entries (user actions, resource changes, auth events) with pagination and filtering.

---

## 27. Managed Nodes

These contracts are promoted into the generated OpenAPI contract and can be generated by a
client without reconstructing payloads from UI requests.

### POST /api/nodes
- **Auth**: Admin
- **Description**: Enroll a provider-neutral node identity and return its bearer token exactly once. The database persists only its digest. Returns `409 Conflict` for a duplicate node ID.

### GET /api/nodes
- **Auth**: Auth
- **Description**: List enrolled nodes. Every record includes server-computed `online`, server-observed `last_seen_at`, advertised capabilities, inventory, and panel/agent compatibility metadata. Current agent inventory includes its bounded `arch`; legacy agents may omit it. `online` remains true for 45 seconds after the last accepted heartbeat.

### GET /api/nodes/{id}
- **Auth**: Auth
- **Description**: Return one enrolled node with the same server-computed connectivity and compatibility fields as the list response.

### POST /api/nodes/{id}/tasks
- **Auth**: Admin
- **Request**: Strict JSON with a fixed `kind`, an optional bounded string-map `payload` of at most six fields, and required `confirmed: true`; missing or false confirmation and unknown fields are rejected.
- **Description**: Validate and queue one structured task allowed by the node's advertised capability. Returns `409 Conflict` and creates no task when the last server-observed heartbeat is older than 45 seconds.

### GET /api/nodes/{id}/tasks
- **Auth**: Manager
- **Description**: Return newest task history for the node. The optional positive `limit` query defaults to 20 and is capped to 50.

### GET /api/nodes/{id}/tasks/{taskID}
- **Auth**: Manager
- **Description**: Return one node-owned structured task with queued/running/completed/failed state, bounded result, error, and lifecycle timestamps.

### POST /api/nodes/{id}/actions/{action}
- **Auth**: Admin
- **Description**: Run one fixed host action through the node's advertised capability and server-local allowlist, then wait for its structured result. Offline nodes or missing capabilities return `409 Conflict` before work is accepted.

### GET /api/nodes/{id}/actions/status
- **Auth**: Admin
- **Description**: Return the queued/running bounded maintenance action observed by the hub.

### GET /api/nodes/{id}/actions/reboot-status
- **Auth**: Admin
- **Description**: Return the managed node's bounded delayed-reboot state derived from completed reboot and cancellation tasks.

### GET /api/nodes/{id}/disk/cleanup
- **Auth**: Admin
- **Description**: Ask the online agent to measure only installation-owned fixed cleanup targets. No deletion occurs.

### POST /api/nodes/{id}/disk/cleanup
- **Auth**: Admin
- **Request**: `{"targets":["journal"],"confirmed":true}` with one to four unique IDs returned by the managed scan.
- **Description**: Execute the confirmed fixed targets through the disk-cleanup capability and return per-target status and measured reclaimed bytes. Arbitrary paths and shell commands are not accepted.

### PUT /api/nodes/{node_id}/deploy/{target_id}/domains/{domain}
- **Auth**: Admin (`RouteAdmin`; panel bearer or same-origin session authentication; mutation-limited)
- **Request**: Strict JSON with exactly `{"expected_revision":"absent","confirmed":true}`; `expected_revision` may instead be a lowercase 64-character SHA-256 revision. Unknown, duplicate, trailing, or oversized JSON is rejected. The body accepts no Nginx content, upstream URL, certificate path, Certbot argument, shell command, or secret.
- **Description**: Ensure one installation-owned managed project-domain mapping through the node's `deploy.domain.action` capability. The selected target, normalized hostname, and fixed loopback upstream come from the local deploy plan. `expected_revision` provides compare-and-swap protection: stale observations, drift, and competing desired revisions return `409 Conflict`; retrying the current active/enabled mapping returns a typed `changed: false` idempotent no-op. A missing mapping is provisioned only with `expected_revision: "absent"`.
- **Response (`200 OK`)**: `{ "changed": true|false, "observation": { ... } }`; the strict typed observation is an active, enabled mapping with target/domain, host ports, the fixed loopback upstream, TLS state, and a lowercase SHA-256 `revision`.
- **Response (`400 Bad Request`)**: The strict body, confirmation, revision, target, or hostname is invalid.
- **Response (`409 Conflict`)**: The node is offline, lacks `deploy.domain.action`, or the expected revision is stale/conflicting or the observed mapping drifted. No unsafe desired input is applied.
- **Response (`422 Unprocessable Entity`)**: The installation-owned managed deploy plan is invalid or cannot satisfy the domain operation.
- **Response (`502 Bad Gateway`)**: The managed agent failed or returned an invalid project-domain receipt; raw agent and Nginx details are not exposed.
- **Response (`504 Gateway Timeout`)**: The fixed project-domain task did not complete within the bounded 75-second wait.
- **Response (`default`)**: The managed project-domain ensure operation could not be completed safely.

### GET /api/nodes/{id}/metrics
- **Auth**: Admin (`RouteAdmin`; panel bearer or same-origin session authentication)
- **Description**: Return one fresh, read-only typed metrics snapshot from an online managed node. The node must advertise `metrics.read`; the hub queues one fixed empty-payload `metrics.read` task and waits up to 45 seconds. The response contains only bounded CPU, load, memory, network, root-disk, and observation timestamp fields; task envelopes, command output, paths, provider data, and secrets never cross this endpoint.
- **Response (`200 OK`)**: The strict typed object contains an RFC3339 UTC `observed_at`, `cpu.usage_percent` and `cpu.core_count`, one/five/fifteen-minute `load` values, memory byte counters and `usage_percent`, network `rx_bytes`/`tx_bytes`, and root-disk byte counters and `usage_percent`. Byte counters are non-negative; percentages are from 0 through 100; the observation timestamp must be recent and UTC.
- **Response (`404 Not Found`)**: `{"error":"not found"}` when the managed node does not exist.
- **Response (`409 Conflict`)**: `{"error":"managed_node_offline"}` when the node heartbeat is outside the online window, or `{"error":"capability_unavailable"}` when the node does not advertise `metrics.read`. Neither condition creates a task.
- **Response (`502 Bad Gateway`)**: `{"error":"managed_metrics_failed"}` when the agent task fails, is incomplete, or returns invalid typed snapshot data. Raw task errors, command output, paths, provider data, and secrets are never returned.
- **Response (`504 Gateway Timeout`)**: `{"error":"managed_metrics_timeout"}` when the fixed task does not complete within the bounded 45-second wait.

---

## 28. Integration Catalog

### GET /api/integrations/catalog
- **Auth**: Auth (`RouteProtected`)
- **Description**: Return the schema-v1 optional-integration catalog as a typed metadata object. This endpoint does **not** run provider probes, observe service health, or claim live integration health; use the respective integration API for runtime state.
- **Source**: The runtime embeds the authoritative [`extensions/catalog.json`](../extensions/catalog.json), validated by its [`extensions/catalog.schema.json`](../extensions/catalog.schema.json); the API exposes metadata from that asset without secret values.
- **Response (`200 OK`)**: The body contains `schema_version: 1`, typed `documentation`, and `entries`. The schema-v1 catalog contains **at least 15 required core entries; additive entries allowed**; clients should treat each entry's metadata contract as authoritative rather than infer health from the count.
- **Entry contract**: Each entry has typed `configuration`, `status`, and `evidence` objects, and may include typed `agent` metadata for managed-node support. `requirement` is `optional` or `feature_specific`; `classes` is drawn from `local_capability`, `managed_node_capability`, `provider_adapter`, and `client_surface`; `targets` is drawn from `local_host` and `managed_node`.
- **Configuration boundary**: `non_secret_keys`, `secret_key_names`, and `secret_file_refs` contain names or protected references only. Secret values and file contents are never returned by this endpoint.
- **Status boundary**: `status.canonical_states` is the exact ordered trio `not_configured`, `unavailable`, `healthy`; raw observations are explanatory mappings only and do not turn this metadata endpoint into a live-health probe.
- **`$schema` semantics**: The optional relative value `./catalog.schema.json` is a local source marker. Clients must not treat it as a promised dereferenceable URI; use the repository's [catalog schema](../extensions/catalog.schema.json) when validation is needed.
- **Response (`401 Unauthorized`)**: `{"error":"..."}` when the panel session is absent or invalid. A server-side embedded-catalog load failure returns the same `ErrorResponse` shape with `500`.

### GET /api/integrations/status
- **Auth**: Auth (`RouteProtected`)
- **Description**: Return a fresh schema-v1, local-only, read-only aggregate for every local integration entry in the catalog. Heyserver keeps fifteen required core probes: `process.pm2` (`pm2_inventory`), `cloudflare.dns` (`cloudflare_zone_list`), `container.docker` (`docker_info`), `web.nginx` (`nginx_readiness`), `firewall.ufw` (`ufw_readiness`), `tls.certbot` (`certbot_readiness`), `dns.bind9` (`bind9_readiness`), `runtime.php_fpm` (`php_fpm_readiness`), `database.local` (`database_readiness`), `storage.smartmontools` (`smartmontools_readiness`), `stalwart.mail` (`stalwart_readiness`), `mail.access` (`mail_access_readiness`), `backup.gdrive` (`gdrive_readiness`), `backup.snapshot.restic` (`restic_readiness`), and `notification.delivery` (`notification_readiness`). Reviewed additive catalog entries may contribute explicitly code-owned probes through the compile-time `api.Deps.IntegrationStatusProbes` seam; entries without a registered local probe remain in `unprobed`. Healthy always requires a fresh successful observation. Notification delivery stays unavailable until a persisted fresh successful delivery receipt exists. Failures and timeouts remain item-level, per-item HTTP 200 results. Managed-node status remains a separate endpoint. Raw provider errors, command output, paths, and secrets never cross this boundary.
- **Response (`200 OK`)**: The typed response contains `schema_version: 1`, an RFC3339 `observed_at`, `target.scope: "local_host"`, safe per-item `results`, an explicit `unprobed` list, and `partial`. Result states are exactly `not_configured`, `unavailable`, or `healthy`; failure items use only bounded `error_code` values and optional non-negative `duration_ms`. Result and unprobed IDs must use the catalog's provider-neutral ID syntax and be present in the current catalog. `partial` is true when an integration remains unprobed or a probe fails or times out; explicitly not-configured optional integrations do not make the aggregate partial.
- **Failure boundary**: Individual probe failures and timeouts remain item results in HTTP `200`; only an unavailable embedded catalog returns `500`. Raw provider errors, command output, and secret values are never returned.
- **Scope**: This endpoint reports local-host observations only. It does not provide live managed-node status.

### GET /api/nodes/{id}/integrations/status
- **Auth**: Admin (`RouteAdmin`; panel bearer or same-origin session authentication)
- **Description**: Return one fresh schema-v1, read-only managed-node observation. The selected online node must advertise `integration.status`; the agent runs exactly one bounded batched task containing the fixed `process.pm2`/`pm2_inventory` and `container.docker`/`docker_info` probes. Concurrent requests for the same node coalesce onto one queued or running task, and the panel waits at most 45 seconds.
- **Response (`200 OK`)**: The strict typed response contains `schema_version: 1`, an RFC3339 `observed_at`, `target` with `scope: "managed_node"` and the selected `node_id`, exactly two `results`, and `partial`. Result IDs are exactly `process.pm2` and `container.docker`; probe IDs are exactly `pm2_inventory` and `docker_info`; states are exactly `not_configured`, `unavailable`, or `healthy`; and optional `error_code` values are only `not_configured`, `probe_failed`, or `timeout`. `duration_ms` is an optional non-negative integer.
- **Response (`404 Not Found`)**: `{"error":"not found"}` when the managed node does not exist.
- **Response (`409 Conflict`)**: `{"error":"managed_node_offline"}` when the node heartbeat is outside the online window, or `{"error":"capability_unavailable"}` when the node does not advertise `integration.status`. Neither condition persists a task.
- **Response (`502 Bad Gateway`)**: `{"error":"managed_status_failed"}` when the agent reports its fixed fatal failure, the task is incomplete, or the returned typed status is invalid or mismatched. The endpoint never exposes raw task/provider errors, command output, paths, secrets, or process/container inventory.
- **Response (`504 Gateway Timeout`)**: `{"error":"managed_status_timeout"}` when the single batched task does not complete within the bounded 45-second wait.

### GET /api/nodes/{id}/profile
- **Auth**: Admin (`RouteAdmin`; panel bearer or same-origin session authentication)
- **Description**: Return the admin-owned desired deployment capability profile together with the latest raw node observation. `desired.state` is `not_configured` or `configured`; an unconfigured node has `desired.revision: 0` and `desired.profile: null`. A configured profile is returned as the fixed seven-field object, including when every capability is disabled.
- **Response (`200 OK`)**: The response contains `nodeId`, `desired`, `observed`, and `apply`. `observed.capabilities` is the raw capability array advertised by the node. `observed.profileState` is `not_reported`, `not_configured`, `pending_restart`, `applied`, or `failed`; capable nodes also expose the observed revision and a closed safe error code. `apply.state` is `manual_required` with reason `self_apply_not_supported` when the node lacks `agent.profile.apply`, otherwise one of `not_requested`, `queued`, `running`, `awaiting_heartbeat`, `applied`, `failed`, or `drifted`.
- **Response (`404 Not Found`)**: `{"error":"not found"}` when the managed node does not exist.

### POST /api/nodes/{id}/profile/apply
- **Auth**: Admin (`RouteAdmin`; authenticated mutation limiter applies)
- **Request**: Strict JSON with exactly `expectedRevision` (a positive integer) and `confirmed` (`true`). The request contains no profile, task payload, command, path, token, or environment value.
- **Description**: Queue the panel-owned desired profile revision for an online node that advertises `agent.profile.apply`. The hub constructs the fixed `agent.profile.apply` task and canonical profile wrapper from its own persisted desired state; callers cannot replace either value.
- **Response (`202 Accepted`)**: The profile task was queued or is awaiting the agent's next heartbeat. The response remains the full `AgentProfileResponse` with `apply.state` of `queued`, `running`, or `awaiting_heartbeat`; task completion is not treated as proof that the unit restarted or that the profile is active.
- **Response (`200 OK`)**: The latest accepted heartbeat already reports `observed.profileState: applied` with `observed.profileRevision` matching the requested `desired.revision`; no duplicate task is created. This is the verified-applied response.
- **Response (`400 Bad Request`)**: `{"error":"invalid request body"}` for unknown, duplicate, missing, or incorrectly typed fields, or `confirmed` other than `true`.
- **Response (`404 Not Found`)**: `{"error":"not found"}` when the managed node does not exist.
- **Response (`409 Conflict`)**: `{"error":"profile_not_configured"}`, `{"error":"stale_profile_revision"}`, `{"error":"profile_apply_in_flight"}`, `{"error":"profile_apply_capability_unavailable"}`, or `{"error":"node_offline"}`. None of these conditions creates a task.
- **Response (`429 Too Many Requests`)**: `{"error":"..."}` when the shared authenticated mutation rate limit is active.
- **Lifecycle boundary**: A completed `agent.profile.apply` task is a transport acknowledgement, not proof of application. Only a subsequent accepted heartbeat with `observed.profileState: applied` and `observed.profileRevision` equal to the panel-owned `desired.revision` proves that the requested profile is applied; a mismatched revision remains drift or pending state.
- **Secrets and task boundary**: Audit data records only the node and desired revision. Agent task results and heartbeat observations retain only state, revision, and safe error codes; raw paths, command output, token content, and environment-file content never cross this API.

### PUT /api/nodes/{id}/profile
- **Auth**: Admin (`RouteAdmin`; authenticated mutation limiter applies)
- **Request**: Strict JSON with exactly `profile` and non-negative `expectedRevision`. `profile` has exactly these required fields: `allowDeployRead`, `allowDeployActions`, `allowDeployDomainRead`, `allowDeployDomainActions`, `deployPlansFile`, `deployAcmeWebroot`, and `deployWriteRoots`. The first four are booleans. Every non-empty path must match ASCII `^[A-Za-z0-9._/+:-]+$`, be a clean absolute path other than `/`, and be no longer than 4096 bytes; file and directory values cannot have a trailing slash. `deployWriteRoots` is an array of at most 16 unique non-empty paths.
- **Profile dependencies**: `allowDeployActions` and `allowDeployDomainRead` require `allowDeployRead`; `allowDeployDomainActions` requires `allowDeployDomainRead`. An all-false, empty-path profile is valid and means configured-but-disabled.
- **Description**: Compare-and-swap updates the panel-owned desired profile only. `expectedRevision: 0` targets a node without a stored profile; each successful write advances the non-negative revision. Before persistence, the hub constructs the canonical apply wrapper for the prospective revision and rejects it if it exceeds the agent's inclusive 16 KiB document limit. Updating desired state does not implicitly restart the agent or enqueue a task; use the explicit `POST /api/nodes/{id}/profile/apply` operation after reviewing the saved revision.
- **Response (`200 OK`)**: Returns the saved `AgentProfileResponse` with top-level `nodeId`; no agent task is created by the update itself.
- **Response (`400 Bad Request`)**: `{"error":"invalid profile"}` for unknown/trailing JSON, missing fixed fields, invalid paths or roots, dependency violations, or a negative revision.
- **Response (`404 Not Found`)**: `{"error":"not found"}` when the managed node does not exist.
- **Response (`409 Conflict`)**: `{"error":"stale_profile_revision"}` when `expectedRevision` no longer matches the stored revision; the newer desired state is not overwritten.
- **Response (`429 Too Many Requests`)**: `{"error":"..."}` when the shared authenticated mutation rate limit is active.
- **Secrets and task boundary**: Profiles contain no token, secret, executable, or arbitrary key. Successful mutations record only the node and new revision in audit data; they never create or expose an agent task.
