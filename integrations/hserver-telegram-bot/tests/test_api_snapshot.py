"""Tests for SnapshotMixin."""

from __future__ import annotations

import httpx
import pytest
import respx

from hserver_bot.api.client import HServerClient
from hserver_bot.api.snapshot import SnapshotMixin

BASE = "http://test"


class SnapshotTestClient(HServerClient, SnapshotMixin):
    """HServerClient extended with SnapshotMixin for isolated tests."""


def _authed_client() -> SnapshotTestClient:
    return SnapshotTestClient(BASE, "admin@test.com", "pass")


def _mock_login() -> None:
    respx.post(f"{BASE}/api/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "jwt"})
    )


@respx.mock
def test_snapshot_status():
    _mock_login()
    respx.get(f"{BASE}/api/backups/snapshot/status").mock(
        return_value=httpx.Response(
            200,
            json={
                "resticFound": True,
                "repoInitialized": True,
                "passwordSet": True,
                "driveConnected": True,
                "repoStats": {"snapshotCount": 3, "totalSize": 1073741824},
                "settings": {"lastRunAt": "2026-06-16T04:00:00Z", "lastError": ""},
            },
        )
    )
    client = _authed_client()
    data = client.snapshot_status()
    assert data["resticFound"] is True
    assert data["repoStats"]["snapshotCount"] == 3


@respx.mock
def test_snapshot_list():
    _mock_login()
    respx.get(f"{BASE}/api/backups/snapshot/list").mock(
        return_value=httpx.Response(
            200,
            json={
                "snapshots": [
                    {"id": "abc123def456", "time": "2026-06-16T04:00:00Z", "size": 512000},
                    {"id": "789xyz000111", "time": "2026-06-15T04:00:00Z", "size": 480000},
                ]
            },
        )
    )
    client = _authed_client()
    data = client.snapshot_list()
    snaps = data["snapshots"]
    assert len(snaps) == 2
    assert snaps[0]["id"] == "abc123def456"


@respx.mock
def test_run_snapshot():
    _mock_login()
    respx.post(f"{BASE}/api/backups/snapshot/run").mock(
        return_value=httpx.Response(
            202,
            json={"status": "running", "message": "incremental snapshot started", "jobId": "job-42"},
        )
    )
    client = _authed_client()
    result = client.run_snapshot()
    assert result["status"] == "running"
    assert result["jobId"] == "job-42"


@respx.mock
def test_snapshot_status_triggers_login():
    _mock_login()
    respx.get(f"{BASE}/api/backups/snapshot/status").mock(
        return_value=httpx.Response(200, json={"resticFound": True})
    )
    client = _authed_client()
    assert client._token is None
    client.snapshot_status()
    assert client._token == "jwt"


@respx.mock
def test_snapshot_status_propagates_http_error():
    _mock_login()
    respx.get(f"{BASE}/api/backups/snapshot/status").mock(
        return_value=httpx.Response(502, json={"error": "bad gateway"})
    )
    client = _authed_client()
    with pytest.raises(httpx.HTTPStatusError):
        client.snapshot_status()


@respx.mock
def test_snapshot_list_empty():
    _mock_login()
    respx.get(f"{BASE}/api/backups/snapshot/list").mock(
        return_value=httpx.Response(200, json={"snapshots": []})
    )
    client = _authed_client()
    data = client.snapshot_list()
    assert data["snapshots"] == []
