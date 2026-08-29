"""Backup & GDrive commands."""

from __future__ import annotations

import html
import json

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client, is_authorized
from hserver_bot.utils.pagination import (
    PAGE_SIZE,
    build_page_keyboard,
    decode_page_cb,
    paginate_items,
)

BACKUPS_PREFIX = "backups"


def _normalize_backups(data: object) -> list:
    if isinstance(data, dict):
        backups = data.get("backups", data)
    else:
        backups = data
    return list(backups or [])


def _format_backups_message(backups: list, page: int) -> tuple[str, object | None]:
    page_items, page, total_pages = paginate_items(backups, page, PAGE_SIZE)
    lines = [f"<b>Son yedekler</b> ({page}/{total_pages})"]
    for backup in page_items:
        if isinstance(backup, dict):
            backup_id = html.escape(str(backup.get("id", "?")))
            size = html.escape(str(backup.get("sizeHuman", backup.get("size", ""))))
            lines.append(f"• <code>{backup_id}</code> — {size}")
    if len(lines) == 1:
        lines.append("Yedek bulunamadı.")
    keyboard = build_page_keyboard(BACKUPS_PREFIX, page, total_pages)
    return "\n".join(lines), keyboard


async def backups_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        backups = _normalize_backups(client.list_backups())
        text, keyboard = _format_backups_message(backups, page=1)
    except Exception as exc:
        text = f"❌ backups hatası: {html.escape(str(exc))}"
        keyboard = None
    if update.message:
        kwargs: dict = {"parse_mode": "HTML"}
        if keyboard is not None:
            kwargs["reply_markup"] = keyboard
        await update.message.reply_text(text, **kwargs)


async def handle_backups_page_callback(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
) -> None:
    query = update.callback_query
    if query is None or query.data is None:
        return
    if not is_authorized(update, context):
        await query.answer("⛔ Bu bot için yetkiniz yok.", show_alert=True)
        return
    await query.answer()
    try:
        prefix, page = decode_page_cb(query.data)
        if prefix != BACKUPS_PREFIX:
            return
    except ValueError:
        return
    client = get_client(context)
    try:
        client.ensure_token()
        backups = _normalize_backups(client.list_backups())
        text, keyboard = _format_backups_message(backups, page)
        edit_kwargs: dict = {"parse_mode": "HTML"}
        if keyboard is not None:
            edit_kwargs["reply_markup"] = keyboard
        await query.edit_message_text(text, **edit_kwargs)
    except Exception as exc:
        await query.edit_message_text(
            f"❌ backups hatası: {html.escape(str(exc))}",
            parse_mode="HTML",
        )


async def gdrive_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        st = client.gdrive_status()
        settings = st.get("settings") or {}
        text = (
            f"<b>GDrive</b>\n"
            f"connected: <code>{html.escape(str(st.get('connected')))}</code>\n"
            f"email: <code>{html.escape(str(st.get('email', '-')))}</code>\n"
            f"autoUpload: <code>{html.escape(str(settings.get('autoUpload')))}</code>\n"
            f"lastUpload: <code>{html.escape(str(settings.get('lastUploadAt', '-')))}</code>\n"
            f"lastError: <code>{html.escape(str(settings.get('lastError', '-')))}</code>"
        )
    except Exception as exc:
        text = f"❌ gdrive hatası: {html.escape(str(exc))}"
    if update.message:
        await update.message.reply_text(text, parse_mode="HTML")


async def gdrive_test_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        result = client.gdrive_test()
        payload = html.escape(json.dumps(result, indent=2))
        text = f"✅ GDrive test\n<pre>{payload}</pre>"
    except Exception as exc:
        text = f"❌ gdrive test: {html.escape(str(exc))}"
    if update.message:
        await update.message.reply_text(text, parse_mode="HTML")


def register(application) -> None:
    application.add_handler(CommandHandler("backups", backups_cmd))
    application.add_handler(CommandHandler("gdrive", gdrive_cmd))
