"""Database commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client


async def databases_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.list_databases()
        dbs = data.get("databases", data) if isinstance(data, dict) else data
        lines = ["*Veritabanları*"]
        for db in (dbs or [])[:15]:
            if isinstance(db, dict):
                name = db.get("name", "?")
                db_type = db.get("type", db.get("driver", "-"))
                size = db.get("sizeHuman", db.get("size", "-"))
                lines.append(f"• `{name}` — {db_type} — {size}")
        text = "\n".join(lines) if len(lines) > 1 else "Veritabanı bulunamadı."
    except Exception as exc:
        text = f"❌ db hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("db", databases_cmd))
