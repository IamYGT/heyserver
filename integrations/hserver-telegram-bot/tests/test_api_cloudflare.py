"""Tests for CloudflareMixin."""

from __future__ import annotations

import httpx
import pytest
import respx

from hserver_bot.api.cloudflare import CloudflareMixin
from conftest import BASE, make_api_test_client, mock_login

CloudflareTestClient = make_api_test_client(CloudflareMixin)


def _authed_client() -> CloudflareTestClient:
    return CloudflareTestClient(BASE, "admin@test.com", "pass")


def _mock_login() -> None:
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )


# ---------------------------------------------------------------------------
# CloudflareMixin
# ---------------------------------------------------------------------------


@respx.mock
def test_list_zones():
    _mock_login()
    respx.get(f"{BASE}/api/cloudflare/zones").mock(
        return_value=httpx.Response(
            200,
            json=[
                {
                    "id": "zone-abc123",
                    "name": "example.com",
                    "status": "active",
                    "plan": {"id": "0", "name": "Free Website"},
                },
                {
                    "id": "zone-def456",
                    "name": "api.example.com",
                    "status": "active",
                    "plan": {"id": "1", "name": "Pro"},
                },
            ],
        )
    )
    client = _authed_client()
    zones = client.list_zones()
    assert isinstance(zones, list)
    assert len(zones) == 2
    assert zones[0]["name"] == "example.com"
    assert zones[1]["id"] == "zone-def456"


@respx.mock
def test_get_zone():
    _mock_login()
    zone_id = "zone-abc123"
    respx.get(f"{BASE}/api/cloudflare/zones/{zone_id}").mock(
        return_value=httpx.Response(
            200,
            json={
                "id": zone_id,
                "name": "example.com",
                "status": "active",
                "plan": {"id": "0", "name": "Free Website"},
                "name_servers": ["ns1.cloudflare.com", "ns2.cloudflare.com"],
            },
        )
    )
    client = _authed_client()
    zone = client.get_zone(zone_id)
    assert isinstance(zone, dict)
    assert zone["id"] == zone_id
    assert zone["name"] == "example.com"
    assert len(zone["name_servers"]) == 2


@respx.mock
def test_list_records():
    _mock_login()
    zone_id = "zone-abc123"
    respx.get(f"{BASE}/api/cloudflare/zones/{zone_id}/records").mock(
        return_value=httpx.Response(
            200,
            json=[
                {
                    "id": "rec-1",
                    "type": "A",
                    "name": "example.com",
                    "content": "192.0.2.1",
                    "ttl": 1,
                    "proxied": True,
                },
                {
                    "id": "rec-2",
                    "type": "MX",
                    "name": "example.com",
                    "content": "mail.example.com",
                    "ttl": 3600,
                    "proxied": False,
                    "priority": 10,
                },
            ],
        )
    )
    client = _authed_client()
    records = client.list_records(zone_id)
    assert isinstance(records, list)
    assert len(records) == 2
    assert records[0]["type"] == "A"
    assert records[1]["priority"] == 10


@respx.mock
def test_purge_zone():
    _mock_login()
    zone_id = "zone-abc123"
    respx.post(f"{BASE}/api/cloudflare/zones/{zone_id}/purge").mock(
        return_value=httpx.Response(200, json={"status": "purged"})
    )
    client = _authed_client()
    result = client.purge_zone(zone_id)
    assert isinstance(result, dict)
    assert result["status"] == "purged"


@respx.mock
def test_list_zones_triggers_login():
    _mock_login()
    respx.get(f"{BASE}/api/cloudflare/zones").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _authed_client()
    assert client._token is None
    client.list_zones()
    assert client._token == "jwt"


@respx.mock
def test_purge_zone_propagates_http_error():
    _mock_login()
    zone_id = "zone-abc123"
    respx.post(f"{BASE}/api/cloudflare/zones/{zone_id}/purge").mock(
        return_value=httpx.Response(502, json={"error": "bad gateway"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.purge_zone(zone_id)
