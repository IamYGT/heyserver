#!/usr/bin/env bash
set -euo pipefail

if [[ ${HSERVER_ACCEPT_DISPOSABLE_HOST:-0} != 1 || ${CI:-false} != true ]]; then
  echo "Refusing managed-agent lifecycle mutation outside an explicitly disposable CI host." >&2
  exit 1
fi
if (( EUID != 0 )); then
  echo "Managed-agent lifecycle acceptance must run as root." >&2
  exit 1
fi
if [[ $# -ne 6 ]]; then
  echo "Usage: $0 VERSION ARCH ARCHIVE CHECKSUM UPGRADE_VERSION UPGRADE_AGENT" >&2
  exit 2
fi

version=$1
arch=$2
archive=$3
checksum=$4
upgrade_version=$5
upgrade_agent=$6
root_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# The managed status endpoint waits up to 45 seconds for the agent task. Keep
# each client request just above that server deadline, while bounding retries
# tightly enough that a lost runner cannot spend tens of minutes polling.
managed_status_poll_attempts=2
managed_status_request_timeout=50
managed_status_poll_interval=2

die() {
  echo "$*" >&2
  exit 1
}

progress() {
  printf '[managed-agent-lifecycle][%s] %s\n' "$arch" "$1"
}

for command_name in base64 curl openssl python3 sha256sum systemctl systemd-run tar timeout; do
  command -v "$command_name" >/dev/null 2>&1 || die "$command_name is required"
done

python3 - "$version" "$upgrade_version" <<'PY'
import re
import sys

def stable(value):
    match = re.fullmatch(r"v?(\d+)\.(\d+)\.(\d+)", value)
    if not match:
        raise SystemExit(f"version is not stable major.minor.patch: {value}")
    return tuple(map(int, match.groups()))

current = stable(sys.argv[1])
upgrade = stable(sys.argv[2])
if upgrade <= current:
    raise SystemExit("upgrade version must be newer than the packaged version")
PY

case "$(uname -m):$arch" in
  x86_64:amd64|aarch64:arm64|arm64:arm64) ;;
  *) die "Native runner architecture $(uname -m) does not match package $arch." ;;
esac

for path in \
  /usr/local/bin/hserver-panel \
  /usr/local/bin/hserver-agent \
  /usr/local/libexec/hserver-agent-install \
  /etc/hserver \
  /etc/hserver-agent.env \
  /etc/hserver-agent.token \
  /var/lib/hserver \
  /var/lib/hserver-agent \
  /etc/systemd/system/hserver.service \
  /etc/systemd/system/hserver-agent.service
do
  [[ ! -e "$path" ]] || die "Refusing to overwrite a pre-existing HServer installation: $path"
done

assert_port_free() {
  python3 - "$1" <<'PY'
import socket
import sys

sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
try:
    sock.bind(("127.0.0.1", int(sys.argv[1])))
finally:
    sock.close()
PY
}

assert_port_free 3085 || die "Refusing to use occupied HServer port 3085."
assert_port_free 38086 || die "Refusing to use occupied release-feed port 38086."
[[ -s "$archive" && -s "$checksum" && -s "$upgrade_agent" ]] || die "Release inputs are missing."

tmp=$(mktemp -d /tmp/hserver-managed-agent-acceptance-XXXXXXXX)
package_dir="$tmp/hserver-panel-${version}-linux-${arch}"
feed_dir="$tmp/feed"
node_id="native-agent-${arch}"
panel_installed=0
agent_touched=0
feed_pid=
cleanup_done=0

bounded_cleanup() {
  local deadline=$1
  shift
  timeout --signal=TERM --kill-after=5s "$deadline" "$@" >/dev/null 2>&1 || true
}

cleanup() {
  if (( cleanup_done )); then
    return
  fi
  cleanup_done=1
  progress "cleanup"
  bounded_cleanup 15s systemctl stop hserver-agent-lifecycle.timer hserver-agent-lifecycle.service
  bounded_cleanup 10s systemctl reset-failed hserver-agent-lifecycle.timer hserver-agent-lifecycle.service
  if [[ -n "$feed_pid" ]]; then
    kill "$feed_pid" >/dev/null 2>&1 || true
    timeout 5s tail --pid="$feed_pid" -f /dev/null >/dev/null 2>&1 || kill -KILL "$feed_pid" >/dev/null 2>&1 || true
    wait "$feed_pid" >/dev/null 2>&1 || true
  fi
  if (( agent_touched )); then
    if [[ -x /usr/local/libexec/hserver-agent-install ]]; then
      bounded_cleanup 45s /usr/local/libexec/hserver-agent-install uninstall --purge-config
    elif [[ -x "$package_dir/agent-install.sh" ]]; then
      bounded_cleanup 45s "$package_dir/agent-install.sh" uninstall --purge-config
    fi
    if [[ -e /var/lib/hserver-agent ]]; then
      bounded_cleanup 30s find /var/lib/hserver-agent -xdev -depth -delete
    fi
  fi
  if (( panel_installed )) && [[ -x "$package_dir/install.sh" ]]; then
    bounded_cleanup 45s env HSERVER_HEALTH_TIMEOUT=3 "$package_dir/install.sh" uninstall --purge-config --purge-data
  fi
  bounded_cleanup 30s rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 143' INT TERM

progress "verify release archive and package"
"$root_dir/scripts/verify-release-archive.sh" "$version" "$arch" "$archive" "$checksum"
tar -xzf "$archive" -C "$tmp"
[[ -x "$package_dir/install.sh" && -x "$package_dir/agent-install.sh" ]] || die "Release package lifecycle tools are missing."
[[ $("$package_dir/hserver-agent" --version) == "hserver-agent $version" ]] || die "Packaged agent version is incorrect."
[[ $("$upgrade_agent" --version) == "hserver-agent $upgrade_version" ]] || die "Upgrade agent version is incorrect."
progress "release archive and package verified"

progress "install panel and verify health"
"$package_dir/doctor.sh" preflight
"$package_dir/install.sh" install
panel_installed=1
"$package_dir/doctor.sh" installed
curl -fsS --max-time 3 http://127.0.0.1:3085/api/health >/dev/null
progress "panel installed and healthy"

progress "authenticate panel and configure CLI context"
login_request="$tmp/login.json"
login_response="$tmp/login-response.json"
auth_header="$tmp/auth-header.txt"
cli_token="$tmp/hserverctl-token"
cli_contexts="$tmp/hserverctl-contexts.json"
python3 - /etc/hserver/hserver.env "$login_request" <<'PY'
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
chmod 0600 "$login_request"
curl -fsS --max-time 5 \
  -H 'Content-Type: application/json' \
  --data-binary "@$login_request" \
  -o "$login_response" \
  http://127.0.0.1:3085/api/auth/login
python3 - "$login_response" "$auth_header" "$cli_token" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    token = json.load(handle).get("token")
if not isinstance(token, str) or not token:
    raise SystemExit("login response did not contain a token")
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    handle.write("Authorization: Bearer " + token + "\n")
with open(sys.argv[3], "w", encoding="utf-8") as handle:
    handle.write(token + "\n")
PY
chmod 0600 "$login_response" "$auth_header" "$cli_token"
export HSERVER_CONTEXT_FILE="$cli_contexts"
/usr/local/bin/hserverctl context add \
  --server http://127.0.0.1:3085 \
  --token-file "$cli_token" \
  --use managed \
  >"$tmp/hserverctl-context-add.txt"
[[ $(stat -c '%a' "$cli_contexts") == 600 ]] || die "Managed lifecycle hserverctl context file is not mode 0600."
/usr/local/bin/hserverctl context current >"$tmp/hserverctl-context-current.json"
python3 - "$tmp/hserverctl-context-current.json" "$cli_token" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    current = json.load(handle)
if current.get("name") != "managed" or current.get("server") != "http://127.0.0.1:3085":
    raise SystemExit(f"managed lifecycle current context is invalid: {current}")
if current.get("token_file") != sys.argv[2] or current.get("current") is not True:
    raise SystemExit(f"managed lifecycle token context is invalid: {current}")
PY
progress "CLI context authenticated"

progress "prepare signed agent release feed"
printf '%s\n' '{"completed":true,"step":5}' >"$tmp/onboarding.json"
curl -fsS --max-time 5 \
  -H "@$auth_header" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/onboarding.json" \
  http://127.0.0.1:3085/api/onboarding >/dev/null

install -d -m 0700 "$feed_dir"
"$root_dir/scripts/package-release.sh" \
  "$upgrade_version" "$arch" \
  "$package_dir/hserver-panel" "$upgrade_agent" "$package_dir/hserverctl" "$feed_dir" >/dev/null
upgrade_archive="$feed_dir/hserver-panel-${upgrade_version}-linux-${arch}.tar.gz"
upgrade_sha=$(sha256sum "$upgrade_archive" | awk '{print $1}')
upgrade_size=$(wc -c <"$upgrade_archive" | tr -d ' ')
python3 - "$feed_dir/release-manifest.json" "$upgrade_version" "$arch" "$upgrade_sha" "$upgrade_size" <<'PY'
import datetime
import json
import sys

output, version, arch, digest, size = sys.argv[1:]
manifest = {
    "schema_version": 1,
    "version": version,
    "published_at": datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "artifacts": {
        f"linux_{arch}": {
            "url": f"http://127.0.0.1:38086/hserver-panel-{version}-linux-{arch}.tar.gz",
            "sha256": digest,
            "size_bytes": int(size),
        }
    },
}
with open(output, "w", encoding="utf-8") as handle:
    json.dump(manifest, handle, indent=2)
    handle.write("\n")
PY
"$root_dir/scripts/generate-release-signing-key.sh" "$tmp/release-private.pem" "$tmp/release-public.b64" >/dev/null
"$root_dir/scripts/sign-release-manifest.sh" \
  "$feed_dir/release-manifest.json" "$tmp/release-private.pem" "$feed_dir/release-manifest.json.sig" >/dev/null
public_key=$(tr -d '\r\n' <"$tmp/release-public.b64")

python3 -m http.server 38086 --bind 127.0.0.1 --directory "$feed_dir" >"$tmp/feed.log" 2>&1 &
feed_pid=$!
for _ in {1..20}; do
  if curl -fsS --max-time 1 http://127.0.0.1:38086/release-manifest.json >/dev/null; then
    break
  fi
  sleep 1
done
curl -fsS --max-time 2 http://127.0.0.1:38086/release-manifest.json >/dev/null || die "Signed release feed did not become reachable."
progress "signed agent release feed ready"

progress "register node and install managed agent"
printf '{"id":"%s","name":"Native Agent %s"}\n' "$node_id" "$arch" >"$tmp/node-register.json"
curl -fsS --max-time 5 \
  -H "@$auth_header" \
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
    raise SystemExit("registered node identity is incorrect")
token = response.get("token")
if not isinstance(token, str) or not token:
    raise SystemExit("node registration did not return a one-time token")
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    handle.write(token + "\n")
PY
chmod 0600 "$tmp/node-register-response.json" "$tmp/agent.token"

cat >"$tmp/agent.env" <<EOF
HSERVER_AGENT_HUB_URL=http://127.0.0.1:3085
HSERVER_AGENT_NODE_ID=$node_id
HSERVER_AGENT_TOKEN_FILE=/etc/hserver-agent.token
HSERVER_AGENT_INTERVAL=5s
HSERVER_AGENT_ALLOW_TERMINAL=true
HSERVER_AGENT_ALLOW_UPDATE_READ=true
HSERVER_AGENT_ALLOW_UPDATE_ACTIONS=true
HSERVER_AGENT_UPDATE_MANIFEST_URL=http://127.0.0.1:38086/release-manifest.json
HSERVER_AGENT_UPDATE_MANIFEST_PUBLIC_KEYS=$public_key
HSERVER_AGENT_STATE_DIR=/var/lib/hserver-agent
HSERVER_AGENT_LIFECYCLE_INSTALLER=/usr/local/libexec/hserver-agent-install
HSERVER_AGENT_SYSTEMD_RUN_BINARY=/usr/bin/systemd-run
HSERVER_AGENT_SYSTEMCTL_BINARY=/usr/bin/systemctl
EOF
chmod 0600 "$tmp/agent.env"

agent_touched=1
"$package_dir/agent-install.sh" install \
  --binary "$package_dir/hserver-agent" \
  --config "$tmp/agent.env" \
  --token-file "$tmp/agent.token"
systemctl is-active --quiet hserver-agent
[[ $(/usr/local/bin/hserver-agent --version) == "hserver-agent $version" ]] || die "Installed agent version is incorrect."
config_sha=$(sha256sum /etc/hserver-agent.env | awk '{print $1}')
token_sha=$(sha256sum /etc/hserver-agent.token | awk '{print $1}')
progress "managed agent installed"

wait_for_node_version() {
  local expected=$1
  local response="$tmp/node-state.json"
  for _ in {1..60}; do
    if curl -fsS --max-time 4 -H "@$auth_header" -o "$response" \
      "http://127.0.0.1:3085/api/nodes/$node_id" && \
      python3 - "$response" "$expected" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    node = json.load(handle)
required = {"inventory", "terminal", "agent.update.read", "agent.update.action"}
if node.get("agent_version") != sys.argv[2] or not node.get("last_seen_at"):
    raise SystemExit(1)
if node.get("online") is not True:
    raise SystemExit(1)
if not required.issubset(set(node.get("capabilities", []))):
    raise SystemExit(1)
PY
    then
      return 0
    fi
    sleep 2
  done
  die "Agent heartbeat did not report version $expected."
}

wait_for_node_offline() {
  local expected_last_seen=$1
  local response="$tmp/node-offline-state.json"
  for _ in {1..40}; do
    if curl -fsS --max-time 4 -H "@$auth_header" -o "$response" \
      "http://127.0.0.1:3085/api/nodes/$node_id" && \
      python3 - "$response" "$expected_last_seen" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    node = json.load(handle)
if node.get("last_seen_at") != sys.argv[2]:
    raise SystemExit("stopped agent changed its server-observed heartbeat")
if node.get("online") is not False:
    raise SystemExit(1)
PY
    then
      return 0
    fi
    sleep 2
  done
  die "Stopped agent did not become offline without changing its last heartbeat."
}

wait_for_update_status() {
  local expected_current=$1
  local expected_operation=$2
  local expected_operation_status=$3
  local response="$tmp/agent-update-status.json"
  local attempt
  for (( attempt=1; attempt<=managed_status_poll_attempts; attempt++ )); do
    if curl -fsS --max-time "$managed_status_request_timeout" -H "@$auth_header" -o "$response" \
      "http://127.0.0.1:3085/api/nodes/$node_id/agent-update" && \
      python3 - "$response" "$expected_current" "$upgrade_version" "$expected_operation" "$expected_operation_status" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
expected_current, expected_latest, expected_operation, expected_status = sys.argv[2:]
if status.get("release_status") != "healthy" or status.get("signature_status") != "verified":
    raise SystemExit(1)
if status.get("current_version") != expected_current or status.get("latest_version") != expected_latest:
    raise SystemExit(1)
if status.get("update_available") is not (expected_current != expected_latest):
    raise SystemExit(1)
if status.get("operation", "") != expected_operation or status.get("operation_status") != expected_status:
    raise SystemExit(1)
if expected_operation == "upgrade" and status.get("rollback_available") is not True:
    raise SystemExit(1)
PY
    then
      return 0
    fi
    if (( attempt < managed_status_poll_attempts )); then
      sleep "$managed_status_poll_interval"
    fi
  done
  die "Agent lifecycle status did not reach ${expected_operation_status}."
}

progress "verify heartbeat, update status, doctor, and terminal"
wait_for_node_version "$version"
wait_for_update_status "$version" "" "idle"
/usr/local/bin/hserverctl updates agent status --node "$node_id" --wait 50s \
  >"$tmp/hserverctl-agent-update-status.json"
python3 - "$tmp/hserverctl-agent-update-status.json" "$version" "$upgrade_version" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if status.get("release_status") != "healthy" or status.get("signature_status") != "verified":
    raise SystemExit(f"packaged hserverctl agent update status is not verified and healthy: {status}")
if status.get("current_version") != sys.argv[2] or status.get("latest_version") != sys.argv[3]:
    raise SystemExit(f"packaged hserverctl agent update status returned the wrong releases: {status}")
if status.get("latest_version_state") != "ahead" or status.get("update_available") is not True:
    raise SystemExit(f"packaged hserverctl agent update status did not report an available update: {status}")
if status.get("operation_status") != "idle" or status.get("rollback_available") is not False:
    raise SystemExit(f"packaged hserverctl agent update status returned an invalid initial lifecycle state: {status}")
PY

/usr/local/bin/hserverctl --timeout 5s doctor \
  --node "$node_id" \
  --require-architecture "$arch" \
  --require-capability terminal \
  --require-capability agent.update.read \
  >"$tmp/hserverctl-managed-doctor.json"
python3 - "$tmp/hserverctl-managed-doctor.json" "$node_id" "$version" "$arch" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    doctor = json.load(handle)
if doctor.get("schema_version") != 1 or doctor.get("ok") is not True:
    raise SystemExit(f"packaged hserverctl managed doctor did not pass: {doctor}")
if doctor.get("server") != "http://127.0.0.1:3085":
    raise SystemExit(f"packaged hserverctl managed doctor used an unexpected server: {doctor}")
node = doctor.get("node", {})
if node.get("id") != sys.argv[2] or node.get("online") is not True:
    raise SystemExit(f"packaged hserverctl managed doctor returned an invalid node: {doctor}")
if node.get("agent_version") != sys.argv[3]:
    raise SystemExit(f"packaged hserverctl managed doctor returned the wrong agent version: {doctor}")
if node.get("architecture") != sys.argv[4]:
    raise SystemExit(f"packaged hserverctl managed doctor returned the wrong agent architecture: {doctor}")
required = {"terminal", "agent.update.read"}
if not required.issubset(set(node.get("capabilities", []))):
    raise SystemExit(f"packaged hserverctl managed doctor omitted required capabilities: {doctor}")
checks = {item.get("name"): item.get("status") for item in doctor.get("checks", [])}
expected_checks = {
    "panel.health": "pass",
    "authentication": "pass",
    "node.status": "pass",
    "node.architecture": "pass",
    "node.capability.agent.update.read": "pass",
    "node.capability.terminal": "pass",
}
if checks != expected_checks:
    raise SystemExit(f"packaged hserverctl managed doctor returned invalid checks: {doctor}")
PY

managed_terminal_marker="__HSERVER_MANAGED_CLI_TERMINAL_${arch}_OK__"
managed_terminal_transcript="$tmp/hserverctl-managed-terminal.log"
printf -v managed_terminal_cli '%q --timeout 5s terminal --node %q' \
  /usr/local/bin/hserverctl "$node_id"
managed_terminal_ready=0
for _ in {1..10}; do
  if printf 'stty -echo; printf "%%s\\n" %q; exit\n' "$managed_terminal_marker" \
    | timeout 30s script -qefc "$managed_terminal_cli" "$managed_terminal_transcript" >/dev/null \
    && grep -aFq "$managed_terminal_marker" "$managed_terminal_transcript"
  then
    managed_terminal_ready=1
    break
  fi
  sleep 1
done
(( managed_terminal_ready )) || die "Packaged hserverctl managed terminal did not return its PTY marker."
progress "managed agent read-only lifecycle checks passed"

progress "verify offline and denied capability boundaries"
systemctl stop hserver-agent
if systemctl is-active --quiet hserver-agent; then
  die "Managed agent remained active after systemctl stop."
fi
curl -fsS --max-time 4 -H "@$auth_header" -o "$tmp/node-stopped-state.json" \
  "http://127.0.0.1:3085/api/nodes/$node_id"
stopped_last_seen=$(python3 - "$tmp/node-stopped-state.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    last_seen = json.load(handle).get("last_seen_at")
if not isinstance(last_seen, str) or not last_seen:
    raise SystemExit("stopped agent has no server-observed heartbeat")
print(last_seen)
PY
)
wait_for_node_offline "$stopped_last_seen"

curl -fsS --max-time 4 -H "@$auth_header" -o "$tmp/tasks-before-offline-request.json" \
  "http://127.0.0.1:3085/api/nodes/$node_id/tasks?limit=100"
printf '%s\n' '{"kind":"agent.update.status","payload":{},"confirmed":true}' >"$tmp/offline-task.json"
offline_code=$(curl -sS --max-time 5 \
  -H "@$auth_header" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/offline-task.json" \
  -o "$tmp/offline-task-response.json" \
  -w '%{http_code}' \
  "http://127.0.0.1:3085/api/nodes/$node_id/tasks")
[[ "$offline_code" == 409 ]] || die "Offline managed node accepted a task with HTTP $offline_code instead of 409."
curl -fsS --max-time 4 -H "@$auth_header" -o "$tmp/tasks-after-offline-request.json" \
  "http://127.0.0.1:3085/api/nodes/$node_id/tasks?limit=100"
python3 - "$tmp/offline-task-response.json" "$tmp/tasks-before-offline-request.json" "$tmp/tasks-after-offline-request.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    error = json.load(handle).get("error", "")
if "offline" not in error.lower():
    raise SystemExit("offline task rejection did not explain the connectivity boundary")
with open(sys.argv[2], encoding="utf-8") as handle:
    before = json.load(handle)
with open(sys.argv[3], encoding="utf-8") as handle:
    after = json.load(handle)
if len(after) != len(before):
    raise SystemExit("offline task rejection changed the persisted task count")
PY

systemctl start hserver-agent
wait_for_node_version "$version"
systemctl is-active --quiet hserver-agent

curl -fsS --max-time 4 -H "@$auth_header" -o "$tmp/tasks-before-disabled-request.json" \
  "http://127.0.0.1:3085/api/nodes/$node_id/tasks?limit=100"
disabled_code=$(curl -sS --max-time 5 \
  -H "@$auth_header" \
  -o "$tmp/disabled-action.json" \
  -w '%{http_code}' \
  -X POST \
  "http://127.0.0.1:3085/api/nodes/$node_id/actions/memory-optimize")
[[ "$disabled_code" == 409 ]] || die "Disabled host capability returned HTTP $disabled_code instead of 409."
curl -fsS --max-time 4 -H "@$auth_header" -o "$tmp/tasks-after-disabled-request.json" \
  "http://127.0.0.1:3085/api/nodes/$node_id/tasks?limit=100"
python3 - "$tmp/disabled-action.json" "$tmp/tasks-before-disabled-request.json" "$tmp/tasks-after-disabled-request.json" <<'PY'
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
    raise SystemExit("disabled capability rejection changed the persisted task count")
PY

progress "run managed agent upgrade"
/usr/local/bin/hserverctl updates agent upgrade --confirm --node "$node_id" --wait 2m \
  >"$tmp/upgrade-response.json"
python3 - "$tmp/upgrade-response.json" "$upgrade_version" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if status.get("operation") != "upgrade" or status.get("operation_status") != "scheduled" or status.get("operation_version") != sys.argv[2]:
    raise SystemExit("managed agent upgrade was not scheduled")
PY
wait_for_node_version "$upgrade_version"
systemctl is-active --quiet hserver-agent
[[ $(/usr/local/bin/hserver-agent --version) == "hserver-agent $upgrade_version" ]] || die "Managed upgrade did not install the expected agent."
[[ $(sha256sum /etc/hserver-agent.env | awk '{print $1}') == "$config_sha" ]] || die "Managed upgrade changed agent configuration."
[[ $(sha256sum /etc/hserver-agent.token | awk '{print $1}') == "$token_sha" ]] || die "Managed upgrade changed agent token."
wait_for_update_status "$upgrade_version" "upgrade" "completed"
progress "managed agent upgrade verified"

progress "run managed agent rollback"
/usr/local/bin/hserverctl updates agent rollback --confirm --node "$node_id" --wait 2m \
  >"$tmp/rollback-response.json"
python3 - "$tmp/rollback-response.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if status.get("operation") != "rollback" or status.get("operation_status") != "scheduled":
    raise SystemExit("managed agent rollback was not scheduled")
PY
wait_for_node_version "$version"
systemctl is-active --quiet hserver-agent
[[ $(/usr/local/bin/hserver-agent --version) == "hserver-agent $version" ]] || die "Managed rollback did not restore the packaged agent."
[[ $(sha256sum /etc/hserver-agent.env | awk '{print $1}') == "$config_sha" ]] || die "Managed rollback changed agent configuration."
[[ $(sha256sum /etc/hserver-agent.token | awk '{print $1}') == "$token_sha" ]] || die "Managed rollback changed agent token."
wait_for_update_status "$version" "rollback" "completed"
progress "managed agent rollback verified"

progress "verify failed-upgrade recovery and uninstall cleanup"
cat >"$tmp/crash-agent" <<'EOF'
#!/usr/bin/env sh
exit 1
EOF
chmod 0755 "$tmp/crash-agent"
if HSERVER_AGENT_HEALTH_TIMEOUT=8 /usr/local/libexec/hserver-agent-install upgrade --binary "$tmp/crash-agent"; then
  die "Crash-looping agent upgrade unexpectedly succeeded."
fi
systemctl is-active --quiet hserver-agent
[[ $(/usr/local/bin/hserver-agent --version) == "hserver-agent $version" ]] || die "Failed upgrade did not restore the previous agent."
[[ $(sha256sum /etc/hserver-agent.env | awk '{print $1}') == "$config_sha" ]] || die "Failed upgrade changed agent configuration."
[[ $(sha256sum /etc/hserver-agent.token | awk '{print $1}') == "$token_sha" ]] || die "Failed upgrade changed agent token."

timeout --signal=TERM --kill-after=5s 60s /usr/local/libexec/hserver-agent-install uninstall \
  || die "Agent non-purge uninstall timed out or failed."
[[ ! -e /usr/local/bin/hserver-agent && ! -e /usr/local/libexec/hserver-agent-install && ! -e /etc/systemd/system/hserver-agent.service ]] \
  || die "Agent non-purge uninstall left executable lifecycle assets."
[[ -f /etc/hserver-agent.env && -f /etc/hserver-agent.token && -d /var/lib/hserver-agent ]] \
  || die "Agent non-purge uninstall did not preserve configuration, token, and state."
timeout --signal=TERM --kill-after=5s 60s "$package_dir/agent-install.sh" uninstall --purge-config >/dev/null \
  || die "Agent purge uninstall timed out or failed."
timeout --signal=TERM --kill-after=5s 30s find /var/lib/hserver-agent -xdev -depth -delete \
  || die "Agent state cleanup timed out or failed."
agent_touched=0
[[ ! -e /etc/hserver-agent.env && ! -e /etc/hserver-agent.token && ! -e /var/lib/hserver-agent ]] \
  || die "Agent purge cleanup did not remove its owned configuration and state."

timeout --signal=TERM --kill-after=5s 60s "$package_dir/install.sh" uninstall --purge-config --purge-data >/dev/null \
  || die "Panel purge uninstall timed out or failed."
panel_installed=0
[[ ! -e /etc/hserver && ! -e /var/lib/hserver && ! -e /usr/local/bin/hserver-panel ]] \
  || die "Panel purge cleanup did not remove the disposable installation."

progress "managed-agent lifecycle acceptance complete"
printf 'native managed-agent lifecycle acceptance: OK (%s)\n' "$arch"
