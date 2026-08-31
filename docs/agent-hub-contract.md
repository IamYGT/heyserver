# Heyserver Agent-Hub v1 Contract

## Boundary

Heyserver is the control plane. Managed nodes initiate every connection to the
hub. The hub never opens SSH to a managed node. Structured task endpoints never
accept arbitrary command strings. A separately authenticated, explicitly
enabled outbound WebSocket carries multiplexed PTY input and output for writable
terminal sessions.

## Authentication

- Node registration generates a random bearer token and returns it once.
- The hub stores only a SHA-256 token hash.
- Agents store the token in a root-readable token file or environment value;
  the packaged example uses a separate token file.
- Agent endpoints reject an unknown node, a wrong token, oversized JSON, an
  unsupported protocol version, or a heartbeat timestamp outside five minutes.
- Each installation chooses its own private or public HTTPS route to the hub;
  no hosting provider, VPN product, hostname, or network range is built into the
  protocol.

## API

### Agent-authenticated

- `POST /api/agent/v1/heartbeat`
- `POST /api/agent/v1/tasks/poll`
- `POST /api/agent/v1/tasks/{id}/result`
- `GET /api/agent/v1/terminal` (WebSocket; requires the `terminal` capability)

### Panel-session authenticated

- `POST /api/nodes`
- `GET /api/nodes`
- `GET /api/nodes/{id}`
- `GET /api/nodes/{id}/integrations/status`
- `POST /api/nodes/{id}/tasks` (admin only; mutation-limited)
- `GET /api/nodes/{id}/tasks`
- `GET /api/nodes/{id}/tasks/{taskID}`
- `GET /api/nodes/{id}/profile` (admin only)
- `PUT /api/nodes/{id}/profile` (admin only; mutation-limited)
- `POST /api/nodes/{id}/profile/apply` (admin only; mutation-limited)
- `PUT /api/nodes/{node_id}/deploy/{target_id}/domains/{domain}` (admin only; mutation-limited)

`GET /api/nodes` and `GET /api/nodes/{id}` add computed `compatibility` and
`online` fields to each node record. `online` is based on the hub's clock and
the server-observed `last_seen_at`; it is true for 45 seconds after the last
accepted heartbeat. Agent timestamps and browser clocks do not decide this
state. `compatibility` contains the running panel release, the expected agent
protocol, a protocol compatibility boolean, and an agent release state:
`current`, `behind`, `ahead`, or `unknown`. Only stable `major.minor.patch`
releases are ordered. Development, commit-derived, and prerelease builds remain
`unknown` rather than being presented as safely current.

The admin-only `POST /api/nodes/{id}/tasks` validates the payload and advertised capability,
then returns `409 Conflict` without creating a task when the node is offline.
Already queued or running tasks retain their recorded state; the online gate
prevents new desired work from silently accumulating for a disconnected agent.
Current agents include their Go binary architecture in heartbeat inventory;
legacy agents may omit it, so the panel renders that state as unknown rather
than guessing from the hostname or kernel string.

`GET /api/nodes/{id}/integrations/status` is a fresh, read-only managed-node
observation gated by the `integration.status` capability. It returns `409
Conflict` with `managed_node_offline` or `capability_unavailable` without
persisting a task when the node cannot accept the request. Concurrent requests
coalesce one queued or running task per node and wait at most 45 seconds. A
completed response is schema-v1 with target scope `managed_node` and exactly
the `process.pm2`/`pm2_inventory` and `container.docker`/`docker_info` probe
pairs. Results contain only canonical states (`healthy`, `unavailable`, or
`not_configured`), safe error codes, and durations; process/container
inventory, command output, paths, secrets, and task error text never cross the
endpoint. Agent-level fatal failure is represented only by the fixed
`integration_status_failed` code.

`GET /api/nodes/{id}/profile` and `PUT /api/nodes/{id}/profile` are an
admin-only desired-state boundary for the managed agent's deploy capability
profile. `POST /api/nodes/{id}/profile/apply` is the separate, explicit
mutation that queues the panel-owned desired revision for an online node that
advertises `agent.profile.apply`; it never accepts a profile, command, path,
or token from the caller. The response separates the resolved node identity in
top-level `nodeId`, `desired.state` (`configured` or `not_configured`) from
the raw `observed` node snapshot. `observed.profileState` is
`not_reported`, `pending_restart`, `applied`, or `failed` (with an optional
observed revision and safe error code), while `apply.state` distinguishes
`not_requested`, `queued`, `running`, `awaiting_heartbeat`, `applied`,
`failed`, and `drifted`; a node without the capability remains on the
`manual_required` / `self_apply_not_supported` path.
For `not_configured`, `desired.profile` is `null`; after the first successful
write it is an object, including when every capability is disabled.

The strict `PUT` body is:

```json
{
  "profile": {
    "allowDeployRead": false,
    "allowDeployActions": false,
    "allowDeployDomainRead": false,
    "allowDeployDomainActions": false,
    "deployPlansFile": "",
    "deployAcmeWebroot": "",
    "deployWriteRoots": []
  },
  "expectedRevision": 0
}
```

`expectedRevision` uses compare-and-swap semantics. A node without a stored
profile has revision `0`; a stale revision returns `409 Conflict` with
`stale_profile_revision`. All-false and empty-path values are valid and mean a
configured-but-disabled profile. Deploy actions require deploy read,
project-domain read requires deploy read, and project-domain actions require
project-domain read. Optional paths must match ASCII
`^[A-Za-z0-9._/+:-]+$`, be clean absolute paths other than `/`, and remain
within 4096 bytes. File and directory values must not have a trailing slash.
Write roots are a unique JSON string array limited to 16 entries. Before
storing desired state, the hub proves that the canonical wrapper for the
prospective revision fits the inclusive 16 KiB agent document limit. The hub
stores no token, secret, or raw request body in
the audit record; successful mutations record only the node and new revision.

The strict apply body is deliberately smaller than the profile update body:

```json
{
  "expectedRevision": 1,
  "confirmed": true
}
```

`expectedRevision` must match the panel-owned desired revision and
`confirmed` must be `true`; unknown, duplicate, or extra fields are rejected.
The hub creates one fixed `agent.profile.apply` task whose payload contains
only the decimal revision and a canonical base64 profile wrapper. A completed
task is a transport acknowledgement, not proof of installation: the agent
first records `pending_restart`, runs its fixed local lifecycle installer, and
the next accepted heartbeat is authoritative for `applied` or `failed`.
Offline, incapable, unconfigured, stale, and already-in-flight requests do not
create a task. Audit and task-result boundaries retain only node/revision,
state, and closed safe error codes; raw agent paths, command output, token
content, and environment-file content never cross to the hub.

The admin-only `PUT /api/nodes/{node_id}/deploy/{target_id}/domains/{domain}`
is the revision-aware managed project-domain ensure boundary. Its strict body
contains exactly `expected_revision` (the lowercase SHA-256 observation
revision or `absent`) and `confirmed: true`; unknown, duplicate, trailing, and
arbitrary fields are rejected. The hub supplies only the validated target,
normalized hostname, fixed `ensure` action, and expected revision to the
installation-owned `deploy.domain.action` task. A current active/enabled
observation returns a typed `changed: false` idempotent no-op; compare-and-swap
rejects stale observations, drift, and competing revisions. The response's
`observation` is credential-free and typed, and no Nginx content, upstream URL,
certificate path, Certbot argument, shell command, or secret is accepted from
the caller.

The generated OpenAPI contract publishes the enrollment, node, inventory,
compatibility, profile desired-state, task request, task history, high-level
host-action, measured disk-cleanup, managed project-domain ensure, and error
schemas for these panel-session endpoints. It also publishes the
agent-authenticated heartbeat, empty-or-claimed task poll, and terminal
task-result wire schemas. The heartbeat contract fixes protocol `v1`, the
matching node identity header remains required, result maps retain their
implemented bounds, and a failed task requires failure text. Task payload
fields remain constrained by the per-kind allowlist below; the generic string
map is not arbitrary shell access.

## Task allowlist

- `service.status`: read one explicitly allowed systemd unit.
- `service.action`: start, stop, or restart one explicitly allowed systemd
  unit. The agent configuration owns the service allowlist.
- `host.action`: run one structured, locally allowed maintenance action. The v1
  action vocabulary is `memory-optimize`, `swap-reset`, `temp-clean`, `reboot`,
  and `reboot-cancel`; no shell text or command arguments are accepted.
- `process.signal`: send `term` or `kill` to a PID only when its reported Linux
  start time still matches. `HSERVER_AGENT_ALLOW_PROCESS_SIGNALS=true` is an
  explicit local opt-in.
- `disk.cleanup.scan`: measure only locally allowed cleanup scopes.
- `disk.cleanup.execute`: run one to four IDs from the fixed vocabulary
  `apt-cache`, `journal`, `tmp-old`, and `rotated-logs`. The hub cannot provide
  a command or filesystem path.
- `logs.read`: return 1-500 structured journal entries from a locally enabled
  source ID. The fixed vocabulary is `system`, `nginx`, `php`, `mariadb`,
  `postgresql`, `pm2`, and `docker`; the hub cannot provide a unit or path.
- `container.list`: return a bounded Docker container inventory without
  accepting payload fields.
- `container.action`: run only `start`, `restart`, or `stop` for a validated
  container name or ID. Arbitrary Docker arguments are not part of the task.
- `nginx.action`: run only `test` or `reload`. Reload always performs a
  successful `nginx -t` first; the hub cannot provide a binary, unit, or flags.
- `nginx.config.list` / `nginx.config.read`: return bounded metadata or content
  from the agent's locally configured available-sites directory. The hub can
  provide only a validated basename, never a filesystem path.
- `nginx.config.write`: atomically replace one existing regular config after a
  SHA-256 compare-and-swap check, create a timestamped backup, validate the full
  Nginx configuration, restore invalid content, and optionally reload.
- `php.inventory`: discover bounded PHP-FPM versions and pool basenames below
  the agent's locally configured PHP root; no hub-supplied path is accepted.
- `php.config.read`: return one bounded regular pool file selected by validated
  version and basename.
- `php.config.write`: atomically replace one existing pool after a SHA-256
  compare-and-swap check, create a timestamped backup, run the matching local
  `php-fpm -t`, restore invalid content, and optionally reload.
- `php.action`: run only `test`, `reload`, or `restart` for a validated version.
  Reload and restart always require a successful matching config test first.
- `pm2.list`: return at most 512 normalized processes from the locally
  configured PM2 binary, home, and Unix identity.
- `pm2.logs`: return at most 500 lines for one validated process name.
- `pm2.action`: run only `start`, `stop`, `restart`, or `reload` for a validated
  process name, then persist the PM2 process list. No shell or PM2 flags arrive
  from the hub.
- `integration.status`: run one bounded read-only PM2 `jlist` probe and one
  bounded read-only Docker `info` probe in parallel. Each probe has a five-
  second limit and the aggregate has a ten-second limit; probe failures remain
  item-level safe states and never include raw command output or diagnostics.
- `cron.inventory`: return bounded managed jobs, cron sources, service state,
  and the current SHA-256 revision from locally configured paths.
- `cron.create` / `cron.update` / `cron.delete`: mutate only the Heyserver-owned
  cron state and rendered cron file with compare-and-swap revision checks,
  syntax validation, atomic writes, backups, and a cross-process lock.
- `cron.run`: execute only a previously stored managed job by validated ID;
  the hub cannot attach a new command to a run request.
- `firewall.inventory`: return bounded IPv4 INPUT and Heyserver-owned chain state,
  persistence state, revision, and the locally configured lockout policy. The
  packaged systemd sandbox permits `AF_NETLINK`, which `iptables-nft` needs to
  read kernel state; the capability remains disabled unless local agent
  configuration explicitly enables it.
- `firewall.add` / `firewall.delete`: mutate only rules carrying an
  Heyserver-generated ID inside `HSERVER-INPUT`, require a current revision,
  serialize mutations with a local file lock, persist through the configured
  fixed binary, and roll the live rule back when persistence fails.
- `domain.inventory`: parse at most 512 regular files from the locally
  configured Nginx available-sites directory and report names, aliases, type,
  document root or proxy target, enabled state, and certificate reference.
- `domain.action`: enable or disable one validated config basename using only a
  symlink in the locally configured enabled-sites directory; validate Nginx and
  restore the prior link state if validation or reload fails.
- `ssl.inventory`: parse at most 512 certificates from the locally configured
  Certbot config directory with Go's X.509 parser; report SANs, issuer, serial,
  validity, remaining days, and whether a renewal config exists.
- `ssl.action`: check one validated certificate chain with the configured
  OpenSSL binary, or run a non-interactive Certbot renewal for that exact name.
  Renewal uses locally configured Certbot state directories, then validates and
  reloads Nginx while holding the shared Nginx mutation lock.
- `database.inventory`: discover MariaDB and the highest online PostgreSQL
  cluster through fixed local binaries and Unix-socket/peer identities; return
  bounded database and session rows without exporting credentials.
- `database.action`: restart only a locally allowlisted MariaDB or PostgreSQL
  engine, then require its fixed socket/readiness health check to pass.
- `backup.inventory`: read bounded systemd plan metadata and backup files from
  an installation-owned JSON plan file. An optional standard `SHA256SUMS` file
  is verified with fixed in-process hashing and byte/file limits.
- `backup.run`: start only the systemd service mapped to a validated plan ID in
  the local plan file, then require the unit result to be `success`.
- `agent.profile.apply`: apply one panel-owned profile revision through the
  agent's local lifecycle installer. The task payload contains only the
  decimal revision and canonical profile wrapper; no installer argument, path,
  command, environment value, or secret is accepted from the hub.

Package management, filesystem access, shell text, database mutations, and
unstructured firewall commands remain outside the structured v1 task
vocabulary. An operator who enables the writable PTY can perform other
operations interactively with the same root authority as a local terminal.

## Capabilities

Every heartbeat reports the exact capability set enabled by the agent's local
configuration. The hub persists and returns that set with the public node
record. An omitted set is valid for agents released before capability
advertising was added.

- `inventory`: bounded operating-system, load, memory, disk, service, and top-50
  process state. Process entries include stable PID/start-time identity and a
  command line capped at 512 bytes.
- `service.status`: status reads for configured observed or action-allowed
  systemd units.
- `service.action`: start, stop, and restart for locally action-allowed units.
- `host.action`: bounded RAM cache release, swap cycling, tmpfiles cleanup, and
  scheduled reboot controls enabled by `HSERVER_AGENT_ALLOWED_HOST_ACTIONS`.
- `process.read`: bounded process inventory; advertised by current agents.
- `process.signal`: stable-identity SIGTERM/SIGKILL enabled only by
  `HSERVER_AGENT_ALLOW_PROCESS_SIGNALS=true`.
- `terminal`: outbound-only multiplexed writable PTY sessions enabled only by
  `HSERVER_AGENT_ALLOW_TERMINAL=true`. Each browser tab has an isolated PTY;
  input and output are bounded envelopes on one authenticated agent connection.
- `disk.cleanup`: measured cleanup scanning and execution constrained by
  `HSERVER_AGENT_ALLOWED_DISK_CLEANUP`.
- `logs.read`: structured journal access constrained by
  `HSERVER_AGENT_ALLOWED_LOG_SOURCES`. The heartbeat reports the enabled source
  IDs so clients do not render controls for locally disabled sources.
- `container.read`: Docker inventory enabled only by
  `HSERVER_AGENT_ALLOW_CONTAINER_READ=true`.
- `container.action`: Docker lifecycle controls constrained by
  `HSERVER_AGENT_ALLOWED_CONTAINER_ACTIONS`.
- `nginx.action`: Nginx test/reload constrained by
  `HSERVER_AGENT_ALLOWED_NGINX_ACTIONS`.
- `nginx.config.read`: bounded Nginx inventory/content enabled by
  `HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ=true` and local directory settings.
- `nginx.config.write`: checksum-guarded config saves enabled separately by
  `HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE=true`.
- `php.read`: bounded PHP-FPM version, pool, and configuration reads enabled by
  `HSERVER_AGENT_ALLOW_PHP_CONFIG_READ=true` and local root settings.
- `php.write`: checksum-guarded PHP-FPM pool saves enabled separately by
  `HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE=true`.
- `php.action`: PHP-FPM test/reload/restart constrained by
  `HSERVER_AGENT_ALLOWED_PHP_ACTIONS`.
- `pm2.read`: PM2 inventory and bounded logs enabled only by
  `HSERVER_AGENT_ALLOW_PM2_READ=true`.
- `pm2.action`: PM2 lifecycle controls constrained by
  `HSERVER_AGENT_ALLOWED_PM2_ACTIONS`; binary, home, and Unix identity remain
  local agent settings.
- `integration.status`: managed PM2 and Docker health probes are advertised by
  current agents as a read-only capability. PM2 requires the local read toggle
  plus configured binary, home, and Unix identity; Docker requires the local
  container-read toggle. Disabled integrations report `not_configured`, while
  configured command or daemon failures report `unavailable`.
- `cron.read`: bounded cron inventory enabled only by
  `HSERVER_AGENT_ALLOW_CRON_READ=true`.
- `cron.write`: managed cron mutations enabled separately by
  `HSERVER_AGENT_ALLOW_CRON_WRITE=true` and local path settings.
- `cron.run`: manual execution of stored jobs enabled separately by
  `HSERVER_AGENT_ALLOW_CRON_RUN=true`.
- `firewall.read`: bounded IPv4 iptables inventory enabled only by
  `HSERVER_AGENT_ALLOW_FIREWALL_READ=true`.
- `firewall.write`: revision-guarded Heyserver-chain mutations enabled separately
  by `HSERVER_AGENT_ALLOW_FIREWALL_WRITE=true`. Binary paths, persistence path,
  and optional protected sources/ports remain local settings.
- `domain.read`: bounded Nginx-backed domain inventory enabled only by
  `HSERVER_AGENT_ALLOW_DOMAIN_READ=true`.
- `domain.action`: reversible enable/disable actions enabled separately by
  `HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS=true`; this requires domain read.
- `ssl.read`: bounded certificate inventory enabled only by
  `HSERVER_AGENT_ALLOW_SSL_READ=true`.
- `ssl.action`: fixed chain-check and renew-if-due operations enabled by
  `HSERVER_AGENT_ALLOW_SSL_ACTIONS=true`; this requires SSL read. Certbot,
  OpenSSL, CA bundle, and state paths remain local installation policy.
- `database.read`: bounded MariaDB/PostgreSQL inventory enabled only by
  `HSERVER_AGENT_ALLOW_DATABASE_READ=true`.
- `database.action`: engine-specific restart and health checks constrained by
  `HSERVER_AGENT_ALLOWED_DATABASE_RESTARTS`; this requires database read.
- `backup.read`: configured systemd plan and bounded file inventory enabled by
  `HSERVER_AGENT_ALLOW_BACKUP_READ=true`.
- `backup.run`: manual execution of a locally configured plan enabled by
  `HSERVER_AGENT_ALLOW_BACKUP_RUN=true`; this requires backup read.
- `files.read`: bounded directory browsing and UTF-8 text reads enabled when
  `HSERVER_AGENT_FILE_READ_ROOTS` contains at least one local absolute root.
- `files.write`: checksum-guarded backup and atomic replacement enabled when
  `HSERVER_AGENT_FILE_WRITE_ROOTS` contains at least one root; every write root
  must also be present in the read-root list. The accepted roots are reported
  in heartbeat inventory so the panel never guesses a server layout.
- `deploy.read`: bounded metadata for installation-owned deploy plans enabled
  by `HSERVER_AGENT_ALLOW_DEPLOY_READ=true`.
- `deploy.action`: a fixed `preflight`, `deploy`, `restart`, or `rollback`
  action enabled by `HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=true`; this requires
  deploy read. The executable, argv, working directory, and timeout come only
  from the agent's local plan file and are never accepted from the hub.
- `deploy.domain.read`: Heyserver-owned project-domain mappings, observed TLS
  state, and fixed loopback health probes enabled by
  `HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=true`; this requires deploy read.
- `deploy.domain.action`: fixed `create`, `ensure`, `delete`, `tls-enable`,
  `tls-disable`, and `tls-renew` operations enabled by
  `HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=true`; this requires project-domain
  read. The hub supplies only a plan ID, ASCII hostname, fixed action, an
  optional ACME email, and (for `ensure`) the expected lowercase SHA-256
  revision or `absent`. The upstream port comes exclusively from `host_port` in
  the installation-owned deploy plan. Nginx content, upstream URLs, certificate
  paths, Certbot argv, and shell commands are never accepted from the hub.
- `agent.update.read`: server-local release discovery, durable lifecycle state,
  and rollback availability enabled by `HSERVER_AGENT_ALLOW_UPDATE_READ=true`.
  The manifest URL and optional comma-separated Ed25519 public-key trust set
  remain local agent configuration and are never returned as executable input.
  Status reports manifest trust as `not_configured`, `verified`, or
  `unavailable`; an expected but invalid signature makes discovery unavailable.
- `agent.update.action`: fixed `upgrade` to an exact stable manifest version or
  `rollback` to the latest verified pre-upgrade snapshot, enabled by
  `HSERVER_AGENT_ALLOW_UPDATE_ACTIONS=true`; this requires update read. The
  agent re-fetches its local manifest, verifies its detached signature when
  trust keys are configured, verifies declared size and SHA-256,
  validates archive paths and ELF architecture, extracts only packaged agent
  lifecycle assets, and schedules a detached systemd unit. The hub cannot
  supply a URL, checksum, filesystem path, command, or systemd argument.
- `agent.profile.apply`: fixed application of the panel-owned profile revision.
  The capability is advertised only when the packaged fixed installer and its
  systemd scheduling prerequisites are present. The task carries only the
  decimal revision and canonical profile wrapper; the agent stages it under
  the existing local `HSERVER_AGENT_STATE_DIR/profile` directory and invokes
  the lifecycle installer with the fixed `apply-profile` action. The hub cannot
  provide installer arguments, paths, commands, environment values, or
  secrets.

Capabilities describe what the agent can execute; they do not grant new
authority. The local allowlists remain the final enforcement boundary. Unknown
or duplicate capability values are rejected rather than silently accepted.

## Profile lifecycle on the managed node

The packaged agent service keeps profile transition state under the existing
local `HSERVER_AGENT_STATE_DIR/profile` directory. The directory is root-owned
and mode `0700`; `candidate.json`, `active.json`, `previous.json`, and
`state.json` are regular root-owned mode-`0600` files. The versioned candidate
wrapper is strict JSON with exactly `schema_version`, positive `revision`, and
`profile`; the profile has exactly the seven fields in the update contract.
Paths match ASCII `^[A-Za-z0-9._/+:-]+$`, are clean absolute paths other than
`/`, and are limited to 4096 bytes,
write roots are unique and limited to 16 entries, and the deploy/domain
dependencies are checked again on the node. The wrapper and state document are
bounded to 16 KiB.

The agent stages the canonical wrapper and invokes only the fixed installer
action `apply-profile`; no profile path, shell, command, or extra argument is
accepted. The installer snapshots the current canonical profile files and the
exact unit, validates `ProtectSystem=strict` and `NoNewPrivileges=yes`, and
atomically replaces only `ReadWritePaths=`. Entries are derived from the
bounded profile write roots and, for domain actions, the configured ACME,
Nginx, and Certbot roots. `EnvironmentFile`, the configured token
`ReadOnlyPaths`, and every other existing sandbox directive remain local
configuration. Before any service candidate is built, profile-provided write
roots and the ACME webroot are checked against static protected directory
trees (`/etc`, `/usr`, `/bin`, `/sbin`, `/boot`, `/proc`, `/sys`, `/dev`,
`/run`) and the resolved local state and release directories; overlap in either
direction is rejected. Those write targets also cannot equal or contain the
local configuration, token, service, installed binary, lifecycle installer,
or read-only deploy-plans file. Existing symlink components and real paths are
subject to the same rules, with cross-boundary aliases rejected closed. The
plans file itself remains a grammar-validated read-only source, so a direct
path such as `/etc/hserver/deploy-plans.json` is valid. The installation-owned
internal profile directory bypasses this input check, as do locally configured
Nginx and Certbot exceptions. The service therefore
has no write exception for arbitrary hub input; the profile directory is
always its state exception and deploy/domain exceptions exist only when the
validated profile enables them.

The transition writes `pending_restart`, performs `daemon-reload`, restarts
the service, requires two active-state checks, and only then atomically
promotes the candidate to `active.json` (moving the prior active wrapper to
`previous.json`). A failure restores the exact unit and profile snapshot,
attempts reload/restart best effort, and records only a closed safe error code
in a `failed` state. It never publishes a raw path, command output, token, or
environment content to the hub. Upgrade snapshots include the profile
`active`/`previous`/`state` (and any staged candidate) files plus the unit
backup, but never environment or token secret contents; rollback restores the
older binary and unit while continuing to use the current local environment
and token destination.

## Acceptance

1. Heyserver registers a provider-neutral node ID and persists only its token hash.
2. The agent heartbeats over the operator-selected HTTPS path after service
   restart and reboot.
3. Heyserver reports current OS, kernel, boot ID, disk, memory, and bounded service
   states for the node.
4. Heyserver returns the capability set advertised by the last accepted
   heartbeat.
5. A `service.status` task is claimed once and its result is persisted.
6. The hub refuses service, host, or process tasks when the node does not
   advertise the required capability; the agent still enforces its own local
   configuration. Tagged native-agent acceptance checks the `409 Conflict`
   response names the missing `host.action` capability and proves the rejected
   request does not change persisted task history.
7. Process signals are rejected for PID 1, a missing start time, an identity
   mismatch, an unsupported signal, or a node without `process.signal`.
8. File tasks are rejected outside agent-configured roots, across escaping
   symlinks, for non-text content, or when the expected checksum is stale.
9. Deploy task status and bounded output are retained in the hub task history;
   the agent never accepts a command, argument, or working directory payload.
10. A node without `terminal` cannot attach the relay or open a browser PTY. A
   node with the opt-in can run simultaneous isolated sessions without inbound
   SSH, and disconnecting the agent closes every attached PTY.
11. Disk mount inventory comes from the heartbeat. Disk cleanup is rejected
   unless the node advertises `disk.cleanup`, and every requested target also
   exists in the agent's local cleanup allowlist.
12. Journal access is rejected unless the node advertises `logs.read`; both the
    hub task validator and the agent enforce the fixed source vocabulary, and
    journal output remains bounded before persistence.
13. Container inventory and actions are separate capabilities. Every action is
    validated by the hub and independently checked against the agent's local
    action allowlist before Docker is invoked with fixed arguments.
14. Nginx reload is rejected without `nginx.action`, is checked again against
    the local action allowlist, and never runs unless the preceding config test
    succeeds.
15. Nginx config tasks cannot select an arbitrary path. Writes reject stale
    checksums, symlinks, oversized content, and invalid syntax; an invalid
    candidate is restored before the task fails.
16. PHP-FPM tasks cannot select an arbitrary path, binary, or systemd unit.
    Writes reject stale checksums, symlinks, oversized content, and invalid
    syntax; actions are both capability-gated and locally allowlisted.
17. PM2 tasks cannot select a binary, home directory, Unix user, or arbitrary
    flags. Reads and actions are separate capabilities, process names and log
    limits are validated twice, and every action is locally allowlisted.
18. Cron write tasks reject stale revisions, malformed jobs, unknown users, and
    invalid syntax before replacing managed files. A run task contains only a
    managed job ID and cannot introduce a new command.
19. Firewall tasks cannot select a binary, chain, or persistence path. The hub
    and agent validate the rule independently; only IPv4 ACCEPT/DROP rules are
    accepted, stale revisions are rejected, configured management paths are
    protected, and a failed persistence save rolls the live mutation back.
20. Domain tasks cannot select an arbitrary path or send Nginx configuration
    text. Inventory ignores symlinks and backups; actions accept one basename,
    operate only on the configured enabled-sites symlink, test before reload,
    and restore the previous state on failure.
21. Agent lifecycle status, profile application, and binary actions are
    separate capabilities. Upgrade task completion means a verified detached
    operation was scheduled, not that the restarted agent already passed its
    active-state check. Profile task completion likewise means transport
    acknowledgement only; `pending_restart` becomes `applied` or `failed`
    only through a subsequent accepted heartbeat. Durable follow-up profile
    states are `queued`, `running`, `awaiting_heartbeat`, `applied`, `failed`,
    and `drifted`; a new heartbeat remains authoritative.
22. A managed agent with release trust keys accepts only an exact manifest body
    verified by the adjacent Ed25519 signature. During rotation, up to eight
    overlapping keys are accepted locally; the hub cannot add, remove, or
    choose a trusted key.
23. Stopping the systemd agent leaves its server-observed heartbeat unchanged.
    The hub reports the node offline after 45 seconds, refuses and does not
    persist a new task, then reports it online again after the restarted agent's
    next accepted heartbeat.
24. Candidate acceptance can run
    `scripts/accept-provider-network-managed-agent.sh` on the native panel host
    against an operator-provisioned disposable VM. The guarded drill requires a
    public HTTPS origin, verifies that the runner and authenticated panel share
    one boot ID while the node reports another, with an explicitly recorded
    hostname fallback for older pre-release panels. It opens the real remote
    CLI PTY, creates one 16–96 MiB marker only when four times the allocation is
    available, observes and terminates only that stable process identity, and
    proves an unadvertised `host.action` or `agent.update.read` task is rejected
    without persistence. Legacy Linux PTY `EIO` close reporting is accepted
    only after the marker is observed and is named in the receipt; current
    agents classify PTY `EIO` as normal exit. Current acceptance also requires
    the managed node to report a supported release architecture and records the
    exact `hserverctl` release identity used for the PTY. The protected receipt
    omits credentials, raw boot IDs, terminal output, and inventory. This proves the
    exercised separate-kernel path, not provider account ownership.
25. `scripts/verify-provider-network-receipt.py` validates protected schema-v3
    artifacts without contacting the node again. Current-candidate defaults
    require schema v3 with exactly 13 successful checks (`checks=13/13`),
    including the CLI identity, managed-node architecture, and public panel
    build commit;
    schema-v1 and schema-v2 remain inspectable only with `--require-schema any`
    and neither binds `panel_commit`. It requires a regular current-user-owned
    mode-`0600` file, the fixed 16–96 MiB marker boundary, a fresh UTC
    timestamp, supported compatibility values, and any caller-supplied exact
    panel, CLI, agent, node, architecture, and origin identities. Strict
    defaults reject hostname and legacy-EIO compatibility. Without signature
    arguments the result explicitly reports `signature=not_checked`. Operators
    can sign the exact receipt bytes with
    `scripts/sign-provider-network-receipt.sh` and require a detached Ed25519
    signature plus a base64 raw public key during verification. A verified
    signature proves that the named key holder signed the unchanged receipt; it
    does not independently attest provider-account ownership or the truth of
    external provisioning claims.
