# Security Policy

## Supported versions

HServer is currently pre-1.0. Security fixes are prepared for the latest tagged
release and the `main` branch. Older snapshots and locally modified forks may
need to upgrade before a fix can be applied.

| Version | Security fixes |
| --- | --- |
| Latest tagged release | Supported |
| `main` | Supported for contributors |
| Older releases | Not supported |

## Report a vulnerability privately

Do **not** open a public issue, pull request, or support request for a
vulnerability, suspected credential exposure, or a report containing real
server inventory. Use the repository's [private security advisory form](https://github.com/IamYGT/heyserver/security/advisories/new)
to keep the report confidential. The same link is available from the GitHub
repository's **Security → Report a vulnerability** action.

Include only the minimum evidence needed to reproduce the problem:

- affected HServer and agent versions;
- installation method and operating-system version;
- affected component and required privileges;
- reproduction steps or a minimal proof of concept;
- expected and observed behavior;
- suggested mitigation, when known.

Never attach live tokens, passwords, private keys, database contents, or a full
production environment file. Replace those values before submission while
preserving the relevant structure. Review operational values in any doctor
report before uploading it.

If GitHub does not show the private advisory form, use the private contact route
published on the [repository owner's GitHub profile](https://github.com/IamYGT)
and state that the message is security-sensitive. Do not move the details to a
public issue while waiting for access to be restored.

The security response maintainer limits report access to the people needed to
validate and fix the issue. The project will acknowledge a complete report,
validate its scope, coordinate a fix and release, and credit the reporter
unless anonymity is requested. Exact response times are not promised before the
project reaches a stable release. See the [maintainer area map](MAINTAINERS.md)
for role coverage without relying on a personal-name roster.

## Operational incidents

A compromised host, lost credential, or outage is an installation incident, not
a public vulnerability report:

1. Isolate or recover the affected installation and rotate or revoke the
   affected credential using the installation's normal operator procedure.
2. Preserve only the minimum diagnostic evidence needed to understand impact;
   do not upload tokens, passwords, private keys, databases, or full inventory.
3. If HServer caused or widened the impact, report the root cause through the
   [private security advisory form](https://github.com/IamYGT/heyserver/security/advisories/new).
4. If the concern is about harassment, retaliation, or another project-space
   conduct issue, use the private route in the [Code of Conduct](CODE_OF_CONDUCT.md)
   instead.

Security reports and conduct reports are private escalations. The public
bug/feature/support/integration forms in [SUPPORT.md](SUPPORT.md) are for
non-sensitive project work only.
