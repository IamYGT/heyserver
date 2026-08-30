# Changelog

Notable HServer changes are recorded here using the
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) structure. The project
is pre-1.0; public release acceptance remains in progress.

## [Unreleased]

## [0.9.6] — 2026-08-30

### Changed

- The next immutable patch candidate is `v0.9.6`; it is not tagged or
  released. The public `v0.9.5` tag points to protected `main` commit
  `b2af1591f7a848acd71bbe54bc4f70fbffe99373`, but tagged run `#33285788628`
  failed both `Managed Agent Lifecycle` jobs because the lifecycle fixture
  posted onboarding step 6 while the canonical maximum is step 5. The tag and
  run remain historical failed-release evidence; no successful `v0.9.5` or
  `v0.9.6` release, clean independent-VM acceptance, or live rollout is
  claimed.

### Fixed

- Managed-agent lifecycle acceptance now submits canonical onboarding step 5
  instead of obsolete step 6, matching the API's allowed 0–5 range.

## [0.9.5] — 2026-08-30

### Added

- Managed-node live metrics now have a provider-neutral, capability-scoped
  `metrics.read` path across the agent, hub, API, CLI, web, and TUI consumers,
  with strict freshness and shape validation plus guest-time-safe CPU
  accounting; OpenAPI contract revision 70 now covers 442 routes and 318
  schemas.
- Admin-only managed project-domain ensure now has a strict
  `PUT /api/nodes/{node_id}/deploy/{target_id}/domains/{domain}` API with
  revision compare-and-swap, typed `changed`/`observation` receipts, an
  idempotent no-op for an already active mapping, and bounded 400/409/422/502/
  504 failure mapping. Arbitrary Nginx/upstream input is excluded; OpenAPI
  contract revision 71 now covers 443 routes and 321 schemas.
- Historical amd64 public-source acceptance from source HEAD `fd227828` remains
  recorded as prior validation evidence; it is not current release or rollout
  evidence.
- The canonical public repository is now
  [`IamYGT/heyserver`](https://github.com/IamYGT/heyserver); immutable public
  source commit `adaccb23adf9720141d721970590de3a82fd17b5` produced the first
  fully green public main CI matrix run `#33283277809`. The current fully green
  public main CI run is `#33283728373`. Branch protection was enabled after
  these runs and is currently active. It was initially published at
  `53df8ba` with private/public tree parity `3719df8`.
- The initial active release signer is prepared in private commit `df0a5070`,
  and the `HSERVER_RELEASE_SIGNING_KEY` Actions secret is configured; public
  signer PR #7 remains pending.
- Docker quick-evaluation documentation now covers first login: read
  `HSERVER_ADMIN_EMAIL` and the generated `HSERVER_ADMIN_PASS` locally from the
  mode-`0600` `.env`, open `http://localhost:3085`, and complete onboarding;
  `init-env.sh` never prints the generated password.
- Panel and managed-agent executable update mutations now require a verified
  signed release manifest at the service/API and CLI/TUI/web boundaries;
  unsigned status remains observable, but staging, installation, and upgrade
  fail closed.

### Changed

- Canonical public repository selection and publication are complete. The
  first fully green public main CI matrix run was `#33283277809`; the current
  fully green public main CI run is `#33283728373`. Branch protection was enabled
  after these runs and is currently active. The initial active signer is
  prepared in private commit `df0a5070`, and the `HSERVER_RELEASE_SIGNING_KEY`
  Actions secret is configured; public signer PR #7 remains pending. The public
  `v0.9.5` tag points to protected `main` commit
  `b2af1591f7a848acd71bbe54bc4f70fbffe99373`, but tagged run `#33285788628`
  failed both `Managed Agent Lifecycle` jobs because the lifecycle fixture
  posted onboarding step 6 while the canonical maximum is step 5. The tag and
  run remain historical failed-release evidence. The first successful signed
  release, GitHub Release, tagged lifecycle/provenance acceptance, clean
  independent-VM acceptance, and live rollout remain pending. The next
  immutable patch candidate is `v0.9.6`; no release or rollout is claimed for
  it. The live `v0.9.3` rollout is not the current source, and these source
  results do not imply a live deployment or public release. Earlier failed runs
  `#33251833442` and `#33281342435` are historical CI evidence only.

## [0.9.4] — 2026-08-29

### Added

- `hserverctl` now covers local disk analysis, directory-size, I/O, largest-file,
  mount, SMART, listing, and usage diagnostics without inventing managed-node
  capabilities.
- The CLI now exposes bounded Stalwart service/status/log/queue/domain
  operations and panel-local blacklist/whitelist management; every mutation
  requires explicit confirmation.
- OpenAPI revision 68 publishes redacted Stalwart service
  status/overview/version/listener/storage read contracts.
- `make dev-check` and `make dev-setup` now derive the exact minimum Go version
  from `go.mod` (currently Go 1.26.1 or newer), require Python 3, and keep setup
  from downloading dependencies when required tools are missing or too old.
- Managed-node PHP-FPM, PM2, and Docker container read endpoints now publish
  strict, credential-free typed OpenAPI projections with capability checks and
  bounded waits in contract revision 69 (312 schemas).
- `hserverctl` now exposes panel-local, read-only PHP diagnostics for
  extensions, INI values/diffs/directives, bounded error/slow logs, security
  profiles/status, and Composer version/outdated observations.
- The interactive `hserverctl` Mail section now shows service status, recent
  logs, queue, domains, and accounts; retry and delete actions require a fresh
  queue re-observation and explicit confirmation.
- Contributor guidance now recommends the nearest focused Go or frontend test
  for fast feedback and distinguishes those checks from the `make ci-fast` and
  `make ci-pr` contributor gates.
- The interactive `hserverctl` Security section now inventories persistent
  panel-local IP blacklist and whitelist entries and supports bounded,
  explicitly confirmed add/delete flows without issuing managed-node requests.
- The interactive Disk section now exposes panel-host-only mounts, usage,
  largest-entry, I/O, SMART, and deep-analysis status views; starting deep
  analysis requires explicit confirmation and managed targets remain unsupported.
- Public installer trust is now anchored to raw-Ed25519-key SHA-256 fingerprints
  and a detached `bootstrap-install.sh.sig` verified before privilege entry;
  the initial active signer is prepared in private commit `df0a5070`, the
  `HSERVER_RELEASE_SIGNING_KEY` Actions secret is configured, and public signer
  PR #7 remains pending before tagged staging.

### Changed

- Login, user management, and host-security pages now distinguish identity,
  permission, service-unavailable, and operation-failure states and keep
  mutations closed until the canonical current-user role is known.
- Local and hosted builds now embed frontend assets through a Go overlay, so a
  clean source checkout retains truthful `vcs.modified=false` provenance while
  dirty developer sources remain marked dirty.
- Public launch guidance now requires the first public tag to use a previously
  unused version built exactly once from protected public history.
- The Developer API page now reports distinct safe states for permission denial
  (401/403), a missing contract (404), temporary unavailability (network/5xx),
  and other operation failures without exposing backend errors.
- Public signer PR #7 and its protected publication remain a public-launch
  prerequisite; the initial active signer is prepared in private commit
  `df0a5070` and the `HSERVER_RELEASE_SIGNING_KEY` Actions secret is configured,
  but tagged staging, the first signed release, and public launch are not yet
  ready.

### Fixed

- Extension-catalog tests now explicitly preserve additive community entries
  while rejecting duplicate core IDs and invalid additive route, evidence, or
  state contracts.

## [0.9.3] — 2026-08-29

### Added

- Read-only `hserverctl disk overview [--node NODE]` now exposes separate local
  and managed-node disk inventories without probing the wrong host boundary.
- Notification-channel read endpoints now publish a redacted, credential-free
  `NotificationChannel` schema with canonical state and detail values in
  OpenAPI contract revision 67.
- A provider-neutral in-tree extension scaffold workflow now creates and checks
  reviewable packets with `scripts/new-extension.sh create|check`; the local
  `make test-shell` gate covers the scaffold contract.

### Changed

- Release archives now package the complete operator documentation set, and
  release-package checks fail when a public Markdown or JSON document is omitted.
- The Debian 12 native release gate now runs for both amd64 and arm64 release
  jobs, verifies the container architecture, and is also covered by the local
  shell-test gate.
- Panel and CLI release binaries now embed the complete 40-character source
  commit in local and hosted builds instead of an abbreviated identity.

### Fixed

- Monitoring charts now distinguish a successful empty history (`No historical
  samples yet`) from loading and from an insufficient-history state.

## [0.9.2] — 2026-08-29

### Added

- Public release assembly now publishes the checksum-first `public-install.sh`
  wrapper and its adjacent SHA-256 asset for Git-free installation.
- Native lifecycle preflight and CI now support Debian 12 alongside Ubuntu
  24.04, with the documented package-manager and prerequisite contract.
- Read-only `hserverctl mail accounts` and `mail aliases` inventory commands
  now support exact domain filters, with their safe response schemas and
  routes promoted in OpenAPI revision 66.

### Fixed

- Provider-network evidence now uses schema-v3 receipts bound to the panel
  build commit; capability-denial probes accept `backup.read` and send the
  required confirmation marker.

## [0.9.1] — 2026-08-29

### Added

- Tagged release builds now require a matching `major.minor.patch`
  changelog section before package publication; an `Unreleased` section alone
  cannot be used as release notes.
- Optional integrations now have an authoritative machine-readable catalog and
  schema under `extensions/`. The catalog verifier runs in shell and public
  source acceptance, release archives carry both JSON files, and new integration
  contributions must update their catalog entry and focused test.
- Public source acceptance now runs backend and frontend unit suites from the
  exported Git-free tree, while the checked-in public-install wrapper verifies
  bootstrap/key checksums before privilege entry. Release manifest generation
  now requires credential-free HTTPS URLs.
- Panel and managed-agent upgrades now retain a bounded, configurable recovery
  window and preserve the active rollback marker. CLI completion discovers the
  nested agent-update, project-domain, and encrypted-snapshot workflows across
  Bash, Zsh, and Fish.
- Contributor routing now links directly to checked-in issue forms and a
  role-based maintainer area map, with consistent private conduct and security
  escalation paths packaged in release archives.
- Fresh native and signed-bootstrap installations now accept an optional
  `--vhosts-root ABSOLUTE_PATH`, validate it before host mutation, persist the
  host path unchanged, create a missing site root safely, preserve existing
  content through rollback and uninstall, and otherwise retain the explicit
  provider-neutral `not_configured` state.
- Public release assembly now packages the complete operator and contributor
  documentation set, publishes a checksummed Ed25519 verification key beside
  the signed manifest, supports checksum-first bootstrap key files, and refuses
  tagged publication unless the tag commit is contained in the repository's
  protected `main` history. Fresh native and managed-agent configuration leaves
  site roots, PM2 identities, and optional mail-provider settings explicitly
  unconfigured instead of selecting provider-specific host paths.
- `hserverctl uptime` now exposes authenticated monitor summary/inventory,
  exact monitor details, heartbeat history, computed availability, incidents,
  status-page inventory, confirmed HTTP/TCP/ping/DNS monitor creation,
  partial monitor updates with explicit clear operations, confirmed active
  checks, pause/resume/deletion, confirmed local-domain import, global uptime
  settings, and complete status-page creation/update/deletion. Fixed endpoints,
  protected request-header/body files, bounded values and waits, portable target
  syntax, normalized HTTP status ranges, deduplicated identities, and pre-request
  confirmation make the monitoring surface safe for scripts.
- Uptime monitor, settings, and status-page APIs now apply bounded strict input,
  atomic status-page mappings, runtime retention/compaction settings, and honest
  missing-resource responses. HTTP checks accept JSON-object or line-oriented
  request headers; DNS edits preserve record type and expected value; generated
  OpenAPI revision 52 publishes these request and response contracts.
- `hserverctl deploy domains` and `deploy domain create|health|tls|delete`
  now provide the complete local project-domain lifecycle through fixed API
  identities. Creation accepts only a validated hostname, Compose service, and
  loopback-published host port; health performs the active bounded upstream
  probe; TLS uses the transactional Certbot/Nginx boundary; and deletion turns
  the API's empty `204` into a stable JSON CLI receipt. Every mutation requires
  confirmation, uses a bounded timeout, and rejects invalid identities, ports,
  service names, email, or action-only flags before network access.
- `hserverctl deploy environment list|set|delete` now exposes the complete local
  Compose environment lifecycle without creating a secret read channel. Lists
  return names only, writes require explicit confirmation and a protected
  bounded UTF-8 value file, and deletions address one validated key. Invalid
  permissions, keys, encodings, storage-breaking characters, target identities,
  or missing confirmation are rejected before mutation, while successful API
  responses remain value-free.
- OpenAPI contract revision 51 makes deployment-target replacement explicit and
  concurrency-safe: `expectedUpdatedAt` is required, stale observations return
  `409 Conflict`, an empty `webhookToken` preserves protected secret state, and
  the new `clearWebhookToken` flag is the only empty-value deletion path.
  Secret replacement, provider transitions, automatic deployment, unavailable
  secret references, SQLite reference storage, and response redaction retain
  distinct validation and failure behavior.
- `hserverctl deploy target update` now refreshes the secret-free inventory,
  overlays only explicitly selected fields, and sends the exact observed target
  revision. It supports Compose/script transitions, bounded script and protected
  webhook-token files, explicit token clearing, active/auto-deploy booleans, and
  refuses no-op, stale, unconfirmed, credential-bearing, or executor-incompatible
  updates without exposing signing material.
- `hserverctl deploy target create|delete` now provides a confirmation-bound
  local deployment-target lifecycle for Compose and script executors. Creation
  validates target identity, absolute project boundaries, credential-free Git
  remotes, branches, executor-specific inputs, and webhook configuration before
  network access; scripts are read from bounded UTF-8 files and optional signing
  tokens from protected files without printing secrets. Deletion uses an exact
  positive target ID and retains the API's project-domain and staging-child
  conflict guards.
- `hserverctl services` now provides local and managed-node service inventory,
  bounded local journal reads, and confirmed `start`, `stop`, or `restart`
  actions. Managed mutations refresh node availability and `service.action`
  capability before creating a task, wait for its terminal receipt, and retain
  the agent's installation-owned service allowlist. CLI help indentation for
  the existing Cloudflare and BIND commands is also normalized.
- OpenAPI contract revision 50 distinguishes an unavailable configured PGM
  backup root from a missing selected backup across inventory, file listing,
  and restore operations. A missing, inaccessible, or non-directory root now
  returns `503 Service Unavailable`; a missing child backup remains `404`, and
  healthy empty inventories remain non-null arrays.
- `hserverctl domains create` now provisions local PHP, static, and reverse
  proxy domains through the same strict rev49 contract as the panel. It exposes
  explicit PHP/FPM, SPA, proxy/PM2, certificate, DNS, document-root, isolated
  Linux-user, and timeout choices; requires `--confirm`; rejects irrelevant
  type-specific flags; and validates paths, ports, runtime allowlists, PM2
  pairs, and issuance email before any request. Local CLI domain operations now
  require exact normalized hostnames while managed-node actions retain their
  observed config-identity boundary.
- OpenAPI contract revision 49 publishes the complete seven-operation local
  Domains surface with stable hostname string identities, non-null inventories,
  explicit provisioning capability states, strict preflight/create/toggle
  bodies, exact delete query semantics, and typed mutation receipts. Domain
  inputs are normalized without silently deleting caller characters, existing
  Nginx configurations are never replaced, missing domains return `404`, absent
  configured inventories return `503` instead of a false empty list, and local
  mutations share the bounded mutation limiter. Create/check bodies are capped
  at 64 KiB, toggle bodies at 4 KiB, duplicate creates return `409`, nested
  subdomain file cleanup remains under the configured vhost root, and uptime
  cleanup matches only the exact hostname.
- OpenAPI contract revision 48 publishes all seven generic and portable
  Settings operations with a closed 12-key installation-portable allowlist,
  typed values, bounded schema-v1 bundles, non-mutating previews, and confirmed
  imports. Generic reads no longer expose credential-bearing internal records
  such as provider OAuth application state; generic writes and deletes cannot
  address onboarding or service-owned keys. Settings JSON is strict and capped
  at 128 KiB, portable oversize failures return `413 Payload Too Large`, admin
  email is capped at 254 bytes, and domain creation now consumes the canonical
  `adminEmail` setting for its default certificate email.
- OpenAPI contract revision 47 publishes the complete bootstrap identity
  surface: health, bounded onboarding state, password and TOTP login, current
  user, logout, TOTP setup/status/activation/disablement, and single-use
  recovery-code login. Authentication JSON is strict and capped at 4096 bytes,
  secret-bearing responses are never cached, browser sessions use the canonical
  proxy-aware strict cookie, onboarding steps are limited to 0–5, and an
  uninitialized settings service returns `503` instead of a false healthy
  response. Recovery login now accepts the frontend's canonical
  `recovery_code` field, and every login path records failures in the same
  soft-ban limiter used by route middleware.
- OpenAPI contract revision 46 publishes the complete eight-operation local
  System read/update surface: observed host information, non-null metric and
  service inventories, fixed-allowlist journald entries, explicit release
  discovery states, nullable latest-stage observation, verified staging, and
  confirmation-bound installation. Empty/repeated service-log limits are
  rejected, staging accepts no body, installation JSON is capped at 4096 bytes
  with `413 Payload Too Large`, and optional runtime/interface inventories no
  longer serialize as `null`.
- OpenAPI contract revision 45 publishes all 12 local database-management
  operations: per-engine readiness, database/user/table inventories, strict
  create and literal-confirmation drop payloads, bounded transaction-enforced
  read-only queries, administrator-only secret-bearing PGM credential
  responses, actionable backup inventories, and root-confined restore
  contracts. Ambiguous or oversized JSON is rejected, `write_mode` no longer
  pretends to enable writes, empty arrays remain non-null, restore symlink/path
  escapes are refused, and fresh installations use the HServer-owned
  `${HSERVER_DATA_DIR}/pgm-backups` root while preserving existing configured
  paths during upgrades.
- OpenAPI contract revision 44 completes the full 25-operation local Deploy API
  family with project-domain inventory/creation/deletion, active loopback health
  probes, and transactional TLS enable/disable contracts. Domain and TLS JSON
  inputs are strict and capped at 8 KiB, path-only delete operations reject
  request bodies, TLS activation accepts an empty body, and ACME email input is
  capped at 254 bytes.
- OpenAPI contract revision 43 now publishes the complete local Compose service
  and write-only environment surface: strict service inventories, bounded
  timestamped logs, fixed lifecycle action receipts, secret-free environment
  metadata, strict variable mutations, and explicit `400`/`404`/`409`/`413`/
  `502`/`503` failures. Service actions reject request bodies, while environment
  writes reject unknown fields and trailing JSON values.
- OpenAPI contract revision 42 now publishes the core local deployment
  lifecycle: strict target create/update payloads, secret-free target
  inventories, read-only preflight reports, asynchronous manual deploy and
  rollback receipts, bounded history filters, and separate run-log responses.
  Target names and path identifiers are now bounded, ambiguous JSON is
  rejected, and invalid history filters return `400 Bad Request` instead of
  silently widening the query.
- OpenAPI contract revision 41 now publishes the complete local Firewall
  surface: structured UFW/iptables readiness, non-null rule inventories,
  bounded creation, SSH-safe deletion receipts, explicit desired-state
  toggles, and observed `400`/`403`/`500` failures. `/api/firewall/status` now
  normalizes an absent rule slice to `[]` instead of emitting `null`.
- OpenAPI contract revision 40 now publishes the complete local PM2 surface:
  process inventory/detail, bounded logs, fixed lifecycle receipts, deployment,
  persistence, and explicit unconfigured/internal failure states. Invalid PM2
  log line values now return `400 Bad Request` before PM2 configuration is
  inspected instead of silently reverting to 100 lines.
- OpenAPI contract revision 39 now publishes the complete local Docker surface:
  readiness, container and image inventories, bounded logs, fixed container
  actions, image pull/removal receipts, query bounds, and observed `502`/`503`
  failure states. Generated clients can distinguish an empty inventory from an
  unavailable Docker boundary.
- OpenAPI contract revision 38 now publishes the complete local Cron surface:
  readiness, user-job and system-file inventories, create/update/delete
  receipts, owner query semantics, and unavailable-client failures. All Cron
  response schemas set `additionalProperties: false` for generated-client
  stability.
- `hserverctl doctor` can now require an exact managed-node architecture, its
  JSON and text reports expose the observed architecture, and the interactive
  fleet view distinguishes `agent/amd64` from `agent/arm64`. The native managed
  agent lifecycle gate requires the runner architecture instead of accepting a
  wrong or missing heartbeat value.
- Managed-node heartbeats now report the agent binary architecture, the fleet
  overview exposes it without breaking legacy inventory, and schema-v2
  provider-network receipts bind panel, CLI, agent, and managed-node release
  identities. The verifier retains explicit schema-v1 inspection compatibility
  while requiring schema v2 for current release evidence.
- Provider-network receipts can now be signed as exact-byte detached Ed25519
  artifacts with a protected operator-held key. Verification requires an
  explicit signature/public-key pair, reports the public-key fingerprint, and
  distinguishes `verified` from `not_checked`; tampered receipts, wrong keys,
  unsafe artifact permissions, symlinks, and accidental signature omission are
  covered by the public fixture.
- Authenticated `hserverctl cloudflare` zone, DNS record, email-routing, cache
  purge, and installation-owned mail DNS reconciliation commands. Every
  provider mutation is confirmation-gated, and partial record updates preserve
  omitted fields through a read-before-replace flow.
- Provider-neutral hub and managed-node agent with outbound enrollment,
  heartbeat, task history, protocol versioning, and capability discovery.
- Agent capabilities for bounded host actions, process signals, terminal relay,
  disk cleanup, logs, containers, nginx, domains, SSL, PHP-FPM, PM2, cron,
  firewall, databases, backups, deploy plans, and file roots.
- Native panel and agent lifecycle installers with protected configuration,
  versioned recovery snapshots, health checks, rollback, and non-purge
  uninstall.
- Checksummed portable panel-state bundles that keep WAL-safe SQLite data and
  referenced notification credential files together, with strict archive
  validation and automatic database-plus-secret rollback on failed activation.
- Version-matched panel lifecycle installer and doctor persisted under
  `/usr/local/libexec`, included in upgrade snapshots and rollback, plus
  password-free first-access guidance for bootstrap and manual installs.
- Installation doctor for host compatibility, systemd reachability, protected
  file permissions, service state, and panel health.
- Checksummed `amd64` and `arm64` release-package workflow containing both
  binaries, lifecycle tools, examples, API inventory, and troubleshooting docs.
- Installation-owned reusable deployment templates with strict file validation,
  admin API and CLI inventory, add-target form prefilling, packaged
  provider-neutral starters, and fresh-install seeding that never overwrites an
  existing template directory or changes it during upgrade.
- Production-derived staging deployment targets with symlink-aware isolated
  project directories, explicit non-copy receipts for environment values,
  webhook secrets, auto-deploy, domains, TLS, and DNS state, guarded parent
  deletion and path updates, plus admin API, panel, CLI, and OpenAPI contracts.
- S3-compatible restic snapshot destinations with client-side encryption,
  protected access/secret-key files, HTTPS and bucket validation, read-only
  remote health probing, explicit integration states, panel/CLI selection, and
  provider-capability-aware repository purge behavior.
- Scriptable encrypted-snapshot vhost discovery, explicitly scoped staging
  restore, and exact-observed-repository purge commands with local preflight
  validation and server-side capability enforcement.
- Generated complete API route inventory with CI drift detection.
- OpenAPI agent wire contracts for authenticated heartbeat acceptance, empty or
  claimed task polling, and bounded terminal task results, including the
  matching node-identity header and failed-result requirement.
- Frontend API route contract verification that checks statically resolvable
  client methods and paths against the backend manifest, expands annotated
  shared route helpers at their call sites, and reports any remaining dynamic
  calls as explicitly unverified.
- Frontend request-payload verification for promoted OpenAPI operations,
  including required and unknown fields, TypeScript value categories, finite
  enums, constants, and statically visible array bounds.
- Provider-neutral onboarding, installation, support, vulnerability-reporting,
  contribution, roadmap, and public-launch documentation.
- Versioned in-tree extension boundary, integration proposal form, and packaged
  contributor guide for provider-neutral community capabilities.
- Read-only `make dev-check` contributor diagnostics and a matching
  `make dev-setup` dependency bootstrap that validates Go, Node.js, npm, Git,
  Make, and CGO support before installing locked repository dependencies, while
  reporting Docker and database drill tools as separate optional capabilities.
- Clean source exporter for seeding a public repository without operational Git
  history.
- Signed-manifest bootstrap installer for one-command native setup, with
  architecture, size, checksum, bounded extraction, version, and ELF validation
  before packaged lifecycle execution.
- Server selector and node-scoped controls in the central web interface.
- Restore artifact preflight that validates complete database, file, and full
  backup contents before the confirmation control is enabled.
- Tagged native `amd64` and `arm64` acceptance now executes an authenticated
  writable local PTY marker and a bounded temporary-file cleanup, then proves
  the maintenance lock returned to inactive.
- Tagged native acceptance authenticates through the installed `hserverctl`
  using a protected password file, requires its token file to be mode `0600`,
  and exercises read-only host status and disk scan commands.
- Managed-agent acceptance now proves a disabled `host.action` request returns
  `409 Conflict`, names the missing capability, and creates no queued task.
- Tagged `amd64` and `arm64` release acceptance now places the hub and agent in
  separate no-default-route Linux network namespaces, proves outbound-only hub
  reachability, capability discovery, observed process inventory, a confirmed
  stable-identity process signal, and task-free denial of an unadvertised host
  action before release publication.
- Provider-network candidate acceptance now has an explicit bounded-marker
  guard and a provider-neutral runner that verifies the public HTTPS path,
  native panel-host identity, a separate managed-node kernel, real remote CLI
  PTY input, observed stable-identity process termination, and task-free
  disabled-capability rejection even when remote host actions are enabled. The
  adaptive marker is capped at 96 MiB, requires four times its allocation to
  remain available, and expires after 300 seconds. A mode-`0600` schema-v2
  receipt records panel/CLI/agent identities, both architectures,
  compatibility modes, bounded allocation, and boolean checks, never
  credentials or raw inventory.
- Managed-agent terminals now classify Linux PTY `EIO` after shell exit as a
  normal close instead of reporting a successful `exit` command as an
  unexpected terminal failure.
- Provider-network receipts now have a reusable strict verifier for mode and
  ownership, exact schema-v1/v2 fields, schema-specific checks, timestamp
  freshness, release/node/origin identities, marker bounds, and compatibility
  policy. Legacy evidence requires explicit overrides; structural verification
  is not presented as cryptographic provider attestation.
- Full-screen `hserverctl ui` control center with local/managed server
  switching, responsive host overview cards, service and stable-identity
  process controls, capability-aware maintenance, measured disk cleanup,
  local/managed container and PM2 lifecycle controls, PM2 process-list
  persistence, scrollable log viewing, periodic refresh, keyboard help, and
  explicit `Y` mutation confirmation.
- The CLI control center now includes a dedicated encrypted-snapshot dashboard
  with provider readiness, bounded remote inventory, confirmed creation,
  policy-preserving destination switching, complete or manifest/vhost-selective
  restore into the fixed staging directory, partial inventory preservation, and
  an explicit local-hub boundary for managed targets.
- The CLI control center now includes a local BIND DNS dashboard with explicit
  readiness states, zone and record browsing, complete configuration-check
  diagnostics, guided zone/record creation, selected record and SOA editing,
  confirmed reload, and stale-observation-protected zone/record mutations.
  Managed targets state the local-only boundary instead of issuing a hub-local
  command for a remote node.
- The CLI control center now includes a deployment lifecycle dashboard with
  local preflight and revision inspection, confirmed deploy and rollback,
  capability-scoped managed actions, recent run or job state, bounded output,
  stale-plan rejection, active-job polling, and contextual quick actions.
- The CLI control center now includes a central Alerts dashboard with distinct
  `not_configured`, `unavailable`, `degraded`, `configured_disabled`, and
  `healthy` states, partial
  channel/rule/history preservation, recent event inspection, confirmed channel
  tests and lifecycle actions, confirmed rule lifecycle actions, stale-state
  rejection, exact receipts, and contextual quick actions.
- The CLI control center now includes a central Cloudflare dashboard with
  distinct `not_configured`, `unavailable`, and `healthy` provider states,
  zone, DNS-record, and optional email-routing inspection, confirmed cache
  purge, proxy toggle, record deletion, installation-owned mail DNS
  reconciliation, fresh-observation rejection, exact receipts, and contextual
  quick actions. Managed targets state the central-panel boundary without
  issuing a substitute node request.
- Scriptable and interactive CLI audit history with bounded pagination,
  user/action/resource/time filters, selected-server scoping, periodic refresh,
  and control-safe local filtering across the newest loaded events.
- Scriptable `hserverctl users` inventory, creation, partial update, password
  replacement, and deletion with explicit mutation confirmation, protected-file
  or echo-disabled password input, strict IDs and roles, and a central-panel
  boundary that never substitutes managed-node operating-system users.
- The CLI control center now includes a central Users dashboard with bounded
  inventory and explicit totals, current-account and TOTP visibility, masked
  account creation and password-replacement forms, profile editing, confirmed
  role changes and deletion, fresh-observation rejection, exact response
  validation, contextual quick actions, and server-enforced current/final-
  administrator safeguards. Managed targets state the central-panel boundary
  without issuing a substitute request.
- Guided `hserverctl connect` first-use flow that authenticates and verifies an
  account before atomically creating a selected named context and independent
  protected token file, leaves no partial state on authentication failure, and
  prints the exact doctor and interactive-control-center next steps.
- Read-only `hserverctl context status` now probes every configured context (or
  explicitly selected names) with bounded parallel health checks and supports
  text or JSON output without exposing token-file references.
- Echo-disabled interactive password and on-demand TOTP prompts for
  `hserverctl connect` and `hserverctl login`, with protected-file inputs kept
  for non-interactive automation and explicit non-TTY guidance.
- Protected `hserverctl doctor --output` reports that refuse existing paths,
  write JSON or text with mode `0600`, preserve failed-check evidence before
  returning non-zero, and are exercised by the packaged native lifecycle gate.
- The CLI control center now includes a selected-server Updates dashboard with
  explicit release/signature/stage/operation states, local panel staging and
  installation, managed-agent upgrade and rollback, contextual quick actions,
  active-operation polling, fresh pre-mutation observation, exact receipt
  validation, and separate confirmation for every lifecycle mutation.
- Scriptable notification channel inventory, creation, partial update, explicit
  credential removal, provider delivery test, and deletion through
  `hserverctl`, with credential values accepted only from protected files.
- Canonical alert-rule web, API, and `hserverctl` management for CPU, memory,
  disk, certificate expiry, systemd unit, and failed-SSH-login conditions, plus
  strict paginated alert history and type-specific targets.
- Scriptable `hserverctl containers` and `hserverctl logs` command families
  with local/managed-node routing, JSON output, explicit mutation confirmation,
  fixed action vocabularies, bounded line counts, and source discovery.
- Scriptable `hserverctl pm2` inventory, detail, log, lifecycle, and local
  process-list persistence commands with target-specific action and line-count
  boundaries.

### Changed

- Panel-user creation and update now reject unknown or trailing JSON, require a
  non-empty mutation, and validate every supplied field before profile
  persistence. Profile and password changes now commit in one transaction, and
  deletion or demotion cannot remove the final administrator. The generated
  OpenAPI contract publishes closed create and update schemas, paginated
  inventory, credential-free user responses, final-administrator conflicts, and
  the empty deletion response; user listing honors its documented 200-record
  limit.
- Cloudflare DNS create and full-update payloads now share one normalized
  validation contract across the service, API, and CLI. Provider mutations
  reject unknown or trailing JSON, require explicit proxy state, enforce empty
  bodies where promised, and publish typed OpenAPI request and response schemas.
- Remote management no longer uses stored SSH passwords or a legacy SSH
  transport; desired operations flow only through fixed agent task schemas.
- Host paths, virtual-host roots, provider origins, PM2 identity, and backup
  locations are installation-owned settings rather than operator defaults.
- Optional integrations distinguish `not configured`, `unavailable`, and
  `healthy` states.
- Legacy `cpu`, `memory`, and `disk` alert types now migrate in place to the
  evaluator's canonical names. Alert mutations reject unknown or trailing JSON,
  preserve omitted update fields, validate type-specific thresholds and
  targets, and no longer expose an operator selector the evaluator ignores.
- Promoted backup, local disk-cleanup, managed disk-cleanup, node-enrollment,
  agent-task, local service-control, process-signal, domain-toggle, and cron
  mutation request bodies reject unknown JSON fields instead of silently
  ignoring unsupported operator input. Cron replacement also requires an
  explicit active state so an omitted boolean cannot disable a job.
- Firewall rule creation and desired-state requests now use closed JSON
  contracts with bounded actions, directions, and protocols. Rule deletion is
  documented as a positive integer path contract with last-SSH-rule safety.
- PM2 process creation now rejects unknown or ignored JSON fields, validates a
  portable application name, bounds execution mode and instances, and reports
  invalid operator input as `400 Bad Request`. Local and managed-node PM2
  action path parameters expose their fixed lifecycle vocabularies in OpenAPI.
- Docker image pulls now reject unknown JSON fields and accept only one bounded
  image reference. Local and managed-node container action path parameters
  expose their distinct fixed lifecycle vocabularies in OpenAPI.
- Backup schedule creation now rejects unknown, trailing, and ambiguous JSON;
  accepts exactly one cron or frequency/time source; and bounds retention aliases
  to one count field. Schedule deletion now requires the exact observed
  `rawLine` instead of silently choosing the first managed entry. The complete
  schedule list, create, and delete family is published in OpenAPI. Custom cron
  expressions retain their exact meaning instead of being labelled as a daily
  preset.
- Snapshot settings now use one complete, closed, validated, and atomically
  persisted replacement policy instead of sequential partial writes. Snapshot
  run rejects ignored request bodies, while restore accepts only an observed
  hexadecimal snapshot identity plus bounded logical manifest or vhost
  selectors, always resolving installation-owned roots and using the fixed
  staging target. The settings, run, and restore contracts are published in
  OpenAPI.
- Webmail no longer flashes `not configured` while settings are loading or
  exposes provider links and client guidance after a settings read failure;
  both missing and unavailable states now include contextual remediation.
- Root-disk SMART monitoring now resolves the observed physical device instead
  of assuming `/dev/sda`, and distinguishes loading, unsupported storage,
  observation failure, unknown health, and definite pass/fail results.
- Cron and Fail2Ban expose stable `healthy`, `not-installed`, `stopped`, and
  `unavailable` readiness states, with contextual remediation and mutations
  gated on observed host availability.
- The web terminal now verifies an `admin` account before mounting a writable
  WebSocket, distinguishes permission denial from account or managed-node
  observation failure, provides focused retries, and disables shell controls
  until both access and target readiness are known.
- Frontend builds use the committed npm lockfile and embedded assets are
  generated from the production Vite build.
- The About page reports build, health, transport, and writable terminal state
  without invented versions or security claims.
- Public project direction is Apache-2.0 community software with no current
  license-key, domain-limit, telemetry, or paid-tier gate.

### Fixed

- Uptime target validation now applies the outbound-host policy to DNS
  monitors, rejects unsupported monitor types, and requires a valid TCP port
  before a monitor can be stored or tested.
- Interactive Users forms now preserve space-key input from real terminals;
  PTY acceptance covers a masked account creation, exact HTTP payload, and
  refreshed inventory receipt.
- The firewall rule dialog sends its source address as the backend `from`
  field instead of an ignored UI-only `source` field, so source restrictions
  are applied rather than silently becoming unrestricted rules.
- PM2 API documentation now describes the implemented script-start behavior
  instead of incorrectly claiming that `POST /api/pm2/deploy` runs Git pull and
  restart operations.

- Panel and agent rollback restore the exact snapshot and preserve the prior
  active and enabled systemd state.
- Failed upgrades restore the previous binary and panel SQLite data instead of
  leaving a failed release selected.
- Native install stops before writing service files when the host preflight
  fails.
- Onboarding resumes saved progress, persists backwards navigation, exposes
  load/save failures, and does not turn a failed security request into a zero
  score.
- Domain provisioning honors the configured virtual-host root and reports
  partial provider failures without hiding successful host work.
- Security status is derived from observed effective host state rather than
  package names or configured URLs.
- Disk overview recursively discovers mounted whole disks, partitions, NVMe,
  encrypted, and LVM devices with their real `lsblk` paths instead of assuming
  one child depth or showing a false empty inventory.
- Disk Overview no longer converts failed `lsblk`/`df` observation into an
  empty host; it exposes the original error and retry guidance while keeping
  independently observed root SMART health visible.
- Disk deep-analysis status, directory listings, and `/etc/fstab` inventory no
  longer convert failed observation into never-run or empty states; each view
  preserves the original error and offers contextual retry guidance, while a
  new deep scan stays paused until the current job state is known.
- File Manager now keeps root, directory, and file-read failures distinct from
  empty states, pauses create/rename/delete/save controls until the relevant
  path is observed, and remounts the editor between files so an unsaved draft
  cannot be written to a newly selected path. Reopened rename dialogs also load
  the selected entry name, and known configuration roots retain their correct
  labels when the virtual-host root is absent.
- User-management dialogs are now isolated by operation and user identity, so
  edit forms always load the selected account and cancelled add-user or password
  drafts are discarded before the dialog is reopened.
- Notification channel and alert-rule add dialogs now discard cancelled drafts,
  including unsaved SMTP passwords and provider tokens. Channel and rule delete
  actions now identify the selected resource and require a separate confirmation
  before sending the delete request.
- Notification channel credentials now migrate from inline SQLite JSON into
  mode-`0600` installation-owned files. Channel APIs return only redacted
  non-secret config and preserve an existing credential when an edit submits an
  empty secret field; the UI exposes protected-store recovery guidance instead
  of leaking or replacing unavailable channel state.
- Mail account, password-change, and alias dialogs now discard unsubmitted
  values and reset password visibility whenever they close, so a cancelled
  password or address cannot appear in a later operation.
- Docker image pulls now discard an unsubmitted image name when the dialog
  closes. Removing a stopped container or local image now requires a separate
  confirmation that identifies the selected Docker resource.
- PM2 process deletion now uses the supported process-action endpoint instead
  of an unregistered `DELETE` route. The UI identifies the selected process,
  explains the separate PM2 Save boundary, and requires confirmation before
  removing it from the active process list.
- Domain detail status controls now use the registered domain toggle endpoint
  and its `active` request payload instead of sending an unsupported `PUT` to
  the domain resource path.
- Remote project-domain requests retain their managed-node deployment prefix
  while no target is selected instead of constructing a path from an empty base
  that could resemble a local domain API route.
- The frontend backup request contract no longer advertises an ignored `note`
  field, and its database engine is constrained to the two values accepted by
  the backend and OpenAPI contract.
- Domain toggle requires an explicit boolean `active` field, so an empty body
  can no longer be interpreted as a request to disable a domain. Local process
  signaling likewise requires an explicit bounded `term` or `kill` value.
- Uptime monitor and status-page create dialogs now discard cancelled drafts on
  close, including unsaved HTTP authorization headers and request bodies, before
  a new create operation begins.
- Deploy-target creation now discards every unsubmitted field, including the
  webhook token, when the dialog is cancelled, dismissed, or closed before a
  new target is created.
- Cron inventory no longer reports an unavailable `crontab` binary or a
  partially unreadable multi-user inventory as an empty successful result.
- Backup scheduling no longer treats a failed `crontab -l` as an empty
  schedule or writes over unobserved entries; list and mutation endpoints now
  return `503 Service Unavailable` with contextual remediation.
- Backup schedule deletion now accepts only an exact, currently observed
  HServer-managed line and preserves every unrelated crontab entry.
- Backup schedule retention is now named and displayed as a backup count,
  matching the pruning behavior; the misleading `retention_days` field remains
  only as a backwards-compatible API alias.
- Invalid backup schedule cron expressions, types, retention counts, and
  database metadata now return `400 Bad Request` before HServer reads or writes
  any host crontab or runner file.
- Snapshot scheduling now records the persisted restic daily-retention policy
  instead of silently hardcoding 14 in the cron metadata.
- Snapshot status, settings, run, list, restore, purge, and scheduled-run paths
  now stop with `503 Service Unavailable` when `snapshot-settings.json` is
  unreadable or malformed instead of silently applying default paths or
  retention; a genuinely missing file still uses provider-neutral defaults.
- Fail2Ban no longer reports an incomplete jail inventory as empty, and an
  absent optional installation is a warning rather than a failed security
  control.
- Activity history and managed-node operations remain scoped to the selected
  server.
- Server-scoped navigation now preserves the selected managed node when moving
  between server views instead of silently falling back to the default.
- PostgreSQL and MariaDB restores preserve their encoded target, create an
  automatic pre-mutation database recovery point, and expose the file rollback
  boundary before confirmation.

### Removed

- Operator hostnames, addresses, accounts, paths, DNS inventory, and generated
  local-tool artifacts from the public source surface.
- Installation-specific commercial pricing and single-server roadmap claims.

## [0.1.0] — 2026-04-12

### Added

- Initial Go control-plane service with embedded React interface and SQLite
  state.
- JWT and bcrypt authentication, admin/manager/viewer roles, TOTP, users, and
  audit history.
- Local-host monitoring, service control, writable WebSocket PTY, file, disk,
  firewall, nginx, PHP-FPM, PM2, database, Docker, cron, DNS, mail, backup, and
  deployment surfaces.
- Native uptime monitoring with incidents, notifications, and explicitly
  configured public status pages.
- Optional Cloudflare, Stalwart, Google Drive/rclone, and notification-provider
  integrations.

This tag predates the provider-neutral community conversion. Its source and
documentation should not be used as public-installation evidence; use the
latest release candidate and current installation guide.

## [0.0.1]

Initial private prototype. No public support or compatibility guarantee.
