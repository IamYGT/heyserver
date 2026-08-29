#!/usr/bin/env bash
# Sync canonical bot repo into hserver-panel integrations tree.
set -euo pipefail

integration_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SRC="${HSERVER_BOT_SRC:?set HSERVER_BOT_SRC to the external bot checkout}"
DEST="${HSERVER_PANEL_INTEGRATION:-$integration_root}"

if [[ ! -d "$SRC" ]]; then
  echo "error: source directory not found: $SRC" >&2
  exit 1
fi

mkdir -p "$DEST"

rsync -a --delete \
  --exclude '.venv/' \
  --exclude '.git/' \
  --exclude '.env' \
  --exclude '__pycache__/' \
  --exclude '*.py[cod]' \
  --exclude '.pytest_cache/' \
  --exclude '.mypy_cache/' \
  --exclude 'dist/' \
  --exclude '*.egg-info/' \
  "$SRC/" "$DEST/"

echo "synced $SRC -> $DEST"
