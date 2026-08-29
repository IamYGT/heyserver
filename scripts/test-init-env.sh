#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
generated="$tmp/generated.env"

output=$("$repo_root/scripts/init-env.sh" "$generated")
[[ -f "$generated" ]]
[[ $(stat -c %a "$generated") == 600 ]]
grep -q '^Environment file created with mode 0600:' <<<"$output"
grep -q '^Initial credentials are stored in that file and were not printed.$' <<<"$output"

for secret_key in HSERVER_JWT_SECRET HSERVER_ADMIN_PASS HSERVER_CRON_SECRET; do
  value=$(sed -n "s/^${secret_key}=//p" "$generated")
  [[ -n "$value" ]]
  if grep -Fq "$value" <<<"$output"; then
    echo "$secret_key leaked in init-env output" >&2
    exit 1
  fi
done

sed -n 's/^\([A-Z0-9_]*\)=.*/\1/p' "$repo_root/.env.example" | sort >"$tmp/example.keys"
sed -n 's/^\([A-Z0-9_]*\)=.*/\1/p' "$generated" | sort >"$tmp/generated.keys"
diff -u "$tmp/example.keys" "$tmp/generated.keys"
diff -u \
  <(grep -E '^HSERVER_UPDATE_(PANEL|CLI)_BINARY_PATH=' "$repo_root/.env.example") \
  <(grep -E '^HSERVER_UPDATE_(PANEL|CLI)_BINARY_PATH=' "$generated")
grep -q '^HSERVER_PGM_BACKUP_DIR=/var/lib/hserver/pgm-backups$' "$generated"
grep -q '^HSERVER_PHP_CONFIG_ROOT=/etc/php$' "$generated"
grep -q '^HSERVER_PHP_BINARY_ROOT=/usr/sbin$' "$generated"

if "$repo_root/scripts/init-env.sh" "$generated" >"$tmp/overwrite.log" 2>&1; then
  echo "init-env unexpectedly overwrote an existing file" >&2
  exit 1
fi
grep -q 'Refusing to overwrite existing environment file' "$tmp/overwrite.log"

for key in $(cat "$tmp/example.keys"); do
  case "$key" in
    HSERVER_UPDATE_PANEL_BINARY_PATH|HSERVER_UPDATE_CLI_BINARY_PATH)
      # Native update destinations are intentionally not part of the Docker evaluation environment.
      continue
      ;;
  esac
  if [[ "$key" == HSERVER_VHOSTS_ROOT ]]; then
    grep -q '^      HSERVER_VHOSTS_ROOT: /app/data/vhosts$' "$repo_root/docker-compose.yml"
  else
    grep -Fq "\${${key}" "$repo_root/docker-compose.yml"
  fi
done

for key in STALWART_URL STALWART_ADMIN_USER HSERVER_STALWART_SERVICE HSERVER_STALWART_CONFIG_PATH HSERVER_STALWART_BIN HSERVER_PM2_ALLOWED_ROOTS; do
  grep -Fxq "      ${key}: \${${key}:-}" "$repo_root/docker-compose.yml"
done

if grep -Eq '^[[:space:]]*(container_name|image):' "$repo_root/docker-compose.yml"; then
  echo "evaluation compose file contains a global container or image identity" >&2
  exit 1
fi
if grep -Eq '^[[:space:]]+name:[[:space:]]*hserver-panel-data[[:space:]]*$' "$repo_root/docker-compose.yml"; then
  echo "evaluation compose volume is not project-scoped" >&2
  exit 1
fi

echo "environment bootstrap contract test: OK"
