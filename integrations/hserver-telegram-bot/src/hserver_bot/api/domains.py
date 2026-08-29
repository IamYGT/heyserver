"""Domains API."""

from __future__ import annotations


class DomainsMixin:
    def list_domains(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/domains")  # type: ignore[attr-defined]

    def get_domain(self, name: str) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json(f"/api/domains/{name}")  # type: ignore[attr-defined]
