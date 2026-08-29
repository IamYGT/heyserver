#!/usr/bin/env sh
set -eu

if [ "$#" -ne 3 ]; then
  printf '%s\n' "Usage: $0 RECEIPT PRIVATE_KEY SIGNATURE" >&2
  exit 2
fi

receipt=$1
private_key=$2
output=$3

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

command -v openssl >/dev/null 2>&1 || fail "openssl is required"
[ -e "$receipt" ] || fail "Provider-network receipt not found: $receipt"
[ ! -L "$receipt" ] && [ -f "$receipt" ] || fail "Provider-network receipt must be a regular file and not a symlink"
[ "$(stat -c '%a' "$receipt")" = "600" ] || fail "Provider-network receipt must have mode 0600"
[ "$(stat -c '%u' "$receipt")" = "$(id -u)" ] || fail "Provider-network receipt must be owned by the current user"
[ -s "$private_key" ] || fail "Receipt signing key not found: $private_key"
[ ! -L "$private_key" ] && [ -f "$private_key" ] || fail "Receipt signing key must be a regular file and not a symlink"
[ "$(stat -c '%a' "$private_key")" = "600" ] || fail "Receipt signing key must have mode 0600"
[ "$(stat -c '%u' "$private_key")" = "$(id -u)" ] || fail "Receipt signing key must be owned by the current user"
[ ! -e "$output" ] && [ ! -L "$output" ] || fail "Refusing to overwrite: $output"

parent=$(dirname "$output")
[ -d "$parent" ] || fail "Signature directory does not exist: $parent"
umask 077
temporary=$(mktemp -d "$parent/.hserver-provider-receipt-signature.XXXXXX")
cleanup() {
  find "$temporary" -xdev -depth -delete
}
trap cleanup EXIT INT TERM

openssl pkeyutl -sign -rawin -inkey "$private_key" -in "$receipt" -out "$temporary/signature.raw" >/dev/null 2>&1 \
  || fail "Could not sign provider-network receipt with the supplied Ed25519 key"
[ "$(wc -c <"$temporary/signature.raw" | tr -d ' ')" -eq 64 ] || fail "Unexpected Ed25519 signature size"
openssl pkey -in "$private_key" -pubout -out "$temporary/public.pem" >/dev/null 2>&1 \
  || fail "Could not derive the Ed25519 verification key"
openssl pkeyutl -verify -pubin -inkey "$temporary/public.pem" -rawin -in "$receipt" -sigfile "$temporary/signature.raw" >/dev/null 2>&1 \
  || fail "Generated provider-network receipt signature could not be verified"
base64 <"$temporary/signature.raw" | tr -d '\n' >"$temporary/signature.b64"
printf '\n' >>"$temporary/signature.b64"
mv "$temporary/signature.b64" "$output"
chmod 0644 "$output"
printf 'Provider-network receipt signature created: %s\n' "$output"
