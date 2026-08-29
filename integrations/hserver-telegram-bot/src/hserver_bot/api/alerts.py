"""Uptime monitors, incidents and alert rules API."""

from __future__ import annotations


class AlertsMixin:
    def list_monitors(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/uptime/monitors")  # type: ignore[attr-defined]

    def list_incidents(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/uptime/incidents")  # type: ignore[attr-defined]

    def list_alert_rules(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/notify/rules")  # type: ignore[attr-defined]

    def get_alert_rule(self, rule_id: int) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        result = self.get_json(f"/api/notify/rules/{rule_id}")  # type: ignore[attr-defined]
        if not isinstance(result, dict):
            raise ValueError("invalid alert rule response")
        return result

    def update_alert_rule(self, rule_id: int, body: dict) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        response = self._request(  # type: ignore[attr-defined]
            "PUT",
            f"/api/notify/rules/{rule_id}",
            json=body,
        )
        return response.json()

    def toggle_alert_rule(self, rule_id: int) -> dict:
        rule = self.get_alert_rule(rule_id)
        rule["enabled"] = not rule.get("enabled", True)
        return self.update_alert_rule(rule_id, rule)
