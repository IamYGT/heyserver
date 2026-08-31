## Outcome

Describe the operator-visible result and why it belongs in Heyserver.

## Scope

- [ ] Local native host
- [ ] Managed-node agent
- [ ] Hub/API
- [ ] Web interface
- [ ] Installer/release
- [ ] Documentation only

## Verification

List the smallest focused command or flow that proves the changed claim.

```text
command and result
```

## Public-project checks

- [ ] No credentials, production inventory, private hostnames, or generated local artifacts were added.
- [ ] Remote management uses an explicit least-privileged agent capability with fixed inputs.
- [ ] Installation, upgrade, rollback, and documentation were updated when their behavior changed.
- [ ] Generated web assets were refreshed when the embedded interface changed.
- [ ] New integrations follow `docs/extension-boundary.md`, link an accepted proposal, and distinguish `not_configured`, `unavailable`, and `healthy` where optional.
- [ ] New or changed integrations update the authoritative `extensions/catalog.json` entry, the matching `docs/optional-integrations.md` row marker, and the focused integration test.
- [ ] `./scripts/test-extension-catalog.py` passes for catalog changes.
- [ ] Entries claiming `local_capability` or `provider_adapter` are wired to a non-nil code-owned production probe; docs/tests references alone do not satisfy registration.
- [ ] `./scripts/test-extension-catalog-registration.py` passes for registration-boundary changes.
