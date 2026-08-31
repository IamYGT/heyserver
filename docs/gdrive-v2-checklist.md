# Google Drive Backup Acceptance

This checklist validates the optional Google Drive destination implemented
through rclone. It is an acceptance guide, not proof that a particular
installation has passed.

## Prerequisites

- `rclone version` succeeds for the Heyserver service identity.
- `HSERVER_GDRIVE_CLIENT_ID` and `HSERVER_GDRIVE_CLIENT_SECRET` are configured
  outside Git.
- The Google OAuth client contains the installation's exact callback URL:
  `https://panel.example.com/api/backups/gdrive/oauth/callback`.
- Any scheduled internal backup trigger uses a protected
  `HSERVER_CRON_SECRET`.
- The local backup directory has sufficient space for creation and restore.

The Google Drive integration is optional. Missing configuration or a missing
rclone dependency must appear as `not configured`; an unreachable configured
provider must appear as `unavailable` rather than healthy.

## Automated checks

Run the focused packages from a clean checkout:

```bash
go test ./internal/services/gdrive/... ./internal/services/backup/... \
  ./internal/api/... -count=1
npm --prefix web run build
```

These checks cover code paths only. They do not prove that the operator's OAuth
client, Drive quota, network, notification channel, or restore destination is
working.

## Disposable-installation smoke test

1. Open **Backups → Google Drive** and complete OAuth.
2. Confirm the status view reports the authenticated account and quota without
   exposing tokens.
3. Run the connection test.
4. Create a small file or database backup and wait for local completion.
5. Upload it and verify checksum-backed completion.
6. Confirm the remote object appears in the Drive inventory.
7. Download the object to a separate restore location.
8. Restore or unpack it and compare the expected content.
9. Enable automatic upload, create a second backup, and confirm it reaches the
   destination without a manual upload click.
10. Configure a short temporary schedule, observe one run, then restore the
    intended schedule.
11. If notifications are configured, prove both success and failure delivery.

## Failure and recovery checks

- Revoke the OAuth grant and confirm Heyserver reports provider failure without
  breaking local backups.
- Temporarily make rclone unavailable and confirm the state is `not configured`
  and the OAuth connection action is disabled.
- Interrupt an upload and confirm the job does not become `completed`.
- Attempt a restore with an unsupported filename and confirm it is rejected.
- Set remote retention to `0` (disabled), reload the page, and confirm the
  value remains `0` and no remote objects are deleted by a later upload.
- Reconnect OAuth and verify a later upload succeeds without deleting existing
  local backups.

## Acceptance evidence

Record the Heyserver version, OS and architecture, rclone version, test timestamp,
backup type, checksum comparison, and resulting job states. Do not record OAuth
tokens, environment files, Drive file contents, or unrelated server inventory.

The integration is accepted for a release only when automated checks pass and a
fresh disposable-installation upload-and-restore drill succeeds.
