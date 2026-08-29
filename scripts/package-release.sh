#!/usr/bin/env sh
set -eu

if [ "$#" -ne 6 ]; then
  printf '%s\n' "Usage: $0 VERSION ARCH PANEL_BINARY AGENT_BINARY CLI_BINARY OUTPUT_DIR" >&2
  exit 1
fi

version=$1
arch=$2
panel_binary=$3
agent_binary=$4
cli_binary=$5
output_dir=$6

case "$version" in
  ''|*[!A-Za-z0-9._-]*) printf '%s\n' "Invalid release version: $version" >&2; exit 1 ;;
esac
case "$arch" in
  amd64|arm64) ;;
  *) printf '%s\n' "Unsupported release architecture: $arch" >&2; exit 1 ;;
esac
[ -s "$panel_binary" ] || { printf '%s\n' "Panel binary not found: $panel_binary" >&2; exit 1; }
[ -s "$agent_binary" ] || { printf '%s\n' "Agent binary not found: $agent_binary" >&2; exit 1; }
[ -s "$cli_binary" ] || { printf '%s\n' "CLI binary not found: $cli_binary" >&2; exit 1; }

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
package_name="hserver-panel-${version}-linux-${arch}"
archive_name="${package_name}.tar.gz"
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT INT TERM
package_dir="$work_dir/$package_name"

install -d -m 0755 "$package_dir/docs" "$package_dir/extensions" "$package_dir/nginx-snippets" "$package_dir/deploy-templates.example"
install -m 0755 "$panel_binary" "$package_dir/hserver-panel"
install -m 0755 "$agent_binary" "$package_dir/hserver-agent"
install -m 0755 "$cli_binary" "$package_dir/hserverctl"
install -m 0755 "$root_dir/scripts/hserver-install.sh" "$package_dir/install.sh"
install -m 0755 "$root_dir/scripts/hserver-agent-install.sh" "$package_dir/agent-install.sh"
install -m 0755 "$root_dir/scripts/hserver-doctor.sh" "$package_dir/doctor.sh"
install -m 0755 "$root_dir/scripts/backup-db.sh" "$package_dir/backup-db.sh"
install -m 0755 "$root_dir/scripts/restore-db.sh" "$package_dir/restore-db.sh"
install -m 0755 "$root_dir/scripts/fix-env-permissions.sh" "$package_dir/fix-env-permissions.sh"
install -m 0644 "$root_dir/deploy/hserver.service" "$package_dir/hserver.service"
install -m 0644 "$root_dir/deploy/hserver-agent.service" "$package_dir/hserver-agent.service"
install -m 0644 "$root_dir/deploy/hserver-agent.env.example" "$package_dir/hserver-agent.env.example"
install -m 0644 "$root_dir/deploy/hserver-agent-backup-plans.example.json" "$package_dir/hserver-agent-backup-plans.example.json"
install -m 0644 "$root_dir/deploy/hserver-agent-deploy-plans.example.json" "$package_dir/hserver-agent-deploy-plans.example.json"
for template in "$root_dir"/deploy/hserver-deploy-templates.example/*.json; do
  [ -f "$template" ] || { printf '%s\n' "Deployment template examples are missing" >&2; exit 1; }
  install -m 0644 "$template" "$package_dir/deploy-templates.example/$(basename "$template")"
done
for snippet in "$root_dir"/deploy/nginx-snippets/hserver-*.conf; do
  [ -f "$snippet" ] || { printf '%s\n' "Managed Nginx snippets are missing" >&2; exit 1; }
  install -m 0644 "$snippet" "$package_dir/nginx-snippets/$(basename "$snippet")"
done
install -m 0644 "$root_dir/README.md" "$package_dir/README.md"
install -m 0644 "$root_dir/AGENTS.md" "$package_dir/AGENTS.md"
install -m 0644 "$root_dir/CONTRIBUTING.md" "$package_dir/CONTRIBUTING.md"
install -m 0644 "$root_dir/CODE_OF_CONDUCT.md" "$package_dir/CODE_OF_CONDUCT.md"
install -m 0644 "$root_dir/GOVERNANCE.md" "$package_dir/GOVERNANCE.md"
install -m 0644 "$root_dir/MAINTAINERS.md" "$package_dir/MAINTAINERS.md"
install -m 0644 "$root_dir/SECURITY.md" "$package_dir/SECURITY.md"
install -m 0644 "$root_dir/SUPPORT.md" "$package_dir/SUPPORT.md"
install -m 0644 "$root_dir/CHANGELOG.md" "$package_dir/CHANGELOG.md"
install -m 0644 "$root_dir/docs/installation-guide.md" "$package_dir/docs/installation-guide.md"
install -m 0644 "$root_dir/docs/agent-hub-contract.md" "$package_dir/docs/agent-hub-contract.md"
install -m 0644 "$root_dir/docs/api-reference.md" "$package_dir/docs/api-reference.md"
install -m 0644 "$root_dir/docs/cli.md" "$package_dir/docs/cli.md"
install -m 0644 "$root_dir/docs/api-routes.md" "$package_dir/docs/api-routes.md"
install -m 0644 "$root_dir/docs/openapi.json" "$package_dir/docs/openapi.json"
install -m 0644 "$root_dir/docs/docker-compose-deployments.md" "$package_dir/docs/docker-compose-deployments.md"
install -m 0644 "$root_dir/docs/database-schema.md" "$package_dir/docs/database-schema.md"
install -m 0644 "$root_dir/docs/dns-management.md" "$package_dir/docs/dns-management.md"
install -m 0644 "$root_dir/docs/feature-roadmap.md" "$package_dir/docs/feature-roadmap.md"
install -m 0644 "$root_dir/docs/frontend-architecture.md" "$package_dir/docs/frontend-architecture.md"
install -m 0644 "$root_dir/docs/gdrive-oauth-automation.md" "$package_dir/docs/gdrive-oauth-automation.md"
install -m 0644 "$root_dir/docs/gdrive-v2-checklist.md" "$package_dir/docs/gdrive-v2-checklist.md"
install -m 0644 "$root_dir/docs/mail-system.md" "$package_dir/docs/mail-system.md"
install -m 0644 "$root_dir/docs/monitoring-architecture.md" "$package_dir/docs/monitoring-architecture.md"
install -m 0644 "$root_dir/docs/native-monitoring-migration.md" "$package_dir/docs/native-monitoring-migration.md"
install -m 0644 "$root_dir/docs/portable-configuration.md" "$package_dir/docs/portable-configuration.md"
install -m 0644 "$root_dir/docs/project-sustainability.md" "$package_dir/docs/project-sustainability.md"
install -m 0644 "$root_dir/docs/public-launch-checklist.md" "$package_dir/docs/public-launch-checklist.md"
install -m 0644 "$root_dir/docs/release-manifest.md" "$package_dir/docs/release-manifest.md"
install -m 0644 "$root_dir/docs/s3-snapshots.md" "$package_dir/docs/s3-snapshots.md"
install -m 0644 "$root_dir/docs/optional-integrations.md" "$package_dir/docs/optional-integrations.md"
install -m 0644 "$root_dir/docs/extension-boundary.md" "$package_dir/docs/extension-boundary.md"
install -m 0644 "$root_dir/extensions/catalog.json" "$package_dir/extensions/catalog.json"
install -m 0644 "$root_dir/extensions/catalog.schema.json" "$package_dir/extensions/catalog.schema.json"
install -m 0644 "$root_dir/docs/security-hardening.md" "$package_dir/docs/security-hardening.md"
install -m 0644 "$root_dir/docs/server-inventory.md" "$package_dir/docs/server-inventory.md"
install -m 0644 "$root_dir/docs/session-history.md" "$package_dir/docs/session-history.md"
install -m 0644 "$root_dir/docs/troubleshooting.md" "$package_dir/docs/troubleshooting.md"
install -m 0644 "$root_dir/LICENSE" "$package_dir/LICENSE"
printf '%s\n' "$version" >"$package_dir/VERSION"

install -d -m 0755 "$output_dir"
tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
  -cf - -C "$work_dir" "$package_name" | gzip -n >"$output_dir/$archive_name"
(
  cd "$output_dir"
  sha256sum "$archive_name" >"$archive_name.sha256"
)
"$root_dir/scripts/stage-public-install.sh" "$output_dir" >/dev/null

printf 'Release archive: %s\n' "$output_dir/$archive_name"
printf 'Checksum: %s\n' "$output_dir/$archive_name.sha256"
