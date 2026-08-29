# HServer CLI

`hserverctl` is the provider-neutral command-line client shipped in every
Linux release archive. It uses the same HTTP API, bearer authentication, role
checks, managed-node connectivity checks, and fixed agent-task boundary as the
web interface. It does not open SSH sessions or turn task creation into an
arbitrary remote shell; its writable shell uses the panel's authenticated,
admin-only terminal WebSocket and the managed agent's explicit terminal
capability.

The native release installer places the version-matched client at
`/usr/local/bin/hserverctl`. Native upgrade and rollback move the panel and CLI
through one release lifecycle, and uninstall removes both executables while
preserving configuration and application data unless purge flags are explicit.

Stable version tags test the installed client on disposable Ubuntu 24.04
`amd64` and `arm64` hosts before release publication. The native acceptance
uses a protected password file for unattended authentication, requires the resulting
token file to be mode `0600`, and parses real `host status`, `disk scan`,
`doctor`, and release-status responses. It then switches a locally signed feed
to a second stable build and uses the packaged `updates stage` and `updates
install` commands to prove detached restart, reconnection, terminal completion,
exact panel/CLI identity, and SQLite preservation. The managed-agent lifecycle
gate likewise uses the packaged CLI for observed update status, exact-version
upgrade, and rollback. Neither gate substitutes raw mutation requests for the
packaged CLI path.

## Connect and authenticate

For a persistent installation, use the guided connection command. It
authenticates, verifies the returned token against `/api/auth/me`, creates an
independent mode-`0600` token file, writes a mode-`0600` named context, selects
it, and prints the exact doctor and control-center next steps. On a real TTY it
prompts for the password, and for a TOTP code only when the account requires
one; both prompts disable terminal echo:

```bash
hserverctl connect \
  --server https://panel.example.com \
  --email admin@example.com \
  production

hserverctl doctor
hserverctl ui
```

`connect` refuses an existing context name or token path. Authentication and
account verification happen before either file is created. If verification
fails, it leaves no partial context or token. If the context write fails after
the new token file is created, the unreferenced token is removed. Use
`--token-file PATH` for an explicit protected token destination. Passwords,
TOTP codes, bearer tokens, account names, and email addresses are not printed.

For unattended automation, provide mode-`0600` regular files rather than
putting secret values in process arguments:

```bash
hserverctl connect \
  --server https://panel.example.com \
  --email admin@example.com \
  --password-file /run/secrets/hserver-password \
  --totp-file /run/secrets/hserver-totp \
  production
```

`--totp-file` is only needed for a TOTP-enabled account. Without
`--password-file`, non-interactive input fails before the login request and
points automation to the protected-file option.

The default server is `http://127.0.0.1:3085`. For a one-off connection, set a
different installation with `--server` before the command or with
`HSERVER_URL`:

```bash
export HSERVER_URL=https://hserver.example.com
hserverctl login \
  --email admin@example.com
```

For regular work across multiple installations, create named contexts instead
of repeatedly pairing server URLs and token files:

```bash
hserverctl context add --server https://panel.example.com production
hserverctl context add --server https://staging.example.com staging
hserverctl context use production
hserverctl context list
hserverctl context current
hserverctl context status
hserverctl context status --format json production staging

# Select another context for one command without changing the current context.
hserverctl --context staging health
```

The first context becomes current automatically; `--use` also selects a context
as it is added. Each context receives an independent protected token-file path
under `${XDG_CONFIG_HOME}/hserver/tokens/` (or
`${HOME}/.config/hserver/tokens/`) unless `context add --token-file PATH` is
explicit. The context file stores only server URLs and token-file references,
never passwords or bearer tokens. It is written atomically with mode `0600`.
`context remove NAME` removes only the reference and deliberately keeps the
token file for explicit operator cleanup.

Context selection order is global `--context NAME`, `HSERVER_CONTEXT`, then the
current context in `contexts.json`. `HSERVER_CONTEXT_FILE` can move that
non-secret configuration file. Explicit `--server` or `HSERVER_URL` overrides
the whole context connection and does **not** reuse its token implicitly; pair
an overridden server with `--token-file`, `HSERVER_TOKEN_FILE`, or the bounded
`HSERVER_TOKEN` automation fallback.

`context status` performs a read-only, unauthenticated `/api/health` probe for
every configured context (or only the named contexts). It runs probes in
parallel, emits a human-readable table by default, and accepts
`--format json` for automation. The output includes only context names, panel
URLs, health state, latency, and a safe error summary; token-file references
and bearer tokens are never included. The command exits non-zero when any
selected context is `unavailable`, `unhealthy`, or `invalid`.

The CLI never accepts a password or bearer token as a command-line argument.
`login` writes only the bearer token to the selected token file, atomically and
with mode `0600`, and prints the file location rather than the token. It refuses
to overwrite an existing token file unless `--replace` is explicit.

Without an explicit server override, token-file selection order is:

1. global `--token-file PATH`;
2. `HSERVER_TOKEN_FILE`;
3. the selected context's independent token-file reference;
4. `${XDG_CONFIG_HOME}/hserver/token`; or
5. `${HOME}/.config/hserver/token`.

If the selected file does not exist, `HSERVER_TOKEN` is accepted as an
automation-only fallback. A present token file must be regular, cannot be a
symlink, and must not be readable by group or other users. Keep bearer tokens
out of shell history, process arguments, logs, and source control.

Verify the active account and role, or remove the locally stored token:

```bash
hserverctl whoami
hserverctl logout
```

`logout` removes only the selected protected token file and never follows a
symlink. It does not claim to revoke copied bearer tokens. If `HSERVER_TOKEN`
is still present, the CLI reports that the environment-backed session remains
active and must be unset by the caller.

For an account with TOTP enabled, place the current code in a short-lived
protected file and add `--totp-file PATH`. The password and TOTP files are read
locally and are never included in CLI output.

## Diagnose a connection

Run the authenticated, read-only connection doctor against the selected
context before collecting a support report or attempting a mutation:

```bash
hserverctl doctor

# Human-readable support view; JSON remains the automation default.
hserverctl doctor --format text

# Create a new, mode-0600 report file without shell redirection.
hserverctl doctor --output ./hserver-doctor.json
hserverctl doctor --format text --output ./hserver-doctor.txt

# Require an online managed node, its exact architecture, and capabilities.
hserverctl doctor \
  --node edge-1 \
  --require-architecture arm64 \
  --require-capability terminal \
  --require-capability files.read
```

Without `--node`, the report checks panel health, authentication, and fleet
inventory. With `--node`, it checks that exact node's identity and online state;
`--require-architecture amd64|arm64` adds an explicit `node.architecture` check,
and each repeated `--require-capability` adds an explicit capability check. A
successful report exits zero. A failed health, authentication, node,
architecture, or capability check still emits the complete available report
and then exits non-zero, so callers can retain the evidence without treating it
as healthy. If the requested node cannot be read, its returned identity does
not match, or its architecture is missing, the architecture check and every
requested capability receive their own failed result instead of being omitted
or trusted from the wrong node.

The default JSON report uses stable `schema_version: 1`. `--format text` renders
the same result as a compact `PASS`/`FAIL` support view with one line per check;
it does not change the exit status. Both formats contain the selected server
URL, check names and messages, panel version and uptime, account role and TOTP
state, fleet counts, or the requested node's ID, version, protocol, and
agent-reported architecture and advertised capabilities. The architecture is
an additive optional `node.architecture` field, so legacy schema-v1 consumers
remain compatible; the text view renders it as `arch amd64` or `arch arm64`.
Reports intentionally do not retain or emit the account email, account name,
password, or bearer token. Text output strips control characters from
server-provided values. The server URL and node identity are operational data,
so review either report before posting it publicly.

`--output PATH` writes the same complete JSON or text result to a new protected
file with mode `0600` and prints only a local write receipt to standard output.
It refuses to overwrite an existing path, including a symlink, before issuing
any panel request. Failed checks still persist their available report and then
return the normal non-zero exit status. The report intentionally retains the
selected server URL and requested node identity; review those operational
values before attaching the file to a public issue.

`hserverctl doctor` diagnoses an already reachable panel connection through the
same public API and protected token as other CLI commands. It is different from
the root-only `/usr/local/libexec/hserver-doctor installed`, which inspects a
native host's installation files, permissions, configuration, and systemd
service locally.

## Manage portable panel settings

The authenticated `settings` command family exposes the same narrow,
installation-portable allowlist as the panel Settings form. It never addresses
internal service records, onboarding state, credentials, tokens, or provider
configuration. Setting values are validated locally before a request is sent;
the server remains the final authority for role checks and validation.

```bash
# Read the complete editable allowlist, or one key.
hserverctl settings list
hserverctl settings get timezone

# Replace one or more keys in one atomic PUT request.
hserverctl settings set hostnameDisplay='Community server' timezone=Europe/Istanbul

# Deletion is an explicit mutation.
hserverctl settings delete --confirm timezone

# Export is file-only; the CLI does not dump portable settings to stdout.
hserverctl settings export --output ./hserver-portable-config-v1.json

# Review a bundle without changing the installation, then apply it explicitly.
hserverctl settings preview --file ./hserver-portable-config-v1.json
hserverctl settings import --file ./hserver-portable-config-v1.json --confirm
```

The seven operations map to the authenticated API routes below:

| CLI operation | Method and route | Mutation | Required role |
| --- | --- | --- | --- |
| `settings list` | `GET /api/settings` | no | authenticated account |
| `settings get KEY` | `GET /api/settings/{key}` | no | authenticated account |
| `settings set KEY=VALUE...` | `PUT /api/settings` | atomic replacement of the supplied keys | manager or admin |
| `settings delete --confirm KEY` | `DELETE /api/settings/{key}` | yes | admin |
| `settings export --output FILE` | `GET /api/settings/portable` | no | admin |
| `settings preview --file FILE` | `POST /api/settings/portable/preview` | no | admin |
| `settings import --file FILE --confirm` | `POST /api/settings/portable/import` | yes | admin |

`settings set` sends every `KEY=VALUE` pair in one JSON body, so the server's
atomic `SetMany` boundary is preserved. Duplicate keys, unknown keys, empty
pairs, control characters, and values outside the portable validators are
rejected before the request. The allowlist is `hostnameDisplay`, `adminEmail`,
`notifyOnLogin`, `notifyOnError`, `notifyOnDeployment`, `webmail_url`,
`mail_admin_url`, `mail_server_host`, `mail_imap_port`,
`mail_smtp_starttls_port`, `mail_smtp_ssl_port`, and `timezone`.

`preview` and `import` accept only a bounded schema-v1 portable JSON bundle.
The input must be a regular, non-symlink file no larger than 128 KiB; unknown
JSON fields and trailing JSON values are rejected locally. `import` sends the
bundle as `{"bundle": ..., "confirmed": true}` only after the explicit
`--confirm` flag. Preview is non-mutating, and the server validates the bundle
again before an import.

`export --output FILE` requires a named destination and never treats `-` as
stdout. The response is written through a private temporary file and atomically
published with mode `0600`; an existing destination, including a symlink, is
not overwritten. Standard output contains only a local write receipt and never
contains the exported settings or file contents. Bearer tokens and server
error bodies are not printed; authentication, role, and server validation
errors retain the normal `hserverctl` recovery guidance.

## Inspect the integration catalog

List the reviewed optional and feature-specific integrations exposed by the
selected panel:

```bash
# JSON is the automation default and retains the schema-v1 catalog object.
hserverctl integrations list

# Human-readable ID, name, requirement, and target table.
hserverctl integrations list --format text

# Inspect one catalog entry without probing the integration itself.
hserverctl integrations show --format text cloudflare.dns
hserverctl integrations show runtime.php_fpm

# Run the supported local-host probes and show their current observations.
hserverctl integrations status
hserverctl integrations status --format text

# Run the fixed read-only probes on one managed node.
hserverctl integrations status --node edge-1
hserverctl integrations status --node edge-1 --format text
```

`integrations` is an authenticated, read-only command family. `list` and
`show ID` fetch `GET /api/integrations/catalog`; `show ID` selects the
requested entry from that schema-v1 object, so additive community entries are
accepted without a second CLI allowlist. An ID absent from the selected
server's catalog is rejected after that authenticated catalog read. The 15
schema-v1 core entries remain required, while the catalog may contain more.
Shell completion includes the `integrations -> list|show|status` path and IDs
from the catalog embedded in the installed CLI build, so completion remains
offline and automatically follows catalog additions in a rebuilt release.

The human `show` view renders the entry's purpose, requirement, classes,
local-host or managed-node targets, non-secret configuration key names, secret
key names and secret-file references (names only), canonical states, raw-state
mappings, and API route prefixes. Secret values are never part of the catalog
or CLI output. Catalog status mappings describe how an integration represents
`not_configured`, `unavailable`, and `healthy`; they are metadata only. The CLI
does not perform a live-health probe and does not claim that an integration is
healthy merely because it appears in the catalog.

`hserverctl integrations status` separately fetches the authenticated
`GET /api/integrations/status` endpoint. With no `--node`, the schema-v1
response is scoped to `target.scope=local_host` and contains the observed
timestamp, each probed integration's canonical state and probe identifier, an
optional bounded `error_code`, the `partial` indicator, and IDs that remain
`unprobed`. The local behavior and output remain unchanged when `--node` is
omitted.

With an explicit non-empty `--node NODE_ID`, the CLI instead performs
authenticated `GET /api/nodes/{id}/integrations/status` (the node path segment
is URL-encoded). This managed response is schema-v1 and must identify
`target.scope=managed_node` plus the requested `target.node_id`. It contains
exactly the two fixed read-only observations: `process.pm2` with
`pm2_inventory`, and `container.docker` with `docker_info`. Only
`not_configured`, `unavailable`, and `healthy` states and their safe
state-specific error codes are accepted; duplicate or unknown IDs, wrong
probes, malformed payloads, unknown fields, and trailing raw JSON are rejected.
The managed response has no `unprobed` list because the two fixed probes are
always represented. A node identity mismatch is rejected before output.

Offline nodes, missing agent capabilities, and managed status failures remain
the server's actionable HTTP errors (for example `managed_node_offline`,
`capability_unavailable`, or `managed_status_failed`); the CLI preserves the
status and adds its normal recovery guidance. The command is read-only and
never sends a node secret or provider error to the server or output.

The status JSON is re-encoded from the stable CLI DTO, so unrecognized server
fields, raw provider errors, command output, and secret values are never
printed. The text view renders `observed_at`, target scope, `partial`, one row
per result with state/probe/error-code/duration, and the `unprobed` ID list.

## Manage panel and agent releases

Inspect the selected panel's signed release feed and its latest local stage:

```bash
hserverctl updates status
hserverctl updates stage-status
```

Discovery is read-only. It distinguishes `not_configured`, `unavailable`, and
`healthy`, and reports signature verification separately. It does not download
or install a release. Stage a newer stable release only after reviewing that
status:

```bash
hserverctl updates stage --confirm
hserverctl updates stage-status
```

Staging asks the panel to download the exact artifact selected by its configured
release manifest, verify its size, checksum, archive boundary, binaries, and
installer, and retain it under the protected update directory. It does not
restart the panel. The request uses a separate 16-minute wait by default and
accepts `--wait DURATION` up to 20 minutes.

Install only the latest observed verified stage:

```bash
hserverctl updates install --confirm
```

The CLI first reads `stage-status` and sends only that server-observed stage ID
and stable version back to the fixed install endpoint. It accepts no URL,
checksum, path, binary, command, or arbitrary version. Installation schedules
the packaged detached lifecycle runner, restarts the panel, and uses its
automatic health-check rollback boundary. Completion also requires the
installed panel and CLI to report the exact version recorded by the verified
stage; a mismatch triggers rollback and a terminal failure receipt. A failed
stage can be rescheduled after its failure receipt has been reviewed. A
`scheduled` or `running` stage cannot be scheduled again.

Managed servers keep their own manifest URL, trust set, state directory, and
retained lifecycle installer. Inspect one exact node before acting:

```bash
hserverctl updates agent status --node edge-1
```

Upgrade to the verified newer stable version reported by that status, or roll
back to the server-local snapshot created by the previous successful upgrade:

```bash
hserverctl updates agent upgrade --confirm --node edge-1
hserverctl updates agent rollback --confirm --node edge-1
```

Before either mutation, the CLI refreshes lifecycle status. Upgrade requires a
healthy release feed, an available newer stable version, and no scheduled or
running operation; it then submits exactly the observed latest version.
Rollback requires an observed rollback snapshot and no active operation. Both
commands require `--confirm`, and the panel still repeats role, node-online,
advertised-capability, receipt, and server-local policy checks. The central
panel never supplies a managed node with a download URL, checksum, path,
command, or systemd argument.

These commands schedule or perform the bounded server-side request and print
its JSON receipt. Continue observing `updates stage-status` or `updates agent
status --node NODE` until the detached operation reaches its terminal state.

## Open a writable terminal

Open the panel host's native writable terminal from an interactive TTY:

```bash
hserverctl terminal
```

Open the same capability-scoped terminal on an online managed node:

```bash
hserverctl terminal --node edge-1
# Context selection works as it does for every authenticated command.
hserverctl --context production terminal --node edge-1
```

The command sends the bearer token in the WebSocket upgrade's Authorization
header, never in the URL, and requires the server to negotiate the
`hserver-terminal` subprotocol. Local shells retain the panel's admin role
check; remote shells additionally require the node to be online and to
advertise the explicit `terminal` capability. Terminal dimensions follow
`SIGWINCH`, Ctrl+C is delivered to the remote shell, and **Ctrl+]** closes the
local CLI connection if the remote shell cannot exit normally. Output uses the
same base64-safe byte transport as the browser terminal, so ANSI control
sequences and UTF-8 output are preserved.

## Open the interactive control center

Run the full-screen terminal interface after login:

```bash
hserverctl ui
# Optional: use a slower refresh interval on high-latency links.
hserverctl ui --refresh 10s
```

The TUI uses the same selected server URL, protected token file, role checks,
agent connectivity state, and capability checks as the non-interactive
commands. It does not introduce an SSH bypass or a second control plane.
Existing JSON-producing commands remain stable for scripts and automation.

The interactive control center includes:

- overview cards for CPU, memory, root disk, load, swap, uptime, and service
  health;
- a panel-host and managed-node switcher available from every section;
- service Start, Restart, and Stop controls;
- stable-identity process TERM and KILL controls;
- RAM optimization, swap reset, temporary-file cleanup, reboot scheduling, and
  HServer reboot cancellation;
- server-measured disk cleanup scanning and fixed-target selection;
- local and managed-node container inventory with fixed Start, Restart, and
  Stop controls;
- local and managed-node PM2 inventory with lifecycle actions, process-list
  persistence, and the shared scrollable log viewer;
- a PHP-FPM section that groups installed versions and observed pools, opens
  structured local or raw managed pool configuration in the safe viewer, and
  exposes only configuration-tested lifecycle actions supported by the target;
- a provider-neutral Web Ops section that loads Nginx configurations, domains,
  and SSL certificates together, preserves partial inventory when one optional
  capability is unavailable, and exposes fixed test/reload, enable/disable,
  check, and renewal actions;
- a local BIND DNS section that distinguishes healthy, stopped, not configured,
  not installed, and unavailable states, browses observed zones and records,
  guides validated zone/record creation and selected record/SOA editing, shows
  complete configuration-check diagnostics, confirms reload, and mutates only
  freshly re-observed record or SOA identities; managed targets explicitly state
  that local BIND has no remote-agent capability yet;
- a Firewall section that distinguishes manageable UFW rules from read-only
  fallback observations, lists capability-scoped managed-node rules, provides
  fixed common inbound allow profiles, deletes only currently observed mutable
  identities, and keeps managed firewall activation under agent-installation
  ownership;
- a Security section that keeps the local security score and Fail2Ban readiness
  independent, distinguishes missing/stopped/unavailable/healthy states, lists
  complete jail and banned-IP observations, and unbans only an exact refreshed
  banned identity after confirmation;
- a Cron section that lists local user jobs or installation-owned managed-agent
  jobs, toggles or deletes an exact observed identity, and can run a managed job
  once only when the agent advertises `cron.run`;
- a Databases section that shows local PostgreSQL/MariaDB sources and databases,
  drops only an exact observed local identity, exposes managed engine/database/
  connection inventory, and runs only agent-allowlisted restart health checks;
- a Files section that discovers installation-configured local or agent roots,
  browses directories, opens bounded UTF-8 text in the control-safe viewer,
  switches roots without escaping them, and deletes an exact observed local
  file or directory only after a separate recursive-delete confirmation;
- a Backups section that shows local artifacts and storage usage or managed-node
  installation-owned plans, shows recent local job progress and bounded logs,
  creates full, database-only, or files-only local backups through fixed
  profiles, selects all website files or up to 16 server-observed website
  folders, applies optional full-backup retention, runs a managed plan, deletes
  one local artifact, and guides restore through full artifact validation before
  a second destructive confirmation;
- an Encrypted Snapshots section for the local hub that distinguishes
  `not_configured`, `unavailable`, and `healthy`, shows restic/password/repository
  readiness and remote inventory, creates a snapshot, switches Google Drive or
  S3-compatible destinations while preserving the latest complete policy, and
  extracts a complete snapshot, selected installation-owned manifest paths, or
  up to 16 observed website folders into the fixed staging directory only after
  a separate confirmation; managed targets explicitly direct the operator back
  to Local rather than pretending to run hub storage commands remotely;
- an Audit section that requests the newest 100 central audit entries scoped
  to the selected local host or managed node, refreshes while visible, keeps
  pagination totals explicit, and filters the loaded user, action, resource,
  detail, and IP fields locally without broadening the selected server scope;
- an Updates section that distinguishes `not_configured`, `unavailable`, and
  `healthy` release discovery from signature state, shows the local verified
  stage or managed-agent lifecycle receipt, stages and installs a panel release,
  and schedules capability-scoped managed-agent upgrade or rollback only after
  refreshing the actionable observation;
- a Deploy section that shows local targets and recent runs or
  capability-scoped managed plans and jobs, inspects local preflight and Git
  revision readiness, opens bounded run/job output, confirms local deploy or
  rollback and advertised managed actions, and rejects changed observations
  before mutation;
- a central Alerts section that distinguishes `not_configured`, `unavailable`,
  `degraded`, `configured_disabled`, and `healthy`, preserves channel, rule, or
  history inventory when another endpoint fails, opens recent event details,
  tests or enables/disables/deletes an exact observed channel, and enables,
  disables, or deletes an exact observed alert rule after confirmation;
- a central Cloudflare section that distinguishes `not_configured`,
  `unavailable`, and `healthy`, browses zones and DNS records, preserves zone
  and record inventory when optional email-routing inspection fails, and
  confirms cache purge, proxy toggle, record deletion, or installation-owned
  mail DNS reconciliation only after re-observing the exact provider state;
- a central Users section that loads the newest 200 panel accounts with explicit
  pagination totals, marks the current account when it is in the loaded page,
  displays role and TOTP state, creates accounts through a masked password form,
  and confirms profile, password, role, or deletion changes only after
  re-observing the exact existing user;
  it never substitutes managed-node operating-system accounts, never offers
  deletion for the current account, and retains final-administrator enforcement
  on the server;
- a searchable `Ctrl+K` quick-action launcher for sections, server switching,
  host maintenance, Nginx, loaded domain/certificate resources, service
  restarts, loaded containers, loaded PM2 processes, loaded manageable local
  BIND reload, loaded deploy/rollback or managed advertised deployment actions,
  loaded notification-channel tests and channel/rule enabled-state actions,
  loaded Cloudflare zone cache/mail actions and DNS proxy toggles,
  central panel-user creation plus loaded profile, password, role, and deletion
  actions,
  loaded release stage/install or agent upgrade/rollback actions,
  firewall profiles, loaded Fail2Ban unban identities, loaded cron job toggles
  or managed run-now actions, and
  loaded managed database-engine restart health checks;
- readable local-file and managed-journal source discovery with a scrollable,
  control-character-safe latest-200-lines viewer; and
- explicit offline and missing-capability states for managed nodes.

Panel-host metrics, service inventory, and process inventory have independent
failure states. If one endpoint is temporarily unavailable, the other loaded
sections remain usable, the failed section says that it is unavailable rather
than empty, and `r` retries the current server snapshot.

Keyboard controls:

| Key | Behavior |
| --- | --- |
| `←` / `→`, `Tab`, `1`–`9`, `0`, `P`, `Z`, `F`, `S`, `C`, `D`, `E`, `B`, `N`, `A`, `G`, `L`, `O`, `I`, or `U` | Switch sections; `0` opens Web Ops, `P` opens PHP-FPM, `Z` opens local DNS, `F` opens Firewall, `S` opens Security, `C` opens Cron, `D` opens Databases, `E` opens Files, `B` opens Backups, `N` opens Encrypted Snapshots, `A` opens Audit history, `G` opens Deploy, `L` opens Alerts, `O` opens Cloudflare, `I` opens Users, and `U` opens Updates |
| `j` / `k` or `↑` / `↓` | Move through the active list |
| `[` / `]` | Switch the active server from any section |
| `Enter` | Select a row, open its bounded action menu, inspect a backup/deployment job, or choose a selected encrypted snapshot's full/manifest/vhost staging-restore scope |
| `Ctrl+K` | Search sections, servers, and currently available bounded actions |
| `r` | Refresh the active server or reload the active container/PM2/PHP/log/Web Ops/DNS/backup/snapshot/audit/deploy/alerts/Cloudflare/Users/update resource; from a passed backup validation receipt, open restore confirmation |
| `s` / `i` | In local Updates, stage the freshly observed stable release or install the exact observed verified stage after confirmation |
| `u` / `o` | In managed Updates, upgrade to the freshly observed exact stable agent version or use the observed rollback snapshot after confirmation; in Files, `u` moves to the parent directory |
| `c` | From local DNS, run the complete configuration and zone check; from local Backups, open the fixed-profile backup creation wizard; from Encrypted Snapshots, open confirmed snapshot creation when the provider is healthy |
| `d` | From Encrypted Snapshots, select Google Drive or S3-compatible/MinIO while preserving the latest complete server-observed policy |
| `/` | In Audit history, edit the case-insensitive local filter; `Ctrl+U` clears the filter and `Enter` applies it |
| `Space` | Select or clear a measured disk cleanup target, or toggle an observed website folder in the backup wizard |
| `a` | In local DNS, create a zone from the zone list or add a record inside a zone; in Firewall, open fixed common inbound profiles; in Users, open the masked account-creation form; in the selective backup-folder dialog, select all when the observed inventory does not exceed the server limit |
| `e` | In local DNS, edit the selected record or load the selected SOA into its structured editor |
| `t` | From local DNS, open BIND reload confirmation; from local Firewall, open explicit UFW enable/disable confirmation |
| `x` | Open confirmation for selected cleanup targets, the exact observed local entry in Files, or the selected local DNS zone/record |
| `Backspace` | In Files, move to the parent directory without crossing the active root; in local DNS or Cloudflare, return from records to zones |
| `,` / `.` | In Files, switch to the previous or next configured root |
| `v` | Open logs for the selected PM2 process |
| `j` / `k`, `PgUp` / `PgDn`, `g` / `G` | Scroll lines or move to an edge in the log viewer |
| `?` | Open keyboard help |
| `q` or `Esc` | Close a dialog; `q` exits from the main screen |
| `Ctrl+C` | Exit from any screen |

Mutations never run from a single list key. The TUI opens a separate
confirmation dialog and requires `Y`; `Enter` alone does not confirm. Remote
controls are disabled while a node is offline or when its agent does not
advertise the required capability. Server-controlled names, process commands,
status details, pool configuration, log lines, audit details, and errors are stripped of terminal control
characters before rendering. Container, PM2, PHP, log, Nginx, domain, SSL, firewall,
cron, database, backup, and audit access uses the same
`container.read`, `container.action`, `pm2.read`, `pm2.action`, `php.read`,
`php.write`, `php.action`, and `logs.read`
capabilities plus the corresponding `nginx.*`, `domain.*`, `ssl.*`,
`firewall.*`, `cron.*`, `database.*`, `backup.*`, `deploy.*`, and `agent.update.*`
capabilities and the same agent-local allowlists as the web interface. Pressing
`Enter` on a Web Ops row opens only the actions supported by that resource and
target; every selected action still passes through the separate `Y`
confirmation dialog.

Press `U` to open Updates for the selected server. On Local, `s` rechecks the
release feed, then downloads and verifies the currently observed stable panel
artifact; `i` re-fetches the exact stage ID, version, and ready status before it
schedules installation through the automatic rollback runner. On a managed
node, `u` requires `agent.update.read` and `agent.update.action`, re-fetches the
healthy release state, exact latest version, and inactive lifecycle receipt,
then schedules upgrade. `o` similarly rechecks the current version, rollback
availability, and inactive operation before scheduling rollback. Each key opens
a confirmation dialog and only `y` mutates. While a scheduled or running
lifecycle operation remains visible, the normal refresh tick reloads its state.
An offline node or an agent without the read capability produces no remote
request and reports the missing boundary instead of showing a false empty or
healthy state. The same JSON-producing `hserverctl updates` commands remain the
stable automation interface.

Press `G` to open Deploy for the selected server. On Local, `Enter` loads the
target's current preflight checks and revision comparison before offering
readiness inspection, deploy, or rollback. Deploy and rollback re-fetch the
exact target, preflight, and revision observations before POST and stop if any
actionable field changed. Recent runs expose bounded server-provided logs. On a
managed node, the section requires `deploy.read`, lists agent-advertised target
actions and recent jobs, and requires `deploy.action` before scheduling only an
advertised `preflight`, `deploy`, `restart`, or `rollback` action. The client
re-fetches the exact managed plan before POST and rejects changed eligibility,
path, revision, status, or actions. Queued and running runs/jobs are refreshed
on the normal tick. Offline or missing-capability nodes make no remote request
and report the boundary explicitly. The JSON-producing `hserverctl deploy`
commands remain the stable local automation interface.

Press `L` to open Alerts. Notification channels, alert rules, and alert history
are central panel resources, so selecting a managed node reports the local-hub
boundary and issues no substitute request against that node. On Local, the
section loads channels, rules, and the newest 100 history events independently;
one unavailable endpoint remains an explicit warning without erasing the other
inventories. `Enter` on a channel offers test, enable/disable, and delete;
`Enter` on a rule offers enable/disable and delete; `Enter` on an event opens
its control-safe details. Every mutation uses a separate `Y` confirmation,
re-fetches the exact redacted channel or complete rule, rejects stale state,
and validates the returned status or updated resource. Secret values are never
loaded into the TUI. Channel/rule creation and credential replacement remain
available through the stable `hserverctl notify` command family and web panel.

Press `O` to open Cloudflare. This optional provider integration is owned by
the central panel, so selecting a managed node reports the Local boundary and
issues no substitute request. On Local, the section distinguishes a missing
token from a temporarily unavailable provider and a healthy integration.
`Enter` on a zone loads the exact zone, its DNS records, and optional email
routing state; an email-routing failure remains visible without erasing the
usable zone and record inventory. The zone action row offers complete cache
purge and installation-owned mail DNS reconciliation. `Enter` on a record
opens its control-safe details, proxy toggle for `A`, `AAAA`, or `CNAME`, and
deletion. Every mutation requires a separate `Y`, re-fetches the exact zone and
record before the provider call, rejects changed observations, and validates
the provider-specific receipt. The API token never enters the TUI. Record
creation or full content editing remains available through the stable
`hserverctl cloudflare` command family and web panel.

Press `Z` to open local DNS. `A` opens a validated zone-creation form from the
zone list or a record-creation form inside a zone. `E` edits the selected
record's value, TTL, and MX/SRV priority; on the SOA row it first loads the
structured primary nameserver, hostmaster, refresh, retry, expire, and minimum
fields. Form navigation uses `Tab` or arrow keys, `Ctrl+U` clears the active
field, and `Enter` validates through the same portable BIND contract before a
separate `Y` confirmation appears. `Enter` on a zone loads its current records,
`Backspace` returns to the zone list, and `C` runs `named-checkconf` plus all
zone checks in the control-safe viewer. `T` reloads BIND only when the observed
status reports reload readiness. `X` deletes the selected zone or non-SOA
record only after a separate `Y` confirmation. Immediately before update or
deletion the client reloads the relevant zone/SOA and rejects stale serial,
file, TTL, priority, type, name, value, or SOA-field observations. The stable
`hserverctl dns` command family remains available for scripts, shell completion,
lookup, export, and the same full mutation surface. Selecting a managed node
never redirects these local endpoints to the hub; the section reports the
missing agent capability boundary.

Press `B` to open Backups. On the panel host, lowercase `c` opens a fixed-profile wizard
for a full application backup with PostgreSQL or MariaDB, all PostgreSQL
databases, all MariaDB databases, or all configured website files. A full
application backup means the selected database-engine scope plus the configured
vhost root; it does not claim to image the complete operating system. Profiles
use portable level-6 compression. For file-bearing profiles, the next step
chooses either the complete configured vhost scope or up to 16 portable direct
folder identities returned by the server's discovery API. The TUI never accepts
a filesystem path, guessed domain, or arbitrary command, and the server rechecks
every selected identity against current host state before starting the job. If
target discovery is unavailable, artifact and job inventory remain usable and
only the all-configured-files scope is offered. The selected database dependency
must be configured on the host, and server preflight failures appear as failed
jobs without accepting an artifact. Full-backup profiles add an explicit
keep-all, 7, 14, or 30 artifact retention choice. Retention cleanup runs only
after successful full backup creation, is disclosed in the final confirmation,
and requires `Y`.

`Enter` on an artifact offers validation or deletion. Validation reads the
complete artifact without mutation and displays the server receipt. Only `R`
from that receipt opens the restore confirmation, and only `Y` starts the
asynchronous restore. On a managed node, `Enter` on a reported plan offers only
execution of that installation-owned plan and still requires `Y`. Managed
delete and restore are not exposed by this control center.

The local Backups section also includes jobs from the last 24 hours. Each row
shows status, phase, progress, ETA when reported, and the current message.
`Enter` opens a control-character-safe detail and log viewer; `R` reloads that
job from the API. While a pending or running job remains on the main Backups
screen, the normal TUI refresh tick also reloads the inventory automatically.
If job history is unavailable because of role or endpoint state, artifact
inventory remains usable and the screen reports that partial failure instead
of presenting an empty or falsely healthy result.

The quick-action launcher is contextual rather than a shell prompt. It omits
mutations for offline nodes and capabilities the selected agent does not
advertise. Type one or more words to filter, use `↑` / `↓`, and press `Enter`
to choose. Navigation and server switching happen directly; every operation
still opens the normal confirmation screen and requires `Y`. Container, PM2,
domain, certificate, cron, and managed database-engine shortcuts appear after
the corresponding inventory has been loaded, so the launcher never invents
resource identities.

## Read monitoring and metrics from scripts

The authenticated `monitoring` and `metrics` commands expose the panel-host
read-only monitoring surfaces without opening the TUI. JSON is the default for
automation; add `--format table` for deterministic tab-separated headers and
rows suitable for a terminal or a simple line-oriented consumer:

```bash
# Current local-host snapshot and the top process list.
hserverctl monitoring stats
hserverctl monitoring processes --format table

# The fixed live metrics snapshot reported by a managed node.
hserverctl monitoring stats --node edge-1
hserverctl monitoring stats --node edge-1 --format table

# Historical system samples. The server defaults to 1h when --range is omitted.
hserverctl metrics history --range 24h

# The nearest historical process snapshot, or the latest snapshot when --at is omitted.
hserverctl metrics processes --at 2026-08-28T12:00:00Z
hserverctl metrics processes timestamps --range 6h --format table

# Service status history and metrics-storage coverage.
hserverctl metrics services history --range 7d
hserverctl metrics summary --format table
```

The eight command forms map directly to these authenticated GET routes. The client
does not send a request body, and an omitted optional query flag remains
omitted so the server's documented default remains authoritative:

| CLI command | Method and route | Optional query |
| --- | --- | --- |
| `monitoring stats` | `GET /api/monitoring/stats` | none |
| `monitoring stats --node NODE` | `GET /api/nodes/{escaped NODE}/metrics` | none |
| `monitoring processes` | `GET /api/monitoring/processes` | none |
| `metrics history` | `GET /api/metrics/history` | `--range 1h\|6h\|24h\|7d\|30d` |
| `metrics processes` | `GET /api/metrics/processes` | `--at RFC3339` |
| `metrics processes timestamps` | `GET /api/metrics/processes/timestamps` | `--range 1h\|6h\|24h\|7d\|30d` |
| `metrics services history` | `GET /api/metrics/services/history` | `--range 1h\|6h\|24h\|7d\|30d` |
| `metrics summary` | `GET /api/metrics/summary` | none |

`--range` accepts only the five server-supported windows. The `metrics processes --at`
option preserves the supplied RFC3339 timestamp after local validation; it does
not round or convert the value. JSON retains the API field names and values,
including the resolution-specific `metrics history` data rows. Table output
uses the corresponding observed fields only: system snapshot sections,
process identity/resource columns, metric history rows, timestamps, service
history rows, or the four summary fields. Empty arrays are reported as `none`
in the table view and remain empty arrays in JSON.

`monitoring stats --node NODE` is read-only and emits a fixed typed projection of
the managed node's live observation: `observed_at`, CPU usage and core count,
one/five/fifteen-minute load, memory totals and usage, network receive/transmit
bytes, and root-disk totals and usage. The client validates required fields and
rejects non-finite or negative numeric values before rendering. `NODE` is escaped
as a path segment; no request is sent when `--node` is empty or when an invalid
option/positional argument is supplied. The `--node` selector is intentionally
available only on this live managed snapshot; historical `metrics` commands do
not accept it.

## Inspect audit history from scripts

Read the newest central audit events or page through older results without
opening the TUI:

```bash
# All panel activity, newest first.
hserverctl audit list

# Only local system operations.
hserverctl audit list --server local --limit 100

# One managed node, with server-side filters and an explicit UTC interval.
hserverctl audit list \
  --server edge-1 \
  --user Operator \
  --action cleanup \
  --resource system \
  --from 2026-08-01T00:00:00Z \
  --to 2026-08-31T23:59:59Z

# Continue from the next page.
hserverctl audit list --limit 100 --offset 100
```

The command is authenticated and read-only. `--limit` is restricted to
`1`–`200`, `--offset` cannot be negative, and `--server` accepts only `local`
or a portable managed-node identity. `--user` and `--action` are
case-insensitive substring filters; `--resource` is exact. `--from` and `--to`
must be RFC3339 timestamps, and the client rejects an inverted interval before
network access. Every successful result preserves the API's `data`, `total`,
`limit`, and `offset` fields as JSON so automation can paginate without treating
a partial page as complete history.

The TUI's Audit section follows the globally selected server. Local selects
local system operations, while an enrolled server selects centrally recorded
remote system operations for that exact node. Press `/` to filter only the
newest 100 loaded entries across user, action, resource, detail, and IP; use the
scriptable pagination command when older history is required. The screen says
how many entries are loaded and how many exist in total rather than silently
presenting the first page as complete.

## Manage panel users

Administrators can manage the central panel-user inventory without opening the
web interface. These commands always target the selected HServer control plane;
they do not pretend that panel accounts are managed-node operating-system
users.

The full-screen `hserverctl ui` Users section (`I`) provides the same central
boundary for inventory, account creation, profile and password replacement,
role changes, and deletion. Press `a` to create an account, or `Enter` on an
existing account to edit its name/email, replace its password, change its role,
or delete it. Password and confirmation fields are masked in both the form and
review screen. `Tab` or vertical arrows move between fields, `Ctrl+U` clears the
active field, and horizontal arrows select the exact role. Every TUI mutation
gets a separate `Y` confirmation; existing-account mutations refresh the exact
observed account first and reject changed state. The current account has no
delete action, while the server retains the final-administrator invariant.
Inventory pages older than the newest 200 remain available through the
scriptable commands below.

```bash
# Paginated credential-free inventory.
hserverctl users list
hserverctl users list --limit 100 --offset 100

# Omit --password-file on a real TTY for an echo-disabled prompt.
hserverctl users create --confirm \
  --email operator@example.com \
  --name "Operations" \
  --role manager \
  --password-file /run/secrets/new-panel-user-password

# Update only explicitly supplied fields. Password replacement is file-backed.
hserverctl users update --confirm --role admin 12
hserverctl users update --confirm \
  --password-file /run/secrets/replacement-panel-user-password \
  12

hserverctl users delete --confirm 12
```

Creation requires email and name; omitted role defaults server-side to
`viewer`. Roles are exactly `admin`, `manager`, or `viewer`. Every mutation
requires `--confirm`, update requires at least one explicit field, and IDs must
be positive canonical integers. Creation prompts securely when no password file
is supplied; non-interactive creation and every scripted password replacement
use a regular, non-symlink file inaccessible to group and other users. Password
and bearer-token values are never printed in successful output.

The API revalidates the complete payload, rejects unknown fields and duplicate
email addresses, prevents an administrator from deleting their current
account, and atomically refuses deletion or demotion of the final administrator.
Profile and password updates commit together or both roll back. Successful
deletion returns a local JSON receipt only after the panel responds with
success. User responses contain role and TOTP-enabled state but never password
hashes or TOTP secrets.

## Manage notification channels

Notification channel automation uses the same write-only secret boundary as
the web panel. Passwords, bot tokens, and webhook URLs are read from regular,
non-symlink files that are inaccessible to group and other users; they never
belong in command arguments or successful JSON output.

```bash
# Read redacted inventory and one channel.
hserverctl notify channels
hserverctl notify channel 1

# Create an email channel. Implicit TLS and STARTTLS choices remain explicit.
printf '%s\n' 'SMTP_PASSWORD' >./smtp-password
chmod 600 ./smtp-password
hserverctl notify create --confirm \
  --name "Primary email" \
  --type email \
  --smtp-host smtp.example.com \
  --smtp-port 587 \
  --smtp-user alerts@example.com \
  --from alerts@example.com \
  --to admin@example.com \
  --smtp-tls false \
  --credential-file ./smtp-password

# Telegram, Discord, and Slack use the same protected credential input.
hserverctl notify create --confirm \
  --name "Telegram operations" \
  --type telegram \
  --chat-id -100123456789 \
  --credential-file ./telegram-bot-token
hserverctl notify create --confirm \
  --name "Discord operations" \
  --type discord \
  --username HServer \
  --credential-file ./discord-webhook-url
hserverctl notify create --confirm \
  --name "Slack operations" \
  --type slack \
  --username HServer \
  --channel '#operations' \
  --credential-file ./slack-webhook-url

# Omitted update fields and an omitted credential preserve their current value.
hserverctl notify update --confirm --enabled false 1
hserverctl notify update --confirm \
  --credential-file ./rotated-smtp-password \
  1

# Credential removal, real provider delivery tests, and deletion are explicit.
hserverctl notify update --confirm --clear-credential 1
hserverctl notify test --confirm 1
hserverctl notify delete --confirm 1
```

Create and update accept only `email`, `telegram`, `discord`, and `slack`.
Email creation requires host, port, sender, recipients, and an explicit
`--smtp-tls true|false`; Telegram creation requires a non-zero chat ID.
`--credential-file` is optional so a disabled or intentionally unconfigured
channel can be staged, while `--clear-credential` is update-only and cannot be
combined with a replacement credential. Updates first read the selected
redacted channel so omitted name, type-specific fields, enabled state, and
credential remain unchanged. Sending a test notification and every mutation
requires `--confirm`; API manager/admin role checks remain authoritative.

## Manage alert rules and history

Alert rules use the evaluator's canonical type names and the same role checks as
the web panel. Successful reads and mutations remain JSON for scripting:

```bash
# Inventory, detail, and bounded dispatch history.
hserverctl notify rules
hserverctl notify rule 4
hserverctl notify history --limit 100 --offset 0

# Percentage rules trigger at or above the threshold.
hserverctl notify rule-create --confirm \
  --name "CPU pressure" \
  --type cpu_usage \
  --threshold 90 \
  --duration-mins 5 \
  --cooldown-mins 30
hserverctl notify rule-create --confirm \
  --name "Data disk pressure" \
  --type disk_usage \
  --threshold 85 \
  --target /srv

# Certificate expiry triggers at or below the remaining-day threshold.
hserverctl notify rule-create --confirm \
  --name "Panel certificate expiry" \
  --type ssl_expiry \
  --threshold 14 \
  --target panel.example.com

# Service-down rules need a portable systemd unit and no threshold flag.
hserverctl notify rule-create --confirm \
  --name "Nginx unavailable" \
  --type service_down \
  --target nginx.service

# Failed-login thresholds are whole counts observed in one minute.
hserverctl notify rule-create --confirm \
  --name "SSH login attacks" \
  --type failed_logins \
  --threshold 5

# Partial updates preserve every omitted field.
hserverctl notify rule-update --confirm --enabled false 4
hserverctl notify rule-update --confirm --threshold 95 4
hserverctl notify rule-delete --confirm 4
```

Canonical types are `cpu_usage`, `memory_usage`, `disk_usage`, `ssl_expiry`,
`service_down`, and `failed_logins`; the legacy CLI aliases `cpu`, `memory`, and
`disk` normalize before transmission. Percentage thresholds accept `0–100`,
certificate days accept `0–3650`, and failed-login counts accept whole values
from `1–1000000`. Duration accepts `0–1440` minutes and cooldown accepts
`1–10080`. Disk targets are absolute mount paths, certificate targets are DNS
names, and service targets are bounded systemd unit names. All rule mutations
require `--confirm`; updates read and validate the existing rule, then send only
the explicitly changed fields.

## Manage local BIND DNS from scripts

The `dns` command family manages the BIND installation on the panel host. It
does not redirect a local operation to Cloudflare or pretend that a managed
node has BIND capabilities it does not advertise.

```bash
# Read readiness, inventory, zone detail, records, SOA, and propagation state.
hserverctl dns status
hserverctl dns zones
hserverctl dns zone example.com
hserverctl dns records example.com
hserverctl dns soa example.com
hserverctl dns lookup --type A www.example.com
hserverctl dns check

# Print the raw zone to stdout, or create one new protected export file.
hserverctl dns export example.com
hserverctl dns export --output ./db.example.com example.com

# Create or delete a complete local master zone.
hserverctl dns zone-create --confirm --ip 192.0.2.10 example.com
hserverctl dns zone-delete --confirm example.com

# Add, replace, and delete one exact record.
hserverctl dns record-add --confirm \
  --name www --type A --value 192.0.2.20 --ttl 300 \
  example.com
hserverctl dns record-update --confirm \
  --name www --type A \
  --old-value 192.0.2.20 --new-value 192.0.2.21 \
  example.com
hserverctl dns record-delete --confirm \
  --name www --type A --value 192.0.2.21 \
  example.com

# A partial CLI edit first reads the complete current SOA, preserves omitted
# fields, validates the resulting full replacement, and then sends one PUT.
hserverctl dns soa-update --confirm --refresh 7200 example.com

# Explicitly reload all configured zones when auto-reload was disabled.
hserverctl dns reload --confirm
```

Zone identities are canonical lower-case DNS names. Record types normalize to
uppercase without freezing the CLI to a finite type list; owner names support
`@`, wildcard records, and underscore service labels. A/AAAA values, numeric
TTL values, priority, SOA names, and SOA timers are validated by the shared
service contract before any request is sent. Control characters are rejected.
Every mutation and reload requires `--confirm`, and every optional `--wait`
must be positive. Record operations default to `--auto-reload=true`; pass
`--auto-reload=false` only when a later explicit reload is intended.

`dns check` always prints the complete JSON diagnostic. An invalid BIND
configuration is represented by `"ok": false`, not hidden behind a generic
transport error. `dns export --output` refuses to overwrite an existing path
and creates the new file with mode `0600`.

## Manage Cloudflare DNS from scripts

Cloudflare commands use the panel's optional provider integration; the API token
stays on the HServer host and is never accepted as a CLI argument or returned in
output. When the integration is not configured, the API keeps the explicit
`HTTP 503` state instead of presenting an empty zone inventory.

```bash
# Read provider inventory and optional email-routing state.
hserverctl cloudflare zones
hserverctl cloudflare zone ZONE_ID
hserverctl cloudflare records ZONE_ID
hserverctl cloudflare records --type A --name www.example.com ZONE_ID
hserverctl cloudflare email-routing ZONE_ID

# Create and partially update a record. Update first reads the exact current
# record from zone inventory so omitted fields survive the provider's full PUT.
hserverctl cloudflare record-create --confirm \
  --type A \
  --name www.example.com \
  --content 192.0.2.10 \
  --ttl 300 \
  --proxied true \
  ZONE_ID
hserverctl cloudflare record-update --confirm \
  --content 192.0.2.20 \
  ZONE_ID RECORD_ID

# Proxy, deletion, complete cache purge, and installation-owned mail DNS
# reconciliation are separate confirmed operations.
hserverctl cloudflare record-proxy --confirm --proxied false ZONE_ID RECORD_ID
hserverctl cloudflare record-delete --confirm ZONE_ID RECORD_ID
hserverctl cloudflare purge --confirm ZONE_ID
hserverctl cloudflare mail-autofix --confirm example.com
```

Record types are normalized to uppercase without freezing the CLI to a provider
type list. Names and content reject control characters, TTL is `1` for automatic
or `30–86400` seconds, priority is `0–65535`, and proxy creation is accepted only
for `A`, `AAAA`, and `CNAME`. Every mutation requires `--confirm`; optional
`--wait` values must be positive. `record-delete` prints a local JSON deletion
receipt only after the API returns a successful no-content response.
`mail-autofix` remains unavailable until the installation-owned mail DNS
contract is configured on the panel.

## Operate the optional mail service from scripts

The mail CLI calls the same authenticated panel endpoints used by the web
panel. It is intentionally local to the HServer panel: there is no `--node`
flag and no direct Stalwart credential or provider URL input. The optional
Stalwart boundary remains truthful. A missing or unavailable integration is
reported as `HTTP 503` (or as an `unavailable`/`not_configured` source in the
overview) rather than being presented as an empty or healthy inventory.

Read the service status, the combined service overview, or the mail status
projection:

```bash
hserverctl mail service status
hserverctl mail service overview
hserverctl mail status
```

Read bounded service logs. The normal endpoint accepts `--lines` from `1`
through `5000`; search and delivery queries are sent through encoded query
parameters and reject control characters before a request is made:

```bash
hserverctl mail logs --lines 100
hserverctl mail logs normal --lines 100
hserverctl mail logs search --query 'from=alice@example.com delivered'
hserverctl mail logs delivery --email alice@example.com
```

Inspect the outbound queue and perform an explicitly confirmed retry or
deletion. Queue listing is bounded to `1` through `1000` messages:

```bash
hserverctl mail queue list --limit 100
hserverctl mail queue retry --confirm QUEUE_MESSAGE_ID
hserverctl mail queue delete --confirm QUEUE_MESSAGE_ID
```

List, create, or delete mail domains. Both domain mutations require the
explicit `--confirm` safety flag:

```bash
hserverctl mail domains list
hserverctl mail domains create --confirm example.com
hserverctl mail domains delete --confirm example.com
```

The account and alias inventory commands remain available with an exact
`--domain` filter:

```bash
hserverctl mail accounts
hserverctl mail accounts --domain example.com
hserverctl mail aliases
hserverctl mail aliases --domain example.com
```

All commands print the handler's typed JSON projections. Account, alias,
queue, domain, and status outputs omit provider-only fields rather than passing
through a raw provider response body. Log entries are also typed JSON, so
control characters are escaped instead of being interpolated into terminal
output; the requested log message itself is preserved. Errors likewise use the
bounded API error envelope instead of dumping response content. Path values and
query values are escaped before requests, and provider secrets are not accepted
as CLI input.

## Manage system services from scripts

Inspect the panel host's bounded service inventory and journal, or the latest
service inventory observed from one managed node:

```bash
# Panel host
hserverctl services list
hserverctl services logs --lines 100 nginx

# Managed node inventory retains last observed data and reports whether the
# node is online and its read/action capabilities are currently available.
hserverctl services list --node edge-1
```

Start, stop, or restart one service through the existing local or managed-node
control boundary:

```bash
hserverctl services action --confirm nginx restart
hserverctl services action --confirm --node edge-1 nginx.service restart
```

Service identities are limited to 1–128 portable systemd-name characters, and
actions are exactly `start`, `stop`, or `restart`. Every mutation requires
`--confirm`; `--wait DURATION` must be positive and at most seven minutes. A
managed mutation first refreshes the exact node and refuses to create a task
when it is offline or does not advertise `service.action`. It then waits for
the capability-scoped task's terminal receipt. The agent still enforces its
installation-owned `HSERVER_AGENT_ALLOWED_SERVICES` allowlist. Service-specific
journal reads remain local; managed logs use the separately allowlisted
`hserverctl logs` commands rather than inventing a remote journal source.

## Manage containers from scripts

Container inventory and fixed lifecycle operations are also available without
opening the TUI. Every successful response remains formatted JSON:

```bash
# Panel host
hserverctl containers status
hserverctl containers list
hserverctl containers action --confirm web restart

# Managed node
hserverctl containers list --node edge-1
hserverctl containers action --confirm --node edge-1 web restart
hserverctl containers logs --tail 250 web

# Local Docker image inventory and lifecycle
hserverctl images list
hserverctl images pull --confirm nginx:1.27
hserverctl images delete --confirm sha256:abc123
```

All mutations require `--confirm`. The local panel host accepts the backend's
fixed `start`, `stop`, `restart`, `pause`, `unpause`, and non-force `remove`
actions. Managed nodes accept only the portable `start`, `stop`, and `restart`
set and still enforce the agent's `container.action` capability and local
allowlist. An optional `--wait DURATION` overrides the seven-minute mutation
wait without changing the global request timeout.

`containers logs` reads one local container through
`GET /api/docker/containers/{id}/logs`; `--tail` is bounded to 1–1000 lines
and defaults to 200. Managed-node log reads are intentionally not implied
because the agent contract does not expose this Docker route.

The `images` commands use the local Docker image routes. `images list` is a
read-only authenticated inventory. `images pull --confirm IMAGE` sends the
exact `{"name":"IMAGE"}` body to `POST /api/docker/images/pull`, and
`images delete --confirm IMAGE` sends `DELETE /api/docker/images/{id}`.
Image references are bounded before any request; image mutations require the
explicit `--confirm` flag and support a positive `--wait DURATION`. All four
commands retain the API response as formatted JSON for automation.

## Signal a managed-node process

Use the dedicated process-signal command after reading a process inventory row
with its stable PID and start-time identity:

```bash
hserverctl processes signal \
  --node edge-1 \
  --pid 4242 \
  --start-time 987654 \
  --signal term \
  --confirm
```

`--node`, `--pid`, `--start-time`, `--signal`, and `--confirm` are required.
The node must be a managed node; this command deliberately does not signal the
local panel host. PID values must be greater than `1`, start-time must be a
positive value from the current process inventory, and the signal is exactly
`term` or `kill`. The command refuses invalid input or a missing confirmation
before making a request and includes the selected node, PID, start-time, and
signal in the refusal.

The command sends the typed JSON body accepted by the managed-node endpoint:
`{"pid":4242,"startTime":987654,"signal":"term"}`. The API waits for the
capability-scoped agent receipt, so the CLI does not create a generic
`tasks create` payload or poll a second task endpoint. Use `--wait DURATION` to
override the local request timeout when an agent needs more time; the API's
bounded managed-node wait still applies. Successful output is the API's JSON
receipt, including `message`, `exited`, and `confirmed`.

## Manage deployments from scripts

The local deployment manager is available as JSON-producing CLI commands:

```bash
hserverctl deploy templates
hserverctl deploy targets

# Create a Docker Compose target. The signing token file must be mode 0600.
hserverctl deploy target create --confirm \
  --name "Example App" \
  --project-dir /srv/apps/example-app \
  --type compose \
  --repo https://github.com/example/example-app.git \
  --branch main \
  --compose-file compose.yaml \
  --webhook-token-file ~/.config/hserver/example-app-webhook \
  --auto-deploy

# Script targets read the deploy body from a bounded local UTF-8 file.
hserverctl deploy target create --confirm \
  --name "Example Worker" \
  --project-dir /srv/apps/example-worker \
  --type script \
  --repo git@example.com:example/example-worker.git \
  --branch main \
  --script-file ./deploy-worker.sh

# Update only the selected fields after a fresh target observation.
hserverctl deploy target update --confirm \
  --name "Example App Production" \
  --branch stable \
  12

# Replace or explicitly clear the protected webhook secret.
hserverctl deploy target update --confirm \
  --webhook-token-file ~/.config/hserver/example-app-webhook-next \
  12
hserverctl deploy target update --confirm \
  --clear-webhook-token \
  --auto-deploy=false \
  12

hserverctl deploy target delete --confirm 12

# Values stay out of argv and CLI responses.
hserverctl deploy environment list 12
hserverctl deploy environment set --confirm \
  --value-file ~/.config/hserver/example-app-database-url \
  12 DATABASE_URL
hserverctl deploy environment delete --confirm 12 DATABASE_URL

# Installation-owned Nginx mapping and active loopback health.
hserverctl deploy domains 12
hserverctl deploy domain create --confirm \
  --service web \
  --host-port 8080 \
  12 app.example.com
hserverctl deploy domain health 12 41

# Transactional Certbot/Nginx TLS lifecycle.
hserverctl deploy domain tls enable --confirm \
  --email admin@example.com \
  12 41
hserverctl deploy domain tls disable --confirm 12 41
hserverctl deploy domain delete --confirm 12 41

hserverctl deploy staging create --confirm \
  --name "App Staging" \
  --branch develop \
  --project-dir /srv/apps/app-staging \
  12
hserverctl deploy revision 12
hserverctl deploy preflight 12
hserverctl deploy history --target 12 --limit 25
hserverctl deploy logs 71

hserverctl deploy services 12
hserverctl deploy service logs --tail 250 12 web
hserverctl deploy service action --confirm 12 web restart

hserverctl deploy run --confirm 12
hserverctl deploy rollback --confirm 12
```

Managed-node deployment inventory and actions use the agent-reported API
surface. The read commands require an explicit non-empty node ID and preserve
the JSON returned by the panel:

```bash
hserverctl deploy remote targets --node edge-1
hserverctl deploy remote jobs --node edge-1

# The action command refreshes the target inventory immediately before the
# mutation. The target must be eligible and advertise the exact action.
hserverctl deploy remote action --confirm --node edge-1 example-app deploy
```

`deploy remote action` accepts only `preflight`, `deploy`, `restart`, or
`rollback`. It looks up the exact target ID in the fresh inventory, requires
`eligible: true`, and requires that the same exact action appears in the
target's advertised `actions` list before issuing the empty-body POST to
`/api/nodes/{node}/deploy/{target}/actions/{action}`. Missing confirmation,
empty node or target IDs, an absent/ineligible target, an unsupported action,
or an action that is not advertised stop before any mutation request. Node,
target, and action path segments are URL-escaped; successful output is the
panel's JSON job receipt without a local wrapper.

Managed project-domain mappings and TLS operations are also available as
scriptable JSON commands. Domain and target identities are validated as ASCII
hostnames/identities, and every path segment is URL-escaped:

```bash
hserverctl deploy remote domains --node edge-1 example-app
hserverctl deploy remote domain-create --confirm --node edge-1 example-app app.example.com
hserverctl deploy remote domain-health --node edge-1 example-app app.example.com
hserverctl deploy remote tls-provision --confirm --node edge-1 \
  --email admin@example.com example-app app.example.com
hserverctl deploy remote tls-renew --confirm --node edge-1 example-app app.example.com
hserverctl deploy remote tls-delete --confirm --node edge-1 example-app app.example.com
hserverctl deploy remote domain-delete --confirm --node edge-1 example-app app.example.com
```

Before each remote domain/TLS mutation, the CLI performs a fresh
`GET /api/nodes/{node}/deploy` inventory read, requires the exact target to be
present and `eligible: true`, then calls only the corresponding managed route:
`POST /api/nodes/{node}/deploy/{target}/domains` for domain creation,
`DELETE /api/nodes/{node}/deploy/{target}/domains/{domain}` for deletion, and
`POST /api/nodes/{node}/deploy/{target}/domains/{domain}/tls`
for TLS provisioning,
`POST /api/nodes/{node}/deploy/{target}/domains/{domain}/tls/renew` for renewal,
and `DELETE /api/nodes/{node}/deploy/{target}/domains/{domain}/tls` for
removal. `domain-health` is read-only and calls
`GET /api/nodes/{node}/deploy/{target}/domains/{domain}/health`; `domains` lists
the target's mappings. All
mutations require explicit `--confirm`, use bounded `--wait` values, and
preserve the panel's JSON receipt. The panel and managed agent remain the
authorities for `deploy.domain.read` and `deploy.domain.action` capability
availability; the CLI checks those node-advertised capabilities and does not
infer them from a target inventory that lacks capability metadata.

`deploy templates` reads the installation-owned template inventory used by the
add-target dialog. It distinguishes `not_configured`, `healthy`, and
`unavailable`, returns valid templates even when another local file is invalid,
and never returns repository credentials, project directories, or webhook
secrets. The command is read-only and requires an administrator context.

`deploy target create` registers a local Compose or script deployment without
placing repository credentials or webhook secrets on the command line. Names,
absolute project paths, credential-free HTTPS/SSH repository URLs, Git branches,
relative Compose paths, deployment types, and webhook providers are validated
before network access. Compose is the default type. Script targets require a
regular, non-symlink, NUL-free UTF-8 `--script-file` of at most 64 KiB. Optional
webhook tokens are read from a regular mode-`0600` file, never printed by the
CLI, and are required when `--auto-deploy` is selected. GitLab uses the API's
`whsec_` signing-token format; GitHub accepts its installation-owned signing
secret. Every create requires `--confirm`.

`deploy target delete` removes the exact positive target ID and also requires
`--confirm`. The API refuses production-target deletion while project domains
or derived staging targets remain attached, so the CLI never silently cascades
those resources.

`deploy target update` first refreshes the secret-free target inventory, finds
the exact positive ID, overlays only explicitly supplied options, and sends the
complete replacement with the observed `updatedAt` value. The API returns
`409 Conflict` rather than overwriting a newer concurrent change. A missing or
empty `webhookToken` preserves the existing protected secret; replacement is
possible only through `--webhook-token-file`, while deletion requires the
separate `--clear-webhook-token` flag and `--auto-deploy=false`. Changing the
webhook provider also requires a replacement token so a GitHub secret is never
silently reused as a GitLab signing token. Boolean state uses explicit values,
for example `--active=false` and `--auto-deploy=false`. No-op, stale,
unconfirmed, executor-incompatible, or locally invalid updates are refused.

`deploy environment` manages the installation-owned, write-only environment of
a local Compose target. `list` returns only `configured` and sorted variable
names; values have no read command and are never present in API or CLI
responses. `set` requires `--confirm` and reads the replacement value from a
regular, non-symlink file that is inaccessible to group and other users. Input
is UTF-8, bounded to 64 KiB, permits an intentionally empty value, strips a
normal trailing line ending, and rejects embedded NUL, carriage return, newline,
or single quote characters before network access. Keys use the portable
`[A-Za-z_][A-Za-z0-9_]{0,127}` contract. `delete` also requires `--confirm`,
removes only the exact key, and deletes the installation-owned secret file when
the final variable is removed. Script targets and installations without the
environment store return their explicit API conflict or unavailable state.

`deploy domains` lists installation-owned Nginx mappings for one local Compose
target, including the fixed `http://127.0.0.1:PORT` upstream and observed TLS
state. `deploy domain create` accepts only an exact portable ASCII hostname, a
bounded Compose service identity, and a published host port from `1` through
`65535`; callers cannot supply raw Nginx or an arbitrary upstream. Creation is
confirmation-bound, validates inputs before network access, and uses a bounded
mutation timeout. It activates only HServer's owned mapping and does not claim
that DNS was changed or the application is healthy.

`deploy domain health` performs the API's active three-second loopback probe.
HTTP `200`–`399` is `healthy`, another response is `unhealthy`, and no response
is `unavailable`. TLS enablement requires `--confirm`, optionally accepts one
plain ACME email with `--email`, and uses the installation-owned Certbot HTTP-01
flow. Omitting email selects the API's documented no-email registration mode.
TLS disablement transactionally restores HTTP while retaining certificate files
for rollback or reuse. Exact-ID domain deletion deactivates only the owned
mapping and returns a CLI JSON receipt even though the API correctly responds
with an empty `204 No Content`. DNS must already point to the panel host before
health or certificate issuance can succeed.

`deploy staging create` derives an isolated staging target from the final
production target ID. `--project-dir` must be an absolute path and `--confirm`
is mandatory before any request is sent. Name and branch are optional and
default from the production source. The returned JSON receipt proves that
environment values, webhook signing secrets, domains, TLS, and DNS state were
not copied and that auto-deploy starts disabled.

Target and run identities must be positive integers. Compose service identities
use the same bounded syntax as the API, log tails are limited to `1`–`1000`,
and service actions are exactly `start`, `stop`, `restart`, or `recreate`.
Every target creation/deletion, staging creation, deployment, rollback, and
service mutation requires `--confirm`; invalid or unconfirmed requests are
rejected before network access. `--wait DURATION` changes only the selected
mutation request timeout.

`deploy revision` is read-only. It compares the current local checkout, newest
successful deployment, and current rollback candidate without fetching remote
refs. The response distinguishes an unprovisioned checkout from an unreadable
one and reports tracked changes, commit distance, changed files, insertions,
and deletions.

## Manage uptime monitoring from scripts

```bash
hserverctl uptime summary
hserverctl uptime monitors
hserverctl uptime monitor get 41

# Create an active HTTP monitor.
hserverctl uptime monitor create --confirm \
  --name "Public API" \
  --type http \
  --url https://app.example.com/health \
  --method HEAD \
  --accepted-statuscodes 200-299,301 \
  --tls-check \
  --alert-channel 7 \
  --alert-channel 9

# Update only the fields named on the command line. The target URL is an
# example target; replace it with the endpoint you actually operate.
hserverctl uptime monitor update --confirm \
  --name "Public API" \
  --url https://example.com/health \
  --method HEAD \
  --interval-secs 60 \
  --timeout-secs 10 \
  --accepted-statuscodes 200-299 \
  41

# Request headers and bodies can be supplied from protected files instead of
# shell arguments (useful for multiline values and JSON request bodies).
chmod 600 ./monitor-headers ./monitor-body
hserverctl uptime monitor update --confirm \
  --req-headers-file ./monitor-headers \
  --req-body-file ./monitor-body \
  41

# Explicit clear operations are separate from replacement values.
hserverctl uptime monitor update --confirm \
  --clear-req-headers \
  --clear-req-body \
  --clear-description \
  --clear-alert-channels \
  41

# TCP, ping, and DNS targets use a hostname instead of a URL.
hserverctl uptime monitor create --confirm \
  --name PostgreSQL \
  --type tcp \
  --hostname db.example.com \
  --port 5432
hserverctl uptime monitor create --confirm \
  --name "Primary DNS" \
  --type dns \
  --hostname example.com \
  --dns-record-type A \
  --dns-expected 192.0.2.10

hserverctl uptime monitor heartbeats --hours 168 41
hserverctl uptime monitor stats 41
hserverctl uptime incidents --monitor 41 --limit 25

# These operations change monitoring state or perform an active probe.
hserverctl uptime monitor check --confirm 41
hserverctl uptime monitor pause --confirm 41
hserverctl uptime monitor resume --confirm 41
hserverctl uptime monitor delete --confirm 41

# Create HTTPS monitors for local domains that are not monitored yet.
hserverctl uptime import-domains --confirm

# Read and update the global uptime defaults (settings update is admin-only).
hserverctl uptime settings
hserverctl uptime settings update --confirm \
  --retention-days 120 \
  --compact-after-days 30 \
  --default-interval-secs 60 \
  --default-timeout-secs 15 \
  --default-channel 7 \
  --default-channel 9
hserverctl uptime settings update --confirm --clear-default-channels

# Status-page inventory remains the plural, read-only command.
hserverctl uptime status-pages

# Create a public page. --monitor/--monitor-id is repeatable.
hserverctl uptime status-page create --confirm \
  --slug public-api \
  --title "Public API" \
  --description "Availability for the public API" \
  --logo-url https://example.com/logo.svg \
  --theme auto \
  --history-days 90 \
  --visibility public \
  --monitor 41 \
  --monitor 42

# Update selected fields, or replace the complete monitor list explicitly.
hserverctl uptime status-page update --confirm \
  --title "Public API status" \
  --private \
  --clear-description \
  --clear-logo-url \
  7
hserverctl uptime status-page update --confirm \
  --clear-monitors \
  7

# Delete a status page (admin-only).
hserverctl uptime status-page delete --confirm 7
```

The uptime commands use fixed authenticated API routes and produce JSON suitable
for automation. `summary`, `monitors`, `monitor get`, `heartbeats`, `stats`,
`incidents`, `status-pages`, and `settings` are read-only. Monitor creation,
update, deletion, pause/resume, active `check`, bulk local-domain import,
settings update, and every status-page create/update/delete require explicit
`--confirm`; invalid or unconfirmed mutations are rejected before the mutation
request is sent. Monitor action commands (`check`, `pause`, `resume`, and
`delete`) retain the bounded `--wait DURATION` behavior documented above;
monitor creation, bulk import, settings, and status-page commands do not add
an unbounded wait flag.

HTTP monitor creation accepts only an absolute credential-free `http://` or
`https://` URL, one supported method, normalized `100`–`599` status values or
ranges, bounded timing/retry values, and optional deduplicated positive alert
channel IDs. TCP requires an explicit port from `1` through `65535`. TCP, ping,
and DNS accept only a portable ASCII hostname or an IP address; DNS record types
are limited to `A`, `AAAA`, `MX`, and `CNAME`. These local syntax checks do not
replace the API's outbound-target policy: the server validates every target
again before it persists or probes it.

`uptime import-domains` uses the installation's observed local-domain inventory,
creates only missing HTTPS monitors, and returns exact created, skipped, and
failed counts. It does not create domains, change DNS, issue certificates, or
claim a target is healthy.

Monitor updates expose the same target, timing, HTTP, DNS, description, and
alert fields as creation, with zero values meaning "not selected" unless the
flag was explicitly supplied. `--alert-channel` is repeatable. The update
command sends only explicitly selected fields; the API merges those values with
the current monitor and validates the resulting monitor atomically. Both the
CLI and API therefore preserve omitted fields. Use
`--clear-url`, `--clear-hostname`, `--clear-keyword`, `--clear-req-headers`,
`--clear-req-body`, `--clear-description`, or `--clear-alert-channels` when an
existing value must be removed; a clear flag cannot be combined with its
replacement flag.

`--req-headers` accepts replacement headers either as a JSON string object or
as one `Header: Value` per line;
`--req-body` accepts a replacement UTF-8 body. Prefer `--req-headers-file`
and `--req-body-file` for multiline or sensitive content. Each input must be a
regular, non-symlink file inaccessible to group and other users; header files
are limited to 32768 bytes and body files to 65536 bytes. The CLI reads the
file locally and sends its content in the authenticated JSON request. Empty
values can clear the corresponding field, but the named clear flags make that
intent explicit.

`uptime settings` reads effective defaults. `settings update` requires at least
one option: `--retention-days` accepts `2`–`3650`, `--compact-after-days`
accepts `1`–`365`, `--default-interval-secs` accepts `10`–`86400`, and
`--default-timeout-secs` accepts `1`–`300`. The compact-after value must remain
below retention, and the default timeout cannot exceed the default interval.
Repeat `--default-channel ID` for a deduplicated list, or use
`--clear-default-channels` to replace that list with `[]`; those two forms
cannot be combined.

`uptime status-page create` requires `--slug` and `--title`. It accepts
`--description`, `--logo-url`, `--theme auto|light|dark`, `--history-days`
(`1`–`3650`), `--visibility public|private`, or the equivalent `--public` /
`--private` flags. `--monitor ID` is repeatable; `--monitor-id ID` is an alias.
Create defaults to an `auto` theme, a 90-day history window, and public
visibility. Status-page update accepts the same fields as replacements and
requires at least one changed option. It first reads status-page inventory,
merges the selected fields, then sends one complete PUT replacement; omitted
fields survive. Supplying any `--monitor` flags replaces the complete monitor
list; `--monitor-id` is the equivalent repeatable alias. `--clear-monitors`
replaces it with an empty list. Use
`--clear-description` or `--clear-logo-url` (also available as
`--clear-logo`) to remove optional values. Clear flags cannot be combined with
their replacement flags.

The API role boundary remains authoritative: authenticated accounts may read
monitor and status-page data and effective settings; monitor create/update,
pause, resume, and active check plus status-page create/update require a
manager or administrator role. Monitor deletion, local-domain import, settings
update, and status-page deletion require an administrator role. The CLI's
`--confirm` is an additional local intent gate and never elevates these roles.

## Read logs from scripts

Discover source identities first, then pass one unchanged to `logs read`:

```bash
# Panel host: source is an allowed absolute path returned by the server
hserverctl logs sources
hserverctl logs read --source /var/log/nginx/error.log --lines 200

# Managed node: source is an agent-advertised journal family
hserverctl logs sources --node edge-1
hserverctl logs read --node edge-1 --source nginx --lines 200
```

The local command accepts `1`–`5000` lines, matching the local log API. Managed
reads accept `1`–`500`, matching the bounded agent route. Query values are URL
encoded by the client, and a missing source or out-of-range line count is
rejected before any request is sent. Managed source discovery reports the
node's observed online state, `logs.read` availability, and source list from
its latest heartbeat.

## Manage PM2 applications

The PM2 command family uses the installation-configured unprivileged PM2 owner,
home, and binary rather than starting or discovering a separate root daemon.
PM2 deployment also requires an explicit `HSERVER_PM2_ALLOWED_ROOTS` allowlist;
an empty allowlist keeps deployment `not_configured` and never falls back to a
vhost, `/home`, or `/opt` path. The following is an explicit opt-in example for
an unprivileged `deploy` account; replace every path with the target's local
configuration:

```dotenv
HSERVER_PM2_USER=deploy
HSERVER_PM2_HOME=/home/deploy/.pm2
HSERVER_PM2_BIN=pm2
HSERVER_PM2_ALLOWED_ROOTS=/srv/hserver/sites,/home/deploy/apps
```

```bash
# Panel host
hserverctl pm2 list
hserverctl pm2 get api
hserverctl pm2 logs --lines 200 api
hserverctl pm2 action --confirm api reload
hserverctl pm2 save --confirm

# Managed node
hserverctl pm2 list --node edge-1
hserverctl pm2 logs --node edge-1 --lines 200 api
hserverctl pm2 action --confirm --node edge-1 api restart
```

Local actions are `start`, `stop`, `restart`, `reload`, and `delete`. Deleting a
local process removes it from the active PM2 list without deleting application
files; run `pm2 save --confirm` separately to persist the new list. The TUI
exposes the same explicit process-list save operation. Managed agents accept
only `start`, `stop`, `restart`, and `reload`, then save the process list after
the allowlisted action completes. Local logs accept `1`–`5000` lines and
managed logs accept `1`–`500`. Every action and save mutation requires explicit
confirmation, while inventory and logs remain JSON-producing read operations.

## Manage PHP-FPM

PHP-FPM inventory and lifecycle commands address the panel host when `--node`
is omitted and an enrolled managed node when it is present:

```bash
# Panel host
hserverctl php versions
hserverctl php pools --version 8.4
hserverctl php pool get 8.4 portal
hserverctl php config get 8.4 portal
hserverctl php config edit --confirm --reload 8.4 portal
hserverctl php config save --confirm \
  --checksum CURRENT_SHA256 \
  --content-file ./portal.conf \
  --reload \
  8.4 portal
hserverctl php action --confirm 8.4 test
hserverctl php action --confirm 8.4 reload
hserverctl php action --confirm 8.4 restart
hserverctl php status 8.4
hserverctl php status 8.4 portal
hserverctl php opcache get 8.4
hserverctl php opcache reset --confirm 8.4

# Managed node
hserverctl php versions --node edge-1
hserverctl php pools --node edge-1 --version 8.4
hserverctl php config get --node edge-1 8.4 portal
hserverctl php config edit --confirm --node edge-1 --reload 8.4 portal
hserverctl php action --confirm --node edge-1 8.4 test
hserverctl php action --confirm --node edge-1 8.4 reload
hserverctl php config save --confirm --node edge-1 \
  --checksum CURRENT_SHA256 \
  --content-file ./portal.conf \
  --reload \
  8.4 portal
```

The local panel also exposes the PHP diagnostics and toolchain observations
that already exist in the API:

```bash
hserverctl php extensions 8.4
hserverctl php ini get 8.4
hserverctl php ini get 8.4 portal
hserverctl php ini diff 8.4
hserverctl php ini directives 8.4
hserverctl php logs error 8.4
hserverctl php logs error --lines 250 8.4
hserverctl php logs slow 8.4 portal
hserverctl php logs slow --lines 100 8.4 portal
hserverctl php security profiles
hserverctl php security status 8.4 portal
hserverctl php composer version
hserverctl php composer outdated --project-dir /var/www/vhosts/example.com 8.4
```

These commands are read-only and panel-local: they deliberately do not accept
`--node`. Error and slow-log reads default to 100 and 50 entries and accept
`--lines` from 1 through 5000. `php composer outdated` requires the explicit
absolute `--project-dir`; the server checks that path against its configured
vhost root, and the CLI does not invent a project directory. Composer
`install`, `update`, and `require` mutations are not exposed by this command
family. Config, log, and provider-derived fields are rendered through fixed
JSON projections with terminal controls removed; unknown provider fields are
not forwarded to CLI output.

Every lifecycle command requires `--confirm`. Local and managed hosts expose
the same fixed `test`, `reload`, and `restart` actions. Local reload and restart
run the installed `php-fpmMAJOR.MINOR -t` validator first and do not call
systemd when the configuration is invalid. Managed actions require both
`php.read` and `php.action`; the agent accepts only an exact version from its
refreshed inventory and rejects masked or missing runtimes.

Local and managed pool replacement both require an exact observed version and
pool, a regular non-symlink NUL-free UTF-8 file no larger than 2 MiB, and the
current lowercase 64-character SHA-256 returned by `php config get`. Before
PUT, the client refreshes pool identity, canonical path, and checksum. The
local panel and managed agent independently repeat the checksum lock, create a
same-directory recovery backup, atomically replace the file, and test the
complete PHP-FPM configuration. Local validation or requested reload failure
restores the previous file before returning an error. Managed writes additionally
require `php.read` and `php.write`; the managed agent restores the previous file
when validation fails. `--reload` requests a graceful reload only after a
successful configuration test. Every successful save returns its backup path
and replacement checksum.

`php config edit` performs that same guarded save without requiring a manual
temporary-file and checksum copy. It refreshes the observed pool and canonical
path, writes the exact current content to a mode-`0600` temporary file, launches
the configured editor, and submits only changed content with the checksum it
just observed. An unchanged editor session returns `changed: false` without a
PUT. Select a single editor executable with `--editor`, `HSERVER_EDITOR`,
`VISUAL`, or `EDITOR`, in that priority order; when the editor needs flags, use
a small wrapper executable. The temporary file is removed after success or
failure, and the panel or agent still owns validation, atomic replacement,
backup, optional reload, and rollback.

In the TUI, press `P`; `Enter` on a version opens the available tested lifecycle
actions, while `Enter` on a pool opens its current configuration viewer.

## Manage Nginx configurations

The same command family addresses the panel host when `--node` is omitted and
an enrolled managed node when it is present:

```bash
hserverctl nginx status
hserverctl nginx configs
hserverctl nginx archives
hserverctl nginx backups
hserverctl nginx snippets
hserverctl nginx get site.conf
hserverctl nginx create --confirm --type static example.com
hserverctl nginx create --confirm \
  --type proxy \
  --proxy-pass http://127.0.0.1:3000 \
  --ssl \
  example.com
hserverctl nginx enable --confirm example.com.conf
hserverctl nginx disable --confirm example.com.conf
hserverctl nginx archive --confirm example.com.conf
hserverctl nginx restore --confirm \
  example.com.conf.hserver-archive-20260827T120000.000000000Z
hserverctl nginx rollback --confirm \
  example.com.conf.hserver-backup-20260827T130000.000000000Z
hserverctl nginx edit --confirm site.conf
hserverctl nginx test
hserverctl nginx reload --confirm
hserverctl nginx save --confirm \
  --content-file ./site.conf \
  --checksum CURRENT_SHA256 \
  site.conf

hserverctl nginx configs --node edge-1
hserverctl nginx get --node edge-1 site.conf
hserverctl nginx enable --confirm --node edge-1 site.conf
hserverctl nginx disable --confirm --node edge-1 site.conf
hserverctl nginx edit --confirm --node edge-1 site.conf
hserverctl nginx test --node edge-1
hserverctl nginx reload --confirm --node edge-1
hserverctl nginx save --confirm --node edge-1 \
  --content-file ./site.conf \
  --checksum CURRENT_SHA256 \
  site.conf
```

Local and managed `nginx get` return the current lowercase SHA-256 checksum.
`nginx save` requires that value through `--checksum` on both targets and
refreshes the selected identity before PUT. The receiving panel or agent repeats
the compare-and-swap lock, so a concurrent server-side change produces
`HTTP 409` instead of being overwritten. The input and destination must be
regular, non-symlink, NUL-free UTF-8 configuration files no larger than 2 MiB.

Local saves create a same-directory timestamped backup, atomically replace the
file while preserving mode and ownership, run the complete `nginx -t`, and
restore the previous file when validation fails. Managed saves provide the same
backup, checksum, validation, atomic-replacement, and rollback contract through
the capability-scoped agent. Reload remains a separate explicit operation on
both targets so an operator or script can inspect the validated receipt before
changing the running service.

`nginx create` is a local-host operation because the panel owns local template,
document-root, managed-snippet, and full-host validation boundaries. It accepts
only `php`, `static`, `proxy`, or `redirect`; type-specific flags cannot be mixed.
PHP and static sites derive their document root from the installation-owned
vhost root when `--doc-root` is omitted. Proxy sites require `--proxy-pass`, and
redirect sites require `--redirect-to`. Without `--ssl`, HServer generates a
real HTTP-only server rather than a port-443 listener without certificates.
With `--ssl`, it adds TLS listeners and an HTTP-to-HTTPS redirect; custom
`--cert-path` and `--key-path` must be safe absolute paths supplied together.
The panel creates the file exclusively, validates the complete Nginx config,
removes a rejected candidate, and returns the new checksum. It does not enable
the site or reload Nginx. `nginx snippets` exposes the installation-owned,
read-only managed snippet inventory used by generated templates.

`nginx enable` and `nginx disable` are explicit desired-state operations, not
flip-state aliases. Repeating either command is safe and leaves the site in the
requested state. On the local host, HServer only manages an exact symlink from
the installation-owned enabled-site directory to the selected regular
available-site file; foreign files and foreign symlink targets are refused and
left untouched. With `--node`, the CLI uses the managed domain action contract
for the exact configuration identity observed from `nginx configs`. State
changes never reload Nginx automatically.

`nginx archive` is local-host only and requires the site to be disabled. By
default it reads the selected config immediately before the mutation and sends
the returned checksum; automation may instead provide an already observed
value with `--checksum`. The panel creates an exclusive same-directory recovery
copy before removing the config from inventory, reruns `nginx -t`, and restores
the original on validation failure. Archival never deletes the document root,
application files, certificates, logs, or database, and never reloads Nginx.

`nginx archives` lists portable recovery identities with their original config
name, checksum, size, and timestamps. `nginx restore` is also local-host only.
It fetches the selected archive checksum immediately before restoring unless
automation supplies an already observed value with `--checksum`. Restore never
overwrites an existing config, retains the archive, validates the candidate with
the complete `nginx -t`, removes a rejected candidate, leaves a successful
restore disabled, and never reloads Nginx. Both archival and recovery therefore
remain explicit, checksum-bound steps.

`nginx backups` lists the pre-edit recovery copies created by successful config
saves. `nginx rollback` fetches both the selected backup checksum and the exact
current config checksum immediately before mutation unless automation supplies
them with `--backup-checksum` and `--current-checksum`. The server refuses either
stale value, retains a fresh pre-restore copy of the current config, atomically
installs the selected backup, runs the complete `nginx -t`, restores the previous
target if validation fails, preserves enabled state, and never reloads Nginx.

`nginx edit` performs the guarded fetch-edit-save cycle without a manual
temporary-file or checksum copy. It writes the exact observed content to a
mode-`0600` temporary file, launches the selected editor, and sends only changed
content with the checksum it just observed. An unchanged session returns
`changed: false` without a PUT. Select one editor executable with `--editor`,
`HSERVER_EDITOR`, `VISUAL`, or `EDITOR`, in that priority order; use a small
wrapper executable when the editor needs flags. The temporary file is removed
after success or failure. Reload and config replacement require `--confirm`.

## Manage domains

```bash
# Panel host
hserverctl domains list
hserverctl domains provisioning
hserverctl domains get example.com
hserverctl domains check example.com
hserverctl domains create --confirm --type php \
  --php-version 8.4 --fpm-preset medium \
  --web-root /srv/sites/example.com/public_html \
  example.com
hserverctl domains create --confirm --type static \
  --web-root /srv/sites/docs.example.com/public_html --spa \
  docs.example.com
hserverctl domains create --confirm --type proxy \
  --proxy-port 3100 \
  --pm2-app api --pm2-script server.js --pm2-cwd /srv/apps/api \
  --pm2-port 3100 --node-env production \
  api.example.com
hserverctl domains create --confirm --type php \
  --issue-ssl --ssl-email admin@example.com --create-dns-record \
  secure.example.com
hserverctl domains action --confirm example.com enable
hserverctl domains delete --confirm example.com
hserverctl domains delete --confirm --delete-files example.com

# Managed node
hserverctl domains list --node edge-1
hserverctl domains action --confirm --node edge-1 example.com.conf disable
```

The portable common surface is inventory plus explicit enable/disable state.
The local panel additionally exposes detail, preflight analysis, provisioning
readiness, typed PHP/static/proxy creation, and deletion. Creation always needs
`--confirm`; the CLI rejects irrelevant type-specific flags instead of sending
options the server would ignore. PHP creation supports the fixed `7.4` and
`8.0`–`8.5` versions, FPM presets, an optional isolated Linux owner, existing
certificates, explicit issuance email, and optional configured DNS
reconciliation. Static creation adds only the optional SPA fallback. Proxy
creation supports an explicit upstream plus an optional PM2 app/script pair;
relative scripts require an absolute `--pm2-cwd`. Server-side allowed-root and
provider checks remain authoritative.

Local `get`, `check`, `action`, `create`, and `delete` accept exact normalized
hostnames. Managed-node action targets remain the config identities returned by
`domains list`, not arbitrary filesystem paths. `--delete-files` is a second,
separate opt-in; without it, deletion removes the domain configuration without
requesting removal of its document root.

## Manage SSL certificates

```bash
# Panel host
hserverctl ssl status
hserverctl ssl list
hserverctl ssl get example.com
hserverctl ssl action --confirm example.com renew
hserverctl ssl issue --confirm \
  --domain example.com \
  --email admin@example.com \
  --challenge http-01

# Managed node
hserverctl ssl list --node edge-1
hserverctl ssl action --node edge-1 example.com check
hserverctl ssl action --confirm --node edge-1 example.com renew
```

Certificate inventory and checks are read-only. Renewal and issuance require
`--confirm`, accept a bounded `--wait` value, and use only the fixed ACME
`http-01` or `dns-01` challenge choices already exposed by the panel. Managed
nodes support inventory, check, and renewal; new issuance is local because the
panel host owns that provisioning contract.

## Manage backups

Local backups are asynchronous jobs. Managed nodes expose only the
installation-owned plans advertised by their agent:

```bash
# Panel host inventory and storage pressure
hserverctl backups list

# Start a local full backup and retain the latest 14 completed artifacts
hserverctl backups create --confirm \
  --type full \
  --name before-upgrade \
  --compression 6 \
  --retention 14

# Restrict a file-bearing backup to observed vhost identities
hserverctl backups create --confirm \
  --type files \
  --vhost example.com \
  --vhost api.example.com

# Observe the asynchronous local job
hserverctl backups jobs --active
hserverctl backups jobs --hours 48
hserverctl backups job JOB_ID

# Fully read and validate a local restore artifact without mutation
hserverctl backups validate BACKUP_ID

# After reviewing that receipt, explicitly start the asynchronous restore
hserverctl backups restore --confirm --validated BACKUP_ID

# Explicit local artifact deletion
hserverctl backups delete --confirm BACKUP_ID

# Inspect and manage the panel host's automatic backup schedule
hserverctl backups schedule list
hserverctl backups schedule set --confirm \
  --frequency daily \
  --time 03:00 \
  --retention-count 14
hserverctl backups schedule set --confirm \
  --cron '15 2 * * 1' \
  --type database \
  --database portal \
  --retention-count 10
hserverctl backups schedule delete --confirm \
  --raw-line '0 3 * * * /var/lib/hserver/backups/run-backup.sh type=full retention=14 # hserver-backup'

# Managed node: discover and run an installation-owned plan
hserverctl backups list --node edge-1
hserverctl backups run --confirm --node edge-1 nightly
```

Local creation accepts only `full`, `database`, or `files`; PostgreSQL or
MariaDB; compression levels `1`–`9`; retention `0`–`365`; and at most 16 unique
portable vhost identities. The API revalidates those vhosts against the
installation-owned root. Managed execution accepts one plan ID returned by
`backups list --node` and waits up to 22 minutes by default. Creation, plan
execution, restore, and deletion require `--confirm`; inventory, job
observation, and artifact validation are read-only JSON operations.

`backups schedule list` returns the local crontab-derived schedule inventory,
including each server-observed `rawLine`. Schedule replacement accepts either
one five-field `--cron` expression or the provider-neutral
`--frequency`/`--time` presets, plus an optional backup type/database and a
retention count from `1` to `365`. Replacement and deletion require
`--confirm`. Deletion accepts only the exact single-line HServer `rawLine`
returned by the inventory call; it never selects or removes an unrelated
crontab entry.

Restore can replace databases and files. Run `backups validate BACKUP_ID`
first, review the complete validation receipt including `databaseRecovery` and
`filesRollback`, then pass both `--confirm` and `--validated` to start the
asynchronous restore. Observe the returned job through `backups job JOB_ID`.
The flags cannot prove that a human reviewed the receipt, but they prevent a
restore from being mistaken for an inventory command; the server still repeats
its artifact and recovery checks before mutation.

### Manage encrypted remote snapshots

The snapshot commands use the currently selected Google Drive or S3-compatible
restic destination. Inventory and selector discovery are read-only:

```bash
hserverctl backups snapshot status
hserverctl backups snapshot list
hserverctl backups snapshot vhosts
```

Create a new client-side encrypted snapshot only after reviewing readiness:

```bash
hserverctl backups snapshot run --confirm
```

Remote snapshot restore extracts into HServer's fixed local staging directory;
it does not silently replace production paths. The CLI requires an explicit
scope even though the API represents a full restore with empty selectors:

```bash
# Extract every path represented by one observed snapshot
hserverctl backups snapshot restore \
  --confirm --all abcdef1234567890

# Extract selected installation-owned manifest paths
hserverctl backups snapshot restore \
  --confirm \
  --manifest nginx \
  --manifest letsencrypt \
  abcdef1234567890

# Extract only observed virtual-host directories
hserverctl backups snapshot restore \
  --confirm \
  --vhost example.com \
  --vhost api.example.com \
  abcdef1234567890
```

Selectable manifest identities are `vhosts`, `nginx`, `letsencrypt`,
`postgresql-cfg`, `mysql-cfg`, `php`, `hserver-data`, `cron-d`, and `systemd`.
`root-crontab` is staged during snapshot creation but is deliberately not a
direct restore selector. `--all` cannot be combined with selectors, and the
complete `vhosts` manifest cannot be combined with individual `--vhost`
values. The CLI validates those boundaries before sending a request; the
server repeats them against its installation-owned manifest.

Complete remote-repository deletion is supported only when the observed
destination advertises that capability. Google Drive currently supports it;
S3-compatible destinations do not. The destructive CLI command refreshes
status first and requires the exact currently observed repository folder:

```bash
hserverctl backups snapshot status
hserverctl backups snapshot purge \
  --confirm \
  --repository hserver-snapshots
```

A stale folder value, unsupported provider, missing confirmation, or active
snapshot job fails before repository deletion. The server resolves the remote
from its persisted settings and never accepts a provider URL or arbitrary
remote path from this command.

## Manage firewalls

Firewall inventory and bounded mutations use the panel's existing local UFW
adapter or the managed agent's installation-owned iptables chain. Every
successful command prints JSON:

```bash
# Panel host: observe UFW readiness and rules
hserverctl firewall status
hserverctl firewall list

# Add an inbound HTTPS rule from a bounded source
hserverctl firewall add --confirm \
  --action allow \
  --protocol tcp \
  --port 443 \
  --source 203.0.113.0/24 \
  --comment "HTTPS office access"

# Local rules use the positive number returned by firewall status/list
hserverctl firewall delete --confirm 3
hserverctl firewall toggle --confirm enable

# Managed node: inventory first, then mutate an installation-owned rule
hserverctl firewall list --node edge-1
hserverctl firewall add --confirm --node edge-1 \
  --action allow --protocol tcp --port 443 --source 203.0.113.0/24
hserverctl firewall delete --confirm --node edge-1 fw-0123456789ab
```

Local adds accept `allow`, `deny`, `reject`, or `limit`; `in` or `out`; TCP,
UDP, or any protocol; bounded UFW port/range/service syntax; and valid source
or destination IP/CIDR values. Managed rules deliberately expose the narrower
agent contract: inbound `allow`/`deny`, IPv4 source filters, TCP/UDP numeric
ports, or an all-protocol rule without a port. Before every managed mutation,
the CLI refreshes inventory and submits its exact revision. Concurrent changes
therefore fail with the server's `409 Conflict` instead of overwriting newer
rules. Managed deletion also requires that the `fw-…` identity is still present
and installation-owned.

Press `F` in `hserverctl ui` for the same inventory. `A` opens fixed SSH, HTTP,
HTTPS, and DNS inbound allow profiles; `Enter` deletes only a mutable observed
rule; and local `T` opens UFW activation confirmation. Read-only iptables
fallback rules remain visible but cannot be mutated. Managed activation is not
presented as a central toggle because it belongs to the agent installation.
Every mutation still requires a separate `Y`; `Enter` alone never executes it.

## Inspect security and manage Fail2Ban

Security score and Fail2Ban commands use the authenticated local panel API.
They do not infer managed-node security state from the panel host:

```bash
hserverctl security score
hserverctl security fail2ban status
hserverctl security fail2ban jail sshd

hserverctl security fail2ban ban --confirm sshd 192.0.2.20
hserverctl security fail2ban unban --confirm sshd 192.0.2.20

# Panel-local persistent access lists (admin only)
hserverctl security ip-blacklist list
hserverctl security ip-blacklist add --confirm \
  --ip 198.51.100.0/24 --comment "blocked network"
hserverctl security ip-blacklist delete --confirm 198.51.100.0/24

hserverctl security ip-whitelist list --format text
hserverctl security ip-whitelist add --confirm \
  --ip 203.0.113.10 --comment "office" --expires-in-minutes 120
hserverctl security ip-whitelist delete --confirm 203.0.113.10
```

`security score` and the status/jail commands are read-only JSON operations.
Ban and unban accept only a 1–64 character portable jail identity and a plain
IPv4 or IPv6 address, require `--confirm`, and reject a non-positive `--wait`.
Before mutation, the CLI refreshes the complete Fail2Ban status, requires
`available: true` and `state: "healthy"`, and verifies that the selected jail
is still observed. Unban additionally requires the exact address to remain in
that jail's current banned-IP inventory. A missing optional installation,
stopped daemon, or incomplete jail read therefore remains visible and cannot be
mistaken for an empty healthy state or used for mutation.

The blacklist and whitelist commands use the panel's persistent local access
lists and require an authenticated admin. Add and delete always require
`--confirm`; addresses are validated as explicit IP or CIDR values and escaped
before they enter an endpoint path. An optional expiration is expressed only
as a non-negative minute count. These commands have no `--node` mode because
the current managed-agent protocol does not expose this state. A server-side
`403 Permission denied` remains an error and never produces a success receipt.

Press `S` in `hserverctl ui` to view the local security score, individual
pass/warn/fail checks, Fail2Ban readiness, jail counters, and banned IPs.
`Enter` on an observed banned IP opens a separate unban confirmation and `Y`
is still required. New bans remain in the scripted command because the TUI does
not introduce an arbitrary address-input surface. `Ctrl+K` includes exact
loaded unban identities. On a managed node, the tab explicitly reports that
these are not current agent capabilities and does not query the panel host as a
substitute.

## Manage cron jobs

Cron inventory and scheduled-job mutations use the panel's existing local
user-crontab adapter or the managed agent's installation-owned
`/etc/cron.d/hserver-managed` boundary. Every successful command prints JSON:

```bash
# Panel host readiness, user jobs, and observed system cron files
hserverctl cron status
hserverctl cron list
hserverctl cron system

# Create an enabled local user job
hserverctl cron create --confirm \
  --schedule "0 3 * * *" \
  --user root \
  --command "/usr/local/bin/backup" \
  --description "Nightly backup"

# Replace the complete observed local job or disable it
hserverctl cron update --confirm \
  --schedule "15 4 * * *" \
  --user root \
  --command "/usr/local/bin/backup --fast" \
  --description "Nightly backup" \
  --disabled \
  0123456789abcdef
hserverctl cron delete --confirm 0123456789abcdef

# Managed node inventory, create, complete replacement, one-time run, and delete
hserverctl cron list --node edge-1
hserverctl cron create --confirm --node edge-1 \
  --schedule "30 2 * * 1" --user deploy \
  --command "/usr/local/bin/report" --description "Weekly report"
hserverctl cron update --confirm --node edge-1 \
  --schedule "45 2 * * 1" --user deploy \
  --command "/usr/local/bin/report" --description "Weekly report" \
  cron-0123456789ab
hserverctl cron run --confirm --node edge-1 cron-0123456789ab
hserverctl cron delete --confirm --node edge-1 cron-0123456789ab
```

Create and update accept only validated schedules, portable lowercase Unix user
names, a control-free command up to 4096 characters, and a control-free
description up to 160 characters. Local creation is enabled because the local
API creates a user-crontab entry atomically; use a subsequent `cron update
--disabled` when an initially disabled local job is required. Managed creation
can use `--disabled` directly. Update is intentionally a complete replacement,
not a partial patch, so the desired schedule, user, and command are explicit.
Local ownership cannot be moved during update.

Before every managed create, update, or delete, the CLI refreshes inventory and
submits the exact 64-character revision. Update, delete, and run also require
that the `cron-…` identity is still present. Concurrent changes therefore fail
with the server's `409 Conflict` rather than overwriting newer state. Manual
run is managed-node-only and still requires both `--confirm` and the agent's
`cron.run` capability; the CLI cannot turn it into a raw task payload.

Press `C` in `hserverctl ui` for the same inventory. `Enter` opens only the
actions supported by the active runtime: enable/disable, deletion, and
managed-agent run-now. Every action re-reads the exact identity before mutation
and requires a separate `Y`. Creation and full-field editing remain available
through the scriptable commands above so the full schedule and command stay
explicit instead of being hidden behind fixed TUI guesses.

## Manage databases

Local PostgreSQL and MariaDB management uses the existing provider-neutral
database API. Managed nodes deliberately expose the narrower agent contract:
inventory plus installation-allowlisted engine restart and engine-specific
health checks.

```bash
# Local source and database inventory
hserverctl databases list
hserverctl databases list --engine postgresql
hserverctl databases users --engine mariadb
hserverctl databases tables --engine postgres portal

# Execute one server-enforced read-only SELECT/WITH query from a regular file
printf '%s\n' 'SELECT id, status FROM jobs LIMIT 20;' >./query.sql
hserverctl databases query \
  --engine postgres \
  --query-file ./query.sql \
  portal

# Create or permanently drop a local database
hserverctl databases create --confirm \
  --engine postgres \
  --owner portal_user \
  portal
hserverctl databases drop --confirm --engine postgres portal

# Managed inventory and allowlisted restart plus readiness/socket health check
hserverctl databases list --node edge-1
hserverctl databases restart --confirm --node edge-1 postgresql
hserverctl databases restart --confirm --node edge-1 mariadb
```

Database and owner identities accept only 1–64 alphanumeric, underscore, or
hyphen characters. `query` reads a regular, non-symlink UTF-8 file of at most
64 KiB, rejects a file that does not begin with `SELECT` or `WITH`, and always
sends `write_mode: false`. The server independently validates the statement,
blocks write/file-access functions, and executes it inside its database-specific
read-only transaction. `hserverctl` does not expose arbitrary SQL write mode.

Local create and drop require `--confirm`. Before drop, the CLI reloads the
selected engine inventory and refuses to operate unless that exact database is
still present; the API additionally requires the literal `DROP <name>` receipt.
Managed restart first reloads engine inventory, accepts only `postgresql` or
`mariadb`, and requires the agent's `database.action` capability and local
restart allowlist. PostgreSQL restart is followed by `pg_isready`; MariaDB uses
its socket ping. Remote database creation, deletion, and queries are not
misrepresented as centrally available operations.

Press `D` in `hserverctl ui` for normalized local or managed inventory. On the
panel host, `Enter` on an observed database opens the destructive drop
confirmation; engine rows are inventory-only. On a managed node, database rows
remain read-only and `Enter` on an engine offers restart plus health check only
when `database.action` is advertised. Every mutation requires a separate `Y`.
Local creation, table inspection, user inventory, and read-only queries remain
available through the explicit script commands above.

## Browse and manage bounded files

The file command family uses the same installation-configured roots as the web
panel and the same `files.read` / `files.write` capability boundary as managed
agents. Paths must be clean absolute UTF-8 paths below a currently reported
root; `..`, control characters, `/`, and unobserved mutation targets are
rejected before a write request. An empty `HSERVER_VHOSTS_ROOT` does not select
another provider layout, so site-root-dependent local file operations remain
`not_configured` while independently configured roots may still be listed. The
following uses `/srv/hserver/sites` only as an explicit
configured-root example; replace it with the path reported by `files roots`.

```bash
# Explicit example after configuring HSERVER_VHOSTS_ROOT=/srv/hserver/sites.
# Discover roots and inspect JSON inventory/content on the panel host
hserverctl files roots
hserverctl files list /srv/hserver/sites
hserverctl files read /srv/hserver/sites/example.com/httpdocs/index.html

# Replace an existing local text file from a regular, non-symlink input
hserverctl files save --confirm \
  --content-file ./index.html \
  /srv/hserver/sites/example.com/httpdocs/index.html

# Create, move, or recursively delete an observed local entry
hserverctl files create --confirm --type directory \
  /srv/hserver/sites/example.com/httpdocs/archive
hserverctl files rename --confirm \
  /srv/hserver/sites/example.com/httpdocs/old.html \
  /srv/hserver/sites/example.com/httpdocs/archive/old.html
hserverctl files delete --confirm \
  /srv/hserver/sites/example.com/httpdocs/archive

# Discover and browse a managed node
hserverctl files roots --node edge-1
hserverctl files list --node edge-1 /srv/apps
hserverctl files read --node edge-1 /srv/apps/example/config.json
```

`files save` reads replacement content only from a regular, non-symlink,
NUL-free UTF-8 file of at most 2 MiB. Local save, create, rename, and delete
refresh the configured roots and parent-directory inventory first. Save accepts
only an observed regular file; create requires an absent destination; rename
requires an observed non-symlink source and absent destination; delete refuses
roots and symlinks and is recursively destructive. Every mutation requires
`--confirm`. Local file writes do not have a checksum-aware API transaction, so
they are bounded and re-observed but cannot claim atomic compare-and-swap.

Managed agents expose browse, read, and replacement of existing text files;
remote create, rename, and delete are intentionally not agent capabilities. A
remote replacement requires the checksum returned by the latest `files read`:

```bash
# Copy the checksum from the JSON response produced by the read command.
hserverctl files read --node edge-1 /srv/apps/example/config.json
hserverctl files save --confirm --node edge-1 \
  --checksum 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  --content-file ./config.json \
  /srv/apps/example/config.json
```

Before the PUT, the CLI reloads node state, checks `files.write`, checks the
current write roots, reads the exact canonical path, and compares the supplied
SHA-256. The agent repeats the checksum check while holding its write lock,
creates a same-directory timestamped backup with the original ownership and
mode, and returns that backup path as the receipt. A concurrent change fails
instead of being overwritten.

General `files save` refuses `/etc/nginx` and `/etc/php`. Use the dedicated
Nginx or PHP management surface so configuration-specific validation and
rollback are not bypassed. Press `E` in `hserverctl ui` for the file browser.
`Enter` opens a directory or a regular text file, `U` moves up, `,` and `.`
cycle configured roots, and `X` opens the confirmed local recursive-delete
flow. Managed-node browsing stays read-only in the TUI; checksum-protected
replacement remains explicit in the scriptable command.

## Read panel and managed-node state

```bash
hserverctl version
hserverctl health
hserverctl openapi >openapi.json
hserverctl nodes list
hserverctl nodes get edge-1
hserverctl nodes profile get edge-1
hserverctl nodes profile set --confirm --profile-file ./edge-1-profile.json edge-1
hserverctl nodes profile apply --confirm edge-1
# Optional bounded wait for the agent-reported terminal state.
hserverctl nodes profile apply --confirm --wait 2m edge-1
hserverctl nodes profile export edge-1 --format env-fragment
hserverctl tasks list --limit 10 edge-1
hserverctl tasks get edge-1 42
```

Successful API responses are printed as indented JSON. Network failures,
invalid JSON, oversized responses, authentication failures, and API errors
produce a non-zero exit status. API errors preserve their HTTP status, for
example `HTTP 409: managed node is offline`.

## Enroll a managed node from scripts

Create a managed-node enrollment with the selected context and administrator
authentication:

```bash
hserverctl nodes enroll \
  --confirm \
  --id edge-1 \
  --name "Production edge server" \
  --agent-token-output edge-1.agent-token \
  --agent-env-output edge-1.agent.env
```

The command validates the node ID against the server contract and requires a
non-blank display name of at most 255 bytes before making a request. Both
output paths are required, must be distinct clean local file targets, and
must have an existing, non-symlink, writable parent directory. Existing
targets, including symlinks and non-regular files, are refused. The CLI
reserves both new files with mode `0600` before the authenticated request, so
an invalid input or path makes zero HTTP requests.

Enrollment uses exactly `POST /api/nodes` with this JSON body; confirmation is
a CLI-only guard and is not sent to the API:

```json
{"id":"edge-1","name":"Production edge server"}
```

The API returns the enrollment token only once. On success, the token file
contains exactly the token followed by one newline, and the environment file
contains no token or secret. Its configuration points at the selected panel
origin, sets the requested node ID, uses
`HSERVER_AGENT_TOKEN_FILE=/etc/hserver-agent.token`, and selects the safe
30-second heartbeat interval. Optional agent capabilities remain explicitly
disabled; the generated file contains no provider-specific defaults,
operator domains, or IP addresses beyond the selected panel origin itself.
Standard output reports only the enrolled node identity and the two local
file paths; it never prints the token.

If the API request fails, both reserved files are removed. If the node has
already been created but either post-response file write or sync fails, the
command exits non-zero with a credential-persistence error and never prints
the token. A completed token file is retained when it is recoverable so it can
be secured and used for the already-created node.

The managed-node profile commands use the admin-only
`GET /api/nodes/{id}/profile`, `PUT /api/nodes/{id}/profile`, and
`POST /api/nodes/{id}/profile/apply` endpoints.
`profile set` reads a strict fixed JSON object before making any request. The
file must be a regular, non-symlink UTF-8 file of at most 16 KiB (inclusive) containing
exactly these seven fields:

```json
{
  "allowDeployRead": false,
  "allowDeployActions": false,
  "allowDeployDomainRead": false,
  "allowDeployDomainActions": false,
  "deployPlansFile": "",
  "deployAcmeWebroot": "",
  "deployWriteRoots": []
}
```

Every non-empty path must match ASCII `^[A-Za-z0-9._/+:-]+$`, be clean and
absolute, differ from `/`, and fit within 4096 bytes. Directory paths cannot
end in `/`; `deployPlansFile` is also subject to the explicit file-path
trailing-slash rule. `deployWriteRoots` is an array of at most 16 unique paths.
The env-fragment exporter joins that array with commas only for the
single `HSERVER_AGENT_DEPLOY_WRITE_ROOTS` line.

Unknown or missing fields, trailing JSON, invalid dependencies, and invalid
paths are rejected locally, so `profile set` does not send a request for an
invalid file. After local validation it fetches the current profile revision
and sends the same profile with `expectedRevision`; a concurrent update is
reported by the server as a stale-revision conflict. The mutation always
requires `--confirm`, and the successful PUT response is printed as JSON.

`profile export` fetches the desired profile and, by default (or with
`--format env-fragment`), prints exactly seven tokenless environment lines:

```text
HSERVER_AGENT_ALLOW_DEPLOY_READ=<bool>
HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=<bool>
HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=<bool>
HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=<bool>
HSERVER_AGENT_DEPLOY_PLANS_FILE=<path>
HSERVER_AGENT_DEPLOY_ACME_WEBROOT=<path>
HSERVER_AGENT_DEPLOY_WRITE_ROOTS=<comma-separated paths>
```

An absent or `not_configured` profile is an explicit export error; the CLI
does not emit placeholder values. `profile get` and the PUT response retain
the server's JSON response shape, including desired, observed, and apply
state.

`profile apply` is the phase-two self-application boundary. It always fetches a
fresh profile first and sends no mutation unless the response has a configured
desired profile with revision at least 1, an online node, the advertised
`agent.profile.apply` capability, and no apply already in `queued`, `running`,
`awaiting_heartbeat`, or other in-flight state. A capable idle node reports
`not_requested`; a phase-one `manual_required` response, a
missing or invalid required response field, an offline node, a stale/invalid
desired profile, or any unsupported/conflicting apply state fails before the
POST. The authenticated mutation is sent to
`POST /api/nodes/{id}/profile/apply` with exactly this body, using the fresh
desired revision:

```json
{"expectedRevision": 1, "confirmed": true}
```

The successful server receipt is printed as JSON without reducing it to a task
ID or inferring completion from task history. With `--wait`, the CLI polls the
same profile GET within the requested positive wait (at most 10 minutes) until
the server reports `applied`, `failed`, `drifted`, or the backward-compatible
`manual_required` terminal state. `queued`, `running`, and
`awaiting_heartbeat` remain pending; a completed task alone never changes the
reported apply state.

Inspect the local panel host through the dedicated system endpoints:

```bash
hserverctl system info
hserverctl system stats
```

`system info` returns observed platform, kernel, boot identity, runtime and
panel build metadata from `GET /api/system/info`. `system stats` returns the
current CPU, memory, swap, disk, load, uptime, hostname, OS, and network
snapshot from `GET /api/system/stats`. Both commands are authenticated and
read-only; they always describe the local panel host, not a selected managed
node.

To signal exactly the process identity returned by monitoring, include both
its PID and observed start time:

```bash
hserverctl system actions process --confirm \
  --pid 1234 --start-time 987654 --signal term
```

The signal is exactly `term` or `kill`. The command requires `--confirm`, a PID
greater than one, a non-zero start time, and a positive `--wait` duration. It
posts only `pid`, `startTime`, and `signal` to
`POST /api/system/actions/process`; the server rechecks the process identity so
a recycled PID cannot target a different process. Managed-node process signals
remain under the separate capability-scoped `processes signal --node` command.

## Run bounded host maintenance

The high-level maintenance commands work against the panel host or a managed
node without requiring operators to assemble raw task payloads:

```bash
# Read-only state
hserverctl host status
hserverctl host reboot-status

# Local panel host
hserverctl host action --confirm memory-optimize
hserverctl host action --confirm swap-reset
hserverctl host action --confirm temp-clean
hserverctl host action --confirm reboot
hserverctl host action --confirm reboot-cancel

# Capability-scoped managed node
hserverctl nodes action --confirm edge-1 memory-optimize
hserverctl nodes action --confirm edge-1 swap-reset
```

Supported actions are exactly `memory-optimize`, `swap-reset`, `temp-clean`,
`reboot`, and `reboot-cancel`. Every mutation requires the explicit
`--confirm` flag. Managed-node operations still require the corresponding
agent capability and server-local action allowlist. They fail before task
persistence when the node is offline or the capability is unavailable.

Maintenance operations can legitimately take longer than the global 30-second
request timeout. Action commands therefore use a separate seven-minute wait by
default; override it explicitly with `--wait DURATION`.

## Scan and clean disk space

Read the disk overview before investigating capacity or I/O pressure:

```bash
# Local panel host: fresh mounted-filesystem and I/O observation
hserverctl disk overview

# Managed node: bounded filesystem mounts from the agent heartbeat
hserverctl disk overview --node edge-1
```

The local response contains mounted partitions, I/O counters, and aggregate
capacity totals from the panel host's `lsblk`, `df`, and `/proc/diskstats`
observation. The managed response uses the separate
`/api/nodes/{id}/disk` endpoint and contains only the selected agent's bounded
disk-mount inventory; it does not probe the panel host, open a shell, queue a
task, or claim fresh remote counters. Both forms are read-only and return JSON.

Run deeper diagnostics only against the local panel host:

```bash
hserverctl disk analysis start --confirm
hserverctl disk analysis status
hserverctl disk dirsize /srv/apps
hserverctl disk io --format table
hserverctl disk largest --limit 20 /srv/apps
hserverctl disk list /srv/apps
hserverctl disk mounts --format table
hserverctl disk smart /dev/nvme0n1
hserverctl disk usage --depth 2 /srv/apps
```

These commands map to the panel-local analysis, directory-size, I/O, largest
file, directory listing, mount, SMART, and usage endpoints. Starting a deep
analysis requires `--confirm`; every other diagnostic above is read-only.
Required paths and SMART devices have no CLI default, and path/device values
are URL-escaped before the request. The diagnostics deliberately reject
`--node`: managed nodes expose only their bounded heartbeat disk overview and
fixed cleanup capabilities until the agent protocol defines equivalent probes.
Use `--format json` (the default) for automation or `--format table` for a
compact terminal view.

Always scan first and select only returned fixed target IDs:

```bash
# Local panel host
hserverctl disk scan
hserverctl disk clean --confirm --target journal --target package-cache

# Managed node
hserverctl disk scan --node edge-1
hserverctl disk clean --confirm --node edge-1 --target journal
```

The CLI refuses cleanup without `--confirm`, rejects empty or duplicate target
IDs, and caps local selection at 20 targets and managed-node selection at four.
The API and agent then validate each fixed target again. Disk cleanup never
accepts a filesystem path or arbitrary command from the CLI. Successful output
includes the server-measured cleanup receipt and reclaimed-byte fields.

## Queue a fixed managed-node task

```bash
hserverctl tasks create \
  --confirm \
  --kind service.status \
  --payload service=nginx \
  edge-1
```

Queueing a task for a managed node always requires the explicit `--confirm`
flag. The command sends `confirmed: true` only after that flag is present; a
missing flag names the target node and fails before any HTTP request is made.
`--payload KEY=VALUE` is repeatable up to the API contract maximum of six
fields. Empty or duplicate keys are rejected locally. The hub still validates
the exact task kind, payload, advertised agent capability, operator role, and
server-observed node connectivity before persisting a task. Consult the
[agent hub contract](agent-hub-contract.md) for supported task payloads.

## Configuration and completion

Global flags must appear before the command:

```text
--server URL
--token-file PATH
--context NAME
--timeout DURATION
```

Generate shell completion without downloading another package:

```bash
hserverctl completion bash
hserverctl completion zsh
hserverctl completion fish
```

Run `hserverctl help` for the complete command inventory. The CLI accepts only
HTTP or HTTPS origin URLs without embedded credentials, query strings,
fragments, or path prefixes. TLS verification uses the host operating system's
trust store and cannot be disabled by a CLI flag.
