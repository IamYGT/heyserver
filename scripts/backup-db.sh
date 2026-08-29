#!/usr/bin/env bash
set -euo pipefail

DATA_DIR="${HSERVER_DATA_DIR:-/var/lib/hserver}"
DB_PATH="${HSERVER_DB_PATH:-$DATA_DIR/hserver.db}"
BACKUP_DIR="${HSERVER_DB_BACKUP_DIR:-$DATA_DIR/backups/db}"
SECRET_DIR="${HSERVER_NOTIFICATION_SECRET_DIR:-$DATA_DIR/notification-channel-secrets}"
RETENTION_DAYS="${HSERVER_DB_BACKUP_RETENTION_DAYS:-30}"
BACKUP_PREFIX="${HSERVER_DB_BACKUP_PREFIX:-hserver}"
MAX_SECRET_FILES="${HSERVER_PANEL_BACKUP_MAX_SECRET_FILES:-10000}"

[[ "$RETENTION_DAYS" =~ ^[0-9]+$ ]] || {
  printf 'HSERVER_DB_BACKUP_RETENTION_DAYS must be a non-negative integer.\n' >&2
  exit 1
}
[[ "$MAX_SECRET_FILES" =~ ^[1-9][0-9]*$ ]] || {
  printf 'HSERVER_PANEL_BACKUP_MAX_SECRET_FILES must be a positive integer.\n' >&2
  exit 1
}
[[ "$BACKUP_PREFIX" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || {
  printf 'HSERVER_DB_BACKUP_PREFIX contains unsupported characters.\n' >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$1" >&2
    exit 1
  }
}

remove_tree() {
  local path=$1
  [[ -n "$path" && "$path" != "/" ]] || return 1
  [[ -e "$path" || -L "$path" ]] || return 0
  find "$path" -xdev -depth -delete
}

validate_json_object() {
  local path=$1 escaped result
  escaped=${path//\'/\'\'}
  result=$(sqlite3 -batch ':memory:' \
    "SELECT CASE WHEN json_valid(CAST(readfile('$escaped') AS TEXT)) AND json_type(CAST(readfile('$escaped') AS TEXT)) = 'object' THEN 1 ELSE 0 END;")
  [[ "$result" == "1" ]]
}

require_command sqlite3
require_command tar
require_command gzip
require_command sha256sum

[[ -f "$DB_PATH" && ! -L "$DB_PATH" ]] || {
  printf 'Installed HServer database must be a regular, non-symlink file: %s\n' "$DB_PATH" >&2
  exit 1
}

install -d -m 0700 "$BACKUP_DIR"
stage=$(mktemp -d "$BACKUP_DIR/.panel-backup-stage-XXXXXX")
archive_tmp=$(mktemp "$BACKUP_DIR/.panel-backup-archive-XXXXXX")
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  remove_tree "${stage:-}" || true
  rm -f -- "${archive_tmp:-}"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

install -d -m 0700 "$stage/notification-channel-secrets"
escaped_backup=${stage//\'/\'\'}
sqlite3 "$DB_PATH" ".backup '$escaped_backup/hserver.db'"
chmod 0600 "$stage/hserver.db"

has_channels=$(sqlite3 -batch "$stage/hserver.db" \
  "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='notification_channels';")
secret_count=0
if [[ "$has_channels" == "1" ]]; then
  protected_ref_count=$(sqlite3 -batch "$stage/hserver.db" \
    "SELECT COUNT(*) FROM notification_channels WHERE config LIKE 'file:%';")
  if (( protected_ref_count > 0 )); then
    [[ -d "$SECRET_DIR" && ! -L "$SECRET_DIR" && "$(stat -c '%a' "$SECRET_DIR")" == "700" ]] || {
      printf 'Notification secret store must be a protected regular directory: %s\n' "$SECRET_DIR" >&2
      exit 1
    }
  fi
  while IFS='|' read -r id config_ref; do
    [[ -n "$id" ]] || continue
    expected_ref="file:channel-$id.json"
    [[ "$id" =~ ^[1-9][0-9]*$ && "$config_ref" == "$expected_ref" ]] || {
      printf 'Notification channel %s has an invalid protected config reference.\n' "$id" >&2
      exit 1
    }
    secret_count=$((secret_count + 1))
    (( secret_count <= MAX_SECRET_FILES )) || {
      printf 'Panel backup exceeds the protected notification file limit (%s).\n' "$MAX_SECRET_FILES" >&2
      exit 1
    }
    source_file="$SECRET_DIR/channel-$id.json"
    [[ -f "$source_file" && ! -L "$source_file" ]] || {
      printf 'Protected notification config is missing or unsafe: %s\n' "$source_file" >&2
      exit 1
    }
    mode=$(stat -c '%a' "$source_file")
    size=$(stat -c '%s' "$source_file")
    [[ "$mode" == "600" && "$size" -gt 0 && "$size" -le 65536 ]] || {
      printf 'Protected notification config has invalid mode or size: %s\n' "$source_file" >&2
      exit 1
    }
    validate_json_object "$source_file" || {
      printf 'Protected notification config is not a JSON object: %s\n' "$source_file" >&2
      exit 1
    }
    install -m 0600 "$source_file" "$stage/notification-channel-secrets/channel-$id.json"
  done < <(sqlite3 -batch -separator '|' "$stage/hserver.db" \
    "SELECT id, config FROM notification_channels WHERE config LIKE 'file:%' ORDER BY id;")
fi

cat >"$stage/manifest.txt" <<'EOF'
HSERVER_PANEL_BACKUP_FORMAT=1
DATABASE=hserver.db
NOTIFICATION_SECRETS=notification-channel-secrets
EOF
chmod 0600 "$stage/manifest.txt"
(
  cd "$stage"
  {
    sha256sum hserver.db
    find notification-channel-secrets -maxdepth 1 -type f -name 'channel-*.json' -printf '%p\n' \
      | LC_ALL=C sort \
      | while IFS= read -r path; do sha256sum "$path"; done
  } >SHA256SUMS
  chmod 0600 SHA256SUMS
)

timestamp=$(date -u +%Y%m%d-%H%M%S)
archive="$BACKUP_DIR/$BACKUP_PREFIX-$timestamp-$RANDOM.panel-backup.tar.gz"
(
  cd "$stage"
  tar --sort=name --owner=0 --group=0 --numeric-owner \
    -cf - manifest.txt SHA256SUMS hserver.db notification-channel-secrets
) | gzip -n >"$archive_tmp"
chmod 0600 "$archive_tmp"
mv -- "$archive_tmp" "$archive"
archive_tmp=

find "$BACKUP_DIR" -maxdepth 1 -type f \
  \( -name "$BACKUP_PREFIX-*.panel-backup.tar.gz" -o -name "$BACKUP_PREFIX-*.db.gz" \) \
  -mtime "+$RETENTION_DAYS" -delete

printf 'Backup complete: %s\n' "$archive"
