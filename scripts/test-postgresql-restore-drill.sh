#!/usr/bin/env bash
set -euo pipefail

root_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root_dir"
for command_name in go pg_config sudo python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "Required command not found: $command_name" >&2
    exit 1
  }
done

pg_bindir=$(pg_config --bindir)
for binary in initdb pg_ctl createdb psql pg_dump; do
  [[ -x "$pg_bindir/$binary" ]] || {
    echo "PostgreSQL binary not found: $pg_bindir/$binary" >&2
    exit 1
  }
done
sudo -n -u postgres true

tmp=$(mktemp -d /tmp/hserver-postgresql-restore-XXXXXXXX)
chmod 0755 "$tmp"
data_dir="$tmp/data"
socket_dir="$tmp/socket"
log_file="$tmp/postgresql.log"
started=0
cleanup() {
  if (( started )); then
    sudo -n -u postgres -- "$pg_bindir/pg_ctl" -D "$data_dir" -m immediate stop >/dev/null 2>&1 || true
  fi
  sudo rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

sudo install -d -m 0700 -o postgres -g postgres "$data_dir" "$socket_dir"
touch "$log_file"
sudo chown postgres:postgres "$log_file"
port=$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
PY
)

sudo -n -u postgres -- "$pg_bindir/initdb" -D "$data_dir" -A trust --no-locale >/dev/null
sudo -n -u postgres -- "$pg_bindir/pg_ctl" \
  -D "$data_dir" \
  -l "$log_file" \
  -o "-F -k $socket_dir -p $port -h ''" \
  -w start >/dev/null
started=1

database=hserver_restore_drill
sudo -n -u postgres -- env \
  "PATH=$pg_bindir:$PATH" \
  "PGHOST=$socket_dir" \
  "PGPORT=$port" \
  "PGUSER=postgres" \
  "$pg_bindir/createdb" "$database"

PATH="$pg_bindir:$PATH" \
HSERVER_PG_RUN_AS=postgres \
HSERVER_PG_BACKUP_USER=postgres \
HSERVER_PG_BACKUP_HOST="$socket_dir" \
HSERVER_PG_BACKUP_PORT="$port" \
HSERVER_PG_PASSFILE='' \
HSERVER_RUN_POSTGRES_RESTORE_DRILL=1 \
HSERVER_TEST_PG_DATABASE="$database" \
  go test ./internal/services/backup -run '^TestPostgreSQLBackupRestoreDrill$' -count=1

printf '%s\n' 'disposable PostgreSQL backup and restore drill: OK'
