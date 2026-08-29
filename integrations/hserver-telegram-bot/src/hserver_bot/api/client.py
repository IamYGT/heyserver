"""HTTP client for hserver-panel REST API."""

from __future__ import annotations

import httpx

from hserver_bot.api.alerts import AlertsMixin
from hserver_bot.api.auth import AuthMixin
from hserver_bot.api.backups import BackupsMixin
from hserver_bot.api.cloudflare import CloudflareMixin
from hserver_bot.api.cron import CronMixin
from hserver_bot.api.databases import DatabasesMixin
from hserver_bot.api.deploy import DeployMixin
from hserver_bot.api.disk import DiskMixin
from hserver_bot.api.disk_cleanup import DiskCleanupMixin
from hserver_bot.api.docker import DockerMixin
from hserver_bot.api.domains import DomainsMixin
from hserver_bot.api.nginx import NginxMixin
from hserver_bot.api.notify import NotifyMixin
from hserver_bot.api.pm2 import Pm2Mixin
from hserver_bot.api.snapshot import SnapshotMixin
from hserver_bot.api.ssl import SSLMixin
from hserver_bot.api.system import SystemMixin


class HServerClient(
    AuthMixin,
    SystemMixin,
    BackupsMixin,
    SnapshotMixin,
    DiskMixin,
    DiskCleanupMixin,
    Pm2Mixin,
    NotifyMixin,
    SSLMixin,
    DatabasesMixin,
    CronMixin,
    NginxMixin,
    DockerMixin,
    CloudflareMixin,
    DeployMixin,
    AlertsMixin,
    DomainsMixin,
):
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
