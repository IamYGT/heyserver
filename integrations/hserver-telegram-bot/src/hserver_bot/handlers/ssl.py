"""SSL certificate commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client


async def ssl_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.ssl_certificates()
        certs = data.get("certificates", data) if isinstance(data, dict) else data
        lines = ["*SSL Sertifikaları*"]
        for cert in (certs or [])[:10]:
            if isinstance(cert, dict):
                domain = cert.get("domain", cert.get("name", "?"))
                expires = cert.get("expiresAt", cert.get("validTo", "-"))
                status = cert.get("status", "-")
                lines.append(f"• `{domain}` — {status} — {expires}")
        text = "\n".join(lines) if len(lines) > 1 else "SSL sertifikası bulunamadı."
    except Exception as exc:
        text = f"❌ ssl hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("ssl", ssl_cmd))
