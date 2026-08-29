#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
inventory_pattern='ygtlabs\.(com|ai)|ecutuningportal\.com|49\.12\.188\.137|admin@ygtlabs\.com|Hserver2026|/opt/hserver-panel/data|pm2-yigit|yigit user|removed from this server as of|when Plesk was the server manager'

inventory_found=0
git_tree=0
scan_report=$(mktemp)
trap 'unlink "$scan_report"' EXIT INT TERM
if git -C "$repo_root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git_tree=1
  if git -C "$repo_root" grep -IEn "$inventory_pattern" \
    -- . ':(exclude)scripts/test-public-docs.sh' >"$scan_report"; then
    scan_status=0
  else
    scan_status=$?
  fi
else
  if grep -RInI -E "$inventory_pattern" \
    --exclude=test-public-docs.sh \
    --exclude-dir=node_modules \
    --exclude-dir=bin \
    -- "$repo_root" >"$scan_report"; then
    scan_status=0
  else
    scan_status=$?
  fi
fi
case "$scan_status" in
  0)
    cat "$scan_report"
    inventory_found=1
    ;;
  1) ;;
  *)
    cat "$scan_report" >&2
    echo "public source inventory scan failed" >&2
    exit 1
    ;;
esac
if (( inventory_found )); then
  echo "public source contains installation-specific inventory" >&2
  exit 1
fi

artifact_found=0
if (( git_tree )); then
  [[ -n "$(git -C "$repo_root" ls-files .superpowers hserver)" ]] && artifact_found=1
elif [[ -e "$repo_root/.superpowers" || -e "$repo_root/hserver" ]]; then
  artifact_found=1
fi
if (( artifact_found )); then
  echo "public source contains local tool state or a root-level build artifact" >&2
  exit 1
fi

installation_guide="$repo_root/docs/installation-guide.md"
release_manifest="$repo_root/docs/release-manifest.md"
trust_readme="$repo_root/trust/README.md"
api_reference="$repo_root/docs/api-reference.md"
cli_guide="$repo_root/docs/cli.md"
mail_guide="$repo_root/docs/mail-system.md"
readme="$repo_root/README.md"
contributing="$repo_root/CONTRIBUTING.md"
optional_integrations="$repo_root/docs/optional-integrations.md"
extension_boundary="$repo_root/docs/extension-boundary.md"
telegram_root="$repo_root/integrations/hserver-telegram-bot"
telegram_readme="$telegram_root/README.md"
telegram_env="$telegram_root/.env.example"
telegram_unit="$telegram_root/deploy/hserver-telegram-bot.service"
telegram_config="$telegram_root/src/hserver_bot/config.py"
telegram_digest="$telegram_root/src/hserver_bot/services/digest.py"
for document in "$installation_guide" "$release_manifest" "$trust_readme" "$api_reference" "$cli_guide" "$mail_guide" "$readme" "$contributing" "$optional_integrations" "$extension_boundary"; do
  if [[ ! -f "$document" ]]; then
    echo "public source is missing required documentation: $document" >&2
    exit 1
  fi
done

# Manifest-backed update mutations are fail-closed. Discovery may be checksum
# only, but panel stage/install and managed-agent upgrade require a verified
# detached signature; keep the public contract synchronized across its three
# owning documents.
for update_contract in \
  'read-only' \
  'checksum-only' \
  'signature_status=verified' \
  'empty trust' \
  'automatic or unattended updater' \
  'rollback boundary'; do
  for document in "$installation_guide" "$release_manifest" "$trust_readme"; do
    if ! grep -Fq -- "$update_contract" "$document"; then
      echo "signed update documentation is missing $update_contract: $document" >&2
      exit 1
    fi
  done
done
for mutation_contract in \
  'panel stage/install' \
  'managed-agent upgrade'; do
  for document in "$installation_guide" "$release_manifest" "$trust_readme"; do
    if ! grep -Fiq -- "$mutation_contract" "$document"; then
      echo "signed update documentation is missing mutation gate: $mutation_contract: $document" >&2
      exit 1
    fi
  done
done

for contributor_test_contract in \
  "go test ./internal/releaseversion -run '^TestCompareStableReleases$' -count=1" \
  'npm --prefix web test -- src/lib/chunkErrors.test.ts' \
  'nearest test for the code you changed' \
  '`make ci-fast`' \
  '`make ci-pr`'; do
  if ! grep -Fq -- "$contributor_test_contract" "$contributing"; then
    echo "contributor documentation is missing focused-test guidance: $contributor_test_contract" >&2
    exit 1
  fi
done

for telegram_file in "$telegram_readme" "$telegram_env" "$telegram_unit" "$telegram_config" "$telegram_digest"; do
  if [[ ! -f "$telegram_file" ]]; then
    echo "public source is missing the Telegram extension portability contract: $telegram_file" >&2
    exit 1
  fi
done

telegram_contract_files=(
  "$telegram_readme"
  "$telegram_env"
  "$telegram_unit"
  "$telegram_config"
  "$telegram_digest"
)
for forbidden_telegram_default in \
  '/opt/hserver-telegram-bot' \
  'User=root' \
  'Group=root' \
  'IamYGT' \
  'ygtlabs'; do
  if grep -Fq -- "$forbidden_telegram_default" "${telegram_contract_files[@]}"; then
    echo "Telegram extension contains a provider-specific default: $forbidden_telegram_default" >&2
    exit 1
  fi
done

for telegram_contract in \
  'HSERVER_BOT_HOME' \
  'HSERVER_BOT_DATA_DIR'; do
  if ! grep -Fq -- "$telegram_contract" "$telegram_env" "$telegram_config" "$telegram_digest"; then
    echo "Telegram extension is missing its path contract: $telegram_contract" >&2
    exit 1
  fi
done
for telegram_service_contract in \
  'EnvironmentFile=/etc/hserver/hserver-telegram-bot.env' \
  'User=hserver-telegram-bot' \
  'Group=hserver-telegram-bot'; do
  if ! grep -Fq -- "$telegram_service_contract" "$telegram_unit"; then
    echo "Telegram systemd unit is missing its portable service contract: $telegram_service_contract" >&2
    exit 1
  fi
done

for catalog_document in "$optional_integrations" "$extension_boundary"; do
  for catalog_contract in \
    '[`extensions/catalog.json`](../extensions/catalog.json)' \
    '[`extensions/catalog.schema.json`](../extensions/catalog.schema.json)' \
    'authoritative' \
    'machine-readable'; do
    if ! grep -Fq -- "$catalog_contract" "$catalog_document"; then
      echo "public documentation is missing the extension catalog contract: $catalog_contract" >&2
      exit 1
    fi
  done
done

if ! grep -Fq '[installation guide](docs/installation-guide.md)' "$readme" ||
   ! grep -Fq '[optional integrations matrix](docs/optional-integrations.md)' "$readme" ||
   ! grep -Fq '[optional integrations matrix](optional-integrations.md)' "$installation_guide"; then
  echo "public documentation links are incomplete" >&2
  exit 1
fi

prerequisite_install_lines=$(awk '
  /^## 2\. Choose an Installation Source$/ { exit }
  /apt[[:space:]]+install/ { print }
' "$installation_guide")
if grep -Eiq '(^|[^[:alnum:]_-])(nginx|certbot|fail2ban)([^[:alnum:]_-]|$)' \
  <<<"$prerequisite_install_lines"; then
  printf '%s\n' "$prerequisite_install_lines" >&2
  echo "core prerequisite documentation installs an optional provider" >&2
  exit 1
fi

for provider_heading in \
  '## 7. Nginx Reverse Proxy (Optional)' \
  '## 8. SSL Certificate (Optional)' \
  '### Fail2Ban security integration (Optional)'; do
  if ! grep -Fqx "$provider_heading" "$installation_guide"; then
    echo "optional provider heading is missing: $provider_heading" >&2
    exit 1
  fi
done
for provider_install in \
  'apt install -y nginx' \
  'apt install -y certbot python3-certbot-nginx' \
  'apt install -y fail2ban'; do
  if ! grep -Fq "$provider_install" "$installation_guide"; then
    echo "optional provider installation step is missing: $provider_install" >&2
    exit 1
  fi
done

if grep -Eq 'curl[^|]*\|[[:space:]]*(sudo[[:space:]]+)?(bash|sh)' \
  "$readme" "$installation_guide"; then
  echo "public documentation executes an unverified download through a shell" >&2
  exit 1
fi
for bootstrap_asset in \
  'bootstrap-install.sh.sha256' \
  'bootstrap-install.sh.sig' \
  'release-public-key.b64' \
  'release-public-key.b64.sha256' \
  'sha256sum --check bootstrap-install.sh.sha256' \
  'sha256sum --check release-public-key.b64.sha256' \
  '--public-key-file ./release-public-key.b64'; do
  if ! grep -Fq -- "$bootstrap_asset" "$readme" ||
     ! grep -Fq -- "$bootstrap_asset" "$installation_guide"; then
    echo "verified bootstrap documentation is missing: $bootstrap_asset" >&2
    exit 1
  fi
done

for public_wrapper_contract in \
  'public-install.sh' \
  'public-install.sh.sha256' \
  'release_base=https://github.com/OWNER/REPOSITORY/releases/download/$release_version' \
  'installer_commit=IMMUTABLE_PUBLIC_COMMIT_SHA' \
  'trusted_installer_sha256=LOWERCASE_SHA256_FROM_AN_INDEPENDENT_SOURCE' \
  'trusted_release_key_sha256=LOWERCASE_SHA256_OF_RAW_ED25519_PUBLIC_KEY' \
  'printf '\''%s  public-install.sh\n'\'' "$trusted_installer_sha256" | sha256sum --check -' \
  '--trusted-release-key-sha256 "$trusted_release_key_sha256"'; do
  if ! grep -Fq -- "$public_wrapper_contract" "$readme" ||
     ! grep -Fq -- "$public_wrapper_contract" "$installation_guide"; then
    echo "Git-free signer-pinned wrapper documentation is missing: $public_wrapper_contract" >&2
    exit 1
  fi
done

if grep -Fq '"$release_base/public-install.sh"' "$readme" "$installation_guide"; then
  echo "public documentation still bootstraps the installer from its mutable release directory" >&2
  exit 1
fi

# Docker quick-evaluation docs must tell a first-time operator where the local
# generated credentials live without publishing a password or exposing it in
# support channels.
docker_eval_readme=$(awk '
  /^## Quick evaluation with Docker$/ { capture=1 }
  capture { print }
  capture && /^## Native installation$/ { exit }
' "$readme")
docker_eval_installation=$(awk '
  /^### 2\.3 Docker quick evaluation$/ { capture=1 }
  capture { print }
  capture && /^## 3\. Configure Environment for a Source Build$/ { exit }
' "$installation_guide")
for docker_login_contract in \
  './scripts/init-env.sh' \
  'docker compose up --build' \
  'HSERVER_ADMIN_EMAIL' \
  'HSERVER_ADMIN_PASS' \
  'admin@localhost' \
  'http://localhost:3085' \
  'complete onboarding' \
  'dashboard' \
  'issues, logs, and chat' \
  'never printed by `init-env.sh`'; do
  if ! grep -Fq -- "$docker_login_contract" <<<"$docker_eval_readme" ||
     ! grep -Fq -- "$docker_login_contract" <<<"$docker_eval_installation"; then
    echo "Docker quick-evaluation first-login documentation is missing: $docker_login_contract" >&2
    exit 1
  fi
done
if grep -Eq 'HSERVER_ADMIN_PASS[[:space:]]*=' <<<"$docker_eval_readme" ||
   grep -Eq 'HSERVER_ADMIN_PASS[[:space:]]*=' <<<"$docker_eval_installation"; then
  echo "Docker quick-evaluation docs must not publish a fixed admin password" >&2
  exit 1
fi

# Fresh installation examples must make the provider-neutral site-root choice
# explicit without turning it into a hidden default.
for site_root_contract in \
  'bootstrap-install.sh' \
  '--vhosts-root /srv/hserver/sites' \
  'root-dependent capabilities report' \
  '`not_configured`'; do
  if ! grep -Fq -- "$site_root_contract" "$readme" ||
     ! grep -Fq -- "$site_root_contract" "$installation_guide"; then
    echo "first-install site-root documentation is missing: $site_root_contract" >&2
    exit 1
  fi
done

for lifecycle_contract in \
  './scripts/public-install.sh https://github.com/OWNER/REPOSITORY' \
  'HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT' \
  'integer `1`–`100`'; do
  if ! grep -Fq -- "$lifecycle_contract" "$readme" "$installation_guide"; then
    echo "public lifecycle documentation is missing: $lifecycle_contract" >&2
    exit 1
  fi
done

# PHP installation roots must document their provider-neutral defaults and
# fail-closed relative-path behavior.
for php_root_contract in \
  '| `HSERVER_PHP_CONFIG_ROOT` | `/etc/php` | No |' \
  '| `HSERVER_PHP_BINARY_ROOT` | `/usr/sbin` | No |' \
  'for PHP-FPM readiness and management' \
  'relative value fails closed'; do
  if ! grep -Fq -- "$php_root_contract" "$installation_guide"; then
    echo "PHP-FPM installation-root documentation is missing: $php_root_contract" >&2
    exit 1
  fi
done

# Provider paths and identities must be explicit opt-ins in public docs. These
# checks prevent a stale host layout from silently returning as a default.
for stale_default in \
  '| `HSERVER_VHOSTS_ROOT` | `/var/www/vhosts` |' \
  '| `STALWART_URL` | `http://127.0.0.1:8080` |' \
  '| `STALWART_ADMIN_USER` | `admin` |' \
  '| `HSERVER_STALWART_SERVICE` | `stalwart` |' \
  '| `HSERVER_STALWART_CONFIG_PATH` | `/opt/stalwart/etc/config.toml` |' \
  '| `HSERVER_STALWART_BIN` | `/opt/stalwart/bin/stalwart-mail` |' \
  '| `HSERVER_PM2_ALLOWED_ROOTS` | `HSERVER_VHOSTS_ROOT,/home,/opt` |' \
  '| `HSERVER_AGENT_PM2_BINARY` | No | Absolute local PM2 executable; default `/usr/local/bin/pm2` |' \
  '| `HSERVER_AGENT_PM2_HOME` | No | PM2 state directory for the managed identity; default `/root/.pm2` |' \
  '| `HSERVER_AGENT_PM2_USER` | No | Local Unix identity used for PM2 commands; default `root` |' \
  'When the variable is unset, HServer uses the configured vhost root plus `/home` and `/opt`.'; do
  if grep -Fq -- "$stale_default" "$installation_guide" "$api_reference" "$cli_guide" "$mail_guide"; then
    echo "public docs contain stale provider-specific default: $stale_default" >&2
    exit 1
  fi
done

for stale_mail_default in \
  'STALWART_URL=http://127.0.0.1:8080' \
  'STALWART_ADMIN_USER=admin'; do
  if grep -Fq -- "$stale_mail_default" "$mail_guide"; then
    echo "public mail documentation contains an implicit provider default: $stale_mail_default" >&2
    exit 1
  fi
done

for mail_contract in \
  'STALWART_URL=' \
  'STALWART_API_KEY=' \
  'STALWART_ADMIN_USER=' \
  'STALWART_ADMIN_PASS=' \
  'has no default administrator username'; do
  if ! grep -Fq -- "$mail_contract" "$mail_guide"; then
    echo "public mail documentation is missing provider-neutral configuration: $mail_contract" >&2
    exit 1
  fi
done

for explicit_contract in \
  '| `HSERVER_VHOSTS_ROOT` | — |' \
  '| `STALWART_URL` | — |' \
  '| `HSERVER_STALWART_SERVICE` | — |' \
  '| `HSERVER_STALWART_CONFIG_PATH` | — |' \
  '| `HSERVER_STALWART_BIN` | — |' \
  '| `HSERVER_PM2_ALLOWED_ROOTS` | — |' \
  '| `HSERVER_AGENT_PM2_BINARY` | No | Absolute local PM2 executable; empty keeps managed-node PM2 `not_configured`' \
  '| `HSERVER_AGENT_PM2_HOME` | No | Absolute PM2 state directory for the configured identity; empty keeps managed-node PM2 `not_configured`' \
  '| `HSERVER_AGENT_PM2_USER` | No | Explicit local unprivileged Unix identity used for PM2 commands; empty keeps managed-node PM2 `not_configured`'; do
  if ! grep -Fq -- "$explicit_contract" "$installation_guide"; then
    echo "public docs are missing explicit provider contract: $explicit_contract" >&2
    exit 1
  fi
done

for explicit_example in \
  'HSERVER_PM2_ALLOWED_ROOTS=/srv/hserver/sites,/home/deploy/apps,/opt/apps' \
  'HSERVER_AGENT_PM2_BINARY=/usr/local/bin/pm2' \
  'HSERVER_AGENT_PM2_HOME=/home/deploy/.pm2' \
  'HSERVER_AGENT_PM2_USER=deploy'; do
  if ! grep -Fq -- "$explicit_example" "$installation_guide"; then
    echo "public docs are missing explicit PM2 example: $explicit_example" >&2
    exit 1
  fi
done
if ! grep -Fq -- 'HSERVER_PM2_ALLOWED_ROOTS=/srv/hserver/sites,/home/deploy/apps' "$cli_guide" ||
   ! grep -Fq -- 'HSERVER_VHOSTS_ROOT=/srv/hserver/sites' "$cli_guide"; then
  echo "CLI docs are missing explicit installation-root examples" >&2
  exit 1
fi

echo "public source inventory check passed"
