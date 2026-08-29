"""Centralized callback query router."""

from __future__ import annotations

import logging
import re
from collections.abc import Awaitable, Callable

from telegram import Update
from telegram.error import BadRequest, NetworkError, TimedOut
from telegram.ext import Application, CallbackQueryHandler, ContextTypes

logger = logging.getLogger(__name__)

CallbackHandler = Callable[[Update, ContextTypes.DEFAULT_TYPE], Awaitable[None]]

CALLBACK_PREFIX_PATTERN = re.compile(r"^(dash|page|confirm|alert):(.+)$")


async def safe_edit(
    query,
    text: str,
    reply_markup=None,
    parse_mode: str = "HTML",
) -> bool:
    """Edit the callback message, tolerating no-op edits and race conditions."""
    try:
        await query.edit_message_text(
            text=text,
            reply_markup=reply_markup,
            parse_mode=parse_mode,
        )
        return True
    except BadRequest as exc:
        message = str(exc).lower()
        if "message is not modified" in message:
            return False
        if any(
            phrase in message
            for phrase in (
                "message to edit not found",
                "message can't be edited",
                "message identifier is not specified",
            )
        ):
            logger.debug("safe_edit skipped race condition: %s", exc)
            return False
        raise
    except (TimedOut, NetworkError) as exc:
        logger.warning("safe_edit transient failure: %s", exc)
        return False


def match_callback_prefix(data: str) -> tuple[str, str] | None:
    """Return (prefix, payload) when data matches a known callback prefix."""
    match = CALLBACK_PREFIX_PATTERN.match(data)
    if not match:
        return None
    return match.group(1), match.group(2)


async def handle_pagination_callback(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
) -> None:
    from hserver_bot.handlers.backups import BACKUPS_PREFIX, handle_backups_page_callback
    from hserver_bot.handlers.disk import (
        DISK_LARGEST_PREFIX,
        DISK_PREFIX,
        handle_disk_largest_page_callback,
        handle_disk_page_callback,
    )
    from hserver_bot.utils.pagination import decode_page_cb

    query = update.callback_query
    if query is None or not query.data:
        return
    try:
        prefix, _page = decode_page_cb(query.data)
    except ValueError:
        return
    if prefix == BACKUPS_PREFIX:
        await handle_backups_page_callback(update, context)
    elif prefix == DISK_PREFIX:
        await handle_disk_page_callback(update, context)
    elif prefix == DISK_LARGEST_PREFIX:
        await handle_disk_largest_page_callback(update, context)


async def handle_confirm_callback_router(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
) -> None:
    from hserver_bot.handlers.confirm import handle_confirm_callback

    await handle_confirm_callback(update, context)


async def handle_alert_callback(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
) -> None:
    from hserver_bot.handlers.alerts import handle_alerts_callback

    await handle_alerts_callback(update, context)


async def handle_dashboard_callback(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
) -> None:
    from hserver_bot.handlers.dashboard import handle_dashboard_callback as dash_handler

    await dash_handler(update, context)


_PREFIX_HANDLERS: dict[str, CallbackHandler] = {
    "dash": handle_dashboard_callback,
    "page": handle_pagination_callback,
    "confirm": handle_confirm_callback_router,
    "alert": handle_alert_callback,
}


async def callback_router(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    query = update.callback_query
    if query is None or not query.data:
        return

    matched = match_callback_prefix(query.data)
    if matched is None:
        await query.answer("Bilinmeyen işlem", show_alert=True)
        return

    prefix, _payload = matched
    handler = _PREFIX_HANDLERS.get(prefix)
    if handler is None:
        await query.answer("Bilinmeyen işlem", show_alert=True)
        return

    await handler(update, context)


def register(application: Application) -> None:
    application.add_handler(CallbackQueryHandler(callback_router))
