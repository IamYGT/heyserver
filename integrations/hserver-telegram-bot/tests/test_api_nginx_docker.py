"""Tests for NginxMixin and DockerMixin."""

from __future__ import annotations

import httpx
import pytest
import respx

from hserver_bot.api.client import HServerClient

BASE = "http://test"


def _authed_client() -> HServerClient:
    return HServerClient(BASE, "admin@test.com", "pass")


# ---------------------------------------------------------------------------
# NginxMixin
# ---------------------------------------------------------------------------


@respx.mock
def test_nginx_status():
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )
    respx.get(f"{BASE}/api/nginx/status").mock(
        return_value=httpx.Response(200, json={"active": True, "version": "1.24.0"})
    )
    client = _authed_client()
    data = client.nginx_status()
    assert data["active"] is True
    assert data["version"] == "1.24.0"


@respx.mock
def test_nginx_test():
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )
    respx.post(f"{BASE}/api/nginx/test").mock(
        return_value=httpx.Response(200, json={"ok": True, "output": "syntax is ok"})
    )
    client = _authed_client()
    result = client.nginx_test()
    assert result["ok"] is True
    assert "syntax" in result["output"]


@respx.mock
def test_nginx_status_propagates_http_error():
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )
    respx.get(f"{BASE}/api/nginx/status").mock(
        return_value=httpx.Response(502, json={"error": "bad gateway"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.nginx_status()


# ---------------------------------------------------------------------------
# DockerMixin
# ---------------------------------------------------------------------------


@respx.mock
def test_docker_status():
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )
    respx.get(f"{BASE}/api/docker/status").mock(
        return_value=httpx.Response(200, json={"running": True, "version": "24.0.5"})
    )
    client = _authed_client()
    data = client.docker_status()
    assert data["running"] is True


@respx.mock
def test_docker_containers():
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )
    respx.get(f"{BASE}/api/docker/containers").mock(
        return_value=httpx.Response(
            200,
            json=[
                {"name": "web", "state": "running"},
                {"name": "db", "state": "exited"},
            ],
        )
    )
    client = _authed_client()
    containers = client.docker_containers()
    assert isinstance(containers, list)
    assert len(containers) == 2
    assert containers[0]["name"] == "web"


@respx.mock
def test_docker_containers_empty():
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )
    respx.get(f"{BASE}/api/docker/containers").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _authed_client()
    result = client.docker_containers()
    assert result == []
