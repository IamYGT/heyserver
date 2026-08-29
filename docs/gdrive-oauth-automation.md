# Google Drive OAuth setup

HServer supports an installation-owned Google OAuth web client for Google Drive
backups. No maintainer project, hostname, redirect URI, or credential is built
into the distribution.

## What can be automated

The included bootstrap script can select a Google Cloud project and enable the
Drive and People APIs. Creating a standard OAuth web client and entering its
authorized redirect URI remains a Google Cloud Console step.

```bash
./scripts/gdrive-gcp-bootstrap.sh my-gcp-project \
  'https://panel.example.com/api/backups/gdrive/oauth/callback'
```

The redirect URI must exactly match the public HServer callback URI configured
in Google Cloud Console.

## Credential sources

The recommended source is the protected HServer environment file:

```bash
sudoedit /etc/hserver/hserver.env
```

```dotenv
HSERVER_GDRIVE_CLIENT_ID=your-client-id.apps.googleusercontent.com
HSERVER_GDRIVE_CLIENT_SECRET=your-client-secret
HSERVER_GDRIVE_REDIRECT_URI=https://panel.example.com/api/backups/gdrive/oauth/callback
```

Restart HServer after changing its environment, then connect the Google account
from **Backups -> Google Drive**. Never commit the environment file, OAuth token,
`rclone.conf`, client secret, or backup encryption password.

An installation may alternatively provide a protected
`/var/lib/hserver/gdrive-vendor-oauth.json` file when its deployment workflow
uses a file-based secret. Environment values take precedence.

## Multi-installation broker boundary

A shared OAuth broker is an optional, separately deployed provider component.
The core HServer installation does not depend on one. Community operators who
run a broker must configure its URL explicitly and keep tenant state signing,
callback validation, and credentials outside the public HServer repository.

## Redirect URI examples

| Purpose | URI |
|---|---|
| Public panel callback | `https://panel.example.com/api/backups/gdrive/oauth/callback` |
| Local development callback | `http://127.0.0.1:3085/api/backups/gdrive/oauth/callback` |

Add only the callback URIs that an installation actually uses.
