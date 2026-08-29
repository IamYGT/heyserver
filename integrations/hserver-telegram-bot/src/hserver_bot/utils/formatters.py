"""Text and data formatting helpers for Telegram messages."""

from __future__ import annotations

_MD_ESCAPE_CHARS = ("\\", "_", "*", "`", "[", "]")
_HTML_ESCAPE_CHARS = (("&", "&amp;"), ("<", "&lt;"), (">", "&gt;"))


def format_bytes(n: int | float) -> str:
    """Return a human-readable byte size using binary units (KiB-style labels)."""
    if n < 0:
        n = abs(n)

    units = ("B", "KB", "MB", "GB", "TB", "PB")
    size = float(n)

    for index, unit in enumerate(units):
        if size < 1024 or index == len(units) - 1:
            if unit == "B":
                return f"{int(size)} {unit}"
            return f"{size:.1f} {unit}"
        size /= 1024

    return f"{size:.1f} PB"


def format_table(rows: list[list[object]], headers: list[str]) -> str:
    """Format rows and headers as a fixed-width pipe-separated table."""
    if len(headers) == 0:
        raise ValueError("headers must not be empty")

    column_count = len(headers)
    widths = [len(header) for header in headers]

    for row in rows:
        if len(row) != column_count:
            raise ValueError("each row must have the same number of columns as headers")
        for index, cell in enumerate(row):
            widths[index] = max(widths[index], len(str(cell)))

    def format_row(cells: list[object]) -> str:
        return " | ".join(str(cell).ljust(widths[index]) for index, cell in enumerate(cells))

    separator = "-+-".join("-" * width for width in widths)
    lines = [format_row(headers), separator]
    if rows:
        lines.extend(format_row(row) for row in rows)
    return "\n".join(lines)


def truncate_telegram(text: str, max: int = 4000) -> str:
    """Truncate text to fit Telegram message limits, preserving an ellipsis suffix."""
    if max < 1:
        raise ValueError("max must be at least 1")
    if len(text) <= max:
        return text
    if max == 1:
        return "…"
    return text[: max - 1] + "…"


def escape_md(text: str) -> str:
    """Escape characters that are special in Telegram legacy Markdown."""
    escaped = text
    for char in _MD_ESCAPE_CHARS:
        escaped = escaped.replace(char, f"\\{char}")
    return escaped


def escape_html(text: str) -> str:
    """Escape characters that are special in Telegram HTML parse mode."""
    escaped = text
    for char, replacement in _HTML_ESCAPE_CHARS:
        escaped = escaped.replace(char, replacement)
    return escaped


def bold(text: str) -> str:
    """Wrap text in an HTML bold tag."""
    return f"<b>{escape_html(text)}</b>"


def code(text: str) -> str:
    """Wrap text in an HTML inline code tag."""
    return f"<code>{escape_html(text)}</code>"


def pre(text: str) -> str:
    """Wrap text in an HTML preformatted block tag."""
    return f"<pre>{escape_html(text)}</pre>"


def status_badge(ok: bool) -> str:
    """Return a check or cross emoji for boolean status."""
    return "✅" if ok else "❌"


def format_kv_card(title: str, pairs: list[tuple[str, str]]) -> str:
    """Format a title and key-value pairs as an HTML card."""
    lines = [bold(title)]
    for key, value in pairs:
        lines.append(f"{bold(key)}: {escape_html(value)}")
    return "\n".join(lines)


def format_uptime(seconds: int) -> str:
    """Format uptime seconds as a compact day/hour/minute string."""
    if seconds < 0:
        seconds = 0

    days = seconds // 86400
    hours = (seconds % 86400) // 3600
    minutes = (seconds % 3600) // 60

    parts: list[str] = []
    if days > 0:
        parts.append(f"{days}d")
    if hours > 0:
        parts.append(f"{hours}h")
    parts.append(f"{minutes}m")
    return " ".join(parts)


def format_disk_bar(used_pct: float, width: int = 10) -> str:
    """Render a text progress bar for disk usage percentage."""
    if width < 1:
        raise ValueError("width must be at least 1")

    clamped = max(0.0, min(100.0, used_pct))
    filled = round(clamped / 100 * width)
    filled = max(0, min(width, filled))
    bar = "█" * filled + "░" * (width - filled)
    return f"{bar} {clamped:.0f}%"


def format_health_summary(data: dict) -> str:
    """Format an hserver /api/health response as an HTML summary card."""
    status = str(data.get("status", "unknown"))
    is_ok = status.lower() == "ok"

    pairs: list[tuple[str, str]] = [
        ("Status", f"{status_badge(is_ok)} {status}"),
    ]

    if version := data.get("version"):
        pairs.append(("Version", str(version)))

    if uptime := data.get("uptime"):
        pairs.append(("Uptime", format_uptime(int(uptime))))

    if build_commit := data.get("build_commit"):
        pairs.append(("Commit", str(build_commit)))

    return format_kv_card("hserver Health", pairs)
