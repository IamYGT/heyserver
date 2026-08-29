import pytest
from unittest.mock import AsyncMock, MagicMock

from hserver_bot.handlers.common import is_authorized, chunk_text


def test_chunk_text_splits_long():
    parts = chunk_text("a" * 5000, limit=4000)
    assert len(parts) == 2
    assert len(parts[0]) == 4000


def test_is_authorized_when_no_allowlist():
    settings = MagicMock()
    settings.allowed_user_ids.return_value = set()
    update = MagicMock()
    update.effective_user = MagicMock(id=99)
    ctx = MagicMock()
    ctx.application.bot_data = {"settings": settings}
    assert is_authorized(update, ctx) is True


def test_is_authorized_with_allowlist():
    settings = MagicMock()
    settings.allowed_user_ids.return_value = {1, 2}
    update = MagicMock()
    update.effective_user = MagicMock(id=99)
    ctx = MagicMock()
    ctx.application.bot_data = {"settings": settings}
    assert is_authorized(update, ctx) is False
