#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
makefile="$repo_root/Makefile"
contributing="$repo_root/CONTRIBUTING.md"

python3 - "$makefile" "$contributing" <<'PY'
import pathlib
import sys

makefile = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
contributing = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")


def require(source: str, needle: str, message: str) -> None:
    if needle not in source:
        raise SystemExit(f"{message}: missing {needle!r}")


require(
    makefile,
    "ci-fast: lint test-all test-coverage-check build",
    "fast contributor baseline",
)
require(makefile, "ci: ci-fast", "legacy ci alias")
require(
    makefile,
    "ci-pr: ci-fast test-security test-database-restore test-docker test-public-source",
    "full contributor gate",
)
require(makefile, "ci-full: ci-pr", "full contributor gate alias")

require(makefile, "test-security:", "vulnerability target")
require(makefile, "govulncheck ./...", "vulnerability target")
require(makefile, "test-database-restore:", "database restore target")
for script in (
    "./scripts/test-postgresql-restore-drill.sh",
    "./scripts/test-mariadb-restore-drill.sh",
):
    require(makefile, script, "database restore target")

require(
    makefile,
    "./scripts/test-contributor-ci-contract.sh",
    "contract test wiring",
)
require(
    contributing,
    "make ci-fast",
    "fast contributor documentation",
)
require(
    contributing,
    "make ci-pr",
    "full contributor documentation",
)
require(
    contributing,
    "make ci-full",
    "full contributor alias documentation",
)
for phrase in (
    "govulncheck",
    "PostgreSQL",
    "MariaDB",
    "Docker",
    "clean committed worktree",
    "native Linux",
    "amd64",
    "arm64",
    "not a promise that every GitHub Actions job will pass",
):
    require(contributing, phrase, "contributor gate boundary documentation")

stale_claim = "Run the same checks as GitHub Actions CI"
if stale_claim in makefile:
    raise SystemExit(f"Makefile still makes the stale parity claim: {stale_claim!r}")

print("contributor CI command contract: OK")
PY
