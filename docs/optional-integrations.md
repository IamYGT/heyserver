# Optional Integrations

HServer's core panel does not require a hosted provider. Integrations are
enabled only through installation-owned configuration and must expose their
state as `not configured`, `unavailable`, or `healthy`.

[`extensions/catalog.json`](../extensions/catalog.json) is the authoritative
machine-readable companion to this human-readable matrix; its shape is defined
by [`extensions/catalog.schema.json`](../extensions/catalog.schema.json).
Every row is linked to one catalog entry by its `optional-integrations:v1:*`
marker. A new integration must update the catalog entry, this row, and its
focused test together.

| Integration | Purpose | Core requirement | Configuration boundary |
| --- | --- | --- | --- |
| Cloudflare | DNS and optional mail-DNS reconciliation | Optional | Scoped API token and explicit origin/settings <!-- optional-integrations:v1:cloudflare --> |
| Stalwart | Mail domains, accounts, aliases, and health | Optional | Local or reachable management API credentials <!-- optional-integrations:v1:stalwart --> |
| Mail access | External webmail plus IMAP/SMTP client guidance | Optional | Complete validated webmail/admin URLs, mail hostname, and ports saved in installation settings <!-- optional-integrations:v1:mail-access --> |
| Google Drive through rclone | Offsite backup upload and restore | Optional | OAuth client, protected token file, and local rclone <!-- optional-integrations:v1:google-drive-through-rclone --> |
| restic snapshots | Client-side encrypted incremental full-server snapshots | Optional | Local restic, explicit Google Drive or S3-compatible destination, and installation-owned repository password <!-- optional-integrations:v1:restic-snapshots --> |
| PM2 | Native Node.js process management | Optional | Explicit Unix user, PM2 home, and executable <!-- optional-integrations:v1:pm2 --> |
| nginx | Local virtual hosts and reverse proxies | Feature-specific | Detected local service and installation-owned paths <!-- optional-integrations:v1:nginx --> |
| PHP-FPM | Pool and runtime management | Feature-specific | Detected local versions, sockets, and config roots <!-- optional-integrations:v1:php-fpm --> |
| UFW | Local firewall rules and lifecycle | Feature-specific | Distribution-supported local UFW client; iptables fallback is observation-only <!-- optional-integrations:v1:ufw --> |
| Certbot | Let's Encrypt inventory, issuance, and renewal | Feature-specific | Local client, observed authenticator plugins, and optional protected DNS credential file <!-- optional-integrations:v1:certbot --> |
| BIND 9 | Local authoritative DNS zones | Feature-specific | Distribution-provided server, configuration, validation tools, and reload client <!-- optional-integrations:v1:bind-9 --> |
| PostgreSQL and MariaDB | Database inventory, sessions, and backups | Feature-specific | Local clients and separately configured credentials <!-- optional-integrations:v1:postgresql-and-mariadb --> |
| Docker | Container inventory and fixed lifecycle actions | Feature-specific | Local Docker socket; never mounted by the evaluation container <!-- optional-integrations:v1:docker --> |
| smartmontools | Physical root-disk SMART health | Optional | Observed root filesystem ancestry and a readable single physical block device <!-- optional-integrations:v1:smartmontools --> |
| Notification delivery | Email, Telegram, Discord, and Slack alert channels | Optional | Write-only provider credentials stored in protected installation-owned channel files <!-- optional-integrations:v1:notification-delivery --> |

## Notification delivery receipt contract

Notification channel health is based on the latest bounded delivery receipt,
not on a configured URL, installed provider, or redacted channel config. Each
channel configuration update advances its internal `config_revision`; a
receipt is accepted only when it matches that revision and is no more than
seven days old. The channel detail values are:

| Channel detail | Canonical state | Meaning and next action |
| --- | --- | --- |
| `delivery_confirmed` | `healthy` | The provider call succeeded and its receipt is current for the channel configuration. |
| `delivery_failed` | `unavailable` | The latest provider call failed; check the channel/provider settings and send a new test. |
| `delivery_stale` | `unavailable` | The last successful receipt is older than seven days; send a new test to refresh it. |
| `probe_unverified` | `unavailable` | No receipt exists for the current configuration, or the stored receipt belongs to another revision; send a new test. |

The aggregate is strict: any configured channel that is disabled, unavailable,
failed, stale, or unverified keeps notification delivery `unavailable`.
Healthy is reported only when every configured channel has a current
`delivery_confirmed` receipt. A manual test becomes healthy only if the
provider call succeeds **and the delivery receipt is persisted**; a sent test
whose receipt cannot be stored remains unavailable. Alert, uptime, and backup
notifications also persist only bounded success/failure outcomes after the
provider call. Receipts never store a subject, body, provider error, secret,
or raw provider payload.

## Design rules

1. A provider URL or installed executable is not proof of health.
2. Credentials remain in protected environment or secret files and are never
   returned by status APIs.
3. Provider failures must not prevent unrelated core pages from loading.
4. Remote-node support requires a matching agent capability; the hub cannot use
   its own provider or host configuration for a selected node.
5. Examples use placeholder identifiers and cannot embed production inventory.
6. Removing an optional provider must leave local monitoring, authentication,
   audit, and lifecycle operations usable.

## Operator recovery contract

An unavailable integration must explain the next operator action without
silently installing packages, changing services, or substituting a maintainer's
provider configuration.

Docker, PM2, nginx, PHP-FPM, UFW, Certbot, BIND, PostgreSQL, MariaDB,
Cloudflare, Stalwart, Mail access, root-disk SMART health, and the
rclone/restic-backed backup flows use the shared contextual-remediation surface:

| Observed state | HServer behavior | Operator guidance |
| --- | --- | --- |
| Docker not installed | Container inventory and mutations stay disabled | Install Docker Engine using packages supported by the host distribution, enable `docker.service`, then retry detection |
| Docker installed but stopped | Images and containers are not described as empty | Inspect and start `docker.service`, then retry detection |
| Docker status unavailable | The panel preserves the unknown state | Run the packaged installation doctor, verify `docker version` and service/socket access, then retry |
| PM2 not configured | No root-owned PM2 daemon is created | Configure `HSERVER_PM2_USER`, `HSERVER_PM2_HOME`, and `HSERVER_PM2_BIN` for an unprivileged application owner |
| PM2 inventory unavailable | PM2 mutations stay disabled | Verify the configured owner, binary, home, and `pm2-APP_USER` service, then retry |
| nginx not installed | Test, reload, and config mutations stay disabled | Install nginx from the supported Ubuntu repositories, verify `nginx -v`, enable the unit, then retry |
| nginx installed but inactive | Config inspection and syntax testing remain available; reload stays disabled | Inspect the unit and journal, run `nginx -t`, start `nginx.service`, then retry |
| nginx state unavailable | Nginx mutations stay disabled | Verify local systemd access and the nginx unit, inspect HServer logs, then retry |
| PHP-FPM not installed | Version and pool controls stay hidden | Install a supported `php<VERSION>-fpm` package, verify `/etc/php/<VERSION>/fpm`, enable the matching unit, then retry |
| PHP-FPM inventory unavailable | Version and pool controls stay paused | Run the packaged doctor, verify configuration access and the matching systemd unit, inspect logs, then retry |
| UFW not installed | Firewall mutations and UFW policy claims stay disabled; observable iptables rules remain read-only | Install `ufw` from the supported Ubuntu repositories, preserve remote-management access, verify `ufw status verbose`, then retry |
| UFW status unavailable | Rules and mutations stay paused | Run the packaged doctor, verify UFW command access with the HServer service identity, inspect logs, then retry |
| Certbot missing | Certificate files remain observable; issuance and renewal stay disabled | Install `certbot` and a supported authenticator from the host distribution, verify `certbot --version` and `certbot plugins`, then retry |
| Certbot plugin inventory unavailable | Existing renewal remains available; new issuance stays disabled | Run `certbot plugins` with the HServer service identity, inspect logs, then retry |
| No supported Certbot authenticator | Renewal remains available; issuance stays disabled | Install `python3-certbot-nginx` or `python3-certbot-dns-cloudflare`, then retry |
| DNS-01 credentials missing or unreadable | HTTP-01 remains independent; DNS issuance stays disabled | Set `HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS` to a protected absolute mode-`0600` INI file, restart HServer, then retry |
| BIND not installed | Local zone inventory is not described as an empty configured server; mutations stay disabled | Install `bind9` and `bind9-utils` from supported Ubuntu repositories, verify `named -v`, then retry |
| BIND management setup incomplete | Existing readable files remain untouched; mutations stay disabled | Verify `/etc/bind/named.conf.local`, `named-checkconf`, `named-checkzone`, and `rndc`, then retry |
| BIND stopped | Existing zones remain readable; changes and reload stay disabled | Inspect the `bind9` or `named` unit and journal, run `named-checkconf -z`, start the selected unit explicitly, then retry |
| BIND state unavailable | Mutations stay disabled and the diagnostic remains visible | Verify version and systemd visibility with the HServer service identity, inspect logs, then retry |
| PostgreSQL or MariaDB client missing | Inventory and database mutations stay disabled | Install the distribution-supported `postgresql-client` or `mariadb-client`, verify its version and local connection, then retry |
| PostgreSQL or MariaDB stopped | A failed connection is not described as an empty inventory | Inspect the matching systemd unit and journal, verify `pg_isready` or the local MariaDB socket after starting it, then retry |
| Database management authentication failed | Inventory and mutations stay disabled while the original error remains visible | Verify the installation-owned PostgreSQL OS identity or MariaDB root/socket policy without printing credentials, then retry |
| Database inventory unavailable | Source counts remain unknown | Run the packaged doctor, verify the client, service, socket, authentication policy, and HServer logs, then retry |
| Cloudflare not configured | Zone controls stay disabled | Create a least-privileged token, set `HSERVER_CF_API_TOKEN` in the protected environment, restart HServer, then retry |
| Cloudflare unavailable | Provider failures are not described as an empty zone list | Verify outbound HTTPS/DNS and token scope without printing the token, inspect logs, then retry |
| Stalwart state unavailable | Mail lifecycle controls stay disabled | Verify `HSERVER_STALWART_SERVICE`, `HSERVER_STALWART_CONFIG_PATH`, and `HSERVER_STALWART_BIN`, inspect the configured unit, then retry |
| Mail access not configured | Webmail links and partial IMAP/SMTP guidance stay hidden | Save complete validated webmail/admin URLs, mail hostname, and ports in **Settings → Mail Access**, then retry detection |
| Mail settings unavailable | HServer does not substitute defaults or claim the provider is absent | Verify the panel API and settings source, inspect the service log, then retry detection |
| Notification channels not configured | Alert rules remain usable without an external destination | Add one channel, save its write-only credential, send a test notification, and confirm the receipt persists before assigning it to rules |
| Notification protected store unavailable | Channel inventory and mutations stay paused; existing delivery state remains unknown | Verify `${HSERVER_DATA_DIR}/notification-channel-secrets` is an installation-owned mode-`0700` directory containing mode-`0600` regular files, then restart HServer and retry |
| Notification `delivery_failed` | The channel remains unavailable after the provider call fails | Check the channel/provider settings, then send a new test notification |
| Notification `delivery_stale` | The channel remains unavailable after its successful receipt exceeds seven days | Send a new test notification to refresh the receipt |
| Notification `probe_unverified` | The channel remains unavailable when no receipt matches its current configuration revision | Send a new test notification and confirm that its receipt is stored |
| Root disk is virtual, non-block, or spans multiple disks | No physical disk is chosen and no healthy state is claimed | Use the hypervisor, provider, RAID, or controller health source appropriate to the observed storage |
| Root SMART observation unavailable | The last state is not replaced with `PASSED` and the diagnostic remains visible | Install `smartmontools` on physical hosts, verify `df`, `lsblk`, and `smartctl` visibility for the HServer service, then retry |
| Disk inventory unavailable | Mounted storage and capacity remain unknown instead of appearing empty or zero-sized | Verify `lsblk`, `df`, and `/proc/diskstats` visibility for the HServer service, inspect the panel log, then retry detection |
| Deep-analysis status unavailable | A new persistent scan stays disabled because the current worker state is unknown | Verify panel API health, local systemd visibility, and the analysis worker, then retry status detection |
| Directory listing unavailable | The selected path is not described as an empty directory | Confirm the path still exists and the HServer service identity can read it, inspect logs, then retry |
| `/etc/fstab` inventory unavailable | The host is not described as having no configured mount entries | Verify the HServer service identity can read `/etc/fstab`, inspect logs, then retry |
| File Manager roots unavailable | Browsing and every file mutation stay paused because the allowed-root boundary is unknown | Verify panel API health and the installation-owned virtual-host root, inspect logs, then retry root detection |
| File Manager directory or file read unavailable | The path is not described as empty and create, rename, delete, or save remains disabled as applicable | Confirm the path is inside an allowed root and readable by the HServer service identity, inspect logs, then retry the exact observation |
| rclone missing | Google OAuth connection stays disabled | Install rclone from the host distribution, verify `rclone version` for the HServer service identity, restart HServer, then retry |
| Google Drive status unavailable | Connection and upload controls stay paused | Verify HServer API health, service logs, and rclone visibility, then retry |
| restic missing | Snapshot and schedule actions stay disabled | Install restic from the host distribution or set `HSERVER_RESTIC_BIN` to the installation-owned absolute binary path, then retry |
| restic password missing | Snapshot and schedule actions stay disabled | Generate a unique password, store it outside the HServer host, set `HSERVER_RESTIC_PASSWORD` in the protected environment, then retry |
| S3 snapshot destination not configured | S3 snapshot actions stay disabled without affecting Google Drive or local backups | Set the endpoint, bucket, optional region, and two protected credential-file references, restart HServer, then retry |
| S3 snapshot destination unavailable | HServer does not describe the destination as healthy from a URL alone | Verify HTTPS reachability, bucket access, credential-file ownership and mode `0600`, then force a fresh snapshot status probe |
| Snapshot status unavailable | Snapshot readiness remains unknown | Verify HServer API health, restic visibility, the selected destination, and service logs, then retry |

The retry button performs observation only. It never installs a dependency,
starts a service, opens OAuth, or rewrites protected configuration. Remaining
optional integration pages should adopt the same component and state contract
as their provider-specific recovery instructions are defined.

## Adding an integration

The complete contribution and compatibility contract is
[Extension Boundary v1](extension-boundary.md). HServer v1 integrations are
reviewed in-tree source contributions; the panel does not load arbitrary
runtime plugins or executable hooks.

A contribution should define:

- the operator problem and whether it targets the local host or managed nodes;
- required configuration keys and their protected storage location;
- status semantics for unconfigured, unavailable, and healthy states;
- bounded API and agent capabilities;
- timeout, retry, audit, and rollback behavior;
- contextual remediation for not-configured, stopped, and unavailable states;
- one focused test for absence and one for the configured path;
- installation and troubleshooting documentation.

Provider-specific business logic belongs behind its own package or adapter. Do
not spread provider assumptions through the generic domain, backup, deployment,
or monitoring services.
