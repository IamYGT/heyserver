#!/usr/bin/env sh
set -eu

root_dir=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
invoking_uid=$(id -u)
invoking_gid=$(id -g)

if [ "$invoking_uid" -eq 0 ]; then
  run_privileged_env() {
    env "$@"
  }
else
  command -v sudo >/dev/null 2>&1 || {
    printf '%s\n' "agent install test requires root or sudo; install sudo or rerun with elevated access" >&2
    exit 1
  }
  run_privileged_env() {
    status=0
    sudo -- env "$@" || status=$?
    # Restore fixture ownership so unprivileged assertions can read private
    # files and the EXIT cleanup can remove every generated path.
    sudo -- env chown -R -- "$invoking_uid:$invoking_gid" "$tmp" >/dev/null 2>&1 \
      || return 1
    return "$status"
  }
fi

tmp=$(mktemp -d)
cleanup() {
  status=$?
  trap - EXIT INT TERM
  cleanup_status=0
  if [ "$invoking_uid" -eq 0 ]; then
    rm -rf -- "$tmp" || cleanup_status=$?
  else
    sudo -- env rm -rf -- "$tmp" >/dev/null 2>&1 || cleanup_status=$?
  fi
  if [ "$status" -eq 0 ] && [ "$cleanup_status" -ne 0 ]; then
    status=$cleanup_status
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

mkdir -p "$tmp/systemctl-state"
mkdir -p "$tmp/custom-systemctl-state"

cat >"$tmp/systemctl" <<'EOF'
#!/usr/bin/env sh
set -eu
state=${HSERVER_AGENT_SYSTEMCTL_STATE_DIR:?}
activate() {
  if [ -n "${HSERVER_AGENT_TEST_BINARY:-}" ] && [ -x "$HSERVER_AGENT_TEST_BINARY" ] && [ "$("$HSERVER_AGENT_TEST_BINARY")" = broken ]; then
    rm -f "$state/active"
  else
    touch "$state/active"
  fi
}
command=$1
shift
case "$command" in
  is-active) [ -f "$state/active" ] ;;
  is-enabled) [ -f "$state/enabled" ] ;;
  show-environment) [ "${TEST_SYSTEMD_STATE:-up}" = up ] ;;
  daemon-reload) ;;
  enable)
    touch "$state/enabled"
    if [ "${1:-}" = --now ]; then
      activate
    fi
    ;;
  disable)
    rm -f "$state/enabled"
    if [ "${1:-}" = --now ]; then
      rm -f "$state/active"
    fi
    ;;
  start) activate ;;
  stop) rm -f "$state/active" ;;
  *) exit 1 ;;
esac
EOF
chmod +x "$tmp/systemctl"

printf '#!/bin/sh\necho agent-v1\n' >"$tmp/agent-v1"
printf '#!/bin/sh\necho agent-v2\n' >"$tmp/agent-v2"
printf '#!/bin/sh\necho broken\n' >"$tmp/agent-broken"
chmod +x "$tmp/agent-v1" "$tmp/agent-v2" "$tmp/agent-broken"
for version in 3 4 5 6 7; do
  printf '#!/bin/sh\necho agent-v%s\n' "$version" >"$tmp/agent-v$version"
  chmod +x "$tmp/agent-v$version"
done
cat >"$tmp/agent.env" <<'EOF'
HSERVER_AGENT_HUB_URL=https://hserver.example.com
HSERVER_AGENT_NODE_ID=production-1
HSERVER_AGENT_TOKEN_FILE=/etc/hserver-agent.token
HSERVER_AGENT_INTERVAL=30s
HSERVER_AGENT_ALLOWED_HOST_ACTIONS=memory-optimize,swap-reset
HSERVER_AGENT_STATE_DIR=/srv/hserver-agent-state
HSERVER_AGENT_LIFECYCLE_INSTALLER=/opt/hserver/bin/agent-install
EOF
printf '%s\n' 'one-time-test-token' >"$tmp/token"
cat >"$tmp/agent-custom.env" <<'EOF'
HSERVER_AGENT_HUB_URL=https://hserver.example.com
HSERVER_AGENT_NODE_ID=custom-node-1
HSERVER_AGENT_TOKEN_FILE=/srv/secrets/hserver-agent.token
HSERVER_AGENT_INTERVAL=30s
HSERVER_AGENT_ALLOWED_HOST_ACTIONS=memory-optimize,swap-reset
HSERVER_AGENT_STATE_DIR=/srv/hserver-agent-state
HSERVER_AGENT_LIFECYCLE_INSTALLER=/opt/hserver/bin/agent-install
EOF
printf '%s\n' 'custom-one-time-test-token' >"$tmp/custom-token"

run_installer() {
  run_privileged_env \
    HSERVER_AGENT_ROOT_PREFIX="$tmp/root" \
    HSERVER_AGENT_SYSTEMCTL="$tmp/systemctl" \
    HSERVER_AGENT_SYSTEMCTL_STATE_DIR="$tmp/systemctl-state" \
    HSERVER_AGENT_TEST_BINARY="$tmp/root/usr/local/bin/hserver-agent" \
    HSERVER_OS_RELEASE=/etc/os-release \
    HSERVER_AGENT_SKIP_HEALTHCHECK=1 \
    TEST_SYSTEMD_STATE="${TEST_SYSTEMD_STATE:-}" \
    "$root_dir/scripts/hserver-agent-install.sh" "$@"
}

run_custom_installer() {
  run_privileged_env \
    HSERVER_AGENT_ROOT_PREFIX="$tmp/custom-root" \
    HSERVER_AGENT_SYSTEMCTL="$tmp/systemctl" \
    HSERVER_AGENT_SYSTEMCTL_STATE_DIR="$tmp/custom-systemctl-state" \
    HSERVER_AGENT_TEST_BINARY="$tmp/custom-root/usr/local/bin/hserver-agent" \
    HSERVER_OS_RELEASE=/etc/os-release \
    HSERVER_AGENT_SKIP_HEALTHCHECK=1 \
    TEST_SYSTEMD_STATE="${TEST_SYSTEMD_STATE:-}" \
    "$root_dir/scripts/hserver-agent-install.sh" "$@"
}

cp "$tmp/agent.env" "$tmp/agent-invalid-pm2.env"
printf '%s\n' 'HSERVER_AGENT_ALLOW_PM2_READ=true' >>"$tmp/agent-invalid-pm2.env"
if run_installer install --binary "$tmp/agent-v1" --config "$tmp/agent-invalid-pm2.env" --token-file "$tmp/token" >"$tmp/invalid-pm2.log" 2>&1; then
  printf '%s\n' "agent install unexpectedly accepted PM2 read without an explicit identity" >&2
  exit 1
fi
[ ! -e "$tmp/root/usr/local/bin/hserver-agent" ]
grep -q 'HSERVER_AGENT_PM2_BINARY must be an explicit absolute path' "$tmp/invalid-pm2.log"

if TEST_SYSTEMD_STATE=down run_installer install --binary "$tmp/agent-v1" --config "$tmp/agent.env" --token-file "$tmp/token" >"$tmp/preflight.log" 2>&1; then
  printf '%s\n' "agent install unexpectedly passed a failed host preflight" >&2
  exit 1
fi
[ ! -e "$tmp/root/usr/local/bin/hserver-agent" ]
grep -q 'host preflight failed' "$tmp/preflight.log"

run_installer install --binary "$tmp/agent-v1" --config "$tmp/agent.env" --token-file "$tmp/token" >/dev/null
[ "$(stat -c %a "$tmp/root/etc/hserver-agent.env")" = 600 ]
[ "$(stat -c %a "$tmp/root/etc/hserver-agent.token")" = 600 ]
cmp -s "$tmp/agent-v1" "$tmp/root/usr/local/bin/hserver-agent"
cmp -s "$root_dir/scripts/hserver-agent-install.sh" "$tmp/root/opt/hserver/bin/agent-install"
grep -F "ExecStart=$tmp/root/usr/local/bin/hserver-agent" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ProtectSystem=strict" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
[ -f "$tmp/systemctl-state/active" ]
[ -f "$tmp/systemctl-state/enabled" ]

cp "$tmp/root/etc/hserver-agent.env" "$tmp/agent-retention-env-before-rejection"
printf '%s\n' 'HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT=0' \
  >>"$tmp/root/etc/hserver-agent.env"
if run_installer upgrade --binary "$tmp/agent-v2" >"$tmp/invalid-retention-upgrade.log" 2>&1; then
  printf '%s\n' "invalid agent snapshot retention was unexpectedly accepted" >&2
  exit 1
fi
mv "$tmp/agent-retention-env-before-rejection" "$tmp/root/etc/hserver-agent.env"
cmp -s "$tmp/agent-v1" "$tmp/root/usr/local/bin/hserver-agent"
[ -f "$tmp/systemctl-state/active" ]
[ -f "$tmp/systemctl-state/enabled" ]
grep -q 'HSERVER_UPGRADE_SNAPSHOT_RETENTION_COUNT must be a positive integer' \
  "$tmp/invalid-retention-upgrade.log"

cat >>"$tmp/root/etc/hserver-agent.env" <<'EOF'
HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ=true
HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE=true
HSERVER_AGENT_NGINX_SITES_AVAILABLE=/srv/nginx/available
HSERVER_AGENT_NGINX_SITES_ENABLED=/srv/nginx/enabled
HSERVER_AGENT_ALLOW_DOMAIN_READ=true
HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS=true
HSERVER_AGENT_ALLOW_SSL_READ=true
HSERVER_AGENT_ALLOW_SSL_ACTIONS=true
HSERVER_AGENT_CERTBOT_CONFIG_DIR=/srv/certbot/config
HSERVER_AGENT_CERTBOT_WORK_DIR=/srv/certbot/work
HSERVER_AGENT_CERTBOT_LOGS_DIR=/srv/certbot/logs
HSERVER_AGENT_ALLOW_PHP_CONFIG_READ=true
HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE=true
HSERVER_AGENT_PHP_CONFIG_ROOT=/srv/php/config
HSERVER_AGENT_ALLOW_PM2_READ=true
HSERVER_AGENT_ALLOWED_PM2_ACTIONS=start,restart,reload,stop
HSERVER_AGENT_PM2_BINARY=/usr/local/bin/pm2
HSERVER_AGENT_PM2_HOME=/home/deploy/.pm2
HSERVER_AGENT_PM2_USER=deploy
HSERVER_AGENT_ALLOW_CRON_READ=true
HSERVER_AGENT_ALLOW_CRON_WRITE=true
HSERVER_AGENT_CRON_STATE_PATH=/srv/hserver/cron.json
HSERVER_AGENT_CRON_FILE_PATH=/srv/cron/hserver-managed
HSERVER_AGENT_ALLOW_FIREWALL_READ=true
HSERVER_AGENT_ALLOW_FIREWALL_WRITE=true
HSERVER_AGENT_FIREWALL_PERSISTENCE_PATH=/srv/firewall/state
HSERVER_AGENT_FILE_READ_ROOTS=/srv/apps,/home/deploy/apps
HSERVER_AGENT_FILE_WRITE_ROOTS=/srv/apps,/home/deploy/apps
HSERVER_AGENT_ALLOW_DEPLOY_READ=true
HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=true
HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=true
HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=true
HSERVER_AGENT_DEPLOY_PLANS_FILE=/srv/hserver/deploy-plans.json
HSERVER_AGENT_DEPLOY_ACME_WEBROOT=/srv/hserver/acme
HSERVER_AGENT_DEPLOY_WRITE_ROOTS=/srv/releases,/home/deploy/releases
HSERVER_AGENT_ALLOW_UPDATE_READ=true
HSERVER_AGENT_ALLOW_UPDATE_ACTIONS=true
HSERVER_AGENT_UPDATE_MANIFEST_URL=https://releases.example.com/hserver/latest/release-manifest.json
EOF
run_installer upgrade --binary "$tmp/agent-v1" >/dev/null
grep -F "ProtectSystem=strict" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/nginx/available" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/nginx/enabled" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/hserver/acme" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
[ -d "$tmp/root/srv/hserver/acme" ]
[ "$(stat -c %a "$tmp/root/srv/hserver/acme")" = "755" ]
grep -F "ReadWritePaths=/srv/nginx" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/certbot/config" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/certbot/work" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/certbot/logs" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/php/config" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ProtectHome=read-only" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/home/deploy/.pm2" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/hserver" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/cron" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/firewall/state" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/apps" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/home/deploy/apps" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/srv/releases" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=/home/deploy/releases" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ReadWritePaths=$tmp/root/srv/hserver-agent-state" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null

sed -i 's/^HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE=true$/HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE=false/' "$tmp/root/etc/hserver-agent.env"
run_installer upgrade --binary "$tmp/agent-v1" >/dev/null
grep -F "ReadWritePaths=/srv/nginx/enabled" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null

printf '%s\n' 'HSERVER_AGENT_ALLOW_TERMINAL=true' >>"$tmp/root/etc/hserver-agent.env"
run_installer upgrade --binary "$tmp/agent-v2" >/dev/null
cmp -s "$tmp/agent-v2" "$tmp/root/usr/local/bin/hserver-agent"
agent_snapshot=$(cat "$tmp/root/srv/hserver-agent-state/releases/latest-pre-upgrade")
[ -d "$agent_snapshot" ]
grep -F "NoNewPrivileges=no" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "PrivateTmp=no" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ProtectHome=no" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "ProtectSystem=no" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
grep -F "MemoryDenyWriteExecute=no" "$tmp/root/etc/systemd/system/hserver-agent.service" >/dev/null
run_installer rollback >/dev/null
cmp -s "$tmp/agent-v1" "$tmp/root/usr/local/bin/hserver-agent"
[ -f "$tmp/systemctl-state/active" ]
[ -f "$tmp/systemctl-state/enabled" ]

rm -f "$tmp/systemctl-state/active" "$tmp/systemctl-state/enabled"
if run_privileged_env \
  HSERVER_AGENT_ROOT_PREFIX="$tmp/root" \
  HSERVER_AGENT_SYSTEMCTL="$tmp/systemctl" \
  HSERVER_AGENT_SYSTEMCTL_STATE_DIR="$tmp/systemctl-state" \
  HSERVER_AGENT_TEST_BINARY="$tmp/root/usr/local/bin/hserver-agent" \
  HSERVER_AGENT_HEALTH_TIMEOUT=3 \
  HSERVER_AGENT_SKIP_HEALTHCHECK=0 \
  "$root_dir/scripts/hserver-agent-install.sh" upgrade --binary "$tmp/agent-broken" >"$tmp/broken-upgrade.log" 2>&1; then
  printf '%s\n' "broken agent upgrade unexpectedly succeeded" >&2
  exit 1
fi
cmp -s "$tmp/agent-v1" "$tmp/root/usr/local/bin/hserver-agent"
[ ! -f "$tmp/systemctl-state/active" ]
[ ! -f "$tmp/systemctl-state/enabled" ]
grep -q 'agent upgrade failed and was rolled back' "$tmp/broken-upgrade.log"

# Five consecutive upgrades must keep only the configured rollback window.
# The latest marker remains valid after pruning and supports a final rollback.
touch "$tmp/systemctl-state/active" "$tmp/systemctl-state/enabled"
for version in 3 4 5 6 7; do
  run_installer upgrade --binary "$tmp/agent-v$version" >/dev/null
done
agent_snapshot_count=$(find "$tmp/root/srv/hserver-agent-state/releases" \
  -mindepth 1 -maxdepth 1 -type d -name '*-pre-upgrade*' | wc -l | tr -d ' ')
[ "$agent_snapshot_count" -eq 3 ]
agent_latest_snapshot=$(cat "$tmp/root/srv/hserver-agent-state/releases/latest-pre-upgrade")
[ -d "$agent_latest_snapshot" ]
[ -f "$agent_latest_snapshot/hserver-agent" ]
run_installer rollback >/dev/null
cmp -s "$tmp/agent-v6" "$tmp/root/usr/local/bin/hserver-agent"
[ -f "$tmp/systemctl-state/active" ]
[ -f "$tmp/systemctl-state/enabled" ]

run_installer uninstall >/dev/null
[ ! -e "$tmp/root/usr/local/bin/hserver-agent" ]
[ ! -e "$tmp/root/opt/hserver/bin/agent-install" ]
[ ! -e "$tmp/root/etc/systemd/system/hserver-agent.service" ]
[ -s "$tmp/root/etc/hserver-agent.env" ]
[ -s "$tmp/root/etc/hserver-agent.token" ]

# A configured non-default destination must be resolved under the staged root,
# receive a private parent and mode-0600 token, and survive both upgrade paths.
custom_token_path="$tmp/custom-root/srv/secrets/hserver-agent.token"
run_custom_installer install \
  --binary "$tmp/agent-v1" \
  --config "$tmp/agent-custom.env" \
  --token-file "$tmp/custom-token" >/dev/null
[ -d "$tmp/custom-root/srv/secrets" ]
[ "$(stat -c %a "$tmp/custom-root/srv/secrets")" = 700 ]
[ "$(stat -c %a "$custom_token_path")" = 600 ]
cmp -s "$tmp/custom-token" "$custom_token_path"
grep -F 'HSERVER_AGENT_TOKEN_FILE=/srv/secrets/hserver-agent.token' \
  "$tmp/custom-root/etc/hserver-agent.env" >/dev/null
grep -F "ReadOnlyPaths=$custom_token_path" \
  "$tmp/custom-root/etc/systemd/system/hserver-agent.service" >/dev/null
[ ! -e "$tmp/custom-root/etc/hserver-agent.token" ]

custom_token_sha=$(sha256sum "$custom_token_path" | awk '{print $1}')
run_custom_installer upgrade --binary "$tmp/agent-v2" >/dev/null
cmp -s "$tmp/agent-v2" "$tmp/custom-root/usr/local/bin/hserver-agent"
[ "$(sha256sum "$custom_token_path" | awk '{print $1}')" = "$custom_token_sha" ]
[ "$(stat -c %a "$tmp/custom-root/srv/secrets")" = 700 ]
[ "$(stat -c %a "$custom_token_path")" = 600 ]
grep -F "ReadOnlyPaths=$custom_token_path" \
  "$tmp/custom-root/etc/systemd/system/hserver-agent.service" >/dev/null

run_custom_installer rollback >/dev/null
cmp -s "$tmp/agent-v1" "$tmp/custom-root/usr/local/bin/hserver-agent"
[ "$(sha256sum "$custom_token_path" | awk '{print $1}')" = "$custom_token_sha" ]
[ "$(stat -c %a "$tmp/custom-root/srv/secrets")" = 700 ]
[ "$(stat -c %a "$custom_token_path")" = 600 ]
grep -F "ReadOnlyPaths=$custom_token_path" \
  "$tmp/custom-root/etc/systemd/system/hserver-agent.service" >/dev/null

run_custom_installer uninstall --purge-config >/dev/null
[ ! -e "$tmp/custom-root/etc/hserver-agent.env" ]
[ ! -e "$custom_token_path" ]
[ ! -e "$tmp/custom-root/etc/hserver-agent.token" ]

printf '%s\n' "agent lifecycle test: OK"
