"""Disk commands."""

from __future__ import annotations

import html
import json

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import chunk_text, deny_unauthorized, get_client, is_authorized
from hserver_bot.utils.pagination import (
    PAGE_SIZE,
    build_page_keyboard,
    decode_page_cb,
    paginate_items,
)

DISK_PREFIX = "disk"
DISK_LARGEST_PREFIX = "disk_largest"
_LARGEST_FETCH_LIMIT = 50


def _format_size(size: int | float | str | None) -> str:
    if size is None:
        return "?"
    try:
        n = int(size)
    except (TypeError, ValueError):
        return str(size)
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if n < 1024:
            return f"{n:.1f} {unit}" if unit != "B" else f"{n} B"
        n /= 1024
    return f"{n:.1f} PB"


def _normalize_largest_files(data: object) -> list:
    if isinstance(data, list):
        return data
    if isinstance(data, dict):
        files = data.get("files", data)
        return list(files or [])
    return []


def _format_largest_lines(
    files: list,
    page: int,
    title: str,
    *,
    prefix: str,
) -> tuple[str, object | None]:
    page_items, page, total_pages = paginate_items(files, page, PAGE_SIZE)
    lines = [f"<b>{title}</b> ({page}/{total_pages})"]
    for item in page_items:
        if isinstance(item, dict):
            path = html.escape(str(item.get("path") or item.get("Path", "?")))
            size = html.escape(_format_size(item.get("size") or item.get("Size")))
            modified = item.get("modified") or item.get("Modified", "")
            suffix = f" — {html.escape(str(modified))}" if modified else ""
            lines.append(f"• <code>{path}</code> — {size}{suffix}")
    if len(lines) == 1:
        lines.append("Büyük dosya bulunamadı.")
    keyboard = build_page_keyboard(prefix, page, total_pages)
    return "\n".join(lines), keyboard


def _format_disk_message(overview: object, files: list, page: int) -> tuple[str, object | None]:
    overview_json = html.escape(json.dumps(overview, indent=2)[:2000])
    largest_text, keyboard = _format_largest_lines(
        files,
        page,
        "Largest Files",
        prefix=DISK_PREFIX,
    )
    text = f"<b>Disk Overview</b>\n<pre>{overview_json}</pre>\n\n{largest_text}"
    return text, keyboard


async def disk_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        overview = client.disk_overview()
        largest = client.disk_largest(_LARGEST_FETCH_LIMIT)
        files = _normalize_largest_files(largest)
        text, keyboard = _format_disk_message(overview, files, page=1)
    except Exception as exc:
        text = f"❌ disk hatası: {html.escape(str(exc))}"
        keyboard = None
    if update.message:
        for index, part in enumerate(chunk_text(text)):
            kwargs: dict = {"parse_mode": "HTML"}
            if index == 0 and keyboard is not None:
                kwargs["reply_markup"] = keyboard
            await update.message.reply_text(part, **kwargs)


async def disk_largest_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.disk_largest(_LARGEST_FETCH_LIMIT)
        files = _normalize_largest_files(data)
        text, keyboard = _format_largest_lines(
            files,
            page=1,
            title="En Büyük Dosyalar",
            prefix=DISK_LARGEST_PREFIX,
        )
    except Exception as exc:
        text = f"❌ disk largest hatası: {html.escape(str(exc))}"
        keyboard = None
    if update.message:
        kwargs: dict = {"parse_mode": "HTML"}
        if keyboard is not None:
            kwargs["reply_markup"] = keyboard
        await update.message.reply_text(text, **kwargs)


async def handle_disk_largest_page_callback(
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
        if prefix != DISK_LARGEST_PREFIX:
            return
    except ValueError:
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.disk_largest(_LARGEST_FETCH_LIMIT)
        files = _normalize_largest_files(data)
        text, keyboard = _format_largest_lines(
            files,
            page,
            title="En Büyük Dosyalar",
            prefix=DISK_LARGEST_PREFIX,
        )
        edit_kwargs: dict = {"parse_mode": "HTML"}
        if keyboard is not None:
            edit_kwargs["reply_markup"] = keyboard
        await query.edit_message_text(text, **edit_kwargs)
    except Exception as exc:
        await query.edit_message_text(
            f"❌ disk largest hatası: {html.escape(str(exc))}",
            parse_mode="HTML",
        )


async def handle_disk_page_callback(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
) -> None:
    """Paginate the largest-files section inside /disk overview messages."""
    query = update.callback_query
    if query is None or query.data is None:
        return
    if not is_authorized(update, context):
        await query.answer("⛔ Bu bot için yetkiniz yok.", show_alert=True)
        return
    await query.answer()
    try:
        prefix, page = decode_page_cb(query.data)
        if prefix != DISK_PREFIX:
            return
    except ValueError:
        return
    client = get_client(context)
    try:
        client.ensure_token()
        overview = client.disk_overview()
        largest = client.disk_largest(_LARGEST_FETCH_LIMIT)
        files = _normalize_largest_files(largest)
        text, keyboard = _format_disk_message(overview, files, page)
        edit_kwargs: dict = {"parse_mode": "HTML"}
        if keyboard is not None:
            edit_kwargs["reply_markup"] = keyboard
        await query.edit_message_text(text, **edit_kwargs)
    except Exception as exc:
        await query.edit_message_text(
            f"❌ disk hatası: {html.escape(str(exc))}",
            parse_mode="HTML",
        )


def register(application) -> None:
    application.add_handler(CommandHandler("disk", disk_cmd))
    application.add_handler(CommandHandler("disk_largest", disk_largest_cmd))
