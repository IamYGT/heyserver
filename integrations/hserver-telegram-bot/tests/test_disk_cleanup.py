"""Tests for DiskCleanupMixin and disk cleanup handlers."""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock, patch

import httpx
import pytest
import respx

from hserver_bot.api.auth import AuthMixin
from hserver_bot.api.disk import DiskMixin
from hserver_bot.api.disk_cleanup import DiskCleanupMixin
from hserver_bot.handlers import disk_cleanup as disk_cleanup_handlers


BASE = "http://test"


class _DiskClient(AuthMixin, DiskMixin, DiskCleanupMixin):
    def __init__(self) -> None:
        self.base_url = BASE
        self.email = "admin@test.com"
        self.password = "pass"
        self.timeout = 60.0
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


def _mock_login() -> None:
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "tok-abc"})
    )


# ---------------------------------------------------------------------------
# DiskCleanupMixin
# ---------------------------------------------------------------------------


@respx.mock
def test_cleanup_scan_returns_targets():
    _mock_login()
    payload = [
        {
            "id": "apt-cache",
            "name": "APT Package Cache",
            "description": "Downloaded .deb packages no longer needed",
            "size": 524288000,
        },
        {
            "id": "journal",
            "name": "Systemd Journal Logs",
            "description": "System logs older than 7 days will be removed",
            "size": 104857600,
        },
    ]
    respx.get(f"{BASE}/api/disk/cleanup/scan").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _DiskClient()
    data = client.cleanup_scan()
    assert isinstance(data, list)
    assert len(data) == 2
    assert data[0]["id"] == "apt-cache"


@respx.mock
def test_cleanup_scan_triggers_login():
    _mock_login()
    respx.get(f"{BASE}/api/disk/cleanup/scan").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _DiskClient()
    assert client._token is None
    client.cleanup_scan()
    assert client._token == "tok-abc"


@respx.mock
def test_cleanup_execute_posts_plan_id():
    _mock_login()
    route = respx.post(f"{BASE}/api/disk/cleanup/execute").mock(
        return_value=httpx.Response(
            200,
            json={
                "results": [
                    {"id": "apt-cache", "status": "ok", "message": "cleaned"},
                ]
            },
        )
    )
    client = _DiskClient()
    result = client.cleanup_execute("apt-cache")
    assert route.calls.last.request.content == b'{"targets":["apt-cache"]}'
    assert result["results"][0]["status"] == "ok"


@respx.mock
def test_cleanup_execute_raises_on_http_error():
    _mock_login()
    respx.post(f"{BASE}/api/disk/cleanup/execute").mock(
        return_value=httpx.Response(500, json={"error": "internal"})
    )
    client = _DiskClient()
    with pytest.raises(httpx.HTTPStatusError):
        client.cleanup_execute("journal")


# ---------------------------------------------------------------------------
# DiskMixin largest (used by /disk_largest handler)
# ---------------------------------------------------------------------------


@respx.mock
def test_disk_largest_with_limit():
    _mock_login()
    payload = [
        {"path": "/var/log/syslog", "size": 1073741824, "modified": "2026-06-01"},
        {"path": "/var/lib/mysql/data.ibd", "size": 536870912, "modified": "2026-05-15"},
    ]
    respx.get(f"{BASE}/api/disk/largest?limit=5").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _DiskClient()
    data = client.disk_largest(5)
    assert isinstance(data, list)
    assert data[0]["path"] == "/var/log/syslog"


# ---------------------------------------------------------------------------
# Handlers
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_disk_scan_cmd_replies_with_targets():
    update = MagicMock()
    update.message = AsyncMock()
    context = MagicMock()
    client = MagicMock()
    client.cleanup_scan.return_value = [
        {"id": "tmp", "name": "Temp Files", "description": "Old /tmp files", "size": 1024},
    ]
    with patch.object(disk_cleanup_handlers, "deny_unauthorized", AsyncMock(return_value=False)), patch.object(
        disk_cleanup_handlers, "get_client", return_value=client
    ):
        await disk_cleanup_handlers.disk_scan_cmd(update, context)
    client.ensure_token.assert_called_once()
    client.cleanup_scan.assert_called_once()
    update.message.reply_text.assert_awaited()
    text = update.message.reply_text.await_args.args[0]
    assert "tmp" in text
    assert "Temp Files" in text


@pytest.mark.asyncio
async def test_disk_largest_cmd_uses_limit_arg():
    update = MagicMock()
    update.message = AsyncMock()
    context = MagicMock()
    context.args = ["15"]
    client = MagicMock()
    client.disk_largest.return_value = [
        {"path": "/big/file.bin", "size": 999999999, "modified": "2026-01-01"},
    ]
    with patch.object(disk_cleanup_handlers, "deny_unauthorized", AsyncMock(return_value=False)), patch.object(
        disk_cleanup_handlers, "get_client", return_value=client
    ):
        await disk_cleanup_handlers.disk_largest_cmd(update, context)
    client.disk_largest.assert_called_once_with(15)
    text = update.message.reply_text.await_args.args[0]
    assert "/big/file.bin" in text


@pytest.mark.asyncio
async def test_disk_scan_cmd_denies_unauthorized():
    update = MagicMock()
    update.message = AsyncMock()
    context = MagicMock()
    with patch.object(disk_cleanup_handlers, "deny_unauthorized", AsyncMock(return_value=True)):
        await disk_cleanup_handlers.disk_scan_cmd(update, context)
    update.message.reply_text.assert_not_awaited()
