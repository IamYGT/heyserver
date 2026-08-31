#!/usr/bin/env bash
set -euo pipefail

# This wrapper is intentionally kept in the checked-in source tree. It never
# downloads or executes a shell pipeline. Adjacent SHA-256 files detect transfer
# damage, while a trusted signer fingerprint and detached Ed25519 signature
# authenticate the bootstrap before it is handed to sudo (or run directly when
# this process is already root).

usage() {
  cat >&2 <<'USAGE'
Usage:
  ./scripts/public-install.sh RELEASE_BASE_OR_REPOSITORY \
    [--version VERSION | --channel CHANNEL] \
    [--trusted-release-key-sha256 LOWERCASE_HEX] \
    [--vhosts-root ABSOLUTE_PATH] [--agent-only]

  ./scripts/public-install.sh --release-base HTTPS_URL \
    [--trusted-release-key-sha256 LOWERCASE_HEX] \
    [--vhosts-root ABSOLUTE_PATH] [--agent-only]
  ./scripts/public-install.sh --repository HTTPS_URL \
    [--version VERSION | --channel CHANNEL] \
    [--trusted-release-key-sha256 LOWERCASE_HEX] \
    [--vhosts-root ABSOLUTE_PATH] [--agent-only]

The destination must be an HTTPS release asset base or repository URL. A
repository URL is resolved to its release download directory; --version picks
a tagged release and --channel picks a named release channel. A release base
already points at the directory containing bootstrap-install.sh and
release-public-key.b64. The wrapper downloads those assets, their adjacent
SHA-256 files, and bootstrap-install.sh.sig into a private temporary directory.
It requires the raw Ed25519 key to match a trusted SHA-256 fingerprint, verifies
the detached bootstrap signature, then invokes the signed bootstrap without
piping downloaded bytes into a privileged command. Official staged wrappers
embed canonical fingerprints. Generic source/fork use requires
--trusted-release-key-sha256 or
HSERVER_PUBLIC_INSTALL_TRUSTED_RELEASE_KEY_SHA256.
With --agent-only, the bootstrap upgrades only an already-installed managed
agent and rejects --vhosts-root.

For tests, HSERVER_PUBLIC_INSTALL_CURL and HSERVER_PUBLIC_INSTALL_SUDO may
point to executable command shims.
USAGE
}

die() {
  printf 'hserver-public-install: %s\n' "$*" >&2
  exit 1
}

require_value() {
  local option=$1
  (( $# > 1 )) || die "missing value for $option"
  [[ -n $2 ]] || die "missing value for $option"
}

release_destination=
destination_kind=auto
version=
channel=
# stage-public-install.sh replaces only the value on the following marked line.
embedded_trusted_release_key_sha256_csv='' # HSERVER_RELEASE_TRUST_EMBED
trusted_release_key_sha256_inputs=()
vhosts_root=
vhosts_root_set=0
agent_only=0
positional_seen=0

while (( $# > 0 )); do
  case "$1" in
    --release-base|--base-url|--release-url)
      require_value "$@"
      (( positional_seen == 0 )) || die "release destination was supplied more than once"
      [[ -z $release_destination ]] || die "release destination was supplied more than once"
      release_destination=$2
      destination_kind=base
      shift 2
      ;;
    --repository|--repo)
      require_value "$@"
      (( positional_seen == 0 )) || die "release destination was supplied more than once"
      [[ -z $release_destination ]] || die "release destination was supplied more than once"
      release_destination=$2
      destination_kind=repository
      shift 2
      ;;
    --version|-v)
      require_value "$@"
      [[ -z $version ]] || die "--version was supplied more than once"
      [[ -z $channel ]] || die "--version and --channel cannot be combined"
      version=$2
      shift 2
      ;;
    --channel|-c)
      require_value "$@"
      [[ -z $channel ]] || die "--channel was supplied more than once"
      [[ -z $version ]] || die "--version and --channel cannot be combined"
      channel=$2
      shift 2
      ;;
    --trusted-release-key-sha256)
      require_value "$@"
      trusted_release_key_sha256_inputs+=("$2")
      shift 2
      ;;
    --vhosts-root)
      require_value "$@"
      (( vhosts_root_set == 0 )) || die "--vhosts-root was supplied more than once"
      vhosts_root=$2
      vhosts_root_set=1
      shift 2
      ;;
    --agent-only)
      agent_only=1
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    --)
      shift
      while (( $# > 0 )); do
        (( positional_seen == 0 )) || die "release destination was supplied more than once"
        release_destination=$1
        destination_kind=auto
        positional_seen=1
        shift
      done
      ;;
    -*)
      die "unknown argument: $1"
      ;;
    *)
      (( positional_seen == 0 )) || die "release destination was supplied more than once"
      [[ -z $release_destination ]] || die "release destination was supplied more than once"
      release_destination=$1
      positional_seen=1
      shift
      ;;
  esac
done

[[ -n $release_destination ]] || {
  usage
  exit 2
}

if (( agent_only && vhosts_root_set )); then
  die "--vhosts-root cannot be used with --agent-only"
fi

# Validate the path before any executable lookup or download. The same
# lexical contract is enforced by bootstrap-install.sh, but rejecting it here
# keeps invalid input from reaching sudo at all.
validate_vhosts_root() {
  local value=$1
  case "$value" in
    '') die "--vhosts-root must be an absolute path" ;;
    /*) ;;
    *) die "--vhosts-root must be an absolute path" ;;
  esac
  [[ $value != / ]] || die "refusing unsafe path: $value"
  [[ $value != *[[:space:]]* ]] || die "--vhosts-root must not contain whitespace"
  [[ $value != *..* ]] || die "--vhosts-root contains an unsafe path traversal sequence"
  [[ $value =~ ^/[A-Za-z0-9._/+:-]+$ ]] || die "--vhosts-root contains unsafe path characters"
  case "$value" in
    /etc|/var|/usr|/usr/local) die "refusing unsafe path: $value" ;;
  esac
}

if (( vhosts_root_set )); then
  validate_vhosts_root "$vhosts_root"
fi

# URL parsing and release selector construction stay in Python so malformed
# ports, encoded traversal, and Unicode/control characters cannot be treated
# differently by shell and URL parsers. The output is one normalized asset
# base URL and is consumed only as a single shell word.
resolve_release_base() {
  python3 - "$release_destination" "$destination_kind" "$version" "$channel" <<'PY'
import re
import sys
from urllib.parse import unquote, urlsplit, urlunsplit

raw, kind, version, channel = sys.argv[1:]

def fail(message: str) -> None:
    raise SystemExit(message)

if not raw:
    fail("release destination is required")
if any(ord(char) < 0x20 or ord(char) == 0x7f or char.isspace() for char in raw):
    fail("release destination contains whitespace or control characters")

try:
    parts = urlsplit(raw)
except ValueError as exc:
    fail(f"release destination is not a valid URL: {exc}")

if parts.scheme.lower() != "https":
    fail("release destination must use HTTPS")
try:
    hostname = parts.hostname
except ValueError as exc:
    fail(f"release destination has an invalid host: {exc}")
if not parts.netloc or not hostname:
    fail("release destination must be an absolute HTTPS URL")
if parts.username is not None or parts.password is not None or "@" in parts.netloc:
    fail("release destination must not contain userinfo")
try:
    parts.port
except ValueError as exc:
    fail(f"release destination has an invalid port: {exc}")
if parts.query or parts.fragment:
    fail("release destination must not contain a query or fragment")

decoded_path = unquote(parts.path)
if any(ord(char) < 0x20 or ord(char) == 0x7f or char.isspace() for char in decoded_path):
    fail("release destination contains encoded whitespace or control characters")
if "\\" in decoded_path:
    fail("release destination contains an unsafe path separator")
path_parts = [part for part in decoded_path.split("/") if part]
if any(part in {".", ".."} for part in path_parts):
    fail("release destination contains an unsafe path traversal sequence")

if version and channel:
    fail("--version and --channel cannot be combined")
if version and not re.fullmatch(r"v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
    fail("--version must be a stable major.minor.patch value")
if channel and not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}", channel):
    fail("--channel contains unsafe characters")

# Keep the original path spelling (apart from its trailing slash) so an
# explicitly supplied release base is forwarded byte-for-byte to the assets.
path = parts.path.rstrip("/")
if not path:
    path = ""

release_pattern = re.search(r"/releases/(?:latest/download|download/[^/]+|[A-Za-z0-9._-]+/download)$", path)
already_release_base = path.endswith("/download") or bool(release_pattern)
release_root = path.endswith("/releases")
repository_path = kind == "repository" and not release_root
if kind == "auto":
    # GitHub/GitLab repository URLs are unambiguous. Other hosts are treated
    # as an explicit asset base unless the caller selected a release selector.
    host = hostname.lower().rstrip(".")
    if host in {"github.com", "gitlab.com"} and not already_release_base and not release_root:
        repository_path = True
    elif (version or channel) and not already_release_base:
        repository_path = True

if repository_path:
    if already_release_base:
        fail("repository destination already names a release download base")
    selector = f"releases/download/{version}" if version else f"releases/{channel or 'latest'}/download"
    path = path + "/" + selector
elif release_root:
    selector = f"download/{version}" if version else f"{channel or 'latest'}/download"
    path = path + "/" + selector
elif path.endswith("/releases/latest"):
    if version or channel:
        fail("release base already names the latest channel; use a repository destination for a selector")
    path = path + "/download"
elif version or channel:
    if path.endswith("/releases/download") and version:
        path = path + "/" + version
    elif already_release_base:
        fail("release base already names a release selector")
    else:
        fail("--version/--channel requires a repository destination or /releases base")

if path == "/":
    path = ""
print(urlunsplit(("https", parts.netloc, path, "", "")))
PY
}

command -v python3 >/dev/null 2>&1 || die "required command not found: python3"
release_base=$(resolve_release_base) || die "invalid release destination"
manifest_url="${release_base%/}/release-manifest.json"

trusted_release_key_sha256_csv=$(python3 - \
  "$embedded_trusted_release_key_sha256_csv" \
  "${HSERVER_PUBLIC_INSTALL_TRUSTED_RELEASE_KEY_SHA256:-}" \
  "${trusted_release_key_sha256_inputs[@]}" <<'PY'
import re
import sys

embedded_csv, environment_csv, *arguments = sys.argv[1:]
pattern = re.compile(r"^[0-9a-f]{64}$")

def parse(values, label):
    result = []
    for value in values:
        if not value:
            continue
        for fingerprint in value.split(","):
            if not pattern.fullmatch(fingerprint):
                raise SystemExit(f"{label} contains an invalid signer fingerprint")
            if fingerprint in result:
                raise SystemExit(f"{label} contains a duplicate signer fingerprint")
            result.append(fingerprint)
    if len(result) > 8:
        raise SystemExit(f"{label} contains more than eight signer fingerprints")
    return result

embedded = parse([embedded_csv], "embedded trust set")
explicit = parse([environment_csv, *arguments], "explicit trust set")
if embedded:
    unknown = [fingerprint for fingerprint in explicit if fingerprint not in embedded]
    if unknown:
        raise SystemExit("explicit signer fingerprint is not present in the embedded trust set")
    trusted = explicit or embedded
else:
    if not explicit:
        raise SystemExit("a trusted release key fingerprint is required")
    trusted = explicit
print(",".join(trusted))
PY
) || die "invalid release signer trust set"

# Test injection accepts an executable path, not a shell fragment. Keeping
# command and arguments separate prevents environment values from becoming a
# second command language.
curl_bin=${HSERVER_PUBLIC_INSTALL_CURL:-${HSERVER_PUBLIC_INSTALL_CURL_BIN:-curl}}
sudo_bin=${HSERVER_PUBLIC_INSTALL_SUDO:-${HSERVER_PUBLIC_INSTALL_SUDO_BIN:-sudo}}
command -v "$curl_bin" >/dev/null 2>&1 || die "required command not found: $curl_bin"

sudo_prefix=()
if [[ -n ${HSERVER_PUBLIC_INSTALL_SUDO:-} || -n ${HSERVER_PUBLIC_INSTALL_SUDO_BIN:-} || $EUID -ne 0 ]]; then
  command -v "$sudo_bin" >/dev/null 2>&1 || die "required command not found: $sudo_bin"
  sudo_prefix=("$sudo_bin")
fi

command -v sha256sum >/dev/null 2>&1 || die "required command not found: sha256sum"
command -v openssl >/dev/null 2>&1 || die "required command not found: openssl"
command -v mktemp >/dev/null 2>&1 || die "required command not found: mktemp"
command -v stat >/dev/null 2>&1 || die "required command not found: stat"
command -v chmod >/dev/null 2>&1 || die "required command not found: chmod"

umask 077
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hserver-public-install.XXXXXXXX")
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  find "$tmp" -xdev -depth -delete 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM

bootstrap_file="$tmp/bootstrap-install.sh"
bootstrap_checksum="$tmp/bootstrap-install.sh.sha256"
bootstrap_signature="$tmp/bootstrap-install.sh.sig"
bootstrap_signature_raw="$tmp/bootstrap-install.sh.sig.raw"
public_key_file="$tmp/release-public-key.b64"
public_key_checksum="$tmp/release-public-key.b64.sha256"
public_key_der="$tmp/release-public-key.der"

download_asset() {
  local name=$1
  local destination=$2
  local url="${release_base%/}/$name"
  "$curl_bin" -q -fsSL --proto '=https' --proto-redir '=https' --max-time 120 \
    --output "$destination" "$url" || die "download failed: $url"
  [[ -f $destination && ! -L $destination && -s $destination ]] \
    || die "download was empty or not a regular file: $url"
}

printf 'Downloading verified Heyserver trust assets from %s\n' "$release_base"
download_asset bootstrap-install.sh "$bootstrap_file"
download_asset bootstrap-install.sh.sha256 "$bootstrap_checksum"
download_asset bootstrap-install.sh.sig "$bootstrap_signature"
download_asset release-public-key.b64 "$public_key_file"
download_asset release-public-key.b64.sha256 "$public_key_checksum"

verify_checksum() {
  local asset=$1 checksum=$2 name=$3
  python3 - "$asset" "$checksum" "$name" <<'PY'
import hashlib
import pathlib
import re
import stat
import sys

asset_path = pathlib.Path(sys.argv[1])
checksum_path = pathlib.Path(sys.argv[2])
expected_name = sys.argv[3]

for path, label in ((asset_path, "asset"), (checksum_path, "checksum")):
    try:
        metadata = path.lstat()
    except FileNotFoundError:
        raise SystemExit(f"{label} does not exist: {path}")
    if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
        raise SystemExit(f"{label} must be a regular file and not a symlink: {path}")

if checksum_path.stat().st_size > 4096:
    raise SystemExit(f"checksum exceeds 4 KiB: {checksum_path}")
try:
    raw = checksum_path.read_bytes()
except OSError as exc:
    raise SystemExit(f"could not read checksum: {exc}")
if raw.endswith(b"\n"):
    raw = raw[:-1]
if not raw or b"\n" in raw or b"\r" in raw:
    raise SystemExit(f"checksum must contain exactly one line: {checksum_path}")
try:
    line = raw.decode("ascii")
except UnicodeDecodeError as exc:
    raise SystemExit(f"checksum is not ASCII: {exc}")

match = re.fullmatch(r"([0-9a-f]{64})  (\*?[A-Za-z0-9._+-]+)", line)
if not match or match.group(2).lstrip("*") != expected_name:
    raise SystemExit(f"checksum has an invalid manifest for {expected_name}")
expected = match.group(1)
hasher = hashlib.sha256()
with asset_path.open("rb") as handle:
    for block in iter(lambda: handle.read(1024 * 1024), b""):
        hasher.update(block)
actual = hasher.hexdigest()
if actual != expected:
    raise SystemExit(f"checksum mismatch for {expected_name}")
PY
}

verify_checksum "$bootstrap_file" "$bootstrap_checksum" bootstrap-install.sh \
  || die "bootstrap checksum verification failed"
verify_checksum "$public_key_file" "$public_key_checksum" release-public-key.b64 \
  || die "public key checksum verification failed"

# A checksum supplied by the release mirror does not establish signer identity.
# Confirm the published key has the pinned raw-key fingerprint, build its DER
# form, and decode the detached signature before entering root.
if ! key_fingerprint=$(python3 - "$public_key_file" "$bootstrap_signature" \
  "$public_key_der" "$bootstrap_signature_raw" <<'PY'
import base64
import binascii
import hashlib
import pathlib
import stat
import sys

path = pathlib.Path(sys.argv[1])
signature_path = pathlib.Path(sys.argv[2])
key_output = pathlib.Path(sys.argv[3])
signature_output = pathlib.Path(sys.argv[4])
metadata = path.lstat()
if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
    raise SystemExit("public key must be a regular file and not a symlink")
raw = path.read_bytes()
if raw.endswith(b"\n"):
    raw = raw[:-1]
if not raw or b"\n" in raw or b"\r" in raw or any(byte > 0x7f for byte in raw):
    raise SystemExit("public key must contain one ASCII base64 line")
try:
    decoded = base64.b64decode(raw, validate=True)
except (binascii.Error, ValueError) as exc:
    raise SystemExit("public key is not valid base64") from exc
if len(decoded) != 32 or base64.b64encode(decoded) != raw:
    raise SystemExit("public key must be canonical base64 for exactly 32 bytes")
signature_metadata = signature_path.lstat()
if stat.S_ISLNK(signature_metadata.st_mode) or not stat.S_ISREG(signature_metadata.st_mode):
    raise SystemExit("bootstrap signature must be a regular file and not a symlink")
if signature_metadata.st_size > 4096:
    raise SystemExit("bootstrap signature exceeds 4 KiB")
signature_raw = signature_path.read_bytes()
if signature_raw.endswith(b"\n"):
    signature_raw = signature_raw[:-1]
if not signature_raw or b"\n" in signature_raw or b"\r" in signature_raw:
    raise SystemExit("bootstrap signature must contain one ASCII base64 line")
try:
    signature = base64.b64decode(signature_raw, validate=True)
except (binascii.Error, ValueError) as exc:
    raise SystemExit("bootstrap signature is not valid base64") from exc
if len(signature) != 64 or base64.b64encode(signature) != signature_raw:
    raise SystemExit("bootstrap signature must be canonical base64 for exactly 64 bytes")
key_output.write_bytes(bytes.fromhex("302a300506032b6570032100") + decoded)
signature_output.write_bytes(signature)
print(hashlib.sha256(decoded).hexdigest())
PY
); then
  die "public key validation failed"
fi

case ",$trusted_release_key_sha256_csv," in
  *",$key_fingerprint,"*) ;;
  *) die "release public key does not match a trusted signer: $key_fingerprint" ;;
esac

openssl pkeyutl -verify -pubin -keyform DER -inkey "$public_key_der" \
  -rawin -in "$bootstrap_file" -sigfile "$bootstrap_signature_raw" \
  >/dev/null 2>&1 || die "bootstrap signature verification failed"

chmod 0700 "$bootstrap_file"

bootstrap_args=(
  "$bootstrap_file"
  --manifest-url "$manifest_url"
  --public-key-file "$public_key_file"
)
if (( agent_only )); then
  bootstrap_args+=(--agent-only)
fi
if (( vhosts_root_set )); then
  bootstrap_args+=(--vhosts-root "$vhosts_root")
fi

printf 'Verified release signer %s and bootstrap signature; invoking signed bootstrap.\n' \
  "$key_fingerprint"
"${sudo_prefix[@]}" "${bootstrap_args[@]}"
