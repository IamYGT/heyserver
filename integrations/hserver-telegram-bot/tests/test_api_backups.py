"""Tests for BackupsMixin."""

from __future__ import annotations

import httpx
import pytest
import respx

from hserver_bot.api.client import HServerClient

BASE = "http://test"


def authed_client() -> HServerClient:
    return HServerClient(BASE, "admin@test.com", "pass")


def mock_login() -> None:
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "tok-abc"})
    )


# ---------------------------------------------------------------------------
# list_backups
# ---------------------------------------------------------------------------


@respx.mock
def test_list_backups_returns_dict_with_backups():
    mock_login()
    payload = {
        "backups": [
            {"id": "bk-001", "sizeHuman": "120 MB"},
            {"id": "bk-002", "sizeHuman": "95 MB"},
        ]
    }
    respx.get(f"{BASE}/api/backups").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = authed_client()
    data = client.list_backups()
    assert isinstance(data, dict)
    backups = data["backups"]
    assert len(backups) == 2
    assert backups[0]["id"] == "bk-001"


@respx.mock
def test_list_backups_returns_list_directly():
    mock_login()
    payload = [{"id": "bk-003", "size": "1.2 GB"}]
    respx.get(f"{BASE}/api/backups").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = authed_client()
    data = client.list_backups()
    assert isinstance(data, list)
    assert data[0]["id"] == "bk-003"


@respx.mock
def test_list_backups_triggers_login():
    mock_login()
    respx.get(f"{BASE}/api/backups").mock(
        return_value=httpx.Response(200, json={"backups": []})
    )
    client = authed_client()
    assert client._token is None
    client.list_backups()
    assert client._token == "tok-abc"


@respx.mock
def test_list_backups_raises_on_http_error():
    mock_login()
    respx.get(f"{BASE}/api/backups").mock(
        return_value=httpx.Response(500, json={"error": "internal"})
    )
    client = authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.list_backups()


# ---------------------------------------------------------------------------
# gdrive_status
# ---------------------------------------------------------------------------


@respx.mock
def test_gdrive_status_returns_connection_details():
    mock_login()
    payload = {
        "connected": True,
        "email": "admin@example.com",
        "settings": {
            "autoUpload": True,
            "lastUploadAt": "2026-06-01T03:00:00Z",
            "lastError": None,
        },
    }
    respx.get(f"{BASE}/api/backups/gdrive/status").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = authed_client()
    data = client.gdrive_status()
    assert data["connected"] is True
    assert data["email"] == "admin@example.com"
    assert data["settings"]["autoUpload"] is True


@respx.mock
def test_gdrive_status_raises_on_http_error():
    mock_login()
    respx.get(f"{BASE}/api/backups/gdrive/status").mock(
        return_value=httpx.Response(403, json={"error": "forbidden"})
    )
    client = authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.gdrive_status()


# ---------------------------------------------------------------------------
# gdrive_test
# ---------------------------------------------------------------------------


@respx.mock
def test_gdrive_test_returns_result():
    mock_login()
    payload = {"ok": True, "message": "upload probe succeeded"}
    route = respx.post(f"{BASE}/api/backups/gdrive/test").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = authed_client()
    data = client.gdrive_test()
    assert data["ok"] is True
    assert route.called


@respx.mock
def test_gdrive_test_raises_on_http_error():
    mock_login()
    respx.post(f"{BASE}/api/backups/gdrive/test").mock(
        return_value=httpx.Response(502, json={"error": "bad gateway"})
    )
    client = authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.gdrive_test()


# ---------------------------------------------------------------------------
# snapshot_status
# ---------------------------------------------------------------------------


@respx.mock
def test_snapshot_status_returns_state():
    mock_login()
    payload = {"enabled": True, "lastRun": "2026-06-17T02:00:00Z", "status": "idle"}
    respx.get(f"{BASE}/api/backups/snapshot/status").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = authed_client()
    data = client.snapshot_status()
    assert data["enabled"] is True
    assert data["status"] == "idle"


@respx.mock
def test_snapshot_status_raises_on_http_error():
    mock_login()
    respx.get(f"{BASE}/api/backups/snapshot/status").mock(
        return_value=httpx.Response(404, json={"error": "not found"})
    )
    client = authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.snapshot_status()


# ---------------------------------------------------------------------------
# upload_backup
# ---------------------------------------------------------------------------


@respx.mock
def test_upload_backup_posts_backup_id():
    mock_login()
    backup_id = "bk-upload-99"
    payload = {"ok": True, "backupId": backup_id}
    route = respx.post(f"{BASE}/api/backups/upload/{backup_id}").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = authed_client()
    data = client.upload_backup(backup_id)
    assert data["ok"] is True
    assert route.called


@respx.mock
def test_upload_backup_raises_on_http_error():
    mock_login()
    respx.post(f"{BASE}/api/backups/upload/missing").mock(
        return_value=httpx.Response(404, json={"error": "not found"})
    )
    client = authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.upload_backup("missing")


# ---------------------------------------------------------------------------
# run_database_backup
# ---------------------------------------------------------------------------


@respx.mock
def test_run_database_backup_with_cron_secret(monkeypatch):
    monkeypatch.setenv("HSERVER_CRON_SECRET", "cron-secret-xyz")
    route = respx.post(f"{BASE}/api/internal/cron/backup").mock(
        return_value=httpx.Response(200, json={"ok": True})
    )
    client = HServerClient(BASE, "admin@test.com", "pass")
    client.run_database_backup()
    assert route.called
    request = route.calls.last.request
    assert request.headers["X-Cron-Secret"] == "cron-secret-xyz"
    assert request.read() == b'{"type":"database","retention":7}'


def test_run_database_backup_requires_cron_secret(monkeypatch):
    monkeypatch.delenv("HSERVER_CRON_SECRET", raising=False)
    client = authed_client()
    with pytest.raises(RuntimeError, match="HSERVER_CRON_SECRET not set"):
        client.run_database_backup()


@respx.mock
def test_run_database_backup_propagates_http_error(monkeypatch):
    monkeypatch.setenv("HSERVER_CRON_SECRET", "cron-secret-xyz")
    respx.post(f"{BASE}/api/internal/cron/backup").mock(
        return_value=httpx.Response(500, json={"error": "internal"})
    )
    client = authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.run_database_backup()
