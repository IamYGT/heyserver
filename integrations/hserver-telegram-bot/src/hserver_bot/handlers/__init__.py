"""Command handlers."""

from __future__ import annotations

from telegram import Update
from telegram.ext import ContextTypes

from hserver_bot.handlers import (
    admin,
    alerts,
    backups,
    callbacks,
    cloudflare,
    confirm,
    cron,
    databases,
    deploy,
    digest,
    disk,
    disk_cleanup,
    docker,
    domains,
    dashboard,
    health,
    nginx,
    pm2,
    snapshot,
    ssl,
    start,
    system,
)

_HANDLER_MODULES = (
    start,
    dashboard,
    callbacks,
    confirm,
    digest,
    health,
    system,
    backups,
    snapshot,
    disk,
    disk_cleanup,
    pm2,
    ssl,
    cron,
    databases,
    nginx,
    docker,
    cloudflare,
    deploy,
    alerts,
    domains,
    admin,
)


def register_handlers(application) -> None:
    for module in _HANDLER_MODULES:
        module.register(application)


async def help_command(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    text = """🤖 <b>HserverTrack Bot</b>

<b>Başlangıç:</b> /menu /start /register
<b>Sistem:</b> /health /system /disk /disk_scan /disk_largest /pm2 /audit
<b>Yedek:</b> /backups /gdrive /gdrive_test /snapshot /snapshot_list /snapshot_run
<b>Servisler:</b> /nginx /docker /ssl /cron /db /domains /domain
<b>Deploy:</b> /deploy_targets /deploy_history /deploy_run
<b>Cloudflare:</b> /cf_zones /cf_purge
<b>Uptime:</b> /monitors /incidents /alerts
<b>Digest:</b> /digest /digest_on /digest_off
<b>Admin:</b> /help

💡 İpucu: /menu ile butonlu dashboard kullanın."""
    if update.message:
        await update.message.reply_text(text, parse_mode="HTML")
