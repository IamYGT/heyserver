"""Confirmation flows for destructive operations via inline keyboards."""

from __future__ import annotations

import json
from typing import Any

from telegram import InlineKeyboardButton, InlineKeyboardMarkup, Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client, is_authorized

PENDING_KEY = "pending_actions"


def _pending_store(context: ContextTypes.DEFAULT_TYPE) -> dict[int, dict[str, Any]]:
    return context.user_data.setdefault(PENDING_KEY, {})


def _set_pending(
    context: ContextTypes.DEFAULT_TYPE,
    user_id: int,
    action: str,
    **payload: Any,
) -> None:
    _pending_store(context)[user_id] = {"action": action, **payload}


def _clear_pending(context: ContextTypes.DEFAULT_TYPE, user_id: int) -> None:
    _pending_store(context).pop(user_id, None)


def _get_pending(context: ContextTypes.DEFAULT_TYPE, user_id: int) -> dict[str, Any] | None:
    return _pending_store(context).get(user_id)


def _confirm_keyboard(action: str, arg: str | None = None) -> InlineKeyboardMarkup:
    callback_data = f"confirm:{action}:{arg}" if arg is not None else f"confirm:{action}"
    return InlineKeyboardMarkup(
        [
            [
                InlineKeyboardButton("✅ Onayla", callback_data=callback_data),
                InlineKeyboardButton("❌ İptal", callback_data="confirm:cancel"),
            ]
        ]
    )


async def _deny_unauthorized_callback(update: Update, context: ContextTypes.DEFAULT_TYPE) -> bool:
    if is_authorized(update, context):
        return False
    query = update.callback_query
    if query:
        await query.answer("⛔ Bu bot için yetkiniz yok.", show_alert=True)
    return True


async def _reply_or_edit(
    update: Update,
    text: str,
    *,
    parse_mode: str | None = "Markdown",
    reply_markup: InlineKeyboardMarkup | None = None,
) -> None:
    query = update.callback_query
    if query and query.message:
        await query.edit_message_text(text, parse_mode=parse_mode, reply_markup=reply_markup)
        return
    if update.message:
        await update.message.reply_text(text, parse_mode=parse_mode, reply_markup=reply_markup)


async def deploy_run_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    if not context.args:
        if update.message:
            await update.message.reply_text("Kullanım: `/deploy_run <target_id>`", parse_mode="Markdown")
        return

    target_id = context.args[0]
    user = update.effective_user
    if user is None:
        return

    _set_pending(context, user.id, "deploy_run", target_id=target_id)
    warning = (
        "⚠️ *Deploy tetiklenecek*\n\n"
        f"Target: `{target_id}`\n\n"
        "Bu işlem canlı ortamda deployment başlatır. Devam etmek için onaylayın."
    )
    if update.message:
        await update.message.reply_text(
            warning,
            parse_mode="Markdown",
            reply_markup=_confirm_keyboard("deploy_run", target_id),
        )


async def snapshot_run_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return

    user = update.effective_user
    if user is None:
        return

    _set_pending(context, user.id, "snapshot_run")
    warning = (
        "⚠️ *Restic snapshot başlatılacak*\n\n"
        "Bu işlem disk ve CPU kullanır; yedekleme süresi sunucu yüküne bağlıdır.\n"
        "Devam etmek için onaylayın."
    )
    if update.message:
        await update.message.reply_text(
            warning,
            parse_mode="Markdown",
            reply_markup=_confirm_keyboard("snapshot_run"),
        )


async def cf_purge_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    if not context.args:
        if update.message:
            await update.message.reply_text("Kullanım: `/cf_purge <zone_id>`", parse_mode="Markdown")
        return

    zone_id = context.args[0]
    user = update.effective_user
    if user is None:
        return

    _set_pending(context, user.id, "cf_purge", zone_id=zone_id)
    warning = (
        "⚠️ *Cloudflare cache temizlenecek*\n\n"
        f"Zone: `{zone_id}`\n\n"
        "Tüm edge cache silinir; trafik geçici olarak origin'e yönlenebilir.\n"
        "Devam etmek için onaylayın."
    )
    if update.message:
        await update.message.reply_text(
            warning,
            parse_mode="Markdown",
            reply_markup=_confirm_keyboard("cf_purge", zone_id),
        )


async def gdrive_test_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return

    user = update.effective_user
    if user is None:
        return

    _set_pending(context, user.id, "gdrive_test")
    warning = (
        "ℹ️ *GDrive bağlantı testi*\n\n"
        "Google Drive API'ye test isteği gönderilecek.\n"
        "Devam etmek için onaylayın."
    )
    if update.message:
        await update.message.reply_text(
            warning,
            parse_mode="Markdown",
            reply_markup=_confirm_keyboard("gdrive_test"),
        )


async def _execute_deploy_run(client, target_id: str) -> str:
    result = client.trigger_deploy(target_id)
    run_id = result.get("runId", "?") if isinstance(result, dict) else "?"
    message = result.get("message", "queued") if isinstance(result, dict) else "queued"
    return f"🚀 Deploy tetiklendi\nTarget: `{target_id}`\nRun: `{run_id}`\n{message}"


async def _execute_snapshot_run(client) -> str:
    result = client.run_snapshot()
    job_id = result.get("jobId", "-")
    message = result.get("message", result.get("status", "started"))
    return f"✅ Snapshot başlatıldı\njobId: `{job_id}`\n{message}"


async def _execute_cf_purge(client, zone_id: str) -> str:
    result = client.purge_zone(zone_id)
    status = result.get("status", result) if isinstance(result, dict) else result
    return f"✅ Cloudflare cache purge: `{status}` (zone `{zone_id}`)"


async def _execute_gdrive_test(client) -> str:
    result = client.gdrive_test()
    return f"✅ GDrive test\n```json\n{json.dumps(result, indent=2)}\n```"


async def handle_confirm_callback(update: Update, context: ContextTypes.DEFAULT_TYPE) -> bool:
    """Handle confirm:* callback_data. Returns True if handled."""
    query = update.callback_query
    if query is None or not query.data:
        return False

    data = query.data
    if not data.startswith("confirm:"):
        return False

    await query.answer()

    if await _deny_unauthorized_callback(update, context):
        return True

    user = update.effective_user
    if user is None:
        return True

    if data == "confirm:cancel":
        _clear_pending(context, user.id)
        await _reply_or_edit(update, "İptal edildi", parse_mode=None)
        return True

    parts = data.split(":", 2)
    if len(parts) < 2:
        return True

    action = parts[1]
    arg = parts[2] if len(parts) > 2 else None
    pending = _get_pending(context, user.id)

    if pending is None or pending.get("action") != action:
        await _reply_or_edit(update, "⏱ Onay süresi doldu veya geçersiz istek. Komutu yeniden çalıştırın.")
        return True

    if action == "deploy_run":
        target_id = pending.get("target_id")
        if not target_id or (arg is not None and str(arg) != str(target_id)):
            await _reply_or_edit(update, "⏱ Onay süresi doldu veya geçersiz istek. Komutu yeniden çalıştırın.")
            return True
    elif action == "cf_purge":
        zone_id = pending.get("zone_id")
        if not zone_id or (arg is not None and str(arg) != str(zone_id)):
            await _reply_or_edit(update, "⏱ Onay süresi doldu veya geçersiz istek. Komutu yeniden çalıştırın.")
            return True

    _clear_pending(context, user.id)
    client = get_client(context)
    try:
        client.ensure_token()
        if action == "deploy_run":
            text = await _execute_deploy_run(client, str(pending["target_id"]))
        elif action == "snapshot_run":
            text = await _execute_snapshot_run(client)
        elif action == "cf_purge":
            text = await _execute_cf_purge(client, str(pending["zone_id"]))
        elif action == "gdrive_test":
            text = await _execute_gdrive_test(client)
        else:
            await _reply_or_edit(update, f"Bilinmeyen onay aksiyonu: `{action}`")
            return True
    except Exception as exc:
        text = f"❌ {action} hatası: {exc}"

    await _reply_or_edit(update, text, parse_mode="Markdown")
    return True


def register(application) -> None:
    application.add_handler(CommandHandler("deploy_run", deploy_run_cmd))
    application.add_handler(CommandHandler("snapshot_run", snapshot_run_cmd))
    application.add_handler(CommandHandler("cf_purge", cf_purge_cmd))
    application.add_handler(CommandHandler("gdrive_test", gdrive_test_cmd))
