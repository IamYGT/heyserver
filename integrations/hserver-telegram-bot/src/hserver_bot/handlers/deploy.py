"""Deploy target & history commands."""

from __future__ import annotations

from telegram import Update
from telegram.ext import CommandHandler, ContextTypes

from hserver_bot.handlers.common import deny_unauthorized, get_client


async def deploy_targets_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.list_targets()
        targets = data if isinstance(data, list) else data.get("targets", data)
        lines = ["*Deploy Targets*"]
        for t in (targets or [])[:15]:
            if isinstance(t, dict):
                active = "✅" if t.get("isActive", True) else "⏸"
                name = t.get("name", "?")
                tid = t.get("id", "?")
                branch = t.get("branch", "-")
                lines.append(f"{active} `{tid}` — {name} (`{branch}`)")
        text = "\n".join(lines) if len(lines) > 1 else "Deploy target bulunamadı."
    except Exception as exc:
        text = f"❌ deploy targets hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


async def deploy_history_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    client = get_client(context)
    try:
        client.ensure_token()
        data = client.deploy_history()
        runs = data if isinstance(data, list) else data.get("runs", data)
        lines = ["*Deploy History*"]
        for r in (runs or [])[:10]:
            if isinstance(r, dict):
                rid = r.get("id", "?")
                status = r.get("status", "?")
                target_id = r.get("targetId", "?")
                branch = r.get("branch", "-")
                icon = "✅" if status == "success" else "❌" if status == "failed" else "⏳"
                lines.append(f"{icon} run `{rid}` — target `{target_id}` ({branch}) — {status}")
        text = "\n".join(lines) if len(lines) > 1 else "Deploy geçmişi boş."
    except Exception as exc:
        text = f"❌ deploy history hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


async def deploy_run_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if await deny_unauthorized(update, context):
        return
    if not context.args:
        text = "Kullanım: `/deploy_run <target_id>`"
        if update.message:
            await update.message.reply_text(text, parse_mode="Markdown")
        return
    target_id = context.args[0]
    client = get_client(context)
    try:
        client.ensure_token()
        result = client.trigger_deploy(target_id)
        run_id = result.get("runId", "?") if isinstance(result, dict) else "?"
        message = result.get("message", "queued") if isinstance(result, dict) else "queued"
        text = f"🚀 Deploy tetiklendi\nTarget: `{target_id}`\nRun: `{run_id}`\n{message}"
    except Exception as exc:
        text = f"❌ deploy run hatası: {exc}"
    if update.message:
        await update.message.reply_text(text, parse_mode="Markdown")


def register(application) -> None:
    application.add_handler(CommandHandler("deploy_targets", deploy_targets_cmd))
    application.add_handler(CommandHandler("deploy_history", deploy_history_cmd))
