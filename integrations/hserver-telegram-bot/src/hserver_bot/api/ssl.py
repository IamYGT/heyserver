"""SSL certificates API."""

from __future__ import annotations


class SSLMixin:
    def ssl_certificates(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/ssl/certificates")  # type: ignore[attr-defined]
