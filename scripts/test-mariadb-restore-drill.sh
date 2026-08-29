#!/usr/bin/env bash
set -euo pipefail

root_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root_dir"
for command_name in go mariadbd mariadb-install-db mariadb mariadb-admin mariadb-dump sudo; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "Required command not found: $command_name" >&2
    exit 1
  }
done
id mysql >/dev/null 2>&1 || {
  echo "Required OS account not found: mysql" >&2
  exit 1
}
sudo -n -u mysql true

tmp=$(mktemp -d /tmp/hserver-mariadb-restore-XXXXXXXX)
chmod 0755 "$tmp"
data_dir="$tmp/data"
socket_dir="$tmp/socket"
socket_path="$socket_dir/mariadb.sock"
pid_file="$data_dir/mariadb.pid"
log_file="$tmp/mariadb.log"
defaults_file="$tmp/client.cnf"
launcher_pid=
started=0
cleanup() {
  if (( started )); then
    mariadb-admin --defaults-extra-file="$defaults_file" shutdown >/dev/null 2>&1 || true
  fi
  if [[ -n "$launcher_pid" ]]; then
    wait "$launcher_pid" >/dev/null 2>&1 || true
  fi
  sudo rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

sudo install -d -m 0700 -o mysql -g mysql "$data_dir"
sudo install -d -m 0755 -o mysql -g mysql "$socket_dir"
touch "$log_file"
sudo chown mysql:mysql "$log_file"
cat >"$defaults_file" <<EOF
[client]
protocol=socket
socket=$socket_path
user=root
EOF
chmod 0600 "$defaults_file"

sudo -n -u mysql -- mariadb-install-db \
  --no-defaults \
  --datadir="$data_dir" \
  --auth-root-authentication-method=normal \
  --skip-test-db >/dev/null

sudo -n -u mysql -- mariadbd \
  --no-defaults \
  --datadir="$data_dir" \
  --socket="$socket_path" \
  --pid-file="$pid_file" \
  --log-error="$log_file" \
  --skip-networking &
launcher_pid=$!

for _ in $(seq 1 100); do
  if mariadb-admin --defaults-extra-file="$defaults_file" ping >/dev/null 2>&1; then
    started=1
    break
  fi
  if ! kill -0 "$launcher_pid" >/dev/null 2>&1; then
    cat "$log_file" >&2
    exit 1
  fi
  sleep 0.1
done
if (( ! started )); then
  cat "$log_file" >&2
  echo "Disposable MariaDB did not become ready" >&2
  exit 1
fi

HSERVER_MYSQL_DEFAULTS_FILE="$defaults_file" \
HSERVER_RUN_MARIADB_RESTORE_DRILL=1 \
HSERVER_TEST_MYSQL_DATABASE=hserver_restore_drill \
  go test ./internal/services/backup -run '^TestMariaDBBackupRestoreDrill$' -count=1

printf '%s\n' 'disposable MariaDB backup and restore drill: OK'
