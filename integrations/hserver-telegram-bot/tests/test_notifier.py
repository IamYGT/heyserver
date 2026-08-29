"""Tests for Telegram notification bridge."""

from __future__ import annotations

import json
import threading
from http.client import HTTPConnection
from http.server import ThreadingHTTPServer
from unittest.mock import patch

import httpx
import respx

from hserver_bot.cli_notify import main as cli_main
from hserver_bot.services.notifier import (
    TELEGRAM_MAX_TEXT,
    send_alert,
    send_message,
)
from hserver_bot.services.webhook import create_handler


BOT_TOKEN = "test-bot-token"
CHAT_ID = 123456789
API_URL = f"https://api.telegram.org/bot{BOT_TOKEN}/sendMessage"


@respx.mock
def test_send_alert_success():
    route = respx.post(API_URL).mock(return_value=httpx.Response(200, json={"ok": True}))
    assert send_alert(BOT_TOKEN, CHAT_ID, "disk 95%") is True
    assert route.called
    sent = json.loads(route.calls[0].request.content)
    assert sent == {"chat_id": CHAT_ID, "text": "disk 95%"}


@respx.mock
def test_send_alert_truncates_long_message():
    long_text = "x" * (TELEGRAM_MAX_TEXT + 500)
    route = respx.post(API_URL).mock(return_value=httpx.Response(200, json={"ok": True}))
    assert send_alert(BOT_TOKEN, CHAT_ID, long_text) is True
    sent = json.loads(route.calls[0].request.content)
    assert len(sent["text"]) == TELEGRAM_MAX_TEXT


@respx.mock
def test_send_alert_retries_on_503():
    route = respx.post(API_URL).mock(
        side_effect=[
            httpx.Response(503, text="unavailable"),
            httpx.Response(200, json={"ok": True}),
        ]
    )
    with patch("hserver_bot.services.notifier.time.sleep"):
        assert send_alert(BOT_TOKEN, CHAT_ID, "retry me", max_retries=2) is True
    assert len(route.calls) == 2


@respx.mock
def test_send_alert_retries_on_network_error():
    route = respx.post(API_URL).mock(
        side_effect=[
            httpx.ConnectError("connection refused"),
            httpx.Response(200, json={"ok": True}),
        ]
    )
    with patch("hserver_bot.services.notifier.time.sleep"):
        assert send_alert(BOT_TOKEN, CHAT_ID, "retry me", max_retries=2) is True
    assert len(route.calls) == 2


@respx.mock
def test_send_alert_fails_after_max_retries():
    route = respx.post(API_URL).mock(return_value=httpx.Response(503, text="unavailable"))
    with patch("hserver_bot.services.notifier.time.sleep"):
        assert send_alert(BOT_TOKEN, CHAT_ID, "fail", max_retries=2) is False
    assert len(route.calls) == 3


@respx.mock
def test_send_alert_no_retry_on_400():
    route = respx.post(API_URL).mock(return_value=httpx.Response(400, json={"ok": False}))
    with patch("hserver_bot.services.notifier.time.sleep") as sleep_mock:
        assert send_alert(BOT_TOKEN, CHAT_ID, "bad request") is False
    assert len(route.calls) == 1
    sleep_mock.assert_not_called()


@respx.mock
def test_send_message_alias():
    respx.post(API_URL).mock(return_value=httpx.Response(200, json={"ok": True}))
    assert send_message(BOT_TOKEN, CHAT_ID, "alias works") is True


@respx.mock
def test_cli_notify_with_message_arg(monkeypatch):
    respx.post(API_URL).mock(return_value=httpx.Response(200, json={"ok": True}))
    monkeypatch.setenv("TELEGRAM_BOT_TOKEN", BOT_TOKEN)
    assert cli_main(["--chat-id", str(CHAT_ID), "--message", "cli alert"]) == 0


@respx.mock
def test_cli_notify_reads_stdin(monkeypatch):
    respx.post(API_URL).mock(return_value=httpx.Response(200, json={"ok": True}))
    monkeypatch.setenv("TELEGRAM_BOT_TOKEN", BOT_TOKEN)
    monkeypatch.setattr(
        "hserver_bot.cli_notify.sys.stdin",
        type("R", (), {"isatty": lambda self: False, "read": lambda self: "piped\n"})(),
    )
    assert cli_main(["--chat-id", str(CHAT_ID)]) == 0


def test_cli_notify_missing_token(monkeypatch):
    monkeypatch.delenv("TELEGRAM_BOT_TOKEN", raising=False)
    assert cli_main(["--chat-id", str(CHAT_ID), "--message", "x"]) == 1


@respx.mock
def test_webhook_notify_success():
    respx.post(API_URL).mock(return_value=httpx.Response(200, json={"ok": True}))
    handler = create_handler(BOT_TOKEN)
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    host, port = server.server_address
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        conn = HTTPConnection(host, port, timeout=5)
        payload = json.dumps({"chat_id": CHAT_ID, "message": "webhook alert"})
        conn.request("POST", "/notify", body=payload, headers={"Content-Type": "application/json"})
        response = conn.getresponse()
        assert response.status == 200
        assert json.loads(response.read()) == {"ok": True}
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


@respx.mock
def test_webhook_requires_secret_when_configured():
    respx.post(API_URL).mock(return_value=httpx.Response(200, json={"ok": True}))
    handler = create_handler(BOT_TOKEN, secret="s3cret")
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    host, port = server.server_address
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        conn = HTTPConnection(host, port, timeout=5)
        payload = json.dumps({"chat_id": CHAT_ID, "message": "no auth"})
        conn.request("POST", "/notify", body=payload, headers={"Content-Type": "application/json"})
        response = conn.getresponse()
        assert response.status == 401
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


@respx.mock
def test_webhook_health_endpoint():
    handler = create_handler(BOT_TOKEN)
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    host, port = server.server_address
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        conn = HTTPConnection(host, port, timeout=5)
        conn.request("GET", "/health")
        response = conn.getresponse()
        assert response.status == 200
        assert json.loads(response.read()) == {"status": "ok"}
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)
