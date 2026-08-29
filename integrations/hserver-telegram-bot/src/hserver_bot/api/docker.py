"""Docker API."""

from __future__ import annotations


class DockerMixin:
    def docker_status(self) -> dict:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/docker/status")  # type: ignore[attr-defined]

    def docker_containers(self) -> dict | list:
        self.ensure_token()  # type: ignore[attr-defined]
        return self.get_json("/api/docker/containers")  # type: ignore[attr-defined]
