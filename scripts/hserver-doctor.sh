#!/usr/bin/env sh
set -eu

PROGRAM=hserver-doctor
MODE=${1:-preflight}
ROOT_PREFIX=${HSERVER_ROOT_PREFIX:-}
SYSTEMCTL=${HSERVER_SYSTEMCTL:-systemctl}
APT_GET=${HSERVER_APT_GET:-apt-get}
CURL=${HSERVER_CURL:-curl}
OPENSSL=${HSERVER_OPENSSL:-openssl}
TAR=${HSERVER_TAR:-tar}
INSTALL=${HSERVER_INSTALL:-install}
SQLITE3=${HSERVER_SQLITE3:-sqlite3}
UNAME=${HSERVER_UNAME:-uname}
UID_VALUE=${HSERVER_UID:-$(id -u)}
OS_RELEASE=${HSERVER_OS_RELEASE:-$ROOT_PREFIX/etc/os-release}
BINARY_PATH=${HSERVER_BINARY_PATH:-$ROOT_PREFIX/usr/local/bin/hserver-panel}
CLI_PATH=${HSERVER_CLI_PATH:-$ROOT_PREFIX/usr/local/bin/hserverctl}
ENV_FILE=${HSERVER_ENV_FILE:-$ROOT_PREFIX/etc/hserver/hserver.env}
DATA_DIR=${HSERVER_DATA_DIR_PATH:-$ROOT_PREFIX/var/lib/hserver}
BIND_JOURNAL_DIR=$DATA_DIR/bind
BIND_JOURNAL=$BIND_JOURNAL_DIR/lifecycle-transaction.json
SERVICE_FILE=${HSERVER_SERVICE_FILE:-$ROOT_PREFIX/etc/systemd/system/hserver.service}
failures=0
warnings=0

usage() {
  cat <<'EOF'
HServer installation diagnostics

Usage:
  ./doctor.sh preflight  # validate a host before native installation
  sudo ./doctor.sh installed # validate an existing native installation

Exit status is non-zero when a required condition fails. Warnings are reported
separately and do not change the exit status.
EOF
}

pass() {
  printf 'PASS  %s\n' "$*"
}

fail() {
  failures=$((failures + 1))
  printf 'FAIL  %s\n' "$*"
}

warn() {
  warnings=$((warnings + 1))
  printf 'WARN  %s\n' "$*"
}

has_command() {
  command -v "$1" >/dev/null 2>&1
}

env_value() {
  key=$1
  fallback=$2
  value=$(sed -n "s/^${key}=//p" "$ENV_FILE" 2>/dev/null | tail -n 1 || true)
  if [ -n "$value" ]; then
    printf '%s\n' "$value"
  else
    printf '%s\n' "$fallback"
  fi
}

check_preflight() {
  os_name=$($UNAME -s 2>/dev/null || true)
  if [ "$os_name" = Linux ]; then
    pass "Linux kernel detected"
  else
    fail "native installation requires Linux (detected: ${os_name:-unknown})"
  fi

  architecture=$($UNAME -m 2>/dev/null || true)
  case "$architecture" in
    x86_64|aarch64|arm64) pass "supported architecture: $architecture" ;;
    *) fail "unsupported architecture: ${architecture:-unknown}; use amd64 or arm64" ;;
  esac

  if [ -r "$OS_RELEASE" ]; then
    os_id=$(sed -n 's/^ID=//p' "$OS_RELEASE" | head -n 1 | tr -d '"')
    os_version=$(sed -n 's/^VERSION_ID=//p' "$OS_RELEASE" | head -n 1 | tr -d '"')
    supported_os=
    case "$os_id" in
      ubuntu)
        if awk -v version="$os_version" 'BEGIN { exit !(version + 0 >= 24.04) }'; then
          supported_os="Ubuntu $os_version"
        fi
        ;;
      debian)
        if awk -v version="$os_version" 'BEGIN { exit !(version + 0 >= 12) }'; then
          supported_os="Debian $os_version"
        fi
        ;;
    esac
    if [ -n "$supported_os" ]; then
      pass "supported operating system: $supported_os"
      if has_command "$APT_GET"; then
        pass "supported package manager available: $APT_GET"
      else
        fail "supported package manager is missing: $APT_GET"
      fi
    else
      fail "native installation currently supports Ubuntu 24.04 or newer or Debian 12 or newer (detected: ${os_id:-unknown} ${os_version:-unknown})"
    fi
  else
    fail "operating-system metadata is not readable: $OS_RELEASE"
  fi

  if [ "$UID_VALUE" = 0 ]; then
    pass "running with root privileges"
  else
    fail "native lifecycle commands require root privileges"
  fi

  for command_name in "$SYSTEMCTL" "$OPENSSL" "$CURL" "$TAR" "$INSTALL" "$SQLITE3" sed; do
    if has_command "$command_name"; then
      pass "required command available: $command_name"
    else
      fail "required command is missing: $command_name"
    fi
  done

  if has_command "$SYSTEMCTL"; then
    if "$SYSTEMCTL" show-environment >/dev/null 2>&1; then
      pass "systemd manager is reachable"
    else
      fail "systemctl is installed, but the systemd manager is not reachable"
    fi
  fi
}

check_installed() {
  if [ -x "$BINARY_PATH" ]; then
    pass "panel binary is executable: $BINARY_PATH"
  else
    fail "panel binary is missing or not executable: $BINARY_PATH"
  fi

  if [ -x "$CLI_PATH" ]; then
    pass "CLI binary is executable: $CLI_PATH"
  else
    fail "CLI binary is missing or not executable: $CLI_PATH"
  fi

  if [ -f "$SERVICE_FILE" ]; then
    pass "systemd unit exists: $SERVICE_FILE"
  else
    fail "systemd unit is missing: $SERVICE_FILE"
  fi

  if [ -f "$ENV_FILE" ]; then
    pass "configuration exists: $ENV_FILE"
    env_mode=$(stat -c %a "$ENV_FILE" 2>/dev/null || true)
    if [ "$env_mode" = 600 ]; then
      pass "configuration permissions are 0600"
    else
      fail "configuration permissions must be 0600 (detected: ${env_mode:-unknown})"
    fi
    if grep -q '^HSERVER_JWT_SECRET=..*$' "$ENV_FILE" && grep -q '^HSERVER_ADMIN_EMAIL=..*$' "$ENV_FILE"; then
      pass "required configuration keys are present"
    else
      fail "configuration is missing HSERVER_JWT_SECRET or HSERVER_ADMIN_EMAIL"
    fi
  else
    fail "configuration is missing: $ENV_FILE"
  fi

  if [ -d "$DATA_DIR" ] && [ -w "$DATA_DIR" ]; then
    pass "data directory is writable: $DATA_DIR"
  else
    fail "data directory is missing or not writable: $DATA_DIR"
  fi

  if [ -e "$BIND_JOURNAL_DIR" ] || [ -L "$BIND_JOURNAL_DIR" ]; then
    bind_journal_dir_mode=$(stat -c %a "$BIND_JOURNAL_DIR" 2>/dev/null || true)
    if [ -d "$BIND_JOURNAL_DIR" ] && [ ! -L "$BIND_JOURNAL_DIR" ] && [ "$bind_journal_dir_mode" = 700 ]; then
      pass "BIND lifecycle journal directory permissions are 0700"
    else
      fail "BIND lifecycle journal directory must be a mode-0700 regular directory (detected: ${bind_journal_dir_mode:-unknown})"
    fi
  fi

  if [ -e "$BIND_JOURNAL" ] || [ -L "$BIND_JOURNAL" ]; then
    bind_journal_mode=$(stat -c %a "$BIND_JOURNAL" 2>/dev/null || true)
    case "$bind_journal_mode" in
      000|200|400|600)
        if [ -f "$BIND_JOURNAL" ] && [ ! -L "$BIND_JOURNAL" ]; then
          pass "BIND lifecycle journal is a protected regular file"
        else
          fail "BIND lifecycle journal must be a regular file"
        fi
        ;;
      *) fail "BIND lifecycle journal permissions must be 0600 or stricter (detected: ${bind_journal_mode:-unknown})" ;;
    esac
    warn "BIND lifecycle recovery is pending; inspect BIND readiness and restart HServer"
  else
    pass "no pending BIND lifecycle recovery journal"
  fi

  if "$SYSTEMCTL" is-enabled --quiet hserver >/dev/null 2>&1; then
    pass "hserver service is enabled"
  else
    warn "hserver service is not enabled"
  fi
  if "$SYSTEMCTL" is-active --quiet hserver >/dev/null 2>&1; then
    pass "hserver service is active"
    if [ -f "$ENV_FILE" ]; then
      port=$(env_value HSERVER_PORT 3085)
      health_url=${HSERVER_HEALTH_URL:-http://127.0.0.1:$port/api/health}
      if "$CURL" -fsS --max-time 3 "$health_url" >/dev/null 2>&1; then
        pass "health endpoint responded: $health_url"
      else
        fail "health endpoint did not respond: $health_url"
      fi
    fi
  else
    fail "hserver service is not active"
  fi
}

case "$MODE" in
  preflight) check_preflight ;;
  installed)
    check_preflight
    check_installed
    ;;
  -h|--help|help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

printf '\nSummary: %s failure(s), %s warning(s)\n' "$failures" "$warnings"
[ "$failures" -eq 0 ]
