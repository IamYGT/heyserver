#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
mkdir -p "$tmp/bin"

cat >"$tmp/bin/uname" <<'EOF'
#!/usr/bin/env sh
case "$1" in
  -s) printf '%s\n' Linux ;;
  -m) printf '%s\n' x86_64 ;;
  *) exit 1 ;;
esac
EOF
cat >"$tmp/bin/systemctl" <<'EOF'
#!/usr/bin/env sh
case "$1" in
  show-environment) exit 0 ;;
  *) exit 0 ;;
esac
EOF
for command_name in apt-get curl openssl tar install sqlite3; do
  cat >"$tmp/bin/$command_name" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
done
chmod 0755 "$tmp/bin"/*

write_os_release() {
  os_id=$1
  os_version=$2
  cat >"$tmp/os-release" <<EOF
ID=$os_id
VERSION_ID="$os_version"
EOF
}

run_doctor() {
  HSERVER_UID=0 \
  HSERVER_UNAME="$tmp/bin/uname" \
  HSERVER_SYSTEMCTL="$tmp/bin/systemctl" \
  HSERVER_APT_GET="${HSERVER_TEST_APT_GET:-$tmp/bin/apt-get}" \
  HSERVER_CURL="$tmp/bin/curl" \
  HSERVER_OPENSSL="$tmp/bin/openssl" \
  HSERVER_TAR="${HSERVER_TEST_TAR:-$tmp/bin/tar}" \
  HSERVER_INSTALL="${HSERVER_TEST_INSTALL:-$tmp/bin/install}" \
  HSERVER_SQLITE3="${HSERVER_TEST_SQLITE3:-$tmp/bin/sqlite3}" \
  HSERVER_OS_RELEASE="$tmp/os-release" \
    "$root_dir/scripts/hserver-doctor.sh" preflight
}

assert_supported() {
  expected=$1
  output=$2
  write_os_release "$3" "$4"
  run_doctor >"$output"
  grep -q "PASS  supported operating system: $expected" "$output"
  grep -q 'PASS  supported package manager available:' "$output"
  for command_name in systemctl openssl curl tar install sqlite3; do
    grep -q "PASS  required command available: .*${command_name}" "$output"
  done
  grep -q 'Summary: 0 failure(s), 0 warning(s)' "$output"
}

assert_supported 'Debian 12' "$tmp/debian.log" debian 12
assert_supported 'Ubuntu 24.04' "$tmp/ubuntu.log" ubuntu 24.04

for unsupported in \
  'debian 11' \
  'ubuntu 22.04' \
  'fedora 40'
do
  set -- $unsupported
  write_os_release "$1" "$2"
  if run_doctor >"$tmp/unsupported-$1-$2.log"; then
    printf 'unsupported operating system unexpectedly passed: %s %s\n' "$1" "$2" >&2
    exit 1
  fi
  grep -q 'native installation currently supports Ubuntu 24.04 or newer or Debian 12 or newer' \
    "$tmp/unsupported-$1-$2.log"
done

write_os_release debian 12
if HSERVER_TEST_APT_GET="$tmp/bin/missing-apt-get" run_doctor >"$tmp/missing-apt.log"; then
  printf '%s\n' 'Debian preflight unexpectedly passed without apt-get.' >&2
  exit 1
fi
grep -q "supported package manager is missing: $tmp/bin/missing-apt-get" "$tmp/missing-apt.log"

for prerequisite in tar sqlite3; do
  write_os_release debian 12
  case "$prerequisite" in
    tar)
      if HSERVER_TEST_TAR="$tmp/bin/missing-$prerequisite" run_doctor >"$tmp/missing-$prerequisite.log"; then
        printf 'Debian preflight unexpectedly passed without %s.\n' "$prerequisite" >&2
        exit 1
      fi
      ;;
    sqlite3)
      if HSERVER_TEST_SQLITE3="$tmp/bin/missing-$prerequisite" run_doctor >"$tmp/missing-$prerequisite.log"; then
        printf 'Debian preflight unexpectedly passed without %s.\n' "$prerequisite" >&2
        exit 1
      fi
      ;;
  esac
  grep -q "required command is missing: $tmp/bin/missing-$prerequisite" \
    "$tmp/missing-$prerequisite.log"
done

if [ "${HSERVER_NATIVE_OS_DETECTION_REAL:-0}" = 1 ]; then
  real_id=$(sed -n 's/^ID=//p' /etc/os-release | head -n 1 | tr -d '"')
  real_version=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | head -n 1 | tr -d '"')
  [ "$real_id" = debian ] || {
    printf 'real OS detection test requires Debian, found: %s\n' "${real_id:-unknown}" >&2
    exit 1
  }
  case "$real_version" in
    12|12.*) ;;
    *) printf 'real OS detection test requires Debian 12, found: %s\n' "${real_version:-unknown}" >&2; exit 1 ;;
  esac
  HSERVER_UID=0 \
  HSERVER_SYSTEMCTL="$tmp/bin/systemctl" \
  HSERVER_OS_RELEASE=/etc/os-release \
  HSERVER_NATIVE_OS_DETECTION_REAL=0 \
    "$root_dir/scripts/hserver-doctor.sh" preflight >"$tmp/real-debian.log"
  grep -q "PASS  supported operating system: Debian $real_version" "$tmp/real-debian.log"
  grep -q 'Summary: 0 failure(s), 0 warning(s)' "$tmp/real-debian.log"
  for command_name in apt-get curl openssl tar install sqlite3; do
    command -v "$command_name" >/dev/null 2>&1
  done
fi

printf '%s\n' 'native OS detection test: OK'
