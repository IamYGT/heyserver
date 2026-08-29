#!/usr/bin/env sh
set -eu

if [ "$#" -ne 3 ]; then
  printf '%s\n' "Usage: $0 MANIFEST PRIVATE_KEY OUTPUT" >&2
  exit 2
fi

manifest=$1
private_key=$2
output=$3
command -v openssl >/dev/null 2>&1 || { printf '%s\n' "openssl is required" >&2; exit 1; }
[ -s "$manifest" ] || { printf '%s\n' "Release manifest not found: $manifest" >&2; exit 1; }
[ -s "$private_key" ] || { printf '%s\n' "Release signing key not found: $private_key" >&2; exit 1; }
[ ! -e "$output" ] || { printf '%s\n' "Refusing to overwrite: $output" >&2; exit 1; }

parent=$(dirname "$output")
mkdir -p "$parent"
umask 077
temporary=$(mktemp -d "$parent/.hserver-manifest-signature.XXXXXX")
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT INT TERM

openssl pkeyutl -sign -rawin -inkey "$private_key" -in "$manifest" -out "$temporary/signature.raw" >/dev/null 2>&1
[ "$(wc -c <"$temporary/signature.raw" | tr -d ' ')" -eq 64 ] || { printf '%s\n' "Unexpected Ed25519 signature size" >&2; exit 1; }
openssl pkey -in "$private_key" -pubout -out "$temporary/public.pem" >/dev/null 2>&1
openssl pkeyutl -verify -pubin -inkey "$temporary/public.pem" -rawin -in "$manifest" -sigfile "$temporary/signature.raw" >/dev/null 2>&1 \
  || { printf '%s\n' "Generated release signature could not be verified" >&2; exit 1; }
base64 <"$temporary/signature.raw" | tr -d '\n' >"$temporary/signature.b64"
printf '\n' >>"$temporary/signature.b64"
mv "$temporary/signature.b64" "$output"
chmod 0644 "$output"
printf 'Release manifest signature created: %s\n' "$output"
