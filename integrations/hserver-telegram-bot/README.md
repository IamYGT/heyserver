# HserverTrack Telegram Bot

Telegram control plane for Heyserver Panel.

## Features

- Read-only server ops via hserver-panel API (health, system, disk, backups, PM2, SSL, cron, DB, nginx, docker)
- `/register` — registers chat as hserver notification channel
- Modular Python package with pytest coverage

## Commands

| Command | Description |
|---------|-------------|
| `/start` | Welcome message and quick intro |
| `/help` | List all commands |
| `/health` | hserver-panel API health |
| `/system` | Server summary (CPU, memory, uptime) |
| `/disk` | Disk usage |
| `/backups` | Backup list |
| `/gdrive` | Google Drive backup status |
| `/gdrive_test` | Trigger GDrive connectivity test |
| `/pm2` | PM2 process list |
| `/ssl` | SSL certificate status |
| `/cron` | Cron job list |
| `/db` | Database list |
| `/nginx` | Nginx status and config test |
| `/docker` | Docker status and container list |
| `/audit` | Quick backup audit |
| `/register` | Register this chat for hserver notifications |

## Setup

The bot uses one path contract in both local and systemd runs:

| Variable | Meaning |
|----------|---------|
| `HSERVER_BOT_HOME` | Installation/work root containing the checkout or package and its `.venv`. Relative values are resolved from the current directory. The local default is the current directory. |
| `HSERVER_BOT_DATA_DIR` | Persistent state directory (including `digest_subscribers.json`). Relative values are resolved below `HSERVER_BOT_HOME`; the local default is `$HSERVER_BOT_HOME/data`. |

Set these values to the actual installation before copying the environment file;
the application creates the data directory at startup and fails clearly when the
configured service identity cannot write to it. No provider-specific filesystem
layout is required.

```bash
export HSERVER_BOT_HOME=/path/to/hserver-telegram-bot
export HSERVER_BOT_DATA_DIR="$HSERVER_BOT_HOME/data"

cd "$HSERVER_BOT_HOME"
cp .env.example .env
# Edit .env and set the required values; never commit secrets.

python3 -m venv .venv
. .venv/bin/activate
pip install -e ".[dev]"
hserver-bot
```

### Environment variables

See [`.env.example`](.env.example):

| Variable | Required | Description |
|----------|----------|-------------|
| `HSERVER_BOT_HOME` | no | Installation/work root; defaults to the current directory for local runs |
| `HSERVER_BOT_DATA_DIR` | no | Persistent state directory; defaults to `data` below `HSERVER_BOT_HOME` |
| `TELEGRAM_BOT_TOKEN` | yes | Bot token from @BotFather |
| `TELEGRAM_ADMIN_CHAT_IDS` | no | Comma-separated chat IDs for notifications |
| `TELEGRAM_ALLOWED_USER_IDS` | no | Comma-separated user IDs allowed to run commands |
| `HSERVER_BASE_URL` | yes | Panel API base URL (default `http://127.0.0.1:3085`) |
| `HSERVER_ADMIN_EMAIL` | yes | Panel admin email |
| `HSERVER_ADMIN_PASS` | yes | Panel admin password |
| `HSERVER_CRON_SECRET` | no | Cron secret for internal backup triggers |
| `HSERVER_HEALTHCHECK_SCRIPT` | no | Absolute path used by the `/audit` backup healthcheck command |

## Optional external-checkout sync

The integration works directly from this repository. Contributors who also keep
an external bot checkout can mirror it into the panel tree with the included
sync helper. Both locations must be supplied explicitly; no machine-specific
path is assumed.

Workflow after changing bot code in the canonical repo:

```bash
cd /path/to/hserver-telegram-bot

# 1. Run tests
pytest -q

# 2. Push/sync into panel integrations tree
./scripts/sync-to-hserver-panel.sh

# 3. Restart the bot service (if already deployed)
sudo systemctl restart hserver-telegram-bot
```

The sync script rsyncs source to the panel integration path, excluding `.venv`, `.git`, and `.env`. Override paths if needed:

```bash
HSERVER_BOT_SRC=/path/to/hserver-telegram-bot \
HSERVER_PANEL_INTEGRATION=/path/to/hserver-panel/integrations/hserver-telegram-bot \
  ./scripts/sync-to-hserver-panel.sh
```

## Tests

```bash
cd /path/to/hserver-telegram-bot
. .venv/bin/activate
pytest -q
```

## systemd deployment

The checked-in unit reads one explicit, root-owned environment file:
`/etc/hserver/hserver-telegram-bot.env`. That file contains the same
`HSERVER_BOT_HOME` and `HSERVER_BOT_DATA_DIR` contract as a local `.env`, plus
the Telegram and Heyserver credentials. The default service identity is the
unprivileged `hserver-telegram-bot` user; the unit never needs root access.

Create the identity and data directory (change the path values to the selected
installation):

```bash
export HSERVER_BOT_HOME=/path/to/hserver-telegram-bot
export HSERVER_BOT_DATA_DIR="$HSERVER_BOT_HOME/data"
export HSERVER_BOT_USER=hserver-telegram-bot

if ! id "$HSERVER_BOT_USER" >/dev/null 2>&1; then
  sudo useradd --system --home-dir "$HSERVER_BOT_HOME" --shell /usr/sbin/nologin "$HSERVER_BOT_USER"
fi
sudo install -d -o "$HSERVER_BOT_USER" -g "$HSERVER_BOT_USER" "$HSERVER_BOT_DATA_DIR"
sudo install -d -m 0755 /etc/hserver
sudo install -o root -g "$HSERVER_BOT_USER" -m 0640 .env.example /etc/hserver/hserver-telegram-bot.env
sudoedit /etc/hserver/hserver-telegram-bot.env
```

Install the venv/package in `HSERVER_BOT_HOME`, then install and enable the unit:

```bash
sudo cp deploy/hserver-telegram-bot.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now hserver-telegram-bot
sudo systemctl status hserver-telegram-bot
```

Useful service commands:

```bash
sudo systemctl restart hserver-telegram-bot
sudo systemctl stop hserver-telegram-bot
journalctl -u hserver-telegram-bot -f
```

The service resolves its executable from `HSERVER_BOT_HOME` and stores digest
subscriptions below `HSERVER_BOT_DATA_DIR`. `WorkingDirectory=%h` follows the
configured service account's home. To use another unprivileged identity, create
a drop-in and set both `User=` and `Group=` there:

```ini
[Service]
User=service-user
Group=service-user
```
