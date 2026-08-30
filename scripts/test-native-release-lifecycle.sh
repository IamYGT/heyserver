#!/usr/bin/env bash
set -euo pipefail

if [[ ${HSERVER_ACCEPT_DISPOSABLE_HOST:-0} != 1 || ${CI:-false} != true ]]; then
  echo "Refusing native lifecycle mutation outside an explicitly disposable CI host." >&2
  exit 1
fi
if (( EUID != 0 )); then
  echo "Native lifecycle acceptance must run as root." >&2
  exit 1
fi
if [[ $# -ne 7 ]]; then
  echo "Usage: $0 VERSION ARCH ARCHIVE CHECKSUM UPGRADE_VERSION UPGRADE_ARCHIVE UPGRADE_CHECKSUM" >&2
  exit 2
fi

version=$1
arch=$2
archive=$3
checksum=$4
upgrade_version=$5
upgrade_archive=$6
upgrade_checksum=$7
root_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

python3 - "$version" "$upgrade_version" <<'PY'
import re
import sys

def stable(value):
    match = re.fullmatch(r"v?(\d+)\.(\d+)\.(\d+)", value)
    if not match:
        raise SystemExit(f"version is not stable major.minor.patch: {value}")
    return tuple(map(int, match.groups()))

if stable(sys.argv[2]) <= stable(sys.argv[1]):
    raise SystemExit("upgrade version must be newer than the packaged version")
PY

case "$(uname -m):$arch" in
  x86_64:amd64|aarch64:arm64|arm64:arm64) ;;
  *) echo "Native runner architecture $(uname -m) does not match package $arch." >&2; exit 1 ;;
esac
if [[ -e /usr/local/bin/hserver-panel || -e /usr/local/bin/hserverctl || \
  -e /usr/local/libexec/hserver-install || -e /usr/local/libexec/hserver-doctor || \
  -e /usr/local/share/hserver || -e /etc/hserver || -e /var/lib/hserver || \
  -e /etc/systemd/system/hserver.service ]]; then
  echo "Refusing to overwrite a pre-existing HServer installation." >&2
  exit 1
fi
if compgen -G '/etc/nginx/snippets/hserver-*.conf' >/dev/null; then
  echo "Refusing to overwrite pre-existing HServer Nginx snippets." >&2
  exit 1
fi
if curl -fsS --max-time 1 http://127.0.0.1:3085/api/health >/dev/null 2>&1; then
  echo "Refusing to use occupied HServer port 3085." >&2
  exit 1
fi
tmp=$(mktemp -d /tmp/hserver-native-acceptance-XXXXXXXX)
umask 077
package_dir="$tmp/hserver-panel-${version}-linux-${arch}"
vhosts_root="$tmp/vhosts"
restore_probe="$vhosts_root/hserver-native-restore-probe"
installed=0
restore_probe_created=0
feed_pid=
cleanup() {
  if [[ -n "$feed_pid" ]]; then
    kill "$feed_pid" >/dev/null 2>&1 || true
    wait "$feed_pid" 2>/dev/null || true
  fi
  if (( installed )); then
    if [[ -x "$package_dir/install.sh" ]]; then
      HSERVER_HEALTH_TIMEOUT=3 "$package_dir/install.sh" uninstall --purge-config --purge-data >/dev/null 2>&1 || true
    elif [[ -x /usr/local/libexec/hserver-install ]]; then
      HSERVER_HEALTH_TIMEOUT=3 /usr/local/libexec/hserver-install uninstall --purge-config --purge-data >/dev/null 2>&1 || true
    fi
  fi
  if (( restore_probe_created )); then
    rm -rf -- "$restore_probe"
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

run_retained_installer() {
  HSERVER_HEALTH_TIMEOUT=30 /usr/local/libexec/hserver-install "$@"
}

capture_native_nginx_assets() {
  local directory=$1
  local output=$2
  [[ -d "$directory" ]] || {
    echo "Native Nginx asset directory is missing: $directory" >&2
    return 1
  }
  compgen -G "$directory/hserver-*.conf" >/dev/null || {
    echo "Native Nginx asset directory has no managed snippets: $directory" >&2
    return 1
  }
  (
    cd "$directory"
    sha256sum hserver-*.conf | sort
  ) >"$output"
}

"$root_dir/scripts/verify-release-archive.sh" "$version" "$arch" "$archive" "$checksum"
"$root_dir/scripts/verify-release-archive.sh" \
  "$upgrade_version" "$arch" "$upgrade_archive" "$upgrade_checksum"
tar -xzf "$archive" -C "$tmp"
installed=1

cat >"$tmp/unhealthy-initial-panel" <<'EOF'
#!/usr/bin/env sh
exit 1
EOF
chmod 0755 "$tmp/unhealthy-initial-panel"
if HSERVER_HEALTH_TIMEOUT=3 "$package_dir/install.sh" install \
  --binary "$tmp/unhealthy-initial-panel" \
  --cli-binary "$package_dir/hserverctl"; then
  echo "An unhealthy initial installation unexpectedly succeeded." >&2
  exit 1
fi
[[ ! -e /usr/local/bin/hserver-panel ]]
[[ ! -e /usr/local/bin/hserverctl ]]
[[ ! -e /etc/systemd/system/hserver.service ]]
[[ ! -e /etc/hserver ]]
[[ ! -e /var/lib/hserver ]]
if compgen -G '/etc/nginx/snippets/hserver-*.conf' >/dev/null; then
  echo "Failed initial installation left managed Nginx snippets behind." >&2
  exit 1
fi
if systemctl is-active --quiet hserver || systemctl is-enabled --quiet hserver; then
  echo "Failed initial installation left the HServer service active or enabled." >&2
  exit 1
fi
printf 'native initial-install rollback acceptance: OK (%s)\n' "$arch"

feed_dir="$tmp/release-feed"
mkdir -m 0700 "$feed_dir"
archive_name=$(basename "$archive")
cp "$archive" "$feed_dir/$archive_name"
upgrade_archive_name=$(basename "$upgrade_archive")
cp "$upgrade_archive" "$feed_dir/$upgrade_archive_name"
archive_sha=$(sha256sum "$archive" | awk '{print $1}')
archive_size=$(stat -c '%s' "$archive")
python3 - "$feed_dir/release-manifest.json" "$version" "$arch" "$archive_name" "$archive_sha" "$archive_size" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
document = {
    "schema_version": 1,
    "version": sys.argv[2],
    "published_at": "2026-01-01T00:00:00Z",
    "artifacts": {
        f"linux_{sys.argv[3]}": {
            "url": f"http://127.0.0.1:38085/{sys.argv[4]}",
            "sha256": sys.argv[5],
            "size_bytes": int(sys.argv[6]),
        }
    },
}
path.write_text(json.dumps(document, separators=(",", ":")), encoding="utf-8")
PY
"$root_dir/scripts/generate-release-signing-key.sh" \
  "$tmp/release-private.pem" "$tmp/release-public.b64" >/dev/null
"$root_dir/scripts/sign-release-manifest.sh" \
  "$feed_dir/release-manifest.json" \
  "$tmp/release-private.pem" \
  "$feed_dir/release-manifest.json.sig" >/dev/null
if curl -fsS --max-time 1 http://127.0.0.1:38085/release-manifest.json >/dev/null 2>&1; then
  echo "Refusing to use occupied bootstrap feed port 38085." >&2
  exit 1
fi
python3 -m http.server 38085 --bind 127.0.0.1 --directory "$feed_dir" \
  >"$tmp/release-feed.log" 2>&1 &
feed_pid=$!
for _ in {1..30}; do
  if curl -fsS --max-time 1 http://127.0.0.1:38085/release-manifest.json >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
curl -fsS --max-time 2 http://127.0.0.1:38085/release-manifest.json >/dev/null \
  || { cat "$tmp/release-feed.log" >&2; exit 1; }
"$root_dir/scripts/bootstrap-install.sh" \
  --manifest-url http://127.0.0.1:38085/release-manifest.json \
  --public-key "$(<"$tmp/release-public.b64")" \
  --vhosts-root "$vhosts_root"
curl -fsS --max-time 3 http://127.0.0.1:3085/api/health >/dev/null
cmp -s "$package_dir/hserverctl" /usr/local/bin/hserverctl
cmp -s "$package_dir/install.sh" /usr/local/libexec/hserver-install
cmp -s "$package_dir/doctor.sh" /usr/local/libexec/hserver-doctor
for snippet in "$package_dir"/nginx-snippets/hserver-*.conf; do
  cmp -s "$snippet" "/usr/local/share/hserver/nginx-snippets/$(basename "$snippet")"
done
/usr/local/libexec/hserver-doctor installed >/dev/null
/usr/local/libexec/hserver-install next-steps >"$tmp/next-steps.txt"
grep -q 'ssh -N -L 3085:127.0.0.1:3085 YOUR_SSH_USER@YOUR_SERVER' "$tmp/next-steps.txt"
grep -q 'Open http://127.0.0.1:3085 in your browser.' "$tmp/next-steps.txt"
/usr/local/bin/hserverctl health >/dev/null

admin_email_file="$tmp/admin-email.txt"
admin_password_file="$tmp/admin-password.txt"
cli_token="$tmp/hserverctl-token"
cli_contexts="$tmp/hserverctl-contexts.json"
auth_header="$tmp/auth-header.txt"
python3 - /etc/hserver/hserver.env "$admin_email_file" "$admin_password_file" <<'PY'
import sys

values = {}
with open(sys.argv[1], encoding="utf-8") as handle:
    for line in handle:
        line = line.rstrip("\n")
        if "=" in line and not line.startswith("#"):
            key, value = line.split("=", 1)
            values[key] = value
for key in ("HSERVER_ADMIN_EMAIL", "HSERVER_ADMIN_PASS"):
    if not values.get(key):
        raise SystemExit(f"native installation did not configure {key}")
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    handle.write(values["HSERVER_ADMIN_EMAIL"] + "\n")
with open(sys.argv[3], "w", encoding="utf-8") as handle:
    handle.write(values["HSERVER_ADMIN_PASS"] + "\n")
PY
admin_email=$(<"$admin_email_file")
if grep -Fq "$(<"$admin_password_file")" "$tmp/next-steps.txt"; then
  echo "First-access guidance exposed the generated administrator password." >&2
  exit 1
fi
export HSERVER_CONTEXT_FILE="$cli_contexts"
/usr/local/bin/hserverctl \
  --timeout 5s \
  connect \
  --server http://127.0.0.1:3085 \
  --token-file "$cli_token" \
  --email "$admin_email" \
  --password-file "$admin_password_file" \
  native \
  >"$tmp/hserverctl-connect.txt"
[[ $(stat -c '%a' "$cli_contexts") == 600 ]] || {
  echo "hserverctl context file is not mode 0600." >&2
  exit 1
}
/usr/local/bin/hserverctl context current >"$tmp/hserverctl-context-current.json"
python3 - "$tmp/hserverctl-context-current.json" "$cli_token" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    current = json.load(handle)
expected = {
    "name": "native",
    "server": "http://127.0.0.1:3085",
    "token_file": sys.argv[2],
    "current": True,
}
if current != expected:
    raise SystemExit(f"hserverctl current context is invalid: {current}")
PY
[[ $(stat -c '%a' "$cli_token") == 600 ]] || {
  echo "hserverctl token file is not mode 0600." >&2
  exit 1
}
python3 - "$cli_token" "$auth_header" <<'PY'
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    token = handle.read().strip()
if not token:
    raise SystemExit("hserverctl token file was empty")
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    handle.write("Authorization: Bearer " + token + "\n")
PY

/usr/local/bin/hserverctl --timeout 5s \
  host status >"$tmp/hserverctl-host-status.json"
/usr/local/bin/hserverctl --timeout 30s \
  disk scan >"$tmp/hserverctl-disk-scan.json"
/usr/local/bin/hserverctl --timeout 5s \
  doctor --output "$tmp/hserverctl-doctor.json" \
  >"$tmp/hserverctl-doctor-receipt.txt"
[[ $(stat -c '%a' "$tmp/hserverctl-doctor.json") == 600 ]] || {
  echo "packaged hserverctl doctor report is not mode 0600." >&2
  exit 1
}
/usr/local/bin/hserverctl --timeout 5s \
  updates status >"$tmp/hserverctl-update-status.json"
/usr/local/bin/hserverctl --timeout 5s \
  updates stage-status >"$tmp/hserverctl-update-stage.json"
python3 - \
  "$tmp/hserverctl-host-status.json" \
  "$tmp/hserverctl-disk-scan.json" \
  "$tmp/hserverctl-doctor.json" \
  "$tmp/hserverctl-update-status.json" \
  "$tmp/hserverctl-update-stage.json" \
  "$version" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if not isinstance(status, dict) or status.get("running") is not False:
    raise SystemExit(f"hserverctl host status did not report an idle host: {status}")

with open(sys.argv[2], encoding="utf-8") as handle:
    targets = json.load(handle)
if not isinstance(targets, list):
    raise SystemExit("hserverctl disk scan did not return a target list")
for target in targets:
    if not isinstance(target, dict) or not isinstance(target.get("id"), str) or not target["id"]:
        raise SystemExit(f"hserverctl disk scan returned an invalid target: {target}")
    if not isinstance(target.get("size"), int) or target["size"] < 0:
        raise SystemExit(f"hserverctl disk scan returned an invalid size: {target}")

with open(sys.argv[3], encoding="utf-8") as handle:
    doctor = json.load(handle)
if doctor.get("schema_version") != 1 or doctor.get("ok") is not True:
    raise SystemExit(f"packaged hserverctl doctor did not pass: {doctor}")
if doctor.get("server") != "http://127.0.0.1:3085":
    raise SystemExit(f"packaged hserverctl doctor used an unexpected server: {doctor}")
panel = doctor.get("panel", {})
if panel.get("status") != "ok" or not isinstance(panel.get("version"), str) or not panel["version"]:
    raise SystemExit(f"packaged hserverctl doctor returned an invalid panel summary: {doctor}")
if doctor.get("account") != {"role": "admin", "totp_enabled": False}:
    raise SystemExit(f"packaged hserverctl doctor returned an invalid account summary: {doctor}")
if doctor.get("fleet") != {"observed": 0, "online": 0, "offline": 0}:
    raise SystemExit(f"packaged hserverctl doctor returned an invalid fresh fleet summary: {doctor}")
checks = {item.get("name"): item.get("status") for item in doctor.get("checks", [])}
expected_checks = {"panel.health": "pass", "authentication": "pass", "fleet.inventory": "pass"}
if checks != expected_checks:
    raise SystemExit(f"packaged hserverctl doctor returned invalid checks: {doctor}")

with open(sys.argv[4], encoding="utf-8") as handle:
    update = json.load(handle)
expected_version = sys.argv[6]
if update.get("status") != "healthy" or update.get("signature_status") != "verified":
    raise SystemExit(f"packaged hserverctl update status is not verified and healthy: {update}")
if update.get("current_version") != expected_version or update.get("latest_version") != expected_version:
    raise SystemExit(f"packaged hserverctl update status returned the wrong release: {update}")
if update.get("latest_version_state") != "current" or update.get("update_available") is not False:
    raise SystemExit(f"packaged hserverctl update status did not report the installed release as current: {update}")

with open(sys.argv[5], encoding="utf-8") as handle:
    stage = json.load(handle)
if stage != {"stage": None}:
    raise SystemExit(f"fresh native installation unexpectedly has an update stage: {stage}")
PY

printf '%s\n' '{"completed":true,"step":5}' >"$tmp/onboarding.json"
curl -fsS --max-time 5 \
  -H "@$auth_header" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/onboarding.json" \
  http://127.0.0.1:3085/api/onboarding >/dev/null
curl -fsS --max-time 5 -H "@$auth_header" \
  -o "$tmp/onboarding-response.json" \
  http://127.0.0.1:3085/api/onboarding
python3 - "$tmp/onboarding-response.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
if state.get("completed") is not True or state.get("step") != 5:
    raise SystemExit("onboarding state was not persisted")
PY

# The installed lifecycle tool must be usable after the release extraction
# disappears. Copy the upgrade pair out of a disposable extraction, remove
# both extracted package trees, and drive the real host through the retained
# /usr/local/libexec/hserver-install path.
native_nginx_snippets_dir=/etc/nginx/snippets
native_lifecycle_snippets_dir=/usr/local/share/hserver/nginx-snippets
capture_native_nginx_assets "$native_nginx_snippets_dir" \
  "$tmp/native-nginx-assets-before-retained-upgrade.sha256"
capture_native_nginx_assets "$native_lifecycle_snippets_dir" \
  "$tmp/native-lifecycle-assets-before-retained-upgrade.sha256"
retained_installer_checksum=$(sha256sum /usr/local/libexec/hserver-install | awk '{print $1}')

retained_upgrade_root="$tmp/retained-upgrade-extract"
mkdir -m 0700 "$retained_upgrade_root"
tar -xzf "$upgrade_archive" -C "$retained_upgrade_root"
retained_upgrade_package_dir="$retained_upgrade_root/hserver-panel-${upgrade_version}-linux-${arch}"
[[ -x "$retained_upgrade_package_dir/hserver-panel" && -x "$retained_upgrade_package_dir/hserverctl" ]] || {
  echo "Retained native upgrade package did not contain the panel/CLI pair." >&2
  exit 1
}
retained_upgrade_binary="$tmp/retained-hserver-panel-${upgrade_version}"
retained_upgrade_cli="$tmp/retained-hserverctl-${upgrade_version}"
install -m 0755 "$retained_upgrade_package_dir/hserver-panel" "$retained_upgrade_binary"
install -m 0755 "$retained_upgrade_package_dir/hserverctl" "$retained_upgrade_cli"
rm -rf -- "$package_dir" "$retained_upgrade_root"
[[ ! -e "$package_dir" && ! -e "$retained_upgrade_root" ]] || {
  echo "Native retained lifecycle test still had an extracted release package." >&2
  exit 1
}

run_retained_installer upgrade --binary "$retained_upgrade_binary" --cli-binary "$retained_upgrade_cli" \
  >"$tmp/retained-upgrade.log"
retained_upgrade_panel_identity=$(/usr/local/bin/hserver-panel --version)
retained_upgrade_cli_identity=$(/usr/local/bin/hserverctl version)
[[ $retained_upgrade_panel_identity == "hserver-panel $upgrade_version (commit "* ]] || {
  echo "Retained installer upgrade installed the wrong panel version: $retained_upgrade_panel_identity" >&2
  exit 1
}
[[ $retained_upgrade_cli_identity == "hserverctl $upgrade_version ("* ]] || {
  echo "Retained installer upgrade installed the wrong CLI version: $retained_upgrade_cli_identity" >&2
  exit 1
}
[[ -x /usr/local/libexec/hserver-install ]] || {
  echo "Retained lifecycle installer disappeared during upgrade." >&2
  exit 1
}
[[ $(sha256sum /usr/local/libexec/hserver-install | awk '{print $1}') == "$retained_installer_checksum" ]] || {
  echo "Retained lifecycle installer changed during binary-pair upgrade." >&2
  exit 1
}
systemctl is-active --quiet hserver
systemctl is-enabled --quiet hserver
curl -fsS --max-time 3 http://127.0.0.1:3085/api/health >/dev/null
capture_native_nginx_assets "$native_nginx_snippets_dir" \
  "$tmp/native-nginx-assets-after-retained-upgrade.sha256"
capture_native_nginx_assets "$native_lifecycle_snippets_dir" \
  "$tmp/native-lifecycle-assets-after-retained-upgrade.sha256"
cmp -s \
  "$tmp/native-nginx-assets-before-retained-upgrade.sha256" \
  "$tmp/native-nginx-assets-after-retained-upgrade.sha256" || {
  echo "Native Nginx snippets changed during retained installer upgrade." >&2
  exit 1
}
cmp -s \
  "$tmp/native-lifecycle-assets-before-retained-upgrade.sha256" \
  "$tmp/native-lifecycle-assets-after-retained-upgrade.sha256" || {
  echo "Native lifecycle Nginx assets changed during retained installer upgrade." >&2
  exit 1
}
curl -fsS --max-time 5 -H "@$auth_header" \
  -o "$tmp/onboarding-after-retained-upgrade.json" \
  http://127.0.0.1:3085/api/onboarding
python3 - "$tmp/onboarding-after-retained-upgrade.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
if state.get("completed") is not True or state.get("step") != 5:
    raise SystemExit("retained native upgrade did not preserve SQLite onboarding state")
PY
printf 'native retained upgrade acceptance: OK (%s)\n' "$arch"

# Mutating a lifecycle asset before the explicit rollback proves the retained
# snapshot, rather than the extracted package, restores the complete host.
printf '%s\n' retained-upgrade-mutation >"$native_lifecycle_snippets_dir/hserver-security-headers.conf"
run_retained_installer rollback >"$tmp/retained-rollback.log"
retained_rollback_panel_identity=$(/usr/local/bin/hserver-panel --version)
retained_rollback_cli_identity=$(/usr/local/bin/hserverctl version)
[[ $retained_rollback_panel_identity == "hserver-panel $version (commit "* ]] || {
  echo "Explicit retained rollback restored the wrong panel version: $retained_rollback_panel_identity" >&2
  exit 1
}
[[ $retained_rollback_cli_identity == "hserverctl $version ("* ]] || {
  echo "Explicit retained rollback restored the wrong CLI version: $retained_rollback_cli_identity" >&2
  exit 1
}
[[ -x /usr/local/libexec/hserver-install ]] || {
  echo "Retained lifecycle installer disappeared during explicit rollback." >&2
  exit 1
}
[[ $(sha256sum /usr/local/libexec/hserver-install | awk '{print $1}') == "$retained_installer_checksum" ]] || {
  echo "Explicit retained rollback changed the lifecycle installer." >&2
  exit 1
}
systemctl is-active --quiet hserver
systemctl is-enabled --quiet hserver
curl -fsS --max-time 3 http://127.0.0.1:3085/api/health >/dev/null
capture_native_nginx_assets "$native_nginx_snippets_dir" \
  "$tmp/native-nginx-assets-after-retained-rollback.sha256"
capture_native_nginx_assets "$native_lifecycle_snippets_dir" \
  "$tmp/native-lifecycle-assets-after-retained-rollback.sha256"
cmp -s \
  "$tmp/native-nginx-assets-before-retained-upgrade.sha256" \
  "$tmp/native-nginx-assets-after-retained-rollback.sha256" || {
  echo "Explicit retained rollback did not restore native Nginx snippets." >&2
  exit 1
}
cmp -s \
  "$tmp/native-lifecycle-assets-before-retained-upgrade.sha256" \
  "$tmp/native-lifecycle-assets-after-retained-rollback.sha256" || {
  echo "Explicit retained rollback did not restore native lifecycle Nginx assets." >&2
  exit 1
}
curl -fsS --max-time 5 -H "@$auth_header" \
  -o "$tmp/onboarding-after-retained-rollback.json" \
  http://127.0.0.1:3085/api/onboarding
python3 - "$tmp/onboarding-after-retained-rollback.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
if state.get("completed") is not True or state.get("step") != 5:
    raise SystemExit("retained native rollback did not preserve SQLite onboarding state")
PY
printf 'native retained explicit rollback acceptance: OK (%s)\n' "$arch"

# Continue the remainder of the lifecycle against an extracted base package;
# the retained upgrade/rollback above already ran with both package trees
# absent, so this extraction is not part of that acceptance boundary.
tar -xzf "$archive" -C "$tmp"
[[ -x "$package_dir/install.sh" ]] || {
  echo "Native base package could not be re-extracted after retained lifecycle acceptance." >&2
  exit 1
}

terminal_marker=__HSERVER_NATIVE_CLI_TERMINAL_OK__
terminal_transcript="$tmp/hserverctl-terminal.log"
printf -v terminal_cli '%q --timeout 5s terminal' /usr/local/bin/hserverctl
printf 'stty -echo; printf "%%s\\n" %q; exit\n' "$terminal_marker" \
  | timeout 30s script -qefc "$terminal_cli" "$terminal_transcript" >/dev/null
grep -aFq "$terminal_marker" "$terminal_transcript" || {
  echo "Packaged hserverctl terminal did not return its PTY marker." >&2
  exit 1
}

curl -fsS --max-time 120 \
  -X POST \
  -H "@$auth_header" \
  -o "$tmp/temp-clean-response.json" \
  http://127.0.0.1:3085/api/system/actions/temp-clean
python3 - "$tmp/temp-clean-response.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    response = json.load(handle)
message = response.get("message")
if not isinstance(message, str) or not message.strip():
    raise SystemExit("temp-clean acceptance did not return an action result")
PY
curl -fsS --max-time 5 \
  -H "@$auth_header" \
  -o "$tmp/maintenance-status.json" \
  http://127.0.0.1:3085/api/system/actions/status
python3 - "$tmp/maintenance-status.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if status.get("running") is not False:
    raise SystemExit(f"maintenance lock remained active after temp-clean: {status}")
PY

"$package_dir/backup-db.sh" >/dev/null
database_backup=$(find /var/lib/hserver/backups/db -type f -name 'hserver-*.panel-backup.tar.gz' | sort | tail -n 1)
[[ -n "$database_backup" && -s "$database_backup" ]] || {
  echo "Native portable panel-state backup was not created." >&2
  exit 1
}
"$package_dir/restore-db.sh" validate "$database_backup" >/dev/null

printf '%s\n' '{"completed":false,"step":1}' >"$tmp/onboarding-mutated.json"
curl -fsS --max-time 5 \
  -H "@$auth_header" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/onboarding-mutated.json" \
  http://127.0.0.1:3085/api/onboarding >/dev/null
curl -fsS --max-time 5 -H "@$auth_header" \
  -o "$tmp/onboarding-mutated-response.json" \
  http://127.0.0.1:3085/api/onboarding
python3 - "$tmp/onboarding-mutated-response.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
if state.get("completed") is not False or state.get("step") != 1:
    raise SystemExit("post-backup onboarding mutation was not persisted")
PY

"$package_dir/restore-db.sh" restore "$database_backup" --confirm >/dev/null
systemctl is-active --quiet hserver
curl -fsS --max-time 3 http://127.0.0.1:3085/api/health >/dev/null
curl -fsS --max-time 5 -H "@$auth_header" \
  -o "$tmp/onboarding-after-restore.json" \
  http://127.0.0.1:3085/api/onboarding
python3 - "$tmp/onboarding-after-restore.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
if state.get("completed") is not True or state.get("step") != 5:
    raise SystemExit("native SQLite restore did not recover the backed-up onboarding state")
PY
recovery_snapshot=$(find /var/lib/hserver/restores -type f -name 'pre-restore-*.panel-backup.tar.gz' | sort | tail -n 1)
[[ -n "$recovery_snapshot" && -s "$recovery_snapshot" ]] || {
  echo "Native panel-state restore did not create a pre-restore recovery bundle." >&2
  exit 1
}

wait_for_backup_job() {
  local job_id=$1
  local response="$tmp/backup-job-${job_id}.json"
  local status
  for _ in {1..60}; do
    curl -fsS --max-time 5 -H "@$auth_header" -o "$response" \
      "http://127.0.0.1:3085/api/backups/jobs/$job_id"
    status=$(python3 - "$response" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    print(json.load(handle).get("status", ""))
PY
)
    case "$status" in
      completed) return 0 ;;
      failed) echo "Backup job $job_id failed." >&2; return 1 ;;
    esac
    sleep 1
  done
  echo "Backup job $job_id did not reach a terminal state." >&2
  return 1
}

install -d -m 0700 "$restore_probe"
restore_probe_created=1
python3 - "$restore_probe/payload.bin" <<'PY'
import os
import sys

with open(sys.argv[1], "wb") as handle:
    handle.write(os.urandom(65536))
PY
chmod 0600 "$restore_probe/payload.bin"
original_file_sha=$(sha256sum "$restore_probe/payload.bin" | awk '{print $1}')
cat >"$tmp/file-backup.json" <<'EOF'
{"type":"files","name":"native-file-restore","compression":6,"retention":3,"vhosts":["hserver-native-restore-probe"]}
EOF
file_backup_code=$(curl -sS --max-time 10 \
  -H "@$auth_header" \
  -H 'Content-Type: application/json' \
  --data-binary "@$tmp/file-backup.json" \
  -o "$tmp/file-backup-response.json" \
  -w '%{http_code}' \
  http://127.0.0.1:3085/api/backups)
[[ "$file_backup_code" == 202 ]] || {
  echo "File backup returned HTTP $file_backup_code instead of 202." >&2
  echo "File backup response body:" >&2
  cat "$tmp/file-backup-response.json" >&2
  printf '\n' >&2
  exit 1
}
file_backup_job=$(python3 - "$tmp/file-backup-response.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    value = json.load(handle).get("jobId")
if not isinstance(value, str) or not value:
    raise SystemExit("file backup response did not contain a job ID")
print(value)
PY
)
wait_for_backup_job "$file_backup_job"

curl -fsS --max-time 5 -H "@$auth_header" -o "$tmp/backups.json" \
  http://127.0.0.1:3085/api/backups
file_backup_id=$(python3 - "$tmp/backups.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    backups = json.load(handle).get("backups", [])
for backup in backups:
    if backup.get("name") == "native-file-restore-files.tar.gz" and backup.get("status") == "completed":
        print(backup.get("id", ""))
        break
else:
    raise SystemExit("completed native file backup was not listed")
PY
)
[[ -n "$file_backup_id" ]] || {
  echo "Completed file backup did not expose a restore ID." >&2
  exit 1
}
curl -fsS --max-time 10 -H "@$auth_header" -o "$tmp/file-restore-validation.json" \
  "http://127.0.0.1:3085/api/backups/restore/$file_backup_id/validate"
python3 - "$tmp/file-restore-validation.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    validation = json.load(handle)
if validation.get("includesFiles") is not True or validation.get("includesDatabase") is not False:
    raise SystemExit("file restore validation reported an incorrect artifact boundary")
if validation.get("filesRollback") is not True:
    raise SystemExit("file restore validation did not report automatic rollback")
PY

printf '%s\n' 'changed-after-file-backup' >"$restore_probe/payload.bin"
[[ $(sha256sum "$restore_probe/payload.bin" | awk '{print $1}') != "$original_file_sha" ]] || {
  echo "File restore probe mutation did not change the source file." >&2
  exit 1
}
file_restore_code=$(curl -sS --max-time 10 \
  -H "@$auth_header" \
  -o "$tmp/file-restore-response.json" \
  -w '%{http_code}' \
  -X POST \
  "http://127.0.0.1:3085/api/backups/restore/$file_backup_id")
[[ "$file_restore_code" == 202 ]] || {
  echo "File restore returned HTTP $file_restore_code instead of 202." >&2
  exit 1
}
file_restore_job=$(python3 - "$tmp/file-restore-response.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    value = json.load(handle).get("jobId")
if not isinstance(value, str) or not value:
    raise SystemExit("file restore response did not contain a job ID")
print(value)
PY
)
wait_for_backup_job "$file_restore_job"
[[ $(sha256sum "$restore_probe/payload.bin" | awk '{print $1}') == "$original_file_sha" ]] || {
  echo "File restore did not recover the original payload." >&2
  exit 1
}
file_recovery=$(find /var/lib/hserver/backups -maxdepth 1 -type f -name 'pre-restore-*-files.tar.gz' -print | sort | tail -n 1)
[[ -n "$file_recovery" && -s "$file_recovery" ]] || {
  echo "File restore did not create a recovery archive." >&2
  exit 1
}
tar -tzf "$file_recovery" >/dev/null
curl -fsS --max-time 5 -H "@$auth_header" -o "$tmp/backups-after-file-restore.json" \
  http://127.0.0.1:3085/api/backups
file_recovery_id=$(python3 - "$tmp/backups-after-file-restore.json" "$(basename "$file_recovery")" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    backups = json.load(handle).get("backups", [])
for backup in backups:
    if backup.get("name") == sys.argv[2] and backup.get("status") == "completed":
        print(backup.get("id", ""))
        break
else:
    raise SystemExit("completed file recovery archive was not listed")
PY
)
[[ -n "$file_recovery_id" ]] || {
  echo "File recovery archive did not expose a restore ID." >&2
  exit 1
}
curl -fsS --max-time 10 -H "@$auth_header" -o "$tmp/file-recovery-validation.json" \
  "http://127.0.0.1:3085/api/backups/restore/$file_recovery_id/validate"
python3 - "$tmp/file-recovery-validation.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    validation = json.load(handle)
if validation.get("includesFiles") is not True or validation.get("filesRollback") is not True:
    raise SystemExit("file recovery archive was not accepted as a restorable files artifact")
PY

upgrade_archive_sha=$(sha256sum "$feed_dir/$upgrade_archive_name" | awk '{print $1}')
upgrade_archive_size=$(stat -c '%s' "$feed_dir/$upgrade_archive_name")
python3 - \
  "$feed_dir/release-manifest.json" \
  "$upgrade_version" \
  "$arch" \
  "$upgrade_archive_name" \
  "$upgrade_archive_sha" \
  "$upgrade_archive_size" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
document = {
    "schema_version": 1,
    "version": sys.argv[2],
    "published_at": "2026-01-02T00:00:00Z",
    "artifacts": {
        f"linux_{sys.argv[3]}": {
            "url": f"http://127.0.0.1:38085/{sys.argv[4]}",
            "sha256": sys.argv[5],
            "size_bytes": int(sys.argv[6]),
        }
    },
}
path.write_text(json.dumps(document, separators=(",", ":")), encoding="utf-8")
PY
# The signer is fail-closed; rotate the fixture signature before publishing
# the upgrade feed.
unlink "$feed_dir/release-manifest.json.sig"
"$root_dir/scripts/sign-release-manifest.sh" \
  "$feed_dir/release-manifest.json" \
  "$tmp/release-private.pem" \
  "$feed_dir/release-manifest.json.sig" >/dev/null

/usr/local/bin/hserverctl updates status >"$tmp/hserverctl-upgrade-available.json"
python3 - \
  "$tmp/hserverctl-upgrade-available.json" \
  "$version" \
  "$upgrade_version" \
  "$upgrade_archive_sha" \
  "$upgrade_archive_size" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if status.get("status") != "healthy" or status.get("signature_status") != "verified":
    raise SystemExit(f"packaged hserverctl did not observe a verified upgrade feed: {status}")
if status.get("current_version") != sys.argv[2] or status.get("latest_version") != sys.argv[3]:
    raise SystemExit(f"packaged hserverctl observed the wrong upgrade versions: {status}")
if status.get("latest_version_state") != "ahead" or status.get("update_available") is not True:
    raise SystemExit(f"packaged hserverctl did not report the native upgrade as available: {status}")
artifact = status.get("artifact", {})
if artifact.get("sha256") != sys.argv[4] or artifact.get("size_bytes") != int(sys.argv[5]):
    raise SystemExit(f"packaged hserverctl observed the wrong upgrade artifact: {status}")
PY

/usr/local/bin/hserverctl updates stage --confirm >"$tmp/hserverctl-upgrade-stage.json"
python3 - \
  "$tmp/hserverctl-upgrade-stage.json" \
  "$version" \
  "$upgrade_version" \
  "linux_$arch" \
  "$upgrade_archive_sha" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    stage = json.load(handle)
if stage.get("version") != sys.argv[3] or stage.get("current_version") != sys.argv[2]:
    raise SystemExit(f"packaged hserverctl staged the wrong release: {stage}")
if stage.get("platform") != sys.argv[4] or stage.get("sha256") != sys.argv[5]:
    raise SystemExit(f"packaged hserverctl staged the wrong native artifact: {stage}")
if stage.get("id") != f"{sys.argv[3]}-{sys.argv[5][:12]}" or stage.get("status") != "staged":
    raise SystemExit(f"packaged hserverctl returned an invalid verified stage: {stage}")
PY

/usr/local/bin/hserverctl updates install --confirm >"$tmp/hserverctl-upgrade-install.json"
python3 - "$tmp/hserverctl-upgrade-install.json" "$upgrade_version" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    stage = json.load(handle)
if stage.get("version") != sys.argv[2] or stage.get("status") != "scheduled":
    raise SystemExit(f"packaged hserverctl did not schedule the verified release: {stage}")
PY

panel_upgrade_complete=0
for _ in {1..90}; do
  if curl -fsS --max-time 3 http://127.0.0.1:3085/api/health >/dev/null 2>&1 \
    && /usr/local/bin/hserverctl --timeout 5s updates stage-status \
      >"$tmp/hserverctl-upgrade-terminal.json" 2>"$tmp/hserverctl-upgrade-poll-error.txt"
  then
    if python3 - "$tmp/hserverctl-upgrade-terminal.json" "$upgrade_version" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    stage = json.load(handle).get("stage")
if not isinstance(stage, dict) or stage.get("version") != sys.argv[2]:
    raise SystemExit(2)
if stage.get("status") == "completed":
    raise SystemExit(0)
if stage.get("status") == "failed":
    print(stage.get("status_detail", "verified native update failed"), file=sys.stderr)
    raise SystemExit(2)
raise SystemExit(1)
PY
    then
      panel_upgrade_complete=1
      break
    else
      update_status=$?
      if [[ $update_status == 2 ]]; then
        echo "Verified native CLI update reached a failed or invalid terminal state." >&2
        exit 1
      fi
    fi
  fi
  sleep 2
done
(( panel_upgrade_complete )) || {
  cat "$tmp/hserverctl-upgrade-poll-error.txt" >&2 2>/dev/null || true
  echo "Verified native CLI update did not reach completed." >&2
  exit 1
}

panel_identity=$(/usr/local/bin/hserver-panel --version)
cli_identity=$(/usr/local/bin/hserverctl version)
[[ $panel_identity == "hserver-panel $upgrade_version (commit "* ]] || {
  echo "Installed panel identity does not match the verified native stage: $panel_identity" >&2
  exit 1
}
[[ $cli_identity == "hserverctl $upgrade_version ("* ]] || {
  echo "Installed CLI identity does not match the verified native stage: $cli_identity" >&2
  exit 1
}
systemctl is-active --quiet hserver
curl -fsS --max-time 5 -H "@$auth_header" \
  -o "$tmp/onboarding-after-cli-update.json" \
  http://127.0.0.1:3085/api/onboarding
python3 - "$tmp/onboarding-after-cli-update.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
if state.get("completed") is not True or state.get("step") != 5:
    raise SystemExit("verified native CLI update did not preserve SQLite onboarding state")
PY

good_checksum=$(sha256sum /usr/local/bin/hserver-panel | awk '{print $1}')
good_cli_checksum=$(sha256sum /usr/local/bin/hserverctl | awk '{print $1}')
cat >"$tmp/unhealthy-panel" <<'EOF'
#!/usr/bin/env sh
exit 1
EOF
chmod 0755 "$tmp/unhealthy-panel"
if HSERVER_HEALTH_TIMEOUT=3 "$package_dir/install.sh" upgrade --binary "$tmp/unhealthy-panel"; then
  echo "An unhealthy upgrade unexpectedly succeeded." >&2
  exit 1
fi
[[ $(sha256sum /usr/local/bin/hserver-panel | awk '{print $1}') == "$good_checksum" ]] || {
  echo "Failed upgrade did not restore the previous panel binary." >&2
  exit 1
}
[[ $(sha256sum /usr/local/bin/hserverctl | awk '{print $1}') == "$good_cli_checksum" ]] || {
  echo "Failed upgrade did not restore the previous CLI binary." >&2
  exit 1
}
systemctl is-active --quiet hserver
curl -fsS --max-time 3 http://127.0.0.1:3085/api/health >/dev/null

curl -fsS --max-time 5 -H "@$auth_header" \
  -o "$tmp/onboarding-after-rollback.json" \
  http://127.0.0.1:3085/api/onboarding
python3 - "$tmp/onboarding-after-rollback.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
if state.get("completed") is not True or state.get("step") != 5:
    raise SystemExit("failed upgrade rollback did not preserve SQLite state")
PY

"$package_dir/install.sh" uninstall
[[ ! -e /usr/local/bin/hserver-panel && ! -e /usr/local/bin/hserverctl && ! -e /etc/systemd/system/hserver.service ]]
[[ ! -e /usr/local/libexec/hserver-install && ! -e /usr/local/libexec/hserver-doctor ]]
[[ ! -e /usr/local/share/hserver/nginx-snippets ]]
[[ -f /etc/hserver/hserver.env && -f /var/lib/hserver/hserver.db ]]
"$package_dir/install.sh" uninstall --purge-config --purge-data
installed=0
[[ ! -e /etc/hserver && ! -e /var/lib/hserver ]]

printf 'native release lifecycle acceptance: OK (%s)\n' "$arch"
