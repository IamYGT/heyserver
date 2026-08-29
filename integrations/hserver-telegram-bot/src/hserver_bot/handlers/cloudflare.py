"""Cloudflare commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client


async def cf_zones_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.list_zones()
        zones = data.get("zones", data) if isinstance(data, dict) else data
        lines = ["*Cloudflare Zones*"]
        for zone in (zones or [])[:20]:
            if isinstance(zone, dict):
                name = zone.get("name", "?")
                zone_id = zone.get("id", "-")
                status = zone.get("status", "-")
                plan = zone.get("plan", {})
                plan_name = plan.get("name", "-") if isinstance(plan, dict) else "-"
                lines.append(f"• `{name}` — {status} — {plan_name}\n  id: `{zone_id}`")
        text = "\n".join(lines) if len(lines) > 1 else "Cloudflare zone bulunamadı."
    except Exception as exc:
        text = f"❌ cloudflare hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


async def cf_purge_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    if not context.args:
        if update.message:
            await update.message.reply_text("Kullanım: /cf_purge <zone_id>")
        return
    zone_id = context.args[0]
    client = get_client(context)
    try:
        client.ensure_token()
        result = client.purge_zone(zone_id)
        status = result.get("status", result) if isinstance(result, dict) else result
        text = f"✅ Cloudflare cache purge: `{status}` (zone `{zone_id}`)"
    except Exception as exc:
        text = f"❌ cloudflare purge hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("cf_zones", cf_zones_cmd))
