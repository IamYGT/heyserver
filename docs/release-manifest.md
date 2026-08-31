# Release Manifest Contract

Heyserver release discovery consumes a small provider-neutral JSON document. The
panel never infers a maintainer repository and remains explicitly
`not_configured` until an operator sets `HSERVER_UPDATE_MANIFEST_URL`.

The JSON schema remains independent from transport trust. Operators may set up
to eight comma-separated base64 Ed25519 public keys in
`HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS`. When at least one key is configured,
Heyserver downloads the same manifest URL with `.sig` appended to its path while
preserving the query string, decodes the base64 64-byte detached signature, and
accepts the exact manifest bytes only when one configured key verifies it. A
missing, malformed, oversized, or mismatched signature makes release discovery
`unavailable`; no artifact metadata is returned or staged. An empty trust set
leaves discovery in the explicit checksum-only state:
`signature_status=not_configured` may expose version and checksum metadata for
read-only inspection, but it never authorizes a mutation. Panel stage/install and
managed-agent upgrade are fail-closed and require `signature_status=verified`;
an empty trust set or any other signature status cannot create, install, or
upgrade a release stage.

## Schema v1

```json
{
  "schema_version": 1,
  "version": "v1.2.3",
  "published_at": "2026-08-26T18:00:00Z",
  "release_notes_url": "https://releases.example.com/hserver/v1.2.3",
  "artifacts": {
    "linux_amd64": {
      "url": "https://releases.example.com/hserver/v1.2.3/hserver-panel-v1.2.3-linux-amd64.tar.gz",
      "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "size_bytes": 12345678
    },
    "linux_arm64": {
      "url": "https://releases.example.com/hserver/v1.2.3/hserver-panel-v1.2.3-linux-arm64.tar.gz",
      "sha256": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
      "size_bytes": 12345678
    }
  }
}
```

- `schema_version` must be `1`.
- `version` must be a stable `major.minor.patch` release with an optional `v`
  prefix. Prerelease and commit-derived values are not ordered.
- `artifacts` must contain the running `GOOS_GOARCH` key. Public Heyserver
  packages currently support `linux_amd64` and `linux_arm64`.
- Each archive contains architecture-matched `hserver-panel`, `hserver-agent`,
  and `hserverctl` binaries; archive verification rejects a missing binary or
  an ELF architecture mismatch before publication.
- Artifact and release-note URLs must use HTTPS and cannot contain URL
  credentials, whitespace, control characters, or malformed ports.
- Every artifact requires a lowercase SHA-256 digest. `size_bytes` is optional
  but cannot be negative.
- Unknown JSON fields, trailing documents, oversized responses, invalid URLs,
  unsupported schema versions, and missing host artifacts make discovery
  `unavailable` rather than healthy.

The local `make release VERSION=v1.2.3` path and tagged CI packages use the
same stable-version validator as manifest generation. This prevents a package
that cannot participate in panel or agent update ordering from being published
as a supported release. Product maturity is independent of SemVer precedence:
`v0.x` remains a valid pre-1.0 product release without an `-rc` suffix.

## Generate the release asset

After both architecture archives and checksum files exist in one directory:

```bash
HSERVER_RELEASE_NOTES_URL=https://github.com/OWNER/REPOSITORY/releases/tag/v1.2.3 \
  ./scripts/generate-release-manifest.sh \
    v1.2.3 \
    https://github.com/OWNER/REPOSITORY/releases/download/v1.2.3 \
    ./dist \
    ./dist/release-manifest.json
```

The generator verifies each adjacent checksum against the archive before
writing the manifest atomically. Tagged CI also requires a matching
`## [major.minor.patch]` entry in `CHANGELOG.md`; the `Unreleased` section alone
does not satisfy the release-notes gate. The public GitHub Actions release job
runs the same command only after both package jobs and native lifecycle gates
pass.

Create a maintainer signing key once outside the repository, then sign the
generated manifest:

```bash
./scripts/generate-release-signing-key.sh \
  /secure/hserver-release-ed25519.pem \
  /secure/hserver-release-ed25519.pub

./scripts/sign-release-manifest.sh \
  ./dist/release-manifest.json \
  /secure/hserver-release-ed25519.pem \
  ./dist/release-manifest.json.sig
```

The private PEM remains outside Git and is stored as the protected
`HSERVER_RELEASE_SIGNING_KEY` secret in the release repository. The `.pub` file
contains only the base64 raw 32-byte public key used by panel and agent
configuration. Before signing an official tag, add that public key and its
lowercase raw-key SHA-256 fingerprint to `trust/release-signers.json` with
status `active`. The checked-in trust store is intentionally empty until the
operator selects the canonical signer; tagged staging fails closed without an
active entry or when the protected secret derives a different public key.

The same active key directly signs the exact `bootstrap-install.sh` bytes:

```bash
./scripts/sign-release-asset.sh \
  ./dist/bootstrap-install.sh \
  /secure/hserver-release-ed25519.pem \
  ./dist/bootstrap-install.sh.sig
```

Tagged releases publish both `release-manifest.json.sig` and
`bootstrap-install.sh.sig`. The public wrapper verifies the downloaded raw key
against its embedded or explicitly supplied fingerprint and verifies the
bootstrap signature before `chmod`, `sudo`, or execution.

For key rotation, retain the old signer as `active`, add the replacement as
`next`, and stage wrappers with both fingerprints while releases remain signed
by the old key. Promote the replacement to `active` only after an old-key-signed
release has distributed the expanded panel and agent trust set. Remove the old
entry only after the supported adoption window. A `next` key may verify a
staged public wrapper during overlap, but tagged CI signing requires `active`.
Duplicate, mismatched, malformed, or more than eight signers fail closed.

Operators should configure a stable URL that resolves to the newest manifest.
For GitHub Releases this can be:

```text
https://github.com/OWNER/REPOSITORY/releases/latest/download/release-manifest.json
```

Tagged public releases also derive and publish `release-public-key.b64` from the
protected signing key, `bootstrap-install.sh.sig`, plus adjacent SHA-256 files
for the key and bootstrap. They publish the canonical `public-install.sh`
wrapper and `public-install.sh.sha256` in the same top-level release directory.
The wrapper pair is deliberately outside the schema-v1 manifest: it is a
trust-bootstrap entry point, not a host architecture artifact. Installation
examples obtain the installer digest and signer fingerprint independently of
the mutable release directory. The wrapper treats adjacent checksums as
corruption detection, pins the downloaded key by raw-key fingerprint, verifies
the bootstrap's detached signature, and passes that key through
`--public-key-file`.
For a fresh native installation, the verified bootstrap may also receive
`--vhosts-root ABSOLUTE_PATH` and forwards it unchanged to the packaged
lifecycle installer. The option is never inferred from the release manifest;
omitting it preserves the provider-neutral `not_configured` state.
The bootstrap requires this signed manifest and at least one configured
Ed25519 public key; it does not offer checksum-only installation.
After selecting the host artifact, it verifies the signature, archive metadata,
bounded extraction contract, packaged version, and ELF architectures before
calling the packaged native lifecycle. The exact verified feed URL and
normalized trust set are persisted for later explicit panel updates.

The bootstrap also has an explicit `--agent-only` bridge for a host where a
managed agent is already installed. It uses the same manifest signature and
trust set, architecture selection, declared size and SHA-256, safe tar
validation, packaged `VERSION`, and ELF checks, but validates the agent-only
package contract (`hserver-agent`, `agent-install.sh`, and `VERSION`) rather
than entering the panel lifecycle. The verified package's
`agent-install.sh upgrade --binary <verified hserver-agent>` is the only
lifecycle command run. The existing agent configuration, custom token path and
file, service enabled/active state, and lifecycle rollback boundary remain in
place; `--vhosts-root` is rejected. This mode never accepts, reads, or prints
a hub token, private signing key, or credential and does not rewrite or
auto-enable update policy in the existing configuration. Operators add local
update policy separately when required.

Discovery itself remains read-only, including checksum-only discovery. An
archive link and admin-only **Stage & verify** control are shown only when the
manifest version is newer than the running stable release. The panel's stage
and install paths, and the managed-agent upgrade path, require
`signature_status=verified`; checksum-only, `not_configured`, and
`unavailable` results stay read-only and fail closed before archive download or
lifecycle scheduling. No automatic or unattended updater is provided or
promised: every update mutation requires an explicit operator action. Heyserver
never performs a silent download, install, restart, or upgrade.

Managed agents may consume the same schema-v1 manifest only when their own
`HSERVER_AGENT_UPDATE_MANIFEST_URL` and update capabilities are enabled. The
`HSERVER_AGENT_UPDATE_MANIFEST_PUBLIC_KEYS` trust set is required for mutation;
an empty set may expose checksum-only discovery but never authorizes an agent
upgrade. When keys are configured, the trust set applies the same
detached-signature requirement locally. The
central panel sends an exact stable version, never artifact metadata. The agent
re-fetches its local manifest, verifies the complete archive SHA-256 and size,
validates the package root and ELF architecture, and extracts only
`hserver-agent`, `agent-install.sh`, and `VERSION` into its protected state
directory. Upgrade and rollback run through a delayed transient systemd unit so
the task receipt can reach the hub before the agent service restarts.

## Verified panel upgrade flow

Every panel stage or install mutation is accepted only for a release result with
`signature_status=verified`. A checksum-only or otherwise unverified result may
be displayed for discovery, but it cannot create a stage, install a stage, or
schedule the lifecycle unit.

On a native installation, an admin can explicitly stage the discovered release
from **About → Release updates**. The panel fetches the manifest again instead
of trusting browser-supplied artifact metadata, then downloads the host artifact
under the installation data directory. Before publishing the stage it verifies:

1. the declared archive size when `size_bytes` is present;
2. the complete lowercase SHA-256 from the manifest;
3. archive expansion and per-entry size limits;
4. canonical paths beneath the exact release package root, with no links or
   unsupported entry types;
5. the packaged `VERSION`;
6. the panel and `hserverctl` ELF architectures against the running platform;
   and
7. the complete installation-owned Nginx snippet set required by the packaged
   lifecycle installer.

The compressed archive is discarded after successful extraction because the
installer uses only the individually hashed staged files. Heyserver retains the
current stage and the newest previous inactive stage; `scheduled` and `running`
stages are never removed by retention. Incomplete temporary staging directories
older than 24 hours are removed on the next successful stage. Unknown files or
directories beneath the update data directory are left untouched.

Staging does not stop or restart the panel. Installation stays disabled until
the admin accepts the restart and rollback notice, then confirms the exact
staged ID and version in a second request. Heyserver rechecks the individually
hashed staged panel, CLI, installer, doctor, and fixed upgrade runner before scheduling a separate
transient systemd unit. The delayed unit survives the panel process being
stopped by the packaged installer.
The detached unit receives fixed server-resolved panel, CLI, and data paths;
the install API never accepts destination paths. Explicit
`HSERVER_UPDATE_PANEL_BINARY_PATH` and `HSERVER_UPDATE_CLI_BINARY_PATH` values
win over the observed running executable and its sibling CLI. The canonical
fallback remains `/usr/local/bin/hserver-panel` plus
`/usr/local/bin/hserverctl`. Before the stage status becomes `scheduled`, both
destinations must be canonical absolute regular executables with the expected
basename, no symlink component, no overlap with the data directory, and no
unsafe executable root.
Every selected Nginx snippet is extracted into the protected stage, hashed in
the stage record, and revalidated immediately before scheduling, just like the
panel, CLI, installer, doctor, and detached runner. This keeps verified
self-update independent of the originally downloaded archive while still
providing the complete lifecycle package required to preserve managed Nginx
configuration.
The packaged installer also persists that exact fixed set under
`/usr/local/share/hserver/nginx-snippets`. Native upgrade snapshots include the
retained set, so the installed lifecycle tool remains usable without the
compressed archive and rollback restores the lifecycle version that belongs to
the previous panel release.

The canonical `/usr/local` lifecycle continues to manage its version-matched
installer, doctor, Nginx assets, systemd unit, and SQLite recovery snapshot.
When either installed executable uses a noncanonical path, preserve-layout mode
instead snapshots the exact binary pair, effective systemd unit/drop-in text,
and enabled/active state, then atomically replaces only those two binaries. It
does not rewrite or migrate the existing unit, drop-ins, `EnvironmentFile`,
`WorkingDirectory`, data directory, or database. It also does not restore a
database it did not mutate. A WAL-consistent SQLite backup is therefore a
required operator pre-rollout step for a noncanonical installation.

The durable stage states are `staged`, `scheduled`, `running`, `completed`, and
`failed`. Every response also includes a stable `status_detail` suitable for the
operator UI. Completion means the packaged installer installed the matched
panel and CLI, restarted an active panel, its health endpoint passed, and both
installed executables reported the exact complete version/build identity of the
verified staged binaries. A deliberately inactive preserve-layout service keeps
its inactive and enabled/disabled state rather than being started implicitly.
If either installed release identity differs, the detached
runner invokes the packaged rollback even when the health endpoint passed and
records `failed`. A failed health check or identity check restores the
pre-upgrade panel, exact earlier CLI state, and previous enabled/active service
state before reporting failure. Canonical mode also restores its lifecycle
installer, doctor, fixed Nginx lifecycle assets, systemd unit, and SQLite
snapshot; preserve-layout mode leaves the existing unit/env/data/DB layout
untouched. A rollback failure remains a terminal `failed`
receipt with an explicit lifecycle-journal diagnostic rather than being
presented as a successful installation.

After the panel reconnects, it compares any persisted `scheduled` or `running`
state with `hserver-panel-upgrade.timer` and
`hserver-panel-upgrade.service`. If systemd conclusively reports that neither is
active before the runner wrote a terminal state, Heyserver records `failed` with
an interrupted-operation detail instead of polling forever. If systemd itself
cannot be inspected, the persisted state is preserved rather than guessed. A
`failed` state should be investigated with `journalctl -u hserver` and
`journalctl -u hserver-panel-upgrade` before retrying.
