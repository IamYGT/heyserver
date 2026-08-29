#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage:
  sudo ./bootstrap-install.sh --manifest-url URL \
    (--public-key BASE64 | --public-key-file FILE) [--vhosts-root ABSOLUTE_PATH] [--agent-only]

Downloads a stable HServer release through a signed schema-v1 manifest,
verifies the selected Linux archive, and runs the packaged native installer.
With --agent-only, the same verified package is used only to upgrade an
already-installed managed HServer agent; the panel lifecycle is never run.
At least one Ed25519 public key is required. Comma-separated key rotation sets
are accepted, up to eight unique keys.
EOF
  exit 2
}

die() {
  printf 'hserver-bootstrap: %s\n' "$*" >&2
  exit 1
}

manifest_url=${HSERVER_BOOTSTRAP_MANIFEST_URL:-}
public_key_inputs=()
public_key_files=()
vhosts_root=
vhosts_root_set=0
agent_only=0
while (( $# )); do
  case "$1" in
    --manifest-url)
      (( $# >= 2 )) || usage
      manifest_url=$2
      shift 2
      ;;
    --public-key)
      (( $# >= 2 )) || usage
      public_key_inputs+=("$2")
      shift 2
      ;;
    --public-key-file)
      (( $# >= 2 )) || usage
      public_key_files+=("$2")
      shift 2
      ;;
    --vhosts-root)
      (( $# >= 2 )) || usage
      vhosts_root=$2
      vhosts_root_set=1
      shift 2
      ;;
    --agent-only)
      agent_only=1
      shift
      ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done

if (( agent_only && vhosts_root_set )); then
  die "--vhosts-root cannot be used with --agent-only"
fi

validate_vhosts_root() {
  local value=$1
  case "$value" in
    '') die "--vhosts-root must be an absolute path" ;;
    /*) ;;
    *) die "--vhosts-root must be an absolute path" ;;
  esac
  [[ "$value" != / ]] || die "refusing unsafe path: $value"
  [[ "$value" != *[[:space:]]* ]] || die "--vhosts-root must not contain whitespace"
  [[ "$value" != *..* ]] || die "--vhosts-root contains an unsafe path traversal sequence"
  [[ "$value" =~ ^/[A-Za-z0-9._/+:-]+$ ]] \
    || die "--vhosts-root contains unsafe path characters"
  case "$value" in
    /etc|/var|/usr|/usr/local) die "refusing unsafe path: $value" ;;
  esac
}

if (( vhosts_root_set )); then
  validate_vhosts_root "$vhosts_root"
fi

[[ $EUID -eq 0 ]] || die "native installation must run as root"
[[ -n "$manifest_url" ]] || usage
if [[ -n ${HSERVER_BOOTSTRAP_PUBLIC_KEYS:-} ]]; then
  public_key_inputs+=("$HSERVER_BOOTSTRAP_PUBLIC_KEYS")
fi

curl_bin=${HSERVER_BOOTSTRAP_CURL:-curl}
for command_name in "$curl_bin" python3 openssl sha256sum stat; do
  command -v "$command_name" >/dev/null 2>&1 || die "required command not found: $command_name"
done

for public_key_file in "${public_key_files[@]}"; do
  [[ -f "$public_key_file" ]] || die "release public key file not found: $public_key_file"
  (( $(stat -c '%s' "$public_key_file") <= 4096 )) \
    || die "release public key file exceeds 4 KiB: $public_key_file"
  public_key_inputs+=("$(<"$public_key_file")")
done
(( ${#public_key_inputs[@]} > 0 )) || die "at least one Ed25519 public key is required"

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

tmp=$(mktemp -d /tmp/hserver-bootstrap-XXXXXXXX)
cleanup() {
  find "$tmp" -xdev -depth -delete 2>/dev/null || true
}
trap cleanup EXIT INT TERM
umask 077
manifest="$tmp/release-manifest.json"
signature="$tmp/release-manifest.json.sig"
signature_raw="$tmp/release-manifest.sig.raw"
key_dir="$tmp/public-keys"
metadata_dir="$tmp/metadata"
archive="$tmp/release.tar.gz"
extract_dir="$tmp/extracted"
mkdir -m 0700 "$key_dir" "$metadata_dir" "$extract_dir"

download() {
  local url=$1 destination=$2
  "$curl_bin" -fsSL --max-time 120 --output "$destination" "$url" \
    || die "download failed: $url"
  [[ -s "$destination" ]] || die "download was empty: $url"
}

download "$manifest_url" "$manifest"
(( $(stat -c '%s' "$manifest") <= 524288 )) || die "release manifest exceeds 512 KiB"

signature_url=$(python3 - "$manifest_url" <<'PY'
import sys
from urllib.parse import urlsplit, urlunsplit

value = sys.argv[1]
parts = urlsplit(value)
if parts.scheme not in {"http", "https"} or not parts.hostname or parts.username or parts.password:
    raise SystemExit("release manifest URL must be HTTP(S) without credentials")
if any(ord(char) < 32 or char.isspace() for char in value):
    raise SystemExit("release manifest URL contains whitespace or control characters")
print(urlunsplit((parts.scheme, parts.netloc, parts.path + ".sig", parts.query, "")))
PY
) || die "invalid release manifest URL"
download "$signature_url" "$signature"
(( $(stat -c '%s' "$signature") <= 4096 )) || die "release signature exceeds 4 KiB"

python3 - "$signature" "$signature_raw" "$key_dir" "$metadata_dir/trust-set" "${public_key_inputs[@]}" <<'PY'
import base64
import binascii
import pathlib
import sys

signature_path = pathlib.Path(sys.argv[1])
signature_output = pathlib.Path(sys.argv[2])
key_dir = pathlib.Path(sys.argv[3])
trust_output = pathlib.Path(sys.argv[4])
inputs = sys.argv[5:]
values = []
for item in inputs:
    values.extend(item.split(","))
if not values or len(values) > 8 or any(not value for value in values):
    raise SystemExit("release trust set must contain one to eight non-empty keys")
if len(set(values)) != len(values):
    raise SystemExit("release trust set contains duplicate keys")
trust_output.write_text(",".join(values), encoding="ascii")

def decode(value: str, label: str, expected: int) -> bytes:
    try:
        decoded = base64.b64decode(value, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise SystemExit(f"{label} is not valid base64") from exc
    if len(decoded) != expected:
        raise SystemExit(f"{label} must decode to {expected} bytes")
    return decoded

signature_value = signature_path.read_text(encoding="ascii").strip()
signature_output.write_bytes(decode(signature_value, "release signature", 64))
spki_prefix = bytes.fromhex("302a300506032b6570032100")
for index, value in enumerate(values):
    (key_dir / f"key-{index}.der").write_bytes(
        spki_prefix + decode(value, f"release public key {index + 1}", 32)
    )
PY

signature_verified=0
for public_key in "$key_dir"/*.der; do
  if openssl pkeyutl -verify -pubin -keyform DER -inkey "$public_key" \
    -rawin -in "$manifest" -sigfile "$signature_raw" >/dev/null 2>&1; then
    signature_verified=1
    break
  fi
done
(( signature_verified )) || die "release manifest signature verification failed"

python3 - "$manifest" "$metadata_dir" "$arch" <<'PY'
import json
import pathlib
import re
import sys
from urllib.parse import urlsplit

manifest_path = pathlib.Path(sys.argv[1])
output = pathlib.Path(sys.argv[2])
arch = sys.argv[3]

def reject_duplicates(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError(f"duplicate JSON field: {key}")
        value[key] = item
    return value

try:
    document = json.loads(manifest_path.read_bytes(), object_pairs_hook=reject_duplicates)
except (json.JSONDecodeError, UnicodeDecodeError, ValueError) as exc:
    raise SystemExit(f"invalid release manifest JSON: {exc}") from exc
if not isinstance(document, dict):
    raise SystemExit("release manifest must be an object")
allowed_root = {"schema_version", "version", "published_at", "release_notes_url", "artifacts"}
if set(document) - allowed_root:
    raise SystemExit("release manifest contains unknown fields")
if document.get("schema_version") != 1:
    raise SystemExit("unsupported release manifest schema")
version = document.get("version")
if not isinstance(version, str) or not re.fullmatch(r"v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
    raise SystemExit("release version must be stable SemVer")
artifacts = document.get("artifacts")
if not isinstance(artifacts, dict):
    raise SystemExit("release artifacts must be an object")
artifact = artifacts.get(f"linux_{arch}")
if not isinstance(artifact, dict):
    raise SystemExit(f"release has no linux_{arch} artifact")
if set(artifact) - {"url", "sha256", "size_bytes"}:
    raise SystemExit("release artifact contains unknown fields")
url = artifact.get("url")
if not isinstance(url, str):
    raise SystemExit("release artifact URL is missing")
parts = urlsplit(url)
if parts.scheme not in {"http", "https"} or not parts.hostname or parts.username or parts.password:
    raise SystemExit("release artifact URL must be HTTP(S) without credentials")
if any(ord(char) < 32 or char.isspace() for char in url):
    raise SystemExit("release artifact URL contains whitespace or control characters")
digest = artifact.get("sha256")
if not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest):
    raise SystemExit("release artifact SHA-256 is invalid")
size = artifact.get("size_bytes", "")
if size != "" and (isinstance(size, bool) or not isinstance(size, int) or size < 0):
    raise SystemExit("release artifact size is invalid")
for name, value in {"version": version, "url": url, "sha256": digest, "size": str(size)}.items():
    (output / name).write_text(value, encoding="utf-8")
PY

version=$(<"$metadata_dir/version")
artifact_url=$(<"$metadata_dir/url")
expected_sha=$(<"$metadata_dir/sha256")
expected_size=$(<"$metadata_dir/size")
download "$artifact_url" "$archive"
actual_size=$(stat -c '%s' "$archive")
(( actual_size <= 1073741824 )) || die "release archive exceeds 1 GiB"
if [[ -n "$expected_size" && "$actual_size" != "$expected_size" ]]; then
  die "release archive size does not match the signed manifest"
fi
actual_sha=$(sha256sum "$archive" | awk '{print $1}')
[[ "$actual_sha" == "$expected_sha" ]] || die "release archive checksum does not match the signed manifest"

package_mode=panel
if (( agent_only )); then
  package_mode=agent
fi
package_dir=$(python3 - "$archive" "$extract_dir" "$version" "$arch" \
  "$package_mode" <<'PY'
import pathlib
import struct
import sys
import tarfile

archive = pathlib.Path(sys.argv[1])
destination = pathlib.Path(sys.argv[2])
version = sys.argv[3]
arch = sys.argv[4]
root = f"hserver-panel-{version}-linux-{arch}"
required = {
    "VERSION",
    "hserver-agent",
}
if sys.argv[5] == "agent":
    required.add("agent-install.sh")
else:
    required.update({"hserver-panel", "hserverctl", "install.sh", "doctor.sh"})
seen = set()
total = 0
with tarfile.open(archive, mode="r:gz") as handle:
    members = handle.getmembers()
    for member in members:
        path = pathlib.PurePosixPath(member.name)
        if path.is_absolute() or not path.parts or any(part in {"", ".", ".."} for part in path.parts):
            raise SystemExit(f"release archive contains an unsafe path: {member.name}")
        if path.parts[0] != root or len(path.parts) == 1 and not member.isdir():
            raise SystemExit(f"release archive entry escapes the package root: {member.name}")
        if member.name in seen:
            raise SystemExit(f"release archive contains a duplicate path: {member.name}")
        seen.add(member.name)
        if not (member.isdir() or member.isfile()):
            raise SystemExit(f"release archive contains an unsupported entry type: {member.name}")
        if member.size < 0 or member.size > 536870912:
            raise SystemExit(f"release archive entry is too large: {member.name}")
        total += member.size
        if total > 1073741824:
            raise SystemExit("expanded release archive exceeds 1 GiB")
    relative = {str(pathlib.PurePosixPath(name).relative_to(root)) for name in seen if name != root}
    missing = sorted(required - relative)
    if missing:
        raise SystemExit("release package is missing: " + ", ".join(missing))
    handle.extractall(destination, filter="data")

package = destination / root
if (package / "VERSION").read_text(encoding="utf-8").strip() != version:
    raise SystemExit("release package VERSION does not match the signed manifest")

expected_machine = {"amd64": 62, "arm64": 183}[arch]
binary_names = ("hserver-agent",) if sys.argv[5] == "agent" else ("hserver-panel", "hserver-agent", "hserverctl")
for name in binary_names:
    data = (package / name).read_bytes()[:20]
    if len(data) < 20 or data[:4] != b"\x7fELF" or data[4] != 2 or data[5] not in {1, 2}:
        raise SystemExit(f"release binary is not a supported 64-bit ELF: {name}")
    byte_order = "<" if data[5] == 1 else ">"
    machine = struct.unpack(byte_order + "H", data[18:20])[0]
    if machine != expected_machine:
        raise SystemExit(f"release binary architecture mismatch: {name}")

script_names = ("agent-install.sh",) if sys.argv[5] == "agent" else ("install.sh", "doctor.sh")
for name in script_names:
    path = package / name
    if not path.is_file() or path.is_symlink() or (
        sys.argv[5] == "agent" and not path.stat().st_mode & 0o111
    ):
        raise SystemExit(f"release lifecycle tool is invalid: {name}")
print(package)
PY
) || die "release archive validation failed"

agent_root_prefix=${HSERVER_AGENT_ROOT_PREFIX:-}
agent_root_path() {
  local logical_path=$1
  if [[ -n "$agent_root_prefix" && "$agent_root_prefix" != / ]]; then
    printf '%s%s\n' "${agent_root_prefix%/}" "$logical_path"
  else
    printf '%s\n' "$logical_path"
  fi
}

if (( agent_only )); then
  agent_binary_path=${HSERVER_AGENT_BINARY_PATH:-$(agent_root_path /usr/local/bin/hserver-agent)}
  agent_config_path=${HSERVER_AGENT_CONFIG_FILE:-$(agent_root_path /etc/hserver-agent.env)}
  [[ -f "$agent_binary_path" && -s "$agent_binary_path" ]] \
    || die "agent-only mode requires an existing managed agent installation (missing binary: $agent_binary_path)"
  [[ -f "$agent_config_path" && -s "$agent_config_path" ]] \
    || die "agent-only mode requires an existing managed agent installation (missing config: $agent_config_path)"
fi

if (( agent_only )); then
  printf 'Verified signed HServer agent release: %s (linux/%s)\n' "$version" "$arch"
  "$package_dir/agent-install.sh" upgrade --binary "$package_dir/hserver-agent"
  printf 'HServer agent %s upgrade completed. Existing configuration and token destination were preserved.\n' "$version"
  exit 0
fi
printf 'Verified signed HServer release: %s (linux/%s)\n' "$version" "$arch"
"$package_dir/doctor.sh" preflight
install_args=(install)
if (( vhosts_root_set )); then
  install_args+=(--vhosts-root "$vhosts_root")
fi
HSERVER_INSTALL_UPDATE_MANIFEST_URL="$manifest_url" \
HSERVER_INSTALL_UPDATE_MANIFEST_PUBLIC_KEYS="$(<"$metadata_dir/trust-set")" \
HSERVER_INSTALL_DEFER_NEXT_STEPS=1 \
  "$package_dir/install.sh" "${install_args[@]}"
"$package_dir/doctor.sh" installed
printf 'HServer %s installation completed. Protected credentials remain in /etc/hserver/hserver.env.\n' "$version"
"$package_dir/install.sh" next-steps
