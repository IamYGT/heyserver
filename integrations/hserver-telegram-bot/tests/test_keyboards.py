"""Tests for inline keyboard builders."""

from __future__ import annotations

from hserver_bot.utils.keyboards import (
    CB_HOME,
    back_home_row,
    main_menu_keyboard,
    pagination_row,
)


def _collect_callback_data(keyboard) -> list[str]:
    return [btn.callback_data for row in keyboard.inline_keyboard for btn in row]


def _row_callback_data(row: list) -> list[str]:
    return [btn.callback_data for btn in row]


def test_main_menu_keyboard_callback_data_under_64_bytes():
    keyboard = main_menu_keyboard()
    for callback_data in _collect_callback_data(keyboard):
        assert len(callback_data.encode("utf-8")) <= 64


def test_main_menu_keyboard_has_expected_sections():
    keyboard = main_menu_keyboard()
    labels = [btn.text for row in keyboard.inline_keyboard for btn in row]
    assert "💚 Health" in labels
    assert "📦 Yedekler" in labels
    assert "📡 Monitörler" in labels
    assert "🔄 Yenile" in labels


def test_back_home_row_callback_under_64_bytes():
    row = back_home_row()
    assert len(row) == 1
    assert row[0].callback_data == CB_HOME
    assert len(row[0].callback_data.encode("utf-8")) <= 64


def test_pagination_row_single_page():
    row = pagination_row("backups", page=1, total_pages=1)
    callbacks = _row_callback_data(row)
    assert callbacks == ["backups:p:1"]
    assert all(len(cb.encode("utf-8")) <= 64 for cb in callbacks)


def test_pagination_row_first_page():
    row = pagination_row("backups", page=1, total_pages=3)
    callbacks = _row_callback_data(row)
    labels = [btn.text for btn in row]
    assert callbacks == ["backups:p:1", "backups:p:2"]
    assert labels == ["1/3", "▶️"]


def test_pagination_row_middle_page():
    row = pagination_row("disk", page=2, total_pages=4)
    callbacks = _row_callback_data(row)
    labels = [btn.text for btn in row]
    assert callbacks == ["disk:p:1", "disk:p:2", "disk:p:3"]
    assert labels == ["◀️", "2/4", "▶️"]


def test_pagination_row_last_page():
    row = pagination_row("disk", page=3, total_pages=3)
    callbacks = _row_callback_data(row)
    labels = [btn.text for btn in row]
    assert callbacks == ["disk:p:2", "disk:p:3"]
    assert labels == ["◀️", "3/3"]


def test_pagination_row_clamps_out_of_range_page():
    row = pagination_row("backups", page=99, total_pages=2)
    callbacks = _row_callback_data(row)
    labels = [btn.text for btn in row]
    assert callbacks == ["backups:p:1", "backups:p:2"]
    assert labels == ["◀️", "2/2"]
