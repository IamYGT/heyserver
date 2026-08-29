"""Tests for AlertsMixin."""

from __future__ import annotations

import httpx
import pytest
import respx

from hserver_bot.api.alerts import AlertsMixin
from conftest import BASE, make_api_test_client, mock_login

AlertsClient = make_api_test_client(AlertsMixin)


def _authed_client() -> AlertsClient:
    return AlertsClient(BASE, "admin@test.com", "pass")


def _mock_login() -> None:
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "tok-abc"})
    )


# ---------------------------------------------------------------------------
# list_monitors
# ---------------------------------------------------------------------------


@respx.mock
def test_list_monitors_returns_list():
    _mock_login()
    payload = [
        {
            "id": 1,
            "name": "example.com",
            "type": "http",
            "url": "https://example.com",
            "current_status": 1,
        },
        {
            "id": 2,
            "name": "api.example.com",
            "type": "http",
            "url": "https://api.example.com",
            "current_status": 0,
        },
    ]
    respx.get(f"{BASE}/api/uptime/monitors").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _authed_client()
    monitors = client.list_monitors()
    assert isinstance(monitors, list)
    assert len(monitors) == 2
    assert monitors[0]["name"] == "example.com"
    assert monitors[1]["current_status"] == 0


@respx.mock
def test_list_monitors_empty():
    _mock_login()
    respx.get(f"{BASE}/api/uptime/monitors").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _authed_client()
    assert client.list_monitors() == []


@respx.mock
def test_list_monitors_triggers_login():
    _mock_login()
    respx.get(f"{BASE}/api/uptime/monitors").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _authed_client()
    assert client._token is None
    client.list_monitors()
    assert client._token == "tok-abc"


# ---------------------------------------------------------------------------
# list_incidents
# ---------------------------------------------------------------------------


@respx.mock
def test_list_incidents_returns_list():
    _mock_login()
    payload = [
        {
            "id": 10,
            "monitor_id": 1,
            "monitor_name": "example.com",
            "type": "down",
            "cause": "HTTP 503",
            "started_at": "2026-06-17T10:00:00Z",
        },
        {
            "id": 9,
            "monitor_id": 2,
            "monitor_name": "api.example.com",
            "type": "down",
            "started_at": "2026-06-16T08:00:00Z",
            "resolved_at": "2026-06-16T09:30:00Z",
        },
    ]
    respx.get(f"{BASE}/api/uptime/incidents").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _authed_client()
    incidents = client.list_incidents()
    assert isinstance(incidents, list)
    assert len(incidents) == 2
    assert incidents[0]["monitor_name"] == "example.com"
    assert incidents[1]["resolved_at"] == "2026-06-16T09:30:00Z"


@respx.mock
def test_list_incidents_empty():
    _mock_login()
    respx.get(f"{BASE}/api/uptime/incidents").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _authed_client()
    assert client.list_incidents() == []


# ---------------------------------------------------------------------------
# list_alert_rules
# ---------------------------------------------------------------------------


@respx.mock
def test_list_alert_rules_returns_list():
    _mock_login()
    payload = [
        {
            "id": 1,
            "name": "CPU high",
            "type": "cpu_usage",
            "threshold": 90.0,
            "enabled": True,
        },
        {
            "id": 2,
            "name": "Disk full",
            "type": "disk_usage",
            "threshold": 85.0,
            "enabled": False,
        },
    ]
    respx.get(f"{BASE}/api/notify/rules").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _authed_client()
    rules = client.list_alert_rules()
    assert isinstance(rules, list)
    assert len(rules) == 2
    assert rules[0]["name"] == "CPU high"
    assert rules[1]["enabled"] is False


@respx.mock
def test_list_alert_rules_triggers_login():
    _mock_login()
    respx.get(f"{BASE}/api/notify/rules").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _authed_client()
    assert client._token is None
    client.list_alert_rules()
    assert client._token == "tok-abc"


# ---------------------------------------------------------------------------
# Error handling
# ---------------------------------------------------------------------------


@respx.mock
def test_list_monitors_raises_on_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/uptime/monitors").mock(
        return_value=httpx.Response(503, json={"error": "uptime engine not initialized"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.list_monitors()


@respx.mock
def test_list_incidents_raises_on_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/uptime/incidents").mock(
        return_value=httpx.Response(500, json={"error": "internal"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.list_incidents()


@respx.mock
def test_list_alert_rules_raises_on_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/notify/rules").mock(
        return_value=httpx.Response(404, json={"error": "not found"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.list_alert_rules()
