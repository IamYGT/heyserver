"""Daily digest commands and per-chat subscription toggles."""

from __future__ import annotations

from pathlib import Path

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import chunk_text, deny_unauthorized, get_client, get_settings
from hserver_bot.services.digest import (
    build_digest_text,
    subscribe_chat,
    unsubscribe_chat,
)


def _settings_data_dir(settings) -> str | Path | None:
    """Return a configured path while keeping lightweight test contexts usable."""
    data_dir = getattr(settings, "hserver_bot_data_dir", None)
    return data_dir if isinstance(data_dir, (str, Path)) else None


async def digest_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    if not update.message or not update.effective_chat:
        return
    client = get_client(context)
    text = await build_digest_text(client)
    for part in chunk_text(text):
        await update.message.reply_text(part, parse_mode="Markdown")


async def digest_on_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    if not update.message or not update.effective_chat:
        return
    chat_id = update.effective_chat.id
    settings = get_settings(context)
    if subscribe_chat(chat_id, _settings_data_dir(settings)):
        await update.message.reply_text(
            f"✅ Günlük digest aboneliği açıldı (chat `{chat_id}`).\n"
            f"Zamanlama: her gün {settings.digest_hour_utc:02d}:00 UTC "
            f"({'aktif' if settings.digest_enabled else 'DIGEST_ENABLED=false'}).",
            parse_mode="Markdown",
        )
    else:
        await update.message.reply_text("ℹ️ Bu chat zaten digest abonesi.")


async def digest_off_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    if not update.message or not update.effective_chat:
        return
    chat_id = update.effective_chat.id
    settings = get_settings(context)
    if unsubscribe_chat(chat_id, _settings_data_dir(settings)):
        await update.message.reply_text(
            f"✅ Günlük digest aboneliği kapatıldı (chat `{chat_id}`).",
            parse_mode="Markdown",
        )
    else:
        if chat_id in settings.admin_chat_ids():
            await update.message.reply_text(
                "ℹ️ Bu chat TELEGRAM_ADMIN_CHAT_IDS içinde; günlük digest yine gönderilir. "
                "Abonelik dosyasından çıkarıldı."
            )
        else:
            await update.message.reply_text("ℹ️ Bu chat digest abonesi değildi.")


def register(application) -> None:
    application.add_handler(CommandHandler("digest", digest_cmd))
    application.add_handler(CommandHandler("digest_on", digest_on_cmd))
    application.add_handler(CommandHandler("digest_off", digest_off_cmd))
