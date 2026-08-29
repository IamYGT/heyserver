#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  find "$tmp" -xdev -depth -delete 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM

feed="$tmp/feed"
mkdir -p "$feed"
bootstrap_log="$tmp/bootstrap.log"
sudo_log="$tmp/sudo.log"
curl_log="$tmp/curl.log"
: >"$bootstrap_log"
: >"$sudo_log"
: >"$curl_log"

cat >"$feed/bootstrap-install.sh" <<'BOOTSTRAP'
#!/usr/bin/env bash
set -euo pipefail
printf 'bootstrap-called\n' >>"${HSERVER_PUBLIC_INSTALL_TEST_BOOTSTRAP_LOG:?}"
manifest=
key_file=
vhosts=
agent_only=0
while (( $# > 0 )); do
  case "$1" in
    --manifest-url) manifest=$2; shift 2 ;;
    --public-key-file) key_file=$2; shift 2 ;;
    --vhosts-root) vhosts=$2; shift 2 ;;
    --agent-only) agent_only=1; shift ;;
    *) printf 'unknown-bootstrap-arg:%s\n' "$1" >>"${HSERVER_PUBLIC_INSTALL_TEST_BOOTSTRAP_LOG:?}"; exit 1 ;;
  esac
done
printf 'manifest:%s\n' "$manifest" >>"${HSERVER_PUBLIC_INSTALL_TEST_BOOTSTRAP_LOG:?}"
printf 'key-file:%s\n' "$key_file" >>"${HSERVER_PUBLIC_INSTALL_TEST_BOOTSTRAP_LOG:?}"
printf 'key:%s\n' "$(<"$key_file")" >>"${HSERVER_PUBLIC_INSTALL_TEST_BOOTSTRAP_LOG:?}"
printf 'vhosts-root:%s\n' "$vhosts" >>"${HSERVER_PUBLIC_INSTALL_TEST_BOOTSTRAP_LOG:?}"
printf 'agent-only:%s\n' "$agent_only" >>"${HSERVER_PUBLIC_INSTALL_TEST_BOOTSTRAP_LOG:?}"
BOOTSTRAP
chmod 0644 "$feed/bootstrap-install.sh"
cp "$feed/bootstrap-install.sh" "$tmp/bootstrap-original.sh"

"$repo_root/scripts/generate-release-signing-key.sh" \
  "$tmp/trusted-private.pem" "$feed/release-public-key.b64" >/dev/null
"$repo_root/scripts/generate-release-signing-key.sh" \
  "$tmp/attacker-private.pem" "$tmp/attacker-public.b64" >/dev/null
trusted_fingerprint=$(python3 - "$feed/release-public-key.b64" <<'PY'
import base64, hashlib, pathlib, sys
print(hashlib.sha256(base64.b64decode(pathlib.Path(sys.argv[1]).read_text().strip(), validate=True)).hexdigest())
PY
)
"$repo_root/scripts/sign-release-asset.sh" "$feed/bootstrap-install.sh" \
  "$tmp/trusted-private.pem" "$feed/bootstrap-install.sh.sig" >/dev/null
(
  cd "$feed"
  sha256sum bootstrap-install.sh >bootstrap-install.sh.sha256
  sha256sum release-public-key.b64 >release-public-key.b64.sha256
)

cat >"$tmp/curl" <<'CURL'
#!/usr/bin/env bash
set -euo pipefail
output=
url=
while (( $# > 0 )); do
  case "$1" in
    --output) output=$2; shift 2 ;;
    --max-time|--proto|--proto-redir) shift 2 ;;
    -q|-fsSL|-f|-s|-S|-L|--fail|--silent|--show-error|--location) shift ;;
    --) shift ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
[[ -n $output && -n $url ]]
printf '%s\n' "$url" >>"${HSERVER_PUBLIC_INSTALL_TEST_CURL_LOG:?}"
base=${HSERVER_PUBLIC_INSTALL_TEST_BASE:?}
case "$url" in
  "$base"/*) source_file="$HSERVER_PUBLIC_INSTALL_TEST_FEED/${url##*/}" ;;
  *) echo "unexpected URL: $url" >&2; exit 1 ;;
esac
[[ -f $source_file ]]
cp "$source_file" "$output"
CURL
chmod 0755 "$tmp/curl"

cat >"$tmp/sudo" <<'SUDO'
#!/usr/bin/env bash
set -euo pipefail
printf 'sudo-argv-begin\n' >>"${HSERVER_PUBLIC_INSTALL_TEST_SUDO_LOG:?}"
printf '%s\n' "$@" >>"${HSERVER_PUBLIC_INSTALL_TEST_SUDO_LOG:?}"
printf 'sudo-argv-end\n' >>"${HSERVER_PUBLIC_INSTALL_TEST_SUDO_LOG:?}"
[[ $# -gt 0 ]]
script=$1
shift
"$script" "$@"
SUDO
chmod 0755 "$tmp/sudo"

base=https://downloads.example.test/hserver
key_value=$(<"$feed/release-public-key.b64")
expected_bootstrap_urls=$(printf '%s\n' \
  "$base/bootstrap-install.sh" \
  "$base/bootstrap-install.sh.sha256" \
  "$base/bootstrap-install.sh.sig" \
  "$base/release-public-key.b64" \
  "$base/release-public-key.b64.sha256")

run_unpinned_wrapper() {
  HSERVER_PUBLIC_INSTALL_CURL="$tmp/curl" \
  HSERVER_PUBLIC_INSTALL_SUDO="$tmp/sudo" \
  HSERVER_PUBLIC_INSTALL_TEST_FEED="$feed" \
  HSERVER_PUBLIC_INSTALL_TEST_BASE="$base" \
  HSERVER_PUBLIC_INSTALL_TEST_CURL_LOG="$curl_log" \
  HSERVER_PUBLIC_INSTALL_TEST_SUDO_LOG="$sudo_log" \
  HSERVER_PUBLIC_INSTALL_TEST_BOOTSTRAP_LOG="$bootstrap_log" \
    "$repo_root/scripts/public-install.sh" "$@"
}

run_wrapper() {
  run_unpinned_wrapper "$@" --trusted-release-key-sha256 "$trusted_fingerprint"
}

reset_logs() {
  : >"$curl_log"
  : >"$sudo_log"
  : >"$bootstrap_log"
}

restore_trusted_feed() {
  cp "$tmp/bootstrap-original.sh" "$feed/bootstrap-install.sh"
  "$repo_root/scripts/generate-release-signing-key.sh" --public-from-private \
    "$tmp/trusted-private.pem" "$tmp/restored-public.b64" >/dev/null
  mv "$tmp/restored-public.b64" "$feed/release-public-key.b64"
  rm -f "$feed/bootstrap-install.sh.sig"
  "$repo_root/scripts/sign-release-asset.sh" "$feed/bootstrap-install.sh" \
    "$tmp/trusted-private.pem" "$feed/bootstrap-install.sh.sig" >/dev/null
  (
    cd "$feed"
    sha256sum bootstrap-install.sh >bootstrap-install.sh.sha256
    sha256sum release-public-key.b64 >release-public-key.b64.sha256
  )
}

# Generic source mode has no implicit signer and fails before download or sudo.
reset_logs
if run_unpinned_wrapper "$base" >"$tmp/unpinned.log" 2>&1; then
  echo "public-install accepted generic source mode without a signer fingerprint" >&2
  exit 1
fi
grep -Fq 'a trusted release key fingerprint is required' "$tmp/unpinned.log"
[[ ! -s $curl_log && ! -s $sudo_log && ! -s $bootstrap_log ]]

# A pinned signer authenticates the bootstrap before it is handed to sudo.
reset_logs
run_wrapper "$base" --vhosts-root /srv/hserver/sites >"$tmp/success.log"
diff -u <(printf '%s\n' "$expected_bootstrap_urls") "$curl_log"
grep -Fq "Verified release signer $trusted_fingerprint and bootstrap signature" "$tmp/success.log"
grep -Fxq 'sudo-argv-begin' "$sudo_log"
script_path=$(sed -n '2p' "$sudo_log")
[[ $script_path == /tmp/hserver-public-install.*/bootstrap-install.sh ]]
grep -Fxq -- '--manifest-url' "$sudo_log"
grep -Fxq -- "$base/release-manifest.json" "$sudo_log"
grep -Fxq -- '--public-key-file' "$sudo_log"
key_path=$(sed -n '6p' "$sudo_log")
[[ $key_path == /tmp/hserver-public-install.*/release-public-key.b64 ]]
grep -Fxq -- '--vhosts-root' "$sudo_log"
grep -Fxq -- /srv/hserver/sites "$sudo_log"
grep -Fxq 'sudo-argv-end' "$sudo_log"
grep -Fxq 'bootstrap-called' "$bootstrap_log"
grep -Fxq "manifest:$base/release-manifest.json" "$bootstrap_log"
grep -Fxq "key:$key_value" "$bootstrap_log"
grep -Fxq 'vhosts-root:/srv/hserver/sites' "$bootstrap_log"
grep -Fxq 'agent-only:0' "$bootstrap_log"

reset_logs
run_wrapper "$base" --agent-only >"$tmp/agent-only.log"
diff -u <(printf '%s\n' "$expected_bootstrap_urls") "$curl_log"
grep -Fxq -- '--agent-only' "$sudo_log"
grep -Fxq 'agent-only:1' "$bootstrap_log"
if grep -Fxq -- '--vhosts-root' "$sudo_log"; then
  echo "public-install forwarded --vhosts-root in agent-only mode" >&2
  exit 1
fi

assert_rejected_without_download() {
  local name=$1
  shift
  reset_logs
  if run_wrapper "$@" >"$tmp/$name.log" 2>&1; then
    echo "public-install accepted invalid input: $name" >&2
    exit 1
  fi
  [[ ! -s $curl_log && ! -s $sudo_log && ! -s $bootstrap_log ]]
}
assert_rejected_without_download plain-http http://downloads.example.test/hserver
assert_rejected_without_download userinfo 'https://user:password@downloads.example.test/hserver'
assert_rejected_without_download whitespace $'https://downloads.example.test/hserver with-space'
assert_rejected_without_download unknown-argument "$base" --unexpected
assert_rejected_without_download unsafe-site-root "$base" --vhosts-root /srv/hserver/sites\ with-space
assert_rejected_without_download root-site "$base" --vhosts-root /
assert_rejected_without_download relative-site "$base" --vhosts-root relative/sites
assert_rejected_without_download agent-only-site-root "$base" --agent-only --vhosts-root /srv/hserver/sites

# A self-consistent replacement key, checksums, bootstrap and signature cannot
# expand the trusted signer set and must fail before privileged execution.
cp "$tmp/attacker-public.b64" "$feed/release-public-key.b64"
rm -f "$feed/bootstrap-install.sh.sig"
"$repo_root/scripts/sign-release-asset.sh" "$feed/bootstrap-install.sh" \
  "$tmp/attacker-private.pem" "$feed/bootstrap-install.sh.sig" >/dev/null
(cd "$feed" && sha256sum release-public-key.b64 >release-public-key.b64.sha256)
reset_logs
if run_wrapper "$base" >"$tmp/unknown-signer.log" 2>&1; then
  echo "public-install accepted a self-consistent replacement signer" >&2
  exit 1
fi
grep -Fq 'release public key does not match a trusted signer' "$tmp/unknown-signer.log"
[[ ! -s $sudo_log && ! -s $bootstrap_log ]]
restore_trusted_feed

# Transfer checks remain useful but are not signer identity.
(cd "$feed" && printf '%064d  bootstrap-install.sh\n' 0 >bootstrap-install.sh.sha256)
reset_logs
if run_wrapper "$base" >"$tmp/bootstrap-checksum.log" 2>&1; then
  echo "public-install accepted a bad bootstrap checksum" >&2
  exit 1
fi
grep -Fq 'bootstrap checksum verification failed' "$tmp/bootstrap-checksum.log"
[[ ! -s $sudo_log && ! -s $bootstrap_log ]]
restore_trusted_feed

# A malformed public key fails before signature verification or sudo.
printf '%s\n' 'not-base64' >"$feed/release-public-key.b64"
(cd "$feed" && sha256sum release-public-key.b64 >release-public-key.b64.sha256)
reset_logs
if run_wrapper "$base" >"$tmp/key-format.log" 2>&1; then
  echo "public-install accepted a malformed public key" >&2
  exit 1
fi
grep -Fq 'public key validation failed' "$tmp/key-format.log"
[[ ! -s $sudo_log && ! -s $bootstrap_log ]]
restore_trusted_feed

# A tampered bootstrap with a matching convenience checksum still fails its
# detached signature before chmod/sudo/exec.
printf '\n# tampered\n' >>"$feed/bootstrap-install.sh"
(cd "$feed" && sha256sum bootstrap-install.sh >bootstrap-install.sh.sha256)
reset_logs
if run_wrapper "$base" >"$tmp/tampered-bootstrap.log" 2>&1; then
  echo "public-install accepted a tampered bootstrap" >&2
  exit 1
fi
grep -Fq 'bootstrap signature verification failed' "$tmp/tampered-bootstrap.log"
[[ ! -s $sudo_log && ! -s $bootstrap_log ]]
restore_trusted_feed

# A well-formed signature made by another key is also rejected before sudo.
rm -f "$feed/bootstrap-install.sh.sig"
"$repo_root/scripts/sign-release-asset.sh" "$feed/bootstrap-install.sh" \
  "$tmp/attacker-private.pem" "$feed/bootstrap-install.sh.sig" >/dev/null
reset_logs
if run_wrapper "$base" >"$tmp/wrong-signature.log" 2>&1; then
  echo "public-install accepted a wrong bootstrap signature" >&2
  exit 1
fi
grep -Fq 'bootstrap signature verification failed' "$tmp/wrong-signature.log"
[[ ! -s $sudo_log && ! -s $bootstrap_log ]]

printf '%s\n' 'public install signer trust contract: OK'
