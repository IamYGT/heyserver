"""Nginx commands."""

from __future__ import annotations

import json

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import chunk_text, deny_unauthorized, get_client


async def nginx_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        status = client.nginx_status()
        test = client.nginx_test()
        text = (
            "*Nginx Status*\n"
            f"```json\n{json.dumps(status, indent=2)[:2000]}\n```\n\n"
            "*Config Test*\n"
            f"```json\n{json.dumps(test, indent=2)[:1000]}\n```"
        )
    except Exception as exc:
        text = f"❌ nginx hatası: {exc}"
    if update.message:
        for part in chunk_text(text):
            await update.message.reply_text(part, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("nginx", nginx_cmd))
