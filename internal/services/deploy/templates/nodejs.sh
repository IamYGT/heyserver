#!/bin/bash
# Heyserver Panel — Generic Node.js Deployment Script Template
# Runs inside the project directory after `git pull`.
set -euo pipefail

# ── Configuration ─────────────────────────────────────────────────────────────
NPM_BIN="${NPM_BIN:-npm}"
# npm run script name — leave empty if there is no build step.
BUILD_CMD="${BUILD_CMD:-}"
# PM2 app name — leave empty to skip PM2 restart.
PM2_APP_NAME="${PM2_APP_NAME:-}"

echo "==> Installing NPM dependencies"
$NPM_BIN ci --prefer-offline

if [ -n "$BUILD_CMD" ]; then
  echo "==> Running: npm run $BUILD_CMD"
  $NPM_BIN run "$BUILD_CMD"
fi

if [ -n "$PM2_APP_NAME" ]; then
  echo "==> Reloading PM2 process: $PM2_APP_NAME"
  pm2 reload "$PM2_APP_NAME" --update-env
  pm2 save
else
  echo "==> PM2_APP_NAME not set — skipping PM2 restart"
fi

echo "==> Node.js deployment complete"
