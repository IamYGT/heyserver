"""CLI entry: pipe or pass alert text to a Telegram chat."""

from __future__ import annotations

import argparse
import os
import sys

from hserver_bot.services.notifier import send_alert


def _read_message(args: argparse.Namespace) -> str:
    if args.message:
        return args.message
    if not sys.stdin.isatty():
        return sys.stdin.read()
    raise SystemExit("provide --message or pipe text on stdin")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Send an hserver alert to a Telegram chat",
        epilog="Example: echo 'disk 95%%' | hserver-bot-notify --chat-id 123",
    )
    parser.add_argument("--chat-id", required=True, help="Telegram chat id")
    parser.add_argument("--message", "-m", help="Alert body (default: read stdin)")
    parser.add_argument(
        "--bot-token",
        default=None,
        help="Bot token (default: TELEGRAM_BOT_TOKEN env)",
    )
    parser.add_argument("--max-retries", type=int, default=3)
    args = parser.parse_args(argv)

    token = (args.bot_token or os.environ.get("TELEGRAM_BOT_TOKEN", "")).strip()
    if not token:
        print("error: TELEGRAM_BOT_TOKEN or --bot-token required", file=sys.stderr)
        return 1

    try:
        chat_id: int | str = int(args.chat_id)
    except ValueError:
        chat_id = args.chat_id

    message = _read_message(args).strip()
    if not message:
        print("error: empty message", file=sys.stderr)
        return 1

    ok = send_alert(token, chat_id, message, max_retries=args.max_retries)
    return 0 if ok else 2


if __name__ == "__main__":
    sys.exit(main())
