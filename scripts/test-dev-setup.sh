#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
tmp=$(mktemp -d)
trap 'find "$tmp" -xdev -depth -delete' EXIT INT TERM

required_go=$(awk '$1 == "go" { print $2; exit }' "$repo_root/go.mod")
if [[ -z "$required_go" ]]; then
  echo "could not determine the minimum Go version from go.mod" >&2
  exit 1
fi
for documentation in README.md CONTRIBUTING.md; do
  grep -Fq "Go $required_go or newer" "$repo_root/$documentation"
  grep -Fq 'go.mod' "$repo_root/$documentation"
  grep -Fq 'Python 3' "$repo_root/$documentation"
done

"$repo_root/scripts/dev-setup.sh" check >"$tmp/real-check.log"
grep -Fq 'Result: ready for dependency setup' "$tmp/real-check.log"
grep -Fq 'Docker Compose v2' "$tmp/real-check.log"
grep -Fq 'PostgreSQL client/server tools' "$tmp/real-check.log"
grep -Fq 'MariaDB client/server tools' "$tmp/real-check.log"

IFS=. read -r go_major go_minor go_patch <<<"$required_go"
go_patch=${go_patch:-0}
if (( go_patch > 0 )); then
  old_go_version="$go_major.$go_minor.$((go_patch - 1))"
elif (( go_minor > 0 )); then
  old_go_version="$go_major.$((go_minor - 1)).99"
else
  old_go_version="$((go_major - 1)).99.99"
fi
old_bin="$tmp/old-bin"
mkdir -p "$old_bin"
cat >"$old_bin/go" <<EOF
#!/bin/sh
echo 'go version go${old_go_version} linux/amd64'
EOF
chmod 0755 "$old_bin/go"
if PATH="$old_bin:$PATH" "$repo_root/scripts/dev-setup.sh" check >"$tmp/old.log" 2>&1; then
  echo "development setup accepted an old Go toolchain" >&2
  exit 1
fi
grep -Fq "Go $old_go_version is too old" "$tmp/old.log"
grep -Fq 'Result: not ready (1 required toolchain issue(s))' "$tmp/old.log"

fake_bin="$tmp/fake-bin"
mkdir -p "$fake_bin"
cat >"$fake_bin/go" <<EOF
#!/bin/sh
if [ "\${1:-}" = version ]; then
  echo 'go version go${required_go} linux/amd64'
  exit 0
fi
printf 'go %s\n' "\$*" >>"\$HSERVER_DEV_SETUP_TEST_LOG"
EOF
cat >"$fake_bin/node" <<'EOF'
#!/bin/sh
echo 'v24.0.0'
EOF
cat >"$fake_bin/npm" <<'EOF'
#!/bin/sh
if [ "${1:-}" = --version ]; then
  echo '11.0.0'
  exit 0
fi
printf 'npm %s\n' "$*" >>"$HSERVER_DEV_SETUP_TEST_LOG"
EOF
chmod 0755 "$fake_bin/go" "$fake_bin/node" "$fake_bin/npm"

no_python_bin="$tmp/no-python-bin"
bash_path=$(command -v bash)
mkdir -p "$no_python_bin"
for command in awk dirname git make cc; do
  ln -s "$(command -v "$command")" "$no_python_bin/$command"
done
ln -s "$fake_bin/go" "$no_python_bin/go"
ln -s "$fake_bin/node" "$no_python_bin/node"
ln -s "$fake_bin/npm" "$no_python_bin/npm"
for mode in check setup; do
  if PATH="$no_python_bin" "$bash_path" "$repo_root/scripts/dev-setup.sh" "$mode" >"$tmp/no-python-$mode.log" 2>&1; then
    echo "development setup accepted a missing python3 toolchain in $mode mode" >&2
    exit 1
  fi
  grep -Fq '[required] python3 is unavailable' "$tmp/no-python-$mode.log"
  grep -Fq 'Result: not ready (1 required toolchain issue(s))' "$tmp/no-python-$mode.log"
done

export HSERVER_DEV_SETUP_TEST_LOG="$tmp/setup-invocations.log"
PATH="$fake_bin:$PATH" "$repo_root/scripts/dev-setup.sh" setup >"$tmp/setup.log"
grep -Fxq 'go mod download' "$HSERVER_DEV_SETUP_TEST_LOG"
grep -Fxq 'npm ci' "$HSERVER_DEV_SETUP_TEST_LOG"
grep -Fq 'Result: contributor dependencies are ready' "$tmp/setup.log"
grep -Fq 'make backend' "$tmp/setup.log"
grep -Fq 'make dev-frontend' "$tmp/setup.log"

if "$repo_root/scripts/dev-setup.sh" unknown >"$tmp/usage.log" 2>&1; then
  echo "development setup accepted an unknown mode" >&2
  exit 1
fi
grep -Fq 'Usage: dev-setup.sh check|setup' "$tmp/usage.log"

echo "development setup contract test: OK"
