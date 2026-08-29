#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workflow="$repo_root/.github/workflows/ci.yml"

python3 - "$workflow" <<'PY'
import pathlib
import re
import sys

workflow = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")


def job_block(name: str) -> str:
    match = re.search(
        rf"(?ms)^  {re.escape(name)}:\n(.*?)(?=^  [a-zA-Z0-9_-]+:\n|\Z)",
        workflow,
    )
    if not match:
        raise SystemExit(f"CI job is missing: {name}")
    return match.group(1)


def named_step(job: str, name: str) -> str:
    match = re.search(
        rf"(?ms)^      - name: {re.escape(name)}\n(.*?)(?=^      - name:|\Z)",
        job,
    )
    if not match:
        raise SystemExit(f"CI step is missing: {name}")
    return match.group(1)


native = job_block("native-acceptance")
for required in (
    "name: Native Lifecycle (${{ matrix.arch }})",
    "if: startsWith(github.ref, 'refs/tags/v')",
    "runner: ubuntu-24.04",
    "runner: ubuntu-24.04-arm",
    "debian_platform: linux/amd64",
    "debian_platform: linux/arm64",
    "debian_arch: amd64",
    "debian_arch: arm64",
    "runs-on: ${{ matrix.runner }}",
    "./scripts/test-native-release-lifecycle.sh",
):
    if required not in native:
        raise SystemExit(f"native clean-install matrix is missing: {required}")

debian = named_step(native, "Exercise Debian 12 native OS and prerequisite gate")
if re.search(r"(?m)^\s+if:", debian):
    raise SystemExit("Debian 12 gate is conditional and may skip an architecture")
for required in (
    'docker run --rm \\\n',
    '--platform "${{ matrix.debian_platform }}"',
    "debian:12",
    'expected_arch="${{ matrix.debian_arch }}"',
    'actual_arch=$(dpkg --print-architecture)',
    'test "$actual_arch" = "$expected_arch"',
    "bash ca-certificates curl openssl tar coreutils sqlite3 systemd",
    "HSERVER_NATIVE_OS_DETECTION_REAL=1 ./scripts/test-native-os-detection.sh",
):
    if required not in debian:
        raise SystemExit(f"Debian 12 architecture gate is missing: {required}")

print("Debian 12 release gate contract: OK")
PY
