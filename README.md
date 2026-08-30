# HServer Panel

HServer is a self-hosted server-management panel built as a Go service with an
embedded React interface. It combines monitoring with direct operations such as
terminal access, process and service control, deployments, backups, firewall,
DNS, mail, database, runtime, disk, RAM, and swap management.

Local deployment targets support both installation-owned scripts and a
first-class Docker Compose mode with read-only readiness checks, fixed Compose
arguments, first-deploy repository provisioning, recorded runs, and Git-based
rollback.

Managed nodes can expose the same project-domain workflow through explicit
`deploy.domain.*` agent capabilities: HServer-owned Nginx reverse proxies,
loopback health probes, verified X.509 state, HTTP-01 issuance, and automatic
renewal. The upstream port remains in the node's local deploy plan and is never
accepted from browser input.

Managed agents can also opt into release discovery and lifecycle actions. Each
server owns its release-manifest URL and verifies the selected archive checksum
locally before extracting only the packaged agent binary and lifecycle
installer. When trusted Ed25519 public keys are configured, the agent also
requires and verifies the manifest's adjacent detached signature before it
accepts artifact metadata. The hub can request only an exact stable version or rollback; it
cannot provide a download URL, checksum, path, command, or systemd argument.

The project is currently **pre-1.0**. Its canonical public repository is
[`IamYGT/heyserver`](https://github.com/IamYGT/heyserver). Immutable public
source commit `adaccb23adf9720141d721970590de3a82fd17b5` produced the first
fully green public main CI matrix run `#33283277809`; the current fully green
public main CI run recorded on 2026-08-30 is `#33283728373`. Branch protection
was enabled after these runs and is currently active. The initial active signer
is prepared in private commit `df0a5070`, and the
`HSERVER_RELEASE_SIGNING_KEY` Actions secret is configured; public signer PR #7,
the first signed release, version tag, GitHub Release, tagged
lifecycle/provenance acceptance, clean independent-VM acceptance, and live
rollout remain pending. The live `v0.9.3` rollout is not the current source.

## Architecture

- **Native panel:** full management of the local Linux host.
- **Hub + agent:** one panel manages additional servers through enrolled,
  capability-scoped agents.
- **Single release artifact:** the production binary embeds the web interface.
- **SQLite state:** local application state with in-place migrations.
- **Optional providers:** Cloudflare, Stalwart, Google Drive, PM2, PostgreSQL,
  PHP-FPM, nginx, Docker, and other host tools are detected or configured; they
  are not prerequisites for the core panel. See the
  [optional integrations matrix](docs/optional-integrations.md) for each
  provider's configuration and health boundary.

See [AGENTS.md](AGENTS.md) for the product boundaries contributors must preserve.

## Quick evaluation with Docker

Docker is useful for evaluating the UI/API and contributing. It intentionally
does **not** grant the container unrestricted control of the host. Use a native
install or enroll an HServer agent for real systemd, nginx, firewall, disk, and
terminal operations.

```bash
git clone https://github.com/IamYGT/heyserver.git
cd heyserver
./scripts/init-env.sh
docker compose up --build
```

Open `http://localhost:3085`. The generated `.env` file has mode `0600`; the
initial credentials are stored there and are never printed by the bootstrap
script.

**First login:** After `init-env.sh` creates the local `.env` and Compose is
running, read `HSERVER_ADMIN_EMAIL` and `HSERVER_ADMIN_PASS` from that file
locally. The default email is `admin@localhost`; the password is generated and
is never printed by `init-env.sh`. Open `http://localhost:3085`, log in, then
complete onboarding to reach the dashboard. Keep `.env` and its password out of
issues, logs, and chat.

Compose container and volume identities are project-scoped rather than
global, so a disposable evaluation does not reuse another checkout's state.
Use `docker compose -p NAME ...` when two checkouts share the same directory
basename. Remove the evaluation and its owned volume with:

```bash
docker compose down --volumes
```

Public CI builds this exact Dockerfile, waits for the health endpoint, logs in,
persists onboarding state, restarts the container, and proves the state remains
available. This is an evaluation/contributor gate, not evidence that a
container can manage its host's systemd or firewall.

## Native installation

Full server management currently targets Ubuntu 24.04 or newer and Debian 12 or
newer, and requires root-owned systemd service access. Follow the
[installation guide](docs/installation-guide.md). A native installation uses
the version-matched panel and `hserverctl` binaries, a generated protected
environment file, and the `hserver-install.sh` lifecycle tool. Upgrades snapshot
the installed binary set and SQLite databases before replacement, health-check
the new service, and roll back automatically on failure. A failed first install
removes the new binaries, generated clean-host configuration and data, and
managed Nginx files while restoring any prior HServer unit, managed snippets,
and service state. With an optional provider-neutral release
manifest configured, admins can stage and verify an archive from the About page
or with `hserverctl updates stage --confirm`, then approve installation in a
separate confirmation. An optional local
Ed25519 trust set upgrades this flow from checksum-only metadata to a signed
manifest; HServer never performs a
silent self-update. Source builds remain available for contributors.

The signed release path needs only the core host tools documented in the
installation guide. Nginx, Certbot, Fail2Ban, PM2, PHP-FPM, UFW, database
clients, Docker, BIND, mail, Cloudflare, and backup providers are optional
feature dependencies; install them only when their corresponding feature is
needed. The guide keeps this provider setup separate from the core install.

Tagged native acceptance builds a second stable package independently on both
Ubuntu `amd64` and `arm64` runners, switches a local signed feed to it, and
performs the complete packaged-CLI stage/install path through a detached systemd
restart. The same acceptance job also runs the native OS and prerequisite gate
inside a disposable Debian 12 container. Publication remains blocked unless the
panel reconnects, the stage is terminally completed, the installed panel and CLI
report the exact staged version, and SQLite state is preserved.

The Git-free path starts from an installer and signer fingerprint obtained
independently of the mutable release asset directory. Replace every placeholder
with values published for the exact source commit and release you intend to
install; do not substitute an unversioned `latest` URL:

```bash
umask 077
release_version=vX.Y.Z
release_base=https://github.com/OWNER/REPOSITORY/releases/download/$release_version
installer_commit=IMMUTABLE_PUBLIC_COMMIT_SHA
installer_url=https://raw.githubusercontent.com/OWNER/REPOSITORY/$installer_commit/scripts/public-install.sh
trusted_installer_sha256=LOWERCASE_SHA256_FROM_AN_INDEPENDENT_SOURCE
trusted_release_key_sha256=LOWERCASE_SHA256_OF_RAW_ED25519_PUBLIC_KEY
install_dir=$(mktemp -d)
cd "$install_dir"
curl --proto '=https' --proto-redir '=https' -fSLo public-install.sh \
  "$installer_url"
printf '%s  public-install.sh\n' "$trusted_installer_sha256" | sha256sum --check -
chmod 0755 public-install.sh
./public-install.sh --release-base "$release_base" \
  --trusted-release-key-sha256 "$trusted_release_key_sha256" \
  --vhosts-root /srv/hserver/sites
```

`public-install.sh` is a manifest-external trust-bootstrap asset. After its
independently obtained digest passes, it downloads `bootstrap-install.sh`, its
detached Ed25519 signature, and the public-key sidecars into a private temporary
directory. The decoded 32-byte public key must match the explicit or embedded
trusted fingerprint, and that key must verify `bootstrap-install.sh.sig` before
the wrapper changes mode or invokes `sudo`. Adjacent SHA-256 files remain useful
for transfer-corruption detection, but files downloaded from the same release
directory do not establish signer identity.
Tagged releases still publish `public-install.sh.sha256` beside the staged
wrapper as a convenience checksum; do not use the pair as its own trust source.

Contributors who already have a trusted source checkout may invoke the same
canonical wrapper directly:

```bash
./scripts/public-install.sh https://github.com/OWNER/REPOSITORY \
  --trusted-release-key-sha256 LOWERCASE_SHA256_OF_RAW_ED25519_PUBLIC_KEY \
  --vhosts-root /srv/hserver/sites
```

An official staged wrapper embeds the canonical `active` and `next` signer
fingerprints from `trust/release-signers.json`. The checked-in generic source
wrapper intentionally embeds none, so a fork or trusted checkout must provide
`--trusted-release-key-sha256` or
`HSERVER_PUBLIC_INSTALL_TRUSTED_RELEASE_KEY_SHA256`.

For an expanded manual audit, download the published bootstrap, detached
signature, and verification key assets. The trusted fingerprint comparison and
bootstrap signature verification must both pass before invoking root:

```bash
umask 077
bootstrap_dir=$(mktemp -d)
cd "$bootstrap_dir"
release_url=https://github.com/OWNER/REPOSITORY/releases/download/vX.Y.Z
curl -fSLO "$release_url/bootstrap-install.sh"
curl -fSLO "$release_url/bootstrap-install.sh.sha256"
curl -fSLO "$release_url/bootstrap-install.sh.sig"
curl -fSLO "$release_url/release-public-key.b64"
curl -fSLO "$release_url/release-public-key.b64.sha256"
sha256sum --check bootstrap-install.sh.sha256
sha256sum --check release-public-key.b64.sha256
actual_release_key_sha256=$(base64 --decode release-public-key.b64 | sha256sum | awk '{print $1}')
test "$actual_release_key_sha256" = "$trusted_release_key_sha256"
TRUSTED_SOURCE_CHECKOUT/scripts/verify-release-asset-signature.sh \
  ./bootstrap-install.sh ./bootstrap-install.sh.sig ./release-public-key.b64
sudo ./bootstrap-install.sh \
  --manifest-url "$release_url/release-manifest.json" \
  --public-key-file ./release-public-key.b64 \
  --vhosts-root /srv/hserver/sites
```

The independently obtained fingerprint establishes the expected signer;
`bootstrap-install.sh.sig` authenticates the privileged bootstrap, and that
bootstrap uses the same key for the stable manifest. Before invoking any
packaged lifecycle tool it verifies the detached Ed25519 signature, target
architecture, declared archive size, SHA-256, bounded package root, entry types,
packaged version, and all three ELF architectures. The verified manifest URL
and trust set are persisted into the new protected installation configuration,
so the About page uses the same signed feed for later explicit updates. Initial
credentials remain only in `/etc/hserver/hserver.env` and are never printed.

Official release archives contain `hserver-panel`, `hserver-agent`, and the
authenticated `hserverctl` automation and interactive management client for
Linux `amd64` and `arm64`,
separate panel and agent lifecycle installers, systemd units, documentation,
and an archive checksum. Install a published archive through the signed
bootstrap flow above: it authenticates the release manifest before downloading
the architecture-specific archive, then verifies its declared size and SHA-256
before extraction or privileged lifecycle commands. An adjacent archive
checksum by itself is only a recovery aid for a copy obtained through an
independently trusted channel; it is not the public release installation path.

### Agent-only bridge for an existing managed node

An existing managed HServer agent can be upgraded without installing or
starting the panel. Run the same public wrapper on the node that already owns
the agent, replacing the repository with the release maintainer's repository:

```bash
./scripts/public-install.sh https://github.com/OWNER/REPOSITORY \
  --trusted-release-key-sha256 LOWERCASE_SHA256_OF_RAW_ED25519_PUBLIC_KEY \
  --agent-only
```

`--agent-only` reuses the signed manifest, trusted Ed25519 public key, detected
architecture, archive size and SHA-256, safe tar extraction, packaged
`VERSION`, and agent ELF checks. It requires the existing managed agent and
configuration, rejects `--vhosts-root`, and invokes only the verified
`agent-install.sh upgrade --binary <verified hserver-agent>` from the package.
The panel doctor, panel installer, and `hserverctl` are never run. The
existing configuration, custom token destination and token file, service
enabled/active state, and agent lifecycle rollback boundary remain owned by
the installed agent lifecycle. No hub token, signing key, or credential is
accepted, read, or printed by this bridge.

The bridge does not enable `HSERVER_AGENT_ALLOW_UPDATE_READ`,
`HSERVER_AGENT_ALLOW_UPDATE_ACTIONS`, or any other update setting and does not
rewrite the existing configuration. If local agent update policy is wanted,
the operator must add it separately to the protected agent configuration and
run the lifecycle installer under that local policy.

`--vhosts-root` is optional on a fresh install. Supply a provider-neutral local
absolute path when domain document roots, file management, site backups, and
snapshots should be available immediately. If it is omitted, the installer
keeps `HSERVER_VHOSTS_ROOT` empty and those root-dependent capabilities report
`not_configured` rather than guessing a host layout.

The packaged installer places the panel at `/usr/local/bin/hserver-panel` and
the authenticated client at `/usr/local/bin/hserverctl`. It also retains the
version-matched lifecycle installer and doctor under `/usr/local/libexec`, plus
the fixed Nginx lifecycle assets under
`/usr/local/share/hserver/nginx-snippets`. A bootstrap installation therefore
does not depend on the downloaded archive remaining on disk. Upgrade and
rollback snapshot these recovery tools and assets together with the panel and
CLI, and non-purge uninstall removes only the HServer-owned lifecycle files.

After authenticating once, `hserverctl ui` opens the full-screen control center
for local and enrolled servers. It provides live overview cards, server
switching, service and process controls, bounded RAM/swap/reboot maintenance,
measured disk cleanup, container lifecycle controls, PM2 application
management, PHP-FPM version/pool inventory with configuration-tested lifecycle
actions and checksum-locked local/managed pool replacement with backup and
validation rollback, combined Nginx/domain/SSL Web Ops, local BIND readiness,
zone/record inspection and creation, selected record/SOA editing, configuration
checks and confirmed reload/deletion,
local UFW and capability-scoped
managed firewall inventory/actions, local security-score and Fail2Ban
readiness/jail/banned-IP controls, local and capability-scoped managed cron
inventory with enable/disable, deletion, and managed-agent run-now actions,
local database inventory/create/drop/read-only queries and capability-scoped
managed database inventory/restart health checks,
configured-root local and managed file browsing with bounded text viewing,
observed local create/rename/recursive-delete actions, and checksum-protected
managed text replacement with backup receipts,
local-artifact and managed-plan backup
management with fixed full-application/database/files creation profiles,
optional full-backup retention, live local-job progress and logs, a dedicated
encrypted-snapshot dashboard with provider health, remote inventory, confirmed
creation, destination switching, and full/manifest/vhost staging restore,
selected-server audit history with local search, contextual `Ctrl+K` quick
actions, a selected-server deployment dashboard with local preflight and
revision inspection, confirmed deploy and rollback, managed-node advertised
deployment actions, recent job state, and bounded output viewing, a
selected-server Updates dashboard that distinguishes release
discovery and signature states, stages and installs an exact observed panel
release, and schedules capability-scoped managed-agent upgrade or rollback only
after a fresh lifecycle preflight, a central Alerts dashboard with explicit
integration states, notification-channel testing and enable/disable/deletion,
alert-rule enable/disable/deletion, recent event inspection, and fresh-observation
guards, a central Cloudflare dashboard with explicit provider states, zone,
DNS-record, and email-routing inspection, confirmed whole-zone cache purge,
proxy toggle, record deletion, and installation-owned mail DNS reconciliation,
and scrollable host or application log
viewers. Backup restore
requires full artifact validation, then separate `R` and `Y` acknowledgements;
every other mutation also has a separate `Y` confirmation. The same client
exposes JSON-producing `containers`, `logs`, `pm2`, `php`, `nginx`, `domains`, `ssl`,
`firewall`, `security`, `cron`, `databases`, `files`, `backups`, `audit`, `notify`,
`dns`, `cloudflare`, `deploy`, and `updates`
command families for automation without creating a second control plane. The
notification family includes protected delivery-channel credentials, canonical
type-specific alert-rule CRUD, and bounded alert-history pagination. The
Cloudflare family keeps provider credentials on the panel while exposing zone,
record, email-routing, confirmed DNS mutation, cache purge, and
installation-owned mail DNS reconciliation operations. The
scriptable backup family also selects Google Drive or S3-compatible
client-side encrypted restic destinations, lists snapshots and observed
vhosts, starts snapshots, and performs explicitly scoped staging restores.
The release commands discover and stage panel artifacts through the configured
signed feed, install only the latest server-observed verified stage, and drive
managed-agent upgrade or rollback only after a fresh status preflight and an
explicit `--confirm`:

```bash
hserverctl updates status
hserverctl updates stage --confirm
hserverctl updates install --confirm
hserverctl updates agent status --node edge-1
hserverctl updates agent upgrade --confirm --node edge-1
hserverctl updates agent rollback --confirm --node edge-1
```

After a successful first install, provider-neutral guidance shows a local SSH
tunnel, loopback browser URL, administrator email, protected credential-file
location, and persistent diagnostic/lifecycle commands without printing the
generated password. Reprint it at any time:

```bash
sudo /usr/local/libexec/hserver-install next-steps
```

Create, authenticate, verify, and select the first CLI context in one command.
On an interactive terminal, `hserverctl` asks for the password and any required
TOTP code with terminal echo disabled. The bearer token is stored separately
with mode `0600`, and no context is persisted unless both login and
`/api/auth/me` verification succeed:

```bash
sudo -u YOUR_USER hserverctl connect \
  --server http://127.0.0.1:3085 \
  --email admin@example.com \
  local

hserverctl doctor
hserverctl ui
```

Unattended automation can instead provide installer-owned mode-`0600`
`--password-file` and `--totp-file` inputs. Secret values are never accepted as
command-line arguments.

The packaged host doctor reports compatibility, missing commands,
protected-file permissions, systemd state, and health-endpoint availability
without printing configuration values. The authenticated CLI doctor can also
write its complete panel, account-role, fleet, or selected-node report directly
to a new mode-`0600` file with `hserverctl doctor --output PATH`; it refuses an
existing destination and preserves the available report even when a check
fails. For managed nodes, unattended provisioning can require both the exact
agent-reported `amd64` or `arm64` architecture and named capabilities. A
non-zero exit status means at least one required check failed, which makes both
doctor paths usable in unattended provisioning and support flows.

After one install with a current release package, the agent lifecycle installer
is retained at `/usr/local/libexec/hserver-agent-install`. An operator may then
enable capability-scoped update status and actions on that managed server; no
remote update occurs unless both the server-local policy and an authenticated
admin confirmation allow it.

## Development

Required toolchains match CI:

- Go 1.26.1 or newer (the exact minimum from the root `go.mod` `go` directive)
- Node.js 24
- npm, Git, Make, Python 3, and a C compiler for CGO

CI additionally provisions isolated PostgreSQL and MariaDB instances and gates
release publication on real dump, restore, recovery-point, and failed-restore
rollback drills for both engines.

```bash
git clone https://github.com/IamYGT/heyserver.git
cd heyserver
make dev-check
make dev-setup
make test
make build
```

`make dev-check` is read-only and distinguishes required build tools from
optional Docker, database-drill, SQLite CLI, and golangci-lint capabilities.
`make dev-setup` stops on a missing required tool, otherwise installs the locked
Go modules and `web/package-lock.json` dependencies without installing system
packages or creating `.env`.

Useful references:

- [Installation](docs/installation-guide.md)
- [Docker Compose deployments](docs/docker-compose-deployments.md)
- [API reference](docs/api-reference.md)
- [Command-line client](docs/cli.md)
- [Complete API route inventory](docs/api-routes.md)
- [Generated OpenAPI 3.1 contract](docs/openapi.json), including promoted local
  bootstrap health, onboarding, authentication and TOTP recovery, safe editable
  and portable settings, the complete local domain lifecycle, system
  observation and verified update lifecycle, backup/restore, bounded
  host-maintenance, measured disk-cleanup, and managed-node
  enrollment/connectivity/task schemas; also served by each installation at
  `/openapi.json` and browsable in
  **Developer API** at `/developer/api`
- [Frontend architecture](docs/frontend-architecture.md)
- [Monitoring architecture](docs/monitoring-architecture.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Portable configuration schema](docs/portable-configuration.md)
- [Community roadmap](docs/feature-roadmap.md)
- [Project sustainability](docs/project-sustainability.md)
- [Optional integrations](docs/optional-integrations.md)
- [Community extension boundary](docs/extension-boundary.md)
- [Release manifest contract](docs/release-manifest.md)
- [Public launch checklist](docs/public-launch-checklist.md)
- [Governance](GOVERNANCE.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)

## Contributing and license

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md), the
[governance model](GOVERNANCE.md), the [Code of Conduct](CODE_OF_CONDUCT.md),
and the repository-wide [product contract](AGENTS.md) before opening a pull
request.

HServer is licensed under the [Apache License 2.0](LICENSE).
