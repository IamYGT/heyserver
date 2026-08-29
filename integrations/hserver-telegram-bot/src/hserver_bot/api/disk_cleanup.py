"""Disk cleanup API."""

from __future__ import annotations


class DiskCleanupMixin:
    def cleanup_scan(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/disk/cleanup/scan")  # type: ignore[attr-defined]

    def cleanup_execute(self, plan_id: str) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.post_json(  # type: ignore[attr-defined]
            "/api/disk/cleanup/execute",
            {"targets": [plan_id]},
        )
