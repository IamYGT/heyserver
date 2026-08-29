"""Start & registration."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client, get_settings
from hserver_bot.handlers.dashboard import show_dashboard

_DEEP_LINK_COMMANDS = {
    "menu": "Dashboard açılıyor…",
    "health": "Sağlık özeti için /health",
    "backups": "Yedekler için /backups",
    "disk": "Disk için /disk",
    "help": "Komutlar için /help",
}


async def start_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    chat = update.effective_chat
    user = update.effective_user
    if not update.message or not chat or not user:
        return

    if context.args:
        action = context.args[0].lower()
        if action == "menu":
            await show_dashboard(update, context)
            return
        hint = _DEEP_LINK_COMMANDS.get(action)
        if hint:
            await update.message.reply_text(
                f"Merhaba {user.first_name}! {hint}",
                parse_mode="HTML",
            )
            return

    await update.message.reply_text(
        f"Merhaba {user.first_name}! HserverTrack aktif.\n"
        f"Chat ID: <code>{chat.id}</code>\n\n"
        "🎛 Dashboard: /menu\n"
        "Bildirimler: /register\n"
        "Komutlar: /help",
        parse_mode="HTML",
    )


async def register_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    chat = update.effective_chat
    settings = get_settings(context)
    client = get_client(context)
    if not update.message or not chat:
        return
    try:
        client.ensure_token()
        result = client.create_telegram_channel(
            name="HserverTrack Bot",
            bot_token=settings.telegram_bot_token,
            chat_id=chat.id,
        )
        await update.message.reply_text(
            f"✅ hserver bildirim kanalı oluşturuldu (id={result.get('id', '?')}).\n"
            f"Chat ID `{chat.id}` kaydedildi.",
            parse_mode="Markdown",
        )
    except Exception as exc:
        await update.message.reply_text(f"⚠️ Kanal kaydı başarısız: {exc}")


def register(application) -> None:
    application.add_handler(CommandHandler("start", start_cmd))
    application.add_handler(CommandHandler("register", register_cmd))
