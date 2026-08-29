"""System & metrics commands."""

from __future__ import annotations

import json

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import chunk_text, deny_unauthorized, get_client


async def system_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        info = client.system_info()
        stats = client.system_stats()
        text = (
            "*System Info*\n"
            f"```json\n{json.dumps(info, indent=2)[:1800]}\n```\n\n"
            "*Stats*\n"
            f"```json\n{json.dumps(stats, indent=2)[:1800]}\n```"
        )
    except Exception as exc:
        text = f"❌ system hatası: {exc}"
    if update.message:
        for part in chunk_text(text):
            await update.message.reply_text(part, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("system", system_cmd))
