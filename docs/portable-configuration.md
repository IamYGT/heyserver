# Portable Configuration Schema v1

Heyserver portable configuration files move a narrow set of non-secret panel
preferences between self-hosted installations. They are not full backups and
do not replace the portable panel-state backup and restore workflow.

## What schema v1 includes

Schema v1 uses a positive allowlist. No database table or unknown setting is
exported automatically.

| Key | Accepted value |
| --- | --- |
| `hostnameDisplay` | Empty or a display label up to 128 bytes |
| `adminEmail` | Empty or one canonical email address up to 254 bytes |
| `notifyOnLogin` | `true` or `false` |
| `notifyOnError` | `true` or `false` |
| `notifyOnDeployment` | `true` or `false` |
| `webmail_url` | Empty or an HTTP(S) URL without embedded credentials |
| `mail_admin_url` | Empty or an HTTP(S) URL without embedded credentials |
| `mail_server_host` | Empty or a DNS hostname |
| `mail_imap_port` | Empty or a canonical integer from 1 to 65535 |
| `mail_smtp_starttls_port` | Empty or a canonical integer from 1 to 65535 |
| `mail_smtp_ssl_port` | Empty or a canonical integer from 1 to 65535 |
| `timezone` | Empty or an installed IANA timezone such as `Europe/Istanbul` |

An invalid allowlisted value is skipped during export and reported by key name
in `warnings`. A file containing an unknown key or an invalid value is rejected
as a whole during preview and import.

The authenticated generic `/api/settings` read, update, per-key read, and
per-key delete endpoints use this same allowlist. Service-owned internal records
remain reachable only through their dedicated masked APIs.

## What schema v1 excludes

Portable files never contain:

- users, password hashes, sessions, recovery codes, or TOTP state;
- API keys, OAuth state, tokens, provider credentials, or secret-file contents;
- enrolled server inventory, agent identities, capabilities, or agent tokens;
- onboarding state, audit history, monitoring samples, operation history, or
  runtime status;
- domains, deployment plans, backup plans, database state, notification channel
  credentials, or arbitrary future settings.

Use the [panel state backup workflow](installation-guide.md#15-back-up-and-restore-panel-state)
when the goal is disaster recovery rather than preference portability.

## File format

```json
{
  "schema_version": 1,
  "exported_at": "2026-08-26T20:00:00Z",
  "source_version": "v1.0.0",
  "settings": {
    "hostnameDisplay": "Community server",
    "notifyOnError": "true",
    "timezone": "Europe/Istanbul"
  }
}
```

The server accepts files up to 128 KiB, rejects unknown JSON fields and trailing
JSON values, and rejects schema versions it does not understand.

## Admin workflow

1. Open **Settings → Portable Configuration** as an admin.
2. Select **Download JSON** on the source installation.
3. On the destination installation, select **Choose JSON file**.
4. Review the server-generated changed and unchanged counts plus every proposed
   value change. Preview does not mutate settings.
5. Select the explicit overlay confirmation and then **Apply reviewed changes**.
6. Approve the browser confirmation dialog.

Import is transactional and overlay-only. It updates keys present in the file,
does not delete keys absent from the file, and leaves all excluded categories
untouched. A bundle whose values already match is a no-op. To reverse a change,
review and import a portable file exported before the change.

Export, preview, rejected requests, and confirmed imports are written to the
central audit trail with key counts only. Setting values are not copied into
audit details.

## API contract

All three endpoints require the `admin` role:

| Method | Endpoint | Purpose |
| --- | --- | --- |
| `GET` | `/api/settings/portable` | Build a schema-v1 allowlisted bundle |
| `POST` | `/api/settings/portable/preview` | Validate a raw bundle and calculate its overlay |
| `POST` | `/api/settings/portable/import` | Apply `{"bundle": ..., "confirmed": true}` |

Clients must always preview before presenting an import confirmation. The
server independently validates the bundle again during import, so a client-side
preview cannot bypass the schema boundary.
