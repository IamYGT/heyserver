"""Tests for DeployMixin."""

from __future__ import annotations

import httpx
import pytest
import respx

from hserver_bot.api.auth import AuthMixin
from hserver_bot.api.deploy import DeployMixin

BASE = "http://test"


class _DeployTestClient(AuthMixin, DeployMixin):
    def __init__(self, base_url: str, email: str, password: str, timeout: float = 60.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.email = email
        self.password = password
        self.timeout = timeout
        self._token: str | None = None

    def _client(self) -> httpx.Client:
        headers = {}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        return httpx.Client(base_url=self.base_url, headers=headers, timeout=self.timeout)

    def _request(self, method: str, path: str, **kwargs) -> httpx.Response:
        with self._client() as client:
            response = client.request(method, path, **kwargs)
            response.raise_for_status()
            return response

    def get_json(self, path: str) -> dict | list:
        return self._request("GET", path).json()

    def post_json(self, path: str, body: dict | None = None) -> dict | list:
        return self._request("POST", path, json=body or {}).json()


def _authed_client() -> _DeployTestClient:
    return _DeployTestClient(BASE, "admin@test.com", "pass")


def _mock_login():
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "tok-abc"})
    )


# ---------------------------------------------------------------------------
# list_targets
# ---------------------------------------------------------------------------


@respx.mock
def test_list_targets_returns_list():
    _mock_login()
    payload = [
        {"id": 1, "name": "landing", "branch": "main", "isActive": True},
        {"id": 2, "name": "api", "branch": "develop", "isActive": False},
    ]
    respx.get(f"{BASE}/api/deploy/targets").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _authed_client()
    data = client.list_targets()
    assert isinstance(data, list)
    assert len(data) == 2
    assert data[0]["name"] == "landing"
    assert data[1]["isActive"] is False


@respx.mock
def test_list_targets_empty():
    _mock_login()
    respx.get(f"{BASE}/api/deploy/targets").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _authed_client()
    assert client.list_targets() == []


@respx.mock
def test_list_targets_triggers_login():
    _mock_login()
    respx.get(f"{BASE}/api/deploy/targets").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _authed_client()
    assert client._token is None
    client.list_targets()
    assert client._token == "tok-abc"


# ---------------------------------------------------------------------------
# deploy_history
# ---------------------------------------------------------------------------


@respx.mock
def test_deploy_history_returns_list():
    _mock_login()
    payload = [
        {
            "id": 10,
            "targetId": 1,
            "status": "success",
            "branch": "main",
            "trigger": "manual",
        },
        {
            "id": 9,
            "targetId": 2,
            "status": "failed",
            "branch": "develop",
            "trigger": "webhook",
        },
    ]
    route = respx.get(f"{BASE}/api/deploy/history").mock(
        return_value=httpx.Response(200, json=payload)
    )
    client = _authed_client()
    data = client.deploy_history(limit=10)
    assert isinstance(data, list)
    assert len(data) == 2
    assert data[0]["status"] == "success"
    assert route.calls[0].request.url.params["limit"] == "10"


@respx.mock
def test_deploy_history_custom_limit():
    _mock_login()
    route = respx.get(f"{BASE}/api/deploy/history").mock(
        return_value=httpx.Response(200, json=[])
    )
    client = _authed_client()
    client.deploy_history(limit=25)
    assert route.calls[0].request.url.params["limit"] == "25"


# ---------------------------------------------------------------------------
# trigger_deploy
# ---------------------------------------------------------------------------


@respx.mock
def test_trigger_deploy_posts_to_manual_endpoint():
    _mock_login()
    route = respx.post(f"{BASE}/api/deploy/manual/3").mock(
        return_value=httpx.Response(202, json={"message": "deployment queued", "runId": 42})
    )
    client = _authed_client()
    result = client.trigger_deploy(3)
    assert route.called
    assert result["runId"] == 42
    assert result["message"] == "deployment queued"


@respx.mock
def test_trigger_deploy_accepts_string_id():
    _mock_login()
    route = respx.post(f"{BASE}/api/deploy/manual/landing-prod").mock(
        return_value=httpx.Response(202, json={"message": "deployment queued", "runId": 7})
    )
    client = _authed_client()
    result = client.trigger_deploy("landing-prod")
    assert route.called
    assert result["runId"] == 7


# ---------------------------------------------------------------------------
# Error handling
# ---------------------------------------------------------------------------


@respx.mock
def test_list_targets_raises_on_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/deploy/targets").mock(
        return_value=httpx.Response(500, json={"error": "internal"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.list_targets()


@respx.mock
def test_deploy_history_raises_on_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/deploy/history").mock(
        return_value=httpx.Response(403, json={"error": "forbidden"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.deploy_history()


@respx.mock
def test_trigger_deploy_raises_on_http_error():
    _mock_login()
    respx.post(f"{BASE}/api/deploy/manual/99").mock(
        return_value=httpx.Response(404, json={"error": "target not found"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.trigger_deploy(99)
