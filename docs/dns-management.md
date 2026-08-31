# DNS Management Guide

## Overview

Heyserver exposes two independent DNS backends. `/dns` manages a BIND 9 server
on the same host as the native panel; `/cloudflare` manages an optional remote
Cloudflare account. Neither backend is required by the core panel, and Heyserver
does not copy or synchronise zones between them.

| Backend | Scope | Service layer |
| --- | --- | --- |
| BIND 9 | Local authoritative zones | `internal/services/bind/` |
| Cloudflare | Zones visible to an installation-owned token | `internal/services/cloudflare/` |

## BIND installation boundary

Heyserver observes the distribution-provided BIND installation. Detection never
installs packages, starts a unit, creates a zone, or replaces configuration.

| Requirement | Default |
| --- | --- |
| Server executable | `named` |
| Zone declarations | `/etc/bind/named.conf.local` |
| Heyserver-created zone files | `/etc/bind/zones/db.<domain>` |
| Configuration checker | `named-checkconf` |
| Zone checker | `named-checkzone` |
| Runtime reload client | `rndc` |
| Observed systemd units | `named`, then `bind9` |

`GET /api/dns/status` distinguishes these states:

| State | Meaning | Panel behavior |
| --- | --- | --- |
| `healthy` | Binary, version, configuration, validation tools, reload client, and running process are observed | Zone mutations and reload are enabled |
| `not-installed` | `named` is not in the service `PATH` | Inventory is not presented as an empty configured server; mutations stay disabled |
| `not-configured` | The local config file or required tools are missing | Existing readable data is preserved; mutations stay disabled |
| `stopped` | Installation and tools are ready but the `named` process is not running | Existing zones remain readable; mutations and reload stay disabled |
| `unavailable` | Version/process state cannot be observed reliably, or an interrupted lifecycle transaction still needs recovery | Mutations stay disabled and the underlying diagnostic remains visible |

The `zoneManagementReady` boolean is the authoritative action gate. All BIND
write methods enforce the same readiness check on the server, so a custom API
client cannot bypass a disabled browser control.

## Zone and record operations

The zone inventory parses `zone` declarations from `named.conf.local`, then
adds the current SOA serial and record count when each referenced file is
readable. A missing `named.conf.local` is an unavailable inventory, not a
successful zero-zone response.

Supported operations include:

- create and delete a local master zone;
- read or export the raw zone file;
- add, update, and delete A, AAAA, CNAME, MX, TXT, NS, SRV, and CAA records;
- read and update SOA fields while incrementing the serial;
- run `named-checkconf -z` and per-zone `named-checkzone` checks;
- explicitly run `rndc reload` after reviewed changes.

Record requests also support an `autoReload` flag for callers that want a
zone-specific `rndc reload <domain>` after the file change. Callers that omit
it must use the explicit reload action. A successful file write and a
successful runtime reload are therefore separate results.

Mutation payloads use a strict shared API, service, and CLI validation
contract. Unknown or trailing JSON is rejected. Zone identities normalize to
lower case without a trailing root dot; record types normalize to uppercase;
record owner names accept `@`, wildcard records, and underscore service
labels. TTL is a decimal value from `0–2147483647`, priority is `0–65535`, and
record values cannot contain control characters. A and AAAA values are also
checked as their respective address families. SOA updates require all six
fields and bound every timer to `0–2147483647` seconds.

Record deletion accepts either a strict JSON identity body or the legacy exact
`name`, `type`, `value`, and optional `autoReload` query contract used by the
panel. It rejects unknown or repeated query fields and never merges body and
query sources. Zone deletion, configuration check, and global reload accept an
empty request body only.

### Validated file transaction

SOA and record changes use a bounded local transaction:

1. read the original regular file and capture its content, mode, and owner;
2. write and `fsync` a same-directory temporary candidate;
3. run `named-checkzone` against the candidate before it can become live;
4. atomically rename the validated candidate over the original and sync the
   directory;
5. when reload was requested, run `rndc reload <domain>`;
6. if reload fails, atomically restore the original file and run one rollback
   reload so disk and runtime return to the previous version.

The API error distinguishes a validation failure, disk rollback failure, and a
restored file whose rollback reload also failed. The UI preserves that exact
diagnostic instead of replacing it with a generic failure message.

Zone creation and deletion use a separate two-file transaction because they
also change `named.conf.local`:

- creation validates both staged files, refuses to overwrite an existing zone
  path, commits the zone and configuration, then performs a global reload;
- deletion validates the staged configuration, atomically moves the zone to a
  hidden tombstone, commits the configuration, reloads BIND, and removes the
  tombstone only after success;
- a commit, reload, or cleanup failure restores both the previous configuration
  and zone file, followed by one rollback reload when runtime state changed.

Heyserver serializes local BIND mutations so two panel requests cannot overwrite
each other's snapshots. Zone create/delete also writes a durable versioned
journal before the first file mutation. The protected journal lives at
`${HSERVER_DATA_DIR}/bind/lifecycle-transaction.json`; its directory is mode
`0700`, the file is mode `0600`, and its embedded pre-change snapshots can
contain the original BIND configuration. Keep the whole data directory private
and include it in installation backups.

On startup, Heyserver examines that journal before the HTTP router starts:

- a transaction already marked `reloaded` is finalized without undoing the
  applied change;
- every earlier stage is conservatively rolled back to the stored config and
  zone snapshots, followed by one global BIND reload;
- the journal is cleared only after recovery succeeds;
- invalid journal data, a failed file restore, or a failed rollback reload
  keeps `recoveryPending=true`, changes readiness to `unavailable`, and blocks
  every BIND mutation.

Do not delete or edit a pending journal manually. Repair BIND validation or
`rndc reload`, then restart Heyserver so the same recovery pass can finish.

## SOA serials

The SOA editor exposes primary nameserver, responsible mailbox, refresh, retry,
expire, and minimum TTL. Heyserver increments an existing date-style serial when
possible; otherwise it uses a monotonically increasing Unix-style value.

## Propagation lookup

The lookup endpoint queries Google (`8.8.8.8`), Cloudflare (`1.1.1.1`), and the
host's system resolver independently. It is an observation tool and does not
prove delegation ownership or change either DNS backend.

The exact query fields are required `domain` and optional `type`, which defaults
to `A`. Unknown, repeated, empty, or invalid fields fail before a resolver is
called. Configuration validation is also diagnostic data: `POST /api/dns/check`
returns HTTP 200 with `ok=false` plus complete global and per-zone output when
BIND rejects the configuration.

## Scriptable local DNS operations

`hserverctl dns` exposes local readiness, zones, records, SOA, lookup, check,
and raw export. Confirmed commands create/delete zones, add/update/delete exact
records, partially edit SOA by first reading the full current value, and reload
BIND. Raw exports can go to stdout or a new mode-`0600` file; an existing output
path is never replaced. See [CLI Guide](cli.md#manage-local-bind-dns-from-scripts)
for complete command examples.

## Cloudflare boundary

Cloudflare is optional and uses the scoped token configured by the
installation. The provider page lists only zones returned for that token and
supports provider-backed record CRUD, eligible proxy toggles, and explicit
cache purge actions. Provider failures never change the local BIND state and
must not be presented as an empty successful inventory.

Keep provider credentials outside Git. Public examples and tests must use
reserved example domains and addresses rather than maintainer or operator
inventory.
