#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
signer="$repo_root/scripts/sign-provider-network-receipt.sh"
verifier="$repo_root/scripts/verify-provider-network-receipt.py"
keygen="$repo_root/scripts/generate-release-signing-key.sh"
tmp=$(mktemp -d)
cleanup() {
  find "$tmp" -xdev -depth -delete
}
trap cleanup EXIT INT TERM

fail() {
  echo "$*" >&2
  exit 1
}

python3 - "$tmp/receipt.json" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

receipt = {
    "schema_version": 3,
    "status": "passed",
    "accepted_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "panel_origin": "https://panel.example.com",
    "node_id": "signed-edge-1",
    "panel_version": "v1.2.3",
    "panel_commit": "signed-panel-commit",
    "panel_arch": "amd64",
    "cli_version": "v1.2.3",
    "agent_version": "v1.2.3",
    "node_arch": "arm64",
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

"$keygen" "$tmp/signing.pem" "$tmp/public.b64" >/dev/null
"$signer" "$tmp/receipt.json" "$tmp/signing.pem" "$tmp/receipt.json.sig" >"$tmp/sign.out"
grep -q 'Provider-network receipt signature created:' "$tmp/sign.out"
[[ $(stat -c '%a' "$tmp/receipt.json.sig") == 644 ]] || fail "provider receipt signature is not mode 0644"
[[ $(base64 -d <"$tmp/receipt.json.sig" | wc -c | tr -d ' ') == 64 ]] || fail "provider receipt signature is not 64 bytes"

"$verifier" "$tmp/receipt.json" \
  --signature "$tmp/receipt.json.sig" \
  --public-key "$tmp/public.b64" \
  --require-signature \
  --panel-version v1.2.3 \
  --panel-commit signed-panel-commit \
  --panel-arch amd64 \
  --cli-version v1.2.3 \
  --agent-version v1.2.3 \
  --node-arch arm64 \
  --node signed-edge-1 \
  --panel-origin https://panel.example.com \
  >"$tmp/verify.out"
grep -q '^signature=verified$' "$tmp/verify.out"
expected_fingerprint=$(python3 - "$tmp/public.b64" <<'PY'
import base64
import hashlib
import sys
with open(sys.argv[1], "rb") as handle:
    public_key = base64.b64decode(handle.read().strip(), validate=True)
print(hashlib.sha256(public_key).hexdigest())
PY
)
grep -q "^signing_key_sha256=$expected_fingerprint$" "$tmp/verify.out"

"$verifier" "$tmp/receipt.json" >"$tmp/unsigned-structural.out"
grep -q '^signature=not_checked$' "$tmp/unsigned-structural.out"
if "$verifier" "$tmp/receipt.json" --require-signature >"$tmp/required.out" 2>"$tmp/required.err"; then
  fail "provider receipt verifier accepted a missing required signature"
fi
grep -q -- '--require-signature needs' "$tmp/required.err"
if "$verifier" "$tmp/receipt.json" --signature "$tmp/receipt.json.sig" >"$tmp/partial.out" 2>"$tmp/partial.err"; then
  fail "provider receipt verifier accepted a signature without a public key"
fi
grep -q -- '--signature and --public-key must be supplied together' "$tmp/partial.err"

cp "$tmp/receipt.json" "$tmp/tampered.json"
python3 - "$tmp/tampered.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    receipt = json.load(handle)
receipt["panel_version"] = "v1.2.4"
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump(receipt, handle, indent=2, sort_keys=True)
    handle.write("\n")
PY
if "$verifier" "$tmp/tampered.json" --signature "$tmp/receipt.json.sig" --public-key "$tmp/public.b64" >"$tmp/tampered.out" 2>"$tmp/tampered.err"; then
  fail "provider receipt verifier accepted a modified signed receipt"
fi
grep -q 'Ed25519 signature verification failed' "$tmp/tampered.err"

"$keygen" "$tmp/wrong-signing.pem" "$tmp/wrong-public.b64" >/dev/null
if "$verifier" "$tmp/receipt.json" --signature "$tmp/receipt.json.sig" --public-key "$tmp/wrong-public.b64" >"$tmp/wrong-key.out" 2>"$tmp/wrong-key.err"; then
  fail "provider receipt verifier accepted the wrong public key"
fi
grep -q 'Ed25519 signature verification failed' "$tmp/wrong-key.err"

cp "$tmp/receipt.json.sig" "$tmp/open-signature.sig"
chmod 0666 "$tmp/open-signature.sig"
if "$verifier" "$tmp/receipt.json" --signature "$tmp/open-signature.sig" --public-key "$tmp/public.b64" >"$tmp/open-signature.out" 2>"$tmp/open-signature.err"; then
  fail "provider receipt verifier accepted an open signature file"
fi
grep -q 'mode 0600 or 0644' "$tmp/open-signature.err"

ln -s "$tmp/receipt.json.sig" "$tmp/symlink.sig"
if "$verifier" "$tmp/receipt.json" --signature "$tmp/symlink.sig" --public-key "$tmp/public.b64" >"$tmp/symlink.out" 2>"$tmp/symlink.err"; then
  fail "provider receipt verifier accepted a signature symlink"
fi
grep -q 'not a symlink' "$tmp/symlink.err"

cp "$tmp/receipt.json" "$tmp/open-receipt.json"
chmod 0644 "$tmp/open-receipt.json"
if "$signer" "$tmp/open-receipt.json" "$tmp/signing.pem" "$tmp/open-receipt.sig" >"$tmp/open-receipt.out" 2>"$tmp/open-receipt.err"; then
  fail "provider receipt signer accepted a mode-0644 receipt"
fi
grep -q 'receipt must have mode 0600' "$tmp/open-receipt.err"

cp "$tmp/signing.pem" "$tmp/open-key.pem"
chmod 0644 "$tmp/open-key.pem"
if "$signer" "$tmp/receipt.json" "$tmp/open-key.pem" "$tmp/open-key.sig" >"$tmp/open-key.out" 2>"$tmp/open-key.err"; then
  fail "provider receipt signer accepted a mode-0644 private key"
fi
grep -q 'signing key must have mode 0600' "$tmp/open-key.err"

if "$signer" "$tmp/receipt.json" "$tmp/signing.pem" "$tmp/receipt.json.sig" >"$tmp/overwrite.out" 2>"$tmp/overwrite.err"; then
  fail "provider receipt signer overwrote an existing signature"
fi
grep -q 'Refusing to overwrite' "$tmp/overwrite.err"

echo "provider-network receipt signing fixture: OK"
