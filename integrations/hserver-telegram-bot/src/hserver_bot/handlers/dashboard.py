"""Interactive dashboard with inline keyboard navigation."""

from __future__ import annotations

import html
import json
import logging

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client
from hserver_bot.utils.formatters import format_health_summary, format_kv_card
from hserver_bot.utils.keyboards import (
    CB_REFRESH,
    DASH_PREFIX,
    back_home_row,
    main_menu_keyboard,
)

logger = logging.getLogger(__name__)

WELCOME_HTML = (
    "<b>🤖 HserverTrack Dashboard</b>\n\n"
    "Sunucu durumuna hızlı erişim için bir bölüm seçin.\n\n"
    "<b>Sistem</b> — health, sistem, disk, pm2\n"
    "<b>Yedek</b> — yedekler, Google Drive\n"
    "<b>Uptime</b> — monitörler, uyarılar"
)


async def _fetch_dashboard_view(action: str, client) -> str:
    client.ensure_token()
    if action == "health":
        return format_health_summary(client.health())
    if action == "system":
        data = client.system_stats()
        pairs = [
            ("Hostname", str(data.get("hostname", "?"))),
            ("CPU", str(data.get("cpuPercent", data.get("cpu", "?")))),
            ("Memory", str(data.get("memoryUsedPercent", data.get("memory", "?")))),
            ("Uptime", str(data.get("uptime", "?"))),
        ]
        return format_kv_card("System", pairs)
    if action == "disk":
        overview = client.disk_overview()
        if isinstance(overview, dict):
            pairs = [
                (k, str(v)) for k, v in list(overview.items())[:8]
            ]
            return format_kv_card("Disk", pairs)
        return f"<pre>{html.escape(json.dumps(overview, indent=2)[:3000])}</pre>"
    if action == "backups":
        data = client.list_backups()
        backups = data.get("backups", data) if isinstance(data, dict) else data
        lines = ["<b>Son yedekler</b>"]
        for backup in (backups or [])[:5]:
            if isinstance(backup, dict):
                bid = html.escape(str(backup.get("id", "?")))
                size = html.escape(str(backup.get("sizeHuman", backup.get("size", ""))))
                lines.append(f"• <code>{bid}</code> — {size}")
        return "\n".join(lines) if len(lines) > 1 else "<b>Yedek bulunamadı</b>"
    if action == "gdrive":
        st = client.gdrive_status()
        settings = st.get("settings") or {}
        return format_kv_card(
            "Google Drive",
            [
                ("Connected", str(st.get("connected"))),
                ("Email", str(st.get("email", "-"))),
                ("Auto upload", str(settings.get("autoUpload"))),
                ("Last upload", str(settings.get("lastUploadAt", "-"))),
                ("Last error", str(settings.get("lastError", "-"))),
            ],
        )
    if action == "pm2":
        data = client.pm2_processes()
        processes = data.get("processes", data) if isinstance(data, dict) else data
        lines = ["<b>PM2</b>"]
        for proc in (processes or [])[:8]:
            if isinstance(proc, dict):
                name = html.escape(str(proc.get("name", "?")))
                pm2_env = proc.get("pm2_env")
                env_status = pm2_env.get("status") if isinstance(pm2_env, dict) else None
                status = html.escape(str(proc.get("status", env_status or "?")))
                lines.append(f"• <code>{name}</code> — {status}")
        return "\n".join(lines) if len(lines) > 1 else "<b>PM2 process yok</b>"
    if action == "monitors":
        data = client.list_monitors()
        monitors = data.get("monitors", data) if isinstance(data, dict) else data
        lines = ["<b>Uptime Monitors</b>"]
        for monitor in (monitors or [])[:8]:
            if isinstance(monitor, dict):
                name = html.escape(str(monitor.get("name", "?")))
                status = monitor.get("current_status")
                icon = "✅" if status == 1 else "🔴"
                lines.append(f"{icon} <code>{name}</code>")
        return "\n".join(lines) if len(lines) > 1 else "<b>Monitor yok</b>"
    if action == "alerts":
        data = client.list_alert_rules()
        rules = data.get("rules", data) if isinstance(data, dict) else data
        lines = ["<b>Alert Rules</b>"]
        for rule in (rules or [])[:8]:
            if isinstance(rule, dict):
                name = html.escape(str(rule.get("name", "?")))
                enabled = "✅" if rule.get("enabled", True) else "⏸"
                lines.append(f"{enabled} <code>{name}</code>")
        return "\n".join(lines) if len(lines) > 1 else "<b>Alert rule yok</b>"
    return "<b>Yakında</b>"


async def show_dashboard(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
    *,
    edit: bool = False,
) -> None:
    """Show or refresh the main dashboard menu."""
    keyboard = main_menu_keyboard()

    if edit:
        query = update.callback_query
        if query:
            await query.edit_message_text(
                WELCOME_HTML,
                reply_markup=keyboard,
                parse_mode="HTML",
            )
        return

    message = update.effective_message
    if message:
        await message.reply_text(
            WELCOME_HTML,
            reply_markup=keyboard,
            parse_mode="HTML",
        )


async def menu_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    await show_dashboard(update, context)


async def dashboard_home_callback(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
) -> None:
    """Return to the main dashboard (dash:home)."""
    query = update.callback_query
    if not query:
        return
    if await deny_unauthorized(update, context):
        await query.answer("⛔ Bu bot için yetkiniz yok.", show_alert=True)
        return
    await query.answer()
    await show_dashboard(update, context, edit=True)


async def handle_dashboard_callback(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
) -> None:
    """Route dash:* callback_data to live API views."""
    query = update.callback_query
    if not query or not query.data:
        return

    if await deny_unauthorized(update, context):
        await query.answer("⛔ Bu bot için yetkiniz yok.", show_alert=True)
        return

    data = query.data
    if not data.startswith(DASH_PREFIX):
        return

    action = data[len(DASH_PREFIX) :]

    if action == "home":
        await dashboard_home_callback(update, context)
        return

    if action == "refresh":
        await query.answer("Yenilendi")
        await show_dashboard(update, context, edit=True)
        return

    known = {"health", "backups", "system", "disk", "gdrive", "pm2", "monitors", "alerts"}
    if action in known:
        client = get_client(context)
        try:
            text = await _fetch_dashboard_view(action, client)
        except Exception as exc:
            logger.exception("dashboard fetch failed action=%s", action)
            text = f"❌ {html.escape(action)} hatası: {html.escape(str(exc))}"

        from telegram import InlineKeyboardMarkup

        from hserver_bot.handlers.callbacks import safe_edit

        keyboard = main_menu_keyboard()
        rows = list(keyboard.inline_keyboard) + [back_home_row()]
        markup = InlineKeyboardMarkup(rows)

        edited = await safe_edit(query, text, reply_markup=markup, parse_mode="HTML")
        await query.answer("Güncellendi" if edited else "Veri aynı")
        return

    await query.answer("Yakında", show_alert=False)


def register(application) -> None:
    application.add_handler(CommandHandler("menu", menu_cmd))
