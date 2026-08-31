#!/usr/bin/env sh
set -eu

PROGRAM=hserver-agent-install
BINARY_NAME=hserver-agent
SERVICE_NAME=hserver-agent
ROOT_PREFIX=${HSERVER_AGENT_ROOT_PREFIX:-}
SYSTEMCTL=${HSERVER_AGENT_SYSTEMCTL:-systemctl}
HEALTH_TIMEOUT=${HSERVER_AGENT_HEALTH_TIMEOUT:-20}
SKIP_HEALTHCHECK=${HSERVER_AGENT_SKIP_HEALTHCHECK:-0}
# Keep a small, provider-neutral rollback window by default. The configured
# count is read from the protected agent environment file and may be
# overridden with HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT.
SNAPSHOT_RETENTION_DEFAULT=3
SNAPSHOT_RETENTION_MAX=100
TOKEN_PATH_DEFAULT=/etc/hserver-agent.token
TOKEN_PATH_MAX=4096
PROFILE_SCHEMA_VERSION=1
PROFILE_MAX_DOCUMENT_BYTES=$((16 * 1024))
PROFILE_DIRECTORY_NAME=profile
PROFILE_NEXT_FILE_NAME=candidate.json
PROFILE_ACTIVE_FILE_NAME=active.json
PROFILE_PREVIOUS_FILE_NAME=previous.json
PROFILE_STATE_FILE_NAME=state.json
PROFILE_ERROR_PAYLOAD=profile_payload_invalid
PROFILE_ERROR_PAYLOAD_TOO_LARGE=profile_payload_too_large
PROFILE_ERROR_REVISION=profile_revision_invalid
PROFILE_ERROR_CORRUPT=profile_corrupt
PROFILE_ERROR_STATE_CORRUPT=profile_state_corrupt
PROFILE_ERROR_STORE=profile_store_failed
PROFILE_ERROR_SCHEDULE=profile_schedule_failed
STATE_DIR_EXPLICIT=${HSERVER_AGENT_STATE_DIR:+1}
RELEASES_DIR_EXPLICIT=${HSERVER_AGENT_RELEASES_DIR:+1}
LIFECYCLE_PATH_EXPLICIT=${HSERVER_AGENT_LIFECYCLE_INSTALLER:+1}

root_path() {
  printf '%s%s\n' "$ROOT_PREFIX" "$1"
}

BINARY_PATH=${HSERVER_AGENT_BINARY_PATH:-$(root_path /usr/local/bin/hserver-agent)}
CONFIG_FILE=${HSERVER_AGENT_CONFIG_FILE:-$(root_path /etc/hserver-agent.env)}
TOKEN_FILE=$(root_path "$TOKEN_PATH_DEFAULT")
SERVICE_FILE=${HSERVER_AGENT_SERVICE_FILE:-$(root_path /etc/systemd/system/hserver-agent.service)}
STATE_DIR=${HSERVER_AGENT_STATE_DIR:-$(root_path /var/lib/hserver-agent)}
RELEASES_DIR=${HSERVER_AGENT_RELEASES_DIR:-$STATE_DIR/releases}
LIFECYCLE_PATH=${HSERVER_AGENT_LIFECYCLE_INSTALLER:-$(root_path /usr/local/libexec/hserver-agent-install)}
PROFILE_DIR=$STATE_DIR/$PROFILE_DIRECTORY_NAME
PROFILE_NEXT_FILE=$PROFILE_DIR/$PROFILE_NEXT_FILE_NAME
PROFILE_ACTIVE_FILE=$PROFILE_DIR/$PROFILE_ACTIVE_FILE_NAME
PROFILE_PREVIOUS_FILE=$PROFILE_DIR/$PROFILE_PREVIOUS_FILE_NAME
PROFILE_STATE_FILE=$PROFILE_DIR/$PROFILE_STATE_FILE_NAME

usage() {
  cat <<'EOF'
Heyserver managed-node agent lifecycle installer

Usage:
  sudo ./hserver-agent-install.sh install [--binary PATH] --config PATH --token-file PATH
  sudo ./hserver-agent-install.sh upgrade [--binary PATH]
  sudo ./hserver-agent-install.sh rollback
  sudo ./hserver-agent-install.sh apply-profile
  sudo ./hserver-agent-install.sh uninstall [--purge-config]

The install command accepts the enrollment token only through a file and never
prints it. HSERVER_AGENT_TOKEN_FILE in the configuration selects the absolute
destination (default: /etc/hserver-agent.token); --token-file remains the
protected install input source. Upgrade and rollback preserve the configured
destination and token.
The installer retains the latest HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT
pre-upgrade snapshots (default: 3; minimum: 1; maximum: 100), including the
snapshot referenced by the rollback marker.
The apply-profile command accepts no arguments. It consumes only the local
profile candidate staged at HSERVER_AGENT_STATE_DIR/profile/candidate.json;
the candidate wrapper and service sandbox are validated locally and no hub
path or command is accepted.
EOF
}

die() {
  printf '%s: %s\n' "$PROGRAM" "$*" >&2
  exit 1
}

require_root() {
  [ "$(id -u)" -eq 0 ] || die "run this command as root"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

validate_token_destination() {
  token_destination=$1
  token_destination_bytes=$(LC_ALL=C printf '%s' "$token_destination" | wc -c | tr -d '[:space:]')
  [ "$token_destination_bytes" -le "$TOKEN_PATH_MAX" ] \
    || die "HSERVER_AGENT_TOKEN_FILE exceeds $TOKEN_PATH_MAX bytes"
  case "$token_destination" in
    '') die "HSERVER_AGENT_TOKEN_FILE is required" ;;
    /*) ;;
    *) die "HSERVER_AGENT_TOKEN_FILE must be a clean absolute file path" ;;
  esac
  case "$token_destination" in
    /) die "HSERVER_AGENT_TOKEN_FILE must be a file path other than /" ;;
    *[[:space:]]*) die "HSERVER_AGENT_TOKEN_FILE must not contain whitespace" ;;
    *//*|*/.|*/..|*/./*|*/../*|*/) \
      die "HSERVER_AGENT_TOKEN_FILE must be a clean absolute file path" ;;
    *[!ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._/+:-]*) \
      die "HSERVER_AGENT_TOKEN_FILE contains unsafe path characters" ;;
  esac
}

validate_token_config_bytes() {
  token_config=$1
  # A NUL cannot survive command substitution into a shell variable. Reject it
  # from the source file before extracting HSERVER_AGENT_TOKEN_FILE instead.
  if LC_ALL=C awk 'index($0, sprintf("%c", 0)) { found = 1 } END { exit !found }' "$token_config"; then
    die "HSERVER_AGENT_TOKEN_FILE must not contain NUL bytes"
  fi
}

resolve_token_destination() {
  token_config=$1
  validate_token_config_bytes "$token_config"
  configured_token=$(config_value_from "$token_config" HSERVER_AGENT_TOKEN_FILE)
  validate_token_destination "$configured_token"
  TOKEN_FILE=$(root_path "$configured_token")
  if [ -L "$TOKEN_FILE" ]; then
    die "HSERVER_AGENT_TOKEN_FILE must not be a symlink"
  fi
  if [ -e "$TOKEN_FILE" ] && [ ! -f "$TOKEN_FILE" ]; then
    die "HSERVER_AGENT_TOKEN_FILE must identify a regular file, not a directory or device"
  fi
}

ensure_token_parent() {
  token_parent=$(dirname "$TOKEN_FILE")
  if [ -e "$token_parent" ]; then
    [ -d "$token_parent" ] \
      || die "HSERVER_AGENT_TOKEN_FILE parent must be a directory"
    [ ! -L "$token_parent" ] \
      || die "HSERVER_AGENT_TOKEN_FILE parent must not be a symlink"
    return 0
  fi
  install -d -m 0700 "$token_parent" \
    || die "could not create HSERVER_AGENT_TOKEN_FILE parent"
}

cleanup_token_tmp() {
  if [ -n "${token_tmp:-}" ]; then
    rm -f -- "$token_tmp"
    token_tmp=
  fi
}

copy_token_atomic() {
  token_source=$1
  ensure_token_parent
  token_tmp=$(mktemp "$(dirname "$TOKEN_FILE")/.hserver-agent-token.XXXXXX") \
    || die "could not create temporary agent token file"
  trap cleanup_token_tmp EXIT HUP INT TERM
  if ! install -m 0600 "$token_source" "$token_tmp"; then
    die "could not stage agent token file"
  fi
  if ! mv -f -- "$token_tmp" "$TOKEN_FILE"; then
    die "could not install agent token file"
  fi
  token_tmp=
  trap - EXIT HUP INT TERM
}

assert_safe_path() {
  case "$1" in
    ''|/|/etc|/var|/usr|/usr/local) die "refusing unsafe path: $1" ;;
  esac
}

default_binary_source() {
  script_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)
  for candidate in \
    "$script_dir/$BINARY_NAME" \
    "$script_dir/../bin/$BINARY_NAME" \
    "./$BINARY_NAME" \
    "./bin/$BINARY_NAME"
  do
    if [ -f "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

validate_binary() {
  [ -f "$1" ] || die "binary not found: $1"
  [ -s "$1" ] || die "binary is empty: $1"
}

run_preflight_doctor() {
  script_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)
  for doctor in "$script_dir/doctor.sh" "$script_dir/hserver-doctor.sh"; do
    if [ -f "$doctor" ]; then
      HSERVER_ROOT_PREFIX="$ROOT_PREFIX" \
      HSERVER_SYSTEMCTL="$SYSTEMCTL" \
        sh "$doctor" preflight || die "host preflight failed"
      return 0
    fi
  done
  die "installation doctor is missing; use the complete release package"
}

validate_config() {
  [ -f "$1" ] || die "agent configuration not found: $1"
  grep -Eq '^HSERVER_AGENT_HUB_URL=https?://[^[:space:]]+$' "$1" \
    || die "agent configuration requires HSERVER_AGENT_HUB_URL"
  grep -Eq '^HSERVER_AGENT_NODE_ID=[A-Za-z0-9][A-Za-z0-9._-]{0,127}$' "$1" \
    || die "agent configuration requires a valid HSERVER_AGENT_NODE_ID"
  if grep -Eq '^HSERVER_AGENT_TOKEN=' "$1"; then
    die "remove HSERVER_AGENT_TOKEN and use the separate --token-file input"
  fi
  resolve_token_destination "$1"
  if grep -Eiq '^HSERVER_AGENT_ALLOW_PM2_READ=true[[:space:]]*$' "$1" || grep -Eq '^HSERVER_AGENT_ALLOWED_PM2_ACTIONS=.+$' "$1"; then
    pm2_binary=$(config_value_from "$1" HSERVER_AGENT_PM2_BINARY)
    pm2_home=$(config_value_from "$1" HSERVER_AGENT_PM2_HOME)
    pm2_user=$(config_value_from "$1" HSERVER_AGENT_PM2_USER)
    printf '%s\n' "$pm2_binary" | grep -Eq '^/[A-Za-z0-9._/+:-]+$' \
      || die "HSERVER_AGENT_PM2_BINARY must be an explicit absolute path when PM2 read or actions are enabled"
    printf '%s\n' "$pm2_home" | grep -Eq '^/[A-Za-z0-9._/+:-]+$' \
      || die "HSERVER_AGENT_PM2_HOME must be an explicit absolute path when PM2 read or actions are enabled"
    printf '%s\n' "$pm2_user" | grep -Eq '^[A-Za-z_][A-Za-z0-9._-]*$' \
      || die "HSERVER_AGENT_PM2_USER must be an explicit local Unix identity when PM2 read or actions are enabled"
    [ "$pm2_user" != root ] \
      || die "HSERVER_AGENT_PM2_USER must be an unprivileged account"
  fi
  validate_snapshot_retention "$1" >/dev/null
}

validate_token_file() {
  [ -f "$1" ] || die "enrollment token file not found: $1"
  [ -s "$1" ] || die "enrollment token file is empty: $1"
  [ "$(wc -c <"$1")" -le 65536 ] || die "enrollment token file exceeds 65536 bytes"
}

config_path_or_default() {
  key=$1
  fallback=$2
  value=$(awk -v key="$key" 'index($0, key "=") == 1 { value = substr($0, length(key) + 2) } END { print value }' "$CONFIG_FILE")
  [ -n "$value" ] || value=$fallback
  printf '%s\n' "$value" | grep -Eq '^/[A-Za-z0-9._/+:-]+$' \
    || die "$key must be an unquoted absolute path without whitespace when configuration writing is enabled"
  printf '%s\n' "$value"
}

config_value() {
  key=$1
  awk -v key="$key" 'index($0, key "=") == 1 { value = substr($0, length(key) + 2) } END { print value }' "$CONFIG_FILE"
}

config_value_from() {
  file=$1
  key=$2
  awk -v key="$key" 'index($0, key "=") == 1 { value = substr($0, length(key) + 2) } END { print value }' "$file"
}

snapshot_retention_value_from() {
  source_config=$1
  configured_from_file=
  if [ -f "$source_config" ]; then
    configured_from_file=$(config_value_from "$source_config" HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT)
  fi
  configured=${HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT:-}
  [ -n "$configured" ] || configured=$configured_from_file
  [ -n "$configured" ] || configured=$SNAPSHOT_RETENTION_DEFAULT
  printf '%s\n' "$configured"
}

validate_snapshot_retention_value() {
  configured=$1
  case "$configured" in
    1|2|3|4|5|6|7|8|9|[1-9][0-9]|100) ;;
    *)
      die "HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT must be a positive integer from 1 to $SNAPSHOT_RETENTION_MAX"
      ;;
  esac
}

validate_snapshot_retention() {
  source_config=$1
  configured=$(snapshot_retention_value_from "$source_config")
  if [ -f "$source_config" ]; then
    configured_from_file=$(config_value_from "$source_config" HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT)
    [ -z "$configured_from_file" ] || validate_snapshot_retention_value "$configured_from_file"
  fi
  validate_snapshot_retention_value "$configured"
  printf '%s\n' "$configured"
}

resolve_lifecycle_paths() {
  source_config=$1
  [ -f "$source_config" ] || return 0
  if [ -z "$STATE_DIR_EXPLICIT" ]; then
    configured_state=$(config_value_from "$source_config" HSERVER_AGENT_STATE_DIR)
    if [ -n "$configured_state" ]; then
      printf '%s\n' "$configured_state" | grep -Eq '^/[A-Za-z0-9._/+:-]+$' \
        || die "HSERVER_AGENT_STATE_DIR must be an unquoted absolute path without whitespace"
      STATE_DIR=$(root_path "$configured_state")
    fi
  fi
  if [ -z "$LIFECYCLE_PATH_EXPLICIT" ]; then
    configured_installer=$(config_value_from "$source_config" HSERVER_AGENT_LIFECYCLE_INSTALLER)
    if [ -n "$configured_installer" ]; then
      printf '%s\n' "$configured_installer" | grep -Eq '^/[A-Za-z0-9._/+:-]+$' \
        || die "HSERVER_AGENT_LIFECYCLE_INSTALLER must be an unquoted absolute path without whitespace"
      LIFECYCLE_PATH=$(root_path "$configured_installer")
    fi
  fi
  if [ -z "$RELEASES_DIR_EXPLICIT" ]; then
    RELEASES_DIR=$STATE_DIR/releases
  fi
  PROFILE_DIR=$STATE_DIR/$PROFILE_DIRECTORY_NAME
  PROFILE_NEXT_FILE=$PROFILE_DIR/$PROFILE_NEXT_FILE_NAME
  PROFILE_ACTIVE_FILE=$PROFILE_DIR/$PROFILE_ACTIVE_FILE_NAME
  PROFILE_PREVIOUS_FILE=$PROFILE_DIR/$PROFILE_PREVIOUS_FILE_NAME
  PROFILE_STATE_FILE=$PROFILE_DIR/$PROFILE_STATE_FILE_NAME
}

append_read_write_path() {
  if [ -n "$read_write_paths" ]; then
    read_write_paths="$read_write_paths
ReadWritePaths=$1"
  else
    read_write_paths="ReadWritePaths=$1"
  fi
}

append_config_read_write_paths() {
  key=$1
  raw=$(config_value "$key")
  [ -n "$raw" ] || return 0
  old_ifs=$IFS
  IFS=,
  set -- $raw
  IFS=$old_ifs
  for value do
    value=$(printf '%s\n' "$value" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    printf '%s\n' "$value" | grep -Eq '^/[A-Za-z0-9._/+:-]+$' \
      || die "$key must contain only comma-separated unquoted absolute paths without whitespace"
    append_read_write_path "$value"
  done
}

persist_installer() {
  script_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")" && pwd)
  script_path=$script_dir/$(basename -- "$0")
  if [ "$script_path" != "$LIFECYCLE_PATH" ]; then
    install -D -m 0755 "$script_path" "$LIFECYCLE_PATH"
  fi
}

configured_paths_need_home_access() {
  for key in HSERVER_AGENT_FILE_READ_ROOTS HSERVER_AGENT_FILE_WRITE_ROOTS HSERVER_AGENT_DEPLOY_WRITE_ROOTS; do
    raw=$(config_value "$key")
    old_ifs=$IFS
    IFS=,
    set -- $raw
    IFS=$old_ifs
    for value do
      value=$(printf '%s\n' "$value" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
      case "$value" in
        /home|/home/*|/root|/root/*) return 0 ;;
      esac
    done
  done
  return 1
}

# The managed profile is intentionally kept out of the environment file.  The
# agent stages a strict wrapper in this directory and invokes this installer
# with the fixed `apply-profile` action.  No profile path or command is accepted
# on the command line.
ensure_profile_directory() {
  [ -n "$STATE_DIR" ] || return 1
  case "$STATE_DIR" in
    /|/*//*|*/.|*/..|*/./*|*/../*) return 1 ;;
  esac
  if [ -e "$STATE_DIR" ]; then
    [ -d "$STATE_DIR" ] || return 1
    [ ! -L "$STATE_DIR" ] || return 1
  else
    install -d -m 0700 "$STATE_DIR" || return 1
  fi
  chmod 0700 "$STATE_DIR" || return 1
  chown root:root "$STATE_DIR" 2>/dev/null || return 1
  if [ -e "$PROFILE_DIR" ]; then
    [ -d "$PROFILE_DIR" ] || return 1
    [ ! -L "$PROFILE_DIR" ] || return 1
  else
    install -d -m 0700 "$PROFILE_DIR" || return 1
  fi
  chmod 0700 "$PROFILE_DIR" || return 1
  chown root:root "$PROFILE_DIR" 2>/dev/null || return 1
  return 0
}

profile_regular_file() {
  [ -f "$1" ] || return 1
  [ ! -L "$1" ] || return 1
  [ "$(stat -c '%u:%g:%a' "$1" 2>/dev/null || true)" = 0:0:600 ] || return 1
}

profile_validate_candidate_permissions() {
  profile_regular_file "$PROFILE_NEXT_FILE" || return 1
  [ "$(wc -c <"$PROFILE_NEXT_FILE" | tr -d '[:space:]')" -le "$PROFILE_MAX_DOCUMENT_BYTES" ] || return 2
  return 0
}

profile_write_atomic_from_file() {
  profile_source=$1
  profile_destination=$2
  profile_destination_dir=$(dirname "$profile_destination")
  profile_atomic_tmp=$(mktemp "$profile_destination_dir/.hserver-profile.XXXXXX") || return 1
  if ! cp "$profile_source" "$profile_atomic_tmp"; then
    rm -f -- "$profile_atomic_tmp"
    return 1
  fi
  chmod 0600 "$profile_atomic_tmp" || { rm -f -- "$profile_atomic_tmp"; return 1; }
  chown root:root "$profile_atomic_tmp" 2>/dev/null || { rm -f -- "$profile_atomic_tmp"; return 1; }
  if [ -L "$profile_destination" ]; then
    rm -f -- "$profile_atomic_tmp"
    return 1
  fi
  if ! mv -f -- "$profile_atomic_tmp" "$profile_destination"; then
    rm -f -- "$profile_atomic_tmp"
    return 1
  fi
  return 0
}

profile_write_state() {
  profile_state=$1
  profile_state_revision=$2
  profile_state_error=${3:-}
  case "$profile_state" in
    pending_restart|active|failed) ;;
    *) return 1 ;;
  esac
  case "$profile_state_revision" in
    ''|0|*[!0-9]*) return 1 ;;
  esac
  case "$profile_state_error" in
    ''|"$PROFILE_ERROR_PAYLOAD"|"$PROFILE_ERROR_PAYLOAD_TOO_LARGE"|"$PROFILE_ERROR_REVISION"|"$PROFILE_ERROR_CORRUPT"|"$PROFILE_ERROR_STATE_CORRUPT"|"$PROFILE_ERROR_STORE"|"$PROFILE_ERROR_SCHEDULE") ;;
    *) return 1 ;;
  esac
  profile_state_tmp=$(mktemp "$PROFILE_DIR/.hserver-profile-state.XXXXXX") || return 1
  if [ -n "$profile_state_error" ]; then
    printf '{"schema_version":%s,"state":"%s","revision":%s,"error_code":"%s"}\n' \
      "$PROFILE_SCHEMA_VERSION" "$profile_state" "$profile_state_revision" "$profile_state_error" >"$profile_state_tmp"
  else
    printf '{"schema_version":%s,"state":"%s","revision":%s}\n' \
      "$PROFILE_SCHEMA_VERSION" "$profile_state" "$profile_state_revision" >"$profile_state_tmp"
  fi
  chmod 0600 "$profile_state_tmp" || { rm -f -- "$profile_state_tmp"; return 1; }
  chown root:root "$profile_state_tmp" 2>/dev/null || { rm -f -- "$profile_state_tmp"; return 1; }
  if [ -L "$PROFILE_STATE_FILE" ]; then
    rm -f -- "$profile_state_tmp"
    return 1
  fi
  if ! mv -f -- "$profile_state_tmp" "$PROFILE_STATE_FILE"; then
    rm -f -- "$profile_state_tmp"
    return 1
  fi
  return 0
}

profile_parse_candidate() {
  profile_parse_error="$PROFILE_ERROR_PAYLOAD"
  profile_permission_status=0
  if profile_validate_candidate_permissions; then
    profile_permission_status=0
  else
    profile_permission_status=$?
    if [ "$profile_permission_status" -eq 2 ]; then
      profile_parse_error="$PROFILE_ERROR_PAYLOAD_TOO_LARGE"
    fi
  fi
  [ "$profile_permission_status" -eq 0 ] || return 1
  require_command python3 >/dev/null 2>&1 || {
    profile_parse_error="$PROFILE_ERROR_STORE"
    return 1
  }
  profile_metadata=
  if profile_metadata=$(python3 - "$PROFILE_NEXT_FILE" "$ROOT_PREFIX" \
    "$CONFIG_FILE" "$TOKEN_FILE" "$SERVICE_FILE" "$BINARY_PATH" \
    "$LIFECYCLE_PATH" "$STATE_DIR" "$RELEASES_DIR" 2>/dev/null <<'PY'
import base64
import json
import errno
import os
import posixpath
import re
import sys

MAX_PATH_BYTES = 4096
MAX_ROOTS = 16
MAX_DOCUMENT_BYTES = 16 * 1024
SAFE_PATH = re.compile(r"^[A-Za-z0-9._/+:-]+$")
ROOT_PREFIX = sys.argv[2]
DYNAMIC_PROTECTED_PATHS = sys.argv[3:]
STATIC_PROTECTED_ROOTS = (
    "/etc",
    "/usr",
    "/bin",
    "/sbin",
    "/boot",
    "/proc",
    "/sys",
    "/dev",
    "/run",
)
WRAPPER_FIELDS = {"schema_version", "revision", "profile"}
PROFILE_FIELDS = {
    "allowDeployRead",
    "allowDeployActions",
    "allowDeployDomainRead",
    "allowDeployDomainActions",
    "deployPlansFile",
    "deployAcmeWebroot",
    "deployWriteRoots",
}

class DuplicateKey(ValueError):
    pass

def root_join(logical):
    if not ROOT_PREFIX or ROOT_PREFIX == "/" or not logical.startswith("/"):
        return logical
    return ROOT_PREFIX.rstrip("/") + logical

def logical_from_actual(actual):
    if ROOT_PREFIX and ROOT_PREFIX != "/":
        prefix = ROOT_PREFIX.rstrip("/")
        if actual == prefix:
            return "/", True
        if actual.startswith(prefix + "/"):
            return actual[len(prefix):], True
    return actual, False

def resolve_rooted_path(path):
    """Resolve symlink components in the staged root's POSIX namespace.

    os.path.realpath() resolves an absolute symlink target against the test
    process's host root. The installer may be exercised with ROOT_PREFIX, so
    resolve one component at a time and keep absolute targets in that staged
    namespace (for example, /srv/link -> /etc must resolve to /etc below the
    staged root rather than the host's /etc).
    """
    current = posixpath.normpath(path)
    seen = set()
    for _ in range(64):
        if current in seen:
            return None
        seen.add(current)
        components = [part for part in current.split("/") if part]
        resolved = []
        for index, component in enumerate(components):
            logical_component = "/" + "/".join(resolved + [component])
            try:
                target = os.readlink(root_join(logical_component))
            except OSError as error:
                if error.errno not in (errno.ENOENT, errno.ENOTDIR, errno.EINVAL, errno.ENAMETOOLONG):
                    return None
                resolved.append(component)
                continue
            base = "/" + "/".join(resolved) if resolved else "/"
            if target.startswith("/"):
                replacement = posixpath.normpath(target)
            else:
                replacement = posixpath.normpath(posixpath.join(base, target))
            remainder = components[index + 1:]
            current = posixpath.normpath(posixpath.join(replacement, *remainder))
            break
        else:
            return "/" + "/".join(resolved) if resolved else "/"
    return None

def path_pair(path, rooted):
    lexical = posixpath.normpath(path)
    if rooted:
        resolved = resolve_rooted_path(lexical)
    else:
        resolved = os.path.realpath(lexical)
    return lexical, posixpath.normpath(resolved) if resolved else None

def path_relation(candidate, protected):
    if candidate == protected or protected.startswith(candidate.rstrip("/") + "/"):
        return "contains"
    if candidate.startswith(protected.rstrip("/") + "/"):
        return "inside"
    return "disjoint"

def protected_references():
    references = [(root, True, "directory") for root in STATIC_PROTECTED_ROOTS]
    for index, actual in enumerate(DYNAMIC_PROTECTED_PATHS):
        if actual:
            protected, rooted = logical_from_actual(actual)
            references.append((protected, rooted, "file" if index < 5 else "directory"))
    return references

PROTECTED_REFERENCES = protected_references()

def relation_is_forbidden(relation, target_kind):
    if target_kind == "directory":
        return relation != "disjoint"
    return relation == "contains"

def reject_protected_write_path(value, deploy_plans_file):
    if not value:
        return
    candidate_lexical, candidate_resolved = path_pair(value, True)
    if candidate_resolved is None:
        fail("profile_payload_invalid")
    references = list(PROTECTED_REFERENCES)
    if deploy_plans_file:
        references.append((deploy_plans_file, True, "file"))
    for protected, rooted, target_kind in references:
        protected_lexical, protected_resolved = path_pair(protected, rooted)
        if protected_resolved is None:
            fail("profile_payload_invalid")
        lexical_relation = path_relation(candidate_lexical, protected_lexical)
        resolved_relation = path_relation(candidate_resolved, protected_resolved)
        # Static and installation-owned directory trees forbid overlap in
        # either direction. Protected files forbid equality and candidate
        # ancestors, while a read-only plans file itself remains valid under
        # a static tree such as /etc.
        if relation_is_forbidden(lexical_relation, target_kind) or relation_is_forbidden(resolved_relation, target_kind):
            fail("profile_payload_invalid")
        # A symlink-mediated relation change touching a protected target is
        # rejected even when the file-target descendant case would otherwise
        # be left to the later filesystem operation.
        if (candidate_resolved != candidate_lexical and
                lexical_relation != resolved_relation and
                (lexical_relation != "disjoint" or resolved_relation != "disjoint")):
            fail("profile_payload_invalid")

def object_pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise DuplicateKey("duplicate")
        result[key] = value
    return result

def fail(code):
    print("error=" + code)
    raise SystemExit(1)

def validate_path(value, *, file=False):
    if not isinstance(value, str):
        fail("profile_payload_invalid")
    try:
        value_bytes = value.encode("utf-8")
    except UnicodeEncodeError:
        fail("profile_payload_invalid")
    if len(value_bytes) > MAX_PATH_BYTES or any(char in value for char in "\r\n\x00"):
        fail("profile_payload_invalid")
    if value == "":
        return
    if value == "/" or not value.startswith("/") or value.startswith("//"):
        fail("profile_payload_invalid")
    if file and value.endswith("/"):
        fail("profile_payload_invalid")
    # ReadWritePaths is an unquoted systemd directive.  Keep its entries
    # unambiguous and reject specifier/control characters rather than turning
    # a valid JSON string into multiple unit paths.
    if SAFE_PATH.fullmatch(value) is None:
        fail("profile_payload_invalid")
    if posixpath.normpath(value) != value:
        fail("profile_payload_invalid")

try:
    with open(sys.argv[1], "rb") as handle:
        data = handle.read(MAX_DOCUMENT_BYTES + 1)
except OSError:
    fail("profile_store_failed")
if not data or len(data) > MAX_DOCUMENT_BYTES:
    fail("profile_payload_too_large")
try:
    wrapper = json.loads(data.decode("utf-8"), object_pairs_hook=object_pairs)
except (UnicodeDecodeError, json.JSONDecodeError, DuplicateKey, ValueError):
    fail("profile_payload_invalid")
if not isinstance(wrapper, dict) or set(wrapper) != WRAPPER_FIELDS:
    fail("profile_payload_invalid")
schema = wrapper["schema_version"]
revision = wrapper["revision"]
profile = wrapper["profile"]
if type(schema) is not int or schema != 1:
    fail("profile_payload_invalid")
if type(revision) is not int or revision < 1 or revision > 9223372036854775807:
    fail("profile_revision_invalid")
if not isinstance(profile, dict) or set(profile) != PROFILE_FIELDS:
    fail("profile_payload_invalid")
for field in ("allowDeployRead", "allowDeployActions", "allowDeployDomainRead", "allowDeployDomainActions"):
    if type(profile[field]) is not bool:
        fail("profile_payload_invalid")
for field in ("deployPlansFile", "deployAcmeWebroot"):
    if not isinstance(profile[field], str):
        fail("profile_payload_invalid")
    validate_path(profile[field], file=field == "deployPlansFile")
reject_protected_write_path(profile["deployAcmeWebroot"], profile["deployPlansFile"])
roots = profile["deployWriteRoots"]
if not isinstance(roots, list) or len(roots) > MAX_ROOTS:
    fail("profile_payload_invalid")
normalized_roots = []
for root in roots:
    if not isinstance(root, str) or root == "":
        fail("profile_payload_invalid")
    validate_path(root)
    reject_protected_write_path(root, profile["deployPlansFile"])
    normalized_roots.append(root)
if len(set(normalized_roots)) != len(normalized_roots):
    fail("profile_payload_invalid")
if profile["allowDeployActions"] and not profile["allowDeployRead"]:
    fail("profile_payload_invalid")
if profile["allowDeployDomainRead"] and not profile["allowDeployRead"]:
    fail("profile_payload_invalid")
if profile["allowDeployDomainActions"] and not profile["allowDeployDomainRead"]:
    fail("profile_payload_invalid")
profile["deployWriteRoots"] = sorted(normalized_roots)
canonical_profile = {
    field: profile[field]
    for field in (
        "allowDeployRead",
        "allowDeployActions",
        "allowDeployDomainRead",
        "allowDeployDomainActions",
        "deployPlansFile",
        "deployAcmeWebroot",
        "deployWriteRoots",
    )
}
canonical = json.dumps(
    {"schema_version": 1, "revision": revision, "profile": canonical_profile},
    ensure_ascii=False,
    separators=(",", ":"),
).encode("utf-8")
if len(canonical) > MAX_DOCUMENT_BYTES:
    fail("profile_payload_too_large")
print("revision=" + str(revision))
for field in ("allowDeployRead", "allowDeployActions", "allowDeployDomainRead", "allowDeployDomainActions"):
    print(field + "=" + ("1" if profile[field] else "0"))
print("deployPlansFile=" + profile["deployPlansFile"])
print("deployAcmeWebroot=" + profile["deployAcmeWebroot"])
for root in profile["deployWriteRoots"]:
    print("root=" + root)
print("canonical_b64=" + base64.b64encode(canonical).decode("ascii"))
PY
  ); then
    :
  else
    profile_parse_error=$(printf '%s\n' "$profile_metadata" | sed -n 's/^error=//p' | tail -n 1)
    [ -n "$profile_parse_error" ] || profile_parse_error="$PROFILE_ERROR_PAYLOAD"
    return 1
  fi
  profile_revision=$(printf '%s\n' "$profile_metadata" | sed -n 's/^revision=//p' | tail -n 1)
  profile_allow_deploy_read=$(printf '%s\n' "$profile_metadata" | sed -n 's/^allowDeployRead=//p' | tail -n 1)
  profile_allow_deploy_actions=$(printf '%s\n' "$profile_metadata" | sed -n 's/^allowDeployActions=//p' | tail -n 1)
  profile_allow_deploy_domain_read=$(printf '%s\n' "$profile_metadata" | sed -n 's/^allowDeployDomainRead=//p' | tail -n 1)
  profile_allow_deploy_domain_actions=$(printf '%s\n' "$profile_metadata" | sed -n 's/^allowDeployDomainActions=//p' | tail -n 1)
  profile_deploy_plans=$(printf '%s\n' "$profile_metadata" | sed -n 's/^deployPlansFile=//p' | tail -n 1)
  profile_deploy_acme=$(printf '%s\n' "$profile_metadata" | sed -n 's/^deployAcmeWebroot=//p' | tail -n 1)
  profile_canonical_b64=$(printf '%s\n' "$profile_metadata" | sed -n 's/^canonical_b64=//p' | tail -n 1)
  case "$profile_revision:$profile_allow_deploy_read:$profile_allow_deploy_actions:$profile_allow_deploy_domain_read:$profile_allow_deploy_domain_actions:$profile_canonical_b64" in
    ''|*:*:*:*:*:) profile_parse_error="$PROFILE_ERROR_PAYLOAD"; return 1 ;;
  esac
  profile_roots=$(printf '%s\n' "$profile_metadata" | sed -n 's/^root=//p')
  profile_normalized_tmp=$(mktemp "$PROFILE_DIR/.hserver-profile-normalized.XXXXXX") || {
    profile_parse_error="$PROFILE_ERROR_STORE"
    return 1
  }
  if ! printf '%s' "$profile_canonical_b64" | base64 -d >"$profile_normalized_tmp" 2>/dev/null; then
    rm -f -- "$profile_normalized_tmp"
    profile_parse_error="$PROFILE_ERROR_PAYLOAD"
    return 1
  fi
  chmod 0600 "$profile_normalized_tmp"
  chown root:root "$profile_normalized_tmp" 2>/dev/null || {
    rm -f -- "$profile_normalized_tmp"
    profile_parse_error="$PROFILE_ERROR_STORE"
    return 1
  }
  return 0
}

profile_validate_existing_wrapper() {
  profile_existing_file=$1
  profile_saved_next_file=$PROFILE_NEXT_FILE
  PROFILE_NEXT_FILE=$profile_existing_file
  if profile_parse_candidate; then
    profile_existing_status=0
  else
    profile_existing_status=$?
  fi
  PROFILE_NEXT_FILE=$profile_saved_next_file
  if [ "$profile_existing_status" -ne 0 ]; then
    rm -f -- "${profile_normalized_tmp:-}"
    return 1
  fi
  rm -f -- "$profile_normalized_tmp"
  profile_normalized_tmp=
  return 0
}

profile_add_rw_path() {
  profile_rw_value=$1
  case "$profile_rw_value" in
    ''|/|*[!ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._/+:-]*|*//*|*/.|*/..|*/./*|*/../*|*/) return 1 ;;
  esac
  printf '%s\n' "$profile_rw_value" >>"$profile_rw_unsorted_file"
}

profile_collect_rw_paths() {
  profile_rw_unsorted_file=$(mktemp "$PROFILE_DIR/.hserver-profile-rw.XXXXXX") || return 1
  chmod 0600 "$profile_rw_unsorted_file"
  chown root:root "$profile_rw_unsorted_file" 2>/dev/null || return 1
  profile_add_rw_path "$PROFILE_DIR" || return 1
  if [ "$profile_allow_deploy_actions" = 1 ]; then
    while IFS= read -r profile_root; do
      [ -n "$profile_root" ] || continue
      profile_add_rw_path "$profile_root" || return 1
    done <<EOF
$profile_roots
EOF
  fi
  if [ "$profile_allow_deploy_domain_actions" = 1 ]; then
    [ -z "$profile_deploy_acme" ] || profile_add_rw_path "$profile_deploy_acme" || return 1
    profile_nginx_available=$(config_path_or_default HSERVER_AGENT_NGINX_SITES_AVAILABLE /etc/nginx/sites-available) || return 1
    profile_nginx_enabled=$(config_path_or_default HSERVER_AGENT_NGINX_SITES_ENABLED /etc/nginx/sites-enabled) || return 1
    profile_certbot_config=$(config_path_or_default HSERVER_AGENT_CERTBOT_CONFIG_DIR /etc/letsencrypt) || return 1
    profile_certbot_work=$(config_path_or_default HSERVER_AGENT_CERTBOT_WORK_DIR /var/lib/letsencrypt) || return 1
    profile_certbot_logs=$(config_path_or_default HSERVER_AGENT_CERTBOT_LOGS_DIR /var/log/letsencrypt) || return 1
    profile_add_rw_path "$profile_nginx_available" || return 1
    profile_add_rw_path "$profile_nginx_enabled" || return 1
    profile_add_rw_path "$profile_certbot_config" || return 1
    profile_add_rw_path "$profile_certbot_work" || return 1
    profile_add_rw_path "$profile_certbot_logs" || return 1
    profile_add_rw_path "$(dirname "$profile_nginx_available")" || return 1
  fi
  profile_rw_file=$(mktemp "$PROFILE_DIR/.hserver-profile-rw-sorted.XXXXXX") || return 1
  if ! sort -u "$profile_rw_unsorted_file" >"$profile_rw_file"; then
    return 1
  fi
  chmod 0600 "$profile_rw_file"
  chown root:root "$profile_rw_file" 2>/dev/null || return 1
  return 0
}

profile_validate_service() {
  [ -f "$SERVICE_FILE" ] || return 1
  [ ! -L "$SERVICE_FILE" ] || return 1
  grep -Eq '^\[Service\][[:space:]]*$' "$SERVICE_FILE" || return 1
  grep -Eq '^\[Install\][[:space:]]*$' "$SERVICE_FILE" || return 1
  grep -Eq '^ProtectSystem=strict[[:space:]]*$' "$SERVICE_FILE" || return 1
  grep -Eq '^NoNewPrivileges=yes[[:space:]]*$' "$SERVICE_FILE" || return 1
  return 0
}

profile_build_service_candidate() {
  profile_service_candidate=$(mktemp "$(dirname "$SERVICE_FILE")/.hserver-agent-profile.XXXXXX") || return 1
  if ! awk -v path_file="$profile_rw_file" '
    function emit_paths(line) {
      while ((getline line < path_file) > 0) {
        if (line != "") print "ReadWritePaths=" line
      }
      close(path_file)
    }
    /^ReadWritePaths=/ { next }
    /^\[Install\][[:space:]]*$/ && !inserted {
      emit_paths()
      inserted = 1
    }
    { print }
    END { if (!inserted) exit 2 }
  ' "$SERVICE_FILE" >"$profile_service_candidate"; then
    rm -f -- "$profile_service_candidate"
    profile_service_candidate=
    return 1
  fi
  chmod 0644 "$profile_service_candidate"
  chown root:root "$profile_service_candidate" 2>/dev/null || {
    rm -f -- "$profile_service_candidate"
    profile_service_candidate=
    return 1
  }
  return 0
}

profile_restore_service_atomic() {
  profile_service_source=$1
  profile_service_destination=$2
  profile_service_destination_dir=$(dirname "$profile_service_destination")
  profile_service_mode=$(stat -c '%a' "$profile_service_source" 2>/dev/null || true)
  case "$profile_service_mode" in
    0|[0-7][0-7][0-7]) ;;
    *) return 1 ;;
  esac
  profile_service_tmp=$(mktemp "$profile_service_destination_dir/.hserver-agent-profile-restore.XXXXXX") || return 1
  if ! cp "$profile_service_source" "$profile_service_tmp"; then
    rm -f -- "$profile_service_tmp"
    return 1
  fi
  chmod "$profile_service_mode" "$profile_service_tmp" || {
    rm -f -- "$profile_service_tmp"
    return 1
  }
  chown root:root "$profile_service_tmp" 2>/dev/null || {
    rm -f -- "$profile_service_tmp"
    return 1
  }
  if [ -L "$profile_service_destination" ]; then
    rm -f -- "$profile_service_tmp"
    return 1
  fi
  if ! mv -f -- "$profile_service_tmp" "$profile_service_destination"; then
    rm -f -- "$profile_service_tmp"
    return 1
  fi
  return 0
}

# Re-apply an already active profile after the ordinary upgrade renderer has
# rebuilt the unit from the local environment file.  This is deliberately an
# overlay of ReadWritePaths only: EnvironmentFile, ReadOnlyPaths (including
# the configured token), and every other sandbox directive remain the upgrade
# renderer's exact output.  A valid active profile must survive a binary
# upgrade without requiring a second hub task.
profile_overlay_active_service() {
  [ -f "$PROFILE_ACTIVE_FILE" ] || return 0
  [ ! -L "$PROFILE_ACTIVE_FILE" ] || return 1
  # An active profile must not silently carry the legacy terminal relaxation
  # through an upgrade. Profile application is only valid for the strict
  # service sandbox promised by this lifecycle contract.
  profile_validate_service || return 1
  profile_overlay_saved_next_file=$PROFILE_NEXT_FILE
  PROFILE_NEXT_FILE=$PROFILE_ACTIVE_FILE
  if profile_parse_candidate; then
    profile_overlay_status=0
  else
    profile_overlay_status=$?
  fi
  PROFILE_NEXT_FILE=$profile_overlay_saved_next_file
  [ "$profile_overlay_status" -eq 0 ] || {
    rm -f -- "${profile_normalized_tmp:-}"
    profile_normalized_tmp=
    return 1
  }
  if ! profile_collect_rw_paths || ! profile_build_service_candidate; then
    rm -f -- "${profile_normalized_tmp:-}" "${profile_rw_unsorted_file:-}" "${profile_rw_file:-}" "${profile_service_candidate:-}"
    profile_normalized_tmp=
    profile_rw_unsorted_file=
    profile_rw_file=
    profile_service_candidate=
    return 1
  fi
  if ! mv -f -- "$profile_service_candidate" "$SERVICE_FILE"; then
    rm -f -- "${profile_normalized_tmp:-}" "${profile_rw_unsorted_file:-}" "${profile_rw_file:-}" "$profile_service_candidate"
    profile_normalized_tmp=
    profile_rw_unsorted_file=
    profile_rw_file=
    profile_service_candidate=
    return 1
  fi
  rm -f -- "$profile_normalized_tmp" "$profile_rw_unsorted_file" "$profile_rw_file"
  profile_normalized_tmp=
  profile_rw_unsorted_file=
  profile_rw_file=
  profile_service_candidate=
  return 0
}

profile_snapshot_transaction() {
  profile_snapshot_service=0
  profile_snapshot_candidate=0
  profile_snapshot_active=0
  profile_snapshot_previous=0
  profile_snapshot_state=0
  profile_transaction_dir=$(mktemp -d "$PROFILE_DIR/.hserver-profile-apply.XXXXXX") || return 1
  chmod 0700 "$profile_transaction_dir"
  chown root:root "$profile_transaction_dir" 2>/dev/null || return 1
  if profile_validate_service; then
    cp -p "$SERVICE_FILE" "$profile_transaction_dir/hserver-agent.service" || return 1
    profile_snapshot_service=1
  else
    return 1
  fi
  for profile_name in candidate.json active.json previous.json state.json; do
    profile_source=$PROFILE_DIR/$profile_name
    if [ -e "$profile_source" ]; then
      profile_regular_file "$profile_source" || return 1
      cp -p "$profile_source" "$profile_transaction_dir/$profile_name" || return 1
      case "$profile_name" in
        candidate.json) profile_snapshot_candidate=1 ;;
        active.json) profile_snapshot_active=1 ;;
        previous.json) profile_snapshot_previous=1 ;;
        state.json) profile_snapshot_state=1 ;;
      esac
    fi
  done
  profile_was_active=0
  profile_was_enabled=0
  service_is_active && profile_was_active=1
  service_is_enabled && profile_was_enabled=1
  return 0
}

profile_restore_transaction() {
  profile_restore_ok=0
  if [ "$profile_snapshot_service" = 1 ]; then
    profile_restore_service_atomic "$profile_transaction_dir/hserver-agent.service" "$SERVICE_FILE" || profile_restore_ok=1
  fi
  for profile_name in candidate.json active.json previous.json state.json; do
    profile_present=0
    case "$profile_name" in
      candidate.json) profile_present=$profile_snapshot_candidate ;;
      active.json) profile_present=$profile_snapshot_active ;;
      previous.json) profile_present=$profile_snapshot_previous ;;
      state.json) profile_present=$profile_snapshot_state ;;
    esac
    profile_destination=$PROFILE_DIR/$profile_name
    if [ "$profile_present" = 1 ]; then
      profile_write_atomic_from_file "$profile_transaction_dir/$profile_name" "$profile_destination" || profile_restore_ok=1
    elif [ -e "$profile_destination" ] || [ -L "$profile_destination" ]; then
      rm -f -- "$profile_destination" || profile_restore_ok=1
    fi
  done
  return "$profile_restore_ok"
}

profile_active_checks() {
  profile_elapsed=0
  profile_stable=0
  while [ "$profile_elapsed" -lt "$HEALTH_TIMEOUT" ]; do
    if service_is_active; then
      profile_stable=$((profile_stable + 1))
      [ "$profile_stable" -ge 2 ] && return 0
    else
      profile_stable=0
    fi
    if [ "$SKIP_HEALTHCHECK" = 1 ]; then
      # The test/staged mode still performs two independent active checks, but
      # does not add a wall-clock delay between them.
      profile_elapsed=$((profile_elapsed + 1))
      continue
    fi
    sleep 1
    profile_elapsed=$((profile_elapsed + 1))
  done
  return 1
}

profile_apply_fail() {
  profile_failure_code=$1
  profile_failure_revision=${profile_revision:-}
  if [ -z "$profile_failure_revision" ]; then
    # The runtime stages a pending state before invoking this fixed helper.
    # Preserve that revision even when the candidate itself is malformed, so
    # the next heartbeat reports the failed requested revision rather than an
    # unrelated sentinel value.
    profile_failure_revision=$(sed -n 's/.*"revision":\([0-9][0-9]*\).*/\1/p' "$PROFILE_STATE_FILE" 2>/dev/null | tail -n 1 || true)
  fi
  case "$profile_failure_revision" in
    ''|0|*[!0-9]*) profile_failure_revision=1 ;;
  esac
  [ -n "${profile_transaction_dir:-}" ] && profile_restore_transaction >/dev/null 2>&1 || true
  if [ -n "${profile_transaction_dir:-}" ]; then
    "$SYSTEMCTL" daemon-reload >/dev/null 2>&1 || true
    if [ "${profile_was_active:-0}" = 1 ]; then
      "$SYSTEMCTL" restart "$SERVICE_NAME" >/dev/null 2>&1 || true
    else
      "$SYSTEMCTL" stop "$SERVICE_NAME" >/dev/null 2>&1 || true
    fi
  fi
  write_state_status=0
  ensure_profile_directory >/dev/null 2>&1 || write_state_status=1
  [ "$write_state_status" -ne 0 ] || profile_write_state failed "$profile_failure_revision" "$profile_failure_code" >/dev/null 2>&1 || true
  rm -f -- "${profile_normalized_tmp:-}" "${profile_rw_unsorted_file:-}" "${profile_rw_file:-}" "${profile_service_candidate:-}"
  [ -n "${profile_transaction_dir:-}" ] && rm -rf -- "$profile_transaction_dir"
  printf '%s: profile apply failed: %s\n' "$PROGRAM" "$profile_failure_code" >&2
  exit 1
}

apply_profile() {
  validate_config "$CONFIG_FILE" >/dev/null 2>&1 || profile_apply_fail "$PROFILE_ERROR_STORE"
  ensure_profile_directory || profile_apply_fail "$PROFILE_ERROR_STORE"
  profile_parse_candidate || profile_apply_fail "$profile_parse_error"
  profile_snapshot_transaction || profile_apply_fail "$PROFILE_ERROR_CORRUPT"
  profile_collect_rw_paths || profile_apply_fail "$PROFILE_ERROR_PAYLOAD"
  profile_build_service_candidate || profile_apply_fail "$PROFILE_ERROR_STORE"
  profile_write_state pending_restart "$profile_revision" || profile_apply_fail "$PROFILE_ERROR_STORE"
  if ! mv -f -- "$profile_service_candidate" "$SERVICE_FILE"; then
    profile_apply_fail "$PROFILE_ERROR_STORE"
  fi
  profile_service_candidate=
  if ! "$SYSTEMCTL" daemon-reload >/dev/null 2>&1; then
    profile_apply_fail "$PROFILE_ERROR_SCHEDULE"
  fi
  if ! "$SYSTEMCTL" restart "$SERVICE_NAME" >/dev/null 2>&1; then
    profile_apply_fail "$PROFILE_ERROR_SCHEDULE"
  fi
  profile_active_checks || profile_apply_fail "$PROFILE_ERROR_SCHEDULE"
  if ! profile_write_atomic_from_file "$profile_normalized_tmp" "$PROFILE_ACTIVE_FILE"; then
    profile_apply_fail "$PROFILE_ERROR_STORE"
  fi
  profile_normalized_tmp=
  if [ "$profile_snapshot_active" = 1 ]; then
    profile_write_atomic_from_file "$profile_transaction_dir/active.json" "$PROFILE_PREVIOUS_FILE" || profile_apply_fail "$PROFILE_ERROR_STORE"
  fi
  if ! profile_write_state active "$profile_revision"; then
    profile_apply_fail "$PROFILE_ERROR_STORE"
  fi
  rm -f -- "$PROFILE_NEXT_FILE" "$profile_rw_unsorted_file" "$profile_rw_file"
  rm -rf -- "$profile_transaction_dir"
  printf '%s\n' 'Profile apply completed successfully.'
}

write_service_unit() {
  resolve_token_destination "$CONFIG_FILE"
  no_new_privileges=yes
  private_tmp=yes
  protect_home=yes
  protect_system=strict
  protect_kernel_tunables=yes
  protect_kernel_modules=yes
  protect_kernel_logs=yes
  protect_control_groups=yes
  restrict_suid_sgid=yes
  restrict_realtime=yes
  lock_personality=yes
  memory_deny_write_execute=yes
  read_write_paths=
  # Profile transitions are staged by the agent under this one private
  # directory.  No environment or token file is made writable by the unit.
  append_read_write_path "$PROFILE_DIR"
  if grep -Eiq '^HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE=true[[:space:]]*$' "$CONFIG_FILE" || grep -Eiq '^HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=true[[:space:]]*$' "$CONFIG_FILE"; then
    nginx_available=$(config_path_or_default HSERVER_AGENT_NGINX_SITES_AVAILABLE /etc/nginx/sites-available)
    append_read_write_path "$nginx_available"
  fi
  if grep -Eiq '^HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE=true[[:space:]]*$' "$CONFIG_FILE" || grep -Eiq '^HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS=true[[:space:]]*$' "$CONFIG_FILE" || grep -Eiq '^HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=true[[:space:]]*$' "$CONFIG_FILE"; then
    nginx_enabled=$(config_path_or_default HSERVER_AGENT_NGINX_SITES_ENABLED /etc/nginx/sites-enabled)
    append_read_write_path "$nginx_enabled"
  fi
  if grep -Eiq '^HSERVER_AGENT_ALLOW_SSL_ACTIONS=true[[:space:]]*$' "$CONFIG_FILE" || grep -Eiq '^HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=true[[:space:]]*$' "$CONFIG_FILE"; then
    certbot_config=$(config_path_or_default HSERVER_AGENT_CERTBOT_CONFIG_DIR /etc/letsencrypt)
    certbot_work=$(config_path_or_default HSERVER_AGENT_CERTBOT_WORK_DIR /var/lib/letsencrypt)
    certbot_logs=$(config_path_or_default HSERVER_AGENT_CERTBOT_LOGS_DIR /var/log/letsencrypt)
    nginx_available=$(config_path_or_default HSERVER_AGENT_NGINX_SITES_AVAILABLE /etc/nginx/sites-available)
    append_read_write_path "$certbot_config"
    append_read_write_path "$certbot_work"
    append_read_write_path "$certbot_logs"
    append_read_write_path "$(dirname "$nginx_available")"
  fi
  if grep -Eiq '^HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=true[[:space:]]*$' "$CONFIG_FILE"; then
    deploy_acme=$(config_path_or_default HSERVER_AGENT_DEPLOY_ACME_WEBROOT /var/www/hserver-acme)
    mkdir -p "$(root_path "$deploy_acme")"
    chmod 0755 "$(root_path "$deploy_acme")"
    append_read_write_path "$deploy_acme"
  fi
  if grep -Eiq '^HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE=true[[:space:]]*$' "$CONFIG_FILE"; then
    php_config_root=$(config_path_or_default HSERVER_AGENT_PHP_CONFIG_ROOT /etc/php)
    append_read_write_path "$php_config_root"
  fi
  if grep -Eiq '^HSERVER_AGENT_ALLOW_PM2_READ=true[[:space:]]*$' "$CONFIG_FILE" || grep -Eq '^HSERVER_AGENT_ALLOWED_PM2_ACTIONS=.+$' "$CONFIG_FILE"; then
    protect_home=read-only
  fi
  if grep -Eq '^HSERVER_AGENT_ALLOWED_PM2_ACTIONS=.+$' "$CONFIG_FILE"; then
    pm2_home=$(config_path_or_default HSERVER_AGENT_PM2_HOME '')
    append_read_write_path "$pm2_home"
  fi
  if grep -Eiq '^HSERVER_AGENT_ALLOW_CRON_WRITE=true[[:space:]]*$' "$CONFIG_FILE"; then
    cron_state=$(config_path_or_default HSERVER_AGENT_CRON_STATE_PATH /etc/hserver/cron-jobs.json)
    cron_file=$(config_path_or_default HSERVER_AGENT_CRON_FILE_PATH /etc/cron.d/hserver-managed)
    append_read_write_path "$(dirname "$cron_state")"
    append_read_write_path "$(dirname "$cron_file")"
  fi
	if grep -Eiq '^HSERVER_AGENT_ALLOW_FIREWALL_WRITE=true[[:space:]]*$' "$CONFIG_FILE"; then
		firewall_state=$(config_path_or_default HSERVER_AGENT_FIREWALL_PERSISTENCE_PATH /etc/iptables)
		append_read_write_path "$firewall_state"
	fi
  if configured_paths_need_home_access; then
    protect_home=read-only
  fi
  append_config_read_write_paths HSERVER_AGENT_FILE_WRITE_ROOTS
  append_config_read_write_paths HSERVER_AGENT_DEPLOY_WRITE_ROOTS
  if grep -Eiq '^HSERVER_AGENT_ALLOW_UPDATE_ACTIONS=true[[:space:]]*$' "$CONFIG_FILE"; then
    append_read_write_path "$STATE_DIR"
  fi
  if grep -Eiq '^HSERVER_AGENT_ALLOW_TERMINAL=true[[:space:]]*$' "$CONFIG_FILE" || grep -Eiq '^HSERVER_AGENT_ALLOW_CRON_RUN=true[[:space:]]*$' "$CONFIG_FILE"; then
    # A writable root terminal cannot function inside the read-only service
    # sandbox. This relaxation is limited to explicit terminal or manual-cron
    # execution opt-ins.
    no_new_privileges=no
    private_tmp=no
    protect_home=no
    protect_system=no
    protect_kernel_tunables=no
    protect_kernel_modules=no
    protect_kernel_logs=no
    protect_control_groups=no
    restrict_suid_sgid=no
    restrict_realtime=no
    lock_personality=no
    memory_deny_write_execute=no
  fi
  unit_tmp=$(mktemp)
  cat >"$unit_tmp" <<EOF
[Unit]
Description=Heyserver Managed Node Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
ExecStart=$BINARY_PATH
EnvironmentFile=$CONFIG_FILE
Restart=on-failure
RestartSec=5s
TimeoutStopSec=20s
UMask=0077
NoNewPrivileges=$no_new_privileges
PrivateTmp=$private_tmp
ProtectHome=$protect_home
ProtectSystem=$protect_system
ProtectKernelTunables=$protect_kernel_tunables
ProtectKernelModules=$protect_kernel_modules
ProtectKernelLogs=$protect_kernel_logs
ProtectControlGroups=$protect_control_groups
RestrictSUIDSGID=$restrict_suid_sgid
RestrictRealtime=$restrict_realtime
LockPersonality=$lock_personality
MemoryDenyWriteExecute=$memory_deny_write_execute
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK
SystemCallArchitectures=native
ReadOnlyPaths=$TOKEN_FILE
$read_write_paths

[Install]
WantedBy=multi-user.target
EOF
  install -D -m 0644 "$unit_tmp" "$SERVICE_FILE"
  rm -f "$unit_tmp"
}

service_is_active() {
  "$SYSTEMCTL" is-active --quiet "$SERVICE_NAME" >/dev/null 2>&1
}

service_is_enabled() {
  "$SYSTEMCTL" is-enabled --quiet "$SERVICE_NAME" >/dev/null 2>&1
}

snapshot_flag() {
  snapshot=$1
  key=$2
  fallback=$3
  value=$(sed -n "s/^${key}=//p" "$snapshot/manifest.env" 2>/dev/null | tail -n 1 || true)
  case "$value" in
    0|1) printf '%s\n' "$value" ;;
    *) printf '%s\n' "$fallback" ;;
  esac
}

wait_active() {
  [ "$SKIP_HEALTHCHECK" = 1 ] && return 0
  elapsed=0
  stable_checks=0
  while [ "$elapsed" -lt "$HEALTH_TIMEOUT" ]; do
    if service_is_active; then
      stable_checks=$((stable_checks + 1))
      [ "$stable_checks" -ge 2 ] && return 0
    else
      stable_checks=0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  return 1
}

snapshot_binary() {
  label=$1
  stamp=$(date -u +%Y%m%dT%H%M%SZ)
  snapshot=$RELEASES_DIR/${stamp}-${label}
  suffix=0
  while [ -e "$snapshot" ]; do
    suffix=$((suffix + 1))
    snapshot=$RELEASES_DIR/${stamp}-${label}-${suffix}
  done
  install -d -m 0700 "$snapshot"
  was_active=0
  was_enabled=0
  service_is_active && was_active=1
  service_is_enabled && was_enabled=1
  {
    printf 'SERVICE_WAS_ACTIVE=%s\n' "$was_active"
    printf 'SERVICE_WAS_ENABLED=%s\n' "$was_enabled"
  } >"$snapshot/manifest.env"
  [ -f "$BINARY_PATH" ] && cp -p "$BINARY_PATH" "$snapshot/$BINARY_NAME"
  [ -f "$SERVICE_FILE" ] && cp -p "$SERVICE_FILE" "$snapshot/hserver-agent.service"
  # Keep the profile transaction state with the binary/unit recovery point,
  # but never copy the environment or token files into a release snapshot.
  if [ -d "$PROFILE_DIR" ] && [ ! -L "$PROFILE_DIR" ]; then
    install -d -m 0700 "$snapshot/$PROFILE_DIRECTORY_NAME"
    for profile_name in candidate.json active.json previous.json state.json; do
      profile_source=$PROFILE_DIR/$profile_name
      if [ -e "$profile_source" ]; then
        profile_regular_file "$profile_source" \
          || die "managed profile snapshot contains an invalid file"
        cp -p "$profile_source" "$snapshot/$PROFILE_DIRECTORY_NAME/$profile_name"
      fi
    done
    printf '%s\n' 1 >"$snapshot/profile-present"
    chmod 0600 "$snapshot/profile-present"
  fi
  if [ "$label" = pre-upgrade ]; then
    printf '%s\n' "$snapshot" >"$RELEASES_DIR/latest-pre-upgrade"
  fi
  printf '%s\n' "$snapshot"
}

prune_pre_upgrade_snapshots() {
  retention=$1
  marker=$RELEASES_DIR/latest-pre-upgrade
  marked_snapshot=
  if [ -f "$marker" ]; then
    marked_snapshot=$(cat "$marker" 2>/dev/null || true)
    case "$marked_snapshot" in
      "$RELEASES_DIR"/*)
        [ -d "$marked_snapshot" ] || marked_snapshot=
        ;;
      *) marked_snapshot= ;;
    esac
  fi

  # Keep the marker target first, then the newest remaining snapshots. This
  # keeps the count bounded even if an old marker survived an interrupted
  # cleanup, while never deleting the snapshot rollback currently selects.
  if [ -n "$marked_snapshot" ]; then
    kept=1
  else
    kept=0
  fi
  find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d -name '*-pre-upgrade*' 2>/dev/null \
    | sort -r \
    | while IFS= read -r candidate; do
        [ -n "$candidate" ] || continue
        case "$candidate" in
          "$RELEASES_DIR"/*) ;;
          *) continue ;;
        esac
        [ "$candidate" = "$marked_snapshot" ] && continue
        if [ "$kept" -lt "$retention" ]; then
          kept=$((kept + 1))
        else
          rm -rf "$candidate"
        fi
      done
}

restore_snapshot() {
  snapshot=$1
  [ -f "$snapshot/$BINARY_NAME" ] || die "snapshot has no previous agent binary: $snapshot"
  "$SYSTEMCTL" stop "$SERVICE_NAME" >/dev/null 2>&1 || true
  install -D -m 0755 "$snapshot/$BINARY_NAME" "$BINARY_PATH"
  if [ -f "$snapshot/hserver-agent.service" ]; then
    install -D -m 0644 "$snapshot/hserver-agent.service" "$SERVICE_FILE"
  fi
  if [ -d "$snapshot/$PROFILE_DIRECTORY_NAME" ] && [ ! -L "$snapshot/$PROFILE_DIRECTORY_NAME" ]; then
    ensure_profile_directory || die "could not restore managed profile directory"
    for profile_name in candidate.json active.json previous.json state.json; do
      profile_source=$snapshot/$PROFILE_DIRECTORY_NAME/$profile_name
      profile_destination=$PROFILE_DIR/$profile_name
      if [ -f "$profile_source" ] && [ ! -L "$profile_source" ]; then
        profile_regular_file "$profile_source" \
          || die "managed profile recovery snapshot contains an invalid file"
        profile_write_atomic_from_file "$profile_source" "$profile_destination" \
          || die "could not restore managed profile state"
      elif [ -e "$profile_destination" ] || [ -L "$profile_destination" ]; then
        rm -f -- "$profile_destination"
      fi
    done
  fi
  "$SYSTEMCTL" daemon-reload
  was_active=$(snapshot_flag "$snapshot" SERVICE_WAS_ACTIVE 1)
  was_enabled=$(snapshot_flag "$snapshot" SERVICE_WAS_ENABLED "$was_active")
  if [ "$was_enabled" = 1 ]; then
    "$SYSTEMCTL" enable "$SERVICE_NAME"
  else
    "$SYSTEMCTL" disable "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
  if [ "$was_active" = 1 ]; then
    "$SYSTEMCTL" start "$SERVICE_NAME"
  else
    "$SYSTEMCTL" stop "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
}

install_agent() {
  source_binary=$1
  config_source=$2
  token_source=$3
  [ ! -e "$BINARY_PATH" ] || die "Heyserver agent is already installed; use upgrade"
  validate_binary "$source_binary"
  validate_config "$config_source"
  validate_token_file "$token_source"
  run_preflight_doctor

  install -d -m 0700 "$(dirname "$CONFIG_FILE")" "$STATE_DIR" "$RELEASES_DIR" "$PROFILE_DIR"
  chmod 0700 "$PROFILE_DIR"
  chown root:root "$PROFILE_DIR" 2>/dev/null || die "could not prepare managed profile directory"
  install -D -m 0755 "$source_binary" "$BINARY_PATH"
  install -m 0600 "$config_source" "$CONFIG_FILE"
  copy_token_atomic "$token_source"
  write_service_unit
  "$SYSTEMCTL" daemon-reload
  "$SYSTEMCTL" enable --now "$SERVICE_NAME"
  wait_active || die "installation finished, but hserver-agent is not active; inspect: journalctl -u hserver-agent"
  persist_installer
  printf '%s\n' "Agent installation completed successfully."
}

upgrade_agent() {
  source_binary=$1
  [ -f "$BINARY_PATH" ] || die "Heyserver agent is not installed; use install first"
  validate_config "$CONFIG_FILE"
  snapshot_retention=$(validate_snapshot_retention "$CONFIG_FILE")
  validate_binary "$source_binary"
  snapshot=$(snapshot_binary pre-upgrade)
  prune_pre_upgrade_snapshots "$snapshot_retention"
  "$SYSTEMCTL" stop "$SERVICE_NAME"
  install -D -m 0755 "$source_binary" "$BINARY_PATH"
  write_service_unit
  if ! profile_overlay_active_service; then
    printf '%s\n' "Active profile could not be applied to the upgraded service; restoring the previous release." >&2
    restore_snapshot "$snapshot"
    if [ "$(snapshot_flag "$snapshot" SERVICE_WAS_ACTIVE 1)" = 1 ]; then
      wait_active || die "automatic rollback completed, but the restored agent is still inactive"
    fi
    die "agent upgrade failed and was rolled back"
  fi
  "$SYSTEMCTL" daemon-reload
  "$SYSTEMCTL" enable --now "$SERVICE_NAME"
  if wait_active; then
    persist_installer
    printf 'Agent upgrade completed successfully. Recovery snapshot: %s\n' "$snapshot"
    return 0
  fi
  printf '%s\n' "New agent failed to become active; restoring the previous binary." >&2
  restore_snapshot "$snapshot"
  if [ "$(snapshot_flag "$snapshot" SERVICE_WAS_ACTIVE 1)" = 1 ]; then
    wait_active || die "automatic rollback completed, but the restored agent is still inactive"
  fi
  die "agent upgrade failed and was rolled back"
}

latest_snapshot() {
  marker=$RELEASES_DIR/latest-pre-upgrade
  if [ -f "$marker" ]; then
    marked_snapshot=$(cat "$marker")
    case "$marked_snapshot" in
      "$RELEASES_DIR"/*)
        if [ -d "$marked_snapshot" ]; then
          printf '%s\n' "$marked_snapshot"
          return 0
        fi
        ;;
    esac
  fi
  find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d -name '*-pre-upgrade*' 2>/dev/null \
    | sort | tail -n 1
}

rollback_agent() {
  validate_config "$CONFIG_FILE"
  snapshot_retention=$(validate_snapshot_retention "$CONFIG_FILE")
  rollback_target=$(latest_snapshot)
  [ -n "$rollback_target" ] || die "no agent upgrade recovery snapshot is available"
  snapshot_binary pre-rollback >/dev/null
  prune_pre_upgrade_snapshots "$snapshot_retention"
  restore_snapshot "$rollback_target"
  if [ "$(snapshot_flag "$rollback_target" SERVICE_WAS_ACTIVE 1)" = 1 ]; then
    wait_active || die "rollback restored $rollback_target, but hserver-agent is still inactive"
  fi
  printf 'Agent rollback completed from snapshot: %s\n' "$rollback_target"
}

uninstall_agent() {
  purge_config=$1
  if [ -f "$CONFIG_FILE" ]; then
    resolve_token_destination "$CONFIG_FILE"
  fi
  "$SYSTEMCTL" disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
  rm -f "$SERVICE_FILE" "$BINARY_PATH" "$LIFECYCLE_PATH"
  "$SYSTEMCTL" daemon-reload
  if [ "$purge_config" = 1 ]; then
    assert_safe_path "$CONFIG_FILE"
    assert_safe_path "$TOKEN_FILE"
    rm -f "$CONFIG_FILE" "$TOKEN_FILE"
  fi
  printf '%s\n' "Heyserver agent binary and service were removed."
  [ "$purge_config" = 1 ] || printf 'Configuration preserved: %s and %s\n' "$CONFIG_FILE" "$TOKEN_FILE"
}

require_root
require_command install

command_name=${1:-}
[ -n "$command_name" ] || { usage; exit 1; }
shift

binary_source=
config_source=
token_source=
purge_config=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --binary) [ "$#" -ge 2 ] || die "--binary requires a path"; binary_source=$2; shift 2 ;;
    --config) [ "$#" -ge 2 ] || die "--config requires a path"; config_source=$2; shift 2 ;;
    --token-file) [ "$#" -ge 2 ] || die "--token-file requires a path"; token_source=$2; shift 2 ;;
    --purge-config) purge_config=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

if [ "$command_name" = install ]; then
  resolve_lifecycle_paths "$config_source"
else
  resolve_lifecycle_paths "$CONFIG_FILE"
fi

case "$command_name" in
  install)
    [ -n "$binary_source" ] || binary_source=$(default_binary_source) || die "use --binary PATH"
    [ -n "$config_source" ] || die "install requires --config PATH"
    [ -n "$token_source" ] || die "install requires --token-file PATH"
    install_agent "$binary_source" "$config_source" "$token_source"
    ;;
  upgrade)
    [ -z "$config_source$token_source" ] || die "upgrade preserves configuration; remove --config and --token-file"
    [ -n "$binary_source" ] || binary_source=$(default_binary_source) || die "use --binary PATH"
    upgrade_agent "$binary_source"
    ;;
  rollback)
    [ -z "$binary_source$config_source$token_source" ] || die "rollback does not accept file arguments"
    rollback_agent
    ;;
  apply-profile)
    [ -z "$binary_source$config_source$token_source" ] || die "apply-profile does not accept file arguments"
    [ "$purge_config" = 0 ] || die "apply-profile does not accept --purge-config"
    apply_profile
    ;;
  uninstall)
    [ -z "$binary_source$config_source$token_source" ] || die "uninstall does not accept file arguments"
    uninstall_agent "$purge_config"
    ;;
  *) usage; die "unknown command: $command_name" ;;
esac
