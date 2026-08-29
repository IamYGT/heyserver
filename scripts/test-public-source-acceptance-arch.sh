#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hserver-public-source-arch-test.XXXXXX")
trap 'find "$tmp" -xdev -depth -delete' EXIT INT TERM
mkdir -p "$tmp/bin"

cat >"$tmp/bin/go" <<'SH'
#!/usr/bin/env sh
set -eu
if [ "$#" -eq 2 ] && [ "$1" = env ] && [ "$2" = GOARCH ]; then
  printf '%s\n' "${FAKE_GOARCH:?}"
  exit 0
fi
if [ "$#" -eq 2 ] && [ "$1" = env ] && [ "$2" = GOOS ]; then
  printf '%s\n' "${FAKE_GOOS:?}"
  exit 0
fi
printf 'unexpected fake go invocation: %s\n' "$*" >&2
exit 1
SH
chmod +x "$tmp/bin/go"

assert_rejected() {
  expected=$1
  shift
  if env PATH="$tmp/bin:$PATH" "$@" >"$tmp/output" 2>&1; then
    printf 'public source acceptance unexpectedly accepted a non-native target\n' >&2
    exit 1
  fi
  grep -F "$expected" "$tmp/output" >/dev/null
}

assert_rejected \
  'Public source acceptance must run natively for linux/arm64; current host is linux/amd64. Use the release-build CI matrix for cross-CGO packages.' \
  FAKE_GOARCH=amd64 FAKE_GOOS=linux ARCH=arm64 \
  "$repo_root/scripts/test-public-source-acceptance.sh"

assert_rejected \
  'Public source acceptance requires a native Linux host; current host is darwin/arm64. Use the release-build CI matrix for cross-platform packages.' \
  FAKE_GOARCH=arm64 FAKE_GOOS=darwin ARCH=arm64 \
  "$repo_root/scripts/test-public-source-acceptance.sh"

assert_rejected \
  'Unsupported public source acceptance architecture: riscv64' \
  FAKE_GOARCH=amd64 FAKE_GOOS=linux ARCH=riscv64 \
  "$repo_root/scripts/test-public-source-acceptance.sh"

printf '%s\n' 'public source acceptance native architecture gate: OK'
