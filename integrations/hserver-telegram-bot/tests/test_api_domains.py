"""Tests for DomainsMixin."""

from __future__ import annotations

import httpx
import pytest
import respx

from hserver_bot.api.domains import DomainsMixin
from conftest import BASE, make_api_test_client, mock_login

DomainsClient = make_api_test_client(DomainsMixin)


def _authed_client() -> DomainsClient:
    return DomainsClient(BASE, "admin@test.com", "pass")


def _mock_login() -> None:
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "tok-abc"})
    )


@respx.mock
def test_list_domains_returns_list():
    _mock_login()
    payload = {
        "domains": [
            {"name": "example.com", "type": "php", "sslEnabled": True, "isActive": True},
            {"name": "api.example.com", "type": "proxy", "sslEnabled": False, "isActive": True},
        ]
    }
    respx.get(f"{BASE}/api/domains").mock(return_value=httpx.Response(200, json=payload))
    client = _authed_client()
    data = client.list_domains()
    domains = data["domains"]
    assert len(domains) == 2
    assert domains[0]["name"] == "example.com"
    assert domains[1]["type"] == "proxy"


@respx.mock
def test_list_domains_empty():
    _mock_login()
    respx.get(f"{BASE}/api/domains").mock(
        return_value=httpx.Response(200, json={"domains": []})
    )
    client = _authed_client()
    data = client.list_domains()
    assert data["domains"] == []


@respx.mock
def test_list_domains_triggers_login():
    _mock_login()
    respx.get(f"{BASE}/api/domains").mock(
        return_value=httpx.Response(200, json={"domains": []})
    )
    client = _authed_client()
    assert client._token is None
    client.list_domains()
    assert client._token == "tok-abc"


@respx.mock
def test_get_domain_returns_detail():
    _mock_login()
    payload = {
        "name": "example.com",
        "type": "php",
        "root": "/var/www/vhosts/example.com",
        "sslEnabled": True,
        "isActive": True,
        "serverNames": ["example.com", "www.example.com"],
        "sslDaysRemaining": 45,
    }
    respx.get(f"{BASE}/api/domains/example.com").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _authed_client()
    detail = client.get_domain("example.com")
    assert detail["name"] == "example.com"
    assert detail["type"] == "php"
    assert detail["sslDaysRemaining"] == 45
    assert "www.example.com" in detail["serverNames"]


@respx.mock
def test_get_domain_raises_on_not_found():
    _mock_login()
    respx.get(f"{BASE}/api/domains/missing.example.com").mock(
        return_value=httpx.Response(404, json={"error": "domain not found"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.get_domain("missing.example.com")


@respx.mock
def test_list_domains_raises_on_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/domains").mock(
        return_value=httpx.Response(500, json={"error": "internal"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.list_domains()
