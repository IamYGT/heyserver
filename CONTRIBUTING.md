# Contributing to HServer

HServer welcomes focused bug fixes, provider integrations, documentation,
tests, and server-management capabilities. Please keep the project useful for a
fresh self-hosted installation rather than coupling a change to one operator's
infrastructure.

Participation follows the [Code of Conduct](CODE_OF_CONDUCT.md). Project roles,
material decisions, and release authority are defined in
[GOVERNANCE.md](GOVERNANCE.md).

## Set up a development environment

The base toolchains match CI: Go 1.26.1 or newer (the exact minimum from the
root `go.mod` `go` directive), Node.js 24, npm, Git, Make, a C compiler, and
Python 3. `make dev-check` performs a read-only version and availability check,
and reports Docker Compose, PostgreSQL, MariaDB, SQLite, and golangci-lint as
separate optional/full-gate capabilities. `make dev-setup` repeats the required
checks and then installs only the repository's locked Go and frontend
dependencies; it does not install operating-system packages or generate an
environment file.

```bash
git clone https://github.com/IamYGT/heyserver.git
cd heyserver
make dev-check
make dev-setup
make test
make build
go build ./cmd/hserver-agent
```

If a required tool is missing or too old, setup stops before dependency
downloads and prints the exact failing boundary. Optional tools are needed only
for their named evaluation, restore-drill, or lint path; their absence does not
misreport the base build environment as broken.

`make build` produces both `bin/hserver-panel` and `bin/hserverctl` from the
same source revision and embedded build metadata. The agent remains a separate
build target because it is installed on managed nodes rather than the panel host.

To reproduce a checksummed package for the current host architecture, provide
an explicit stable release identity:

```bash
make release VERSION=v1.2.3 ARCH="$(go env GOARCH)"
```

The target runs the repository tests, rebuilds the embedded frontend, builds
the panel, agent, and CLI with the same version, verifies the release inputs,
and writes the archive plus adjacent checksum under `dist/`. Release identities
must be exact `major.minor.patch` values with an optional `v` prefix. Commit
hashes, dirty versions, and prerelease suffixes are rejected. Official
`amd64`/`arm64` cross-compilation remains in the public CI matrix, which owns the
required cross-CGO toolchains.

Before creating the matching tag, add a `## [major.minor.patch]` entry to
`CHANGELOG.md` (omit the optional `v` prefix in the heading). Tagged CI rejects
the package before publication when that release entry is missing; the
`Unreleased` section is not treated as release notes.

Use `./scripts/init-env.sh` for a local Docker environment. The generated `.env`
is ignored by Git and must never be attached to an issue or pull request.

### Focused checks during iteration

Start with the nearest test for the code you changed, then use the repository
gates when the change is ready. For example:

```bash
# One Go package and one test:
go test ./internal/releaseversion -run '^TestCompareStableReleases$' -count=1

# One frontend Vitest file:
npm --prefix web test -- src/lib/chunkErrors.test.ts
```

Choose the package and test file nearest to your change; add or update a
focused regression test when the behavior is new or fixed. These commands are
fast feedback only: `make ci-fast` runs the provider-neutral contributor
baseline, while `make ci-pr` runs the full locally reproducible gate. Run the
appropriate gate before opening a pull request; focused commands do not replace
either gate.

## Before opening a pull request

1. Keep the change focused and use an English commit message.
2. Add the nearest test for changed behavior.
3. Update public documentation for new configuration, API, installation, or
   migration behavior.
4. Use provider-neutral defaults and `example.com` in examples.
5. Describe any database, permission, upgrade, or rollback effect explicitly.
6. Do not include production inventory, logs containing secrets, generated
   `.env` files, databases, backups, or embedded credentials.

### Contributor CI commands

The fast provider-neutral contributor baseline is:

```bash
make ci-fast
```

This runs the repository lint, Go and frontend tests, route and portable
lifecycle checks, coverage threshold, and build. `make ci` remains a
backward-compatible alias for `make ci-fast`. Neither command claims exact
GitHub Actions parity: environment-bound acceptance gates are intentionally
kept in the full command below.

Before opening a pull request, run the full locally reproducible gate:

```bash
make ci-pr
```

`make ci-pr` delegates to the existing canonical targets and scripts. In
addition to `ci-fast`, it runs `govulncheck`, both isolated database restore
drills, the Docker quick evaluation, and the Git-free public-source
acceptance. `make ci-full` is an alias for `make ci-pr`. These full-gate
dependencies are not silently skipped: install them or the command fails at
the missing boundary. The vulnerability scanner can be installed with:

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
```

The PostgreSQL and MariaDB drills need their local server/client tools and
passwordless `sudo -n -u postgres` / `sudo -n -u mysql` access. The Docker
gate needs Docker Engine with Compose v2. The public-source gate needs a
clean committed worktree, native Linux, and the requested native architecture
(`ARCH` defaults to `go env GOARCH`); its export intentionally rejects a dirty
worktree.

Passing `make ci-pr` is not a promise that every GitHub Actions job will pass.
The hosted workflow additionally owns the following boundaries:

- public-source acceptance runs separate native `amd64` and `arm64` Ubuntu
  jobs, while a local run covers only one native architecture;
- release packages use the hosted cross-CGO `amd64`/`arm64` matrix (a local
  `make release VERSION=v1.2.3 ARCH=...` covers one explicitly selected
  architecture); and
- tag-only native lifecycle, managed-agent, network-isolation, release
  provenance, and GitHub release-publication jobs require hosted runners or
  GitHub permissions and are intentionally outside `make ci-pr`.

The command names make this boundary explicit: `ci-fast` is the quick local
feedback loop, while `ci-pr` is the strongest provider-neutral local contract
that can be reproduced without pretending to provide GitHub's runner,
architecture, or release environment.

Before changing distribution, installation, or release behavior, reproduce the
Git-free public-source gate from a clean committed tree:

```bash
make test-public-source ARCH="$(go env GOARCH)"
```

This exports only committed files into a temporary tree, scans it for
installation-specific inventory, builds the frontend, panel, CLI, and agent
without Git metadata, then packages and verifies a release archive. CI invokes
the same target for `amd64`, so the contributor command and release gate cannot
silently drift apart. This acceptance target intentionally requires native
Linux on the requested architecture because the panel uses CGO-backed SQLite;
use the public `Release Package (amd64/arm64)` CI matrix for cross-CGO builds.

The focused clean-history fixture is also available independently:

```bash
./scripts/test-create-public-repository.sh
```

It proves that the maintainer-facing public repository creator rejects dirty
source and existing or private-tree destinations, then produces a clean `main`
repository with exactly one root commit and no configured remote. The creator
never pushes; public destination ownership and branch protection remain an
explicit release-maintainer action.

When Docker Engine with Compose v2 is available, also exercise the public
quick-evaluation path:

```bash
make test-docker
```

This creates an isolated Compose project and generated temporary environment,
builds the documented Dockerfile, verifies health and admin login, persists
onboarding state across a container restart, then removes its project-scoped
container, network, and volume. This is the same `test-docker` target invoked
by `make ci-pr`; GitHub Actions runs it as the required `Docker Quick
Evaluation` release gate on a dedicated Ubuntu runner.

API route changes must update the authoritative router/manifest and regenerate
both the Markdown inventory and OpenAPI 3.1 contract:

```bash
make gen-routes
make gen-api-docs
make sync-dist
make verify-api-docs
```

`docs/openapi.json` guarantees route, path-parameter, and access-level coverage.
Its promoted schemas currently cover local backup and restore workflows,
bounded host maintenance, measured disk cleanup, exact cron and firewall
mutations, bounded PM2 deployment, exact Docker image pulls, and managed-node
enrollment, inventory, server-observed connectivity, compatibility, task
creation, task history, and conflict errors. Do not invent payload fields:
promote them from implemented handlers and exported Go types, add a focused
generator assertion, increment `x-hserver-contract-version`, then run
`make gen-api-docs` and `make sync-dist`.

Direct frontend `api.get`, `api.post`, `api.put`, and `api.delete` calls are
checked against `internal/api/routes_manifest.go` for the registered HTTP method
and route shape. When an operation has a promoted JSON request schema in
`docs/openapi.json`, the same verifier also checks required fields, rejected
unknown fields, primitive/array/object types, enums, constants, and bounded
literal collections that can be proven from TypeScript:

```bash
npm --prefix web run verify:api-routes
npm --prefix web run verify:api-routes -- --show-unverified
npm --prefix web run test:api-routes
```

The verifier proves statically resolvable paths and reports helper-built or
otherwise dynamic calls separately without claiming that they passed. `npm run
lint` runs the verifier, and CI additionally runs its focused regression tests.
Shared route helpers can declare one or more adjacent `@apiRoute` JSDoc patterns;
the verifier expands their call-site arguments and checks every resulting
method/path combination. Keep the helper's focused unit test aligned with those
patterns. When a dynamic call still cannot be resolved, inspect it with
`--show-unverified` and add a focused behavior test rather than weakening or
bypassing the route check.

A promoted request schema with `additionalProperties: false` is an executable
API promise: its Go handler must use strict JSON decoding and its frontend body
type must not advertise ignored fields. Update the handler test, OpenAPI
generator assertion, TypeScript request type, and caller together. The verifier
checks only promoted OpenAPI request bodies; absence of a payload failure does
not imply that an unpromoted operation has a documented body contract.

For a documentation-only change, an exact diff and link check are sufficient.

Database restore changes must also pass the isolated lifecycle drills. Each
script creates its own non-networked temporary server and removes it afterward:

```bash
./scripts/test-postgresql-restore-drill.sh
./scripts/test-mariadb-restore-drill.sh
```

## Architecture expectations

Read [AGENTS.md](AGENTS.md) before implementing a new capability. In particular:

- optional providers must degrade to an honest `not configured` state;
- remote operations go through the HServer agent contract;
- existing databases upgrade in place;
- source defaults cannot contain installation-specific domains or identities;
- host-mutating operations require a bounded action, visible result, and useful
  failure message.

## Add an integration or capability

Read the [Extension Boundary v1](docs/extension-boundary.md) before proposing a
provider adapter, local host capability, managed-node task, or new client
surface. Start with the dedicated **Integration proposal** issue form. HServer
v1 accepts reviewed in-tree extensions compiled into normal release artifacts;
it does not load arbitrary plugins, executable hooks, or remote UI bundles at
runtime.

The extension proposal must define its configuration and secret-file boundary,
honest optional states, fixed operations, managed-agent capabilities when
applicable, failure/audit/rollback behavior, lifecycle effects, and focused
acceptance evidence. The implementation must land source, API contract,
applicable web or CLI behavior, tests, and operator documentation together.

The machine-readable integration catalog is the authoritative companion for
this contribution surface. A new or changed integration must update its
`extensions/catalog.json` entry, the matching row marker in
`docs/optional-integrations.md`, and the focused integration test together. The
catalog schema is `extensions/catalog.schema.json`; run the required verifier
before opening the pull request:

```bash
./scripts/test-extension-catalog.py
```

## License

By submitting a contribution, you agree that it is provided under the
[Apache License 2.0](LICENSE), consistent with section 5 of that license. Do not
submit code you do not have the right to contribute.
