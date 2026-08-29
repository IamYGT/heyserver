#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

printf '%s\n' '{"schema_version":1,"version":"v1.2.3","artifacts":{}}' >"$tmp/release-manifest.json"
"$root_dir/scripts/generate-release-signing-key.sh" "$tmp/signing.pem" "$tmp/public.b64" >/dev/null
"$root_dir/scripts/generate-release-signing-key.sh" \
  --public-from-private "$tmp/signing.pem" "$tmp/derived-public.b64" >/dev/null
cmp "$tmp/public.b64" "$tmp/derived-public.b64"
[ "$(stat -c '%a' "$tmp/derived-public.b64")" = "644" ]
"$root_dir/scripts/sign-release-manifest.sh" "$tmp/release-manifest.json" "$tmp/signing.pem" "$tmp/release-manifest.json.sig" >/dev/null

[ "$(stat -c '%a' "$tmp/signing.pem")" = "600" ]
[ "$(base64 -d <"$tmp/public.b64" | wc -c | tr -d ' ')" -eq 32 ]
[ "$(base64 -d <"$tmp/release-manifest.json.sig" | wc -c | tr -d ' ')" -eq 64 ]
openssl pkey -in "$tmp/signing.pem" -pubout -out "$tmp/public.pem" >/dev/null 2>&1
base64 -d <"$tmp/release-manifest.json.sig" >"$tmp/signature.raw"
openssl pkeyutl -verify -pubin -inkey "$tmp/public.pem" -rawin -in "$tmp/release-manifest.json" -sigfile "$tmp/signature.raw" >/dev/null 2>&1

printf '\n' >>"$tmp/release-manifest.json"
if openssl pkeyutl -verify -pubin -inkey "$tmp/public.pem" -rawin -in "$tmp/release-manifest.json" -sigfile "$tmp/signature.raw" >/dev/null 2>&1; then
  printf '%s\n' "release signature accepted a modified manifest" >&2
  exit 1
fi
if "$root_dir/scripts/sign-release-manifest.sh" "$tmp/release-manifest.json" "$tmp/signing.pem" "$tmp/release-manifest.json.sig" >/dev/null 2>&1; then
  printf '%s\n' "release signing overwrote an existing signature" >&2
  exit 1
fi
if "$root_dir/scripts/generate-release-signing-key.sh" \
  --public-from-private "$tmp/signing.pem" "$tmp/derived-public.b64" >/dev/null 2>&1; then
  printf '%s\n' "release public key extraction overwrote an existing asset" >&2
  exit 1
fi
printf '%s\n' 'not a private key' >"$tmp/invalid-private.pem"
if "$root_dir/scripts/generate-release-signing-key.sh" \
  --public-from-private "$tmp/invalid-private.pem" "$tmp/invalid-public.b64" >/dev/null 2>&1; then
  printf '%s\n' "release public key extraction accepted an invalid private key" >&2
  exit 1
fi
[ ! -e "$tmp/invalid-public.b64" ]

printf '%s\n' "release signing test: OK"
