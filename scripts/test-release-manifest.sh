#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

version=v1.2.3
for arch in amd64 arm64; do
  archive="$tmp/hserver-panel-${version}-linux-${arch}.tar.gz"
  printf 'package-%s\n' "$arch" >"$archive"
  (cd "$tmp" && sha256sum "$(basename "$archive")" >"$(basename "$archive").sha256")
done

HSERVER_RELEASE_NOTES_URL=https://releases.example.com/v1.2.3 \
  "$root_dir/scripts/generate-release-manifest.sh" \
    "$version" \
    https://releases.example.com/download/v1.2.3 \
    "$tmp" \
    "$tmp/release-manifest.json" >/dev/null

python3 - "$tmp/release-manifest.json" <<'PY'
import json
import pathlib
import sys

document = json.loads(pathlib.Path(sys.argv[1]).read_text())
assert document["schema_version"] == 1
assert document["version"] == "v1.2.3"
assert document["release_notes_url"] == "https://releases.example.com/v1.2.3"
assert set(document["artifacts"]) == {"linux_amd64", "linux_arm64"}
for architecture, artifact in document["artifacts"].items():
    assert artifact["url"].endswith(f"hserver-panel-v1.2.3-{architecture.replace('_', '-')}.tar.gz")
    assert len(artifact["sha256"]) == 64
    assert artifact["size_bytes"] > 0
PY

assert_rejected_url() {
  name=$1
  base_url=$2
  notes_url=${3-}
  output="$tmp/rejected-$name.json"
  if HSERVER_RELEASE_NOTES_URL="$notes_url" \
    "$root_dir/scripts/generate-release-manifest.sh" \
      "$version" "$base_url" "$tmp" "$output" >"$tmp/$name.log" 2>&1; then
    printf '%s\n' "manifest generation accepted invalid $name URL" >&2
    cat "$tmp/$name.log" >&2
    exit 1
  fi
  [ ! -e "$output" ]
}

assert_rejected_url userinfo-base \
  'https://release-user:release-pass@releases.example.com/v1.2.3'
assert_rejected_url userinfo-notes \
  https://releases.example.com/v1.2.3 \
  'https://release-user:release-pass@releases.example.com/v1.2.3'
assert_rejected_url http-base \
  http://releases.example.com/v1.2.3
assert_rejected_url http-notes \
  https://releases.example.com/v1.2.3 \
  http://releases.example.com/v1.2.3
assert_rejected_url whitespace-base \
  'https://releases.example.com/release path'
assert_rejected_url whitespace-notes \
  https://releases.example.com/v1.2.3 \
  'https://releases.example.com/release path'

tab_url=$(printf 'https://releases.example.com/release\tpath')
newline_url=$(printf 'https://releases.example.com/release\npath')
control_url=$(printf 'https://releases.example.com/release\001path')
assert_rejected_url tab-base "$tab_url"
assert_rejected_url newline-base "$newline_url"
assert_rejected_url control-base "$control_url"

printf '%064d  wrong-file\n' 0 >"$tmp/hserver-panel-${version}-linux-arm64.tar.gz.sha256"
if "$root_dir/scripts/generate-release-manifest.sh" \
  "$version" https://releases.example.com "$tmp" "$tmp/invalid.json" >/dev/null 2>&1; then
  printf '%s\n' "manifest generation accepted a checksum mismatch" >&2
  exit 1
fi
[ ! -e "$tmp/invalid.json" ]

if "$root_dir/scripts/generate-release-manifest.sh" \
  v1.2.3-rc.1 https://releases.example.com "$tmp" "$tmp/prerelease.json" >/dev/null 2>&1; then
  printf '%s\n' "manifest generation accepted a prerelease version" >&2
  exit 1
fi
[ ! -e "$tmp/prerelease.json" ]

printf '%s\n' "release manifest test: OK"
