"""Authentication against hserver-panel."""

from __future__ import annotations


class AuthMixin:
    base_url: str
    email: str
    password: str
    _token: str | None

    def login(self) -> str:
        data = self.post_json("/api/auth/login", {"email": self.email, "password": self.password})
        token = data.get("token") if isinstance(data, dict) else None
        if not token:
            raise RuntimeError("hserver login failed: no token")
        self._token = str(token)
        return self._token

    def ensure_token(self) -> str:
        if not self._token:
            return self.login()
        return self._token
