"""Tests for operator-only audit helpers."""

from __future__ import annotations

from hserver_bot.handlers.admin import audit_cmd


async def test_audit_cmd_reports_unconfigured_healthcheck(handler_context, telegram_update):
    handler_context.application.bot_data["settings"].hserver_healthcheck_script = ""

    await audit_cmd(telegram_update, handler_context)

    telegram_update.message.reply_text.assert_awaited_once_with(
        "➖ backup healthcheck is not configured"
    )
