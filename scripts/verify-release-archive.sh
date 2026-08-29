#!/usr/bin/env sh
set -eu

if [ "$#" -ne 4 ]; then
  printf '%s\n' "Usage: $0 VERSION ARCH ARCHIVE CHECKSUM" >&2
  exit 1
fi

version=$1
arch=$2
archive=$3
checksum=$4

case "$version" in
  ''|*[!A-Za-z0-9._-]*) printf '%s\n' "Invalid release version: $version" >&2; exit 1 ;;
esac
case "$arch" in
  amd64) expected_machine='Advanced Micro Devices X86-64' ;;
  arm64) expected_machine='AArch64' ;;
  *) printf '%s\n' "Unsupported release architecture: $arch" >&2; exit 1 ;;
esac
[ -s "$archive" ] || { printf '%s\n' "Release archive not found: $archive" >&2; exit 1; }
[ -s "$checksum" ] || { printf '%s\n' "Release checksum not found: $checksum" >&2; exit 1; }
command -v readelf >/dev/null 2>&1 || {
  printf '%s\n' "readelf is required to verify release architecture" >&2
  exit 1
}

expected_checksum=$(awk 'NR == 1 { print $1 }' "$checksum")
actual_checksum=$(sha256sum "$archive" | awk '{ print $1 }')
[ -n "$expected_checksum" ] && [ "$actual_checksum" = "$expected_checksum" ] || {
  printf '%s\n' "Release checksum mismatch" >&2
  exit 1
}

package_name="hserver-panel-${version}-linux-${arch}"
contents=$(tar -tzf "$archive")
while IFS= read -r entry; do
  case "$entry" in
    "$package_name"|"$package_name"/*) ;;
    *) printf '%s\n' "Unexpected release archive entry: $entry" >&2; exit 1 ;;
  esac
  case "/$entry/" in
    */../*|*/./*) printf '%s\n' "Unsafe release archive entry: $entry" >&2; exit 1 ;;
  esac
done <<EOF
$contents
EOF

for required in \
  hserver-panel \
  hserver-agent \
  hserverctl \
  install.sh \
  agent-install.sh \
  doctor.sh \
  backup-db.sh \
  restore-db.sh \
  fix-env-permissions.sh \
  hserver.service \
  hserver-agent.service \
  nginx-snippets/hserver-acme-challenge.conf \
  nginx-snippets/hserver-ssl-params.conf \
  nginx-snippets/hserver-security-headers.conf \
  nginx-snippets/hserver-security-deny.conf \
  nginx-snippets/hserver-compression.conf \
  nginx-snippets/hserver-static-cache.conf \
  nginx-snippets/hserver-php-fpm.conf \
  nginx-snippets/hserver-proxy-params.conf \
  README.md \
  AGENTS.md \
  CONTRIBUTING.md \
  CODE_OF_CONDUCT.md \
  GOVERNANCE.md \
  MAINTAINERS.md \
  SECURITY.md \
  SUPPORT.md \
  CHANGELOG.md \
  LICENSE \
  VERSION \
  docs/installation-guide.md \
  docs/agent-hub-contract.md \
  docs/api-reference.md \
  docs/cli.md \
  docs/api-routes.md \
  docs/openapi.json \
  docs/docker-compose-deployments.md \
  docs/feature-roadmap.md \
  docs/frontend-architecture.md \
  docs/monitoring-architecture.md \
  docs/portable-configuration.md \
  docs/project-sustainability.md \
  docs/public-launch-checklist.md \
  docs/release-manifest.md \
  docs/s3-snapshots.md \
  docs/optional-integrations.md \
  docs/extension-boundary.md \
  extensions/catalog.json \
  extensions/catalog.schema.json \
  docs/security-hardening.md \
  docs/troubleshooting.md
do
  printf '%s\n' "$contents" | grep -Fx "$package_name/$required" >/dev/null || {
    printf '%s\n' "Release archive is missing: $required" >&2
    exit 1
  }
done

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT INT TERM
tar -xzf "$archive" -C "$work_dir"
package_dir="$work_dir/$package_name"
[ "$(cat "$package_dir/VERSION")" = "$version" ] || {
  printf '%s\n' "Release VERSION does not match $version" >&2
  exit 1
}
for binary in hserver-panel hserver-agent hserverctl; do
  [ -x "$package_dir/$binary" ] || {
    printf '%s\n' "Release binary is not executable: $binary" >&2
    exit 1
  }
  machine=$(readelf -h "$package_dir/$binary" | awk -F: '/Machine:/ { sub(/^[[:space:]]+/, "", $2); print $2; exit }')
  [ "$machine" = "$expected_machine" ] || {
    printf '%s\n' "Release binary $binary targets $machine, expected $expected_machine" >&2
    exit 1
  }
done

printf 'Release archive verified: %s (%s)\n' "$version" "$arch"
