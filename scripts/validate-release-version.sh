#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  printf '%s\n' "Usage: $0 VERSION" >&2
  exit 2
fi

version=$1
if ! printf '%s\n' "$version" | grep -Eq '^v?[0-9]+\.[0-9]+\.[0-9]+$'; then
  printf '%s\n' "Release version must be stable major.minor.patch: $version" >&2
  exit 1
fi

printf '%s\n' "$version"
