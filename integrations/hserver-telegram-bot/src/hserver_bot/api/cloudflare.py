"""Cloudflare API."""

from __future__ import annotations


class CloudflareMixin:
    def list_zones(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/cloudflare/zones")  # type: ignore[attr-defined]

    def get_zone(self, zone_id: str) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json(f"/api/cloudflare/zones/{zone_id}")  # type: ignore[attr-defined]

    def list_records(self, zone_id: str) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json(f"/api/cloudflare/zones/{zone_id}/records")  # type: ignore[attr-defined]

    def purge_zone(self, zone_id: str) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.post_json(f"/api/cloudflare/zones/{zone_id}/purge")  # type: ignore[attr-defined]
