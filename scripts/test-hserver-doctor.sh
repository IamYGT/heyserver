#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
mkdir -p "$tmp/root/usr/local/bin" "$tmp/root/etc/hserver" \
  "$tmp/root/etc/systemd/system" "$tmp/root/var/lib/hserver"

cat >"$tmp/os-release" <<'EOF'
ID=ubuntu
VERSION_ID="24.04"
EOF
cat >"$tmp/uname" <<'EOF'
#!/usr/bin/env sh
case "$1" in
  -s) printf '%s\n' Linux ;;
  -m) printf '%s\n' "${TEST_ARCH:-x86_64}" ;;
  *) exit 1 ;;
esac
EOF
cat >"$tmp/systemctl" <<'EOF'
#!/usr/bin/env sh
case "$1" in
  is-active|is-enabled) [ "${TEST_SERVICE_STATE:-up}" = up ] ;;
  show-environment) [ "${TEST_SYSTEMD_STATE:-up}" = up ] ;;
  *) exit 0 ;;
esac
EOF
cat >"$tmp/curl" <<'EOF'
#!/usr/bin/env sh
[ "${TEST_HEALTH_STATE:-up}" = up ]
EOF
cat >"$tmp/root/usr/local/bin/hserver-panel" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
cat >"$tmp/root/usr/local/bin/hserverctl" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
chmod +x "$tmp/uname" "$tmp/systemctl" "$tmp/curl" "$tmp/root/usr/local/bin/hserver-panel" "$tmp/root/usr/local/bin/hserverctl"

cat >"$tmp/root/etc/hserver/hserver.env" <<'EOF'
HSERVER_PORT=3085
HSERVER_JWT_SECRET=test-only-secret
HSERVER_ADMIN_EMAIL=admin@localhost
EOF
chmod 0600 "$tmp/root/etc/hserver/hserver.env"
printf '%s\n' '[Service]' >"$tmp/root/etc/systemd/system/hserver.service"

run_doctor() {
  HSERVER_ROOT_PREFIX="$tmp/root" \
  HSERVER_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_CURL="$tmp/curl" \
  HSERVER_UNAME="$tmp/uname" \
  HSERVER_UID=0 \
  HSERVER_OS_RELEASE="$tmp/os-release" \
    "$root_dir/scripts/hserver-doctor.sh" "$@"
}

run_doctor preflight >"$tmp/preflight.log"
grep -q 'Summary: 0 failure(s), 0 warning(s)' "$tmp/preflight.log"

run_doctor installed >"$tmp/installed.log"
grep -q 'health endpoint responded' "$tmp/installed.log"
grep -q 'no pending BIND lifecycle recovery journal' "$tmp/installed.log"
grep -q 'Summary: 0 failure(s), 0 warning(s)' "$tmp/installed.log"

mkdir -p "$tmp/root/var/lib/hserver/bind"
chmod 0700 "$tmp/root/var/lib/hserver/bind"
printf '%s\n' '{"test":"journal content is never printed"}' >"$tmp/root/var/lib/hserver/bind/lifecycle-transaction.json"
chmod 0600 "$tmp/root/var/lib/hserver/bind/lifecycle-transaction.json"
run_doctor installed >"$tmp/journal-pending.log"
grep -q 'BIND lifecycle journal directory permissions are 0700' "$tmp/journal-pending.log"
grep -q 'BIND lifecycle journal is a protected regular file' "$tmp/journal-pending.log"
grep -q 'BIND lifecycle recovery is pending' "$tmp/journal-pending.log"
if grep -q 'journal content is never printed' "$tmp/journal-pending.log"; then
  printf '%s\n' "doctor printed BIND lifecycle journal content" >&2
  exit 1
fi
grep -q 'Summary: 0 failure(s), 1 warning(s)' "$tmp/journal-pending.log"

chmod 0644 "$tmp/root/var/lib/hserver/bind/lifecycle-transaction.json"
if run_doctor installed >"$tmp/journal-file-mode.log"; then
  printf '%s\n' "loosely permissioned BIND lifecycle journal unexpectedly passed" >&2
  exit 1
fi
grep -q 'BIND lifecycle journal permissions must be 0600 or stricter' "$tmp/journal-file-mode.log"

chmod 0600 "$tmp/root/var/lib/hserver/bind/lifecycle-transaction.json"
chmod 0755 "$tmp/root/var/lib/hserver/bind"
if run_doctor installed >"$tmp/journal-dir-mode.log"; then
  printf '%s\n' "loosely permissioned BIND lifecycle journal directory unexpectedly passed" >&2
  exit 1
fi
grep -q 'BIND lifecycle journal directory must be a mode-0700 regular directory' "$tmp/journal-dir-mode.log"
rm -rf "$tmp/root/var/lib/hserver/bind"

if TEST_ARCH=riscv64 run_doctor preflight >"$tmp/architecture.log"; then
  printf '%s\n' "unsupported architecture unexpectedly passed" >&2
  exit 1
fi
grep -q 'unsupported architecture: riscv64' "$tmp/architecture.log"

if TEST_SYSTEMD_STATE=down run_doctor preflight >"$tmp/systemd.log"; then
  printf '%s\n' "unreachable systemd manager unexpectedly passed" >&2
  exit 1
fi
grep -q 'systemd manager is not reachable' "$tmp/systemd.log"

if TEST_SERVICE_STATE=down run_doctor installed >"$tmp/service.log"; then
  printf '%s\n' "inactive service unexpectedly passed" >&2
  exit 1
fi
grep -q 'hserver service is not active' "$tmp/service.log"

if TEST_HEALTH_STATE=down run_doctor installed >"$tmp/health.log"; then
  printf '%s\n' "unhealthy endpoint unexpectedly passed" >&2
  exit 1
fi
grep -q 'health endpoint did not respond' "$tmp/health.log"

printf '%s\n' "hserver doctor test: OK"
