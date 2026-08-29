"""Databases API."""

from __future__ import annotations


class DatabasesMixin:
    def list_databases(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/databases")  # type: ignore[attr-defined]
