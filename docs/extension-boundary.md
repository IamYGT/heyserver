# HServer Extension Boundary v1

HServer v1 accepts provider integrations and new server-management capabilities
as reviewed, in-tree source contributions. They are compiled into the panel,
agent, CLI, and web bundle and travel through the normal versioned release
lifecycle. HServer does **not** load arbitrary Go plugins, executable hooks,
shell fragments, remote UI bundles, or third-party binaries at runtime.

This boundary lets community contributors extend a self-hosted installation
without creating a second control plane or weakening local and managed-node
authority. It is a contribution contract, not a stable ABI for importing
packages under `internal/` from another repository.

## Machine-readable catalog

[`extensions/catalog.json`](../extensions/catalog.json) is the authoritative
machine-readable companion to this boundary and the
[optional integrations matrix](optional-integrations.md). It records each
integration's stable identity, supported classes and targets, configuration and
status contract, agent capabilities, and focused evidence. The companion
[`extensions/catalog.schema.json`](../extensions/catalog.schema.json) defines
the catalog shape and is part of every release archive.

When a new integration is proposed or an existing one changes, update its
catalog entry and matching optional-integrations row together, then add or
update the focused integration test. The catalog verifier is a required public
and release check; documentation or source support is incomplete until that
entry and test pass.

## Supported extension classes

| Class | Examples | Required owner |
| --- | --- | --- |
| Local capability | A bounded service inventory or lifecycle action on the panel host | Native service package plus authenticated API handlers |
| Managed-node capability | A bounded read or mutation performed by an enrolled server | Shared task schema, hub capability gate, agent implementation, and installation-owned enablement |
| Provider adapter | A backup, DNS, notification, or deployment provider | Provider-specific package behind the generic domain service |
| Client surface | A web page, `hserverctl` command, or TUI section for an existing API contract | Existing API authorization and failure-state boundary |

An extension may cover more than one class, but each class must remain usable or
honestly unavailable independently. A provider adapter cannot be required for
login, local monitoring, audit, backup recovery, or the native release
lifecycle unless it is explicitly promoted into the core product contract.

## v1 non-goals

- No runtime marketplace or installation of code from the browser.
- No Go `plugin` ABI, shared-object loading, `eval`, or arbitrary executable
  path supplied through an API request.
- No provider credential stored in a frontend bundle, audit detail, task
  payload, Git fixture, or database setting intended for ordinary preferences.
- No remote action implemented by running a local hub command against the
  selected node's name.
- No hidden SSH password or unrestricted remote command disguised as an
  extension.

A future out-of-tree runtime SDK requires a separate public proposal covering
artifact signing, compatibility, isolation, permission declaration, upgrade,
rollback, and removal. Until then, source forks can add private in-tree
extensions under the same contract, and upstream contributions remain
reviewable and reproducible from one repository.

## Required proposal

Start with the **Integration proposal** issue form. The proposal must name:

1. the operator problem and supported extension class;
2. local host, managed node, or both as the target;
3. the dependency or provider and its behavior when absent;
4. every configuration key and secret-file reference;
5. the `not_configured`, `unavailable`, and `healthy` observations;
6. every read and mutation, including fixed inputs and maximum sizes/counts;
7. any managed-agent capability and its installation-owned enablement;
8. timeout, retry, audit, failure, recovery, and rollback behavior;
9. the focused acceptance path; and
10. installation, upgrade, removal, and portability effects.

Maintainer discussion may narrow an extension before code is accepted. Approval
of a proposal does not bypass normal review or release gates.

## Configuration and secret boundary

Non-secret configuration belongs in the typed config package, an
installation-owned settings file, or an explicitly allowlisted database
setting. Add a provider-neutral example to `.env.example` or the relevant
`deploy/*.example` file and document the default. Empty optional configuration
means `not_configured`; it must not silently select a maintainer service.

Credential values belong in the protected environment or a dedicated file.
When a file reference is used:

- the path is configured locally, not accepted from an ordinary mutation body;
- status reports only configured/readable/protected state, never the path or
  contents;
- examples contain no usable credential;
- API, audit, logs, CLI output, and frontend state cannot reproduce the secret;
- upgrade preserves the reference, rollback restores the previous compatible
  configuration, and uninstall does not delete provider-side data.

Portable configuration exports remain positive-allowlist only. A new setting
is excluded unless its portability and non-secret nature are explicitly added
to `docs/portable-configuration.md` and its schema tests.

## Status and remediation contract

Every optional extension exposes these top-level meanings, even when it has
more detailed internal states:

| State | Meaning | Mutations |
| --- | --- | --- |
| `not_configured` | The required installation-owned configuration is absent | Disabled |
| `unavailable` | Configuration exists, but dependency, credentials, network, or observation failed | Disabled unless a separately observed safe subset remains available |
| `healthy` | A fresh read-only observation proved the dependency or provider is usable | Enabled only when authorization and all action-specific preconditions also pass |

An installed binary or configured URL alone is not a health proof. A retry
performs observation only; it cannot install packages, start services, open
OAuth, or rewrite protected configuration. The web and TUI surfaces preserve
loaded independent data when one optional request fails and distinguish
loading, empty, not configured, unavailable, permission denied, and operation
failed.

Remediation names the exact operator-owned next step without printing a secret
or claiming an automatic repair. Add the provider-specific recovery row to
`docs/optional-integrations.md` and the applicable installation/troubleshooting
section.

## Local capability implementation

A local capability normally follows this path:

1. Put provider or host logic in its own package under `internal/services/`.
   Accept dependencies through constructors or small interfaces so focused
   tests do not mutate the developer host.
2. Resolve executable names, allowed roots, units, and timeouts from typed or
   installation-owned configuration. Never accept a command line from the API.
3. Return structured observations and retain the original bounded failure
   detail for logs/remediation without returning secrets.
4. Register authenticated routes in `internal/api/router.go`; use the narrowest
   existing role and mutation limiter.
5. Regenerate `internal/api/routes_manifest.go`, `docs/api-routes.md`, and
   `docs/openapi.json`. Promote request schemas from the implemented Go type and
   use strict JSON decoding for promoted bodies.
6. Add the web and/or CLI client only after absence and unavailable states are
   represented. A disabled button is not a substitute for explaining why.
7. Audit successful and failed mutations with identifiers and outcomes, not
   credential values or complete configuration bodies.

For the local integration aggregate, register a code-owned
`integrationstatus.Probe` whose ID exactly matches the reviewed catalog entry.
The runtime accepts additive catalog IDs and emits a deterministic ID-derived
probe name; catalog display text, purpose, paths, and configuration metadata
are never executable inputs or wire probe names. A catalog entry without a
registered probe remains explicitly `unprobed` rather than being reported as
healthy. The production router keeps the fifteen core definitions as defaults;
an in-tree contributor passes additional definitions through the explicit
`api.Deps.IntegrationStatusProbes` field when constructing the router. This is
compile-time Go wiring, not runtime plugin discovery or catalog-driven function
lookup. The authenticated CLI validates returned result and `unprobed` IDs
against the server catalog, so an additive result is accepted only when its
catalog entry is present.

The catalog verifier also enforces this source-to-production boundary. An entry
claiming `local_capability` or `provider_adapter` must have a non-nil probe in
the canonical production constructor (`internal/api/handlers_integrations.go`);
catalog rows, documentation, and tests are evidence of the contract but never
count as runtime registration. A `client_surface`-only entry may remain
metadata-only and `unprobed` until a local/provider implementation is added.
Run both focused checks when changing this boundary:

```bash
./scripts/test-extension-catalog.py
./scripts/test-extension-catalog-registration.py
```

A mutation must use a fixed action vocabulary, validate current observed
identity when applicable, have a deadline, and describe rollback or the absence
of rollback before confirmation.

## Managed-node capability implementation

A managed capability is an end-to-end protocol addition, not just a new
button. Its canonical path is:

1. Define portable task and capability names in `internal/agenthub/types.go`.
   Prefer separate read and mutation capabilities such as `feature.read` and
   `feature.action`.
2. Map every task kind to its required capability in
   `internal/agenthub/service.go`. The hub must reject missing capability before
   a task is queued.
3. Add explicit agent installation configuration and advertise the capability
   only when that local policy is enabled. A panel setting cannot grant the
   agent new authority.
4. Validate the fixed task payload both at the hub handler and again in the
   agent. Paths, identities, action names, counts, bytes, and durations are
   bounded; arbitrary commands and caller-selected executables are rejected.
5. Implement the agent operation with a deadline, bounded output, and a
   structured terminal receipt. Update the shared protocol version only when
   compatibility rules require it.
6. Keep the central panel as the source of desired work and the agent as the
   source of observed node state. Offline work is rejected rather than queued
   as if it were live.
7. Prove allowed execution, missing-capability rejection with unchanged task
   history, invalid payload rejection, timeout/failure receipt, and the
   enabled/disabled advertisement paths.

The web and CLI must read the selected node's observed online/capability state.
They cannot fall back to the local host implementation when a remote capability
is absent.

## Provider adapter implementation

Provider-specific transport and credentials stay inside one package or adapter.
The generic service consumes a narrow interface and owns the provider-neutral
policy. Adding a destination must not add provider names to unrelated database,
domain, monitoring, or deployment code.

At minimum, a provider adapter defines:

- configuration validation without a network mutation;
- a real read-only health probe;
- typed error mapping to `not_configured` or `unavailable`;
- request deadlines and bounded response bodies;
- retry behavior that cannot duplicate a mutation silently;
- provider capability differences, such as an unsupported purge operation;
- fixture-only tests with placeholder endpoints and no live account; and
- operator instructions for credentials, connectivity, and removal.

The S3-compatible snapshot destination is a reference: provider configuration
and credential-file validation live under `internal/services/snapshot/`, the
generic snapshot policy selects a destination, status distinguishes provider
health, and repository purge is advertised only by destinations that implement
that capability.

## Client and API compatibility

The generated OpenAPI document and route manifest are public HTTP contracts.
Backward-compatible response fields may be added; removing or changing fields,
roles, task meanings, or stored formats requires a versioned migration and
release note. Unknown request fields should be rejected for promoted mutation
schemas rather than ignored.

Packages under `internal/` are not a public Go SDK. External automation should
use the authenticated HTTP API or `hserverctl`. The panel, agent, and CLI in one
release archive share a version identity; managed agents additionally expose
protocol compatibility so an unsupported combination is visible before an
action.

## Required acceptance evidence

| Changed boundary | Minimum focused evidence |
| --- | --- |
| Local read-only provider | Missing/unconfigured, healthy, and failed-probe service tests |
| Local mutation | Validation, authorization, success receipt, failure audit, and rollback/no-rollback disclosure |
| Managed read | Enabled/disabled capability advertisement, task execution, and offline/missing-capability rejection |
| Managed mutation | All managed-read evidence plus invalid payload, failure receipt, and unchanged task history on preflight rejection |
| API request | Handler test, generated route/OpenAPI check, and strict payload contract when promoted |
| Web behavior | Loading, empty, not configured, unavailable, denied, failed, and one healthy target flow as applicable |
| CLI/TUI behavior | Invalid input rejected before network access and one target-scoped success flow |
| Configuration or package | Upgrade/rollback preservation and release-package inventory check |

Run the nearest focused checks while developing. Before a distribution change,
run the Git-free source gate from a clean commit:

```bash
make gen-routes
make gen-api-docs
make sync-dist
make verify-api-docs
./scripts/test-extension-catalog.py
make test-public-source ARCH="$(go env GOARCH)"
```

Do not weaken a verifier or replace a real failure state with a successful empty
response to make an extension pass.

## Scaffold and check workflow

Use the repository scaffold to start a reviewable in-tree extension packet
without selecting a provider or generating credentials:

```bash
./scripts/new-extension.sh create example.health \
  --display-name "Example health" \
  --purpose "Observe one bounded service" \
  --class provider_adapter \
  --target local_host
./scripts/new-extension.sh check extensions/example.health
```

The command refuses to overwrite an existing directory and creates exactly
`README.md`, `catalog-entry.json`, and `integration_test.go` below
`extensions/<id>/`. The standalone catalog candidate uses the
`https://example.com/health` placeholder, empty optional configuration arrays,
and the canonical `not_configured`, `unavailable`, and `healthy` states. It is
not merged into `extensions/catalog.json` or wired into production
automatically. For a managed-node target, replace the explicit
`TaskExtensionRead`/`CapabilityExtensionRead` placeholders with task and
capability constants that are actually declared and installation-owned.

Run `check` on the untouched packet before replacing its skipped test and
placeholders. It fails closed on malformed metadata, non-empty scaffold
configuration, secret-like material, credential or binary artifacts, runtime
loading, `eval`, and arbitrary command execution; it never executes generated
files or contacts a provider. After implementation, merge the candidate only
with the matching documentation row, code-owned production registration,
generated API contract where applicable, and focused evidence described above.

## Pull request completion

An extension pull request links its accepted proposal and states:

- operator-visible outcome and extension class;
- local/managed/provider/client files changed;
- configuration and secret storage;
- new routes, capabilities, tasks, and migrations;
- absence, failure, confirmation, audit, and rollback behavior;
- exact focused evidence and any architecture/provider boundary not verified;
- installation, upgrade, rollback, removal, and public documentation effects.

The extension is available only when source, route, applicable client surface,
focused verification, and operator documentation land together. Source support
does not imply that every Linux distribution or third-party provider has been
certified.
