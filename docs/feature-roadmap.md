# HServer Community Roadmap

**Updated:** 2026-08-30

**Direction:** provider-neutral, Apache-2.0 licensed, self-hosted Linux server
management with a native local control plane and capability-scoped remote agents.

This roadmap describes the public project rather than one installation. A
feature is considered available only when its source, route, user interface, and
focused verification exist. Availability does not imply that every Linux
distribution or third-party provider has been certified.

## Product boundaries

- The panel runs natively for full control of its local Linux host.
- Additional servers connect outbound through the HServer agent; the hub does
  not execute local commands while pretending to manage a remote node.
- Remote mutations require an explicit agent capability and fixed, locally
  configured inputs.
- nginx, PHP-FPM, PM2, Docker, Cloudflare, Stalwart, databases, and backup
  providers are optional. Their absence must degrade to `not configured` or
  `unavailable`, not a false healthy state.
- Normal upgrades preserve existing SQLite data and support automatic rollback.
- Production inventory, credentials, and operator-only runbooks are never part
  of the public distribution.

## Current foundation

The current source contains the following product surfaces. Public release
acceptance is still required before treating all of them as generally available.

| Area | Local host | Managed node |
| --- | --- | --- |
| Monitoring and service inventory | Available | Agent inventory and heartbeat; live metrics source, TUI, and OpenAPI contract are complete |
| Writable terminal | Available | Explicit terminal capability |
| Process termination | Available | Stable-identity signal capability |
| Reboot and reboot cancellation | Available | Explicit host-action capability |
| RAM optimization, swap reset, temporary-file cleanup | Available | Explicit host-action capability |
| Disk analysis and bounded cleanup | Available, including interactive panel-host-only mounts, usage, largest-entry, I/O, SMART, and confirmed deep-analysis status/start | Fixed cleanup scopes |
| nginx, domains, and SSL inventory/actions | Available | Capability-scoped agent tasks |
| PHP-FPM and PM2 management | Lifecycle, checksum-locked pool read/replace, validation, backup, and rollback available | Capability-scoped inventory, actions, and checksum-locked pool replacement |
| Files, cron, firewall, databases, backups, deploy plans | Available | Locally bounded roots and plans |
| Guided restore preflight and rollback disclosure | Full artifact validation before confirmation | Central backup artifacts; target checks repeat when restore starts |
| Docker container inventory/actions and Compose deployments | Normalized host inventory, project-scoped services, isolated production-derived staging targets, write-only private environments, bounded logs, read-only revision comparison, installation-owned reusable templates, signed replay-resistant GitHub/GitLab webhooks, fixed lifecycle actions, image lifecycle, preflight-gated Git clone/update, deploy, and rollback | Explicit container capabilities; installation-owned deploy plans |
| Uptime monitoring and public status pages | Available | Centrally monitored |
| Users, RBAC, TOTP, and audit trail | Strict credential-free CRUD, transactional profile/password updates, final-administrator invariant, and interactive bounded inventory with masked creation/profile/password/role/deletion controls | Central control-plane boundary with no managed-node account substitute |
| Searchable operation history | Local audit filters | Local and managed-node audit filters |
| Portable configuration schema v1 | Admin export, preview, and confirmed allowlisted overlay | Central installation preferences only; node state excluded |
| Agent protocol and release compatibility | Central fleet summary | Per-node compatibility state and agent-reported binary architecture |
| Release discovery and verified upgrade | Optional Ed25519-signed, checksum-bound manifest; explicit stage and second admin confirmation | Server-local trust set, explicit update capabilities, exact version confirmation, detached upgrade/rollback, and durable follow-up state |
| Installer, doctor, upgrade, rollback, uninstall | Panel lifecycle | Agent lifecycle |
| Docker quick evaluation | Project-scoped build, health, login, restart, and persisted-state CI gate | Not a host-management substitute |
| Generated API contracts | OpenAPI 3.1 route, path-parameter, access inventory, bootstrap health/onboarding/authentication/TOTP recovery, closed editable and portable Settings, the complete strict local Domains lifecycle, strict panel-user CRUD, backup/restore, complete local System observation/update, host-maintenance, measured disk-cleanup, complete local database management, the complete local Deploy family including Compose services, write-only environments, project domains, health, and TLS, local PHP pool transaction, canonical alert-rule/history, strict Cloudflare zone/record/email-routing/cache/mail-reconciliation schemas, and redacted Stalwart service status/overview/version/listener/storage reads | Enrollment, inventory, connectivity, compatibility, high-level host actions, disk cleanup, task/history, conflict, and agent-authenticated heartbeat/poll/result wire schemas promoted, plus strict credential-free managed-node PHP-FPM, PM2, Docker container, live metrics read projections, and revision-aware project-domain ensure in OpenAPI revision 71 (443 routes and 321 schemas); remaining route families stay incremental |
| Authenticated automation CLI | Guided verified connect with echo-disabled interactive password/TOTP prompts and protected-file automation fallback, named server contexts, JSON or human-readable connection doctor with protected report-file output, writable terminal, health, version, OpenAPI, bounded service inventory/journals/actions, maintenance, disk cleanup and local diagnostics, including interactive panel-host-only Disk mounts, usage, largest-entry, I/O, SMART, and confirmed deep-analysis views plus panel-local read-only PHP extensions, INI, bounded error/slow logs, security, and Composer observations, containers, logs, PM2, checksum-locked PHP-FPM pool replacement, guarded Nginx site creation/editing/archival/recovery and edit-backup rollback plus managed-snippet inventory, domains, SSL, firewall, security score, Fail2Ban, panel-local persistent IP blacklist/whitelist inventory with confirmed add/delete, cron, databases, files, backups, Stalwart service/status/log/queue/domain operations, confirmed and optimistic-concurrency-guarded deploy target creation/update/deletion, protected-file write-only deploy environment lifecycle, installation-owned deploy domain/active-health/TLS lifecycle, plus runs/preflight/revision/actions/logs, complete local uptime monitor inventory/history/incidents/create/update/state/probe/domain-import management, effective settings management, and status-page lifecycle, filtered audit history, confirmed panel-user CRUD with protected password input, protected-file notification channels, canonical alert-rule CRUD/history, strict local BIND readiness/zone/record/SOA/check/lookup/export controls, optional Cloudflare zone/record/email-routing inventory with confirmed DNS mutation/cache purge/mail reconciliation, signed release status, explicit staging, observed-stage installation, and an interactive control center with PHP/security/cron/database actions, Mail service status, logs, queue, domains, and accounts plus confirmed queue retry/delete actions, guided local BIND zone/record creation plus record/SOA editing/check/reload/deletion, local backup-job progress/logs, target-scoped audit search, a fresh-observation Deploy dashboard, central notification/rule/history controls, central Cloudflare zone/record/email-routing inspection with fresh-observation cache/proxy/delete/mail actions, central masked panel-user creation/profile/password/role/deletion controls, and panel stage/install lifecycle actions | Context-selected node inventory and architecture-gated doctor, writable capability-scoped terminal, plus capability-aware service inventory/actions, host, disk, container, log, PM2, PHP-FPM inventory/config/action, Nginx, domain, SSL, firewall, cron, database inventory/restart, files, backup-plan, central target-scoped audit history, fixed task operations, capability-scoped deploy plan/action/job/output through the interactive Deploy dashboard, and preflighted exact-version agent upgrade/rollback through both JSON commands and the interactive Updates dashboard using the same role and capability checks; central panel users, Alerts, and Cloudflare explicitly retain their local-hub boundary, local BIND remains panel-host native until an explicit agent capability exists, and none has a false managed-node substitute |

**Managed-node live metrics status (source HEAD):** The provider-neutral
`metrics.read` path is complete across agent collection, capability/task
admission, hub/API validation, CLI/web/TUI consumers, and OpenAPI contract
revision 71 (443 routes, 321 schemas), including guest CPU de-duplication. The
combined acceptance for this path is complete. Canonical public repository
selection and publication are complete at
[`IamYGT/heyserver`](https://github.com/IamYGT/heyserver), initially published
at `53df8ba`, with private/public tree parity `3719df8`. Immutable public
source commit `adaccb23adf9720141d721970590de3a82fd17b5` produced the first
fully green public main CI matrix run `#33283277809`; the current fully green
public main CI run is `#33283728373` (2026-08-30). Branch protection was enabled
after these runs and is currently active. The initial active signer is prepared
in private commit `df0a5070`, and the `HSERVER_RELEASE_SIGNING_KEY` Actions
secret is configured; public signer PR #7 remains pending. The public `v0.9.5`
tag points to protected `main` commit
`b2af1591f7a848acd71bbe54bc4f70fbffe99373`, but tagged run `#33285788628`
failed both `Managed Agent Lifecycle` jobs because the lifecycle fixture posted
onboarding step 6 while the canonical maximum is step 5. The tag and run remain
historical failed-release evidence. The first successful signed release,
GitHub Release, tagged lifecycle/provenance acceptance, clean independent-VM
acceptance, and local/Contabo live rollout remain open. The next immutable patch
candidate is `v0.9.6`; no release or rollout is claimed for it. The live
`v0.9.3` rollout is not the current source. This records source and acceptance
progress only; it is not evidence of live deployment or a public release.

## v1.0 release gates

These are release requirements, not optional ideas.

1. **Clean installation matrix**

   Install release archives on disposable Ubuntu 24.04 `amd64` and `arm64`
   hosts. Prove login, onboarding, local terminal, one maintenance action,
   failed first-install cleanup, upgrade, failed-health rollback, and non-purge
   uninstall. The tagged native gate now automates an injected unhealthy first
   install with atomic cleanup, login and onboarding, an authenticated writable
   local PTY marker, bounded temporary-file cleanup with an inactive completion lock,
   protected-file `hserverctl login`, mode-`0600` token persistence, read-only
   CLI host status, disk scan, signed release status, and empty fresh-stage
   observation. It then builds a second stable native release, switches the
   signed feed, stages and installs it through `hserverctl`, and requires
   reconnect, terminal completion, exact panel/CLI identities, and preserved
   SQLite state before the existing failed-health rollback and non-purge
   uninstall checks. A fresh candidate tag must still pass that gate on both
   architectures before general availability.

2. **Managed-node acceptance**

   Enroll a second disposable host, prove network isolation, capability
   discovery, one read task, one allowed mutation, denial of a disabled
   capability, agent upgrade, and rollback. Version tags already gate a real
   panel, hub protocol, systemd agent, explicit missing-capability rejection
   with unchanged task history, server-observed stop/offline/reject and
   restart/online transitions, signed upgrade, rollback, crash-loop recovery,
   and non-purge uninstall on disposable `amd64` and `arm64` runners. The
   tagged matrix now also creates independent hub and managed-node Linux network
   namespaces with no default route, proves the node cannot reach the hub over
   loopback, proves the hub has no panel listener on the node address, and then
   exercises outbound enrollment, capability discovery, process inventory, one
   stable-identity process signal, and task-free denial of a disabled host
   action on both architectures. A real separate-VM/provider-network drill
   remains part of candidate acceptance because namespace isolation does not
   claim an independent kernel or infrastructure path. The public
   `accept-provider-network-managed-agent.sh` tool now makes that manual drill
   reproducible and emits a protected schema-v2 receipt after proving the
   native panel host, a different managed-node kernel, exact panel/CLI/agent
   identities, both release architectures, the real CLI PTY path, one bounded
   observed process mutation, and task-free disabled-capability denial. It
   works whether `host.action` is enabled or disabled by selecting a
   known safe task whose required capability is actually absent, and records
   legacy hostname/PTY-close compatibility instead of silently upgrading weak
   evidence.
   A fresh receipt from an operator-provisioned separate VM is still required;
   the repository fixture does not claim a provider run. The native
   managed-agent lifecycle jobs require the packaged CLI doctor to observe
   their exact runner architecture. The public
   `verify-provider-network-receipt.py` command now rejects stale, weak-default,
   incomplete, permission-open, identity-mismatched, or schema-drifted receipts
   before they are used as release evidence. Schema v1 remains explicitly
   inspectable compatibility evidence; neither schema is cryptographic provider
   attestation. Current release decisions can additionally require an
   exact-byte detached Ed25519 signature from a dedicated operator-held key;
   the verifier reports both verification state and public-key fingerprint
   without claiming that a signature proves provider-account ownership.

   The current source contains a bounded, capability-scoped managed-node
   live-metrics path with freshness and shape validation, its TUI consumer, and
   OpenAPI contract revision 71 (443 routes and 321 schemas). The combined
   metrics acceptance is complete. The canonical public repository is
   [`IamYGT/heyserver`](https://github.com/IamYGT/heyserver), initially
   published at `53df8ba`, with private/public tree parity `3719df8`. Immutable
   public source commit `adaccb23adf9720141d721970590de3a82fd17b5` produced the
   first fully green public main CI matrix run `#33283277809`; the current fully
   green public main CI run is `#33283728373` (2026-08-30). Branch protection was
   enabled after these runs and is currently active. The initial active signer is
   prepared in private commit `df0a5070`, and the `HSERVER_RELEASE_SIGNING_KEY`
   Actions secret is configured; public signer PR #7 remains pending. The
   public `v0.9.5` tag points to protected `main` commit
   `b2af1591f7a848acd71bbe54bc4f70fbffe99373`, but tagged run `#33285788628`
   failed both `Managed Agent Lifecycle` jobs because the lifecycle fixture
   posted onboarding step 6 while the canonical maximum is step 5. The tag and
   run remain historical failed-release evidence. The first successful signed
   release, GitHub Release, tagged lifecycle/provenance acceptance, clean
   independent-VM acceptance, and local/Contabo live rollout remain open. The
   next immutable patch candidate is `v0.9.6`; no release or rollout is claimed
   for it. The live `v0.9.3` rollout is not the current source, and no live
   deployment or public release is claimed here.

3. **Clean public history**

   The one-time public-history creation and publication are complete. The
   canonical public repository is
   [`IamYGT/heyserver`](https://github.com/IamYGT/heyserver), initially
   published at `53df8ba`, with private/public tree parity `3719df8`; any
   temporary export or creation path is disposable staging and evidence, not a
   permanent workflow. Main branch protection is complete. The initial active
   signer is prepared in private commit `df0a5070`, and the
   `HSERVER_RELEASE_SIGNING_KEY` Actions secret is configured; public signer PR
   #7 remains pending through protected review. The release workflow fails
   closed unless the version tag descends from the protected `main` commit,
   derives and publishes the manifest verification key without staging the
   private key, and provides checksums for both the bootstrap installer and
   public key. The public installer verifies a detached
   `bootstrap-install.sh.sig` against the configured release signer before
   privilege entry. The failed public `v0.9.5` tag/run remains historical
   evidence only; the next immutable patch candidate is `v0.9.6`. Successful
   tagged publication, GitHub Release publication, tagged lifecycle/provenance
   acceptance, the first signed release, clean independent-VM acceptance, and
   live rollout remain public-launch prerequisites, so this project is not
   public-launch ready until those gates
   and the independently authenticated installer anchor are complete.

4. **Restore drills**

   Prove restoration of portable panel state, configured file snapshots, and at
   least one supported database backup. A backup without a tested restore path
   is not release-complete. Stable tags now automate real panel SQLite and
   bounded files-root restore paths on disposable `amd64` and `arm64` hosts.
   Panel-state acceptance includes API-observed SQLite rollback and a
   pre-restore bundle that also preserves protected notification configs. File
   acceptance includes asynchronous job completion,
   artifact preflight with `filesRollback=true`, source mutation, recovered
   SHA-256 comparison, and a listed, independently valid pre-restore recovery
   archive. Focused service tests also prove rollback after partial extraction
   failure. The dedicated `Database Restore` CI matrix starts isolated real
   PostgreSQL and MariaDB servers on Ubuntu 24.04, creates a native dump,
   mutates the database, restores the dump, requires a recovery point, injects
   a partially mutating failure, and proves automatic rollback. Tagged release
   publication depends on both engine results. Candidate environments still
   verify installation-specific network, role, and credential-file policy.

5. **Failure-state consistency**

All primary pages must distinguish loading, empty, not configured,
unavailable, permission denied, and operation failed. Mutating controls must
remain disabled until the relevant state and capability are known. The web
terminal now verifies the current account before mounting its WebSocket and
distinguishes identity loading, identity failure, permission denial, remote
observation failure, offline state, and missing terminal capability while
keeping shell controls disabled until an admin target is ready.
Login now distinguishes credential rejection from temporary service
unavailability and other operation failures without rendering raw errors.
User management and host-security mutations use the canonical current-user
identity, remain closed while identity is unknown, and expose distinct
permission-denied states while backend role enforcement remains authoritative.
The Developer API page maps 401/403 permission denial, 404 missing, network or
5xx temporary unavailability, and other operation failures to distinct safe
messages without exposing backend response details.
The interactive Security section keeps persistent IP blacklist/whitelist
changes panel-local, bounds their display and inputs, and keeps add/delete
controls unavailable for managed targets.

6. **Operator documentation**

   Installation, reverse proxy, agent enrollment, upgrades, rollback, backup
   recovery, troubleshooting, and public API behavior must match the packaged
   release.

7. **Contributor documentation**

   A new contributor must be able to clone, install locked dependencies, run
   focused tests, build the panel, agent, and CLI binaries, and package a
   release without private infrastructure knowledge. The provider-neutral
   `make dev-check` and `make dev-setup` entry points now derive the exact
   minimum Go version from `go.mod` (currently Go 1.26.1 or newer), require
   Python 3, distinguish base requirements from optional/full-gate tools, and
   stop before downloads when a required toolchain is missing or too old.
   `CONTRIBUTING.md` also documents nearest focused Go and frontend tests as
   fast feedback, while retaining `make ci-fast` and `make ci-pr` as the
   provider-neutral contributor gates.

## After v1.0

Priorities are ordered by operator value and community maintainability.

### P1 — usability and recovery

- ✅ Contextual remediation now covers Docker, PM2, nginx, PHP-FPM, UFW,
  Certbot, BIND, PostgreSQL, MariaDB, Cron, Fail2Ban, Cloudflare, Stalwart,
  Webmail access, notification delivery, file and disk inventory, root-disk
  SMART health, Google Drive/rclone, and restic snapshot states. Optional
  dependency pages distinguish not configured, unavailable, and observed-ready
  states without converting failures into empty inventories.

### P2 — deployment workflows

- ✅ S3-compatible snapshot destinations are delivered with restic
  client-side encryption, protected credential-file references, explicit
  provider health states, panel and CLI destination selection, and bounded
  Google Drive-only repository purge capability.

### P3 — ecosystem

- Promote curated request, response, and error schemas into the generated
  OpenAPI contract without inventing behavior absent from handlers. The
  complete bootstrap health/onboarding/authentication and seven-operation
  Settings surfaces, seven-operation local Domains, eight-operation local
  System, Cron, Docker, PM2, Firewall, 12-operation Database, and 25-operation
  Deploy surfaces are now promoted with
  strict response objects, query semantics, bounded inputs, fixed mutation
  receipts, active project-domain health, transactional TLS state, and
  unavailable-runtime failures, plus strict credential-free managed-node
  PHP-FPM, PM2, and Docker container read projections in OpenAPI revision 69
  (312 schemas); remaining route families stay incremental.
- Extend the delivered stable `hserverctl` client beyond its current health,
  maintenance, disk cleanup, containers, logs, PM2, PHP-FPM, Nginx, domain, SSL,
  firewall, local security/Fail2Ban, cron, database, backup, audit, notification,
  managed-node inventory, and fixed task automation surface as additional API
  schemas become stable.
- ✅ A documented v1 in-tree extension boundary, dedicated integration proposal,
  and contribution checklist are delivered. Runtime-loaded third-party plugins
  remain outside v1 until a separate signing, compatibility, isolation,
  permission, upgrade, rollback, and removal proposal is accepted.
- Additional Linux distributions only after native lifecycle and service
  behavior pass the same acceptance matrix.
- Translated interface and documentation driven by community demand.

## Explicitly not promised

- Windows host management.
- Kubernetes as a prerequisite for the core panel.
- Hidden SSH password storage for managed nodes.
- Provider-specific defaults in the core installation.
- Arbitrary remote shell commands disguised as fixed agent tasks.
- A hosted control plane, marketplace, or paid tier without a separate public
  proposal and governance decision.

## How roadmap changes are accepted

Feature proposals should start from an operator problem, name the local or
managed-node scope, define the least-privileged capability boundary, describe
failure and rollback behavior, and include an acceptance path. Use the public
feature-request issue form. A roadmap entry is not a commitment until a
maintainer assigns it to a release milestone.
