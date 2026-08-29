"""Tests for interactive dashboard handler."""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

from hserver_bot.handlers.dashboard import handle_dashboard_callback, menu_cmd
from hserver_bot.utils.keyboards import main_menu_keyboard, back_home_row


@pytest.mark.asyncio
async def test_menu_cmd_denies_unauthorized(mock_client, telegram_update):
    settings = MagicMock()
    settings.allowed_user_ids.return_value = {1, 2}
    ctx = MagicMock()
    ctx.application.bot_data = {
        "hserver_client": mock_client,
        "settings": settings,
    }
    telegram_update.message.reply_text = AsyncMock()

    await menu_cmd(telegram_update, ctx)

    telegram_update.message.reply_text.assert_awaited_once_with(
        "⛔ Bu bot için yetkiniz yok.",
    )


@pytest.mark.asyncio
async def test_menu_cmd_shows_dashboard(mock_client, handler_context, telegram_update):
    telegram_update.message.reply_text = AsyncMock()
    telegram_update.effective_message = telegram_update.message

    await menu_cmd(telegram_update, handler_context)

    telegram_update.message.reply_text.assert_awaited_once()
    text = telegram_update.message.reply_text.call_args[0][0]
    kwargs = telegram_update.message.reply_text.call_args.kwargs
    assert "HserverTrack Dashboard" in text
    assert kwargs.get("parse_mode") == "HTML"
    assert kwargs.get("reply_markup") is not None
