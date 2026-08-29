#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

for command_name in curl docker openssl python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required for Docker evaluation acceptance" >&2
    exit 1
  }
done
docker compose version >/dev/null

tmp=$(mktemp -d /tmp/hserver-docker-evaluation-XXXXXXXX)
env_file="$tmp/evaluation.env"
project="hserver-eval-${RANDOM}-$$"
started=0

cleanup() {
  if (( started )); then
    HSERVER_PORT="$host_port" docker compose \
      --project-directory "$repo_root" \
      --project-name "$project" \
      --env-file "$env_file" \
      down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

host_port=$(python3 <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)

"$repo_root/scripts/init-env.sh" "$env_file" >/dev/null
compose=(
  docker compose
  --project-directory "$repo_root"
  --project-name "$project"
  --env-file "$env_file"
)

HSERVER_PORT="$host_port" "${compose[@]}" config --quiet
started=1
HSERVER_PORT="$host_port" "${compose[@]}" up --build --detach

wait_for_health() {
  local phase=$1
  for _ in {1..90}; do
    if curl -fsS --max-time 2 "http://127.0.0.1:${host_port}/api/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  HSERVER_PORT="$host_port" "${compose[@]}" ps >&2 || true
  echo "Docker evaluation did not become healthy after $phase" >&2
  return 1
}

wait_for_health "initial startup"

python3 - "$env_file" "$tmp/login.json" <<'PY'
import json
import sys

values = {}
with open(sys.argv[1], encoding="utf-8") as handle:
    for line in handle:
        line = line.rstrip("\n")
        if "=" in line and not line.startswith("#"):
            key, value = line.split("=", 1)
            values[key] = value
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    json.dump({"email": values["HSERVER_ADMIN_EMAIL"], "password": values["HSERVER_ADMIN_PASS"]}, handle)
PY
chmod 0600 "$tmp/login.json"
curl -fsS --max-time 5 \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/login.json" \
  -o "$tmp/login-response.json" \
  "http://127.0.0.1:${host_port}/api/auth/login"
python3 - "$tmp/login-response.json" "$tmp/auth-header.txt" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    token = json.load(handle).get("token")
if not isinstance(token, str) or not token:
    raise SystemExit("Docker evaluation login did not return a token")
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    handle.write("Authorization: Bearer " + token + "\n")
PY
chmod 0600 "$tmp/login-response.json" "$tmp/auth-header.txt"

onboarding_step=5
printf '%s\n' '{"completed":true,"step":'"$onboarding_step"'}' >"$tmp/onboarding.json"
curl -fsS --max-time 5 \
  -H "@$tmp/auth-header.txt" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/onboarding.json" \
  "http://127.0.0.1:${host_port}/api/onboarding" >/dev/null

HSERVER_PORT="$host_port" "${compose[@]}" restart hserver-panel >/dev/null
wait_for_health "container restart"
curl -fsS --max-time 5 \
  -H "@$tmp/auth-header.txt" \
  -o "$tmp/onboarding-after-restart.json" \
  "http://127.0.0.1:${host_port}/api/onboarding"
python3 - "$tmp/onboarding-after-restart.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
if state.get("completed") is not True or state.get("step") != 5:
    raise SystemExit("Docker evaluation did not preserve onboarding state across restart")
PY

printf 'Docker quick-evaluation acceptance: OK (project=%s)\n' "$project"
