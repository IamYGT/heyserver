"""Nginx API."""

from __future__ import annotations


class NginxMixin:
    def nginx_status(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/nginx/status")  # type: ignore[attr-defined]

    def nginx_test(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.post_json("/api/nginx/test")  # type: ignore[attr-defined]
