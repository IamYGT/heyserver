#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
bootstrap_prefix=()
if (( EUID != 0 )); then
  command -v sudo >/dev/null 2>&1 || {
    echo "manual release install test requires root or sudo; install sudo or rerun with elevated access" >&2
    exit 1
  }
  # bootstrap-install.sh must stay root-only; elevate only its disposable
  # fixture runs so the surrounding contributor test remains unprivileged.
  bootstrap_prefix=(sudo --)
fi
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hserver-manual-release-test.XXXXXXXX")
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  find "$tmp" -xdev -depth -delete 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM

guide="$repo_root/docs/installation-guide.md"
readme="$repo_root/README.md"
for required in \
  'release-manifest.json.sig' \
  'bootstrap-install.sh.sig' \
  'detached Ed25519 signature' \
  '--public-key-file ./release-public-key.b64' \
  '### Checksum-only discovery and recovery boundary'; do
  grep -Fq -- "$required" "$guide" || {
    printf 'manual release guide is missing signed-install contract: %s\n' "$required" >&2
    exit 1
  }
done
for required in \
  'Install a published archive through the signed' \
  'it is not the public release installation path'; do
  grep -Fq -- "$required" "$readme" || {
    printf 'README is missing signed-install contract: %s\n' "$required" >&2
    exit 1
  }
done
for unsafe_example in \
  'VERSION=v1.0.0' \
  'curl -fLO "${BASE_URL}/${ARCHIVE}"' \
  'sha256sum -c "${ARCHIVE}.sha256"' \
  'tar -xzf "${ARCHIVE}"'; do
  if grep -Fq -- "$unsafe_example" "$guide"; then
    printf 'manual release guide still contains checksum-only install sequence: %s\n' \
      "$unsafe_example" >&2
    exit 1
  fi
done
for unsafe_example in \
  'ARCHIVE=hserver-panel-v1.0.0-linux-amd64.tar.gz' \
  'sudo ./install.sh install --vhosts-root /srv/hserver/sites'; do
  if grep -Fq -- "$unsafe_example" "$readme"; then
    printf 'README still contains checksum-only install sequence: %s\n' \
      "$unsafe_example" >&2
    exit 1
  fi
done

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) printf 'manual release test does not support %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

feed="$tmp/feed"
fixture="$tmp/fixture"
lifecycle_log="$tmp/lifecycle.log"
curl_log="$tmp/curl.log"
privileged_marker="$tmp/privileged-mutation.marker"
mkdir -p "$feed" "$fixture"

version=v1.2.3
package_name="hserver-panel-${version}-linux-${arch}"
package_dir="$fixture/$package_name"
archive="$feed/$package_name.tar.gz"
mkdir -p "$package_dir"
printf '%s\n' "$version" >"$package_dir/VERSION"
for binary in hserver-panel hserver-agent hserverctl; do
  cp /bin/true "$package_dir/$binary"
done
cat >"$package_dir/doctor.sh" <<'DOCTOR'
#!/usr/bin/env sh
set -eu
printf 'doctor:%s\n' "$1" >>"${HSERVER_MANUAL_RELEASE_TEST_LIFECYCLE_LOG:?}"
DOCTOR
cat >"$package_dir/install.sh" <<'INSTALL'
#!/usr/bin/env sh
set -eu
printf 'install:%s\n' "$1" >>"${HSERVER_MANUAL_RELEASE_TEST_LIFECYCLE_LOG:?}"
if [ "$1" = install ]; then
  : >"${HSERVER_MANUAL_RELEASE_TEST_PRIVILEGED_MARKER:?}"
fi
INSTALL
chmod 0755 "$package_dir/doctor.sh" "$package_dir/install.sh"
tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
  -czf "$archive" -C "$fixture" "$package_name"
cp "$archive" "$tmp/good-archive"

private_key="$tmp/release-private.pem"
public_key_file="$tmp/release-public.b64"
"$repo_root/scripts/generate-release-signing-key.sh" \
  "$private_key" "$public_key_file" >/dev/null

write_manifest() {
  local digest size
  digest=$(sha256sum "$archive" | awk '{print $1}')
  size=$(stat -c '%s' "$archive")
  cat >"$feed/release-manifest.json" <<MANIFEST
{"schema_version":1,"version":"$version","published_at":"2026-08-27T00:00:00Z","artifacts":{"linux_$arch":{"url":"http://manual-release.example/$package_name.tar.gz","sha256":"$digest","size_bytes":$size}}}
MANIFEST
  unlink "$feed/release-manifest.json.sig" 2>/dev/null || true
  "$repo_root/scripts/sign-release-manifest.sh" \
    "$feed/release-manifest.json" "$private_key" \
    "$feed/release-manifest.json.sig" >/dev/null
}
write_manifest
cp "$feed/release-manifest.json" "$tmp/good-manifest"
cp "$feed/release-manifest.json.sig" "$tmp/good-signature"

cat >"$tmp/curl" <<'CURL'
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
printf '%s\n' "$url" >>"${HSERVER_MANUAL_RELEASE_TEST_CURL_LOG:?}"
case "$url" in
  http://manual-release.example/release-manifest.json)
    source_file="$HSERVER_MANUAL_RELEASE_TEST_FEED/release-manifest.json" ;;
  http://manual-release.example/release-manifest.json.sig)
    source_file="$HSERVER_MANUAL_RELEASE_TEST_FEED/release-manifest.json.sig" ;;
  http://manual-release.example/*.tar.gz)
    source_file="$HSERVER_MANUAL_RELEASE_TEST_FEED/${url##*/}" ;;
  *) printf 'unexpected release URL: %s\n' "$url" >&2; exit 1 ;;
esac
cp "$source_file" "$output"
CURL
chmod 0755 "$tmp/curl"

run_bootstrap() {
  local trust_key=$1
  shift
  "${bootstrap_prefix[@]}" env \
    HSERVER_BOOTSTRAP_CURL="$tmp/curl" \
    HSERVER_MANUAL_RELEASE_TEST_FEED="$feed" \
    HSERVER_MANUAL_RELEASE_TEST_CURL_LOG="$curl_log" \
    HSERVER_MANUAL_RELEASE_TEST_LIFECYCLE_LOG="$lifecycle_log" \
    HSERVER_MANUAL_RELEASE_TEST_PRIVILEGED_MARKER="$privileged_marker" \
    "$repo_root/scripts/bootstrap-install.sh" \
      --manifest-url http://manual-release.example/release-manifest.json \
      --public-key-file "$trust_key" "$@"
}

: >"$lifecycle_log"
: >"$curl_log"
run_bootstrap "$public_key_file" >"$tmp/success.log"
grep -Fq "Verified signed HServer release: $version (linux/$arch)" "$tmp/success.log"
diff -u <(printf '%s\n' \
  'doctor:preflight' \
  'install:install' \
  'doctor:installed' \
  'install:next-steps') "$lifecycle_log"
[ -f "$privileged_marker" ]

assert_rejected_before_lifecycle() {
  local name=$1 expected=$2 trust_key=${3:-$public_key_file} archive_expected=${4:-0}
  : >"$lifecycle_log"
  : >"$curl_log"
  unlink "$privileged_marker" 2>/dev/null || true
  if run_bootstrap "$trust_key" >"$tmp/$name.log" 2>&1; then
    printf 'bootstrap accepted corrupted manual release asset: %s\n' "$name" >&2
    exit 1
  fi
  grep -Fq -- "$expected" "$tmp/$name.log" || {
    cat "$tmp/$name.log" >&2
    printf 'bootstrap failure did not identify %s\n' "$name" >&2
    exit 1
  }
  if (( archive_expected )); then
    grep -Fq -- "http://manual-release.example/$package_name.tar.gz" "$curl_log" || {
      printf 'rejected %s did not fetch the signed archive for verification\n' "$name" >&2
      exit 1
    }
  elif grep -Fq -- "http://manual-release.example/$package_name.tar.gz" "$curl_log"; then
    printf 'rejected %s fetched the archive before trust verification\n' "$name" >&2
    exit 1
  fi
  [ ! -s "$lifecycle_log" ] || {
    printf 'rejected %s reached packaged lifecycle\n' "$name" >&2
    exit 1
  }
  [ ! -e "$privileged_marker" ] || {
    printf 'rejected %s reached privileged mutation\n' "$name" >&2
    exit 1
  }
}

# A manifest edit invalidates its detached signature, so the archive is never
# fetched and no package lifecycle command can be reached.
printf '\n' >>"$feed/release-manifest.json"
assert_rejected_before_lifecycle manifest \
  'release manifest signature verification failed'
cp "$tmp/good-manifest" "$feed/release-manifest.json"

# A corrupt detached signature is rejected against the trusted public key.
python3 - "$feed/release-manifest.json.sig" <<'PY'
import base64
import pathlib
import sys

pathlib.Path(sys.argv[1]).write_text(base64.b64encode(bytes(64)).decode("ascii") + "\n", encoding="ascii")
PY
assert_rejected_before_lifecycle signature \
  'release manifest signature verification failed'
cp "$tmp/good-signature" "$feed/release-manifest.json.sig"

# An archive byte change is rejected against the digest authenticated by the
# signed manifest before extraction or the packaged installer is started.
python3 - "$archive" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
value = bytearray(path.read_bytes())
value[-1] ^= 1
path.write_bytes(value)
PY
assert_rejected_before_lifecycle archive \
  'release archive checksum does not match the signed manifest' \
  "$public_key_file" 1
cp "$tmp/good-archive" "$archive"

# A validly encoded but untrusted key cannot authorize the signed manifest.
wrong_private_key="$tmp/wrong-release-private.pem"
wrong_public_key="$tmp/wrong-release-public.b64"
"$repo_root/scripts/generate-release-signing-key.sh" \
  "$wrong_private_key" "$wrong_public_key" >/dev/null
assert_rejected_before_lifecycle wrong-public-key \
  'release manifest signature verification failed' "$wrong_public_key"

printf '%s\n' 'manual signed release installation contract: OK'
