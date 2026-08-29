"""Notification channels API."""

from __future__ import annotations


class NotifyMixin:
    def list_channels(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/notify/channels")  # type: ignore[attr-defined]

    def create_telegram_channel(self, name: str, bot_token: str, chat_id: int) -> dict:
        import json

        self.ensure_token()  # type: ignore[attr-defined]
        body = {
            "name": name,
            "type": "telegram",
            "enabled": True,
            "config": json.dumps({"botToken": bot_token, "chatId": chat_id}),
        }
        return self.post_json("/api/notify/channels", body)  # type: ignore[attr-defined]

    def test_channel(self, channel_id: int) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.post_json(f"/api/notify/channels/{channel_id}/test")  # type: ignore[attr-defined]
