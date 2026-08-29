# HServer Panel — Installation Guide

Target OS: **Ubuntu 24.04 LTS or newer, or Debian 12 or newer**
Installed binary: `/usr/local/bin/hserver-panel`
Service: `/etc/systemd/system/hserver.service`
Runtime port: `3085` (optionally proxied via Nginx)

---

## 1. Prerequisites

The core native panel consists of the signed or locally built binaries, a
root-owned systemd service, embedded frontend assets, and SQLite state. Nginx,
Certbot, Fail2Ban, PM2, PHP-FPM, database servers, Docker, BIND, Cloudflare,
Stalwart, and other host providers are feature-specific; none is required to
start the core panel. Choose the installation source in section 2 and install
only the matching core prerequisites below. Provider setup is documented
separately after the installation flow.

### 1.1 Core packages for a release installation

A signed release archive is the smallest native installation path. On a stock
supported Ubuntu or Debian host, install the packages used by the bootstrap and
lifecycle doctor:

```bash
apt-get update && apt-get install -y \
    ca-certificates curl openssl tar coreutils sqlite3 systemd python3
```

Ubuntu and Debian's base systems supply `apt-get`, `systemctl`, `install`,
`sed`, `sha256sum`, `stat`, `tar`, `uname`, `mktemp`, and `find`. The packaged
doctor accepts only Ubuntu 24.04+ and Debian 12+ and checks the native lifecycle
commands (`systemctl`, `apt-get`, `openssl`, `curl`, `tar`, `install`, `sqlite3`,
and `sed`) before it writes installation state. No provider daemon is needed for
this preflight. The package-manager check is detection-only; the installer never
silently changes host packages.

### 1.2 Core packages for a source build

A source checkout adds the build toolchain to the release prerequisites:

```bash
apt-get update && apt-get install -y \
    git wget build-essential pkg-config libsqlite3-dev
```

The `sqlite3` command-line client is used by the bundled backup and restore
helpers; keeping it installed also makes the native lifecycle preflight complete.

```bash
apt-get install -y sqlite3
```

### 1.3 Go 1.26+

Source builds currently require Go 1.26 or newer (`CGO_ENABLED=1` for SQLite).

```bash
# Check if already installed
go version

# Install the current supported Go 1.26 patch release (adjust patch as needed)
wget https://go.dev/dl/go1.26.6.linux-amd64.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf go1.26.6.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile.d/go.env
source /etc/profile.d/go.env
go version
```

### 1.4 Node.js 24+

Required only for building the React frontend. The compiled binary
embeds the frontend so Node.js is NOT needed on production after build.

```bash
# Install Node.js 24 from an approved package repository for this host.
# Do not pipe a downloaded setup script into a shell.
apt install -y nodejs
node --version   # should be 24.x or higher
npm --version
```

### 1.5 Verify CGO availability

```bash
# gcc must be available for go-sqlite3 to compile
gcc --version
```

### 1.6 Optional physical-disk SMART health

The Disk page works without SMART tooling. On a physical host, install
`smartmontools` to observe the physical disk resolved behind the root
filesystem:

```bash
apt install -y smartmontools
smartctl --version
```

Virtual disks, storage controllers, RAID, and roots spanning multiple physical
disks may not expose one readable SMART device. HServer reports that state as
unavailable and does not assume `/dev/sda` or choose one disk arbitrarily.

The general Disk Overview does not require SMART. It recursively inventories
the mounted `lsblk` tree, including whole-disk mounts, partitions, NVMe,
encrypted volumes, and nested LVM devices. Real `lsblk` paths such as
`/dev/mapper/...` are preserved; an installation does not need an `sda` device.

### 1.7 Optional provider installation

Install providers only for the corresponding feature. A local loopback panel
does not need any of these packages. The [optional integrations matrix](optional-integrations.md)
defines the separate `not configured`,
`unavailable`, and `healthy` states; the provider sections below contain the
installation and recovery steps:

- [Nginx reverse proxy](#7-nginx-reverse-proxy-optional) for a public hostname,
  reverse proxy, or Nginx-backed domain management.
- [SSL and Certbot](#8-ssl-certificate-optional) for certificate inventory,
  issuance, or renewal.
- [Fail2Ban](#fail2ban-security-integration-optional) for jail inventory and
  IP ban/unban actions.
- PM2, PHP-FPM, BIND, UFW, database clients, mail, Cloudflare, Docker, and
  backup providers only when their feature is needed.

---

## 2. Choose an Installation Source

### 2.1 Versioned release package

Release packages are published for Linux `amd64` and `arm64`. Each archive
contains the panel binary, managed-node agent binary, authenticated
`hserverctl` automation client, native lifecycle installer, installation
doctor, systemd units, license, and installation documentation.

Each tagged release also publishes the provider-neutral `public-install.sh`
wrapper and its adjacent `public-install.sh.sha256` checksum as top-level
release assets. They are trust-bootstrap assets, intentionally outside the
schema-v1 manifest `artifacts` object; the wrapper retains responsibility for
pinning signer identity and verifying `bootstrap-install.sh.sig` before it
invokes the signed bootstrap. The adjacent checksum detects corruption; when it
comes from the same mutable release directory it does not authenticate the
wrapper or signer.

The Git-free path starts with installer bytes and a signer fingerprint obtained
independently of the release asset directory. Replace every placeholder with
the exact immutable source and release values; do not use `latest`:

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

The independently obtained installer digest is verified before execution. The
wrapper downloads `bootstrap-install.sh`, `bootstrap-install.sh.sig`, the
convenience checksum, and public-key sidecars into a private directory. It
compares SHA-256 of the decoded raw 32-byte key with the trusted fingerprint and
uses that key to verify the bootstrap's detached Ed25519 signature before
`chmod`, `sudo`, or execution. A self-consistent replacement key and checksum
from the release directory cannot expand the trust set.

For contributors who already have a trusted source checkout, the same wrapper
can be run directly:

```bash
./scripts/public-install.sh https://github.com/OWNER/REPOSITORY \
  --trusted-release-key-sha256 LOWERCASE_SHA256_OF_RAW_ED25519_PUBLIC_KEY \
  --vhosts-root /srv/hserver/sites
```

Official staged wrappers embed the canonical `active` and `next` fingerprints
from `trust/release-signers.json`. Generic source/fork mode has no embedded
identity and requires `--trusted-release-key-sha256` or
`HSERVER_PUBLIC_INSTALL_TRUSTED_RELEASE_KEY_SHA256`. The expanded manual audit
equivalent is:

```bash
umask 077
bootstrap_dir=$(mktemp -d)
cd "$bootstrap_dir"
release_url=https://github.com/OWNER/REPOSITORY/releases/download/vX.Y.Z
curl --proto '=https' --proto-redir '=https' -fSLO "$release_url/bootstrap-install.sh"
curl --proto '=https' --proto-redir '=https' -fSLO "$release_url/bootstrap-install.sh.sha256"
curl --proto '=https' --proto-redir '=https' -fSLO "$release_url/bootstrap-install.sh.sig"
curl --proto '=https' --proto-redir '=https' -fSLO "$release_url/release-public-key.b64"
curl --proto '=https' --proto-redir '=https' -fSLO "$release_url/release-public-key.b64.sha256"
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

The public key is not a credential. Official tagged releases must derive it
from a private key whose raw public-key fingerprint is an `active` entry in the
checked-in canonical trust store. Checksums happen before `sudo` but provide
only corruption detection; the independently trusted fingerprint establishes
signer identity and the detached signature authenticates the bootstrap. The
bootstrap requires root only for the native lifecycle. It first
downloads the exact manifest bytes and adjacent `.sig`, accepts one to eight
unique Ed25519 keys for zero-gap rotation,
selects the detected `linux_amd64` or `linux_arm64` artifact, and validates the
declared size, SHA-256, bounded tar paths and types, package version, and panel,
agent, and CLI ELF architectures. Only then does it run packaged preflight,
install, and installed-mode doctor checks. A signature, checksum, archive, host,
installation, or health failure returns non-zero and does not continue to the
next lifecycle stage.

`--vhosts-root` is optional and is accepted only for a fresh installation. Give
it a provider-neutral absolute host path when HServer should enable local
document-root, file-management, site-backup, and snapshot capabilities from the
first start. The installer creates a missing directory with mode `0755`, writes
the host path unchanged to `HSERVER_VHOSTS_ROOT`, and never removes that tree on
normal uninstall. Omit the option to leave the setting empty and keep those
root-dependent capabilities reporting `not_configured`.

For new installations, the bootstrap also passes the verified manifest URL and
normalized trust set into the packaged installer. They are written to the
protected `/etc/hserver/hserver.env`, enabling the same signed feed for later
explicit **About → Release updates** operations. The bootstrap never prints the
generated administrator password or silently installs a future update.

The expanded flow above is also the supported manual release installation
flow. Do not replace it with a direct archive download, checksum, extraction,
and privileged install: a sibling checksum can validate bytes without
authenticating which release manifest selected those bytes. The signed
bootstrap downloads the exact schema-v1 manifest and its
adjacent `release-manifest.json.sig`, verifies that detached Ed25519 signature
against the supplied trusted public key, then verifies the selected archive's
size, SHA-256, bounded extraction contract, package version, and ELF
architectures before it extracts or invokes any packaged lifecycle command.

### Agent-only signed bridge for an existing managed agent

A host that already runs a managed HServer agent can receive a signed agent
upgrade without installing the panel. The bridge accepts the same release
manifest and public-key assets as a fresh installation:

```bash
./scripts/public-install.sh https://github.com/OWNER/REPOSITORY \
  --trusted-release-key-sha256 LOWERCASE_SHA256_OF_RAW_ED25519_PUBLIC_KEY \
  --agent-only
```

The equivalent verified bootstrap invocation is:

```bash
sudo ./bootstrap-install.sh \
  --manifest-url "$release_url/release-manifest.json" \
  --public-key-file ./release-public-key.b64 \
  --agent-only
```

Before the lifecycle command runs, the bridge verifies the detached Ed25519
manifest signature, detected `linux_amd64` or `linux_arm64` artifact, declared
archive size, SHA-256, bounded tar paths and entry types, packaged `VERSION`,
and the agent ELF architecture. The archive must contain both
`hserver-agent` and `agent-install.sh`. It then requires an already-installed
managed agent and invokes only the verified packaged
`agent-install.sh upgrade --binary <verified hserver-agent>`. Panel doctor,
panel install, CLI install, and panel/CLI binaries are not used in this mode.
A missing legacy installation, signature, trust key, checksum, architecture,
or archive validation failure stops before agent lifecycle mutation.

Agent-only mode preserves the existing agent configuration, configured custom
token destination and token file, service enabled/active state, and the
installed lifecycle installer's snapshot and automatic rollback boundary. It
rejects `--vhosts-root`. It never accepts, reads, or prints a hub token,
private signing key, or credential. It also does not enable
`HSERVER_AGENT_ALLOW_UPDATE_READ` or `HSERVER_AGENT_ALLOW_UPDATE_ACTIONS`, and
does not rewrite the existing configuration; add any local update policy
separately in the protected agent environment before invoking the bridge.

### Checksum-only discovery and recovery boundary

Checksum-only discovery may expose version and archive metadata read-only as
`signature_status=not_configured`. It is not an installation trust mechanism and
cannot authorize panel stage/install or managed-agent upgrade. Checksum-only
verification is retained for recovering an archive that was already established
through an independent trusted release record (for example, an existing local
recovery copy); it must not be used for a newly downloaded public release. If
the signed manifest, detached signature, or trusted public key is unavailable or
cannot be verified, stop and restore the trust assets; do not proceed with the
privileged lifecycle installer.

The installer places the version-matched panel and CLI at
`/usr/local/bin/hserver-panel` and `/usr/local/bin/hserverctl`. It persists the
matching lifecycle installer at `/usr/local/libexec/hserver-install` and doctor
at `/usr/local/libexec/hserver-doctor`, together with the complete fixed Nginx
lifecycle set at `/usr/local/share/hserver/nginx-snippets`; upgrades and
rollback snapshot them with the binary set. This retained set lets the installed
lifecycle tool perform a later binary-pair upgrade after the extracted archive
has been removed. The package-local CLI can verify health before installation,
and the installed CLI can verify it afterward. After an account exists,
authenticate without placing its password or bearer token in process
arguments. Interactive terminals prompt with echo disabled; automation can add
`--password-file` and, when required, `--totp-file` pointing to mode-`0600`
regular files:

```bash
hserverctl health
hserverctl --server https://hserver.example.com login \
  --email admin@example.com
hserverctl --server https://hserver.example.com nodes list
```

See the [CLI guide](cli.md) for token-file permissions, TOTP, task creation,
environment variables, and shell completion.

The signed manifest's archive checksum must pass before installation. The
installer starts the service,
waits for the health endpoint, and leaves generated credentials only in the
protected environment file. On success it prints a provider-neutral SSH tunnel
and loopback URL for safe first access, the administrator email, and the
credential-file location without printing the generated password. Reprint the
guidance or run installed diagnostics later without retaining the archive:

```bash
sudo /usr/local/libexec/hserver-install next-steps
sudo /usr/local/libexec/hserver-doctor installed
```

The doctor prints pass, warning, and failure lines
without printing configuration values; it exits non-zero when a required host
or installation check fails. `install.sh install` also runs the host preflight
itself and stops before writing installation files when a required check fails.
Provisioning systems may seed the same verified update feed during first install
with `HSERVER_INSTALL_UPDATE_MANIFEST_URL` and
`HSERVER_INSTALL_UPDATE_MANIFEST_PUBLIC_KEYS`. These inputs affect only a newly
generated environment file; an existing configuration is never overwritten.
Before the first mutation, the installer records the existing HServer systemd
unit, enabled/active state, and every `hserver-*.conf` Nginx snippet. If binary
installation, snippet installation, service activation, or the health check
fails, it removes the attempted panel and CLI and restores that state. On a
clean host it also removes the configuration and data directories generated by
the failed attempt. Pre-existing configuration, data, and unrelated Nginx
files are preserved.
The installed-mode doctor also checks the protected BIND lifecycle journal
directory and file permissions without reading or printing journal content. A
valid pending journal is a recovery warning; a symlink, non-regular path, mode
other than `0700` on its directory, or file permission broader than `0600`
fails diagnostics.

For a public HTTPS endpoint, continue at the optional [Nginx reverse
proxy](#7-nginx-reverse-proxy-optional) and [SSL certificate](#8-ssl-certificate-optional)
sections. The core service can remain on its loopback listener without either
provider.

Release discovery is optional and provider-neutral. Set the stable URL of a
schema-v1 HServer release manifest to enable the **About → Release updates**
card. A GitHub release repository can use its redirecting latest-release URL:

```bash
HSERVER_UPDATE_MANIFEST_URL=https://github.com/OWNER/REPOSITORY/releases/latest/download/release-manifest.json
HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS=BASE64_ED25519_PUBLIC_KEY
# Optional when the installed binary pair is not in /usr/local/bin:
HSERVER_UPDATE_PANEL_BINARY_PATH=/opt/hserver-panel/bin/hserver-panel
HSERVER_UPDATE_CLI_BINARY_PATH=/opt/hserver-panel/bin/hserverctl
```

The panel reads at most 512 KiB, accepts only HTTP(S) artifact and release-note
URLs without embedded credentials, and requires a lowercase 64-character
SHA-256 for every artifact. Discovery is read-only and never downloads,
installs, or restarts HServer automatically. With an empty public-key trust set,
the panel may report checksum-only discovery as `signature_status=not_configured`
and expose version/checksum metadata for inspection. That status is never an
update authorization: panel **Stage & verify**, **Install verified release**,
and managed-agent upgrade require `signature_status=verified`; an empty trust
set or any other status fails closed before mutation. The About page reports
`Ed25519 signature verified`, `checksum only`, or signature failure explicitly.
A native-install admin may explicitly choose **Stage & verify** only after the
verified status is present; this downloads the archive server-side and validates
its size, SHA-256, bounded tar paths, packaged version, and the panel plus CLI
ELF architectures without restarting the service. Installation requires a
separate restart notice, checkbox, browser confirmation, and exact server-side
stage confirmation. No automatic or unattended updater is provided or
promised.
See the [release manifest contract](release-manifest.md) for the complete state
and verification contract.

### 2.2 Source checkout

```bash
mkdir -p "$HOME/src/hserver-panel"
cd "$HOME/src/hserver-panel"

# Clone the repository (authentication may be required until public launch)
git clone https://github.com/OWNER/REPOSITORY.git .

# Verify structure
ls -la
# Expected: bin/ cmd/ data/ deploy/ docs/ internal/ web/ Makefile go.mod
```

### 2.3 Docker quick evaluation

Docker can evaluate the UI, API, login, and persisted application state without
installing HServer into the host's systemd. It is not the full-management
installation mode and does not grant the container control of the host's nginx,
firewall, runtimes, storage, or terminal.

```bash
./scripts/init-env.sh
docker compose up --build
# open http://localhost:3085
docker compose down --volumes
```

**First login:** After `init-env.sh` creates the local `.env` and Compose is
running, read `HSERVER_ADMIN_EMAIL` and `HSERVER_ADMIN_PASS` from that file
locally. The default email is `admin@localhost`; the password is generated and
is never printed by `init-env.sh`. Open `http://localhost:3085`, log in, then
complete onboarding to reach the dashboard. Keep `.env` and its password out of
issues, logs, and chat.

The Compose container and data volume are project-scoped. Use
`docker compose -p NAME ...` for an explicit identity when multiple checkouts
have the same directory basename. Public CI runs `make test-docker`, which
builds this exact path, verifies health and authenticated onboarding, restarts
the container, proves SQLite state persistence, and removes the disposable
project and volume.

---

## 3. Configure Environment for a Source Build

The application reads configuration from environment variables. The native
service loads them from `/etc/hserver/hserver.env`; secrets do not belong in the
systemd unit or the repository.

```bash
sudo install -d -m 0700 /etc/hserver /var/lib/hserver
sudo ./scripts/init-env.sh /etc/hserver/hserver.env
```

The bootstrap writes a strong JWT secret and first-login password with mode
`0600` and does not print either value. Review the protected file locally to set
your admin email and optional provider values before the first start. Running
the script again refuses to overwrite an existing file.

Optional provider settings in the table below are intentionally empty unless
shown as a core default. An empty value keeps that integration explicitly
`not_configured`; HServer does not infer a provider endpoint, filesystem layout,
service name, or privileged identity from the host.

**All environment variables:**

| Variable | Default | Required | Description |
|---|---|---|---|
| `HSERVER_PORT` | `3085` | No | TCP port the binary listens on |
| `HSERVER_DB_PATH` | `/var/lib/hserver/hserver.db` | No | SQLite database file path |
| `HSERVER_JWT_SECRET` | — | **YES** | HS256 signing secret; minimum 32 bytes |
| `HSERVER_DATA_DIR` | `/var/lib/hserver` | No | Directory for runtime data files |
| `HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT` | `3` | No | Number of panel pre-upgrade recovery snapshots retained; integer `1`–`100`; the active rollback marker is always preserved |
| `HSERVER_LOG_LEVEL` | `info` | No | `debug`, `info`, `warn`, `error` |
| `HSERVER_ADMIN_EMAIL` | `admin@localhost` | No | First-run admin account email |
| `HSERVER_ADMIN_PASS` | generated | First boot | First administrator password; generated by the lifecycle installer and required while the user database is empty |
| `HSERVER_CRON_SECRET` | generated | No | Protected secret for localhost-only scheduled backup triggers |
| `HSERVER_VHOSTS_ROOT` | — | No | Optional installation-owned absolute base path for generated document roots and root-dependent domain, Composer, file-backup, and snapshot operations; empty keeps those capabilities `not_configured` (set an explicit path such as `/srv/hserver/sites` before use) |
| `HSERVER_PHP_CONFIG_ROOT` | `/etc/php` | No | Installation-local absolute PHP-FPM configuration root used for readiness and management; an explicitly configured relative value fails closed rather than falling back to a host path |
| `HSERVER_PHP_BINARY_ROOT` | `/usr/sbin` | No | Installation-local absolute directory containing `php-fpm<VERSION>` binaries used for readiness and management; an explicitly configured relative value fails closed rather than falling back to a host path |
| `HSERVER_NGINX_SITES_AVAILABLE` | `/etc/nginx/sites-available` | No | Native Nginx available-site directory used by domain and config inventory, template creation, and transactional project-domain mappings |
| `HSERVER_NGINX_SITES_ENABLED` | `/etc/nginx/sites-enabled` | No | Native Nginx enabled-site directory used by domain/config status, toggles, and transactional project-domain mappings |
| `HSERVER_NGINX_SNIPPETS_DIR` | `/etc/nginx/snippets` | No | Absolute include root for release-owned `hserver-*.conf` Nginx snippets used by generated vhosts |
| `HSERVER_UPDATE_MANIFEST_URL` | — | No | Stable HTTP(S) schema-v1 release manifest URL; empty keeps release discovery explicitly not configured |
| `HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS` | — | No | Up to eight comma-separated base64 raw Ed25519 public keys; non-empty requires and verifies the adjacent manifest `.sig` before discovery can become healthy; empty permits checksum-only read-only discovery but never authorizes panel stage/install or agent upgrade |
| `HSERVER_UPDATE_PANEL_BINARY_PATH` | observed executable, then `/usr/local/bin/hserver-panel` | No | Fixed installed panel update destination; must be an absolute canonical regular executable named `hserver-panel`, with no symlink component or unsafe executable root |
| `HSERVER_UPDATE_CLI_BINARY_PATH` | sibling of an observed custom panel, then `/usr/local/bin/hserverctl` | No | Fixed installed CLI update destination; must be an absolute canonical regular executable named `hserverctl`, with no symlink component or unsafe executable root |
| `HSERVER_PGM_BACKUP_DIR` | `${HSERVER_DATA_DIR}/pgm-backups` | No | Absolute installation-owned directory containing PostgreSQL Manager snapshot folders exposed by the database backup browser; in-place upgrades preserve an existing configured legacy directory |
| `HSERVER_PG_RUN_AS` | `postgres` | No | Local unprivileged OS account used to execute PostgreSQL client tools through `sudo` |
| `HSERVER_PG_BACKUP_USER` | `postgres` | No | PostgreSQL role used by dump and restore clients |
| `HSERVER_PG_BACKUP_HOST` | local socket | No | Optional PostgreSQL socket directory or host shared by dump and restore |
| `HSERVER_PG_BACKUP_PORT` | client default | No | Optional PostgreSQL port shared by dump and restore |
| `HSERVER_PG_PASSFILE` | — | No | Optional protected pgpass path passed as `PGPASSFILE`; password values are never placed in command arguments |
| `HSERVER_MYSQL_DEFAULTS_FILE` | — | No | Optional protected MariaDB/MySQL client option file shared by dump and restore; connection credentials stay out of command arguments |
| `STALWART_URL` | — | No | Optional Stalwart management API base URL; empty keeps mail `not_configured`, and a URL alone does not prove `healthy` |
| `STALWART_API_KEY` | — | No | Stalwart Bearer token (preferred) |
| `STALWART_ADMIN_USER` | — | No | Optional Stalwart Basic-auth username fallback; set explicitly with `STALWART_ADMIN_PASS` when Bearer auth is not used |
| `STALWART_ADMIN_PASS` | — | No | Stalwart Basic auth password (fallback) |
| `HSERVER_STALWART_SERVICE` | — | No | Optional local Stalwart systemd unit for service status and controls; empty leaves local service discovery `not_configured` |
| `HSERVER_STALWART_CONFIG_PATH` | — | No | Optional absolute local Stalwart configuration path for listener and storage discovery; set explicitly for the installed layout |
| `HSERVER_STALWART_BIN` | — | No | Optional absolute local Stalwart binary path or command used for version discovery; set explicitly for the installed layout |
| `HSERVER_CF_API_TOKEN` | — | No | Cloudflare API token (Bearer, v4 API) |
| `HSERVER_CF_API_EMAIL` | — | No | Account email when using a Cloudflare Global API Key |
| `HSERVER_DOMAIN_DNS_ORIGIN` | — | No | Explicit IPv4 or IPv6 origin used only when domain creation requests a Cloudflare address record |
| `HSERVER_DOMAIN_DNS_PROXIED` | `false` | No | `true` creates or reconciles the domain address record through the Cloudflare proxy; `false` uses DNS-only mode |
| `HSERVER_MAIL_DNS_HOSTNAME` | — | No | MX target; required only for Cloudflare mail DNS auto-fix |
| `HSERVER_MAIL_DNS_PUBLIC_IP` | — | No | Optional A/AAAA address and generated SPF source |
| `HSERVER_MAIL_DNS_MX_PRIORITY` | `10` | No | MX priority used by mail DNS auto-fix |
| `HSERVER_MAIL_DNS_SPF` | generated | No | Optional explicit SPF TXT value |
| `HSERVER_MAIL_DNS_DMARC` | `v=DMARC1; p=none` | No | Optional explicit DMARC TXT value |
| `HSERVER_PM2_USER` | — | No | Unprivileged Linux account that owns PM2; required to enable PM2 management and PM2 log discovery |
| `HSERVER_PM2_HOME` | — | No | Optional absolute `PM2_HOME`, such as `/home/deploy/.pm2`; its `logs` directory is the only user-owned PM2 log root exposed by the panel |
| `HSERVER_PM2_BIN` | `pm2` | No | PM2 command name or absolute binary path (useful for NVM installs) |
| `HSERVER_PM2_ALLOWED_ROOTS` | — | No | Explicit comma-separated absolute roots accepted for PM2 deploy script and working-directory paths; empty keeps PM2 deployment `not_configured`, and `/` or relative entries fail closed |
| `HSERVER_CERTBOT_BIN` | `certbot` | No | Certbot command name or absolute normalized path used for status, issuance, and renewal |
| `HSERVER_CERTBOT_CONFIG_DIR` | `/etc/letsencrypt` | No | Absolute Certbot state root shared by certificate inventory and Project Domains |
| `HSERVER_ACME_WEBROOT` | `/var/www/hserver-acme` | No | Absolute HTTP-01 challenge webroot served by HServer-owned project-domain Nginx configurations |
| `HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS` | — | No | Absolute path to a mode-`0600` Certbot Cloudflare INI file; required only for DNS-01 issuance and never returned by the API |
| `HSERVER_GDRIVE_CLIENT_ID` | — | No | Optional Google OAuth client ID for the rclone-backed Drive destination |
| `HSERVER_GDRIVE_CLIENT_SECRET` | — | No | Optional Google OAuth client secret; keep only in the protected environment file |
| `HSERVER_GDRIVE_REDIRECT_URI` | derived | No | Explicit OAuth callback override when the public panel origin cannot be derived |
| `HSERVER_RCLONE_BIN` | `rclone` | No | rclone command name or absolute binary path used by Google Drive operations |
| `HSERVER_RESTIC_BIN` | `restic` | No | restic command name or absolute binary path used by encrypted snapshots |
| `HSERVER_RESTIC_PASSWORD` | — | Snapshot only | Installation-owned restic repository password; keep a durable copy outside the HServer host |
| `HSERVER_S3_ENDPOINT` | — | No | Optional absolute S3-compatible HTTPS endpoint; loopback HTTP is accepted for local MinIO only |
| `HSERVER_S3_BUCKET` | — | No | Portable 3–63 character lowercase bucket name used by encrypted snapshots |
| `HSERVER_S3_REGION` | — | No | Optional provider region passed to the S3 client |
| `HSERVER_S3_ACCESS_KEY_FILE` | — | No | Absolute regular mode-`0600` file containing one S3 access-key line; symlinks are rejected |
| `HSERVER_S3_SECRET_KEY_FILE` | — | No | Absolute regular mode-`0600` file containing one S3 secret-key line; symlinks are rejected |
| `HSERVER_S3_BUCKET_LOOKUP` | `auto` | No | S3 bucket addressing mode: `auto`, `dns`, or `path` |

### Protected notification channel configuration

Email passwords, Telegram bot tokens, and Discord or Slack webhook URLs are
write-only values in the panel API. HServer stores the complete channel config
under `${HSERVER_DATA_DIR}/notification-channel-secrets`: the directory is mode
`0700`, each `channel-<id>.json` file is mode `0600`, and SQLite stores only a
deterministic `file:channel-<id>.json` reference. List, detail, create, and update
responses return non-secret fields plus `secret_configured`; they never return
the credential. Leaving a secret input empty preserves the stored value; the
separate **Remove the stored credential** control clears it explicitly.

On the first startup after this storage boundary is introduced, legacy inline
channel JSON is moved in place to the protected directory before notification
APIs become available. If the directory, reference, file type, permissions, or
JSON is invalid, notification inventory and delivery remain unavailable while
unrelated panel features continue to run. Repair the protected store and restart
HServer; do not replace the failure with an empty channel inventory.

The packaged `backup-db.sh` workflow includes the SQLite database and every
referenced protected notification config in one checksummed, mode-`0600`
panel-state bundle. This bundle can move between self-hosted installations.
The default `hserver-data` entry in an encrypted HServer snapshot remains the
broader full-data recovery path. A lifecycle upgrade keeps a pre-upgrade SQLite
copy so its automatic rollback can restore the previous binary's compatible
database representation.

Domain creation uses the configured Nginx, vhost, Certbot, and ACME paths as one
installation boundary. New domains without an existing certificate start with
HTTP only. When certificate issuance is requested, HServer serves the HTTP-01
challenge from `HSERVER_ACME_WEBROOT` and enables HTTPS only after Certbot
succeeds. When domain DNS creation and certificate issuance are requested
together, the fixed order is local HTTP/runtime activation, Cloudflare address
record reconciliation, Certbot issuance, and HTTPS promotion. A DNS failure
skips Certbot; a DNS or issuance failure leaves the HTTP domain active and
returns `207 Multi-Status` with a visible partial-result warning instead of
leaving Nginx pointed at missing certificate files.

The native release archive ships the complete `nginx-snippets/` set. Install
and upgrade copy only the `hserver-*.conf` files into
`HSERVER_NGINX_SNIPPETS_DIR`; unrelated operator snippets are not replaced.
Upgrade snapshots include the managed snippet set, so automatic and manual
rollback restore the exact previous HServer-owned files together with the
binary and database snapshot.

The domain wizard's runtime controls are applied during provisioning rather
than stored as presentation-only metadata. PHP sites generate their pool from
the selected `low`, `medium`, or `high` resource preset (`medium` by default),
and invalid preset names are rejected before any host file is written. New
non-isolated PHP pools default their worker and socket ownership to Ubuntu's
`www-data` identity; they do not require a hosting-panel-specific Unix group.
Explicit identities on existing or manually configured pools remain readable
and editable. Static
sites with SPA Mode enabled fall back to `index.html` for client-side routes;
ordinary static sites keep the strict `404` fallback. Node.js domains pass the
selected production or development environment to the new PM2 process as the
bounded `NODE_ENV` value; arbitrary environment maps are not accepted.

> **Security:** `HSERVER_JWT_SECRET` must be set. The process will refuse
> to start if the value is empty, equals `change-me-in-production`, or is
> shorter than 32 characters.

Domain DNS provisioning remains disabled until both `HSERVER_CF_API_TOKEN` and
`HSERVER_DOMAIN_DNS_ORIGIN` are set. The panel probes Cloudflare and reports the
integration as **not configured**, **unavailable**, or **healthy**; it never
falls back to a maintainer or detected public IP. Use a scoped token with Zone
Read and DNS Edit permissions for the zones HServer may manage.

---

## 4. Build

The build pipeline is: `npm ci + npm run build` (frontend) →
copy dist to Go embed directory → `go build` (binary with embedded assets).

```bash
cd "$HOME/src/hserver-panel"

# Full build (frontend + panel and CLI binaries)
make build

# This runs three steps automatically:
#   1. cd web && npm ci && npm run build
#   2. cp web/dist/* cmd/hserver/web/dist/
#   3. build bin/hserver-panel and bin/hserverctl with matching provenance

# Verify the embedded provenance without starting the service
./bin/hserver-panel --version
./bin/hserverctl version
```

The exact binary size and build duration depend on the toolchain, architecture,
dependency cache, and source revision.

---

## 5. Install

### From a source checkout

```bash
cd "$HOME/src/hserver-panel"
make install
make doctor

# This runs:
#   create /var/lib/hserver and /etc/hserver with restrictive permissions
#   create /etc/hserver/hserver.env only when it does not exist
#   install bin/hserver-panel to /usr/local/bin/hserver-panel
#   install bin/hserverctl to /usr/local/bin/hserverctl
#   install the provider-neutral systemd unit
#   enable and start hserver
#   wait for /api/health to succeed
```

The same lifecycle tool can install a prebuilt version-matched binary pair
directly:

```bash
sudo ./scripts/hserver-install.sh install \
  --binary ./hserver-panel \
  --cli-binary ./hserverctl \
  --vhosts-root /srv/hserver/sites
```

The installer never prints the generated first-login password or JWT secret.
They remain in `/etc/hserver/hserver.env`, which is created with mode `0600`.

Verify:

```bash
which hserver-panel
# /usr/local/bin/hserver-panel
which hserverctl
# /usr/local/bin/hserverctl

ls /etc/systemd/system/hserver.service
```

---

## 6. Systemd Service

The installed unit loads protected configuration from a separate file:

```ini
[Unit]
Description=HServer Panel - Server Management GUI
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/hserver-panel
Restart=always
RestartSec=5
EnvironmentFile=/etc/hserver/hserver.env
WorkingDirectory=/var/lib/hserver
UMask=0077

[Install]
WantedBy=multi-user.target
```

The installer enables and starts the service. Manual service controls remain
available when needed:

```bash
systemctl daemon-reload
systemctl enable hserver
systemctl start hserver
systemctl status hserver
```

Check logs:

```bash
journalctl -u hserver -f
# Expected: "database ready" and "listening on :3085"
```

---

## 7. Nginx Reverse Proxy (Optional)

Nginx is not required for the core panel. Install it only when exposing
HServer through a public hostname, terminating TLS, or managing Nginx-backed
domains:

```bash
apt update
apt install -y nginx
nginx -v
systemctl enable --now nginx
```

HServer Panel listens on `127.0.0.1:3085`; the configuration below adds an
Nginx reverse proxy with SSL termination.

```bash
cat > /etc/nginx/sites-available/hserver.yourdomain.com << 'EOF'
server {
    listen 80;
    listen [::]:80;
    server_name hserver.yourdomain.com;

    # Let certbot handle ACME challenges
    include snippets/acme-challenge.conf;

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name hserver.yourdomain.com;

    ssl_certificate     /etc/letsencrypt/live/hserver.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/hserver.yourdomain.com/privkey.pem;

    include snippets/ssl-params.conf;
    include snippets/security-headers.conf;

    # WebSocket support (required for the terminal feature)
    location /api/terminal/ws {
        proxy_pass         http://127.0.0.1:3085;
        proxy_http_version 1.1;
        proxy_set_header   Upgrade $http_upgrade;
        proxy_set_header   Connection "upgrade";
        proxy_set_header   Host $host;
        proxy_set_header   X-Real-IP $remote_addr;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    # All other requests
    location / {
        proxy_pass         http://127.0.0.1:3085;
        proxy_http_version 1.1;
        proxy_set_header   Host $host;
        proxy_set_header   X-Real-IP $remote_addr;
        proxy_set_header   X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        client_max_body_size 32m;
    }
}
EOF

ln -s /etc/nginx/sites-available/hserver.yourdomain.com \
      /etc/nginx/sites-enabled/

nginx -t && systemctl reload nginx
```

> **Critical:** Always use `listen 443 ssl;` without an IP address.
> Never write `listen 49.12.x.x:443 ssl;` — IP-specific listen directives
> create a separate address:port group in nginx and break all other vhosts
> that share the same port.

The Nginx management page separately observes the executable and systemd unit:

| Observed state | Panel behavior |
| --- | --- |
| Executable missing | Reports **not configured**; test, reload, and config mutations stay disabled |
| Unit inactive or failed | Reports **stopped**; config inspection and `nginx -t` remain available, reload stays disabled |
| systemd state unavailable | Reports **unavailable**; mutations stay disabled until service detection succeeds |
| Unit active | Config test, guarded edits, enable/disable, and test-before-reload are available |

The recovery action performs detection only. HServer does not install nginx,
start its unit, rewrite configuration, or reload the service without an
explicit operator action.

---

## 8. SSL Certificate (Optional)

Certbot is not required for the core panel. Install it only for certificate
inventory, issuance, or renewal. HTTP-01 through the optional Nginx provider
is the default self-hosted path:

```bash
apt update
apt install -y certbot python3-certbot-nginx
certbot --version
certbot plugins
```

The SSL page independently observes certificate files and Certbot readiness.
Existing files remain visible when Certbot is missing, but issue and renew
controls stay disabled. New issuance is enabled only after a supported
authenticator is observed. Detection never installs a package, registers an
ACME account, changes DNS, or issues a certificate.

Compose **Project Domains** use Certbot's webroot authenticator instead of
allowing Certbot to edit Nginx. Keep `HSERVER_CERTBOT_CONFIG_DIR` and
`HSERVER_ACME_WEBROOT` on absolute installation-owned paths. HServer creates
the challenge root when an admin requests TLS, but public DNS must already
resolve to this host and inbound port 80 must reach the generated virtual host.
The built-in maintenance pass checks managed project certificates at startup
and every 12 hours, renewing only certificates observed as expiring or expired.

For optional Cloudflare DNS-01 and wildcard certificates, install the plugin
and create a protected Certbot credential file outside the repository:

```bash
apt install -y python3-certbot-dns-cloudflare
install -d -m 0700 /etc/hserver/secrets
install -m 0600 /dev/null /etc/hserver/secrets/certbot-cloudflare.ini
```

Populate that file locally using Certbot's `dns_cloudflare_api_token` format;
do not print the token or place it in Git. Then set only its path in the
protected HServer environment:

```dotenv
HSERVER_CERTBOT_BIN=certbot
HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS=/etc/hserver/secrets/certbot-cloudflare.ini
```

Restart HServer explicitly after changing its protected environment and use
**Retry detection**. The status API exposes only configured/readable booleans,
not the path or credential value.

To issue the panel's own certificate manually:

```bash
certbot certonly --nginx \
    -d hserver.yourdomain.com \
    --email admin@yourdomain.com \
    --agree-tos \
    --non-interactive

# Verify certificate
certbot certificates | grep hserver.yourdomain.com

# Reload nginx with new certificate
nginx -t && systemctl reload nginx
```

Certbot auto-renewal is configured by the certbot package installer.
Verify the timer is active:

```bash
systemctl status certbot.timer
```

HServer renews one named certificate with non-interactive `certbot renew
--cert-name DOMAIN`. Issuance accepts only `http-01` or `dns-01`; unknown
challenge types are rejected before Certbot runs. DNS-01 always uses the
installation-owned credential path and never accepts an arbitrary path from a
browser request.

---

## 9. First Login and Admin Account

On first startup the application checks whether the `users` table is
empty. If so, it creates an admin account using `HSERVER_ADMIN_EMAIL`
and `HSERVER_ADMIN_PASS`.

1. Open `https://hserver.yourdomain.com` in your browser
2. Log in with the email and password from the service environment
3. Navigate to **Settings → Users** and change the admin password
4. To carry non-secret preferences from another HServer installation, use
   **Settings → Portable Configuration** and follow the
   [schema-v1 import workflow](portable-configuration.md). This is an
   allowlisted overlay, not a panel-state restore.
4. Optionally create additional manager or viewer accounts

> If the database already contains users, first-run environment values do not
> overwrite them. Use the authenticated Users screen or the documented password
> reset utility in `scripts/reset-admin-password`. The utility reads the new
> value from the protected environment file and never prints it. Never delete
> the database to recover account access.

---

## 10. PM2 Setup for Node.js Apps (Optional)

HServer Panel can manage Node.js processes through PM2. PM2 **must run
as a non-root user** — the panel communicates with it via `sudo`-wrapped
commands or the `PM2_HOME` environment variable pointing to the user's
PM2 daemon.

```bash
# Install PM2 globally
npm install -g pm2

# Explicit example: use an unprivileged account that owns the Node.js apps.
su - deploy -c "pm2 startup systemd -u deploy --hp /home/deploy"
# Run the output command as root to register the systemd unit

# Verify the systemd unit
systemctl status pm2-deploy
```

Configure HServer in `/etc/hserver/hserver.env` with the same unprivileged
application user and an explicit path allowlist:

```dotenv
HSERVER_PM2_USER=deploy
HSERVER_PM2_HOME=/home/deploy/.pm2
HSERVER_PM2_BIN=pm2
HSERVER_PM2_ALLOWED_ROOTS=/srv/hserver/sites,/home/deploy/apps,/opt/apps
```

If PM2 was installed through NVM, set `HSERVER_PM2_BIN` to its absolute path,
for example `/home/deploy/.nvm/versions/node/v22.14.0/bin/pm2`. Leave
`HSERVER_PM2_HOME` empty only when PM2's normal home discovery is correct. When
`HSERVER_PM2_USER` is empty, the PM2 API explicitly reports the integration as
not configured; HServer never starts a root-owned PM2 daemon silently. Deploy
scripts and working directories must remain below one of the absolute
`HSERVER_PM2_ALLOWED_ROOTS`. Leaving that variable empty keeps PM2 deployment
`not_configured`; HServer never falls back to `HSERVER_VHOSTS_ROOT`, `/home`, or
`/opt`. Set the variable explicitly for every installation, including when a
custom `HSERVER_VHOSTS_ROOT` such as `/srv/hserver/sites` is used.

The default native unit runs HServer as root because host-management actions
need elevated access; it can switch to `deploy` without a sudoers addition.
If you deliberately run a custom unit as a dedicated `hserver` account, allow
only the matching PM2 command with `visudo -f /etc/sudoers.d/hserver-pm2` and
replace the binary path when you use NVM:

```sudoers
hserver ALL=(deploy) NOPASSWD: /usr/bin/env PM2_HOME=/home/deploy/.pm2 pm2 *
```

Do NOT run `pm2 startup` as root and do NOT start a second root-owned PM2 daemon.
When PM2 is not configured or its inventory fails, the PM2 page keeps Save,
deploy, and process actions disabled and shows these same owner, binary, home,
and service recovery checks before offering read-only detection retry.

### PHP-FPM runtime management

PHP-FPM is optional for the core panel and required only when HServer manages
PHP sites and pools. Install each desired version from repositories supported
by the target Ubuntu release. For the Ubuntu 24.04 default runtime:

```bash
apt update
apt install -y php8.3-fpm
systemctl enable --now php8.3-fpm
test -d /etc/php/8.3/fpm
systemctl status php8.3-fpm --no-pager
```

HServer detects versioned configuration roots below
`HSERVER_PHP_CONFIG_ROOT` (default `/etc/php`) and locates matching
`php-fpm<VERSION>` binaries below `HSERVER_PHP_BINARY_ROOT` (default
`/usr/sbin`) for PHP-FPM readiness and management. These are installation-local
absolute paths, not provider-specific assumptions. An explicitly configured
relative value fails closed: HServer does not reinterpret it relative to the
process directory or silently fall back to `/etc/php` or `/usr/sbin`; PHP
readiness and management remain unavailable until an absolute local root is
configured. If no version is detected, the PHP page reports **not configured**
and keeps runtime controls hidden. If inventory detection fails, it reports
**unavailable**, preserves the error, and offers a read-only retry after the
operator checks the HServer logs, configuration access, binary, and matching
systemd unit.

PHP pool defaults, security-profile document roots, and Composer project access
use the installation's absolute `HSERVER_VHOSTS_ROOT`. Composer rejects missing
projects, paths outside that root, and symlinks that resolve outside it. Domain
file controls use the document root observed in the domain inventory and pause
instead of guessing a provider-specific path when that value is unavailable.

The release archive also includes a bounded application-secret permission
repair utility. Its default mode is read-only and exits `3` when it finds a
root-owned `.env` file with a discoverable non-root application owner:

```bash
HSERVER_VHOSTS_ROOT=/srv/hserver/sites ./fix-env-permissions.sh --check
sudo HSERVER_VHOSTS_ROOT=/srv/hserver/sites \
  HSERVER_ENV_READ_GROUP=www-data ./fix-env-permissions.sh --apply
```

`--apply` changes only those root-owned `.env` files to the discovered
application owner, the configured runtime group, and mode `0640`. It refuses a
relative root or `/`, handles filenames without whitespace splitting, skips
sites without a non-root owner, and never guesses or restarts a PHP-FPM unit.

### BIND DNS management

BIND is optional for the core panel and required only for local authoritative
zone management. Install the packages supported by the target Ubuntu release:

```bash
apt update
apt install -y bind9 bind9-utils
named -v
named-checkconf -z
systemctl status bind9 --no-pager
```

HServer observes `/etc/bind/named.conf.local`, `named-checkconf`,
`named-checkzone`, `rndc`, and the `named` or `bind9` unit. It does not install
packages, create a default zone, start a unit, or replace existing BIND files
during detection. The DNS page classifies readiness as `healthy`,
`not-installed`, `not-configured`, `stopped`, or `unavailable` and enables zone
mutations only when `zoneManagementReady` is true.

When BIND is stopped, existing zones remain readable but changes and reloads
stay paused. Check the configuration before explicitly starting the unit:

```bash
named-checkconf -z
systemctl status bind9 named --no-pager
journalctl -u bind9 -u named --since "30 minutes ago" --no-pager
```

Use **Retry detection** after repairing the installation. Retry only observes
state; it never mutates the host.

Zone create/delete transactions persist a protected recovery journal below
`${HSERVER_DATA_DIR}/bind/`. It contains pre-change BIND file snapshots, so the
data directory must remain private and backed up; do not copy the journal into
public diagnostics. HServer checks it before starting the HTTP router. An
interrupted pre-reload transaction is rolled back and BIND is reloaded, while a
transaction already marked reloaded is finalized. If recovery cannot finish,
the DNS page reports **BIND Recovery Required** and keeps all DNS mutations
disabled. Repair BIND validation or `rndc reload`, then restart HServer; never
delete the pending journal to bypass the action gate.

### PostgreSQL and MariaDB management

Database inventory and management use the host's local command-line clients;
HServer does not download a client or collect an arbitrary database password.
Install only the engines and clients that this host will manage:

```bash
apt update
apt install -y postgresql-client mariadb-client

psql --version
mysql --version
```

PostgreSQL management runs through the installation-owned `postgres` OS
identity (`sudo -u postgres psql`). MariaDB management uses the local root
identity (`mysql -u root`) and the authentication policy configured by the
MariaDB installation. Verify those same paths locally without printing a
password:

```bash
sudo -u postgres psql -d postgres -c '\conninfo'
sudo mysql -u root -e 'SELECT 1;'
```

The Databases page classifies each source as `healthy`, `client-missing`,
`stopped`, `authentication-failed`, or `unavailable`. A failed source is never
reported as an empty healthy inventory: counts remain unknown and create/drop
controls stay disabled. Recovery guidance points to the matching client,
systemd unit, local authentication identity, and preserved command error; its
retry action performs detection only and never installs packages, starts a
service, or changes authentication.

### Local firewall management

HServer's local firewall mutations use UFW. Install it from the target Ubuntu
release before enabling firewall controls:

```bash
apt update
apt install -y ufw
ufw version
ufw status verbose
```

Before enabling UFW on a remote host, add and verify the allow rule for the
actual management port and source used by that installation. HServer does not
install or enable UFW during readiness detection.

`GET /api/firewall/status` distinguishes a healthy UFW installation from
`ufw-missing` and `unavailable`. When UFW is missing but `iptables` can still be
read, the panel shows those legacy rules as **read-only observation**; toggle,
add, delete, presets, and default-policy claims remain disabled. An installed
but inactive UFW is still manageable and can be enabled only through the
operator's explicit action. The toggle request always sends explicit
`{"enable": true}` or `{"enable": false}` intent.

### Fail2Ban security integration (Optional)

Fail2Ban is optional for the core panel and is needed only for local jail
inventory and IP ban/unban actions. Install and enable it explicitly on hosts
where that feature is desired:

```bash
apt update
apt install -y fail2ban
systemctl enable --now fail2ban
fail2ban-client --version
fail2ban-client ping
```

The Security page reports a missing Fail2Ban client as `not installed`, a
stopped daemon as `stopped`, and an indeterminate client or socket as
`unavailable`; it never installs packages or creates a jail during detection.
After repairing the host service, use **Retry detection**. Ban and unban
controls remain disabled until the daemon and its complete jail inventory are
observed as healthy.

---

## 11. Stalwart Mail Integration (Optional)

Stalwart is not configured by default. Use this section only when Stalwart is
explicitly installed and enabled on the target host. Set the API endpoint,
credentials, service unit, configuration path, and binary path to values from
that installation; HServer does not infer them. The `/opt/stalwart/...` values
below are an explicit opt-in example, not defaults:

1. Obtain a Stalwart management API key from the Stalwart admin panel.
2. Add the API connection and local discovery boundary to
   `/etc/hserver/hserver.env`:

```dotenv
# Explicit opt-in example; replace the endpoint and paths with local values.
STALWART_URL=http://127.0.0.1:9080
STALWART_API_KEY=replace-with-a-protected-api-key
HSERVER_STALWART_SERVICE=stalwart
HSERVER_STALWART_CONFIG_PATH=/opt/stalwart/etc/config.toml
HSERVER_STALWART_BIN=/opt/stalwart/bin/stalwart-mail
```

Or use Basic auth credentials as fallback:

```dotenv
# Explicit opt-in fallback; keep both values in the protected env file.
STALWART_ADMIN_USER=admin
STALWART_ADMIN_PASS=replace-with-a-protected-password
```

3. Reload:

```bash
systemctl daemon-reload && systemctl restart hserver
```

4. In the panel, navigate to **Mail** to verify connectivity.
   The panel provides: mail account management, DKIM key generation,
   DNS health checks, queue management, spam configuration, and
   delivery statistics.

Empty settings keep Mail `not_configured`; a supplied URL or local path alone
does not prove `healthy`. HServer reports a failed configured endpoint or local
discovery source as `unavailable`, preserves that state, and disables start,
stop, and restart. The Mail overview lists the discovery keys, configured-unit
check, and a read-only detection retry; it never guesses a service name or
changes protected paths.

---

## 12. Cloudflare Integration (Optional)

1. In the Cloudflare dashboard, create an API Token with the following
   permissions for each zone you want to manage:
   - Zone: Read
   - DNS: Edit
   - Cache: Purge
   - Email Routing: Edit (if managing mail DNS)

2. Add the token to `/etc/hserver/hserver.env`:

```dotenv
HSERVER_CF_API_TOKEN=replace-with-a-protected-cloudflare-token
```

3. Reload:

```bash
systemctl daemon-reload && systemctl restart hserver
```

4. In the panel, navigate to **Cloudflare** to list zones and manage DNS records.

A missing token is reported as **not configured**. Provider connectivity or
scope failures are reported as **unavailable**, not as an empty zone list. The
recovery action only repeats detection; it never creates or broadens a token.

---

## Google Drive Integration (Optional)

Google Drive offsite backups require `rclone` on the HServer host plus a Google
OAuth client owned by the installation:

1. Install `rclone` from packages supported by the host distribution.
2. Run `rclone version` as, or in the environment of, the HServer service
   identity.
3. Set `HSERVER_GDRIVE_CLIENT_ID`, `HSERVER_GDRIVE_CLIENT_SECRET`, and, only
   when origin discovery is unsuitable, `HSERVER_GDRIVE_REDIRECT_URI` in
   `/etc/hserver/hserver.env`.
4. Restart HServer, open **Backups → Google Drive**, and retry detection before
   starting OAuth.

HServer never installs rclone automatically. When rclone is missing, the panel
shows installation-owned recovery guidance and keeps the Google connection
button disabled. OAuth credentials may still be prepared, but the provider
flow cannot start until dependency detection succeeds.

### Encrypted restic snapshots

The optional full-server snapshot flow additionally requires `restic` and an
installation-owned repository password:

```dotenv
HSERVER_RCLONE_BIN=rclone
HSERVER_RESTIC_BIN=restic
HSERVER_RESTIC_PASSWORD=replace-with-a-unique-generated-secret
```

Generate the password locally with `openssl rand -base64 32`, store it in a
separate durable password vault, then place it in the protected HServer
environment file. Losing this value makes the encrypted repository
unrecoverable; HServer cannot display or recreate it.

When restic is missing, the panel keeps snapshot and schedule actions disabled
and explains the supported-package and `HSERVER_RESTIC_BIN` recovery path. When
the password is missing, it shows a separate not-configured state. Detection
retry remains read-only and never installs a package or generates a secret.

### S3-compatible encrypted snapshot destination

HServer can use an installation-owned S3-compatible service or local MinIO
instead of Google Drive. Restic encrypts repository content on the HServer host
before upload; the provider receives encrypted repository objects. Configure
the provider without placing credential values in panel settings or SQLite:

```dotenv
HSERVER_S3_ENDPOINT=https://objects.example.com
HSERVER_S3_BUCKET=hserver-backups
HSERVER_S3_REGION=eu-central-1
HSERVER_S3_ACCESS_KEY_FILE=/etc/hserver/secrets/s3-access-key
HSERVER_S3_SECRET_KEY_FILE=/etc/hserver/secrets/s3-secret-key
HSERVER_S3_BUCKET_LOOKUP=auto
```

Create both credential files as absolute, regular files owned by the HServer
service identity and set their mode to `0600`. HServer rejects symlinks,
group/world-readable files, empty values, multiline values, and oversized
files. Restart the service, then choose **Backups → Snapshot hedefi →
S3-compatible / MinIO** or run:

```bash
hserverctl backups snapshot destination s3
hserverctl backups snapshot status
hserverctl backups snapshot list
hserverctl backups snapshot vhosts
hserverctl backups snapshot run --confirm
```

The destination reports `not_configured`, `unavailable`, or `healthy`
separately. `healthy` requires a read-only remote repository probe; the mere
presence of an endpoint URL is insufficient. A missing repository remains a
healthy first-run state only when the remote endpoint accepted the repository
request. Repository purge is deliberately unavailable for S3 in this release;
snapshot creation, retention, listing, and restore remain supported.

Restore always extracts into HServer's fixed local staging directory. It
requires an observed 8–64 character hexadecimal snapshot identity and an
explicit scope:

```bash
hserverctl backups snapshot restore --confirm --all abcdef1234567890
hserverctl backups snapshot restore \
  --confirm --vhost example.com abcdef1234567890
```

See the CLI guide for installation-owned manifest selectors and the
Google-Drive-only exact-repository purge command.

See [S3-compatible encrypted snapshots](s3-snapshots.md) for the complete
provider-neutral setup and recovery contract.

---

## Local Docker Compose Deployments

Native installations can register an existing local Git checkout as a Docker
Compose deployment target. HServer validates the target with a read-only
preflight and executes fixed `docker compose config` and `docker compose up`
arguments; Compose mode does not accept an arbitrary command. Docker Engine and
the Compose v2 plugin are optional and are reported as unavailable when absent.
An absent or empty absolute project directory can be provisioned from a
token-free HTTPS or SSH repository URL during its first deployment.

See [Docker Compose deployments](docker-compose-deployments.md) for the target
fields, exact command contract, credential boundary, and rollback limits.

### Optional deployment templates

The native panel reads reusable target presets from the fixed
`${HSERVER_DATA_DIR}/deploy-templates` directory. A fresh lifecycle installation
seeds the provider-neutral Compose and Node.js starter templates when that
directory does not exist. An existing directory—including an intentionally
empty one—is never populated or overwritten, and upgrades never change its
files. A failed initial installation removes the seeded directory when HServer
created it as part of that attempt.

When the directory is absent or empty, the API and panel report
`not_configured` and keep custom target creation available. A source checkout
contains the starters under `deploy/hserver-deploy-templates.example`; a release
archive contains the same files under `deploy-templates.example`.

To restore missing packaged starter files without replacing existing names:

```bash
sudo install -d -m 0755 /var/lib/hserver/deploy-templates
for template in deploy-templates.example/*.json; do
  target="/var/lib/hserver/deploy-templates/$(basename "$template")"
  sudo test -e "$target" || sudo install -m 0644 "$template" "$target"
done
```

When installing from source, replace the second path with
`deploy/hserver-deploy-templates.example/*.json`. If `HSERVER_DATA_DIR` is not
`/var/lib/hserver`, use its configured value instead. Restart is unnecessary;
the inventory is read when the panel or `hserverctl deploy templates` requests
it.

The directory and files must not be group- or world-writable. Symlinks,
non-regular files, files larger than 64 KiB, unknown JSON fields, more than 128
JSON files, and filenames outside the lowercase `template-id.json` contract are
reported as `unavailable`. Valid files remain selectable while invalid files
are listed for repair.

## 13. Enroll a Managed Server Agent

Each additional server connects outbound to the HServer HTTPS origin. The hub
does not need inbound SSH access to the managed server. Register a
provider-neutral node ID through `POST /api/nodes` using an authenticated HServer
admin session; the response contains the enrollment token exactly once.

On the managed server, extract the release archive for its CPU architecture.
Download the generated environment from the enrollment card, save the one-time
token to a separate protected file, and run the packaged lifecycle installer:

```bash
./hserver-agent --version
sudo ./agent-install.sh install \
  --binary ./hserver-agent \
  --config ./hserver-agent-node-1.env \
  --token-file ./hserver-agent.token
```

The installer validates the inputs, copies the environment file with mode
`0600`, and atomically copies the token source to the destination named by
`HSERVER_AGENT_TOKEN_FILE`. The destination must be a clean absolute file path
of at most 4096 bytes without CR, LF, NUL, whitespace, root, traversal,
duplicate-slash, or trailing-slash ambiguity; its missing parent is created by
the installer with mode `0700`, and the installed token has mode `0600`. The
canonical default remains `/etc/hserver-agent.token`; for example, a
provider-neutral custom layout can use
`HSERVER_AGENT_TOKEN_FILE=/srv/secrets/hserver-agent.token`. The same resolved
destination is retained by upgrades and rollback, preserved by a normal
uninstall, and removed only by `uninstall --purge-config`.

`--token-file` is the protected one-time **source** supplied to `install`; it is
never copied into the environment file and the installer never prints its
contents. The running agent also supports `HSERVER_AGENT_TOKEN` as a
runtime-only fallback when it is launched directly without a token-file
setting, but the lifecycle installer rejects any `HSERVER_AGENT_TOKEN=...` line
so installer-managed configuration cannot embed a literal token. A failed
preflight stops before installation files are written. Do not print or commit
the source token file.
The resulting environment supports:

| Variable | Required | Purpose |
|---|---|---|
| `HSERVER_AGENT_HUB_URL` | Yes | Public or private HTTP(S) origin of this HServer installation |
| `HSERVER_AGENT_NODE_ID` | Yes | Node ID used during registration |
| `HSERVER_AGENT_TOKEN_FILE` | Yes* | Authoritative absolute destination of the root-readable one-time token file; default `/etc/hserver-agent.token` |
| `HSERVER_AGENT_TOKEN` | Runtime only | Direct-launch fallback when no token-file setting is used; rejected by the lifecycle installer |
| `HSERVER_AGENT_INTERVAL` | No | Heartbeat/task poll interval, from `5s` to `1h`; default `30s` |
| `HSERVER_AGENT_OBSERVED_SERVICES` | No | Comma-separated systemd units reported as inventory |
| `HSERVER_AGENT_ALLOWED_SERVICES` | No | Units the panel may start, stop, or restart |
| `HSERVER_AGENT_ALLOWED_HOST_ACTIONS` | No | Any subset of `memory-optimize,swap-reset,temp-clean,reboot,reboot-cancel` |
| `HSERVER_AGENT_ALLOW_PROCESS_SIGNALS` | No | `true` enables stable-identity SIGTERM/SIGKILL; default `false` |
| `HSERVER_AGENT_ALLOW_TERMINAL` | No | `true` enables outbound multiplexed writable root PTYs; default `false` |
| `HSERVER_AGENT_ALLOWED_DISK_CLEANUP` | No | Comma-separated fixed scopes: `apt-cache,journal,tmp-old,rotated-logs`; empty disables remote cleanup |
| `HSERVER_AGENT_ALLOWED_LOG_SOURCES` | No | Comma-separated fixed journal sources: `system,nginx,php,mariadb,postgresql,pm2,docker`; empty disables remote journal access |
| `HSERVER_AGENT_ALLOW_CONTAINER_READ` | No | `true` enables bounded Docker container inventory; default `false` |
| `HSERVER_AGENT_ALLOWED_CONTAINER_ACTIONS` | No | Any subset of `start,restart,stop`; empty disables remote container actions |
| `HSERVER_AGENT_ALLOWED_NGINX_ACTIONS` | No | Any subset of `test,reload`; empty disables remote Nginx actions |
| `HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ` | No | `true` enables bounded Nginx config inventory/read; default `false` |
| `HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE` | No | `true` enables checksum-guarded atomic save, backup, validation and optional reload; requires config read |
| `HSERVER_AGENT_NGINX_SITES_AVAILABLE` | No | Local available-config directory; default `/etc/nginx/sites-available` |
| `HSERVER_AGENT_NGINX_SITES_ENABLED` | No | Local enabled-config directory; default `/etc/nginx/sites-enabled` |
| `HSERVER_AGENT_ALLOW_DOMAIN_READ` | No | `true` enables bounded Nginx-backed domain inventory; default `false` |
| `HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS` | No | `true` enables reversible site enable/disable with Nginx validation and reload; requires domain read |
| `HSERVER_AGENT_ALLOW_SSL_READ` | No | `true` enables bounded local X.509/Certbot certificate inventory; default `false` |
| `HSERVER_AGENT_ALLOW_SSL_ACTIONS` | No | `true` enables fixed chain-check and renew-if-due actions; requires SSL read |
| `HSERVER_AGENT_CERTBOT_CONFIG_DIR` | No | Certbot configuration/state root; default `/etc/letsencrypt` |
| `HSERVER_AGENT_CERTBOT_WORK_DIR` | No | Certbot working directory; default `/var/lib/letsencrypt` |
| `HSERVER_AGENT_CERTBOT_LOGS_DIR` | No | Certbot log directory; default `/var/log/letsencrypt` |
| `HSERVER_AGENT_CERTBOT_BINARY` | No | Absolute Certbot executable path; default `/usr/bin/certbot` |
| `HSERVER_AGENT_OPENSSL_BINARY` | No | Absolute OpenSSL executable path; default `/usr/bin/openssl` |
| `HSERVER_AGENT_CA_BUNDLE` | No | Absolute CA bundle path used for chain checks; default `/etc/ssl/certs/ca-certificates.crt` |
| `HSERVER_AGENT_ALLOW_DATABASE_READ` | No | `true` enables bounded MariaDB/PostgreSQL inventory over local identities; default `false` |
| `HSERVER_AGENT_ALLOWED_DATABASE_RESTARTS` | No | Any subset of `mariadb,postgresql`; empty disables database restart actions |
| `HSERVER_AGENT_MARIADB_BINARY` | No | Absolute MariaDB client path; default `/usr/bin/mariadb` |
| `HSERVER_AGENT_MARIADB_ADMIN_BINARY` | No | Absolute MariaDB health-check client path; default `/usr/bin/mariadb-admin` |
| `HSERVER_AGENT_PG_LSCLUSTERS_BINARY` | No | Absolute PostgreSQL cluster discovery path; default `/usr/bin/pg_lsclusters` |
| `HSERVER_AGENT_PSQL_BINARY` | No | Absolute PostgreSQL client path; default `/usr/bin/psql` |
| `HSERVER_AGENT_PG_ISREADY_BINARY` | No | Absolute PostgreSQL readiness client path; default `/usr/bin/pg_isready` |
| `HSERVER_AGENT_ALLOW_BACKUP_READ` | No | `true` enables bounded inventory for locally configured backup plans; default `false` |
| `HSERVER_AGENT_ALLOW_BACKUP_RUN` | No | `true` enables manual runs for plan IDs in the local plan file; requires backup read |
| `HSERVER_AGENT_BACKUP_PLANS_FILE` | No | Absolute JSON plan file; default `/etc/hserver/backup-plans.json` |
| `HSERVER_AGENT_ALLOW_DEPLOY_READ` | No | `true` exposes bounded metadata for locally configured deploy plans; default `false` |
| `HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS` | No | `true` runs only plan-declared `preflight`, `deploy`, `restart`, or `rollback` argv; requires deploy read |
| `HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ` | No | `true` exposes only HServer-owned project-domain mappings, observed TLS state, and loopback health probes; requires deploy read |
| `HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS` | No | `true` enables fixed create/delete/TLS operations using the local plan port; requires project-domain read |
| `HSERVER_AGENT_DEPLOY_PLANS_FILE` | No | Absolute JSON plan file; default `/etc/hserver/deploy-plans.json` |
| `HSERVER_AGENT_DEPLOY_ACME_WEBROOT` | No | Absolute HTTP-01 challenge root; default `/var/www/hserver-acme` |
| `HSERVER_AGENT_DEPLOY_WRITE_ROOTS` | No | Comma-separated local paths granted through the agent service's read-only systemd sandbox for release commands |
| `HSERVER_AGENT_ALLOW_UPDATE_READ` | No | `true` exposes server-local release discovery, durable lifecycle state, and rollback availability; default `false` |
| `HSERVER_AGENT_ALLOW_UPDATE_ACTIONS` | No | `true` allows only an exact stable-version upgrade or latest verified rollback; requires update read; default `false` |
| `HSERVER_AGENT_UPDATE_MANIFEST_URL` | No | Server-local HTTP(S) schema-v1 release manifest URL without credentials; never accepted from the hub |
| `HSERVER_AGENT_UPDATE_MANIFEST_PUBLIC_KEYS` | No | Up to eight comma-separated base64 raw Ed25519 public keys; non-empty requires a valid adjacent detached signature before the agent accepts release metadata |
| `HSERVER_AGENT_STATE_DIR` | No | Protected agent lifecycle state and snapshot root; default `/var/lib/hserver-agent` |
| `HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT` | No | Shared lifecycle retention count for managed-agent pre-upgrade snapshots; default `3`, integer `1`–`100`; the active rollback marker is always preserved |
| `HSERVER_AGENT_LIFECYCLE_INSTALLER` | No | Persisted lifecycle installer path; default `/usr/local/libexec/hserver-agent-install` |
| `HSERVER_AGENT_SYSTEMD_RUN_BINARY` | No | Absolute transient-unit scheduler; default `/usr/bin/systemd-run` |
| `HSERVER_AGENT_SYSTEMCTL_BINARY` | No | Absolute lifecycle reconciliation tool; default `/usr/bin/systemctl` |
| `HSERVER_AGENT_FILE_READ_ROOTS` | No | Comma-separated clean absolute roots exposed for bounded directory browsing and UTF-8 text reads; empty disables file management |
| `HSERVER_AGENT_FILE_WRITE_ROOTS` | No | Read-root subset that permits checksum-guarded backup and atomic replacement; empty keeps file management read-only |
| `HSERVER_AGENT_ALLOWED_PHP_ACTIONS` | No | Any subset of `test,reload,restart`; empty disables remote PHP-FPM actions |
| `HSERVER_AGENT_ALLOW_PHP_CONFIG_READ` | No | `true` enables bounded PHP-FPM inventory and pool reads; default `false` |
| `HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE` | No | `true` enables checksum-guarded atomic pool save, backup, validation and optional reload; requires config read |
| `HSERVER_AGENT_PHP_CONFIG_ROOT` | No | Local PHP configuration root; default `/etc/php` |
| `HSERVER_AGENT_PHP_BINARY_ROOT` | No | Local PHP-FPM binary directory; default `/usr/sbin` |
| `HSERVER_AGENT_ALLOW_PM2_READ` | No | `true` enables bounded PM2 process inventory and logs; default `false` |
| `HSERVER_AGENT_ALLOWED_PM2_ACTIONS` | No | Any subset of `start,restart,reload,stop`; empty disables PM2 actions |
| `HSERVER_AGENT_PM2_BINARY` | No | Absolute local PM2 executable; empty keeps managed-node PM2 `not_configured` until explicitly enabled |
| `HSERVER_AGENT_PM2_HOME` | No | Absolute PM2 state directory for the configured identity; empty keeps managed-node PM2 `not_configured` until explicitly enabled |
| `HSERVER_AGENT_PM2_USER` | No | Explicit local unprivileged Unix identity used for PM2 commands; empty keeps managed-node PM2 `not_configured` and `root` is rejected |
| `HSERVER_AGENT_ALLOW_CRON_READ` | No | `true` enables bounded cron inventory; default `false` |
| `HSERVER_AGENT_ALLOW_CRON_WRITE` | No | `true` enables revision-guarded managed cron changes; requires cron read |
| `HSERVER_AGENT_ALLOW_CRON_RUN` | No | `true` enables manual execution of previously stored managed jobs; requires cron read |
| `HSERVER_AGENT_CRON_STATE_PATH` | No | Managed cron JSON state; default `/etc/hserver/cron-jobs.json` |
| `HSERVER_AGENT_CRON_FILE_PATH` | No | Rendered system cron file; default `/etc/cron.d/hserver-managed` |
| `HSERVER_AGENT_CRON_LOCK_PATH` | No | Cross-process mutation lock; default `/run/lock/hserver-cron.lock` |
| `HSERVER_AGENT_CRONTAB_BINARY` | No | Absolute syntax-check executable; default `/usr/bin/crontab` |
| `HSERVER_AGENT_RUNUSER_BINARY` | No | Absolute user-switch executable; default `/usr/sbin/runuser` |
| `HSERVER_AGENT_CRON_SHELL` | No | Absolute shell for rendered/manual jobs; default `/bin/bash` |
| `HSERVER_AGENT_CRON_SERVICE` | No | Local systemd cron unit; default `cron.service` |
| `HSERVER_AGENT_ALLOW_FIREWALL_READ` | No | `true` enables bounded IPv4 INPUT and managed-chain inventory; default `false` |
| `HSERVER_AGENT_ALLOW_FIREWALL_WRITE` | No | `true` enables revision-guarded HServer-chain mutations; requires firewall read |
| `HSERVER_AGENT_IPTABLES_BINARY` | No | Absolute local iptables executable; default `/usr/sbin/iptables` |
| `HSERVER_AGENT_FIREWALL_SAVE_BINARY` | No | Absolute persistence executable invoked with fixed `save`; default `/usr/sbin/netfilter-persistent` |
| `HSERVER_AGENT_FIREWALL_LOCK_PATH` | No | Cross-process mutation lock; default `/run/lock/hserver-firewall.lock` |
| `HSERVER_AGENT_FIREWALL_PERSISTENCE_SERVICE` | No | Local systemd unit reported as persistence state; default `netfilter-persistent.service` |
| `HSERVER_AGENT_FIREWALL_PERSISTENCE_PATH` | No | Directory the persistence tool writes; default `/etc/iptables`; the installer grants only this path through the read-only service sandbox |
| `HSERVER_AGENT_FIREWALL_PROTECTED_SOURCES` | No | Comma-separated local IPv4 addresses/CIDRs protected from overlapping DROP rules; empty by default |
| `HSERVER_AGENT_FIREWALL_PROTECTED_PORTS` | No | Comma-separated local management ports protected with the source list; empty by default |

Managed-node PM2 remains `not_configured` until the identity fields and the
corresponding read/action flags are explicitly set. For example, on a node
whose local PM2 account is the unprivileged `deploy` user:

```dotenv
HSERVER_AGENT_ALLOW_PM2_READ=true
HSERVER_AGENT_ALLOWED_PM2_ACTIONS=start,restart,reload,stop
HSERVER_AGENT_PM2_BINARY=/usr/local/bin/pm2
HSERVER_AGENT_PM2_HOME=/home/deploy/.pm2
HSERVER_AGENT_PM2_USER=deploy
```

The binary and home path must exist on that node, and `HSERVER_AGENT_PM2_USER`
must name the local non-root owner of the PM2 daemon. Replace every example
value with the target's explicit installation paths; leaving any identity field
empty keeps PM2 unavailable for managed actions.

`*` `HSERVER_AGENT_TOKEN` may be used instead, but never configure both token
inputs. Empty action allowlists intentionally keep the corresponding controls
read-only. The packaged systemd unit runs the agent as root because the enabled
host actions require systemd and kernel maintenance privileges; the agent only
accepts the structured actions explicitly listed in its local configuration.
The unit keeps its address-family sandbox, but includes `AF_NETLINK` so the
standard `iptables-nft` backend can observe and update kernel firewall state
when the corresponding local capabilities are enabled. Removing `AF_NETLINK`
causes `firewall.inventory` to fail even for a root agent; it does not provide
an extra hub operation or enable firewall writes by itself.

Backup plans are installation-owned. Copy the packaged
`hserver-agent-backup-plans.example.json` to the configured plan-file path,
replace its service, timer, root, and display name with local values, then set
the read/run opt-ins. `checksum_file`, when present, is relative to `root` and
uses the standard `SHA256SUMS` format. The hub sends only the plan ID; it cannot
provide a unit name, filesystem path, or command.

Deploy plans are also installation-owned. Copy
`hserver-agent-deploy-plans.example.json` to the configured plan-file path and
replace the example ID, working directory, absolute executable, fixed argument
arrays, timeouts, and optional loopback `host_port` with local values. Supported plan kinds are `application`,
`compose`, `service`, and `custom`; supported action keys are `preflight`,
`deploy`, `restart`, and `rollback`. Configure only the filesystem paths those
commands must modify in `HSERVER_AGENT_DEPLOY_WRITE_ROOTS`. The panel queues
only the selected plan ID and action; central task history retains status and
at most 1 MiB of command output.

Project-domain management is independently opt-in. Set `host_port` to the
application port already published on `127.0.0.1`, then enable
`HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ` and, when mutations are wanted,
`HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS`. The central panel never supplies
an upstream. The agent creates only HServer-owned Nginx files, validates the
complete Nginx configuration before reload, restores the previous state after
a failed mutation, and reads the actual X.509 certificate before reporting TLS
as healthy. HTTP-01 issuance requires the domain's public A/AAAA records to
reach the managed node on ports 80 and 443. The agent checks managed TLS at
startup and every 12 hours, renewing only certificates observed as expiring or
expired. Disabling TLS preserves Certbot certificate material.

Agent lifecycle management is independently opt-in. A current package install
persists the lifecycle tool at `/usr/local/libexec/hserver-agent-install`.
Configure a provider-neutral schema-v1 manifest on the managed server, then
enable read and optionally actions:

```dotenv
HSERVER_AGENT_ALLOW_UPDATE_READ=true
HSERVER_AGENT_ALLOW_UPDATE_ACTIONS=true
HSERVER_AGENT_UPDATE_MANIFEST_URL=https://github.com/OWNER/REPOSITORY/releases/latest/download/release-manifest.json
HSERVER_AGENT_UPDATE_MANIFEST_PUBLIC_KEYS=BASE64_ED25519_PUBLIC_KEY
```

Run one manual package upgrade after changing these settings so the installer
regenerates the systemd sandbox with write access only to the protected agent
state directory. **Managed Servers → Overview → Agent lifecycle** can then
check, upgrade, and roll back this server. The browser sends only an exact
stable version plus confirmation. The agent re-fetches its own manifest and
requires `signature_status=verified` before downloading or scheduling an
upgrade. Checksum-only discovery (`signature_status=not_configured`) remains
read-only; an empty trust set or any other unverified status fails closed before
agent lifecycle mutation. After that gate, the agent verifies archive size and
SHA-256, rejects unsafe archive entries, validates the packaged `VERSION` and
ELF architecture, and schedules a detached systemd unit.
The task receipt reports `scheduled`; the follow-up lifecycle state and a new
heartbeat prove completion. No manifest URL, checksum, path, command, or
systemd argument is accepted from the panel.

Writable terminal access is a separate explicit opt-in. When
`HSERVER_AGENT_ALLOW_TERMINAL=true`, the lifecycle installer generates a
systemd unit without the read-only filesystem, private `/tmp`, home-directory,
or executable-memory restrictions that would make a real administrative shell
misleadingly read-only. With the toggle omitted or `false`, the stricter service
sandbox remains active. For an existing installation, edit
`/etc/hserver-agent.env` and run an agent upgrade so the lifecycle installer
regenerates the unit; a service restart alone does not rewrite the sandbox:

```bash
sudo ./agent-install.sh upgrade --binary ./hserver-agent
```

Firewall lockout protection is deliberately installation-specific: it becomes
active only when both protected source and port lists are configured. The
generated enrollment enables firewall read/write and suggests port `22`, but
does not invent a trusted source address. Add the real management source CIDR
locally before relying on the protection policy. Firewall writes also require
the configured persistence package and writable persistence path.

Start and verify the agent:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now hserver-agent
sudo systemctl status hserver-agent --no-pager
sudo journalctl -u hserver-agent -n 50 --no-pager
```

Agent updates preserve the configuration and configured token destination, create a recovery snapshot,
and restore the previous binary automatically if the new service does not become
active. Rollback also restores the agent's previous enabled and active state, so
an intentionally stopped agent is not left running after a failed update:

```bash
sudo ./agent-install.sh upgrade --binary ./hserver-agent
sudo ./agent-install.sh rollback
sudo ./agent-install.sh uninstall # preserves configuration and token destination
```

The lifecycle installer is copied to its configured persistent path after a
successful install or upgrade and removed by uninstall. A failed managed
upgrade invokes the same automatic binary/service rollback boundary as a
manual upgrade.

For stable version tags, the public CI workflow installs a real panel and
systemd-managed agent on disposable Ubuntu 24.04 runners for both `amd64` and
`arm64`. It uses a temporary Ed25519-signed next-release feed to verify node
enrollment, heartbeat capabilities, update status, central upgrade, completed
follow-up state, a systemd stop followed by the 45-second offline transition,
rejection and non-persistence of work while offline, restart and online
recovery, central rollback, crash-loop automatic recovery, preservation of
`/etc/hserver-agent.env` and `/etc/hserver-agent.token`, and non-purge uninstall.
The packaged CLI doctor in each managed-agent job also requires the enrolled
node to report that job's exact `amd64` or `arm64` architecture; a missing or
cross-architecture heartbeat cannot satisfy the release gate.
The tag matrix also runs the real panel and agent in separate Linux network
namespaces with no default route on both architectures. It proves outbound
enrollment over the point-to-point link, distinct loopback boundaries,
capability discovery, observed process inventory, one stable-identity process
signal, and task-free `409 Conflict` rejection for an unadvertised host action.
`Create GitHub Release` cannot run unless both managed-agent jobs, both network
isolation jobs, and both native panel lifecycle jobs pass. Operators should
still run the public launch checklist's separate-VM/provider-network drill
before a general-availability declaration because namespaces share the runner
kernel and infrastructure path.

The public source includes a reusable candidate drill for that remaining real
network boundary. Run it on the native panel host against an already enrolled
disposable VM whose agent enables `terminal` and process signals. Leave at
least one safe denial-check capability, `host.action`, `agent.update.read`, or
`backup.read`, disabled:

```bash
HSERVER_ACCEPT_PROVIDER_NETWORK=1 \
  ./scripts/accept-provider-network-managed-agent.sh \
  --confirm-bounded-marker \
  --panel-url https://panel.example.com \
  --node disposable-edge-1 \
  --token-file "$HOME/.config/hserver/token" \
  --receipt "$HOME/provider-network-receipt.json"
```

The panel URL must be an HTTPS origin resolving to a globally routable address.
The admin token must be a current-user-owned, non-symlink regular file with
mode `0600`. The script refuses a third-host runner by comparing the native
panel's authenticated `/api/system/info` boot ID with the local kernel, then
requires a different managed-node boot ID. Pre-release panels without the local
boot-ID field must match the runner hostname and record
`hostname_compatibility` instead. Current acceptance also requires the managed
node heartbeat to report `amd64` or `arm64` and records the exact `hserverctl`
version used to open the terminal. Schema v3 also reads the public
`/api/health` build commit and binds it to the receipt.

Its only mutation is a uniquely named, detached Python marker lasting at most
300 seconds. The allocation is sized between 16 MiB and 96 MiB so the marker is
observable in the agent's bounded top-50 process inventory; the drill refuses
unless the node reports at least four times that amount available. It then
terminates the exact observed PID and process start time. The runner chooses
`host.action`, `agent.update.read`, or `backup.read` only when the node does not
advertise it, requires `409 Conflict`, and confirms unchanged task history. Linux PTY `EIO`
from an older agent is recorded as `legacy_agent_eio`; current agents report a
normal terminal close. See the
[public launch checklist](public-launch-checklist.md) for the receipt boundary
and general-availability decision.

Validate the resulting receipt against the exact release identities before
using it as candidate evidence:

```bash
./scripts/verify-provider-network-receipt.py \
  "$HOME/provider-network-receipt.json" \
  --max-age 24h \
  --panel-version v1.0.0 \
  --panel-commit 0123456789abcdef0123456789abcdef01234567 \
  --panel-arch amd64 \
  --cli-version v1.0.0 \
  --agent-version v1.0.0 \
  --node-arch arm64 \
  --node disposable-edge-1 \
  --panel-origin https://panel.example.com
```

The strict defaults require schema v3, `panel_identity_method=boot_id`, and
`terminal_close_mode=normal`. Schema-v1 and schema-v2 receipts remain
inspectable only with `--require-schema any`, but neither binds
`panel_commit`; hostname and legacy-EIO compatibility also require their
explicit `any` overrides. The verifier checks mode `0600`, current-user
ownership, non-symlink type, exact schema fields, all 13 current boolean
checks, a bounded marker allocation, timestamp freshness, and every supplied
identity. It does not claim cryptographic attestation or provider-account
ownership.

For release evidence, create a dedicated receipt-signing key outside the
repository. Do not reuse the release-manifest private key even though both use
the same Ed25519 file format:

```bash
./scripts/generate-release-signing-key.sh \
  /secure/hserver-provider-receipt-ed25519.pem \
  /secure/hserver-provider-receipt-ed25519.pub

./scripts/sign-provider-network-receipt.sh \
  "$HOME/provider-network-receipt.json" \
  /secure/hserver-provider-receipt-ed25519.pem \
  "$HOME/provider-network-receipt.json.sig"
```

The signer requires the receipt and private key to be current-user-owned,
non-symlink regular files with mode `0600`, refuses to overwrite an existing
signature, verifies the generated signature before publishing it, and writes a
portable base64 detached signature with mode `0644`. Require that signature
when accepting the receipt:

```bash
./scripts/verify-provider-network-receipt.py \
  "$HOME/provider-network-receipt.json" \
  --signature "$HOME/provider-network-receipt.json.sig" \
  --public-key /secure/hserver-provider-receipt-ed25519.pub \
  --require-signature \
  --panel-version v1.0.0 \
  --panel-commit 0123456789abcdef0123456789abcdef01234567 \
  --panel-arch amd64 \
  --cli-version v1.0.0 \
  --agent-version v1.0.0 \
  --node-arch arm64 \
  --node disposable-edge-1 \
  --panel-origin https://panel.example.com
```

Success reports `signature=verified` and the SHA-256 fingerprint of the raw
public key. Omitting signature arguments remains useful for structural
inspection but reports `signature=not_checked`; `--require-signature` prevents
that weaker mode from being accepted accidentally. Signature verification
proves receipt integrity and signer identity, not ownership of the provider
account that supplied the VM.

The node should become online in **Managed Servers** after its first accepted
heartbeat. The hub is the connectivity source of truth: it reports the node
offline 45 seconds after the last accepted heartbeat and refuses new work until
the agent reconnects. Its advertised capabilities determine which controls are
enabled.
See [the agent-hub contract](agent-hub-contract.md) for the exact protocol and
execution boundary.

---

## 14. Updating

### Verified update from the panel

When `HSERVER_UPDATE_MANIFEST_URL` points to a newer stable release and the
manifest result reports `signature_status=verified`, open **About → Release
updates** as an admin:

1. confirm **Manifest trust** reports `Ed25519 signature verified` and
   `signature_status=verified`;
2. choose **Stage & verify** and wait for `Archive verified and ready`;
3. compare the displayed version, archive size, and SHA-256 with the release;
4. accept the restart and automatic-rollback notice;
5. choose **Install verified release** and confirm the exact version; and
6. wait for `Upgrade completed` after the panel reconnects.

If discovery reports `checksum only`, `signature_status=not_configured`, or any
other unverified status, the release card remains read-only: no panel stage,
install, or lifecycle scheduling mutation is accepted. The same fail-closed
gate applies to the managed-agent upgrade action. HServer has no automatic or
unattended updater; updates occur only after an explicit action.

The equivalent packaged-CLI path uses the same API, configured trust set, and
server-observed stage identity:

```bash
hserverctl updates status
hserverctl updates stage --confirm
hserverctl updates install --confirm
hserverctl updates stage-status
```

The second action schedules `hserver-panel-upgrade` through a separate transient
systemd unit after a short delay, so the HTTP response reaches the browser
before HServer stops itself. The detached command receives only server-resolved,
validated installed paths; the API request cannot supply a filesystem target.
`HSERVER_UPDATE_PANEL_BINARY_PATH` and `HSERVER_UPDATE_CLI_BINARY_PATH` take
precedence. Without them, HServer observes its own executable and uses a sibling
`hserverctl`; a process that is not running as `hserver-panel` retains the
native `/usr/local/bin` defaults. Both destinations must already be absolute,
canonical, regular executables with the expected basename. Symlinks, symlinked
parents, application-data destinations, and unsafe executable roots are
rejected before scheduling or service mutation.

For the canonical `/usr/local` layout, the existing lifecycle installer keeps
the native contract: it snapshots the panel and CLI binaries, managed lifecycle
assets, systemd unit, and SQLite databases, then health-checks the replacement.
For an observed or explicitly configured noncanonical layout, the installer
enters preserve-layout mode. It snapshots the exact binary pair plus effective
systemd unit/drop-in and enabled/active state, atomically replaces only the two
configured binaries, and does not rewrite the unit, drop-ins, `EnvironmentFile`,
`WorkingDirectory`, data directory, or databases. An active service is restarted
and health-checked; a deliberately stopped and disabled service stays stopped
and disabled.

The detached runner requires the installed panel and CLI identity strings to
match the complete version/build identities reported by the verified staged
binaries. If health or identity fails, the matching lifecycle restores the
previous binary pair and enabled/active service state and reports the stage as
`failed`; an unsuccessful automatic rollback is also reported as a terminal
failure instead of completion.

Preserve-layout updates deliberately do not copy or restore SQLite because this
binary-only lifecycle does not mutate the database directly and a live file
copy would not be WAL-consistent. Before every noncanonical rollout, create and
verify a transactionally consistent backup. Use the packaged `backup-db.sh`
workflow when available, or SQLite's online backup command for each configured
database, for example:

```bash
sudo sqlite3 /srv/hserver-custom/state/hserver.db \
  ".backup '/root/hserver-pre-update.db'"
sudo sqlite3 /srv/hserver-custom/state/metrics.db \
  ".backup '/root/hserver-metrics-pre-update.db'"
sudo sqlite3 /root/hserver-pre-update.db 'PRAGMA integrity_check;'
```

The protected stage contains the complete fixed HServer Nginx snippet set as
well as the panel, CLI, installer, and doctor. Each selected asset is hashed
when extracted and revalidated before scheduling, so the detached installer has
the complete package without trusting a retained compressed archive.

Inspect both units before retrying:

```bash
journalctl -u hserver-panel-upgrade --since "15 minutes ago"
journalctl -u hserver --since "15 minutes ago"
```

If a reboot or forced stop interrupts the transient unit, the next stage-status
request checks both the timer and service. When systemd conclusively reports
that neither is active, the persisted state becomes `failed` with an explicit
interruption detail instead of remaining `running` indefinitely. The verified
compressed archive is removed after extraction; stage retention keeps the
current stage, the newest previous inactive stage, and every still-active stage.

The manual source and retained binary-recovery workflows below remain available
for development and recovery. They do not replace the signed bootstrap for a
new public release installation; leaving release discovery unconfigured is
appropriate only for a source build or an already trusted local recovery copy.

### Standard update (from the source directory)

```bash
cd "$HOME/src/hserver-panel"

# Pull latest changes
git pull origin main

# Rebuild, snapshot state, upgrade, and health-check
make upgrade

# Verify
systemctl status hserver
journalctl -u hserver --since "1 minute ago"
```

`make deploy` remains an alias for `make upgrade`. Before replacing the binary
set, the lifecycle tool stops the service and stores the current panel and CLI
binaries, systemd unit, and SQLite databases under
`/var/lib/hserver/releases/`. It then starts
the new release and waits for `/api/health`. A failed health check triggers an
automatic rollback to that snapshot. Rollback also restores whether the service
was enabled and active before the upgrade, so updating a deliberately stopped
installation does not silently leave it running.

To upgrade with an already downloaded version-matched binary pair while the new
release package is available, invoke its installer so the lifecycle tools and
assets are refreshed to the same release:

```bash
sudo ./scripts/hserver-install.sh upgrade \
  --binary ./hserver-panel \
  --cli-binary ./hserverctl
```

For recovery or an intentionally binary-only delivery, the retained installer
can perform the same snapshot, health-check, and rollback flow without the old
archive or source checkout. It reuses the complete lifecycle assets persisted by
the last package installation:

```bash
sudo /usr/local/libexec/hserver-install upgrade \
  --binary ./hserver-panel \
  --cli-binary ./hserverctl
```

### Rollback to previous binary

```bash
sudo /usr/local/libexec/hserver-install rollback
```

Rollback restores the previous panel, the exact earlier CLI presence and
version, retained lifecycle installer, doctor, fixed Nginx lifecycle assets,
and the pre-upgrade database snapshot. State written after the upgrade is
therefore not retained. The tool also creates a pre-rollback recovery snapshot
before restoring the older state.

Panel and managed-agent lifecycle tools retain three pre-upgrade recovery
snapshots by default. Set `HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT` to an
integer from `1` through `100` in the corresponding protected environment file
to choose a different bounded window. Pruning never deletes the snapshot named
by `latest-pre-upgrade` and never treats pre-rollback snapshots as disposable
pre-upgrade history.

### Uninstall

```bash
sudo /usr/local/libexec/hserver-install uninstall
```

The default uninstall removes the service, panel executable, CLI executable,
persisted lifecycle installer, persisted doctor, and the fixed files under
`/usr/local/share/hserver/nginx-snippets`. It preserves unrelated files in the
parent directory, `/etc/hserver`, and `/var/lib/hserver`. Data or configuration
is deleted only when the operator explicitly supplies `--purge-data` or
`--purge-config`.

---

## 15. Back Up and Restore Panel State

The release archive includes guided panel-state backup and restore tools. A
source checkout exposes the same tools under `scripts/`. The portable artifact
contains a WAL-safe SQLite copy, a fixed format manifest, payload checksums, and
the exact protected notification channel files referenced by that copy.

Create a transactionally consistent backup, including when SQLite WAL mode is
active:

```bash
sudo ./backup-db.sh
# Source checkout: sudo ./scripts/backup-db.sh
```

Validate a backup without stopping or changing HServer:

```bash
sudo ./restore-db.sh validate /var/lib/hserver/backups/db/hserver-TIMESTAMP-ID.panel-backup.tar.gz
```

Restore only after validation and an explicit confirmation flag:

```bash
sudo ./restore-db.sh restore /var/lib/hserver/backups/db/hserver-TIMESTAMP-ID.panel-backup.tar.gz --confirm
```

The restore command does not return merely because systemd reports the service
as active. It waits for the local HServer health endpoint before declaring the
restore successful. Custom ports are read from `HSERVER_PORT`; advanced layouts
can override the probe with `HSERVER_RESTORE_HEALTH_URL`. If the health probe
does not succeed within `HSERVER_RESTORE_ACTIVE_TIMEOUT` seconds (default 20),
the complete pre-restore panel state is recovered automatically.

Restore creates a protected `.panel-backup.tar.gz` recovery bundle under
`/var/lib/hserver/restores`, stops the service only while replacing the database
and protected notification directory, and returns it to its previous active or
inactive state. If the restored state does not allow the service to become
healthy, both resources are rolled back automatically. Validation rejects
symlinks, duplicate or unexpected archive entries, unsupported archive types,
unsafe permissions, checksum mismatches, malformed JSON, missing or orphaned
notification files, and database/file-reference disagreement before mutation.

Legacy regular `.db` and `.db.gz` artifacts remain accepted for compatibility.
They restore SQLite only and intentionally leave the installed notification
secret directory unchanged. New backups always use the portable bundle format.
Set `HSERVER_DB_BACKUP_DIR`, `HSERVER_DB_RECOVERY_DIR`, or
`HSERVER_NOTIFICATION_SECRET_DIR` only for deliberate custom layouts; all three
must stay on protected local storage. `HSERVER_DB_BACKUP_RETENTION_DAYS`
defaults to `30`.

Stable version tags exercise this packaged flow on disposable Ubuntu 24.04
`amd64` and `arm64` runners before release publication. The acceptance gate
first authenticates with the installed `hserverctl` through a protected
password file, requires its token file to be mode `0600`, and parses read-only
host status and disk scan responses. It then proves an authenticated writable
local terminal by executing and observing a unique PTY marker, runs bounded
temporary-file cleanup, and requires the maintenance status lock to return
inactive. It creates and validates a live WAL-safe portable panel-state bundle,
changes persisted onboarding state through the authenticated API, restores the
bundle, checks the original state through the restarted panel, and requires the
pre-restore recovery bundle. The same tag gate also creates a bounded
files-root archive
through the authenticated backup API, waits for the asynchronous job, validates
the artifact as files-only, changes the source payload, restores it, and
compares its original SHA-256. The gate requires the preflight to report
`filesRollback=true`, then verifies that restore created a readable
`pre-restore-...-files.tar.gz` recovery archive, exposed it as a completed local
backup, and accepted it as a restorable files artifact. HServer automatically
uses that archive to restore overwritten paths if extraction fails and removes
paths created by the failed extraction. A successful restore keeps the recovery
archive so an operator can manually reverse overwritten file content after
application verification.

The public `Database Restore` CI matrix separately provisions isolated real
PostgreSQL and MariaDB servers on Ubuntu 24.04. For each engine it creates a
native dump, changes persisted data, restores the artifact, requires the
pre-restore recovery point, injects a partially mutating SQL failure, and proves
automatic rollback recovered the original state. Every pull request, protected
branch push, and version tag runs both engine jobs; tagged release publication
depends on their success. This core restore evidence does not replace a
candidate installation's connection, OS-role, network, and protected
credential-file checks.

PostgreSQL database backups carry an encoded restore target inside the SQL
stream. HServer rejects target metadata that disagrees with the artifact name,
passes the same configured role, host, port, and protected pgpass locator to
both dump and restore clients, and restores a single-database dump only into its
original database. Dumps include idempotent clean statements, so restore
replaces the backed-up schema state instead of silently mixing it with newer
tables. Before either a database-only or full-bundle restore mutates the target,
HServer automatically writes a `pre-restore-...sql.gz` recovery point to the
backup directory. A failed database stage, or a later failed files stage in a
full restore, triggers an automatic database rollback from that artifact. Keep
the recovery point until application verification passes. If `HSERVER_PG_PASSFILE`
is set, the file must be readable by `HSERVER_PG_RUN_AS` and should not be
group- or world-readable.

MariaDB and MySQL use the same recovery workflow. Set
`HSERVER_MYSQL_DEFAULTS_FILE` to a client option file containing the connection
settings needed by both `mariadb-dump`/`mysqldump` and `mariadb`/`mysql`.
HServer passes only that file path as the first client option; credentials stay
out of process arguments. The file should be owned by the HServer service
account and have mode `0600`.

The Backups page runs `GET /api/backups/restore/{id}/validate` before enabling
the restore confirmation. This preflight reads the complete artifact, validates
nested full-backup parts and target metadata, and shows the exact database and
file rollback boundary. It does not contact or mutate the target database;
connection and recovery-point creation are checked when the restore starts.

---

## Troubleshooting

### Service fails to start: "HSERVER_JWT_SECRET is not set"

```bash
# Verify the secret key is present without printing its value
sudo grep -q '^HSERVER_JWT_SECRET=.' /etc/hserver/hserver.env && echo present

# Generate a new one if missing
openssl rand -hex 32
```

### Port 3085 already in use

```bash
ss -tlnp | grep 3085
# Kill the conflicting process or change HSERVER_PORT
```

### Database locked / busy_timeout exceeded

Indicates multiple instances are running. Check:

```bash
systemctl status hserver
ps aux | grep hserver-panel
# If a stale process exists: kill -9 <PID>
```

### Frontend shows blank page / 404 on assets

The `cmd/hserver/web/dist/` directory was not populated before the Go
build. Run `make build` (not `go build` directly) to trigger `sync-dist`.

### nginx 502 Bad Gateway

The Go binary is not running or listening on the expected port:

```bash
systemctl status hserver
curl -s http://127.0.0.1:3085/api/health
```

### WebSocket terminal disconnects immediately

Check that the nginx `location /api/terminal/ws` block includes
`Upgrade` and `Connection` headers and has `proxy_read_timeout` set
to at least 3600s. Standard proxy timeouts (60s) will kill idle
terminal sessions.
