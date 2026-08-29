#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
arch=${ARCH:-$(go env GOARCH)}
native_arch=$(go env GOARCH)
native_os=$(go env GOOS)

case "$arch" in
  amd64|arm64) ;;
  *)
    printf 'Unsupported public source acceptance architecture: %s\n' "$arch" >&2
    exit 1
    ;;
esac
[ "$native_os" = linux ] || {
  printf 'Public source acceptance requires a native Linux host; current host is %s/%s. Use the release-build CI matrix for cross-platform packages.\n' "$native_os" "$native_arch" >&2
  exit 1
}
[ "$arch" = "$native_arch" ] || {
  printf 'Public source acceptance must run natively for linux/%s; current host is linux/%s. Use the release-build CI matrix for cross-CGO packages.\n' "$arch" "$native_arch" >&2
  exit 1
}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hserver-public-source-acceptance.XXXXXX")
trap 'find "$tmp" -xdev -depth -delete' EXIT INT TERM
snapshot="$tmp/source"

"$repo_root/scripts/export-public-source.sh" "$snapshot"

cd "$snapshot"
./scripts/test-extension-catalog.py
./scripts/test-public-docs.sh
./scripts/test-community-docs.sh
./scripts/test-provider-neutral-scripts.sh
./scripts/test-release-trust.sh
./scripts/test-public-install.sh
./scripts/test-public-release-assets.sh
make build
# The build above installs the locked frontend dependencies once; run both
# unit-test suites against this Git-free snapshot before packaging it.
test_runtime_root="$tmp/runtime"
mkdir -p "$test_runtime_root/backups" "$test_runtime_root/vhosts"
HSERVER_VHOSTS_ROOT="$test_runtime_root/vhosts" \
  BACKUP_DIR="$test_runtime_root/backups" \
  make test-go
make test-frontend

CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
  -ldflags "-s -w -X main.agentVersion=source-snapshot" \
  -o "bin/hserver-agent-linux-$arch" \
  ./cmd/hserver-agent

./scripts/package-release.sh \
  source-snapshot \
  "$arch" \
  bin/hserver-panel \
  "bin/hserver-agent-linux-$arch" \
  bin/hserverctl \
  dist

./scripts/verify-release-archive.sh \
  source-snapshot \
  "$arch" \
  "dist/hserver-panel-source-snapshot-linux-$arch.tar.gz" \
  "dist/hserver-panel-source-snapshot-linux-$arch.tar.gz.sha256"

printf 'public source acceptance: OK (%s)\n' "$arch"
