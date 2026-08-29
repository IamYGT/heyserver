"""Scheduled daily digest reports via JobQueue."""

from __future__ import annotations

import asyncio
import json
import logging
from datetime import datetime, time
from pathlib import Path
from zoneinfo import ZoneInfo

from telegram.ext import ContextTypes

from hserver_bot.api.client import HServerClient
from hserver_bot.config import Settings, ensure_writable_data_dir, resolve_data_dir
from hserver_bot.handlers.common import chunk_text
from hserver_bot.utils.formatters import format_bytes, truncate_telegram

logger = logging.getLogger(__name__)

JOB_NAME = "daily_digest"


def subscribers_file(data_dir: str | Path | None = None) -> Path:
    """Return the subscriber store below the configured data directory."""
    return resolve_data_dir(data_dir) / "digest_subscribers.json"


def ensure_data_dir(data_dir: str | Path | None = None) -> Path:
    """Create and verify the configured data directory before using it."""
    return ensure_writable_data_dir(resolve_data_dir(data_dir))


def load_subscriber_ids(data_dir: str | Path | None = None) -> list[int]:
    subscriber_path = subscribers_file(data_dir)
    if not subscriber_path.exists():
        return []
    try:
        raw = json.loads(subscriber_path.read_text(encoding="utf-8"))
        if isinstance(raw, list):
            return sorted({int(chat_id) for chat_id in raw})
        if isinstance(raw, dict) and "chat_ids" in raw:
            return sorted({int(chat_id) for chat_id in raw["chat_ids"]})
    except (json.JSONDecodeError, TypeError, ValueError):
        logger.warning("invalid digest subscribers file: %s", subscriber_path)
    return []


def save_subscriber_ids(chat_ids: list[int], data_dir: str | Path | None = None) -> None:
    subscriber_path = subscribers_file(data_dir)
    ensure_data_dir(subscriber_path.parent)
    unique = sorted(set(chat_ids))
    subscriber_path.write_text(json.dumps(unique, indent=2) + "\n", encoding="utf-8")


def subscribe_chat(chat_id: int, data_dir: str | Path | None = None) -> bool:
    subscribers = load_subscriber_ids(data_dir)
    if chat_id in subscribers:
        return False
    subscribers.append(chat_id)
    save_subscriber_ids(subscribers, data_dir)
    return True


def unsubscribe_chat(chat_id: int, data_dir: str | Path | None = None) -> bool:
    subscribers = load_subscriber_ids(data_dir)
    if chat_id not in subscribers:
        return False
    save_subscriber_ids([item for item in subscribers if item != chat_id], data_dir)
    return True


def _extract_backups(data: dict | list | None) -> list[dict]:
    if isinstance(data, list):
        return [item for item in data if isinstance(item, dict)]
    if isinstance(data, dict):
        backups = data.get("backups", data)
        if isinstance(backups, list):
            return [item for item in backups if isinstance(item, dict)]
    return []


def _extract_incidents(data: dict | list | None) -> list[dict]:
    if isinstance(data, list):
        return [item for item in data if isinstance(item, dict)]
    if isinstance(data, dict):
        incidents = data.get("incidents", data)
        if isinstance(incidents, list):
            return [item for item in incidents if isinstance(item, dict)]
    return []


def _count_open_incidents(incidents: list[dict]) -> int:
    return sum(1 for incident in incidents if not incident.get("resolved_at"))


def _fetch_digest_data(client: HServerClient) -> dict:
    client.ensure_token()
    return {
        "health": client.health(),
        "disk": client.disk_overview(),
        "backups": client.list_backups(),
        "gdrive": client.gdrive_status(),
        "incidents": client.list_incidents(),
    }


def _format_digest(data: dict) -> str:
    now = datetime.now(tz=ZoneInfo("UTC")).strftime("%Y-%m-%d %H:%M UTC")
    lines = [f"📊 *HserverTrack Daily Digest*\n_{now}_"]

    health = data.get("health")
    if isinstance(health, dict):
        status = health.get("status", "?")
        version = health.get("version")
        health_line = f"*Health:* `{status}`"
        if version:
            health_line += f" (v{version})"
        lines.append(health_line)
    else:
        lines.append("*Health:* unavailable")

    disk = data.get("disk")
    if isinstance(disk, dict):
        total = disk.get("totalSize") or disk.get("total_size") or 0
        used = disk.get("totalUsed") or disk.get("total_used") or 0
        free = disk.get("totalFree") or disk.get("total_free") or 0
        if total:
            pct = (used / total) * 100 if total else 0.0
            lines.append(
                "*Disk:* "
                f"{format_bytes(used)} / {format_bytes(total)} ({pct:.1f}% used), "
                f"free {format_bytes(free)}"
            )
        else:
            lines.append("*Disk:* no partition totals")
    else:
        lines.append("*Disk:* unavailable")

    backups = _extract_backups(data.get("backups"))
    if backups:
        latest = backups[0]
        backup_id = latest.get("id", "?")
        size = latest.get("sizeHuman") or latest.get("size", "?")
        created = latest.get("createdAt") or latest.get("created_at")
        backup_line = f"*Last backup:* `{backup_id}` — {size}"
        if created:
            backup_line += f" ({created})"
        lines.append(backup_line)
    else:
        lines.append("*Last backup:* none")

    gdrive = data.get("gdrive")
    if isinstance(gdrive, dict):
        settings = gdrive.get("settings") or {}
        connected = gdrive.get("connected")
        last_upload = settings.get("lastUploadAt", "-")
        last_error = settings.get("lastError")
        gdrive_line = f"*GDrive:* connected=`{connected}` lastUpload=`{last_upload}`"
        if last_error:
            gdrive_line += f"\nlastError: `{last_error}`"
        lines.append(gdrive_line)
    else:
        lines.append("*GDrive:* unavailable")

    incidents = _extract_incidents(data.get("incidents"))
    open_count = _count_open_incidents(incidents)
    lines.append(f"*Open incidents:* {open_count}")

    return truncate_telegram("\n".join(lines))


async def build_digest_text(client: HServerClient) -> str:
    try:
        data = await asyncio.to_thread(_fetch_digest_data, client)
    except Exception as exc:
        logger.exception("digest data fetch failed")
        return f"❌ digest oluşturulamadı: {exc}"
    return _format_digest(data)


async def send_digest(application, chat_ids: list[int]) -> None:
    if not chat_ids:
        return
    client = application.bot_data["hserver_client"]
    text = await build_digest_text(client)
    for chat_id in chat_ids:
        try:
            for part in chunk_text(text):
                await application.bot.send_message(chat_id=chat_id, text=part, parse_mode="Markdown")
        except Exception:
            logger.exception("digest send failed chat_id=%s", chat_id)


def resolve_digest_chat_ids(settings: Settings) -> list[int]:
    recipients = set(settings.admin_chat_ids())
    recipients.update(load_subscriber_ids(settings.hserver_bot_data_dir))
    return sorted(recipients)


async def _daily_digest_callback(context: ContextTypes.DEFAULT_TYPE) -> None:
    settings = context.application.bot_data["settings"]
    chat_ids = resolve_digest_chat_ids(settings)
    if not chat_ids:
        logger.info("daily digest skipped: no recipients configured")
        return
    await send_digest(context.application, chat_ids)


def schedule_digest(application, settings: Settings) -> None:
    if not settings.digest_enabled:
        return
    job_queue = application.job_queue
    if job_queue is None:
        logger.warning("JobQueue unavailable; daily digest not scheduled")
        return

    for job in job_queue.get_jobs_by_name(JOB_NAME):
        job.schedule_removal()

    job_queue.run_daily(
        _daily_digest_callback,
        time=time(hour=settings.digest_hour_utc, minute=0, tzinfo=ZoneInfo("UTC")),
        name=JOB_NAME,
    )
    logger.info("daily digest scheduled at %02d:00 UTC", settings.digest_hour_utc)
