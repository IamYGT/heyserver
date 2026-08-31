#!/usr/bin/env bash
# Google Drive OAuth keepalive with an optional installation-owned alert hook.
set -euo pipefail

ENV_FILE="${HSERVER_ENV_FILE:-/etc/hserver/hserver.env}"
BASE="${HSERVER_BASE_URL:-http://127.0.0.1:3085}"
LOG="${LOG:-/var/log/hserver-gdrive-keepalive.log}"
STATE="${STATE:-/var/run/hserver-gdrive-keepalive.state}"
ALERT_SCRIPT="${ALERT_SCRIPT:-}"

ts() { date -Iseconds; }
log() { echo "[$(ts)] $*" >>"$LOG"; }

env_value() {
  [[ -f "$ENV_FILE" ]] || return 0
  awk -F= -v key="$1" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$ENV_FILE"
}

EMAIL="${HSERVER_ADMIN_EMAIL:-$(env_value HSERVER_ADMIN_EMAIL)}"
PASS="${HSERVER_ADMIN_PASS:-$(env_value HSERVER_ADMIN_PASS)}"
if [[ -z "$EMAIL" || -z "$PASS" ]]; then
  echo "ERROR: HSERVER_ADMIN_EMAIL and HSERVER_ADMIN_PASS must be configured." >&2
  exit 1
fi
prev_state="unknown"
[[ -f "$STATE" ]] && prev_state=$(cat "$STATE" 2>/dev/null || echo unknown)

alert_once() {
  local msg="$1"
  log "ALERT: $msg"
  if [[ "$prev_state" != "fail" && -x "$ALERT_SCRIPT" ]]; then
    bash "$ALERT_SCRIPT" "GDrive keepalive" "$msg" 2>/dev/null || true
  fi
  echo fail >"$STATE"
}

LOGIN_PAYLOAD=$(python3 -c 'import json,sys; print(json.dumps({"email":sys.argv[1],"password":sys.argv[2]}))' "$EMAIL" "$PASS")
TOKEN=$(curl -sf --max-time 15 -X POST "$BASE/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d "$LOGIN_PAYLOAD" \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || true)

if [[ -z "$TOKEN" ]]; then
  alert_once "Heyserver login failed in gdrive-keepalive"
  exit 1
fi

STATUS=$(curl -sf --max-time 30 -H "Authorization: Bearer $TOKEN" "$BASE/api/backups/gdrive/status" 2>/dev/null || echo '{}')
CONNECTED=$(python3 -c "import json,sys; s=json.load(sys.stdin); print('yes' if s.get('connected') else 'no')" <<<"$STATUS" 2>/dev/null || echo no)

curl -sf --max-time 60 -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/backups/gdrive/test" >/dev/null 2>&1 || true

if [[ "$CONNECTED" != "yes" ]]; then
  ERR=$(python3 -c "import json,sys; s=json.load(sys.stdin); print((s.get('settings') or {}).get('lastError','')[:120])" <<<"$STATUS" 2>/dev/null || true)
  alert_once "GDrive disconnected: ${ERR:-unknown}"
  exit 1
fi

log "OK: GDrive connected"
if [[ "$prev_state" == "fail" && -x "$ALERT_SCRIPT" ]]; then
  bash "$ALERT_SCRIPT" "GDrive keepalive" "GDrive connection recovered ($(hostname))" 2>/dev/null || true
fi
echo ok >"$STATE"
