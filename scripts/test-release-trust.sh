#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

"$repo_root/scripts/release-trust.py" "$repo_root/trust/release-signers.json"
canonical_public_key="$tmp/canonical-active.b64"
canonical_active_count=$(python3 - "$repo_root/trust/release-signers.json" "$canonical_public_key" <<'PY'
import json
import pathlib
import sys

document = json.loads(pathlib.Path(sys.argv[1]).read_text())
active = [signer["public_key"] for signer in document["signers"] if signer["status"] == "active"]
if active:
    pathlib.Path(sys.argv[2]).write_text(active[0] + "\n")
print(len(active))
PY
)

cat >"$tmp/empty-trust.json" <<'EOF'
{"schema_version":1,"signers":[]}
EOF
if "$repo_root/scripts/release-trust.py" "$tmp/empty-trust.json" \
  --require-active >"$tmp/empty.log" 2>&1; then
  echo "empty temporary trust store passed the official release gate" >&2
  exit 1
fi
grep -Fq 'release trust store has no active signer' "$tmp/empty.log"

if [[ "$canonical_active_count" -eq 0 ]]; then
  if "$repo_root/scripts/release-trust.py" "$repo_root/trust/release-signers.json" \
    --require-active >"$tmp/canonical-empty.log" 2>&1; then
    echo "empty canonical trust store passed the official release gate" >&2
    exit 1
  fi
  grep -Fq 'release trust store has no active signer' "$tmp/canonical-empty.log"
else
  "$repo_root/scripts/release-trust.py" "$repo_root/trust/release-signers.json" \
    --require-active >/dev/null
  canonical_fingerprints=$("$repo_root/scripts/release-trust.py" \
    "$repo_root/trust/release-signers.json" --require-active --fingerprints)
  canonical_active_fingerprint=$("$repo_root/scripts/release-trust.py" \
    "$repo_root/trust/release-signers.json" \
    --require-active --assert-active-key "$canonical_public_key")
  [[ ",$canonical_fingerprints," == *",$canonical_active_fingerprint,"* ]]
fi

"$repo_root/scripts/generate-release-signing-key.sh" \
  "$tmp/active.pem" "$tmp/active.b64" >/dev/null
"$repo_root/scripts/generate-release-signing-key.sh" \
  "$tmp/next.pem" "$tmp/next.b64" >/dev/null
python3 - "$tmp/active.b64" "$tmp/next.b64" "$tmp/trust.json" <<'PY'
import base64, hashlib, json, pathlib, sys
signers = []
for path, status in ((sys.argv[1], "active"), (sys.argv[2], "next")):
    public_key = pathlib.Path(path).read_text().strip()
    key_id = hashlib.sha256(base64.b64decode(public_key, validate=True)).hexdigest()
    signers.append({"key_id": key_id, "public_key": public_key, "status": status})
pathlib.Path(sys.argv[3]).write_text(json.dumps({"schema_version": 1, "signers": signers}) + "\n")
PY
fingerprints=$("$repo_root/scripts/release-trust.py" "$tmp/trust.json" \
  --require-active --fingerprints)
[[ $fingerprints == *,* ]]
active_fingerprint=$("$repo_root/scripts/release-trust.py" "$tmp/trust.json" \
  --assert-active-key "$tmp/active.b64")
[[ $fingerprints == "$active_fingerprint,"* ]]
if "$repo_root/scripts/release-trust.py" "$tmp/trust.json" \
  --assert-active-key "$tmp/next.b64" >"$tmp/next.log" 2>&1; then
  echo "next signer passed the active release signing gate" >&2
  exit 1
fi
grep -Fq 'is not an active trusted signer' "$tmp/next.log"

python3 - "$tmp/trust.json" "$tmp/mismatch.json" <<'PY'
import json, pathlib, sys
doc = json.loads(pathlib.Path(sys.argv[1]).read_text())
doc["signers"][0]["key_id"] = "0" * 64
pathlib.Path(sys.argv[2]).write_text(json.dumps(doc))
PY
if "$repo_root/scripts/release-trust.py" "$tmp/mismatch.json" >"$tmp/mismatch.log" 2>&1; then
  echo "trust store accepted a key_id/public_key mismatch" >&2
  exit 1
fi
grep -Fq 'key_id does not match public_key' "$tmp/mismatch.log"

python3 - "$tmp/trust.json" "$tmp/duplicate.json" <<'PY'
import json, pathlib, sys
doc = json.loads(pathlib.Path(sys.argv[1]).read_text())
doc["signers"].append(dict(doc["signers"][0]))
pathlib.Path(sys.argv[2]).write_text(json.dumps(doc))
PY
if "$repo_root/scripts/release-trust.py" "$tmp/duplicate.json" >"$tmp/duplicate.log" 2>&1; then
  echo "trust store accepted a duplicate signer" >&2
  exit 1
fi
grep -Fq 'duplicate signer' "$tmp/duplicate.log"

printf '%s\n' 'release signer trust store contract: OK'
