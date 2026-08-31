# Heyserver API Route Inventory

> Code generated from `internal/api/routes_manifest.go`; do not edit by hand.
> Regenerate with `make gen-api-docs`.

Total routes: **443**

The inventory is the complete routing and access-level contract. Request and
response guidance for common workflows remains in the curated
[API reference](api-reference.md).
The generated [OpenAPI 3.1 contract](openapi.json) exposes the same routes,
path parameters, and access boundaries to clients and development tools.

| Method | Path | Access |
| --- | --- | --- |
| `GET` | `/.well-known/autoconfig/mail/config-v1.1.xml` | Public |
| `POST` | `/api/agent/v1/heartbeat` | Managed-node agent |
| `POST` | `/api/agent/v1/tasks/poll` | Managed-node agent |
| `POST` | `/api/agent/v1/tasks/{id}/result` | Managed-node agent |
| `GET` | `/api/agent/v1/terminal` | Managed-node agent |
| `GET` | `/api/audit` | Authenticated |
| `POST` | `/api/auth/2fa/disable` | Authenticated |
| `POST` | `/api/auth/2fa/recovery` | Public |
| `POST` | `/api/auth/2fa/setup` | Authenticated |
| `GET` | `/api/auth/2fa/status` | Authenticated |
| `POST` | `/api/auth/2fa/verify` | Authenticated |
| `POST` | `/api/auth/login` | Public |
| `POST` | `/api/auth/logout` | Public |
| `GET` | `/api/auth/me` | Authenticated |
| `POST` | `/api/auth/totp-verify` | Public |
| `GET` | `/api/backups` | Authenticated |
| `POST` | `/api/backups` | Admin |
| `GET` | `/api/backups/download/{id}` | Admin |
| `POST` | `/api/backups/gdrive/disconnect` | Admin |
| `POST` | `/api/backups/gdrive/dismiss-error` | Admin |
| `GET` | `/api/backups/gdrive/oauth-app` | Admin |
| `PUT` | `/api/backups/gdrive/oauth-app` | Admin |
| `GET` | `/api/backups/gdrive/oauth/callback` | Public |
| `POST` | `/api/backups/gdrive/oauth/complete` | Admin |
| `POST` | `/api/backups/gdrive/oauth/start` | Admin |
| `GET` | `/api/backups/gdrive/remote` | Admin |
| `POST` | `/api/backups/gdrive/restore` | Admin |
| `PUT` | `/api/backups/gdrive/settings` | Admin |
| `GET` | `/api/backups/gdrive/status` | Admin |
| `POST` | `/api/backups/gdrive/test` | Admin |
| `GET` | `/api/backups/jobs` | Admin |
| `GET` | `/api/backups/jobs/stream` | Admin |
| `GET` | `/api/backups/jobs/{id}` | Admin |
| `POST` | `/api/backups/jobs/{id}/dismiss` | Admin |
| `POST` | `/api/backups/purge-invalid` | Admin |
| `POST` | `/api/backups/purge-orphaned` | Admin |
| `POST` | `/api/backups/restore/{id}` | Admin |
| `GET` | `/api/backups/restore/{id}/validate` | Admin |
| `DELETE` | `/api/backups/schedules` | Admin |
| `GET` | `/api/backups/schedules` | Admin |
| `POST` | `/api/backups/schedules` | Admin |
| `GET` | `/api/backups/snapshot/list` | Admin |
| `POST` | `/api/backups/snapshot/purge-repo` | Admin |
| `POST` | `/api/backups/snapshot/restore` | Admin |
| `POST` | `/api/backups/snapshot/run` | Admin |
| `GET` | `/api/backups/snapshot/settings` | Admin |
| `PUT` | `/api/backups/snapshot/settings` | Admin |
| `GET` | `/api/backups/snapshot/status` | Admin |
| `GET` | `/api/backups/snapshot/vhosts` | Admin |
| `GET` | `/api/backups/targets` | Admin |
| `POST` | `/api/backups/upload/{id}` | Admin |
| `DELETE` | `/api/backups/{id}` | Admin |
| `POST` | `/api/cloudflare/mail-autofix/{domain}` | Admin |
| `GET` | `/api/cloudflare/zones` | Authenticated |
| `GET` | `/api/cloudflare/zones/{zoneId}` | Authenticated |
| `GET` | `/api/cloudflare/zones/{zoneId}/email-routing` | Authenticated |
| `POST` | `/api/cloudflare/zones/{zoneId}/purge` | Admin |
| `GET` | `/api/cloudflare/zones/{zoneId}/records` | Authenticated |
| `POST` | `/api/cloudflare/zones/{zoneId}/records` | Manager or admin |
| `DELETE` | `/api/cloudflare/zones/{zoneId}/records/{recordId}` | Admin |
| `PUT` | `/api/cloudflare/zones/{zoneId}/records/{recordId}` | Manager or admin |
| `PUT` | `/api/cloudflare/zones/{zoneId}/records/{recordId}/proxy` | Manager or admin |
| `GET` | `/api/cron/jobs` | Authenticated |
| `POST` | `/api/cron/jobs` | Manager or admin |
| `DELETE` | `/api/cron/jobs/{id}` | Admin |
| `PUT` | `/api/cron/jobs/{id}` | Manager or admin |
| `GET` | `/api/cron/status` | Authenticated |
| `GET` | `/api/cron/system` | Authenticated |
| `GET` | `/api/databases` | Authenticated |
| `POST` | `/api/databases` | Admin |
| `GET` | `/api/databases/credentials` | Admin |
| `GET` | `/api/databases/credentials/{name}` | Admin |
| `GET` | `/api/databases/pgm-backup-files/{name}` | Authenticated |
| `GET` | `/api/databases/pgm-backups` | Authenticated |
| `GET` | `/api/databases/pgm-credentials` | Admin |
| `POST` | `/api/databases/pgm-restore` | Admin |
| `GET` | `/api/databases/users` | Authenticated |
| `DELETE` | `/api/databases/{engine}/{name}` | Admin |
| `POST` | `/api/databases/{engine}/{name}/query` | Manager or admin |
| `GET` | `/api/databases/{engine}/{name}/tables` | Authenticated |
| `GET` | `/api/deploy/history` | Authenticated |
| `GET` | `/api/deploy/history/{id}/logs` | Authenticated |
| `POST` | `/api/deploy/manual/{targetId}` | Manager or admin |
| `POST` | `/api/deploy/rollback/{targetId}` | Admin |
| `GET` | `/api/deploy/targets` | Authenticated |
| `POST` | `/api/deploy/targets` | Admin |
| `DELETE` | `/api/deploy/targets/{id}` | Admin |
| `PUT` | `/api/deploy/targets/{id}` | Admin |
| `GET` | `/api/deploy/targets/{id}/domains` | Authenticated |
| `POST` | `/api/deploy/targets/{id}/domains` | Admin |
| `DELETE` | `/api/deploy/targets/{id}/domains/{domainId}` | Admin |
| `GET` | `/api/deploy/targets/{id}/domains/{domainId}/health` | Authenticated |
| `DELETE` | `/api/deploy/targets/{id}/domains/{domainId}/tls` | Admin |
| `POST` | `/api/deploy/targets/{id}/domains/{domainId}/tls` | Admin |
| `GET` | `/api/deploy/targets/{id}/environment` | Admin |
| `PUT` | `/api/deploy/targets/{id}/environment` | Admin |
| `DELETE` | `/api/deploy/targets/{id}/environment/{key}` | Admin |
| `GET` | `/api/deploy/targets/{id}/preflight` | Authenticated |
| `GET` | `/api/deploy/targets/{id}/revision` | Authenticated |
| `GET` | `/api/deploy/targets/{id}/services` | Authenticated |
| `GET` | `/api/deploy/targets/{id}/services/{service}/logs` | Authenticated |
| `POST` | `/api/deploy/targets/{id}/services/{service}/{action}` | Manager or admin |
| `POST` | `/api/deploy/targets/{id}/staging` | Admin |
| `GET` | `/api/deploy/templates` | Admin |
| `POST` | `/api/deploy/webhook/{targetId}` | Public |
| `POST` | `/api/disk/analysis/start` | Admin |
| `GET` | `/api/disk/analysis/status` | Admin |
| `POST` | `/api/disk/cleanup/execute` | Admin |
| `GET` | `/api/disk/cleanup/scan` | Admin |
| `GET` | `/api/disk/dirsize` | Admin |
| `GET` | `/api/disk/io` | Admin |
| `GET` | `/api/disk/largest` | Admin |
| `GET` | `/api/disk/list` | Admin |
| `GET` | `/api/disk/mounts` | Admin |
| `GET` | `/api/disk/overview` | Admin |
| `GET` | `/api/disk/smart/{device}` | Admin |
| `GET` | `/api/disk/usage` | Admin |
| `POST` | `/api/dns/check` | Authenticated |
| `GET` | `/api/dns/lookup` | Authenticated |
| `POST` | `/api/dns/reload` | Manager or admin |
| `GET` | `/api/dns/status` | Authenticated |
| `GET` | `/api/dns/zones` | Authenticated |
| `POST` | `/api/dns/zones` | Admin |
| `DELETE` | `/api/dns/zones/{domain}` | Admin |
| `GET` | `/api/dns/zones/{domain}` | Authenticated |
| `GET` | `/api/dns/zones/{domain}/export` | Authenticated |
| `DELETE` | `/api/dns/zones/{domain}/records` | Manager or admin |
| `GET` | `/api/dns/zones/{domain}/records` | Authenticated |
| `POST` | `/api/dns/zones/{domain}/records` | Manager or admin |
| `PUT` | `/api/dns/zones/{domain}/records` | Manager or admin |
| `GET` | `/api/dns/zones/{domain}/soa` | Authenticated |
| `PUT` | `/api/dns/zones/{domain}/soa` | Manager or admin |
| `GET` | `/api/docker/containers` | Authenticated |
| `GET` | `/api/docker/containers/{id}/logs` | Authenticated |
| `POST` | `/api/docker/containers/{id}/{action}` | Admin |
| `GET` | `/api/docker/images` | Authenticated |
| `POST` | `/api/docker/images/pull` | Admin |
| `DELETE` | `/api/docker/images/{id}` | Admin |
| `GET` | `/api/docker/status` | Authenticated |
| `GET` | `/api/domains` | Authenticated |
| `POST` | `/api/domains` | Admin |
| `POST` | `/api/domains/check` | Manager or admin |
| `GET` | `/api/domains/provisioning` | Authenticated |
| `DELETE` | `/api/domains/{id}` | Admin |
| `GET` | `/api/domains/{id}` | Authenticated |
| `POST` | `/api/domains/{id}/toggle` | Manager or admin |
| `DELETE` | `/api/files` | Manager or admin |
| `GET` | `/api/files` | Authenticated |
| `POST` | `/api/files/create` | Manager or admin |
| `GET` | `/api/files/read` | Authenticated |
| `POST` | `/api/files/rename` | Manager or admin |
| `PUT` | `/api/files/write` | Manager or admin |
| `GET` | `/api/firewall/rules` | Authenticated |
| `POST` | `/api/firewall/rules` | Admin |
| `DELETE` | `/api/firewall/rules/{number}` | Admin |
| `GET` | `/api/firewall/status` | Authenticated |
| `POST` | `/api/firewall/toggle` | Admin |
| `GET` | `/api/health` | Public |
| `GET` | `/api/integrations/catalog` | Authenticated |
| `GET` | `/api/integrations/status` | Authenticated |
| `POST` | `/api/internal/cron/backup` | Local internal trigger |
| `GET` | `/api/internal/deploy/preflight` | Local internal trigger |
| `GET` | `/api/logs/download` | Authenticated |
| `GET` | `/api/logs/read` | Authenticated |
| `GET` | `/api/logs/search` | Authenticated |
| `GET` | `/api/logs/sources` | Authenticated |
| `GET` | `/api/logs/stream` | Authenticated |
| `GET` | `/api/mail/accounts` | Authenticated |
| `POST` | `/api/mail/accounts` | Manager or admin |
| `DELETE` | `/api/mail/accounts/{email}` | Admin |
| `GET` | `/api/mail/accounts/{email}` | Authenticated |
| `GET` | `/api/mail/accounts/{email}/password` | Admin |
| `PUT` | `/api/mail/accounts/{email}/password` | Manager or admin |
| `PUT` | `/api/mail/accounts/{email}/quota` | Manager or admin |
| `GET` | `/api/mail/aliases` | Authenticated |
| `POST` | `/api/mail/aliases` | Manager or admin |
| `DELETE` | `/api/mail/aliases/{id}` | Admin |
| `GET` | `/api/mail/config` | Authenticated |
| `GET` | `/api/mail/dkim/{domain}` | Authenticated |
| `POST` | `/api/mail/dkim/{domain}` | Manager or admin |
| `GET` | `/api/mail/dkim/{domain}/config` | Authenticated |
| `POST` | `/api/mail/dkim/{domain}/rotate` | Manager or admin |
| `GET` | `/api/mail/dkim/{domain}/{selector}/dns` | Authenticated |
| `GET` | `/api/mail/dns-check/{domain}` | Authenticated |
| `GET` | `/api/mail/domains` | Authenticated |
| `POST` | `/api/mail/domains` | Manager or admin |
| `DELETE` | `/api/mail/domains/{domain}` | Admin |
| `GET` | `/api/mail/groups` | Authenticated |
| `POST` | `/api/mail/groups` | Manager or admin |
| `PATCH` | `/api/mail/groups/{name}/members` | Manager or admin |
| `GET` | `/api/mail/listeners` | Authenticated |
| `GET` | `/api/mail/logs` | Authenticated |
| `GET` | `/api/mail/logs/delivery` | Authenticated |
| `GET` | `/api/mail/logs/search` | Authenticated |
| `GET` | `/api/mail/queue` | Authenticated |
| `DELETE` | `/api/mail/queue/{id}` | Admin |
| `POST` | `/api/mail/queue/{id}/retry` | Manager or admin |
| `GET` | `/api/mail/security/connections` | Authenticated |
| `GET` | `/api/mail/security/failed-logins` | Admin |
| `GET` | `/api/mail/security/rate-limits` | Authenticated |
| `PUT` | `/api/mail/security/rate-limits` | Admin |
| `GET` | `/api/mail/security/tls` | Authenticated |
| `GET` | `/api/mail/service/overview` | Authenticated |
| `GET` | `/api/mail/service/status` | Authenticated |
| `POST` | `/api/mail/service/{action}` | Admin |
| `GET` | `/api/mail/spam/allowlist` | Authenticated |
| `POST` | `/api/mail/spam/allowlist` | Manager or admin |
| `DELETE` | `/api/mail/spam/allowlist/{pattern}` | Manager or admin |
| `GET` | `/api/mail/spam/blocklist` | Authenticated |
| `POST` | `/api/mail/spam/blocklist` | Manager or admin |
| `DELETE` | `/api/mail/spam/blocklist/{pattern}` | Manager or admin |
| `GET` | `/api/mail/spam/config` | Authenticated |
| `PUT` | `/api/mail/spam/config` | Admin |
| `GET` | `/api/mail/stats` | Authenticated |
| `GET` | `/api/mail/stats/deliverability` | Authenticated |
| `GET` | `/api/mail/stats/storage` | Authenticated |
| `GET` | `/api/mail/stats/top-recipients` | Authenticated |
| `GET` | `/api/mail/stats/top-senders` | Authenticated |
| `GET` | `/api/mail/stats/volume` | Authenticated |
| `GET` | `/api/mail/status` | Authenticated |
| `GET` | `/api/mail/storage` | Authenticated |
| `GET` | `/api/mail/version` | Authenticated |
| `GET` | `/api/metrics/history` | Authenticated |
| `GET` | `/api/metrics/processes` | Authenticated |
| `GET` | `/api/metrics/processes/timestamps` | Authenticated |
| `GET` | `/api/metrics/services/history` | Authenticated |
| `GET` | `/api/metrics/summary` | Authenticated |
| `GET` | `/api/monitoring/processes` | Authenticated |
| `GET` | `/api/monitoring/stats` | Authenticated |
| `GET` | `/api/nginx/archives` | Authenticated |
| `POST` | `/api/nginx/archives/{archive}/restore` | Manager or admin |
| `GET` | `/api/nginx/backups` | Authenticated |
| `POST` | `/api/nginx/backups/{backup}/restore` | Manager or admin |
| `GET` | `/api/nginx/configs` | Authenticated |
| `POST` | `/api/nginx/configs` | Manager or admin |
| `DELETE` | `/api/nginx/configs/{filename}` | Manager or admin |
| `GET` | `/api/nginx/configs/{filename}` | Authenticated |
| `PUT` | `/api/nginx/configs/{filename}` | Manager or admin |
| `PUT` | `/api/nginx/configs/{filename}/state` | Manager or admin |
| `POST` | `/api/nginx/configs/{filename}/toggle` | Manager or admin |
| `POST` | `/api/nginx/reload` | Manager or admin |
| `GET` | `/api/nginx/snippets` | Authenticated |
| `GET` | `/api/nginx/status` | Authenticated |
| `POST` | `/api/nginx/test` | Authenticated |
| `GET` | `/api/nodes` | Authenticated |
| `POST` | `/api/nodes` | Admin |
| `GET` | `/api/nodes/{id}` | Authenticated |
| `GET` | `/api/nodes/{id}/actions/reboot-status` | Admin |
| `GET` | `/api/nodes/{id}/actions/status` | Admin |
| `POST` | `/api/nodes/{id}/actions/{action}` | Admin |
| `GET` | `/api/nodes/{id}/agent-update` | Admin |
| `POST` | `/api/nodes/{id}/agent-update/rollback` | Admin |
| `POST` | `/api/nodes/{id}/agent-update/upgrade` | Admin |
| `GET` | `/api/nodes/{id}/backups` | Admin |
| `POST` | `/api/nodes/{id}/backups/{plan}/run` | Admin |
| `GET` | `/api/nodes/{id}/certificates` | Admin |
| `POST` | `/api/nodes/{id}/certificates/{name}/actions/{action}` | Admin |
| `GET` | `/api/nodes/{id}/containers` | Admin |
| `POST` | `/api/nodes/{id}/containers/{container}/actions/{action}` | Admin |
| `GET` | `/api/nodes/{id}/cron` | Admin |
| `POST` | `/api/nodes/{id}/cron` | Admin |
| `DELETE` | `/api/nodes/{id}/cron/{job}` | Admin |
| `PUT` | `/api/nodes/{id}/cron/{job}` | Admin |
| `POST` | `/api/nodes/{id}/cron/{job}/run` | Admin |
| `GET` | `/api/nodes/{id}/databases` | Admin |
| `POST` | `/api/nodes/{id}/databases/{engine}/actions/{action}` | Admin |
| `GET` | `/api/nodes/{id}/deploy` | Admin |
| `GET` | `/api/nodes/{id}/deploy/jobs` | Admin |
| `POST` | `/api/nodes/{id}/deploy/{target}/actions/{action}` | Admin |
| `GET` | `/api/nodes/{id}/deploy/{target}/domains` | Admin |
| `POST` | `/api/nodes/{id}/deploy/{target}/domains` | Admin |
| `PUT` | `/api/nodes/{node_id}/deploy/{target_id}/domains/{domain}` | Admin |
| `DELETE` | `/api/nodes/{id}/deploy/{target}/domains/{domain}` | Admin |
| `GET` | `/api/nodes/{id}/deploy/{target}/domains/{domain}/health` | Admin |
| `DELETE` | `/api/nodes/{id}/deploy/{target}/domains/{domain}/tls` | Admin |
| `POST` | `/api/nodes/{id}/deploy/{target}/domains/{domain}/tls` | Admin |
| `POST` | `/api/nodes/{id}/deploy/{target}/domains/{domain}/tls/renew` | Admin |
| `GET` | `/api/nodes/{id}/disk` | Admin |
| `GET` | `/api/nodes/{id}/disk/cleanup` | Admin |
| `POST` | `/api/nodes/{id}/disk/cleanup` | Admin |
| `GET` | `/api/nodes/{id}/domains` | Admin |
| `POST` | `/api/nodes/{id}/domains/{config}/actions/{action}` | Admin |
| `GET` | `/api/nodes/{id}/file` | Admin |
| `PUT` | `/api/nodes/{id}/file` | Admin |
| `GET` | `/api/nodes/{id}/files` | Admin |
| `GET` | `/api/nodes/{id}/firewall` | Admin |
| `POST` | `/api/nodes/{id}/firewall` | Admin |
| `DELETE` | `/api/nodes/{id}/firewall/{rule}` | Admin |
| `GET` | `/api/nodes/{id}/integrations/status` | Admin |
| `GET` | `/api/nodes/{id}/logs` | Admin |
| `GET` | `/api/nodes/{id}/memory` | Admin |
| `GET` | `/api/nodes/{id}/metrics` | Admin |
| `POST` | `/api/nodes/{id}/nginx/actions/{action}` | Admin |
| `GET` | `/api/nodes/{id}/nginx/configs` | Admin |
| `GET` | `/api/nodes/{id}/nginx/configs/{name}` | Admin |
| `PUT` | `/api/nodes/{id}/nginx/configs/{name}` | Admin |
| `GET` | `/api/nodes/{id}/php` | Admin |
| `POST` | `/api/nodes/{id}/php/{version}/actions/{action}` | Admin |
| `GET` | `/api/nodes/{id}/php/{version}/pools/{pool}` | Admin |
| `PUT` | `/api/nodes/{id}/php/{version}/pools/{pool}` | Admin |
| `GET` | `/api/nodes/{id}/pm2` | Admin |
| `POST` | `/api/nodes/{id}/pm2/{name}/actions/{action}` | Admin |
| `GET` | `/api/nodes/{id}/pm2/{name}/logs` | Admin |
| `GET` | `/api/nodes/{id}/processes` | Admin |
| `POST` | `/api/nodes/{id}/processes/signal` | Admin |
| `GET` | `/api/nodes/{id}/profile` | Admin |
| `PUT` | `/api/nodes/{id}/profile` | Admin |
| `POST` | `/api/nodes/{id}/profile/apply` | Admin |
| `GET` | `/api/nodes/{id}/tasks` | Manager or admin |
| `POST` | `/api/nodes/{id}/tasks` | Admin |
| `GET` | `/api/nodes/{id}/tasks/{taskID}` | Manager or admin |
| `GET` | `/api/notify/channels` | Authenticated |
| `POST` | `/api/notify/channels` | Manager or admin |
| `DELETE` | `/api/notify/channels/{id}` | Admin |
| `GET` | `/api/notify/channels/{id}` | Authenticated |
| `PUT` | `/api/notify/channels/{id}` | Manager or admin |
| `POST` | `/api/notify/channels/{id}/test` | Manager or admin |
| `GET` | `/api/notify/history` | Authenticated |
| `GET` | `/api/notify/rules` | Authenticated |
| `POST` | `/api/notify/rules` | Manager or admin |
| `DELETE` | `/api/notify/rules/{id}` | Admin |
| `GET` | `/api/notify/rules/{id}` | Authenticated |
| `PUT` | `/api/notify/rules/{id}` | Manager or admin |
| `GET` | `/api/onboarding` | Authenticated |
| `POST` | `/api/onboarding` | Admin |
| `GET` | `/api/php/composer/version` | Authenticated |
| `POST` | `/api/php/composer/{version}/install` | Manager or admin |
| `POST` | `/api/php/composer/{version}/outdated` | Manager or admin |
| `POST` | `/api/php/composer/{version}/require` | Admin |
| `POST` | `/api/php/composer/{version}/update` | Admin |
| `GET` | `/api/php/extensions/{version}` | Authenticated |
| `POST` | `/api/php/extensions/{version}/{name}/disable` | Admin |
| `POST` | `/api/php/extensions/{version}/{name}/enable` | Admin |
| `GET` | `/api/php/ini/{version}` | Authenticated |
| `PUT` | `/api/php/ini/{version}` | Manager or admin |
| `GET` | `/api/php/ini/{version}/diff` | Authenticated |
| `GET` | `/api/php/ini/{version}/directives` | Authenticated |
| `GET` | `/api/php/ini/{version}/{domain}` | Authenticated |
| `PUT` | `/api/php/ini/{version}/{domain}` | Manager or admin |
| `DELETE` | `/api/php/ini/{version}/{domain}/{key}` | Admin |
| `GET` | `/api/php/logs/{version}/error` | Authenticated |
| `GET` | `/api/php/logs/{version}/{domain}/slow` | Authenticated |
| `GET` | `/api/php/opcache/{version}` | Authenticated |
| `POST` | `/api/php/opcache/{version}/reset` | Admin |
| `GET` | `/api/php/pools` | Authenticated |
| `POST` | `/api/php/pools` | Manager or admin |
| `POST` | `/api/php/pools/auto-tune` | Authenticated |
| `POST` | `/api/php/pools/switch-version` | Admin |
| `DELETE` | `/api/php/pools/{version}/{domain}` | Admin |
| `GET` | `/api/php/pools/{version}/{domain}` | Authenticated |
| `POST` | `/api/php/pools/{version}/{domain}` | Manager or admin |
| `PUT` | `/api/php/pools/{version}/{domain}` | Manager or admin |
| `GET` | `/api/php/pools/{version}/{domain}/config` | Authenticated |
| `PUT` | `/api/php/pools/{version}/{domain}/config` | Manager or admin |
| `POST` | `/api/php/pools/{version}/{domain}/preset` | Manager or admin |
| `POST` | `/api/php/pools/{version}/{domain}/restart` | Manager or admin |
| `GET` | `/api/php/presets` | Authenticated |
| `POST` | `/api/php/restart/{version}` | Manager or admin |
| `GET` | `/api/php/security/profiles` | Authenticated |
| `GET` | `/api/php/security/{version}/{domain}` | Authenticated |
| `POST` | `/api/php/security/{version}/{domain}` | Admin |
| `GET` | `/api/php/status/{version}` | Authenticated |
| `GET` | `/api/php/status/{version}/{domain}` | Authenticated |
| `GET` | `/api/php/versions` | Authenticated |
| `POST` | `/api/php/versions/{version}/actions/{action}` | Manager or admin |
| `POST` | `/api/php/versions/{version}/restart` | Manager or admin |
| `POST` | `/api/pm2/deploy` | Admin |
| `GET` | `/api/pm2/processes` | Authenticated |
| `GET` | `/api/pm2/processes/{id}` | Authenticated |
| `GET` | `/api/pm2/processes/{id}/logs` | Authenticated |
| `POST` | `/api/pm2/processes/{id}/{action}` | Manager or admin |
| `POST` | `/api/pm2/save` | Manager or admin |
| `POST` | `/api/security/fail2ban/ban` | Admin |
| `GET` | `/api/security/fail2ban/jails/{jail}` | Admin |
| `GET` | `/api/security/fail2ban/status` | Admin |
| `POST` | `/api/security/fail2ban/unban` | Admin |
| `GET` | `/api/security/ip-blacklist` | Admin |
| `POST` | `/api/security/ip-blacklist` | Admin |
| `DELETE` | `/api/security/ip-blacklist/{ip}` | Admin |
| `GET` | `/api/security/ip-whitelist` | Admin |
| `POST` | `/api/security/ip-whitelist` | Admin |
| `DELETE` | `/api/security/ip-whitelist/{ip}` | Admin |
| `GET` | `/api/security/score` | Authenticated |
| `GET` | `/api/settings` | Authenticated |
| `PUT` | `/api/settings` | Manager or admin |
| `GET` | `/api/settings/portable` | Admin |
| `POST` | `/api/settings/portable/import` | Admin |
| `POST` | `/api/settings/portable/preview` | Admin |
| `DELETE` | `/api/settings/{key}` | Admin |
| `GET` | `/api/settings/{key}` | Authenticated |
| `GET` | `/api/ssl/certificates` | Authenticated |
| `GET` | `/api/ssl/certificates/{domain}` | Authenticated |
| `POST` | `/api/ssl/issue` | Admin |
| `POST` | `/api/ssl/renew/{domain}` | Manager or admin |
| `GET` | `/api/ssl/status` | Authenticated |
| `GET` | `/api/status/{slug}` | Public |
| `POST` | `/api/system/actions/memory-optimize` | Admin |
| `POST` | `/api/system/actions/process` | Admin |
| `POST` | `/api/system/actions/reboot` | Admin |
| `POST` | `/api/system/actions/reboot-cancel` | Admin |
| `GET` | `/api/system/actions/reboot-status` | Admin |
| `POST` | `/api/system/actions/service` | Admin |
| `GET` | `/api/system/actions/status` | Admin |
| `POST` | `/api/system/actions/swap-reset` | Admin |
| `POST` | `/api/system/actions/temp-clean` | Admin |
| `GET` | `/api/system/info` | Authenticated |
| `GET` | `/api/system/services` | Authenticated |
| `GET` | `/api/system/services/{service}/logs` | Authenticated |
| `GET` | `/api/system/stats` | Authenticated |
| `GET` | `/api/system/update` | Authenticated |
| `POST` | `/api/system/update/install` | Admin |
| `GET` | `/api/system/update/stage` | Admin |
| `POST` | `/api/system/update/stage` | Admin |
| `GET` | `/api/terminal/ws` | Admin |
| `GET` | `/api/uptime/incidents` | Authenticated |
| `GET` | `/api/uptime/monitor-by-domain/{domain}` | Authenticated |
| `GET` | `/api/uptime/monitors` | Authenticated |
| `POST` | `/api/uptime/monitors` | Manager or admin |
| `POST` | `/api/uptime/monitors/bulk-from-domains` | Admin |
| `GET` | `/api/uptime/monitors/summary` | Authenticated |
| `POST` | `/api/uptime/monitors/test` | Manager or admin |
| `DELETE` | `/api/uptime/monitors/{id}` | Admin |
| `GET` | `/api/uptime/monitors/{id}` | Authenticated |
| `PUT` | `/api/uptime/monitors/{id}` | Manager or admin |
| `POST` | `/api/uptime/monitors/{id}/check-now` | Manager or admin |
| `GET` | `/api/uptime/monitors/{id}/heartbeats` | Authenticated |
| `GET` | `/api/uptime/monitors/{id}/incidents` | Authenticated |
| `POST` | `/api/uptime/monitors/{id}/pause` | Manager or admin |
| `POST` | `/api/uptime/monitors/{id}/resume` | Manager or admin |
| `GET` | `/api/uptime/monitors/{id}/uptime` | Authenticated |
| `GET` | `/api/uptime/settings` | Authenticated |
| `PUT` | `/api/uptime/settings` | Admin |
| `GET` | `/api/uptime/status-pages` | Authenticated |
| `POST` | `/api/uptime/status-pages` | Manager or admin |
| `DELETE` | `/api/uptime/status-pages/{id}` | Admin |
| `PUT` | `/api/uptime/status-pages/{id}` | Manager or admin |
| `GET` | `/api/users` | Admin |
| `POST` | `/api/users` | Admin |
| `DELETE` | `/api/users/{id}` | Admin |
| `PUT` | `/api/users/{id}` | Admin |
| `POST` | `/autodiscover/autodiscover.xml` | Public |
| `GET` | `/mail/autoconfig/mail/config-v1.1.xml` | Public |
| `GET` | `/status/{slug}` | Public |
