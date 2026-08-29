import pytest
import respx
import httpx
from pathlib import Path

from hserver_bot.config import Settings
from hserver_bot.api.client import HServerClient


def test_settings_admin_chat_ids_parsing(monkeypatch):
    monkeypatch.setenv("TELEGRAM_BOT_TOKEN", "test-token")
    monkeypatch.setenv("HSERVER_ADMIN_EMAIL", "a@b.com")
    monkeypatch.setenv("HSERVER_ADMIN_PASS", "secret")
    monkeypatch.setenv("TELEGRAM_ADMIN_CHAT_IDS", "123, 456")
    s = Settings()
    assert s.admin_chat_ids() == [123, 456]


def test_settings_resolves_relative_data_dir_from_bot_home(monkeypatch, tmp_path):
    monkeypatch.setenv("TELEGRAM_BOT_TOKEN", "test-token")
    monkeypatch.setenv("HSERVER_ADMIN_EMAIL", "a@b.com")
    monkeypatch.setenv("HSERVER_ADMIN_PASS", "secret")
    bot_home = tmp_path / "bot-install"
    monkeypatch.setenv("HSERVER_BOT_HOME", str(bot_home))
    monkeypatch.setenv("HSERVER_BOT_DATA_DIR", "state")

    settings = Settings()

    assert settings.hserver_bot_home == bot_home
    assert settings.hserver_bot_data_dir == bot_home / "state"
    assert isinstance(settings.hserver_bot_data_dir, Path)


@respx.mock
def test_client_login_and_health():
    respx.post("http://test/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt-test"})
    )
    respx.get("http://test/api/health").mock(
        return_value=httpx.Response(200, json={"status": "ok"})
    )
    client = HServerClient("http://test", "admin@test.com", "pass")
    assert client.health() == {"status": "ok"}
    client.login()
    assert client._token == "jwt-test"


@respx.mock
def test_gdrive_status_requires_login():
    respx.post("http://test/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )
    respx.get("http://test/api/backups/gdrive/status").mock(
        return_value=httpx.Response(200, json={"connected": True})
    )
    client = HServerClient("http://test", "a@b.com", "p")
    data = client.gdrive_status()
    assert data["connected"] is True
