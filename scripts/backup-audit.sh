#!/usr/bin/env bash
# Heyserver backup/snapshot durum özeti (smoke audit)
set -euo pipefail
ENV_FILE="${HSERVER_ENV_FILE:-/etc/hserver/hserver.env}"
BASE="${HSERVER_BASE_URL:-http://127.0.0.1:3085}"

env_value() {
  [[ -f "$ENV_FILE" ]] || return 0
  awk -F= -v key="$1" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$ENV_FILE"
}

EMAIL="${HSERVER_ADMIN_EMAIL:-$(env_value HSERVER_ADMIN_EMAIL)}"
PASS="${HSERVER_ADMIN_PASS:-$(env_value HSERVER_ADMIN_PASS)}"
CRON="${HSERVER_CRON_SECRET:-$(env_value HSERVER_CRON_SECRET)}"
if [[ -z "$EMAIL" || -z "$PASS" || -z "$CRON" ]]; then
  echo "ERROR: HSERVER_ADMIN_EMAIL, HSERVER_ADMIN_PASS, and HSERVER_CRON_SECRET must be configured." >&2
  exit 1
fi

LOGIN_PAYLOAD=$(python3 -c 'import json,sys; print(json.dumps({"email":sys.argv[1],"password":sys.argv[2]}))' "$EMAIL" "$PASS")
TOKEN=$(curl -sf -X POST "$BASE/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d "$LOGIN_PAYLOAD" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))")

echo "=== Health ==="
curl -sf "$BASE/api/health" | python3 -m json.tool

echo "=== Deploy preflight ==="
curl -sf -H "X-Cron-Secret: $CRON" "$BASE/api/internal/deploy/preflight" | python3 -m json.tool

echo "=== GDrive status ==="
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/api/backups/gdrive/status" | python3 -c "
import sys,json
s=json.load(sys.stdin)
print('connected', s.get('connected'))
print('email', s.get('email',''))
print('autoUpload', (s.get('settings') or {}).get('autoUpload'))
if not s.get('connected'):
    print('FAIL: GDrive not connected')
    sys.exit(1)
"

echo "=== Snapshot status ==="
SNAP_JSON=$(curl -sf --max-time 120 -H "Authorization: Bearer $TOKEN" "$BASE/api/backups/snapshot/status")
echo "$SNAP_JSON" | python3 -c "
import sys,json
s=json.load(sys.stdin)
print('resticFound', s.get('resticFound'))
print('repoInitialized', s.get('repoInitialized'))
print('passwordSet', s.get('passwordSet'))
print('driveConnected', s.get('driveConnected'))
stats=s.get('repoStats') or {}
print('repoStats', stats)
print('lastError', (s.get('settings') or {}).get('lastError','')[:120])
fail=False
for k in ('resticFound','passwordSet','driveConnected'):
    if not s.get(k):
        print(f'FAIL: snapshot.{k}=false')
        fail=True
if fail:
    sys.exit(1)
count=int(stats.get('snapshotCount') or 0)
if count < 1:
    print('WARN: henüz tamamlanmış snapshot yok — panelden Snapshot al veya 04:00 cron bekleyin')
"

echo "=== Schedules ==="
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/api/backups/schedules" | python3 -c "
import sys,json
s=json.load(sys.stdin)
if s is None:
    print('WARN: zamanlama kapalı')
else:
    print('type', s.get('type'), 'frequency', s.get('frequency'), 'time', s.get('time'))
"

echo "=== Backups ==="
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/api/backups" | python3 -c "
import sys,json
d=json.load(sys.stdin)
bs=d.get('backups',d if isinstance(d,list) else [])
inv=sum(1 for b in bs if b.get('status')=='invalid')
print('total', len(bs), 'invalid', inv)
"

echo "=== Active jobs ==="
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/api/backups/jobs?active=1" | python3 -c "
import sys,json
d=json.load(sys.stdin)
jobs=d.get('jobs',[])
print('count', len(jobs))
for j in jobs: print(' ', j.get('id'), j.get('type'), j.get('status'))
"

echo "=== Disk ==="
df -h / | tail -1
df -P / | tail -1 | awk '{
  pct=$5; gsub(/%/,"",pct);
  if (pct+0 >= 90) { print "WARN: disk kullanımı %" pct " — snapshot öncesi eski arşivleri temizleyin" }
}'
