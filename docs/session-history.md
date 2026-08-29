# HServer development history

This public history records product milestones without installation inventories,
accounts, credentials, production domains, IP addresses, or operator-only
runbooks. Detailed changes remain available in Git history and `CHANGELOG.md`.

## Foundation

- Go HTTP API, embedded React SPA, SQLite persistence, JWT authentication, and
  admin/manager/viewer roles.
- Native local integrations for Nginx, PHP-FPM, certificates, processes,
  databases, files, terminals, cron, containers, firewalls, backups, mail, DNS,
  notifications, and monitoring.
- Reproducible frontend embedding and a single-binary panel release path.

## Reliability and operations

- Focused service parsers and handler tests were added around system state.
- Native uptime checks, notification dispatch, status pages, audit history, and
  backup job tracking replaced installation-specific operational glue.
- Install, upgrade, rollback, uninstall, and release-package checks became part
  of the repository lifecycle contract.

## Self-hosted distribution

- Runtime defaults were separated from maintainer infrastructure.
- Optional integrations gained explicit configuration and honest health states.
- Local BIND management gained structured installation, configuration, service,
  and action readiness with server-side mutation gates.
- BIND SOA and record writes gained staged validation, atomic replacement, and
  original-file restoration when a requested runtime reload fails.
- BIND zone creation and deletion gained serialized config-and-zone
  transactions with reversible deletion tombstones and global reload rollback.
- BIND zone lifecycle transactions gained a protected durable journal, startup
  crash recovery, recovery-aware readiness, and UI guidance that keeps
  mutations locked until recovery succeeds.
- Remote management moved to the fixed, least-privileged HServer agent task
  protocol instead of SSH command execution.
- Public examples use installation-owned values or reserved example domains and
  addresses; secrets and production inventory are excluded from distribution.

## Current direction

HServer is evolving as a provider-neutral community project that operators can
build, install, inspect, extend, upgrade, and roll back on their own servers.
See `AGENTS.md`, `README.md`, `docs/installation-guide.md`, and
`docs/agent-hub-contract.md` for the current contracts.
