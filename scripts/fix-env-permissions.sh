#!/usr/bin/env bash
set -euo pipefail

PROGRAM=fix-env-permissions
MODE=check
VHOSTS_ROOT=${HSERVER_VHOSTS_ROOT-}
READ_GROUP=${HSERVER_ENV_READ_GROUP:-www-data}

usage() {
  cat <<'EOF'
Inspect or repair root-owned application .env files inside one Heyserver vhost root.

Usage:
  fix-env-permissions.sh --check
  sudo fix-env-permissions.sh --apply

Configuration:
  HSERVER_VHOSTS_ROOT      Required absolute installation-owned vhost root
  HSERVER_ENV_READ_GROUP  Runtime group allowed to read repaired files (default: www-data)

--check is read-only and exits 3 when repairable drift is found.
--apply requires root, changes only root-owned .env files, and never restarts services.
EOF
}

die() {
  printf '%s: %s\n' "$PROGRAM" "$*" >&2
  exit 1
}

case "${1:---check}" in
  --check) MODE=check ;;
  --apply) MODE=apply ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 1 ;;
esac
[ "$#" -le 1 ] || { usage >&2; exit 1; }

case "$VHOSTS_ROOT" in
  '') die "HSERVER_VHOSTS_ROOT must be set to an absolute path" ;;
  /*) ;;
  *) die "HSERVER_VHOSTS_ROOT must be an absolute path" ;;
esac
[ "$VHOSTS_ROOT" != "/" ] || die "refusing to scan the filesystem root"
[ -d "$VHOSTS_ROOT" ] || die "vhosts root does not exist: $VHOSTS_ROOT"
command -v getent >/dev/null 2>&1 || die "getent is required"
getent group "$READ_GROUP" >/dev/null 2>&1 || die "group does not exist: $READ_GROUP"

if [ "$MODE" = apply ] && [ "$(id -u)" -ne 0 ]; then
  die "--apply must run as root"
fi

VHOSTS_ROOT=$(readlink -f -- "$VHOSTS_ROOT")
list_file=$(mktemp)
trap 'unlink "$list_file"' EXIT INT TERM
if ! find -P "$VHOSTS_ROOT" -type f -name .env -user root -print0 >"$list_file"; then
  die "could not scan vhosts root: $VHOSTS_ROOT"
fi

DRIFT=0
FIXED=0
SKIPPED=0

while IFS= read -r -d '' envfile; do
  probe=$(dirname -- "$envfile")
  owner=root
  while [ "$probe" != "$VHOSTS_ROOT" ]; do
    owner=$(stat -c '%U' -- "$probe")
    [ "$owner" = root ] || break
    parent=$(dirname -- "$probe")
    [ "$parent" != "$probe" ] || break
    probe=$parent
  done

  if [ "$owner" = root ] || ! id "$owner" >/dev/null 2>&1; then
    printf '[SKIPPED] %s (no non-root application owner found)\n' "$envfile"
    SKIPPED=$((SKIPPED + 1))
    continue
  fi

  DRIFT=$((DRIFT + 1))
  if [ "$MODE" = check ]; then
    printf '[DRIFT] %s should be %s:%s mode 0640\n' "$envfile" "$owner" "$READ_GROUP"
    continue
  fi

  if chown -- "$owner:$READ_GROUP" "$envfile" && chmod -- 0640 "$envfile"; then
    printf '[FIXED] %s -> %s:%s mode 0640\n' "$envfile" "$owner" "$READ_GROUP"
    FIXED=$((FIXED + 1))
  else
    printf '[FAILED] %s\n' "$envfile" >&2
    SKIPPED=$((SKIPPED + 1))
  fi
done <"$list_file"

if [ "$MODE" = check ]; then
  printf 'Check complete: %d repairable, %d skipped\n' "$DRIFT" "$SKIPPED"
  [ "$DRIFT" -eq 0 ] || exit 3
else
  printf 'Repair complete: %d fixed, %d skipped\n' "$FIXED" "$SKIPPED"
  [ "$FIXED" -eq "$DRIFT" ] || exit 1
fi
