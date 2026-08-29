"""Cron job commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client


async def cron_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.list_cron_jobs()
        jobs = data.get("jobs", data) if isinstance(data, dict) else data
        lines = ["*Cron Jobs*"]
        for job in (jobs or [])[:15]:
            if isinstance(job, dict):
                name = job.get("name", job.get("command", "?"))
                schedule = job.get("schedule", job.get("cron", "-"))
                enabled = "✅" if job.get("enabled", job.get("active", True)) else "⏸"
                lines.append(f"{enabled} `{name}` — `{schedule}`")
        text = "\n".join(lines) if len(lines) > 1 else "Cron job bulunamadı."
    except Exception as exc:
        text = f"❌ cron hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("cron", cron_cmd))
