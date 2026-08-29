"""Shared handler utilities."""

from __future__ import annotations

import logging

from telegram import Update
from telegram.ext import ContextTypes

from hserver_bot.api.client import HServerClient
from hserver_bot.config import Settings
from hserver_bot.middleware.rate_limit import get_rate_limiter

logger = logging.getLogger(__name__)


def get_client(context: ContextTypes.DEFAULT_TYPE) -> HServerClient:
    return context.application.bot_data["hserver_client"]


def get_settings(context: ContextTypes.DEFAULT_TYPE) -> Settings:
    return context.application.bot_data["settings"]


def get_user_id(update: Update) -> int | None:
    user = update.effective_user
    return user.id if user else None


def is_admin_chat(update: Update, context: ContextTypes.DEFAULT_TYPE) -> bool:
    settings = get_settings(context)
    chat = update.effective_chat
    if chat is None:
        return False
    admin_chat_ids = settings.admin_chat_ids()
    if not admin_chat_ids:
        return False
    return chat.id in admin_chat_ids


def is_authorized(update: Update, context: ContextTypes.DEFAULT_TYPE) -> bool:
    settings = get_settings(context)
    user = update.effective_user
    if user is None:
        return False
    allowed = settings.allowed_user_ids()
    if not allowed:
        return True
    return user.id in allowed


async def deny_unauthorized(update: Update, context: ContextTypes.DEFAULT_TYPE) -> bool:
    if is_authorized(update, context):
        return False

    user_id = get_user_id(update)
    chat_id = update.effective_chat.id if update.effective_chat else None
    logger.warning(
        "Unauthorized bot access attempt user_id=%s chat_id=%s",
        user_id,
        chat_id,
    )

    if update.message:
        await update.message.reply_text("⛔ Bu bot için yetkiniz yok.")
    return True


async def deny_rate_limited(update: Update, context: ContextTypes.DEFAULT_TYPE) -> bool:
    """Return True when the update should be blocked by rate limiting."""
    user_id = get_user_id(update)
    if user_id is None:
        return False

    limiter = get_rate_limiter(context)
    if limiter is None:
        return False

    if limiter.check(user_id):
        return False

    wait = limiter.wait_seconds(user_id)
    if update.message:
        await update.message.reply_text(f"⏳ Çok hızlı — {wait} sn bekleyin")
    return True


def chunk_text(text: str, limit: int = 4000) -> list[str]:
    if len(text) <= limit:
        return [text]
    parts: list[str] = []
    while text:
        parts.append(text[:limit])
        text = text[limit:]
    return parts
