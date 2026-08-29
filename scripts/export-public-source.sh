#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
destination=${1:-}

if [[ -z "$destination" ]]; then
  echo "Usage: $0 DESTINATION" >&2
  exit 2
fi
if [[ -e "$destination" ]]; then
  echo "Refusing to overwrite existing destination: $destination" >&2
  exit 1
fi
if [[ -n "$(git -C "$repo_root" status --porcelain)" ]]; then
  echo "Refusing to export a dirty worktree; commit or remove pending changes first." >&2
  exit 1
fi

parent=$(dirname "$destination")
mkdir -p "$parent"
staging=$(mktemp -d "$parent/.hserver-public-source.XXXXXX")
cleanup() {
  if [[ -d "$staging" ]]; then
    rm -rf "$staging"
  fi
}
trap cleanup EXIT INT TERM

git -C "$repo_root" archive --format=tar HEAD | tar -xf - -C "$staging"
if [[ -e "$staging/.git" ]]; then
  echo "Export unexpectedly contains Git metadata." >&2
  exit 1
fi
for required in README.md LICENSE SECURITY.md SUPPORT.md AGENTS.md; do
  if [[ ! -f "$staging/$required" ]]; then
    echo "Export is missing required public file: $required" >&2
    exit 1
  fi
done
if [[ ! -x "$staging/scripts/test-public-docs.sh" ]]; then
  echo "Export is missing the executable public inventory validator." >&2
  exit 1
fi
"$staging/scripts/test-public-docs.sh"

mv "$staging" "$destination"
trap - EXIT INT TERM
printf 'Public source snapshot created: %s\n' "$destination"
printf 'Source commit: %s\n' "$(git -C "$repo_root" rev-parse HEAD)"
