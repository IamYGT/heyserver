"""Admin / audit commands."""

from __future__ import annotations

import subprocess

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import chunk_text, deny_unauthorized, get_settings


async def audit_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    script = get_settings(context).hserver_healthcheck_script.strip()
    if not script:
        if update.message:
            await update.message.reply_text("➖ backup healthcheck is not configured")
        return
    try:
        proc = subprocess.run([script], capture_output=True, text=True, timeout=60, check=False)
        text = proc.stdout + proc.stderr
        if proc.returncode != 0:
            text = f"❌ healthcheck exit {proc.returncode}\n{text}"
        else:
            text = f"✅ backup healthcheck\n{text}"
    except Exception as exc:
        text = f"❌ audit hatası: {exc}"
    if update.message:
        for part in chunk_text(text):
            await update.message.reply_text(part)


def register(application) -> None:
    application.add_handler(CommandHandler("audit", audit_cmd))
