"""Restic snapshot API."""

from __future__ import annotations


class SnapshotMixin:
    def snapshot_status(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/backups/snapshot/status")  # type: ignore[attr-defined]

    def snapshot_list(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/backups/snapshot/list")  # type: ignore[attr-defined]

    def run_snapshot(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.post_json("/api/backups/snapshot/run")  # type: ignore[attr-defined]
