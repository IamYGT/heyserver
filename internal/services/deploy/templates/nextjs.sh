#!/bin/bash
# HServer Panel — Next.js Deployment Script Template
# Runs inside the project directory after `git pull`.
set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────
NPM_BIN="${NPM_BIN:-npm}"
# PM2 app name — e.g. "customer-portal". Leave empty to skip PM2 restart.
PM2_APP_NAME="${PM2_APP_NAME:-}"

echo "==> Installing NPM dependencies"
$NPM_BIN ci --prefer-offline

echo "==> Building Next.js application"
$NPM_BIN run build

if [ -n "$PM2_APP_NAME" ]; then
  echo "==> Reloading PM2 process: $PM2_APP_NAME"
  pm2 reload "$PM2_APP_NAME" --update-env
  pm2 save
else
  echo "==> PM2_APP_NAME not set — skipping PM2 restart"
fi

echo "==> Next.js deployment complete"
