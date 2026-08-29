# HServer Panel — Troubleshooting Guide

Provider-neutral recovery notes for common installation and runtime failures.

---

## 0. Diagnose Panel or Managed-Node Connectivity

**Problem:** It is unclear whether an operation is failing because the panel is
unreachable, authentication is invalid, a managed node is offline, or its agent
does not advertise the required capability.

**Solution:** Run the read-only CLI doctor through the same named context and
protected token file used by normal commands:

```bash
hserverctl --context production doctor --format text

hserverctl --context production doctor \
  --node edge-1 \
  --require-capability terminal \
  --require-capability files.read

# Persist the complete available report even when a check exits non-zero.
hserverctl --context production doctor \
  --output ./hserver-doctor.json
```

The default output is schema-v1 JSON for automation; `--format text` is the
human-readable view. `--output` refuses to replace an existing destination and
writes a new mode-`0600` file without relying on shell redirection. A failed
check still produces the available report and then exits non-zero. The report
omits account email/name and bearer-token data, but includes the server URL and
requested node identity, so review it before posting it publicly. Use root-only
`/usr/local/libexec/hserver-doctor installed` instead when diagnosing native
installation files, permissions, configuration, or systemd state on the panel
host itself.

---

## 1. Black Screen / White Page on Load

**Problem:** Panel loads but shows a blank white or black page. Browser console shows a 404 or MIME type error for a JS file.

**Cause:** The SPA fallback handler serves `index.html` for all unmatched routes, including `/assets/*.js` requests. When the browser requests a bundled JS chunk that doesn't exist in the embedded filesystem (stale build, wrong dist path), it receives HTML instead of JavaScript. The browser then throws a MIME type error and the app fails to boot.

**Solution:**
1. Check the browser console for the exact failing request (e.g. `GET /assets/index-abc123.js` returned HTML).
2. Rebuild and redeploy the frontend:
   ```bash
   cd /path/to/hserver-panel
   make deploy
   ```
3. Hard-refresh the browser (Ctrl+Shift+R / Cmd+Shift+R) to clear cached asset URLs.

**Prevention:**
- The router has an explicit `/assets/` guard: requests under `/assets/` that don't match a real file return 404, never HTML.
- `make deploy` includes a `sync-dist` step that copies `web/dist/` → `cmd/hserver/web/dist/` before `go build`. Never build the Go binary without running `sync-dist` first.

---

## 2. API Returns 401 After Successful Login

**Problem:** User logs in successfully, is redirected to the dashboard, but all subsequent API calls return `401 Unauthorized`. Refreshing the page redirects back to `/login`.

**Cause:** The JWT token stored in `localStorage` has expired (default expiry: 24 hours). The frontend's `api.ts` interceptor checks for 401 responses and triggers a redirect to `/login`. If the token is expired but the app was left open overnight, the first real API call after returning will hit this.

**Solution:**
1. Log out and log back in — a fresh token will be issued.
2. If this happens immediately after login, check the server clock: JWT validation compares `exp` against server time. Clock drift between client and server can cause immediate expiry.
   ```bash
   timedatectl status
   # Verify NTP is synchronized
   ```

**Prevention:**
- The frontend `useAuth` hook checks token expiry on app startup and redirects to `/login` proactively.
- Ensure `HSERVER_JWT_SECRET` is set in `/etc/hserver/hserver.env` — if the secret changes between restarts, all existing tokens are invalidated.

---

## 3. PM2 Shows "Failed" Status in systemd

**Problem:** After a server reboot, `systemctl status pm2-apps` shows `failed` or `inactive`. PM2 processes appear to be down.

**Cause:** The `pm2-apps.service` systemd unit was originally configured as `Type=forking`, which expects the process to fork and the parent to exit. PM2's startup script (`pm2 resurrect`) exits after restoring processes — it does not stay running. systemd interpreted the exit as a failure.

**Solution:**
Verify the unit file has the correct type:
```bash
cat /etc/systemd/system/pm2-apps.service
```
The unit must use:
```ini
[Service]
Type=oneshot
RemainAfterExit=yes
```
If it shows `Type=forking`, fix it:
```bash
systemctl edit pm2-apps --force
# Or edit /etc/systemd/system/pm2-apps.service directly
systemctl daemon-reload
systemctl enable --now pm2-apps
```

**Prevention:**
- `Type=oneshot` + `RemainAfterExit=yes` is the correct pattern for any service that runs a setup command and then considers itself "active".
- Never run PM2 as root or regenerate the startup script as root — PM2 daemon runs as the configured application user.

---

## 4. Database Size Shows "unknown"

**Problem:** The Databases page or Settings > System Info cards show `"unknown"` for database size.

**Cause:** The original implementation used Go's `database/sql` driver to run `SELECT pg_database_size(...)`. This worked for simple queries but failed when the SQL string contained single quotes or special characters, returning an error that was silently swallowed and replaced with `"unknown"`.

**Solution:** The fix was to use `exec.Command` to call `psql` directly with the query as a separate argument (not interpolated into a shell string):
```go
out, err := exec.Command("psql", "-U", "postgres", "-d", dbName,
    "-t", "-c", "SELECT pg_size_pretty(pg_database_size(current_database()))").Output()
```
If you see `"unknown"` again, check the panel logs:
```bash
journalctl -u hserver-panel -n 50 | grep -i "database size"
```

**Prevention:**
- Always pass SQL as a separate `exec.Command` argument, never interpolate it into a shell string.
- Avoid silent error suppression — log the error even when returning a fallback value.

---

## 5. Security Score Shows "NaN"

**Problem:** The Security page shows `NaN/100` or `NaN%` for the security score.

**Cause:** A type mismatch between the API response and frontend expectation. The `/api/security/score` endpoint returned an array of check results `[{name, passed, ...}]`, but the frontend TypeScript type declared it as `{score: number, checks: [...]}` (an object). Accessing `.score` on an array returns `undefined`, and `undefined / 100 * 100` evaluates to `NaN`.

**Solution:**
1. Check the actual API response shape:
   ```bash
   curl -s -H "Authorization: Bearer TOKEN" http://localhost:3085/api/security/score | jq .
   ```
2. Ensure the frontend TypeScript interface matches the real response shape.
3. Add a null guard in the component before performing arithmetic on the score value.

**Prevention:**
- All API responses that are consumed by frontend components must have explicit TypeScript interfaces in `web/src/lib/types.ts`.
- Always null-guard API response fields before arithmetic operations: `score ?? 0`.

---

## 6. Mail DNS Health Page Crashes (White Screen)

**Problem:** Navigating to Mail > DNS Health causes the page to crash with a React error boundary, or shows a blank section.

**Cause:** The DNS Health component iterated over `suggestions` from the API response. When all DNS records were correctly configured, the API returned `suggestions: null` instead of `suggestions: []`. Calling `.map()` on `null` throws a TypeError, crashing the component.

**Solution:** Add a null guard before rendering the suggestions list:
```tsx
{(suggestions ?? []).map((s) => <SuggestionRow key={s.id} {...s} />)}
```
If you see this crash again, check the API response:
```bash
curl -s -H "Authorization: Bearer TOKEN" http://localhost:3085/api/mail/dns-health | jq '.suggestions'
```

**Prevention:**
- Always treat array fields from API responses as potentially null. Use `field ?? []` before calling `.map()`, `.filter()`, or `.length`.
- The general rule: **never trust API array fields to be non-null in the frontend**.

---

## 7. All nginx Sites Show "Disabled" in Panel

**Problem:** The Domains / nginx page lists all sites as "disabled" even though they are serving traffic normally.

**Cause:** Some control panels or migration tools create nginx config files inside `sites-enabled/` as **direct copies** rather than symlinks. HServer detects enabled sites by checking whether a `sites-available/DOMAIN` file has a corresponding symlink in `sites-enabled/`. File copies are not symlinks, so the check returns false for every site.

**Solution:**
For each affected domain, replace the file copy with a proper symlink:
```bash
# Example for example.com
rm /etc/nginx/sites-enabled/example.com
ln -s /etc/nginx/sites-available/example.com /etc/nginx/sites-enabled/example.com
nginx -t && systemctl reload nginx
```

Verify the panel now shows the correct status.

**Prevention:**
- Always use `ln -s` when enabling nginx sites — never copy files.
- After migrating from another panel, normalize copied entries in the configured enabled-site directory before managing them with HServer.

---

## 8. WebSocket Terminal Returns 401 or 500

**Problem:** Clicking the Terminal icon opens the terminal panel but it fails to connect. The browser network tab shows a WebSocket handshake that returns HTTP 401 or 500.

**Cause (401):** The WebSocket upgrade request cannot carry an `Authorization: Bearer` header (browsers do not allow custom headers on WebSocket connections). The terminal handler expects the JWT token to be passed as a **query parameter**: `?token=JWT_HERE`. If the frontend sends the token in the header or doesn't send it at all, authentication fails with 401.

**Cause (500):** The Go HTTP handler attempted to hijack the connection using the `http.Hijacker` interface, but the underlying `http.ResponseWriter` did not implement `Hijacker` (e.g. it was wrapped by a middleware that stripped the interface). This causes a 500 at the type assertion.

**Solution:**
1. Confirm the frontend sends the token as a query parameter:
   ```
   ws://localhost:3085/api/terminal/ws?token=<JWT>
   ```
2. For 500 errors, check panel logs:
   ```bash
   journalctl -u hserver-panel -n 20
   ```
   If you see `does not implement http.Hijacker`, ensure the middleware stack does not wrap the `ResponseWriter` in a way that loses the `Hijacker` interface.

**Prevention:**
- WebSocket auth via query param is intentional — document this in any client integration.
- Middleware that wraps `ResponseWriter` must also delegate `Hijack()` calls.

---

## 9. pg_hba.conf Edits Cause "Permission Denied" for PostgreSQL

**Problem:** After editing `/etc/postgresql/*/main/pg_hba.conf` via the panel or manually, PostgreSQL fails to reload with `permission denied` or fails to read the config file at all.

**Cause:** The file was saved (or created) as root-owned. PostgreSQL's `postgres` process runs as the `postgres` user and requires the config file to be owned by `postgres:postgres`. If root writes the file, PostgreSQL cannot read it.

**Solution:**
```bash
chown postgres:postgres /etc/postgresql/16/main/pg_hba.conf
chmod 640 /etc/postgresql/16/main/pg_hba.conf
systemctl reload postgresql
```

**Prevention:**
- Any tool that writes `pg_hba.conf` (the panel's PostgreSQL service, manual edits, scripts) must restore ownership immediately after writing.
- The panel's pg_hba handler calls `chown` after every write operation.

---

## 10. PM2 Memory / Uptime Shows "0"

**Problem:** The PM2 Processes page shows `0 MB` memory and `0m` uptime for all running processes.

**Cause:** Double unit conversion. The PM2 API returns memory in **bytes**. The Go backend divided by 1024×1024 to convert to MB before sending to the frontend. The frontend TypeScript then divided the value by 1024×1024 again, assuming it received bytes. The result of `MB / 1024 / 1024` is effectively 0.

The same happened for uptime: PM2 returns milliseconds, Go converted to seconds, frontend divided by 1000 again.

**Solution:** The fix was to establish a clear contract: the Go API sends MB (memory) and seconds (uptime) as the canonical unit. The frontend uses those values directly without additional conversion.

If this reappears, check the raw API response:
```bash
curl -s -H "Authorization: Bearer TOKEN" http://localhost:3085/api/pm2/processes | jq '.[0] | {memory_mb, uptime_s}'
```
Then verify the frontend is not performing an extra conversion.

**Prevention:**
- Document the unit for every numeric field in API response types (`memory_mb: number`, `uptime_s: number`).
- Never perform unit conversion in both the backend and the frontend for the same field.

---

## 11. Deploy Migration Fails with SQL Syntax Error

**Problem:** Running `make deploy` or triggering a deploy webhook fails with a database error like:
```
near "commit": syntax error
near "trigger": syntax error
```

**Cause:** The deploy module stores migration history in SQLite. The migration SQL file used `commit` and `trigger` as column names (e.g. `commit TEXT`, `trigger TEXT`). Both are **reserved keywords** in SQLite and cannot be used as bare identifiers.

**Solution:**
Wrap reserved words in double quotes in the SQL schema:
```sql
CREATE TABLE IF NOT EXISTS deploy_history (
    id INTEGER PRIMARY KEY,
    "commit" TEXT NOT NULL,
    "trigger" TEXT NOT NULL,
    deployed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

If an existing installation encountered this historical migration, preserve the database and use the corrected in-place migration or restore the pre-upgrade backup. Do not drop an existing table to repair a normal release.

**Prevention:**
- Avoid using SQL reserved words as column names. When in doubt, check the [SQLite keywords list](https://www.sqlite.org/lang_keywords.html).
- Test migrations against SQLite specifically — some keywords are reserved in SQLite but not in PostgreSQL.

---

## 12. Frontend Changes Not Reflected After Deploy

**Problem:** After running `make deploy`, the panel binary is updated but the UI still shows the old version. Hard refresh (Ctrl+Shift+R) makes no difference.

**Cause:** The Go binary embeds the frontend via `go:embed`. The embed directive reads from `cmd/hserver/web/dist/`. However, `vite build` outputs to `web/dist/` (the root-level source directory). If `sync-dist` is skipped, the stale `cmd/hserver/web/dist/` is embedded in the new binary — the frontend is a different version from what was just built.

**Solution:**
```bash
cd /path/to/hserver-panel
# Manually sync and rebuild
cp -r web/dist/* cmd/hserver/web/dist/
make build
make restart
```
Or simply use `make deploy` which always runs `sync-dist` as its first step.

**Prevention:**
- Always use `make deploy` — never run `go build` directly in the `cmd/hserver/` directory.
- The `sync-dist` Makefile target is the authoritative step that keeps both dist directories in sync.

---

## 13. Build Fails with "Text File Busy"

**Problem:** `make deploy` fails during the binary copy step with:
```
cp: error writing '/usr/local/bin/hserver-panel': Text file busy
```

**Cause:** The running `hserver-panel` binary is memory-mapped by the OS. On Linux, you cannot overwrite an executable file that is currently running (the kernel holds an open reference). `cp` fails with "Text file busy" when it tries to truncate the file in place.

**Solution:**
Use the lifecycle installer, which stops the correct `hserver` service before
replacing the binary and preserves a rollback snapshot:
```bash
sudo ./scripts/hserver-install.sh upgrade --binary ./hserver-panel
```
From a source checkout, `make upgrade` builds and invokes the same lifecycle
installer. `make deploy` remains an alias for that target.

**Prevention:**
- Always use `make upgrade`, `make deploy`, or `hserver-install.sh upgrade`; do
  not copy over the running executable directly.

---

## 14. MariaDB Shows "Access Denied"

**Problem:** The panel cannot connect to a MariaDB database. The error is:
```
Access denied for user 'dbuser'@'127.0.0.1' (using password: YES)
```
But the credentials are correct and the user exists.

**Cause:** MariaDB distinguishes between `'user'@'localhost'` and `'user'@'127.0.0.1'` as **different accounts**. When PHP-FPM or the panel connects via TCP (`127.0.0.1:3306`), MariaDB checks for the `@'127.0.0.1'` grant. If the user was created with `GRANT ... TO 'user'@'localhost'`, that grant does not apply to TCP connections.

**Solution:**
Create the missing host-specific grant:
```sql
GRANT ALL PRIVILEGES ON dbname.* TO 'dbuser'@'127.0.0.1' IDENTIFIED BY 'password';
FLUSH PRIVILEGES;
```

**Prevention:**
- When creating database users, create grants for both `localhost` (Unix socket) and `127.0.0.1` (TCP).
- The panel's "Add Database User" feature creates both grants automatically.

---

## 15. Fail2Ban Status Shows "Stopped" Badge

**Problem:** The Security page shows Fail2Ban as "Stopped" even though `systemctl status fail2ban` shows it as active.

**Cause:** The frontend checked for a `status` field in the API response (e.g. `response.status === "running"`). The Go backend's Fail2Ban service returned the operational state in a field named `running` (boolean), not `status` (string). The frontend received `undefined` for `status`, which is falsy, and rendered the "Stopped" badge.

**Solution:** Check the actual API response field name:
```bash
curl -s -H "Authorization: Bearer TOKEN" http://localhost:3085/api/security/fail2ban | jq '{running, status}'
```
The correct field is `running: true`. If the frontend template is checking the wrong field, update it to use `running`.

**Prevention:**
- Use explicit TypeScript interfaces for all API responses. Accessing an undefined field should cause a compile-time type error, not a silent runtime falsy check.
- API field names must use camelCase consistently (`running`, not `is_running` or `status`).

---

## 16. Verified Upgrade Remains Scheduled or Running

**Problem:** The About page continues to show `scheduled` or `running` after an
unexpected reboot, forced stop, or interrupted transient unit.

**Automatic recovery:** Refresh **About → Release updates**. HServer inspects
only its fixed `hserver-panel-upgrade.timer` and
`hserver-panel-upgrade.service`. If neither is active, the persisted stage is
changed to `failed` with an interruption detail. If systemd cannot be queried,
HServer keeps the last known state instead of inventing a terminal result.

**Inspection:**

```bash
hserverctl updates status
hserverctl updates stage-status
systemctl status hserver-panel-upgrade.timer hserver-panel-upgrade.service
journalctl -u hserver-panel-upgrade --since "30 minutes ago"
journalctl -u hserver --since "30 minutes ago"
/usr/local/bin/hserver-panel --version
/usr/local/bin/hserverctl version
```

If the panel is healthy and the stage reports `failed`, review the journals and
then repeat the explicit confirmation with `hserverctl updates install
--confirm`. The CLI rereads the latest stage and will not schedule a different
ID or version supplied by the caller. A detail mentioning a release identity
mismatch means the replacement became healthy but the installed panel or CLI
did not report the exact verified-stage version, so HServer attempted automatic
rollback. Confirm both commands above report the earlier release before
retrying. Do not delete the current stage or retry while a timer or service is
active.

---

## 17. BIND Zones Appear Unavailable or Controls Stay Disabled

**Problem:** The DNS page reports **Not Installed**, **Setup Required**,
**Stopped**, or **Unavailable** instead of showing an editable empty zone list.

**Cause:** HServer could not prove the local BIND management boundary. It
requires `named`, `/etc/bind/named.conf.local`, `named-checkconf`,
`named-checkzone`, `rndc`, and an observable running `named` process before it
allows zone writes.

**Inspection:**

```bash
named -v
test -f /etc/bind/named.conf.local
command -v named-checkconf named-checkzone rndc
named-checkconf -z
systemctl status bind9 named --no-pager
journalctl -u bind9 -u named --since "30 minutes ago" --no-pager
```

Install missing distribution packages or repair the existing configuration,
then start the installation-selected unit explicitly. **Retry detection** does
not install, start, rewrite, or reload anything. A missing
`named.conf.local` is intentionally not reported as a healthy zero-zone server.

For record and SOA changes, an error containing `original zone file restored
and reloaded` means the new candidate passed validation but `rndc` rejected the
runtime reload; HServer restored both the previous disk content and runtime
view. An error containing `runtime rollback reload failed` means the disk file
was restored but the running daemon could not confirm that rollback. Inspect
the journal and run `named-checkconf -z` before an explicit reload.

Zone create/delete failures use parallel diagnostics. `created zone and
configuration rolled back and reloaded` or `deleted zone and configuration
restored and reloaded` means both disk files and the runtime view returned to
their previous version. A message containing `rollback reload failed` means
the files were restored but the running daemon still needs inspection before
another mutation.

If the page reports **BIND Recovery Required**, HServer found the protected
zone-lifecycle journal in `${HSERVER_DATA_DIR}/bind/` and could not complete its
startup recovery. Do not remove or edit that journal: it contains the original
config and zone snapshots needed for recovery. Run `named-checkconf -z`, restore
`rndc reload` and service availability, inspect the HServer service log, then
restart HServer. A successful startup either restores the interrupted
pre-reload transaction or finalizes a transaction already recorded as reloaded;
the journal is removed only after that recovery succeeds.

---

## 18. Managed Firewall Inventory Returns `read INPUT rules: exit status 1`

**Problem:** A managed node advertises `firewall.read`, but the panel or CLI
returns `HTTP 502: read INPUT rules: exit status 1`. Running
`sudo iptables -S INPUT` interactively on the node may still succeed.

**Cause:** An older agent systemd unit may restrict socket families to
`AF_UNIX AF_INET AF_INET6`. Ubuntu's default `iptables-nft` backend uses a
Netlink socket to read kernel firewall state, so a process inside that unit
cannot perform the same observation even when it runs as root and has
`CAP_NET_ADMIN`.

**Inspection:**

```bash
systemctl show hserver-agent \
  -p RestrictAddressFamilies \
  -p CapabilityBoundingSet \
  -p ActiveState

iptables --version
sudo iptables -S INPUT
journalctl -u hserver-agent --since "30 minutes ago" --no-pager
```

The current packaged unit must report `AF_NETLINK` alongside `AF_UNIX`,
`AF_INET`, and `AF_INET6`. Upgrade the agent with the current packaged
`agent-install.sh`; the lifecycle installer rewrites the owned unit, runs
`systemctl daemon-reload`, and restarts the agent while preserving its protected
configuration and token. Do not remove the complete address-family sandbox or
enable firewall writes merely to repair read inventory.

After the agent reconnects, verify the read path without changing rules:

```bash
hserverctl nodes get NODE
hserverctl firewall list --node NODE
```

`AF_NETLINK` only restores the kernel observation channel required by
`iptables-nft`. The hub still cannot read or mutate firewall state unless the
node explicitly advertises the corresponding capability, and writes remain
limited to the HServer-owned chain and revision checks.

---

## 19. Notification Channels Are Unavailable

**Problem:** The Notifications page reports that its protected channel store is
unavailable and pauses channel creation, editing, testing, and deletion.

**Cause:** HServer could not create or validate
`${HSERVER_DATA_DIR}/notification-channel-secrets`, a SQLite channel references
an unexpected file, or a channel config is missing, non-regular, group/world
accessible, oversized, or malformed. HServer does not return an empty channel
list or fall back to legacy database secrets.

**Inspection:** Read `HSERVER_DATA_DIR` from the protected environment without
printing other values, then inspect only this fixed directory:

```bash
sudo namei -l /var/lib/hserver/notification-channel-secrets
sudo find /var/lib/hserver/notification-channel-secrets -maxdepth 1 -type f \
  -name 'channel-*.json' -printf '%m %u:%g %f\n'
sudo journalctl -u hserver --since "30 minutes ago" --no-pager
```

The directory must be owned by the HServer service identity and mode `0700`;
each channel file must be a regular, non-symlink mode-`0600` file. Do not paste
file contents into logs or support reports. Repair ownership or permissions,
restore the matching `hserver-data` snapshot when a file is missing, restart
HServer, and use **Retry detection**. An empty secret field submitted by the edit
dialog intentionally preserves the current protected credential; use the
explicit removal checkbox when the credential must be cleared.

---

## Quick Reference: Common Commands

```bash
# Rebuild and redeploy everything
cd /path/to/hserver-panel && make deploy

# View live panel logs
journalctl -u hserver-panel -f

# Restart panel only (no rebuild)
systemctl restart hserver-panel

# Check PostgreSQL connectivity
psql -U postgres -c "SELECT version();"

# Check PM2 processes as the configured application user
sudo -u APP_USER PM2_HOME=/home/APP_USER/.pm2 pm2 list

# Verify nginx config and reload
nginx -t && systemctl reload nginx

# Check panel port is listening
ss -tlnp | grep 3085
```
