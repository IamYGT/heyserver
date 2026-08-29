#!/usr/bin/env sh
set -eu

if [ "$#" -ne 3 ]; then
  printf '%s\n' "Usage: $0 ASSET SIGNATURE PUBLIC_KEY" >&2
  exit 2
fi

asset=$1
signature=$2
public_key=$3
for path in "$asset" "$signature" "$public_key"; do
  [ -f "$path" ] && [ ! -L "$path" ] && [ -s "$path" ] || {
    printf 'Release signature input is missing, empty, or not a regular file: %s\n' "$path" >&2
    exit 1
  }
done
command -v python3 >/dev/null 2>&1 || { printf '%s\n' "python3 is required" >&2; exit 1; }
command -v openssl >/dev/null 2>&1 || { printf '%s\n' "openssl is required" >&2; exit 1; }

tmp=$(mktemp -d "${TMPDIR:-/tmp}/hserver-release-signature.XXXXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM
python3 - "$public_key" "$signature" "$tmp/public.der" "$tmp/signature.raw" <<'PY'
import base64
import binascii
import pathlib
import sys

key_path = pathlib.Path(sys.argv[1])
signature_path = pathlib.Path(sys.argv[2])
key_output = pathlib.Path(sys.argv[3])
signature_output = pathlib.Path(sys.argv[4])

def decode_one_line(path, label, size):
    raw = path.read_bytes()
    if raw.endswith(b"\n"):
        raw = raw[:-1]
    if not raw or b"\n" in raw or b"\r" in raw:
        raise SystemExit(f"{label} must contain one ASCII base64 line")
    try:
        decoded = base64.b64decode(raw, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise SystemExit(f"{label} is not valid base64") from exc
    if len(decoded) != size or base64.b64encode(decoded) != raw:
        raise SystemExit(f"{label} must be canonical base64 for exactly {size} bytes")
    return decoded

key = decode_one_line(key_path, "release public key", 32)
signature = decode_one_line(signature_path, "release signature", 64)
key_output.write_bytes(bytes.fromhex("302a300506032b6570032100") + key)
signature_output.write_bytes(signature)
PY
openssl pkeyutl -verify -pubin -keyform DER -inkey "$tmp/public.der" \
  -rawin -in "$asset" -sigfile "$tmp/signature.raw" >/dev/null 2>&1 || {
    printf 'Release asset signature verification failed: %s\n' "$asset" >&2
    exit 1
  }
printf 'Release asset signature verified: %s\n' "$asset"
