#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
verifier="$repo_root/scripts/verify-provider-network-receipt.py"
tmp=$(mktemp -d)
cleanup() {
  find "$tmp" -xdev -depth -delete
}
trap cleanup EXIT INT TERM

fail() {
  echo "$*" >&2
  exit 1
}

write_receipt() {
  local destination=$1
  local accepted_at=$2
  local status=${3:-passed}
  python3 - "$destination" "$accepted_at" "$status" <<'PY'
import json
import os
import sys

receipt = {
    "schema_version": 3,
    "status": sys.argv[3],
    "accepted_at": sys.argv[2],
    "panel_origin": "https://panel.example.com",
    "node_id": "fixture-edge-1",
    "panel_version": "v1.2.3",
    "panel_commit": "fixture-panel-commit",
    "panel_arch": "arm64",
    "cli_version": "v1.2.3",
    "agent_version": "v1.2.3",
    "node_arch": "amd64",
    "panel_identity_method": "boot_id",
    "disabled_capability": "host.action",
    "terminal_close_mode": "normal",
    "marker_allocation_bytes": 16 << 20,
    "checks": {
        "public_https_path": True,
        "acceptance_runs_on_panel_kernel": True,
        "server_observed_online": True,
        "protocol_v1": True,
        "separate_kernel_boot_id": True,
        "required_capabilities": True,
        "cli_release_identity": True,
        "managed_node_architecture": True,
        "writable_remote_terminal": True,
        "process_inventory": True,
        "stable_identity_process_signal": True,
        "disabled_capability_rejected": True,
        "rejected_task_not_persisted": True,
    },
}
fd = os.open(sys.argv[1], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as handle:
    json.dump(receipt, handle, indent=2, sort_keys=True)
    handle.write("\n")
PY
}

now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
write_receipt "$tmp/valid.json" "$now"
"$verifier" "$tmp/valid.json" \
  --max-age 1h \
  --panel-version v1.2.3 \
  --panel-commit fixture-panel-commit \
  --panel-arch arm64 \
  --cli-version v1.2.3 \
  --agent-version v1.2.3 \
  --node-arch amd64 \
  --node fixture-edge-1 \
  --panel-origin https://panel.example.com \
  >"$tmp/valid.out"
grep -q 'provider-network receipt verification: OK' "$tmp/valid.out"
grep -q 'schema_version=3' "$tmp/valid.out"
grep -q 'panel_commit=fixture-panel-commit' "$tmp/valid.out"
grep -q 'checks=13/13' "$tmp/valid.out"

cp "$tmp/valid.json" "$tmp/open-mode.json"
chmod 0644 "$tmp/open-mode.json"
if "$verifier" "$tmp/open-mode.json" >"$tmp/open-mode.out" 2>"$tmp/open-mode.err"; then
  fail "provider receipt verifier accepted mode 0644"
fi
grep -q 'mode 0600' "$tmp/open-mode.err"

ln -s "$tmp/valid.json" "$tmp/symlink.json"
if "$verifier" "$tmp/symlink.json" >"$tmp/symlink.out" 2>"$tmp/symlink.err"; then
  fail "provider receipt verifier accepted a symlink"
fi
grep -q 'not a symlink' "$tmp/symlink.err"

stale=$(date -u -d '3 hours ago' +%Y-%m-%dT%H:%M:%SZ)
write_receipt "$tmp/stale.json" "$stale"
if "$verifier" "$tmp/stale.json" --max-age 1h >"$tmp/stale.out" 2>"$tmp/stale.err"; then
  fail "provider receipt verifier accepted a stale receipt"
fi
grep -q 'receipt is stale' "$tmp/stale.err"

if "$verifier" "$tmp/valid.json" --panel-version v9.9.9 >"$tmp/version.out" 2>"$tmp/version.err"; then
  fail "provider receipt verifier accepted a different panel version"
fi
grep -q 'panel_version mismatch' "$tmp/version.err"

if "$verifier" "$tmp/valid.json" --panel-commit different-commit >"$tmp/commit.out" 2>"$tmp/commit.err"; then
  fail "provider receipt verifier accepted a different panel commit"
fi
grep -q 'panel_commit mismatch' "$tmp/commit.err"

python3 - "$tmp/valid.json" "$tmp/incomplete.json" <<'PY'
import json
import os
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    receipt = json.load(handle)
receipt["checks"].pop("process_inventory")
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as handle:
    json.dump(receipt, handle)
PY
if "$verifier" "$tmp/incomplete.json" >"$tmp/incomplete.out" 2>"$tmp/incomplete.err"; then
  fail "provider receipt verifier accepted incomplete checks"
fi
grep -q 'exactly the schema-v3 acceptance checks' "$tmp/incomplete.err"

if "$verifier" "$tmp/valid.json" --node-arch arm64 >"$tmp/node-arch.out" 2>"$tmp/node-arch.err"; then
  fail "provider receipt verifier accepted a different managed-node architecture"
fi
grep -q 'node_arch mismatch' "$tmp/node-arch.err"

python3 - "$tmp/valid.json" "$tmp/legacy.json" <<'PY'
import json
import os
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    receipt = json.load(handle)
receipt["panel_identity_method"] = "hostname_compatibility"
receipt["terminal_close_mode"] = "legacy_agent_eio"
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as handle:
    json.dump(receipt, handle)
PY
if "$verifier" "$tmp/legacy.json" >"$tmp/legacy.out" 2>"$tmp/legacy.err"; then
  fail "provider receipt verifier accepted legacy evidence under strict defaults"
fi
grep -q 'panel_identity_method mismatch' "$tmp/legacy.err"
"$verifier" "$tmp/legacy.json" \
  --require-panel-identity any \
  --require-terminal-close any \
  >"$tmp/legacy-compatible.out"
grep -q 'terminal_close_mode=legacy_agent_eio' "$tmp/legacy-compatible.out"

python3 - "$tmp/valid.json" "$tmp/schema-v1.json" <<'PY'
import json
import os
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    receipt = json.load(handle)
receipt["schema_version"] = 1
receipt.pop("panel_commit")
receipt.pop("cli_version")
receipt.pop("node_arch")
receipt["checks"].pop("cli_release_identity")
receipt["checks"].pop("managed_node_architecture")
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as handle:
    json.dump(receipt, handle)
PY
if "$verifier" "$tmp/schema-v1.json" >"$tmp/schema-v1.out" 2>"$tmp/schema-v1.err"; then
  fail "provider receipt verifier accepted schema v1 under current defaults"
fi
grep -q 'schema_version mismatch' "$tmp/schema-v1.err"
"$verifier" "$tmp/schema-v1.json" --require-schema any >"$tmp/schema-v1-compatible.out"
grep -q 'schema_version=1' "$tmp/schema-v1-compatible.out"
grep -q 'checks=11/11' "$tmp/schema-v1-compatible.out"
if "$verifier" "$tmp/schema-v1.json" --require-schema any --cli-version v1.2.3 >"$tmp/schema-v1-cli.out" 2>"$tmp/schema-v1-cli.err"; then
  fail "provider receipt verifier treated schema v1 as CLI-bound evidence"
fi
grep -q 'does not bind cli_version' "$tmp/schema-v1-cli.err"

python3 - "$tmp/valid.json" "$tmp/schema-v2.json" <<'PY'
import json
import os
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    receipt = json.load(handle)
receipt["schema_version"] = 2
receipt.pop("panel_commit")
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as handle:
    json.dump(receipt, handle)
PY
"$verifier" "$tmp/schema-v2.json" --require-schema any >"$tmp/schema-v2-compatible.out"
grep -q 'schema_version=2' "$tmp/schema-v2-compatible.out"
grep -q 'checks=13/13' "$tmp/schema-v2-compatible.out"
if "$verifier" "$tmp/schema-v2.json" --require-schema any --panel-commit fixture-panel-commit >"$tmp/schema-v2-commit.out" 2>"$tmp/schema-v2-commit.err"; then
  fail "provider receipt verifier treated schema v2 as panel-commit-bound evidence"
fi
grep -q 'schema-v2 receipt does not bind panel_commit' "$tmp/schema-v2-commit.err"

echo "provider-network receipt verifier fixture: OK"
