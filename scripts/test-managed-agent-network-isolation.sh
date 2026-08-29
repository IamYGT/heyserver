#!/usr/bin/env bash
set -euo pipefail

if [[ ${HSERVER_ACCEPT_NETWORK_NAMESPACE:-0} != 1 || ${CI:-false} != true ]]; then
  echo "Refusing network-isolation mutation outside an explicitly disposable CI host." >&2
  exit 1
fi
if (( EUID != 0 )); then
  echo "Managed-agent network isolation acceptance must run as root." >&2
  exit 1
fi
if [[ $# -ne 2 ]]; then
  echo "Usage: $0 PANEL_BINARY AGENT_BINARY" >&2
  exit 2
fi

die() {
  echo "$*" >&2
  exit 1
}

for command_name in curl find grep install ip openssl ping python3 readlink; do
  command -v "$command_name" >/dev/null 2>&1 || die "$command_name is required"
done

panel_binary=$(readlink -f "$1")
agent_binary=$(readlink -f "$2")
[[ -x "$panel_binary" ]] || die "Panel binary is not executable: $panel_binary"
[[ -x "$agent_binary" ]] || die "Agent binary is not executable: $agent_binary"

suffix=$(openssl rand -hex 3)
hub_namespace="hs-hub-$suffix"
node_namespace="hs-node-$suffix"
hub_veth="hsh${suffix}"
node_veth="hsn${suffix}"
hub_address="10.203.0.1"
node_address="10.203.0.2"
hub_origin="http://${hub_address}:3085"
node_id="isolated-node-${suffix}"
marker="hserver-isolation-${suffix}"
tmp=$(mktemp -d /tmp/hserver-network-isolation-XXXXXXXX)
panel_pid=
agent_pid=
marker_pid=

cleanup() {
  for pid in "$agent_pid" "$marker_pid" "$panel_pid"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
  ip netns delete "$node_namespace" >/dev/null 2>&1 || true
  ip netns delete "$hub_namespace" >/dev/null 2>&1 || true
  find "$tmp" -xdev -depth -delete >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

ip netns add "$hub_namespace"
ip netns add "$node_namespace"
ip link add "$hub_veth" type veth peer name "$node_veth"
ip link set "$hub_veth" netns "$hub_namespace"
ip link set "$node_veth" netns "$node_namespace"
ip -n "$hub_namespace" link set lo up
ip -n "$node_namespace" link set lo up
ip -n "$hub_namespace" address add "${hub_address}/30" dev "$hub_veth"
ip -n "$node_namespace" address add "${node_address}/30" dev "$node_veth"
ip -n "$hub_namespace" link set "$hub_veth" up
ip -n "$node_namespace" link set "$node_veth" up

if ip -n "$hub_namespace" route show | grep -q '^default '; then
  die "Hub namespace unexpectedly has a default route."
fi
if ip -n "$node_namespace" route show | grep -q '^default '; then
  die "Managed-node namespace unexpectedly has a default route."
fi
ip netns exec "$node_namespace" ping -c 1 -W 1 "$hub_address" >/dev/null

admin_email="admin@localhost"
admin_password=$(openssl rand -hex 24)
jwt_secret=$(openssl rand -hex 32)
install -d -m 0700 "$tmp/data" "$tmp/vhosts" "$tmp/nginx/available" "$tmp/nginx/enabled" "$tmp/nginx/snippets"

ip netns exec "$hub_namespace" env \
  HSERVER_PORT=3085 \
  HSERVER_DB_PATH="$tmp/data/hserver.db" \
  HSERVER_DATA_DIR="$tmp/data" \
  HSERVER_JWT_SECRET="$jwt_secret" \
  HSERVER_ADMIN_EMAIL="$admin_email" \
  HSERVER_ADMIN_PASS="$admin_password" \
  HSERVER_LOG_LEVEL=warn \
  HSERVER_VHOSTS_ROOT="$tmp/vhosts" \
  HSERVER_NGINX_SITES_AVAILABLE="$tmp/nginx/available" \
  HSERVER_NGINX_SITES_ENABLED="$tmp/nginx/enabled" \
  HSERVER_NGINX_SNIPPETS_DIR="$tmp/nginx/snippets" \
  "$panel_binary" >"$tmp/panel.log" 2>&1 &
panel_pid=$!

hub_curl() {
  ip netns exec "$hub_namespace" curl --noproxy '*' "$@"
}

panel_ready=0
for _ in {1..30}; do
  if hub_curl -fsS --max-time 1 http://127.0.0.1:3085/api/health >/dev/null 2>&1; then
    panel_ready=1
    break
  fi
  if ! kill -0 "$panel_pid" >/dev/null 2>&1; then
    cat "$tmp/panel.log" >&2
    die "Panel exited before the isolated hub became healthy."
  fi
  sleep 1
done
(( panel_ready )) || {
  cat "$tmp/panel.log" >&2
  die "Isolated hub did not become healthy."
}

# The node cannot confuse its own loopback with the hub, and the hub cannot
# initiate a panel connection to the node. Only the veth peer is reachable.
if ip netns exec "$node_namespace" curl --noproxy '*' -fsS --max-time 1 http://127.0.0.1:3085/api/health >/dev/null 2>&1; then
  die "Managed-node loopback unexpectedly reaches the hub panel."
fi
if hub_curl -fsS --max-time 1 "http://${node_address}:3085/api/health" >/dev/null 2>&1; then
  die "Hub unexpectedly reached a panel listener on the managed node."
fi

python3 - "$tmp/login.json" "$admin_email" "$admin_password" <<'PY'
import json
import sys

with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump({"email": sys.argv[2], "password": sys.argv[3]}, handle)
PY
chmod 0600 "$tmp/login.json"
hub_curl -fsS --max-time 5 \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/login.json" \
  -o "$tmp/login-response.json" \
  http://127.0.0.1:3085/api/auth/login
python3 - "$tmp/login-response.json" "$tmp/auth-header" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    token = json.load(handle).get("token")
if not isinstance(token, str) or not token:
    raise SystemExit("isolated hub login did not return a token")
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    handle.write("Authorization: Bearer " + token + "\n")
PY
chmod 0600 "$tmp/login-response.json" "$tmp/auth-header"

printf '{"id":"%s","name":"Network-isolated node"}\n' "$node_id" >"$tmp/node-register.json"
hub_curl -fsS --max-time 5 \
  -H "@$tmp/auth-header" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/node-register.json" \
  -o "$tmp/node-register-response.json" \
  http://127.0.0.1:3085/api/nodes
python3 - "$tmp/node-register-response.json" "$tmp/agent.token" "$node_id" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    response = json.load(handle)
if response.get("node", {}).get("id") != sys.argv[3]:
    raise SystemExit("isolated node registration returned the wrong identity")
token = response.get("token")
if not isinstance(token, str) or not token:
    raise SystemExit("isolated node registration did not return a one-time token")
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    handle.write(token + "\n")
PY
chmod 0600 "$tmp/node-register-response.json" "$tmp/agent.token"

ip netns exec "$node_namespace" bash -c 'exec -a "$1" sleep 300' _ "$marker" &
marker_pid=$!
ip netns exec "$node_namespace" env \
  HSERVER_AGENT_HUB_URL="$hub_origin" \
  HSERVER_AGENT_NODE_ID="$node_id" \
  HSERVER_AGENT_TOKEN_FILE="$tmp/agent.token" \
  HSERVER_AGENT_INTERVAL=5s \
  HSERVER_AGENT_ALLOW_PROCESS_SIGNALS=true \
  NO_PROXY='*' \
  no_proxy='*' \
  "$agent_binary" >"$tmp/agent.log" 2>&1 &
agent_pid=$!

node_ready=0
for _ in {1..30}; do
  if hub_curl -fsS --max-time 3 \
    -H "@$tmp/auth-header" \
    -o "$tmp/node.json" \
    "http://127.0.0.1:3085/api/nodes/$node_id" && \
    python3 - "$tmp/node.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    node = json.load(handle)
capabilities = set(node.get("capabilities", []))
required = {"inventory", "process.read", "process.signal"}
if node.get("online") is not True or not node.get("last_seen_at"):
    raise SystemExit(1)
if not required.issubset(capabilities):
    raise SystemExit(1)
if "host.action" in capabilities:
    raise SystemExit("isolated node unexpectedly advertised disabled host.action")
PY
  then
    node_ready=1
    break
  fi
  if ! kill -0 "$agent_pid" >/dev/null 2>&1; then
    cat "$tmp/agent.log" >&2
    die "Agent exited before the isolated node became online."
  fi
  sleep 1
done
(( node_ready )) || {
  cat "$tmp/agent.log" >&2
  die "Network-isolated agent did not report the required capabilities."
}

hub_curl -fsS --max-time 5 \
  -H "@$tmp/auth-header" \
  -o "$tmp/processes.json" \
  "http://127.0.0.1:3085/api/nodes/$node_id/processes"
python3 - "$tmp/processes.json" "$tmp/process-signal.json" "$marker" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    processes = json.load(handle)
matches = [item for item in processes if sys.argv[3] in item.get("command", "")]
if len(matches) != 1:
    raise SystemExit(f"expected one isolated marker process, found {len(matches)}")
process = matches[0]
if not isinstance(process.get("pid"), int) or process["pid"] <= 1:
    raise SystemExit("isolated marker process has an invalid pid")
if not isinstance(process.get("startTime"), int) or process["startTime"] <= 0:
    raise SystemExit("isolated marker process has no stable start-time identity")
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    json.dump({"pid": process["pid"], "startTime": process["startTime"], "signal": "term"}, handle)
PY

hub_curl -fsS --max-time 35 \
  -H "@$tmp/auth-header" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/process-signal.json" \
  -o "$tmp/process-signal-response.json" \
  "http://127.0.0.1:3085/api/nodes/$node_id/processes/signal"
python3 - "$tmp/process-signal-response.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    result = json.load(handle)
if result.get("exited") is not True or result.get("confirmed") is not True:
    raise SystemExit(f"isolated process mutation was not confirmed: {result}")
if not isinstance(result.get("message"), str) or not result["message"]:
    raise SystemExit("isolated process mutation omitted its receipt")
PY
wait "$marker_pid" >/dev/null 2>&1 || true
marker_pid=

hub_curl -fsS --max-time 5 -H "@$tmp/auth-header" \
  -o "$tmp/tasks-before-disabled.json" \
  "http://127.0.0.1:3085/api/nodes/$node_id/tasks?limit=100"
disabled_code=$(hub_curl -sS --max-time 5 \
  -H "@$tmp/auth-header" \
  -o "$tmp/disabled-action.json" \
  -w '%{http_code}' \
  -X POST \
  "http://127.0.0.1:3085/api/nodes/$node_id/actions/memory-optimize")
[[ "$disabled_code" == 409 ]] || die "Disabled host.action returned HTTP $disabled_code instead of 409."
hub_curl -fsS --max-time 5 -H "@$tmp/auth-header" \
  -o "$tmp/tasks-after-disabled.json" \
  "http://127.0.0.1:3085/api/nodes/$node_id/tasks?limit=100"
python3 - "$tmp/disabled-action.json" "$tmp/tasks-before-disabled.json" "$tmp/tasks-after-disabled.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    error = json.load(handle).get("error", "")
if "does not advertise host.action" not in error:
    raise SystemExit(f"disabled capability rejection did not name host.action: {error}")
with open(sys.argv[2], encoding="utf-8") as handle:
    before = json.load(handle)
with open(sys.argv[3], encoding="utf-8") as handle:
    after = json.load(handle)
if len(after) != len(before):
    raise SystemExit("disabled capability rejection changed persisted task history")
PY

printf '%s\n' 'managed-agent network isolation acceptance: OK'
