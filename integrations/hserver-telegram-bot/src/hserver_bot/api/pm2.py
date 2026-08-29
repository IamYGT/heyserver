"""PM2 API."""

from __future__ import annotations


class Pm2Mixin:
    def pm2_processes(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/pm2/processes")  # type: ignore[attr-defined]
