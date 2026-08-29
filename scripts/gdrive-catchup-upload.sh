#!/usr/bin/env bash
# Upload selected existing backups to Drive after OAuth is connected.
set -euo pipefail

ENV_FILE="${HSERVER_ENV_FILE:-/etc/hserver/hserver.env}"
DATA_DIR="${HSERVER_DATA_DIR:-/var/lib/hserver}"
BASE="${HSERVER_BASE_URL:-http://127.0.0.1:3085}"
PANEL_URL="${HSERVER_PANEL_URL:-$BASE}"
RCLONE_CONFIG="${RCLONE_CONFIG:-$DATA_DIR/rclone.conf}"
DB_PATH="${HSERVER_DB_PATH:-$DATA_DIR/hserver.db}"

env_value() {
  [[ -f "$ENV_FILE" ]] || return 0
  awk -F= -v key="$1" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$ENV_FILE"
}

EMAIL="${HSERVER_ADMIN_EMAIL:-$(env_value HSERVER_ADMIN_EMAIL)}"
PASS="${HSERVER_ADMIN_PASS:-$(env_value HSERVER_ADMIN_PASS)}"

if [[ $# -eq 0 ]]; then
  echo "Usage: $0 BACKUP_ID [BACKUP_ID ...]" >&2
  exit 1
fi
if [[ -z "$EMAIL" || -z "$PASS" ]]; then
  echo "ERROR: HSERVER_ADMIN_EMAIL and HSERVER_ADMIN_PASS must be set in the environment or $ENV_FILE." >&2
  exit 1
fi

if [ ! -f "$RCLONE_CONFIG" ]; then
  echo "ERROR: OAuth is not connected; $RCLONE_CONFIG does not exist."
  echo "Panel: $PANEL_URL -> Backups -> Google Drive"
  exit 1
fi

LOGIN_PAYLOAD=$(python3 -c 'import json,sys; print(json.dumps({"email":sys.argv[1],"password":sys.argv[2]}))' "$EMAIL" "$PASS")
TOKEN=$(curl -sf -X POST "$BASE/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d "$LOGIN_PAYLOAD" | python3 -c "import json,sys; print(json.load(sys.stdin)['token'])")

for id in "$@"; do
  echo "Starting upload: $id"
  curl -sf -X POST "$BASE/api/backups/upload/${id}" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' && echo " -> OK" || echo " -> FAIL"
done

if command -v sqlite3 >/dev/null 2>&1 && [[ -f "$DB_PATH" ]]; then
  sqlite3 "$DB_PATH" \
    "SELECT 'lastUploadAt='||json_extract(value,'$.lastUploadAt'), 'lastError='||coalesce(json_extract(value,'$.lastError'),'none') FROM settings WHERE key='gdrive_settings';"
fi
