"""Health commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import chunk_text, deny_unauthorized, get_client


async def health_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        data = client.health()
        text = f"✅ hserver health\n```json\n{data}\n```"
    except Exception as exc:
        text = f"❌ health hatası: {exc}"
    if update.message:
        for part in chunk_text(text):
            await update.message.reply_text(part, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("health", health_cmd))
