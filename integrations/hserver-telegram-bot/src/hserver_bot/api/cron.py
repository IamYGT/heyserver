"""Cron jobs API."""

from __future__ import annotations


class CronMixin:
    def list_cron_jobs(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/cron")  # type: ignore[attr-defined]
