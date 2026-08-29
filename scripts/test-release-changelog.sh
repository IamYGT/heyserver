#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

cat >"$tmp/CHANGELOG.md" <<'EOF'
# Changelog

## [Unreleased]

## [1.2.3] — 2026-08-29
EOF

"$root_dir/scripts/validate-release-changelog.sh" v1.2.3 "$tmp/CHANGELOG.md" >/dev/null
"$root_dir/scripts/validate-release-changelog.sh" 1.2.3 "$tmp/CHANGELOG.md" >/dev/null

if "$root_dir/scripts/validate-release-changelog.sh" v1.2.4 "$tmp/CHANGELOG.md" >"$tmp/missing.log" 2>&1; then
  echo "changelog validator accepted a release without a matching entry" >&2
  exit 1
fi
grep -Fq 'Release changelog entry is missing for v1.2.4' "$tmp/missing.log"

if "$root_dir/scripts/validate-release-changelog.sh" v1.2.3 "$tmp/missing.md" >"$tmp/file.log" 2>&1; then
  echo "changelog validator accepted a missing changelog file" >&2
  exit 1
fi
grep -Fq 'Release changelog is missing:' "$tmp/file.log"

if "$root_dir/scripts/validate-release-changelog.sh" v1.2.3-extra "$tmp/CHANGELOG.md" >"$tmp/version.log" 2>&1; then
  echo "changelog validator accepted a prerelease version" >&2
  exit 1
fi
grep -Fq 'Release version must be stable major.minor.patch' "$tmp/version.log"

printf '%s\n' 'release changelog gate: OK'
