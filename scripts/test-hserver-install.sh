#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
invoking_uid=$(id -u)
invoking_gid=$(id -g)
stage='fixture setup'
wal_sqlite_pid=
wal_sqlite_fd_open=0

if [ "$invoking_uid" -eq 0 ]; then
  run_privileged() {
    "$@"
  }
else
  command -v sudo >/dev/null 2>&1 || {
    printf '%s\n' 'hserver install test requires root or sudo; install sudo or rerun with elevated access' >&2
    exit 1
  }
  # hserver-install.sh must stay root-only; elevate only its disposable
  # fixture runs so the surrounding contributor test remains unprivileged.
  run_privileged() {
    status=0
    sudo -- "$@" || status=$?
    sudo -- env chown -R -- "$invoking_uid:$invoking_gid" "$tmp" >/dev/null 2>&1 \
      || return 1
    return "$status"
  }
fi

tmp=$(mktemp -d)
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ -n "${wal_sqlite_pid:-}" ]; then
    if [ "${wal_sqlite_fd_open:-0}" -eq 1 ]; then
      printf '%s\n' '.exit' >&3 2>/dev/null || true
      exec 3>&- || true
      wal_sqlite_fd_open=0
    fi
    wait "$wal_sqlite_pid" 2>/dev/null || true
    wal_sqlite_pid=
  fi
  cleanup_status=0
  if [ "$invoking_uid" -eq 0 ]; then
    rm -rf -- "$tmp" || cleanup_status=$?
  else
    sudo -- env rm -rf -- "$tmp" >/dev/null 2>&1 || cleanup_status=$?
  fi
  if [ "$status" -eq 0 ] && [ "$cleanup_status" -ne 0 ]; then
    status=$cleanup_status
  fi
  if [ "$status" -ne 0 ]; then
    printf '%s\n' "hserver-install lifecycle test failed at stage: $stage" >&2
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

mkdir -p "$tmp/systemctl-state" "$tmp/bin"

cat >"$tmp/systemctl" <<'EOF'
#!/usr/bin/env sh
set -eu
state=${HSERVER_SYSTEMCTL_STATE_DIR:?}
command=$1
shift
[ -z "${HSERVER_SYSTEMCTL_LOG:-}" ] || printf '%s\n' "$command" >>"$HSERVER_SYSTEMCTL_LOG"
case "$command" in
  is-active) [ -f "$state/active" ] ;;
  is-enabled) [ -f "$state/enabled" ] ;;
  show-environment) [ "${TEST_SYSTEMD_STATE:-up}" = up ] ;;
  show)
    printf 'FragmentPath=%s\n' "${HSERVER_TEST_SERVICE_FILE:-/etc/systemd/system/hserver.service}"
    printf 'DropInPaths=%s\n' "${HSERVER_TEST_DROPIN_FILE:-}"
    [ -f "$state/enabled" ] && printf 'UnitFileState=enabled\n' || printf 'UnitFileState=disabled\n'
    ;;
  cat)
    [ -z "${HSERVER_TEST_SERVICE_FILE:-}" ] || cat "$HSERVER_TEST_SERVICE_FILE"
    [ -z "${HSERVER_TEST_DROPIN_FILE:-}" ] || cat "$HSERVER_TEST_DROPIN_FILE"
    ;;
  daemon-reload) ;;
  enable)
    touch "$state/enabled"
    if [ "${1:-}" = --now ]; then
      touch "$state/active"
    fi
    ;;
  disable)
    rm -f "$state/enabled"
    if [ "${1:-}" = --now ]; then
      rm -f "$state/active"
    fi
    ;;
  start) touch "$state/active" ;;
  stop) rm -f "$state/active" ;;
  *) exit 1 ;;
esac
EOF
chmod +x "$tmp/systemctl"

cat >"$tmp/v1" <<'EOF'
#!/usr/bin/env sh
echo v1
EOF
cat >"$tmp/v2" <<'EOF'
#!/usr/bin/env sh
echo v2
EOF
cat >"$tmp/broken" <<'EOF'
#!/usr/bin/env sh
echo broken
EOF
cat >"$tmp/cli-v1" <<'EOF'
#!/usr/bin/env sh
echo cli-v1
EOF
cat >"$tmp/cli-v2" <<'EOF'
#!/usr/bin/env sh
echo cli-v2
EOF
chmod +x "$tmp/v1" "$tmp/v2" "$tmp/broken" "$tmp/cli-v1" "$tmp/cli-v2"
for version in 3 4 5 6 7; do
  printf '#!/usr/bin/env sh\necho v%s\n' "$version" >"$tmp/v$version"
  printf '#!/usr/bin/env sh\necho cli-v%s\n' "$version" >"$tmp/cli-v$version"
  chmod +x "$tmp/v$version" "$tmp/cli-v$version"
done

cat >"$tmp/bin/curl" <<'EOF'
#!/usr/bin/env sh
[ "$(${HSERVER_HEALTH_BINARY:?})" != broken ]
EOF
chmod +x "$tmp/bin/curl"

cat >"$tmp/bin/cp" <<'EOF'
#!/usr/bin/env sh
set -eu
failure=${HSERVER_TEST_SNAPSHOT_COPY_FAILURE:-}
marker=${HSERVER_TEST_SNAPSHOT_COPY_MARKER:-}
if [ -n "$failure" ] && [ -n "$marker" ] && [ ! -e "$marker" ]; then
  : >"$marker"
  case "$failure" in
    copy)
      printf '%s\n' 'simulated snapshot copy failure' >&2
      exit 1
      ;;
    disk)
      printf '%s\n' 'simulated snapshot disk-full failure' >&2
      exit 28
      ;;
    permission)
      printf '%s\n' 'simulated snapshot permission failure' >&2
      exit 13
      ;;
    *)
      printf '%s\n' "unknown snapshot copy failure: $failure" >&2
      exit 1
      ;;
  esac
fi
exec /bin/cp "$@"
EOF
chmod +x "$tmp/bin/cp"

run_installer() {
  run_privileged env \
    HSERVER_ROOT_PREFIX="$tmp/root" \
    HSERVER_SYSTEMCTL="$tmp/systemctl" \
    HSERVER_SYSTEMCTL_STATE_DIR="$tmp/systemctl-state" \
    HSERVER_OS_RELEASE=/etc/os-release \
    TEST_SYSTEMD_STATE="${TEST_SYSTEMD_STATE:-up}" \
    HSERVER_INSTALL_UPDATE_MANIFEST_URL="${HSERVER_INSTALL_UPDATE_MANIFEST_URL:-}" \
    HSERVER_INSTALL_UPDATE_MANIFEST_PUBLIC_KEYS="${HSERVER_INSTALL_UPDATE_MANIFEST_PUBLIC_KEYS:-}" \
    HSERVER_SKIP_HEALTHCHECK=1 \
      "$root_dir/scripts/hserver-install.sh" "$@"
}

run_retained_installer() {
  run_privileged env \
    HSERVER_ROOT_PREFIX="$tmp/root" \
    HSERVER_SYSTEMCTL="$tmp/systemctl" \
    HSERVER_SYSTEMCTL_STATE_DIR="$tmp/systemctl-state" \
    HSERVER_OS_RELEASE=/etc/os-release \
    HSERVER_SKIP_HEALTHCHECK=1 \
      "$tmp/root/usr/local/libexec/hserver-install" "$@"
}

run_faulted_upgrade() {
  failure=$1
  run_privileged env \
    HSERVER_ROOT_PREFIX="$tmp/root" \
    HSERVER_SYSTEMCTL="$tmp/systemctl" \
    HSERVER_SYSTEMCTL_STATE_DIR="$tmp/systemctl-state" \
    HSERVER_SYSTEMCTL_LOG="$tmp/$failure-systemctl.log" \
    HSERVER_OS_RELEASE=/etc/os-release \
    HSERVER_SKIP_HEALTHCHECK=1 \
    HSERVER_TEST_SNAPSHOT_COPY_FAILURE="$failure" \
    HSERVER_TEST_SNAPSHOT_COPY_MARKER="$tmp/$failure-copy-attempted" \
    PATH="$tmp/bin:$PATH" \
      "$root_dir/scripts/hserver-install.sh" upgrade \
        --binary "$tmp/v2" --cli-binary "$tmp/cli-v2"
}

wait_for_sqlite_marker() {
  marker=$1
  attempts=0
  while ! grep -Fqx "$marker" "$wal_sqlite_log"; do
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 100 ]; then
      cat "$wal_sqlite_log" >&2
      printf '%s\n' "SQLite fixture did not reach marker: $marker" >&2
      exit 1
    fi
    sleep 0.1
  done
}

stage='unsafe vhosts-root rejection'
for unsafe_vhosts_root in \
  relative-sites \
  / \
  '/srv/hserver/sites with spaces' \
  '/srv/hserver/sites"quoted' \
  /srv/hserver/../sites \
  /srv/hserver/sites\;unsafe
do
  if run_installer install --vhosts-root "$unsafe_vhosts_root" \
      --binary "$tmp/v1" --cli-binary "$tmp/cli-v1" \
      >"$tmp/unsafe-vhosts-root.log" 2>&1; then
    printf '%s\n' "unsafe --vhosts-root was unexpectedly accepted: $unsafe_vhosts_root" >&2
    exit 1
  fi
  [ ! -e "$tmp/root" ]
done

stage='default install'
no_option_root=$tmp/no-option-root
no_option_state=$tmp/no-option-systemctl-state
mkdir -p "$no_option_state"
run_privileged env \
  HSERVER_ROOT_PREFIX="$no_option_root" \
  HSERVER_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_SYSTEMCTL_STATE_DIR="$no_option_state" \
  HSERVER_OS_RELEASE=/etc/os-release \
  HSERVER_SKIP_HEALTHCHECK=1 \
    "$root_dir/scripts/hserver-install.sh" install \
      --binary "$tmp/v1" --cli-binary "$tmp/cli-v1" >"$tmp/no-option-install.log"
grep -q '^HSERVER_VHOSTS_ROOT=$' "$no_option_root/etc/hserver/hserver.env"
[ ! -e "$no_option_root/srv/hserver/sites" ]

stage='missing CLI rejection'
if run_installer install --binary "$tmp/v1" --cli-binary "$tmp/missing-cli" >"$tmp/missing-cli.log" 2>&1; then
  printf '%s\n' "install unexpectedly accepted a missing CLI" >&2
  exit 1
fi
[ ! -e "$tmp/root/usr/local/bin/hserver-panel" ]
[ ! -e "$tmp/root/usr/local/bin/hserverctl" ]
grep -q 'binary not found' "$tmp/missing-cli.log"

stage='host preflight rejection'
if TEST_SYSTEMD_STATE=down run_installer install --binary "$tmp/v1" --cli-binary "$tmp/cli-v1" >"$tmp/preflight.log" 2>&1; then
  printf '%s\n' "install unexpectedly passed a failed host preflight" >&2
  exit 1
fi
[ ! -e "$tmp/root/usr/local/bin/hserver-panel" ]
[ ! -e "$tmp/root/usr/local/bin/hserverctl" ]
grep -q 'host preflight failed' "$tmp/preflight.log"

stage='failed initial install rollback'
failed_root=$tmp/failed-root
failed_state=$tmp/failed-systemctl-state
mkdir -p "$failed_state"
if run_privileged env \
  PATH="$tmp/bin:$PATH" \
  HSERVER_ROOT_PREFIX="$failed_root" \
  HSERVER_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_SYSTEMCTL_STATE_DIR="$failed_state" \
  HSERVER_OS_RELEASE=/etc/os-release \
  HSERVER_HEALTH_BINARY="$failed_root/usr/local/bin/hserver-panel" \
  HSERVER_HEALTH_TIMEOUT=1 \
  HSERVER_SKIP_HEALTHCHECK=0 \
    "$root_dir/scripts/hserver-install.sh" install \
      --vhosts-root /srv/hserver/sites \
      --binary "$tmp/broken" --cli-binary "$tmp/cli-v1" >"$tmp/broken-install.log" 2>&1; then
  printf '%s\n' "broken initial install unexpectedly succeeded" >&2
  exit 1
fi
[ ! -e "$failed_root/usr/local/bin/hserver-panel" ]
[ ! -e "$failed_root/usr/local/bin/hserverctl" ]
[ ! -e "$failed_root/usr/local/libexec/hserver-install" ]
[ ! -e "$failed_root/usr/local/libexec/hserver-doctor" ]
[ ! -e "$failed_root/etc/systemd/system/hserver.service" ]
[ ! -e "$failed_root/etc/hserver" ]
[ ! -e "$failed_root/var/lib/hserver" ]
[ ! -e "$failed_root/srv/hserver/sites" ]
[ ! -e "$failed_state/active" ]
[ ! -e "$failed_state/enabled" ]
[ ! -d "$failed_root" ] \
  || [ -z "$(find "$failed_root" -type f -name 'hserver-*.conf' -print -quit)" ]
grep -q 'installation failed its health check and was rolled back' "$tmp/broken-install.log" \
  || { cat "$tmp/broken-install.log" >&2; exit 1; }

stage='preserved initial state rollback'
preserved_root=$tmp/preserved-root
preserved_state=$tmp/preserved-systemctl-state
mkdir -p \
  "$preserved_root/etc/hserver" \
  "$preserved_root/etc/systemd/system" \
  "$preserved_root/etc/nginx/snippets" \
  "$preserved_root/usr/local/libexec" \
  "$preserved_root/usr/local/share/hserver/nginx-snippets" \
  "$preserved_root/srv/hserver/sites" \
  "$preserved_root/var/lib/hserver/deploy-templates" \
  "$preserved_root/var/lib/hserver" \
  "$preserved_state"
cat >"$preserved_root/etc/hserver/hserver.env" <<'EOF'
HSERVER_PORT=3085
HSERVER_NGINX_SNIPPETS_DIR=/etc/nginx/snippets
EOF
printf '%s\n' preserved-data >"$preserved_root/var/lib/hserver/sentinel"
printf '%s\n' preserved-sites >"$preserved_root/srv/hserver/sites/sentinel"
printf '%s\n' preserved-template >"$preserved_root/var/lib/hserver/deploy-templates/custom.json"
printf '%s\n' preserved-unit >"$preserved_root/etc/systemd/system/hserver.service"
printf '%s\n' preserved-installer >"$preserved_root/usr/local/libexec/hserver-install"
printf '%s\n' preserved-doctor >"$preserved_root/usr/local/libexec/hserver-doctor"
printf '%s\n' preserved-managed >"$preserved_root/etc/nginx/snippets/hserver-security-headers.conf"
printf '%s\n' unrelated >"$preserved_root/etc/nginx/snippets/unrelated.conf"
printf '%s\n' preserved-lifecycle >"$preserved_root/usr/local/share/hserver/nginx-snippets/hserver-security-headers.conf"
printf '%s\n' lifecycle-unrelated >"$preserved_root/usr/local/share/hserver/unrelated"
touch "$preserved_state/active" "$preserved_state/enabled"
if run_privileged env \
  PATH="$tmp/bin:$PATH" \
  HSERVER_ROOT_PREFIX="$preserved_root" \
  HSERVER_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_SYSTEMCTL_STATE_DIR="$preserved_state" \
  HSERVER_OS_RELEASE=/etc/os-release \
  HSERVER_HEALTH_BINARY="$preserved_root/usr/local/bin/hserver-panel" \
  HSERVER_HEALTH_TIMEOUT=1 \
  HSERVER_SKIP_HEALTHCHECK=0 \
    "$root_dir/scripts/hserver-install.sh" install \
      --vhosts-root /srv/hserver/sites \
      --binary "$tmp/broken" --cli-binary "$tmp/cli-v1" >"$tmp/preserved-install.log" 2>&1; then
  printf '%s\n' "broken initial install with prior state unexpectedly succeeded" >&2
  exit 1
fi
[ ! -e "$preserved_root/usr/local/bin/hserver-panel" ]
[ ! -e "$preserved_root/usr/local/bin/hserverctl" ]
[ "$(cat "$preserved_root/usr/local/libexec/hserver-install")" = preserved-installer ]
[ "$(cat "$preserved_root/usr/local/libexec/hserver-doctor")" = preserved-doctor ]
[ "$(cat "$preserved_root/etc/systemd/system/hserver.service")" = preserved-unit ]
[ "$(cat "$preserved_root/etc/nginx/snippets/hserver-security-headers.conf")" = preserved-managed ]
[ "$(cat "$preserved_root/etc/nginx/snippets/unrelated.conf")" = unrelated ]
[ "$(cat "$preserved_root/usr/local/share/hserver/nginx-snippets/hserver-security-headers.conf")" = preserved-lifecycle ]
[ "$(cat "$preserved_root/usr/local/share/hserver/unrelated")" = lifecycle-unrelated ]
[ "$(cat "$preserved_root/var/lib/hserver/sentinel")" = preserved-data ]
[ "$(cat "$preserved_root/srv/hserver/sites/sentinel")" = preserved-sites ]
[ "$(cat "$preserved_root/var/lib/hserver/deploy-templates/custom.json")" = preserved-template ]
[ ! -e "$preserved_root/var/lib/hserver/deploy-templates/docker-compose.json" ]
grep -q '^HSERVER_PORT=3085$' "$preserved_root/etc/hserver/hserver.env"
[ -f "$preserved_state/active" ]
[ -f "$preserved_state/enabled" ]
grep -q 'installation failed its health check and was rolled back' "$tmp/preserved-install.log"

stage='successful install and next steps'
HSERVER_INSTALL_UPDATE_MANIFEST_URL=https://releases.example.com/hserver/release-manifest.json \
HSERVER_INSTALL_UPDATE_MANIFEST_PUBLIC_KEYS=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= \
  run_installer install --vhosts-root /srv/hserver/sites \
    --binary "$tmp/v1" --cli-binary "$tmp/cli-v1" >"$tmp/install.log"
[ "$("$tmp/root/usr/local/bin/hserver-panel")" = v1 ]
[ "$("$tmp/root/usr/local/bin/hserverctl")" = cli-v1 ]
[ -x "$tmp/root/usr/local/libexec/hserver-install" ]
[ -x "$tmp/root/usr/local/libexec/hserver-doctor" ]
cmp -s "$root_dir/scripts/hserver-install.sh" "$tmp/root/usr/local/libexec/hserver-install"
cmp -s "$root_dir/scripts/hserver-doctor.sh" "$tmp/root/usr/local/libexec/hserver-doctor"
grep -q 'HServer is ready for first access.' "$tmp/install.log"
grep -Fq "Installed starter deployment templates: $tmp/root/var/lib/hserver/deploy-templates" "$tmp/install.log"
grep -q 'ssh -N -L 3085:127.0.0.1:3085 YOUR_SSH_USER@YOUR_SERVER' "$tmp/install.log"
grep -q 'Open http://127.0.0.1:3085 in your browser.' "$tmp/install.log"
grep -q 'Sign in as admin@localhost' "$tmp/install.log"
grep -Fq "$tmp/root/etc/hserver/hserver.env" "$tmp/install.log"
initial_password=$(sed -n 's/^HSERVER_ADMIN_PASS=//p' "$tmp/root/etc/hserver/hserver.env")
[ -n "$initial_password" ]
! grep -Fq "$initial_password" "$tmp/install.log"
grep -q '^HSERVER_VHOSTS_ROOT=/srv/hserver/sites$' "$tmp/root/etc/hserver/hserver.env"
! grep -q "^HSERVER_VHOSTS_ROOT=$tmp/root/" "$tmp/root/etc/hserver/hserver.env"
grep -q '^HSERVER_PHP_CONFIG_ROOT=/etc/php$' "$tmp/root/etc/hserver/hserver.env"
grep -q '^HSERVER_PHP_BINARY_ROOT=/usr/sbin$' "$tmp/root/etc/hserver/hserver.env"
grep -q '^HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT=3$' "$tmp/root/etc/hserver/hserver.env"
[ -d "$tmp/root/srv/hserver/sites" ]
[ "$(stat -c %a "$tmp/root/srv/hserver/sites")" = 755 ]

# The running panel keeps committed onboarding/settings rows in SQLite's WAL.
# Keep that connection open while the installer snapshots the live database so
# this exercises the online backup path rather than the legacy main-file copy.
stage='WAL online backup preserves onboarding state'
wal_db=$tmp/root/var/lib/hserver/hserver.db
wal_sqlite_fifo=$tmp/wal-sqlite-input
wal_sqlite_log=$tmp/wal-sqlite.log
mkfifo "$wal_sqlite_fifo"
sqlite3 "$wal_db" <"$wal_sqlite_fifo" >"$wal_sqlite_log" 2>&1 &
wal_sqlite_pid=$!
exec 3>"$wal_sqlite_fifo"
wal_sqlite_fd_open=1
printf '%s\n' \
  'PRAGMA journal_mode=WAL;' \
  'PRAGMA wal_autocheckpoint=0;' \
  'CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL);' \
  'BEGIN;' \
  "INSERT INTO settings(key,value,updated_at) VALUES ('onboarding_completed','true','before-upgrade');" \
  "INSERT INTO settings(key,value,updated_at) VALUES ('onboarding_step','5','before-upgrade');" \
  "INSERT INTO settings(key,value,updated_at) VALUES ('installation_label','wal-before-upgrade','before-upgrade');" \
  'COMMIT;' \
  '.print wal-state-before-upgrade' >&3
wait_for_sqlite_marker wal-state-before-upgrade
[ -f "$wal_db-wal" ]
[ -f "$wal_db-shm" ]
# Restrictive source sidecars make the fixture match an installation-owned
# database, and the installer must create the backup with the same 0600 mode.
chmod 0600 "$wal_db" "$wal_db-wal" "$wal_db-shm"
[ "$(stat -c %a "$wal_db-wal")" = 600 ]
[ "$(stat -c %a "$wal_db-shm")" = 600 ]

run_installer upgrade --binary "$tmp/v2" --cli-binary "$tmp/cli-v2" >/dev/null
wal_snapshot=$(cat "$tmp/root/var/lib/hserver/releases/latest-pre-upgrade")
case "$wal_snapshot" in
  "$tmp/root/var/lib/hserver/releases/"*) ;;
  *)
    printf '%s\n' 'WAL snapshot marker points outside the fixture releases directory' >&2
    exit 1
    ;;
esac
[ -f "$wal_snapshot/hserver.db" ]
grep -Fqx "hserver.db=$wal_db" "$wal_snapshot/databases.map"
[ "$(stat -c %a "$wal_snapshot/hserver.db")" = 600 ]
[ "$(sqlite3 "$wal_snapshot/hserver.db" "SELECT value FROM settings WHERE key='onboarding_completed';")" = true ]
[ "$(sqlite3 "$wal_snapshot/hserver.db" "SELECT value FROM settings WHERE key='onboarding_step';")" = 5 ]
[ "$(sqlite3 "$wal_snapshot/hserver.db" "SELECT value FROM settings WHERE key='installation_label';")" = wal-before-upgrade ]

printf '%s\n' \
  'BEGIN;' \
  "UPDATE settings SET value='false', updated_at='after-upgrade' WHERE key='onboarding_completed';" \
  "UPDATE settings SET value='0', updated_at='after-upgrade' WHERE key='onboarding_step';" \
  "UPDATE settings SET value='wal-after-upgrade', updated_at='after-upgrade' WHERE key='installation_label';" \
  'COMMIT;' \
  '.print wal-state-after-upgrade' >&3
wait_for_sqlite_marker wal-state-after-upgrade
printf '%s\n' '.exit' >&3
exec 3>&-
wal_sqlite_fd_open=0
wait "$wal_sqlite_pid"
wal_sqlite_pid=

run_installer rollback >/dev/null
[ ! -e "$wal_db-wal" ]
[ ! -e "$wal_db-shm" ]
[ "$(sqlite3 "$wal_db" "SELECT value FROM settings WHERE key='onboarding_completed';")" = true ]
[ "$(sqlite3 "$wal_db" "SELECT value FROM settings WHERE key='onboarding_step';")" = 5 ]
[ "$(sqlite3 "$wal_db" "SELECT value FROM settings WHERE key='installation_label';")" = wal-before-upgrade ]

stage='lifecycle argument validation'
cp "$tmp/root/etc/hserver/hserver.env" "$tmp/vhosts-root-env-before-rejection"
for lifecycle_command in upgrade rollback uninstall next-steps; do
  if run_installer "$lifecycle_command" --vhosts-root /srv/hserver/sites \
      >"$tmp/$lifecycle_command-vhosts-root.log" 2>&1; then
    printf '%s\n' "$lifecycle_command unexpectedly accepted --vhosts-root" >&2
    exit 1
  fi
  cmp -s "$tmp/vhosts-root-env-before-rejection" "$tmp/root/etc/hserver/hserver.env"
done

stage='retained lifecycle upgrade and rollback'
run_privileged env \
  HSERVER_ROOT_PREFIX="$tmp/root" \
    "$tmp/root/usr/local/libexec/hserver-install" next-steps >"$tmp/next-steps.log"
grep -q 'HServer is ready for first access.' "$tmp/next-steps.log"
! grep -Fq "$initial_password" "$tmp/next-steps.log"
[ -f "$tmp/root/etc/hserver/hserver.env" ]
[ "$(stat -c %a "$tmp/root/etc/hserver/hserver.env")" = 600 ]
grep -Fq "HSERVER_PGM_BACKUP_DIR=$tmp/root/var/lib/hserver/pgm-backups" \
  "$tmp/root/etc/hserver/hserver.env"
[ -d "$tmp/root/var/lib/hserver/pgm-backups" ]
[ "$(stat -c %a "$tmp/root/var/lib/hserver/pgm-backups")" = 700 ]
grep -q '^HSERVER_UPDATE_MANIFEST_URL=https://releases.example.com/hserver/release-manifest.json$' \
  "$tmp/root/etc/hserver/hserver.env"
grep -q '^HSERVER_UPDATE_MANIFEST_PUBLIC_KEYS=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=$' \
  "$tmp/root/etc/hserver/hserver.env"
for key in $(sed -n 's/^\([A-Z0-9_]*\)=.*/\1/p' "$root_dir/.env.example"); do
  [ "$key" = VERSION ] && continue
  grep -q "^${key}=" "$tmp/root/etc/hserver/hserver.env"
done
[ -f "$tmp/root/etc/systemd/system/hserver.service" ]
[ -f "$tmp/systemctl-state/active" ]
[ -f "$tmp/systemctl-state/enabled" ]
for snippet in "$root_dir"/deploy/nginx-snippets/hserver-*.conf; do
  cmp -s "$snippet" "$tmp/root/etc/nginx/snippets/$(basename "$snippet")"
  cmp -s "$snippet" "$tmp/root/usr/local/share/hserver/nginx-snippets/$(basename "$snippet")"
done
for template in "$root_dir"/deploy/hserver-deploy-templates.example/*.json; do
  cmp -s "$template" "$tmp/root/var/lib/hserver/deploy-templates/$(basename "$template")"
done

printf '%s\n' operator-template >"$tmp/root/var/lib/hserver/deploy-templates/node-build.json"

# Upgrades must preserve explicit PHP installation roots in the existing
# protected environment file rather than regenerating or normalizing it.
sed -i \
  -e 's#^HSERVER_PHP_CONFIG_ROOT=.*#HSERVER_PHP_CONFIG_ROOT=/srv/custom/php/config#' \
  -e 's#^HSERVER_PHP_BINARY_ROOT=.*#HSERVER_PHP_BINARY_ROOT=/srv/custom/php/bin#' \
  "$tmp/root/etc/hserver/hserver.env"
cp "$tmp/root/etc/hserver/hserver.env" "$tmp/php-roots-env-before-upgrade"

# The retained installer must remain sufficient after the extracted release
# package is gone. It sources the persisted fixed Nginx lifecycle assets.
run_retained_installer upgrade --binary "$tmp/v2" --cli-binary "$tmp/cli-v2" >/dev/null
[ "$("$tmp/root/usr/local/bin/hserver-panel")" = v2 ]
[ "$("$tmp/root/usr/local/bin/hserverctl")" = cli-v2 ]
cmp -s "$tmp/php-roots-env-before-upgrade" "$tmp/root/etc/hserver/hserver.env"
cmp -s "$root_dir/scripts/hserver-install.sh" "$tmp/root/usr/local/libexec/hserver-install"
[ "$(cat "$tmp/root/var/lib/hserver/deploy-templates/node-build.json")" = operator-template ]
retained_upgrade_snapshot=$(cat "$tmp/root/var/lib/hserver/releases/latest-pre-upgrade")
grep -q '^LIFECYCLE_ASSETS_DIR_WAS_PRESENT=1$' "$retained_upgrade_snapshot/manifest.env"
grep -q '^LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT=1$' "$retained_upgrade_snapshot/manifest.env"
grep -q '^LIFECYCLE_SNIPPETS_WERE_PRESENT=1$' "$retained_upgrade_snapshot/manifest.env"
printf '%s\n' modified-retained >"$tmp/root/usr/local/share/hserver/nginx-snippets/hserver-security-headers.conf"
run_retained_installer rollback >/dev/null
[ "$("$tmp/root/usr/local/bin/hserver-panel")" = v1 ]
[ "$("$tmp/root/usr/local/bin/hserverctl")" = cli-v1 ]
cmp -s "$root_dir/deploy/nginx-snippets/hserver-security-headers.conf" \
  "$tmp/root/usr/local/share/hserver/nginx-snippets/hserver-security-headers.conf"

# Snapshot copy failures must be rejected before systemctl stop. The old
# release, configuration, snapshot marker, and service state stay intact for
# copy, disk-full, and permission-style failures.
stage='snapshot copy failure rollback'
fault_snapshot_count=$(find "$tmp/root/var/lib/hserver/releases" \
  -mindepth 1 -maxdepth 1 -type d -name '*-pre-upgrade*' | wc -l | tr -d ' ')
cp "$tmp/root/var/lib/hserver/releases/latest-pre-upgrade" "$tmp/fault-marker-before"
cp "$tmp/root/etc/hserver/hserver.env" "$tmp/fault-env-before"
cp "$tmp/root/etc/systemd/system/hserver.service" "$tmp/fault-unit-before"
for failure in copy disk permission; do
  rm -f "$tmp/$failure-copy-attempted" "$tmp/$failure-systemctl.log"
  if run_faulted_upgrade "$failure" >"$tmp/$failure-upgrade.log" 2>&1; then
    printf '%s\n' "snapshot $failure failure unexpectedly allowed an upgrade" >&2
    exit 1
  fi
  [ -f "$tmp/$failure-copy-attempted" ]
  grep -q 'existing service was left running' "$tmp/$failure-upgrade.log"
  [ "$("$tmp/root/usr/local/bin/hserver-panel")" = v1 ]
  [ "$("$tmp/root/usr/local/bin/hserverctl")" = cli-v1 ]
  cmp -s "$tmp/fault-env-before" "$tmp/root/etc/hserver/hserver.env"
  cmp -s "$tmp/fault-unit-before" "$tmp/root/etc/systemd/system/hserver.service"
  cmp -s "$tmp/fault-marker-before" "$tmp/root/var/lib/hserver/releases/latest-pre-upgrade"
  [ -f "$tmp/systemctl-state/active" ]
  [ -f "$tmp/systemctl-state/enabled" ]
  if grep -q '^stop$' "$tmp/$failure-systemctl.log"; then exit 1; fi
  if grep -q '^start$' "$tmp/$failure-systemctl.log"; then exit 1; fi
  if grep -q '^daemon-reload$' "$tmp/$failure-systemctl.log"; then exit 1; fi
  current_snapshot_count=$(find "$tmp/root/var/lib/hserver/releases" \
    -mindepth 1 -maxdepth 1 -type d -name '*-pre-upgrade*' | wc -l | tr -d ' ')
  [ "$current_snapshot_count" -eq "$fault_snapshot_count" ]
  [ -z "$(find "$tmp/root/var/lib/hserver/releases" \
    -mindepth 1 -maxdepth 1 -type d -name '.pre-upgrade.*' -print -quit)" ]
done

stage='invalid snapshot retention rejection'
cp "$tmp/root/etc/hserver/hserver.env" "$tmp/retention-env-before-rejection"
printf '%s\n' 'HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT=0' \
  >>"$tmp/root/etc/hserver/hserver.env"
if run_installer upgrade --binary "$tmp/v2" --cli-binary "$tmp/cli-v2" \
    >"$tmp/invalid-retention-upgrade.log" 2>&1; then
  printf '%s\n' "invalid panel snapshot retention was unexpectedly accepted" >&2
  exit 1
fi
mv "$tmp/retention-env-before-rejection" "$tmp/root/etc/hserver/hserver.env"
[ "$("$tmp/root/usr/local/bin/hserver-panel")" = v1 ]
[ -f "$tmp/systemctl-state/active" ]
[ -f "$tmp/systemctl-state/enabled" ]
grep -q 'HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT must be a positive integer' \
  "$tmp/invalid-retention-upgrade.log"

stage='legacy lifecycle recovery'
printf '%s\n' sentinel >"$tmp/root/var/lib/hserver/hserver.db"
cat >"$tmp/root/usr/local/libexec/hserver-install" <<'EOF'
#!/usr/bin/env sh
echo legacy-installer
EOF
cat >"$tmp/root/usr/local/libexec/hserver-doctor" <<'EOF'
#!/usr/bin/env sh
echo legacy-doctor
EOF
chmod 0755 \
  "$tmp/root/usr/local/libexec/hserver-install" \
  "$tmp/root/usr/local/libexec/hserver-doctor"
run_installer upgrade --binary "$tmp/v2" --cli-binary "$tmp/cli-v2" >/dev/null
[ "$("$tmp/root/usr/local/bin/hserver-panel")" = v2 ]
[ "$("$tmp/root/usr/local/bin/hserverctl")" = cli-v2 ]
cmp -s "$root_dir/scripts/hserver-install.sh" "$tmp/root/usr/local/libexec/hserver-install"
cmp -s "$root_dir/scripts/hserver-doctor.sh" "$tmp/root/usr/local/libexec/hserver-doctor"
[ -n "$(find "$tmp/root/var/lib/hserver/releases" -type f -name hserver-panel -print -quit)" ]
[ -n "$(find "$tmp/root/var/lib/hserver/releases" -type f -name hserverctl -print -quit)" ]
releases_dir=$tmp/root/var/lib/hserver/releases
upgrade_marker=$releases_dir/latest-pre-upgrade
[ -f "$upgrade_marker" ]
upgrade_snapshot=$(cat "$upgrade_marker")
case "$upgrade_snapshot" in
  "$releases_dir"/*) ;;
  *)
    printf '%s\n' 'latest-pre-upgrade marker points outside the releases directory' >&2
    exit 1
    ;;
esac
[ -d "$upgrade_snapshot" ]
upgrade_manifest=$upgrade_snapshot/manifest.env
[ -f "$upgrade_manifest" ]
grep -q '^SERVICE_WAS_ACTIVE=1$' "$upgrade_manifest"
grep -q '^SERVICE_WAS_ENABLED=1$' "$upgrade_manifest"
grep -q '^CLI_WAS_PRESENT=1$' "$upgrade_manifest"
grep -q '^INSTALLER_WAS_PRESENT=1$' "$upgrade_manifest"
grep -q '^DOCTOR_WAS_PRESENT=1$' "$upgrade_manifest"
grep -q '^NGINX_SNIPPETS_WERE_PRESENT=1$' "$upgrade_manifest"
grep -q '^LIFECYCLE_ASSETS_DIR_WAS_PRESENT=1$' "$upgrade_manifest"
grep -q '^LIFECYCLE_SNIPPETS_DIR_WAS_PRESENT=1$' "$upgrade_manifest"
grep -q '^LIFECYCLE_SNIPPETS_WERE_PRESENT=1$' "$upgrade_manifest"

printf '%s\n' changed-after-upgrade >"$tmp/root/etc/nginx/snippets/hserver-security-headers.conf"

run_installer rollback >/dev/null
[ "$("$tmp/root/usr/local/bin/hserver-panel")" = v1 ]
[ "$("$tmp/root/usr/local/bin/hserverctl")" = cli-v1 ]
[ "$("$tmp/root/usr/local/libexec/hserver-install")" = legacy-installer ]
[ "$("$tmp/root/usr/local/libexec/hserver-doctor")" = legacy-doctor ]
[ "$(cat "$tmp/root/var/lib/hserver/hserver.db")" = sentinel ]
[ -f "$tmp/systemctl-state/active" ]
[ -f "$tmp/systemctl-state/enabled" ]
cmp -s "$root_dir/deploy/nginx-snippets/hserver-security-headers.conf" \
  "$tmp/root/etc/nginx/snippets/hserver-security-headers.conf"

rm -f "$tmp/systemctl-state/active" "$tmp/systemctl-state/enabled"
if run_privileged env \
  PATH="$tmp/bin:$PATH" \
  HSERVER_ROOT_PREFIX="$tmp/root" \
  HSERVER_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_SYSTEMCTL_STATE_DIR="$tmp/systemctl-state" \
  HSERVER_HEALTH_BINARY="$tmp/root/usr/local/bin/hserver-panel" \
  HSERVER_HEALTH_TIMEOUT=1 \
  HSERVER_SKIP_HEALTHCHECK=0 \
    "$root_dir/scripts/hserver-install.sh" upgrade --binary "$tmp/broken" --cli-binary "$tmp/cli-v2" >"$tmp/broken-upgrade.log" 2>&1; then
  printf '%s\n' "broken upgrade unexpectedly succeeded" >&2
  exit 1
fi
[ "$("$tmp/root/usr/local/bin/hserver-panel")" = v1 ]
[ "$("$tmp/root/usr/local/bin/hserverctl")" = cli-v1 ]
[ "$("$tmp/root/usr/local/libexec/hserver-install")" = legacy-installer ]
[ "$("$tmp/root/usr/local/libexec/hserver-doctor")" = legacy-doctor ]
[ "$(cat "$tmp/root/var/lib/hserver/hserver.db")" = sentinel ]
[ ! -f "$tmp/systemctl-state/active" ]
[ ! -f "$tmp/systemctl-state/enabled" ]
grep -q 'upgrade failed and was rolled back' "$tmp/broken-upgrade.log"

# A host upgraded from a pre-CLI release gains the CLI, but a later explicit
# rollback restores the exact earlier binary set and removes that new CLI.
rm -f "$tmp/root/usr/local/bin/hserverctl"
run_installer upgrade --binary "$tmp/v2" --cli-binary "$tmp/cli-v2" >/dev/null
legacy_snapshot=$(cat "$tmp/root/var/lib/hserver/releases/latest-pre-upgrade")
grep -q '^CLI_WAS_PRESENT=0$' "$legacy_snapshot/manifest.env"
[ "$("$tmp/root/usr/local/bin/hserverctl")" = cli-v2 ]
run_installer rollback >/dev/null
[ "$("$tmp/root/usr/local/bin/hserver-panel")" = v1 ]
[ ! -e "$tmp/root/usr/local/bin/hserverctl" ]

# Five consecutive upgrades must not grow the pre-upgrade snapshot directory
# without bound. The newest marker remains usable and its SQLite copy is the
# state restored by the final manual rollback.
stage='snapshot retention bound and rollback'
for version in 3 4 5 6 7; do
  printf 'db-before-v%s\n' "$version" >"$tmp/root/var/lib/hserver/hserver.db"
  run_installer upgrade --binary "$tmp/v$version" --cli-binary "$tmp/cli-v$version" >/dev/null
done
panel_snapshot_count=$(find "$tmp/root/var/lib/hserver/releases" \
  -mindepth 1 -maxdepth 1 -type d -name '*-pre-upgrade*' | wc -l | tr -d ' ')
[ "$panel_snapshot_count" -eq 3 ]
panel_latest_snapshot=$(cat "$tmp/root/var/lib/hserver/releases/latest-pre-upgrade")
[ -d "$panel_latest_snapshot" ]
[ -f "$panel_latest_snapshot/hserver-panel" ]
printf '%s\n' db-after-v7 >"$tmp/root/var/lib/hserver/hserver.db"
run_installer rollback >/dev/null
[ "$("$tmp/root/usr/local/bin/hserver-panel")" = v6 ]
[ "$("$tmp/root/usr/local/bin/hserverctl")" = cli-v6 ]
[ "$(cat "$tmp/root/var/lib/hserver/hserver.db")" = db-before-v7 ]
[ -f "$tmp/systemctl-state/active" ]
[ -f "$tmp/systemctl-state/enabled" ]

stage='uninstall'
printf '%s\n' preserve-on-uninstall >"$tmp/root/usr/local/share/hserver/unrelated"
run_installer uninstall >/dev/null
[ ! -e "$tmp/root/usr/local/bin/hserver-panel" ]
[ ! -e "$tmp/root/usr/local/bin/hserverctl" ]
[ ! -e "$tmp/root/usr/local/libexec/hserver-install" ]
[ ! -e "$tmp/root/usr/local/libexec/hserver-doctor" ]
[ ! -e "$tmp/root/usr/local/share/hserver/nginx-snippets" ]
[ "$(cat "$tmp/root/usr/local/share/hserver/unrelated")" = preserve-on-uninstall ]
[ ! -e "$tmp/root/etc/systemd/system/hserver.service" ]
[ -f "$tmp/root/etc/hserver/hserver.env" ]
[ -f "$tmp/root/var/lib/hserver/hserver.db" ]
[ -d "$tmp/root/srv/hserver/sites" ]

# Preserve-layout mode is used only by the detached updater for an observed or
# explicitly configured noncanonical installation. It replaces the fixed
# binary pair and leaves the custom unit, drop-in, environment, data, and DB
# layout byte-for-byte unchanged.
stage='preserve-layout fixture setup'
custom_root=$tmp/custom-root
custom_state=$tmp/custom-systemctl-state
custom_bin=$custom_root/opt/hserver-panel/bin
custom_data=$custom_root/opt/hserver-panel/state
custom_unit=$custom_root/etc/systemd/system/hserver.service
custom_dropin=$custom_root/etc/systemd/system/hserver.service.d/layout.conf
mkdir -p "$custom_bin" "$custom_data" "$(dirname "$custom_dropin")" "$custom_state"
cp "$tmp/v1" "$custom_bin/hserver-panel"
cp "$tmp/cli-v1" "$custom_bin/hserverctl"
chmod 0755 "$custom_bin/hserver-panel" "$custom_bin/hserverctl"
cat >"$custom_data/hserver.env" <<'EOF'
HSERVER_PORT=3085
HSERVER_DB_PATH=/opt/hserver-panel/state/hserver.db
HSERVER_DATA_DIR=/opt/hserver-panel/state
EOF
printf '%s\n' 'custom-db-sentinel' >"$custom_data/hserver.db"
cat >"$custom_unit" <<'EOF'
[Service]
ExecStart=/opt/hserver-panel/bin/hserver-panel
EnvironmentFile=/opt/hserver-panel/state/hserver.env
WorkingDirectory=/opt/hserver-panel/state
EOF
cat >"$custom_dropin" <<'EOF'
[Service]
UMask=0077
EOF
cp "$custom_data/hserver.env" "$tmp/custom-env-before"
cp "$custom_data/hserver.db" "$tmp/custom-db-before"
cp "$custom_unit" "$tmp/custom-unit-before"
cp "$custom_dropin" "$tmp/custom-dropin-before"
touch "$custom_state/active" "$custom_state/enabled"

run_preserve_upgrade() {
  panel_source=$1
  cli_source=$2
  run_privileged env \
    HSERVER_ROOT_PREFIX="$custom_root" \
    HSERVER_SYSTEMCTL="$tmp/systemctl" \
    HSERVER_SYSTEMCTL_STATE_DIR="$custom_state" \
    HSERVER_TEST_SERVICE_FILE="$custom_unit" \
    HSERVER_TEST_DROPIN_FILE="$custom_dropin" \
    HSERVER_PRESERVE_LAYOUT=1 \
    HSERVER_BINARY_PATH="$custom_bin/hserver-panel" \
    HSERVER_CLI_PATH="$custom_bin/hserverctl" \
    HSERVER_DATA_DIR_PATH="$custom_data" \
    HSERVER_ENV_FILE="$custom_data/hserver.env" \
    HSERVER_HEALTH_BINARY="$custom_bin/hserver-panel" \
    HSERVER_HEALTH_TIMEOUT=1 \
    HSERVER_SKIP_HEALTHCHECK=${HSERVER_TEST_SKIP_HEALTHCHECK:-1} \
    PATH="$tmp/bin:$PATH" \
      "$root_dir/scripts/hserver-install.sh" upgrade \
        --binary "$panel_source" --cli-binary "$cli_source"
}

stage='preserve-layout successful upgrade'
run_preserve_upgrade "$tmp/v2" "$tmp/cli-v2" >/dev/null
[ "$("$custom_bin/hserver-panel")" = v2 ]
[ "$("$custom_bin/hserverctl")" = cli-v2 ]
cmp -s "$tmp/custom-env-before" "$custom_data/hserver.env"
cmp -s "$tmp/custom-db-before" "$custom_data/hserver.db"
cmp -s "$tmp/custom-unit-before" "$custom_unit"
cmp -s "$tmp/custom-dropin-before" "$custom_dropin"
[ -f "$custom_state/active" ]
[ -f "$custom_state/enabled" ]
custom_snapshot=$(cat "$custom_data/releases/latest-pre-upgrade")
grep -q '^PRESERVE_LAYOUT=1$' "$custom_snapshot/manifest.env"
grep -q '^SERVICE_WAS_ACTIVE=1$' "$custom_snapshot/manifest.env"
grep -q '^SERVICE_WAS_ENABLED=1$' "$custom_snapshot/manifest.env"
[ -f "$custom_snapshot/systemd-unit-state.txt" ]
[ -f "$custom_snapshot/systemd-unit-content.txt" ]
[ ! -e "$custom_snapshot/databases.map" ]
[ ! -e "$custom_root/usr/local/libexec/hserver-install" ]

# A deliberately stopped and disabled custom service remains stopped and
# disabled after a successful binary-pair update.
stage='preserve-layout stopped service'
rm -f "$custom_state/active" "$custom_state/enabled"
run_preserve_upgrade "$tmp/v1" "$tmp/cli-v1" >/dev/null
[ "$("$custom_bin/hserver-panel")" = v1 ]
[ "$("$custom_bin/hserverctl")" = cli-v1 ]
[ ! -e "$custom_state/active" ]
[ ! -e "$custom_state/enabled" ]

# A failed candidate health check restores exact binaries and the prior
# enabled/active service state without restoring or deleting live SQLite files.
stage='preserve-layout health rollback'
touch "$custom_state/active" "$custom_state/enabled"
cp "$custom_bin/hserver-panel" "$tmp/custom-panel-before-failure"
cp "$custom_bin/hserverctl" "$tmp/custom-cli-before-failure"
if HSERVER_TEST_SKIP_HEALTHCHECK=0 run_preserve_upgrade "$tmp/broken" "$tmp/cli-v2" \
    >"$tmp/custom-broken-upgrade.log" 2>&1; then
  printf '%s\n' 'broken preserve-layout upgrade unexpectedly succeeded' >&2
  exit 1
fi
cmp -s "$tmp/custom-panel-before-failure" "$custom_bin/hserver-panel"
cmp -s "$tmp/custom-cli-before-failure" "$custom_bin/hserverctl"
cmp -s "$tmp/custom-env-before" "$custom_data/hserver.env"
cmp -s "$tmp/custom-db-before" "$custom_data/hserver.db"
cmp -s "$tmp/custom-unit-before" "$custom_unit"
cmp -s "$tmp/custom-dropin-before" "$custom_dropin"
[ -f "$custom_state/active" ]
[ -f "$custom_state/enabled" ]
grep -q 'upgrade failed and was rolled back' "$tmp/custom-broken-upgrade.log"

# Symlink and non-regular destinations fail before a snapshot, systemctl call,
# or binary mutation.
stage='symlink destination rejection'
symlink_root=$tmp/symlink-root
symlink_bin=$symlink_root/opt/hserver-panel/bin
symlink_data=$symlink_root/opt/hserver-panel/state
mkdir -p "$symlink_bin" "$symlink_data"
cp "$tmp/v1" "$symlink_bin/panel-target"
cp "$tmp/cli-v1" "$symlink_bin/hserverctl"
chmod 0755 "$symlink_bin/panel-target" "$symlink_bin/hserverctl"
ln -s panel-target "$symlink_bin/hserver-panel"
: >"$tmp/symlink-systemctl.log"
if run_privileged env \
  HSERVER_ROOT_PREFIX="$symlink_root" \
  HSERVER_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_SYSTEMCTL_STATE_DIR="$custom_state" \
  HSERVER_SYSTEMCTL_LOG="$tmp/symlink-systemctl.log" \
  HSERVER_PRESERVE_LAYOUT=1 \
  HSERVER_BINARY_PATH="$symlink_bin/hserver-panel" \
  HSERVER_CLI_PATH="$symlink_bin/hserverctl" \
  HSERVER_DATA_DIR_PATH="$symlink_data" \
  HSERVER_SKIP_HEALTHCHECK=1 \
    "$root_dir/scripts/hserver-install.sh" upgrade \
      --binary "$tmp/v2" --cli-binary "$tmp/cli-v2" \
      >"$tmp/symlink-upgrade.log" 2>&1; then
  printf '%s\n' 'preserve-layout upgrade unexpectedly accepted a symlink destination' >&2
  exit 1
fi
[ -s "$tmp/symlink-systemctl.log" ] && exit 1
[ ! -e "$symlink_data/releases" ]
[ "$("$symlink_bin/panel-target")" = v1 ]
grep -q 'destination must not be a symlink' "$tmp/symlink-upgrade.log"

printf '%s\n' "hserver-install lifecycle test: OK"
