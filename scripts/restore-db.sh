#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Usage:
  sudo ./restore-db.sh validate BACKUP_FILE
  sudo ./restore-db.sh restore BACKUP_FILE --confirm

Validates and restores an Heyserver panel-state bundle or a legacy SQLite backup.
A panel-state bundle restores the database and protected notification channel
config files together. Restore creates a protected pre-restore recovery bundle,
returns the service to its previous active or inactive state, and rolls back the
previous state automatically if service activation fails.
USAGE
  exit 2
}

[[ $# -ge 2 ]] || usage
command_name=$1
backup_file=$2
confirmation=${3:-}

data_dir=${HSERVER_DATA_DIR:-/var/lib/hserver}
db_path=${HSERVER_DB_PATH:-$data_dir/hserver.db}
secret_dir=${HSERVER_NOTIFICATION_SECRET_DIR:-$data_dir/notification-channel-secrets}
recovery_dir=${HSERVER_DB_RECOVERY_DIR:-$data_dir/restores}
systemctl_bin=${HSERVER_SYSTEMCTL:-systemctl}
service_name=${HSERVER_SERVICE_NAME:-hserver}
active_timeout=${HSERVER_RESTORE_ACTIVE_TIMEOUT:-20}
health_url=${HSERVER_RESTORE_HEALTH_URL:-http://127.0.0.1:${HSERVER_PORT:-3085}/api/health}
curl_bin=${HSERVER_CURL:-curl}
backup_script=${HSERVER_BACKUP_SCRIPT:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/backup-db.sh}
max_secret_files=${HSERVER_PANEL_BACKUP_MAX_SECRET_FILES:-10000}

[[ "$active_timeout" =~ ^[1-9][0-9]*$ ]] || {
  printf 'HSERVER_RESTORE_ACTIVE_TIMEOUT must be a positive integer.\n' >&2
  exit 1
}
[[ "$max_secret_files" =~ ^[1-9][0-9]*$ ]] || {
  printf 'HSERVER_PANEL_BACKUP_MAX_SECRET_FILES must be a positive integer.\n' >&2
  exit 1
}
[[ "$data_dir" = /* && "$db_path" = /* && "$secret_dir" = /* && "$recovery_dir" = /* ]] || {
  printf 'Heyserver data, database, notification secret, and recovery paths must be absolute.\n' >&2
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

validate_source() {
  local source=$1
  [[ -f "$source" && ! -L "$source" ]] || {
    printf 'Backup must be a regular, non-symlink file: %s\n' "$source" >&2
    return 1
  }
  [[ -s "$source" ]] || {
    printf 'Backup is empty: %s\n' "$source" >&2
    return 1
  }
  case "$source" in
    *.panel-backup.tar.gz|*.db|*.db.gz) ;;
    *)
      printf 'Backup must end in .panel-backup.tar.gz, .db, or .db.gz: %s\n' "$source" >&2
      return 1
      ;;
  esac
}

validate_database() {
  local candidate=$1 integrity required_tables
  integrity=$(sqlite3 -batch "$candidate" 'PRAGMA quick_check;')
  [[ "$integrity" == "ok" ]] || {
    printf 'SQLite integrity validation failed: %s\n' "$integrity" >&2
    return 1
  }
  required_tables=$(sqlite3 -batch "$candidate" \
    "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('schema_migrations','users');")
  [[ "$required_tables" == "2" ]] || {
    printf 'Backup is valid SQLite but not an Heyserver database.\n' >&2
    return 1
  }
}

materialize_legacy_backup() {
  local source=$1 destination=$2
  if [[ "$source" == *.db.gz ]]; then
    gzip -t -- "$source"
    gzip -dc -- "$source" >"$destination"
  else
    cp -- "$source" "$destination"
  fi
  chmod 0600 "$destination"
}

validate_archive_entries() {
  local source=$1 list_file=$2 verbose_file=$3 entry entry_count
  LC_ALL=C tar -tzf "$source" >"$list_file"
  [[ -s "$list_file" ]] || {
    printf 'Panel-state bundle has no entries.\n' >&2
    return 1
  }
  entry_count=$(wc -l <"$list_file")
  (( entry_count <= max_secret_files + 4 )) || {
    printf 'Panel-state bundle exceeds the entry limit.\n' >&2
    return 1
  }
  [[ -z "$(LC_ALL=C sort "$list_file" | uniq -d)" ]] || {
    printf 'Panel-state bundle contains duplicate entries.\n' >&2
    return 1
  }
  while IFS= read -r entry; do
    case "$entry" in
      manifest.txt|SHA256SUMS|hserver.db|notification-channel-secrets/) ;;
      notification-channel-secrets/channel-*.json)
        [[ "$entry" =~ ^notification-channel-secrets/channel-[1-9][0-9]*\.json$ ]] || {
          printf 'Unsafe panel-state bundle entry: %s\n' "$entry" >&2
          return 1
        }
        ;;
      *)
        printf 'Unexpected panel-state bundle entry: %s\n' "$entry" >&2
        return 1
        ;;
    esac
  done <"$list_file"
  for required in manifest.txt SHA256SUMS hserver.db notification-channel-secrets/; do
    [[ "$(grep -Fxc "$required" "$list_file")" == "1" ]] || {
      printf 'Panel-state bundle is missing required entry: %s\n' "$required" >&2
      return 1
    }
  done

  LC_ALL=C tar -tvzf "$source" >"$verbose_file"
  while read -r mode _owner _size _date _time name extra; do
    [[ -n "$name" && -z "${extra:-}" ]] || {
      printf 'Panel-state bundle contains an unsupported archive record.\n' >&2
      return 1
    }
    if [[ "$name" == "notification-channel-secrets/" ]]; then
      [[ "$mode" == drwx------ ]] || {
        printf 'Panel-state secret directory is not protected in the archive.\n' >&2
        return 1
      }
    else
      [[ "$mode" == -rw------- ]] || {
        printf 'Panel-state bundle file is not protected: %s\n' "$name" >&2
        return 1
      }
    fi
  done <"$verbose_file"
}

validate_bundle_relationship() {
  local root=$1 database=$2 expected=$3 actual=$4 has_channels id config_ref expected_ref file size
  : >"$expected"
  has_channels=$(sqlite3 -batch "$database" \
    "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='notification_channels';")
  if [[ "$has_channels" == "1" ]]; then
    while IFS='|' read -r id config_ref; do
      [[ -n "$id" ]] || continue
      expected_ref="file:channel-$id.json"
      [[ "$id" =~ ^[1-9][0-9]*$ && "$config_ref" == "$expected_ref" ]] || {
        printf 'Notification channel %s has an invalid protected config reference.\n' "$id" >&2
        return 1
      }
      printf 'notification-channel-secrets/channel-%s.json\n' "$id" >>"$expected"
    done < <(sqlite3 -batch -separator '|' "$database" \
      "SELECT id, config FROM notification_channels WHERE config LIKE 'file:%' ORDER BY id;")
  fi
  find "$root/notification-channel-secrets" -maxdepth 1 -type f -name 'channel-*.json' \
    -printf 'notification-channel-secrets/%f\n' | LC_ALL=C sort >"$actual"
  LC_ALL=C sort -o "$expected" "$expected"
  cmp -s "$expected" "$actual" || {
    printf 'Panel-state notification files do not match database references.\n' >&2
    return 1
  }
  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    file="$root/$file"
    [[ -f "$file" && ! -L "$file" ]] || return 1
    size=$(stat -c '%s' "$file")
    [[ "$size" -gt 0 && "$size" -le 65536 ]] || {
      printf 'Protected notification config has an invalid size: %s\n' "$file" >&2
      return 1
    }
    validate_json_object "$file" || {
      printf 'Protected notification config is not a JSON object: %s\n' "$file" >&2
      return 1
    }
  done <"$actual"
}

extract_and_validate_bundle() {
  local source=$1 destination=$2 list_file verbose_file expected_manifest checksums listed actual line hash path
  list_file="$destination/.archive-entries"
  verbose_file="$destination/.archive-verbose"
  expected_manifest="$destination/.expected-manifest"
  listed="$destination/.checksum-listed"
  actual="$destination/.checksum-actual"

  gzip -t -- "$source"
  validate_archive_entries "$source" "$list_file" "$verbose_file"
  tar -xzf "$source" -C "$destination" --no-same-owner --no-same-permissions

  [[ -f "$destination/manifest.txt" && ! -L "$destination/manifest.txt" \
     && -f "$destination/SHA256SUMS" && ! -L "$destination/SHA256SUMS" \
     && -f "$destination/hserver.db" && ! -L "$destination/hserver.db" \
     && -d "$destination/notification-channel-secrets" && ! -L "$destination/notification-channel-secrets" ]] || {
    printf 'Panel-state bundle extraction did not produce protected regular payloads.\n' >&2
    return 1
  }
  cat >"$expected_manifest" <<'MANIFEST'
HSERVER_PANEL_BACKUP_FORMAT=1
DATABASE=hserver.db
NOTIFICATION_SECRETS=notification-channel-secrets
MANIFEST
  cmp -s "$expected_manifest" "$destination/manifest.txt" || {
    printf 'Unsupported or malformed panel-state bundle manifest.\n' >&2
    return 1
  }

  : >"$listed"
  while IFS= read -r line; do
    [[ "$line" =~ ^([0-9a-f]{64})\ \ (hserver\.db|notification-channel-secrets/channel-[1-9][0-9]*\.json)$ ]] || {
      printf 'Panel-state bundle contains an invalid checksum record.\n' >&2
      return 1
    }
    hash=${BASH_REMATCH[1]}
    path=${BASH_REMATCH[2]}
    printf '%s\n' "$path" >>"$listed"
    [[ -n "$hash" ]]
  done <"$destination/SHA256SUMS"
  [[ -z "$(LC_ALL=C sort "$listed" | uniq -d)" ]] || {
    printf 'Panel-state bundle contains duplicate checksum records.\n' >&2
    return 1
  }
  {
    printf 'hserver.db\n'
    find "$destination/notification-channel-secrets" -maxdepth 1 -type f -name 'channel-*.json' \
      -printf 'notification-channel-secrets/%f\n' | LC_ALL=C sort
  } >"$actual"
  LC_ALL=C sort -o "$listed" "$listed"
  LC_ALL=C sort -o "$actual" "$actual"
  cmp -s "$listed" "$actual" || {
    printf 'Panel-state bundle checksum inventory does not match its payload.\n' >&2
    return 1
  }
  (cd "$destination" && sha256sum -c SHA256SUMS >/dev/null) || {
    printf 'Panel-state bundle checksum validation failed.\n' >&2
    return 1
  }

  chmod 0600 "$destination/manifest.txt" "$destination/SHA256SUMS" "$destination/hserver.db"
  chmod 0700 "$destination/notification-channel-secrets"
  find "$destination/notification-channel-secrets" -maxdepth 1 -type f -name 'channel-*.json' -exec chmod 0600 {} +
  validate_database "$destination/hserver.db"
  validate_bundle_relationship "$destination" "$destination/hserver.db" \
    "$destination/.expected-secrets" "$destination/.actual-secrets"
}

wait_ready() {
  local waited=0
  while (( waited < active_timeout )); do
    if "$systemctl_bin" is-active --quiet "$service_name" \
      && "$curl_bin" -fsS --max-time 2 "$health_url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done
  return 1
}

restore_candidate() {
  local candidate=$1 mode=$2 uid=$3 gid=$4
  rm -f -- "$db_path-wal" "$db_path-shm"
  mv -f -- "$candidate" "$db_path"
  chmod "$mode" "$db_path"
  chown "$uid:$gid" "$db_path"
}

rollback_secret_state() {
  (( secrets_swapped )) || return 0
  remove_tree "$secret_dir"
  if (( original_secret_present )); then
    mv -- "$old_secret_dir" "$secret_dir"
    old_secret_dir=
  fi
  secrets_swapped=0
}

require_command sqlite3
require_command gzip
require_command tar
require_command sha256sum
require_command cmp
validate_source "$backup_file"

validation_dir=$(mktemp -d)
validation_file=
source_kind=legacy
restore_in_progress=0
was_active=0
recovery_file=
restore_file=
rollback_file=
restore_secret_dir=
old_secret_dir=
secrets_swapped=0
original_secret_present=0

on_exit() {
  local status=$?
  trap - EXIT INT TERM
  set +e
  if (( status != 0 && restore_in_progress )) && [[ -f "${rollback_file:-}" ]]; then
    if restore_candidate "$rollback_file" "$original_mode" "$original_uid" "$original_gid" \
      && rollback_secret_state; then
      rollback_file=
      if (( was_active )); then
        "$systemctl_bin" start "$service_name" >/dev/null 2>&1 || true
      fi
      printf 'Interrupted restore was rolled back from: %s\n' "$recovery_file" >&2
    else
      printf 'Emergency rollback failed; recovery bundle remains at: %s\n' "$recovery_file" >&2
    fi
  fi
  remove_tree "${validation_dir:-}" || true
  remove_tree "${restore_secret_dir:-}" || true
  remove_tree "${old_secret_dir:-}" || true
  rm -f -- "${restore_file:-}" "${rollback_file:-}"
  exit "$status"
}
trap on_exit EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [[ "$backup_file" == *.panel-backup.tar.gz ]]; then
  source_kind=bundle
  extract_and_validate_bundle "$backup_file" "$validation_dir"
  validation_file="$validation_dir/hserver.db"
else
  validation_file="$validation_dir/hserver.db"
  materialize_legacy_backup "$backup_file" "$validation_file"
  validate_database "$validation_file"
fi

case "$command_name" in
  validate)
    [[ $# -eq 2 ]] || usage
    if [[ "$source_kind" == bundle ]]; then
      printf 'Heyserver panel-state bundle validation passed: %s\n' "$backup_file"
    else
      printf 'Heyserver legacy database backup validation passed: %s\n' "$backup_file"
    fi
    ;;
  restore)
    [[ $# -eq 3 && "$confirmation" == "--confirm" ]] || {
      printf 'Restore requires an explicit --confirm flag after validation.\n' >&2
      exit 2
    }
    [[ $EUID -eq 0 ]] || {
      printf 'Restore must run as root.\n' >&2
      exit 1
    }
    [[ -f "$db_path" && ! -L "$db_path" ]] || {
      printf 'Installed Heyserver database not found: %s\n' "$db_path" >&2
      exit 1
    }
    [[ -x "$backup_script" && ! -L "$backup_script" ]] || {
      printf 'Protected panel-state backup helper not found: %s\n' "$backup_script" >&2
      exit 1
    }
    require_command "$systemctl_bin"
    require_command "$curl_bin"
    install -d -m 0700 "$data_dir" "$recovery_dir"

    original_mode=$(stat -c '%a' "$db_path")
    original_uid=$(stat -c '%u' "$db_path")
    original_gid=$(stat -c '%g' "$db_path")
    if "$systemctl_bin" is-active --quiet "$service_name"; then
      was_active=1
    fi

    recovery_output=$(HSERVER_DATA_DIR="$data_dir" \
      HSERVER_DB_PATH="$db_path" \
      HSERVER_NOTIFICATION_SECRET_DIR="$secret_dir" \
      HSERVER_DB_BACKUP_DIR="$recovery_dir" \
      HSERVER_DB_BACKUP_PREFIX=pre-restore \
      HSERVER_PANEL_BACKUP_MAX_SECRET_FILES="$max_secret_files" \
      "$backup_script")
    recovery_file=${recovery_output#Backup complete: }
    [[ "$recovery_file" != "$recovery_output" && -f "$recovery_file" && ! -L "$recovery_file" ]] || {
      printf 'Pre-restore recovery bundle was not created.\n' >&2
      exit 1
    }
    rollback_file=$(mktemp "$data_dir/.hserver-rollback-XXXXXX")
    tar -xOzf "$recovery_file" hserver.db >"$rollback_file"
    chmod 0600 "$rollback_file"
    validate_database "$rollback_file"

    restore_file=$(mktemp "$data_dir/.hserver-restore-XXXXXX")
    cp -- "$validation_file" "$restore_file"
    chmod 0600 "$restore_file"

    if [[ "$source_kind" == bundle ]]; then
      secret_parent=$(dirname "$secret_dir")
      [[ -d "$secret_parent" && ! -L "$secret_parent" ]] || {
        printf 'Notification secret parent must be a regular directory: %s\n' "$secret_parent" >&2
        exit 1
      }
      if [[ -e "$secret_dir" || -L "$secret_dir" ]]; then
        [[ -d "$secret_dir" && ! -L "$secret_dir" ]] || {
          printf 'Installed notification secret path is unsafe: %s\n' "$secret_dir" >&2
          exit 1
        }
        original_secret_present=1
        secret_uid=$(stat -c '%u' "$secret_dir")
        secret_gid=$(stat -c '%g' "$secret_dir")
      else
        secret_uid=$original_uid
        secret_gid=$original_gid
      fi
      restore_secret_dir=$(mktemp -d "$secret_parent/.hserver-notification-restore-XXXXXX")
      chmod 0700 "$restore_secret_dir"
      find "$validation_dir/notification-channel-secrets" -maxdepth 1 -type f -name 'channel-*.json' \
        -exec install -m 0600 {} "$restore_secret_dir/" \;
      chown "$secret_uid:$secret_gid" "$restore_secret_dir"
      find "$restore_secret_dir" -maxdepth 1 -type f -name 'channel-*.json' \
        -exec chown "$secret_uid:$secret_gid" {} +
    fi

    if (( was_active )); then
      "$systemctl_bin" stop "$service_name"
    fi
    restore_in_progress=1

    if [[ "$source_kind" == bundle ]]; then
      if (( original_secret_present )); then
        old_secret_dir=$(mktemp -d "$(dirname "$secret_dir")/.hserver-notification-old-XXXXXX")
        rmdir "$old_secret_dir"
        mv -- "$secret_dir" "$old_secret_dir"
      fi
      mv -- "$restore_secret_dir" "$secret_dir"
      restore_secret_dir=
      secrets_swapped=1
    fi

    restore_candidate "$restore_file" "$original_mode" "$original_uid" "$original_gid"
    restore_file=

    if (( was_active )); then
      if ! "$systemctl_bin" start "$service_name" || ! wait_ready; then
        "$systemctl_bin" stop "$service_name" >/dev/null 2>&1 || true
        if ! restore_candidate "$rollback_file" "$original_mode" "$original_uid" "$original_gid" \
          || ! rollback_secret_state; then
          printf 'Restore failed and automatic rollback failed; recovery bundle remains at: %s\n' "$recovery_file" >&2
          exit 1
        fi
        rollback_file=
        restore_in_progress=0
        "$systemctl_bin" start "$service_name"
        wait_ready || {
          printf 'Restore failed; the recovery state was restored but Heyserver is still unhealthy.\n' >&2
          exit 1
        }
        printf 'Restore failed health activation and was rolled back from: %s\n' "$recovery_file" >&2
        exit 1
      fi
    fi

    restore_in_progress=0
    rm -f -- "$rollback_file"
    rollback_file=
    remove_tree "${old_secret_dir:-}" || true
    old_secret_dir=
    printf 'Heyserver panel-state restore completed. Recovery bundle: %s\n' "$recovery_file"
    ;;
  *) usage ;;
esac
