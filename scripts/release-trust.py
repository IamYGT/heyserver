#!/usr/bin/env python3
"""Validate the provider-neutral HServer release signer trust store."""

from __future__ import annotations

import argparse
import base64
import binascii
import hashlib
import json
import pathlib
import re
import sys


FINGERPRINT = re.compile(r"^[0-9a-f]{64}$")
ALLOWED_STATUSES = {"active", "next"}


class TrustError(ValueError):
    pass


def load_trust_store(path: pathlib.Path) -> list[dict[str, str]]:
    try:
        raw = path.read_bytes()
    except OSError as exc:
        raise TrustError(f"could not read release trust store: {exc}") from exc
    try:
        document = json.loads(raw, object_pairs_hook=_reject_duplicates)
    except (json.JSONDecodeError, UnicodeDecodeError, TrustError) as exc:
        raise TrustError(f"invalid release trust store JSON: {exc}") from exc
    if not isinstance(document, dict) or set(document) != {"schema_version", "signers"}:
        raise TrustError("release trust store must contain only schema_version and signers")
    if document["schema_version"] != 1:
        raise TrustError("unsupported release trust store schema")
    signers = document["signers"]
    if not isinstance(signers, list) or len(signers) > 8:
        raise TrustError("release trust store must contain at most eight signers")

    normalized: list[dict[str, str]] = []
    seen_ids: set[str] = set()
    seen_keys: set[str] = set()
    for index, signer in enumerate(signers, start=1):
        if not isinstance(signer, dict) or set(signer) != {"key_id", "public_key", "status"}:
            raise TrustError(f"release signer {index} has unknown or missing fields")
        key_id = signer["key_id"]
        public_key = signer["public_key"]
        status = signer["status"]
        if not isinstance(key_id, str) or not FINGERPRINT.fullmatch(key_id):
            raise TrustError(f"release signer {index} key_id must be lowercase SHA-256 hex")
        if not isinstance(public_key, str) or not isinstance(status, str):
            raise TrustError(f"release signer {index} fields must be strings")
        if status not in ALLOWED_STATUSES:
            raise TrustError(f"release signer {index} status must be active or next")
        try:
            decoded = base64.b64decode(public_key, validate=True)
        except (binascii.Error, ValueError) as exc:
            raise TrustError(f"release signer {index} public_key is not valid base64") from exc
        if len(decoded) != 32 or base64.b64encode(decoded).decode("ascii") != public_key:
            raise TrustError(f"release signer {index} public_key must be canonical base64 for 32 bytes")
        actual_id = hashlib.sha256(decoded).hexdigest()
        if actual_id != key_id:
            raise TrustError(f"release signer {index} key_id does not match public_key")
        if key_id in seen_ids or public_key in seen_keys:
            raise TrustError("release trust store contains a duplicate signer")
        seen_ids.add(key_id)
        seen_keys.add(public_key)
        normalized.append({"key_id": key_id, "public_key": public_key, "status": status})
    return normalized


def _reject_duplicates(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise TrustError(f"duplicate JSON field: {key}")
        result[key] = value
    return result


def public_key_fingerprint(path: pathlib.Path) -> str:
    try:
        value = path.read_text(encoding="ascii").strip()
    except (OSError, UnicodeError) as exc:
        raise TrustError(f"could not read release public key: {exc}") from exc
    if not value or "\n" in value or "\r" in value:
        raise TrustError("release public key must contain one base64 line")
    try:
        decoded = base64.b64decode(value, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise TrustError("release public key is not valid base64") from exc
    if len(decoded) != 32 or base64.b64encode(decoded).decode("ascii") != value:
        raise TrustError("release public key must be canonical base64 for 32 bytes")
    return hashlib.sha256(decoded).hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("trust_store", type=pathlib.Path)
    parser.add_argument("--require-active", action="store_true")
    parser.add_argument("--fingerprints", action="store_true")
    parser.add_argument("--assert-active-key", type=pathlib.Path)
    args = parser.parse_args()

    try:
        signers = load_trust_store(args.trust_store)
        active = {signer["key_id"] for signer in signers if signer["status"] == "active"}
        if args.require_active and not active:
            raise TrustError("release trust store has no active signer")
        if args.assert_active_key is not None:
            fingerprint = public_key_fingerprint(args.assert_active_key)
            if fingerprint not in active:
                raise TrustError(f"release public key is not an active trusted signer: {fingerprint}")
            print(fingerprint)
        elif args.fingerprints:
            print(",".join(signer["key_id"] for signer in signers))
    except TrustError as exc:
        print(f"hserver-release-trust: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
