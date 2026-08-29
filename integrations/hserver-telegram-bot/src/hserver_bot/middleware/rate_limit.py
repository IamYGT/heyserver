"""In-memory sliding-window rate limiting for Telegram handlers."""

from __future__ import annotations

import math
import time
from collections import defaultdict
from collections.abc import Awaitable, Callable
from typing import Any

from telegram import Update
from telegram.ext import Application, ContextTypes

DEFAULT_MAX_REQUESTS = 20
DEFAULT_WINDOW_SECONDS = 60

Handler = Callable[[Update, ContextTypes.DEFAULT_TYPE], Awaitable[Any]]


class RateLimiter:
    """Sliding-window rate limiter keyed by Telegram user id."""

    def __init__(
        self,
        max_requests: int = DEFAULT_MAX_REQUESTS,
        window_seconds: float = DEFAULT_WINDOW_SECONDS,
    ) -> None:
        self.max_requests = max_requests
        self.window_seconds = window_seconds
        self._hits: dict[int, list[float]] = defaultdict(list)

    def _prune(self, user_id: int, now: float) -> list[float]:
        cutoff = now - self.window_seconds
        hits = [timestamp for timestamp in self._hits[user_id] if timestamp > cutoff]
        self._hits[user_id] = hits
        return hits

    def check(self, user_id: int) -> bool:
        """Return True if the request is allowed, False when rate limited."""
        now = time.monotonic()
        hits = self._prune(user_id, now)
        if len(hits) >= self.max_requests:
            return False
        hits.append(now)
        self._hits[user_id] = hits
        return True

    def remaining(self, user_id: int) -> int:
        """Return how many requests the user can still make in the current window."""
        now = time.monotonic()
        hits = self._prune(user_id, now)
        return max(0, self.max_requests - len(hits))

    def wait_seconds(self, user_id: int) -> int:
        """Seconds until the oldest hit leaves the window (minimum 1 when limited)."""
        now = time.monotonic()
        hits = self._prune(user_id, now)
        if len(hits) < self.max_requests:
            return 0
        oldest = min(hits)
        wait = self.window_seconds - (now - oldest)
        return max(1, math.ceil(wait))


def attach_rate_limiter(application: Application) -> RateLimiter:
    """Create a shared limiter and store it on ``application.bot_data``."""
    limiter = RateLimiter()
    application.bot_data["rate_limiter"] = limiter
    return limiter


def get_rate_limiter(context: ContextTypes.DEFAULT_TYPE) -> RateLimiter | None:
    limiter = context.application.bot_data.get("rate_limiter")
    return limiter if isinstance(limiter, RateLimiter) else None


async def rate_limit_middleware(
    update: Update,
    context: ContextTypes.DEFAULT_TYPE,
    next_handler: Handler,
) -> Any:
    """Pre-handler middleware: block updates that exceed the per-user rate limit."""
    limiter = get_rate_limiter(context)
    if limiter is None:
        return await next_handler(update, context)

    user = update.effective_user
    if user is None:
        return await next_handler(update, context)

    if limiter.check(user.id):
        return await next_handler(update, context)

    wait = limiter.wait_seconds(user.id)
    if update.message:
        await update.message.reply_text(f"⏳ Çok hızlı — {wait} sn bekleyin")
    return None


def rate_limit_wrapper(handler: Handler) -> Handler:
    """Wrap a handler so rate limiting runs before the handler body."""

    async def wrapped(update: Update, context: ContextTypes.DEFAULT_TYPE) -> Any:
        return await rate_limit_middleware(update, context, handler)

    return wrapped
