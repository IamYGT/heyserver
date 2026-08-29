#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

root="$tmp/root"
state="$tmp/systemctl-state"
mkdir -p "$state"

cat >"$tmp/systemctl" <<'__FAKE_SYSTEMCTL__'
#!/usr/bin/env sh
set -eu

state=$HSERVER_AGENT_SYSTEMCTL_STATE_DIR
mkdir -p "$state"
{
  printf '%s' "$0"
  for argument do printf ' %s' "$argument"; done
  printf '\n'
} >>"$state/calls.log"

activate() {
  touch "$state/active"
}

[ "$#" -ge 1 ] || exit 1
command_name=$1
shift
case "$command_name" in
  is-active)
    [ -f "$state/active" ]
    ;;
  is-enabled)
    [ -f "$state/enabled" ]
    ;;
  show-environment|daemon-reload)
    daemon_reload_fail=$(printenv FAKE_SYSTEMCTL_DAEMON_RELOAD_FAIL 2>/dev/null || printf '0')
    [ "$daemon_reload_fail" = 1 ] && [ "$command_name" = daemon-reload ] && exit 1
    :
    ;;
  enable)
    touch "$state/enabled"
    if [ "$1" = --now ]; then activate; fi
    ;;
  disable)
    rm -f "$state/enabled"
    if [ "$1" = --now ]; then rm -f "$state/active"; fi
    ;;
  start)
    activate
    ;;
  stop)
    rm -f "$state/active"
    ;;
  restart)
    fail_once=$(printenv FAKE_SYSTEMCTL_RESTART_FAIL_ONCE 2>/dev/null || printf '0')
    if [ "$fail_once" = 1 ] && [ ! -e "$state/restart-failed" ]; then
      : >"$state/restart-failed"
      rm -f "$state/active"
      exit 1
    fi
    activate
    ;;
  *)
    exit 1
    ;;
esac
__FAKE_SYSTEMCTL__
chmod 0755 "$tmp/systemctl"

printf '#!/bin/sh\nprintf "agent-v1\\n"\n' >"$tmp/agent-v1"
printf '#!/bin/sh\nprintf "agent-v2\\n"\n' >"$tmp/agent-v2"
chmod 0755 "$tmp/agent-v1" "$tmp/agent-v2"

secret_value='profile-test-secret-value'
printf '%s\n' "$secret_value" >"$tmp/token"
cat >"$tmp/agent.env" <<'__AGENT_ENV__'
HSERVER_AGENT_HUB_URL=https://hserver.example.com
HSERVER_AGENT_NODE_ID=profile-test-node
HSERVER_AGENT_TOKEN_FILE=/srv/secrets/hserver-agent.token
HSERVER_AGENT_INTERVAL=30s
HSERVER_AGENT_STATE_DIR=/srv/hserver-agent-state
HSERVER_AGENT_LIFECYCLE_INSTALLER=/opt/hserver/bin/agent-install
HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT=3
__AGENT_ENV__

run_installer() {
  HSERVER_AGENT_ROOT_PREFIX="$root" \
  HSERVER_AGENT_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_AGENT_SYSTEMCTL_STATE_DIR="$state" \
  HSERVER_AGENT_TEST_BINARY="$root/usr/local/bin/hserver-agent" \
  HSERVER_OS_RELEASE=/etc/os-release \
  HSERVER_AGENT_HEALTH_TIMEOUT=3 \
  HSERVER_AGENT_SKIP_HEALTHCHECK=1 \
    "$@"
}

assert_mode_owner() {
  expected=$1
  file=$2
  [ "$(stat -c '%u:%g:%a' "$file")" = "0:0:$expected" ]
}

assert_no_secret() {
  output_file=$1
  ! grep -F "$secret_value" "$output_file" >/dev/null 2>&1
}

run_installer "$root_dir/scripts/hserver-agent-install.sh" install \
  --binary "$tmp/agent-v1" --config "$tmp/agent.env" --token-file "$tmp/token" \
  >"$tmp/install.log"

agent_cmd="$root/opt/hserver/bin/agent-install"
config_file="$root/etc/hserver-agent.env"
token_file="$root/srv/secrets/hserver-agent.token"
profile_dir="$root/srv/hserver-agent-state/profile"
service_file="$root/etc/systemd/system/hserver-agent.service"

[ -x "$agent_cmd" ]
assert_mode_owner 700 "$profile_dir"
assert_mode_owner 600 "$config_file"
assert_mode_owner 600 "$token_file"
cmp -s "$tmp/token" "$token_file"
grep -F 'EnvironmentFile='"$root/etc/hserver-agent.env" "$service_file" >/dev/null
grep -F 'ReadOnlyPaths='"$token_file" "$service_file" >/dev/null
grep -F 'ReadWritePaths='"$root/srv/hserver-agent-state/profile" "$service_file" >/dev/null
grep -F 'ProtectSystem=strict' "$service_file" >/dev/null
grep -F 'NoNewPrivileges=yes' "$service_file" >/dev/null

token_sha=$(sha256sum "$token_file" | awk '{print $1}')
config_sha=$(sha256sum "$config_file" | awk '{print $1}')

cat >"$profile_dir/candidate.json" <<'__PROFILE_ONE__'
{"schema_version":1,"revision":1,"profile":{"allowDeployRead":true,"allowDeployActions":true,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"/srv/hserver/deploy-plans.json","deployAcmeWebroot":"","deployWriteRoots":["/srv/releases/b","/srv/releases/a"]}}
__PROFILE_ONE__
chmod 0600 "$profile_dir/candidate.json"
run_installer "$agent_cmd" apply-profile >"$tmp/apply-v1.log"
assert_no_secret "$tmp/apply-v1.log"
[ ! -e "$profile_dir/candidate.json" ]
assert_mode_owner 600 "$profile_dir/active.json"
assert_mode_owner 600 "$profile_dir/state.json"
assert_mode_owner 644 "$service_file"
grep -F '"state":"active"' "$profile_dir/state.json" >/dev/null
grep -F '"revision":1' "$profile_dir/state.json" >/dev/null
grep -F 'ReadWritePaths=/srv/releases/a' "$service_file" >/dev/null
grep -F 'ReadWritePaths=/srv/releases/b' "$service_file" >/dev/null
! grep -F 'ReadWritePaths=/srv/hserver/deploy-plans.json' "$service_file" >/dev/null 2>&1

# The local completion marker is intentionally stronger than the detached
# scheduler result; the hub must still wait for the next accepted heartbeat.
grep -F '"state":"active"' "$profile_dir/state.json" >/dev/null
grep -F 'daemon-reload' "$state/calls.log" >/dev/null
[ "$(grep -c ' restart ' "$state/calls.log")" -ge 1 ]
[ "$(grep -c ' is-active ' "$state/calls.log")" -ge 2 ]

cat >"$tmp/domain.env" <<'__DOMAIN_ENV__'
HSERVER_AGENT_NGINX_SITES_AVAILABLE=/srv/nginx/available
HSERVER_AGENT_NGINX_SITES_ENABLED=/srv/nginx/enabled
HSERVER_AGENT_CERTBOT_CONFIG_DIR=/srv/certbot/config
HSERVER_AGENT_CERTBOT_WORK_DIR=/srv/certbot/work
HSERVER_AGENT_CERTBOT_LOGS_DIR=/srv/certbot/logs
__DOMAIN_ENV__
cat "$tmp/domain.env" >>"$config_file"
config_sha=$(sha256sum "$config_file" | awk '{print $1}')

cat >"$profile_dir/candidate.json" <<'__PROFILE_TWO__'
{"schema_version":1,"revision":2,"profile":{"allowDeployRead":true,"allowDeployActions":true,"allowDeployDomainRead":true,"allowDeployDomainActions":true,"deployPlansFile":"/srv/hserver/deploy-plans.json","deployAcmeWebroot":"/srv/acme/webroot","deployWriteRoots":["/srv/releases/b","/srv/releases/a"]}}
__PROFILE_TWO__
chmod 0600 "$profile_dir/candidate.json"
run_installer "$agent_cmd" apply-profile >"$tmp/apply-v2.log"
assert_no_secret "$tmp/apply-v2.log"
assert_mode_owner 600 "$profile_dir/active.json"
assert_mode_owner 600 "$profile_dir/previous.json"
assert_mode_owner 600 "$profile_dir/state.json"
grep -F '"revision":2' "$profile_dir/active.json" >/dev/null
grep -F '"revision":1' "$profile_dir/previous.json" >/dev/null
for path in \
  /srv/releases/a /srv/releases/b /srv/acme/webroot /srv/nginx/available \
  /srv/nginx/enabled /srv/nginx /srv/certbot/config /srv/certbot/work /srv/certbot/logs; do
  grep -F "ReadWritePaths=$path" "$service_file" >/dev/null
done
! grep -F 'ReadWritePaths=/srv/hserver/deploy-plans.json' "$service_file" >/dev/null 2>&1

service_before_upgrade=$(sha256sum "$service_file" | awk '{print $1}')
active_before_upgrade=$(sha256sum "$profile_dir/active.json" | awk '{print $1}')
previous_before_upgrade=$(sha256sum "$profile_dir/previous.json" | awk '{print $1}')
state_before_upgrade=$(sha256sum "$profile_dir/state.json" | awk '{print $1}')
run_installer "$agent_cmd" upgrade --binary "$tmp/agent-v2" >"$tmp/upgrade.log"
assert_no_secret "$tmp/upgrade.log"
snapshot=$(cat "$root/srv/hserver-agent-state/releases/latest-pre-upgrade")
[ -f "$snapshot/hserver-agent" ]
[ -f "$snapshot/hserver-agent.service" ]
for profile_file in active.json previous.json state.json; do
  [ -f "$snapshot/profile/$profile_file" ]
  assert_mode_owner 600 "$snapshot/profile/$profile_file"
done
! find "$snapshot" -type f \( -name '*env*' -o -name '*token*' \) -print | grep . >/dev/null 2>&1
! grep -R -F "$secret_value" "$snapshot" >/dev/null 2>&1
grep -F 'ReadWritePaths=/srv/releases/a' "$service_file" >/dev/null
grep -F 'ReadWritePaths=/srv/acme/webroot' "$service_file" >/dev/null

run_installer "$agent_cmd" rollback >"$tmp/rollback.log"
assert_no_secret "$tmp/rollback.log"
cmp -s "$tmp/agent-v1" "$root/usr/local/bin/hserver-agent"
[ "$(sha256sum "$service_file" | awk '{print $1}')" = "$service_before_upgrade" ]
[ "$(sha256sum "$profile_dir/active.json" | awk '{print $1}')" = "$active_before_upgrade" ]
[ "$(sha256sum "$profile_dir/previous.json" | awk '{print $1}')" = "$previous_before_upgrade" ]
[ "$(sha256sum "$profile_dir/state.json" | awk '{print $1}')" = "$state_before_upgrade" ]
[ "$(sha256sum "$token_file" | awk '{print $1}')" = "$token_sha" ]
[ "$(sha256sum "$config_file" | awk '{print $1}')" = "$config_sha" ]

service_before_failure=$(sha256sum "$service_file" | awk '{print $1}')
active_before_failure=$(sha256sum "$profile_dir/active.json" | awk '{print $1}')
previous_before_failure=$(sha256sum "$profile_dir/previous.json" | awk '{print $1}')
cat >"$profile_dir/candidate.json" <<'__PROFILE_THREE__'
{"schema_version":1,"revision":3,"profile":{"allowDeployRead":true,"allowDeployActions":true,"allowDeployDomainRead":true,"allowDeployDomainActions":true,"deployPlansFile":"/srv/hserver/deploy-plans.json","deployAcmeWebroot":"/srv/acme/new","deployWriteRoots":["/srv/releases/new"]}}
__PROFILE_THREE__
chmod 0600 "$profile_dir/candidate.json"
if FAKE_SYSTEMCTL_RESTART_FAIL_ONCE=1 run_installer "$agent_cmd" apply-profile >"$tmp/restart-failure.log" 2>&1; then
  printf '%s\n' 'restart failure unexpectedly succeeded' >&2
  exit 1
fi
assert_no_secret "$tmp/restart-failure.log"
grep -F 'profile apply failed: profile_schedule_failed' "$tmp/restart-failure.log" >/dev/null
[ "$(sha256sum "$service_file" | awk '{print $1}')" = "$service_before_failure" ]
[ "$(stat -c '%u:%g:%a' "$service_file")" = '0:0:644' ]
[ "$(sha256sum "$profile_dir/active.json" | awk '{print $1}')" = "$active_before_failure" ]
[ "$(sha256sum "$profile_dir/previous.json" | awk '{print $1}')" = "$previous_before_failure" ]
grep -F '"state":"failed"' "$profile_dir/state.json" >/dev/null
grep -F '"error_code":"profile_schedule_failed"' "$profile_dir/state.json" >/dev/null
[ -f "$profile_dir/candidate.json" ]
[ "$(grep -c ' daemon-reload' "$state/calls.log")" -ge 2 ]
[ "$(grep -c ' restart ' "$state/calls.log")" -ge 3 ]

# Unknown fields and dependency violations are rejected locally with a safe
# code. The candidate remains available for an explicit retry.
cat >"$profile_dir/candidate.json" <<'__PROFILE_INVALID__'
{"schema_version":1,"revision":4,"profile":{"allowDeployRead":false,"allowDeployActions":true,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"/srv/private/raw/path","deployAcmeWebroot":"","deployWriteRoots":[]},"raw_path":"/srv/private/raw/path"}
__PROFILE_INVALID__
chmod 0600 "$profile_dir/candidate.json"
if run_installer "$agent_cmd" apply-profile >"$tmp/invalid.log" 2>&1; then
  printf '%s\n' 'invalid profile unexpectedly succeeded' >&2
  exit 1
fi
assert_no_secret "$tmp/invalid.log"
grep -F 'profile apply failed: profile_payload_invalid' "$tmp/invalid.log" >/dev/null
grep -F '"error_code":"profile_payload_invalid"' "$profile_dir/state.json" >/dev/null
[ -f "$profile_dir/candidate.json" ]

# Every profile-provided path is checked against the installer-owned boundary.
# Exact protected targets and candidate ancestors are rejected, including the
# configured custom token destination and every lifecycle path.
assert_protected_profile_path_rejected() {
  candidate_path=$1
  revision=$2
  target_kind=${3:-root}
  python3 - "$profile_dir/candidate.json" "$candidate_path" "$revision" "$target_kind" <<'__MAKE_PROTECTED__'
import json
import sys

candidate_path = sys.argv[2]
target_kind = sys.argv[4]
candidate = {
    "schema_version": 1,
    "revision": int(sys.argv[3]),
    "profile": {
        "allowDeployRead": True,
        "allowDeployActions": target_kind == "root",
        "allowDeployDomainRead": target_kind == "acme",
        "allowDeployDomainActions": target_kind == "acme",
        "deployPlansFile": f"/srv/read-only-plans-{sys.argv[3]}.json",
        "deployAcmeWebroot": candidate_path if target_kind == "acme" else "",
        "deployWriteRoots": [candidate_path] if target_kind == "root" else [],
    },
}
with open(sys.argv[1], "w") as handle:
    json.dump(candidate, handle, separators=(",", ":"))
__MAKE_PROTECTED__
  chmod 0600 "$profile_dir/candidate.json"
  if run_installer "$agent_cmd" apply-profile >"$tmp/protected-$revision.log" 2>&1; then
    printf 'protected profile path unexpectedly succeeded: %s\n' "$candidate_path" >&2
    exit 1
  fi
  assert_no_secret "$tmp/protected-$revision.log"
  grep -F 'profile apply failed: profile_payload_invalid' "$tmp/protected-$revision.log" >/dev/null
  grep -F '"error_code":"profile_payload_invalid"' "$profile_dir/state.json" >/dev/null
}

protected_revision=10
for protected_path in \
  /etc /usr /bin /sbin /boot /proc /sys /dev /run \
  /etc/hserver-managed /usr/local/hserver-managed /bin/hserver-managed \
  /sbin/hserver-managed /boot/hserver-managed /proc/hserver-managed \
  /sys/hserver-managed /dev/hserver-managed /run/hserver-managed \
  /etc/hserver-agent.env \
  /srv/secrets/hserver-agent.token \
  /etc/systemd/system/hserver-agent.service \
  /usr/local/bin/hserver-agent \
  /opt/hserver/bin/agent-install \
  /srv/hserver-agent-state \
  /srv/hserver-agent-state/releases \
  /srv/hserver-agent-state/work \
  /srv/hserver-agent-state/releases/work \
  /srv /usr/local /opt/hserver; do
  assert_protected_profile_path_rejected "$protected_path" "$protected_revision"
  protected_revision=$((protected_revision + 1))
done

# ACME is a profile-provided write target and receives the same boundary.
assert_protected_profile_path_rejected /srv/secrets "$protected_revision" acme
protected_revision=$((protected_revision + 1))

# The read-only plans source becomes a dynamic protected file for write
# targets: equality and candidate ancestors are both rejected.
for plans_write_target in /mnt/protected-plans.json /mnt/plans; do
  python3 - "$profile_dir/candidate.json" "$plans_write_target" "$protected_revision" <<'__MAKE_PLANS_OVERLAP__'
import json
import sys

write_target = sys.argv[2]
plans_file = "/mnt/protected-plans.json"
if write_target == "/mnt/plans":
    plans_file = "/mnt/plans/protected-plans.json"
candidate = {
    "schema_version": 1,
    "revision": int(sys.argv[3]),
    "profile": {
        "allowDeployRead": True,
        "allowDeployActions": True,
        "allowDeployDomainRead": False,
        "allowDeployDomainActions": False,
        "deployPlansFile": plans_file,
        "deployAcmeWebroot": "",
        "deployWriteRoots": [write_target],
    },
}
with open(sys.argv[1], "w") as handle:
    json.dump(candidate, handle, separators=(",", ":"))
__MAKE_PLANS_OVERLAP__
  chmod 0600 "$profile_dir/candidate.json"
  if run_installer "$agent_cmd" apply-profile >"$tmp/plans-overlap-$protected_revision.log" 2>&1; then
    printf '%s\n' 'plans/write overlap unexpectedly succeeded' >&2
    exit 1
  fi
  grep -F 'profile apply failed: profile_payload_invalid' "$tmp/plans-overlap-$protected_revision.log" >/dev/null
  protected_revision=$((protected_revision + 1))
done

# Existing symlink components cannot alias a candidate across a protected
# boundary. Absolute staged-root links are resolved inside the staged root.
mkdir -p "$root/srv"
ln -s /etc "$root/srv/etc-link"
assert_protected_profile_path_rejected /srv/etc-link/hserver-agent.env "$protected_revision"
protected_revision=$((protected_revision + 1))

# A symlink wholly outside every protected boundary remains usable; protection
# is an overlap/realpath rule rather than a blanket symlink ban.
mkdir -p "$root/srv/apps-real"
ln -s /srv/apps-real "$root/srv/apps-link"
cat >"$profile_dir/candidate.json" <<EOF
{"schema_version":1,"revision":$protected_revision,"profile":{"allowDeployRead":true,"allowDeployActions":true,"allowDeployDomainRead":false,"allowDeployDomainActions":false,"deployPlansFile":"/etc/hserver/deploy-plans.json","deployAcmeWebroot":"","deployWriteRoots":["/srv/apps-link"]}}
EOF
chmod 0600 "$profile_dir/candidate.json"
run_installer "$agent_cmd" apply-profile >"$tmp/apply-safe-symlink.log"
grep -F 'ReadWritePaths=/srv/apps-link' "$service_file" >/dev/null
protected_revision=$((protected_revision + 1))

# Static protected roots apply only to profile input. Installation-owned
# Nginx/Certbot paths still become domain-action sandbox exceptions.
sed -i \
  -e '/^HSERVER_AGENT_NGINX_SITES_AVAILABLE=/d' \
  -e '/^HSERVER_AGENT_NGINX_SITES_ENABLED=/d' \
  -e '/^HSERVER_AGENT_CERTBOT_CONFIG_DIR=/d' \
  -e '/^HSERVER_AGENT_CERTBOT_WORK_DIR=/d' \
  -e '/^HSERVER_AGENT_CERTBOT_LOGS_DIR=/d' \
  "$config_file"
cat >"$profile_dir/candidate.json" <<EOF
{"schema_version":1,"revision":$protected_revision,"profile":{"allowDeployRead":true,"allowDeployActions":false,"allowDeployDomainRead":true,"allowDeployDomainActions":true,"deployPlansFile":"/etc/hserver/deploy-plans.json","deployAcmeWebroot":"/srv/acme-owned","deployWriteRoots":[]}}
EOF
chmod 0600 "$profile_dir/candidate.json"
run_installer "$agent_cmd" apply-profile >"$tmp/apply-installation-owned-domain.log"
for path in \
  /etc/nginx/sites-available /etc/nginx/sites-enabled /etc/nginx \
  /etc/letsencrypt /var/lib/letsencrypt /var/log/letsencrypt; do
  grep -F "ReadWritePaths=$path" "$service_file" >/dev/null
done

# A canonical candidate exactly at the shared 16 KiB limit is accepted. This
# protects the inclusive boundary as well as the rejection case below.
python3 - "$profile_dir/candidate.json" <<'__MAKE_BOUNDARY__'
import json
import sys

candidate = {"schema_version": 1}
roots = []
target_lengths = [1009] + [1006] * 15
for index in range(16):
    prefix = f"/r{index:02d}"
    roots.append(prefix + ("a" * (target_lengths[index] - len(prefix))))
candidate["revision"] = 5
candidate["profile"] = {
    "allowDeployRead": True,
    "allowDeployActions": True,
    "allowDeployDomainRead": False,
    "allowDeployDomainActions": False,
    "deployPlansFile": "/srv/plans.json",
    "deployAcmeWebroot": "",
    "deployWriteRoots": roots,
}
encoded = json.dumps(candidate, ensure_ascii=False, separators=(",", ":"))
assert len(encoded.encode("utf-8")) == 16 * 1024
with open(sys.argv[1], "w") as handle:
    handle.write(encoded)
__MAKE_BOUNDARY__
chmod 0600 "$profile_dir/candidate.json"
[ "$(wc -c <"$profile_dir/candidate.json" | tr -d '[:space:]')" -eq 16384 ]
run_installer "$agent_cmd" apply-profile >"$tmp/apply-boundary.log"
assert_no_secret "$tmp/apply-boundary.log"
grep -F '"revision":5' "$profile_dir/active.json" >/dev/null
grep -F '"revision":5' "$profile_dir/state.json" >/dev/null

# The lifecycle candidate has the same 16 KiB bound as the agent store.
cp -p "$profile_dir/active.json" "$profile_dir/candidate.json"
python3 - "$profile_dir/candidate.json" <<'__MAKE_OVERSIZE__'
import json
import sys
candidate = json.loads(open(sys.argv[1]).read())
candidate["revision"] = 6
candidate["profile"]["deployWriteRoots"] = ["/" + ("a" * 4094) + str(index) for index in range(16)]
with open(sys.argv[1], "w") as handle:
    json.dump(candidate, handle, separators=(",", ":"))
__MAKE_OVERSIZE__
chmod 0600 "$profile_dir/candidate.json"
if run_installer "$agent_cmd" apply-profile >"$tmp/oversize.log" 2>&1; then
  printf '%s\n' 'oversized profile unexpectedly succeeded' >&2
  exit 1
fi
assert_no_secret "$tmp/oversize.log"
grep -F 'profile apply failed: profile_payload_too_large' "$tmp/oversize.log" >/dev/null
grep -F '"error_code":"profile_payload_too_large"' "$profile_dir/state.json" >/dev/null

if run_installer "$agent_cmd" apply-profile --binary "$tmp/secret-binary" >"$tmp/args.log" 2>&1; then
  printf '%s\n' 'apply-profile unexpectedly accepted an argument' >&2
  exit 1
fi
assert_no_secret "$tmp/args.log"
grep -F 'apply-profile does not accept file arguments' "$tmp/args.log" >/dev/null

printf '%s\n' 'agent profile apply test: OK'
