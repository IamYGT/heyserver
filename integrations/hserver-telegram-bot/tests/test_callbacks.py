"""Tests for centralized callback router."""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest
from telegram.error import BadRequest, NetworkError, TimedOut

from hserver_bot.handlers.callbacks import (
    callback_router,
    match_callback_prefix,
    safe_edit,
)


@pytest.mark.asyncio
async def test_safe_edit_success():
    query = MagicMock()
    query.edit_message_text = AsyncMock()
    result = await safe_edit(query, "hello")
    assert result is True
    query.edit_message_text.assert_awaited_once_with(
        text="hello",
        reply_markup=None,
        parse_mode="HTML",
    )


@pytest.mark.asyncio
async def test_safe_edit_message_not_modified():
    query = MagicMock()
    query.edit_message_text = AsyncMock(
        side_effect=BadRequest("Message is not modified"),
    )
    result = await safe_edit(query, "hello")
    assert result is False


@pytest.mark.asyncio
async def test_safe_edit_race_condition_skipped():
    query = MagicMock()
    query.edit_message_text = AsyncMock(
        side_effect=BadRequest("message to edit not found"),
    )
    result = await safe_edit(query, "hello")
    assert result is False


@pytest.mark.asyncio
async def test_safe_edit_transient_network_error():
    query = MagicMock()
    query.edit_message_text = AsyncMock(side_effect=NetworkError("timeout"))
    result = await safe_edit(query, "hello")
    assert result is False


@pytest.mark.asyncio
async def test_safe_edit_reraises_unexpected_bad_request():
    query = MagicMock()
    query.edit_message_text = AsyncMock(side_effect=BadRequest("chat not found"))
    with pytest.raises(BadRequest):
        await safe_edit(query, "hello")


def test_match_callback_prefix_known():
    assert match_callback_prefix("dash:health") == ("dash", "health")
    assert match_callback_prefix("page:backups:2") == ("page", "backups:2")
    assert match_callback_prefix("confirm:cancel") == ("confirm", "cancel")


def test_match_callback_prefix_unknown():
    assert match_callback_prefix("unknown:action") is None
    assert match_callback_prefix("nodash") is None


@pytest.mark.asyncio
async def test_callback_router_unknown_callback():
    update = MagicMock()
    update.callback_query = MagicMock()
    update.callback_query.data = "unknown:action"
    update.callback_query.answer = AsyncMock()
    context = MagicMock()

    await callback_router(update, context)

    update.callback_query.answer.assert_awaited_once_with(
        "Bilinmeyen işlem",
        show_alert=True,
    )


@pytest.mark.asyncio
async def test_callback_router_dispatches_known_prefix(monkeypatch):
    handler = AsyncMock()
    monkeypatch.setitem(
        __import__("hserver_bot.handlers.callbacks", fromlist=["_PREFIX_HANDLERS"])._PREFIX_HANDLERS,
        "dash",
        handler,
    )

    update = MagicMock()
    update.callback_query = MagicMock()
    update.callback_query.data = "dash:health"
    update.callback_query.answer = AsyncMock()
    context = MagicMock()

    await callback_router(update, context)

    update.callback_query.answer.assert_not_awaited()
    handler.assert_awaited_once_with(update, context)
