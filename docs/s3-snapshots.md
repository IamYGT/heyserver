# S3-compatible encrypted snapshots

Heyserver supports an explicit S3-compatible destination for incremental restic
snapshots. Google Drive remains available as a separate destination. Switching
snapshot providers does not change local backup artifacts or upload their
contents implicitly.

## Data and credential boundary

- Restic encrypts repository data on the Heyserver host before it is uploaded.
- `HSERVER_RESTIC_PASSWORD` is shared only with the local restic process. Keep a
  durable copy outside the server; losing it makes the repository unrecoverable.
- S3 access and secret key **values** are never accepted by the panel/API,
  written to `snapshot-settings.json`, returned in status, or bundled into the
  web application.
- Snapshot settings persist only `destination: "s3"`, repository folder,
  manifest selection, retention, and the password-backup acknowledgement.
- The installation environment points to two protected credential files.

Restic's S3 backend and repository URL contract are documented in the
[official restic repository preparation guide](https://restic.readthedocs.io/en/stable/030_preparing_a_new_repo.html).

## Configuration

Install restic from a package source supported by the host, create the bucket
with the provider, and prepare the local secret directory:

```bash
install -d -m 0700 /etc/hserver/secrets
install -m 0600 /dev/null /etc/hserver/secrets/s3-access-key
install -m 0600 /dev/null /etc/hserver/secrets/s3-secret-key
sudoedit /etc/hserver/secrets/s3-access-key
sudoedit /etc/hserver/secrets/s3-secret-key
```

Each file must contain exactly one non-empty credential line. Set ownership to
the Heyserver systemd service identity. Symlinks and any group/world permission
bits fail closed.

Add the provider-neutral values to `/etc/hserver/hserver.env`:

```dotenv
HSERVER_RESTIC_BIN=restic
HSERVER_RESTIC_PASSWORD=replace-with-a-unique-generated-secret
HSERVER_S3_ENDPOINT=https://objects.example.com
HSERVER_S3_BUCKET=hserver-backups
HSERVER_S3_REGION=eu-central-1
HSERVER_S3_ACCESS_KEY_FILE=/etc/hserver/secrets/s3-access-key
HSERVER_S3_SECRET_KEY_FILE=/etc/hserver/secrets/s3-secret-key
HSERVER_S3_BUCKET_LOOKUP=auto
```

`HSERVER_S3_ENDPOINT` must use HTTPS. Plain HTTP is accepted only for loopback
MinIO endpoints such as `http://127.0.0.1:9000`. Userinfo, query strings, and
fragments are rejected. `HSERVER_S3_BUCKET_LOOKUP` accepts:

| Value | Use |
| --- | --- |
| `auto` | Let restic select portable bucket addressing |
| `dns` | Virtual-host-style bucket lookup when provider DNS supports it |
| `path` | Path-style lookup, commonly needed by local or compatible services |

Restart Heyserver after changing installation configuration. Then select the
destination in the panel or CLI:

```bash
hserverctl backups snapshot destination s3
hserverctl backups snapshot status
hserverctl backups snapshot list
hserverctl backups snapshot vhosts
hserverctl backups snapshot run --confirm
```

The destination command first reads the complete observed snapshot policy and
replaces only its destination, so manifest and retention settings are retained.

## Health states

| State | Meaning |
| --- | --- |
| `not_configured` | No S3 configuration exists, or the optional provider has not been selected/configured |
| `unavailable` | Configuration is incomplete/unsafe, restic cannot verify the remote repository, or provider access failed |
| `healthy` | A read-only restic request reached the selected endpoint and either opened the repository or observed its expected uninitialized first-run state |

An endpoint URL alone never produces `healthy`. Use
`GET /api/backups/snapshot/status?refresh=1` or the panel retry action for a
fresh read-only probe.

## Operations and current capability boundary

Snapshot run, retention (`restic forget --prune`), inventory, and restore use
the same selected S3 repository. Restore always extracts into Heyserver's fixed
local staging directory before an operator moves data into production paths.

Use an observed hexadecimal identity from `snapshot list`. The CLI requires an
explicit full or selective restore scope:

```bash
# Extract the complete snapshot into the fixed staging directory.
hserverctl backups snapshot restore --confirm --all abcdef1234567890

# Extract one observed vhost only.
hserverctl backups snapshot restore \
  --confirm --vhost example.com abcdef1234567890

# Extract fixed installation-owned manifest paths.
hserverctl backups snapshot restore \
  --confirm --manifest nginx --manifest letsencrypt abcdef1234567890
```

The server repeats snapshot identity, manifest allowlist, uniqueness, and vhost
name validation before starting the asynchronous restore job. It never accepts
an arbitrary local restore path from the CLI or API.

The full-screen client exposes the same bounded choices under **Encrypted
Snapshots**:

```bash
hserverctl ui
# N opens Encrypted Snapshots
# Enter opens complete, manifest, or observed-vhost restore scope
# Space selects identities; Enter continues; Y confirms
```

If refreshed remote inventory or local vhost-selector discovery temporarily
fails, the screen preserves the cached snapshot rows returned by status and
shows the failed sub-request as a warning instead of misreporting an empty
repository.

The destructive **purge entire repository** operation remains available only
for the Google Drive/rclone destination. S3 returns `422 Unprocessable Entity`
and the panel hides that control. This avoids adding a broad provider SDK or
claiming deletion that Heyserver cannot yet prove. Bucket/prefix deletion remains
an explicit provider-side operation in this release.

## Recovery checks

1. Confirm `restic version` works in the Heyserver systemd environment.
2. Confirm both credential paths are absolute regular files with mode `0600`
   and correct ownership.
3. Confirm the endpoint uses HTTPS, except for loopback MinIO.
4. Confirm the bucket exists and the key can list, read, write, and delete
   objects within the dedicated snapshot prefix.
5. Force a status refresh and inspect `destinationMessage` without printing
   credential values.
6. Preserve `HSERVER_RESTIC_PASSWORD` in the external vault before the first
   snapshot.
