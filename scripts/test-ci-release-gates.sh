#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
workflow="$repo_root/.github/workflows/ci.yml"
native_lifecycle="$repo_root/scripts/test-native-release-lifecycle.sh"
managed_lifecycle="$repo_root/scripts/test-native-managed-agent-lifecycle.sh"
network_isolation="$repo_root/scripts/test-managed-agent-network-isolation.sh"
public_acceptance="$repo_root/scripts/test-public-source-acceptance.sh"
makefile="$repo_root/Makefile"
release_launcher="$repo_root/internal/services/releaseupdates/launcher.go"
release_manager="$repo_root/internal/services/releaseupdates/manager.go"
installer_lifecycle="$repo_root/scripts/test-hserver-install.sh"
release_config="$repo_root/internal/config/config.go"
release_router="$repo_root/internal/api/router.go"

python3 - "$workflow" "$native_lifecycle" "$managed_lifecycle" "$network_isolation" "$public_acceptance" "$makefile" "$release_launcher" "$release_manager" "$installer_lifecycle" "$release_config" "$release_router" <<'PY'
import pathlib
import re
import sys

workflow = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
native_lifecycle = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")
managed_lifecycle = pathlib.Path(sys.argv[3]).read_text(encoding="utf-8")
network_isolation = pathlib.Path(sys.argv[4]).read_text(encoding="utf-8")
public_acceptance = pathlib.Path(sys.argv[5]).read_text(encoding="utf-8")
makefile = pathlib.Path(sys.argv[6]).read_text(encoding="utf-8")
release_launcher = pathlib.Path(sys.argv[7]).read_text(encoding="utf-8")
release_manager = pathlib.Path(sys.argv[8]).read_text(encoding="utf-8")
installer_lifecycle = pathlib.Path(sys.argv[9]).read_text(encoding="utf-8")
release_config = pathlib.Path(sys.argv[10]).read_text(encoding="utf-8")
release_router = pathlib.Path(sys.argv[11]).read_text(encoding="utf-8")

def job_block(name: str) -> str:
    match = re.search(
        rf"(?ms)^  {re.escape(name)}:\n(.*?)(?=^  [a-zA-Z0-9_-]+:\n|\Z)",
        workflow,
    )
    if not match:
        raise SystemExit(f"CI job is missing: {name}")
    return match.group(1)

database = job_block("database-restore-acceptance")
for required in (
    "runs-on: ubuntu-24.04",
    "engine: PostgreSQL",
    "package: postgresql",
    "script: ./scripts/test-postgresql-restore-drill.sh",
    "engine: MariaDB",
    "package: mariadb-server",
    "script: ./scripts/test-mariadb-restore-drill.sh",
    "Exercise real backup, restore, and automatic rollback",
):
    if required not in database:
        raise SystemExit(f"database restore acceptance is missing: {required}")

go_test = job_block("go-test")
if "test-postgresql-restore-drill.sh" in go_test or "test-mariadb-restore-drill.sh" in go_test:
    raise SystemExit("database restore drills are still hidden inside the generic Go test job")
for required in (
    "actions/setup-node@v4",
    "node-version: ${{ env.NODE_VERSION }}",
    "run: make test-shell",
):
    if required not in go_test:
        raise SystemExit(f"generic shell acceptance is missing its contributor toolchain: {required}")

docker = job_block("docker-evaluation")
for required in (
    "name: Docker Quick Evaluation",
    "runs-on: ubuntu-24.04",
    "run: make test-docker",
):
    if required not in docker:
        raise SystemExit(f"Docker evaluation acceptance is missing: {required}")

public_source = job_block("public-source-acceptance")
for required in (
    'name: Public Source Snapshot (${{ matrix.arch }})',
    "arch: amd64",
    "arch: arm64",
    "runner: ubuntu-24.04",
    "runner: ubuntu-24.04-arm",
    "runs-on: ${{ matrix.runner }}",
    "Export, build, and package the committed public source tree",
    'run: make test-public-source ARCH=${{ matrix.arch }}',
):
    if required not in public_source:
        raise SystemExit(f"public source acceptance is missing: {required}")

for required in (
    '"$repo_root/scripts/export-public-source.sh" "$snapshot"',
    "./scripts/test-extension-catalog.py",
    "./scripts/test-public-docs.sh",
    "./scripts/test-community-docs.sh",
    "./scripts/test-provider-neutral-scripts.sh",
    "./scripts/test-release-trust.sh",
    "./scripts/test-public-install.sh",
    "./scripts/test-public-release-assets.sh",
    "make build",
    'test_runtime_root="$tmp/runtime"',
    'HSERVER_VHOSTS_ROOT="$test_runtime_root/vhosts"',
    'BACKUP_DIR="$test_runtime_root/backups"',
    "make test-go",
    "make test-frontend",
    "./cmd/hserver-agent",
    "./scripts/package-release.sh",
    "./scripts/verify-release-archive.sh",
):
    if required not in public_acceptance:
        raise SystemExit(f"public source acceptance script is missing: {required}")

if "./scripts/test-create-public-repository.sh" not in makefile:
    raise SystemExit("portable shell gate is missing clean public repository acceptance")
if "./scripts/test-extension-catalog.py" not in makefile:
    raise SystemExit("portable shell gate is missing extension catalog verification")

def assert_frontend_overlay_job(name: str, wrapper_calls: int) -> str:
    block = job_block(name)
    if "path: cmd/hserver/web/dist/" in block:
        raise SystemExit(f"{name} still writes the frontend artifact into the tracked embed tree")
    if "path: web/dist/" not in block:
        raise SystemExit(f"{name} does not download the frontend artifact into ignored web/dist")
    if "HSERVER_FRONTEND_DIST: web/dist" not in block:
        raise SystemExit(f"{name} does not pass ignored web/dist to the Go build wrapper")
    actual_calls = block.count("scripts/go-build-with-frontend.sh")
    if actual_calls != wrapper_calls:
        raise SystemExit(
            f"{name} must use scripts/go-build-with-frontend.sh {wrapper_calls} times, found {actual_calls}"
        )
    if re.search(r"(?m)^\s*CGO_ENABLED=.*\bgo build\b", block):
        raise SystemExit(f"{name} still invokes go build directly instead of the frontend overlay wrapper")
    return block

go_build = assert_frontend_overlay_job("go-build", 1)
for required in (
    "-X github.com/IamYGT/heyserver/internal/config.Version=${BUILD_VERSION}",
    "-X github.com/IamYGT/heyserver/internal/config.BuildCommit=${{ steps.version.outputs.commit }}",
    "-X github.com/IamYGT/heyserver/internal/config.BuildDate=${{ steps.version.outputs.build_date }}",
):
    if required not in go_build:
        raise SystemExit(f"go-build lost its explicit release identity flag: {required}")

for required in (
    'expected_version=$6',
    '"$installed_binary" --version',
    '"$installed_cli" version',
    'identities match the verified stage',
    'run_installer rollback',
    'previous release was restored',
    'record.Version',
    'nativePanelPath = "/usr/local/bin/hserver-panel"',
    'nativeCLIPath   = "/usr/local/bin/hserverctl"',
    'layoutMode = "preserve"',
):
    if required not in release_launcher:
        raise SystemExit(f"detached release identity gate is missing: {required}")

for required in (
    'NginxSnippetSHA256 map[string]string',
    '"nginx-snippets/" + name',
    'nginxSnippets: snippetHashes',
):
    if required not in release_manager:
        raise SystemExit(f"verified release Nginx asset gate is missing: {required}")

for required in (
    'HSERVER_UPDATE_PANEL_BINARY_PATH',
    'HSERVER_UPDATE_CLI_BINARY_PATH',
):
    if required not in release_config:
        raise SystemExit(f"path-aware release configuration is missing: {required}")
for required in (
    'cfg.UpdatePanelBinaryPath',
    'cfg.UpdateCLIBinaryPath',
):
    if required not in release_router:
        raise SystemExit(f"path-aware release manager wiring is missing: {required}")

release_build = job_block("release-build")
for required in (
    'name: Release Package (${{ matrix.arch }})',
    "arch: amd64",
    "arch: arm64",
    'bin/hserverctl-linux-${GOARCH}',
    "./cmd/hserverctl",
    '"bin/hserverctl-linux-${GOARCH}"',
    "Verify release archive architecture and contents",
    "Require a stable version for tagged releases",
    './scripts/validate-release-version.sh "$BUILD_VERSION"',
    "Require a matching changelog entry for tagged releases",
    './scripts/validate-release-changelog.sh "$BUILD_VERSION"',
):
    if required not in release_build:
        raise SystemExit(f"release package gate is missing CLI evidence: {required}")
assert_frontend_overlay_job("release-build", 3)
for required in (
    "-X github.com/IamYGT/heyserver/internal/config.Version=${BUILD_VERSION}",
    "-X github.com/IamYGT/heyserver/internal/config.BuildCommit=${{ steps.version.outputs.commit }}",
    "-X github.com/IamYGT/heyserver/internal/config.BuildDate=${{ steps.version.outputs.build_date }}",
):
    if required not in release_build:
        raise SystemExit(f"release-build lost its explicit release identity flag: {required}")

native = job_block("native-acceptance")
for required in (
    'name: Native Lifecycle (${{ matrix.arch }})',
    "runner: ubuntu-24.04",
    "runner: ubuntu-24.04-arm",
    "HSERVER_ACCEPT_DISPOSABLE_HOST=1",
    "./scripts/test-native-release-lifecycle.sh",
    "sqlite3 util-linux",
    "Build native upgrade acceptance release",
    "hserver-panel-native-upgrade-${GOARCH}",
    "hserverctl-native-upgrade-${GOARCH}",
    "native-upgrade/hserver-panel-${NEXT_VERSION}-linux-${GOARCH}.tar.gz",
    "steps.upgrade.outputs.version",
):
    if required not in native:
        raise SystemExit(f"native lifecycle gate is missing: {required}")
assert_frontend_overlay_job("native-acceptance", 3)
for required in (
    "-X github.com/IamYGT/heyserver/internal/config.Version=${NEXT_VERSION}",
    "-X github.com/IamYGT/heyserver/internal/config.BuildCommit=${BUILD_COMMIT}",
    "-X github.com/IamYGT/heyserver/internal/config.BuildDate=${BUILD_DATE}",
):
    if required not in native:
        raise SystemExit(f"native acceptance release lost its explicit release identity flag: {required}")

assert_frontend_overlay_job("network-isolation-acceptance", 2)

for required in (
    'cmp -s "$package_dir/hserverctl" /usr/local/bin/hserverctl',
    'cmp -s "$package_dir/install.sh" /usr/local/libexec/hserver-install',
    'cmp -s "$package_dir/doctor.sh" /usr/local/libexec/hserver-doctor',
    '/usr/local/libexec/hserver-install next-steps',
    'First-access guidance exposed the generated administrator password.',
    "/usr/local/bin/hserverctl health",
    '--password-file "$admin_password_file"',
    'stat -c \'%a\' "$cli_token"',
    'cli_contexts="$tmp/hserverctl-contexts.json"',
    'export HSERVER_CONTEXT_FILE="$cli_contexts"',
    'connect \\\n',
    '--server http://127.0.0.1:3085',
    'native \\\n',
    'stat -c \'%a\' "$cli_contexts"',
    'hserverctl context current',
    'host status >"$tmp/hserverctl-host-status.json"',
    'disk scan >"$tmp/hserverctl-disk-scan.json"',
    'doctor --output "$tmp/hserverctl-doctor.json"',
    'packaged hserverctl doctor report is not mode 0600.',
    'packaged hserverctl doctor did not pass',
    '"fleet") != {"observed": 0, "online": 0, "offline": 0}',
    'updates status >"$tmp/hserverctl-update-status.json"',
    'updates stage-status >"$tmp/hserverctl-update-stage.json"',
    'packaged hserverctl update status is not verified and healthy',
    'fresh native installation unexpectedly has an update stage',
    'updates stage --confirm >"$tmp/hserverctl-upgrade-stage.json"',
    'updates install --confirm >"$tmp/hserverctl-upgrade-install.json"',
    'packaged hserverctl did not schedule the verified release',
    'hserverctl-upgrade-terminal.json',
    'stage.get("status") == "completed"',
    '/usr/local/bin/hserver-panel --version',
    '/usr/local/bin/hserverctl version',
    'Installed panel identity does not match the verified native stage',
    'verified native CLI update did not preserve SQLite onboarding state',
    'An unhealthy upgrade unexpectedly succeeded.',
    'Failed upgrade did not restore the previous panel binary.',
    'Failed upgrade did not restore the previous CLI binary.',
    'failed upgrade rollback did not preserve SQLite state',
    'printf -v terminal_cli',
    "'%q --timeout 5s terminal' /usr/local/bin/hserverctl",
    'script -qefc "$terminal_cli" "$terminal_transcript"',
    '__HSERVER_NATIVE_CLI_TERMINAL_OK__',
    'Packaged hserverctl terminal did not return its PTY marker.',
    'scripts/bootstrap-install.sh"',
    '--manifest-url http://127.0.0.1:38085/release-manifest.json',
    '--public-key "$(<"$tmp/release-public.b64")"',
    'unhealthy-initial-panel',
    'native initial-install rollback acceptance: OK',
    '[[ ! -e /etc/hserver ]]',
    '[[ ! -e /var/lib/hserver ]]',
    "systemctl is-active --quiet hserver || systemctl is-enabled --quiet hserver",
    "/api/system/actions/temp-clean",
    "/api/system/actions/status",
    'sha256sum /usr/local/bin/hserverctl',
    '! -e /usr/local/bin/hserverctl',
    '! -e /usr/local/libexec/hserver-install',
    '! -e /usr/local/libexec/hserver-doctor',
    '/usr/local/share/hserver/nginx-snippets/',
    '! -e /usr/local/share/hserver/nginx-snippets',
):
    if required not in native_lifecycle:
        raise SystemExit(f"native lifecycle CLI acceptance is missing: {required}")

for required in (
    'run_retained_installer upgrade --binary "$retained_upgrade_binary" --cli-binary "$retained_upgrade_cli"',
    'run_retained_installer rollback >"$tmp/retained-rollback.log"',
    'retained_upgrade_root="$tmp/retained-upgrade-extract"',
    'tar -xzf "$upgrade_archive" -C "$retained_upgrade_root"',
    'rm -rf -- "$package_dir" "$retained_upgrade_root"',
    '[[ ! -e "$package_dir" && ! -e "$retained_upgrade_root" ]]',
    'retained_upgrade_panel_identity=$(/usr/local/bin/hserver-panel --version)',
    'retained_rollback_panel_identity=$(/usr/local/bin/hserver-panel --version)',
    'retained_upgrade_cli_identity=$(/usr/local/bin/hserverctl version)',
    'retained_rollback_cli_identity=$(/usr/local/bin/hserverctl version)',
    'systemctl is-enabled --quiet hserver',
    'curl -fsS --max-time 3 http://127.0.0.1:3085/api/health',
    'onboarding-after-retained-upgrade.json',
    'retained native upgrade did not preserve SQLite onboarding state',
    'onboarding-after-retained-rollback.json',
    'retained native rollback did not preserve SQLite onboarding state',
    'native-lifecycle-assets-before-retained-upgrade.sha256',
    'native-lifecycle-assets-after-retained-upgrade.sha256',
    'native-lifecycle-assets-after-retained-rollback.sha256',
    'Native lifecycle Nginx assets changed during retained installer upgrade.',
    'Explicit retained rollback did not restore native lifecycle Nginx assets.',
    'native retained upgrade acceptance: OK',
    'native retained explicit rollback acceptance: OK',
):
    if required not in native_lifecycle:
        raise SystemExit(f"retained native lifecycle acceptance is missing: {required}")

for required in (
    'run_retained_installer upgrade --binary "$tmp/v2" --cli-binary "$tmp/cli-v2"',
    'LIFECYCLE_ASSETS_DIR_WAS_PRESENT=1',
    'LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT=1',
    'LIFECYCLE_SNIPPETS_WERE_PRESENT=1',
    'modified-retained',
    'run_retained_installer rollback',
    'preserve-on-uninstall',
    'PRESERVE_LAYOUT=1',
    'systemd-unit-content.txt',
    'destination must not be a symlink',
    '[ ! -e "$custom_snapshot/databases.map" ]',
):
    if required not in installer_lifecycle:
        raise SystemExit(f"retained lifecycle asset acceptance is missing: {required}")

managed = job_block("managed-agent-acceptance")
for required in (
    'name: Managed Agent Lifecycle (${{ matrix.arch }})',
    "runner: ubuntu-24.04",
    "runner: ubuntu-24.04-arm",
    "HSERVER_ACCEPT_DISPOSABLE_HOST=1",
    "./scripts/test-native-managed-agent-lifecycle.sh",
    "binutils util-linux",
):
    if required not in managed:
        raise SystemExit(f"managed-agent lifecycle gate is missing: {required}")

for required in (
    "HSERVER_AGENT_ALLOW_TERMINAL=true",
    'required = {"inventory", "terminal", "agent.update.read", "agent.update.action"}',
    '--use managed',
    'Managed lifecycle hserverctl context file is not mode 0600.',
    'managed lifecycle current context is invalid',
    '>"$tmp/hserverctl-managed-doctor.json"',
    '--require-capability terminal',
    '--require-capability agent.update.read',
    'packaged hserverctl managed doctor did not pass',
    '"node.capability.agent.update.read": "pass"',
    '"node.capability.terminal": "pass"',
    'updates agent status --node "$node_id"',
    'packaged hserverctl agent update status is not verified and healthy',
    'updates agent upgrade --confirm --node "$node_id"',
    'managed agent upgrade was not scheduled',
    'updates agent rollback --confirm --node "$node_id"',
    'managed agent rollback was not scheduled',
    'managed_terminal_cli',
    'terminal --node %q',
    'script -qefc "$managed_terminal_cli" "$managed_terminal_transcript"',
    'Packaged hserverctl managed terminal did not return its PTY marker.',
    "/actions/memory-optimize",
    '[[ "$disabled_code" == 409 ]]',
    "does not advertise host.action",
    "tasks-before-disabled-request.json",
    "tasks-after-disabled-request.json",
    "disabled capability rejection changed the persisted task count",
):
    if required not in managed_lifecycle:
        raise SystemExit(f"managed-agent CLI acceptance is missing: {required}")

network = job_block("network-isolation-acceptance")
for required in (
    'name: Managed Agent Network Isolation (${{ matrix.arch }})',
    "runner: ubuntu-24.04",
    "runner: ubuntu-24.04-arm",
    "runs-on: ${{ matrix.runner }}",
    "gcc iproute2 iputils-ping",
    "HSERVER_ACCEPT_NETWORK_NAMESPACE=1",
    "./scripts/test-managed-agent-network-isolation.sh",
    'bin/hserver-panel-network-${GOARCH}',
    'bin/hserver-agent-network-${GOARCH}',
    "Prove outbound-only capability-scoped management",
):
    if required not in network:
        raise SystemExit(f"managed-agent network isolation gate is missing: {required}")

for required in (
    'ip netns add "$hub_namespace"',
    'ip netns add "$node_namespace"',
    "Managed-node loopback unexpectedly reaches the hub panel.",
    "Hub unexpectedly reached a panel listener on the managed node.",
    'HSERVER_AGENT_HUB_URL="$hub_origin"',
    "HSERVER_AGENT_ALLOW_PROCESS_SIGNALS=true",
    'required = {"inventory", "process.read", "process.signal"}',
    '"host.action" in capabilities',
    '/api/nodes/$node_id/processes',
    '/api/nodes/$node_id/processes/signal',
    'result.get("exited") is not True',
    '/api/nodes/$node_id/actions/memory-optimize',
    '[[ "$disabled_code" == 409 ]]',
    "disabled capability rejection changed persisted task history",
):
    if required not in network_isolation:
        raise SystemExit(f"managed-agent network isolation script is missing: {required}")

provenance = job_block("release-provenance")
for required in (
    "name: Release Provenance",
    "contents: read",
    "Resolve the protected main trust anchor",
    "context.eventName !== 'push'",
    "context.payload.repository?.default_branch !== 'main'",
    "github.rest.repos.getBranch",
    "branch.data.protected !== true",
    "core.setOutput('main_sha', branch.data.commit.sha)",
    "fetch-depth: 0",
    'PROTECTED_MAIN_SHA: ${{ steps.protected-main.outputs.main_sha }}',
    'git fetch --no-tags origin',
    'refs/heads/main:refs/remotes/origin/release-main',
    '[[ "$fetched_main" == "$PROTECTED_MAIN_SHA" ]]',
    'git merge-base --is-ancestor "$tag_commit" "$PROTECTED_MAIN_SHA"',
):
    if required not in provenance:
        raise SystemExit(f"release provenance gate is missing: {required}")

release = job_block("release")
if "- release-provenance" not in release:
    raise SystemExit("tagged release does not depend on protected-main provenance")
if "- database-restore-acceptance" not in release:
    raise SystemExit("tagged release does not depend on database restore acceptance")
if "- docker-evaluation" not in release:
    raise SystemExit("tagged release does not depend on Docker evaluation acceptance")
if "- public-source-acceptance" not in release:
    raise SystemExit("tagged release does not depend on public source acceptance")
if "- native-acceptance" not in release:
    raise SystemExit("tagged release does not depend on native lifecycle acceptance")
if "- managed-agent-acceptance" not in release:
    raise SystemExit("tagged release does not depend on managed-agent acceptance")
if "- network-isolation-acceptance" not in release:
    raise SystemExit("tagged release does not depend on managed-agent network isolation acceptance")
if "prerelease: false" not in release:
    raise SystemExit("stable-only release workflow can still publish a prerelease")
for required in (
    "Sign release manifest and bootstrap with the canonical signer",
    'key_file=$(mktemp "${RUNNER_TEMP}/hserver-release-signing-key.XXXXXXXX")',
    "--public-from-private",
    "release/release-public-key.b64",
    "./scripts/release-trust.py trust/release-signers.json",
    "--assert-active-key release/release-public-key.b64",
    "sha256sum release-public-key.b64 > release-public-key.b64.sha256",
    "install -m 0755 scripts/bootstrap-install.sh release/bootstrap-install.sh",
    "sha256sum bootstrap-install.sh > bootstrap-install.sh.sha256",
    "./scripts/sign-release-asset.sh",
    "release/bootstrap-install.sh.sig",
    "Verify bootstrap trust assets",
    "sha256sum --check bootstrap-install.sh.sha256",
    "sha256sum --check release-public-key.b64.sha256",
    "../scripts/verify-release-asset-signature.sh",
    "A private key was staged for release.",
    "release/release-public-key.b64.sha256",
    "Stage public install wrapper",
    "./scripts/stage-public-install.sh release trust/release-signers.json",
    "Verify complete public release asset set",
    "./scripts/verify-public-release-assets.sh release trust/release-signers.json",
    "release/bootstrap-install.sh.sig",
    "release/public-install.sh",
    "release/public-install.sh.sha256",
):
    if required not in release:
        raise SystemExit(f"tagged release trust chain is missing: {required}")
if re.search(r"(?m)^\s+files:\s+release/\*\s*$|^\s+release/\*\s*$", release):
    raise SystemExit("tagged release still uploads the whole staging directory")
PY

printf '%s\n' 'CI release gate contract: OK'
