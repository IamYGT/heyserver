#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
validator="$root_dir/scripts/validate-release-version.sh"

for version in 1.2.3 v1.2.3; do
  [ "$("$validator" "$version")" = "$version" ]
  make -s -C "$root_dir" release-check VERSION="$version" >/dev/null
done

for version in '' v1.2 v1.2.3-rc.1 17a28bb v1.2.3-dirty; do
  if "$validator" "$version" >/dev/null 2>&1; then
    printf '%s\n' "validator accepted an unstable release version: $version" >&2
    exit 1
  fi
  if make -s -C "$root_dir" release-check VERSION="$version" >/dev/null 2>&1; then
    printf '%s\n' "Makefile accepted an unstable release version: $version" >&2
    exit 1
  fi
done

printf '%s\n' "release version contract test: OK"
