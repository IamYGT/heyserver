#!/usr/bin/env sh
set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  printf '%s\n' "Usage: $0 VERSION [CHANGELOG]" >&2
  exit 2
fi

version=$1
changelog=${2:-CHANGELOG.md}
script_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)

"$script_dir/validate-release-version.sh" "$version" >/dev/null
[ -f "$changelog" ] || {
  printf 'Release changelog is missing: %s\n' "$changelog" >&2
  exit 1
}

normalized=${version#v}
escaped=$(printf '%s\n' "$normalized" | sed 's/[.[\\*^$]/\\&/g')
grep -Eq "^## \\[${escaped}\\]([[:space:]]|$)" "$changelog" || {
  printf 'Release changelog entry is missing for %s (expected ## [%s])\n' \
    "$version" "$normalized" >&2
  exit 1
}

printf 'Release changelog entry verified: %s\n' "$version"
