#!/bin/bash
# HServer Panel — Astro Deployment Script Template
# Runs inside the project directory after `git pull`.
set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────
NPM_BIN="${NPM_BIN:-npm}"
# PM2 app name — leave empty to skip PM2 restart.
PM2_APP_NAME="${PM2_APP_NAME:-}"
# Output mode: "static" | "server" | "hybrid"
OUTPUT_MODE="${OUTPUT_MODE:-server}"
# Set to "true" to reload nginx after a static build.
NGINX_RELOAD="${NGINX_RELOAD:-false}"

echo "==> Installing NPM dependencies"
$NPM_BIN ci --prefer-offline

echo "==> Building Astro application (output: $OUTPUT_MODE)"
$NPM_BIN run build

if [ "$OUTPUT_MODE" = "static" ]; then
  echo "==> Static output — no server restart required"
  if [ "$NGINX_RELOAD" = "true" ]; then
    echo "==> Reloading nginx"
    sudo systemctl reload nginx
  fi
else
  if [ -n "$PM2_APP_NAME" ]; then
    echo "==> Reloading PM2 process: $PM2_APP_NAME"
    pm2 reload "$PM2_APP_NAME" --update-env
    pm2 save
  else
    echo "==> PM2_APP_NAME not set — skipping PM2 restart"
  fi
fi

echo "==> Astro deployment complete"
