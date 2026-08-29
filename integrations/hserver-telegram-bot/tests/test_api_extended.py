"""Tests for SSL, Databases and Cron API mixins."""

import pytest
import respx
import httpx

from hserver_bot.api.client import HServerClient

BASE = "http://test"


def _authed_client() -> HServerClient:
    return HServerClient(BASE, "admin@test.com", "pass")


def _mock_login():
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "tok-abc"})
    )


# ---------------------------------------------------------------------------
# SSL
# ---------------------------------------------------------------------------


@respx.mock
def test_ssl_certificates_returns_list():
    _mock_login()
    payload = {
        "certificates": [
            {"domain": "example.com", "status": "valid", "expiresAt": "2026-12-01"},
            {"domain": "api.example.com", "status": "expiring_soon", "expiresAt": "2026-07-01"},
        ]
    }
    respx.get(f"{BASE}/api/ssl/certificates").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _authed_client()
    data = client.ssl_certificates()
    assert isinstance(data, dict)
    certs = data["certificates"]
    assert len(certs) == 2
    assert certs[0]["domain"] == "example.com"


@respx.mock
def test_ssl_certificates_triggers_login():
    _mock_login()
    respx.get(f"{BASE}/api/ssl/certificates").mock(
        return_value=httpx.Response(200, json={"certificates": []})
    )
    client = _authed_client()
    assert client._token is None
    client.ssl_certificates()
    assert client._token == "tok-abc"


# ---------------------------------------------------------------------------
# Databases
# ---------------------------------------------------------------------------


@respx.mock
def test_list_databases_returns_list():
    _mock_login()
    payload = {
        "databases": [
            {"name": "app_db", "type": "mysql", "sizeHuman": "120 MB"},
            {"name": "analytics", "type": "postgresql", "sizeHuman": "4.2 GB"},
        ]
    }
    respx.get(f"{BASE}/api/databases").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _authed_client()
    data = client.list_databases()
    dbs = data["databases"]
    assert len(dbs) == 2
    assert dbs[0]["name"] == "app_db"
    assert dbs[1]["type"] == "postgresql"


@respx.mock
def test_list_databases_empty():
    _mock_login()
    respx.get(f"{BASE}/api/databases").mock(
        return_value=httpx.Response(200, json={"databases": []})
    )
    client = _authed_client()
    data = client.list_databases()
    assert data["databases"] == []


# ---------------------------------------------------------------------------
# Cron
# ---------------------------------------------------------------------------


@respx.mock
def test_list_cron_jobs_returns_list():
    _mock_login()
    payload = {
        "jobs": [
            {"name": "db-backup", "schedule": "0 3 * * *", "enabled": True},
            {"name": "log-rotate", "schedule": "0 0 * * 0", "enabled": False},
        ]
    }
    respx.get(f"{BASE}/api/cron").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _authed_client()
    data = client.list_cron_jobs()
    jobs = data["jobs"]
    assert len(jobs) == 2
    assert jobs[0]["name"] == "db-backup"
    assert jobs[1]["enabled"] is False


@respx.mock
def test_list_cron_jobs_triggers_login():
    _mock_login()
    respx.get(f"{BASE}/api/cron").mock(
        return_value=httpx.Response(200, json={"jobs": []})
    )
    client = _authed_client()
    assert client._token is None
    client.list_cron_jobs()
    assert client._token == "tok-abc"


# ---------------------------------------------------------------------------
# Error handling
# ---------------------------------------------------------------------------


@respx.mock
def test_ssl_certificates_raises_on_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/ssl/certificates").mock(
        return_value=httpx.Response(500, json={"error": "internal"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.ssl_certificates()


@respx.mock
def test_list_databases_raises_on_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/databases").mock(
        return_value=httpx.Response(403, json={"error": "forbidden"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.list_databases()


@respx.mock
def test_list_cron_jobs_raises_on_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/cron").mock(
        return_value=httpx.Response(404, json={"error": "not found"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.list_cron_jobs()
