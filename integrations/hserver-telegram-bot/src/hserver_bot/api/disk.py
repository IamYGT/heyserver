"""Disk API."""

from __future__ import annotations


class DiskMixin:
    def disk_overview(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/disk/overview")  # type: ignore[attr-defined]

    def disk_usage(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/disk/usage")  # type: ignore[attr-defined]

    def disk_largest(self, limit: int = 10) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json(f"/api/disk/largest?limit={limit}")  # type: ignore[attr-defined]
