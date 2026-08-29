"""Backup & GDrive API."""

from __future__ import annotations


class BackupsMixin:
    def list_backups(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/backups")  # type: ignore[attr-defined]

    def gdrive_status(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/backups/gdrive/status")  # type: ignore[attr-defined]

    def gdrive_test(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.post_json("/api/backups/gdrive/test")  # type: ignore[attr-defined]

    def snapshot_status(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/backups/snapshot/status")  # type: ignore[attr-defined]

    def run_database_backup(self) -> None:
        import os

        secret = os.environ.get("HSERVER_CRON_SECRET", "")
        if not secret:
            raise RuntimeError("HSERVER_CRON_SECRET not set")
        with self._client() as client:  # type: ignore[attr-defined]
            r = client.post(
                "/api/internal/cron/backup",
                headers={"X-Cron-Secret": secret},
                json={"type": "database", "retention": 7},
            )
            r.raise_for_status()

    def upload_backup(self, backup_id: str) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.post_json(f"/api/backups/upload/{backup_id}")  # type: ignore[attr-defined]
