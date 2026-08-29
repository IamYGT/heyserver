#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
mode=${1:-check}

usage() {
  cat <<'EOF'
Usage: dev-setup.sh check|setup

  check  Inspect the required and optional contributor toolchains.
  setup  Run the same checks, then install locked Go and frontend dependencies.

The script never installs operating-system packages or writes an environment
file. Optional database and Docker tools are reported separately from the base
build requirements.
EOF
}

if (($# > 1)); then
  usage >&2
  exit 2
fi
case "$mode" in
  check|setup) ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

required_go=$(awk '$1 == "go" { print $2; exit }' "$repo_root/go.mod")
required_node=24
required_failures=0

ready() {
  printf '[ready] %s\n' "$1"
}

required_failure() {
  printf '[required] %s\n' "$1" >&2
  required_failures=$((required_failures + 1))
}

optional_state() {
  printf '[optional] %s\n' "$1"
}

parse_version() {
  local value=$1
  if [[ "$value" =~ ^([0-9]+)\.([0-9]+)(\.([0-9]+))? ]]; then
    printf '%s %s %s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[4]:-0}"
    return 0
  fi
  return 1
}

version_at_least() {
  local actual=$1 required=$2
  local actual_parts required_parts
  actual_parts=$(parse_version "$actual") || return 1
  required_parts=$(parse_version "$required") || return 1
  local actual_major actual_minor actual_patch required_major required_minor required_patch
  read -r actual_major actual_minor actual_patch <<<"$actual_parts"
  read -r required_major required_minor required_patch <<<"$required_parts"
  (( actual_major > required_major )) && return 0
  (( actual_major < required_major )) && return 1
  (( actual_minor > required_minor )) && return 0
  (( actual_minor < required_minor )) && return 1
  (( actual_patch >= required_patch ))
}

printf 'HServer contributor environment\n\n'

if command -v go >/dev/null 2>&1; then
  go_output=$(go version 2>/dev/null || true)
  if [[ "$go_output" =~ go([0-9]+\.[0-9]+(\.[0-9]+)?) ]]; then
    go_version=${BASH_REMATCH[1]}
    if version_at_least "$go_version" "$required_go"; then
      ready "Go $go_version (requires >= $required_go)"
    else
      required_failure "Go $go_version is too old; install Go $required_go or newer"
    fi
  else
    required_failure "Go is present but its version could not be determined"
  fi
else
  required_failure "Go $required_go or newer is unavailable"
fi

if command -v node >/dev/null 2>&1; then
  node_version=$(node --version 2>/dev/null || true)
  node_version=${node_version#v}
  if [[ "$node_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] && version_at_least "$node_version" "$required_node.0.0"; then
    ready "Node.js $node_version (requires >= $required_node)"
  else
    required_failure "Node.js ${node_version:-unknown} is too old or invalid; install Node.js $required_node or newer"
  fi
else
  required_failure "Node.js $required_node or newer is unavailable"
fi

for command in npm git make python3; do
  if command -v "$command" >/dev/null 2>&1; then
    ready "$command ($(command -v "$command"))"
  else
    required_failure "$command is unavailable"
  fi
done

if command -v cc >/dev/null 2>&1; then
  ready "C compiler ($(command -v cc))"
elif command -v gcc >/dev/null 2>&1; then
  ready "C compiler ($(command -v gcc))"
else
  required_failure "a C compiler is unavailable; CGO-backed panel builds require cc or gcc"
fi

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  ready "Docker Compose v2 (make test-docker and quick evaluation)"
else
  optional_state "Docker Compose v2 unavailable; only Docker evaluation is skipped"
fi

if command -v psql >/dev/null 2>&1 && { command -v postgres >/dev/null 2>&1 || command -v initdb >/dev/null 2>&1; }; then
  ready "PostgreSQL client/server tools (restore drill)"
else
  optional_state "PostgreSQL client/server tools unavailable; PostgreSQL restore drill is skipped"
fi

if { command -v mariadb >/dev/null 2>&1 || command -v mysql >/dev/null 2>&1; } &&
   { command -v mariadbd >/dev/null 2>&1 || command -v mysqld >/dev/null 2>&1; }; then
  ready "MariaDB client/server tools (restore drill)"
else
  optional_state "MariaDB client/server tools unavailable; MariaDB restore drill is skipped"
fi

for optional in sqlite3 golangci-lint; do
  if command -v "$optional" >/dev/null 2>&1; then
    ready "$optional ($(command -v "$optional"))"
  else
    optional_state "$optional unavailable"
  fi
done

printf '\n'
if (( required_failures > 0 )); then
  printf 'Result: not ready (%d required toolchain issue(s))\n' "$required_failures" >&2
  printf 'Install the required tools for your operating system, then run: make dev-check\n' >&2
  exit 1
fi

if [[ "$mode" == check ]]; then
  printf 'Result: ready for dependency setup\n'
  printf 'Next: make dev-setup\n'
  exit 0
fi

printf 'Installing locked Go modules...\n'
(cd "$repo_root" && go mod download)
printf 'Installing locked frontend packages...\n'
(cd "$repo_root/web" && npm ci)

cat <<'EOF'

Result: contributor dependencies are ready
Next checks:
  make test
  make build

Run the local backend and frontend in separate terminals:
  make backend
  make dev-frontend

For the isolated Docker evaluation:
  ./scripts/init-env.sh
  docker compose up --build
EOF
