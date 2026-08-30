#!/usr/bin/env bash
set -euo pipefail

root_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

# Generic source staging remains available for forks, but has no implicit trust.
staged="$tmp/staged"
"$root_dir/scripts/stage-public-install.sh" "$staged" >/dev/null
for asset in public-install.sh public-install.sh.sha256; do
  [[ -f $staged/$asset && ! -L $staged/$asset && -s $staged/$asset ]] || {
    printf 'staged public install asset is invalid: %s\n' "$asset" >&2
    exit 1
  }
done
[[ $(stat -c '%a' "$staged/public-install.sh") == 755 ]]
(cd "$staged" && sha256sum --check public-install.sh.sha256 >/dev/null)
cmp -s "$root_dir/scripts/public-install.sh" "$staged/public-install.sh"

# The checked-in canonical state carries the active signer used for official
# tagged staging. Keep the empty-store gate below on a temporary fixture.
canonical_trust="$root_dir/trust/release-signers.json"
canonical_public_key="$tmp/canonical-active.b64"
canonical_active_count=$(python3 - "$canonical_trust" "$canonical_public_key" <<'PY'
import json
import pathlib
import sys

document = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
active = [signer["public_key"] for signer in document["signers"] if signer["status"] == "active"]
if active:
    pathlib.Path(sys.argv[2]).write_text(active[0] + "\n", encoding="ascii")
print(len(active))
PY
)
[[ "$canonical_active_count" -gt 0 ]] || {
  echo "canonical trust store must contain an active signer" >&2
  exit 1
}
"$root_dir/scripts/release-trust.py" "$canonical_trust" --require-active >/dev/null
canonical_fingerprints=$(
  "$root_dir/scripts/release-trust.py" "$canonical_trust" \
    --require-active --fingerprints
)
canonical_active_fingerprint=$(
  "$root_dir/scripts/release-trust.py" "$canonical_trust" \
    --require-active --assert-active-key "$canonical_public_key"
)
[[ ",$canonical_fingerprints," == *",$canonical_active_fingerprint,"* ]]
official="$tmp/official"
"$root_dir/scripts/stage-public-install.sh" "$official" "$canonical_trust" >/dev/null
grep -Fq "embedded_trusted_release_key_sha256_csv='$canonical_fingerprints'" \
  "$official/public-install.sh"
(cd "$official" && sha256sum --check public-install.sh.sha256 >/dev/null)

cat >"$tmp/empty-trust.json" <<'EOF'
{"schema_version":1,"signers":[]}
EOF
if "$root_dir/scripts/stage-public-install.sh" "$tmp/blocked" \
  "$tmp/empty-trust.json" >"$tmp/blocked.log" 2>&1; then
  echo "official staging accepted the temporary empty trust store" >&2
  exit 1
fi
grep -Fq 'release trust store has no active signer' "$tmp/blocked.log"

"$root_dir/scripts/generate-release-signing-key.sh" \
  "$tmp/signing.pem" "$tmp/public.b64" >/dev/null
"$root_dir/scripts/generate-release-signing-key.sh" \
  --public-from-private "$tmp/signing.pem" "$tmp/derived-public.b64" >/dev/null
cmp -s "$tmp/public.b64" "$tmp/derived-public.b64"
"$root_dir/scripts/generate-release-signing-key.sh" \
  "$tmp/next.pem" "$tmp/next.b64" >/dev/null
python3 - "$tmp/public.b64" "$tmp/next.b64" "$tmp/trust.json" <<'PY'
import base64, hashlib, json, pathlib, sys
signers = []
for path, status in ((sys.argv[1], "active"), (sys.argv[2], "next")):
    public_key = pathlib.Path(path).read_text().strip()
    signers.append({
        "key_id": hashlib.sha256(base64.b64decode(public_key, validate=True)).hexdigest(),
        "public_key": public_key,
        "status": status,
    })
pathlib.Path(sys.argv[3]).write_text(json.dumps({"schema_version": 1, "signers": signers}) + "\n")
PY
fingerprints=$("$root_dir/scripts/release-trust.py" "$tmp/trust.json" --fingerprints)
temporary_official="$tmp/temporary-official"
"$root_dir/scripts/stage-public-install.sh" "$temporary_official" "$tmp/trust.json" >/dev/null
grep -Fq "embedded_trusted_release_key_sha256_csv='$fingerprints'" \
  "$temporary_official/public-install.sh"
! cmp -s "$root_dir/scripts/public-install.sh" "$temporary_official/public-install.sh"
(cd "$temporary_official" && sha256sum --check public-install.sh.sha256 >/dev/null)

# A complete feed carries a directly signed bootstrap in addition to convenience
# checksums. The release key must be active in the trust store used for testing.
feed="$tmp/feed"
mkdir -p "$feed"
cp "$temporary_official/public-install.sh" "$feed/public-install.sh"
cp "$temporary_official/public-install.sh.sha256" "$feed/public-install.sh.sha256"
cp "$root_dir/scripts/bootstrap-install.sh" "$feed/bootstrap-install.sh"
chmod 0755 "$feed/bootstrap-install.sh"
cp "$tmp/public.b64" "$feed/release-public-key.b64"
printf '%s\n' '{}' >"$feed/release-manifest.json"
printf '%s\n' 'public-test-signature' >"$feed/release-manifest.json.sig"
"$root_dir/scripts/sign-release-asset.sh" "$feed/bootstrap-install.sh" \
  "$tmp/signing.pem" "$feed/bootstrap-install.sh.sig" >/dev/null
(
  cd "$feed"
  sha256sum bootstrap-install.sh >bootstrap-install.sh.sha256
  sha256sum release-public-key.b64 >release-public-key.b64.sha256
)
printf '%s\n' 'archive' >"$feed/hserver-panel-v0.0.0-test-linux-amd64.tar.gz"
(cd "$feed" && sha256sum hserver-panel-v0.0.0-test-linux-amd64.tar.gz \
  >hserver-panel-v0.0.0-test-linux-amd64.tar.gz.sha256)
"$root_dir/scripts/verify-public-release-assets.sh" "$feed" "$tmp/trust.json" >/dev/null

for missing in public-install.sh public-install.sh.sha256 bootstrap-install.sh.sig; do
  backup="$tmp/$missing"
  mv "$feed/$missing" "$backup"
  if "$root_dir/scripts/verify-public-release-assets.sh" "$feed" "$tmp/trust.json" \
    >"$tmp/missing.log" 2>&1; then
    printf 'release inventory accepted missing public asset: %s\n' "$missing" >&2
    exit 1
  fi
  grep -Fq 'Release asset is missing' "$tmp/missing.log"
  mv "$backup" "$feed/$missing"
done

printf '\n# tampered\n' >>"$feed/bootstrap-install.sh"
(cd "$feed" && sha256sum bootstrap-install.sh >bootstrap-install.sh.sha256)
if "$root_dir/scripts/verify-public-release-assets.sh" "$feed" "$tmp/trust.json" \
  >"$tmp/tampered.log" 2>&1; then
  echo "release inventory accepted a bootstrap with an invalid signature" >&2
  exit 1
fi
grep -Fq 'signature verification failed' "$tmp/tampered.log"

printf '%s\n' 'public release signer asset contract: OK'
