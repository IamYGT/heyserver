"""Push hserver alert events to registered Telegram chats."""

from __future__ import annotations

import logging
import time
from typing import Union

import httpx

logger = logging.getLogger(__name__)

TELEGRAM_MAX_TEXT = 4000
DEFAULT_MAX_RETRIES = 3
DEFAULT_BASE_DELAY = 0.5
RETRYABLE_STATUS_CODES = frozenset({429, 500, 502, 503, 504})

ChatId = Union[int, str]


def send_alert(
    bot_token: str,
    chat_id: ChatId,
    message: str,
    *,
    max_retries: int = DEFAULT_MAX_RETRIES,
    base_delay: float = DEFAULT_BASE_DELAY,
) -> bool:
    """Send *message* to *chat_id* via Telegram Bot API with exponential backoff retry."""
    text = message[:TELEGRAM_MAX_TEXT]
    url = f"https://api.telegram.org/bot{bot_token}/sendMessage"
    payload = {"chat_id": chat_id, "text": text}

    attempt = 0
    while True:
        attempt += 1
        try:
            with httpx.Client(timeout=30) as client:
                response = client.post(url, json=payload)
            if response.is_success:
                return True
            if response.status_code not in RETRYABLE_STATUS_CODES or attempt > max_retries:
                logger.warning(
                    "telegram send failed chat_id=%s status=%s body=%s",
                    chat_id,
                    response.status_code,
                    response.text[:200],
                )
                return False
            logger.info(
                "telegram send retry %s/%s chat_id=%s status=%s",
                attempt,
                max_retries,
                chat_id,
                response.status_code,
            )
        except (httpx.TimeoutException, httpx.NetworkError) as exc:
            if attempt > max_retries:
                logger.warning("telegram send failed chat_id=%s error=%s", chat_id, exc)
                return False
            logger.info(
                "telegram send retry %s/%s chat_id=%s error=%s",
                attempt,
                max_retries,
                chat_id,
                exc,
            )

        time.sleep(base_delay * (2 ** (attempt - 1)))


def send_message(bot_token: str, chat_id: ChatId, text: str) -> bool:
    """Backward-compatible alias for :func:`send_alert`."""
    return send_alert(bot_token, chat_id, text)
