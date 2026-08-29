#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
targets=(
  "$repo_root/.env.example"
  "$repo_root/scripts/init-env.sh"
  "$repo_root/scripts/hserver-install.sh"
  "$repo_root/deploy/hserver-agent.env.example"
  "$repo_root/scripts/hserver-agent-install.sh"
  "$repo_root/scripts/bootstrap-install.sh"
  "$repo_root/scripts/backup-db.sh"
  "$repo_root/scripts/backup-audit.sh"
  "$repo_root/scripts/gdrive-catchup-upload.sh"
  "$repo_root/scripts/gdrive-gcp-bootstrap.sh"
  "$repo_root/scripts/gdrive-healthcheck.sh"
  "$repo_root/scripts/gdrive-keepalive.sh"
  "$repo_root/scripts/verify-routes.sh"
  "$repo_root/scripts/fix-env-permissions.sh"
  "$repo_root/scripts/test-managed-agent-network-isolation.sh"
  "$repo_root/scripts/accept-provider-network-managed-agent.sh"
  "$repo_root/scripts/sign-provider-network-receipt.sh"
  "$repo_root/scripts/verify-provider-network-receipt.py"
  "$repo_root/scripts/refresh-rclone-conf/main.go"
  "$repo_root/scripts/reset-admin-password/main.go"
)

if grep -En 'ygtlabs\.com|/opt/hserver-panel|admin@ygtlabs\.com' "${targets[@]}"; then
  echo "provider-neutral script check failed" >&2
  exit 1
fi

# Public bootstrap surfaces must not silently opt into one provider's layout.
# Provider integrations and broad filesystem roots remain empty until an
# operator configures an explicit, installation-owned value.
if grep -En \
  '^[[:space:]]*(HSERVER_VHOSTS_ROOT=/var/www/vhosts|STALWART_URL=http://127\.0\.0\.1:8080|HSERVER_STALWART_(CONFIG_PATH|BIN)=/opt/stalwart|HSERVER_PM2_ALLOWED_ROOTS=/var/www/vhosts,/home,/opt|HSERVER_AGENT_PM2_BINARY=/usr/local/bin/pm2|HSERVER_AGENT_PM2_HOME=/root/\.pm2|HSERVER_AGENT_PM2_USER=root)$' \
  "${targets[@]}"; then
  echo "provider-specific runtime default found in a public bootstrap surface" >&2
  exit 1
fi

for script in "${targets[@]:0:8}"; do
  bash -n "$script"
done

bash -n "$repo_root/scripts/fix-env-permissions.sh"
bash -n "$repo_root/scripts/test-managed-agent-network-isolation.sh"
bash -n "$repo_root/scripts/accept-provider-network-managed-agent.sh"
bash -n "$repo_root/scripts/sign-provider-network-receipt.sh"
if grep -En 'systemctl|php[0-9]+\.[0-9]+-fpm|find /var/www/vhosts' "$repo_root/scripts/fix-env-permissions.sh"; then
  echo "environment permission repair script contains a fixed host layout" >&2
  exit 1
fi
grep -q 'HSERVER_VHOSTS_ROOT' "$repo_root/scripts/fix-env-permissions.sh"
grep -q -- '--check' "$repo_root/scripts/fix-env-permissions.sh"
grep -q -- '--apply' "$repo_root/scripts/fix-env-permissions.sh"

test_root=$(mktemp -d)
trap 'find "$test_root" -xdev -depth -delete' EXIT INT TERM

generated_env="$test_root/generated.env"
"$repo_root/scripts/init-env.sh" "$generated_env" >/dev/null
grep -q '^HSERVER_VHOSTS_ROOT=$' "$generated_env"
grep -q '^STALWART_URL=$' "$generated_env"
grep -q '^STALWART_ADMIN_USER=$' "$generated_env"
grep -q '^HSERVER_STALWART_CONFIG_PATH=$' "$generated_env"
grep -q '^HSERVER_STALWART_BIN=$' "$generated_env"
grep -q '^HSERVER_PM2_ALLOWED_ROOTS=$' "$generated_env"

missing_vhosts_root_log="$test_root/missing-vhosts-root.log"
if HSERVER_VHOSTS_ROOT= HSERVER_ENV_READ_GROUP="$(id -gn)" \
  "$repo_root/scripts/fix-env-permissions.sh" --check >"$missing_vhosts_root_log" 2>&1; then
  echo "environment permission check silently accepted an empty vhosts root" >&2
  exit 1
fi
grep -q '^fix-env-permissions: HSERVER_VHOSTS_ROOT must be set to an absolute path$' \
  "$missing_vhosts_root_log"

empty_vhosts="$test_root/empty"
mkdir "$empty_vhosts"
HSERVER_VHOSTS_ROOT="$empty_vhosts" HSERVER_ENV_READ_GROUP="$(id -gn)" \
  "$repo_root/scripts/fix-env-permissions.sh" --check >/dev/null
if HSERVER_VHOSTS_ROOT=relative/sites HSERVER_ENV_READ_GROUP="$(id -gn)" \
  "$repo_root/scripts/fix-env-permissions.sh" --check >/dev/null 2>&1; then
  echo "environment permission repair accepted a relative vhosts root" >&2
  exit 1
fi

if [ "$(id -u)" -eq 0 ] && id nobody >/dev/null 2>&1; then
  repair_root="$test_root/repair"
  app_dir="$repair_root/example.org"
  mkdir -p "$app_dir"
  chown nobody "$app_dir"
  : >"$app_dir/.env"
  if HSERVER_VHOSTS_ROOT="$repair_root" HSERVER_ENV_READ_GROUP="$(id -gn)" \
    "$repo_root/scripts/fix-env-permissions.sh" --check >/dev/null; then
    echo "environment permission check missed root-owned drift" >&2
    exit 1
  elif [ "$?" -ne 3 ]; then
    echo "environment permission check returned an unexpected drift status" >&2
    exit 1
  fi
  HSERVER_VHOSTS_ROOT="$repair_root" HSERVER_ENV_READ_GROUP="$(id -gn)" \
    "$repo_root/scripts/fix-env-permissions.sh" --apply >/dev/null
  [ "$(stat -c '%U:%G:%a' "$app_dir/.env")" = "nobody:$(id -gn):640" ] || {
    echo "environment permission repair did not apply the expected owner and mode" >&2
    exit 1
  }
fi

echo "provider-neutral script check passed"
