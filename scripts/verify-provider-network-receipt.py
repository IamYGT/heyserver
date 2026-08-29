#!/usr/bin/env python3
"""Validate a protected HServer provider-network acceptance receipt."""

from __future__ import annotations

import argparse
import base64
import binascii
import hashlib
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.parse import urlsplit


EXPECTED_CHECKS_V1 = {
    "public_https_path",
    "acceptance_runs_on_panel_kernel",
    "server_observed_online",
    "protocol_v1",
    "separate_kernel_boot_id",
    "required_capabilities",
    "writable_remote_terminal",
    "process_inventory",
    "stable_identity_process_signal",
    "disabled_capability_rejected",
    "rejected_task_not_persisted",
}
EXPECTED_CHECKS_V2 = EXPECTED_CHECKS_V1 | {
    "cli_release_identity",
    "managed_node_architecture",
}
EXPECTED_FIELDS_V1 = {
    "schema_version",
    "status",
    "accepted_at",
    "panel_origin",
    "node_id",
    "panel_version",
    "panel_arch",
    "agent_version",
    "panel_identity_method",
    "disabled_capability",
    "terminal_close_mode",
    "marker_allocation_bytes",
    "checks",
}
EXPECTED_FIELDS_V2 = EXPECTED_FIELDS_V1 | {"cli_version", "node_arch"}
EXPECTED_FIELDS_V3 = EXPECTED_FIELDS_V2 | {"panel_commit"}
NODE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
DURATION = re.compile(r"^([1-9][0-9]*)([mhd])$")
MAX_RECEIPT_BYTES = 64 << 10
MIN_MARKER_BYTES = 16 << 20
MAX_MARKER_BYTES = 96 << 20


class ReceiptError(ValueError):
    pass


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Verify a protected HServer provider-network acceptance receipt."
    )
    parser.add_argument("receipt", type=Path)
    parser.add_argument("--max-age", default="24h", metavar="DURATION")
    parser.add_argument("--panel-version")
    parser.add_argument("--panel-commit")
    parser.add_argument("--panel-arch", choices=("amd64", "arm64"))
    parser.add_argument("--cli-version")
    parser.add_argument("--agent-version")
    parser.add_argument("--node-arch", choices=("amd64", "arm64"))
    parser.add_argument("--node")
    parser.add_argument("--panel-origin")
    parser.add_argument("--signature", type=Path)
    parser.add_argument("--public-key", type=Path)
    parser.add_argument(
        "--require-signature",
        action="store_true",
        help="fail unless --signature and --public-key verify the exact receipt bytes",
    )
    parser.add_argument(
        "--require-schema",
        choices=("1", "2", "3", "any"),
        default="3",
        help="required receipt schema; v3 is the current release-evidence default",
    )
    parser.add_argument(
        "--require-panel-identity",
        choices=("boot_id", "hostname_compatibility", "any"),
        default="boot_id",
    )
    parser.add_argument(
        "--require-terminal-close",
        choices=("normal", "legacy_agent_eio", "any"),
        default="normal",
    )
    return parser.parse_args()


def fail(message: str) -> None:
    raise ReceiptError(message)


def parse_duration(value: str) -> timedelta:
    match = DURATION.fullmatch(value)
    if not match:
        fail("--max-age must use a positive whole-number suffix: m, h, or d")
    amount = int(match.group(1))
    unit = match.group(2)
    seconds = amount * {"m": 60, "h": 3600, "d": 86400}[unit]
    if seconds > 30 * 86400:
        fail("--max-age must not exceed 30d")
    return timedelta(seconds=seconds)


def reject_duplicate_keys(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            fail(f"receipt contains duplicate JSON key: {key}")
        result[key] = value
    return result


def read_receipt(path: Path) -> dict[str, object]:
    try:
        metadata = path.lstat()
    except FileNotFoundError:
        fail(f"receipt does not exist: {path}")
    if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
        fail("receipt must be a regular file and not a symlink")
    if stat.S_IMODE(metadata.st_mode) != 0o600:
        fail("receipt must have mode 0600")
    if metadata.st_uid != os.geteuid():
        fail("receipt must be owned by the current user")
    if metadata.st_size < 2 or metadata.st_size > MAX_RECEIPT_BYTES:
        fail(f"receipt size must be between 2 and {MAX_RECEIPT_BYTES} bytes")
    try:
        raw = path.read_text(encoding="utf-8")
        receipt = json.loads(raw, object_pairs_hook=reject_duplicate_keys)
    except UnicodeDecodeError as error:
        fail(f"receipt is not UTF-8: {error}")
    except json.JSONDecodeError as error:
        fail(f"receipt is not valid JSON: {error}")
    if not isinstance(receipt, dict):
        fail("receipt root must be a JSON object")
    return receipt


def required_text(receipt: dict[str, object], key: str, maximum: int = 128) -> str:
    value = receipt.get(key)
    if not isinstance(value, str) or not value.strip() or value != value.strip():
        fail(f"receipt field {key} must be a non-empty trimmed string")
    if len(value.encode("utf-8")) > maximum:
        fail(f"receipt field {key} exceeds {maximum} bytes")
    return value


def read_base64_artifact(path: Path, label: str, decoded_size: int) -> bytes:
    try:
        metadata = path.lstat()
    except FileNotFoundError:
        fail(f"{label} does not exist: {path}")
    if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISREG(metadata.st_mode):
        fail(f"{label} must be a regular file and not a symlink")
    if stat.S_IMODE(metadata.st_mode) not in (0o600, 0o644):
        fail(f"{label} must have mode 0600 or 0644")
    if metadata.st_uid != os.geteuid():
        fail(f"{label} must be owned by the current user")
    if metadata.st_size < 2 or metadata.st_size > 1024:
        fail(f"{label} size must be between 2 and 1024 bytes")
    try:
        encoded = path.read_bytes()
    except OSError as error:
        fail(f"could not read {label}: {error}")
    if encoded.endswith(b"\n"):
        encoded = encoded[:-1]
    if not encoded or b"\n" in encoded or b"\r" in encoded:
        fail(f"{label} must contain one canonical base64 line")
    try:
        decoded = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError):
        fail(f"{label} is not valid base64")
    if base64.b64encode(decoded) != encoded:
        fail(f"{label} is not canonical base64")
    if len(decoded) != decoded_size:
        fail(f"{label} must decode to exactly {decoded_size} bytes")
    return decoded


def verify_detached_signature(receipt_path: Path, signature_path: Path, public_key_path: Path) -> str:
    openssl = shutil.which("openssl")
    if openssl is None:
        fail("openssl is required for provider-network receipt signature verification")
    signature = read_base64_artifact(signature_path, "receipt signature", 64)
    public_key = read_base64_artifact(public_key_path, "receipt public key", 32)
    public_der = bytes.fromhex("302a300506032b6570032100") + public_key
    with tempfile.TemporaryDirectory(prefix="hserver-provider-receipt-verify-") as temporary:
        temporary_path = Path(temporary)
        public_path = temporary_path / "public.der"
        signature_raw_path = temporary_path / "signature.raw"
        public_path.write_bytes(public_der)
        signature_raw_path.write_bytes(signature)
        public_path.chmod(0o600)
        signature_raw_path.chmod(0o600)
        result = subprocess.run(
            [
                openssl,
                "pkeyutl",
                "-verify",
                "-pubin",
                "-keyform",
                "DER",
                "-inkey",
                str(public_path),
                "-rawin",
                "-in",
                str(receipt_path),
                "-sigfile",
                str(signature_raw_path),
            ],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
    if result.returncode != 0:
        fail("provider-network receipt Ed25519 signature verification failed")
    return hashlib.sha256(public_key).hexdigest()


def validate_origin(value: str) -> None:
    parsed = urlsplit(value)
    if parsed.scheme != "https" or not parsed.hostname:
        fail("receipt panel_origin must be an absolute HTTPS origin")
    if parsed.username is not None or parsed.password is not None:
        fail("receipt panel_origin must not contain credentials")
    if parsed.path not in ("", "/") or parsed.query or parsed.fragment:
        fail("receipt panel_origin must not contain a path, query, or fragment")
    try:
        _ = parsed.port
    except ValueError as error:
        fail(f"receipt panel_origin has an invalid port: {error}")


def parse_accepted_at(value: str) -> datetime:
    if not value.endswith("Z"):
        fail("receipt accepted_at must be an RFC3339 UTC timestamp ending in Z")
    try:
        accepted = datetime.fromisoformat(value[:-1] + "+00:00")
    except ValueError as error:
        fail(f"receipt accepted_at is invalid: {error}")
    if accepted.tzinfo != timezone.utc:
        fail("receipt accepted_at must use UTC")
    return accepted


def expect(actual: str, expected: str | None, label: str) -> None:
    if expected is not None and actual != expected:
        fail(f"receipt {label} mismatch: got {actual!r}, expected {expected!r}")


def validate(receipt: dict[str, object], args: argparse.Namespace) -> int:
    if (args.signature is None) != (args.public_key is None):
        fail("--signature and --public-key must be supplied together")
    if args.require_signature and args.signature is None:
        fail("--require-signature needs --signature and --public-key")
    schema_version = receipt.get("schema_version")
    if not isinstance(schema_version, int) or isinstance(schema_version, bool) or schema_version not in (1, 2, 3):
        fail("receipt schema_version must equal 1, 2, or 3")
    if args.require_schema != "any" and schema_version != int(args.require_schema):
        fail(f"receipt schema_version mismatch: got {schema_version!r}, expected {args.require_schema!r}")
    if schema_version == 3:
        expected_fields = EXPECTED_FIELDS_V3
        expected_checks = EXPECTED_CHECKS_V2
    elif schema_version == 2:
        expected_fields = EXPECTED_FIELDS_V2
        expected_checks = EXPECTED_CHECKS_V2
    else:
        expected_fields = EXPECTED_FIELDS_V1
        expected_checks = EXPECTED_CHECKS_V1
    if set(receipt) != expected_fields:
        fail(f"receipt must contain exactly the schema-v{schema_version} top-level fields")
    if receipt.get("status") != "passed":
        fail("receipt status must equal passed")

    panel_origin = required_text(receipt, "panel_origin", 2048)
    validate_origin(panel_origin)
    node_id = required_text(receipt, "node_id")
    if not NODE_ID.fullmatch(node_id):
        fail("receipt node_id is invalid")
    panel_version = required_text(receipt, "panel_version")
    panel_commit = required_text(receipt, "panel_commit") if schema_version == 3 else None
    panel_arch = required_text(receipt, "panel_arch", 16)
    if panel_arch not in ("amd64", "arm64"):
        fail("receipt panel_arch must equal amd64 or arm64")
    agent_version = required_text(receipt, "agent_version")
    cli_version = required_text(receipt, "cli_version") if schema_version >= 2 else None
    node_arch = required_text(receipt, "node_arch", 16) if schema_version >= 2 else None
    if node_arch is not None and node_arch not in ("amd64", "arm64"):
        fail("receipt node_arch must equal amd64 or arm64")

    identity_method = required_text(receipt, "panel_identity_method", 32)
    if identity_method not in ("boot_id", "hostname_compatibility"):
        fail("receipt panel_identity_method is unsupported")
    terminal_close = required_text(receipt, "terminal_close_mode", 32)
    if terminal_close not in ("normal", "legacy_agent_eio"):
        fail("receipt terminal_close_mode is unsupported")
    disabled = required_text(receipt, "disabled_capability", 64)
    if disabled not in ("host.action", "agent.update.read", "backup.read"):
        fail("receipt disabled_capability is unsupported")

    marker_bytes = receipt.get("marker_allocation_bytes")
    if not isinstance(marker_bytes, int) or isinstance(marker_bytes, bool):
        fail("receipt marker_allocation_bytes must be an integer")
    if marker_bytes < MIN_MARKER_BYTES or marker_bytes > MAX_MARKER_BYTES:
        fail("receipt marker_allocation_bytes is outside the 16-96 MiB boundary")

    checks = receipt.get("checks")
    if not isinstance(checks, dict) or set(checks) != expected_checks:
        fail(f"receipt checks must contain exactly the schema-v{schema_version} acceptance checks")
    if any(value is not True for value in checks.values()):
        fail("every receipt check must equal true")

    accepted = parse_accepted_at(required_text(receipt, "accepted_at", 64))
    now = datetime.now(timezone.utc)
    if accepted > now + timedelta(minutes=5):
        fail("receipt accepted_at is more than five minutes in the future")
    age = max(timedelta(), now - accepted)
    if age > parse_duration(args.max_age):
        fail(f"receipt is stale: age {int(age.total_seconds())} seconds exceeds {args.max_age}")

    expect(panel_version, args.panel_version, "panel_version")
    if args.panel_commit is not None:
        if panel_commit is None:
            fail(f"schema-v{schema_version} receipt does not bind panel_commit")
        expect(panel_commit, args.panel_commit, "panel_commit")
    expect(panel_arch, args.panel_arch, "panel_arch")
    if args.cli_version is not None:
        if cli_version is None:
            fail(f"schema-v{schema_version} receipt does not bind cli_version")
        expect(cli_version, args.cli_version, "cli_version")
    expect(agent_version, args.agent_version, "agent_version")
    if args.node_arch is not None:
        if node_arch is None:
            fail(f"schema-v{schema_version} receipt does not bind node_arch")
        expect(node_arch, args.node_arch, "node_arch")
    expect(node_id, args.node, "node_id")
    expect(panel_origin, args.panel_origin, "panel_origin")
    if args.require_panel_identity != "any":
        expect(identity_method, args.require_panel_identity, "panel_identity_method")
    if args.require_terminal_close != "any":
        expect(terminal_close, args.require_terminal_close, "terminal_close_mode")

    signing_key_fingerprint = None
    if args.signature is not None and args.public_key is not None:
        signing_key_fingerprint = verify_detached_signature(args.receipt, args.signature, args.public_key)

    print("provider-network receipt verification: OK")
    print(f"receipt={args.receipt}")
    print(f"schema_version={schema_version}")
    print(f"age_seconds={int(age.total_seconds())}")
    print(f"panel={panel_version} ({panel_arch})")
    if panel_commit is not None:
        print(f"panel_commit={panel_commit}")
    if cli_version is not None:
        print(f"cli={cli_version}")
    print(f"agent={agent_version}")
    print(f"node={node_id}" + (f" ({node_arch})" if node_arch is not None else ""))
    print(f"panel_identity_method={identity_method}")
    print(f"terminal_close_mode={terminal_close}")
    print(f"disabled_capability={disabled}")
    print(f"checks={len(expected_checks)}/{len(expected_checks)}")
    if signing_key_fingerprint is None:
        print("signature=not_checked")
    else:
        print("signature=verified")
        print(f"signing_key_sha256={signing_key_fingerprint}")
    return 0


def main() -> int:
    args = parse_args()
    try:
        return validate(read_receipt(args.receipt), args)
    except ReceiptError as error:
        print(f"provider-network receipt verification failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
