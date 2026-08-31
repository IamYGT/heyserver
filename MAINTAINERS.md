# Heyserver Maintainers and Areas

This is a responsibility map, not a personal-name roster. It intentionally
assigns work to project roles without inventing identities. Current maintainer
and reviewer membership is determined by the repository's GitHub permissions.
When a role is not separately assigned, the lead maintainer owns the gap until
coverage is recorded.

## Role and area map

| Area | Accountable role | Scope and review boundary | Escalation or handoff |
| --- | --- | --- | --- |
| Project administration and governance | Lead maintainer | Repository permissions, governance changes, Code of Conduct enforcement and appeals, unresolved material decisions | An uninvolved maintainer handles a conflict or recusal |
| Release and compatibility | Release maintainer | Version contract, release artifacts, checksums, public CI gates, changelog, upgrade and rollback notes | Lead maintainer authorizes the official release |
| Core panel, API, and storage | Core maintainer | Go panel services, HTTP/API routes, SQLite schema and migrations, audit and compatibility behavior | Include agent or integration maintainer when a boundary crosses out of the panel |
| Web UI and CLI | Client maintainer | `web/`, embedded assets, `hserverctl`, user-facing workflows, API client contracts | Include core maintainer for route or payload changes |
| Agent and hub protocol | Agent maintainer | `hserver-agent`, hub/agent authentication, capabilities, task schemas, remote trust boundaries | Security response maintainer handles private security implications |
| Host and provider integrations | Integration maintainer | Nginx, PHP-FPM, systemd, firewall, DNS, mail, backup, PM2, Cloudflare, Docker evaluation, and provider adapters | Follow `docs/extension-boundary.md`; absent providers must remain honest `not configured` or `unavailable` |
| Documentation and contributor workflow | Documentation maintainer | Public docs, installation/troubleshooting guidance, issue forms, pull-request workflow, and community references | Governance or security maintainer owns policy decisions |
| Private security response | Security response maintainer | Private vulnerability reports, credential exposure, security fixes, disclosure coordination, and incident handoff | Use [SECURITY.md](SECURITY.md); do not move sensitive details to a public issue |

The same change can belong to several rows. Contributors should identify every
affected area in the pull request and request review from the corresponding role
when that role is available. Area accountability does not make an area closed to
community contributions, and a reviewer may recommend changes without gaining
merge, release, or repository administration authority.

## Routing and escalation

- **Defect, feature, support, or integration proposal:** use the direct forms in
  [SUPPORT.md](SUPPORT.md).
- **Conduct concern:** use the private route in the [Code of Conduct](CODE_OF_CONDUCT.md),
  never a public issue.
- **Vulnerability or suspected credential exposure:** use the [private security
  advisory](https://github.com/IamYGT/heyserver/security/advisories/new)
  and follow [SECURITY.md](SECURITY.md).
- **Compromised installation or outage:** recover the installation and rotate
  affected credentials first, then use the private security advisory if
  Heyserver caused or widened the impact.

Private reports are handled on a least-privilege basis. A maintainer who is
personally involved in a report or has a material conflict must recuse and hand
the matter to an uninvolved maintainer. See [GOVERNANCE.md](GOVERNANCE.md) for
appointment, removal, decision, and release authority.

## Keeping the map current

A governance pull request must update this map when an area or escalation
boundary changes. It should state whether coverage is added, removed, or
transferred, without adding personal information that is not needed for the
public process. Repository permission changes remain the authoritative record
of who currently holds a role.
