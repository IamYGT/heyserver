"""Tests for backup & GDrive Telegram handlers."""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import httpx
import pytest

from hserver_bot.handlers.backups import backups_cmd, gdrive_cmd


async def test_backups_cmd_lists_backups(mock_client, handler_context, telegram_update):
    mock_client.list_backups.return_value = {
        "backups": [
            {"id": "bk-001", "sizeHuman": "120 MB"},
            {"id": "bk-002", "size": "95 MB"},
        ]
    }

    await backups_cmd(telegram_update, handler_context)

    mock_client.ensure_token.assert_called_once()
    mock_client.list_backups.assert_called_once()
    telegram_update.message.reply_text.assert_awaited_once()
    text = telegram_update.message.reply_text.call_args[0][0]
    assert "Son yedekler" in text
    assert "bk-001" in text
    assert "120 MB" in text
    assert "bk-002" in text


async def test_backups_cmd_accepts_list_response(mock_client, handler_context, telegram_update):
    mock_client.list_backups.return_value = [{"id": "bk-flat", "sizeHuman": "1 GB"}]

    await backups_cmd(telegram_update, handler_context)

    text = telegram_update.message.reply_text.call_args[0][0]
    assert "bk-flat" in text
    assert "1 GB" in text


async def test_backups_cmd_limits_to_five_per_page(mock_client, handler_context, telegram_update):
    mock_client.list_backups.return_value = {
        "backups": [{"id": f"bk-{i}", "sizeHuman": f"{i} MB"} for i in range(12)]
    }

    await backups_cmd(telegram_update, handler_context)

    text = telegram_update.message.reply_text.call_args[0][0]
    assert "bk-4" in text
    assert "bk-5" not in text
    assert "(1/" in text


async def test_backups_cmd_handles_api_error(mock_client, handler_context, telegram_update):
    mock_client.list_backups.side_effect = httpx.HTTPStatusError(
        "fail",
        request=MagicMock(),
        response=MagicMock(status_code=500),
    )

    await backups_cmd(telegram_update, handler_context)

    text = telegram_update.message.reply_text.call_args[0][0]
    assert text.startswith("❌ backups hatası:")


async def test_backups_cmd_denies_unauthorized(mock_client, telegram_update):
    settings = MagicMock()
    settings.allowed_user_ids.return_value = {1, 2}
    ctx = MagicMock()
    ctx.application.bot_data = {
        "hserver_client": mock_client,
        "settings": settings,
    }

    await backups_cmd(telegram_update, ctx)

    mock_client.ensure_token.assert_not_called()
    mock_client.list_backups.assert_not_called()
    telegram_update.message.reply_text.assert_awaited_once_with("⛔ Bu bot için yetkiniz yok.")


async def test_gdrive_cmd_shows_status(mock_client, handler_context, telegram_update):
    mock_client.gdrive_status.return_value = {
        "connected": True,
        "email": "admin@example.com",
        "settings": {
            "autoUpload": True,
            "lastUploadAt": "2026-06-01T03:00:00Z",
            "lastError": None,
        },
    }

    await gdrive_cmd(telegram_update, handler_context)

    mock_client.ensure_token.assert_called_once()
    mock_client.gdrive_status.assert_called_once()
    telegram_update.message.reply_text.assert_awaited_once()
    text = telegram_update.message.reply_text.call_args[0][0]
    assert "GDrive" in text
    assert "True" in text
    assert "admin@example.com" in text
    assert "2026-06-01T03:00:00Z" in text


async def test_gdrive_cmd_handles_missing_settings(mock_client, handler_context, telegram_update):
    mock_client.gdrive_status.return_value = {"connected": False, "email": None}

    await gdrive_cmd(telegram_update, handler_context)

    text = telegram_update.message.reply_text.call_args[0][0]
    assert "connected:" in text
    assert "False" in text
    assert "autoUpload:" in text
    assert "lastUpload:" in text
    assert "lastError:" in text


async def test_gdrive_cmd_handles_api_error(mock_client, handler_context, telegram_update):
    mock_client.gdrive_status.side_effect = RuntimeError("token expired")

    await gdrive_cmd(telegram_update, handler_context)

    text = telegram_update.message.reply_text.call_args[0][0]
    assert text.startswith("❌ gdrive hatası:")
    assert "token expired" in text


async def test_gdrive_cmd_denies_unauthorized(mock_client, telegram_update):
    settings = MagicMock()
    settings.allowed_user_ids.return_value = {42}
    ctx = MagicMock()
    ctx.application.bot_data = {
        "hserver_client": mock_client,
        "settings": settings,
    }

    await gdrive_cmd(telegram_update, ctx)

    mock_client.ensure_token.assert_not_called()
    mock_client.gdrive_status.assert_not_called()
    telegram_update.message.reply_text.assert_awaited_once_with("⛔ Bu bot için yetkiniz yok.")
