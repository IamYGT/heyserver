#!/usr/bin/env sh
set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  printf '%s\n' "Usage: $0 RELEASE_DIRECTORY [CANONICAL_TRUST_STORE]" >&2
  exit 2
fi

release_dir=$1
trust_store=${2:-}
root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd -P)
[ -d "$release_dir" ] || {
  printf 'Release directory not found: %s\n' "$release_dir" >&2
  exit 1
}

require_asset() {
  name=$1
  path="$release_dir/$name"
  [ -f "$path" ] && [ ! -L "$path" ] && [ -s "$path" ] || {
    printf 'Release asset is missing, empty, or not a regular file: %s\n' "$name" >&2
    exit 1
  }
}

for asset in \
  bootstrap-install.sh \
  bootstrap-install.sh.sha256 \
  bootstrap-install.sh.sig \
  release-public-key.b64 \
  release-public-key.b64.sha256 \
  release-manifest.json \
  release-manifest.json.sig \
  public-install.sh \
  public-install.sh.sha256; do
  require_asset "$asset"
done

if [ -n "$trust_store" ]; then
  "$root_dir/scripts/release-trust.py" "$trust_store" --require-active \
    --assert-active-key "$release_dir/release-public-key.b64" >/dev/null
fi

"$root_dir/scripts/verify-release-asset-signature.sh" \
  "$release_dir/bootstrap-install.sh" \
  "$release_dir/bootstrap-install.sh.sig" \
  "$release_dir/release-public-key.b64" >/dev/null

command -v sha256sum >/dev/null 2>&1 || { printf '%s\n' 'sha256sum is required' >&2; exit 1; }
command -v stat >/dev/null 2>&1 || { printf '%s\n' 'stat is required' >&2; exit 1; }

for asset in bootstrap-install.sh release-public-key.b64 public-install.sh; do
  checksum="$asset.sha256"
  (cd "$release_dir" && sha256sum --check "$checksum" >/dev/null) || {
    printf 'Release asset checksum verification failed: %s\n' "$asset" >&2
    exit 1
  }
done

[ "$(stat -c '%a' "$release_dir/public-install.sh")" = 755 ] || {
  printf '%s\n' 'public-install.sh must be executable with mode 0755' >&2
  exit 1
}

archive_count=0
for archive in "$release_dir"/hserver-panel-*.tar.gz; do
  [ -f "$archive" ] || continue
  [ ! -L "$archive" ] || {
    printf 'Release archive must not be a symlink: %s\n' "$(basename "$archive")" >&2
    exit 1
  }
  archive_name=$(basename "$archive")
  checksum="$release_dir/$archive_name.sha256"
  [ -f "$checksum" ] && [ ! -L "$checksum" ] && [ -s "$checksum" ] || {
    printf 'Release archive checksum is missing: %s\n' "$archive_name.sha256" >&2
    exit 1
  }
  (cd "$release_dir" && sha256sum --check "$archive_name.sha256" >/dev/null) || {
    printf 'Release archive checksum verification failed: %s\n' "$archive_name" >&2
    exit 1
  }
  archive_count=$((archive_count + 1))
done
[ "$archive_count" -gt 0 ] || {
  printf '%s\n' 'Release package is missing an architecture archive' >&2
  exit 1
}

printf 'Public release asset inventory verified: %s\n' "$release_dir"
