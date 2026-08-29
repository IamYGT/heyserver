"""Production runtime bootstrap — webhook/polling modes and bot command menu."""

from __future__ import annotations

import asyncio
import logging
from typing import TYPE_CHECKING

from telegram import BotCommand
from telegram.ext import Application

from hserver_bot.config import Settings, load_settings

if TYPE_CHECKING:
    from telegram.ext import ApplicationBuilder

logger = logging.getLogger(__name__)

ALLOWED_UPDATES = ["message", "callback_query"]

BOT_COMMANDS = [
    BotCommand("menu", "Open dashboard menu"),
    BotCommand("health", "Server health summary"),
    BotCommand("system", "System metrics"),
    BotCommand("disk", "Disk usage"),
    BotCommand("backups", "Backup status"),
    BotCommand("gdrive", "Google Drive backup"),
    BotCommand("monitors", "Uptime monitors"),
    BotCommand("alerts", "Alert rules"),
    BotCommand("help", "Command reference"),
    BotCommand("register", "Register chat for notifications"),
]


async def post_init(application: Application) -> None:
    """Register native Telegram command menu via setMyCommands."""
    await application.bot.set_my_commands(BOT_COMMANDS)
    logger.info("Registered %d bot commands", len(BOT_COMMANDS))


def configure_application_builder(settings: Settings) -> ApplicationBuilder:
    """Return a pre-configured Application builder with runtime hooks."""
    return (
        Application.builder()
        .token(settings.telegram_bot_token)
        .post_init(post_init)
    )


def _resolve_settings(app: Application) -> Settings:
    settings = app.bot_data.get("settings")
    if isinstance(settings, Settings):
        return settings
    return load_settings()


def _run_blocking(app: Application, settings: Settings) -> None:
    if settings.telegram_use_webhook:
        if not settings.telegram_webhook_url.strip():
            raise ValueError("TELEGRAM_WEBHOOK_URL is required when TELEGRAM_USE_WEBHOOK=true")
        url_path = settings.telegram_webhook_path.lstrip("/")
        logger.info(
            "Starting webhook on 0.0.0.0:%s/%s -> %s",
            settings.telegram_webhook_port,
            url_path,
            settings.telegram_webhook_url,
        )
        app.run_webhook(
            listen="0.0.0.0",
            port=settings.telegram_webhook_port,
            url_path=url_path,
            webhook_url=settings.telegram_webhook_url,
            allowed_updates=ALLOWED_UPDATES,
        )
        return

    logger.info("Starting long polling (allowed_updates=%s)", ALLOWED_UPDATES)
    app.run_polling(allowed_updates=ALLOWED_UPDATES)


async def run_application(app: Application) -> None:
    """Run the bot in webhook or polling mode based on settings."""
    settings = _resolve_settings(app)
    await asyncio.to_thread(_run_blocking, app, settings)
