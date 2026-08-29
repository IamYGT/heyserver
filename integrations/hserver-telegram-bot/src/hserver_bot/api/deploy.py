"""Deploy targets & history API."""

from __future__ import annotations


class DeployMixin:
    def list_targets(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/deploy/targets")  # type: ignore[attr-defined]

    def deploy_history(self, limit: int = 10) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json(f"/api/deploy/history?limit={limit}")  # type: ignore[attr-defined]

    def trigger_deploy(self, target_id: str | int) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.post_json(f"/api/deploy/manual/{target_id}")  # type: ignore[attr-defined]
