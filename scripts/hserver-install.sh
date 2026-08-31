#!/usr/bin/env sh
set -eu

PROGRAM=hserver-install
BINARY_NAME=hserver-panel
CLI_NAME=hserverctl
SERVICE_NAME=hserver
ROOT_PREFIX=${HSERVER_ROOT_PREFIX:-}
SYSTEMCTL=${HSERVER_SYSTEMCTL:-systemctl}
APT_GET=${HSERVER_APT_GET:-apt-get}
SQLITE3=${HSERVER_SQLITE3:-sqlite3}
HEALTH_TIMEOUT=${HSERVER_HEALTH_TIMEOUT:-30}
SKIP_HEALTHCHECK=${HSERVER_SKIP_HEALTHCHECK:-0}
PRESERVE_LAYOUT=${HSERVER_PRESERVE_LAYOUT:-0}
# Keep a small, provider-neutral rollback window by default. The value is
# persisted in hserver.env and may be overridden for an installation with
# HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT.
SNAPSHOT_RETENTION_DEFAULT=3
SNAPSHOT_RETENTION_MAX=100
INITIAL_STATE_SNAPSHOT=
VHOSTS_ROOT_OPTION=
VHOSTS_ROOT_PATH=

cleanup_initial_state_snapshot() {
  if [ -n "$INITIAL_STATE_SNAPSHOT" ] && [ -d "$INITIAL_STATE_SNAPSHOT" ]; then
    rm -rf "$INITIAL_STATE_SNAPSHOT"
  fi
  INITIAL_STATE_SNAPSHOT=
}

trap cleanup_initial_state_snapshot 0

root_path() {
  printf '%s%s\n' "$ROOT_PREFIX" "$1"
}

BINARY_PATH=${HSERVER_BINARY_PATH:-$(root_path /usr/local/bin/hserver-panel)}
CLI_PATH=${HSERVER_CLI_PATH:-$(root_path /usr/local/bin/hserverctl)}
LIFECYCLE_INSTALLER_PATH=${HSERVER_LIFECYCLE_INSTALLER_PATH:-$(root_path /usr/local/libexec/hserver-install)}
DOCTOR_PATH=${HSERVER_DOCTOR_PATH:-$(root_path /usr/local/libexec/hserver-doctor)}
LIFECYCLE_ASSETS_DIR=${HSERVER_LIFECYCLE_ASSETS_DIR:-$(root_path /usr/local/share/hserver)}
LIFECYCLE_SNIPPETS_DIR=${HSERVER_LIFECYCLE_SNIPPETS_DIR:-$LIFECYCLE_ASSETS_DIR/nginx-snippets}
CONFIG_DIR=${HSERVER_CONFIG_DIR:-$(root_path /etc/hserver)}
ENV_FILE=${HSERVER_ENV_FILE:-$CONFIG_DIR/hserver.env}
DATA_DIR=${HSERVER_DATA_DIR_PATH:-$(root_path /var/lib/hserver)}
DEPLOY_TEMPLATES_DIR=$DATA_DIR/deploy-templates
SERVICE_FILE=${HSERVER_SERVICE_FILE:-$(root_path /etc/systemd/system/hserver.service)}
RELEASES_DIR=${HSERVER_RELEASES_DIR:-$DATA_DIR/releases}
NGINX_SNIPPETS_DIR=${HSERVER_NGINX_SNIPPETS_DIR_PATH:-$(root_path /etc/nginx/snippets)}

usage() {
  cat <<'EOF'
Heyserver native lifecycle installer

Supported hosts: Ubuntu 24.04+ or Debian 12+ with a reachable systemd manager
and the apt-get, curl, openssl, tar, install, and sqlite3 lifecycle tools.

Usage:
  sudo ./hserver-install.sh install [--binary PATH] [--cli-binary PATH] [--vhosts-root ABSOLUTE_PATH]
  sudo ./hserver-install.sh upgrade [--binary PATH] [--cli-binary PATH]
  sudo /usr/local/libexec/hserver-install rollback
  sudo /usr/local/libexec/hserver-install uninstall [--purge-config] [--purge-data]
  sudo /usr/local/libexec/hserver-install next-steps
  sudo /usr/local/libexec/hserver-doctor installed

The installer preserves /etc/hserver and /var/lib/hserver by default. A failed
initial installation restores the pre-install host state. Upgrade creates a
versioned recovery snapshot and automatically rolls back when the new service
does not become healthy. It retains the latest
HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT pre-upgrade snapshots (default: 3;
minimum: 1; maximum: 100), including the snapshot referenced by the rollback
marker.
EOF
}

die() {
  printf '%s: %s\n' "$PROGRAM" "$*" >&2
  exit 1
}

require_root() {
  [ "$(id -u)" -eq 0 ] || die "run this command as root"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

assert_safe_path() {
  case "$1" in
    ''|/|/etc|/var|/usr|/usr/local) die "refusing unsafe path: $1" ;;
  esac
}

validate_vhosts_root() {
  vhosts_root_candidate=$1
  case "$vhosts_root_candidate" in
    '') die "--vhosts-root must be an absolute path" ;;
    /*) ;;
    *) die "--vhosts-root must be an absolute path" ;;
  esac
  case "$vhosts_root_candidate" in
    /) die "refusing unsafe path: /" ;;
    *[[:space:]]*) die "--vhosts-root must not contain whitespace" ;;
    *..*) die "--vhosts-root contains an unsafe path traversal sequence" ;;
    *[!ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._/+:-]*)
      die "--vhosts-root contains unsafe path characters"
      ;;
    /etc|/var|/usr|/usr/local)
      die "refusing unsafe path: $vhosts_root_candidate"
      ;;
  esac
}

default_binary_source() {
  script_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)
  for candidate in \
    "$script_dir/$BINARY_NAME" \
    "$script_dir/../bin/$BINARY_NAME" \
    "./$BINARY_NAME" \
    "./bin/$BINARY_NAME"
  do
    if [ -f "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

default_cli_source() {
  script_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)
  for candidate in \
    "$script_dir/$CLI_NAME" \
    "$script_dir/../bin/$CLI_NAME" \
    "./$CLI_NAME" \
    "./bin/$CLI_NAME"
  do
    if [ -f "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

default_snippets_source() {
  script_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)
  for candidate in \
    "$script_dir/nginx-snippets" \
    "$script_dir/../deploy/nginx-snippets" \
    "$LIFECYCLE_SNIPPETS_DIR"
  do
    if [ -d "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

default_deploy_templates_source() {
  script_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)
  for candidate in \
    "$script_dir/deploy-templates.example" \
    "$script_dir/../deploy/hserver-deploy-templates.example"
  do
    if [ -d "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

default_deploy_template_names() {
  cat <<'EOF'
docker-compose.json
node-build.json
EOF
}

seed_default_deploy_templates() {
  [ ! -e "$DEPLOY_TEMPLATES_DIR" ] || return 0
  source_dir=$(default_deploy_templates_source) || return 1
  install -d -m 0755 "$DEPLOY_TEMPLATES_DIR" || return 1
  default_deploy_template_names | while IFS= read -r name; do
    [ -f "$source_dir/$name" ] || exit 1
    install -m 0644 "$source_dir/$name" "$DEPLOY_TEMPLATES_DIR/$name" || exit 1
  done || return 1
  printf 'Installed starter deployment templates: %s\n' "$DEPLOY_TEMPLATES_DIR"
}

installer_source() {
  source_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)
  source_path=$source_dir/$(basename -- "$0")
  [ -f "$source_path" ] || return 1
  printf '%s\n' "$source_path"
}

doctor_source() {
  source_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)
  for source_path in "$source_dir/doctor.sh" "$source_dir/hserver-doctor.sh" "$source_dir/hserver-doctor"; do
    if [ -f "$source_path" ]; then
      printf '%s\n' "$source_path"
      return 0
    fi
  done
  return 1
}

managed_snippet_names() {
  cat <<'EOF'
hserver-acme-challenge.conf
hserver-ssl-params.conf
hserver-security-headers.conf
hserver-security-deny.conf
hserver-compression.conf
hserver-static-cache.conf
hserver-php-fpm.conf
hserver-proxy-params.conf
EOF
}

install_managed_snippets() {
  source_dir=$(default_snippets_source) || return 1
  install -d -m 0755 "$NGINX_SNIPPETS_DIR" || return 1
  managed_snippet_names | while IFS= read -r name; do
    [ -f "$source_dir/$name" ] || exit 1
    install -m 0644 "$source_dir/$name" "$NGINX_SNIPPETS_DIR/$name" || exit 1
  done
}

persist_lifecycle_snippets() (
  source_dir=$(default_snippets_source) || return 1
  install -d -m 0755 "$LIFECYCLE_ASSETS_DIR" || return 1
  staged=$(mktemp -d "$LIFECYCLE_ASSETS_DIR/.nginx-snippets.XXXXXX") || return 1
  trap 'rm -rf "$staged"' EXIT INT TERM

  managed_snippet_names | while IFS= read -r name; do
    [ -f "$source_dir/$name" ] || exit 1
    install -m 0644 "$source_dir/$name" "$staged/$name" || exit 1
  done || return 1

  install -d -m 0755 "$LIFECYCLE_SNIPPETS_DIR" || return 1
  managed_snippet_names | while IFS= read -r name; do
    install -m 0644 "$staged/$name" "$LIFECYCLE_SNIPPETS_DIR/.$name.tmp.$$" || exit 1
    mv -f "$LIFECYCLE_SNIPPETS_DIR/.$name.tmp.$$" "$LIFECYCLE_SNIPPETS_DIR/$name" || exit 1
  done
)

validate_binary() {
  [ -f "$1" ] || die "binary not found: $1"
  [ -s "$1" ] || die "binary is empty: $1"
}

logical_installed_path() {
  candidate=$1
  if [ -n "$ROOT_PREFIX" ]; then
    case "$candidate" in
      "$ROOT_PREFIX"/*) printf '%s\n' "${candidate#"$ROOT_PREFIX"}"; return 0 ;;
    esac
  fi
  printf '%s\n' "$candidate"
}

validate_installed_executable() {
  candidate=$1
  expected_name=$2
  logical=$(logical_installed_path "$candidate")
  case "$logical" in
    /*) ;;
    *) die "$expected_name destination must be an absolute path" ;;
  esac
  case "$logical" in
    */../*|*/..|*/./*|*/.) die "$expected_name destination must be canonical" ;;
    *[[:space:]]*) die "$expected_name destination must not contain whitespace" ;;
    /etc/*|/proc/*|/sys/*|/dev/*|/run/*|/tmp/*|/var/*)
      die "$expected_name destination is under an unsafe executable root"
      ;;
  esac
  [ "$(basename -- "$logical")" = "$expected_name" ] \
    || die "$expected_name destination must end with /$expected_name"
  [ ! -L "$candidate" ] \
    || die "$expected_name destination must not be a symlink: $logical"
  [ -f "$candidate" ] && [ -x "$candidate" ] \
    || die "$expected_name destination must be an existing regular executable: $logical"
  resolved=$(readlink -f -- "$candidate" 2>/dev/null || true)
  [ "$resolved" = "$candidate" ] \
    || die "$expected_name destination and its parent directories must not be symlinks: $logical"
}

validate_preserve_layout() {
  [ "$PRESERVE_LAYOUT" = 1 ] || return 0
  require_command readlink
  validate_installed_executable "$BINARY_PATH" "$BINARY_NAME"
  validate_installed_executable "$CLI_PATH" "$CLI_NAME"
  [ "$BINARY_PATH" != "$CLI_PATH" ] || die "panel and CLI destinations must differ"
  [ -d "$DATA_DIR" ] && [ ! -L "$DATA_DIR" ] \
    || die "preserve-layout data directory must be an existing regular directory"
  resolved_data=$(readlink -f -- "$DATA_DIR" 2>/dev/null || true)
  [ "$resolved_data" = "$DATA_DIR" ] \
    || die "preserve-layout data directory and its parent directories must not be symlinks"
  case "$BINARY_PATH" in "$DATA_DIR"/*) die "panel destination must not be inside the data directory" ;; esac
  case "$CLI_PATH" in "$DATA_DIR"/*) die "CLI destination must not be inside the data directory" ;; esac
}

atomic_install_binary() {
  source_binary=$1
  destination=$2
  destination_dir=$(dirname -- "$destination")
  destination_name=$(basename -- "$destination")
  temporary=$(mktemp "$destination_dir/.${destination_name}.upgrade.XXXXXX") || return 1
  if ! install -m 0755 "$source_binary" "$temporary" || ! mv -f "$temporary" "$destination"; then
    rm -f "$temporary"
    return 1
  fi
}

run_preflight_doctor() {
  doctor=$(doctor_source) \
    || die "installation doctor is missing; use the complete release package"
  HSERVER_ROOT_PREFIX="$ROOT_PREFIX" \
  HSERVER_SYSTEMCTL="$SYSTEMCTL" \
  HSERVER_APT_GET="$APT_GET" \
    sh "$doctor" preflight || die "host preflight failed"
}

install_lifecycle_tools() {
  source_installer=$(installer_source) || return 1
  source_doctor=$(doctor_source) || return 1
  if [ ! "$source_installer" -ef "$LIFECYCLE_INSTALLER_PATH" ]; then
    install -D -m 0755 "$source_installer" "$LIFECYCLE_INSTALLER_PATH" || return 1
  fi
  if [ ! "$source_doctor" -ef "$DOCTOR_PATH" ]; then
    install -D -m 0755 "$source_doctor" "$DOCTOR_PATH" || return 1
  fi
  chmod 0755 "$LIFECYCLE_INSTALLER_PATH" "$DOCTOR_PATH" || return 1
  persist_lifecycle_snippets
}

generate_environment() {
  require_command openssl
  install_update_manifest_url=${HSERVER_INSTALL_UPDATE_MANIFEST_URL:-}
  install_update_public_keys=${HSERVER_INSTALL_UPDATE_MANIFEST_PUBLIC_KEYS:-}
  case "$install_update_manifest_url" in
    '')
      [ -z "$install_update_public_keys" ] \
        || die "HSERVER_INSTALL_UPDATE_MANIFEST_PUBLIC_KEYS requires a manifest URL"
      ;;
    http://*|https://*) ;;
    *) die "HSERVER_INSTALL_UPDATE_MANIFEST_URL must use HTTP or HTTPS" ;;
  esac
  case "$install_update_manifest_url" in
    *[[:space:]]*) die "HSERVER_INSTALL_UPDATE_MANIFEST_URL cannot contain whitespace" ;;
  esac
  case "$install_update_public_keys" in
    *[!A-Za-z0-9+/=,]*) die "HSERVER_INSTALL_UPDATE_MANIFEST_PUBLIC_KEYS contains unsupported characters" ;;
  esac
  old_umask=$(umask)
  umask 077
  jwt_secret=$(openssl rand -hex 32)
  admin_password=$(openssl rand -hex 16)
  cron_secret=$(openssl rand -hex 32)
  cat >"$ENV_FILE" <<EOF
HSERVER_PORT=3085
HSERVER_DB_PATH=$DATA_DIR/hserver.db
HSERVER_DATA_DIR=$DATA_DIR
HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT=$(snapshot_retention_value)
HSERVER_LOG_LEVEL=info
HSERVER_JWT_SECRET=$jwt_secret
HSERVER_ADMIN_EMAIL=admin@localhost
HSERVER_ADMIN_PASS=$admin_password
HSERVER_CRON_SECRET=$cron_secret
# Optional installation-owned site root. Leave empty until a local absolute
# path is selected; domain, file, and site-backup capabilities stay disabled.
HSERVER_VHOSTS_ROOT=$VHOSTS_ROOT_OPTION
# Installation-local PHP-FPM roots used for readiness and management. An
# explicitly configured relative path fails closed instead of using a host default.
HSERVER_PHP_CONFIG_ROOT=/etc/php
HSERVER_PHP_BINARY_ROOT=/usr/sbin
HSERVER_NGINX_SITES_AVAILABLE=/etc/nginx/sites-available
HSERVER_NGINX_SITES_ENABLED=/etc/nginx/sites-enabled
HSERVER_NGINX_SNIPPETS_DIR=/etc/nginx/snippets
HSERVER_PGM_BACKUP_DIR=$DATA_DIR/pgm-backups
HSERVER_PG_RUN_AS=postgres
HSERVER_PG_BACKUP_USER=postgres
HSERVER_PG_BACKUP_HOST=
HSERVER_PG_BACKUP_PORT=
HSERVER_PG_PASSFILE=
HSERVER_MYSQL_DEFAULTS_FILE=
HSERVER_STALWART_SERVICE=
HSERVER_STALWART_CONFIG_PATH=
HSERVER_STALWART_BIN=

# Optional integrations. Empty provider settings keep integrations explicitly
# not configured until the operator supplies local endpoints and credentials.
HSERVER_UPDATE_MANIFEST_URL=$install_update_manifest_url
HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS=$install_update_public_keys
HSERVER_UPDATE_PANEL_BINARY_PATH=$BINARY_PATH
HSERVER_UPDATE_CLI_BINARY_PATH=$CLI_PATH
STALWART_URL=
STALWART_API_KEY=
STALWART_ADMIN_USER=
STALWART_ADMIN_PASS=
HSERVER_CF_API_TOKEN=
HSERVER_CF_API_EMAIL=
HSERVER_DOMAIN_DNS_ORIGIN=
HSERVER_DOMAIN_DNS_PROXIED=false
HSERVER_MAIL_DNS_HOSTNAME=
HSERVER_MAIL_DNS_PUBLIC_IP=
HSERVER_MAIL_DNS_MX_PRIORITY=10
HSERVER_MAIL_DNS_SPF=
HSERVER_MAIL_DNS_DMARC=
HSERVER_PM2_USER=
HSERVER_PM2_HOME=
HSERVER_PM2_BIN=pm2
# Optional absolute roots for PM2 deploy paths. Set an explicit comma-separated
# allowlist when PM2 management is enabled; empty keeps it disabled.
HSERVER_PM2_ALLOWED_ROOTS=
HSERVER_CERTBOT_BIN=certbot
HSERVER_CERTBOT_CONFIG_DIR=/etc/letsencrypt
HSERVER_ACME_WEBROOT=/var/www/hserver-acme
HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS=
HSERVER_GDRIVE_CLIENT_ID=
HSERVER_GDRIVE_CLIENT_SECRET=
HSERVER_GDRIVE_REDIRECT_URI=
HSERVER_RCLONE_BIN=rclone
HSERVER_RESTIC_BIN=restic
HSERVER_RESTIC_PASSWORD=
HSERVER_S3_ENDPOINT=
HSERVER_S3_BUCKET=
HSERVER_S3_REGION=
HSERVER_S3_ACCESS_KEY_FILE=
HSERVER_S3_SECRET_KEY_FILE=
HSERVER_S3_BUCKET_LOOKUP=auto
EOF
  chmod 0600 "$ENV_FILE"
  umask "$old_umask"
  printf '%s\n' "Created protected configuration: $ENV_FILE"
  printf '%s\n' "Initial credentials remain in that file and were not printed."
}

write_service_unit() {
  unit_tmp=$(mktemp) || return 1
  if ! cat >"$unit_tmp" <<EOF
[Unit]
Description=Heyserver Panel - Server Management GUI
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=$BINARY_PATH
Restart=always
RestartSec=5
EnvironmentFile=$ENV_FILE
WorkingDirectory=$DATA_DIR
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
  then
    rm -f "$unit_tmp"
    return 1
  fi
  if ! install -D -m 0644 "$unit_tmp" "$SERVICE_FILE"; then
    rm -f "$unit_tmp"
    return 1
  fi
  rm -f "$unit_tmp" || return 1
}

env_value() {
  key=$1
  fallback=$2
  value=$(sed -n "s/^${key}=//p" "$ENV_FILE" 2>/dev/null | tail -n 1 || true)
  if [ -n "$value" ]; then
    printf '%s\n' "$value"
  else
    printf '%s\n' "$fallback"
  fi
}

snapshot_retention_value() {
  configured_from_file=
  if [ -f "$ENV_FILE" ]; then
    configured_from_file=$(env_value HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT "")
  fi
  configured=${HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT:-}
  [ -n "$configured" ] || configured=$configured_from_file
  [ -n "$configured" ] || configured=$SNAPSHOT_RETENTION_DEFAULT
  printf '%s\n' "$configured"
}

validate_snapshot_retention_value() {
  configured=$1
  case "$configured" in
    1|2|3|4|5|6|7|8|9|[1-9][0-9]|100) ;;
    *)
      die "HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT must be a positive integer from 1 to $SNAPSHOT_RETENTION_MAX"
      ;;
  esac
}

validate_snapshot_retention() {
  configured=$(snapshot_retention_value)
  if [ -f "$ENV_FILE" ]; then
    configured_from_file=$(env_value HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT "")
    [ -z "$configured_from_file" ] || validate_snapshot_retention_value "$configured_from_file"
  fi
  validate_snapshot_retention_value "$configured"
  printf '%s\n' "$configured"
}

resolve_managed_snippets_dir() {
  [ -z "${HSERVER_NGINX_SNIPPETS_DIR_PATH:-}" ] || return 0
  configured=$(env_value HSERVER_NGINX_SNIPPETS_DIR /etc/nginx/snippets)
  case "$configured" in
    /*) NGINX_SNIPPETS_DIR=$(root_path "$configured") ;;
    *) die "HSERVER_NGINX_SNIPPETS_DIR must be an absolute path" ;;
  esac
}

prepare_vhosts_root() {
  [ -n "$VHOSTS_ROOT_PATH" ] || return 0
  if [ -L "$VHOSTS_ROOT_PATH" ]; then
    printf '%s\n' "--vhosts-root must not be a symlink: $VHOSTS_ROOT_OPTION" >&2
    return 1
  fi
  if [ -e "$VHOSTS_ROOT_PATH" ]; then
    [ -d "$VHOSTS_ROOT_PATH" ] || {
      printf '%s\n' "--vhosts-root must point to a directory: $VHOSTS_ROOT_OPTION" >&2
      return 1
    }
    return 0
  fi
  install -d -m 0755 "$VHOSTS_ROOT_PATH"
}

health_url() {
  if [ -n "${HSERVER_HEALTH_URL:-}" ]; then
    printf '%s\n' "$HSERVER_HEALTH_URL"
    return
  fi
  port=$(env_value HSERVER_PORT 3085)
  printf 'http://127.0.0.1:%s/api/health\n' "$port"
}

show_next_steps() {
  [ -f "$ENV_FILE" ] || die "configuration is missing: $ENV_FILE"
  next_port=$(env_value HSERVER_PORT 3085)
  case "$next_port" in
    ''|*[!0-9]*) next_port=3085 ;;
    *)
      if ! [ "$next_port" -ge 1 ] 2>/dev/null || ! [ "$next_port" -le 65535 ] 2>/dev/null; then
        next_port=3085
      fi
      ;;
  esac
  next_admin=$(env_value HSERVER_ADMIN_EMAIL admin@localhost)
  case "$next_admin" in
    ''|*[!A-Za-z0-9@._+-]*) next_admin='the administrator email stored in the protected configuration' ;;
  esac

  cat <<EOF

Heyserver is ready for first access.

1. From your workstation, create an encrypted SSH tunnel:
   ssh -N -L ${next_port}:127.0.0.1:${next_port} YOUR_SSH_USER@YOUR_SERVER
2. Open http://127.0.0.1:${next_port} in your browser.
3. Sign in as ${next_admin} and complete onboarding.

The initial password remains only in the root-readable file: ${ENV_FILE}
Configure an HTTPS reverse proxy before exposing Heyserver directly to a network.

Diagnostics: sudo ${DOCTOR_PATH} installed
Lifecycle:   sudo ${LIFECYCLE_INSTALLER_PATH} --help
EOF
}

wait_healthy() {
  [ "$SKIP_HEALTHCHECK" = 1 ] && return 0
  require_command curl
  url=$(health_url)
  elapsed=0
  while [ "$elapsed" -lt "$HEALTH_TIMEOUT" ]; do
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  return 1
}

service_is_active() {
  "$SYSTEMCTL" is-active --quiet "$SERVICE_NAME" >/dev/null 2>&1
}

service_is_enabled() {
  "$SYSTEMCTL" is-enabled --quiet "$SERVICE_NAME" >/dev/null 2>&1
}

restore_service_state() {
  restore_active=$1
  restore_enabled=$2
  if [ "$restore_enabled" = 1 ]; then
    "$SYSTEMCTL" enable "$SERVICE_NAME" || return 1
  else
    "$SYSTEMCTL" disable "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
  if [ "$restore_active" = 1 ]; then
    "$SYSTEMCTL" start "$SERVICE_NAME" || return 1
  else
    "$SYSTEMCTL" stop "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
}

snapshot_flag() {
  snapshot=$1
  key=$2
  fallback=$3
  value=$(sed -n "s/^${key}=//p" "$snapshot/manifest.env" 2>/dev/null | tail -n 1 || true)
  case "$value" in
    0|1) printf '%s\n' "$value" ;;
    *) printf '%s\n' "$fallback" ;;
  esac
}

snapshot_manifest_flag() {
  snapshot=$1
  key=$2
  [ -f "$snapshot/manifest.env" ] || return 1
  value=$(sed -n "s/^${key}=//p" "$snapshot/manifest.env" 2>/dev/null | tail -n 1 || true)
  case "$value" in
    0|1) printf '%s\n' "$value" ;;
    *) return 1 ;;
  esac
}

validate_snapshot() {
  snapshot=$1
  [ -d "$snapshot" ] || return 1
  [ -f "$snapshot/manifest.env" ] || return 1
  [ -f "$snapshot/$BINARY_NAME" ] || return 1

  if [ "$(snapshot_flag "$snapshot" PRESERVE_LAYOUT 0)" = 1 ]; then
    [ -f "$snapshot/$CLI_NAME" ] || return 1
    [ -s "$snapshot/systemd-unit-state.txt" ] || return 1
    [ -s "$snapshot/systemd-unit-content.txt" ] || return 1
  fi

  for key in \
    SERVICE_WAS_ACTIVE \
    SERVICE_WAS_ENABLED \
    CLI_WAS_PRESENT \
    INSTALLER_WAS_PRESENT \
    DOCTOR_WAS_PRESENT \
    SERVICE_FILE_WAS_PRESENT \
    LIFECYCLE_ASSETS_DIR_WAS_PRESENT \
    LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT \
    NGINX_SNIPPETS_WERE_PRESENT \
    LIFECYCLE_SNIPPETS_WERE_PRESENT
  do
    snapshot_manifest_flag "$snapshot" "$key" >/dev/null || return 1
  done

  if [ "$(snapshot_manifest_flag "$snapshot" CLI_WAS_PRESENT)" = 1 ]; then
    [ -f "$snapshot/$CLI_NAME" ] || return 1
  fi
  if [ "$(snapshot_manifest_flag "$snapshot" INSTALLER_WAS_PRESENT)" = 1 ]; then
    [ -f "$snapshot/hserver-install" ] || return 1
  fi
  if [ "$(snapshot_manifest_flag "$snapshot" DOCTOR_WAS_PRESENT)" = 1 ]; then
    [ -f "$snapshot/hserver-doctor" ] || return 1
  fi
  if [ "$(snapshot_manifest_flag "$snapshot" SERVICE_FILE_WAS_PRESENT)" = 1 ]; then
    [ -f "$snapshot/hserver.service" ] || return 1
  fi

  if [ "$(snapshot_manifest_flag "$snapshot" NGINX_SNIPPETS_WERE_PRESENT)" = 1 ]; then
    snapshot_file_present=0
    for snapshot_file in "$snapshot/nginx-snippets"/hserver-*.conf; do
      if [ -f "$snapshot_file" ]; then
        snapshot_file_present=1
        break
      fi
    done
    [ "$snapshot_file_present" = 1 ] || return 1
  fi
  if [ "$(snapshot_manifest_flag "$snapshot" LIFECYCLE_SNIPPETS_WERE_PRESENT)" = 1 ]; then
    snapshot_file_present=0
    for snapshot_file in "$snapshot/lifecycle-nginx-snippets"/hserver-*.conf; do
      if [ -f "$snapshot_file" ]; then
        snapshot_file_present=1
        break
      fi
    done
    [ "$snapshot_file_present" = 1 ] || return 1
  fi

  if [ -f "$snapshot/databases.map" ]; then
    while IFS='=' read -r db_name db_target; do
      [ -n "$db_name" ] || continue
      [ -n "$db_target" ] || return 1
      [ -f "$snapshot/$db_name" ] || return 1
    done <"$snapshot/databases.map"
  fi
  return 0
}

cleanup_snapshot_state() {
  exit_status=$?
  trap - 0
  if [ "$exit_status" -ne 0 ]; then
    [ -z "$stage" ] || rm -rf "$stage"
    [ "$snapshot_committed" = 1 ] && rm -rf "$snapshot"
  fi
  exit "$exit_status"
}

snapshot_state() (
  label=$1
  observed_active=${2:-}
  observed_enabled=${3:-}
  stamp=$(date -u +%Y%m%dT%H%M%SZ) || return 1
  snapshot=$RELEASES_DIR/${stamp}-${label}
  suffix=0
  while [ -e "$snapshot" ]; do
    suffix=$((suffix + 1))
    snapshot=$RELEASES_DIR/${stamp}-${label}-${suffix}
  done
  stage=
  snapshot_committed=0
  trap cleanup_snapshot_state 0
  trap 'exit 1' HUP INT TERM
  stage=$(mktemp -d "$RELEASES_DIR/.${label}.XXXXXX") || return 1

  if [ -z "$observed_active" ]; then
    observed_active=0
    service_is_active && observed_active=1
  fi
  if [ -z "$observed_enabled" ]; then
    observed_enabled=0
    service_is_enabled && observed_enabled=1
  fi

  if [ "$PRESERVE_LAYOUT" = 1 ]; then
    {
      printf 'PRESERVE_LAYOUT=1\n'
      printf 'SERVICE_WAS_ACTIVE=%s\n' "$observed_active"
      printf 'SERVICE_WAS_ENABLED=%s\n' "$observed_enabled"
      printf 'CLI_WAS_PRESENT=1\n'
      printf 'INSTALLER_WAS_PRESENT=0\n'
      printf 'DOCTOR_WAS_PRESENT=0\n'
      printf 'SERVICE_FILE_WAS_PRESENT=0\n'
      printf 'LIFECYCLE_ASSETS_DIR_WAS_PRESENT=0\n'
      printf 'LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT=0\n'
      printf 'NGINX_SNIPPETS_WERE_PRESENT=0\n'
      printf 'LIFECYCLE_SNIPPETS_WERE_PRESENT=0\n'
    } >"$stage/manifest.env" || return 1
    cp -p "$BINARY_PATH" "$stage/$BINARY_NAME" || return 1
    cp -p "$CLI_PATH" "$stage/$CLI_NAME" || return 1
    "$SYSTEMCTL" show \
      --property=FragmentPath \
      --property=DropInPaths \
      --property=UnitFileState \
      "$SERVICE_NAME" >"$stage/systemd-unit-state.txt" || return 1
    "$SYSTEMCTL" cat "$SERVICE_NAME" >"$stage/systemd-unit-content.txt" || return 1
    mv "$stage" "$snapshot" || return 1
    stage=
    snapshot_committed=1
    printf '%s\n' "$snapshot"
    return 0
  fi
  {
    printf 'SERVICE_WAS_ACTIVE=%s\n' "$observed_active"
    printf 'SERVICE_WAS_ENABLED=%s\n' "$observed_enabled"
    if [ -f "$CLI_PATH" ]; then
      printf 'CLI_WAS_PRESENT=1\n'
    else
      printf 'CLI_WAS_PRESENT=0\n'
    fi
    [ -f "$LIFECYCLE_INSTALLER_PATH" ] && printf 'INSTALLER_WAS_PRESENT=1\n' || printf 'INSTALLER_WAS_PRESENT=0\n'
    [ -f "$DOCTOR_PATH" ] && printf 'DOCTOR_WAS_PRESENT=1\n' || printf 'DOCTOR_WAS_PRESENT=0\n'
    [ -d "$LIFECYCLE_ASSETS_DIR" ] && printf 'LIFECYCLE_ASSETS_DIR_WAS_PRESENT=1\n' || printf 'LIFECYCLE_ASSETS_DIR_WAS_PRESENT=0\n'
    [ -d "$LIFECYCLE_SNIPPETS_DIR" ] && printf 'LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT=1\n' || printf 'LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT=0\n'
    [ -f "$SERVICE_FILE" ] && printf 'SERVICE_FILE_WAS_PRESENT=1\n' || printf 'SERVICE_FILE_WAS_PRESENT=0\n'
  } >"$stage/manifest.env" || return 1

  if [ -f "$BINARY_PATH" ] && ! cp -p "$BINARY_PATH" "$stage/$BINARY_NAME"; then
    return 1
  fi
  if [ -f "$CLI_PATH" ] && ! cp -p "$CLI_PATH" "$stage/$CLI_NAME"; then
    return 1
  fi
  if [ -f "$LIFECYCLE_INSTALLER_PATH" ] && ! cp -p "$LIFECYCLE_INSTALLER_PATH" "$stage/hserver-install"; then
    return 1
  fi
  if [ -f "$DOCTOR_PATH" ] && ! cp -p "$DOCTOR_PATH" "$stage/hserver-doctor"; then
    return 1
  fi
  if [ -f "$SERVICE_FILE" ] && ! cp -p "$SERVICE_FILE" "$stage/hserver.service"; then
    return 1
  fi

  snippet_snapshot=$stage/nginx-snippets
  snippet_present=0
  install -d -m 0700 "$snippet_snapshot" || return 1
  for snippet in "$NGINX_SNIPPETS_DIR"/hserver-*.conf; do
    [ -f "$snippet" ] || continue
    cp -p "$snippet" "$snippet_snapshot/" || return 1
    snippet_present=1
  done
  printf 'NGINX_SNIPPETS_WERE_PRESENT=%s\n' "$snippet_present" >>"$stage/manifest.env" || return 1

  lifecycle_snippet_snapshot=$stage/lifecycle-nginx-snippets
  lifecycle_snippet_present=0
  install -d -m 0700 "$lifecycle_snippet_snapshot" || return 1
  for snippet in "$LIFECYCLE_SNIPPETS_DIR"/hserver-*.conf; do
    [ -f "$snippet" ] || continue
    cp -p "$snippet" "$lifecycle_snippet_snapshot/" || return 1
    lifecycle_snippet_present=1
  done
  printf 'LIFECYCLE_SNIPPETS_WERE_PRESENT=%s\n' "$lifecycle_snippet_present" >>"$stage/manifest.env" || return 1

  db_path=$(env_value HSERVER_DB_PATH "$DATA_DIR/hserver.db")
  for database in "$db_path" "$DATA_DIR/metrics.db"; do
    if [ -f "$database" ]; then
      db_name=$(basename "$database")
      if [ -f "$database-wal" ] || [ -f "$database-shm" ]; then
        require_command "$SQLITE3"
        # SQLite runs the panel databases in WAL mode. Copying only the main
        # file while the service is live can omit committed pages still held
        # in the WAL, so use SQLite's online backup API through the CLI.
        (umask 077; "$SQLITE3" -batch -bail "$database" \
          '.timeout 5000' ".backup '$stage/$db_name'") || return 1
      else
        # A standalone legacy or non-SQLite state file has no WAL to merge;
        # retain its exact bytes for the existing rollback compatibility path.
        cp -p "$database" "$stage/$db_name" || return 1
      fi
      printf '%s=%s\n' "$db_name" "$database" >>"$stage/databases.map" || return 1
    fi
  done
  mv "$stage" "$snapshot" || return 1
  stage=
  snapshot_committed=1
  printf '%s\n' "$snapshot"
)

mark_latest_snapshot() {
  snapshot=$1
  case "$snapshot" in
    "$RELEASES_DIR"/*) ;;
    *) return 1 ;;
  esac
  [ -d "$snapshot" ] || return 1
  marker_tmp=$(mktemp "$RELEASES_DIR/.latest-pre-upgrade.XXXXXX") || return 1
  if ! printf '%s\n' "$snapshot" >"$marker_tmp"; then
    rm -f "$marker_tmp"
    return 1
  fi
  if ! mv -f "$marker_tmp" "$RELEASES_DIR/latest-pre-upgrade"; then
    rm -f "$marker_tmp"
    return 1
  fi
}

discard_snapshot() {
  snapshot=$1
  marker=$RELEASES_DIR/latest-pre-upgrade
  if [ -f "$marker" ] && [ "$(cat "$marker" 2>/dev/null || true)" = "$snapshot" ]; then
    rm -f "$marker" || return 1
  fi
  rm -rf "$snapshot"
}

prune_pre_upgrade_snapshots() {
  retention=$1
  marker=$RELEASES_DIR/latest-pre-upgrade
  marked_snapshot=
  if [ -f "$marker" ]; then
    marked_snapshot=$(cat "$marker" 2>/dev/null || true)
    case "$marked_snapshot" in
      "$RELEASES_DIR"/*)
        [ -d "$marked_snapshot" ] || marked_snapshot=
        ;;
      *) marked_snapshot= ;;
    esac
  fi

  # Keep the marker target first, then the newest remaining snapshots. This
  # keeps the count bounded even if an old marker survived an interrupted
  # cleanup, while never deleting the snapshot rollback currently selects.
  if [ -n "$marked_snapshot" ]; then
    kept=1
  else
    kept=0
  fi
  find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d -name '*-pre-upgrade*' 2>/dev/null \
    | sort -r \
    | while IFS= read -r candidate; do
        [ -n "$candidate" ] || continue
        case "$candidate" in
          "$RELEASES_DIR"/*) ;;
          *) continue ;;
        esac
        [ "$candidate" = "$marked_snapshot" ] && continue
        if [ "$kept" -lt "$retention" ]; then
          kept=$((kept + 1))
        else
          rm -rf "$candidate"
        fi
      done
}

snapshot_initial_state() (
  state_snapshot=$1
  install -d -m 0700 "$state_snapshot"

  state_active=0
  state_enabled=0
  service_is_active && state_active=1
  service_is_enabled && state_enabled=1
  {
    printf 'SERVICE_WAS_ACTIVE=%s\n' "$state_active"
    printf 'SERVICE_WAS_ENABLED=%s\n' "$state_enabled"
    [ -f "$SERVICE_FILE" ] && printf 'SERVICE_FILE_WAS_PRESENT=1\n' || printf 'SERVICE_FILE_WAS_PRESENT=0\n'
    [ -f "$LIFECYCLE_INSTALLER_PATH" ] && printf 'INSTALLER_WAS_PRESENT=1\n' || printf 'INSTALLER_WAS_PRESENT=0\n'
    [ -f "$DOCTOR_PATH" ] && printf 'DOCTOR_WAS_PRESENT=1\n' || printf 'DOCTOR_WAS_PRESENT=0\n'
    [ -d "$CONFIG_DIR" ] && printf 'CONFIG_DIR_WAS_PRESENT=1\n' || printf 'CONFIG_DIR_WAS_PRESENT=0\n'
    [ -f "$ENV_FILE" ] && printf 'ENV_FILE_WAS_PRESENT=1\n' || printf 'ENV_FILE_WAS_PRESENT=0\n'
    [ -d "$DATA_DIR" ] && printf 'DATA_DIR_WAS_PRESENT=1\n' || printf 'DATA_DIR_WAS_PRESENT=0\n'
    [ -e "$DEPLOY_TEMPLATES_DIR" ] && printf 'DEPLOY_TEMPLATES_DIR_WAS_PRESENT=1\n' || printf 'DEPLOY_TEMPLATES_DIR_WAS_PRESENT=0\n'
    [ -d "$RELEASES_DIR" ] && printf 'RELEASES_DIR_WAS_PRESENT=1\n' || printf 'RELEASES_DIR_WAS_PRESENT=0\n'
    [ -d "$NGINX_SNIPPETS_DIR" ] && printf 'NGINX_SNIPPETS_DIR_WAS_PRESENT=1\n' || printf 'NGINX_SNIPPETS_DIR_WAS_PRESENT=0\n'
    [ -d "$LIFECYCLE_ASSETS_DIR" ] && printf 'LIFECYCLE_ASSETS_DIR_WAS_PRESENT=1\n' || printf 'LIFECYCLE_ASSETS_DIR_WAS_PRESENT=0\n'
    [ -d "$LIFECYCLE_SNIPPETS_DIR" ] && printf 'LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT=1\n' || printf 'LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT=0\n'
    if [ -n "$VHOSTS_ROOT_PATH" ] && [ -d "$VHOSTS_ROOT_PATH" ] && [ ! -L "$VHOSTS_ROOT_PATH" ]; then
      printf 'VHOSTS_ROOT_WAS_PRESENT=1\n'
    else
      printf 'VHOSTS_ROOT_WAS_PRESENT=0\n'
    fi
  } >"$state_snapshot/manifest.env"

  [ -f "$SERVICE_FILE" ] && cp -p "$SERVICE_FILE" "$state_snapshot/hserver.service"
  [ -f "$LIFECYCLE_INSTALLER_PATH" ] && cp -p "$LIFECYCLE_INSTALLER_PATH" "$state_snapshot/hserver-install"
  [ -f "$DOCTOR_PATH" ] && cp -p "$DOCTOR_PATH" "$state_snapshot/hserver-doctor"
  install -d -m 0700 "$state_snapshot/nginx-snippets"
  state_snippets_present=0
  for state_snippet in "$NGINX_SNIPPETS_DIR"/hserver-*.conf; do
    [ -f "$state_snippet" ] || continue
    cp -p "$state_snippet" "$state_snapshot/nginx-snippets/"
    state_snippets_present=1
  done
  printf 'NGINX_SNIPPETS_WERE_PRESENT=%s\n' "$state_snippets_present" >>"$state_snapshot/manifest.env"

  install -d -m 0700 "$state_snapshot/lifecycle-nginx-snippets"
  lifecycle_state_snippets_present=0
  for state_snippet in "$LIFECYCLE_SNIPPETS_DIR"/hserver-*.conf; do
    [ -f "$state_snippet" ] || continue
    cp -p "$state_snippet" "$state_snapshot/lifecycle-nginx-snippets/"
    lifecycle_state_snippets_present=1
  done
  printf 'LIFECYCLE_SNIPPETS_WERE_PRESENT=%s\n' "$lifecycle_state_snippets_present" >>"$state_snapshot/manifest.env"
)

rollback_initial_install() (
  state_snapshot=$1
  [ -d "$state_snapshot" ] || return 1

  "$SYSTEMCTL" disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
  rm -f "$BINARY_PATH" "$CLI_PATH"

  if [ "$(snapshot_flag "$state_snapshot" INSTALLER_WAS_PRESENT 0)" = 1 ]; then
    [ -f "$state_snapshot/hserver-install" ] || return 1
    install -D -m 0755 "$state_snapshot/hserver-install" "$LIFECYCLE_INSTALLER_PATH"
  else
    rm -f "$LIFECYCLE_INSTALLER_PATH"
  fi
  if [ "$(snapshot_flag "$state_snapshot" DOCTOR_WAS_PRESENT 0)" = 1 ]; then
    [ -f "$state_snapshot/hserver-doctor" ] || return 1
    install -D -m 0755 "$state_snapshot/hserver-doctor" "$DOCTOR_PATH"
  else
    rm -f "$DOCTOR_PATH"
  fi

  if [ "$(snapshot_flag "$state_snapshot" SERVICE_FILE_WAS_PRESENT 0)" = 1 ]; then
    [ -f "$state_snapshot/hserver.service" ] || return 1
    install -D -m 0644 "$state_snapshot/hserver.service" "$SERVICE_FILE"
  else
    rm -f "$SERVICE_FILE"
  fi

  install -d -m 0755 "$NGINX_SNIPPETS_DIR"
  rm -f "$NGINX_SNIPPETS_DIR"/hserver-*.conf
  if [ "$(snapshot_flag "$state_snapshot" NGINX_SNIPPETS_WERE_PRESENT 0)" = 1 ]; then
    for state_snippet in "$state_snapshot/nginx-snippets"/hserver-*.conf; do
      [ -f "$state_snippet" ] || continue
      install -m 0644 "$state_snippet" "$NGINX_SNIPPETS_DIR/$(basename "$state_snippet")"
    done
  fi
  if [ "$(snapshot_flag "$state_snapshot" NGINX_SNIPPETS_DIR_WAS_PRESENT 0)" = 0 ]; then
    rmdir "$NGINX_SNIPPETS_DIR" >/dev/null 2>&1 || true
  fi

  install -d -m 0755 "$LIFECYCLE_SNIPPETS_DIR"
  rm -f "$LIFECYCLE_SNIPPETS_DIR"/hserver-*.conf
  if [ "$(snapshot_flag "$state_snapshot" LIFECYCLE_SNIPPETS_WERE_PRESENT 0)" = 1 ]; then
    for state_snippet in "$state_snapshot/lifecycle-nginx-snippets"/hserver-*.conf; do
      [ -f "$state_snippet" ] || continue
      install -m 0644 "$state_snippet" "$LIFECYCLE_SNIPPETS_DIR/$(basename "$state_snippet")"
    done
  fi
  if [ "$(snapshot_flag "$state_snapshot" LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT 0)" = 0 ]; then
    rmdir "$LIFECYCLE_SNIPPETS_DIR" >/dev/null 2>&1 || true
  fi
  if [ "$(snapshot_flag "$state_snapshot" LIFECYCLE_ASSETS_DIR_WAS_PRESENT 0)" = 0 ]; then
    rmdir "$LIFECYCLE_ASSETS_DIR" >/dev/null 2>&1 || true
  fi

  if [ "$(snapshot_flag "$state_snapshot" ENV_FILE_WAS_PRESENT 0)" = 0 ]; then
    rm -f "$ENV_FILE"
  fi
  if [ "$(snapshot_flag "$state_snapshot" DEPLOY_TEMPLATES_DIR_WAS_PRESENT 0)" = 0 ]; then
    assert_safe_path "$DEPLOY_TEMPLATES_DIR"
    rm -rf "$DEPLOY_TEMPLATES_DIR"
  fi
  if [ "$(snapshot_flag "$state_snapshot" CONFIG_DIR_WAS_PRESENT 0)" = 0 ]; then
    assert_safe_path "$CONFIG_DIR"
    rm -rf "$CONFIG_DIR"
  fi
  if [ "$(snapshot_flag "$state_snapshot" DATA_DIR_WAS_PRESENT 0)" = 0 ]; then
    assert_safe_path "$DATA_DIR"
    rm -rf "$DATA_DIR"
  elif [ "$(snapshot_flag "$state_snapshot" RELEASES_DIR_WAS_PRESENT 0)" = 0 ]; then
    rmdir "$RELEASES_DIR" >/dev/null 2>&1 || true
  fi

  if [ -n "$VHOSTS_ROOT_PATH" ] && [ "$(snapshot_flag "$state_snapshot" VHOSTS_ROOT_WAS_PRESENT 0)" = 0 ]; then
    rmdir "$VHOSTS_ROOT_PATH" >/dev/null 2>&1 || true
  fi

  "$SYSTEMCTL" daemon-reload
  if [ "$(snapshot_flag "$state_snapshot" SERVICE_WAS_ENABLED 0)" = 1 ]; then
    "$SYSTEMCTL" enable "$SERVICE_NAME"
  else
    "$SYSTEMCTL" disable "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
  if [ "$(snapshot_flag "$state_snapshot" SERVICE_WAS_ACTIVE 0)" = 1 ]; then
    "$SYSTEMCTL" start "$SERVICE_NAME"
  else
    "$SYSTEMCTL" stop "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
)

restore_snapshot() {
  snapshot=$1
  [ -d "$snapshot" ] || die "snapshot not found: $snapshot"
  [ -f "$snapshot/$BINARY_NAME" ] || die "snapshot has no previous binary: $snapshot"

  "$SYSTEMCTL" stop "$SERVICE_NAME" >/dev/null 2>&1 || true
  if [ "$(snapshot_flag "$snapshot" PRESERVE_LAYOUT 0)" = 1 ]; then
    [ "$PRESERVE_LAYOUT" = 1 ] \
      || die "preserve-layout snapshot requires the fixed preserve-layout lifecycle"
    validate_preserve_layout
    [ -f "$snapshot/$CLI_NAME" ] \
      || die "preserve-layout snapshot has no previous CLI binary: $snapshot"
    if ! atomic_install_binary "$snapshot/$BINARY_NAME" "$BINARY_PATH" \
      || ! atomic_install_binary "$snapshot/$CLI_NAME" "$CLI_PATH"; then
      die "could not atomically restore preserve-layout binaries from: $snapshot"
    fi
    was_active=$(snapshot_flag "$snapshot" SERVICE_WAS_ACTIVE 1)
    was_enabled=$(snapshot_flag "$snapshot" SERVICE_WAS_ENABLED "$was_active")
    restore_service_state "$was_active" "$was_enabled"
    return 0
  fi
  install -D -m 0755 "$snapshot/$BINARY_NAME" "$BINARY_PATH"
  cli_fallback=0
  [ -f "$snapshot/$CLI_NAME" ] && cli_fallback=1
  cli_was_present=$(snapshot_flag "$snapshot" CLI_WAS_PRESENT "$cli_fallback")
  if [ "$cli_was_present" = 1 ]; then
    [ -f "$snapshot/$CLI_NAME" ] || die "snapshot declares a CLI but has no previous CLI binary: $snapshot"
    install -D -m 0755 "$snapshot/$CLI_NAME" "$CLI_PATH"
  else
    rm -f "$CLI_PATH"
  fi
  installer_fallback=0
  [ -f "$snapshot/hserver-install" ] && installer_fallback=1
  installer_was_present=$(snapshot_flag "$snapshot" INSTALLER_WAS_PRESENT "$installer_fallback")
  if [ "$installer_was_present" = 1 ]; then
    [ -f "$snapshot/hserver-install" ] || die "snapshot declares an installer but has no previous lifecycle installer: $snapshot"
    install -D -m 0755 "$snapshot/hserver-install" "$LIFECYCLE_INSTALLER_PATH"
  else
    rm -f "$LIFECYCLE_INSTALLER_PATH"
  fi
  doctor_fallback=0
  [ -f "$snapshot/hserver-doctor" ] && doctor_fallback=1
  doctor_was_present=$(snapshot_flag "$snapshot" DOCTOR_WAS_PRESENT "$doctor_fallback")
  if [ "$doctor_was_present" = 1 ]; then
    [ -f "$snapshot/hserver-doctor" ] || die "snapshot declares a doctor but has no previous lifecycle doctor: $snapshot"
    install -D -m 0755 "$snapshot/hserver-doctor" "$DOCTOR_PATH"
  else
    rm -f "$DOCTOR_PATH"
  fi
  if [ -f "$snapshot/hserver.service" ]; then
    install -D -m 0644 "$snapshot/hserver.service" "$SERVICE_FILE"
  fi
  install -d -m 0755 "$NGINX_SNIPPETS_DIR"
  rm -f "$NGINX_SNIPPETS_DIR"/hserver-*.conf
  if [ "$(snapshot_flag "$snapshot" NGINX_SNIPPETS_WERE_PRESENT 0)" = 1 ]; then
    for snippet in "$snapshot/nginx-snippets"/hserver-*.conf; do
      [ -f "$snippet" ] || continue
      install -m 0644 "$snippet" "$NGINX_SNIPPETS_DIR/$(basename "$snippet")"
    done
  fi
  install -d -m 0755 "$LIFECYCLE_SNIPPETS_DIR"
  rm -f "$LIFECYCLE_SNIPPETS_DIR"/hserver-*.conf
  if [ "$(snapshot_flag "$snapshot" LIFECYCLE_SNIPPETS_WERE_PRESENT 0)" = 1 ]; then
    for snippet in "$snapshot/lifecycle-nginx-snippets"/hserver-*.conf; do
      [ -f "$snippet" ] || continue
      install -m 0644 "$snippet" "$LIFECYCLE_SNIPPETS_DIR/$(basename "$snippet")"
    done
  fi
  if [ "$(snapshot_flag "$snapshot" LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT 0)" = 0 ]; then
    rmdir "$LIFECYCLE_SNIPPETS_DIR" >/dev/null 2>&1 || true
  fi
  if [ "$(snapshot_flag "$snapshot" LIFECYCLE_ASSETS_DIR_WAS_PRESENT 0)" = 0 ]; then
    rmdir "$LIFECYCLE_ASSETS_DIR" >/dev/null 2>&1 || true
  fi
  if [ -f "$snapshot/databases.map" ]; then
    while IFS='=' read -r db_name db_target; do
      [ -n "$db_name" ] || continue
      [ -f "$snapshot/$db_name" ] || continue
      install -D -m 0600 "$snapshot/$db_name" "$db_target"
      rm -f "$db_target-wal" "$db_target-shm"
    done <"$snapshot/databases.map"
  fi
  "$SYSTEMCTL" daemon-reload
  was_active=$(snapshot_flag "$snapshot" SERVICE_WAS_ACTIVE 1)
  was_enabled=$(snapshot_flag "$snapshot" SERVICE_WAS_ENABLED "$was_active")
  if [ "$was_enabled" = 1 ]; then
    "$SYSTEMCTL" enable "$SERVICE_NAME"
  else
    "$SYSTEMCTL" disable "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
  if [ "$was_active" = 1 ]; then
    "$SYSTEMCTL" start "$SERVICE_NAME"
  else
    "$SYSTEMCTL" stop "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
}

install_or_upgrade() {
  mode=$1
  source_binary=$2
  source_cli=$3
  snapshot_retention=$(validate_snapshot_retention)
  validate_binary "$source_binary"
  validate_binary "$source_cli"
  if [ "$PRESERVE_LAYOUT" = 1 ]; then
    [ "$mode" = upgrade ] || die "preserve-layout mode is only valid for upgrade and rollback"
    validate_preserve_layout
  fi
  [ "$mode" = upgrade ] || run_preflight_doctor

  recovery_snapshot=
  if [ "$mode" = upgrade ]; then
    [ -f "$BINARY_PATH" ] || die "Heyserver is not installed; use install first"
  elif [ -e "$BINARY_PATH" ] || [ -e "$CLI_PATH" ]; then
    die "Heyserver panel or CLI is already installed; use upgrade"
  fi

  if [ "$PRESERVE_LAYOUT" != 1 ]; then
    resolve_managed_snippets_dir
    default_snippets_source >/dev/null \
      || die "managed Nginx snippets are missing; use the complete release package"
    installer_source >/dev/null \
      || die "installation lifecycle tool is missing; use the complete release package"
    doctor_source >/dev/null \
      || die "installation doctor is missing; use the complete release package"
  fi

  if [ "$mode" = install ]; then
    INITIAL_STATE_SNAPSHOT=$(mktemp -d "${TMPDIR:-/tmp}/hserver-install-state.XXXXXX") \
      || die "could not create the initial installation recovery state"
    snapshot_initial_state "$INITIAL_STATE_SNAPSHOT" \
      || die "could not capture the initial installation recovery state"

    if ! prepare_vhosts_root; then
      if rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
        cleanup_initial_state_snapshot
        die "installation vhosts root preparation failed and was rolled back"
      fi
      die "installation vhosts root preparation failed; automatic rollback was incomplete"
    fi
  fi

  directory_preparation_ok=0
  if [ "$PRESERVE_LAYOUT" = 1 ]; then
    install -d -m 0700 "$RELEASES_DIR" && directory_preparation_ok=1
  else
    install -d -m 0700 "$CONFIG_DIR" "$DATA_DIR" "$RELEASES_DIR" && directory_preparation_ok=1
  fi
  if [ "$directory_preparation_ok" != 1 ]; then
    if [ "$mode" = install ] && rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
      cleanup_initial_state_snapshot
      die "installation directory preparation failed and was rolled back"
    fi
    die "installation directory preparation failed; automatic rollback was incomplete"
  fi
  if [ "$PRESERVE_LAYOUT" != 1 ] && [ ! -f "$ENV_FILE" ] && ! install -d -m 0700 "$DATA_DIR/pgm-backups"; then
    if [ "$mode" = install ] && rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
      cleanup_initial_state_snapshot
      die "database backup directory preparation failed and was rolled back"
    fi
    die "database backup directory preparation failed; automatic rollback was incomplete"
  fi
  if [ "$PRESERVE_LAYOUT" != 1 ] && [ ! -f "$ENV_FILE" ] && ! (generate_environment); then
    if [ "$mode" = install ] && rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
      cleanup_initial_state_snapshot
      die "installation configuration generation failed and was rolled back"
    fi
    die "installation configuration generation failed; automatic rollback was incomplete"
  fi
  if [ "$mode" = install ] && ! seed_default_deploy_templates; then
    if rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
      cleanup_initial_state_snapshot
      die "installation could not seed deployment templates and was rolled back"
    fi
    die "installation could not seed deployment templates; automatic rollback was incomplete"
  fi

  if [ "$mode" = upgrade ]; then
    was_active=0
    was_enabled=0
    service_is_active && was_active=1
    service_is_enabled && was_enabled=1
    # Complete and validate the recovery snapshot while the old service is
    # still running. A copy, disk, or permission failure must not strand it.
    if ! recovery_snapshot=$(snapshot_state pre-upgrade "$was_active" "$was_enabled"); then
      die "upgrade recovery snapshot could not be created; existing service was left running"
    fi
    if ! validate_snapshot "$recovery_snapshot"; then
      discard_snapshot "$recovery_snapshot" || true
      die "upgrade recovery snapshot failed validation; existing service was left running"
    fi
    if ! mark_latest_snapshot "$recovery_snapshot"; then
      discard_snapshot "$recovery_snapshot" || true
      die "upgrade recovery snapshot could not be registered; existing service was left running"
    fi
    if ! prune_pre_upgrade_snapshots "$snapshot_retention"; then
      die "upgrade recovery snapshot retention cleanup failed; existing service was left running"
    fi
    if ! "$SYSTEMCTL" stop "$SERVICE_NAME"; then
      if restore_service_state "$was_active" "$was_enabled"; then
        die "upgrade could not stop the service; previous service state was restored"
      fi
      die "upgrade could not stop the service; previous service state restoration was incomplete"
    fi
  fi

  binary_install_ok=0
  if [ "$PRESERVE_LAYOUT" = 1 ]; then
    if atomic_install_binary "$source_binary" "$BINARY_PATH" \
      && atomic_install_binary "$source_cli" "$CLI_PATH"; then
      binary_install_ok=1
    fi
  elif install -D -m 0755 "$source_binary" "$BINARY_PATH" \
    && install -D -m 0755 "$source_cli" "$CLI_PATH"; then
    binary_install_ok=1
  fi
  if [ "$binary_install_ok" != 1 ]; then
    if [ -n "$recovery_snapshot" ]; then
      restore_snapshot "$recovery_snapshot"
      die "upgrade binary installation failed and was rolled back"
    fi
    if rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
      cleanup_initial_state_snapshot
      die "installation could not install the panel and CLI binaries and was rolled back"
    fi
    die "installation could not install the panel and CLI binaries; automatic rollback was incomplete"
  fi
  if [ "$PRESERVE_LAYOUT" != 1 ] && ! install_managed_snippets; then
    if [ -n "$recovery_snapshot" ]; then
      restore_snapshot "$recovery_snapshot"
      die "upgrade Nginx snippet installation failed and was rolled back"
    fi
    if rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
      cleanup_initial_state_snapshot
      die "installation could not install managed Nginx snippets and was rolled back"
    fi
    die "installation could not install managed Nginx snippets; automatic rollback was incomplete"
  fi
  if [ "$PRESERVE_LAYOUT" != 1 ] && ! install_lifecycle_tools; then
    if [ -n "$recovery_snapshot" ]; then
      restore_snapshot "$recovery_snapshot"
      die "upgrade lifecycle tool installation failed and was rolled back"
    fi
    if rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
      cleanup_initial_state_snapshot
      die "installation could not persist lifecycle tools and was rolled back"
    fi
    die "installation could not persist lifecycle tools; automatic rollback was incomplete"
  fi
  if [ "$PRESERVE_LAYOUT" != 1 ] && ! write_service_unit; then
    if [ -n "$recovery_snapshot" ]; then
      restore_snapshot "$recovery_snapshot"
      die "upgrade service unit installation failed and was rolled back"
    fi
    if rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
      cleanup_initial_state_snapshot
      die "installation could not install the systemd service and was rolled back"
    fi
    die "installation could not install the systemd service; automatic rollback was incomplete"
  fi
  service_activation_ok=0
  if [ "$PRESERVE_LAYOUT" = 1 ]; then
    if [ "$was_active" = 1 ]; then
      "$SYSTEMCTL" start "$SERVICE_NAME" && service_activation_ok=1
    else
      service_activation_ok=1
    fi
  elif "$SYSTEMCTL" daemon-reload && "$SYSTEMCTL" enable --now "$SERVICE_NAME"; then
    service_activation_ok=1
  fi
  if [ "$service_activation_ok" != 1 ]; then
    if [ -n "$recovery_snapshot" ]; then
      restore_snapshot "$recovery_snapshot"
      die "upgrade service activation failed and was rolled back"
    fi
    if rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
      cleanup_initial_state_snapshot
      die "installation could not activate the systemd service and was rolled back"
    fi
    die "installation could not activate the systemd service; automatic rollback was incomplete"
  fi

  if [ "$PRESERVE_LAYOUT" = 1 ] && [ "$was_active" = 0 ]; then
    health_ok=1
  elif wait_healthy; then
    health_ok=1
  else
    health_ok=0
  fi
  if [ "$health_ok" = 1 ]; then
    printf '%s completed successfully.\n' "$mode"
    [ -n "$recovery_snapshot" ] && printf 'Recovery snapshot: %s\n' "$recovery_snapshot"
    [ "$mode" = install ] && cleanup_initial_state_snapshot
    if [ "$mode" = install ] && [ "${HSERVER_INSTALL_DEFER_NEXT_STEPS:-0}" != 1 ]; then
      show_next_steps
    fi
    return 0
  fi

  if [ -n "$recovery_snapshot" ]; then
    if [ "$PRESERVE_LAYOUT" = 1 ]; then
      printf '%s\n' "New release failed its health check; restoring the previous binaries and service state." >&2
    else
      printf '%s\n' "New release failed its health check; restoring the previous binary and database snapshot." >&2
    fi
    restore_snapshot "$recovery_snapshot"
    if [ "$(snapshot_flag "$recovery_snapshot" SERVICE_WAS_ACTIVE 1)" = 1 ]; then
      wait_healthy || die "automatic rollback completed, but the restored service is still unhealthy"
    fi
    die "upgrade failed and was rolled back"
  fi
  if rollback_initial_install "$INITIAL_STATE_SNAPSHOT"; then
    cleanup_initial_state_snapshot
    die "installation failed its health check and was rolled back"
  fi
  die "installation failed its health check; automatic rollback was incomplete; inspect: journalctl -u hserver"
}

latest_snapshot() {
  marker=$RELEASES_DIR/latest-pre-upgrade
  if [ -f "$marker" ]; then
    marked_snapshot=$(cat "$marker")
    case "$marked_snapshot" in
      "$RELEASES_DIR"/*)
        if [ -d "$marked_snapshot" ]; then
          printf '%s\n' "$marked_snapshot"
          return 0
        fi
        ;;
    esac
  fi
  find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d -name '*-pre-upgrade*' 2>/dev/null \
    | sort | tail -n 1
}

rollback_release() {
  snapshot_retention=$(validate_snapshot_retention)
  if [ "$PRESERVE_LAYOUT" = 1 ]; then
    validate_preserve_layout
  else
    resolve_managed_snippets_dir
  fi
  target=$(latest_snapshot)
  [ -n "$target" ] || die "no upgrade recovery snapshot is available"
  was_active=0
  was_enabled=0
  service_is_active && was_active=1
  service_is_enabled && was_enabled=1
  "$SYSTEMCTL" stop "$SERVICE_NAME" >/dev/null 2>&1 || true
  snapshot_state pre-rollback "$was_active" "$was_enabled" >/dev/null
  prune_pre_upgrade_snapshots "$snapshot_retention"
  restore_snapshot "$target"
  if [ "$(snapshot_flag "$target" SERVICE_WAS_ACTIVE 1)" = 1 ]; then
    wait_healthy || die "rollback restored $target, but Heyserver is still unhealthy"
  fi
  printf 'Rollback completed from snapshot: %s\n' "$target"
}

uninstall_release() {
  purge_config=$1
  purge_data=$2
  "$SYSTEMCTL" disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
  rm -f "$SERVICE_FILE" "$BINARY_PATH" "$CLI_PATH" "$LIFECYCLE_INSTALLER_PATH" "$DOCTOR_PATH"
  rm -f "$LIFECYCLE_SNIPPETS_DIR"/hserver-*.conf
  rmdir "$LIFECYCLE_SNIPPETS_DIR" >/dev/null 2>&1 || true
  rmdir "$LIFECYCLE_ASSETS_DIR" >/dev/null 2>&1 || true
  "$SYSTEMCTL" daemon-reload

  if [ "$purge_config" = 1 ]; then
    assert_safe_path "$CONFIG_DIR"
    rm -rf "$CONFIG_DIR"
  fi
  if [ "$purge_data" = 1 ]; then
    assert_safe_path "$DATA_DIR"
    rm -rf "$DATA_DIR"
  fi

  printf '%s\n' "Heyserver panel, CLI, and service were removed."
  [ "$purge_config" = 1 ] || printf 'Configuration preserved: %s\n' "$CONFIG_DIR"
  [ "$purge_data" = 1 ] || printf 'Application data preserved: %s\n' "$DATA_DIR"
}

require_root
require_command install

case "$PRESERVE_LAYOUT" in
  0|1) ;;
  *) die "HSERVER_PRESERVE_LAYOUT must be 0 or 1" ;;
esac

command_name=${1:-}
[ -n "$command_name" ] || { usage; exit 1; }
shift

binary_source=
cli_source=
purge_config=0
purge_data=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --binary)
      [ "$#" -ge 2 ] || die "--binary requires a path"
      binary_source=$2
      shift 2
      ;;
    --cli-binary)
      [ "$#" -ge 2 ] || die "--cli-binary requires a path"
      cli_source=$2
      shift 2
      ;;
    --vhosts-root)
      [ "$#" -ge 2 ] || die "--vhosts-root requires a path"
      validate_vhosts_root "$2"
      VHOSTS_ROOT_OPTION=$2
      VHOSTS_ROOT_PATH=$(root_path "$2")
      shift 2
      ;;
    --purge-config) purge_config=1; shift ;;
    --purge-data) purge_data=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

case "$command_name" in
  install)
    if [ -z "$binary_source" ]; then
      binary_source=$(default_binary_source) || die "no release binary found; use --binary PATH"
    fi
    if [ -z "$cli_source" ]; then
      cli_source=$(default_cli_source) || die "no release CLI found; use --cli-binary PATH"
    fi
    install_or_upgrade install "$binary_source" "$cli_source"
    ;;
  upgrade)
    [ -z "$VHOSTS_ROOT_OPTION" ] || die "upgrade does not accept --vhosts-root"
    if [ -z "$binary_source" ]; then
      binary_source=$(default_binary_source) || die "no release binary found; use --binary PATH"
    fi
    if [ -z "$cli_source" ]; then
      cli_source=$(default_cli_source) || die "no release CLI found; use --cli-binary PATH"
    fi
    install_or_upgrade upgrade "$binary_source" "$cli_source"
    ;;
  rollback)
    [ -z "$binary_source" ] && [ -z "$cli_source" ] || die "rollback does not accept binary arguments"
    [ -z "$VHOSTS_ROOT_OPTION" ] || die "rollback does not accept --vhosts-root"
    rollback_release
    ;;
  next-steps)
    [ -z "$binary_source" ] && [ -z "$cli_source" ] \
      && [ -z "$VHOSTS_ROOT_OPTION" ] \
      && [ "$purge_config" = 0 ] && [ "$purge_data" = 0 ] \
      || die "next-steps does not accept lifecycle arguments"
    show_next_steps
    ;;
  uninstall)
    [ -z "$binary_source" ] && [ -z "$cli_source" ] || die "uninstall does not accept binary arguments"
    [ -z "$VHOSTS_ROOT_OPTION" ] || die "uninstall does not accept --vhosts-root"
    uninstall_release "$purge_config" "$purge_data"
    ;;
  -h|--help) usage ;;
  *) usage; die "unknown command: $command_name" ;;
esac
