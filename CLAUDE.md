# Heyserver contributor context

Heyserver is a provider-neutral, self-hosted server-management panel built with a
Go backend and an embedded React frontend. `AGENTS.md` is the authoritative
repository contract for automated contributors.

## Repository layout

```text
cmd/hserver/          panel entry point and embedded production frontend
cmd/hserver-agent/    least-privileged remote node agent
internal/api/         HTTP handlers, middleware, and route manifest
internal/agenthub/    central desired-action and observed-state contract
internal/services/    local system-management integrations
internal/store/       SQLite repositories and migrations
web/                  React, TypeScript, and Vite frontend source
scripts/              reproducible install, upgrade, release, and checks
deploy/               distribution service and configuration examples
docs/                 public architecture, API, install, and operations docs
```

## Development commands

```bash
make test-go
make test-frontend
make test-shell
make sync-dist
make build
```

Use the nearest focused check for a bounded change. `make sync-dist` copies the
Vite output to `cmd/hserver/web/dist` for Go embedding.

## Configuration and secrets

The standard installation reads `/etc/hserver/hserver.env` and stores runtime
data below `/var/lib/hserver`. Both locations can be overridden by documented
installation settings. Copy `.env.example`; never commit a generated environment
file, token, password, production hostname, IP address, account, database, or
provider inventory.

Optional integrations must remain disabled until explicitly configured and must
report `not configured`, `unavailable`, and `healthy` as distinct states.

## Product boundaries

- Local management uses controlled native host integrations.
- Remote management uses fixed Heyserver agent task schemas; do not fall back to
  SSH or execute arbitrary shell payloads.
- The panel owns desired actions; each agent owns observed node state.
- Provider-specific integrations stay behind explicit boundaries and the core
  UI must remain useful without them.
- Installation, upgrade, rollback, and in-place database migration paths must
  remain reproducible.
