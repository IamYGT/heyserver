#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
acceptance="$repo_root/scripts/accept-provider-network-managed-agent.sh"
tmp=$(mktemp -d)
cleanup() {
  find "$tmp" -xdev -depth -delete
}
trap cleanup EXIT INT TERM

fail() {
  echo "$*" >&2
  exit 1
}

mkdir -p "$tmp/bin" "$tmp/state"
token='fixture-provider-network-token-not-a-real-secret'
printf '%s\n' "$token" >"$tmp/admin.token"
chmod 0600 "$tmp/admin.token"

cat >"$tmp/bin/hserverctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ $1 == version ]]; then
  [[ $# == 1 ]]
  printf 'hserverctl v1.0.0-fixture (fixture-commit, 2026-08-28T00:00:00Z)\n'
  exit 0
fi
[[ $# == 9 ]]
[[ $1 == --server && $2 == https://8.8.8.8 ]]
[[ $3 == --token-file && -f $4 ]]
[[ $5 == --timeout && $6 == 10s ]]
[[ $7 == terminal && $8 == --node && $9 == fixture-node ]]
IFS= read -r command
marker=$(printf '%s' "$command" | grep -oE 'hserver-provider-acceptance-[a-f0-9]{24}' | head -n 1)
[[ -n $marker ]]
[[ $command == *'setsid -f'* ]]
[[ $command == *'bytearray('* ]]
printf '%s' "$marker" >"$HSERVER_PROVIDER_FIXTURE_STATE/marker"
printf 'HSERVER_PROVIDER_MARKER_PID:4242\r\n'
if [[ ${HSERVER_PROVIDER_FIXTURE_MODE:-success} == hostname-compatibility ]]; then
  printf 'hserverctl: terminal closed: terminal process ended unexpectedly\r\n' >&2
  exit 1
fi
EOF
chmod +x "$tmp/bin/hserverctl"

cat >"$tmp/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=
write_format=
url=
data_file=
while (( $# > 0 )); do
  case "$1" in
    -o|-w|-H|--max-time|--data-binary)
      [[ $1 == -o ]] && output=$2
      [[ $1 == -w ]] && write_format=$2
      [[ $1 == --data-binary ]] && data_file=${2#@}
      shift 2
      ;;
    -X)
      shift 2
      ;;
    -*)
      shift
      ;;
    *)
      url=$1
      shift
      ;;
  esac
done
[[ -n $output && -n $url ]]
local_boot=$(tr -d '\r\n' </proc/sys/kernel/random/boot_id)
remote_boot=fixture-independent-kernel
[[ ${HSERVER_PROVIDER_FIXTURE_MODE:-success} == same-kernel ]] && remote_boot=$local_boot
case "$url" in
  */api/health)
    printf '{"status":"ok","version":"v1.0.0-fixture","build_commit":"fixture-panel-commit"}\n' >"$output"
    ;;
  */api/system/info)
    if [[ ${HSERVER_PROVIDER_FIXTURE_MODE:-success} == hostname-compatibility ]]; then
      printf '{"hostname":"%s","panel_version":"v0.9.0-fixture","arch":"amd64"}\n' "$(hostname)" >"$output"
    else
      printf '{"boot_id":"%s","hostname":"%s","panel_version":"v1.0.0-fixture","arch":"amd64"}\n' "$local_boot" "$(hostname)" >"$output"
    fi
    ;;
  */api/nodes/fixture-node)
    cat >"$output" <<JSON
{"id":"fixture-node","online":true,"last_seen_at":"2026-08-28T10:00:00Z","protocol_version":"v1","agent_version":"v1.0.0-fixture","capabilities":["inventory","terminal","process.read","process.signal","host.action","agent.update.read"],"inventory":{"arch":"arm64","boot_id":"$remote_boot","memory_available_bytes":1073741824,"processes":[]}}
JSON
    ;;
  */api/nodes/fixture-node/processes)
    marker=$(cat "$HSERVER_PROVIDER_FIXTURE_STATE/marker")
    printf '[{"pid":4242,"startTime":777,"user":"fixture","cpu":0,"memory":0,"rss":1,"command":"%s"}]\n' "$marker" >"$output"
    ;;
  */api/nodes/fixture-node/processes/signal)
    printf '{"message":"fixture marker exited","exited":true,"confirmed":true}\n' >"$output"
    ;;
  */api/nodes/fixture-node/tasks\?limit=100)
    printf '[]\n' >"$output"
    ;;
  */api/nodes/fixture-node/tasks)
    python3 - "$data_file" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    request = json.load(handle)
if request != {"kind": "backup.inventory", "payload": {}, "confirmed": True}:
    raise SystemExit(f"unexpected disabled-capability request: {request}")
PY
    printf '{"error":"agent hub: node does not advertise backup.read: capability unavailable"}\n' >"$output"
    [[ -n $write_format ]] && printf '409'
    ;;
  *)
    echo "unexpected fixture URL: $url" >&2
    exit 1
    ;;
esac
EOF
chmod +x "$tmp/bin/curl"

common=(
  --confirm-bounded-marker
  --panel-url https://8.8.8.8
  --node fixture-node
  --token-file "$tmp/admin.token"
  --hserverctl "$tmp/bin/hserverctl"
)

if PATH="$tmp/bin:$PATH" "$acceptance" "${common[@]}" >"$tmp/no-opt-in.out" 2>"$tmp/no-opt-in.err"; then
  fail "provider-network acceptance ran without its mutation opt-in"
fi
grep -q 'Refusing provider-network mutation' "$tmp/no-opt-in.err"

insecure=("${common[@]}")
insecure[2]=http://127.0.0.1
if HSERVER_ACCEPT_PROVIDER_NETWORK=1 PATH="$tmp/bin:$PATH" \
  "$acceptance" "${insecure[@]}" >"$tmp/insecure.out" 2>"$tmp/insecure.err"; then
  fail "provider-network acceptance accepted an insecure loopback panel URL"
fi
grep -q 'absolute HTTPS URL' "$tmp/insecure.err"

if HSERVER_ACCEPT_PROVIDER_NETWORK=1 HSERVER_PROVIDER_FIXTURE_MODE=same-kernel \
  HSERVER_PROVIDER_FIXTURE_STATE="$tmp/state" PATH="$tmp/bin:$PATH" \
  "$acceptance" "${common[@]}" >"$tmp/same-kernel.out" 2>"$tmp/same-kernel.err"; then
  fail "provider-network acceptance accepted a shared kernel boot ID"
fi
grep -q 'use a real separate VM' "$tmp/same-kernel.err"

receipt="$tmp/provider-network-receipt.json"
HSERVER_ACCEPT_PROVIDER_NETWORK=1 HSERVER_PROVIDER_FIXTURE_STATE="$tmp/state" \
  PATH="$tmp/bin:$PATH" "$acceptance" "${common[@]}" --receipt "$receipt" \
  >"$tmp/success.out" 2>"$tmp/success.err"
grep -q 'provider-network managed-agent acceptance: OK' "$tmp/success.out"
[[ ! -s $tmp/success.err ]] || fail "successful provider-network fixture wrote unexpected stderr"
[[ $(stat -c '%a' "$receipt") == 600 ]] || fail "provider-network receipt is not mode 0600"
python3 - "$receipt" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    receipt = json.load(handle)
if receipt.get("schema_version") != 3 or receipt.get("status") != "passed":
    raise SystemExit("provider-network fixture returned an invalid receipt")
checks = receipt.get("checks", {})
if len(checks) != 13 or not all(checks.values()):
    raise SystemExit(f"provider-network receipt has incomplete checks: {checks}")
if receipt.get("node_id") != "fixture-node" or receipt.get("panel_origin") != "https://8.8.8.8":
    raise SystemExit("provider-network receipt returned the wrong destination")
if receipt.get("panel_version") != "v1.0.0-fixture" or receipt.get("panel_arch") != "amd64":
    raise SystemExit("provider-network receipt omitted the panel release identity")
if receipt.get("panel_commit") != "fixture-panel-commit":
    raise SystemExit("provider-network receipt omitted the public panel build commit")
if receipt.get("agent_version") != "v1.0.0-fixture":
    raise SystemExit("provider-network receipt omitted the agent release identity")
if receipt.get("cli_version") != "v1.0.0-fixture":
    raise SystemExit("provider-network receipt omitted the CLI release identity")
if receipt.get("node_arch") != "arm64":
    raise SystemExit("provider-network receipt omitted the managed-node architecture")
if receipt.get("panel_identity_method") != "boot_id":
    raise SystemExit("provider-network receipt omitted the panel identity method")
if receipt.get("disabled_capability") != "backup.read":
    raise SystemExit("provider-network receipt omitted the selected disabled capability")
if receipt.get("terminal_close_mode") != "normal":
    raise SystemExit("provider-network receipt omitted the normal terminal close mode")
if receipt.get("marker_allocation_bytes") != 16 << 20:
    raise SystemExit("provider-network receipt omitted the bounded marker allocation")
PY

compat_receipt="$tmp/provider-network-compat-receipt.json"
HSERVER_ACCEPT_PROVIDER_NETWORK=1 HSERVER_PROVIDER_FIXTURE_MODE=hostname-compatibility \
  HSERVER_PROVIDER_FIXTURE_STATE="$tmp/state" PATH="$tmp/bin:$PATH" \
  "$acceptance" "${common[@]}" --receipt "$compat_receipt" >/dev/null
python3 - "$compat_receipt" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    receipt = json.load(handle)
if receipt.get("panel_identity_method") != "hostname_compatibility":
    raise SystemExit("legacy panel identity compatibility was not recorded")
if receipt.get("panel_version") != "v0.9.0-fixture":
    raise SystemExit("legacy panel release identity was not recorded")
if receipt.get("terminal_close_mode") != "legacy_agent_eio":
    raise SystemExit("legacy agent PTY close compatibility was not recorded")
PY
if grep -R -F "$token" "$tmp" --exclude=admin.token >/dev/null; then
  fail "provider-network acceptance exposed the token in output or fixture state"
fi

echo "provider-network managed-agent acceptance fixture: OK"
