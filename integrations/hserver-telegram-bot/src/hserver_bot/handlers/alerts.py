"""Uptime monitor and alert commands."""

from __future__ import annotations

from telegram import InlineKeyboardButton, InlineKeyboardMarkup, Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client, is_authorized
from hserver_bot.utils.formatters import escape_md, truncate_telegram

_MONITOR_STATUS = {
    0: "🔴 down",
    1: "✅ up",
    2: "⏳ pending",
    3: "🔧 maint",
    4: "⚠️ tls",
}

_MAX_ALERT_BUTTONS = 8


def _monitor_status_label(status: object) -> str:
    if status is None:
        return "?"
    try:
        return _MONITOR_STATUS.get(int(status), str(status))
    except (TypeError, ValueError):
        return str(status)


def _normalize_rules(data: dict | list) -> list[dict]:
    rules = data.get("rules", data) if isinstance(data, dict) else data
    if not isinstance(rules, list):
        return []
    return [rule for rule in rules if isinstance(rule, dict)]


def _format_alerts_text(rules: list[dict]) -> str:
    lines = ["*Alert Rules*"]
    for rule in rules[:15]:
        name = rule.get("name", rule.get("id", "?"))
        rtype = rule.get("type", "-")
        threshold = rule.get("threshold", "-")
        enabled = "✅" if rule.get("enabled", True) else "⏸"
        lines.append(
            f"{enabled} `{escape_md(str(name))}` — {escape_md(str(rtype))} — eşik: {threshold}"
        )
    if len(lines) == 1:
        return "Alert rule bulunamadı."
    return truncate_telegram("\n".join(lines))


def _build_alerts_keyboard(rules: list[dict]) -> InlineKeyboardMarkup:
    rows: list[list[InlineKeyboardButton]] = []
    for rule in rules[:_MAX_ALERT_BUTTONS]:
        rule_id = rule.get("id")
        if rule_id is None:
            continue
        name = str(rule.get("name", rule_id))
        enabled = rule.get("enabled", True)
        icon = "✅" if enabled else "⏸"
        label = truncate_telegram(f"{icon} {name}", max=60)
        rows.append(
            [InlineKeyboardButton(label, callback_data=f"alert:toggle:{rule_id}")]
        )
    rows.append([InlineKeyboardButton("🔄 Yenile", callback_data="alert:refresh")])
    return InlineKeyboardMarkup(rows)


async def _fetch_alerts_payload(client) -> tuple[str, InlineKeyboardMarkup | None]:
    data = client.list_alert_rules()
    rules = _normalize_rules(data)
    text = _format_alerts_text(rules)
    keyboard = _build_alerts_keyboard(rules)
    return text, keyboard


async def _deny_unauthorized_callback(update: Update, context: ContextTypes.DEFAULT_TYPE) -> bool:
    if is_authorized(update, context):
        return False
    query = update.callback_query
    if query:
        await query.answer("⛔ Bu bot için yetkiniz yok.", show_alert=True)
    return True


async def monitors_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.list_monitors()
        monitors = data.get("monitors", data) if isinstance(data, dict) else data
        lines = ["*Uptime Monitors*"]
        for monitor in (monitors or [])[:20]:
            if isinstance(monitor, dict):
                name = monitor.get("name", monitor.get("id", "?"))
                mtype = monitor.get("type", "-")
                target = monitor.get("url") or monitor.get("hostname") or "-"
                status = _monitor_status_label(monitor.get("current_status"))
                lines.append(f"{status} `{name}` — {mtype} — {target}")
        text = "\n".join(lines) if len(lines) > 1 else "Uptime monitor bulunamadı."
    except Exception as exc:
        text = f"❌ monitors hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


async def incidents_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.list_incidents()
        incidents = data.get("incidents", data) if isinstance(data, dict) else data
        lines = ["*Uptime Incidents*"]
        for incident in (incidents or [])[:15]:
            if isinstance(incident, dict):
                name = incident.get("monitor_name") or incident.get("monitor_id", "?")
                cause = incident.get("cause") or incident.get("type", "-")
                started = incident.get("started_at", "-")
                resolved = incident.get("resolved_at")
                state = "✅ resolved" if resolved else "🔴 open"
                lines.append(f"{state} `{name}` — {cause} — {started}")
        if len(lines) > 1:
            lines.append("")
            lines.append("_İpucu: daha fazla kayıt için /incidents 2_")
        text = "\n".join(lines) if len(lines) > 2 else "Açık incident bulunamadı."
    except Exception as exc:
        text = f"❌ incidents hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


async def alerts_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        text, keyboard = await _fetch_alerts_payload(client)
    except Exception as exc:
        text = f"❌ alerts hatası: {exc}"
        keyboard = None
    if update.message:
        await update.message.reply_text(
            text,
            parse_mode="Markdown",
            reply_markup=keyboard,
        )


async def handle_alerts_callback(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    query = update.callback_query
    if query is None or query.data is None:
        return
    if await _deny_unauthorized_callback(update, context):
        return

    client = get_client(context)
    data = query.data

    try:
        client.ensure_token()
        if data.startswith("alert:toggle:"):
            rule_id = int(data.rsplit(":", 1)[-1])
            updated = client.toggle_alert_rule(rule_id)
            enabled = updated.get("enabled", True)
            state = "etkinleştirildi" if enabled else "devre dışı bırakıldı"
            await query.answer(f"Kural {state}.")
        elif data == "alert:refresh":
            await query.answer("Liste yenileniyor…")
        else:
            await query.answer()
            return

        text, keyboard = await _fetch_alerts_payload(client)
    except Exception as exc:
        await query.answer(f"Hata: {exc}", show_alert=True)
        return

    if query.message:
        await query.message.edit_text(
            text,
            parse_mode="Markdown",
            reply_markup=keyboard,
        )


def register(application) -> None:
    application.add_handler(CommandHandler("monitors", monitors_cmd))
    application.add_handler(CommandHandler("incidents", incidents_cmd))
    application.add_handler(CommandHandler("alerts", alerts_cmd))
