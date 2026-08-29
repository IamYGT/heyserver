#!/usr/bin/env python3
"""Exercise the catalog-to-production-registration acceptance boundary."""

from __future__ import annotations

import importlib.util
import sys
from pathlib import Path
from typing import Any


def load_verifier(path: Path) -> Any:
    spec = importlib.util.spec_from_file_location("extension_catalog_verifier", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load catalog verifier: {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def entries_with(extra: dict[str, Any], registrations: dict[str, str]) -> list[dict[str, Any]]:
    """Keep the canonical registry IDs present while adding one fixture entry."""

    entries = [{"id": entry_id, "classes": ["client_surface"]} for entry_id in registrations]
    entries.append(extra)
    return entries


def expect_rejected(
    verifier: Any,
    root: Path,
    entries: list[dict[str, Any]],
    registrations: dict[str, str],
    label: str,
) -> None:
    try:
        verifier.validate_production_registrations(root, entries, registrations)
    except verifier.CatalogError as exc:
        if "production registration" not in str(exc):
            raise AssertionError(f"{label} failed for an unexpected reason: {exc}") from exc
        return
    raise AssertionError(f"{label} was accepted without a production registration")


def main() -> int:
    script_root = Path(__file__).resolve().parents[1]
    verifier = load_verifier(script_root / "scripts/test-extension-catalog.py")
    try:
        registrations = verifier.parse_production_registration(script_root)

        expect_rejected(
            verifier,
            script_root,
            entries_with(
                {
                    "id": "fixture.local",
                    "classes": ["local_capability"],
                    "evidence": {
                        "docs": [{"path": "docs/extension-boundary.md", "claim": "fixture.local documentation only"}],
                        "tests": [
                            {
                                "path": "scripts/test-extension-catalog-registration.py",
                                "claim": "fixture.local test only",
                            }
                        ],
                    },
                },
                registrations,
            ),
            registrations,
            "catalog-only local capability fixture",
        )
        expect_rejected(
            verifier,
            script_root,
            entries_with(
                {
                    "id": "fixture.provider",
                    "classes": ["provider_adapter"],
                    "evidence": {
                        "docs": [
                            {"path": "docs/extension-boundary.md", "claim": "fixture.provider documentation only"}
                        ],
                        "tests": [
                            {
                                "path": "scripts/test-extension-catalog-registration.py",
                                "claim": "fixture.provider test only",
                            }
                        ],
                    },
                },
                registrations,
            ),
            registrations,
            "catalog-only provider adapter fixture",
        )

        core_id = "cloudflare.dns"
        if core_id not in registrations:
            raise AssertionError(f"canonical core registration is missing: {core_id}")
        canonical_entries = [
            {
                "id": entry_id,
                "classes": ["provider_adapter"] if entry_id == core_id else ["client_surface"],
            }
            for entry_id in registrations
        ]
        verifier.validate_production_registrations(
            script_root,
            canonical_entries,
            registrations,
        )

        verifier.validate_production_registrations(
            script_root,
            entries_with({"id": "fixture.client", "classes": ["client_surface"]}, registrations),
            registrations,
        )
    except (AssertionError, verifier.CatalogError) as exc:
        print(f"extension catalog registration fixture failed: {exc}", file=sys.stderr)
        return 1

    print(
        "extension catalog registration fixtures verified: "
        "catalog-only local/provider rejected, canonical core accepted, metadata-only client surface allowed"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
