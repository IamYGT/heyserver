"""Runtime configuration and relocatable installation paths."""

from __future__ import annotations

import os
from pathlib import Path
from tempfile import NamedTemporaryFile

from pydantic import Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


DEFAULT_BOT_HOME = Path(".")
DEFAULT_DATA_DIR = Path("data")


def resolve_bot_home(value: str | Path | None = None) -> Path:
    """Return the absolute installation/work directory for this checkout."""
    raw = value if value is not None else os.environ.get("HSERVER_BOT_HOME")
    path = Path(raw).expanduser() if raw else DEFAULT_BOT_HOME
    if not path.is_absolute():
        path = Path.cwd() / path
    return path.resolve()


def resolve_data_dir(
    value: str | Path | None = None,
    *,
    bot_home: str | Path | None = None,
) -> Path:
    """Resolve the data directory from configuration, relative to bot home."""
    raw = value if value is not None else os.environ.get("HSERVER_BOT_DATA_DIR")
    path = Path(raw).expanduser() if raw else DEFAULT_DATA_DIR
    if not path.is_absolute():
        path = resolve_bot_home(bot_home) / path
    return path.resolve()


def ensure_writable_data_dir(data_dir: str | Path) -> Path:
    """Create *data_dir* and verify the service identity can write to it."""
    path = resolve_data_dir(data_dir)
    try:
        path.mkdir(parents=True, exist_ok=True)
    except (FileExistsError, NotADirectoryError) as exc:
        raise NotADirectoryError(f"HSERVER_BOT_DATA_DIR is not a directory: {path}") from exc
    except OSError as exc:
        raise PermissionError(f"HSERVER_BOT_DATA_DIR cannot be created: {path}") from exc

    if not path.is_dir():
        raise NotADirectoryError(f"HSERVER_BOT_DATA_DIR is not a directory: {path}")

    try:
        with NamedTemporaryFile(prefix=".hserver-bot-write-test-", dir=path):
            pass
    except OSError as exc:
        raise PermissionError(f"HSERVER_BOT_DATA_DIR is not writable: {path}") from exc
    return path


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    # HSERVER_BOT_HOME is the install/work root. Relative data paths are
    # resolved against it, which keeps local checkouts and packaged installs
    # on the same contract.
    hserver_bot_home: Path = Field(default=DEFAULT_BOT_HOME, alias="HSERVER_BOT_HOME")
    hserver_bot_data_dir: Path = Field(default=DEFAULT_DATA_DIR, alias="HSERVER_BOT_DATA_DIR")

    telegram_bot_token: str = Field(alias="TELEGRAM_BOT_TOKEN")
    telegram_admin_chat_ids: str = Field(default="", alias="TELEGRAM_ADMIN_CHAT_IDS")
    telegram_allowed_user_ids: str = Field(default="", alias="TELEGRAM_ALLOWED_USER_IDS")
    telegram_webhook_url: str = Field(default="", alias="TELEGRAM_WEBHOOK_URL")
    telegram_webhook_port: int = Field(default=8443, alias="TELEGRAM_WEBHOOK_PORT")
    telegram_webhook_path: str = Field(default="/webhook", alias="TELEGRAM_WEBHOOK_PATH")
    telegram_use_webhook: bool = Field(default=False, alias="TELEGRAM_USE_WEBHOOK")

    hserver_base_url: str = Field(default="http://127.0.0.1:3085", alias="HSERVER_BASE_URL")
    hserver_admin_email: str = Field(alias="HSERVER_ADMIN_EMAIL")
    hserver_admin_pass: str = Field(alias="HSERVER_ADMIN_PASS")
    hserver_healthcheck_script: str = Field(default="", alias="HSERVER_HEALTHCHECK_SCRIPT")

    digest_enabled: bool = Field(default=False, alias="DIGEST_ENABLED")
    digest_hour_utc: int = Field(default=7, alias="DIGEST_HOUR_UTC")

    @model_validator(mode="after")
    def resolve_installation_paths(self) -> Settings:
        self.hserver_bot_home = resolve_bot_home(self.hserver_bot_home)
        self.hserver_bot_data_dir = resolve_data_dir(
            self.hserver_bot_data_dir,
            bot_home=self.hserver_bot_home,
        )
        if self.hserver_bot_data_dir.exists() and not self.hserver_bot_data_dir.is_dir():
            raise ValueError(
                f"HSERVER_BOT_DATA_DIR must point to a directory: {self.hserver_bot_data_dir}"
            )
        return self

    @field_validator("hserver_base_url")
    @classmethod
    def strip_trailing_slash(cls, v: str) -> str:
        return v.rstrip("/")

    def admin_chat_ids(self) -> list[int]:
        if not self.telegram_admin_chat_ids.strip():
            return []
        return [int(x.strip()) for x in self.telegram_admin_chat_ids.split(",") if x.strip()]

    def allowed_user_ids(self) -> set[int]:
        if not self.telegram_allowed_user_ids.strip():
            return set()
        return {int(x.strip()) for x in self.telegram_allowed_user_ids.split(",") if x.strip()}


def load_settings() -> Settings:
    return Settings()  # type: ignore[call-arg]
