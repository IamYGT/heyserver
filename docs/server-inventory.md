# Server Inventory Template

Production inventory is operator state, not public source code. Keep the real
inventory in a protected configuration repository or secret-management system
and use this file only as a template.

## Node

- **Node ID:** `edge-eu-1`
- **Display name:** `Edge EU 1`
- **Provider:** optional operator metadata
- **Operating system:** detected by the Heyserver agent
- **Agent version:** reported by heartbeat
- **Last verified:** ISO-8601 timestamp

## Capabilities

Record only intentionally enabled agent capabilities, for example:

- `host.read`, `host.action`
- `service.read`, `service.action`
- `terminal`
- `files.read`, `files.write`
- `deploy.read`, `deploy.action`
- `deploy.domain.read`, `deploy.domain.action`

The panel remains the source of desired actions. The agent remains the source of
observed state. Do not put enrollment tokens, API keys, private addresses, SSH
keys, customer domains, or executable deploy arguments in this document.

## Applications and services

| Workload | Kind | Owner | Recovery reference |
|---|---|---|---|
| `customer-portal` | application | team name | protected runbook ID |

## Recovery boundary

Record the protected locations for backups, provider consoles, and operator
runbooks without copying their secret contents into Git.
