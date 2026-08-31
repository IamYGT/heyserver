# Heyserver product contract

These rules apply to the entire repository in addition to the operator contract.

## Product direction

Heyserver is a provider-neutral, self-hosted server-management product. It must be
possible for a new contributor to build it from source and for an operator to
install it on infrastructure that has no relationship to YGT Labs.

- Never add operator domains, email addresses, IPs, usernames, paths, tokens, or
  infrastructure assumptions as runtime defaults. Use `example.com`, detected
  host state, empty optional configuration, or an explicit setting.
- Optional integrations must report `not configured`, `unavailable`, and
  `healthy` as different states. Never claim a service is active from the
  presence of a URL alone.
- Full local management runs natively because it needs controlled access to
  systemd, nginx, firewalls, runtimes, storage, and terminals. Remote servers
  are managed through the least-privileged Heyserver agent contract.
- The central panel is the source of desired actions; each agent is the source
  of observed node state. Do not fake remote actions with local host commands.
- Keep secrets in environment or dedicated secret files. Persist only secret
  references or encrypted values, and never include installation-specific
  secrets in Git, examples, fixtures, logs, or UI bundles.
- New installation paths must be reproducible, documented, versioned, and have
  an upgrade and rollback boundary. Existing databases are upgraded in place;
  never require a reset for a normal release.
- Provider-specific capabilities belong behind an explicit provider boundary.
  The core UI and API must degrade usefully when that provider is absent.
- User-facing behavior changes include the nearest focused test. Distribution
  changes include a build or configuration check that proves the affected path.

## Contribution boundary

Keep commits focused and use English commit messages. Update public docs when a
configuration key, install step, API contract, or contributor workflow changes.
Do not commit production inventory or operator-only runbooks to the public
distribution surface.
