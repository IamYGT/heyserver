"""Tests for in-memory rate limiting."""

from __future__ import annotations

from hserver_bot.middleware.rate_limit import RateLimiter


def test_rate_limiter_allows_first_20_requests():
    limiter = RateLimiter(max_requests=20, window_seconds=60)
    user_id = 42

    for _ in range(20):
        assert limiter.check(user_id) is True


def test_rate_limiter_blocks_21st_request():
    limiter = RateLimiter(max_requests=20, window_seconds=60)
    user_id = 42

    for _ in range(20):
        limiter.check(user_id)

    assert limiter.check(user_id) is False


def test_rate_limiter_remaining_decrements():
    limiter = RateLimiter(max_requests=20, window_seconds=60)
    user_id = 42

    assert limiter.remaining(user_id) == 20
    limiter.check(user_id)
    assert limiter.remaining(user_id) == 19


def test_rate_limiter_wait_seconds_when_limited():
    limiter = RateLimiter(max_requests=2, window_seconds=60)
    user_id = 7

    limiter.check(user_id)
    limiter.check(user_id)
    assert limiter.check(user_id) is False
    assert limiter.wait_seconds(user_id) >= 1


def test_rate_limiter_isolated_per_user():
    limiter = RateLimiter(max_requests=1, window_seconds=60)

    assert limiter.check(1) is True
    assert limiter.check(1) is False
    assert limiter.check(2) is True
