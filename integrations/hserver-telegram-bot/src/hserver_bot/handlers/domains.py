"""Domain commands."""

from __future__ import annotations

import json

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import chunk_text, deny_unauthorized, get_client


async def domains_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.list_domains()
        domains = data.get("domains", data) if isinstance(data, dict) else data
        lines = ["*Domainler*"]
        for domain in (domains or [])[:20]:
            if isinstance(domain, dict):
                name = domain.get("name", domain.get("id", "?"))
                dtype = domain.get("type", "-")
                ssl = "SSL" if domain.get("sslEnabled") else "no-SSL"
                active = "aktif" if domain.get("isActive", True) else "pasif"
                lines.append(f"• `{name}` — {dtype} — {ssl} — {active}")
        text = "\n".join(lines) if len(lines) > 1 else "Domain bulunamadı."
    except Exception as exc:
        text = f"❌ domains hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


async def domain_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    if not context.args:
        if update.message:
            await update.message.reply_text("Kullanım: /domain <isim>")
        return
    name = context.args[0]
    client = get_client(context)
    try:
        client.ensure_token()
        detail = client.get_domain(name)
        text = f"*Domain: {name}*\n```json\n{json.dumps(detail, indent=2)[:3500]}\n```"
    except Exception as exc:
        text = f"❌ domain hatası: {exc}"
    if update.message:
        for part in chunk_text(text):
            await update.message.reply_text(part, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("domains", domains_cmd))
    application.add_handler(CommandHandler("domain", domain_cmd))
