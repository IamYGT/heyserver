# Public Launch Checklist

This checklist separates a clean public source snapshot from the private
operational history that preceded HServer's community-project conversion.

## Current source-head progress

This status is source progress only, not release evidence. The managed-node
live-metrics source path is complete for the capability-scoped `metrics.read`
agent task, hub/API validation, and current CLI/web consumers, including
guest-time-safe CPU accounting. The TUI consumer and OpenAPI contract revision
71 (443 routes, 321 schemas) are complete, and combined metrics acceptance is
complete. Canonical public repository selection and publication are complete at
[`IamYGT/heyserver`](https://github.com/IamYGT/heyserver), initially published
at `53df8ba`, with initial private/public tree parity at `3719df8`. Immutable
public source commit `adaccb23adf9720141d721970590de3a82fd17b5` produced the
first fully green public main CI matrix run `#33283277809`. Public
[PR #13](https://github.com/IamYGT/heyserver/pull/13) has head
`1fcb2319160696e21fb25cd6b52c21f69cf0e3ee`; its CI run `#33295933393`
completed with 14/14 checks successful. It merged at `2026-08-30T06:07:31Z`
as protected `main` commit
`bf67c80c9b383d28a4eefd1d75a014b41bd00f45` with exact tree
`bb838be28a9cb23897e943250e1c69ce9e71d138`. The prior protected-main run
`#33294999297` failed only its `EXIT cleanup` race, caused by Git 2.55
detached auto-maintenance; private fix
`aa10cb7b08fa98695ebdea7b95bfb2fb3a4835bd` uses command-scoped
`maintenance.auto=false`. Protected-main CI run `#33296189213`
completed/success with 14/14 checks, including the exact cleanup test and
successful `Release Package (amd64)`/`Release Package (arm64)` jobs.
Branch protection was enabled after these runs and is currently active. The
initial active signer is
prepared in private commit `df0a5070`, and the `HSERVER_RELEASE_SIGNING_KEY`
Actions secret is configured; public signer PR #7 was merged at protected `main`
commit `b2af1591` on 2026-08-30. Public tag `v0.9.5` points to protected
`main` commit
`b2af1591f7a848acd71bbe54bc4f70fbffe99373`, but tagged run `#33285788628`
failed all four lifecycle jobs (both `Native Lifecycle` jobs and both
`Managed Agent Lifecycle` jobs) because their fixtures posted onboarding step 6
while the canonical maximum is step 5. The tag and run remain
historical failed-release evidence; no successful `v0.9.5` GitHub Release is
claimed. The public `v0.9.6` tag points to protected `main` commit
`8d19991e7db7aa8ee39564a41bf1e6ef649d8d96`, but tagged workflow
`#33288698005` is not releasable: `Public Source Snapshot (amd64)` failed
asynchronous backup temporary-directory cleanup, and both `Native Lifecycle`
jobs failed because retained rollback did not preserve SQLite onboarding state.
Its managed-agent lifecycle jobs had not completed at audit time, and no
successful `v0.9.6` GitHub Release exists. The public `v0.9.7` tag points to
protected public `main` commit
`5c5bcf053b333f657ef011078f32ed54126946e6`, but tagged workflow
`#33291179584` is immutable disqualified evidence: both `Native Lifecycle` jobs
returned file-backup HTTP 400 because the fixture lacked an explicit temporary
vhosts root. The `Managed Agent Lifecycle` jobs were cancelled; their arm64
runner log preserved `agent release archive contains an invalid path`. The
workflow reached terminal `completed/cancelled` at
`2026-08-30T04:27:26Z` after normal cancellation followed by a force-cancel
because runner finalization stalled. No successful `v0.9.7` GitHub Release
exists.
The public `v0.9.8` tag object is
`81d3095f4c6f0615bd6912225bf18c38788e6e6c` and resolves to public `main`
commit `6f399eba2d4871d783dfff45a6d4ece276865d9a`. Tagged workflow
`#33293250350` is immutable disqualified evidence. The initial signed install,
retained upgrade, and rollback stages passed before the `Native Lifecycle`
amd64 job `#99208960092` and arm64 job `#99208960118` failed because
upgrade-feed signature rotation was missing, with
`Refusing to overwrite ... release-manifest.json.sig`. The `Managed Agent
Lifecycle` jobs `#99208960112` and `#99208960115` were cancelled. The workflow
reached terminal `completed/cancelled` at `2026-08-30T05:20:10Z` after normal
cancellation at `05:15 UTC` followed by a force-cancel accepted with HTTP 202
at `05:17:32 UTC` because runner finalization stalled. No `v0.9.8` GitHub
Release exists. Its immutable tag and
workflow are disqualified evidence; do not delete or reuse `v0.9.8`. The next
immutable replacement candidate is `v0.9.9`.
Source fix `c97ced79` preserves the signer fail-closed contract, explicitly
rotates the native upgrade-feed signature, and locks the static gate call
ordering that disqualified `v0.9.8`.
`v0.9.9` remains pending acceptance; its tag, GitHub Release, clean-host
acceptance, and live rollout are not claimed. Public PR #13 and the green
protected-main CI are source/merge evidence only. The live `v0.9.3` rollout is
not the current source; source or release evidence does not imply live
deployment.

Historical CI failures `#33251833442`, `#33281342435`, and tagged runs
`#33285788628`, `#33291179584`, and `#33293250350` are retained only as
historical evidence.

## 1. Export the audited tree

Do not make an old operational repository public merely because its current
working tree looks clean. Deleted files, hostnames, inventory, and generated
operator artifacts remain recoverable from Git history.

From a clean, committed checkout:

```bash
./scripts/export-public-source.sh /tmp/hserver-panel-public
```

The `/tmp/hserver-panel-public` path is disposable staging for a one-time
audit only; it is not the canonical public checkout or a permanent publication
workflow.

The exporter uses `git archive`, refuses a dirty tree or an existing
destination, omits `.git`, verifies the public policy files, and runs the
Git-free installation-inventory validator before publishing the destination.
A validation error leaves no destination behind. Run the public tree,
provider-neutral script, lifecycle, and release-package checks before using the
snapshot.

The canonical local acceptance command reproduces the CI snapshot build and
package check from a clean committed tree:

```bash
make test-public-source ARCH="$(go env GOARCH)"
```

The `Public Source Snapshot (amd64)` and `Public Source Snapshot (arm64)` CI
jobs perform this export natively on every change, build the frontend, panel,
CLI, and agent from the Git-free tree, then create and verify the matching
release archive. Before building, each exported tree runs its own
installation-inventory and provider-neutral script checks without relying on
Git metadata. Tagged publication depends on the complete matrix, so a source
snapshot that relies on untracked files, private Git history,
installation-specific inventory, architecture-specific omissions, or missing
packaged files cannot become a release.

**Recorded amd64 acceptance result (historical evidence, not a permanent
path):** From source HEAD `fd227828`, the clean Git-free v0.9.5 candidate was
produced in the temporary workspace
`/var/tmp/hserver-panel-public-v0.9.5-fd227828` with
candidate root commit `009ec010cfe09e21a6010b7ea93761359cc3b128` and tree
`a4a988f0fb8caffdd5c31245e1aa78ef00144d63`. Full public-source acceptance
passed with 92 frontend files, 489 tests, and 10 route tests. This records the
source validation result only; it predates the public `v0.9.5` tag and its
failed tagged run `#33285788628`, and does not establish a public release, live
deployment, or runtime rollout. The temporary workspace is disposable evidence,
not a canonical checkout or permanent workflow.

## 2. Create new public history

The one-time public-history creation and publication are complete. The
canonical public repository is
[`IamYGT/heyserver`](https://github.com/IamYGT/heyserver), initially published
at `53df8ba`, with private/public tree parity `3719df8`; immutable public source
commit `adaccb23adf9720141d721970590de3a82fd17b5` produced the first fully green
public main CI matrix run `#33283277809`. The
commands below document the one-time staging procedure that produced that
history; their `/tmp` destination is disposable and must not be treated as a
canonical checkout or permanent publication workflow.

```bash
./scripts/create-public-repository.sh /tmp/hserver-panel-public \
  --author-name "HServer Maintainers" \
  --author-email "maintainers@example.com"

cd /tmp/hserver-panel-public
git log --oneline --decorate --all
git fsck --strict --no-dangling
git remote add origin YOUR_NEW_PUBLIC_REMOTE
git push -u origin main
```

The creator delegates source selection to `export-public-source.sh`, refuses a
dirty private checkout, an existing destination, or a destination inside the
private tree, and builds in a sibling staging directory before an atomic move.
It requires exactly one root commit on `main`, a clean index, a strict Git object
check, and the public inventory validator. It deliberately configures no remote
and never pushes; inspect the resulting repository before adding the new public
destination.

Keep the original operational repository private as the audit archive. Do not
copy its `.git` directory, refs, tags, reflogs, release artifacts, or
pull-request refs into the public repository.

## 3. Configure the public repository

- `main` branch protection is complete, with the required lint, test, frontend
  build, Go build,
  `Database Restore (PostgreSQL)`, `Database Restore (MariaDB)`,
  `Docker Quick Evaluation`, `Public Source Snapshot (amd64)`,
  `Public Source Snapshot (arm64)`,
  `Release Package (amd64)`, and
  `Release Package (arm64)` CI checks;
- enable private vulnerability reporting for `SECURITY.md`;
- enable Dependabot and platform secret scanning when available;
- verify issue forms, pull-request template, license detection, and support
  links;
- allow releases only from version tags created from protected `main`; the
  release provenance job verifies the live protected branch and tag ancestry;
- keep the release Ed25519 private key outside Git. The initial active signer is
  prepared in private commit `df0a5070`, and the `HSERVER_RELEASE_SIGNING_KEY`
  Actions secret is configured; public signer PR #7 was merged at protected
  `main` commit `b2af1591` on 2026-08-30;
- require the configured key and the publicly reviewed signer from merged PR #7
  to match before tagged staging; signer publication is complete, but staging
  remains gated by the release checks below;
- use the canonical public repository
  [`IamYGT/heyserver`](https://github.com/IamYGT/heyserver), initially published
  at `53df8ba`, with private/public tree parity `3719df8`; the immutable public
  source commit associated with the first fully green public main CI matrix
  run is `adaccb23adf9720141d721970590de3a82fd17b5`;
- following the protected merge of public signer PR #7 at `b2af1591` on
  2026-08-30, publish the immutable installer commit plus installer digest and
  signer fingerprint through an independently authenticated channel. A checksum
  adjacent to a mutable release asset is a convenience integrity check, not
  signer identity;
- keep production deployment credentials outside repository actions. The
  release signing key signs public metadata only and grants no server access.

## 4. Release an installation candidate

The first fully green public main CI matrix run does not create a release.
The public `v0.9.5` tag points to protected `main` commit
`b2af1591f7a848acd71bbe54bc4f70fbffe99373`, but tagged run `#33285788628`
failed all four lifecycle jobs (both `Native Lifecycle` jobs and both
`Managed Agent Lifecycle` jobs) because their fixtures posted onboarding step 6
while the canonical maximum is step 5. That tag and run are
historical failed-release evidence, not a successful release. The public
`v0.9.6` tag and tagged workflow `#33288698005` are also failed-release evidence:
required public-source and native-lifecycle jobs failed, and no successful
GitHub Release exists. The public `v0.9.7` tag points to protected public
`main` commit `5c5bcf053b333f657ef011078f32ed54126946e6`, but tagged workflow
`#33291179584` is immutable disqualified evidence: both `Native Lifecycle` jobs
returned file-backup HTTP 400 because the fixture lacked an explicit temporary
vhosts root. The `Managed Agent Lifecycle` jobs were cancelled; their arm64
runner log preserved `agent release archive contains an invalid path`. The
workflow reached terminal `completed/cancelled` at
`2026-08-30T04:27:26Z` after normal cancellation followed by a force-cancel
because runner finalization stalled. No successful `v0.9.7` GitHub Release
exists. The
public `v0.9.8` tag object is
`81d3095f4c6f0615bd6912225bf18c38788e6e6c` and resolves to public `main`
commit `6f399eba2d4871d783dfff45a6d4ece276865d9a`; tagged workflow
`#33293250350` is immutable disqualified evidence. The initial signed install,
retained upgrade, and rollback stages passed before `Native Lifecycle` amd64
job `#99208960092` and arm64 job `#99208960118` failed because upgrade-feed
signature rotation was missing, with
`Refusing to overwrite ... release-manifest.json.sig`. `Managed Agent
Lifecycle` jobs `#99208960112` and `#99208960115` were cancelled. The workflow
reached terminal `completed/cancelled` at `2026-08-30T05:20:10Z` after normal
cancellation at `05:15 UTC` followed by a force-cancel accepted with HTTP 202
at `05:17:32 UTC` because runner finalization stalled. No `v0.9.8` GitHub
Release exists; do not delete or reuse its
immutable tag, which remains disqualified evidence. The next immutable
replacement candidate is `v0.9.9`. Its acceptance is established only by a
published GitHub Release, a successful tagged lifecycle/provenance workflow,
clean independent-VM acceptance, and the separate live rollout receipt. The
Public PR #13 and its 14/14-success CI run `#33295933393` merged at
`2026-08-30T06:07:31Z` as protected `main` commit
`bf67c80c9b383d28a4eefd1d75a014b41bd00f45` (exact tree
`bb838be28a9cb23897e943250e1c69ce9e71d138`). Protected-main CI
`#33296189213` completed/success with 14/14 checks, including the exact cleanup
test and both architecture release-package jobs. The prior main run
`#33294999297` failed only its `EXIT cleanup` race from Git 2.55 detached
auto-maintenance; private fix
`aa10cb7b08fa98695ebdea7b95bfb2fb3a4835bd` uses command-scoped
`maintenance.auto=false`. These are source/merge and protected-main CI evidence
only; the `v0.9.9` tag, GitHub Release, clean-host acceptance, and live rollout
remain unclaimed.

Create the next installation candidate from an exact, previously unused stable
SemVer tag in the designated public repository. Build it from the exact commit
on protected public `main`; do not reuse the failed `v0.9.5` or `v0.9.6` tags,
the disqualified `v0.9.7` and `v0.9.8` tags, or a version whose immutable feed
was built from a different repository commit, even when the exported source
trees are identical. Use `v0.9.9` as the next immutable patch candidate and
publish that
version only once after its fresh gates pass. A
`v0.x` release can remain product-level pre-1.0 without using an unordered `-rc`
suffix. HServer update manifests intentionally order only stable
`major.minor.patch` versions, so the release workflow rejects commit-derived,
dirty, and hyphenated prerelease tags before building tagged release binaries.
Add the matching `## [X.Y.Z]` section to `CHANGELOG.md` before creating the
`vX.Y.Z` tag; tagged CI rejects publication when the exact changelog section is
absent. Keep prior release entries and their acceptance evidence unchanged.
Confirm both architecture
archives,
adjacent checksum files, `bootstrap-install.sh`,
`bootstrap-install.sh.sha256`, `bootstrap-install.sh.sig`, `release-public-key.b64`,
`release-public-key.b64.sha256`, `public-install.sh`,
`public-install.sh.sha256`, `release-manifest.json`, and
`release-manifest.json.sig` exist, then install through the independently
anchored wrapper on a disposable Ubuntu 24.04 VM:

```bash
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

The wrapper is a manifest-external trust-bootstrap asset. It retains its own
checksum checks for transfer corruption, but signer identity comes only from
the embedded or explicitly supplied raw-key fingerprint. The downloaded key
must verify `bootstrap-install.sh.sig` before the wrapper changes mode or enters
root. The release inventory gate also compares the derived release key with the
canonical active signer and fails when the bootstrap signature is absent or
invalid. The signed manifest schema remains unchanged.

The release-package jobs run for every pull request and protected-branch push,
not only for tags. They cross-compile the real CGO-enabled panel binary, build
the agent and `hserverctl`, verify the archive checksum and required contents, and reject a
binary whose ELF architecture disagrees with the package name. This artifact
gate does not replace the native disposable-host installation flow below.
The independent `Docker Quick Evaluation` job builds the documented Dockerfile
from a clean checkout, including the generated OpenAPI contract, with
project-scoped container and volume identities. It waits for health, uses the
generated protected credentials to authenticate without printing them,
persists onboarding state, restarts the container, verifies the state, and
removes the disposable project and volume. Its first-login contract is also
explicit: `init-env.sh` creates a mode-`0600` `.env`; the operator reads
`HSERVER_ADMIN_EMAIL` and the generated `HSERVER_ADMIN_PASS` locally, opens
`http://localhost:3085`, and completes onboarding to reach the dashboard;
`init-env.sh` never prints the generated password. Tagged release publication
depends on this result. It proves the public evaluation path, not native
host-control capabilities.
The release job generates the schema-v1 manifest only after both architecture
packages pass and recalculates every published artifact hash. Configure a test
panel with the candidate release asset's stable latest URL and public key.
Confirm the UI reports `Ed25519 signature verified` and that a modified
manifest or signature makes discovery unavailable. Confirm discovery
does not download anything by itself, then explicitly stage the archive in the
About page, verify the displayed digest and platform, approve installation in
the second admin confirmation, and observe the terminal stage state.

Every panel-stage, panel-install, and managed-agent-upgrade mutation must also
fail closed when the release `signature_status` is not `verified`, at both the
server boundary and the CLI/TUI/web clients. Unsigned status may remain
observable, but it is not actionable release evidence.

Before the successful path, run the packaged installer once with a deliberately
unhealthy panel executable. Require a non-zero result and confirm the panel,
CLI, systemd unit, generated configuration/data, managed Nginx snippets, and
active/enabled service state are all absent afterward. Then continue with the
verified release archive:

1. `sudo ./doctor.sh preflight`
2. `sudo ./install.sh install`
3. `sudo ./doctor.sh installed`, then confirm the matching installer and doctor
   persist under `/usr/local/libexec`, every packaged fixed Nginx lifecycle file
   matches `/usr/local/share/hserver/nginx-snippets`, and
   `hserver-install next-steps` identifies the protected credential file
   without exposing its password;
4. create a named packaged-CLI context with an independent token-file
   reference, require both context and token files to be mode `0600`,
   authenticate with a protected password file through the current context,
   parse `host status`, `disk scan`, signed `updates status`, and empty
   `updates stage-status` without repeating connection flags, run
   `hserverctl doctor --output` into a mode-`0600` file, require its schema-v1
   panel, authentication, and empty fresh-fleet checks to pass, and complete
   onboarding;
5. open the authenticated local terminal through that current context, execute
   a unique marker, and observe that marker in real PTY output;
6. invoke one bounded maintenance action and confirm its result plus an inactive
   maintenance lock after completion;
7. create and validate a portable panel-state bundle, change persisted
   onboarding state, restore the bundle, and confirm the original state plus a
   pre-restore recovery bundle;
8. create a bounded files-root backup, validate that its boundary is files-only
   with automatic file rollback, change a source payload, restore it, compare
   the recovered SHA-256, and confirm the pre-restore file recovery archive is
   listed and independently valid;
9. enroll a second disposable VM with `agent-install.sh`, enable `terminal` and
   process signals for the drill, and leave at least one safe denial-check
   capability (`host.action` or `agent.update.read`) disabled;
10. from the native panel host, run
    `scripts/accept-provider-network-managed-agent.sh` to prove the public HTTPS
    path, separate kernel, writable remote terminal, process inventory, one
    stable-identity process signal, and task-free rejection of a known disabled
    capability;
11. switch the signed feed to a second stable build, use packaged
   `hserverctl updates stage --confirm` and `updates install --confirm`, confirm the
   installed panel and CLI both report that exact staged version, and confirm
   its detached unit reaches `completed` after the panel reconnects; also run a
   disposable binary-pair upgrade through the retained
   `/usr/local/libexec/hserver-install` with no extracted package present and
   require its rollback to restore the retained lifecycle assets;
12. repeat with an injected failed health check and confirm `failed` plus
   automatic rollback preserve the matched panel and CLI, SQLite data, and
   service state;
13. interrupt a disposable-host upgrade before its terminal status, restart the
   panel, and confirm inactive transient units reconcile to `failed` rather than
   polling forever;
14. uninstall without purge, confirm both executables and the fixed retained
    Nginx lifecycle directory are removed, and confirm configuration, data, and
    unrelated files in `/usr/local/share/hserver` remain.

Run step 10 from the native panel host, not from a third workstation. Use an
admin token file owned by the current user with mode `0600`; the script reads
the credential without printing it. The node must already be online through
the public HTTPS panel origin and advertise `inventory`, `terminal`,
`process.read`, and `process.signal`. At least one of `host.action`,
`agent.update.read`, and `backup.read` must remain absent so the drill can prove
fail-closed task admission without changing node configuration:

```bash
HSERVER_ACCEPT_PROVIDER_NETWORK=1 \
  ./scripts/accept-provider-network-managed-agent.sh \
  --confirm-bounded-marker \
  --panel-url https://panel.example.com \
  --node disposable-edge-1 \
  --token-file "$HOME/.config/hserver/token" \
  --receipt "$HOME/provider-network-receipt.json"
```

The explicit environment opt-in and bounded-marker confirmation are both
mandatory; candidate publication still requires the node itself to be a
disposable independent VM. Current panels prove the runner identity with the
authenticated `/api/system/info` boot ID. Older pre-release panels without that
field use exact hostname compatibility and record that weaker method in the
receipt. The managed node must report a different boot ID.

The PTY launches only a uniquely named Python marker for at most 300 seconds.
Because heartbeat inventory is deliberately capped to the 50 highest-memory
processes, the runner sizes the marker from 16 MiB to at most 96 MiB above the
observed inventory floor and refuses unless four times that allocation remains
available. It detaches the marker from the PTY, finds exactly one matching PID
and start time in agent inventory, and terminates that stable identity. If a
later check fails, cleanup retries only the observed identity; an unobserved
marker expires on its own.

The mode-`0600` schema-v3 receipt contains the panel origin, node ID, exact
panel version and public `/api/health` build commit, CLI/agent releases, panel
and managed-node architectures, panel identity method, selected disabled
capability, terminal close mode, bounded marker allocation, timestamp, and
boolean checks. The drill refuses nodes whose
current heartbeat omits a supported release architecture. It does not
contain tokens, raw boot IDs, terminal output, or host inventory. A legacy
agent that reports Linux PTY `EIO` as an unexpected close is accepted only
after the marker receipt and full process observation, and that compatibility
mode remains explicit in the receipt. Current agent builds treat PTY `EIO` as
a normal shell exit.

Passing this drill proves a separate kernel and the exercised network/runtime
path. The operator remains responsible for recording that the disposable node
was actually provisioned with the intended independent provider or VM account.

Before accepting the receipt for a release, validate its protected-file mode,
freshness, exact panel/CLI/agent identities, both architectures, destination,
schema, bounded marker, and all 13 checks. Strict defaults require schema v3, a
current panel boot-ID match, and a normal terminal close:

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

`--max-age` accepts whole minutes, hours, or days and never more than `30d`.
The verifier rejects symlinks, non-`0600` or foreign-owned files, duplicate or
unknown schema fields, missing/false checks, future or stale timestamps,
unsupported capability/compatibility values, and identity mismatches. Legacy
schema-v1 and schema-v2 receipts can be inspected with `--require-schema any`,
but neither schema binds `panel_commit`; legacy panel
identity and terminal-close modes separately require `--require-panel-identity
any` and `--require-terminal-close any`. Those explicit compatibility modes do
not satisfy the strict current-candidate example above. Verification proves the
receipt is structurally complete, fresh, protected, and matches the expected
release identities. Structural verification alone is not cryptographic
attestation and does not establish provider-account ownership.

Current release evidence must also bind the exact receipt bytes to a dedicated
operator-held Ed25519 key. Generate a provider-receipt key separately from the
release-manifest key, sign without overwriting the structural receipt, and then
require the signature during acceptance:

```bash
./scripts/generate-release-signing-key.sh \
  /secure/hserver-provider-receipt-ed25519.pem \
  /secure/hserver-provider-receipt-ed25519.pub

./scripts/sign-provider-network-receipt.sh \
  "$HOME/provider-network-receipt.json" \
  /secure/hserver-provider-receipt-ed25519.pem \
  "$HOME/provider-network-receipt.json.sig"

./scripts/verify-provider-network-receipt.py \
  "$HOME/provider-network-receipt.json" \
  --signature "$HOME/provider-network-receipt.json.sig" \
  --public-key /secure/hserver-provider-receipt-ed25519.pub \
  --require-signature \
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

Record the emitted `signing_key_sha256` beside the release decision. The
verifier rejects a modified receipt, wrong key, malformed signature, symlink,
foreign ownership, or artifact mode other than `0600`/`0644`. Without the two
signature paths it deliberately reports `signature=not_checked`; the
`--require-signature` flag turns accidental omission into failure. This proves
which key signed the exact receipt bytes, but still does not independently
prove provider-account ownership.

Version tags enforce this native panel lifecycle on GitHub-hosted Ubuntu 24.04
VMs for both architectures before the release job can publish. The labels are
`ubuntu-24.04` for `amd64` and `ubuntu-24.04-arm` for `arm64`. The acceptance
script refuses to run unless it is root on an explicitly disposable CI host,
refuses a pre-existing HServer installation or occupied port, and purges only
the installation it created after checking the non-purge uninstall boundary.
It first injects an unhealthy initial executable and requires atomic cleanup
before starting the signed bootstrap path.
Its initial installation is performed through a locally signed schema-v1 feed
and the published bootstrap client, so signature, archive, architecture,
packaged lifecycle, persistent recovery tools, password-free first-access
guidance, and persisted update-feed behavior remain part of the same native
release gate rather than a shell-only fixture.
The same tag gate creates a WAL-safe portable panel-state bundle, validates it,
changes real onboarding state through the API, restores the bundle through the
packaged lifecycle tool, checks the recovered state through the restarted
panel, and requires the protected pre-restore recovery bundle. Before the
restore drill,
it creates a mode-`0600` named context with an independent protected token-file
reference, authenticates through that current context, verifies mode-`0600`
token persistence, and parses read-only host status, disk scan, signed release
status, and empty fresh-stage output without repeated connection flags. It also
runs the packaged read-only `hserverctl doctor --output` through that context,
requires the report file to be mode `0600`, and requires its schema-v1 panel,
authentication, and fresh fleet checks to pass. It then launches the packaged
`hserverctl terminal` through the same current context inside an allocated TTY,
executes a unique marker in the writable local PTY, and requires that marker in
the CLI transcript. This proves the shipped client, persisted context
selection, protected token-file selection, authenticated WebSocket upgrade,
protocol negotiation, raw terminal mode, PTY input, and byte-safe output as one
path. Finally it invokes the bounded `temp-clean` maintenance action, requires
a non-empty action result, and confirms the maintenance status lock is
inactive.

Each tag also runs `Managed Agent Network Isolation` natively on `amd64` and
`arm64`. The gate creates separate hub and node Linux network namespaces joined
only by a point-to-point veth pair, gives neither namespace a default route,
and confirms that node loopback cannot reach the panel and that the hub cannot
find a panel listener at the node address. The real agent must then enroll
outbound, advertise `inventory`, `process.read`, and `process.signal`, expose a
fresh process observation, and terminate that exact stable process identity.
The same run confirms that an unadvertised `host.action` returns `409 Conflict`
without creating a task. The release job depends on both architecture results.
This deterministic kernel-network boundary complements rather than replaces
the candidate's separate-VM/provider-network drill.

The same runner builds a second native panel, agent, and CLI package for its
own architecture without publishing that acceptance-only artifact. It switches
the local signed feed to that newer stable version, proves the CLI observes the
exact SHA-256 and size, stages the full lifecycle package, and schedules the
installation through `hserverctl`. After the real detached systemd restart, it
requires the stage to reach `completed`, both installed executable identities
to match the staged version, the service to be active, and the persisted
onboarding state to remain unchanged.

It also creates a bounded random file payload, runs the authenticated asynchronous
files backup, waits for its durable job state, validates the files-only restore
boundary, changes the payload, restores it, and compares the original SHA-256.
The test requires `filesRollback=true`, verifies the pre-restore recovery
archive is readable and listed as completed, and validates that archive as a
restorable files artifact. Deterministic service tests inject a partial
extraction failure and prove that overwritten bytes are recovered while newly
created paths are removed.

The independent `Database Restore` matrix provisions isolated PostgreSQL and
MariaDB servers on Ubuntu 24.04. Both jobs exercise a real dump, mutation,
restore, recovery-point creation, deliberately failing partial mutation, and
automatic rollback. The release job names the matrix as a required dependency,
so a tag cannot publish packages when either engine result fails. A release
candidate must still verify its installation-specific database identities,
network paths, and protected credential files.

Each tag also runs `Managed Agent Lifecycle` on both architectures. The public
`v0.9.5` tagged run `#33285788628` failed all four lifecycle jobs (both Native
Lifecycle jobs and both Managed Agent Lifecycle jobs) because their fixtures
posted onboarding step 6 while the canonical maximum is 5. The `v0.9.6` tagged
workflow `#33288698005` is also disqualified by required public-source and
native-lifecycle failures. The public `v0.9.7` tagged workflow
`#33291179584` is also disqualified: both `Native Lifecycle` jobs returned
file-backup HTTP 400 because the fixture lacked an explicit temporary vhosts
root. The `Managed Agent Lifecycle` jobs were cancelled; their arm64 runner log
preserved `agent release archive contains an invalid path`. The workflow
reached terminal `completed/cancelled` at
`2026-08-30T04:27:26Z` after normal cancellation followed by a force-cancel
because runner finalization stalled. No successful `v0.9.7` GitHub Release
exists. The public `v0.9.8` tag object is
`81d3095f4c6f0615bd6912225bf18c38788e6e6c` and resolves to public `main`
commit `6f399eba2d4871d783dfff45a6d4ece276865d9a`; tagged workflow
`#33293250350` is also immutable disqualified evidence. Its initial signed
install, retained upgrade, and rollback stages passed before `Native
Lifecycle` amd64 job `#99208960092` and arm64 job `#99208960118` failed because
upgrade-feed signature rotation was missing, with
`Refusing to overwrite ... release-manifest.json.sig`. `Managed Agent
Lifecycle` jobs `#99208960112` and `#99208960115` were cancelled. The workflow
reached terminal `completed/cancelled` at `2026-08-30T05:20:10Z` after normal
cancellation at `05:15 UTC` followed by a force-cancel accepted with HTTP 202
at `05:17:32 UTC` because runner finalization stalled. No `v0.9.8` GitHub
Release exists; do not delete or reuse its
immutable tag, which remains disqualified evidence. The next immutable
`v0.9.9` candidate must rerun this gate. Public PR #13 and
its 14/14-success CI run `#33295933393` merged at `2026-08-30T06:07:31Z` as
protected `main` commit `bf67c80c9b383d28a4eefd1d75a014b41bd00f45` (exact tree
`bb838be28a9cb23897e943250e1c69ce9e71d138`). Protected-main CI
`#33296189213` completed/success with 14/14 checks, including the exact cleanup
test and both architecture release-package jobs. The prior main run
`#33294999297` failed only its `EXIT cleanup` race from Git 2.55 detached
auto-maintenance; private fix `aa10cb7b08fa98695ebdea7b95bfb2fb3a4835bd`
uses command-scoped `maintenance.auto=false`. These are source/merge and
protected-main CI evidence only; the `v0.9.9` tag, GitHub Release, clean-host
acceptance, and live rollout remain unclaimed.
That gate installs the real
panel and systemd-managed agent on the disposable runner,
enrolls the node through the one-time token API, serves a locally signed next
release, and proves heartbeat, signed discovery, an explicitly enabled remote
terminal capability, a protected named CLI context, a packaged
`hserverctl doctor --node` report that requires the runner's exact native agent
architecture plus the terminal and agent-update read capabilities, and a
packaged `hserverctl terminal --node` PTY marker round trip through that current
context. It reads the verified newer agent release through
`hserverctl updates agent status`, then performs upgrade and rollback through
the packaged, explicitly confirmed `hserverctl updates agent` commands. It also
proves disabled-capability
denial, the missing-capability error, unchanged task history after that
rejection, systemd stop,
server-observed offline transition, rejection and non-persistence of new work
while offline, restart and online recovery, central upgrade, completed
lifecycle state, central rollback, crash-loop automatic recovery,
configuration/token preservation, and non-purge uninstall. The release job
depends on both architecture results. This co-located systemd lifecycle gate
and the independent network-namespace gate do not replace the separate-VM
provider-network drill in the manual candidate checklist.
These runner labels are listed in the official
[GitHub-hosted runners reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).

Do not call the release generally available until that disposable-host flow has
fresh evidence for both `amd64` and `arm64`, or until the unsupported architecture
is explicitly excluded from the release.
