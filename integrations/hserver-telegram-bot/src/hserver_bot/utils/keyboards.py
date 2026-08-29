"""Inline keyboard builders for dashboard and pagination."""

from __future__ import annotations

from telegram import InlineKeyboardButton, InlineKeyboardMarkup

# Compact callback_data prefixes (Telegram limit: 64 bytes).
CB_HEALTH = "dash:health"
CB_BACKUPS = "dash:backups"
CB_SYSTEM = "dash:system"
CB_DISK = "dash:disk"
CB_GDRIVE = "dash:gdrive"
CB_PM2 = "dash:pm2"
CB_MONITORS = "dash:monitors"
CB_ALERTS = "dash:alerts"
CB_REFRESH = "dash:refresh"
CB_HOME = "dash:home"

DASH_PREFIX = "dash:"


def _btn(label: str, callback_data: str) -> InlineKeyboardButton:
    return InlineKeyboardButton(label, callback_data=callback_data)


def main_menu_keyboard() -> InlineKeyboardMarkup:
    """Two-column grid grouped by Sistem, Yedek, Servisler, Uptime."""
    rows = [
        # Sistem
        [_btn("💚 Health", CB_HEALTH), _btn("🖥 Sistem", CB_SYSTEM)],
        [_btn("💾 Disk", CB_DISK), _btn("⚙️ PM2", CB_PM2)],
        # Yedek
        [_btn("📦 Yedekler", CB_BACKUPS), _btn("☁️ GDrive", CB_GDRIVE)],
        # Uptime
        [_btn("📡 Monitörler", CB_MONITORS), _btn("🔔 Uyarılar", CB_ALERTS)],
        # Actions
        [_btn("🔄 Yenile", CB_REFRESH)],
    ]
    return InlineKeyboardMarkup(rows)


def back_home_row() -> list[InlineKeyboardButton]:
    """Single row with home navigation button."""
    return [_btn("🏠 Ana Menü", CB_HOME)]


def pagination_row(prefix: str, page: int, total_pages: int) -> list[InlineKeyboardButton]:
    """Navigation row for paginated lists. *prefix* is e.g. ``backups`` or ``disk``."""
    if total_pages < 1:
        total_pages = 1
    page = max(1, min(page, total_pages))
    row: list[InlineKeyboardButton] = []

    if page > 1:
        row.append(_btn("◀️", f"{prefix}:p:{page - 1}"))

    row.append(
        InlineKeyboardButton(
            f"{page}/{total_pages}",
            callback_data=f"{prefix}:p:{page}",
        )
    )

    if page < total_pages:
        row.append(_btn("▶️", f"{prefix}:p:{page + 1}"))

    return row
