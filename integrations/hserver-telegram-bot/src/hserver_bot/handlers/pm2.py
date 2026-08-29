"""PM2 commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client


async def pm2_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.pm2_processes()
        procs = data.get("processes", data) if isinstance(data, dict) else data
        lines = ["*PM2*"]
        for p in (procs or [])[:15]:
            if isinstance(p, dict):
                lines.append(
                    f"• `{p.get('name', '?')}` — {p.get('status', p.get('pm2_env', {}).get('status', '?'))}"
                )
        text = "\n".join(lines) if len(lines) > 1 else "PM2 süreç bulunamadı."
    except Exception as exc:
        text = f"❌ pm2 hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("pm2", pm2_cmd))
