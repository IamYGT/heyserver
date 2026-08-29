#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$(id -u)" -eq 0 ]; then
  run_fixture_install() {
    env "$@"
  }
else
  command -v sudo >/dev/null 2>&1 || {
    echo "package release test requires root or sudo; install sudo or rerun with elevated access" >&2
    exit 1
  }
  # Packaged installers must stay root-only; elevate only their disposable
  # fixture runs so the surrounding release test remains unprivileged.
  run_fixture_install() {
    sudo -- env "$@"
  }
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf '#!/bin/sh\necho panel\n' >"$tmp/panel"
printf '#!/bin/sh\necho agent\n' >"$tmp/agent"
printf '#!/bin/sh\necho cli\n' >"$tmp/cli"
chmod +x "$tmp/panel" "$tmp/agent" "$tmp/cli"

"$root_dir/scripts/package-release.sh" v0.0.0-test amd64 "$tmp/panel" "$tmp/agent" "$tmp/cli" "$tmp/dist" >/dev/null
archive="$tmp/dist/hserver-panel-v0.0.0-test-linux-amd64.tar.gz"
[ -s "$archive" ]
(cd "$tmp/dist" && sha256sum -c "$(basename "$archive").sha256" >/dev/null)
[ -x "$tmp/dist/public-install.sh" ]
[ -s "$tmp/dist/public-install.sh.sha256" ]
(cd "$tmp/dist" && sha256sum --check public-install.sh.sha256 >/dev/null)
cmp -s "$root_dir/scripts/public-install.sh" "$tmp/dist/public-install.sh"

contents=$(tar -tzf "$archive")
for expected in \
  hserver-panel-v0.0.0-test-linux-amd64/hserver-panel \
  hserver-panel-v0.0.0-test-linux-amd64/hserver-agent \
  hserver-panel-v0.0.0-test-linux-amd64/hserverctl \
  hserver-panel-v0.0.0-test-linux-amd64/install.sh \
  hserver-panel-v0.0.0-test-linux-amd64/agent-install.sh \
  hserver-panel-v0.0.0-test-linux-amd64/doctor.sh \
  hserver-panel-v0.0.0-test-linux-amd64/backup-db.sh \
  hserver-panel-v0.0.0-test-linux-amd64/restore-db.sh \
  hserver-panel-v0.0.0-test-linux-amd64/fix-env-permissions.sh \
  hserver-panel-v0.0.0-test-linux-amd64/hserver.service \
  hserver-panel-v0.0.0-test-linux-amd64/hserver-agent.service \
  hserver-panel-v0.0.0-test-linux-amd64/hserver-agent.env.example \
  hserver-panel-v0.0.0-test-linux-amd64/hserver-agent-backup-plans.example.json \
  hserver-panel-v0.0.0-test-linux-amd64/hserver-agent-deploy-plans.example.json \
  hserver-panel-v0.0.0-test-linux-amd64/deploy-templates.example/docker-compose.json \
  hserver-panel-v0.0.0-test-linux-amd64/deploy-templates.example/node-build.json \
  hserver-panel-v0.0.0-test-linux-amd64/nginx-snippets/hserver-acme-challenge.conf \
  hserver-panel-v0.0.0-test-linux-amd64/nginx-snippets/hserver-ssl-params.conf \
  hserver-panel-v0.0.0-test-linux-amd64/nginx-snippets/hserver-security-headers.conf \
  hserver-panel-v0.0.0-test-linux-amd64/nginx-snippets/hserver-security-deny.conf \
  hserver-panel-v0.0.0-test-linux-amd64/nginx-snippets/hserver-compression.conf \
  hserver-panel-v0.0.0-test-linux-amd64/nginx-snippets/hserver-static-cache.conf \
  hserver-panel-v0.0.0-test-linux-amd64/nginx-snippets/hserver-php-fpm.conf \
  hserver-panel-v0.0.0-test-linux-amd64/nginx-snippets/hserver-proxy-params.conf \
  hserver-panel-v0.0.0-test-linux-amd64/docs/installation-guide.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/agent-hub-contract.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/api-reference.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/cli.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/api-routes.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/openapi.json \
  hserver-panel-v0.0.0-test-linux-amd64/docs/docker-compose-deployments.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/database-schema.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/dns-management.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/feature-roadmap.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/frontend-architecture.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/gdrive-oauth-automation.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/gdrive-v2-checklist.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/mail-system.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/monitoring-architecture.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/native-monitoring-migration.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/portable-configuration.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/project-sustainability.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/public-launch-checklist.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/release-manifest.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/s3-snapshots.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/optional-integrations.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/extension-boundary.md \
  hserver-panel-v0.0.0-test-linux-amd64/extensions/catalog.json \
  hserver-panel-v0.0.0-test-linux-amd64/extensions/catalog.schema.json \
  hserver-panel-v0.0.0-test-linux-amd64/docs/security-hardening.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/server-inventory.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/session-history.md \
  hserver-panel-v0.0.0-test-linux-amd64/docs/troubleshooting.md \
  hserver-panel-v0.0.0-test-linux-amd64/AGENTS.md \
  hserver-panel-v0.0.0-test-linux-amd64/CONTRIBUTING.md \
  hserver-panel-v0.0.0-test-linux-amd64/CODE_OF_CONDUCT.md \
  hserver-panel-v0.0.0-test-linux-amd64/GOVERNANCE.md \
  hserver-panel-v0.0.0-test-linux-amd64/MAINTAINERS.md \
  hserver-panel-v0.0.0-test-linux-amd64/SUPPORT.md \
  hserver-panel-v0.0.0-test-linux-amd64/CHANGELOG.md \
  hserver-panel-v0.0.0-test-linux-amd64/SECURITY.md \
  hserver-panel-v0.0.0-test-linux-amd64/LICENSE
do
  printf '%s\n' "$contents" | grep -Fx "$expected" >/dev/null
done

# Keep the release archive's operator documentation in lockstep with the
# public docs tree. A newly added public document must be packaged explicitly
# instead of silently disappearing from offline installations.
for document in "$root_dir"/docs/*.md "$root_dir"/docs/*.json; do
  expected="hserver-panel-v0.0.0-test-linux-amd64/docs/$(basename "$document")"
  printf '%s\n' "$contents" | grep -Fx "$expected" >/dev/null || {
    echo "release archive is missing public documentation: $expected" >&2
    exit 1
  }
done

mkdir -p "$tmp/extracted"
tar -xzf "$archive" -C "$tmp/extracted"
package_dir="$tmp/extracted/hserver-panel-v0.0.0-test-linux-amd64"

cat >"$tmp/systemctl" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
chmod +x "$tmp/systemctl"

run_fixture_install \
  HSERVER_ROOT_PREFIX="$tmp/panel-root" \
  HSERVER_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_OS_RELEASE=/etc/os-release \
  HSERVER_SKIP_HEALTHCHECK=1 \
  "$package_dir/install.sh" install >/dev/null
cmp -s "$tmp/panel" "$tmp/panel-root/usr/local/bin/hserver-panel"
cmp -s "$tmp/cli" "$tmp/panel-root/usr/local/bin/hserverctl"
cmp -s "$package_dir/install.sh" "$tmp/panel-root/usr/local/libexec/hserver-install"
cmp -s "$package_dir/doctor.sh" "$tmp/panel-root/usr/local/libexec/hserver-doctor"
[ -f "$tmp/panel-root/etc/hserver/hserver.env" ]
for snippet in "$package_dir"/nginx-snippets/hserver-*.conf; do
  cmp -s "$snippet" "$tmp/panel-root/etc/nginx/snippets/$(basename "$snippet")"
done
for template in "$package_dir"/deploy-templates.example/*.json; do
  cmp -s "$template" "$tmp/panel-root/var/lib/hserver/deploy-templates/$(basename "$template")"
done

cat >"$tmp/agent.env" <<'EOF'
HSERVER_AGENT_HUB_URL=https://hserver.example.com
HSERVER_AGENT_NODE_ID=archive-test
HSERVER_AGENT_TOKEN_FILE=/etc/hserver-agent.token
EOF
printf '%s\n' test-only-token >"$tmp/agent.token"
run_fixture_install \
  HSERVER_AGENT_ROOT_PREFIX="$tmp/agent-root" \
  HSERVER_AGENT_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_OS_RELEASE=/etc/os-release \
  HSERVER_AGENT_SKIP_HEALTHCHECK=1 \
  "$package_dir/agent-install.sh" install \
    --config "$tmp/agent.env" \
    --token-file "$tmp/agent.token" >/dev/null
cmp -s "$tmp/agent" "$tmp/agent-root/usr/local/bin/hserver-agent"
[ "$(stat -c %a "$tmp/agent-root/etc/hserver-agent.token")" = 600 ]

"$root_dir/scripts/package-release.sh" \
  v0.0.0-verify amd64 /bin/true /bin/true /bin/true "$tmp/verify-dist" >/dev/null
"$root_dir/scripts/verify-release-archive.sh" \
  v0.0.0-verify \
  amd64 \
  "$tmp/verify-dist/hserver-panel-v0.0.0-verify-linux-amd64.tar.gz" \
  "$tmp/verify-dist/hserver-panel-v0.0.0-verify-linux-amd64.tar.gz.sha256" >/dev/null

if CI=true "$root_dir/scripts/test-native-release-lifecycle.sh" \
  v0.0.0-verify amd64 \
  "$tmp/verify-dist/hserver-panel-v0.0.0-verify-linux-amd64.tar.gz" \
  "$tmp/verify-dist/hserver-panel-v0.0.0-verify-linux-amd64.tar.gz.sha256" \
  >"$tmp/native-guard.out" 2>&1
then
  echo "native lifecycle acceptance ran without its disposable-host gate" >&2
  exit 1
fi
grep -F "Refusing native lifecycle mutation" "$tmp/native-guard.out" >/dev/null

if CI=true "$root_dir/scripts/test-native-managed-agent-lifecycle.sh" \
  v0.0.0 amd64 \
  "$tmp/verify-dist/hserver-panel-v0.0.0-verify-linux-amd64.tar.gz" \
  "$tmp/verify-dist/hserver-panel-v0.0.0-verify-linux-amd64.tar.gz.sha256" \
  v0.0.1 "$tmp/agent" \
  >"$tmp/managed-agent-guard.out" 2>&1
then
  echo "managed-agent lifecycle acceptance ran without its disposable-host gate" >&2
  exit 1
fi
grep -F "Refusing managed-agent lifecycle mutation" "$tmp/managed-agent-guard.out" >/dev/null

printf '%s\n' "release package test: OK"
