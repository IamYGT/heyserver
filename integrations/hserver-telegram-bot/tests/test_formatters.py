"""Tests for formatter utilities."""

import pytest

from hserver_bot.utils.formatters import (
    bold,
    code,
    escape_html,
    escape_md,
    format_bytes,
    format_disk_bar,
    format_health_summary,
    format_kv_card,
    format_table,
    format_uptime,
    pre,
    status_badge,
    truncate_telegram,
)


def test_format_bytes_zero():
    assert format_bytes(0) == "0 B"


def test_format_bytes_small_values():
    assert format_bytes(512) == "512 B"
    assert format_bytes(1023) == "1023 B"


def test_format_bytes_kilobytes_and_above():
    assert format_bytes(1024) == "1.0 KB"
    assert format_bytes(1536) == "1.5 KB"
    assert format_bytes(1024**2) == "1.0 MB"
    assert format_bytes(1024**3) == "1.0 GB"


def test_format_bytes_negative_uses_absolute_value():
    assert format_bytes(-2048) == "2.0 KB"


def test_format_table_basic():
    table = format_table(
        [["web", "running"], ["db", "stopped"]],
        ["service", "status"],
    )
    assert table == (
        "service | status \n"
        "--------+--------\n"
        "web     | running\n"
        "db      | stopped"
    )


def test_format_table_empty_rows():
    table = format_table([], ["name", "value"])
    assert table == "name | value\n-----+------"


def test_format_table_rejects_empty_headers():
    with pytest.raises(ValueError, match="headers must not be empty"):
        format_table([["a"]], [])


def test_format_table_rejects_mismatched_row_width():
    with pytest.raises(ValueError, match="same number of columns"):
        format_table([["only-one"]], ["a", "b"])


def test_truncate_telegram_short_text_unchanged():
    assert truncate_telegram("hello") == "hello"


def test_truncate_telegram_at_limit():
    text = "a" * 4000
    assert truncate_telegram(text) == text


def test_truncate_telegram_adds_ellipsis():
    text = "a" * 4005
    result = truncate_telegram(text)
    assert len(result) == 4000
    assert result.endswith("…")
    assert result.startswith("a")


def test_truncate_telegram_custom_max():
    assert truncate_telegram("abcdef", max=4) == "abc…"


def test_truncate_telegram_rejects_invalid_max():
    with pytest.raises(ValueError, match="max must be at least 1"):
        truncate_telegram("x", max=0)


def test_escape_md_special_characters():
    assert escape_md("_*[]`\\") == "\\_\\*\\[\\]\\`\\\\"


def test_escape_md_plain_text_unchanged():
    assert escape_md("plain text 123") == "plain text 123"


def test_escape_html_special_characters():
    assert escape_html("a & b <tag>") == "a &amp; b &lt;tag&gt;"


def test_escape_html_plain_text_unchanged():
    assert escape_html("plain text 123") == "plain text 123"


def test_bold_wraps_and_escapes():
    assert bold("disk <95%") == "<b>disk &lt;95%</b>"


def test_code_wraps_and_escapes():
    assert code("a & b") == "<code>a &amp; b</code>"


def test_pre_wraps_and_escapes():
    assert pre("line\n<two>") == "<pre>line\n&lt;two&gt;</pre>"


def test_status_badge_ok_and_fail():
    assert status_badge(True) == "✅"
    assert status_badge(False) == "❌"


def test_format_kv_card_renders_title_and_pairs():
    card = format_kv_card("Server", [("Host", "h1"), ("IP", "10.0.0.1")])
    assert card == (
        "<b>Server</b>\n"
        "<b>Host</b>: h1\n"
        "<b>IP</b>: 10.0.0.1"
    )


def test_format_kv_card_escapes_values():
    card = format_kv_card("Alert", [("Msg", "disk > 90%")])
    assert card == "<b>Alert</b>\n<b>Msg</b>: disk &gt; 90%"


def test_format_uptime_days_hours_minutes():
    assert format_uptime(90061) == "1d 1h 1m"


def test_format_uptime_minutes_only():
    assert format_uptime(45) == "0m"


def test_format_uptime_hours_and_minutes():
    assert format_uptime(3660) == "1h 1m"


def test_format_disk_bar_half_full():
    assert format_disk_bar(50, width=10) == "█████░░░░░ 50%"


def test_format_disk_bar_clamps_percentage():
    assert format_disk_bar(150, width=4) == "████ 100%"
    assert format_disk_bar(-5, width=4) == "░░░░ 0%"


def test_format_disk_bar_rejects_invalid_width():
    with pytest.raises(ValueError, match="width must be at least 1"):
        format_disk_bar(50, width=0)


def test_format_health_summary_full_payload():
    summary = format_health_summary(
        {
            "status": "ok",
            "version": "1.2.3",
            "uptime": 3661,
            "build_commit": "abc123",
        }
    )
    assert summary == (
        "<b>hserver Health</b>\n"
        "<b>Status</b>: ✅ ok\n"
        "<b>Version</b>: 1.2.3\n"
        "<b>Uptime</b>: 1h 1m\n"
        "<b>Commit</b>: abc123"
    )


def test_format_health_summary_degraded_status():
    summary = format_health_summary({"status": "degraded"})
    assert "<b>Status</b>: ❌ degraded" in summary
    assert "<b>Version</b>" not in summary
