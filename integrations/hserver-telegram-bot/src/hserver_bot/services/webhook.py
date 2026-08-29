"""Optional stdlib HTTP bridge for hserver alert delivery.

hserver-panel can push alerts via:

1. **CLI** (simplest, no daemon)::

       echo "disk full" | hserver-bot-notify --chat-id 123456789

   Or with an explicit message::

       hserver-bot-notify --chat-id 123456789 --message "disk full"

2. **HTTP webhook** (long-running listener)::

       python -m hserver_bot.services.webhook --port 8765

   POST ``/notify`` with JSON ``{"chat_id": 123, "message": "..."}``.
   Optional ``Authorization: Bearer <HSERVER_NOTIFY_SECRET>`` when
   ``HSERVER_NOTIFY_SECRET`` is set in the environment.

Environment:
    TELEGRAM_BOT_TOKEN — bot token (required)
    HSERVER_NOTIFY_SECRET — optional shared secret for POST auth
    HSERVER_NOTIFY_HOST — bind host (default 127.0.0.1)
    HSERVER_NOTIFY_PORT — bind port (default 8765)
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from hserver_bot.services.notifier import send_alert

logger = logging.getLogger(__name__)


def _resolve_bot_token(explicit: str | None = None) -> str:
    token = explicit or os.environ.get("TELEGRAM_BOT_TOKEN", "").strip()
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN is required")
    return token


def _check_auth(headers: Any, secret: str | None) -> bool:
    if not secret:
        return True
    return headers.get("Authorization", "") == f"Bearer {secret}"


def create_handler(
    bot_token: str,
    secret: str | None = None,
) -> type[BaseHTTPRequestHandler]:
    """Build a request handler bound to *bot_token* and optional *secret*."""

    class NotifyHandler(BaseHTTPRequestHandler):
        def log_message(self, fmt: str, *args: Any) -> None:
            logger.info("%s - %s", self.address_string(), fmt % args)

        def _json_response(self, status: int, body: dict) -> None:
            raw = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self) -> None:
            if self.path.rstrip("/") == "/health":
                self._json_response(200, {"status": "ok"})
                return
            self._json_response(404, {"error": "not found"})

        def do_POST(self) -> None:
            if self.path.rstrip("/") != "/notify":
                self._json_response(404, {"error": "not found"})
                return
            if not _check_auth(self.headers, secret):
                self._json_response(401, {"error": "unauthorized"})
                return

            length = int(self.headers.get("Content-Length", 0))
            body_bytes = self.rfile.read(length) if length else b""
            try:
                data = json.loads(body_bytes.decode() or "{}")
            except json.JSONDecodeError:
                self._json_response(400, {"error": "invalid json"})
                return

            chat_id = data.get("chat_id")
            message = data.get("message") or data.get("text")
            token = data.get("bot_token") or bot_token

            if chat_id is None or not message:
                self._json_response(400, {"error": "chat_id and message required"})
                return

            ok = send_alert(str(token), chat_id, str(message))
            if ok:
                self._json_response(200, {"ok": True})
            else:
                self._json_response(502, {"ok": False, "error": "delivery failed"})

    return NotifyHandler


def serve(
    host: str = "127.0.0.1",
    port: int = 8765,
    *,
    bot_token: str | None = None,
    secret: str | None = None,
) -> None:
    token = _resolve_bot_token(bot_token)
    resolved_secret = (
        secret
        if secret is not None
        else os.environ.get("HSERVER_NOTIFY_SECRET", "").strip() or None
    )
    handler = create_handler(token, resolved_secret)
    server = ThreadingHTTPServer((host, port), handler)
    logger.info("notify webhook listening on %s:%s", host, port)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        logger.info("shutdown requested")
    finally:
        server.server_close()


def main(argv: list[str] | None = None) -> int:
    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(message)s")
    parser = argparse.ArgumentParser(description="hserver Telegram notify webhook")
    parser.add_argument("--host", default=os.environ.get("HSERVER_NOTIFY_HOST", "127.0.0.1"))
    parser.add_argument(
        "--port",
        type=int,
        default=int(os.environ.get("HSERVER_NOTIFY_PORT", "8765")),
    )
    parser.add_argument("--bot-token", default=None)
    parser.add_argument("--secret", default=None)
    args = parser.parse_args(argv)
    try:
        serve(args.host, args.port, bot_token=args.bot_token, secret=args.secret)
    except ValueError as exc:
        logger.error("%s", exc)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
