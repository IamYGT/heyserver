"""Docker commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client


async def docker_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        status = client.docker_status()
        containers = client.docker_containers()
        items = containers.get("containers", containers) if isinstance(containers, dict) else containers
        lines = ["*Docker*"]
        running = status.get("running", status.get("status", "?")) if isinstance(status, dict) else str(status)
        lines.append(f"Durum: `{running}`")
        lines.append("")
        for c in (items or [])[:20]:
            if isinstance(c, dict):
                name = c.get("name") or c.get("Names") or c.get("id", "?")
                state = c.get("state") or c.get("State") or c.get("status", "?")
                lines.append(f"• `{name}` — {state}")
        text = "\n".join(lines) if len(lines) > 3 else "Docker container bulunamadı."
    except Exception as exc:
        text = f"❌ docker hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("docker", docker_cmd))
