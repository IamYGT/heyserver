"""Inline keyboard pagination helpers for long Telegram lists."""

from __future__ import annotations

from telegram import InlineKeyboardButton, InlineKeyboardMarkup

PAGE_SIZE = 5
_CB_PREFIX = "page"


def encode_page_cb(prefix: str, page: int) -> str:
    """Encode a page callback_data string (must stay under 64 bytes)."""
    return f"{_CB_PREFIX}:{prefix}:{page}"


def decode_page_cb(data: str) -> tuple[str, int]:
    """Decode callback_data into (prefix, page)."""
    parts = data.split(":")
    if len(parts) < 3 or parts[0] != _CB_PREFIX:
        raise ValueError(f"invalid page callback: {data!r}")
    prefix = parts[1]
    try:
        page = int(parts[2])
    except ValueError as exc:
        raise ValueError(f"invalid page number in {data!r}") from exc
    return prefix, page


def paginate_items(
    items: list,
    page: int,
    page_size: int = PAGE_SIZE,
) -> tuple[list, int, int]:
    """Return (page_slice, clamped_page, total_pages) for the given items."""
    if page_size < 1:
        page_size = PAGE_SIZE
    total = len(items)
    total_pages = max(1, (total + page_size - 1) // page_size)
    page = max(1, min(page, total_pages))
    start = (page - 1) * page_size
    return items[start : start + page_size], page, total_pages


def _back_home_row() -> list[InlineKeyboardButton]:
    try:
        from hserver_bot.utils import keyboards

        return keyboards.back_home_row()
    except (ImportError, AttributeError):
        return [InlineKeyboardButton("🏠 Home", callback_data="dash:home")]


def build_page_keyboard(
    prefix: str,
    page: int,
    total_pages: int,
) -> InlineKeyboardMarkup | None:
    """Build ◀️ page/N ▶️ navigation keyboard; None when a single page."""
    if total_pages <= 1:
        return None

    nav_buttons: list[InlineKeyboardButton] = []
    if page > 1:
        nav_buttons.append(
            InlineKeyboardButton("◀️", callback_data=encode_page_cb(prefix, page - 1))
        )
    nav_buttons.append(
        InlineKeyboardButton(f"{page}/{total_pages}", callback_data=encode_page_cb(prefix, page))
    )
    if page < total_pages:
        nav_buttons.append(
            InlineKeyboardButton("▶️", callback_data=encode_page_cb(prefix, page + 1))
        )

    rows = [nav_buttons, _back_home_row()]
    return InlineKeyboardMarkup(rows)
