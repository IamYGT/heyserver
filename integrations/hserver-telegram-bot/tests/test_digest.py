"""Tests for daily digest service."""

from __future__ import annotations

from unittest.mock import MagicMock

import pytest

from hserver_bot.services.digest import (
    build_digest_text,
    ensure_data_dir,
    load_subscriber_ids,
    subscribe_chat,
    subscribers_file,
    unsubscribe_chat,
)


@pytest.mark.asyncio
async def test_build_digest_text_combines_sections(mock_client):
    mock_client.health.return_value = {"status": "ok", "version": "1.2.3"}
    mock_client.disk_overview.return_value = {
        "totalSize": 1_000_000_000,
        "totalUsed": 500_000_000,
        "totalFree": 500_000_000,
    }
    mock_client.list_backups.return_value = {
        "backups": [{"id": "bk-001", "sizeHuman": "120 MB", "createdAt": "2026-06-01"}],
    }
    mock_client.gdrive_status.return_value = {
        "connected": True,
        "settings": {"lastUploadAt": "2026-06-01T03:00:00Z", "lastError": None},
    }
    mock_client.list_incidents.return_value = {
        "incidents": [
            {"id": 1},
            {"id": 2, "resolved_at": "2026-06-01"},
        ],
    }

    text = await build_digest_text(mock_client)

    mock_client.ensure_token.assert_called_once()
    mock_client.health.assert_called_once()
    mock_client.disk_overview.assert_called_once()
    mock_client.list_backups.assert_called_once()
    mock_client.gdrive_status.assert_called_once()
    mock_client.list_incidents.assert_called_once()
    assert "Daily Digest" in text
    assert "ok" in text
    assert "1.2.3" in text
    assert "Disk" in text
    assert "bk-001" in text
    assert "GDrive" in text
    assert "Open incidents" in text
    assert "1" in text


@pytest.mark.asyncio
async def test_build_digest_text_handles_fetch_error(mock_client):
    mock_client.ensure_token.side_effect = RuntimeError("auth failed")

    text = await build_digest_text(mock_client)

    assert text.startswith("❌ digest oluşturulamadı:")
    assert "auth failed" in text


def test_subscriber_storage_uses_configured_data_dir(monkeypatch, tmp_path):
    bot_home = tmp_path / "bot-install"
    monkeypatch.setenv("HSERVER_BOT_HOME", str(bot_home))
    monkeypatch.setenv("HSERVER_BOT_DATA_DIR", "state")

    assert subscribers_file() == bot_home / "state" / "digest_subscribers.json"
    assert ensure_data_dir() == bot_home / "state"
    assert subscribe_chat(123)
    assert load_subscriber_ids() == [123]
    assert unsubscribe_chat(123)
    assert load_subscriber_ids() == []


def test_ensure_data_dir_rejects_a_file(tmp_path):
    path = tmp_path / "not-a-directory"
    path.write_text("not a directory", encoding="utf-8")

    with pytest.raises(NotADirectoryError, match="HSERVER_BOT_DATA_DIR"):
        ensure_data_dir(path)
