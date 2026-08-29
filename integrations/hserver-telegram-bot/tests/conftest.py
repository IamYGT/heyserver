"""Shared pytest fixtures."""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import httpx
import pytest
import respx

from hserver_bot.api.auth import AuthMixin
from hserver_bot.api.client import HServerClient

BASE = "http://test"


def make_api_test_client(*mixins: type) -> type:
    """Build a test client class without MRO conflicts with HServerClient."""

    class _TestClient(AuthMixin, *mixins):  # type: ignore[misc]
        def __init__(self, base_url: str, email: str, password: str, timeout: float = 60.0) -> None:
            self.base_url = base_url.rstrip("/")
            self.email = email
            self.password = password
            self.timeout = timeout
            self._token: str | None = None

        def _client(self) -> httpx.Client:
            headers = {}
            if self._token:
                headers["Authorization"] = f"Bearer {self._token}"
            return httpx.Client(base_url=self.base_url, headers=headers, timeout=self.timeout)

        def _request(self, method: str, path: str, **kwargs) -> httpx.Response:
            with self._client() as client:
                response = client.request(method, path, **kwargs)
                response.raise_for_status()
                return response

        def get_json(self, path: str) -> dict | list:
            return self._request("GET", path).json()

        def post_json(self, path: str, body: dict | None = None) -> dict | list:
            return self._request("POST", path, json=body or {}).json()

    return _TestClient


def authed_client() -> HServerClient:
    return HServerClient(BASE, "admin@test.com", "pass")


def mock_login() -> None:
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "tok-abc"})
    )


@pytest.fixture
def mock_client() -> MagicMock:
    client = MagicMock()
    client.ensure_token.return_value = "tok-abc"
    return client


@pytest.fixture
def handler_context(mock_client: MagicMock) -> MagicMock:
    settings = MagicMock()
    settings.allowed_user_ids.return_value = set()
    ctx = MagicMock()
    ctx.application.bot_data = {
        "hserver_client": mock_client,
        "settings": settings,
    }
    return ctx


@pytest.fixture
def telegram_update() -> MagicMock:
    update = MagicMock()
    update.effective_user = MagicMock(id=99)
    update.message = MagicMock()
    update.message.reply_text = AsyncMock()
    return update
