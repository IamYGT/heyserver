#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
bootstrap_prefix=()
if (( EUID != 0 )); then
  command -v sudo >/dev/null 2>&1 || {
    echo "bootstrap install test requires root or sudo; install sudo or rerun with elevated access" >&2
    exit 1
  }
  # bootstrap-install.sh must stay root-only; elevate only its disposable
  # fixture runs so the surrounding contributor test remains unprivileged.
  bootstrap_prefix=(sudo --)
fi
tmp=$(mktemp -d)
trap 'find "$tmp" -xdev -depth -delete' EXIT INT TERM
feed="$tmp/feed"
fixture="$tmp/fixture"
package_log="$tmp/package.log"
curl_log="$tmp/curl.log"
mkdir -p "$feed" "$fixture"
: >"$package_log"
: >"$curl_log"

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "bootstrap test does not support $(uname -m)" >&2; exit 1 ;;
esac
version=v1.2.3
package_name="hserver-panel-${version}-linux-${arch}"
package_dir="$fixture/$package_name"
archive="$feed/$package_name.tar.gz"
mkdir -p "$package_dir"
printf '%s\n' "$version" >"$package_dir/VERSION"
for binary in hserver-panel hserver-agent hserverctl; do
  cp /bin/true "$package_dir/$binary"
done
cat >"$package_dir/doctor.sh" <<'EOF'
#!/usr/bin/env sh
set -eu
printf 'doctor:%s\n' "$1" >>"${HSERVER_BOOTSTRAP_TEST_LOG:?}"
EOF
cat >"$package_dir/install.sh" <<'EOF'
#!/usr/bin/env sh
set -eu
command=$1
shift
printf 'install:%s\n' "$command" >>"${HSERVER_BOOTSTRAP_TEST_LOG:?}"
while [ "$#" -gt 0 ]; do
  printf 'install-arg:%s\n' "$1" >>"${HSERVER_BOOTSTRAP_TEST_LOG:?}"
  shift
done
if [ "$command" = install ]; then
  printf 'manifest:%s\n' "${HSERVER_INSTALL_UPDATE_MANIFEST_URL:-}" >>"${HSERVER_BOOTSTRAP_TEST_LOG:?}"
  printf 'public-keys:%s\n' "${HSERVER_INSTALL_UPDATE_MANIFEST_PUBLIC_KEYS:-}" >>"${HSERVER_BOOTSTRAP_TEST_LOG:?}"
  printf 'defer-next-steps:%s\n' "${HSERVER_INSTALL_DEFER_NEXT_STEPS:-}" >>"${HSERVER_BOOTSTRAP_TEST_LOG:?}"
elif [ "$command" = next-steps ]; then
  printf '%s\n' 'Heyserver is ready for first access.'
  printf '%s\n' 'Open http://127.0.0.1:3085 in your browser.'
fi
EOF
cat >"$package_dir/agent-install.sh" <<'EOF'
#!/usr/bin/env sh
set -eu
command=$1
shift
printf 'agent-install:%s\n' "$command" >>"${HSERVER_BOOTSTRAP_TEST_LOG:?}"
while [ "$#" -gt 0 ]; do
  printf 'agent-install-arg:%s\n' "$1" >>"${HSERVER_BOOTSTRAP_TEST_LOG:?}"
  shift
done
EOF
chmod 0755 "$package_dir/doctor.sh" "$package_dir/install.sh" "$package_dir/agent-install.sh"
tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
  -czf "$archive" -C "$fixture" "$package_name"

private_key="$tmp/release-private.pem"
public_key_file="$tmp/release-public.b64"
"$repo_root/scripts/generate-release-signing-key.sh" "$private_key" "$public_key_file" >/dev/null
public_key=$(<"$public_key_file")

write_manifest() {
  local selected_archive=$1
  local digest size
  digest=$(sha256sum "$selected_archive" | awk '{print $1}')
  size=$(stat -c '%s' "$selected_archive")
  cat >"$feed/release-manifest.json" <<EOF
{"schema_version":1,"version":"$version","published_at":"2026-08-27T00:00:00Z","artifacts":{"linux_$arch":{"url":"http://bootstrap.example/$package_name.tar.gz","sha256":"$digest","size_bytes":$size}}}
EOF
  unlink "$feed/release-manifest.json.sig" 2>/dev/null || true
  "$repo_root/scripts/sign-release-manifest.sh" \
    "$feed/release-manifest.json" "$private_key" "$feed/release-manifest.json.sig" >/dev/null
}
write_manifest "$archive"

cat >"$tmp/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=
url=
while (( $# )); do
  case "$1" in
    --output) output=$2; shift 2 ;;
    --max-time) shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
[[ -n "$output" && -n "$url" ]]
printf '%s\n' "$url" >>"${HSERVER_BOOTSTRAP_TEST_CURL_LOG:?}"
case "$url" in
  http://bootstrap.example/release-manifest.json)
    source_file="$HSERVER_BOOTSTRAP_TEST_FEED/release-manifest.json"
    ;;
  http://bootstrap.example/release-manifest.json.sig)
    source_file="$HSERVER_BOOTSTRAP_TEST_FEED/release-manifest.json.sig"
    ;;
  http://bootstrap.example/*.tar.gz)
    source_file="$HSERVER_BOOTSTRAP_TEST_FEED/${url##*/}"
    ;;
  *) echo "unexpected bootstrap URL: $url" >&2; exit 1 ;;
esac
cp "$source_file" "$output"
EOF
chmod 0755 "$tmp/curl"

run_bootstrap() {
  "${bootstrap_prefix[@]}" env \
    HSERVER_BOOTSTRAP_CURL="$tmp/curl" \
    HSERVER_BOOTSTRAP_TEST_FEED="$feed" \
    HSERVER_BOOTSTRAP_TEST_LOG="$package_log" \
    HSERVER_BOOTSTRAP_TEST_CURL_LOG="$curl_log" \
    "$repo_root/scripts/bootstrap-install.sh" \
      --manifest-url http://bootstrap.example/release-manifest.json \
      --public-key-file "$public_key_file" \
      "$@"
}

legacy_root="$tmp/legacy-root"
mkdir -p "$legacy_root/usr/local/bin" "$legacy_root/etc" "$legacy_root/srv/secrets"
cp /bin/true "$legacy_root/usr/local/bin/hserver-agent"
cat >"$legacy_root/etc/hserver-agent.env" <<'EOF'
HSERVER_AGENT_HUB_URL=https://legacy.example
HSERVER_AGENT_NODE_ID=legacy-agent
HSERVER_AGENT_TOKEN_FILE=/srv/secrets/legacy-agent.token
EOF
printf '%s\n' 'legacy-agent-token' >"$legacy_root/srv/secrets/legacy-agent.token"
chmod 0600 "$legacy_root/etc/hserver-agent.env" "$legacy_root/srv/secrets/legacy-agent.token"
legacy_config_sha=$(sha256sum "$legacy_root/etc/hserver-agent.env" | awk '{print $1}')
legacy_token_sha=$(sha256sum "$legacy_root/srv/secrets/legacy-agent.token" | awk '{print $1}')

run_agent_bootstrap() {
  "${bootstrap_prefix[@]}" env \
    HSERVER_BOOTSTRAP_CURL="$tmp/curl" \
    HSERVER_BOOTSTRAP_TEST_FEED="$feed" \
    HSERVER_BOOTSTRAP_TEST_LOG="$package_log" \
    HSERVER_BOOTSTRAP_TEST_CURL_LOG="$curl_log" \
    HSERVER_AGENT_ROOT_PREFIX="$legacy_root" \
    "$repo_root/scripts/bootstrap-install.sh" \
      --manifest-url http://bootstrap.example/release-manifest.json \
      --public-key-file "$public_key_file" \
      --agent-only
}

run_bootstrap >"$tmp/success.log"
grep -q "Verified signed Heyserver release: $version (linux/$arch)" "$tmp/success.log"
grep -q "Heyserver $version installation completed" "$tmp/success.log"
grep -q 'Heyserver is ready for first access.' "$tmp/success.log"
grep -q 'Open http://127.0.0.1:3085 in your browser.' "$tmp/success.log"
diff -u <(printf '%s\n' \
  'doctor:preflight' \
  'install:install' \
  'manifest:http://bootstrap.example/release-manifest.json' \
  "public-keys:$public_key" \
  'defer-next-steps:1' \
  'doctor:installed' \
  'install:next-steps') "$package_log"

: >"$package_log"
: >"$curl_log"
run_agent_bootstrap >"$tmp/agent-success.log"
grep -q "Verified signed Heyserver agent release: $version (linux/$arch)" "$tmp/agent-success.log"
grep -q "Heyserver agent $version upgrade completed" "$tmp/agent-success.log"
grep -q 'agent-install:upgrade' "$package_log"
grep -q 'agent-install-arg:--binary' "$package_log"
if grep -Eq '^(doctor|install):' "$package_log"; then
  echo "agent-only mode reached panel lifecycle tools" >&2
  exit 1
fi
[[ "$(sha256sum "$legacy_root/etc/hserver-agent.env" | awk '{print $1}')" = "$legacy_config_sha" ]]
[[ "$(sha256sum "$legacy_root/srv/secrets/legacy-agent.token" | awk '{print $1}')" = "$legacy_token_sha" ]]
if grep -Fq 'legacy-agent-token' "$tmp/agent-success.log"; then
  echo "agent-only output leaked the legacy token" >&2
  exit 1
fi

rm -f "$legacy_root/usr/local/bin/hserver-agent"
: >"$package_log"
if run_agent_bootstrap >"$tmp/missing-agent.log" 2>&1; then
  echo "agent-only mode accepted a missing legacy agent" >&2
  exit 1
fi
grep -q 'agent-only mode requires an existing managed agent installation' "$tmp/missing-agent.log"
[[ ! -s "$package_log" ]] || {
  echo "missing legacy agent reached packaged lifecycle tools" >&2
  exit 1
}
cp /bin/true "$legacy_root/usr/local/bin/hserver-agent"

vhosts_root="$tmp/provider-neutral-sites"
: >"$package_log"
: >"$curl_log"
run_bootstrap --vhosts-root "$vhosts_root" >"$tmp/forward.log"
grep -q "Verified signed Heyserver release: $version (linux/$arch)" "$tmp/forward.log"
diff -u <(printf '%s\n' \
  'doctor:preflight' \
  'install:install' \
  'install-arg:--vhosts-root' \
  "install-arg:$vhosts_root" \
  'manifest:http://bootstrap.example/release-manifest.json' \
  "public-keys:$public_key" \
  'defer-next-steps:1' \
  'doctor:installed' \
  'install:next-steps') "$package_log"

assert_rejected_before_download() {
  local value=$1 expected=$2
  local output
  output=$tmp/rejected-$(printf '%s' "$RANDOM").log
  : >"$curl_log"
  : >"$package_log"
  if run_bootstrap --vhosts-root "$value" >"$output" 2>&1; then
    echo "bootstrap accepted invalid --vhosts-root: $value" >&2
    exit 1
  fi
  grep -q -- "$expected" "$output"
  [[ ! -s "$curl_log" ]] || {
    echo "invalid --vhosts-root started a download: $value" >&2
    exit 1
  }
  [[ ! -s "$package_log" ]] || {
    echo "invalid --vhosts-root reached packaged lifecycle tools: $value" >&2
    exit 1
  }
}

assert_rejected_before_download relative/sites '--vhosts-root must be an absolute path'
assert_rejected_before_download / 'refusing unsafe path: /'
assert_rejected_before_download "$tmp/sites with whitespace" '--vhosts-root must not contain whitespace'
assert_rejected_before_download "$tmp/sites;unsafe" '--vhosts-root contains unsafe path characters'
assert_rejected_before_download "$tmp/sites/../unsafe" '--vhosts-root contains an unsafe path traversal sequence'

cp "$feed/release-manifest.json.sig" "$tmp/good-signature"
printf '%s\n' 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==' \
  >"$feed/release-manifest.json.sig"
: >"$package_log"
if run_bootstrap >"$tmp/bad-signature.log" 2>&1; then
  echo "bootstrap accepted an invalid manifest signature" >&2
  exit 1
fi
grep -q 'release manifest signature verification failed' "$tmp/bad-signature.log"
[[ ! -s "$package_log" ]] || {
  echo "invalid signature reached packaged lifecycle tools" >&2
  exit 1
}
cp "$tmp/good-signature" "$feed/release-manifest.json.sig"

python3 - "$archive" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
value = bytearray(path.read_bytes())
value[-1] ^= 1
path.write_bytes(value)
PY
: >"$package_log"
if run_bootstrap >"$tmp/bad-checksum.log" 2>&1; then
  echo "bootstrap accepted an archive checksum mismatch" >&2
  exit 1
fi
grep -q 'release archive checksum does not match the signed manifest' "$tmp/bad-checksum.log"
[[ ! -s "$package_log" ]] || {
  echo "checksum mismatch reached packaged lifecycle tools" >&2
  exit 1
}

malicious="$feed/malicious.tar.gz"
python3 - "$malicious" "$package_name" <<'PY'
import io
import sys
import tarfile

with tarfile.open(sys.argv[1], "w:gz") as archive:
    root = tarfile.TarInfo(sys.argv[2])
    root.type = tarfile.DIRTYPE
    root.mode = 0o755
    archive.addfile(root)
    link = tarfile.TarInfo(sys.argv[2] + "/hserver-panel")
    link.type = tarfile.SYMTYPE
    link.linkname = "/bin/true"
    archive.addfile(link)
    for name in ("hserver-agent", "hserverctl"):
        entry = tarfile.TarInfo(sys.argv[2] + "/" + name)
        entry.size = 4
        entry.mode = 0o755
        archive.addfile(entry, io.BytesIO(b"ELF!"))
    for name, data in (("VERSION", b"v1.2.3\n"), ("install.sh", b"#!/bin/sh\n"), ("doctor.sh", b"#!/bin/sh\n")):
        entry = tarfile.TarInfo(sys.argv[2] + "/" + name)
        entry.size = len(data)
        entry.mode = 0o755
        archive.addfile(entry, io.BytesIO(data))
PY
cp "$malicious" "$archive"
write_manifest "$archive"
: >"$package_log"
if run_bootstrap >"$tmp/unsafe-archive.log" 2>&1; then
  echo "bootstrap accepted an unsafe archive entry" >&2
  exit 1
fi
grep -q 'unsupported entry type' "$tmp/unsafe-archive.log"
[[ ! -s "$package_log" ]] || {
  echo "unsafe archive reached packaged lifecycle tools" >&2
  exit 1
}

printf '%s\n' 'signed release bootstrap contract: OK'
