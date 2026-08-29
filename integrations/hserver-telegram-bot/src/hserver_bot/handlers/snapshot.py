"""Restic snapshot commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client


def _format_bytes(size: int | float | None) -> str:
    if not size or size <= 0:
        return "0 B"
    units = ["B", "KB", "MB", "GB", "TB"]
    value = float(size)
    unit = 0
    while value >= 1024 and unit < len(units) - 1:
        value /= 1024
        unit += 1
    return f"{value:.1f} {units[unit]}"


async def snapshot_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        st = client.snapshot_status()
        settings = st.get("settings") or {}
        stats = st.get("repoStats") or {}
        text = (
            "*Restic Snapshot*\n"
            f"restic: `{st.get('resticFound')}`\n"
            f"repo: `{st.get('repoInitialized')}`\n"
            f"password: `{st.get('passwordSet')}`\n"
            f"drive: `{st.get('driveConnected')}`\n"
            f"snapshots: `{stats.get('snapshotCount', 0)}`\n"
            f"repo size: `{_format_bytes(stats.get('totalSize'))}`\n"
            f"last run: `{settings.get('lastRunAt', '-')}`\n"
            f"last error: `{settings.get('lastError', '-')}`"
        )
    except Exception as exc:
        text = f"❌ snapshot hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


async def snapshot_list_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.snapshot_list()
        snaps = data.get("snapshots", data) if isinstance(data, dict) else data
        lines = ["*Snapshot listesi*"]
        for snap in (snaps or [])[:15]:
            if isinstance(snap, dict):
                snap_id = snap.get("id", "?")
                when = snap.get("time", "-")
                size = _format_bytes(snap.get("size"))
                lines.append(f"• `{snap_id[:12]}` — {when} — {size}")
        text = "\n".join(lines) if len(lines) > 1 else "Snapshot bulunamadı."
    except Exception as exc:
        text = f"❌ snapshot list hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


async def snapshot_run_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        result = client.run_snapshot()
        job_id = result.get("jobId", "-")
        message = result.get("message", result.get("status", "started"))
        text = f"✅ Snapshot başlatıldı\njobId: `{job_id}`\n{message}"
    except Exception as exc:
        text = f"❌ snapshot run hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("snapshot", snapshot_cmd))
    application.add_handler(CommandHandler("snapshot_list", snapshot_list_cmd))
