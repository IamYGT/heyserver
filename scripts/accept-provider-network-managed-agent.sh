#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  HSERVER_ACCEPT_PROVIDER_NETWORK=1 accept-provider-network-managed-agent.sh \
    --confirm-bounded-marker \
    --panel-url https://panel.example.com \
    --node NODE_ID \
    --token-file /protected/admin.token \
    [--hserverctl /usr/local/bin/hserverctl] \
    [--receipt /protected/provider-network-receipt.json]

Runs a bounded acceptance drill against an already enrolled disposable managed
node. The drill creates only its own temporary sleep process through the remote
terminal, observes that exact stable process identity, terminates it through the
managed-node API, and proves a known disabled capability is rejected without a
task. A schema-v3 receipt binds the panel build commit, CLI, agent, and
managed-node architectures to the exercised path.
EOF
}

die() {
  echo "$*" >&2
  exit 1
}

panel_url=
node_id=
token_file=
hserverctl_binary=/usr/local/bin/hserverctl
receipt_path=
confirmed=0

while (( $# > 0 )); do
  case "$1" in
    --confirm-bounded-marker)
      confirmed=1
      shift
      ;;
    --panel-url|--node|--token-file|--hserverctl|--receipt)
      (( $# >= 2 )) || die "Missing value for $1."
      case "$1" in
        --panel-url) panel_url=$2 ;;
        --node) node_id=$2 ;;
        --token-file) token_file=$2 ;;
        --hserverctl) hserverctl_binary=$2 ;;
        --receipt) receipt_path=$2 ;;
      esac
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage >&2
      die "Unknown argument: $1"
      ;;
  esac
done

if [[ ${HSERVER_ACCEPT_PROVIDER_NETWORK:-0} != 1 || $confirmed != 1 ]]; then
  die "Refusing provider-network mutation without HSERVER_ACCEPT_PROVIDER_NETWORK=1 and --confirm-bounded-marker."
fi
[[ -n $panel_url ]] || die "--panel-url is required."
[[ -n $node_id ]] || die "--node is required."
[[ -n $token_file ]] || die "--token-file is required."
[[ $node_id =~ ^[[:alnum:]][[:alnum:]._-]{0,127}$ ]] || die "--node must be a valid managed-node ID."

for command in curl python3 script timeout stat install hostname; do
  command -v "$command" >/dev/null 2>&1 || die "Required command is unavailable: $command"
done
[[ -x $hserverctl_binary ]] || die "hserverctl is not executable: $hserverctl_binary"
[[ -e $token_file ]] || die "Token file does not exist: $token_file"
[[ ! -L $token_file && -f $token_file ]] || die "Token file must be a regular file and not a symlink."
[[ $(stat -c '%a' "$token_file") == 600 ]] || die "Token file must have mode 0600."
[[ $(stat -c '%u' "$token_file") == $(id -u) ]] || die "Token file must be owned by the current user."

panel_origin=$(python3 - "$panel_url" <<'PY'
import ipaddress
import socket
import sys
from urllib.parse import urlsplit

raw = sys.argv[1]
parsed = urlsplit(raw)
if parsed.scheme != "https" or not parsed.hostname:
    raise SystemExit("--panel-url must be an absolute HTTPS URL")
if parsed.username is not None or parsed.password is not None:
    raise SystemExit("--panel-url must not contain credentials")
if parsed.path not in ("", "/") or parsed.query or parsed.fragment:
    raise SystemExit("--panel-url must be an HTTPS origin without a path, query, or fragment")
try:
    port = parsed.port
except ValueError as error:
    raise SystemExit(f"--panel-url has an invalid port: {error}")

host = parsed.hostname
try:
    addresses = {ipaddress.ip_address(host)}
except ValueError:
    try:
        addresses = {
            ipaddress.ip_address(item[4][0])
            for item in socket.getaddrinfo(host, port or 443, type=socket.SOCK_STREAM)
        }
    except OSError as error:
        raise SystemExit(f"--panel-url hostname cannot be resolved: {error}")
if not any(address.is_global for address in addresses):
    raise SystemExit("--panel-url must resolve to at least one globally routable address")

hostname = f"[{host}]" if ":" in host else host
print(f"https://{hostname}" + (f":{port}" if port and port != 443 else ""))
PY
) || die "Provider-network panel URL validation failed."

local_boot_file=/proc/sys/kernel/random/boot_id
[[ -r $local_boot_file ]] || die "Local kernel boot ID is unavailable."
local_boot_id=$(tr -d '\r\n' <"$local_boot_file")
[[ -n $local_boot_id ]] || die "Local kernel boot ID is empty."
local_hostname=$(hostname)
[[ -n $local_hostname ]] || die "Local hostname is empty."

if [[ -n $receipt_path && ( -e $receipt_path || -L $receipt_path ) ]]; then
  die "Refusing to overwrite existing receipt: $receipt_path"
fi

tmp=$(mktemp -d)
marker_identity=
marker_terminated=0
cleanup() {
  local status=$?
  set +e
  if [[ -n $marker_identity && $marker_terminated != 1 && -f $tmp/process-signal.json ]]; then
    curl -sS --max-time 35 \
      -H "@$tmp/auth-header" \
      -H 'Content-Type: application/json' \
      --data-binary "@$tmp/process-signal.json" \
      -o /dev/null \
      "$panel_origin/api/nodes/$node_id/processes/signal" >/dev/null 2>&1
  fi
  find "$tmp" -xdev -depth -delete
  exit "$status"
}
trap cleanup EXIT INT TERM

"$hserverctl_binary" version >"$tmp/cli-version-output"
python3 - "$tmp/cli-version-output" "$tmp/cli-version" <<'PY'
import os
import re
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    output = handle.read().strip()
match = re.fullmatch(r"hserverctl ([^\s\x00-\x1f\x7f]{1,128}) \([^\r\n]*\)", output)
if not match:
    raise SystemExit("hserverctl version returned an unsupported release identity")
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as handle:
    handle.write(match.group(1))
PY

python3 - "$token_file" "$tmp/auth-header" <<'PY'
import os
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    token = handle.read().rstrip("\r\n")
if not token or "\n" in token or "\r" in token:
    raise SystemExit("token file must contain exactly one non-empty line")
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as handle:
    handle.write("Authorization: Bearer " + token + "\n")
PY

curl -fsS --max-time 10 \
  -H "@$tmp/auth-header" \
  -o "$tmp/panel-system-info.json" \
  "$panel_origin/api/system/info"
curl -fsS --max-time 10 \
  -o "$tmp/panel-health.json" \
  "$panel_origin/api/health"
python3 - "$tmp/panel-health.json" "$tmp/panel-commit" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    health = json.load(handle)
panel_commit = health.get("build_commit")
if not isinstance(panel_commit, str) or not panel_commit.strip() or panel_commit != panel_commit.strip():
    raise SystemExit("public panel health has no valid build commit")
if len(panel_commit.encode("utf-8")) > 128 or any(ord(character) < 32 or ord(character) == 127 for character in panel_commit):
    raise SystemExit("public panel health build commit is unsupported")
fd = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as handle:
    handle.write(panel_commit)
PY
python3 - "$tmp/panel-system-info.json" "$local_boot_id" "$local_hostname" "$tmp/panel-identity-method" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    system_info = json.load(handle)
panel_boot_id = system_info.get("boot_id")
if panel_boot_id:
    if panel_boot_id != sys.argv[2]:
        raise SystemExit("run this acceptance script on the native panel host; kernel boot IDs differ")
    method = "boot_id"
else:
    if system_info.get("hostname") != sys.argv[3]:
        raise SystemExit("run this acceptance script on the native panel host; hostnames differ")
    method = "hostname_compatibility"
with open(sys.argv[4], "w", encoding="utf-8") as handle:
    handle.write(method)
PY

curl -fsS --max-time 10 \
  -H "@$tmp/auth-header" \
  -o "$tmp/node.json" \
  "$panel_origin/api/nodes/$node_id"

python3 - "$tmp/node.json" "$node_id" "$local_boot_id" "$tmp/remote-boot-id" "$tmp/disabled-task.json" "$tmp/disabled-capability" "$tmp/marker-allocation-bytes" "$tmp/node-arch" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    node = json.load(handle)
if node.get("id") != sys.argv[2]:
    raise SystemExit("panel returned a different managed-node identity")
if node.get("online") is not True or not node.get("last_seen_at"):
    raise SystemExit("managed node is not online with a server-observed heartbeat")
if node.get("protocol_version") != "v1":
    raise SystemExit("managed node is not using agent protocol v1")
capabilities = set(node.get("capabilities", []))
required = {"inventory", "terminal", "process.read", "process.signal"}
missing = sorted(required - capabilities)
if missing:
    raise SystemExit("managed node is missing required capabilities: " + ", ".join(missing))
remote_boot_id = node.get("inventory", {}).get("boot_id")
if not isinstance(remote_boot_id, str) or not remote_boot_id.strip():
    raise SystemExit("managed node inventory has no kernel boot ID")
if remote_boot_id == sys.argv[3]:
    raise SystemExit("managed node shares the panel host kernel boot ID; use a real separate VM")
with open(sys.argv[4], "w", encoding="utf-8") as handle:
    handle.write(remote_boot_id)
node_arch = node.get("inventory", {}).get("arch")
if node_arch not in ("amd64", "arm64"):
    raise SystemExit("managed node inventory has no supported release architecture")
with open(sys.argv[8], "w", encoding="utf-8") as handle:
    handle.write(node_arch)
disabled_checks = (
    ("host.action", {"kind": "host.action", "payload": {"action": "memory-optimize"}, "confirmed": True}),
    ("agent.update.read", {"kind": "agent.update.status", "payload": {}, "confirmed": True}),
    ("backup.read", {"kind": "backup.inventory", "payload": {}, "confirmed": True}),
)
for capability, request in disabled_checks:
    if capability not in capabilities:
        with open(sys.argv[5], "w", encoding="utf-8") as handle:
            json.dump(request, handle)
        with open(sys.argv[6], "w", encoding="utf-8") as handle:
            handle.write(capability)
        break
else:
    raise SystemExit("managed node advertises every safe denial-check capability: host.action, agent.update.read, backup.read")
processes = node.get("inventory", {}).get("processes", [])
observed_floor = 0
rss_values = [item.get("rss", 0) for item in processes if isinstance(item.get("rss"), int)]
if len(processes) >= 50 and rss_values:
    observed_floor = min(rss_values)
allocation = max(16 << 20, observed_floor + (8 << 20))
if allocation > 96 << 20:
    raise SystemExit("managed node process inventory floor requires a marker larger than the 96 MiB acceptance cap")
available = node.get("inventory", {}).get("memory_available_bytes", 0)
if not isinstance(available, int) or available < allocation * 4:
    raise SystemExit("managed node does not have four times the bounded marker allocation available")
with open(sys.argv[7], "w", encoding="utf-8") as handle:
    handle.write(str(allocation))
PY

marker="hserver-provider-acceptance-$(python3 - <<'PY'
import secrets
print(secrets.token_hex(12))
PY
)"
transcript="$tmp/terminal.log"
printf -v terminal_cli '%q --server %q --token-file %q --timeout 10s terminal --node %q' \
  "$hserverctl_binary" "$panel_origin" "$token_file" "$node_id"
printf -v quoted_marker '%q' "$marker"
marker_allocation_bytes=$(cat "$tmp/marker-allocation-bytes")
terminal_input="stty -echo; command -v setsid >/dev/null || { printf 'HSERVER_PROVIDER_MARKER_ERROR:no-setsid\\n'; exit 127; }; command -v python3 >/dev/null || { printf 'HSERVER_PROVIDER_MARKER_ERROR:no-python3\\n'; exit 127; }; nohup setsid -f bash -c 'exec -a \"\$1\" python3 -c \"import time; payload=bytearray(\$2); time.sleep(300)\"' _ $quoted_marker $marker_allocation_bytes </dev/null >/dev/null 2>&1 & printf 'HSERVER_PROVIDER_MARKER_PID:%s\\n' \"\$!\"; exit"
set +e
printf '%s\n' "$terminal_input" \
  | timeout 45s script -qefc "$terminal_cli" "$transcript" >/dev/null
terminal_status=$?
set -e

terminal_pid=$(grep -aoE 'HSERVER_PROVIDER_MARKER_PID:[0-9]+' "$transcript" \
  | tail -n 1 | cut -d: -f2 || true)
if [[ ! $terminal_pid =~ ^[0-9]+$ || $terminal_pid -le 1 ]]; then
  grep -aE 'hserverctl:|HTTP [0-9]|connect terminal|terminal server|websocket|HSERVER_PROVIDER_MARKER_ERROR' "$transcript" \
    | tail -n 3 >&2 || true
  die "Remote terminal did not return a valid marker PID."
fi
terminal_close_mode=normal
if (( terminal_status != 0 )); then
  if grep -aFq 'hserverctl: terminal closed: terminal process ended unexpectedly' "$transcript"; then
    terminal_close_mode=legacy_agent_eio
  else
    grep -aE 'hserverctl:|HTTP [0-9]|connect terminal|terminal server|websocket|HSERVER_PROVIDER_MARKER_ERROR' "$transcript" \
      | tail -n 3 >&2 || true
    die "Remote hserverctl terminal failed after returning its marker PID."
  fi
fi

process_observed=0
for _ in {1..10}; do
  curl -fsS --max-time 10 \
    -H "@$tmp/auth-header" \
    -o "$tmp/processes.json" \
    "$panel_origin/api/nodes/$node_id/processes"
  if python3 - "$tmp/processes.json" "$tmp/process-signal.json" "$marker" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    processes = json.load(handle)
matches = [item for item in processes if sys.argv[3] in item.get("command", "")]
if len(matches) != 1:
    raise SystemExit(1)
process = matches[0]
if not isinstance(process.get("pid"), int) or process["pid"] <= 1:
    raise SystemExit("process inventory marker has an invalid PID")
if not isinstance(process.get("startTime"), int) or process["startTime"] <= 0:
    raise SystemExit("process inventory marker has no stable start-time identity")
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    json.dump({"pid": process["pid"], "startTime": process["startTime"], "signal": "term"}, handle)
PY
  then
    process_observed=1
    marker_identity=observed
    break
  fi
  sleep 3
done
(( process_observed )) || die "Managed-node inventory did not observe the terminal marker process."

curl -fsS --max-time 35 \
  -H "@$tmp/auth-header" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/process-signal.json" \
  -o "$tmp/process-signal-response.json" \
  "$panel_origin/api/nodes/$node_id/processes/signal"
python3 - "$tmp/process-signal-response.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    result = json.load(handle)
if result.get("exited") is not True or result.get("confirmed") is not True:
    raise SystemExit("stable-identity process mutation was not confirmed")
if not isinstance(result.get("message"), str) or not result["message"].strip():
    raise SystemExit("stable-identity process mutation omitted its receipt")
PY
marker_terminated=1

curl -fsS --max-time 10 \
  -H "@$tmp/auth-header" \
  -o "$tmp/tasks-before.json" \
  "$panel_origin/api/nodes/$node_id/tasks?limit=100"
disabled_capability=$(cat "$tmp/disabled-capability")
disabled_code=$(curl -sS --max-time 10 \
  -H "@$tmp/auth-header" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/disabled-task.json" \
  -o "$tmp/disabled-action.json" \
  -w '%{http_code}' \
  "$panel_origin/api/nodes/$node_id/tasks")
[[ $disabled_code == 409 ]] || die "Disabled $disabled_capability returned HTTP $disabled_code instead of 409."
curl -fsS --max-time 10 \
  -H "@$tmp/auth-header" \
  -o "$tmp/tasks-after.json" \
  "$panel_origin/api/nodes/$node_id/tasks?limit=100"
python3 - "$tmp/disabled-action.json" "$tmp/tasks-before.json" "$tmp/tasks-after.json" "$disabled_capability" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    error = json.load(handle).get("error", "")
if f"does not advertise {sys.argv[4]}" not in error:
    raise SystemExit("disabled capability rejection did not name the selected capability")
with open(sys.argv[2], encoding="utf-8") as handle:
    before = json.load(handle)
with open(sys.argv[3], encoding="utf-8") as handle:
    after = json.load(handle)
if len(after) != len(before):
    raise SystemExit("disabled capability rejection changed persisted task history")
PY

python3 - "$panel_origin" "$node_id" "$tmp/panel-system-info.json" "$tmp/panel-commit" "$tmp/node.json" "$tmp/panel-identity-method" "$tmp/disabled-capability" "$terminal_close_mode" "$marker_allocation_bytes" "$tmp/cli-version" "$tmp/node-arch" "$tmp/receipt.json" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

with open(sys.argv[3], encoding="utf-8") as handle:
    panel = json.load(handle)
with open(sys.argv[4], encoding="utf-8") as handle:
    panel_commit = handle.read().strip()
with open(sys.argv[5], encoding="utf-8") as handle:
    node = json.load(handle)
with open(sys.argv[6], encoding="utf-8") as handle:
    panel_identity_method = handle.read().strip()
with open(sys.argv[7], encoding="utf-8") as handle:
    disabled_capability = handle.read().strip()
with open(sys.argv[10], encoding="utf-8") as handle:
    cli_version = handle.read().strip()
with open(sys.argv[11], encoding="utf-8") as handle:
    node_arch = handle.read().strip()
receipt = {
    "schema_version": 3,
    "status": "passed",
    "accepted_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "panel_origin": sys.argv[1],
    "node_id": sys.argv[2],
    "panel_version": panel.get("panel_version", ""),
    "panel_commit": panel_commit,
    "panel_arch": panel.get("arch", ""),
    "cli_version": cli_version,
    "agent_version": node.get("agent_version", ""),
    "node_arch": node_arch,
    "panel_identity_method": panel_identity_method,
    "disabled_capability": disabled_capability,
    "terminal_close_mode": sys.argv[8],
    "marker_allocation_bytes": int(sys.argv[9]),
    "checks": {
        "public_https_path": True,
        "acceptance_runs_on_panel_kernel": True,
        "server_observed_online": True,
        "protocol_v1": True,
        "separate_kernel_boot_id": True,
        "required_capabilities": True,
        "cli_release_identity": True,
        "managed_node_architecture": True,
        "writable_remote_terminal": True,
        "process_inventory": True,
        "stable_identity_process_signal": True,
        "disabled_capability_rejected": True,
        "rejected_task_not_persisted": True,
    },
}
fd = os.open(sys.argv[12], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as handle:
    json.dump(receipt, handle, indent=2, sort_keys=True)
    handle.write("\n")
PY

if [[ -n $receipt_path ]]; then
  receipt_parent=$(dirname "$receipt_path")
  [[ -d $receipt_parent ]] || die "Receipt directory does not exist: $receipt_parent"
  install -m 0600 "$tmp/receipt.json" "$receipt_path"
  printf 'provider-network managed-agent acceptance: OK (receipt: %s)\n' "$receipt_path"
else
  cat "$tmp/receipt.json"
fi
