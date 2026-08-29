"""Tests for pagination helpers."""

from __future__ import annotations

import pytest

from hserver_bot.utils.pagination import (
    PAGE_SIZE,
    build_page_keyboard,
    decode_page_cb,
    encode_page_cb,
    paginate_items,
)


def test_encode_decode_roundtrip():
    data = encode_page_cb("backups", 2)
    assert decode_page_cb(data) == ("backups", 2)


def test_encode_page_cb_under_64_bytes():
    data = encode_page_cb("backups", 999)
    assert data == "page:backups:999"
    assert len(data.encode("utf-8")) <= 64


def test_decode_page_cb_invalid_prefix():
    with pytest.raises(ValueError, match="invalid page callback"):
        decode_page_cb("dash:backups:1")


def test_decode_page_cb_invalid_page_number():
    with pytest.raises(ValueError, match="invalid page number"):
        decode_page_cb("page:backups:abc")


def test_paginate_items_empty_list():
    page_items, page, total_pages = paginate_items([], page=1)
    assert page_items == []
    assert page == 1
    assert total_pages == 1


def test_paginate_items_single_page():
    items = list(range(3))
    page_items, page, total_pages = paginate_items(items, page=1, page_size=PAGE_SIZE)
    assert page_items == [0, 1, 2]
    assert page == 1
    assert total_pages == 1


def test_paginate_items_second_page():
    items = list(range(12))
    page_items, page, total_pages = paginate_items(items, page=2, page_size=5)
    assert page_items == [5, 6, 7, 8, 9]
    assert page == 2
    assert total_pages == 3


def test_paginate_items_clamps_high_page():
    items = list(range(6))
    page_items, page, total_pages = paginate_items(items, page=99, page_size=5)
    assert page_items == [5]
    assert page == 2
    assert total_pages == 2


def test_build_page_keyboard_none_for_single_page():
    assert build_page_keyboard("backups", page=1, total_pages=1) is None


def test_build_page_keyboard_has_navigation_buttons():
    keyboard = build_page_keyboard("backups", page=2, total_pages=3)
    assert keyboard is not None
    nav_row = keyboard.inline_keyboard[0]
    callbacks = [btn.callback_data for btn in nav_row]
    assert callbacks == ["page:backups:1", "page:backups:2", "page:backups:3"]
    assert all(len(cb.encode("utf-8")) <= 64 for cb in callbacks)
