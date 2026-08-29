#!/usr/bin/env sh
set -eu

mode=generate
if [ "$#" -eq 2 ]; then
  private_key=$1
  public_key=$2
elif [ "$#" -eq 3 ] && [ "$1" = --public-from-private ]; then
  mode=public-from-private
  private_key=$2
  public_key=$3
else
  printf '%s\n' "Usage: $0 PRIVATE_KEY PUBLIC_KEY" >&2
  printf '%s\n' "       $0 --public-from-private PRIVATE_KEY PUBLIC_KEY" >&2
  exit 2
fi

command -v openssl >/dev/null 2>&1 || { printf '%s\n' "openssl is required" >&2; exit 1; }
[ ! -e "$public_key" ] || { printf '%s\n' "Refusing to overwrite: $public_key" >&2; exit 1; }

if [ "$mode" = generate ]; then
  [ ! -e "$private_key" ] || { printf '%s\n' "Refusing to overwrite: $private_key" >&2; exit 1; }
else
  [ -f "$private_key" ] || { printf '%s\n' "Private key not found: $private_key" >&2; exit 1; }
fi

private_parent=$(dirname "$private_key")
public_parent=$(dirname "$public_key")
mkdir -p "$public_parent"
if [ "$mode" = generate ]; then
  mkdir -p "$private_parent"
fi
umask 077
temporary=$(mktemp -d "${TMPDIR:-/tmp}/hserver-release-key.XXXXXX")
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT INT TERM

if [ "$mode" = generate ]; then
  openssl genpkey -algorithm ED25519 -out "$temporary/private.pem" >/dev/null 2>&1
  private_key_source="$temporary/private.pem"
else
  private_key_source=$private_key
fi

openssl pkey -in "$private_key_source" -pubout -outform DER -out "$temporary/public.der" >/dev/null 2>&1
[ "$(wc -c <"$temporary/public.der" | tr -d ' ')" -eq 44 ] || { printf '%s\n' "Unexpected Ed25519 public key encoding" >&2; exit 1; }
prefix=$(dd if="$temporary/public.der" bs=1 count=12 2>/dev/null | od -An -tx1 | tr -d ' \n')
[ "$prefix" = "302a300506032b6570032100" ] || { printf '%s\n' "Unexpected Ed25519 public key prefix" >&2; exit 1; }
tail -c 32 "$temporary/public.der" | base64 | tr -d '\n' >"$temporary/public.b64"
printf '\n' >>"$temporary/public.b64"

mv "$temporary/public.b64" "$public_key"
chmod 0644 "$public_key"
if [ "$mode" = generate ]; then
  mv "$temporary/private.pem" "$private_key"
  chmod 0600 "$private_key"
  printf 'Release signing key created: %s\n' "$private_key"
fi
printf 'Release verification key created: %s\n' "$public_key"
