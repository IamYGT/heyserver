"""Disk cleanup and largest-file commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import chunk_text, deny_unauthorized, get_client


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


async def disk_scan_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.cleanup_scan()
        targets = data if isinstance(data, list) else data.get("targets", data)
        lines = ["*Disk Cleanup Scan*"]
        for target in (targets or [])[:20]:
            if isinstance(target, dict):
                tid = target.get("id", "?")
                name = target.get("name", tid)
                desc = target.get("description", "")
                size = _format_size(target.get("size"))
                lines.append(f"• `{tid}` — {name} ({size})")
                if desc:
                    lines.append(f"  _{desc}_")
        text = "\n".join(lines) if len(lines) > 1 else "Temizlenebilir hedef bulunamadı."
    except Exception as exc:
        text = f"❌ disk scan hatası: {exc}"
    if update.message:
        for part in chunk_text(text):
            await update.message.reply_text(part, parse_mode="Markdown")


async def disk_largest_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    limit = 10
    if context.args:
        try:
            limit = max(1, min(50, int(context.args[0])))
        except ValueError:
            pass
    try:
        client.ensure_token()
        data = client.disk_largest(limit)
        files = data if isinstance(data, list) else data.get("files", data)
        lines = [f"*En Büyük Dosyalar* (top {limit})"]
        for item in (files or [])[:limit]:
            if isinstance(item, dict):
                path = item.get("path") or item.get("Path", "?")
                size = _format_size(item.get("size") or item.get("Size"))
                modified = item.get("modified") or item.get("Modified", "")
                suffix = f" — {modified}" if modified else ""
                lines.append(f"• `{path}` — {size}{suffix}")
        text = "\n".join(lines) if len(lines) > 1 else "Büyük dosya bulunamadı."
    except Exception as exc:
        text = f"❌ disk largest hatası: {exc}"
    if update.message:
        for part in chunk_text(text):
            await update.message.reply_text(part, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("disk_scan", disk_scan_cmd))
