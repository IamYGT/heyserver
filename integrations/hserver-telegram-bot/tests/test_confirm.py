"""Tests for destructive-operation confirmation flows."""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

from hserver_bot.handlers.confirm import (
    PENDING_KEY,
    deploy_run_cmd,
    handle_confirm_callback,
)


@pytest.fixture
def confirm_context(mock_client, handler_context):
    handler_context.user_data = {}
    return handler_context


@pytest.fixture
def callback_update():
    update = MagicMock()
    update.effective_user = MagicMock(id=99)
    update.callback_query = MagicMock()
    update.callback_query.message = MagicMock()
    update.callback_query.edit_message_text = AsyncMock()
    update.callback_query.answer = AsyncMock()
    return update


@pytest.mark.asyncio
async def test_deploy_run_sets_pending_state(telegram_update, confirm_context):
    telegram_update.message.reply_text = AsyncMock()
    confirm_context.args = ["target-42"]

    await deploy_run_cmd(telegram_update, confirm_context)

    pending = confirm_context.user_data[PENDING_KEY][99]
    assert pending["action"] == "deploy_run"
    assert pending["target_id"] == "target-42"
    telegram_update.message.reply_text.assert_awaited_once()
    markup = telegram_update.message.reply_text.call_args.kwargs["reply_markup"]
    callbacks = [btn.callback_data for row in markup.inline_keyboard for btn in row]
    assert "confirm:deploy_run:target-42" in callbacks
    assert "confirm:cancel" in callbacks


@pytest.mark.asyncio
async def test_confirm_cancel_clears_pending(callback_update, confirm_context):
    confirm_context.user_data[PENDING_KEY] = {
        99: {"action": "deploy_run", "target_id": "target-42"},
    }
    callback_update.callback_query.data = "confirm:cancel"

    handled = await handle_confirm_callback(callback_update, confirm_context)

    assert handled is True
    assert 99 not in confirm_context.user_data[PENDING_KEY]
    callback_update.callback_query.edit_message_text.assert_awaited_once()
    assert callback_update.callback_query.edit_message_text.call_args[0][0] == "İptal edildi"


@pytest.mark.asyncio
async def test_confirm_without_pending_rejects(callback_update, confirm_context):
    callback_update.callback_query.data = "confirm:deploy_run:target-42"

    handled = await handle_confirm_callback(callback_update, confirm_context)

    assert handled is True
    text = callback_update.callback_query.edit_message_text.call_args[0][0]
    assert "geçersiz" in text.lower() or "süresi doldu" in text.lower()


@pytest.mark.asyncio
async def test_confirm_deploy_run_executes_and_clears_pending(
    callback_update,
    confirm_context,
    mock_client,
):
    confirm_context.user_data[PENDING_KEY] = {
        99: {"action": "deploy_run", "target_id": "target-42"},
    }
    callback_update.callback_query.data = "confirm:deploy_run:target-42"
    mock_client.trigger_deploy.return_value = {"runId": "run-1", "message": "queued"}

    handled = await handle_confirm_callback(callback_update, confirm_context)

    assert handled is True
    mock_client.ensure_token.assert_called_once()
    mock_client.trigger_deploy.assert_called_once_with("target-42")
    assert 99 not in confirm_context.user_data[PENDING_KEY]
    text = callback_update.callback_query.edit_message_text.call_args[0][0]
    assert "Deploy tetiklendi" in text
    assert "target-42" in text
