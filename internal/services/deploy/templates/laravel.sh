#!/bin/bash
# Heyserver — Laravel Deployment Script Template
# Runs inside the project directory after `git pull`.
# Customize variables at the top to match your environment.
set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────
PHP_BIN="${PHP_BIN:-php8.4}"
COMPOSER_BIN="${COMPOSER_BIN:-composer}"
NPM_BIN="${NPM_BIN:-npm}"

# Set to the PHP-FPM systemd service name to reload after deployment.
# Leave empty to skip (e.g. PHP_FPM_SERVICE="php8.4-fpm").
PHP_FPM_SERVICE="${PHP_FPM_SERVICE:-}"

echo "==> Installing Composer dependencies (--no-dev)"
$COMPOSER_BIN install --no-dev --optimize-autoloader --no-interaction --no-progress

echo "==> Installing NPM dependencies"
$NPM_BIN ci --prefer-offline

echo "==> Building frontend assets"
$NPM_BIN run build

echo "==> Caching config"
$PHP_BIN artisan config:cache

echo "==> Caching routes"
$PHP_BIN artisan route:cache

echo "==> Caching views"
$PHP_BIN artisan view:cache

echo "==> Running database migrations"
$PHP_BIN artisan migrate --force

echo "==> Restarting queue workers"
$PHP_BIN artisan queue:restart

if [ -n "$PHP_FPM_SERVICE" ]; then
  echo "==> Reloading PHP-FPM: $PHP_FPM_SERVICE"
  sudo systemctl reload "$PHP_FPM_SERVICE"
fi

echo "==> Laravel deployment complete"
