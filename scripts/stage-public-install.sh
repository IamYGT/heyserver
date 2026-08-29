#!/usr/bin/env sh
set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  printf '%s\n' "Usage: $0 RELEASE_DIRECTORY [CANONICAL_TRUST_STORE]" >&2
  exit 2
fi

output_dir=$1
trust_store=${2:-}
root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd -P)
source_file="$root_dir/scripts/public-install.sh"
public_install="$output_dir/public-install.sh"
public_install_checksum="$output_dir/public-install.sh.sha256"

[ -f "$source_file" ] || {
  printf 'Public install wrapper not found: %s\n' "$source_file" >&2
  exit 1
}
[ ! -L "$source_file" ] || {
  printf 'Public install wrapper must not be a symlink: %s\n' "$source_file" >&2
  exit 1
}
[ -x "$source_file" ] || {
  printf 'Public install wrapper is not executable: %s\n' "$source_file" >&2
  exit 1
}

command -v install >/dev/null 2>&1 || { printf '%s\n' 'install is required' >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { printf '%s\n' 'sha256sum is required' >&2; exit 1; }
if [ -n "$trust_store" ]; then
  command -v python3 >/dev/null 2>&1 || { printf '%s\n' 'python3 is required' >&2; exit 1; }
fi

install -d -m 0755 "$output_dir"
[ -d "$output_dir" ] || {
  printf 'Release directory is not a directory: %s\n' "$output_dir" >&2
  exit 1
}
for path in "$public_install" "$public_install_checksum"; do
  [ ! -L "$path" ] || {
    printf 'Refusing to replace symlink release asset: %s\n' "$path" >&2
    exit 1
  }
done

if [ -n "$trust_store" ]; then
  fingerprints=$("$root_dir/scripts/release-trust.py" "$trust_store" \
    --require-active --fingerprints)
  python3 - "$source_file" "$public_install" "$fingerprints" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
output = pathlib.Path(sys.argv[2])
fingerprints = sys.argv[3]
marker = "embedded_trusted_release_key_sha256_csv='' # HSERVER_RELEASE_TRUST_EMBED"
replacement = f"embedded_trusted_release_key_sha256_csv='{fingerprints}' # HSERVER_RELEASE_TRUST_EMBED"
if source.count(marker) != 1:
    raise SystemExit("public install trust embed marker is missing or ambiguous")
output.write_text(source.replace(marker, replacement), encoding="utf-8")
PY
  chmod 0755 "$public_install"
else
  install -m 0755 "$source_file" "$public_install"
fi
(cd "$output_dir" && sha256sum public-install.sh >public-install.sh.sha256)

[ -f "$public_install" ] && [ -s "$public_install" ] || {
  printf 'Public install wrapper was not staged: %s\n' "$public_install" >&2
  exit 1
}
[ -f "$public_install_checksum" ] && [ -s "$public_install_checksum" ] || {
  printf 'Public install wrapper checksum was not staged: %s\n' "$public_install_checksum" >&2
  exit 1
}

printf 'Public install assets staged: %s\n' "$output_dir"
