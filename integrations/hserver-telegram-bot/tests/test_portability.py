"""Portability contracts for the packaged integration."""

from pathlib import Path


ROOT = Path(__file__).parents[1]


def test_systemd_unit_uses_configured_paths_and_unprivileged_identity():
    unit = (ROOT / "deploy" / "hserver-telegram-bot.service").read_text(encoding="utf-8")

    assert "EnvironmentFile=/etc/hserver/hserver-telegram-bot.env" in unit
    assert "HSERVER_BOT_HOME" in unit
    assert "User=hserver-telegram-bot" in unit
    assert "Group=hserver-telegram-bot" in unit
    assert "User=" + "root" not in unit
