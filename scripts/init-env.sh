#!/usr/bin/env sh
set -eu

CDPATH=
export CDPATH
root_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
target=${1:-"$root_dir/.env"}

if [ -e "$target" ]; then
  printf '%s\n' "Refusing to overwrite existing environment file: $target" >&2
  exit 1
fi

if ! command -v openssl >/dev/null 2>&1; then
  printf '%s\n' "openssl is required to generate installation secrets." >&2
  exit 1
fi

old_umask=$(umask)
umask 077
jwt_secret=$(openssl rand -hex 32)
admin_password=$(openssl rand -hex 16)
cron_secret=$(openssl rand -hex 32)

cat >"$target" <<EOF
VERSION=dev
HSERVER_PORT=3085
HSERVER_JWT_SECRET=$jwt_secret
HSERVER_ADMIN_EMAIL=admin@localhost
HSERVER_ADMIN_PASS=$admin_password
HSERVER_CRON_SECRET=$cron_secret
HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT=3
# Optional installation-owned site root. Leave empty until a local absolute
# path is selected; domain, file, and site-backup capabilities stay disabled.
HSERVER_VHOSTS_ROOT=
# Installation-local PHP-FPM roots used for readiness and management. An
# explicitly configured relative path fails closed instead of using a host default.
HSERVER_PHP_CONFIG_ROOT=/etc/php
HSERVER_PHP_BINARY_ROOT=/usr/sbin
HSERVER_NGINX_SITES_AVAILABLE=/etc/nginx/sites-available
HSERVER_NGINX_SITES_ENABLED=/etc/nginx/sites-enabled
HSERVER_NGINX_SNIPPETS_DIR=/etc/nginx/snippets
HSERVER_PGM_BACKUP_DIR=/var/lib/hserver/pgm-backups
HSERVER_PG_RUN_AS=postgres
HSERVER_PG_BACKUP_USER=postgres
HSERVER_PG_BACKUP_HOST=
HSERVER_PG_BACKUP_PORT=
HSERVER_PG_PASSFILE=
HSERVER_MYSQL_DEFAULTS_FILE=

# Optional integrations. Empty provider settings keep integrations explicitly
# not configured until the operator supplies local endpoints and credentials.
HSERVER_UPDATE_MANIFEST_URL=
HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS=
# Optional fixed update destinations. Empty observes the running hserver-panel
# executable and its sibling hserverctl; both paths are validated before staging mutates state.
HSERVER_UPDATE_PANEL_BINARY_PATH=
HSERVER_UPDATE_CLI_BINARY_PATH=
STALWART_URL=
STALWART_API_KEY=
STALWART_ADMIN_USER=
STALWART_ADMIN_PASS=
HSERVER_STALWART_SERVICE=
HSERVER_STALWART_CONFIG_PATH=
HSERVER_STALWART_BIN=
HSERVER_CF_API_TOKEN=
HSERVER_CF_API_EMAIL=
HSERVER_DOMAIN_DNS_ORIGIN=
HSERVER_DOMAIN_DNS_PROXIED=false

# Optional Cloudflare mail DNS reconciliation. Hostname is required to enable it.
HSERVER_MAIL_DNS_HOSTNAME=
HSERVER_MAIL_DNS_PUBLIC_IP=
HSERVER_MAIL_DNS_MX_PRIORITY=10
HSERVER_MAIL_DNS_SPF=
HSERVER_MAIL_DNS_DMARC=

# Optional PM2 process management. User is required to enable it.
HSERVER_PM2_USER=
HSERVER_PM2_HOME=
HSERVER_PM2_BIN=pm2
# Optional absolute roots for PM2 deploy paths. Set an explicit comma-separated
# allowlist when PM2 management is enabled; empty keeps it disabled.
HSERVER_PM2_ALLOWED_ROOTS=

# Optional certificate lifecycle. DNS credentials are read from a protected INI file.
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

umask "$old_umask"
printf '%s\n' "Environment file created with mode 0600: $target"
printf '%s\n' "Initial credentials are stored in that file and were not printed."
