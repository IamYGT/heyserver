#!/usr/bin/env bash
set -euo pipefail

root_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
restore_prefix=()
if (( EUID != 0 )); then
  command -v sudo >/dev/null 2>&1 || {
    echo "restore database test requires root or sudo; install sudo or rerun with elevated access" >&2
    exit 1
  }
  # restore-db.sh must stay root-only; elevate only its disposable fixture
  # runs so the surrounding contributor test remains unprivileged.
  restore_prefix=(sudo --)
fi
tmp=$(mktemp -d)
trap 'find "$tmp" -xdev -depth -delete' EXIT INT TERM
restore_owner_uid=$(id -u)
restore_owner_gid=$(id -g)

fixture_backup_script="$tmp/backup-db"
cat >"$fixture_backup_script" <<'BACKUP'
#!/usr/bin/env bash
set -euo pipefail

backup_output=$("${HSERVER_TEST_REAL_BACKUP_SCRIPT:?}")
backup=${backup_output#Backup complete: }
[[ "$backup" != "$backup_output" && -f "$backup" && ! -L "$backup" ]] || {
  echo "fixture backup helper did not produce a regular backup" >&2
  exit 1
}
[[ -d "${HSERVER_DB_BACKUP_DIR:?}" ]] || {
  echo "fixture backup helper did not produce a backup directory" >&2
  exit 1
}
chown "$HSERVER_TEST_OWNER_UID:$HSERVER_TEST_OWNER_GID" \
  "$HSERVER_DB_BACKUP_DIR" "$backup"
printf 'Backup complete: %s\n' "$backup"
BACKUP
chmod 0755 "$fixture_backup_script"

mkdir -p "$tmp/data/notification-channel-secrets" "$tmp/state"
chmod 0700 "$tmp/data/notification-channel-secrets"
db="$tmp/data/hserver.db"
secret="$tmp/data/notification-channel-secrets/channel-1.json"
backup_dir="$tmp/backups"

create_db() {
  local path=$1 marker=$2
  sqlite3 "$path" <<SQL
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL);
CREATE TABLE notification_channels (id INTEGER PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL, config TEXT NOT NULL, enabled INTEGER NOT NULL);
CREATE TABLE restore_probe (marker TEXT NOT NULL);
INSERT INTO schema_migrations(version) VALUES (1);
INSERT INTO users(id, email) VALUES (1, 'operator@example.test');
INSERT INTO notification_channels(id, name, type, config, enabled) VALUES (1, 'Ops Webhook', 'webhook', 'file:channel-1.json', 1);
INSERT INTO restore_probe(marker) VALUES ('$marker');
SQL
}

write_secret() {
  local path=$1 value=$2
  printf '{"webhookUrl":"https://%s.example.test/hook"}\n' "$value" >"$path"
  chmod 0600 "$path"
}

create_db "$db" before-restore
write_secret "$secret" before-restore
backup_output=$(HSERVER_DATA_DIR="$tmp/data" \
  HSERVER_DB_PATH="$db" \
  HSERVER_DB_BACKUP_DIR="$backup_dir" \
  HSERVER_DB_BACKUP_RETENTION_DAYS=30 \
  "$root_dir/scripts/backup-db.sh")
backup=${backup_output#Backup complete: }
[[ "$backup" == *.panel-backup.tar.gz && -s "$backup" ]]
[[ "$(stat -c '%a' "$backup")" == 600 ]]
tar -tzf "$backup" | grep -Fx 'notification-channel-secrets/channel-1.json' >/dev/null

sqlite3 "$db" "UPDATE restore_probe SET marker='changed-after-backup';"
write_secret "$secret" changed-after-backup
touch "$tmp/state/active"

cat >"$tmp/systemctl" <<'SYSTEMCTL'
#!/usr/bin/env bash
set -euo pipefail
state=${HSERVER_TEST_SYSTEMCTL_STATE:?}
case "$1" in
  is-active) [[ -f "$state/active" ]] ;;
  stop) rm -f "$state/active" "$state/unhealthy" ;;
  start)
    touch "$state/active"
    if [[ -f "$state/unhealthy-next-start" ]]; then
      rm -f "$state/unhealthy-next-start"
      touch "$state/unhealthy"
    else
      rm -f "$state/unhealthy"
    fi
    ;;
  *) exit 1 ;;
esac
SYSTEMCTL
chmod +x "$tmp/systemctl"

cat >"$tmp/curl" <<'CURL'
#!/usr/bin/env bash
set -euo pipefail
state=${HSERVER_TEST_SYSTEMCTL_STATE:?}
[[ -f "$state/active" && ! -f "$state/unhealthy" ]]
CURL
chmod +x "$tmp/curl"

run_restore() {
  "${restore_prefix[@]}" env \
    HSERVER_DATA_DIR="$tmp/data" \
    HSERVER_DB_PATH="$db" \
    HSERVER_DB_RECOVERY_DIR="$tmp/data/restores" \
    HSERVER_BACKUP_SCRIPT="$fixture_backup_script" \
    HSERVER_SYSTEMCTL="$tmp/systemctl" \
    HSERVER_CURL="$tmp/curl" \
    HSERVER_TEST_SYSTEMCTL_STATE="$tmp/state" \
    HSERVER_RESTORE_ACTIVE_TIMEOUT=1 \
    HSERVER_TEST_REAL_BACKUP_SCRIPT="$root_dir/scripts/backup-db.sh" \
    HSERVER_TEST_OWNER_UID="$restore_owner_uid" \
    HSERVER_TEST_OWNER_GID="$restore_owner_gid" \
    "$root_dir/scripts/restore-db.sh" "$@"
}

run_restore validate "$backup" >/dev/null
if run_restore restore "$backup" >"$tmp/missing-confirm.log" 2>&1; then
  echo "restore unexpectedly succeeded without --confirm" >&2
  exit 1
fi
grep -q 'explicit --confirm' "$tmp/missing-confirm.log"

run_restore restore "$backup" --confirm >/dev/null
[[ "$(sqlite3 "$db" 'SELECT marker FROM restore_probe;')" == before-restore ]]
[[ "$(sqlite3 "$db" 'PRAGMA quick_check;')" == ok ]]
grep -q 'changed-after-backup.example.test' "$secret" && {
  echo "portable restore left the post-backup notification secret in place" >&2
  exit 1
}
grep -q 'before-restore.example.test' "$secret"
[[ "$(stat -c '%a' "$tmp/data/notification-channel-secrets")" == 700 ]]
[[ "$(stat -c '%a' "$secret")" == 600 ]]
[[ -f "$tmp/state/active" ]]

recovery=$(find "$tmp/data/restores" -maxdepth 1 -type f -name 'pre-restore-*.panel-backup.tar.gz' | sort | head -n 1)
[[ -n "$recovery" && -s "$recovery" ]]
run_restore validate "$recovery" >/dev/null
mkdir "$tmp/recovered"
tar -xzf "$recovery" -C "$tmp/recovered"
[[ "$(sqlite3 "$tmp/recovered/hserver.db" 'SELECT marker FROM restore_probe;')" == changed-after-backup ]]
grep -q 'changed-after-backup.example.test' "$tmp/recovered/notification-channel-secrets/channel-1.json"

# Legacy .db.gz remains accepted and changes only SQLite state.
create_db "$tmp/legacy.db" legacy-target
gzip -n "$tmp/legacy.db"
run_restore validate "$tmp/legacy.db.gz" >/dev/null
run_restore restore "$tmp/legacy.db.gz" --confirm >/dev/null
[[ "$(sqlite3 "$db" 'SELECT marker FROM restore_probe;')" == legacy-target ]]
grep -q 'before-restore.example.test' "$secret"

# A failed activation rolls back both the database and protected secret files.
mkdir -p "$tmp/target-data/notification-channel-secrets"
chmod 0700 "$tmp/target-data/notification-channel-secrets"
create_db "$tmp/target-data/hserver.db" rejected-target
write_secret "$tmp/target-data/notification-channel-secrets/channel-1.json" rejected-target
target_output=$(HSERVER_DATA_DIR="$tmp/target-data" \
  HSERVER_DB_PATH="$tmp/target-data/hserver.db" \
  HSERVER_DB_BACKUP_DIR="$tmp/target-backups" \
  "$root_dir/scripts/backup-db.sh")
target_bundle=${target_output#Backup complete: }
touch "$tmp/state/unhealthy-next-start"
if run_restore restore "$target_bundle" --confirm >"$tmp/failed-activation.log" 2>&1; then
  echo "restore unexpectedly succeeded after failed service activation" >&2
  exit 1
fi
grep -q 'was rolled back' "$tmp/failed-activation.log"
[[ "$(sqlite3 "$db" 'SELECT marker FROM restore_probe;')" == legacy-target ]]
grep -q 'before-restore.example.test' "$secret"
[[ "$(sqlite3 "$db" 'PRAGMA quick_check;')" == ok ]]
[[ -f "$tmp/state/active" ]]

# An archive entry with a fixed payload name but symlink type is rejected.
mkdir -p "$tmp/unsafe/notification-channel-secrets"
chmod 0700 "$tmp/unsafe/notification-channel-secrets"
printf '%s\n' 'HSERVER_PANEL_BACKUP_FORMAT=1' 'DATABASE=hserver.db' \
  'NOTIFICATION_SECRETS=notification-channel-secrets' >"$tmp/unsafe/manifest.txt"
printf '%s\n' 'not-reached' >"$tmp/unsafe/SHA256SUMS"
cp "$db" "$tmp/unsafe/hserver.db"
ln -s /etc/passwd "$tmp/unsafe/notification-channel-secrets/channel-1.json"
chmod 0600 "$tmp/unsafe/manifest.txt" "$tmp/unsafe/SHA256SUMS" "$tmp/unsafe/hserver.db"
tar --owner=0 --group=0 --numeric-owner -czf "$tmp/unsafe.panel-backup.tar.gz" \
  -C "$tmp/unsafe" manifest.txt SHA256SUMS hserver.db notification-channel-secrets
if run_restore validate "$tmp/unsafe.panel-backup.tar.gz" >"$tmp/unsafe.log" 2>&1; then
  echo "unsafe panel-state bundle unexpectedly passed validation" >&2
  exit 1
fi
grep -Eq 'unsupported archive record|not protected' "$tmp/unsafe.log"

# A structurally valid bundle with changed protected content fails checksums.
mkdir "$tmp/tampered"
tar -xzf "$backup" -C "$tmp/tampered"
write_secret "$tmp/tampered/notification-channel-secrets/channel-1.json" tampered
tar --owner=0 --group=0 --numeric-owner -czf "$tmp/tampered.panel-backup.tar.gz" \
  -C "$tmp/tampered" manifest.txt SHA256SUMS hserver.db notification-channel-secrets
if run_restore validate "$tmp/tampered.panel-backup.tar.gz" >"$tmp/tampered.log" 2>&1; then
  echo "tampered panel-state bundle unexpectedly passed validation" >&2
  exit 1
fi
grep -q 'checksum validation failed' "$tmp/tampered.log"

# Backup refuses a database reference whose protected file is missing.
mkdir -p "$tmp/broken-data/notification-channel-secrets"
chmod 0700 "$tmp/broken-data/notification-channel-secrets"
cp "$db" "$tmp/broken-data/hserver.db"
if HSERVER_DATA_DIR="$tmp/broken-data" \
  HSERVER_DB_PATH="$tmp/broken-data/hserver.db" \
  HSERVER_DB_BACKUP_DIR="$tmp/broken-backups" \
  "$root_dir/scripts/backup-db.sh" >"$tmp/broken.log" 2>&1; then
  echo "incomplete panel-state backup unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'missing or unsafe' "$tmp/broken.log"

printf '%s\n' 'hserver portable panel-state restore drill: OK'
