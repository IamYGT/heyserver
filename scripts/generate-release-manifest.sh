#!/usr/bin/env sh
set -eu

if [ "$#" -ne 4 ]; then
  printf '%s\n' "Usage: $0 VERSION BASE_URL PACKAGE_DIRECTORY OUTPUT" >&2
  exit 2
fi

version=$1
base_url=${2%/}
package_dir=$3
output=$4
script_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)

validate_public_url() {
  url_label=$1
  url_value=$2

  if ! python3 - "$url_value" "$url_label" <<'PY'
import sys
import unicodedata
from urllib.parse import urlsplit

value = sys.argv[1]
label = sys.argv[2]

if any(unicodedata.category(char) == "Cc" or char.isspace() for char in value):
    raise SystemExit(f"{label} URL contains whitespace or control characters")
if '"' in value or "\\" in value:
    raise SystemExit(f"{label} URL contains unsupported characters")

try:
    parts = urlsplit(value)
    hostname = parts.hostname
    # Force validation of an explicitly supplied port as well.
    parts.port
except ValueError:
    raise SystemExit(f"{label} URL is invalid")

if parts.scheme != "https" or not hostname:
    raise SystemExit(f"{label} URL must use HTTPS")
if parts.username is not None or parts.password is not None:
    raise SystemExit(f"{label} URL must not contain credentials")
PY
  then
    exit 1
  fi
}

"$script_dir/validate-release-version.sh" "$version" >/dev/null
validate_public_url "Release base" "$base_url"

release_notes_url=${HSERVER_RELEASE_NOTES_URL:-}
case "$release_notes_url" in
  '') release_notes_json='' ;;
  *)
    validate_public_url "Release notes" "$release_notes_url"
    release_notes_json=$(printf ',\n  "release_notes_url": "%s"' "$release_notes_url")
    ;;
esac

artifact_json=''
for arch in amd64 arm64; do
  archive_name="hserver-panel-${version}-linux-${arch}.tar.gz"
  archive="$package_dir/$archive_name"
  checksum="$archive.sha256"
  [ -s "$archive" ] || { printf '%s\n' "Release archive not found: $archive" >&2; exit 1; }
  [ -s "$checksum" ] || { printf '%s\n' "Release checksum not found: $checksum" >&2; exit 1; }
  expected=$(awk 'NR == 1 { print $1 }' "$checksum")
  case "$expected" in
    *[!0-9a-f]*|'') printf '%s\n' "Invalid SHA-256 in $checksum" >&2; exit 1 ;;
  esac
  [ "${#expected}" -eq 64 ] || { printf '%s\n' "Invalid SHA-256 length in $checksum" >&2; exit 1; }
  actual=$(sha256sum "$archive" | awk '{ print $1 }')
  [ "$actual" = "$expected" ] || { printf '%s\n' "Checksum mismatch for $archive" >&2; exit 1; }
  size=$(wc -c <"$archive" | tr -d ' ')
  entry=$(printf '    "linux_%s": {"url": "%s/%s", "sha256": "%s", "size_bytes": %s}' "$arch" "$base_url" "$archive_name" "$expected" "$size")
  if [ -n "$artifact_json" ]; then
    artifact_json="$artifact_json,
$entry"
  else
    artifact_json=$entry
  fi
done

published_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

mkdir -p "$(dirname "$output")"
temporary="$output.tmp.$$"
trap 'rm -f "$temporary"' EXIT INT TERM
cat >"$temporary" <<EOF
{
  "schema_version": 1,
  "version": "$version",
  "published_at": "$published_at"$release_notes_json,
  "artifacts": {
$artifact_json
  }
}
EOF
mv "$temporary" "$output"
trap - EXIT INT TERM
printf 'Release manifest created: %s\n' "$output"
