"""System & health API."""

from __future__ import annotations


class SystemMixin:
    def health(self) -> dict:
        return self.get_json("/api/health")  # type: ignore[attr-defined]

    def system_info(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/system/info")  # type: ignore[attr-defined]

    def system_services(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/system/services")  # type: ignore[attr-defined]

    def system_stats(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/system/stats")  # type: ignore[attr-defined]

    def metrics_summary(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/metrics/summary")  # type: ignore[attr-defined]
