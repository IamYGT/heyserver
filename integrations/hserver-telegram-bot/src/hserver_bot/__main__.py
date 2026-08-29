"""Application entrypoint."""

from __future__ import annotations

import logging

from hserver_bot.api.client import HServerClient
from hserver_bot.config import load_settings
from hserver_bot.handlers import help_command, register_handlers
from hserver_bot.middleware.rate_limit import attach_rate_limiter
from hserver_bot.services.digest import ensure_data_dir, schedule_digest
from hserver_bot.services.runtime import ALLOWED_UPDATES, configure_application_builder
from telegram.ext import CommandHandler

logging.basicConfig(
    format="%(asctime)s %(name)s %(levelname)s %(message)s",
    level=logging.INFO,
)
logger = logging.getLogger("hserver_bot")


def build_application():
    settings = load_settings()
    ensure_data_dir(settings.hserver_bot_data_dir)
    client = HServerClient(
        base_url=settings.hserver_base_url,
        email=settings.hserver_admin_email,
        password=settings.hserver_admin_pass,
    )
    app = configure_application_builder(settings).build()
    app.bot_data["settings"] = settings
    app.bot_data["hserver_client"] = client
    attach_rate_limiter(app)
    register_handlers(app)
    app.add_handler(CommandHandler("help", help_command))
    schedule_digest(app, settings)
    return app


def main() -> None:
    logger.info("Starting HserverTrack bot v0.3.0")
    app = build_application()
    app.run_polling(allowed_updates=ALLOWED_UPDATES)


if __name__ == "__main__":
    main()
