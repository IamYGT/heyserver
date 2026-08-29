#!/usr/bin/env bash
# Günlük GDrive + yedekleme sağlık kontrolü. Hata → log + exit 1 (cron/monitoring).
set -euo pipefail

ENV_FILE="${HSERVER_ENV_FILE:-/etc/hserver/hserver.env}"
DATA_DIR="${HSERVER_DATA_DIR:-/var/lib/hserver}"
BASE="${HSERVER_BASE_URL:-http://127.0.0.1:3085}"
LOG="${LOG:-/var/log/hserver-gdrive-health.log}"
LEGACY_BACKUP_TIMER="${LEGACY_BACKUP_TIMER:-}"

env_value() {
  [[ -f "$ENV_FILE" ]] || return 0
  awk -F= -v key="$1" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$ENV_FILE"
}

EMAIL="${HSERVER_ADMIN_EMAIL:-$(env_value HSERVER_ADMIN_EMAIL)}"
PASS="${HSERVER_ADMIN_PASS:-$(env_value HSERVER_ADMIN_PASS)}"

ts() { date -Iseconds; }
log() { echo "[$(ts)] $*" | tee -a "$LOG"; }

fail() { log "FAIL: $*"; exit 1; }

[[ -n "$EMAIL" && -n "$PASS" ]] || fail "HSERVER_ADMIN_EMAIL and HSERVER_ADMIN_PASS are not configured"

# hserver ayakta mı?
curl -sf --max-time 10 "$BASE/api/health" >/dev/null || fail "hserver API yanıt vermiyor ($BASE)"

LOGIN_PAYLOAD=$(python3 -c 'import json,sys; print(json.dumps({"email":sys.argv[1],"password":sys.argv[2]}))' "$EMAIL" "$PASS")
TOKEN=$(curl -sf --max-time 15 -X POST "$BASE/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d "$LOGIN_PAYLOAD" \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))")
[ -n "$TOKEN" ] || fail "hserver login başarısız"

# GDrive OAuth
GDRIVE=$(curl -sf --max-time 30 -H "Authorization: Bearer $TOKEN" "$BASE/api/backups/gdrive/status")
python3 - <<'PY' "$GDRIVE" || fail "GDrive bağlı değil veya lastError var"
import json, sys
s = json.loads(sys.argv[1])
if not s.get("connected"):
    raise SystemExit("connected=false")
err = (s.get("settings") or {}).get("lastError") or ""
if err:
    raise SystemExit(f"lastError={err}")
if not (s.get("settings") or {}).get("autoUpload"):
    raise SystemExit("autoUpload=false")
print("gdrive_ok", s.get("email"), (s.get("settings") or {}).get("lastUploadAt"))
PY

# Token dosyaları
[ -f "$DATA_DIR/gdrive-token.json" ] || fail "gdrive-token.json yok"
[ -f "$DATA_DIR/rclone.conf" ] || fail "rclone.conf yok"

# Son PG yedeği 36 saatten eski mi?
LATEST=$(find "$DATA_DIR/backups" -maxdepth 1 -type f -name 'backup-*-db-all-postgresql.sql.gz' -printf '%T@ %p\n' 2>/dev/null | sort -nr | sed -n '1s/^[^ ]* //p')
[ -n "$LATEST" ] || fail "lokal PG dump bulunamadı"
AGE_H=$(( ($(date +%s) - $(stat -c %Y "$LATEST")) / 3600 ))
[ "$AGE_H" -le 36 ] || fail "son PG dump ${AGE_H}h eski: $(basename "$LATEST")"

# Optionally ensure an installation-specific legacy timer is disabled.
if [[ -n "$LEGACY_BACKUP_TIMER" ]] && systemctl is-enabled "$LEGACY_BACKUP_TIMER" 2>/dev/null | grep -q enabled; then
  fail "$LEGACY_BACKUP_TIMER is still enabled — duplicate backup risk"
fi

log "OK: GDrive bağlı, autoUpload açık, son dump $(basename "$LATEST") (${AGE_H}h)"

# Snapshot uyarısı (kritik değil — PG dump offsite asıl güvence)
SNAP_AGE_DAYS=999
SNAP_FILE="$DATA_DIR/snapshot-settings.json"
if [ -f "$SNAP_FILE" ]; then
  LAST_RUN=$(python3 -c "import json; print(json.load(open('$SNAP_FILE')).get('lastRunAt',''))" 2>/dev/null || true)
  if [ -n "$LAST_RUN" ]; then
    SNAP_EPOCH=$(date -d "$LAST_RUN" +%s 2>/dev/null || echo 0)
    NOW=$(date +%s)
    SNAP_AGE_DAYS=$(( (NOW - SNAP_EPOCH) / 86400 ))
  fi
fi
if [ "$SNAP_AGE_DAYS" -gt 8 ]; then
  log "WARN: son restic snapshot ${SNAP_AGE_DAYS} gün önce — 04:00 cron veya panelden Snapshot al"
fi
